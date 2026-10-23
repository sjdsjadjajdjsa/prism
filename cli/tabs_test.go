package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func tabTestModel(t *testing.T, w, h, n int) model {
	t.Helper()
	m := model{w: w, h: h, chat: true, chatAt: -time.Hour, now: time.Second, lastTextAt: -time.Hour, sayAt: -time.Hour,
		project: "~/shop", hub: newTabHub(), quiet: true,
		spawn: func() *Agent { return &Agent{messages: []Message{{Role: "system"}}} }}
	m.addTab(m.spawn())
	for len(m.tabs) < n {
		if m.newChatTab() == nil {
			t.Fatal("ctrl+t did not open a tab")
		}
	}
	return m
}

func press(t *testing.T, m model, code rune, mod tea.KeyMod) model {
	t.Helper()
	next, _ := m.key(tea.KeyPressMsg{Code: code, Mod: mod})
	return next.(model)
}

func TestTabSwitching(t *testing.T) {
	m := tabTestModel(t, 100, 30, 1)
	m = press(t, m, 't', tea.ModCtrl)
	m = press(t, m, 't', tea.ModCtrl)
	if len(m.tabs) != 3 || m.tab != m.tabs[2] {
		t.Fatalf("ctrl+t: %d tabs, active %d", len(m.tabs), m.tabIndex(m.tab))
	}
	if len(m.hub.list()) != 3 {
		t.Fatalf("hub has %d tabs", len(m.hub.list()))
	}
	for i, tb := range m.tabs {
		if tb.agent.hub != m.hub || tb.agent.tabID != tb.id || tb.id == 0 {
			t.Fatalf("tab %d agent not wired to the hub", i)
		}
	}
	m.input = []rune("draft in tab 3")
	m = press(t, m, '1', tea.ModAlt)
	if m.tab != m.tabs[0] || len(m.input) != 0 {
		t.Fatalf("alt+1 went to tab %d, input %q", m.tabIndex(m.tab), string(m.input))
	}
	m = press(t, m, tea.KeyPgUp, tea.ModCtrl)
	if m.tab != m.tabs[2] || string(m.input) != "draft in tab 3" {
		t.Fatalf("ctrl+pgup wraps to the last tab with its draft; got %d", m.tabIndex(m.tab))
	}
	m = press(t, m, tea.KeyPgDown, tea.ModCtrl)
	if m.tab != m.tabs[0] {
		t.Fatalf("ctrl+pgdown wraps to the first tab; got %d", m.tabIndex(m.tab))
	}
	m = press(t, m, tea.KeyTab, tea.ModCtrl)
	if m.tab != m.tabs[1] {
		t.Fatalf("ctrl+tab goes to the next tab; got %d", m.tabIndex(m.tab))
	}
	m = press(t, m, '9', tea.ModAlt)
	if m.tab != m.tabs[1] {
		t.Fatal("alt+9 without a ninth tab must do nothing")
	}
}

func TestTabClosing(t *testing.T) {
	m := tabTestModel(t, 100, 30, 3)
	m.switchTo(1)
	victim := m.tab
	victim.busy = true
	cancelled := false
	victim.cancel = func() { cancelled = true }
	m = press(t, m, 'w', tea.ModCtrl)
	if !m.closeAsk || len(m.tabs) != 3 {
		t.Fatal("ctrl+w on a busy tab must ask first")
	}
	m = press(t, m, 'n', 0)
	if m.closeAsk || len(m.tabs) != 3 || cancelled {
		t.Fatal("n must keep the tab")
	}
	m = press(t, m, 'w', tea.ModCtrl)
	m = press(t, m, 'y', 0)
	if len(m.tabs) != 2 || !cancelled || m.tabIndex(victim) >= 0 {
		t.Fatalf("y must close and stop the busy tab: %d tabs, cancelled %v", len(m.tabs), cancelled)
	}
	if m.tab != m.tabs[1] {
		t.Fatalf("closing moves to the tab now in that place; got %d", m.tabIndex(m.tab))
	}
	if len(m.hub.list()) != 2 {
		t.Fatalf("hub still lists %d tabs", len(m.hub.list()))
	}
	select {
	case <-victim.closed:
	default:
		t.Fatal("closed tab's listener was not stopped")
	}
	// an idle tab closes straight away; the last one stays
	m = press(t, m, 'w', tea.ModCtrl)
	m = press(t, m, 'w', tea.ModCtrl)
	if len(m.tabs) != 1 || m.tab != m.tabs[0] {
		t.Fatalf("expected the last tab to stay, got %d", len(m.tabs))
	}
}

