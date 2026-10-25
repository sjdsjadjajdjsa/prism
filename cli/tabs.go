package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// tab is one chat in the window: its agent, transcript and turn state. model
// embeds the active tab, so m.entries, m.agent and so on mean the tab on screen.
type tab struct {
	input      []rune
	entries    []*entry
	busy       bool
	busySince  time.Duration
	thinking   bool
	reasoning  string
	thinkSince time.Duration
	waited     time.Duration
	cancel     context.CancelFunc
	events     chan any
	agent      *Agent
	route      string
	source     string // who answered last: routeRow.Source
	tokens     int
	window     int
	scroll     int
	scrollPos  float64
	turnTools  int
	turnTests  bool
	turnFailed bool
	draft      *evDraft
	answering  bool
	expandAll  bool
	pending    *evApprove
	askedAt    time.Duration // when pending showed: keys typed right before don't answer it
	queued     []*evApprove
	planAsk    bool // a planned turn ended: offer run / keep planning / leave when shown

	id     int    // tab id in the hub (0 without a hub)
	name   string // short name in the tab bar
	named  bool   // the name was given with /rename (no auto-naming)
	result string // "done" or "error" after a turn, until the tab is left
	task   string // what the tab was last asked
	closed chan struct{}
	// session and msgLen are copies written by evSaved on the UI goroutine, so
	// saveTabs never touches agent.messages or agent.session.
	session string
	msgLen  int
}

// tabMsg is an agent event of one tab; background tabs keep receiving them.
type tabMsg struct {
	t     *tab
	event any
}

// evSaved follows a finished turn: the chat is on disk, so the open tabs can be saved.
// It carries the session id and message count so saveTabs never reads agent fields
// from the UI goroutine while a background goroutine may be appending to them.
type evSaved struct {
	session string
	msgLen  int
}

func (m model) anyBusy() bool {
	for _, t := range m.tabs {
		if t.busy {
			return true
		}
	}
	return m.tab != nil && m.busy
}

// openTabs fills the window at start: the first agent's chat, or with
// `prism --continue` every chat that was open last time.
func (m *model) openTabs(first *Agent, resume bool) {
	var saved []openTab
	if resume && first.store != nil {
		saved = first.store.loadOpenTabs()
	}
	if len(saved) == 0 {
		m.addTab(first)
		return
	}
	active := 0
	// Track loaded session IDs to avoid opening the same session twice when
	// saved[0] fails to load and `first` already has a session from main().
	loaded := map[string]bool{}
	if first.session != "" {
		loaded[first.session] = true
	}
	for _, s := range saved {
		a := first
		if len(m.tabs) > 0 {
			if m.spawn == nil {
				break
			}
			a = m.spawn()
		}
		if loaded[s.Session] {
			continue // already open (e.g. first tab resumed this session in main)
		}
		sess, err := a.store.loadSession(s.Session)
		if err != nil {
			if len(m.tabs) > 0 {
				continue
			}
		} else {
			a.resume(sess)
			loaded[sess.ID] = true
		}
		t, _ := m.addTab(a) // Init starts listening to every tab
		if s.Name != "" {
			t.name = clipText(s.Name, 24)
			if m.hub != nil {
				m.hub.rename(t.id, t.name)
			}
		}
		if s.Active {
			active = len(m.tabs) - 1
		}
	}
	m.switchTo(active)
}

func newTab(agent *Agent) *tab {
	t := &tab{agent: agent, events: make(chan any, 256), closed: make(chan struct{}), name: "new chat"}
	if agent != nil {
		t.window, t.tokens = agent.window, agent.tokens
		t.entries = entriesFrom(agent.messages)
		wireApprove(agent, t.events)
		if title := firstUserText(agent.messages); title != "" {
			t.name = autoName(title)
		}
	}
	return t
}

// wireApprove makes the agent ask its questions through this tab's events; prism fix also
// asks before every change.
func wireApprove(agent *Agent, events chan any) {
	agent.confirm = func(ctx context.Context, q approval) answer {
		reply := make(chan answer, 1)
		select {
		case events <- evApprove{q, reply}:
		case <-ctx.Done():
			return answerNo
		}
		select {
		case a := <-reply:
			return a
		case <-ctx.Done():
			return answerNo
		}
	}
	if agent.system {
		agent.approve = yesNo(agent.confirm)
	}
}

