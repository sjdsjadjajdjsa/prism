package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

const (
	fastFrame = 33 * time.Millisecond
	slowFrame = 100 * time.Millisecond
)

type tickMsg time.Time

type statusMsg struct {
	ready int
	err   error
}

type entry struct {
	kind   string
	text   string
	id     string
	tool   string
	arg    string
	done   bool
	res    toolResult
	open   bool
	rows   []routeRow
	more   int    // rows hidden by the /models cap
	source string // kind=="meta": who answered, routeRow.Source
	lines  []string
	crew   *crewBlock
	view   *cmdView // structured command output (kind=="cmd")
}

type model struct {
	*tab                  // the chat on screen
	tabs           []*tab // every open chat, in tab bar order
	hub            *tabHub
	spawn          func() *Agent // a fresh agent for a new tab
	closeAsk       bool          // ctrl+w on a busy tab is waiting for y/n
	w, h           int
	theme          int
	start          time.Time
	now            time.Duration
	chat           bool
	chatAt         time.Duration
	ready          int
	offline        bool
	project        string
	gallery        string
	say            string
	sayAt          time.Duration
	doneAt         time.Duration
	sleepyUntil    time.Duration
	beamAt         time.Duration
	beamOn         bool
	quiet          bool
	proud          bool
	lastTextAt     time.Duration
	surprisedUntil time.Duration
	web            *webSession
	ticking        bool
	active         time.Duration
	menu           menuState    // the pick-one-thing overlay
	sug            suggestState // the slash-command suggestion list's highlight
	fb             filesState   // the /files browser and viewer overlay
	loginTab       *tab         // only the tab that started the browser poll captures codes
	loginWaiting   string       // browser sign-in using the main input for pasted codes
	loginFinished  string       // suppress a late poll after paste completion or cancellation
	es             editorState  // the Neovim panel
	playing        *videoPlayer // the video the viewer is playing
	kittyShown     string       // the real picture on screen, as path|x,y,w,h
	side           sidebarState // the file tree on the left
}

// idleAfter is how long the screen keeps animating after the last key, event or resize.
// After that the frame loop stops completely (no redraws, no CPU) until something happens.
const idleAfter = 3 * time.Second

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{tick(fastFrame), fetchStatus(m.agent.api)}
	for _, t := range m.tabs {
		cmds = append(cmds, waitTab(t))
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, isTick := msg.(tickMsg); !isTick && !m.ticking {
		m.now = time.Since(m.start) // the clock stood still while idle
	}
	next, cmd := m.update(msg)
	nm, ok := next.(model)
	if _, isTick := msg.(tickMsg); !ok || isTick {
		return next, cmd
	}
	if nm.loginTab != nil && nm.loginTab != nm.tab {
		nm.loginWaiting, nm.loginTab = "", nil
	}
	nm.now = time.Since(nm.start)
	nm.active = nm.now
	nm.syncEditor()
	cmd = tea.Batch(cmd, nm.syncMedia())
	if nm.planAsk && nm.chat && nm.agent != nil && nm.agent.plan.Load() && !nm.busy && !nm.menu.open && !nm.fb.open && len(nm.input) == 0 && nm.loginWaiting == "" {
		nm.planAsk = false
		nm.openPlanMenu()
	}
	if !nm.ticking {
		nm.ticking = true
		cmd = tea.Batch(cmd, tick(fastFrame))
	}
	return nm, cmd
}

