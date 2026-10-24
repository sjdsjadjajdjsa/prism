package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// How the loop detector can break: a reply stuck repeating itself streams on until it runs
// out of room, its junk stays on screen, the retry goes back to the same model, a tool call
// repeated with the same result goes on forever (or a normal read-edit-read is taken for a
// loop), the turn never stops after several switches, or a pinned chat claims to switch.

func from(route string, script func(http.ResponseWriter) string) func(http.ResponseWriter) string {
	return func(w http.ResponseWriter) string {
		routeHeader(w, route)
		script(w)
		return route
	}
}

// stream sends each piece as its own delta (content, reasoning_content, or a write's content).
func stream(field string, pieces ...string) func(http.ResponseWriter) string {
	var lines []string
	for _, p := range pieces {
		v, _ := json.Marshal(p)
		lines = append(lines, delta(`{"`+field+`":`+string(v)+`}`))
	}
	return sseText(append(lines, sseDone)...)
}

func loopAgent(t *testing.T, replies ...func(http.ResponseWriter) string) (*Agent, *fakeRouter, string) {
	t.Helper()
	dir := t.TempDir()
	fake := &fakeRouter{mu: &sync.Mutex{}, replies: replies}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	return newTestAgent(t, srv.URL, dir), fake, dir
}

func repeated(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func noted(events []any, part string) bool {
	return hasEvent(events, func(ev any) bool { n, ok := ev.(evNote); return ok && strings.Contains(n.text, part) })
}

func TestRepeatingAnswerIsDroppedAndAskedElsewhere(t *testing.T) {
	junk := append([]string{"Sure, let me look. "}, repeated("I will check the file now and fix it.\n", 200)...)
	a, fake, _ := loopAgent(t,
		from("looper", stream("content", junk...)),
		from("fine", say("Fixed the bug in shop.py.")))
	events, err := collect(a, "fix the bug")
	if err != nil {
		t.Fatal(err)
	}
	var shown, dropped string
	for _, ev := range events {
		switch e := ev.(type) {
		case evText:
			shown += e.text
		case evDropText:
			dropped = e.text
		}
	}
	if !strings.HasPrefix(shown, dropped) || len(dropped) == 0 || len(dropped) > 4000 {
		t.Errorf("the repeating answer must be cut early and taken back off the screen: shown %d bytes, dropped %d", len(shown), len(dropped))
	}
	if !noted(events, "repeating itself") {
		t.Errorf("the user must hear why the answer was dropped")
	}
	if last := a.messages[len(a.messages)-1]; last.Content != "Fixed the bug in shop.py." || strings.Contains(a.messages[len(a.messages)-2].Content, "I will check") {
		t.Errorf("only the good answer is kept: %+v", a.messages)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.seen) != 2 || !slices.Contains(fake.seen[1].avoid, "looper") {
		t.Errorf("the retry must go to another model: %+v", fake.seen)
	}
}

func TestRepeatingReasoningAndFileContentAreDropped(t *testing.T) {
	a, fake, dir := loopAgent(t,
		from("thinker", stream("reasoning_content", repeated("Wait, let me reconsider the approach. ", 200)...)),
		from("writer", sseText(append(append([]string{"data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"w1","function":{"name":"write","arguments":"{\"path\":\"out.txt\",\"content\":\""}}]}}]}`},
			slices.Repeat([]string{"data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"same line again\\n"}}]}}]}`}, 600)...),
			"data: "+`{"choices":[{"finish_reason":"stop","delta":{}}]}`, sseDone)...)),
		from("fine", say("done")))
	events, err := collect(a, "write it")
	if err != nil {
		t.Fatal(err)
	}
	if exists(dir, "out.txt") {
		t.Errorf("a write whose content repeats forever must not run")
	}
	n := 0
	for _, ev := range events {
		if e, ok := ev.(evNote); ok && strings.Contains(e.text, "repeating itself") {
			n++
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if n != 2 || len(fake.seen) != 3 || !slices.Contains(fake.seen[2].avoid, "thinker") || !slices.Contains(fake.seen[2].avoid, "writer") {
		t.Errorf("both looping replies must be dropped and the third model asked: notes %d, requests %d", n, len(fake.seen))
	}
}

func TestRepeatedToolCallSwitchesModel(t *testing.T) {
	read := call("r", "read", `"{\"path\":\"shop.py\"}"`)
	a, fake, dir := loopAgent(t, from("looper", read), from("looper", read), from("looper", read), from("fine", say("shop.py has a bug on line 1")))
	os.WriteFile(filepath.Join(dir, "shop.py"), []byte("def total(): pass\n"), 0o644)
	events, err := collect(a, "look at shop.py")
	if err != nil {
		t.Fatal(err)
	}
	if !noted(events, "going in circles (read shop.py), switching") {
		t.Errorf("the user must hear the model was going in circles")
	}
	var tools []string
	for _, m := range a.messages {
		if m.Role == "tool" {
			tools = append(tools, m.Content)
		}
	}
	if len(tools) != 3 || strings.Contains(tools[1], loopNudge) || !strings.HasSuffix(tools[2], loopNudge) {
		t.Errorf("the model must be told on the third repeat only: %q", tools)
	}
	for _, e := range entriesFrom(a.messages) {
		if e.kind == "tool" && e.res.summary != "1 lines" {
			t.Errorf("a reopened chat shows the read as it was, not the note to the model: %q", e.res.summary)
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !slices.Contains(fake.seen[3].avoid, "looper") || slices.Contains(fake.seen[2].avoid, "looper") {
		t.Errorf("only the step after the loop goes to another model: %v / %v", fake.seen[2].avoid, fake.seen[3].avoid)
	}
}

func TestEndlessLoopStopsTheTurn(t *testing.T) {
	read := call("r", "read", `"{\"path\":\"shop.py\"}"`)
	var replies []func(http.ResponseWriter) string
	for i := 0; i < 9; i++ {
		replies = append(replies, from("route-"+string(rune('a'+i/3)), read))
	}
	a, fake, dir := loopAgent(t, replies...)
	os.WriteFile(filepath.Join(dir, "shop.py"), []byte("x\n"), 0o644)
	events, err := collect(a, "look")
	var le *loopError
	if !errors.As(err, &le) || !strings.Contains(err.Error(), "read shop.py") {
		t.Fatalf("after %d switches the turn must stop and say on what, got %v", maxLoopSwitches, err)
	}
	if n := len(fake.seen); n != 9 {
		t.Errorf("want 9 requests before stopping, got %d", n)
	}
	if !noted(events, "switching") {
		t.Errorf("the switches must be shown on the way")
	}
	a.startLoopGuard()
	if a.loopSwitches != 0 || len(a.loopAvoid) != 0 {
		t.Errorf("the next turn starts fresh")
	}
}

func TestReadEditReadIsNotALoop(t *testing.T) {
	a, _, dir := loopAgent(t,
		call("r1", "read", `"{\"path\":\"a.txt\"}"`),
		call("e1", "edit", `"{\"path\":\"a.txt\",\"old_string\":\"one\",\"new_string\":\"two\"}"`),
		call("r2", "read", `"{\"path\":\"a.txt\"}"`),
		call("e2", "edit", `"{\"path\":\"a.txt\",\"old_string\":\"two\",\"new_string\":\"three\"}"`),
		call("r3", "read", `"{\"path\":\"a.txt\"}"`),
		say("done"))
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	events, err := collect(a, "go")
	if err != nil {
		t.Fatal(err)
	}
	if noted(events, "circles") || strings.Contains(toolOutputs(a)["r3"], loopNudge) {
		t.Errorf("reading a file again after changing it is not a loop")
	}
}

func TestPinnedModelIsToldNotSwitched(t *testing.T) {
	read := call("r", "read", `"{\"path\":\"shop.py\"}"`)
	a, fake, dir := loopAgent(t, from("mine", read), from("mine", read), from("mine", read), from("mine", say("ok")))
	os.WriteFile(filepath.Join(dir, "shop.py"), []byte("x\n"), 0o644)
	a.model = "chatgpt/gpt-6.1-sol"
	events, err := collect(a, "look")
	if err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !noted(events, "asking it to try another way") || slices.Contains(fake.seen[3].avoid, "mine") {
		t.Errorf("a pinned chat can't switch models; the model is only told")
	}
}

func TestDroppedTextLeavesTheScreen(t *testing.T) {
	m := suggestModel(t, 80, 24)
	m.handle(evText{"Here is the plan. "})
	m.handle(evText{"loop loop loop"})
	m.handle(evDropText{"Here is the plan. loop loop loop"})
	for _, e := range m.entries {
		if e.kind == "text" {
			t.Errorf("a dropped answer must leave the screen, left %q", e.text)
		}
	}
	m.handle(evToolStart{"t1", "read", "a.txt"})
	m.handle(evText{"Good part. "})
	m.handle(evText{"junk junk"})
	m.handle(evDropText{"junk junk"})
	if last := m.entries[len(m.entries)-1]; last.kind != "text" || last.text != "Good part. " {
		t.Errorf("only the dropped end is taken back: %+v", last)
	}
}

func TestRepeatingFileThatClosesIsWritten(t *testing.T) {
	content := strings.Repeat("0,0,0,0\n", 1500)
	args, _ := json.Marshal(map[string]string{"path": "zeros.csv", "content": content})
	quoted, _ := json.Marshal(string(args))
	a, _, dir := loopAgent(t, call("w1", "write", string(quoted)), say("wrote it"))
	if _, err := collect(a, "make a csv of zeros"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "zeros.csv")); string(data) != content {
		t.Errorf("a file meant to repeat must be written, got %d bytes", len(data))
	}
}

func TestWaitingAndTimingsAndPairs(t *testing.T) {
	poll := bashCall("p", "sleep 0; cat status.txt")
	a, _, dir := loopAgent(t, poll, poll, poll, poll, say("ready"))
	os.WriteFile(filepath.Join(dir, "status.txt"), []byte("building\n"), 0o644)
	events, err := collect(a, "wait for the build")
	if err != nil || noted(events, "circles") {
		t.Errorf("waiting on something is not a loop: %v", err)
	}
	if callSig("bash", `{"command":"go test"}`, "ok  prism 0.12s") != callSig("bash", `{"command":"go test"}`, "ok  prism 0.31s") {
		t.Errorf("the same run with different timings is the same result")
	}
	for _, same := range [][2]string{{"Time: 1.234 s", "Time: 2.5 s"}, {"5 passed in 0.12s", "5 passed in 3.40s"}, {"built at 12:01:33", "built at 12:02:07"}} {
		if callSig("bash", "x", same[0]) != callSig("bash", "x", same[1]) {
			t.Errorf("%q and %q differ only in time", same[0], same[1])
		}
	}
	for _, differ := range [][2]string{{"3 tests failed", "2 tests failed"}, {"progress 50%", "progress 70%"}, {"line 12", "line 14"}} {
		if callSig("bash", "x", differ[0]) == callSig("bash", "x", differ[1]) {
			t.Errorf("%q and %q are real progress", differ[0], differ[1])
		}
	}
	for cmd, want := range map[string]bool{"cat watch.json": true, "ls wait/": true, "gh run watch 123": false, "kubectl wait --for=condition=ready pod/x": false, "docker compose up --wait": false, "sleep 2 && curl localhost:3000": false, "until curl -s x; do :; done": false} {
		args, _ := json.Marshal(map[string]string{"command": cmd})
		if got := countsForLoops(FunctionCall{Name: "bash", Arguments: string(args)}); got != want {
			t.Errorf("%q counts for loops = %v, want %v", cmd, got, want)
		}
	}
	if countsForLoops(FunctionCall{Name: "browser__browser_wait_for", Arguments: "{}"}) {
		t.Errorf("an MCP tool that waits does not count")
	}
	ra, rb := call("a", "read", `"{\"path\":\"a.txt\"}"`), call("b", "read", `"{\"path\":\"b.txt\"}"`)
	b, _, dir2 := loopAgent(t, ra, rb, ra, rb, ra, rb, say("done"))
	os.WriteFile(filepath.Join(dir2, "a.txt"), []byte("a\n"), 0o644)
	os.WriteFile(filepath.Join(dir2, "b.txt"), []byte("b\n"), 0o644)
	if events, err := collect(b, "compare"); err != nil || !noted(events, "circles") {
		t.Errorf("going back and forth between two reads is a loop: %v", err)
	}
}

func TestRepeatingEveryTimeReachesTheUser(t *testing.T) {
	junk := stream("content", repeated("again and again and again. ", 200)...)
	a, _, _ := loopAgent(t, from("x", junk), from("y", junk), from("z", junk))
	if _, err := collect(a, "go"); !errors.Is(err, errLooping) {
		t.Errorf("after 3 looping replies the user must hear why, got %v", err)
	}
}

func TestDroppedReplyClearsDraftAndThinking(t *testing.T) {
	m := suggestModel(t, 80, 24)
	m.handle(evReasoning{"thinking about it"})
	m.handle(evDraft{"write", "out.txt", 6000})
	m.handle(evDropText{""})
	if m.draft != nil || m.reasoning != "" {
		t.Errorf("a dropped reply must not leave its draft or reasoning on screen: %+v %q", m.draft, m.reasoning)
	}
}

func TestRepeatingInputCutOffWithoutAnEnd(t *testing.T) {
	body := append([]string{"data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"w1","function":{"name":"write","arguments":"{\"path\":\"out.txt\",\"content\":\""}}]}}]}`},
		slices.Repeat([]string{"data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"same line again\\n"}}]}}]}`}, 600)...)
	a, _, dir := loopAgent(t, from("writer", sseText(body...)), from("fine", say("done")))
	events, err := collect(a, "write it")
	if err != nil || exists(dir, "out.txt") || !noted(events, "repeating itself") {
		t.Errorf("input cut off mid-loop with no end at all is still a loop: err %v, notes %v", err, events)
	}
}
