package main

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestSteerAfterToolBeforeNextCall(t *testing.T) {
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
		sseText(delta(`{"tool_calls":[{"index":0,"id":"one","function":{"name":"list","arguments":"{}"}}]}`), sseDone),
		sseText(delta(`{"content":"corrected"}`), sseDone),
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a := newTestAgent(t, srv.URL, t.TempDir())
	a.Run(context.Background(), "list files", func(ev any) {
		if _, ok := ev.(evToolStart); ok {
			a.Steer("only markdown")
		}
	})
	if len(fake.seen) != 2 {
		t.Fatalf("calls: %d", len(fake.seen))
	}
	msgs := fake.seen[1].messages
	if msgs[len(msgs)-1].Role != "user" || msgs[len(msgs)-1].Content != "only markdown" || msgs[len(msgs)-2].Role != "tool" {
		t.Fatalf("correction not after results: %+v", msgs)
	}
}
func TestSteerAfterLastCallStartsNextTurn(t *testing.T) {
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
		sseText(delta(`{"content":"first finished"}`), sseDone), sseText(delta(`{"content":"second finished"}`), sseDone)}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a := newTestAgent(t, srv.URL, t.TempDir())
	sent := false
	follows := 0
	a.Run(context.Background(), "first", func(ev any) {
		if _, ok := ev.(evText); ok && !sent {
			sent = true
			a.Steer("late correction")
		}
		if _, ok := ev.(evFollowup); ok {
			follows++
		}
	})
	if len(fake.seen) != 2 || follows != 1 {
		t.Fatalf("calls=%d followups=%d", len(fake.seen), follows)
	}
	msgs := fake.seen[1].messages
	if msgs[len(msgs)-1].Content != "late correction" {
		t.Fatalf("missing next prompt: %+v", msgs)
	}
}
func TestQueuedPromptsRunInOrder(t *testing.T) {
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
		sseText(delta(`{"content":"one"}`), sseDone),
		sseText(delta(`{"content":"two"}`), sseDone),
		sseText(delta(`{"content":"three"}`), sseDone),
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a := newTestAgent(t, srv.URL, t.TempDir())
	queued := false
	a.Run(context.Background(), "first prompt", func(ev any) {
		if _, ok := ev.(evText); ok && !queued {
			queued = true
			a.Queue("second prompt")
			a.Queue("third prompt")
		}
	})
	if len(fake.seen) != 3 {
		t.Fatalf("calls=%d", len(fake.seen))
	}
	for i, want := range []string{"first prompt", "second prompt", "third prompt"} {
		msgs := fake.seen[i].messages
		if got := msgs[len(msgs)-1].Content; got != want {
			t.Fatalf("call %d prompt=%q want %q", i, got, want)
		}
	}
}

func TestQueuedPromptOrderAndStopRestore(t *testing.T) {
	a := &Agent{}
	a.openSteering()
	a.Queue("first")
	a.Queue("second")
	a.Queue("third")
	a.steerMu.Lock()
	first, _ := a.takePrompt()
	second, _ := a.takePrompt()
	a.steerMu.Unlock()
	if first != "first" || second != "second" {
		t.Fatal("queue reordered")
	}
	if got := a.restorePrompts(); got != "third" {
		t.Fatalf("restore=%q", got)
	}
	if a.queuedPrompts() != 0 {
		t.Fatal("restored queue not cleared")
	}
}
func TestNotificationOnlyWhenBlurred(t *testing.T) {
	testEnv(t)
	if notification(false, "done") != nil {
		t.Fatal("focused notification")
	}
	if notification(true, "done") == nil {
		t.Fatal("no blurred notification")
	}
	m := tabTestModel(t, 80, 24, 1)
	m.command("/notify off")
	if !m.notifyOff || loadSetting("notify") != "off" || notification(!m.notifyOff, "done") != nil {
		t.Fatal("disabled notification")
	}
}

func TestQueueLoginAndSlashSafety(t *testing.T) {
	for _, busy := range []bool{false, true} {
		m := tabTestModel(t, 80, 24, 1)
		m.busy, m.loginWaiting, m.input = busy, "grok", []rune("secret-code")
		next, cmd := m.key(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
		got := next.(model)
		if cmd == nil || len(got.entries) != 0 || len(got.input) != 0 || got.agent.restorePrompts() != "" {
			t.Fatal("alt+enter did not use the login path")
		}
	}
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEnter, Mod: tea.ModAlt}, keyPress("ctrl+q")} {
		m := tabTestModel(t, 80, 24, 1)
		m.busy, m.input = true, []rune("/quiet off")
		next, _ := m.key(key)
		if next.(model).agent.queuedPrompts() != 0 {
			t.Fatal("slash command queued")
		}
	}
	if (&Agent{}).Queue(" /custom args") {
		t.Fatal("agent accepted a slash command")
	}
}

func TestSteerFailedTurnDoesNotChain(t *testing.T) {
	a := newTestAgent(t, "", t.TempDir())
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			a.Queue("leave for later")
		}
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()
	a.api = srv.URL
	var doneErr error
	a.Run(context.Background(), "first", func(ev any) {
		switch e := ev.(type) {
		case evFollowup:
			t.Error("failed turn chained")
		case evDone:
			doneErr = e.err
		}
	})
	if doneErr == nil || calls != 3 || a.restorePrompts() != "leave for later" {
		t.Fatalf("err=%v calls=%d", doneErr, calls)
	}
	if a.Steer("too late") || a.Queue("too late") {
		t.Fatal("closed run accepted text")
	}
}

