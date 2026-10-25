package main

import (
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type evFollowup struct {
	text    string
	steered bool
}

// Steer is safe to call while Run is executing. Only Run mutates messages.
func (a *Agent) Steer(text string) bool {
	return a.acceptPrompt(text, false)
}
func (a *Agent) Queue(text string) bool {
	return a.acceptPrompt(text, true)
}
func (a *Agent) acceptPrompt(text string, queue bool) bool {
	text = strings.TrimSpace(text)
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	if !a.steeringOpen || text == "" || strings.HasPrefix(text, "/") {
		return false
	}
	if queue {
		a.promptQueue = append(a.promptQueue, text)
	} else {
		a.pendingSteers = append(a.pendingSteers, text)
	}
	return true
}
func (a *Agent) openSteering() {
	a.steerMu.Lock()
	a.steeringOpen = true
	a.steerMu.Unlock()
}

// finishPrompt atomically takes a follow-up or closes acceptance for this run.
func (a *Agent) finishPrompt(chain bool) (string, bool) {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	if chain {
		if text, steered := a.takePrompt(); text != "" {
			return text, steered
		}
	}
	a.steeringOpen = false
	return "", false
}
func (a *Agent) queuedPrompts() int {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	return len(a.promptQueue)
}
func (a *Agent) drainSteers() {
	a.steerMu.Lock()
	pending := a.pendingSteers
	a.pendingSteers = nil
	a.steerMu.Unlock()
	for _, text := range pending {
		a.messages = append(a.messages, Message{Role: "user", Content: text})
	}
}
func (a *Agent) steeringIsOpen() bool {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	return a.steeringOpen
}

// takePrompt requires steerMu.
func (a *Agent) takePrompt() (string, bool) {
	if len(a.pendingSteers) > 0 {
		text := a.pendingSteers[0]
		a.pendingSteers = a.pendingSteers[1:]
		return text, true
	}
	if len(a.promptQueue) > 0 {
		text := a.promptQueue[0]
		a.promptQueue = a.promptQueue[1:]
		return text, false
	}
	return "", false
}
func (a *Agent) restorePrompts() string {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	texts := append(a.pendingSteers, a.promptQueue...)
	a.pendingSteers, a.promptQueue = nil, nil
	return strings.Join(texts, "\n\n")
}
func notification(blurred bool, text string) tea.Cmd {
	if !blurred {
		return nil
	}
	text = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, text)
	if os.Getenv("TERM_PROGRAM") == "iTerm.app" {
		return tea.Raw("\x1b]9;" + text + "\a")
	}
	return tea.Raw("\x1b]777;notify;Prism;" + text + "\a")
}
