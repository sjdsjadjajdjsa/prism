package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testEnv isolates the config dir (HOME / XDG_CONFIG_HOME) so tests do not
// touch the real ~/.config/prism store, sessions, or usage file.
func testEnv(t *testing.T) string {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("HOME", cfg)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("PRISM_MEMORY", "off")
	return cfg
}

// fakeReq records what the agent posted to the fake router.
type fakeReq struct {
	messages []Message
	avoid    []string
}

// fakeRouter serves scripted SSE (or plain 503) replies. Scripts must set
// the x-free-route header via routeHeader before writing any body bytes,
// just like the real router: agent.go reads that header after the stream
// has ended.
type fakeRouter struct {
	mu      *sync.Mutex
	replies []func(w http.ResponseWriter) string // returns the x-free-route
	seen    []fakeReq
}

// routeHeader sets the x-free-route header and returns its value, so a
// script can say "return routeHeader(w, \"slow-route\")".
func routeHeader(w http.ResponseWriter, route string) string {
	w.Header().Set("x-free-route", route)
	return route
}

func (f *fakeRouter) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []Message `json:"messages"`
			Avoid    []string  `json:"prism_avoid"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		f.mu.Lock()
		n := len(f.seen)
		f.seen = append(f.seen, fakeReq{messages: body.Messages, avoid: body.Avoid})
		script := f.replies[n]
		f.mu.Unlock()
		script(w)
	}
}

func sseText(lines ...string) func(http.ResponseWriter) string {
	return func(w http.ResponseWriter) string {
		w.Header().Set("content-type", "text/event-stream")
		for _, line := range lines {
			fmt.Fprintln(w, line)
		}
		return ""
	}
}

func delta(payload string) string {
	return "data: {\"choices\":[{\"delta\":" + payload + "}]}"
}

const sseDone = "data: [DONE]"

func newTestAgent(t *testing.T, api, dir string) *Agent {
	t.Helper()
	testEnv(t)
	return newAgent(api, dir, dir)
}

// collect runs the agent and gathers every emitted event.
func collect(a *Agent, prompt string) ([]any, error) {
	var mu sync.Mutex
	var events []any
	done := make(chan struct{})
	go func() {
		a.Run(context.Background(), prompt, func(ev any) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
			if _, ok := ev.(evDone); ok {
				close(done)
			}
		})
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		panic("agent run did not finish")
	}
	mu.Lock()
	defer mu.Unlock()
	last, ok := events[len(events)-1].(evDone)
	if !ok {
		panic("last event was not evDone")
	}
	return events, last.err
}

func hasEvent(events []any, match func(any) bool) bool {
	for _, ev := range events {
		if match(ev) {
			return true
		}
	}
	return false
}

func TestStreamTextAndReasoning(t *testing.T) {
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
		sseText(
			delta(`{"reasoning_content":"let me think"}`),
			delta(`{"content":"Hel"}`),
			delta(`{"content":"lo there"}`),
			sseDone,
		),
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a := newTestAgent(t, srv.URL, t.TempDir())

	events, err := collect(a, "say hi")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !hasEvent(events, func(ev any) bool { _, ok := ev.(evThinking); return ok }) {
		t.Error("no evThinking event emitted")
	}
	if !hasEvent(events, func(ev any) bool {
		r, ok := ev.(evReasoning)
		return ok && r.text == "let me think"
	}) {
		t.Error("no evReasoning event with the reasoning text")
	}
	text := ""
	for _, ev := range events {
		if e, ok := ev.(evText); ok {
			text += e.text
		}
	}
	if text != "Hello there" {
		t.Errorf("streamed text = %q, want %q", text, "Hello there")
	}
	last := a.messages[len(a.messages)-1]
	if last.Role != "assistant" || last.Content != "Hello there" {
		t.Errorf("last message = %+v", last)
	}
}

func TestToolCallsSplitAcrossChunks(t *testing.T) {
	argsJSON := func(v string) string {
		out, _ := json.Marshal(v)
		return string(out)
	}
	cases := []struct {
		name  string
		lines []string
	}{
		{
			name: "with index",
			lines: []string{
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"read\",\"arguments\":" + argsJSON(`{"pa`) + "}}]}}]}",
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":" + argsJSON(`th":"a.go"}`) + "}}]}}]}",
				"data: {\"choices\":[{\"finish_reason\":\"tool_calls\",\"delta\":{}}]}",
				sseDone,
			},
		},
		{
			name: "without index",
			lines: []string{
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_1\",\"function\":{\"name\":\"read\",\"arguments\":" + argsJSON(`{"pa`) + "}}]}}]}",
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":" + argsJSON(`th":"a.go"}`) + "}}]}}]}",
				"data: {\"choices\":[{\"finish_reason\":\"tool_calls\",\"delta\":{}}]}",
				sseDone,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
				sseText(tc.lines...),
				sseText(delta(`{"content":"done"}`), sseDone),
			}}
			srv := httptest.NewServer(fake.handler())
			defer srv.Close()
			a := newTestAgent(t, srv.URL, dir)

			events, err := collect(a, "read a.go")
			if err != nil {
				t.Fatalf("run failed: %v", err)
			}
			if !hasEvent(events, func(ev any) bool {
				s, ok := ev.(evToolStart)
				return ok && s.name == "read" && s.id == "call_1"
			}) {
				t.Error("no evToolStart for the reassembled read call")
			}
			if !hasEvent(events, func(ev any) bool {
				d, ok := ev.(evToolDone)
				return ok && d.result.ok && strings.Contains(d.result.output, "package x")
			}) {
				t.Error("read tool result missing the file content")
			}
		})
	}
}

