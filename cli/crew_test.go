package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type crewReq struct {
	who     string
	model   string
	crew    string
	avoid   []string
	exclude []string
	tools   []string
	system  string
	last    string
	results []string
	n       int
}

type crewFake struct {
	mu       sync.Mutex
	reqs     []crewReq
	inflight int
	peak     int
	status   func(w http.ResponseWriter)
	script   func(w http.ResponseWriter, r crewReq)
}

var workerName = regexp.MustCompile(`You are worker (\w+) in a crew`)

func (f *crewFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/status" {
			if f.status == nil {
				http.NotFound(w, req)
				return
			}
			f.status(w)
			return
		}
		var body struct {
			Model    string    `json:"model"`
			Messages []Message `json:"messages"`
			Avoid    []string  `json:"prism_avoid"`
			Exclude  []string  `json:"prism_exclude_families"`
			Tools    []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		data, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(data, &body)
		r := crewReq{model: body.Model, crew: req.Header.Get("x-prism-crew"), avoid: body.Avoid, exclude: body.Exclude}
		for _, t := range body.Tools {
			r.tools = append(r.tools, t.Function.Name)
		}
		if len(body.Messages) > 0 {
			r.system = body.Messages[0].Content
			r.last = body.Messages[len(body.Messages)-1].Content
		}
		for _, m := range body.Messages {
			if m.Role == "assistant" {
				r.n++
			}
			if m.Role == "tool" {
				r.results = append(r.results, m.Content)
			}
		}
		if m := workerName.FindStringSubmatch(r.system); m != nil {
			r.who = m[1]
		} else if r.model == "auto:review" {
			r.who = "review"
		}
		f.mu.Lock()
		f.reqs = append(f.reqs, r)
		f.inflight++
		f.peak = max(f.peak, f.inflight)
		f.mu.Unlock()
		defer func() {
			f.mu.Lock()
			f.inflight--
			f.mu.Unlock()
		}()
		f.script(w, r)
	}
}

func (f *crewFake) of(who string) []crewReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []crewReq
	for _, r := range f.reqs {
		if r.who == who {
			out = append(out, r)
		}
	}
	return out
}

func replyText(w http.ResponseWriter, route, family, text string) {
	w.Header().Set("x-free-route", route)
	if family != "" {
		w.Header().Set("x-prism-family", family)
	}
	w.Header().Set("content-type", "text/event-stream")
	fmt.Fprintln(w, delta(string(mustJSON(map[string]any{"content": text}))))
	fmt.Fprintln(w, sseDone)
}

func replyCall(w http.ResponseWriter, route, family, name string, args map[string]any) {
	w.Header().Set("x-free-route", route)
	if family != "" {
		w.Header().Set("x-prism-family", family)
	}
	w.Header().Set("content-type", "text/event-stream")
	call := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{
		"index": 0, "id": "c" + name, "function": map[string]any{"name": name, "arguments": string(mustJSON(args))},
	}}}}}}
	fmt.Fprintln(w, "data: "+string(mustJSON(call)))
	fmt.Fprintln(w, `data: {"choices":[{"finish_reason":"tool_calls","delta":{}}]}`)
	fmt.Fprintln(w, sseDone)
}

func mustJSON(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}

func statusRows(providers ...string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		var rows []map[string]any
		for i, p := range providers {
			rows = append(rows, map[string]any{"route": fmt.Sprintf("%s-%d", p, i), "provider": p, "family": p, "ready": true})
		}
		w.Write(mustJSON(rows))
	}
}

func crewPlan(review bool, tasks ...[2]string) string {
	var list []map[string]any
	for _, t := range tasks {
		list = append(list, map[string]any{"id": t[0], "role": "code", "task": "build " + t[0], "files": strings.Split(t[1], ",")})
	}
	return string(mustJSON(map[string]any{"tasks": list, "review": review}))
}

type crewRun struct {
	fake   *crewFake
	dir    string
	lead   *Agent
	events []any
	result toolResult
}

