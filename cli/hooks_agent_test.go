package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// How hooks can break inside a turn: a blocking before_bash hook still lets the command
// run, an untrusted or AI-edited hooks file runs anyway, after_edit output never reaches
// the AI, a failing must_pass check lets the turn end (or loops forever), checks run on
// turns that changed nothing, or a reopened chat shows the retry as the user's words.

func toolCall(id, name, args string) string {
	return `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"` + id + `","function":{"name":"` + name + `","arguments":` + args + `}}]}}]}`
}

func say(text string) func(http.ResponseWriter) string {
	return sseText(delta(`{"content":"`+text+`"}`), sseDone)
}

func call(id, name, args string) func(http.ResponseWriter) string {
	return sseText(toolCall(id, name, args), sseDone)
}

func hookAgent(t *testing.T, hooksJSON string, trust bool, replies ...func(http.ResponseWriter) string) (*Agent, *fakeRouter, string) {
	t.Helper()
	dir := t.TempDir()
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: replies}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	a := newTestAgent(t, srv.URL, dir)
	os.MkdirAll(filepath.Join(dir, ".prism"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, ".prism", "hooks.json"), []byte(hooksJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if trust {
		if _, err := trustHooks(dir); err != nil {
			t.Fatal(err)
		}
	}
	return a, fake, dir
}

func toolOutputs(a *Agent) map[string]string {
	out := map[string]string{}
	for _, m := range a.messages {
		if m.Role == "tool" {
			out[m.ToolCallID] = m.Content
		}
	}
	return out
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func TestHookBeforeBashBlocks(t *testing.T) {
	a, _, dir := hookAgent(t, `{"before_bash": ["case \"$PRISM_COMMAND\" in *rm*) echo no deleting here; exit 1;; esac"]}`, true,
		call("b1", "bash", `"{\"command\":\"touch a.txt\"}"`),
		call("b2", "bash", `"{\"command\":\"rm -f keep.txt\"}"`),
		say("ok"))
	os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("x"), 0o644)
	if _, err := collect(a, "tidy up"); err != nil {
		t.Fatal(err)
	}
	if !exists(dir, "a.txt") || !exists(dir, "keep.txt") {
		t.Fatalf("a.txt must be made and keep.txt kept: a=%v keep=%v", exists(dir, "a.txt"), exists(dir, "keep.txt"))
	}
	if out := toolOutputs(a)["b2"]; !strings.Contains(out, "stopped this command") || !strings.Contains(out, "no deleting here") {
		t.Errorf("the AI must hear why the command was stopped: %q", out)
	}
}

func TestHookTurnEndKeepsWorkingUntilItPasses(t *testing.T) {
	a, fake, dir := hookAgent(t, `{
		"after_edit": [{"run": "echo formatted $PRISM_FILE", "match": "*.txt"}],
		"turn_end": [{"run": "test -f fixed.txt || { echo fixed.txt is missing; exit 1; }", "must_pass": true}]}`, true,
		call("w1", "write", `"{\"path\":\"a.txt\",\"content\":\"a\"}"`),
		say("done"),
		call("w2", "write", `"{\"path\":\"fixed.txt\",\"content\":\"ok\"}"`),
		say("done now"),
		say("hello"))
	events, err := collect(a, "make the files")
	if err != nil {
		t.Fatal(err)
	}
	if !exists(dir, "fixed.txt") {
		t.Fatalf("the failed check must send the AI back to work")
	}
	if out := toolOutputs(a)["w1"]; !strings.Contains(out, "formatted a.txt") {
		t.Errorf("after_edit output must reach the AI: %q", out)
	}
	retries, hookSteps := 0, 0
	for _, m := range a.messages {
		if m.Role == "user" && strings.HasPrefix(m.Content, hookRetryPrefix) {
			retries++
			if !strings.Contains(m.Content, "fixed.txt is missing") {
				t.Errorf("the retry must carry the check's output: %q", m.Content)
			}
		}
	}
	for _, ev := range events {
		if s, ok := ev.(evToolStart); ok && s.name == "hooks" {
			hookSteps++
		}
	}
	if retries != 1 || hookSteps != 2 {
		t.Errorf("want one retry and two check steps, got %d and %d", retries, hookSteps)
	}
	for _, e := range entriesFrom(a.messages) {
		if e.kind == "user" && strings.HasPrefix(e.text, hookRetryPrefix) {
			t.Errorf("a reopened chat must show the retry as a note, not as the user's words")
		}
	}

	events, err = collect(a, "say hi")
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if s, ok := ev.(evToolStart); ok && s.name == "hooks" {
			t.Errorf("a turn that changed nothing must not run the checks")
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.seen) != 5 {
		t.Errorf("router saw %d requests, want 5", len(fake.seen))
	}
}

func TestHookTurnEndGivesUpAfterThreeTries(t *testing.T) {
	a, _, _ := hookAgent(t, `{"turn_end": [{"run": "echo still broken; exit 1", "must_pass": true}]}`, true,
		call("w1", "write", `"{\"path\":\"a.txt\",\"content\":\"a\"}"`),
		say("done"), say("done"), say("done"), say("done"))
	events, err := collect(a, "go")
	if err != nil {
		t.Fatal(err)
	}
	if !hasEvent(events, func(ev any) bool {
		n, ok := ev.(evNote)
		return ok && strings.Contains(n.text, "still fail after 3 tries")
	}) {
		t.Errorf("after 3 tries prism must stop and say so")
	}
}

func TestHookUntrustedOrRewrittenNeverRuns(t *testing.T) {
	a, _, dir := hookAgent(t, `{"before_bash": ["exit 1"], "turn_end": ["touch end.txt"]}`, false,
		call("b1", "bash", `"{\"command\":\"touch a.txt\"}"`),
		say("ok"),
		call("b2", "bash", `"{\"command\":\"touch b.txt\"}"`),
		say("ok"))
	notes := 0
	for range 2 {
		events, err := collect(a, "go")
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range events {
			if n, ok := ev.(evNote); ok && strings.Contains(n.text, "/hooks trust") {
				notes++
			}
		}
	}
	if !exists(dir, "a.txt") || !exists(dir, "b.txt") || exists(dir, "end.txt") {
		t.Errorf("untrusted hooks must not run: a=%v b=%v end=%v", exists(dir, "a.txt"), exists(dir, "b.txt"), exists(dir, "end.txt"))
	}
	if notes != 1 {
		t.Errorf("say once that hooks are off, said %d times", notes)
	}

	b, _, dir2 := hookAgent(t, `{"turn_end": ["touch end.txt"]}`, true,
		call("w1", "write", `"{\"path\":\".prism/hooks.json\",\"content\":\"{\\\"turn_end\\\": [\\\"touch evil.txt\\\"]}\"}"`),
		say("ok"))
	if _, err := collect(b, "go"); err != nil {
		t.Fatal(err)
	}
	if exists(dir2, "evil.txt") || exists(dir2, "end.txt") {
		t.Errorf("a hooks file the AI rewrote must not run until trusted again: evil=%v end=%v", exists(dir2, "evil.txt"), exists(dir2, "end.txt"))
	}
}

func TestHookRetryKeepsTheCache(t *testing.T) {
	a, fake, _ := hookAgent(t, `{"turn_end": [{"run": "test -f fixed.txt || { echo missing; exit 1; }", "must_pass": true}]}`, true,
		call("w1", "write", `"{\"path\":\"a.txt\",\"content\":\"a\"}"`),
		say("done"),
		call("w2", "write", `"{\"path\":\"fixed.txt\",\"content\":\"ok\"}"`),
		say("done now"),
		say("hi"))
	a.hub = newTabHub()
	a.tabID = a.hub.add("this one")
	a.hub.add("another tab")
	if _, err := collect(a, "make the files"); err != nil {
		t.Fatal(err)
	}
	if _, err := collect(a, "thanks"); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	retry := func(msgs []Message) string {
		for _, m := range msgs {
			if m.Role == "user" && strings.HasPrefix(m.Content, hookRetryPrefix) {
				return m.Content
			}
		}
		return ""
	}
	during, after := retry(fake.seen[3].messages), retry(fake.seen[4].messages)
	if during == "" || during != after {
		t.Errorf("the retry message must be sent the same way every time so the cache holds:\n%q\n%q", during, after)
	}
}

func TestHooksFileMustBeASmallRegularFile(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".prism"), 0o755)
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skip("no fifo here:", err)
	}
	if err := os.Symlink(fifo, filepath.Join(dir, ".prism", "hooks.json")); err != nil {
		t.Fatal(err)
	}
	done := make(chan hookSet, 1)
	go func() { done <- loadHooks(dir) }()
	select {
	case h := <-done:
		if h.problem == "" || h.active() {
			t.Errorf("a hooks.json that is not a plain file must be refused: %+v", h)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reading a hooks.json that points at a pipe hung prism")
	}
	os.Remove(filepath.Join(dir, ".prism", "hooks.json"))
	big := `{"turn_end": ["true"], "x": "` + strings.Repeat("a", 70<<10) + `"}`
	os.WriteFile(filepath.Join(dir, ".prism", "hooks.json"), []byte(big), 0o644)
	if h := loadHooks(dir); h.problem == "" || !strings.Contains(h.problem, "too big") {
		t.Errorf("a huge hooks.json must be refused, got %q", h.problem)
	}
}