func TestCutOffReplyRetriedWithAvoid(t *testing.T) {
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
		// First reply: streaming stops without [DONE] or finish_reason.
		func(w http.ResponseWriter) string {
			w.Header().Set("content-type", "text/event-stream")
			route := routeHeader(w, "slow-route")
			fmt.Fprintln(w, delta(`{"content":"half a rep`))
			return route
		},
		sseText(delta(`{"content":"full reply"}`), sseDone),
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a := newTestAgent(t, srv.URL, t.TempDir())

	events, err := collect(a, "hello")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.seen) != 2 {
		t.Fatalf("router saw %d requests, want 2", len(fake.seen))
	}
	if len(fake.seen[0].avoid) != 0 {
		t.Errorf("first request avoid = %v, want none", fake.seen[0].avoid)
	}
	// This asserts the intended behaviour: the retry must carry
	// prism_avoid=["slow-route"], the x-free-route of the cut-off reply.
	if len(fake.seen[1].avoid) != 1 || fake.seen[1].avoid[0] != "slow-route" {
		t.Errorf("retry avoid = %v, want [slow-route]", fake.seen[1].avoid)
	}
	if !hasEvent(events, func(ev any) bool {
		n, ok := ev.(evNote)
		return ok && strings.Contains(n.text, "trying another")
	}) {
		t.Error("no retry note emitted")
	}
	text := ""
	for _, ev := range events {
		if e, ok := ev.(evText); ok {
			text += e.text
		}
	}
	if !strings.Contains(text, "full reply") {
		t.Errorf("final text = %q, want it to contain %q", text, "full reply")
	}
}

func TestRateLimitWaitsAndRetries(t *testing.T) {
	saved := busyWaits
	busyWaits = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}
	defer func() { busyWaits = saved }()
	plain429 := func(w http.ResponseWriter) string {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"status":429,"title":"Too Many Requests"}`)
		return ""
	}
	failures := func(statuses string) func(http.ResponseWriter) string {
		return func(w http.ResponseWriter) string {
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"error":{"message":"Every free AI route failed.","failures":[`+statuses+`]}}`)
			return ""
		}
	}
	allBusy := failures(`{"route":"a","status":429},{"route":"b","status":429}`)
	mixed := failures(`{"route":"a","status":429},{"route":"b","status":500}`)
	ok := sseText(delta(`{"content":"done"}`), sseDone)
	cases := []struct {
		name     string
		replies  []func(http.ResponseWriter) string
		requests int
		wantErr  string
		notes    int
	}{
		{"429 then success", []func(http.ResponseWriter) string{plain429, ok}, 2, "", 1},
		{"every route 429 then success", []func(http.ResponseWriter) string{allBusy, allBusy, ok}, 3, "", 2},
		{"429 on every try gives up", []func(http.ResponseWriter) string{plain429, plain429, plain429}, 3, "router error 429", 2},
		{"a real failure is not waited on", []func(http.ResponseWriter) string{mixed}, 1, "Every free AI route failed.", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeRouter{mu: &sync.Mutex{}, replies: c.replies}
			srv := httptest.NewServer(fake.handler())
			defer srv.Close()
			a := newTestAgent(t, srv.URL, t.TempDir())
			events, err := collect(a, "hello")
			if c.wantErr == "" && err != nil {
				t.Fatalf("run failed: %v", err)
			}
			if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
				t.Fatalf("err = %v, want it to contain %q", err, c.wantErr)
			}
			fake.mu.Lock()
			seen := len(fake.seen)
			fake.mu.Unlock()
			if seen != c.requests {
				t.Errorf("router saw %d requests, want %d", seen, c.requests)
			}
			notes := 0
			for _, ev := range events {
				if n, ok := ev.(evNote); ok && strings.Contains(n.text, "busy") {
					notes++
				}
			}
			if notes != c.notes {
				t.Errorf("%d busy notes, want %d", notes, c.notes)
			}
		})
	}
}