// idle reports whether nothing on screen is moving and nothing is waiting on a timer.
func (m model) idle() bool {
	return m.web == nil && m.gallery == "" && !m.anyBusy() && !m.animating() && m.now-m.active > idleAfter &&
		m.now-m.sayAt > 5*time.Second && m.now >= m.sleepyUntil &&
		(m.chat || m.now > 15*time.Second) && m.scrollPos == float64(m.scroll)
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tickMsg:
		m.now = time.Since(m.start)
		if d := float64(m.scroll) - m.scrollPos; math.Abs(d) > 0.05 {
			m.scrollPos += d * 0.35
		} else {
			m.scrollPos = float64(m.scroll)
		}
		m.checkWeb()
		var refresh tea.Cmd
		if m.sideOn() && m.anyBusy() && time.Since(m.side.at) > time.Second {
			m.side.at = time.Now()
			refresh = sideTree(m.agent, 0, false)
		}
		if m.idle() {
			m.ticking = false
			// Nothing will draw until the next key or event: hand freed memory back to the system.
			go debug.FreeOSMemory()
			return m, refresh
		}
		frame := slowFrame
		if m.gallery != "" || (!m.chat && m.now < 4*time.Second) || m.busy || m.now-m.chatAt < time.Second || m.animating() {
			frame = fastFrame
		} else if !m.chat {
			frame = 50 * time.Millisecond
		}
		return m, tea.Batch(tick(frame), refresh)
	case statusMsg:
		m.ready, m.offline = msg.ready, msg.err != nil
	case pickerMsg:
		m.pickerLoaded(msg)
	case filesMsg:
		m.filesLoaded(msg)
	case modelsMsg:
		m.entries = append(m.entries, &entry{kind: "models", rows: msg.rows, text: errText(msg.err), more: msg.more, arg: msg.filter})
		m.scroll = 0
	case loginsMsg:
		m.loginsLoaded(msg)
	case loginOutMsg:
		if msg.err == nil && m.loginWaiting == msg.id {
			m.loginFinished, m.loginWaiting, m.loginTab = msg.id, "", nil
		}
		text := "signed out of " + msg.id
		if msg.err != nil {
			text = msg.err.Error()
		}
		m.entries = append(m.entries, &entry{kind: "note", text: text})
		m.scroll = 0
	case loginCodeMsg:
		if msg.err != nil {
			m.entries = append(m.entries, &entry{kind: "note", text: msg.err.Error()})
			break
		}
		m.loginFinished, m.loginWaiting, m.loginTab = "", "", nil
		if msg.browser {
			// The chat input takes the pasted code until the sign-in ends.
			m.loginWaiting, m.loginTab = msg.id, m.tab
		}
		// Kind "login": the link renders as a clickable link and the device code stands out.
		m.entries = append(m.entries, &entry{kind: "login", text: "sign in to " + msg.id, lines: loginLines(msg), arg: msg.url, tool: msg.code})
		m.scroll = 0
		interval := 5
		if msg.interval > 0 {
			interval = msg.interval
		}
		return m, tea.Batch(pollLogin(m.agent.api, msg.id, interval, msg.expires, msg.browser), loginStarted(msg))
	case loginDoneMsg:
		if m.loginFinished == msg.id {
			break
		}
		if msg.err != nil {
			m.entries = append(m.entries, &entry{kind: "note", text: msg.id + ": " + msg.err.Error()})
			if msg.pasted && msg.retryable && m.loginWaiting == msg.id {
				m.entries = append(m.entries, &entry{kind: "note", text: loginPastePrompt})
			} else if m.loginWaiting == msg.id {
				m.loginWaiting, m.loginTab = "", nil
			}
			break
		}
		m.loginFinished = msg.id
		if m.loginWaiting == msg.id {
			m.loginWaiting, m.loginTab = "", nil
		}
		lines := []string{"signed in to " + msg.id + " · " + countModels(msg.models)}
		if msg.models > 0 {
			lines = append(lines, countModels(msg.models)+" ready · pick one by name in /models")
		}
		if msg.note != "" {
			lines = append(lines, msg.note)
		}
		m.entries = append(m.entries, &entry{kind: "info", text: msg.id, lines: lines})
		m.scroll = 0
	case modelMsg:
		switch {
		case msg.err != nil:
			m.entries = append(m.entries, &entry{kind: "note", text: "pinned " + msg.name + " · " + msg.err.Error()})
		case msg.found:
			m.entries = append(m.entries, &entry{kind: "note", text: "model: " + msg.name + " · every request in this chat uses it"})
		default:
			m.entries = append(m.entries, &entry{kind: "note", text: "pinned " + msg.name + " · the router does not list it yet (it may appear once its key is set)"})
		}
		m.scroll = 0
	case keysMsg:
		m.keysLoaded(msg)
	case keySetMsg:
		switch {
		case msg.err != nil:
			m.entries = append(m.entries, &entry{kind: "note", text: msg.err.Error()})
		case msg.masked == "":
			m.entries = append(m.entries, &entry{kind: "note", text: "removed the " + msg.id + " key"})
		default:
			lines := []string{"saved · " + msg.masked}
			if msg.models > 0 {
				lines = append(lines, countModels(msg.models)+" ready · pick one by name in /models")
			}
			if msg.note != "" {
				lines = append(lines, msg.note)
			}
			m.entries = append(m.entries, &entry{kind: "info", text: msg.id + " key", lines: lines})
		}
		m.scroll = 0
	case tabMsg:
		if m.tabIndex(msg.t) < 0 {
			return m, nil // closed meanwhile
		}
		if ev, ok := msg.event.(evSaved); ok {
			// Snapshot session id and message count onto the tab struct so
			// saveTabs can read them safely without touching agent fields.
			msg.t.session = ev.session
			msg.t.msgLen = ev.msgLen
			m.saveTabs()
			return m, waitTab(msg.t)
		}
		if msg.t == m.tab {
			m.keepView(func() { m.handle(msg.event) })
		} else {
			// A background tab: apply the event to that tab and keep this one on screen.
			shown := m.tab
			m.tab = msg.t
			m.keepView(func() { m.handle(msg.event) })
			m.tab = shown
		}
		m.syncTab(msg.t)
		return m, waitTab(msg.t)
	case tea.PasteMsg:
		if m.chat && m.editorOn() && m.es.focus && !m.menu.open && !m.fb.open {
			m.es.ed.paste(msg.Content)
		} else if m.chat {
			m.input = append(m.input, []rune(strings.ReplaceAll(msg.Content, "\r\n", "\n"))...)
		}
	case editorFlushMsg, editorExitMsg:
		return m.editorMsg(msg)
	case sideTreeMsg:
		m, cmd := m.sideMsg(msg)
		m.sideReveal()
		return m, cmd
	case videoFrameMsg:
		if msg.p == m.playing {
			return m, waitVideo(msg.p)
		}
	case kittyMsg:
		return m, m.kittyShow(msg)
	case tea.MouseClickMsg, tea.MouseWheelMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg:
		return m.mouse(msg)
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

// mouse sends the pointer to the open overlay, or else to the chat: a click
// opens or closes a step, the wheel scrolls.
func (m model) mouse(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.chat && m.web == nil && m.gallery == "" && m.sideOn() {
		mouse := msg.(interface{ Mouse() tea.Mouse }).Mouse()
		if mouse.X < m.sideW() && !m.es.drag {
			next, cmd := m.sideMouse(msg)
			return next, cmd
		}
		if _, click := msg.(tea.MouseClickMsg); click {
			m.side.focus = false
		}
	}
	if m.chat && m.web == nil && m.gallery == "" && m.editorOn() && !m.menu.open && !m.fb.open {
		next, done := m.editorMouse(msg)
		if done {
			return next, nil
		}
		m = next
	}
	msg = shiftMouse(msg, -m.chatX())
	if m.chat && m.web == nil && m.gallery == "" {
		if m.menu.open {
			return m.menuMouse(msg)
		}
		if m.fb.open {
			return m.filesMouse(msg)
		}
		if next, cmd, ok := m.suggestMouse(msg); ok {
			return next, cmd
		}
	}
	switch msg := msg.(type) {
	case tea.MouseClickMsg:
		if mouse := tea.Mouse(msg); m.chat && m.web == nil && mouse.Button == tea.MouseLeft {
			if act := m.actAt(mouse.X, mouse.Y); act != "" {
				return m.clickAct(act)
			}
		}
		if mouse := tea.Mouse(msg); m.chat && mouse.Button == tea.MouseLeft {
			lines, start, end, top, _ := m.visible(themes[m.theme])
			if idx := start + mouse.Y - top; mouse.Y >= top && idx < end {
				if ref := lines[idx].ref; ref != nil {
					m.keepView(func() { ref.open = !ref.open })
				}
			}
		}
	case tea.MouseWheelMsg:
		switch tea.Mouse(msg).Button {
		case tea.MouseWheelUp:
			m.scrollBy(3)
		case tea.MouseWheelDown:
			m.scrollBy(-3)
		}
	}
	return m, nil
}

func (m model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if m.chat && m.web == nil && m.editorOn() && m.es.focus && !m.menu.open && !m.fb.open && !m.closeAsk && m.pending == nil {
		return m.editorKey(msg)
	}
	if m.chat && m.web == nil && m.sideOn() && m.side.focus && !m.menu.open && !m.fb.open && !m.closeAsk && m.pending == nil && !globalKey(k) {
		return m.sideKey(msg)
	}
	if k == "ctrl+c" {
		if m.es.ed != nil && !m.es.quitOK && m.es.ed.unsaved() {
			m.es.quitOK = true
			m.note("unsaved changes in the editor · save them there, or ctrl+c again to quit")
			return m, nil
		}
		if m.cancel != nil {
			m.cancel()
		}
		if m.es.ed != nil {
			m.es.ed.close()
		}
		if m.playing != nil {
			m.playing.stop()
		}
		m.endWeb()
		if m.kittyShown != "" {
			return m, tea.Sequence(tea.Raw(kittyClear()), tea.Quit)
		}
		return m, tea.Quit
	}
	if m.gallery != "" {
		if k == "ctrl+y" || k == "ctrl+t" {
			m.theme = (m.theme + 1) % len(themes)
		}
		return m, nil
	}
	if !m.chat {
		m.openChat()
		return m, nil
	}
	if m.web != nil {
		switch k {
		case "enter":
			return m, m.endWeb()
		case "o":
			m.startWeb()
		case "c":
			return m, copyWebLink(m.web.url)
		}
		return m, nil
	}
	if m.closeAsk {
		m.closeAsk = false
		if k == "y" || k == "Y" || k == "enter" {
			m.closeTab(true)
		}
		return m, nil
	}
	if m.menu.open {
		return m.menuKey(msg)
	}
	if m.fb.open {
		return m.filesKey(msg)
	}
	switch k {
	case "ctrl+e":
		return m, m.showEditor("")
	case "ctrl+b":
		return m, m.toggleSidebar()
	case "ctrl+t":
		return m, m.newChatTab()
	case "ctrl+w":
		m.closeTab(false)
		return m, nil
	case "ctrl+tab", "ctrl+pgdown":
		m.cycleTab(1)
		return m, nil
	case "ctrl+shift+tab", "ctrl+pgup":
		m.cycleTab(-1)
		return m, nil
	}
	if d, ok := strings.CutPrefix(k, "alt+"); ok && len(d) == 1 && d >= "1" && d <= "9" {
		m.switchTo(int(d[0] - '1'))
		m.saveTabs()
		return m, nil
	}
	if m.pending != nil {
		if m.now-m.askedAt < askGuard {
			return m, nil // a key meant for the chat box, typed as the question appeared
		}
		switch k {
		case "y", "Y", "enter":
			m.answer(answerYes)
		case "a", "A":
			if len(m.pending.risks) > 0 {
				m.answer(answerChat)
			}
		case "n", "N", "esc":
			m.answer(answerNo)
		}
		m.syncTab(m.tab)
		return m, nil
	}
	if cmd, ok := m.suggestKey(k); ok {
		return m, cmd
	}
	before := string(m.input)
	switch k {
	case "esc":
		m.loginWaiting, m.loginTab = "", nil
		if m.cancel != nil {
			m.cancel()
		}
	case "ctrl+y":
		m.theme = (m.theme + 1) % len(themes)
		saveTheme(themes[m.theme].name)
	case "enter":
		if text := strings.TrimSpace(string(m.input)); m.loginWaiting != "" && !strings.HasPrefix(text, "/") {
			if text == "" {
				return m, nil
			}
			m.input = nil
			m.sug = suggestState{}
			return m, pasteLoginCode(m.agent.api, m.loginWaiting, text)
		}
		return m, m.submit()
	case "tab":
		// tab only takes a suggestion; it never types a tab
	case "shift+enter", "ctrl+j":
		m.input = append(m.input, '\n')
	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	case "ctrl+u":
		m.input = nil
	case "ctrl+o":
		m.expandAll = !m.expandAll
	case "pgup":
		m.scrollBy(m.page())
	case "pgdown":
		m.scrollBy(-m.page())
	case "end":
		m.scroll = 0
	default:
		if msg.Text != "" {
			m.input = append(m.input, []rune(msg.Text)...)
		}
	}
	if string(m.input) != before {
		m.sug = suggestState{}
	}
	return m, nil
}

// globalKeys work wherever the keyboard is, except inside Neovim, which gets
// every key but ctrl+e.
var globalKeys = map[string]bool{"ctrl+c": true, "ctrl+b": true, "ctrl+t": true, "ctrl+w": true, "ctrl+y": true, "ctrl+o": true,
	"ctrl+tab": true, "ctrl+shift+tab": true, "ctrl+pgup": true, "ctrl+pgdown": true}

func globalKey(k string) bool {
	if d, ok := strings.CutPrefix(k, "alt+"); ok && len(d) == 1 && d >= "1" && d <= "9" {
		return true
	}
	return globalKeys[k]
}

// actAt draws the chat as it is on screen and returns what a click at x, y
// (chat coordinates) does: a tab, a key hint in the status bar, an answer.
func (m model) actAt(x, y int) string {
	chat := m
	chat.w = m.chatW()
	chat.es.shown, chat.side.shown = false, false
	cv := newCanvas(chat.w, m.h, rgb{}, rgb{})
	drawChat(cv, themes[m.theme], chat)
	if c := cv.get(x, y); c != nil {
		return c.act
	}
	return ""
}

// clickAct runs a clicked tab or key hint as if it had been chosen with the keyboard.
func (m model) clickAct(act string) (tea.Model, tea.Cmd) {
	if t, ok := strings.CutPrefix(act, "tab:"); ok {
		switch t {
		case "prev":
			m.cycleTab(-1)
		case "next":
			m.cycleTab(1)
		default:
			if i, err := strconv.Atoi(t); err == nil {
				m.switchTo(i)
			}
		}
		m.saveTabs()
		return m, nil
	}
	k, _ := strings.CutPrefix(act, "key:")
	if k == ":w" {
		if m.es.ed != nil {
			m.es.ed.save()
		}
		return m, nil
	}
	if m.pending != nil && len(k) == 1 {
		m.askedAt = -time.Hour
	}
	return m.key(keyPress(k))
}

// keyPress builds the key event for a name like "ctrl+b", "esc" or "y".
func keyPress(k string) tea.KeyPressMsg {
	var mod tea.KeyMod
	if rest, ok := strings.CutPrefix(k, "ctrl+"); ok {
		mod, k = tea.ModCtrl, rest
	}
	switch k {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape, Mod: mod}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter, Mod: mod}
	}
	r := []rune(k)[0]
	if mod != 0 {
		return tea.KeyPressMsg{Code: r, Mod: mod}
	}
	return tea.KeyPressMsg{Code: r, Text: k}
}

