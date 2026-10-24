package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// How asking first can break: a risky command runs without a yes, a plain command asks (noise),
// "no" still runs it, "yes for this chat" doesn't stick or sticks past /new, a block rule asks
// instead of blocking, nobody-to-ask runs it anyway, prism fix stops asking for every command,
// or the web never gets the question or its answer never reaches the turn.

func riskAgent(t *testing.T, replies ...func(http.ResponseWriter) string) (*Agent, string, *[]approval, *[]answer) {
	t.Helper()
	dir := t.TempDir()
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: replies}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	a := newTestAgent(t, srv.URL, dir)
	var asked []approval
	answers := &[]answer{}
	a.confirm = func(ctx context.Context, q approval) answer {
		asked = append(asked, q)
		if len(*answers) == 0 {
			return answerNo
		}
		next := (*answers)[0]
		*answers = (*answers)[1:]
		return next
	}
	return a, dir, &asked, answers
}

func bashCall(id, command string) func(http.ResponseWriter) string {
	args, _ := json.Marshal(map[string]string{"command": command})
	quoted, _ := json.Marshal(string(args))
	return call(id, "bash", string(quoted))
}

func TestRiskyCommandsAskAndPlainOnesDont(t *testing.T) {
	a, dir, asked, answers := riskAgent(t,
		bashCall("b1", "mkdir -p src && touch src/keep.txt && ls"),
		bashCall("b2", "rm -rf src"),
		bashCall("b3", "rm -rf src"),
		bashCall("b4", "mkdir -p src && rm -r src && touch again.txt"),
		say("done"))
	*answers = []answer{answerNo, answerChat}
	if _, err := collect(a, "clean up"); err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 2 {
		t.Fatalf("want 2 questions (rm said no, rm allowed for the chat), got %d: %+v", len(*asked), *asked)
	}
	q := (*asked)[0]
	if q.tool != "bash" || q.arg != "rm -rf src" || q.why == "" || len(q.risks) == 0 {
		t.Errorf("the question must show the command, why, and offer yes for this chat: %+v", q)
	}
	out := toolOutputs(a)
	if out["b2"] != saidNo {
		t.Errorf("no must stop the command: %q", out["b2"])
	}
	if exists(dir, "src") || !exists(dir, "again.txt") {
		t.Errorf("yes for this chat must let the same kind run again without asking: src=%v again=%v", exists(dir, "src"), exists(dir, "again.txt"))
	}
	a.reset()
	if len(a.allowedRisks) != 0 {
		t.Errorf("/new must forget what was allowed for the chat")
	}
}

func TestRulesBlockAndAskFirst(t *testing.T) {
	a, dir, asked, answers := riskAgent(t,
		bashCall("b1", "touch blocked.txt"),
		bashCall("b2", "ls -la"),
		say("ok"))
	os.MkdirAll(filepath.Join(dir, ".prism"), 0o755)
	os.WriteFile(filepath.Join(dir, ".prism", "rules.json"), []byte(`{"block": ["touch *"], "ask": ["ls *"]}`), 0o644)
	*answers = []answer{answerChat}
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	if exists(dir, "blocked.txt") || !strings.Contains(toolOutputs(a)["b1"], "never allow touch *") {
		t.Errorf("a block rule must stop the command and say which rule: %q", toolOutputs(a)["b1"])
	}
	if len(*asked) != 1 || (*asked)[0].arg != "ls -la" || len((*asked)[0].risks) != 0 || !strings.Contains((*asked)[0].why, "always ask ls *") {
		t.Fatalf("an ask rule asks every time, with no yes for this chat: %+v", *asked)
	}
}

func TestNobodyToAskMeansNo(t *testing.T) {
	a, dir, _, _ := riskAgent(t, bashCall("b1", "mkdir src && rm -rf src && touch ran.txt"), say("ok"))
	a.confirm = nil
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	if exists(dir, "ran.txt") || !strings.Contains(toolOutputs(a)["b1"], "needs a yes") {
		t.Errorf("with nobody to ask, a risky command must not run: %q", toolOutputs(a)["b1"])
	}
}

func TestPrismFixStillAsksBeforeEveryCommand(t *testing.T) {
	a, _, asked, answers := riskAgent(t, bashCall("b1", "ls"), bashCall("b2", "git push origin main"), say("ok"))
	a.approve = yesNo(a.confirm)
	*answers = []answer{answerYes, answerNo}
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 2 {
		t.Fatalf("prism fix asks before every command, asked %d times", len(*asked))
	}
	for _, q := range *asked {
		if len(q.risks) != 0 {
			t.Errorf("prism fix offers only yes or no: %+v", q)
		}
	}
	if (*asked)[1].why == "" {
		t.Errorf("a risky command still says why in prism fix: %+v", (*asked)[1])
	}
}

func TestTerminalAnswersForTheChat(t *testing.T) {
	m := suggestModel(t, 80, 30)
	reply := make(chan answer, 3)
	m.handle(evApprove{approval{tool: "bash", arg: "git push", why: "sends your code to GitHub or another server", risks: []string{"git push"}}, reply})
	if screen := crewScreen(m); !strings.Contains(screen, "yes for this chat") || !strings.Contains(screen, "sends your code") {
		t.Fatalf("the question must say why and offer yes for this chat:\n%s", screen)
	}
	m = press(t, m, 'a', 0)
	if m.pending == nil {
		t.Fatalf("a key typed as the question appears is meant for the chat box, not an answer")
	}
	m.now += askGuard
	m = press(t, m, 'a', 0)
	if got := <-reply; got != answerChat {
		t.Errorf("a must answer yes for this chat, got %v", got)
	}
	m.handle(evApprove{approval{tool: "bash", arg: "ls"}, reply})
	m.now += askGuard
	m = press(t, m, 'a', 0)
	if m.pending == nil {
		t.Errorf("a must do nothing when the question has no yes for this chat")
	}
	m = press(t, m, 'n', 0)
	if got := <-reply; got != answerNo {
		t.Errorf("n must answer no, got %v", got)
	}
}

