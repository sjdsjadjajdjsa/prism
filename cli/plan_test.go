package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// How plan mode can break: a change slips through (write, edit, bash), reading gets
// blocked too, the plan rules never reach the model or change between requests (the
// prompt cache breaks), the rules leak into the saved chat, the model is never told plan
// mode ended, the run choice doesn't start the work, or the picker never shows.

func lastUser(msgs []Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

func TestPlanModeRefusesChangesAndTellsTheModel(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "shop.py"), []byte("def total(): pass\n"), 0o644)
	calls := `{"choices":[{"delta":{"tool_calls":[` +
		`{"index":0,"id":"c1","function":{"name":"read","arguments":"{\"path\":\"shop.py\"}"}},` +
		`{"index":1,"id":"c2","function":{"name":"write","arguments":"{\"path\":\"out.txt\",\"content\":\"x\"}"}},` +
		`{"index":2,"id":"c3","function":{"name":"bash","arguments":"{\"command\":\"touch ran.txt\"}"}}]}}]}`
	write := `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c4","function":{"name":"write","arguments":"{\"path\":\"out.txt\",\"content\":\"x\"}"}}]}}]}`
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
		sseText("data: "+calls, sseDone),
		sseText(delta(`{"content":"Plan: 1. write out.txt"}`), sseDone),
		sseText("data: "+write, sseDone),
		sseText(delta(`{"content":"Done."}`), sseDone),
		sseText(delta(`{"content":"You're welcome."}`), sseDone),
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a := newTestAgent(t, srv.URL, dir)

	if on, _, msg := planSwitch(a, ""); !on || msg != "" {
		t.Fatalf("/plan should switch plan mode on and send nothing, got on=%v msg=%q", on, msg)
	}
	if _, err := collect(a, "make out.txt"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"out.txt", "ran.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("plan mode let %s be created", f)
		}
	}
	tools := map[string]string{}
	for _, m := range a.messages {
		if m.Role == "tool" {
			tools[m.ToolCallID] = m.Content
		}
	}
	if strings.Contains(tools["c1"], "not in plan mode") || !strings.Contains(tools["c1"], "def total") {
		t.Errorf("read must work in plan mode, got %q", tools["c1"])
	}
	for _, id := range []string{"c2", "c3"} {
		if !strings.HasPrefix(tools[id], "Error: not in plan mode") {
			t.Errorf("call %s was not refused: %q", id, tools[id])
		}
	}
	fake.mu.Lock()
	first, second := lastUser(fake.seen[0].messages), lastUser(fake.seen[1].messages)
	fake.mu.Unlock()
	if !strings.HasSuffix(first, planOnNote) || first != second {
		t.Fatalf("every request of a planned turn must carry the same plan note:\n%q\n%q", first, second)
	}
	if a.messages[1].Content != "make out.txt" {
		t.Errorf("the note leaked into the saved message: %q", a.messages[1].Content)
	}
	for _, e := range entriesFrom(a.messages) {
		if e.kind == "tool" && e.tool != "read" && (e.res.ok || e.res.summary != "not in plan mode") {
			t.Errorf("a reopened chat must show the refused %s as not in plan mode, got ok=%v %q", e.tool, e.res.ok, e.res.summary)
		}
	}

	if on, _, msg := planSwitch(a, "run"); on || msg != planGo {
		t.Fatalf("run must leave plan mode and send the go message, got on=%v msg=%q", on, msg)
	}
	if _, err := collect(a, planGo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.txt")); err != nil {
		t.Errorf("after run the write must happen: %v", err)
	}
	fake.mu.Lock()
	run := fake.seen[2].messages
	fake.mu.Unlock()
	if !strings.HasSuffix(lastUser(run), planOffNote) {
		t.Errorf("the first message after plan mode must say it is off: %q", lastUser(run))
	}
	if run[1].Content != first {
		t.Errorf("the planned message must be resent byte for byte so the cache holds:\n%q\n%q", run[1].Content, first)
	}

	if _, err := collect(a, "thanks"); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	thanks := fake.seen[4].messages
	fake.mu.Unlock()
	if lastUser(thanks) != "thanks" {
		t.Errorf("the off note is sent once, got %q", lastUser(thanks))
	}
	sess, err := a.store.loadSession(a.session)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(sess.Messages)
	if strings.Contains(string(data), planOnNote) || strings.Contains(string(data), planOffNote) {
		t.Errorf("the saved chat must not keep the plan notes")
	}
}