func (m *model) openChat() {
	m.chat = true
	m.chatAt = m.now
	m.greet()
}

func (m *model) submit() tea.Cmd {
	text := strings.TrimSpace(string(m.input))
	if text == "" || m.busy {
		return nil
	}
	m.input, m.scroll, m.planAsk = nil, 0, false
	m.sug = suggestState{}
	if strings.HasPrefix(text, "/") {
		if fields := strings.Fields(text); fields[0] == "/new" || fields[0] == "/clear" {
			m.loginWaiting, m.loginTab = "", nil
		}
		return m.command(text)
	}
	m.entries = append(m.entries, &entry{kind: "user", text: text})
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel, m.busy, m.thinking, m.busySince, m.thinkSince, m.reasoning = cancel, true, true, m.now, m.now, ""
	m.turnTools, m.turnTests, m.turnFailed, m.answering = 0, false, false, false
	m.started(text)
	m.speak("refracting…")
	events, agent := m.events, m.agent
	go func() {
		agent.Run(ctx, text, func(e any) { events <- e })
		// Run has returned, so agent.session and agent.messages are stable. Copy
		// them into evSaved so the UI goroutine never reads agent fields while
		// another goroutine may be appending.
		events <- evSaved{session: agent.session, msgLen: len(agent.messages)}
	}()
	return nil
}

