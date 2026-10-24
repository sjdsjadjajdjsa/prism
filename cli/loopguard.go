package main

import (
	"errors"
	"slices"
	"strings"
)

// Free models sometimes go in circles. A reply that keeps repeating the same text (in the
// answer, the reasoning or a tool call's input) is dropped and asked again of another model
// (readStream, completeRetry). A turn that keeps making the same tool call with the same
// result gets a note telling the model so, and its next step goes to another model; after
// maxLoopSwitches of those the turn stops instead of using up the free models.

const (
	maxLoopSwitches = 2
	textLoopMin     = 1200 // repeated bytes before an answer or reasoning counts as a loop
	argsLoopMin     = 2000 // tool input (a file's content) counts only when it runs out of room
	loopNudge       = "[Prism: this exact call gave the same result several times in a row. Repeating it will not help. Try a different approach, or stop and tell the user what is blocking you.]"
)

var errLooping = errors.New("The model kept repeating itself. Try again, reword the request, or pick another model with /model.")

// loopError stops a turn that kept going in circles.
type loopError struct{ what string }

func (e *loopError) Error() string {
	return "Prism stopped: the models kept repeating the same step (" + e.what + ") without getting anywhere. Try again with more detail, or pick a model with /model."
}

// evDropText takes back the end of the answer on screen (text streamed by a reply that was
// dropped because it kept repeating itself) and the reply's draft and reasoning.
type evDropText struct{ text string }

// waitWords mark a call that waits on something (a build, a server, CI): the same result
// again is not a loop. They count as whole words only, so cat watch.json still counts.
var waitWords = map[string]bool{"sleep": true, "wait": true, "watch": true, "until": true, "--wait": true, "--watch": true}

func waits(text string) bool {
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return strings.ContainsRune(" \t\n;|&()`_", r) }) {
		if waitWords[w] {
			return true
		}
	}
	return false
}

// countsForLoops reports whether a call goes into the loop check.
func countsForLoops(call FunctionCall) bool {
	switch {
	case call.Name == "bash":
		return !waits(sarg(argsOf(call.Arguments), "command"))
	case isMCP(call.Name):
		return !waits(strings.ReplaceAll(call.Name, "-", "_"))
	}
	return true
}

func (a *Agent) startLoopGuard() {
	a.loopAvoid, a.loopSwitches = nil, 0
	a.calls.reset()
}

// pinned reports whether the chat is pinned to one model, so switching models can't help.
func (a *Agent) pinned() bool { return a.model != "" && !strings.HasPrefix(a.model, "auto") }

// offLoop handles a tool call the model repeated with the same result: the model is told so
// in the latest tool result and its next step goes to another model.
func (a *Agent) offLoop(call FunctionCall, emit func(any)) error {
	what := strings.TrimSpace(call.Name + " " + strings.TrimPrefix(describe(call.Arguments), a.dir+"/"))
	a.calls.reset()
	a.loopSwitches++
	if a.loopSwitches > maxLoopSwitches {
		return &loopError{what: clip(what, 80)}
	}
	if n := len(a.messages); n > 0 && a.messages[n-1].Role == "tool" {
		a.messages[n-1].Content += "\n\n" + loopNudge
	}
	if a.pinned() || a.lastRoute == "" {
		emit(evNote{"the model is going in circles (" + clip(what, 60) + "), asking it to try another way…"})
		return nil
	}
	if !slices.Contains(a.loopAvoid, a.lastRoute) {
		a.loopAvoid = append(a.loopAvoid, a.lastRoute)
	}
	emit(evNote{"the model was going in circles (" + clip(what, 60) + "), switching to another…"})
	return nil
}