func TestHookChangedMidTurnStopsCommands(t *testing.T) {
	a, _, dir := hookAgent(t, `{"before_bash": ["true"], "turn_end": [{"run": "true", "must_pass": true}]}`, true,
		call("w1", "write", `"{\"path\":\".prism/hooks.json\",\"content\":\"{}\"}"`),
		call("b1", "bash", `"{\"command\":\"touch ran.txt\"}"`),
		say("done"))
	events, err := collect(a, "go")
	if err != nil {
		t.Fatal(err)
	}
	if exists(dir, "ran.txt") {
		t.Errorf("after the hooks file changed mid-turn, commands must wait for /hooks trust")
	}
	if out := toolOutputs(a)["b1"]; !strings.Contains(out, "/hooks trust") {
		t.Errorf("the AI must hear why: %q", out)
	}
	if !hasEvent(events, func(ev any) bool { n, ok := ev.(evNote); return ok && strings.Contains(n.text, "changed") }) {
		t.Errorf("the user must hear that the turn-end checks were skipped because the file changed")
	}
}

func TestHookBlockedCommandIsNotAChange(t *testing.T) {
	a, _, dir := hookAgent(t, `{"before_bash": ["exit 1"], "turn_end": ["touch end.txt"]}`, true,
		call("b1", "bash", `"{\"command\":\"ls\"}"`),
		say("ok"))
	if _, err := collect(a, "look"); err != nil {
		t.Fatal(err)
	}
	if exists(dir, "end.txt") {
		t.Errorf("a turn whose only command was stopped changed nothing, so turn_end must not run")
	}
}