func TestSteerWebStopRestoresAcceptedPrompts(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	}))
	defer srv.Close()
	a := newTestAgent(t, srv.URL, t.TempDir())
	s := &server{agent: a}
	out := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		s.chat(out, httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"message":"first"}`)))
	}()
	<-entered
	for _, body := range []string{`{"message":"correction"}`, `{"message":"later","mode":"queue"}`} {
		w := httptest.NewRecorder()
		s.chat(w, httptest.NewRequest("POST", "/api/chat", strings.NewReader(body)))
		if w.Code != http.StatusOK {
			t.Errorf("accept: %d %s", w.Code, w.Body.String())
		}
	}
	s.stop(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/stop", nil))
	close(release)
	<-finished
	if !strings.Contains(out.Body.String(), `"restore":"correction\n\nlater"`) {
		t.Fatalf("missing restored prompts: %s", out.Body.String())
	}
	if a.restorePrompts() != "" || a.Steer("late") {
		t.Fatal("pending prompts leaked or closed steering accepted")
	}
	a.drainSteers()
	for _, msg := range a.messages {
		if msg.Content == "correction" || msg.Content == "later" {
			t.Fatal("leftovers leaked into history")
		}
	}
}

func TestQueueFollowupResetsTurnCounters(t *testing.T) {
	m := tabTestModel(t, 80, 24, 1)
	m.now = 5 * time.Second
	m.turnTools, m.turnTests, m.turnFailed = 8, true, true
	m.handle(evFollowup{text: "next"})
	if m.turnTools != 0 || m.turnTests || m.turnFailed || m.busySince != m.now {
		t.Fatal("TUI follow-up retained turn counters")
	}
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{
		sseText(delta(`{"tool_calls":[{"index":0,"id":"one","function":{"name":"list","arguments":"{}"}}]}`), sseDone),
		sseText(delta(`{"content":"first done"}`), sseDone),
		sseText(delta(`{"content":"second done"}`), sseDone),
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a := newTestAgent(t, srv.URL, t.TempDir())
	a.openSteering()
	a.Queue("follow up")
	s := &server{agent: a}
	out := httptest.NewRecorder()
	s.chat(out, httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"message":"first"}`)))
	if !strings.Contains(out.Body.String(), `"tools":0`) || !strings.Contains(out.Body.String(), `"type":"followup"`) {
		t.Fatalf("web counters: %s", out.Body.String())
	}
}

func TestSteerIdleAgentRejectsInput(t *testing.T) {
	a := &Agent{}
	if a.Steer("correction") || a.Queue("later") || a.restorePrompts() != "" {
		t.Fatal("idle agent accepted input")
	}
}

func TestSteerEnterDuringAskDoesNotSteerIdleAgent(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.Write([]byte(sseText(delta(`{"content":"answer"}`), sseDone)(w)))
	}))
	defer srv.Close()
	defer close(release)
	m := tabTestModel(t, 80, 24, 1)
	m.agent.api = srv.URL
	m.events = make(chan any, 128)
	m.askCommand([]string{"/ask", "1", "question"})
	<-entered
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, {Code: tea.KeyEnter, Mod: tea.ModAlt}} {
		m.input = []rune("unrelated input")
		next, _ := m.key(key)
		m = next.(model)
		if string(m.input) != "unrelated input" || m.agent.restorePrompts() != "" {
			t.Fatal("/ask consumed input for the idle tab agent")
		}
	}
}

func TestSteerEnterDuringCompactDoesNotSteerIdleAgent(t *testing.T) {
	a := newTestAgent(t, "", t.TempDir())
	a.messages = append(a.messages, Message{Role: "user", Content: "first"})
	s := &server{agent: a}
	s.memWG.Add(1) // Hold compaction after it has marked the server busy.
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		r := httptest.NewRequest("POST", "/api/command", nil)
		ctx, cancel := context.WithCancel(r.Context())
		cancel() // Avoid a model request once the compaction barrier is released.
		s.compact(httptest.NewRecorder(), r.WithContext(ctx), a, "")
	}()
	defer func() { s.memWG.Done(); <-finished }()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		s.mu.Lock()
		busy := s.busy
		s.mu.Unlock()
		if busy {
			break
		}
		select {
		case <-deadline:
			t.Fatal("compaction did not start")
		case <-tick.C:
		}
	}
	for _, mode := range []string{"steer", "queue"} {
		w := httptest.NewRecorder()
		s.chat(w, httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"message":"unrelated input","mode":"`+mode+`"}`)))
		if w.Code != http.StatusConflict || a.restorePrompts() != "" {
			t.Fatalf("compaction accepted input: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestSteerCanceledTurnClosesAcceptance(t *testing.T) {
	a := newTestAgent(t, "", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.openSteering()
	a.Queue("later")
	a.Run(ctx, "first", func(ev any) {
		if e, ok := ev.(evDone); ok && !errors.Is(e.err, context.Canceled) {
			t.Errorf("err=%v", e.err)
		}
	})
	if a.Queue("late") {
		t.Fatal("accepted after canceled run")
	}
}
