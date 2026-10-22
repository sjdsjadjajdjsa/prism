package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const mcpProtocol = "2025-06-18"

type mcpServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Off     bool              `json:"disabled"`
}

type mcpTool struct {
	server, name, wire, description string
	schema                          map[string]any
}

type mcpServer struct {
	name   string
	config mcpServerConfig
	state  string
	err    string
	tools  []mcpTool
	conn   mcpConn
}

type mcpConn interface {
	call(ctx context.Context, method string, params any) (json.RawMessage, error)
	notify(method string, params any)
	close()
}

type mcpHub struct {
	closed  bool
	mu      sync.Mutex
	servers []*mcpServer
}

func loadMCPConfig(dir string) (map[string]mcpServerConfig, []string) {
	merged := map[string]mcpServerConfig{}
	var problems []string
	for _, path := range []string{filepath.Join(configDir(), "mcp.json"), filepath.Join(dir, ".prism", "mcp.json")} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var file struct {
			Servers map[string]mcpServerConfig `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &file); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		for name, cfg := range file.Servers {
			merged[name] = cfg
		}
	}
	return merged, problems
}

func startMCP(dir string) *mcpHub {
	hub := &mcpHub{}
	configs, problems := loadMCPConfig(dir)
	for _, p := range problems {
		hub.servers = append(hub.servers, &mcpServer{name: "config", state: "error", err: p})
	}
	names := make([]string, 0, len(configs))
	for name := range configs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cfg := configs[name]
		s := &mcpServer{name: name, config: cfg, state: "starting"}
		if cfg.Off {
			s.state = "off"
		}
		hub.servers = append(hub.servers, s)
		if !cfg.Off {
			go hub.connect(s)
		}
	}
	return hub
}

func (h *mcpHub) set(s *mcpServer, fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fn()
}

func (h *mcpHub) connect(s *mcpServer) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	conn, err := dialMCP(s.config)
	if err == nil {
		_, err = conn.call(ctx, "initialize", map[string]any{
			"protocolVersion": mcpProtocol,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "prism", "version": "0.2.0"},
		})
	}
	var tools []mcpTool
	if err == nil {
		conn.notify("notifications/initialized", map[string]any{})
		tools, err = listMCPTools(ctx, conn, s.name)
	}
	if err != nil {
		if conn != nil {
			conn.close()
		}
		h.set(s, func() { s.state, s.err = "error", err.Error() })
		return
	}
	closed := false
	h.set(s, func() {
		closed = h.closed
		if !closed {
			s.state, s.conn, s.tools = "ready", conn, tools
			dedupWireNames(h.servers)
		} else {
			s.state = "error"
			s.err = "hub closed before connect finished"
		}
	})
	if closed {
		conn.close()
	}
}

func listMCPTools(ctx context.Context, conn mcpConn, server string) ([]mcpTool, error) {
	var tools []mcpTool
	cursor := ""
	for page := 0; page < 20; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := conn.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var res struct {
			Tools []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, err
		}
		for _, t := range res.Tools {
			schema := t.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools = append(tools, mcpTool{server: server, name: t.Name, wire: mcpWireName(server, t.Name), description: t.Description, schema: schema})
		}
		if len(tools) >= 128 || res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if len(tools) > 128 {
		tools = tools[:128]
	}
	return tools, nil
}

// dedupWireNames resolves cross-server wire-name collisions in place.
// It must be called under h.mu after all tools have been collected.
// The suffix loop guarantees the renamed wire is itself not already taken.
func dedupWireNames(servers []*mcpServer) {
	used := map[string]bool{}
	for _, s := range servers {
		for _, t := range s.tools {
			used[t.wire] = true
		}
	}
	// Second pass: for any wire that appears more than once, rename the
	// later occurrences with _2, _3, … skipping any name already taken.
	count := map[string]int{}
	for _, s := range servers {
		for i := range s.tools {
			w := s.tools[i].wire
			count[w]++
			if count[w] > 1 {
				base := cutUTF8(w, 60)
				for n := 2; ; n++ {
					candidate := base + fmt.Sprintf("_%d", n)
					if !used[candidate] {
						s.tools[i].wire = candidate
						used[candidate] = true
						break
					}
				}
			}
		}
	}
}

var unsafeTool = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func mcpWireName(server, tool string) string {
	return cutUTF8(unsafeTool.ReplaceAllString(server, "_"), 24) + "__" + cutUTF8(unsafeTool.ReplaceAllString(tool, "_"), 36)
}

// cutUTF8 returns at most n bytes of s, backing off so a multi-byte rune is
// never split.
func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// clipUTF8 is cutUTF8 with surrounding space trimmed and an ellipsis when cut.
func clipUTF8(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return cutUTF8(s, n) + "…"
}

func (h *mcpHub) specs() []any {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []any
	for _, s := range h.servers {
		if s.state != "ready" {
			continue
		}
		for _, t := range s.tools {
			desc := t.description
			if len(desc) > 900 {
				desc = cutUTF8(desc, 900) + "…"
			}
			out = append(out, map[string]any{"type": "function", "function": map[string]any{
				"name":        t.wire,
				"description": "[" + s.name + "] " + desc,
				"parameters":  t.schema,
			}})
		}
	}
	return out
}

func (h *mcpHub) find(wire string) (*mcpServer, mcpTool, bool) {
	if h == nil {
		return nil, mcpTool{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.servers {
		for _, t := range s.tools {
			if t.wire == wire && s.state == "ready" {
				return s, t, true
			}
		}
	}
	return nil, mcpTool{}, false
}

func (h *mcpHub) run(ctx context.Context, wire, raw string) toolResult {
	s, t, ok := h.find(wire)
	if !ok {
		return fail("the " + wire + " tool is not connected")
	}
	args := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			return fail("the tool arguments were not valid JSON: " + err.Error())
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	res, err := s.conn.call(callCtx, "tools/call", map[string]any{"name": t.name, "arguments": args})
	if err != nil {
		return fail(s.name + ": " + err.Error())
	}
	var out struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			MimeType string `json:"mimeType"`
			Resource struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"resource"`
		} `json:"content"`
		Structured any  `json:"structuredContent"`
		IsError    bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return fail("unreadable reply from " + s.name)
	}
	var parts []string
	for _, c := range out.Content {
		switch c.Type {
		case "text":
			parts = append(parts, c.Text)
		case "resource":
			parts = append(parts, c.Resource.URI+"\n"+c.Resource.Text)
		default:
			parts = append(parts, fmt.Sprintf("[%s %s not shown]", c.Type, c.MimeType))
		}
	}
	if len(parts) == 0 && out.Structured != nil {
		data, _ := json.Marshal(out.Structured)
		parts = append(parts, string(data))
	}
	text := strings.Join(parts, "\n")
	if len(text) > 30000 {
		text = cutUTF8(text, 30000) + "\n… (cut)"
	}
	first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if out.IsError {
		return toolResult{output: "Error: " + text, summary: clipUTF8(first, 80)}
	}
	return toolResult{ok: true, output: text, summary: "→ " + clipUTF8(first, 80)}
}