// waitTab delivers the tab's next event, or nothing once the tab is closed.
func waitTab(t *tab) tea.Cmd {
	return func() tea.Msg {
		select {
		case e := <-t.events:
			return tabMsg{t, e}
		case <-t.closed:
			return nil
		}
	}
}

func firstUserText(messages []Message) string {
	for _, msg := range messages {
		if msg.Role == "user" && !strings.HasPrefix(msg.Content, summaryPrefix) {
			return msg.Content
		}
	}
	return ""
}

// autoName turns a first message into a 3-4 word tab name.
func autoName(text string) string {
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '.' && r != '_' && r != '/'
	})
	var out []string
	size := 0
	for _, w := range words {
		w = strings.Trim(w, ".-_/")
		if w == "" {
			continue
		}
		if len(out) >= 4 || (len(out) >= 3 && size+len(w) > 22) {
			break
		}
		out, size = append(out, strings.ToLower(w)), size+len(w)+1
	}
	if len(out) == 0 {
		return "new chat"
	}
	return clipText(strings.Join(out, " "), 24)
}

// status is the tab's state for the tab bar and the hub.
func (t *tab) status() string {
	switch {
	case t.pending != nil:
		return "waiting"
	case t.busy:
		return "working"
	case t.result != "":
		return t.result
	}
	return "idle"
}

func (m *model) syncTab(t *tab) {
	if m.hub != nil && t.id != 0 {
		m.hub.setStatus(t.id, t.status(), t.task)
	}
}

// agentByIDMu guards agentByIDMap, which is read from HTTP goroutines.
var agentByIDMu sync.RWMutex
var agentByIDMap = map[int]*Agent{}

func registerAgent(id int, a *Agent) {
	agentByIDMu.Lock()
	agentByIDMap[id] = a
	agentByIDMu.Unlock()
}

func unregisterAgent(id int) {
	agentByIDMu.Lock()
	delete(agentByIDMap, id)
	agentByIDMu.Unlock()
}

// agentFor is safe to call from HTTP goroutines.
func agentFor(id int) *Agent {
	agentByIDMu.RLock()
	a := agentByIDMap[id]
	agentByIDMu.RUnlock()
	return a
}

// syncHubPositions sets hub.posOf so brief() and claimFile() show tab-bar
// positions instead of hub IDs.
func (m *model) syncHubPositions() {
	if m.hub == nil {
		return
	}
	// Snapshot the positions on the UI goroutine.
	pos := make(map[int]int, len(m.tabs))
	for i, t := range m.tabs {
		pos[t.id] = i + 1
	}
	m.hub.mu.Lock()
	m.hub.posOf = func(id int) int {
		if p, ok := pos[id]; ok {
			return p
		}
		return id
	}
	m.hub.mu.Unlock()
}

func (m *model) addTab(agent *Agent) (*tab, tea.Cmd) {
	t := newTab(agent)
	if m.hub != nil {
		t.id = m.hub.add(t.name)
		if agent != nil {
			agent.hub, agent.tabID = m.hub, t.id
			registerAgent(t.id, agent)
		}
	}
	m.tabs = append(m.tabs, t)
	m.switchTo(len(m.tabs) - 1)
	m.syncHubPositions()
	return t, waitTab(t)
}

// newChatTab is ctrl+t: a fresh agent on the same project.
func (m *model) newChatTab() tea.Cmd {
	if len(m.tabs) >= maxOpenTabs {
		m.speak("nine tabs is plenty.")
		return nil
	}
	if m.spawn == nil {
		return nil
	}
	_, cmd := m.addTab(m.spawn())
	m.saveTabs()
	return cmd
}

func (m *model) tabIndex(t *tab) int {
	for i, x := range m.tabs {
		if x == t {
			return i
		}
	}
	return -1
}

// switchTo shows tab i. The tab being left forgets its done/error mark.
func (m *model) switchTo(i int) {
	if i < 0 || i >= len(m.tabs) || m.tabs[i] == m.tab {
		return
	}
	if old := m.tab; old != nil && old.result != "" {
		old.result = ""
		m.syncTab(old)
	}
	m.tab, m.closeAsk = m.tabs[i], false
	m.scrollPos = float64(m.scroll)
}