func TestPreferAndCachedTokens(t *testing.T) {
	for _, oldRouter := range []bool{false, true} {
		t.Run(fmt.Sprintf("old router %v", oldRouter), func(t *testing.T) {
			var mu sync.Mutex
			var bodies []map[string]any
			step := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				mu.Lock()
				bodies = append(bodies, body)
				step++
				n := step
				mu.Unlock()
				w.Header().Set("content-type", "text/event-stream")
				w.Header().Set("x-free-route", fmt.Sprintf("route-%d", n))
				if !oldRouter {
					w.Header().Set("x-prism-prefer", "1")
				}
				if n == 1 {
					fmt.Fprintln(w, delta(`{"tool_calls":[{"index":0,"id":"c1","function":{"name":"list","arguments":"{}"}}]}`))
				} else {
					fmt.Fprintln(w, delta(`{"content":"done"}`))
				}
				fmt.Fprintf(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1000,\"total_tokens\":1010,\"prompt_tokens_details\":{\"cached_tokens\":%d}}}\n", 100*n)
				fmt.Fprintln(w, sseDone)
			}))
			defer srv.Close()
			a := newTestAgent(t, srv.URL, t.TempDir())
			if _, err := collect(a, "please look at the files in this folder and tell me which ones matter, in detail, thanks"); err != nil {
				t.Fatal(err)
			}
			if _, ok := bodies[0]["prism_prefer"]; ok {
				t.Errorf("first request has prism_prefer: %v", bodies[0]["prism_prefer"])
			}
			got, sent := bodies[1]["prism_prefer"]
			switch {
			case oldRouter && sent:
				t.Errorf("sent prism_prefer %v to a router that did not announce it", got)
			case !oldRouter && got != "route-1":
				t.Errorf("second request prism_prefer = %v, want route-1", got)
			}
			if a.cached != 300 || a.sent != 2000 {
				t.Errorf("cached = %d, sent = %d; want 300 and 2000", a.cached, a.sent)
			}
		})
	}
}

func TestLightFirstStepDoesNotSetPrefer(t *testing.T) {
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
		func(w http.ResponseWriter) string {
			route := routeHeader(w, "small-route")
			w.Header().Set("content-type", "text/event-stream")
			fmt.Fprintln(w, delta(`{"content":"hi"}`))
			fmt.Fprintln(w, sseDone)
			return route
		},
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a := newTestAgent(t, srv.URL, t.TempDir())
	if _, err := collect(a, "hi"); err != nil {
		t.Fatal(err)
	}
	if a.prefer != "" {
		t.Errorf("prefer = %q after a quick first step; the big model's cache is what to keep", a.prefer)
	}
}

func TestPruneWaitsUntilQuarterWindow(t *testing.T) {
	long := strings.Repeat("x", pruneOver+100)
	msgs := []Message{{Role: "system", Content: "sys"}}
	for range 20 + keepRecent {
		msgs = append(msgs, Message{Role: "tool", Content: long})
	}
	a := &Agent{messages: append([]Message(nil), msgs...), window: contextLimit}
	a.prune()
	for i, m := range a.messages {
		if m.Content != msgs[i].Content {
			t.Fatalf("message %d trimmed while the chat is far below a quarter of the window", i)
		}
	}
	a.window = 2 * estimate(msgs)
	a.prune()
	if !strings.Contains(a.messages[1].Content, prunedNote) {
		t.Error("old tool output not trimmed past a quarter of the window")
	}
}

func TestReadStreamCutOff(t *testing.T) {
	a := newTestAgent(t, "http://unused", t.TempDir())
	_, err := a.readStream(strings.NewReader(delta(`{"content":"x"}`)+"\n"), func(any) {})
	if !errors.Is(err, errCutOff) {
		t.Errorf("err = %v, want errCutOff", err)
	}
}

