package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeMCPEnv makes the test binary act as a stdio MCP server when set,
// so the stdio tests don't need python or any other external tool.
const fakeMCPEnv = "PRISM_FAKE_MCP"

func TestMain(m *testing.M) {
	if os.Getenv(fakeMCPEnv) == "1" {
		runFakeStdioMCP(os.Stdin, os.Stdout)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runFakeStdioMCP speaks newline-delimited JSON-RPC: initialize, a two page
// tools/list, tools/call for "alpha" (sends a ping to the client mid-call and
// waits for the answer) and "beta" (isError).
func runFakeStdioMCP(in io.Reader, out io.Writer) {
	reader := bufio.NewReader(in)
	w := bufio.NewWriter(out)
	send := func(v any) {
		data, _ := json.Marshal(v)
		w.Write(append(data, '\n'))
		w.Flush()
	}
	next := func() (map[string]any, bool) {
		for {
			line, err := reader.ReadString('\n')
			if err != nil && line == "" {
				return nil, false
			}
			var msg map[string]any
			if json.Unmarshal([]byte(strings.TrimSpace(line)), &msg) == nil {
				return msg, true
			}
			if err != nil {
				return nil, false
			}
		}
	}
	reply := func(id any, result any) {
		send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	for {
		msg, ok := next()
		if !ok {
			return
		}
		id, hasID := msg["id"]
		method, _ := msg["method"].(string)
		params, _ := msg["params"].(map[string]any)
		if !hasID {
			continue // notification
		}
		switch method {
		case "initialize":
			if d := os.Getenv("PRISM_FAKE_MCP_DELAY"); d != "" {
				if ms, err := strconv.Atoi(d); err == nil && ms > 0 {
					time.Sleep(time.Duration(ms) * time.Millisecond)
				}
			}
			reply(id, map[string]any{
				"protocolVersion": mcpProtocol,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake", "version": "0"},
			})
		case "tools/list":
			cursor, _ := params["cursor"].(string)
			if cursor == "" {
				reply(id, map[string]any{
					"tools": []any{map[string]any{
						"name":        "alpha",
						"description": "first tool",
						"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"who": map[string]any{"type": "string"}}},
					}},
					"nextCursor": "page2",
				})
			} else {
				reply(id, map[string]any{
					"tools": []any{map[string]any{"name": "beta", "description": "always fails"}},
				})
			}
		case "tools/call":
			name, _ := params["name"].(string)
			args, _ := params["arguments"].(map[string]any)
			switch name {
			case "alpha":
				// Server-initiated requests mid-call, one with a numeric id and
				// one with a string id; the client must answer both with the
				// id exactly as sent.
				send(map[string]any{"jsonrpc": "2.0", "id": 9001, "method": "ping"})
				send(map[string]any{"jsonrpc": "2.0", "id": "ping-str", "method": "ping"})
				answers := map[string]string{}
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) && len(answers) < 2 {
					m, ok := next()
					if !ok {
						break
					}
					var key string
					switch rid := m["id"].(type) {
					case float64:
						if rid == 9001 {
							key = "num"
						}
					case string:
						if rid == "ping-str" {
							key = "str"
						}
					}
					if key == "" {
						continue
					}
					if _, isErr := m["error"]; isErr {
						answers[key] = "ping errored"
					} else if _, hasRes := m["result"]; hasRes {
						answers[key] = "pong"
					}
				}
				pong := "ping unanswered"
				if answers["num"] == "pong" && answers["str"] == "pong" {
					pong = "pong"
				} else if answers["num"] != "" || answers["str"] != "" {
					pong = fmt.Sprintf("num=%q str=%q", answers["num"], answers["str"])
				}
				who, _ := args["who"].(string)
				reply(id, map[string]any{
					"content": []any{
						map[string]any{"type": "text", "text": "hello " + who},
						map[string]any{"type": "text", "text": pong},
					},
				})
			case "beta":
				reply(id, map[string]any{
					"isError": true,
					"content": []any{map[string]any{"type": "text", "text": "beta exploded"}},
				})
			default:
				send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32602, "message": "unknown tool " + name}})
			}
		default:
			send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
}

// writeMCPConfig points XDG_CONFIG_HOME at a temp dir holding prism/mcp.json
// and returns a project dir with no .prism overrides.
func writeMCPConfig(t *testing.T, servers map[string]any) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	if dir := configDir(); !strings.HasPrefix(dir, home) {
		t.Fatalf("configDir() = %q, expected it under XDG_CONFIG_HOME %q", dir, home)
	}
	if err := os.MkdirAll(configDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"mcpServers": servers})
	if err := os.WriteFile(filepath.Join(configDir(), "mcp.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return t.TempDir()
}

func waitMCP(t *testing.T, hub *mcpHub, name string) mcpStatus {
	t.Helper()
	hub.wait(20 * time.Second)
	for _, s := range hub.status() {
		if s.name == name {
			if s.state == "starting" {
				t.Fatalf("server %s still starting after wait", name)
			}
			return s
		}
	}
	t.Fatalf("server %s missing from status: %+v", name, hub.status())
	return mcpStatus{}
}

func specNames(hub *mcpHub) []string {
	var names []string
	for _, spec := range hub.specs() {
		fn := spec.(map[string]any)["function"].(map[string]any)
		names = append(names, fn["name"].(string))
	}
	return names
}

func TestMCPStdio(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := writeMCPConfig(t, map[string]any{
		"fake": map[string]any{"command": exe, "env": map[string]string{fakeMCPEnv: "1"}},
	})
	hub := startMCP(dir)
	defer hub.close()

	st := waitMCP(t, hub, "fake")
	if st.state != "ready" {
		t.Fatalf("state = %q err = %q, want ready", st.state, st.err)
	}
	if st.kind != "command" {
		t.Errorf("kind = %q, want command", st.kind)
	}
	if got := strings.Join(st.tools, ","); got != "alpha,beta" {
		t.Errorf("tools across both pages = %q, want alpha,beta", got)
	}
	if got := strings.Join(specNames(hub), ","); got != "fake__alpha,fake__beta" {
		t.Errorf("spec names = %q, want fake__alpha,fake__beta", got)
	}
	for _, spec := range hub.specs() {
		fn := spec.(map[string]any)["function"].(map[string]any)
		if desc := fn["description"].(string); !strings.HasPrefix(desc, "[fake] ") {
			t.Errorf("description %q not prefixed with server name", desc)
		}
		params := fn["parameters"].(map[string]any)
		if params["type"] != "object" {
			t.Errorf("%s parameters missing object schema: %v", fn["name"], params)
		}
	}

	ctx := context.Background()
	res := hub.run(ctx, "fake__alpha", `{"who":"world"}`)
	if !res.ok {
		t.Fatalf("alpha not ok: %q", res.output)
	}
	if res.output != "hello world\npong" {
		t.Errorf("alpha output = %q, want hello world + pong (numeric and string-id pings answered)", res.output)
	}
	if res.summary != "→ hello world" {
		t.Errorf("alpha summary = %q", res.summary)
	}

	res = hub.run(ctx, "fake__beta", "")
	if res.ok {
		t.Errorf("beta isError should map to ok=false: %+v", res)
	}
	if !strings.HasPrefix(res.output, "Error: beta exploded") {
		t.Errorf("beta output = %q", res.output)
	}
	if res.summary != "beta exploded" {
		t.Errorf("beta summary = %q", res.summary)
	}

	res = hub.run(ctx, "fake__alpha", "{not json")
	if res.ok || !strings.Contains(res.output, "not valid JSON") {
		t.Errorf("bad args: %+v", res)
	}
	res = hub.run(ctx, "fake__nope", "")
	if res.ok || !strings.Contains(res.output, "not connected") {
		t.Errorf("unknown tool: %+v", res)
	}
}

func TestMCPHTTP(t *testing.T) {
	const session = "sess-42"
	var (
		mu         sync.Mutex
		sessionErr []string
		seen       []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if err := json.Unmarshal(body, &msg); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		mu.Lock()
		seen = append(seen, msg.Method)
		if msg.Method != "initialize" && r.Header.Get("mcp-session-id") != session {
			sessionErr = append(sessionErr, fmt.Sprintf("%s: mcp-session-id=%q", msg.Method, r.Header.Get("mcp-session-id")))
		}
		mu.Unlock()
		if !strings.Contains(r.Header.Get("accept"), "text/event-stream") {
			http.Error(w, "accept must include text/event-stream", 406)
			return
		}
		if r.Header.Get("x-token") != "secret" {
			http.Error(w, "missing custom header", 401)
			return
		}
		writeJSON := func(result any) {
			w.Header().Set("content-type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
		}
		writeSSE := func(result any, multiline bool) {
			w.Header().Set("content-type", "text/event-stream")
			w.WriteHeader(200)
			// Noise first: a comment, an unrelated notification, a reply to
			// a different (string) id, then the real reply.
			fmt.Fprint(w, ": keepalive\n\n")
			fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{}}\n\n")
			fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":\"someone-else\",\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"wrong\"}]}}\n\n")
			data, _ := json.Marshal(result)
			if multiline {
				// One event whose JSON spans several data: lines (joined with
				// \n per the SSE spec); the blank line ends the event.
				fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\ndata:\"id\":%s,\ndata: \"result\":%s\ndata: }\n\n", msg.ID, data)
				return
			}
			fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":%s}\n\n", msg.ID, data)
		}
		switch msg.Method {
		case "initialize":
			w.Header().Set("mcp-session-id", session)
			writeJSON(map[string]any{"protocolVersion": mcpProtocol, "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "h", "version": "0"}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeSSE(map[string]any{"tools": []any{
				map[string]any{"name": "json-tool", "description": "replies as json"},
				map[string]any{"name": "sse-tool", "description": "replies as sse"},
				map[string]any{"name": "sse-multi", "description": "replies as multi-line sse"},
			}}, false)
		case "tools/call":
			name, _ := msg.Params["name"].(string)
			result := map[string]any{"content": []any{map[string]any{"type": "text", "text": "from " + name}}}
			switch name {
			case "sse-tool":
				writeSSE(result, false)
			case "sse-multi":
				writeSSE(result, true)
			default:
				writeJSON(result)
			}
		default:
			writeJSON(map[string]any{})
		}
	}))
	defer srv.Close()

	t.Setenv("MCP_TEST_TOKEN", "secret")
	dir := writeMCPConfig(t, map[string]any{
		"web": map[string]any{"url": srv.URL, "headers": map[string]string{"x-token": "$MCP_TEST_TOKEN"}},
	})
	hub := startMCP(dir)
	defer hub.close()

	st := waitMCP(t, hub, "web")
	if st.state != "ready" {
		t.Fatalf("state = %q err = %q, want ready", st.state, st.err)
	}
	if st.kind != "url" {
		t.Errorf("kind = %q, want url", st.kind)
	}
	if got := strings.Join(specNames(hub), ","); got != "web__json-tool,web__sse-tool,web__sse-multi" {
		t.Errorf("spec names = %q", got)
	}

	ctx := context.Background()
	res := hub.run(ctx, "web__json-tool", `{}`)
	if !res.ok || res.output != "from json-tool" {
		t.Errorf("json-tool: %+v", res)
	}
	res = hub.run(ctx, "web__sse-tool", `{}`)
	if !res.ok || res.output != "from sse-tool" {
		t.Errorf("sse-tool: %+v", res)
	}
	res = hub.run(ctx, "web__sse-multi", `{}`)
	if !res.ok || res.output != "from sse-multi" {
		t.Errorf("sse-multi (multi-line data:): %+v", res)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sessionErr) > 0 {
		t.Errorf("mcp-session-id not echoed: %v", sessionErr)
	}
	want := "initialize,notifications/initialized,tools/list,tools/call,tools/call,tools/call"
	if got := strings.Join(seen, ","); got != want {
		t.Errorf("requests seen = %q, want %q", got, want)
	}
}