// keepView runs change (an agent event, a step group opening) without moving what a
// scrolled-up reader sees: scroll counts lines up from the bottom, so it grows with the
// transcript. Only End or scrolling down returns to following (scroll 0).
func (m *model) keepView(change func()) {
	if m.scroll == 0 || !m.chat {
		change()
		return
	}
	t, width := themes[m.theme], m.column()
	before := len(m.transcript(t, width))
	change()
	if m.scroll == 0 {
		return
	}
	after := len(m.transcript(t, width))
	limit := max(0, after-m.viewHeight())
	m.scroll = clamp(m.scroll+after-before, min(1, limit), limit)
	m.scrollPos = math.Max(float64(min(1, limit)), math.Min(m.scrollPos+float64(after-before), float64(limit)))
}

func (m *model) handle(event any) {
	switch e := event.(type) {
	case evRoute:
		m.route, m.source, m.waited, m.answering = e.route, e.source, e.wait, true
	case evReasoning:
		m.reasoning += e.text
		if len(m.reasoning) > 4000 {
			m.reasoning = m.reasoning[len(m.reasoning)-3000:]
		}
	case evThinking:
		m.thinking = true
	case evApprove:
		m.thinking = false
		if m.pending == nil {
			m.pending, m.askedAt = &e, m.now
		} else {
			m.queued = append(m.queued, &e)
		}
		m.speak("may I?")
	case evCrewStart:
		m.thinking, m.reasoning, m.draft = false, "", nil
		block := newCrewBlock(e)
		placed := false
		for i := len(m.entries) - 1; i >= 0 && !placed; i-- {
			if en := m.entries[i]; en.kind == "tool" && en.tool == "crew" && !en.done {
				en.kind, en.crew, placed = "crew", block, true
			}
		}
		if !placed {
			m.entries = append(m.entries, &entry{kind: "crew", crew: block})
		}
		m.crewReact(e)
	case evCrewWorker:
		if c := m.activeCrew(); c != nil {
			c.worker(e)
		}
	case evCrewDone:
		if c := m.activeCrew(); c != nil {
			c.finish(e.summary)
		}
		m.crewReact(e)
	case evDraft:
		m.thinking, m.reasoning, m.draft = false, "", &e
	case evText:
		m.thinking, m.reasoning, m.lastTextAt, m.draft = false, "", m.now, nil
		if n := len(m.entries); n > 0 && m.entries[n-1].kind == "text" {
			m.entries[n-1].text += e.text
		} else {
			m.entries = append(m.entries, &entry{kind: "text", text: e.text})
		}
	case evDropText:
		m.draft, m.reasoning = nil, ""
		if n := len(m.entries); e.text != "" && n > 0 && m.entries[n-1].kind == "text" && strings.HasSuffix(m.entries[n-1].text, e.text) {
			if m.entries[n-1].text = strings.TrimSuffix(m.entries[n-1].text, e.text); strings.TrimSpace(m.entries[n-1].text) == "" {
				m.entries = m.entries[:n-1]
			}
		}
	case evToolStart:
		m.thinking, m.reasoning, m.draft = false, "", nil
		m.entries = append(m.entries, &entry{kind: "tool", id: e.id, tool: e.name, arg: e.arg})
		m.turnTools++
	case evToolDone:
		for i := len(m.entries) - 1; i >= 0; i-- {
			if en := m.entries[i]; (en.kind == "tool" || en.kind == "crew") && en.id == e.id && !en.done {
				en.done, en.res = true, e.result
				if en.tool == "bash" && e.result.ok && strings.Contains(en.arg, "test") {
					m.turnTests = true
				}
				if !e.result.ok {
					m.surprisedUntil = m.now + 1500*time.Millisecond
					if !m.turnFailed {
						m.speak("hm. a crack in the lens.")
					}
					m.turnFailed = true
				}
				break
			}
		}
		m.thinking, m.thinkSince, m.answering = true, m.now, false
	case evUsage:
		m.tokens, m.window = e.tokens, e.window
		m.maybeNudgeCompact()
	case evNote:
		m.entries = append(m.entries, &entry{kind: "note", text: e.text})
		if strings.HasPrefix(e.text, "compacting") {
			m.speak("folding the light.")
		}
	case evRemembered:
		if e.changed {
			m.speak("noted.")
		}
	case evDone:
		m.busy, m.thinking, m.cancel, m.reasoning, m.draft, m.pending, m.queued = false, false, nil, "", nil, nil, nil
		m.settleCrews()
		m.result = "done"
		if e.err != nil && !errors.Is(e.err, context.Canceled) {
			m.result = "error"
		} else if e.err != nil {
			m.result = ""
		}
		switch {
		case e.err == nil:
			m.entries = append(m.entries, &entry{kind: "meta", text: fmt.Sprintf("%.1fs", (m.now - m.busySince).Seconds()), arg: m.route, source: m.source})
			m.finish(true, false)
			m.planAsk = m.agent.plan.Load()
			if job := m.agent.memoryJob(); job != nil {
				events := m.events
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					defer cancel()
					changed, _ := job.run(ctx)
					events <- evRemembered{changed}
				}()
			}
		case errors.Is(e.err, context.Canceled):
			m.entries = append(m.entries, &entry{kind: "note", text: "stopped"})
		default:
			headline, detail := friendly(e.err)
			m.entries = append(m.entries, &entry{kind: "error", text: headline, arg: detail})
			m.finish(false, strings.HasPrefix(headline, "Every free AI model is busy"))
		}
	}
}