func (h *mcpHub) close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for _, s := range h.servers {
		if s.state == "starting" {
			s.state, s.err = "error", "hub closed"
		}
		if s.conn != nil {
			s.conn.close()
		}
	}
}

type mcpStatus struct {
	name, state, err, kind string
	tools                  []string
}

func (h *mcpHub) status() []mcpStatus {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []mcpStatus
	for _, s := range h.servers {
		kind := "command"
		if s.config.URL != "" {
			kind = "url"
		}
		st := mcpStatus{name: s.name, state: s.state, err: s.err, kind: kind}
		for _, t := range s.tools {
			st.tools = append(st.tools, t.name)
		}
		out = append(out, st)
	}
	return out
}

func dialMCP(cfg mcpServerConfig) (mcpConn, error) {
	switch {
	case cfg.URL != "":
		return &mcpHTTP{url: cfg.URL, headers: cfg.Headers}, nil
	case cfg.Command != "":
		conn, err := dialStdio(cfg)
		if err != nil {
			return nil, err
		}
		return conn, nil
	}
	return nil, errors.New("needs a \"command\" or a \"url\"")
}

// rpcMessage is one JSON-RPC envelope. ID is kept raw: we always send numeric
// ids, but servers may use strings for their own requests and we must echo
// those back byte-for-byte.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// rpcID renders an outgoing numeric id; the same bytes key the waiting map.
func rpcID(n int64) json.RawMessage {
	return json.RawMessage(strconv.FormatInt(n, 10))
}

// hasID reports whether a message carries a usable id (absent and null both
// mean "notification").
func (m rpcMessage) hasID() bool {
	return len(m.ID) > 0 && string(m.ID) != "null"
}

type mcpStdio struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex
	next    atomic.Int64
	mu      sync.Mutex
	waiting map[string]chan rpcMessage
	done    chan struct{}
	stderr  *tailBuffer
}

func dialStdio(cfg mcpServerConfig) (*mcpStdio, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+os.ExpandEnv(v))
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c := &mcpStdio{cmd: cmd, stdin: stdin, waiting: map[string]chan rpcMessage{}, done: make(chan struct{}), stderr: &tailBuffer{limit: 4000}}
	cmd.Stderr = c.stderr
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%s isn't installed in prism's box", cfg.Command)
		}
		return nil, fmt.Errorf("can't start %s: %w", cfg.Command, err)
	}
	go c.read(stdout)
	return c, nil
}

func (c *mcpStdio) read(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		var msg rpcMessage
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			continue
		}
		switch {
		case msg.hasID() && msg.Method != "":
			// Server-initiated request: answer with its id exactly as sent.
			reply := map[string]any{"jsonrpc": "2.0", "id": msg.ID}
			if msg.Method == "ping" {
				reply["result"] = map[string]any{}
			} else {
				reply["error"] = map[string]any{"code": -32601, "message": "not supported by prism"}
			}
			c.write(reply)
		case msg.hasID():
			key := string(msg.ID)
			c.mu.Lock()
			ch := c.waiting[key]
			delete(c.waiting, key)
			c.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
		}
	}
	close(c.done)
	// Reap the child so it doesn't linger as a zombie once stdout closes.
	if c.cmd != nil {
		c.cmd.Wait()
	}
}

