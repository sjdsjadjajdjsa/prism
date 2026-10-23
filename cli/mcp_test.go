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
	if got := strings.Join(specNames(hub), ","); got != "web__json-tool,web__sse-multi,web__sse-tool" {
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

// --- easy setup: catalog, add/remove, keys, reconnect ---------------------

func setupHome(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return t.TempDir()
}

func projectServers(t *testing.T, dir string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(mcpProjectPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("project file is not valid JSON: %v\n%s", err, data)
	}
	return file.Servers
}

func TestMCPCatalogValid(t *testing.T) {
	list := mcpCatalog()
	if len(list) < 6 || len(list) > 10 {
		t.Fatalf("catalog has %d entries, want 6-10", len(list))
	}
	ids := map[string]bool{}
	for _, e := range list {
		if !skillName.MatchString(e.ID) {
			t.Errorf("id %q is not a simple lowercase name", e.ID)
		}
		if ids[e.ID] {
			t.Errorf("duplicate id %q", e.ID)
		}
		ids[e.ID] = true
		if e.Name == "" || e.About == "" || e.Try == "" {
			t.Errorf("%s: name, about and try are all required: %+v", e.ID, e)
		}
		if (e.Command == "") == (e.URL == "") {
			t.Errorf("%s: needs exactly one of command or url", e.ID)
		}
		if e.Command != "" && e.Command != "npx" && e.Command != "node" {
			t.Errorf("%s: command %q isn't something prism's container has", e.ID, e.Command)
		}
		if e.URL != "" && !strings.HasPrefix(e.URL, "https://") {
			t.Errorf("%s: url must be https", e.ID)
		}
		cfg := e.config()
		vars := mcpConfigVars(cfg)
		needed := map[string]bool{}
		for _, n := range e.Needs {
			needed[n.Name] = true
			if n.Where == "" || !strings.Contains(n.Where, "https://") {
				t.Errorf("%s: %s must say where to get it (a link)", e.ID, n.Name)
			}
		}
		for _, v := range vars {
			if !needed[v] {
				t.Errorf("%s: ${%s} is used but not listed in needs", e.ID, v)
			}
		}
		for n := range needed {
			if !strings.Contains(strings.Join(vars, " "), n) {
				t.Errorf("%s: need %s is never referenced as ${%s}", e.ID, n, n)
			}
		}
		for _, v := range append(mapValues(cfg.Env), mapValues(cfg.Headers)...) {
			if strings.Contains(v, "ghp_") || (len(mcpVars(v)) == 0 && len(v) > 24) {
				t.Errorf("%s: looks like a literal secret: %q", e.ID, v)
			}
		}
	}
	if e, ok := mcpFindCatalog("2"); !ok || e.ID != list[1].ID {
		t.Errorf("number lookup = %+v %v", e, ok)
	}
	if _, ok := mcpFindCatalog("0"); ok {
		t.Error("0 must not match")
	}
	if _, ok := mcpFindCatalog("99"); ok {
		t.Error("99 must not match")
	}
	if e, ok := mcpFindCatalog("FETCH"); !ok || e.ID != "fetch" {
		t.Errorf("id lookup = %+v %v", e, ok)
	}
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func TestMCPAddMergeRemove(t *testing.T) {
	dir := setupHome(t)
	path := mcpProjectPath(dir)
	os.MkdirAll(filepath.Dir(path), 0o755)
	mine := `{"note": "keep me", "mcpServers": {"mine": {"command": "my-server", "args": ["--x"], "env": {"K": "${MY_KEY}"}}}}`
	os.WriteFile(path, []byte(mine), 0o644)

	fetch, _ := mcpFindCatalog("fetch")
	github, _ := mcpFindCatalog("github")
	if added, err := mcpAddServer(dir, fetch); err != nil || !added {
		t.Fatalf("add fetch = %v %v", added, err)
	}
	if added, err := mcpAddServer(dir, github); err != nil || !added {
		t.Fatalf("add github = %v %v", added, err)
	}
	servers := projectServers(t, dir)
	if len(servers) != 3 {
		t.Fatalf("servers = %v, want mine+fetch+github", servers)
	}
	if !strings.Contains(string(servers["mine"]), "${MY_KEY}") {
		t.Errorf("existing entry changed: %s", servers["mine"])
	}
	if !strings.Contains(string(servers["github"]), "${GITHUB_TOKEN}") || strings.Contains(string(servers["github"]), "ghp_") {
		t.Errorf("github must keep the ${GITHUB_TOKEN} reference: %s", servers["github"])
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"note": "keep me"`) {
		t.Errorf("unknown top-level key was dropped:\n%s", data)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.tmp"))
	if len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}

	// Adding again leaves a hand-edited entry alone.
	edited := strings.Replace(string(data), `"mcp-fetch-server"`, `"mcp-fetch-server", "--my-flag"`, 1)
	os.WriteFile(path, []byte(edited), 0o644)
	if added, err := mcpAddServer(dir, fetch); err != nil || added {
		t.Fatalf("second add = %v %v, want not added", added, err)
	}
	if now, _ := os.ReadFile(path); string(now) != edited {
		t.Errorf("file rewritten although nothing was added")
	}

	if found, err := mcpRemoveServer(dir, "fetch"); err != nil || !found {
		t.Fatalf("remove fetch = %v %v", found, err)
	}
	if found, _ := mcpRemoveServer(dir, "fetch"); found {
		t.Error("removing twice should report not found")
	}
	servers = projectServers(t, dir)
	if len(servers) != 2 || servers["mine"] == nil || servers["github"] == nil {
		t.Errorf("after remove: %v", servers)
	}
	for _, name := range []string{"github", "mine"} {
		mcpRemoveServer(dir, name)
	}
	if servers = projectServers(t, dir); len(servers) != 0 {
		t.Errorf("all removed, still have %v", servers)
	}
}

func TestMCPAddCreatesFileAndKeepsBrokenOne(t *testing.T) {
	dir := setupHome(t)
	mem, _ := mcpFindCatalog("memory")
	if added, err := mcpAddServer(dir, mem); err != nil || !added {
		t.Fatalf("add into empty project = %v %v", added, err)
	}
	if projectServers(t, dir)["memory"] == nil {
		t.Fatal("memory not written")
	}
	bad := t.TempDir()
	os.MkdirAll(filepath.Join(bad, ".prism"), 0o755)
	os.WriteFile(mcpProjectPath(bad), []byte("{oops"), 0o644)
	_, err := mcpAddServer(bad, mem)
	if err == nil || !strings.Contains(err.Error(), "isn't valid JSON") {
		t.Fatalf("err = %v, want a plain invalid-JSON message", err)
	}
	if data, _ := os.ReadFile(mcpProjectPath(bad)); string(data) != "{oops" {
		t.Errorf("broken file was overwritten: %q", data)
	}
}

func TestMCPEnvHints(t *testing.T) {
	setupHome(t)
	t.Setenv("GITHUB_TOKEN", "")
	github, _ := mcpFindCatalog("github")
	missing := mcpMissingVars(github.config())
	if len(missing) != 1 || missing[0] != "GITHUB_TOKEN" {
		t.Fatalf("missing = %v", missing)
	}
	_, err := dialMCP(github.config())
	if err == nil {
		t.Fatal("dial without the token must fail before any request")
	}
	for _, want := range []string{"GITHUB_TOKEN", "https://github.com/settings/tokens", "/mcp key GITHUB_TOKEN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("hint %q lacks %q", err, want)
		}
	}

	if err := mcpSetKey("GITHUB_TOKEN", "abc123"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(mcpEnvPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mcp.env mode = %v, want 0600", info.Mode().Perm())
	}
	if got := mcpExpand("Bearer ${GITHUB_TOKEN}"); got != "Bearer abc123" {
		t.Errorf("expand = %q", got)
	}
	if len(mcpMissingVars(github.config())) != 0 {
		t.Error("key saved but still reported missing")
	}
	t.Setenv("GITHUB_TOKEN", "fromenv")
	if got := mcpExpand("$GITHUB_TOKEN"); got != "fromenv" {
		t.Errorf("real environment should win, got %q", got)
	}
	t.Setenv("GITHUB_TOKEN", "")
	mcpSetKey("OTHER", "x")
	mcpSetKey("GITHUB_TOKEN", "second")
	data, _ := os.ReadFile(mcpEnvPath())
	if strings.Count(string(data), "GITHUB_TOKEN=") != 1 || !strings.Contains(string(data), "OTHER=x") || !strings.Contains(string(data), "GITHUB_TOKEN=second") {
		t.Errorf("mcp.env after update:\n%s", data)
	}
	for _, bad := range [][2]string{{"bad name", "v"}, {"OK", ""}, {"OK", "a\nb"}, {"1X", "v"}} {
		if mcpSetKey(bad[0], bad[1]) == nil {
			t.Errorf("mcpSetKey(%q, %q) should fail", bad[0], bad[1])
		}
	}
}

func TestMCPExplain(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"npx isn't installed in prism's box", "nodejs.org"},
		{"uvx isn't installed in prism's box", "starts with npx"},
		{"foo isn't installed in prism's box", "foo isn't installed here"},
		{"context deadline exceeded", "took too long"},
		{"the tool stopped: Error: bad api key", "crashed"},
		{"HTTP 401 unauthorized", "refused the key"},
		{"HTTP 404 nope", "Check the url"},
		{"Post x: dial tcp: lookup x: no such host", "internet"},
		{"something odd", "something odd"},
	}
	for _, c := range cases {
		got := mcpExplain("srv", c.raw)
		if !strings.Contains(got, c.want) {
			t.Errorf("explain(%q) = %q, want it to contain %q", c.raw, got, c.want)
		}
	}
}

func fakeAgent(t *testing.T, servers map[string]any) *Agent {
	t.Helper()
	dir := setupHome(t)
	if servers != nil {
		os.MkdirAll(filepath.Join(dir, ".prism"), 0o755)
		data, _ := json.Marshal(map[string]any{"mcpServers": servers})
		os.WriteFile(mcpProjectPath(dir), data, 0o644)
	}
	hub := startMCP(dir)
	t.Cleanup(hub.close)
	return &Agent{dir: dir, mcp: hub}
}

func runMCP(t *testing.T, a *Agent, text string) string {
	t.Helper()
	res, follow := mcpCommand(a, strings.Fields(text))
	if follow != nil {
		late, _ := follow(20 * time.Second)
		res.lines = append(res.lines, late.lines...)
	}
	return res.title + "\n" + strings.Join(res.lines, "\n")
}

func TestMCPReconnectFakeServer(t *testing.T) {
	exe, _ := os.Executable()
	t.Setenv("MCP_TEST_SECRET", "")
	a := fakeAgent(t, map[string]any{
		"fake":    map[string]any{"command": exe, "env": map[string]string{fakeMCPEnv: "1"}},
		"needy":   map[string]any{"command": exe, "env": map[string]string{fakeMCPEnv: "1", "SECRET": "${MCP_TEST_SECRET}"}},
		"ghost":   map[string]any{"command": "prism-no-such-binary-xyz"},
		"crasher": map[string]any{"command": "sh", "args": []string{"-c", "echo bad credentials >&2; exit 3"}},
	})
	a.mcp.wait(20 * time.Second)

	out := runMCP(t, a, "/mcp test fake")
	if !strings.Contains(out, "● fake is ready") || !strings.Contains(out, "alpha, beta") {
		t.Errorf("test fake:\n%s", out)
	}

	out = runMCP(t, a, "/mcp test needy")
	if !strings.Contains(out, "✕ needy") || !strings.Contains(out, "MCP_TEST_SECRET") || !strings.Contains(out, "/mcp key MCP_TEST_SECRET") {
		t.Errorf("missing env var not explained:\n%s", out)
	}
	out = runMCP(t, a, "/mcp key MCP_TEST_SECRET s3cret")
	if !strings.Contains(out, "reconnecting needy") || !strings.Contains(out, "● needy is ready") {
		t.Errorf("saving the key should reconnect needy:\n%s", out)
	}
	if strings.Contains(out, "s3cret") {
		t.Errorf("the key was echoed back:\n%s", out)
	}

	out = runMCP(t, a, "/mcp test ghost")
	if !strings.Contains(out, "✕ ghost") || !strings.Contains(out, "isn't installed here") {
		t.Errorf("ghost:\n%s", out)
	}
	out = runMCP(t, a, "/mcp test crasher")
	if !strings.Contains(out, "✕ crasher") || !strings.Contains(out, "crashed") || !strings.Contains(out, "wrong or missing key") {
		t.Errorf("crasher:\n%s", out)
	}

	res, _ := mcpCommand(a, strings.Fields("/mcp test nope"))
	if !res.failed || !strings.Contains(res.title, "no server called nope") {
		t.Errorf("unknown server: %+v", res)
	}
	if names := strings.Join(specNames(a.mcp), ","); !strings.Contains(names, "fake__alpha") || !strings.Contains(names, "needy__alpha") {
		t.Errorf("tools after reconnect = %s", names)
	}
}

func TestMCPCommandAddAndRemove(t *testing.T) {
	a := fakeAgent(t, nil)
	t.Setenv("PATH", t.TempDir()) // no npx in here
	t.Setenv("GITHUB_TOKEN", "")

	list := runMCP(t, a, "/mcp add")
	for _, want := range []string{"1  fetch", "github", "[needs a free key]", "/mcp add 1"} {
		if !strings.Contains(list, want) {
			t.Errorf("catalog list lacks %q:\n%s", want, list)
		}
	}
	out := runMCP(t, a, "/mcp add 1")
	if !strings.Contains(out, "added fetch to .prism/mcp.json") || !strings.Contains(out, "✕ fetch") || !strings.Contains(out, "nodejs.org") {
		t.Errorf("add 1 without node:\n%s", out)
	}
	if projectServers(t, a.dir)["fetch"] == nil {
		t.Error("fetch not saved in the project file")
	}
	out = runMCP(t, a, "/mcp add github")
	if !strings.Contains(out, "/mcp key GITHUB_TOKEN") || !strings.Contains(out, "https://github.com/settings/tokens") {
		t.Errorf("add github should say how to set the token:\n%s", out)
	}
	out = runMCP(t, a, "/mcp add fetch")
	if !strings.Contains(out, "already in .prism/mcp.json") {
		t.Errorf("second add:\n%s", out)
	}
	out = runMCP(t, a, "/mcp")
	if !strings.Contains(out, "fetch") || !strings.Contains(out, "add more: /mcp add") {
		t.Errorf("/mcp listing:\n%s", out)
	}
	if res, _ := mcpCommand(a, strings.Fields("/mcp add nonsense")); !res.failed {
		t.Error("unknown catalog id should fail")
	}

	out = runMCP(t, a, "/mcp remove fetch")
	if !strings.Contains(out, "removed fetch") {
		t.Errorf("remove:\n%s", out)
	}
	for _, s := range a.mcp.status() {
		if s.name == "fetch" {
			t.Errorf("fetch still in the hub: %+v", s)
		}
	}
	if projectServers(t, a.dir)["fetch"] != nil || projectServers(t, a.dir)["github"] == nil {
		t.Error("remove must only touch fetch")
	}
	if res, _ := mcpCommand(a, strings.Fields("/mcp remove fetch")); !res.failed {
		t.Error("removing a missing server should fail")
	}
}

func TestMCPRemoveGlobalServerExplains(t *testing.T) {
	dir := writeMCPConfig(t, map[string]any{"shared": map[string]any{"command": "prism-no-such-binary-xyz"}})
	hub := startMCP(dir)
	defer hub.close()
	a := &Agent{dir: dir, mcp: hub}
	res, _ := mcpCommand(a, strings.Fields("/mcp remove shared"))
	if !res.failed || !strings.Contains(strings.Join(res.lines, " "), "~/.config/prism/mcp.json") {
		t.Errorf("global entry removal: %+v", res)
	}
}

func TestMCPCommandShapes(t *testing.T) {
	exe, _ := os.Executable()
	a := fakeAgent(t, map[string]any{"fake": map[string]any{"command": exe, "env": map[string]string{fakeMCPEnv: "1"}}})
	a.mcp.wait(20 * time.Second)

	post := func(text string) map[string]any {
		s := &server{agent: a}
		body, _ := json.Marshal(map[string]string{"text": text})
		w := httptest.NewRecorder()
		s.command(w, httptest.NewRequest("POST", "/command", strings.NewReader(string(body))))
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%q: %v: %s", text, err, w.Body.String())
		}
		return out
	}
	got := post("/mcp")
	if got["kind"] != "info" || got["title"] != "connected tools (MCP)" {
		t.Errorf("/mcp web shape = %v", got)
	}
	got = post("/mcp add")
	if got["kind"] != "info" || got["lines"] == nil {
		t.Errorf("/mcp add web shape = %v", got)
	}
	got = post("/mcp test fake")
	if lines, _ := got["lines"].([]any); got["kind"] != "info" || len(lines) == 0 || !strings.Contains(fmt.Sprint(lines), "ready") {
		t.Errorf("/mcp test web shape = %v", got)
	}
	got = post("/mcp test nope")
	if got["kind"] != "error" || !strings.Contains(fmt.Sprint(got["headline"]), "no server called nope") {
		t.Errorf("web error shape = %v", got)
	}
	got = post("/mcp remove fake")
	if got["kind"] != "info" || !strings.Contains(fmt.Sprint(got["title"]), "removed fake") {
		t.Errorf("/mcp remove web shape = %v", got)
	}

	// Terminal: same text, on the entry list.
	m := &model{agent: a, events: make(chan any, 4)}
	m.command("/mcp add")
	e := m.entries[len(m.entries)-1]
	if e.kind != "info" || !strings.Contains(strings.Join(e.lines, "\n"), "fetch") {
		t.Errorf("terminal /mcp add entry = %+v", e)
	}
	m.command("/mcp remove nothing-here")
	if e := m.entries[len(m.entries)-1]; e.kind != "error" {
		t.Errorf("terminal error entry = %+v", e)
	}
}

func TestMCPRestartReplacesRunningServer(t *testing.T) {
	exe, _ := os.Executable()
	a := fakeAgent(t, map[string]any{"fake": map[string]any{"command": exe, "env": map[string]string{fakeMCPEnv: "1"}}})
	a.mcp.wait(20 * time.Second)
	for i := 0; i < 3; i++ {
		runMCP(t, a, "/mcp test fake")
	}
	count := 0
	for _, s := range a.mcp.status() {
		if s.name == "fake" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("fake appears %d times after restarts", count)
	}
	if res := a.mcp.run(context.Background(), "fake__alpha", `{"who":"again"}`); !res.ok || !strings.Contains(res.output, "hello again") {
		t.Errorf("tool call after restart: %+v", res)
	}
}

// --- error paths, dedup suffixing and config merge --------------------------

func TestMCPConfigMergeLayers(t *testing.T) {
	cases := []struct {
		name        string
		user        string // "" = no file
		project     string // "" = no file
		wantURLs    map[string]string
		wantOff     map[string]bool
		wantProblem string // substring of the single expected problem, "" = none
	}{
		{
			name:     "project overrides same-named user server wholesale",
			user:     `{"mcpServers":{"a":{"url":"http://user/a","headers":{"X":"1"}}}}`,
			project:  `{"mcpServers":{"a":{"url":"http://proj/a"}}}`,
			wantURLs: map[string]string{"a": "http://proj/a"},
		},
		{
			name:     "distinct names from both layers are unioned",
			user:     `{"mcpServers":{"a":{"url":"http://u"}}}`,
			project:  `{"mcpServers":{"b":{"url":"http://p"}}}`,
			wantURLs: map[string]string{"a": "http://u", "b": "http://p"},
		},
		{
			name:     "project can disable a user server",
			user:     `{"mcpServers":{"a":{"url":"http://u"}}}`,
			project:  `{"mcpServers":{"a":{"url":"http://u","disabled":true}}}`,
			wantURLs: map[string]string{"a": "http://u"},
			wantOff:  map[string]bool{"a": true},
		},
		{
			name:        "broken project file is reported and user config survives",
			user:        `{"mcpServers":{"a":{"url":"http://u"}}}`,
			project:     `{"mcpServers":`,
			wantURLs:    map[string]string{"a": "http://u"},
			wantProblem: filepath.Join(".prism", "mcp.json"),
		},
		{
			name:        "broken user file is reported and project config survives",
			user:        `[1,2`,
			project:     `{"mcpServers":{"b":{"url":"http://p"}}}`,
			wantURLs:    map[string]string{"b": "http://p"},
			wantProblem: "mcp.json",
		},
		{
			name:     "no files at all gives an empty map",
			wantURLs: map[string]string{},
		},
		{
			name:     "file without mcpServers key adds nothing",
			user:     `{"other":1}`,
			wantURLs: map[string]string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			dir := t.TempDir()
			if tc.user != "" {
				if err := os.MkdirAll(configDir(), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(configDir(), "mcp.json"), []byte(tc.user), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.project != "" {
				if err := os.MkdirAll(filepath.Join(dir, ".prism"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, ".prism", "mcp.json"), []byte(tc.project), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, problems := loadMCPConfig(dir)
			if len(got) != len(tc.wantURLs) {
				t.Errorf("servers = %v, want urls %v", got, tc.wantURLs)
			}
			for name, url := range tc.wantURLs {
				cfg, ok := got[name]
				if !ok {
					t.Errorf("server %q missing from %v", name, got)
					continue
				}
				if cfg.URL != url {
					t.Errorf("server %q url = %q, want %q", name, cfg.URL, url)
				}
				if cfg.Off != tc.wantOff[name] {
					t.Errorf("server %q disabled = %v, want %v", name, cfg.Off, tc.wantOff[name])
				}
			}
			if tc.name == "project overrides same-named user server wholesale" && len(got["a"].Headers) != 0 {
				t.Errorf("user headers leaked through the override: %v", got["a"].Headers)
			}
			switch {
			case tc.wantProblem == "" && len(problems) != 0:
				t.Errorf("unexpected problems: %v", problems)
			case tc.wantProblem != "" && (len(problems) != 1 || !strings.Contains(problems[0], tc.wantProblem)):
				t.Errorf("problems = %v, want one containing %q", problems, tc.wantProblem)
			}
		})
	}
}

func TestMCPDisabledServerNeverStarts(t *testing.T) {
	dir := writeMCPConfig(t, map[string]any{
		"off": map[string]any{"command": "definitely-not-a-real-binary-xyz", "disabled": true},
	})
	hub := startMCP(dir)
	defer hub.close()
	sts := hub.status()
	if len(sts) != 1 || sts[0].state != "off" || sts[0].err != "" {
		t.Errorf("disabled server status = %+v, want state off and no error", sts)
	}
}

func TestMCPDedupSuffixing(t *testing.T) {
	wiresOf := func(servers []*mcpServer) [][]string {
		var out [][]string
		for _, s := range servers {
			var w []string
			for _, tl := range s.tools {
				w = append(w, tl.wire)
			}
			out = append(out, w)
		}
		return out
	}
	mk := func(name string, wires ...string) *mcpServer {
		s := &mcpServer{name: name}
		for _, w := range wires {
			s.tools = append(s.tools, mcpTool{server: name, wire: w})
		}
		return s
	}
	long := strings.Repeat("a", 24) + "__" + strings.Repeat("b", 36) // 62 bytes
	cases := []struct {
		name    string
		servers []*mcpServer
		want    [][]string
	}{
		{"no collisions is a no-op", []*mcpServer{mk("s", "s__a", "s__b"), mk("t", "t__a")}, [][]string{{"s__a", "s__b"}, {"t__a"}}},
		{"two servers: later one gets _2", []*mcpServer{mk("s", "x__a"), mk("t", "x__a")}, [][]string{{"x__a"}, {"x__a_2"}}},
		{"three-way collision gets _2 then _3", []*mcpServer{mk("s", "x__a"), mk("t", "x__a"), mk("u", "x__a")}, [][]string{{"x__a"}, {"x__a_2"}, {"x__a_3"}}},
		{"suffix skips a name already taken elsewhere", []*mcpServer{mk("s", "x__a", "x__a_2"), mk("t", "x__a")}, [][]string{{"x__a", "x__a_2"}, {"x__a_3"}}},
		{"duplicate inside one server", []*mcpServer{mk("s", "x__a", "x__a")}, [][]string{{"x__a", "x__a_2"}}},
		{"long wire is cut to 60 bytes before suffixing", []*mcpServer{mk("s", long), mk("t", long)}, [][]string{{long}, {long[:60] + "_2"}}},
		{"empty hub", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dedupWireNames(tc.servers)
			got := wiresOf(tc.servers)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("wires = %v, want %v", got, tc.want)
			}
			// Idempotent: a second pass must not rename anything further.
			dedupWireNames(tc.servers)
			if again := wiresOf(tc.servers); fmt.Sprint(again) != fmt.Sprint(tc.want) {
				t.Errorf("second pass changed wires: %v, want %v", again, tc.want)
			}
		})
	}
}

func TestMCPDialErrors(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PRISM_TEST_UNSET_KEY", "")
	cases := []struct {
		name string
		cfg  mcpServerConfig
		want string
	}{
		{"neither command nor url", mcpServerConfig{}, `needs a "command" or a "url"`},
		{"command not on PATH", mcpServerConfig{Command: "prism-no-such-binary-xyz"}, "isn't installed"},
		{"command path that cannot execute", mcpServerConfig{Command: t.TempDir()}, "can't start"},
		{"missing key in env", mcpServerConfig{Command: "x", Env: map[string]string{"K": "${PRISM_TEST_UNSET_KEY}"}}, "PRISM_TEST_UNSET_KEY"},
		{"missing key in headers", mcpServerConfig{URL: "http://127.0.0.1:1", Headers: map[string]string{"Authorization": "Bearer ${PRISM_TEST_UNSET_KEY}"}}, "/mcp key PRISM_TEST_UNSET_KEY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := dialMCP(tc.cfg)
			if err == nil {
				conn.close()
				t.Fatalf("dialMCP succeeded, want error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestMCPStdioErrorPaths(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs /bin/sh")
	}
	sh := func(script string) *mcpStdio {
		t.Helper()
		c, err := dialStdio(mcpServerConfig{Command: "/bin/sh", Args: []string{"-c", script}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(c.close)
		return c
	}
	cases := []struct {
		name    string
		script  string
		timeout time.Duration
		want    string // substring of the error
		wantRes string // expected result when want == ""
		ctxErr  error
	}{
		{
			name:    "server answers with a JSON-RPC error",
			script:  `read l; printf '%s\n' '{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"nope"}}'; sleep 5`,
			timeout: 5 * time.Second,
			want:    "nope",
		},
		{
			name:    "server dies with stderr: last line is reported",
			script:  `read l; echo first >&2; echo kaboom >&2; exit 3`,
			timeout: 5 * time.Second,
			want:    "the tool stopped",
		},
		{
			name:    "server dies silently",
			script:  `read l; exit 0`,
			timeout: 5 * time.Second,
			want:    "the tool stopped",
		},
		{
			name:    "garbage and unrelated ids are skipped, matching reply wins",
			script:  `read l; printf '%s\n' 'not json' '{"jsonrpc":"2.0","id":99,"result":"other"}' '{"jsonrpc":"2.0","method":"notifications/x"}' '{"jsonrpc":"2.0","id":1,"result":{"ok":true}}'; sleep 5`,
			timeout: 5 * time.Second,
			wantRes: `{"ok":true}`,
		},
		{
			name:    "reply delivered just before exit is not lost",
			script:  `read l; printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":"late"}'`,
			timeout: 5 * time.Second,
			wantRes: `"late"`,
		},
		{
			name:    "silent server hits the context deadline",
			script:  `sleep 5`,
			timeout: 150 * time.Millisecond,
			ctxErr:  context.DeadlineExceeded,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := sh(tc.script)
			ctx, cancel := context.WithTimeout(context.Background(), tc.timeout)
			defer cancel()
			res, err := c.call(ctx, "ping", nil)
			switch {
			case tc.ctxErr != nil:
				if err != tc.ctxErr {
					t.Fatalf("err = %v, want %v", err, tc.ctxErr)
				}
				c.mu.Lock()
				left := len(c.waiting)
				c.mu.Unlock()
				if left != 0 {
					t.Errorf("%d waiter(s) leaked after cancel", left)
				}
			case tc.want != "":
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v, want it to contain %q (result %s)", err, tc.want, res)
				}
				if tc.name == "server dies with stderr: last line is reported" && strings.Contains(err.Error(), "first") {
					t.Errorf("err %q should only carry the last stderr line", err)
				}
			default:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if string(res) != tc.wantRes {
					t.Errorf("result = %s, want %s", res, tc.wantRes)
				}
			}
		})
	}
}

func TestMCPStdioCallAfterDeath(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs /bin/sh")
	}
	c, err := dialStdio(mcpServerConfig{Command: "/bin/sh", Args: []string{"-c", "echo gone >&2; exit 0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		t.Fatal("reader never noticed the server exit")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = c.call(ctx, "tools/list", nil)
	if err == nil {
		t.Fatal("call on a dead server succeeded")
	}
	if !strings.Contains(err.Error(), "gone") && !strings.Contains(err.Error(), "the tool stopped") && !strings.Contains(err.Error(), "broken pipe") {
		t.Errorf("err = %q, want a stopped/stderr message", err)
	}
}

func TestMCPHTTPErrorPaths(t *testing.T) {
	const sseJSON = `{"jsonrpc":"2.0","id":1,"result":{"v":1}}`
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    string // error substring
		wantRes string
	}{
		{
			name:    "HTTP 500 carries status and body",
			handler: func(w http.ResponseWriter, r *http.Request) { http.Error(w, "kaput", 500) },
			want:    "HTTP 500 kaput",
		},
		{
			name:    "HTTP 401 is reported",
			handler: func(w http.ResponseWriter, r *http.Request) { http.Error(w, "bad key", 401) },
			want:    "HTTP 401 bad key",
		},
		{
			name: "long error body is clipped to 300 bytes",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(502)
				io.WriteString(w, strings.Repeat("z", 1000))
			},
			want: "HTTP 502 " + strings.Repeat("z", 300),
		},
		{
			name: "invalid JSON body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("content-type", "application/json")
				io.WriteString(w, "<html>oops")
			},
			want: "invalid character",
		},
		{
			name: "empty JSON body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("content-type", "application/json")
			},
			want: "EOF",
		},
		{
			name: "JSON-RPC error object becomes the error text",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("content-type", "application/json")
				io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-1,"message":"tool blew up"}}`)
			},
			want: "tool blew up",
		},
		{
			name: "event stream with no matching id",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("content-type", "text/event-stream")
				io.WriteString(w, "data: {\"jsonrpc\":\"2.0\",\"id\":77,\"result\":1}\n\n: comment\n\n")
			},
			want: "no reply in the event stream",
		},
		{
			name: "event stream with only garbage data",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("content-type", "text/event-stream")
				io.WriteString(w, "data: not json\n\n")
			},
			want: "no reply in the event stream",
		},
		{
			name: "empty event stream",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("content-type", "text/event-stream")
			},
			want: "no reply in the event stream",
		},
		{
			name: "event stream carrying a JSON-RPC error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("content-type", "text/event-stream")
				io.WriteString(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"error\":{\"code\":1,\"message\":\"sse bad\"}}\n\n")
			},
			want: "sse bad",
		},
		{
			name: "event stream: notification first, CRLF, reply without trailing blank line",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("content-type", "text/event-stream")
				io.WriteString(w, "event: message\r\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\r\n\r\ndata: "+sseJSON+"\r\n")
			},
			wantRes: `{"v":1}`,
		},
		{
			name: "event stream: one event split across data lines",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("content-type", "text/event-stream")
				io.WriteString(w, "data:{\"jsonrpc\":\"2.0\",\ndata: \"id\":1,\ndata: \"result\":{\"v\":2}}\n\n")
			},
			wantRes: `{"v":2}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			c := &mcpHTTP{url: srv.URL}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			res, err := c.call(ctx, "tools/list", nil)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(res) != tc.wantRes {
				t.Errorf("result = %s, want %s", res, tc.wantRes)
			}
		})
	}
}