func (m *model) cycleTab(d int) {
	if n := len(m.tabs); n > 1 {
		m.switchTo((m.tabIndex(m.tab) + d + n) % n)
		m.saveTabs()
	}
}

// closeTab is ctrl+w; a busy tab asks first unless force.
func (m *model) closeTab(force bool) {
	if len(m.tabs) < 2 {
		m.speak("this is the last tab.")
		return
	}
	t := m.tab
	if t.busy && !force {
		m.closeAsk = true
		return
	}
	m.closeAsk = false
	if t.cancel != nil {
		t.cancel()
	}
	close(t.closed)
	if m.hub != nil && t.id != 0 {
		m.hub.remove(t.id)
		unregisterAgent(t.id)
	}
	i := m.tabIndex(t)
	m.tabs = append(m.tabs[:i:i], m.tabs[i+1:]...)
	m.tab = nil
	m.switchTo(min(i, len(m.tabs)-1))
	m.syncHubPositions()
	m.saveTabs()
}

func (m *model) renameTab(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		m.entries = append(m.entries, &entry{kind: "note", text: "usage: /rename <name>"})
		return
	}
	m.name, m.named = clipText(name, 24), true
	if m.hub != nil && m.id != 0 {
		m.hub.rename(m.id, m.name)
	}
	m.entries = append(m.entries, &entry{kind: "note", text: "tab renamed to " + m.name})
	m.saveTabs()
}

// started is called when the active tab sends a message.
func (m *model) started(text string) {
	m.result, m.task = "", clipText(strings.Join(strings.Fields(text), " "), 80)
	if !m.named && m.name == "new chat" {
		m.name = autoName(text)
		if m.hub != nil && m.id != 0 {
			m.hub.rename(m.id, m.name)
		}
	}
	m.syncTab(m.tab)
}

// saveTabs remembers which chats are open, for `prism --continue`. It reads
// only tab.session and tab.msgLen, never the agent's fields.
func (m *model) saveTabs() {
	if m.tab == nil || m.agent == nil || m.agent.store == nil || len(m.tabs) == 0 {
		return
	}
	var list []openTab
	for _, t := range m.tabs {
		if t.session == "" || t.msgLen < 2 {
			continue // nothing saved for this chat yet
		}
		list = append(list, openTab{Session: t.session, Name: t.name, Active: t == m.tab})
	}
	m.agent.store.saveOpenTabs(list)
}

// tabsCommand is /tabs: pick an open chat to switch to.
func (m *model) tabsCommand() {
	var items []menuItem
	cur := 0
	for i, t := range m.tabs {
		task := t.task
		if task == "" {
			task = "—"
		}
		it := menuItem{dot: "◌", label: fmt.Sprintf("%d  %s", i+1, clipText(t.name, 16)), detail: t.status(), state: clipText(task, 60)}
		if t == m.tab {
			it.dot, it.dotTone, it.state, cur = "●", toneAccent, "current · "+it.state, i
		}
		target := t
		it.run = func(m *model) tea.Cmd {
			m.switchTo(m.tabIndex(target))
			m.saveTabs()
			return nil
		}
		items = append(items, it)
	}
	m.openMenu(menuState{
		kind:  "tabs",
		title: "tabs",
		hint:  "enter switch · ctrl+t new · ctrl+w close · alt+1..9 jump",
		empty: "no open chats",
		items: items,
		sel:   cur,
		noun:  "tab",
	})
}

// askCommand is /ask <n> <question>: a read-only copy of tab n's chat answers
// here, without touching tab n.
func (m *model) askCommand(fields []string) tea.Cmd {
	n := 0
	if len(fields) > 1 {
		n, _ = strconv.Atoi(fields[1])
	}
	if len(fields) < 3 || n < 1 || n > len(m.tabs) {
		m.entries = append(m.entries, &entry{kind: "note", text: "usage: /ask <tab number> <question> · see /tabs"})
		return nil
	}
	if m.busy {
		return nil
	}
	srcTab := m.tabs[n-1]
	if srcTab.agent == nil {
		return nil
	}
	if srcTab.busy {
		m.entries = append(m.entries, &entry{kind: "note", text: fmt.Sprintf("tab %d is busy; wait until it finishes before using /ask", n)})
		return nil
	}
	src := srcTab.agent
	question := strings.Join(fields[2:], " ")
	ask := &Agent{api: src.api, dir: src.dir, window: src.window, readOnly: true, quick: src.quick,
		messages: append([]Message(nil), src.messages...)}
	m.entries = append(m.entries, &entry{kind: "user", text: fmt.Sprintf("ask tab %d · %s", n, question)})
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel, m.busy, m.thinking, m.busySince, m.thinkSince, m.reasoning = cancel, true, true, m.now, m.now, ""
	m.turnTools, m.turnTests, m.turnFailed, m.answering = 0, false, false, false
	events := m.events
	prompt := question + "\n\n(Answer from this conversation and the project files. This is a side question: read only, change nothing.)"
	go ask.Run(ctx, prompt, func(e any) {
		if _, ok := e.(evUsage); ok {
			return // the context size belongs to the other tab
		}
		events <- e
	})
	m.syncTab(m.tab)
	return nil
}