func TestMCPMissingCommand(t *testing.T) {
	dir := writeMCPConfig(t, map[string]any{
		"ghost":   map[string]any{"command": "prism-no-such-binary-xyz"},
		"abs":     map[string]any{"command": filepath.Join(t.TempDir(), "nope")},
		"nothing": map[string]any{},
		"off":     map[string]any{"command": "whatever", "disabled": true},
	})
	hub := startMCP(dir)
	defer hub.close()

	for _, name := range []string{"ghost", "abs", "nothing"} {
		st := waitMCP(t, hub, name)
		if st.state != "error" {
			t.Errorf("%s: state = %q, want error", name, st.state)
		}
		if st.err == "" {
			t.Errorf("%s: empty error", name)
		}
	}
	st := waitMCP(t, hub, "ghost")
	if !strings.Contains(st.err, "isn't installed") {
		t.Errorf("ghost error = %q, want the not-installed message", st.err)
	}
	st = waitMCP(t, hub, "off")
	if st.state != "off" {
		t.Errorf("off: state = %q", st.state)
	}
	if specs := hub.specs(); len(specs) != 0 {
		t.Errorf("no specs expected, got %d", len(specs))
	}
	if res := hub.run(context.Background(), "ghost__x", ""); res.ok {
		t.Errorf("run on errored server should fail")
	}
}