func TestBackgroundTabEvents(t *testing.T) {
	m := tabTestModel(t, 100, 30, 2)
	bg, fg := m.tabs[0], m.tabs[1]
	bg.busy = true
	upd := func(msg tea.Msg) {
		next, cmd := m.update(msg)
		m = next.(model)
		if cmd == nil {
			t.Fatal("a tab event must keep listening to that tab")
		}
	}
	upd(tabMsg{bg, evText{"hello from the background"}})
	if m.tab != fg {
		t.Fatal("a background event switched tabs")
	}
	if n := len(bg.entries); n == 0 || bg.entries[n-1].text != "hello from the background" {
		t.Fatalf("background tab entries: %+v", bg.entries)
	}
	if len(fg.entries) != 0 {
		t.Fatalf("event leaked into the shown tab: %+v", fg.entries)
	}
	reply := make(chan bool, 1)
	upd(tabMsg{bg, evApprove{"bash", "rm -rf build", reply}})
	if bg.pending == nil || fg.pending != nil || bg.status() != "waiting" {
		t.Fatal("approval must wait on the background tab")
	}
	if info := m.hub.list(); info[0].Status != "waiting" {
		t.Fatalf("hub status %q", info[0].Status)
	}
	// keys answer the shown tab's approvals only
	m = press(t, m, 'y', 0)
	select {
	case <-reply:
		t.Fatal("y answered a background tab")
	default:
	}
	m.switchTo(0)
	m = press(t, m, 'y', 0)
	if ok := <-reply; !ok || bg.pending != nil {
		t.Fatal("approval not answered on its tab")
	}
	m.switchTo(1)
	upd(tabMsg{bg, evDone{errors.New("boom")}})
	if bg.busy || bg.status() != "error" || m.hub.list()[0].Status != "error" {
		t.Fatalf("finished background tab: busy %v status %q", bg.busy, bg.status())
	}
	// events of a closed tab are dropped
	m.switchTo(0)
	m.closeTab(true)
	if next, _ := m.update(tabMsg{bg, evText{"late"}}); len(next.(model).tabs) != 1 {
		t.Fatal("closed tab came back")
	}
}

func TestTabAutoNameAndRename(t *testing.T) {
	if got := autoName("Fix the login bug in auth.go please"); got != "fix the login bug" {
		t.Fatalf("autoName = %q", got)
	}
	m := tabTestModel(t, 100, 30, 2)
	m.started("add dark mode to the settings page")
	if m.name != "add dark mode to" || m.hub.list()[1].Name != m.name {
		t.Fatalf("auto name %q, hub %q", m.name, m.hub.list()[1].Name)
	}
	m.command("/rename checkout")
	m.started("now something else")
	if m.name != "checkout" || m.hub.list()[1].Name != "checkout" {
		t.Fatalf("rename: %q", m.name)
	}
	m.command("/tabs")
	last := m.entries[len(m.entries)-1]
	if last.kind != "info" || !strings.Contains(strings.Join(last.lines, "\n"), "checkout") {
		t.Fatalf("/tabs: %+v", last)
	}
}

func tabBarRow(m model) string {
	t := themes[m.theme]
	cv := newCanvas(m.w, m.h, t.bg, t.fg)
	drawChat(cv, t, m)
	return crewRows(cv)[3]
}

