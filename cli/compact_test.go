package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestCompactCommandReducesHistory verifies that compactCommand replaces the
// conversation with a single summary message and reports before/after counts.
func TestCompactCommandReducesHistory(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()

	// The fake router returns a summary when asked.
	summary := "Goal: write hello.txt. Decision: used write tool. Files changed: out/hello.txt. Done."
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
		sseText(
			delta(`{"content":"`+summary+`"}`),
			sseDone,
		),
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	a := newTestAgent(t, srv.URL, dir)
	// Simulate a conversation with several messages.
	a.messages = append(a.messages,
		Message{Role: "user", Content: "write hello.txt"},
		Message{Role: "assistant", Content: "I'll write it now."},
		Message{Role: "user", Content: "make it say hi"},
		Message{Role: "assistant", Content: "Done, wrote hi."},
	)
	a.tokens = 500
	a.window = 1000

	m := model{tab: &tab{agent: a, window: a.window, tokens: a.tokens}}

	m.compactCommand("")

	// Should have added an info entry.
	if len(m.entries) == 0 {
		t.Fatal("expected at least one entry after compact")
	}
	last := m.entries[len(m.entries)-1]
	if last.kind == "error" {
		t.Fatalf("compact returned error: %s", last.text)
	}
	if last.kind != "info" {
		t.Fatalf("expected info entry, got kind=%q text=%q", last.kind, last.text)
	}

	// History should now be system + 1 summary user message.
	if len(a.messages) != 2 {
		t.Fatalf("after compact want 2 messages (system+summary), got %d", len(a.messages))
	}
	if a.messages[0].Role != "system" {
		t.Errorf("first message should be system, got %q", a.messages[0].Role)
	}
	if a.messages[1].Role != "user" {
		t.Errorf("second message should be user (summary), got %q", a.messages[1].Role)
	}
	if !strings.Contains(a.messages[1].Content, summary) {
		t.Errorf("summary message should contain the model's summary text")
	}

	// Token count should be updated.
	if m.tokens == 0 {
		t.Error("tokens should be non-zero after compact")
	}

	// Info lines should mention before/after.
	found := false
	for _, line := range last.lines {
		if strings.Contains(line, "before") || strings.Contains(line, "after") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("info lines should mention before/after token counts, got: %v", last.lines)
	}
}

// TestCompactCommandWithFocus verifies that a focus string is passed through.
func TestCompactCommandWithFocus(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()

	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
		sseText(delta(`{"content":"focused summary"}`), sseDone),
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	a := newTestAgent(t, srv.URL, dir)
	a.messages = append(a.messages,
		Message{Role: "user", Content: "do something"},
		Message{Role: "assistant", Content: "done"},
	)
	a.tokens = 200
	a.window = 1000

	m := model{tab: &tab{agent: a, window: a.window, tokens: a.tokens}}
	m.compactCommand("the auth module")

	if len(m.entries) == 0 {
		t.Fatal("expected entry")
	}
	last := m.entries[len(m.entries)-1]
	if last.kind == "error" {
		t.Fatalf("compact with focus returned error: %s", last.text)
	}
	// The focus should appear in the info lines.
	found := false
	for _, line := range last.lines {
		if strings.Contains(line, "the auth module") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("focus string should appear in output lines, got: %v", last.lines)
	}
}

// TestCompactCommandBusyRefuses verifies that /compact refuses while agent is busy.
func TestCompactCommandBusyRefuses(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()
	a := newTestAgent(t, "http://localhost:1", dir)
	m := model{tab: &tab{agent: a, busy: true}}
	m.compactCommand("")
	if len(m.entries) == 0 {
		t.Fatal("expected a note entry")
	}
	if m.entries[0].kind != "note" {
		t.Errorf("expected note, got %q", m.entries[0].kind)
	}
}

// TestCompactCommandEmptyHistoryRefuses verifies that /compact refuses on empty history.
func TestCompactCommandEmptyHistoryRefuses(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()
	a := newTestAgent(t, "http://localhost:1", dir)
	// Only system message.
	m := model{tab: &tab{agent: a}}
	m.compactCommand("")
	if len(m.entries) == 0 {
		t.Fatal("expected a note entry")
	}
	if m.entries[0].kind != "note" {
		t.Errorf("expected note, got %q", m.entries[0].kind)
	}
}

// TestMaybeNudgeCompact verifies the nudge fires once at ≥70% context.
func TestMaybeNudgeCompact(t *testing.T) {
	m := model{tab: &tab{tokens: 750, window: 1000}}
	m.maybeNudgeCompact()
	if len(m.entries) != 1 {
		t.Fatalf("expected 1 nudge entry, got %d", len(m.entries))
	}
	if !strings.Contains(m.entries[0].text, "/compact") {
		t.Errorf("nudge should mention /compact, got: %q", m.entries[0].text)
	}

	// Second call should not add another nudge.
	m.maybeNudgeCompact()
	if len(m.entries) != 1 {
		t.Errorf("nudge should only fire once, got %d entries", len(m.entries))
	}
}