func TestMCPHTTPTransportFailures(t *testing.T) {
	t.Run("connection refused", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		c := &mcpHTTP{url: url}
		if _, err := c.call(context.Background(), "initialize", nil); err == nil {
			t.Fatal("expected a dial error against a closed server")
		}
		c.notify("notifications/initialized", nil) // must not panic or hang
	})
	t.Run("malformed url", func(t *testing.T) {
		c := &mcpHTTP{url: "http://bad host/\x7f"}
		if _, err := c.call(context.Background(), "initialize", nil); err == nil {
			t.Fatal("expected a request-building error")
		}
	})
	t.Run("cancelled context aborts a hung server", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		defer srv.Close()
		defer close(release)
		c := &mcpHTTP{url: srv.URL}
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := c.call(ctx, "tools/call", nil)
		if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
			t.Fatalf("err = %v, want deadline exceeded", err)
		}
		if time.Since(start) > 3*time.Second {
			t.Errorf("call took %v, context was ignored", time.Since(start))
		}
	})
	t.Run("session id is remembered and configured headers are expanded", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv("PRISM_TEST_HDR_KEY", "sekret")
		var mu sync.Mutex
		var sessions, auths []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			sessions = append(sessions, r.Header.Get("mcp-session-id"))
			auths = append(auths, r.Header.Get("authorization"))
			mu.Unlock()
			w.Header().Set("mcp-session-id", "S1")
			w.Header().Set("content-type", "application/json")
			io.WriteString(w, `{"jsonrpc":"2.0","id":`+strconv.Itoa(len(sessions))+`,"result":{}}`)
		}))
		defer srv.Close()
		c := &mcpHTTP{url: srv.URL, headers: map[string]string{"Authorization": "Bearer ${PRISM_TEST_HDR_KEY}"}}
		for i := 0; i < 2; i++ {
			if _, err := c.call(context.Background(), "ping", nil); err != nil {
				t.Fatal(err)
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if fmt.Sprint(sessions) != "[ S1]" {
			t.Errorf("session headers = %q, want first empty then S1", sessions)
		}
		for _, a := range auths {
			if a != "Bearer sekret" {
				t.Errorf("authorization = %q, want expanded key", a)
			}
		}
	})
}

func TestMCPHubReportsTransportErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", 503)
	}))
	defer srv.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	dir := writeMCPConfig(t, map[string]any{
		"http-503":    map[string]any{"url": srv.URL},
		"http-down":   map[string]any{"url": closedURL},
		"no-binary":   map[string]any{"command": "prism-no-such-binary-xyz"},
		"empty-entry": map[string]any{},
	})
	hub := startMCP(dir)
	defer hub.close()
	hub.wait(20 * time.Second)
	want := map[string]string{
		"http-503":    "HTTP 503 nope",
		"http-down":   "",
		"no-binary":   "isn't installed",
		"empty-entry": `needs a "command" or a "url"`,
	}
	seen := 0
	for _, s := range hub.status() {
		sub, ok := want[s.name]
		if !ok {
			continue
		}
		seen++
		if s.state != "error" || s.err == "" || !strings.Contains(s.err, sub) {
			t.Errorf("%s: state=%q err=%q, want error containing %q", s.name, s.state, s.err, sub)
		}
		if len(s.tools) != 0 {
			t.Errorf("%s: failed server exposes tools %v", s.name, s.tools)
		}
	}
	if seen != len(want) {
		t.Errorf("saw %d of %d servers in status: %+v", seen, len(want), hub.status())
	}
	if names := specNames(hub); len(names) != 0 {
		t.Errorf("failed servers leaked tool specs: %v", names)
	}
}