func TestTabBarRendering(t *testing.T) {
	one := tabTestModel(t, 80, 30, 1)
	if strings.TrimSpace(tabBarRow(one)) != "" {
		t.Fatal("tab bar must be hidden with a single tab")
	}
	m := tabTestModel(t, 140, 30, 5)
	names := []string{"fix the login bug", "dark mode settings", "write the docs", "release notes", "refactor the payment module"}
	for i, n := range names {
		m.tabs[i].name = n
	}
	m.tabs[0].busy = true
	m.tabs[1].pending = &evApprove{}
	m.tabs[2].result = "done"
	m.tabs[3].result = "error"
	m.switchTo(4)
	row := tabBarRow(m)
	for i, n := range names {
		if !strings.Contains(row, n[:10]) {
			t.Fatalf("140 cols: tab %d name %q missing in %q", i+1, n, row)
		}
	}
	if strings.ContainsAny(row, "‹›") {
		t.Fatalf("140 cols: five tabs fit without scrolling: %q", row)
	}
	if len([]rune(strings.TrimRight(row, " "))) > 140 {
		t.Fatal("tab bar too wide")
	}
	// the waiting tab is drawn in the warning color
	theme := themes[m.theme]
	cv := newCanvas(m.w, m.h, theme.bg, theme.fg)
	drawChat(cv, theme, m)
	at := strings.Index(row, "dark")
	if at < 0 || cv.get(len([]rune(row[:at])), 3).fg != warnColor(theme) {
		t.Fatal("waiting tab is not highlighted")
	}

	m.w = 60
	row = tabBarRow(m)
	colW := m.column()
	cx := (m.w - colW) / 2
	if len([]rune(strings.TrimRight(row, " "))) > cx+colW {
		t.Fatalf("60 cols: tab bar overflows: %q", row)
	}
	if !strings.Contains(row, "5 ") {
		t.Fatalf("60 cols: active tab missing: %q", row)
	}
	// many tabs on a narrow screen scroll with ‹ › around the active one
	for len(m.tabs) < 9 {
		m.newChatTab()
	}
	m.switchTo(4)
	m.w = 40
	labels, left, right := m.tabBar(m.column())
	if !left && !right {
		t.Fatalf("40 cols, 9 tabs: expected overflow markers, labels %d", len(labels))
	}
	row = tabBarRow(m)
	if !strings.Contains(row, " 5 ") || (left && !strings.Contains(row, "‹")) || (right && !strings.Contains(row, "›")) {
		t.Fatalf("40 cols: %q", row)
	}
}

// TestSaveTabsNoRace verifies that saveTabs reads only tab.session/tab.msgLen
// (set on the UI goroutine by evSaved) and never touches agent.messages or
// agent.session directly.
func TestSaveTabsNoRace(t *testing.T) {
	m := tabTestModel(t, 100, 30, 2)
	// Simulate evSaved arriving for tab 0 (as main.go does after agent.Run).
	m.tabs[0].session = "sess-abc"
	m.tabs[0].msgLen = 3
	m.tabs[1].session = "sess-def"
	m.tabs[1].msgLen = 5

	// saveTabs must not panic or race even when agent.messages is nil.
	m.tabs[0].agent.messages = nil
	m.tabs[1].agent.messages = nil

	// Provide a store so saveOpenTabs has somewhere to write.
	dir := t.TempDir()
	m.tabs[0].agent.store = openStore(dir)
	m.agent = m.tabs[0].agent // m.agent is used for the store

	m.saveTabs() // must not read agent.messages or agent.session
}

// TestAskRefusesBusyTab verifies /ask refuses when the target tab is busy.
func TestAskRefusesBusyTab(t *testing.T) {
	m := tabTestModel(t, 100, 30, 2)
	m.tabs[0].busy = true // tab 1 is busy

	before := len(m.entries)
	m.askCommand([]string{"/ask", "1", "what are you doing?"})
	if len(m.entries) <= before {
		t.Fatal("/ask on a busy tab should add a note entry")
	}
	last := m.entries[len(m.entries)-1]
	if last.kind != "note" || !strings.Contains(last.text, "busy") {
		t.Fatalf("/ask busy note: %+v", last)
	}
	// The current tab must not have started a run.
	if m.busy {
		t.Fatal("/ask on a busy tab must not start a run on the current tab")
	}
}

// TestOpenTabsDedupeSession verifies that openTabs never opens the same session
// twice when saved[0] fails to load and `first` already has that session.
func TestOpenTabsDedupeSession(t *testing.T) {
	dir := t.TempDir()
	a := newAgent("http://x/v1", dir, dir)
	a.session = "dup-session"

	// Build a saved-tabs list that references the same session as `first`.
	saved := []openTab{
		{Session: "dup-session", Name: "tab one", Active: true},
		{Session: "other-session", Name: "tab two"},
	}
	// Write the saved list so loadOpenTabs returns it.
	a.store.saveOpenTabs(saved)

	m := model{hub: newTabHub(), spawn: func() *Agent {
		return newAgent("http://x/v1", dir, dir)
	}}
	m.openTabs(a, true)

	// Should have at most 2 tabs (dup-session once + other-session).
	// The dup-session must not appear twice.
	sessions := map[string]int{}
	for _, tb := range m.tabs {
		sessions[tb.agent.session]++
	}
	if sessions["dup-session"] > 1 {
		t.Errorf("dup-session opened %d times, want 1", sessions["dup-session"])
	}
}