// activeCrew is the crew block still receiving worker updates.
func (m *model) activeCrew() *crewBlock {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if c := m.entries[i].crew; c != nil && !c.done {
			return c
		}
	}
	return nil
}

// settleCrews ends any crew block the turn left unfinished (esc, an error) so
// its rows read cancelled instead of spinning.
func (m *model) settleCrews() {
	for _, en := range m.entries {
		if en.crew != nil && !en.crew.done {
			en.crew.settle()
		}
	}
}

// answer replies to the approval on screen and brings up the next one.
func (m *model) answer(a answer) {
	m.pending.reply <- a
	m.pending = nil
	if len(m.queued) > 0 {
		m.pending, m.queued, m.askedAt = m.queued[0], m.queued[1:], m.now
	}
}

func (m model) page() int { return max(1, (m.h-12)/2) }

func (m *model) scrollBy(n int) {
	if !m.chat {
		return
	}
	limit := len(m.transcript(themes[m.theme], m.column())) - m.viewHeight()
	m.scroll = clamp(m.scroll+n, 0, max(0, limit))
}

func (m model) View() tea.View {
	if m.w == 0 {
		return tea.NewView("")
	}
	t := themes[m.theme]
	cv := newCanvas(m.w, m.h, t.bg, t.fg)
	var cursor *tea.Cursor
	switch {
	case m.web != nil:
		drawHandoff(cv, t, m)
	case strings.HasPrefix(m.gallery, "beam:"):
		freeze, _ := strconv.Atoi(strings.TrimPrefix(m.gallery, "beam:"))
		m.chat, m.beamOn, m.chatAt = true, true, -time.Hour
		m.beamAt = m.now - time.Duration(freeze)*time.Millisecond
		if m.beamAt <= 0 {
			m.beamAt = 1
			m.now = m.beamAt + time.Duration(freeze)*time.Millisecond
		}
		demo := *m.tab
		m.tab = &demo
		m.entries = []*entry{{kind: "user", text: "fix the bug in shop.py"}, {kind: "text", text: "Fixed. total() now multiplies price by quantity."}, {kind: "meta", text: "4.2s", arg: "nvidia/nemotron-3-ultra"}}
		drawChat(cv, t, m)
	case m.gallery != "":
		drawShardGallery(cv, t, ms(m.now), m.gallery == "beam")
	case m.chat && (m.editorOn() || m.sideOn()):
		cursor = drawLayout(cv, t, m)
	case m.chat:
		drawChat(cv, t, m)
	default:
		drawSplash(cv, t, m)
	}
	v := tea.NewView(cv.String())
	v.AltScreen = true
	v.WindowTitle = "prism"
	v.Cursor = cursor
	if m.chat {
		v.MouseMode = tea.MouseModeCellMotion
		if m.menu.open || m.fb.open || len(m.suggesting().rows) > 0 {
			// An overlay highlights the row under the pointer, so it needs
			// motion without a button held.
			v.MouseMode = tea.MouseModeAllMotion
		}
	}
	return v
}

