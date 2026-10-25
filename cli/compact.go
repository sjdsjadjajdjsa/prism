package main

import (
	"context"
	"fmt"
	"strings"
)

// compactWindow is the fraction of the context window at which Prism suggests /compact.
const compactWindow = 0.70

// compactCommand runs /compact [focus]: it swaps the history for the system
// prompt plus a summary and reports token counts before and after.
func (m *model) compactCommand(focus string) {
	if m.busy {
		m.entries = append(m.entries, &entry{kind: "note", text: "wait until prism finishes, then run /compact"})
		return
	}
	if len(m.agent.messages) <= 1 {
		m.entries = append(m.entries, &entry{kind: "note", text: "nothing to compact yet"})
		return
	}
	// The API would reject a rebuilt history that ends with unanswered tool calls.
	if hasOrphanedToolCalls(m.agent.messages) {
		m.entries = append(m.entries, &entry{kind: "note", text: "cannot compact: history has unanswered tool calls; wait for the turn to finish"})
		return
	}

	before := m.agent.tokens
	if before == 0 {
		before = estimate(m.agent.messages)
	}

	ctx := context.Background()
	var noteLines []string

	prompt := compactPrompt
	if focus != "" {
		prompt = fmt.Sprintf("Summarize this conversation for yourself so you can continue the work with no other context. Focus especially on: %s. Include the user's goal and latest request, key decisions, every file read or changed and how, commands run and their results, problems found, and exactly what remains to do next. Be concise but complete. Do not call tools.", focus)
	}

	// Keep in sync with agent.compact.
	request := append(append([]Message{}, m.agent.messages...), Message{Role: "user", Content: prompt})
	light := m.agent.light
	m.agent.light = false
	reply, err := m.agent.complete(ctx, request, map[string]any{"tool_choice": "none"}, func(any) {})
	m.agent.light = light
	if err != nil {
		m.entries = append(m.entries, &entry{kind: "error", text: "compact failed: " + err.Error()})
		return
	}
	summary := strings.TrimSpace(reply.Content)
	if summary == "" {
		m.entries = append(m.entries, &entry{kind: "error", text: "compact failed: model returned empty summary"})
		return
	}

	if m.agent.mapReady != nil {
		<-m.agent.mapReady
	}
	files := m.agent.mapProject()
	m.agent.touchMu.Lock()
	m.agent.files = files
	m.agent.touchMu.Unlock()

	m.agent.messages = []Message{
		{Role: "system", Content: m.agent.systemPrompt()},
		{Role: "user", Content: summaryPrefix + " (earlier messages were compacted):\n\n" + summary + "\n\nThe user's latest request, word for word:\n" + m.agent.latestRequest() + "\n\nContinue from where you left off."},
	}
	m.agent.memMark = 1
	after := estimate(m.agent.messages)
	m.agent.tokens = after

	noteLines = append(noteLines,
		fmt.Sprintf("before  %s tokens  (%d messages)", short(before), len(request)-1),
		fmt.Sprintf("after   %s tokens  (1 summary message)", short(after)),
		fmt.Sprintf("saved   %s tokens", short(before-after)),
	)
	if focus != "" {
		noteLines = append(noteLines, "focus: "+focus)
	}
	noteLines = append(noteLines, "", "conversation compacted · keep going")
	m.entries = append(m.entries, &entry{kind: "info", text: "compact", lines: noteLines})
	m.tokens = after
}

// hasOrphanedToolCalls reports whether the history ends with tool calls that
// have no results yet.
func hasOrphanedToolCalls(msgs []Message) bool {

	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == "tool" {

			return false
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {

			return true
		}
		if m.Role == "assistant" || m.Role == "user" || m.Role == "system" {

			return false
		}
	}
	return false
}

// maybeNudgeCompact adds a one-time note once context passes compactWindow.
func (m *model) maybeNudgeCompact() {
	if m.window == 0 {
		return
	}
	pct := float64(m.tokens) / float64(m.window)
	if pct < compactWindow {
		return
	}
	// Only nudge once per session.
	for _, e := range m.entries {
		if e.kind == "note" && strings.Contains(e.text, "/compact") {
			return
		}
	}
	m.entries = append(m.entries, &entry{
		kind: "note",
		text: fmt.Sprintf("context is %.0f%% full · run /compact to summarise and free space", pct*100),
	})
}