func TestRouterError503(t *testing.T) {
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
		func(w http.ResponseWriter) string {
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"error":{"message":"all routes failed","failures":[{"route":"route-a","status":429},{"route":"route-b","status":500}]}}`)
			return ""
		},
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a := newTestAgent(t, srv.URL, t.TempDir())

	_, err := collect(a, "hello")
	var re *routerError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v (%T), want a *routerError", err, err)
	}
	if re.message != "all routes failed" {
		t.Errorf("message = %q", re.message)
	}
	if len(re.failures) != 2 || re.failures[0].Route != "route-a" || re.failures[0].Status != 429 || re.failures[1].Status != 500 {
		t.Errorf("failures = %+v", re.failures)
	}
}

func TestFullLoopWriteThenAnswer(t *testing.T) {
	dir := t.TempDir()
	writeCall := `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"write","arguments":"{\"path\":\"out/hello.txt\",\"content\":\"hi prism\"}"}}]}}]}`
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
		sseText("data: "+writeCall, "data: {\"choices\":[{\"finish_reason\":\"tool_calls\",\"delta\":{}}]}", sseDone),
		sseText(delta(`{"content":"I wrote hello.txt for you."}`), sseDone),
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a := newTestAgent(t, srv.URL, dir)

	events, err := collect(a, "create a file out/hello.txt that says hi prism")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	data, readErr := os.ReadFile(filepath.Join(dir, "out", "hello.txt"))
	if readErr != nil {
		t.Fatalf("the write tool did not create the file: %v", readErr)
	}
	if string(data) != "hi prism" {
		t.Errorf("file content = %q", data)
	}
	// History: system, user, assistant(tool call), tool, assistant(answer).
	if len(a.messages) != 5 {
		t.Fatalf("history has %d messages, want 5: %+v", len(a.messages), a.messages)
	}
	if a.messages[2].Role != "assistant" || len(a.messages[2].ToolCalls) != 1 || a.messages[2].ToolCalls[0].Function.Name != "write" {
		t.Errorf("assistant tool-call message wrong: %+v", a.messages[2])
	}
	if a.messages[3].Role != "tool" || a.messages[3].ToolCallID != "call_1" {
		t.Errorf("tool message wrong: %+v", a.messages[3])
	}
	if a.messages[4].Role != "assistant" || !strings.Contains(a.messages[4].Content, "wrote hello.txt") {
		t.Errorf("final answer wrong: %+v", a.messages[4])
	}
	if !hasEvent(events, func(ev any) bool {
		s, ok := ev.(evToolStart)
		return ok && s.name == "write" && strings.Contains(s.arg, "out/hello.txt")
	}) {
		t.Error("no evToolStart for write")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.seen) != 2 {
		t.Fatalf("router saw %d requests, want 2", len(fake.seen))
	}
}

func TestReadStreamTable(t *testing.T) {
	type call struct{ id, name, args string }
	finish := `data: {"choices":[{"finish_reason":"stop","delta":{}}]}`
	toolPart := func(index int, id, name, args string) string {
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
			"tool_calls": []any{map[string]any{"index": index, "id": id, "function": map[string]any{"name": name, "arguments": args}}},
		}}}})
		return "data: " + string(raw)
	}
	cases := []struct {
		name      string
		lines     []string
		wantErr   string
		cutOff    bool
		content   string
		calls     []call
		texts     []string
		thinking  int
		reasoning string
		tokens    int
		prompt    int
	}{
		{
			name:    "finish_reason without DONE ends the stream",
			lines:   []string{delta(`{"content":"ok"}`), finish},
			content: "ok",
			texts:   []string{"ok"},
		},
		{
			name:    "non-data lines, comments and bad JSON are skipped",
			lines:   []string{": keep-alive", "event: message", "", "data: {not json", delta(`{"content":"a"}`), "data:" + `{"choices":[{"delta":{"content":"b"}}]}`, sseDone},
			content: "ab",
			texts:   []string{"a", "b"},
		},
		{
			name:    "error chunk aborts with its message",
			lines:   []string{delta(`{"content":"x"}`), `data: {"error":{"message":"upstream exploded"}}`, sseDone},
			wantErr: "upstream exploded",
		},
		{
			name:    "error chunk with empty message is ignored",
			lines:   []string{`data: {"error":{"message":""}}`, delta(`{"content":"fine"}`), sseDone},
			content: "fine",
			texts:   []string{"fine"},
		},
		{
			name:   "empty stream is cut off",
			lines:  []string{},
			cutOff: true,
		},
		{
			name:    "leading whitespace content is kept but not emitted",
			lines:   []string{delta(`{"content":"\n"}`), delta(`{"content":"hi"}`), sseDone},
			content: "\nhi",
			texts:   []string{"hi"},
		},
		{
			name:      "reasoning and reasoning_content emit thinking once",
			lines:     []string{delta(`{"reasoning":"a"}`), delta(`{"reasoning_content":"b"}`), delta(`{"content":"c"}`), sseDone},
			content:   "c",
			texts:     []string{"c"},
			thinking:  1,
			reasoning: "ab",
		},
		{
			name:    "usage with total tokens",
			lines:   []string{delta(`{"content":"u"}`), `data: {"choices":[],"usage":{"prompt_tokens":10,"total_tokens":15}}`, sseDone},
			content: "u",
			texts:   []string{"u"},
			tokens:  15,
			prompt:  10,
		},
		{
			name:    "usage with only prompt tokens",
			lines:   []string{`data: {"choices":[],"usage":{"prompt_tokens":7}}`, sseDone},
			content: "",
			tokens:  7,
			prompt:  7,
		},
		{
			name: "parallel tool calls keep their order",
			lines: []string{
				toolPart(0, "c0", "read", `{"path":`),
				toolPart(1, "c1", "list", `{"path":"."}`),
				toolPart(0, "", "", `"a.go"}`),
				sseDone,
			},
			calls: []call{{"c0", "read", `{"path":"a.go"}`}, {"c1", "list", `{"path":"."}`}},
		},
		{
			name: "reused index with a new id starts a new call",
			lines: []string{
				toolPart(0, "c0", "read", `{"path":"a"}`),
				toolPart(0, "c1", "read", `{"path":"b"}`),
				sseDone,
			},
			calls: []call{{"c0", "read", `{"path":"a"}`}, {"c1", "read", `{"path":"b"}`}},
		},
		{
			name: "reused index with the same id appends",
			lines: []string{
				toolPart(0, "c0", "bash", `{"command":`),
				toolPart(0, "c0", "ignored", `"ls"}`),
				sseDone,
			},
			calls: []call{{"c0", "bash", `{"command":"ls"}`}},
		},
		{
			name: "missing id in first part is filled by a later part",
			lines: []string{
				toolPart(0, "", "grep", `{"pattern":"x"}`),
				toolPart(0, "late", "", ``),
				sseDone,
			},
			calls: []call{{"late", "grep", `{"pattern":"x"}`}},
		},
		{
			name: "text and tool call together",
			lines: []string{
				delta(`{"content":"checking"}`),
				toolPart(0, "c0", "list", `{}`),
				sseDone,
			},
			content: "checking",
			texts:   []string{"checking"},
			calls:   []call{{"c0", "list", `{}`}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Agent{}
			var texts []string
			thinking := 0
			reasoning := ""
			body := strings.Join(tc.lines, "\n")
			if body != "" {
				body += "\n"
			}
			reply, err := a.readStream(strings.NewReader(body), func(ev any) {
				switch e := ev.(type) {
				case evText:
					texts = append(texts, e.text)
				case evThinking:
					thinking++
				case evReasoning:
					reasoning += e.text
				}
			})
			if tc.cutOff {
				if !errors.Is(err, errCutOff) {
					t.Fatalf("err = %v, want errCutOff", err)
				}
				return
			}
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if reply.Role != "assistant" || reply.Content != tc.content {
				t.Errorf("reply = %+v, want content %q", reply, tc.content)
			}
			if strings.Join(texts, "|") != strings.Join(tc.texts, "|") {
				t.Errorf("evText = %q, want %q", texts, tc.texts)
			}
			if thinking != tc.thinking || reasoning != tc.reasoning {
				t.Errorf("thinking=%d reasoning=%q, want %d %q", thinking, reasoning, tc.thinking, tc.reasoning)
			}
			if a.tokens != tc.tokens || a.lastPrompt != tc.prompt {
				t.Errorf("tokens=%d lastPrompt=%d, want %d %d", a.tokens, a.lastPrompt, tc.tokens, tc.prompt)
			}
			if len(reply.ToolCalls) != len(tc.calls) {
				t.Fatalf("got %d tool calls, want %d: %+v", len(reply.ToolCalls), len(tc.calls), reply.ToolCalls)
			}
			for i, want := range tc.calls {
				got := reply.ToolCalls[i]
				if got.ID != want.id || got.Function.Name != want.name || got.Function.Arguments != want.args {
					t.Errorf("call %d = %+v, want %+v", i, got, want)
				}
			}
		})
	}
}

func TestReadStreamDraftEvents(t *testing.T) {
	a := &Agent{}
	big := strings.Repeat("x", 250)
	lines := []string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"write","arguments":"{\"path\":\"dir/f.txt\","}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"content\":\"` + big + `"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"}"}}]}}]}`,
		sseDone,
	}
	var drafts []evDraft
	_, err := a.readStream(strings.NewReader(strings.Join(lines, "\n")+"\n"), func(ev any) {
		if d, ok := ev.(evDraft); ok {
			drafts = append(drafts, d)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	// First part always drafts; second grows by >=200 chars; third is too small.
	if len(drafts) != 2 {
		t.Fatalf("got %d drafts, want 2: %+v", len(drafts), drafts)
	}
	for _, d := range drafts {
		if d.name != "write" || d.arg != "dir/f.txt" {
			t.Errorf("draft = %+v", d)
		}
	}
	if drafts[1].chars <= drafts[0].chars {
		t.Errorf("draft chars did not grow: %+v", drafts)
	}
}

func TestReadWholeTable(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string
		content string
		calls   int
		tokens  int
		texts   int
	}{
		{name: "plain reply", body: `{"choices":[{"message":{"role":"x","content":"hello"}}],"usage":{"prompt_tokens":3,"total_tokens":9}}`, content: "hello", tokens: 9, texts: 1},
		{name: "tool call reply emits no text", body: `{"choices":[{"message":{"content":"","tool_calls":[{"id":"t","function":{"name":"list","arguments":"{}"}}]}}]}`, calls: 1},
		{name: "no choices", body: `{"choices":[]}`, wantErr: "the model sent an empty reply"},
		{name: "not JSON", body: `<html>`, wantErr: "unreadable reply from the router"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Agent{}
			texts := 0
			reply, err := a.readWhole(strings.NewReader(tc.body), func(ev any) {
				if _, ok := ev.(evText); ok {
					texts++
				}
			})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if reply.Role != "assistant" || reply.Content != tc.content || len(reply.ToolCalls) != tc.calls {
				t.Errorf("reply = %+v", reply)
			}
			if a.tokens != tc.tokens || texts != tc.texts {
				t.Errorf("tokens=%d texts=%d, want %d %d", a.tokens, texts, tc.tokens, tc.texts)
			}
		})
	}
}