func fetchStatus(api string) tea.Cmd {
	return func() tea.Msg {
		rows, err := fetchRoutes(api)
		ready := 0
		for _, row := range rows {
			if row.Ready {
				ready++
			}
		}
		return statusMsg{ready: ready, err: err}
	}
}

func clamp(v, lo, hi int) int { return max(lo, min(hi, v)) }

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func hash(a, b, c int) float64 {
	x := uint32(a*374761393 + b*668265263 + c*1442695041)
	x = (x ^ (x >> 13)) * 1274126177
	x ^= x >> 16
	return float64(x%10000) / 10000
}

func ease(v float64) float64 {
	v = math.Max(0, math.Min(1, v))
	return 1 - math.Pow(1-v, 3)
}

func settingPath(name string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "prism", name)
}

func loadSetting(name string) string {
	data, _ := os.ReadFile(settingPath(name))
	return strings.TrimSpace(string(data))
}

func saveSetting(name, value string) {
	if path := settingPath(name); path != "" {
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(value+"\n"), 0o644)
	}
}

func loadTheme() string { return loadSetting("theme") }

func saveTheme(name string) { saveSetting("theme", name) }

func themeIndex(name string) int {
	for i, t := range themes {
		if t.name == name {
			return i
		}
	}
	return 0
}

