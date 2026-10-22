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
		return ok && strings.Contains(n.text, "again")
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