// TestMCPStringIDs drives the stdio reader directly: a server request with a
// string id must be answered with that id verbatim, a reply carrying a
// string id must not be mistaken for our numeric one, and the numeric reply
// must still arrive even if the server exits right after sending it.
func TestMCPStringIDs(t *testing.T) {
	fromServer, serverOut := io.Pipe()
	clientIn, toServer := io.Pipe()
	c := &mcpStdio{stdin: toServer, waiting: map[string]chan rpcMessage{}, done: make(chan struct{}), stderr: &tailBuffer{limit: 100}}
	go c.read(fromServer)

	var (
		linesMu sync.Mutex
		lines   []string
	)
	go func() {
		sc := bufio.NewScanner(clientIn)
		for sc.Scan() {
			linesMu.Lock()
			lines = append(lines, sc.Text())
			linesMu.Unlock()
		}
	}()
	waitLines := func(n int) []string {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			linesMu.Lock()
			got := append([]string(nil), lines...)
			linesMu.Unlock()
			if len(got) >= n {
				return got
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %d lines from the client", n)
		return nil
	}

	type resp struct {
		res json.RawMessage
		err error
	}
	got := make(chan resp, 1)
	go func() {
		r, err := c.call(context.Background(), "tools/call", map[string]any{})
		got <- resp{r, err}
	}()
	req := waitLines(1)[0]
	var sent map[string]any
	if err := json.Unmarshal([]byte(req), &sent); err != nil {
		t.Fatalf("client request not JSON: %q", req)
	}
	if _, isNum := sent["id"].(float64); !isNum {
		t.Fatalf("outgoing id should be numeric, got %q", req)
	}

	// A ping with a string id, then a stray reply with a string id, then the
	// real reply with our numeric id, then EOF (server exits immediately).
	fmt.Fprint(serverOut, `{"jsonrpc":"2.0","id":"abc-123","method":"ping"}`+"\n")
	fmt.Fprint(serverOut, `{"jsonrpc":"2.0","id":"1","result":{"wrong":true}}`+"\n")
	fmt.Fprint(serverOut, `{"jsonrpc":"2.0","id":1,"result":{"right":true}}`+"\n")
	serverOut.Close()

	pong := waitLines(2)[1]
	var reply map[string]any
	if err := json.Unmarshal([]byte(pong), &reply); err != nil {
		t.Fatalf("ping reply not JSON: %q", pong)
	}
	if id, _ := reply["id"].(string); id != "abc-123" {
		t.Errorf("ping reply id = %v, want the string \"abc-123\" verbatim", reply["id"])
	}
	if _, ok := reply["result"]; !ok {
		t.Errorf("ping reply missing result: %q", pong)
	}

	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("call failed: %v (reply delivered right before EOF must still win)", r.err)
		}
		if string(r.res) != `{"right":true}` {
			t.Errorf("call result = %s, want the numeric-id reply, not the string-id one", r.res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("call did not return")
	}
	<-c.done
}

// TestMCPCrossServerDedupWire verifies that two servers whose tool names
// produce the same wire name are disambiguated globally (not per-server).
func TestMCPCrossServerDedupWire(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Both servers expose a tool called "alpha". Dots and slashes sanitize to
	// the same prefix, so both wires would be "fake_srv__alpha" without
	// cross-server dedup. "fake" and "fake2" would not collide.
	dir := writeMCPConfig(t, map[string]any{
		"fake.srv": map[string]any{"command": exe, "env": map[string]string{fakeMCPEnv: "1"}},
		"fake/srv": map[string]any{"command": exe, "env": map[string]string{fakeMCPEnv: "1"}},
	})
	hub := startMCP(dir)
	defer hub.close()
	hub.wait(20 * time.Second)

	names := specNames(hub)
	seen := map[string]int{}
	for _, n := range names {
		seen[n]++
	}
	for n, c := range seen {
		if c > 1 {
			t.Errorf("wire name %q appears %d times — cross-server dedup failed", n, c)
		}
	}
	// Both servers must still expose their tools (none silently dropped).
	if len(names) != 4 { // fake.srv: alpha,beta + fake/srv: alpha,beta
		t.Errorf("expected 4 unique wire names, got %d: %v", len(names), names)
	}
	want := map[string]bool{"fake_srv__alpha": true, "fake_srv__beta": true, "fake_srv__alpha_2": true, "fake_srv__beta_2": true}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected wire %q; names %v (sanitized prefixes must actually collide)", n, names)
		}
	}
}