// TestMaybeNudgeCompactBelowThreshold verifies no nudge below 70%.
func TestMaybeNudgeCompactBelowThreshold(t *testing.T) {
	m := model{tab: &tab{tokens: 600, window: 1000}}
	m.maybeNudgeCompact()
	if len(m.entries) != 0 {
		t.Errorf("expected no nudge below threshold, got %d entries", len(m.entries))
	}
}

// TestCompactOrphanedToolCallsRefuses: /compact must refuse when the history
// ends with an assistant tool_calls message that has no matching tool results
// (regression: compacting such history produces an API-rejected message list).
func TestCompactOrphanedToolCallsRefuses(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()
	a := newTestAgent(t, "http://localhost:1", dir)
	// Build a history that ends with an unanswered tool_calls.
	a.messages = append(a.messages,
		Message{Role: "user", Content: "do something"},
		Message{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_1", Type: "function", Function: FunctionCall{Name: "read", Arguments: `{"path":"x"}`}},
		}},
		// No tool result follows — orphaned tool_calls.
	)
	m := model{tab: &tab{agent: a}}
	m.compactCommand("")
	if len(m.entries) == 0 {
		t.Fatal("expected a note entry")
	}
	last := m.entries[len(m.entries)-1]
	if last.kind != "note" {
		t.Errorf("expected note entry, got kind=%q text=%q", last.kind, last.text)
	}
	if !strings.Contains(last.text, "unanswered tool calls") {
		t.Errorf("note should mention unanswered tool calls, got: %q", last.text)
	}
}

// TestHasOrphanedToolCalls: unit test for the helper function.
func TestHasOrphanedToolCalls(t *testing.T) {
	call := ToolCall{ID: "c1", Type: "function", Function: FunctionCall{Name: "read", Arguments: "{}"}}

	// No messages: no orphan.
	if hasOrphanedToolCalls(nil) {
		t.Error("nil messages should not be orphaned")
	}

	// Assistant with tool_calls, no tool result: orphan.
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{call}},
	}
	if !hasOrphanedToolCalls(msgs) {
		t.Error("expected orphan when tool_calls has no following tool result")
	}

	// Assistant with tool_calls followed by tool result: not orphaned.
	msgs2 := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{call}},
		{Role: "tool", ToolCallID: "c1", Content: "result"},
	}
	if hasOrphanedToolCalls(msgs2) {
		t.Error("should not be orphaned when tool result follows")
	}

	// Plain assistant message (no tool_calls): not orphaned.
	msgs3 := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "done"},
	}
	if hasOrphanedToolCalls(msgs3) {
		t.Error("plain assistant message should not be orphaned")
	}
}

func TestCompactCommandRefusesUnansweredToolCalls(t *testing.T) {
	testEnv(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	a := newTestAgent(t, srv.URL, t.TempDir())
	a.messages = append(a.messages,
		Message{Role: "user", Content: "list files"},
		Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Type: "function"}}},
	)
	before := len(a.messages)
	m := model{tab: &tab{agent: a}}
	m.compactCommand("")
	if len(m.entries) != 1 || m.entries[0].kind != "note" || !strings.Contains(m.entries[0].text, "unanswered tool calls") {
		t.Fatalf("want unanswered-tool-calls note, got %+v", m.entries)
	}
	if hits != 0 {
		t.Errorf("model called %d times, want 0", hits)
	}
	if len(a.messages) != before {
		t.Errorf("history changed: %d -> %d messages", before, len(a.messages))
	}
}

func TestCompactCommandKeepsHistoryOnModelFailure(t *testing.T) {
	testEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"error":{"message":"all routes failed"}}`)
	}))
	defer srv.Close()
	a := newTestAgent(t, srv.URL, t.TempDir())
	a.messages = append(a.messages,
		Message{Role: "user", Content: "write hello.txt"},
		Message{Role: "assistant", Content: "done"},
	)
	want := append([]Message{}, a.messages...)
	m := model{tab: &tab{agent: a}}
	m.compactCommand("")
	if len(m.entries) == 0 || m.entries[len(m.entries)-1].kind != "error" || !strings.Contains(m.entries[len(m.entries)-1].text, "compact failed") {
		t.Fatalf("want compact failed error, got %+v", m.entries)
	}
	if len(a.messages) != len(want) {
		t.Fatalf("history changed: %d -> %d messages", len(want), len(a.messages))
	}
	for i := range want {
		if a.messages[i].Role != want[i].Role || a.messages[i].Content != want[i].Content {
			t.Errorf("message %d changed: %+v", i, a.messages[i])
		}
	}
}