func TestPlanWorkersAndResetIgnorePlanMode(t *testing.T) {
	a := &Agent{role: "worker"}
	a.plan.Store(true)
	if a.planNote() != "" {
		t.Errorf("crew workers never get the plan note")
	}
	b := newTestAgent(t, "http://127.0.0.1:1", t.TempDir())
	b.plan.Store(true)
	b.planNote()
	b.reset()
	if b.plan.Load() || b.planNote() != "" {
		t.Errorf("/new must leave plan mode and forget it")
	}
}

// planTUI is a chat whose agent talks to fake, with the tab registered so its events land.
func planTUI(t *testing.T, fake *fakeRouter) model {
	t.Helper()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	a := newTestAgent(t, srv.URL, t.TempDir())
	m := model{tab: &tab{agent: a, events: make(chan any, 64)}, w: 90, h: 30, chat: true, ticking: true, chatAt: -time.Hour, now: time.Second, lastTextAt: -time.Hour, sayAt: -time.Hour}
	m.tabs = []*tab{m.tab}
	return m
}

// drain feeds the running turn's events to the model until it is done.
func drain(t *testing.T, m model) model {
	t.Helper()
	for {
		select {
		case ev := <-m.events:
			next, _ := m.Update(tabMsg{t: m.tab, event: ev})
			m = next.(model)
			if _, ok := ev.(evSaved); ok {
				return m
			}
		case <-time.After(20 * time.Second):
			t.Fatal("the turn never finished")
		}
	}
}

func TestPlanTerminalFlow(t *testing.T) {
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
		sseText(delta(`{"content":"Plan: 1. fix total() in shop.py"}`), sseDone),
		sseText(delta(`{"content":"Fixed."}`), sseDone),
	}}
	m := planTUI(t, fake)
	m = typeText(t, m, "/plan")
	m = hit(t, m, tea.KeyEnter)
	m = hit(t, m, tea.KeyEnter)
	if !m.agent.plan.Load() {
		t.Fatalf("/plan did not switch plan mode on")
	}
	if screen := crewScreen(m); !strings.Contains(screen, "plan mode") {
		t.Errorf("the status bar must show plan mode:\n%s", screen)
	}
	m = typeText(t, m, "fix the total")
	m = drain(t, hit(t, m, tea.KeyEnter))
	if !m.menu.open || m.menu.kind != "plan" {
		t.Fatalf("a planned turn must end with the plan picker, menu=%+v", m.menu)
	}
	m = hit(t, m, tea.KeyEscape)
	if m.menu.open || !m.agent.plan.Load() {
		t.Fatalf("esc keeps planning: menu open=%v plan=%v", m.menu.open, m.agent.plan.Load())
	}
	m.openPlanMenu()
	m = drain(t, hit(t, m, tea.KeyEnter))
	if m.agent.plan.Load() {
		t.Errorf("run this plan must leave plan mode")
	}
	var users []string
	for _, e := range m.entries {
		if e.kind == "user" {
			users = append(users, e.text)
		}
	}
	if len(users) != 2 || users[1] != planGo {
		t.Errorf("run this plan must send the go message, sent %q", users)
	}
	if m.menu.open {
		t.Errorf("the picker must not come back once plan mode is off")
	}
}

func TestPlanWebCommand(t *testing.T) {
	h, s, port := testServer(t)
	host := "127.0.0.1:" + port
	post := func(text string) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"text": text})
		rec := do(t, h, http.MethodPost, "/api/command", host, "http://"+host, string(body))
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %q: %d %s", text, rec.Code, rec.Body.String())
		}
		return decode(t, rec)
	}
	if res := post("/plan"); res["kind"] != "plan" || res["plan"] != true || res["text"] == "" {
		t.Fatalf("/plan = %v", res)
	}
	if rec := do(t, h, http.MethodGet, "/api/info", host, "", ""); !strings.Contains(rec.Body.String(), `"plan":true`) {
		t.Errorf("/api/info must say plan mode is on: %s", rec.Body.String())
	}
	if res := post("/plan add a login page"); res["plan"] != true || res["message"] != "add a login page" {
		t.Errorf("/plan <task> = %v", res)
	}
	if res := post("/plan run"); res["plan"] != false || res["message"] != planGo || s.agent.plan.Load() {
		t.Errorf("/plan run = %v", res)
	}
	s.mu.Lock()
	s.busy = true
	s.mu.Unlock()
	if res := post("/plan"); res["kind"] != "error" || s.agent.plan.Load() {
		t.Errorf("/plan while busy must wait, got %v", res)
	}
}