func newCrewRun(t *testing.T, fake *crewFake) *crewRun {
	t.Helper()
	if fake.status == nil {
		fake.status = statusRows("p1", "p2", "p3")
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	testEnv(t)
	return &crewRun{fake: fake, dir: dir, lead: &Agent{api: srv.URL + "/v1", dir: dir, window: contextLimit}}
}

func (c *crewRun) run(ctx context.Context, plan string) toolResult {
	var mu sync.Mutex
	c.result = c.lead.runCrew(ctx, plan, func(e any) {
		mu.Lock()
		c.events = append(c.events, e)
		mu.Unlock()
	})
	return c.result
}

func (c *crewRun) statuses(id string) []string {
	var out []string
	for _, e := range c.events {
		if w, ok := e.(evCrewWorker); ok && w.id == id {
			out = append(out, w.status)
		}
	}
	return out
}

func toolNames(specs []any) []string {
	var out []string
	for _, s := range specs {
		fn, _ := s.(map[string]any)["function"].(map[string]any)
		out = append(out, fn["name"].(string))
	}
	return out
}

func quickWait(t *testing.T) {
	old := crewWait
	crewWait = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(func() { crewWait = old })
}

func TestCrewWorkersWriteTheirOwnFilesInParallel(t *testing.T) {
	var gate sync.WaitGroup
	gate.Add(2)
	arrived := make(chan struct{})
	go func() { gate.Wait(); close(arrived) }()
	var seen sync.Map
	fake := &crewFake{script: func(w http.ResponseWriter, r crewReq) {
		if _, dup := seen.LoadOrStore(r.who, true); !dup {
			gate.Done()
		}
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			http.Error(w, "workers did not run in parallel", 500)
			return
		}
		if r.n == 0 {
			replyCall(w, "route-"+r.who, "fam-"+r.who, "write", map[string]any{"path": r.who + ".txt", "content": "from " + r.who})
			return
		}
		replyText(w, "route-"+r.who, "fam-"+r.who, "wrote "+r.who+".txt")
	}}
	c := newCrewRun(t, fake)
	var asked []string
	var askMu sync.Mutex
	c.lead.approve = func(ctx context.Context, tool, arg string) bool {
		askMu.Lock()
		defer askMu.Unlock()
		asked = append(asked, tool+" "+arg)
		return true
	}
	res := c.run(context.Background(), crewPlan(false, [2]string{"a", "a.txt"}, [2]string{"b", "b.txt"}))
	if !res.ok {
		t.Fatalf("crew failed: %s", res.output)
	}
	for _, id := range []string{"a", "b"} {
		data, err := os.ReadFile(filepath.Join(c.dir, id+".txt"))
		if err != nil || string(data) != "from "+id {
			t.Errorf("%s.txt = %q, %v", id, data, err)
		}
		reqs := fake.of(id)
		if len(reqs) != 2 {
			t.Fatalf("worker %s made %d requests, want 2", id, len(reqs))
		}
		if reqs[0].model != "auto:code" || reqs[0].crew != "1" {
			t.Errorf("worker %s sent model %q crew header %q", id, reqs[0].model, reqs[0].crew)
		}
		if !strings.Contains(res.output, "["+id+"] done") || !strings.Contains(res.output, "wrote "+id+".txt") {
			t.Errorf("report misses worker %s: %s", id, res.output)
		}
		got := c.statuses(id)
		if got[0] != "queued" || got[len(got)-1] != "done" || !slices.Contains(got, "running") {
			t.Errorf("worker %s statuses = %v", id, got)
		}
	}
	start, ok := c.events[0].(evCrewStart)
	if !ok || len(start.tasks) != 2 || start.tasks[0].ID != "a" {
		t.Errorf("first event = %#v", c.events[0])
	}
	done, ok := c.events[len(c.events)-1].(evCrewDone)
	if !ok || done.summary != "2 of 2 workers done" {
		t.Errorf("last event = %#v", c.events[len(c.events)-1])
	}
	if len(asked) != 2 {
		t.Fatalf("approvals = %v", asked)
	}
	for _, line := range asked {
		if !strings.HasPrefix(line, "write worker a: ") && !strings.HasPrefix(line, "write worker b: ") {
			t.Errorf("approval not labelled: %q", line)
		}
	}
}

