package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// approval is one question to the user before a tool call runs.
type approval struct {
	tool, arg string
	why       string   // plain words: what a risky command can do, or which rule asked
	risks     []string // risky kinds "yes for this chat" allows again; empty: only yes or no
}

type answer int

const (
	answerNo answer = iota
	answerYes
	answerChat // yes, and these kinds of command don't ask again in this chat
)

// answerOf reads an answer the web or a terminal sent as text.
func answerOf(s string) answer {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes", "once":
		return answerYes
	case "a", "chat", "always":
		return answerChat
	}
	return answerNo
}

// askGuard is how long a new question ignores keys, so the next letter of a message being
// typed can't answer it.
const askGuard = 600 * time.Millisecond

const saidNo = "The user said no to this. Do not try it again another way; explain what you wanted to do and ask what they prefer."

// refused decides whether a tool call needs a yes first and asks for it. It reports true,
// with the result to send back, when the call must not run. Commands are checked against the
// built-in risky kinds and the user's rules (risk.go); prism fix asks before every change.
func (a *Agent) refused(ctx context.Context, call FunctionCall, result *toolResult) bool {
	askAll := a.approve != nil && (needsApproval[call.Name] || a.mcp.has(call.Name))
	q := approval{tool: call.Name, arg: approvalText(call.Name, call.Arguments)}
	if call.Name == "bash" && a.role == "" {
		c := a.rules.check(q.arg)
		if c.block != "" {
			*result = fail(fmt.Sprintf("blocked: the rule \"never allow %s\" (%s) stops this command. Do not try it another way; tell the user what you wanted to run.", c.block, c.from))
			result.summary = "blocked by your rule"
			return true
		}
		var whys []string
		for _, r := range c.risks {
			if !a.allowedRisks[r.key] {
				q.risks = append(q.risks, r.key)
				whys = append(whys, r.why)
			}
		}
		q.why = strings.Join(whys, " · ")
		if c.ask != "" {
			q.why, q.risks = fmt.Sprintf("your rule \"always ask %s\" (%s)", c.ask, c.from), nil
		}
		if !askAll && c.ask == "" && len(q.risks) == 0 {
			return false
		}
		if askAll {
			q.risks = nil // prism fix asks before every command anyway
		}
	} else if !askAll {
		return false
	}
	ask := a.confirm
	if ask == nil && a.approve != nil {
		yes := a.approve
		ask = func(ctx context.Context, q approval) answer {
			if yes(ctx, q.tool, q.arg) {
				return answerYes
			}
			return answerNo
		}
	}
	if ask == nil {
		*result = fail("this command needs a yes from the user (" + q.why + "), and nobody can answer here. Tell the user the command so they can run it themselves.")
		result.summary = "needs your yes"
		return true
	}
	switch ask(ctx, q) {
	case answerNo:
		*result = toolResult{output: saidNo, summary: "you said no"}
		return true
	case answerChat:
		if a.allowedRisks == nil {
			a.allowedRisks = map[string]bool{}
		}
		for _, k := range q.risks {
			a.allowedRisks[k] = true
		}
	}
	return false
}

// startRules reads the rules once per turn: changing them mid-turn can't loosen this turn.
func (a *Agent) startRules() {
	if a.role == "" {
		a.rules = loadRules(a.dir)
	}
}

// askOnTerminal is the question for `prism run`. Without a terminal to type into, nothing
// that needs a yes runs.
func askOnTerminal(ctx context.Context, q approval) answer {
	verb := map[string]string{"bash": "run", "write": "write", "edit": "edit"}[q.tool]
	if verb == "" {
		verb = "use " + q.tool
	}
	if info, err := os.Stdin.Stat(); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		why := ""
		if q.why != "" {
			why = " (" + q.why + ")"
		}
		fmt.Fprintf(os.Stderr, "\n! prism wanted to %s, which needs a yes: %s%s\n  an allow rule (/rules) lets it run without asking\n", verb, q.arg, why)
		return answerNo
	}
	fmt.Printf("\n? prism wants to %s:\n    %s\n", verb, q.arg)
	if q.why != "" {
		fmt.Printf("  %s\n", q.why)
	}
	keys := "[y/N]"
	if len(q.risks) > 0 {
		keys = "[y = yes once / a = yes for this chat / N]"
	}
	fmt.Printf("  allow? %s ", keys)
	var line string
	fmt.Scanln(&line)
	return answerOf(line)
}

// yesNo turns a confirm into the plain allow-or-not question older callers ask.
func yesNo(confirm func(context.Context, approval) answer) func(ctx context.Context, tool, arg string) bool {
	return func(ctx context.Context, tool, arg string) bool {
		return confirm(ctx, approval{tool: tool, arg: arg}) != answerNo
	}
}