// TestMCPDedupSuffixNoCollide verifies that when a tool is renamed to _2 and
// a tool named _2 already exists, the suffix keeps incrementing until free.
func TestMCPDedupSuffixNoCollide(t *testing.T) {
	// Build a hub directly with hand-crafted tools that would collide naively.
	hub := &mcpHub{}
	s1 := &mcpServer{name: "s", state: "ready", tools: []mcpTool{
		{server: "s", name: "x", wire: "s__x"},
		{server: "s", name: "x", wire: "s__x"},     // dup → would become s__x_2
		{server: "s", name: "x_2", wire: "s__x_2"}, // already taken
	}}
	hub.servers = []*mcpServer{s1}
	dedupWireNames(hub.servers)

	wires := make([]string, len(s1.tools))
	for i, t := range s1.tools {
		wires[i] = t.wire
	}
	seen := map[string]int{}
	for _, w := range wires {
		seen[w]++
	}
	for w, c := range seen {
		if c > 1 {
			t.Errorf("wire %q still duplicated after dedup: %v", w, wires)
		}
	}
}

// TestMCPHubClosedDuringConnect starts a real hub against a slow fake server,
// closes it while connect is still in flight, and checks close() itself marks
// that server error "hub closed" instead of leaving it "starting".
func TestMCPHubClosedDuringConnect(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := writeMCPConfig(t, map[string]any{
		"slow": map[string]any{"command": exe, "env": map[string]string{fakeMCPEnv: "1", "PRISM_FAKE_MCP_DELAY": "800"}},
	})
	hub := startMCP(dir)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st := hub.status()
		if len(st) == 1 && st[0].name == "slow" && st[0].state == "starting" {
			break
		}
		if len(st) == 1 && st[0].state != "starting" {
			t.Fatalf("slow server left starting before close: %+v", st)
		}
		time.Sleep(10 * time.Millisecond)
	}
	hub.close()
	var got mcpStatus
	for _, s := range hub.status() {
		if s.name == "slow" {
			got = s
		}
	}
	if got.state == "starting" {
		t.Fatalf("server state is still starting after hub.close()")
	}
	if got.state != "error" || got.err != "hub closed" {
		t.Fatalf("status after close = %+v, want state error err %q", got, "hub closed")
	}
}

func TestMCPBadConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	os.MkdirAll(configDir(), 0o755)
	os.WriteFile(filepath.Join(configDir(), "mcp.json"), []byte("{nope"), 0o644)
	hub := startMCP(t.TempDir())
	defer hub.close()
	sts := hub.status()
	if len(sts) != 1 || sts[0].name != "config" || sts[0].state != "error" || !strings.Contains(sts[0].err, "mcp.json") {
		t.Errorf("bad config status = %+v", sts)
	}
}