func TestWebAnswersTheQuestion(t *testing.T) {
	testEnv(t)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: []func(http.ResponseWriter) string{bashCall("b1", "rm -rf src"), say("ok")}}
	router := httptest.NewServer(fake.handler())
	defer router.Close()
	a := newAgent(router.URL, dir, dir)
	s := newServer(a, t.TempDir(), "prism")
	ts := httptest.NewUnstartedServer(nil)
	ts.Start()
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "http://")
	ts.Config.Handler = s.handler(host[strings.LastIndex(host, ":")+1:])
	post := func(path, body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(body))
		req.Header.Set("content-type", "application/json")
		req.Header.Set("Origin", ts.URL)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := post("/api/chat", `{"message":"clean up"}`)
	defer res.Body.Close()
	lines := bufio.NewScanner(res.Body)
	deadline := time.After(10 * time.Second)
	got := make(chan map[string]any)
	go func() {
		for lines.Scan() {
			var ev map[string]any
			if json.Unmarshal([]byte(strings.TrimPrefix(lines.Text(), "data: ")), &ev) == nil {
				got <- ev
			}
		}
		close(got)
	}()
	asked := false
	for {
		select {
		case ev, ok := <-got:
			if !ok {
				if !asked || exists(dir, "src") {
					t.Fatalf("the web must be asked and its yes must run the command: asked=%v src left=%v", asked, exists(dir, "src"))
				}
				if r := post("/api/approve", `{"id": 1, "answer": "yes"}`); r.StatusCode != http.StatusNotFound {
					t.Errorf("an answer nobody waits for must be refused, got %d", r.StatusCode)
				}
				if a.confirm != nil {
					t.Errorf("the page's asker must not stay on the agent after the turn")
				}
				return
			}
			if ev["type"] == "approve" {
				asked = true
				if ev["arg"] != "rm -rf src" || ev["chat"] != true || ev["why"] == "" {
					t.Errorf("approve event: %v", ev)
				}
				body, _ := json.Marshal(map[string]any{"id": ev["id"], "answer": "yes"})
				if r := post("/api/approve", string(body)); r.StatusCode != http.StatusNoContent {
					t.Fatalf("answer: %d", r.StatusCode)
				}
			}
		case <-deadline:
			t.Fatal("the turn never finished")
		}
	}
}

func TestRulesChangedMidTurnOnlyGetStricter(t *testing.T) {
	a, dir, asked, _ := riskAgent(t)
	rules := filepath.Join(dir, ".prism", "rules.json")
	os.MkdirAll(filepath.Dir(rules), 0o755)
	os.WriteFile(rules, []byte(`{"block": ["touch *"]}`), 0o644)
	a.startRules()
	os.WriteFile(rules, []byte(`{"allow": ["rm -rf *"]}`), 0o644)
	trustRules(dir)
	var r toolResult
	if !a.refused(context.Background(), FunctionCall{Name: "bash", Arguments: `{"command":"touch x"}`}, &r) {
		t.Errorf("a block rule removed mid-turn must still block this turn")
	}
	if !a.refused(context.Background(), FunctionCall{Name: "bash", Arguments: `{"command":"rm -rf src"}`}, &r) || len(*asked) != 1 {
		t.Errorf("an allow rule added mid-turn must not count until the next turn, asked %d", len(*asked))
	}
	os.WriteFile(rules, []byte(`{"block": ["ls *"]}`), 0o644)
	if !a.refused(context.Background(), FunctionCall{Name: "bash", Arguments: `{"command":"ls -la"}`}, &r) || !strings.Contains(r.output, "never allow ls *") {
		t.Errorf("a block rule added mid-turn must count right away: %q", r.output)
	}
}

func TestWebTurnWaitsForTheTerminalTab(t *testing.T) {
	h, s, port := testServerWithHub(t)
	host := "127.0.0.1:" + port
	var working int
	for _, tb := range s.hub.list() {
		if tb.Status == "working" {
			working = tb.ID
		}
	}
	rec := do(t, h, http.MethodPost, "/api/chat?tab="+strconv.Itoa(working), host, "http://"+host, `{"message":"hi"}`)
	if rec.Code != http.StatusConflict || s.agentFor(working).confirm != nil {
		t.Errorf("a web turn on a tab the terminal is running must wait (two turns would share its questions), got %d", rec.Code)
	}
	q := "/api/new?tab=" + strconv.Itoa(working)
	if rec := do(t, h, http.MethodPost, q, host, "http://"+host, `{}`); rec.Code != http.StatusConflict {
		t.Errorf("/new from the web must not reset a tab the terminal is running, got %d", rec.Code)
	}
	body, _ := json.Marshal(map[string]string{"text": "/compact"})
	if res := decode(t, do(t, h, http.MethodPost, "/api/command?tab="+strconv.Itoa(working), host, "http://"+host, string(body))); res["kind"] != "error" {
		t.Errorf("/compact from the web must wait for a tab the terminal is running, got %v", res)
	}
}

func TestResumeForgetsAllowedKinds(t *testing.T) {
	a := newTestAgent(t, "http://127.0.0.1:1", t.TempDir())
	a.allowedRisks = map[string]bool{"git push": true}
	a.resume(session{ID: "x", Messages: []Message{{Role: "system"}, {Role: "user", Content: "hi"}}})
	if len(a.allowedRisks) != 0 {
		t.Errorf("yes for this chat must not carry into another chat")
	}
}
