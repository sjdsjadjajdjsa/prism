package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// menuLabels is every row label of the menu as it is filtered now.
func menuLabels(p menuState) []string {
	var out []string
	for _, it := range p.visible() {
		out = append(out, it.label)
	}
	return out
}

// menuModel is a chat-mode model with a terminal of w×h and a menu of n rows
// named row-00…; picking a row records its label in *picked.
func menuModel(t *testing.T, w, h, n int, picked *string) model {
	t.Helper()
	m := model{tab: &tab{agent: &Agent{}}, w: w, h: h, chat: true}
	var items []menuItem
	for i := 0; i < n; i++ {
		label := fmt.Sprintf("row-%02d", i)
		items = append(items, menuItem{label: label, detail: "detail " + label, dot: "●", state: "ok",
			run: func(m *model) tea.Cmd { *picked = label; return nil }})
	}
	m.openMenu(menuState{kind: "test", title: "test", filterable: true, noun: "row", items: items})
	return m
}

func send(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	next, cmd := m.update(msg)
	return next.(model), cmd
}

func keyMsg(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

func typeText(t *testing.T, m model, s string) model {
	for _, r := range s {
		m, _ = send(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

// drawn renders the menu onto a fresh canvas and returns it.
func drawn(m model) *canvas {
	t := themes[m.theme]
	cv := newCanvas(m.w, m.h, t.bg, t.fg)
	x, y, w, top := m.overlayArea()
	drawMenu(cv, t, m, x, y, w, top)
	return cv
}

func rowText(cv *canvas, y int) string {
	var b strings.Builder
	for x := 0; x < cv.w; x++ {
		if c := cv.get(x, y); c != nil && c.ch != 0 {
			b.WriteRune(c.ch)
		}
	}
	return b.String()
}

// checkMenuFits fails if the menu wrote anything outside its column.
func checkMenuFits(t *testing.T, m model) {
	t.Helper()
	cv := drawn(m)
	bg := themes[m.theme].bg
	x, _, w, _ := m.overlayArea()
	for y := 0; y < cv.h; y++ {
		for cx := 0; cx < cv.w; cx++ {
			if cx >= x+1 && cx <= x+w-2 {
				continue
			}
			if c := cv.get(cx, y); c.ch != ' ' || c.bg != bg {
				t.Fatalf("menu %q drew outside its column at %d,%d (width %d): %q", m.menu.kind, cx, y, m.w, rowText(cv, y))
			}
		}
	}
}

func TestMenuKeysWrapPageAndEnter(t *testing.T) {
	var picked string
	m := menuModel(t, 100, 30, 20, &picked)
	m, _ = send(t, m, keyMsg(tea.KeyUp))
	if m.menu.sel != 19 {
		t.Fatalf("up from the top should wrap to 19, got %d", m.menu.sel)
	}
	m, _ = send(t, m, keyMsg(tea.KeyDown))
	if m.menu.sel != 0 {
		t.Fatalf("down from the bottom should wrap to 0, got %d", m.menu.sel)
	}
	m, _ = send(t, m, keyMsg(tea.KeyPgDown))
	_, y, _, top := m.overlayArea()
	if page := m.menu.layout(y, top).shown; m.menu.sel != page {
		t.Fatalf("pgdown: sel %d, want %d", m.menu.sel, page)
	}
	m, _ = send(t, m, keyMsg(tea.KeyEnd))
	if l := m.menu.layout(y, top); m.menu.sel != 19 || l.off+l.shown != 20 {
		t.Fatalf("end: sel %d off %d", m.menu.sel, l.off)
	}
	m, _ = send(t, m, keyMsg(tea.KeyPgUp))
	m, _ = send(t, m, keyMsg(tea.KeyHome))
	if m.menu.sel != 0 || m.menu.off != 0 {
		t.Fatalf("home: sel %d off %d", m.menu.sel, m.menu.off)
	}
	m, _ = send(t, m, keyMsg(tea.KeyDown))
	m, _ = send(t, m, keyMsg(tea.KeyDown))
	m, _ = send(t, m, keyMsg(tea.KeyEnter))
	if picked != "row-02" || m.menu.open {
		t.Fatalf("enter picked %q, open %v", picked, m.menu.open)
	}
}

func TestMenuFilterAndEsc(t *testing.T) {
	var picked string
	m := menuModel(t, 100, 30, 20, &picked)
	closed := false
	m.menu.onClose = func(*model) { closed = true }
	m = typeText(t, m, "15")
	if got := menuLabels(m.menu); len(got) != 1 || got[0] != "row-15" {
		t.Fatalf("filter 15: %v", got)
	}
	m, _ = send(t, m, keyMsg(tea.KeyBackspace))
	if got := menuLabels(m.menu); len(got) != 11 || m.menu.sel != 0 {
		t.Fatalf("filter 1: %d rows, sel %d", len(got), m.menu.sel)
	}
	m = typeText(t, m, "zz")
	if !strings.Contains(rowText(drawn(m), m.h-3-m.boxHeight()-1), `no row matches "1zz"`) {
		t.Errorf("an empty filter result should say so")
	}
	m, _ = send(t, m, keyMsg(tea.KeyEnter)) // nothing to pick
	if !m.menu.open || picked != "" {
		t.Fatal("enter on an empty list should do nothing")
	}
	m, _ = send(t, m, keyMsg(tea.KeyEscape))
	if m.menu.open || !closed || picked != "" {
		t.Fatalf("esc: open %v, onClose %v, picked %q", m.menu.open, closed, picked)
	}
}

func TestMenuMouseHitsScrolledRows(t *testing.T) {
	var picked string
	m := menuModel(t, 100, 30, 40, &picked)
	m, _ = send(t, m, tea.MouseWheelMsg{X: 50, Y: 15, Button: tea.MouseWheelDown})
	m, _ = send(t, m, tea.MouseWheelMsg{X: 50, Y: 15, Button: tea.MouseWheelDown})
	_, y, _, top := m.overlayArea()
	l := m.menu.layout(y, top)
	if l.off != 6 {
		t.Fatalf("two wheel notches should scroll 6 rows, off %d", l.off)
	}
	if m.menu.sel < l.off || m.menu.sel >= l.off+l.shown {
		t.Fatalf("highlight %d left the view %d..%d", m.menu.sel, l.off, l.off+l.shown)
	}
	// every row on screen maps to the item drawn on it
	cv := drawn(m)
	vis := m.menu.visible()
	rows := 0
	for sy := 0; sy < m.h; sy++ {
		idx, _ := m.menuHit(10, sy)
		if idx < 0 {
			continue
		}
		rows++
		if !strings.Contains(rowText(cv, sy), vis[idx].label) {
			t.Fatalf("row %d hits %s but shows %q", sy, vis[idx].label, rowText(cv, sy))
		}
	}
	if rows != l.shown {
		t.Fatalf("%d rows hit, %d shown", rows, l.shown)
	}
	// hover highlights without scrolling, click picks the row under the pointer
	first := y - l.lines + l.head
	m, _ = send(t, m, tea.MouseMotionMsg{X: 10, Y: first + 4})
	if m.menu.sel != l.off+4 || m.menu.layout(y, top).off != l.off {
		t.Fatalf("hover: sel %d off %d", m.menu.sel, m.menu.layout(y, top).off)
	}
	want := vis[l.off+5].label
	m, _ = send(t, m, tea.MouseClickMsg{X: 10, Y: first + 5, Button: tea.MouseLeft})
	if picked != want || m.menu.open {
		t.Fatalf("click picked %q, want %q", picked, want)
	}
}

func TestMenuClickOutsideCloses(t *testing.T) {
	var picked string
	m := menuModel(t, 100, 30, 5, &picked)
	closed := false
	m.menu.onClose = func(*model) { closed = true }
	_, y, _, _ := m.overlayArea()
	// the title line is inside: nothing happens
	l := m.menu.layout(y, 0)
	m, _ = send(t, m, tea.MouseClickMsg{X: 10, Y: y - l.lines, Button: tea.MouseLeft})
	if !m.menu.open {
		t.Fatal("a click on the title closed the menu")
	}
	m, _ = send(t, m, tea.MouseClickMsg{X: 10, Y: 1, Button: tea.MouseLeft})
	if m.menu.open || !closed || picked != "" {
		t.Fatalf("click outside: open %v closed %v picked %q", m.menu.open, closed, picked)
	}
}

func TestMenuFitsAt60Columns(t *testing.T) {
	var picked string
	m := menuModel(t, 60, 24, 30, &picked)
	m.menu.items[3].label = strings.Repeat("a very long label ", 8)
	m.menu.items[4].state = strings.Repeat("a very long state ", 8)
	m.menu.items[5].sub = true
	m.menu.items[5].label = strings.Repeat("sign out of something long ", 5)
	checkMenuFits(t, m)
	m.w = 30
	checkMenuFits(t, m)
}

func TestModelPickerOnMenu(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"route":"nvidia/llama-big:free","ready":true},{"route":"groq/qwen-small","ready":false,"retryInSeconds":30}]`))
	}))
	defer srv.Close()
	a := newTestAgent(t, srv.URL+"/v1", t.TempDir())
	a.model = "groq/qwen-small"
	m := model{tab: &tab{agent: a}, w: 100, h: 30, chat: true}
	cmd := m.command("/model")
	if !m.menu.open || !m.menu.loading || !strings.Contains(rowText(drawn(m), m.h-3-m.boxHeight()-1), "loading") {
		t.Fatal("/model should open a loading picker")
	}
	m, _ = send(t, m, cmd())
	got := menuLabels(m.menu)
	if len(got) != 3 || got[0] != "auto" || got[1] != "llama-big" {
		t.Fatalf("picker rows: %v", got)
	}
	if !strings.Contains(m.menu.items[2].state, "current") || strings.Contains(m.menu.items[0].state, "current") {
		t.Fatalf("the pinned route should be marked current: %+v", m.menu.items)
	}
	m, _ = send(t, m, keyMsg(tea.KeyDown))
	m, _ = send(t, m, keyMsg(tea.KeyEnter))
	if a.model != "nvidia/llama-big:free" || m.menu.open {
		t.Fatalf("enter pinned %q", a.model)
	}
	checkMenuFits(t, m)
}

func TestLoginMenu(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		switch r.Method {
		case "GET":
			w.Write([]byte(`{"subscriptions":[{"id":"copilot","name":"GitHub Copilot","signedIn":true,"models":12},{"id":"claude","name":"Claude","signedIn":false,"error":"token expired"}]}`))
		default:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["signOut"] == true {
				w.Write([]byte(`{"ok":true}`))
				return
			}
			w.Write([]byte(`{"userCode":"ABCD-1234","verificationURI":"https://example.test/device","expires":900}`))
		}
	}))
	defer srv.Close()
	a := newTestAgent(t, srv.URL+"/v1", t.TempDir())
	m := model{tab: &tab{agent: a}, w: 60, h: 30, chat: true}
	cmd := m.command("/login")
	if !m.menu.open || m.menu.kind != "login" {
		t.Fatal("/login should open a menu")
	}
	m, _ = send(t, m, cmd())
	got := strings.Join(menuLabels(m.menu), "|")
	if got != "copilot|sign out of GitHub Copilot|claude" {
		t.Fatalf("login rows: %s", got)
	}
	if it := m.menu.items[2]; !strings.Contains(it.state, "not signed in") || !strings.Contains(it.state, "token expired") || it.stateTone != toneErr {
		t.Fatalf("claude row should show its state and last error: %+v", it)
	}
	if !strings.Contains(m.menu.items[0].state, "12 models") {
		t.Fatalf("copilot row: %+v", m.menu.items[0])
	}
	checkMenuFits(t, m)

	m.menuSelect(2)
	cmd = m.menuRun()
	if code, ok := cmd().(loginCodeMsg); !ok || code.id != "claude" || code.code != "ABCD-1234" {
		t.Fatalf("enter should start the claude sign-in, got %+v", code)
	}
	m.command("/login")
	m.fillMenu("login", loginItems([]loginRow{{ID: "copilot", Name: "GitHub Copilot", SignedIn: true}}), nil)
	m.menuSelect(1)
	if out, ok := m.menuRun()().(loginOutMsg); !ok || out.id != "copilot" || out.err != nil {
		t.Fatalf("sign-out row: %+v", out)
	}
	// the typed forms still work
	if code, ok := m.command("/login claude")().(loginCodeMsg); !ok || code.id != "claude" {
		t.Fatalf("/login claude: %+v", code)
	}
	if out, ok := m.command("/login rm copilot")().(loginOutMsg); !ok || out.id != "copilot" {
		t.Fatalf("/login rm copilot: %+v", out)
	}
}

func TestKeyMenu(t *testing.T) {
	srv := keysTestServer(t)
	defer srv.Close()
	api := srv.URL + "/v1"
	if _, err := postKey(api, "nv", "nvapi-secret-value"); err != nil {
		t.Fatal(err)
	}
	a := newTestAgent(t, api, t.TempDir())
	m := model{tab: &tab{agent: a}, w: 60, h: 30, chat: true}
	cmd := m.command("/key")
	if !m.menu.open || m.menu.kind != "key" {
		t.Fatal("/key should open a menu")
	}
	m, _ = send(t, m, cmd())
	got := strings.Join(menuLabels(m.menu), "|")
	if got != "nv|remove nv key|deepseek" {
		t.Fatalf("key rows: %s", got)
	}
	if m.menu.items[0].state != "sk-...3456" || m.menu.items[2].state != "not set" {
		t.Fatalf("masked state: %+v", m.menu.items)
	}
	for _, it := range m.menu.items {
		if strings.Contains(it.label+it.detail+it.state, "secret-value") {
			t.Fatal("the menu shows the secret")
		}
	}
	checkMenuFits(t, m)
	m, _ = send(t, m, keyMsg(tea.KeyEnd))
	m, _ = send(t, m, keyMsg(tea.KeyEnter))
	if string(m.input) != "/key deepseek " || m.menu.open {
		t.Fatalf("enter should prefill the input, got %q", string(m.input))
	}
	m.input = nil
	m.command("/key")
	m.fillMenu("key", keyMenuItems(keysInfo{Free: []keyRow{{ID: "nv", Set: "sk-...3456"}}}), nil)
	m.menuSelect(1)
	if res, ok := m.menuRun()().(keySetMsg); !ok || res.id != "nv" || res.err != nil || res.masked != "" {
		t.Fatalf("remove row: %+v", res)
	}
}

func TestResumeMenu(t *testing.T) {
	a := newTestAgent(t, "http://127.0.0.1:1/v1", t.TempDir())
	for i, title := range []string{"fix the login bug", "add dark mode"} {
		err := a.store.saveSession(session{ID: fmt.Sprintf("old-%d", i), Title: title, Updated: time.Now().Add(-time.Duration(i+1) * time.Hour),
			Messages: []Message{{Role: "system"}, {Role: "user", Content: title}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	m := model{tab: &tab{agent: a}, w: 60, h: 30, chat: true}
	m.command("/resume")
	if !m.menu.open || m.menu.kind != "resume" {
		t.Fatal("/resume should open a menu")
	}
	if got := strings.Join(menuLabels(m.menu), "|"); got != "fix the login bug|add dark mode" {
		t.Fatalf("resume rows: %s", got)
	}
	checkMenuFits(t, m)
	m, _ = send(t, m, keyMsg(tea.KeyDown))
	m, _ = send(t, m, keyMsg(tea.KeyEnter))
	if a.session != "old-1" || !strings.Contains(m.entries[len(m.entries)-1].text, "chat reopened") {
		t.Fatalf("enter should reopen old-1, session %q", a.session)
	}
}

func TestThemeMenuPreviewAndRestore(t *testing.T) {
	testEnv(t)
	m := model{tab: &tab{agent: &Agent{}}, w: 60, h: 30, chat: true, theme: 1}
	m.command("/theme")
	if !m.menu.open || len(m.menu.items) != len(themes) || m.menu.sel != 1 {
		t.Fatalf("/theme menu: open %v rows %d sel %d", m.menu.open, len(m.menu.items), m.menu.sel)
	}
	if got := menuLabels(m.menu); got[0] != themes[0].name || m.menu.items[1].state != "current" {
		t.Fatalf("theme rows: %v", got)
	}
	checkMenuFits(t, m)
	m, _ = send(t, m, keyMsg(tea.KeyDown))
	if m.theme != 2 {
		t.Fatalf("moving the highlight should preview theme 2, on %d", m.theme)
	}
	m, _ = send(t, m, keyMsg(tea.KeyEscape))
	if m.theme != 1 || m.menu.open {
		t.Fatalf("esc should restore theme 1, on %d", m.theme)
	}
	m.command("/theme")
	m, _ = send(t, m, keyMsg(tea.KeyDown))
	m, _ = send(t, m, keyMsg(tea.KeyDown))
	m, _ = send(t, m, keyMsg(tea.KeyEnter))
	if m.theme != 3 || loadTheme() != themes[3].name {
		t.Fatalf("enter should keep theme 3, on %d (saved %q)", m.theme, loadTheme())
	}
	m.command("/theme " + themes[0].name)
	if m.theme != 0 || m.menu.open {
		t.Fatal("/theme <name> should still switch directly")
	}
}

func TestFilesMouse(t *testing.T) {
	m := model{tab: &tab{agent: &Agent{}}, w: 100, h: 30, chat: true}
	m.fb = filesState{open: true, expanded: map[string]bool{}, nodes: []treeNode{{Path: "a", Dir: true}, {Path: "a/x.go"}, {Path: "b.go"}}}
	x, y, _, _ := m.overlayArea()
	lines, head, _, _ := m.filesLayout()
	first := y - lines + head
	m, _ = send(t, m, tea.MouseMotionMsg{X: x + 5, Y: first + 1})
	if m.fb.sel != 1 {
		t.Fatalf("hover should highlight b.go, sel %d", m.fb.sel)
	}
	m, _ = send(t, m, tea.MouseClickMsg{X: x + 5, Y: first, Button: tea.MouseLeft})
	if !m.fb.expanded["a"] || len(m.fb.rows()) != 3 {
		t.Fatalf("click on a folder should open it: %v", m.fb.expanded)
	}
	var text []string
	for i := 0; i < 200; i++ {
		text = append(text, fmt.Sprint("line ", i))
	}
	m.fb.view = &fileViewer{path: "big.txt", lines: text}
	m, _ = send(t, m, tea.MouseWheelMsg{X: x + 5, Y: y - 2, Button: tea.MouseWheelDown})
	if m.fb.view.top != 3 {
		t.Fatalf("wheel should scroll the viewer, top %d", m.fb.view.top)
	}
	m, _ = send(t, m, tea.MouseClickMsg{X: x + 5, Y: 0, Button: tea.MouseLeft})
	if m.fb.view != nil || !m.fb.open {
		t.Fatal("a click outside the viewer should go back to the tree")
	}
	m, _ = send(t, m, tea.MouseClickMsg{X: x + 5, Y: 0, Button: tea.MouseLeft})
	if m.fb.open {
		t.Fatal("a click outside the tree should close it")
	}
}