func TestPlanPickerWaitsForEmptyInputAndPlanMode(t *testing.T) {
	m := suggestModel(t, 80, 24)
	m.agent.plan.Store(true)
	m.planAsk, m.input = true, []rune("one more thing")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if m = next.(model); m.menu.open {
		t.Fatalf("the plan picker must not take the keys while you type")
	}
	m.input = nil
	m.agent.plan.Store(false)
	next, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if m = next.(model); m.menu.open {
		t.Errorf("plan mode was turned off meanwhile, so there is no plan to run")
	}
}

func TestPlanTaskKeepsItsTextAndRefusesCommands(t *testing.T) {
	a := &Agent{}
	if _, text, msg := planSwitch(a, "/new"); msg != "" || text == "" || a.plan.Load() {
		t.Errorf("/plan /new must not send /new as a task: text=%q msg=%q", text, msg)
	}
	m := suggestModel(t, 80, 24)
	m.command("/plan add a page\nwith a form")
	if !m.agent.plan.Load() {
		t.Fatalf("/plan <task> must switch plan mode on")
	}
	var sent string
	for _, e := range m.entries {
		if e.kind == "user" {
			sent = e.text
		}
	}
	if sent != "add a page\nwith a form" {
		t.Errorf("the task must be sent as typed, got %q", sent)
	}
}

func TestPlanNoteSurvivesCompaction(t *testing.T) {
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
		sseText(delta(`{"content":"summary of the work"}`), sseDone),
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a := newTestAgent(t, srv.URL, t.TempDir())
	a.plan.Store(true)
	a.messages = append(a.messages, Message{Role: "user", Content: "plan the login page", Note: a.planNote()})
	if err := a.compact(context.Background(), func(any) {}); err != nil {
		t.Fatal(err)
	}
	if got := lastUser(withNotes(a.messages)); !strings.HasSuffix(got, planOnNote) {
		t.Errorf("a planned turn that gets compacted must keep the plan rules: %q", got)
	}
}

func TestWebPlanWaitsForItsWorkingTab(t *testing.T) {
	h, s, port := testServerWithHub(t)
	host := "127.0.0.1:" + port
	var working int
	for _, tb := range s.hub.list() {
		if tb.Status == "working" {
			working = tb.ID
		}
	}
	body, _ := json.Marshal(map[string]string{"text": "/plan"})
	rec := do(t, h, http.MethodPost, fmt.Sprintf("/api/command?tab=%d", working), host, "http://"+host, string(body))
	if res := decode(t, rec); res["kind"] != "error" || s.agentFor(working).plan.Load() {
		t.Errorf("/plan on a tab that is working must wait, got %v", res)
	}
	rec = do(t, h, http.MethodPost, "/api/command", host, "http://"+host, string(body))
	if res := decode(t, rec); res["kind"] != "plan" || !s.agent.plan.Load() {
		t.Errorf("/plan on an idle tab works while another tab is busy, got %v", res)
	}
	s.agent.plan.Store(false)
	s.agent.tabID = working
	rec = do(t, h, http.MethodPost, "/api/command", host, "http://"+host, string(body))
	if res := decode(t, rec); res["kind"] != "error" || s.agent.plan.Load() {
		t.Errorf("the page's own tab (no ?tab=) is working, so /plan must wait, got %v", res)
	}
	rec = do(t, h, http.MethodPost, "/api/command?tab=bad", host, "http://"+host, string(body))
	if res := decode(t, rec); res["kind"] != "error" || s.agent.plan.Load() {
		t.Errorf("a bad ?tab= falls back to the page's own tab, which is working, got %v", res)
	}
}

func TestPlanOffNoteSurvivesCompaction(t *testing.T) {
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
		sseText(delta(`{"content":"summary"}`), sseDone),
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a := newTestAgent(t, srv.URL, t.TempDir())
	a.plan.Store(true)
	a.messages = append(a.messages, Message{Role: "user", Content: "plan it", Note: a.planNote()})
	a.plan.Store(false)
	a.messages = append(a.messages, Message{Role: "user", Content: planGo, Note: a.planNote()})
	if err := a.compact(context.Background(), func(any) {}); err != nil {
		t.Fatal(err)
	}
	if got := lastUser(withNotes(a.messages)); !strings.HasSuffix(got, planOffNote) {
		t.Errorf("compacting the first turn after plan mode must still say it is off: %q", got)
	}
}