func TestHooksTrustWaitsWhileBusy(t *testing.T) {
	m := suggestModel(t, 80, 24)
	dir := t.TempDir()
	m.agent.dir = dir
	os.MkdirAll(filepath.Join(dir, ".prism"), 0o755)
	os.WriteFile(filepath.Join(dir, ".prism", "hooks.json"), []byte(`{"turn_end": ["true"]}`), 0o644)
	m.busy = true
	m.command("/hooks trust")
	if loadHooks(dir).trusted {
		t.Errorf("/hooks trust while prism works must wait: the AI could swap the file in between")
	}
}

func TestHooksTrustWaitsWhileAnyTabWorks(t *testing.T) {
	m := suggestModel(t, 80, 24)
	dir := t.TempDir()
	m.agent.dir = dir
	os.MkdirAll(filepath.Join(dir, ".prism"), 0o755)
	os.WriteFile(filepath.Join(dir, ".prism", "hooks.json"), []byte(`{"turn_end": ["true"]}`), 0o644)
	other := &tab{agent: &Agent{dir: dir}, busy: true}
	m.tabs = []*tab{other, m.tab}
	m.command("/hooks trust")
	if loadHooks(dir).trusted {
		t.Errorf("another tab on this project is working, so /hooks trust must wait")
	}
}

func TestHookCommandThatNeverRanIsNotAChange(t *testing.T) {
	a, _, dir := hookAgent(t, `{"turn_end": ["touch end.txt"]}`, true,
		call("b1", "bash", `"{\"command\":\"  \"}"`),
		call("b2", "bash", `"not json"`),
		say("ok"))
	if _, err := collect(a, "look"); err != nil {
		t.Fatal(err)
	}
	if exists(dir, "end.txt") {
		t.Errorf("commands that never ran changed nothing, so turn_end must not run")
	}
}

func TestWebHooksTrustWaitsForWorkingTabs(t *testing.T) {
	h, s, port := testServerWithHub(t)
	host := "127.0.0.1:" + port
	os.MkdirAll(filepath.Join(s.agent.dir, ".prism"), 0o755)
	os.WriteFile(filepath.Join(s.agent.dir, ".prism", "hooks.json"), []byte(`{"turn_end": ["true"]}`), 0o644)
	body, _ := json.Marshal(map[string]string{"text": "/hooks trust"})
	rec := do(t, h, http.MethodPost, "/api/command", host, "http://"+host, string(body))
	if res := decode(t, rec); res["kind"] != "error" || loadHooks(s.agent.dir).trusted {
		t.Errorf("/hooks trust from the web while a terminal tab works must wait, got %v", res)
	}
}

func TestHookStopIsNotAChangedFile(t *testing.T) {
	a, _, _ := hookAgent(t, `{"before_bash": ["true"]}`, true)
	a.hooks = loadHooks(a.dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := a.bashWithHooks(ctx, `{"command":"ls"}`); strings.Contains(r.output, "changed") {
		t.Errorf("a stopped turn must not blame a changed hooks file: %q", r.output)
	}
}