func TestCrewRejectsWritesOutsideOwnership(t *testing.T) {
	var dir string
	fake := &crewFake{script: func(w http.ResponseWriter, r crewReq) {
		if r.who != "a" {
			replyText(w, "rb", "", "nothing to do")
			return
		}
		list := []map[string]any{
			{"path": "./other.txt", "content": "x"},
			{"path": "sub/../other.txt", "content": "x"},
			{"path": filepath.Join(dir, "other.txt"), "content": "x"},
			{"path": "link.txt", "content": "x"},
			{"path": "sub/../a.txt", "content": "mine"},
		}
		if r.n < len(list) {
			replyCall(w, "ra", "", "write", list[r.n])
			return
		}
		replyText(w, "ra", "", "done")
	}}
	c := newCrewRun(t, fake)
	dir = c.dir
	if err := os.WriteFile(filepath.Join(c.dir, "other.txt"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other.txt", filepath.Join(c.dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(c.dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := c.run(context.Background(), crewPlan(false, [2]string{"a", "a.txt"}, [2]string{"b", "other.txt"}))
	if !res.ok {
		t.Fatalf("crew failed: %s", res.output)
	}
	if data, _ := os.ReadFile(filepath.Join(c.dir, "other.txt")); string(data) != "original" {
		t.Errorf("worker a changed a file it does not own: %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(c.dir, "a.txt")); string(data) != "mine" {
		t.Errorf("a.txt = %q", data)
	}
	reqs := fake.of("a")
	last := reqs[len(reqs)-1]
	if len(last.results) != 5 {
		t.Fatalf("worker a saw %d tool results, want 5: %v", len(last.results), last.results)
	}
	for i, out := range last.results[:4] {
		if !strings.Contains(out, "you do not own") || !strings.Contains(out, "a.txt") {
			t.Errorf("write %d was not refused with the owned files: %q", i, out)
		}
	}
	if strings.Contains(last.results[4], "you do not own") {
		t.Errorf("alias of an owned file was refused: %q", last.results[4])
	}
}

func TestCrewPlanValidation(t *testing.T) {
	fake := &crewFake{script: func(w http.ResponseWriter, r crewReq) { replyText(w, "r", "", "ok") }}
	c := newCrewRun(t, fake)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(c.dir, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("x.txt", filepath.Join(c.dir, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(c.dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, plan, want string
	}{
		{"one task", crewPlan(false, [2]string{"a", "a.txt"}), "needs 2 or 3 tasks"},
		{"four tasks", crewPlan(false, [2]string{"a", "a"}, [2]string{"b", "b"}, [2]string{"c", "c"}, [2]string{"d", "d"}), "needs 2 or 3 tasks"},
		{"same id", crewPlan(false, [2]string{"a", "a.txt"}, [2]string{"a", "b.txt"}), "id is used twice"},
		{"dot alias", crewPlan(false, [2]string{"a", "x.txt"}, [2]string{"b", "./x.txt"}), "overlaps"},
		{"dotdot alias", crewPlan(false, [2]string{"a", "x.txt"}, [2]string{"b", "sub/../x.txt"}), "overlaps"},
		{"absolute alias", crewPlan(false, [2]string{"a", "x.txt"}, [2]string{"b", filepath.Join(c.dir, "x.txt")}), "overlaps"},
		{"symlink alias", crewPlan(false, [2]string{"a", "x.txt"}, [2]string{"b", "alias.txt"}), "overlaps"},
		{"folder over file", crewPlan(false, [2]string{"a", "sub"}, [2]string{"b", "sub/m.go"}), "overlaps"},
		{"same task twice", crewPlan(false, [2]string{"a", "x.txt,./x.txt"}, [2]string{"b", "y.txt"}), "overlaps"},
		{"no files", `{"tasks":[{"id":"a","task":"t","files":[]},{"id":"b","task":"t","files":["b"]}]}`, "files is empty"},
		{"blank file", `{"tasks":[{"id":"a","task":"t","files":[" "]},{"id":"b","task":"t","files":["b"]}]}`, "path is empty"},
		{"parent dir", crewPlan(false, [2]string{"a", "../evil.txt"}, [2]string{"b", "b.txt"}), "not a file inside the project"},
		{"absolute outside", crewPlan(false, [2]string{"a", filepath.Join(outside, "evil.txt")}, [2]string{"b", "b.txt"}), "not a file inside the project"},
		{"symlink outside", crewPlan(false, [2]string{"a", "escape/evil.txt"}, [2]string{"b", "b.txt"}), "not a file inside the project"},
		{"project root", crewPlan(false, [2]string{"a", "."}, [2]string{"b", "b.txt"}), "not a file inside the project"},
		{"bad role", `{"tasks":[{"id":"a","role":"slow","task":"t","files":["a"]},{"id":"b","task":"t","files":["b"]}]}`, "role must be code or fast"},
		{"bad json", `{`, "not valid JSON"},
	}
	for _, anyOn := range []bool{false, true} {
		anyPath = anyOn
		for _, tc := range cases {
			res := c.run(context.Background(), tc.plan)
			if res.ok || !strings.HasPrefix(res.output, "Error: ") || !strings.Contains(res.output, tc.want) {
				t.Errorf("%s (anyPath=%v): got ok=%v %q, want error containing %q", tc.name, anyOn, res.ok, res.output, tc.want)
			}
		}
	}
	anyPath = false
	if n := len(fake.reqs); n != 0 {
		t.Errorf("invalid plans reached the router %d times", n)
	}
	if len(c.events) != 0 {
		t.Errorf("invalid plans emitted events: %v", c.events)
	}
}

func TestCrewWorkerToolsAndPrompt(t *testing.T) {
	c := newCrewRun(t, &crewFake{script: func(w http.ResponseWriter, r crewReq) {}})
	lead := c.lead
	lead.skills = []skill{{name: "demo", description: "a skill"}}
	lead.reset()
	if slices.Contains(toolNames(lead.tools()), "crew") || strings.Contains(lead.systemPrompt(), "CREW:") {
		t.Error("crew tool and guidance must stay out until the chat asks for agents")
	}
	lead.crewOn = true
	if !slices.Contains(toolNames(lead.tools()), "crew") || !slices.Contains(toolNames(lead.tools()), "bash") {
		t.Errorf("lead tools = %v", toolNames(lead.tools()))
	}
	if !strings.Contains(lead.systemPrompt(), "CREW:") {
		t.Error("lead prompt has no crew guidance")
	}
	workers, _, problems := lead.parseCrew(crewPlan(false, [2]string{"a", "a.txt"}, [2]string{"b", "b.txt"}))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	w := lead.newWorker(workers[0], workers)
	want := []string{"list", "grep", "read", "edit", "write"}
	if got := toolNames(w.tools()); !slices.Equal(got, want) {
		t.Errorf("worker tools = %v, want %v", got, want)
	}
	prompt := w.systemPrompt()
	for _, banned := range []string{"bash", "screenshot", "crew tool", "CREW:"} {
		if strings.Contains(prompt, banned) {
			t.Errorf("worker prompt mentions %q:\n%s", banned, prompt)
		}
	}
	if !strings.Contains(prompt, "You are worker a in a crew") || !strings.Contains(prompt, "You own exactly: a.txt") {
		t.Errorf("worker prompt lacks the protocol:\n%s", prompt)
	}
	if w.store != nil || w.mcp != nil || w.skills != nil || w.system || w.role != "worker" || w.model != "auto:code" || w.stepLimit != 25 {
		t.Errorf("worker is not isolated: %+v", w)
	}
	if r := w.runCrew(context.Background(), crewPlan(false, [2]string{"a", "a.txt"}, [2]string{"b", "b.txt"}), func(any) {}); r.ok || !strings.Contains(r.output, "unknown tool crew") {
		t.Errorf("worker could start a crew: %+v", r)
	}
	rev := &Agent{dir: c.dir, role: "reviewer", readOnly: true}
	if got := toolNames(rev.tools()); !slices.Equal(got, []string{"list", "grep", "read"}) {
		t.Errorf("reviewer tools = %v", got)
	}
	if strings.Contains(crewBasePrompt(c.dir, true), "bash") {
		t.Error("reviewer prompt mentions bash")
	}
}

func TestCrewWorkerCannotUseOtherTools(t *testing.T) {
	fake := &crewFake{script: func(w http.ResponseWriter, r crewReq) {
		if r.who != "a" || r.n > 2 {
			replyText(w, "r", "", "done")
			return
		}
		if r.n == 0 {
			replyCall(w, "r", "", "crew", map[string]any{"tasks": []any{}})
			return
		}
		replyCall(w, "r", "", "bash", map[string]any{"command": "touch pwned"})
	}}
	c := newCrewRun(t, fake)
	c.run(context.Background(), crewPlan(false, [2]string{"a", "a.txt"}, [2]string{"b", "b.txt"}))
	if _, err := os.Stat(filepath.Join(c.dir, "pwned")); err == nil {
		t.Error("worker ran bash")
	}
	reqs := fake.of("a")
	if len(reqs) < 3 {
		t.Fatalf("worker a made %d requests", len(reqs))
	}
	got := reqs[2].results
	if len(got) != 2 || !strings.Contains(got[0], "unknown tool crew. Use list, grep, read, edit or write") || !strings.Contains(got[1], "unknown tool bash. Use list, grep, read, edit or write") {
		t.Errorf("results = %v", got)
	}
	for _, r := range reqs {
		if !slices.Equal(r.tools, []string{"list", "grep", "read", "edit", "write"}) {
			t.Errorf("worker sent tools %v", r.tools)
		}
	}
}

func TestCrewWorkersWriteNoSessionFile(t *testing.T) {
	fake := &crewFake{script: func(w http.ResponseWriter, r crewReq) {
		if r.n == 0 {
			replyCall(w, "r", "", "write", map[string]any{"path": r.who + ".txt", "content": "x"})
			return
		}
		replyText(w, "r", "", "done")
	}}
	c := newCrewRun(t, fake)
	cfg := os.Getenv("XDG_CONFIG_HOME")
	c.run(context.Background(), crewPlan(false, [2]string{"a", "a.txt"}, [2]string{"b", "b.txt"}))
	var found []string
	filepath.Walk(cfg, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.Contains(path, "session") {
			found = append(found, path)
		}
		return nil
	})
	if len(found) > 0 {
		t.Errorf("session files written: %v", found)
	}
}

func TestCrewReviewerExcludesWorkerFamilies(t *testing.T) {
	fake := &crewFake{script: func(w http.ResponseWriter, r crewReq) {
		switch {
		case r.who == "review":
			replyText(w, "rv", "gamma", "OK")
		case r.n == 0:
			replyCall(w, "route-"+r.who, "fam-"+r.who, "write", map[string]any{"path": r.who + ".txt", "content": "x"})
		default:
			replyText(w, "route-"+r.who, "fam-"+r.who, "done")
		}
	}}
	c := newCrewRun(t, fake)
	res := c.run(context.Background(), crewPlan(true, [2]string{"a", "a.txt"}, [2]string{"b", "b.txt"}))
	if !res.ok || !strings.Contains(res.output, "Review:\nOK") {
		t.Fatalf("result: %s", res.output)
	}
	reviews := fake.of("review")
	if len(reviews) != 1 {
		t.Fatalf("reviewer made %d requests", len(reviews))
	}
	r := reviews[0]
	slices.Sort(r.exclude)
	if !slices.Equal(r.exclude, []string{"fam-a", "fam-b"}) {
		t.Errorf("prism_exclude_families = %v", r.exclude)
	}
	if !slices.Equal(r.tools, []string{"list", "grep", "read"}) || r.crew != "1" {
		t.Errorf("reviewer tools %v crew header %q", r.tools, r.crew)
	}
	for _, id := range []string{"a", "b"} {
		for _, q := range fake.of(id) {
			if len(q.exclude) != 0 {
				t.Errorf("worker %s sent exclude %v", id, q.exclude)
			}
		}
		if len(fake.of(id)) != 2 {
			t.Errorf("worker %s was sent back though the review was OK", id)
		}
	}
	if got := c.statuses("review"); got[len(got)-1] != "done" {
		t.Errorf("review statuses = %v", got)
	}
}

func TestCrewReviewSkippedWhenNothingChanged(t *testing.T) {
	fake := &crewFake{script: func(w http.ResponseWriter, r crewReq) { replyText(w, "r", "f", "nothing needed") }}
	c := newCrewRun(t, fake)
	res := c.run(context.Background(), crewPlan(true, [2]string{"a", "a.txt"}, [2]string{"b", "b.txt"}))
	if len(fake.of("review")) != 0 || !strings.Contains(res.output, "skipped: no worker changed any file") {
		t.Errorf("reviewer ran or note missing: %s", res.output)
	}
}

func TestCrewRepairRoundSendsFindingsToTheWorker(t *testing.T) {
	fake := &crewFake{script: func(w http.ResponseWriter, r crewReq) {
		switch {
		case r.who == "review":
			replyText(w, "rv", "", "a.txt: missing the header")
		case r.n == 0:
			replyCall(w, "route-"+r.who, "", "write", map[string]any{"path": r.who + ".txt", "content": "x"})
		case r.n == 1:
			replyText(w, "route-"+r.who, "", "done")
		case r.n == 2:
			replyCall(w, "route-"+r.who, "", "write", map[string]any{"path": r.who + ".txt", "content": "fixed"})
		default:
			replyText(w, "route-"+r.who, "", "fixed it")
		}
	}}
	c := newCrewRun(t, fake)
	res := c.run(context.Background(), crewPlan(true, [2]string{"a", "a.txt"}, [2]string{"b", "b.txt"}))
	if !res.ok || !strings.Contains(res.output, "worker a was sent back once") {
		t.Fatalf("result: %s", res.output)
	}
	if len(fake.of("a")) != 4 || len(fake.of("b")) != 2 || len(fake.of("review")) != 1 {
		t.Errorf("requests a=%d b=%d review=%d", len(fake.of("a")), len(fake.of("b")), len(fake.of("review")))
	}
	if data, _ := os.ReadFile(filepath.Join(c.dir, "a.txt")); string(data) != "fixed" {
		t.Errorf("a.txt = %q", data)
	}
	if !strings.Contains(fake.of("a")[2].last, "The reviewer reported") {
		t.Errorf("repair prompt = %q", fake.of("a")[2].last)
	}
}

func TestCrewConcurrencyCap(t *testing.T) {
	cases := []struct {
		name      string
		status    func(w http.ResponseWriter)
		providers int
		wantPeak  int
		note      bool
	}{
		{"one provider", statusRows("p1"), 1, 1, true},
		{"two providers", statusRows("p1", "p2"), 2, 1, true},
		{"three providers", statusRows("p1", "p2", "p3"), 3, 2, false},
		{"many routes one provider", statusRows("p1", "p1", "p1", "p1"), 1, 1, true},
		{"status down", nil, 0, 2, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &crewFake{status: tc.status, script: func(w http.ResponseWriter, r crewReq) {
				time.Sleep(60 * time.Millisecond)
				if r.n == 0 {
					replyCall(w, "r"+r.who, "", "write", map[string]any{"path": r.who + ".txt", "content": "x"})
					return
				}
				replyText(w, "r"+r.who, "", "done")
			}}
			c := newCrewRun(t, fake)
			fake.status = tc.status
			res := c.run(context.Background(), crewPlan(false, [2]string{"a", "a.txt"}, [2]string{"b", "b.txt"}, [2]string{"c", "c.txt"}))
			if !res.ok {
				t.Fatalf("crew failed: %s", res.output)
			}
			if fake.peak != tc.wantPeak {
				t.Errorf("peak concurrent workers = %d, want %d", fake.peak, tc.wantPeak)
			}
			if got := strings.Contains(res.output, "one after another"); got != tc.note {
				t.Errorf("sequential note = %v, want %v", got, tc.note)
			}
		})
	}
}

func TestCrewConcurrencyCountsProviders(t *testing.T) {
	rows := func(w http.ResponseWriter) {
		w.Write(mustJSON([]map[string]any{
			{"route": "a", "provider": "p1", "ready": true},
			{"route": "b", "provider": "p1", "ready": true},
			{"route": "c", "provider": "p2", "ready": false, "retryInSeconds": 30},
			{"route": "d", "provider": "p3", "ready": true},
		}))
	}
	fake := &crewFake{status: rows, script: func(w http.ResponseWriter, r crewReq) {}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	if got := crewConcurrency(context.Background(), srv.URL+"/v1"); got != 1 {
		t.Errorf("two ready providers gave %d, want 1", got)
	}
	if d := crewRetryDelay(context.Background(), srv.URL+"/v1"); d != 30*time.Second {
		t.Errorf("retry delay = %v, want 30s", d)
	}
}

func TestCrewWorkersAvoidRoutesOfEarlierWorkers(t *testing.T) {
	var gate sync.WaitGroup
	gate.Add(2)
	arrived := make(chan struct{})
	go func() { gate.Wait(); close(arrived) }()
	fake := &crewFake{script: func(w http.ResponseWriter, r crewReq) {
		if r.n == 0 {
			replyCall(w, "route-"+r.who, "", "write", map[string]any{"path": r.who + ".txt", "content": "x"})
			return
		}
		if r.n == 1 {
			gate.Done()
			select {
			case <-arrived:
			case <-time.After(5 * time.Second):
			}
		}
		replyText(w, "route-"+r.who, "", "done")
	}}
	c := newCrewRun(t, fake)
	c.run(context.Background(), crewPlan(false, [2]string{"a", "a.txt"}, [2]string{"b", "b.txt"}))
	a, b := fake.of("a"), fake.of("b")
	if len(a) != 2 || len(b) != 2 {
		t.Fatalf("requests a=%d b=%d", len(a), len(b))
	}
	if len(a[1].avoid) != 0 {
		t.Errorf("worker a avoided %v", a[1].avoid)
	}
	if !slices.Equal(b[1].avoid, []string{"route-a"}) {
		t.Errorf("worker b avoid = %v, want [route-a]", b[1].avoid)
	}
}

func TestCrewRouterErrorWaitsAndRetries(t *testing.T) {
	quickWait(t)
	var failures sync.Map
	fake := &crewFake{script: func(w http.ResponseWriter, r crewReq) {
		if r.who == "a" && r.n == 0 {
			n, _ := failures.LoadOrStore("a", new(int))
			if *n.(*int) < 2 {
				*n.(*int)++
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte(`{"error":{"message":"every free model is busy"}}`))
				return
			}
		}
		if r.n == 0 {
			replyCall(w, "r", "", "write", map[string]any{"path": r.who + ".txt", "content": "x"})
			return
		}
		replyText(w, "r", "", "done")
	}}
	c := newCrewRun(t, fake)
	res := c.run(context.Background(), crewPlan(false, [2]string{"a", "a.txt"}, [2]string{"b", "b.txt"}))
	if !res.ok || !strings.Contains(res.output, "[a] done") {
		t.Fatalf("worker a did not recover: %s", res.output)
	}
	if n := len(fake.of("a")); n != 4 {
		t.Errorf("worker a made %d requests, want 4", n)
	}
}

func TestCrewRouterErrorGivesUpAfterThreeRetries(t *testing.T) {
	quickWait(t)
	fake := &crewFake{script: func(w http.ResponseWriter, r crewReq) {
		if r.who == "a" {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":{"message":"every free model is busy"}}`))
			return
		}
		replyText(w, "r", "", "done")
	}}
	c := newCrewRun(t, fake)
	res := c.run(context.Background(), crewPlan(false, [2]string{"a", "a.txt"}, [2]string{"b", "b.txt"}))
	if n := len(fake.of("a")); n != 4 {
		t.Errorf("worker a made %d requests, want 4", n)
	}
	if !strings.Contains(res.output, "[a] failed") || !strings.Contains(res.output, "[b] done") || !res.ok {
		t.Errorf("result: %s", res.output)
	}
}

func TestCrewCancellationKeepsPartialWork(t *testing.T) {
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	fake := &crewFake{script: func(w http.ResponseWriter, r crewReq) {
		if r.n == 0 {
			replyCall(w, "r", "", "write", map[string]any{"path": r.who + ".txt", "content": "partial"})
			return
		}
		started <- struct{}{}
		<-release
	}}
	c := newCrewRun(t, fake)
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		<-started
		cancel()
	}()
	begin := time.Now()
	res := c.run(ctx, crewPlan(false, [2]string{"a", "a.txt"}, [2]string{"b", "b.txt"}, [2]string{"c", "c.txt"}))
	if time.Since(begin) > 4*time.Second {
		t.Errorf("cancel took %v", time.Since(begin))
	}
	if res.ok {
		t.Errorf("a cancelled crew reported success: %s", res.output)
	}
	partial := 0
	for _, id := range []string{"a", "b", "c"} {
		if !strings.Contains(res.output, "["+id+"] cancelled") {
			t.Errorf("worker %s not reported as cancelled: %s", id, res.output)
		}
		if got := c.statuses(id); got[len(got)-1] != "cancelled" {
			t.Errorf("%s statuses = %v", id, got)
		}
		if data, _ := os.ReadFile(filepath.Join(c.dir, id+".txt")); string(data) == "partial" {
			partial++
		}
	}
	if partial != 2 {
		t.Errorf("%d partial files kept, want 2", partial)
	}
	if _, ok := c.events[len(c.events)-1].(evCrewDone); !ok {
		t.Errorf("last event = %#v", c.events[len(c.events)-1])
	}
}

func TestCrewFastRoleUsesFastModel(t *testing.T) {
	fake := &crewFake{script: func(w http.ResponseWriter, r crewReq) { replyText(w, "r", "", "done") }}
	c := newCrewRun(t, fake)
	plan := `{"tasks":[{"id":"a","role":"fast","task":"t","files":["a.txt"]},{"id":"b","task":"t","files":["b.txt"]}]}`
	c.run(context.Background(), plan)
	if got := fake.of("a")[0].model; got != "auto:fast" {
		t.Errorf("fast worker model = %q", got)
	}
	if got := fake.of("b")[0].model; got != "auto:code" {
		t.Errorf("default worker model = %q", got)
	}
}

func TestLeadRequestsUseAutoAndNoCrewHeader(t *testing.T) {
	fake := &crewFake{script: func(w http.ResponseWriter, r crewReq) { replyText(w, "r", "fam", "hi") }}
	c := newCrewRun(t, fake)
	c.lead.reset()
	c.lead.crewOn = true
	if _, err := c.lead.complete(context.Background(), c.lead.messages, nil, func(any) {}); err != nil {
		t.Fatal(err)
	}
	r := fake.reqs[0]
	if r.model != "auto" || r.crew != "" || len(r.exclude) != 0 || !slices.Contains(r.tools, "crew") {
		t.Errorf("lead request = %+v", r)
	}
	if !slices.Equal(c.lead.families, []string{"fam"}) {
		t.Errorf("families = %v", c.lead.families)
	}
}

func TestCrewParsePlanDetails(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{dir: dir}
	workers, review, problems := a.parseCrew(`{"review":true,"tasks":[
		{"id":" a ","task":"build a","files":["src/a.go","./docs/a.md"]},
		{"id":"b","role":" fast ","task":"build b","files":["b.txt"]}]}`)
	if len(problems) != 0 {
		t.Fatalf("valid plan had problems: %v", problems)
	}
	if !review {
		t.Error("review flag was lost")
	}
	if len(workers) != 2 {
		t.Fatalf("got %d workers, want 2", len(workers))
	}
	wa, wb := workers[0], workers[1]
	if wa.task.ID != "a" || wa.task.Role != "code" {
		t.Errorf("worker a id=%q role=%q, want trimmed id and default role code", wa.task.ID, wa.task.Role)
	}
	if wb.task.Role != "fast" {
		t.Errorf("worker b role = %q, want fast", wb.task.Role)
	}
	if want := []string{"src/a.go", "docs/a.md"}; !slices.Equal(wa.shown, want) {
		t.Errorf("shown = %v, want %v", wa.shown, want)
	}
	if want := []string{filepath.Join(dir, "src/a.go"), filepath.Join(dir, "docs/a.md")}; !slices.Equal(wa.files, want) {
		t.Errorf("files = %v, want %v", wa.files, want)
	}
	for i, w := range workers {
		if w.idx != i || w.status != "queued" || w.budget != crewSteps {
			t.Errorf("worker %d: idx=%d status=%q budget=%d", i, w.idx, w.status, w.budget)
		}
	}
}

func TestCrewParsePlanCollectsEveryProblem(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{dir: dir}
	_, _, problems := a.parseCrew(`{"tasks":[
		{"id":"","task":"t","files":["z"]},
		{"id":"b","task":"  ","files":["b"]},
		{"id":"c","role":"slow","task":"t","files":["b"]},
		{"id":"d","task":"t","files":[]}]}`)
	want := []string{
		"a crew needs 2 or 3 tasks, got 4",
		"task 1: id is empty",
		"task b: the task brief is empty",
		"task c: role must be code or fast",
		"task c: b overlaps a file owned by task b",
		"task d: files is empty",
	}
	if len(problems) != len(want) {
		t.Fatalf("got %d problems, want %d: %q", len(problems), len(want), problems)
	}
	for i, w := range want {
		if !strings.HasPrefix(problems[i], w) {
			t.Errorf("problem %d = %q, want prefix %q", i, problems[i], w)
		}
	}
	if _, _, p := a.parseCrew(`{"tasks":null}`); len(p) != 1 || !strings.Contains(p[0], "got 0") {
		t.Errorf("missing tasks gave %q", p)
	}
}

func TestCrewOwnsAndNotOwnedMessage(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{dir: dir, owned: []string{filepath.Join(dir, "a.txt"), filepath.Join(dir, "lib", "b.go")}}
	cases := []struct {
		path string
		want bool
	}{
		{"a.txt", true},
		{"./a.txt", true},
		{"lib/../a.txt", true},
		{filepath.Join(dir, "lib", "b.go"), true},
		{"", false},
		{"   ", false},
		{"lib", false},
		{"c.txt", false},
		{"lib/b.go/x", false},
	}
	for _, tc := range cases {
		if got := a.owns(tc.path); got != tc.want {
			t.Errorf("owns(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
	msg := a.notOwned("c.txt")
	if !strings.HasPrefix(msg, "you do not own c.txt.") || !strings.Contains(msg, "a.txt, lib/b.go") {
		t.Errorf("notOwned message = %q", msg)
	}
	if strings.Contains(msg, dir) {
		t.Errorf("notOwned leaks absolute paths: %q", msg)
	}
}

func TestCrewRejectsEditsOutsideOwnership(t *testing.T) {
	fake := &crewFake{script: func(w http.ResponseWriter, r crewReq) {
		if r.who != "a" {
			replyText(w, "rb", "", "nothing to do")
			return
		}
		list := []map[string]any{
			{"path": "other.txt", "old": "original", "new": "hacked"},
			{"path": "", "old": "original", "new": "hacked"},
		}
		if r.n < len(list) {
			replyCall(w, "ra", "", "edit", list[r.n])
			return
		}
		replyText(w, "ra", "", "done")
	}}
	c := newCrewRun(t, fake)
	if err := os.WriteFile(filepath.Join(c.dir, "other.txt"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := c.run(context.Background(), crewPlan(false, [2]string{"a", "a.txt"}, [2]string{"b", "other.txt"}))
	if !res.ok {
		t.Fatalf("crew failed: %s", res.output)
	}
	if data, _ := os.ReadFile(filepath.Join(c.dir, "other.txt")); string(data) != "original" {
		t.Errorf("worker a edited a file it does not own: %q", data)
	}
	reqs := fake.of("a")
	last := reqs[len(reqs)-1]
	if len(last.results) != 2 {
		t.Fatalf("worker a saw %d tool results, want 2: %v", len(last.results), last.results)
	}
	for i, out := range last.results {
		if !strings.Contains(out, "you do not own") {
			t.Errorf("edit %d was not refused: %q", i, out)
		}
	}
}

func TestCrewStatusFallbacks(t *testing.T) {
	cases := []struct {
		name      string
		handler   http.HandlerFunc
		wantConc  int
		wantDelay time.Duration
	}{
		{"server error", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) }, 2, 10 * time.Second},
		{"bad json", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("not json")) }, 2, 10 * time.Second},
		{"nothing ready", func(w http.ResponseWriter, r *http.Request) {
			w.Write(mustJSON([]map[string]any{
				{"route": "a", "provider": "p1", "ready": false, "retryInSeconds": 500},
				{"route": "b", "provider": "p2", "ready": false, "retryInSeconds": 90},
			}))
		}, 1, 60 * time.Second},
		{"ready rows ignored for delay", func(w http.ResponseWriter, r *http.Request) {
			w.Write(mustJSON([]map[string]any{
				{"route": "a", "provider": "p1", "ready": true, "retryInSeconds": 3},
				{"route": "b", "provider": "p2", "ready": false, "retryInSeconds": 0},
				{"route": "c", "provider": "p3", "ready": true},
				{"route": "d", "provider": "p4", "ready": true},
				{"route": "e", "provider": "p5", "ready": true},
			}))
		}, 2, 10 * time.Second},
		{"soonest wins", func(w http.ResponseWriter, r *http.Request) {
			w.Write(mustJSON([]map[string]any{
				{"route": "a", "provider": "p1", "ready": false, "retryInSeconds": 40},
				{"route": "b", "provider": "p2", "ready": false, "retryInSeconds": 7},
				{"route": "c", "provider": "p3", "ready": true},
			}))
		}, 1, 7 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var paths []string
			var mu sync.Mutex
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.URL.Path)
				mu.Unlock()
				tc.handler(w, r)
			}))
			defer srv.Close()
			if got := crewConcurrency(context.Background(), srv.URL+"/v1/"); got != tc.wantConc {
				t.Errorf("concurrency = %d, want %d", got, tc.wantConc)
			}
			if got := crewRetryDelay(context.Background(), srv.URL+"/v1"); got != tc.wantDelay {
				t.Errorf("retry delay = %v, want %v", got, tc.wantDelay)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, p := range paths {
				if p != "/status" {
					t.Errorf("status fetched from %q, want /status", p)
				}
			}
		})
	}
	if got := crewConcurrency(context.Background(), "http://127.0.0.1:1/v1"); got != 2 {
		t.Errorf("unreachable router gave concurrency %d, want 2", got)
	}
}