func TestCompleteRetryPaths(t *testing.T) {
	cutOff := func(route string) func(http.ResponseWriter) string {
		return func(w http.ResponseWriter) string {
			w.Header().Set("content-type", "text/event-stream")
			routeHeader(w, route)
			fmt.Fprintln(w, delta(`{"content":"par`))
			return route
		}
	}
	plain500 := func(w http.ResponseWriter) string {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "  boom  ")
		return ""
	}
	routerFail := func(w http.ResponseWriter) string {
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"error":{"message":"no routes"}}`)
		return ""
	}
	ok := sseText(delta(`{"content":"done"}`), sseDone)
	cases := []struct {
		name      string
		replies   []func(http.ResponseWriter) string
		requests  int
		wantErr   string
		cutOff    bool
		router    bool
		notes     int
		lastAvoid []string
	}{
		{name: "success first try", replies: []func(http.ResponseWriter) string{ok}, requests: 1},
		{name: "router error is not retried", replies: []func(http.ResponseWriter) string{routerFail, ok}, requests: 1, router: true, wantErr: "no routes"},
		{name: "plain http error retried then succeeds", replies: []func(http.ResponseWriter) string{plain500, ok}, requests: 2, notes: 1},
		{name: "plain http error gives up after three tries", replies: []func(http.ResponseWriter) string{plain500, plain500, plain500, ok}, requests: 3, notes: 2, wantErr: "router error 500: boom"},
		{
			name:      "cut off three times avoids every route",
			replies:   []func(http.ResponseWriter) string{cutOff("r1"), cutOff("r2"), cutOff("r3"), ok},
			requests:  3,
			notes:     2,
			cutOff:    true,
			lastAvoid: []string{"r1", "r2"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testEnv(t)
			fake := &fakeRouter{mu: &sync.Mutex{}, replies: tc.replies}
			srv := httptest.NewServer(fake.handler())
			defer srv.Close()
			a := &Agent{api: srv.URL, window: contextLimit, messages: []Message{{Role: "user", Content: "hi"}}}
			notes := 0
			reply, err := a.completeRetry(context.Background(), func(ev any) {
				if _, ok := ev.(evNote); ok {
					notes++
				}
			})
			fake.mu.Lock()
			seen := append([]fakeReq{}, fake.seen...)
			fake.mu.Unlock()
			if len(seen) != tc.requests {
				t.Errorf("router saw %d requests, want %d", len(seen), tc.requests)
			}
			if notes != tc.notes {
				t.Errorf("retry notes = %d, want %d", notes, tc.notes)
			}
			switch {
			case tc.cutOff:
				if !errors.Is(err, errCutOff) {
					t.Errorf("err = %v, want errCutOff", err)
				}
			case tc.wantErr != "":
				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("err = %v, want %q", err, tc.wantErr)
				}
				var re *routerError
				if errors.As(err, &re) != tc.router {
					t.Errorf("routerError = %v, want %v", errors.As(err, &re), tc.router)
				}
			default:
				if err != nil || reply.Content != "done" {
					t.Errorf("reply=%+v err=%v", reply, err)
				}
			}
			if tc.lastAvoid != nil {
				got := seen[len(seen)-1].avoid
				if strings.Join(got, ",") != strings.Join(tc.lastAvoid, ",") {
					t.Errorf("last avoid = %v, want %v", got, tc.lastAvoid)
				}
			}
		})
	}
}

func TestCompleteRetryStopsOnCancel(t *testing.T) {
	testEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
		func(w http.ResponseWriter) string {
			cancel()
			w.WriteHeader(http.StatusInternalServerError)
			return ""
		},
		sseText(delta(`{"content":"never"}`), sseDone),
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a := &Agent{api: srv.URL, window: contextLimit, messages: []Message{{Role: "user", Content: "hi"}}}
	_, err := a.completeRetry(ctx, func(any) {})
	if err == nil {
		t.Fatal("want an error after cancel")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.seen) != 1 {
		t.Errorf("router saw %d requests after cancel, want 1", len(fake.seen))
	}
}

func TestCompleteUnreachableRouter(t *testing.T) {
	testEnv(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	a := &Agent{api: url, window: contextLimit}
	_, err := a.complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, func(any) {})
	if err == nil || !strings.Contains(err.Error(), "can't reach the free AI router") {
		t.Errorf("err = %v", err)
	}
}

func TestCompleteHeadersAndToolCallFixups(t *testing.T) {
	cases := []struct {
		name        string
		headers     map[string]string
		body        string
		wantRoute   string
		wantWait    time.Duration
		wantFamily  []string
		wantSource  string
		routeEvents int
	}{
		{
			name:        "x-free-route with wait",
			headers:     map[string]string{"x-free-route": "free-a", "x-free-wait": "250", "content-type": "application/json"},
			wantRoute:   "free-a",
			wantWait:    250 * time.Millisecond,
			routeEvents: 1,
		},
		{
			name:        "x-prism-route fallback and family",
			headers:     map[string]string{"x-prism-route": "prism-b", "x-prism-family": "llama", "content-type": "application/json"},
			wantRoute:   "prism-b",
			wantFamily:  []string{"llama"},
			routeEvents: 1,
		},
		{
			name:        "a plan's answer carries its source",
			headers:     map[string]string{"x-free-route": "claude/opus", "x-prism-source": "subscription", "content-type": "application/json"},
			wantRoute:   "claude/opus",
			wantSource:  "subscription",
			routeEvents: 1,
		},
		{
			name:    "no route header emits no route event",
			headers: map[string]string{"content-type": "application/json"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testEnv(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				io.WriteString(w, `{"choices":[{"message":{"content":"","tool_calls":[{"id":"","function":{"name":"list","arguments":"{}"}},{"id":"keep","function":{"name":"read","arguments":"{}"}}]}}]}`)
			}))
			defer srv.Close()
			a := &Agent{api: srv.URL, window: contextLimit, families: []string{}}
			var routes []evRoute
			reply, err := a.complete(context.Background(), []Message{{Role: "user", Content: "hello world"}}, nil, func(ev any) {
				if r, ok := ev.(evRoute); ok {
					routes = append(routes, r)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if a.lastRoute != tc.wantRoute {
				t.Errorf("lastRoute = %q, want %q", a.lastRoute, tc.wantRoute)
			}
			if len(routes) != tc.routeEvents {
				t.Fatalf("route events = %+v", routes)
			}
			if tc.routeEvents == 1 && (routes[0].route != tc.wantRoute || routes[0].wait != tc.wantWait || routes[0].source != tc.wantSource) {
				t.Errorf("route event = %+v", routes[0])
			}
			if strings.Join(a.families, ",") != strings.Join(tc.wantFamily, ",") {
				t.Errorf("families = %v, want %v", a.families, tc.wantFamily)
			}
			if len(reply.ToolCalls) != 2 {
				t.Fatalf("tool calls = %+v", reply.ToolCalls)
			}
			if !strings.HasPrefix(reply.ToolCalls[0].ID, "call_") || reply.ToolCalls[1].ID != "keep" {
				t.Errorf("ids = %q, %q", reply.ToolCalls[0].ID, reply.ToolCalls[1].ID)
			}
			for _, c := range reply.ToolCalls {
				if c.Type != "function" {
					t.Errorf("type = %q, want function", c.Type)
				}
			}
			if a.requests != 1 || a.sent == 0 || a.tokens == 0 {
				t.Errorf("requests=%d sent=%d tokens=%d", a.requests, a.sent, a.tokens)
			}
		})
	}
}

// TestRunReleasesClaimsOnTurnEnd: hub.release must be called when Run returns
// so file claims don't accumulate across turns (regression for bug 1).
func TestRunReleasesClaimsOnTurnEnd(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
		sseText(delta(`{"content":"done"}`), sseDone),
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	h := newTabHub()
	a := newTestAgent(t, srv.URL, dir)
	a.hub = h
	a.tabID = h.add("test-tab")

	// Manually plant a claim as if the agent wrote a file during the turn.
	h.claim(a.tabID, filepath.Join(dir, "claimed.go"))
	if _, ok := h.owner(filepath.Join(dir, "claimed.go")); !ok {
		t.Fatal("precondition: claim should be set before Run")
	}

	_, err := collect(a, "hello")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	// After Run returns, the claim must be gone.
	if _, ok := h.owner(filepath.Join(dir, "claimed.go")); ok {
		t.Error("claim should be released after Run returns")
	}
}

// TestInjectBriefDoesNotMutateMessages: injectBrief must not modify a.messages;
// the brief should only appear in the returned copy (regression for bug 5).
func TestInjectBriefDoesNotMutateMessages(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()
	h := newTabHub()
	a := newTestAgent(t, "http://localhost:1", dir)
	a.hub = h
	a.tabID = h.add("self")
	other := h.add("other")
	h.setStatus(other, "working", "some task")

	a.messages = []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "original user message"},
	}
	originalContent := a.messages[1].Content
	a.turnBrief = h.brief(a.tabID)

	out := a.injectBrief(a.messages)
	h.setStatus(other, "done", "a different task")
	if again := a.injectBrief(a.messages); again[1].Content != out[1].Content {
		t.Errorf("brief changed within one turn: %q then %q", out[1].Content, again[1].Content)
	}

	// The original slice must be unchanged.
	if a.messages[1].Content != originalContent {
		t.Errorf("injectBrief mutated a.messages[1].Content: got %q, want %q",
			a.messages[1].Content, originalContent)
	}
	// The returned copy must contain the brief.
	if !strings.Contains(out[1].Content, "Prism tabs") {
		t.Errorf("returned copy should contain brief, got: %q", out[1].Content)
	}
	// The returned copy must still contain the original content.
	if !strings.Contains(out[1].Content, originalContent) {
		t.Errorf("returned copy should still contain original content, got: %q", out[1].Content)
	}
}

// TestInjectBriefAloneReturnsOriginal: when there are no other tabs, injectBrief
// must return the original slice unchanged.
func TestInjectBriefAloneReturnsOriginal(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()
	h := newTabHub()
	a := newTestAgent(t, "http://localhost:1", dir)
	a.hub = h
	a.tabID = h.add("solo")

	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
	}
	a.turnBrief = h.brief(a.tabID)
	out := a.injectBrief(msgs)
	if out[1].Content != msgs[1].Content {
		t.Errorf("injectBrief with no other tabs should not change content, got %q", out[1].Content)
	}
}

func TestDraftArgAndApprovalText(t *testing.T) {
	drafts := []struct{ in, want string }{
		{`{"path":"a/b.go","content":"x"}`, "a/b.go"},
		{`{"command": "ls -la"}`, "ls -la"},
		{`{"pattern":"fo\"o"}`, `fo\"o`},
		{`{"path":"unterminated`, "unterminated"},
		{`{"other":"x"}`, ""},
	}
	for _, d := range drafts {
		if got := draftArg(d.in); got != d.want {
			t.Errorf("draftArg(%q) = %q, want %q", d.in, got, d.want)
		}
	}
	approvals := []struct{ tool, raw, want string }{
		{"bash", `{"command":"rm x"}`, "rm x"},
		{"write", `{"path":"f.txt"}`, "write the file f.txt"},
		{"edit", `{"path":"g.txt"}`, "change the file g.txt"},
		{"github__create_issue", `{"title":"t"}`, `use the create_issue tool from github with {"title":"t"}`},
		{"read", `{}`, "read"},
	}
	for _, c := range approvals {
		if got := approvalText(c.tool, c.raw); got != c.want {
			t.Errorf("approvalText(%q) = %q, want %q", c.tool, got, c.want)
		}
	}
}