func (c *mcpStdio) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

func (c *mcpStdio) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.next.Add(1)
	raw := rpcID(id)
	key := string(raw)
	ch := make(chan rpcMessage, 1)
	c.mu.Lock()
	c.waiting[key] = ch
	c.mu.Unlock()
	if err := c.write(rpcMessage{JSONRPC: "2.0", ID: raw, Method: method, Params: params}); err != nil {
		return nil, c.died(err)
	}
	answer := func(msg rpcMessage) (json.RawMessage, error) {
		if msg.Error != nil {
			return nil, errors.New(msg.Error.Message)
		}
		return msg.Result, nil
	}
	select {
	case msg := <-ch:
		return answer(msg)
	case <-c.done:
		// The reply may have been delivered just before the server exited.
		select {
		case msg := <-ch:
			return answer(msg)
		default:
		}
		return nil, c.died(errors.New("the tool stopped"))
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.waiting, key)
		c.mu.Unlock()
		c.write(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": id}})
		return nil, ctx.Err()
	}
}

func (c *mcpStdio) died(err error) error {
	if tail := strings.TrimSpace(c.stderr.tail()); tail != "" {
		lines := splitLines(tail)
		return fmt.Errorf("%v: %s", err, lines[len(lines)-1])
	}
	return err
}

func (c *mcpStdio) notify(method string, params any) {
	c.write(rpcMessage{JSONRPC: "2.0", Method: method, Params: params})
}

// close shuts stdin so a well-behaved server exits on its own; if it hasn't
// within 2s it is killed. The read goroutine reaps it either way.
func (c *mcpStdio) close() {
	c.stdin.Close()
	if c.cmd != nil && c.cmd.Process != nil {
		go func() {
			select {
			case <-c.done:
			case <-time.After(2 * time.Second):
				c.cmd.Process.Kill()
			}
		}()
	}
}

type mcpHTTP struct {
	url     string
	headers map[string]string
	session string
	next    atomic.Int64
	mu      sync.Mutex
}

func (c *mcpHTTP) post(ctx context.Context, msg rpcMessage) (*http.Response, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json, text/event-stream")
	req.Header.Set("mcp-protocol-version", mcpProtocol)
	for k, v := range c.headers {
		req.Header.Set(k, os.ExpandEnv(v))
	}
	c.mu.Lock()
	if c.session != "" {
		req.Header.Set("mcp-session-id", c.session)
	}
	c.mu.Unlock()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if s := res.Header.Get("mcp-session-id"); s != "" {
		c.mu.Lock()
		c.session = s
		c.mu.Unlock()
	}
	return res, nil
}

func (c *mcpHTTP) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := rpcID(c.next.Add(1))
	res, err := c.post(ctx, rpcMessage{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return nil, fmt.Errorf("HTTP %d %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var msg rpcMessage
	if strings.Contains(res.Header.Get("content-type"), "event-stream") {
		scanner := bufio.NewScanner(res.Body)
		scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
		found := false
		// SSE: an event's data is every "data:" line up to the blank line,
		// joined with newlines. Only a complete event is parsed.
		var data []string
		flush := func() bool {
			if len(data) == 0 {
				return false
			}
			event := strings.Join(data, "\n")
			data = nil
			var m rpcMessage
			if json.Unmarshal([]byte(event), &m) == nil && string(m.ID) == string(id) {
				msg, found = m, true
				return true
			}
			return false
		}
		for scanner.Scan() {
			line := strings.TrimSuffix(scanner.Text(), "\r")
			if line == "" {
				if flush() {
					break
				}
				continue
			}
			if field, ok := strings.CutPrefix(line, "data:"); ok {
				data = append(data, strings.TrimPrefix(field, " "))
			}
		}
		if !found {
			flush() // stream ended without a trailing blank line
		}
		if !found {
			return nil, errors.New("no reply in the event stream")
		}
	} else if err := json.NewDecoder(res.Body).Decode(&msg); err != nil {
		return nil, err
	}
	if msg.Error != nil {
		return nil, errors.New(msg.Error.Message)
	}
	return msg.Result, nil
}

func (c *mcpHTTP) notify(method string, params any) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if res, err := c.post(ctx, rpcMessage{JSONRPC: "2.0", Method: method, Params: params}); err == nil {
		res.Body.Close()
	}
}

func (c *mcpHTTP) close() {}

func (h *mcpHub) wait(limit time.Duration) {
	if h == nil {
		return
	}
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		busy := false
		for _, s := range h.status() {
			busy = busy || s.state == "starting"
		}
		if !busy {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (h *mcpHub) has(wire string) bool {
	_, _, ok := h.find(wire)
	return ok
}
