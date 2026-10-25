package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Plan mode: prism looks around and writes a plan, and nothing changes until the user
// runs it. The tool list stays the same so the provider's prompt cache keeps working;
// calls that would change something are refused, and a note on each user message tells
// the model which mode it is in.

const (
	planOnNote  = "[Plan mode is on. Look around with list, grep, read and web, but change nothing: edit, write, bash, browse, crew and MCP tools are refused. When you know enough, reply with the plan: the goal in one line, numbered steps naming the files you will change, and how you will check the work. If something important is unclear, ask instead. The user decides when to run it.]"
	planOffNote = "[Plan mode is off: you can change files and run commands again.]"
	planGo      = "Go ahead with the plan above. Work through it step by step, then check your work."
)

var planTools = map[string]bool{"list": true, "grep": true, "read": true, "web": true, "screenshot": true, "skill": true}

func (a *Agent) planAllows(tool string) bool { return planTools[tool] || a.isSkill(tool) }

// planRefused reads as a failed step, here and in a reopened chat.
func planRefused(tool string) toolResult {
	r := fail("not in plan mode\n" + tool + " is not available until the user runs the plan. Use list, grep, read and web, then reply with the plan.")
	r.summary = "not in plan mode"
	return r
}

// planNote is the note for the user message that starts a turn: the plan rules while
// plan mode is on, and once "it is off now" after a planned turn.
func (a *Agent) planNote() string {
	if a.role != "" {
		return ""
	}
	if a.plan.Load() {
		a.planSent = true
		return planOnNote
	}
	if a.planSent {
		a.planSent = false
		return planOffNote
	}
	return ""
}

// compactNote keeps the plan rules on the summary that replaces a planned turn.
// The first turn after plan mode keeps saying it is off.
func (a *Agent) compactNote() string {
	if a.role != "" {
		return ""
	}
	if a.plan.Load() {
		return planOnNote
	}
	for i := len(a.messages) - 1; i > 0; i-- {
		if a.messages[i].Role == "user" {
			if a.messages[i].Note == planOffNote {
				return planOffNote
			}
			break
		}
	}
	return ""
}

// withNotes returns msgs with each message's note appended to what is sent, copying only
// when there is a note; the saved chat and its history keep the plain text.
func withNotes(msgs []Message) []Message {
	var out []Message
	for i, m := range msgs {
		if m.Note == "" {
			continue
		}
		if out == nil {
			out = append([]Message(nil), msgs...)
		}
		out[i].Content += "\n\n" + m.Note
	}
	if out == nil {
		return msgs
	}
	return out
}

func planText(on bool) string {
	if on {
		return "plan mode on · nothing changes until you pick run this plan"
	}
	return "plan mode off · prism can change files again"
}

// planSwitch is /plan. arg is empty (switch), on, off,
// keep, run, or a task to plan. message is what to send next, if anything.
func planSwitch(a *Agent, arg string) (on bool, text, message string) {
	arg = strings.TrimSpace(arg)
	if strings.HasPrefix(arg, "/") {
		return a.plan.Load(), "/plan takes a task in words, like /plan add a login page", ""
	}
	switch strings.ToLower(arg) {
	case "":
		on = !a.plan.Load()
		text = planText(on)
	case "on", "off":
		on = strings.ToLower(arg) == "on"
		text = planText(on)
	case "keep":
		on, text = true, "still planning · type what to change"
	case "run", "go":
		on, message = false, planGo
	default:
		on, message = true, arg
	}
	a.plan.Store(on)
	return on, text, message
}

func planMenu() menuState {
	return menuState{kind: "plan", title: "plan ready", hint: "↑↓ move · enter pick · esc keep planning", items: []menuItem{
		{label: "run this plan", detail: "plan mode turns off and prism starts", dot: "◆", dotTone: toneOK, match: "run"},
		{label: "keep planning", detail: "type what to change", dot: "◇", dotTone: toneAccent, match: "keep"},
		{label: "leave plan mode", detail: "nothing runs", dot: "·", dotTone: toneMuted, match: "off"},
	}}
}

// commandArg keeps newlines.
func commandArg(text, name string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), name))
}

func (m *model) planCommand(arg string) tea.Cmd {
	if m.busy {
		m.entries = append(m.entries, &entry{kind: "note", text: "wait until prism finishes, then /plan"})
		return nil
	}
	_, text, message := planSwitch(m.agent, arg)
	if text != "" {
		m.entries = append(m.entries, &entry{kind: "note", text: text})
	}
	if message == "" {
		return nil
	}
	m.input = []rune(message)
	cmd := m.submit()
	m.input = nil
	return cmd
}

func (m *model) openPlanMenu() {
	p := planMenu()
	for i := range p.items {
		arg := p.items[i].match
		p.items[i].run = func(m *model) tea.Cmd { return m.planCommand(arg) }
	}
	m.openMenu(p)
}
