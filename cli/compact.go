package main

import (
	"context"
	"fmt"
	"strings"
)

// compactWindow is the fraction of the context window at which Prism suggests /compact.
const compactWindow = 0.70

// compactCommand runs /compact [focus] from the UI.
// It summarises the conversation using the agent's existing request path,
// replaces history with system prompt + summary, and reports before/after token counts.
func (m *model) compactCommand(focus string) {
	if m.busy {
		m.entries = append(m.entries, &entry{kind: "note", text: "wait until prism finishes, then run /compact"})
		return
	}
	if len(m.agent.messages) <= 1 {
		m.entries = append(m.entries, &entry{kind: "note", text: "nothing to compact yet"})
		return
	}
	// Refuse to compact if the history ends with an assistant tool_calls message
	// that has no matching tool results — the API would reject the rebuilt history.
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

	// Use the agent's existing complete() path directly (same as agent.compact).
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

	// Rebuild project map (same as agent.compact does).
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

// hasOrphanedToolCalls returns true if the message history ends with an
// assistant message that has tool_calls but no following tool-result messages.
// Such a history would be rejected by the API after compaction.
func hasOrphanedToolCalls(msgs []Message) bool {
	// Walk backwards to find the last assistant message with tool_calls.
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == "tool" {
			// There is at least one tool result after the last assistant message.
			return false
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			// Assistant tool_calls with no following tool results.
			return true
		}
		if m.Role == "assistant" || m.Role == "user" || m.Role == "system" {
			// A non-tool-call assistant message or user/system message — no orphan.
			return false
		}
	}
	return false
}

// maybeNudgeCompact appends a one-time note when context passes ~70% of the window.
// It is called from handle() after evUsage events.
func (m *model) maybeNudgeCompact() {
	if m.window == 0 {
		return
	}
	pct := float64(m.tokens) / float64(m.window)
	if pct < compactWindow {
		return
	}
	// Only nudge once per session: check if we already have a compact nudge entry.
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