// tabsShown is whether the tab bar takes a line (only with 2+ tabs).
func (m model) tabsShown() bool { return len(m.tabs) > 1 }

type tabLabel struct {
	text   string
	t      *tab
	active bool
}

// tabBar lays out the tab labels for width cells: names shrink first, then
// tabs scroll with ‹ › around the active one.
func (m model) tabBar(width int) (labels []tabLabel, left, right bool) {
	build := func(nameW int) []tabLabel {
		out := make([]tabLabel, len(m.tabs))
		for i, t := range m.tabs {
			text := fmt.Sprintf(" %d ● ", i+1)
			if nameW > 0 {
				text = fmt.Sprintf(" %d ● %s ", i+1, clipText(t.name, nameW))
			}
			out[i] = tabLabel{text, t, t == m.tab}
		}
		return out
	}
	total := func(ls []tabLabel) int {
		n := 0
		for _, l := range ls {
			n += len([]rune(l.text)) + 1
		}
		return n
	}
	for _, nameW := range []int{18, 12, 8, 5} {
		if ls := build(nameW); total(ls) <= width {
			return ls, false, false
		}
	}
	all := build(5)
	active := max(0, m.tabIndex(m.tab))
	lo, hi := active, active+1
	room := width - 4 // ‹ and › with their spaces
	used := len([]rune(all[active].text)) + 1
	for {
		grew := false
		if hi < len(all) && used+len([]rune(all[hi].text))+1 <= room {
			used += len([]rune(all[hi].text)) + 1
			hi++
			grew = true
		}
		if lo > 0 && used+len([]rune(all[lo-1].text))+1 <= room {
			lo--
			used += len([]rune(all[lo].text)) + 1
			grew = true
		}
		if !grew {
			break
		}
	}
	return all[lo:hi], lo > 0, hi < len(all)
}

// warnColor is the theme's most yellow spectrum color.
func warnColor(t theme) rgb {
	best, score := t.spectrum[0], -1000
	for _, c := range t.spectrum {
		if s := int(min(c.r, c.g)) - int(c.b); s > score {
			best, score = c, s
		}
	}
	return best
}

func drawTabBar(cv *canvas, t theme, m model, x, y, w int) {
	labels, left, right := m.tabBar(w)
	n := ms(m.now)
	cx := x
	if left {
		cx = cv.text(cx, y, "‹ ", t.muted, false)
	}
	for _, l := range labels {
		status := l.t.status()
		fg, dotCol, dot, bold := t.muted, t.muted, '○', false
		switch status {
		case "working":
			dot = []rune("◐◓◑◒")[int(n/180)%4]
			dotCol = t.at(n / 3000)
		case "waiting":
			dot, dotCol, fg, bold = '●', warnColor(t), warnColor(t), true
		case "done":
			dot, dotCol = '●', t.green
		case "error":
			dot, dotCol = '●', t.red
		}
		if l.active {
			cv.fill(cx, y, len([]rune(l.text)), 1, t.panel)
			if status != "waiting" {
				fg = t.fg
			}
			bold = true
		}
		start := cx
		cx = cv.text(cx, y, l.text, fg, bold)
		// The dot is the third cell: " 1 ● name ".
		if i := strings.IndexRune(l.text, '●'); i >= 0 {
			cv.set(start+len([]rune(l.text[:i])), y, dot, dotCol, true)
		}
		cx++
	}
	if right {
		cv.text(x+w-1, y, "›", t.muted, false)
	}
}