func projectName(dir string) string {
	if name := os.Getenv("PRISM_PROJECT"); name != "" {
		return name
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(dir, home) {
		return "~" + strings.TrimPrefix(dir, home)
	}
	return dir
}

// plainMemoryWait caps how long `prism run` waits for the memory update after
// the answer is printed.
const plainMemoryWait = 15 * time.Second

func runPlain(agent *Agent, prompt string) int {
	if strings.TrimSpace(prompt) == "" {
		fmt.Fprintln(os.Stderr, "usage: prism run <message>")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	code, route := 0, ""
	start := time.Now()
	var first time.Duration
	seen := func() {
		if first == 0 {
			first = time.Since(start)
		}
	}
	agent.Run(ctx, prompt, func(event any) {
		switch event.(type) {
		case evText, evToolStart:
			seen()
		}
		switch e := event.(type) {
		case evRoute:
			if e.route != route {
				route = e.route
				fmt.Printf("\n[%s]\n", route)
			}
		case evText:
			fmt.Print(e.text)
		case evDropText:
			if e.text != "" {
				fmt.Print("\n· (the repeated text above was dropped)\n")
			}
		case evToolStart:
			fmt.Printf("\n◆ %s %s\n", e.name, e.arg)
		case evToolDone:
			mark := "✓"
			if !e.result.ok {
				mark = "✕"
			}
			fmt.Printf("  %s %s\n", mark, e.result.summary)
		case evNote:
			fmt.Printf("\n· %s\n", e.text)
		case evCrewStart:
			fmt.Printf("  crew · %d workers\n", len(e.tasks))
		case evCrewWorker:
			if mark := map[string]string{"done": "✓", "failed": "✕", "cancelled": "–"}[e.status]; mark != "" {
				fmt.Printf("  %s %s\n", mark, e.id)
			}
		case evCrewDone:
			fmt.Printf("  %s\n", e.summary)
		case evDone:
			fmt.Println()
			if e.err != nil {
				fmt.Fprintln(os.Stderr, "error:", e.err)
				code = 1
			}
		}
	})
	// The answer is done: report the run first, then (only if the memory can be
	// saved) update it, waiting at most plainMemoryWait.
	cached := ""
	if agent.cached > 0 {
		cached = fmt.Sprintf(" (%d cached)", agent.cached)
	}
	fmt.Printf("· %d requests · %d tokens sent%s · first word %.1fs · total %.1fs\n", agent.requests, agent.sent, cached, first.Seconds(), time.Since(start).Seconds())
	if job := agent.memoryJob(); job != nil {
		mctx, cancel := context.WithTimeout(context.Background(), plainMemoryWait)
		changed, err := job.run(mctx)
		cancel()
		switch {
		case err != nil:
			fmt.Fprintln(os.Stderr, "· memory not saved:", err)
		case changed:
			fmt.Println("· memory updated")
		}
	}
	return code
}

func main() {
	names := make([]string, len(themes))
	for i, t := range themes {
		names[i] = t.name
	}
	system := flag.Bool("system", false, "computer help mode: runs on this computer and asks before every change")
	resume := flag.Bool("continue", false, "continue the last chat in this folder")
	themeName := flag.String("theme", "", "color theme: "+strings.Join(names, ", "))
	gallery := flag.String("gallery", "", "")
	api := flag.String("api", "http://127.0.0.1:4848/v1", "free AI router address")
	flag.Parse()
	if env := os.Getenv("PRISM_API"); env != "" && !isFlagSet("api") {
		*api = env
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	project := os.Getenv("PRISM_PROJECT")
	if project == "" {
		project = dir
	}
	agent := newAgent(*api, dir, project)
	agent.skills = loadSkills(dir)
	agent.mcp = startMCP(dir)
	defer agent.mcp.close()
	agent.system = *system
	anyPath = *system
	agent.messages[0].Content = agent.systemPrompt()
	if *resume {
		if list := agent.store.sessions(); len(list) > 0 {
			agent.resume(list[0])
		}
	}
	if args := flag.Args(); len(args) > 0 && args[0] == "tree" {
		os.Exit(treeMain(agent, args[1:]))
	}
	if args := flag.Args(); len(args) > 0 && args[0] == "serve" {
		dir := webDir()
		if dir == "" {
			fmt.Fprintln(os.Stderr, "the web page files were not found")
			os.Exit(1)
		}
		addr := os.Getenv("PRISM_LISTEN")
		if addr == "" {
			addr = "127.0.0.1:4849"
		}
		if err := serve(agent, addr, dir, loadTheme()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if args := flag.Args(); len(args) > 0 && args[0] == "run" {
		agent.mcp.wait(90 * time.Second)
		agent.confirm = askOnTerminal
		if agent.system {
			agent.approve = yesNo(askOnTerminal)
		}
		os.Exit(runPlain(agent, strings.Join(args[1:], " ")))
	}
	if *themeName == "" {
		*themeName = loadTheme()
	}
	// Every tab gets its own agent on the same project, sharing skills and MCP servers.
	spawn := func() *Agent {
		a := newAgent(*api, dir, project)
		a.skills, a.mcp, a.system = agent.skills, agent.mcp, agent.system
		a.messages[0].Content = a.systemPrompt()
		return a
	}
	m := model{ticking: true, start: time.Now(), hub: newTabHub(), spawn: spawn, ready: -1, project: projectName(dir), lastTextAt: -time.Hour, theme: themeIndex(*themeName), gallery: *gallery,
		beamOn: loadSetting("beam") != "off", quiet: loadSetting("quiet") == "on", sayAt: -time.Hour, es: editorState{split: loadEditorSplit()}, side: sidebarState{expanded: map[string]bool{}}}
	m.openTabs(agent, *resume)
	var options []tea.ProgramOption
	if os.Getenv("COLORTERM") == "" && os.Getenv("TERM") != "linux" {
		options = append(options, tea.WithColorProfile(colorprofile.TrueColor))
	}
	if _, err := tea.NewProgram(m, options...).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func isFlagSet(name string) bool {
	set := false
	flag.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

type evRemembered struct{ changed bool }

func entriesFrom(messages []Message) []*entry {
	outputs := map[string]string{}
	for _, msg := range messages {
		if msg.Role == "tool" {
			outputs[msg.ToolCallID] = strings.TrimSuffix(msg.Content, "\n\n"+loopNudge)
		}
	}
	var out []*entry
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			if strings.HasPrefix(msg.Content, summaryPrefix) {
				out = append(out, &entry{kind: "note", text: "earlier messages were compacted"})
				continue
			}
			if strings.HasPrefix(msg.Content, hookRetryPrefix) {
				out = append(out, &entry{kind: "note", text: "a turn-end check failed · prism kept working"})
				continue
			}
			out = append(out, &entry{kind: "user", text: msg.Content})
		case "assistant":
			if strings.TrimSpace(msg.Content) != "" {
				out = append(out, &entry{kind: "text", text: msg.Content})
			}
			for _, c := range msg.ToolCalls {
				output := outputs[c.ID]
				ok := !strings.HasPrefix(output, "Error:")
				if c.Function.Name == "browse" && strings.Contains(output, "\nRESULT: ") {
					ok = !strings.Contains(output, "steps failed")
				}
				if c.Function.Name == "crew" && ok {
					if block := crewFromHistory(c.Function.Arguments, output); block != nil {
						out = append(out, &entry{kind: "crew", id: c.ID, tool: "crew", done: true, crew: block, res: toolResult{ok: true}})
						continue
					}
				}
				out = append(out, &entry{kind: "tool", id: c.ID, tool: c.Function.Name, arg: describe(c.Function.Arguments), done: true,
					res: toolResult{ok: ok, summary: pastSummary(c.Function.Name, output, ok)}})
			}
		}
	}
	return out
}

func pastSummary(tool, output string, ok bool) string {
	lines := splitLines(strings.TrimSpace(output))
	switch {
	case len(lines) == 0:
		return ""
	case tool == "browse":
		if result := lines[len(lines)-1]; strings.HasPrefix(result, "RESULT: ") {
			return strings.TrimSuffix(strings.SplitN(strings.TrimPrefix(result, "RESULT: "), ". ", 2)[0], ".")
		}
		if !ok {
			return strings.TrimPrefix(lines[0], "Error: ")
		}
		return "page outline"
	case !ok:
		return strings.TrimPrefix(lines[0], "Error: ")
	case tool == "read":
		return fmt.Sprintf("%d lines", len(lines))
	case tool == "edit", tool == "write":
		return "done"
	case tool == "bash":
		for i := len(lines) - 1; i >= 0; i-- {
			if l := strings.TrimSpace(lines[i]); l != "" && !strings.HasPrefix(l, "[") {
				return "→ " + l
			}
		}
		return lines[len(lines)-1]
	}
	return lines[0]
}
