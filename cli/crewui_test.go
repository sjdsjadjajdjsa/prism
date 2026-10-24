package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

var crewDemoTasks = []crewTask{
	{ID: "api", Role: "code", Task: "build the checkout api with validation, error states and a retry friendly submit flow", Files: []string{"api.go"}},
	{ID: "ui", Role: "fast", Task: "build the checkout form", Files: []string{"ui.js"}},
	{ID: "docs", Role: "code", Task: "write the readme", Files: []string{"README.md"}},
}

func crewRows(cv *canvas) []string {
	var rows []string
	for y := 0; y < cv.h; y++ {
		var b strings.Builder
		for _, c := range cv.cells[y*cv.w : (y+1)*cv.w] {
			if c.ch != 0 {
				b.WriteRune(c.ch)
			}
		}
		rows = append(rows, strings.TrimRight(b.String(), " "))
	}
	return rows
}

func crewScreen(m model) string {
	t := themes[m.theme]
	cv := newCanvas(m.w, m.h, t.bg, t.fg)
	drawChat(cv, t, m)
	return strings.Join(crewRows(cv), "\n")
}

func crewTestModel(w, h int) model {
	m := model{tab: &tab{busy: true, events: make(chan any, 8)}, w: w, h: h, chat: true, chatAt: -time.Hour, now: time.Second, lastTextAt: -time.Hour, sayAt: -time.Hour,
		project: "~/shop"}
	m.entries = []*entry{{kind: "user", text: "build the checkout"}}
	return m
}

func feed(m *model, events ...any) {
	for _, e := range events {
		m.handle(e)
	}
}

func TestCrewPanelRendersAtTwoWidths(t *testing.T) {
	steps := [][]any{
		{evToolStart{"c1", "crew", "3 workers"}, evCrewStart{crewDemoTasks},
			evCrewWorker{id: "api", model: "auto:code", status: "queued"}, evCrewWorker{id: "ui", model: "auto:fast", status: "queued"}, evCrewWorker{id: "docs", model: "auto:code", status: "queued"}},
		{evCrewWorker{id: "api", model: "nvidia/nemotron-3-ultra:free", status: "running", steps: 2, note: "writing api.go"},
			evCrewWorker{id: "ui", model: "groq/llama-4-scout:free", status: "running", steps: 1}},
		{evCrewWorker{id: "api", model: "nvidia/nemotron-3-ultra:free", status: "done", steps: 5},
			evCrewWorker{id: "ui", model: "groq/llama-4-scout:free", status: "failed", steps: 3, note: "the router gave up"},
			evCrewWorker{id: "docs", model: "auto:code", status: "cancelled", note: "stopped before it started"},
			evCrewWorker{id: "review", model: "auto:review", status: "running", steps: 1, note: "reading api.go"}},
		{evCrewWorker{id: "review", model: "auto:review", status: "done", steps: 2}, evCrewDone{"1 of 3 workers done, 2 did not finish"}},
	}
	long := map[int]int{}
	for _, size := range [][2]int{{80, 24}, {140, 40}} {
		m := crewTestModel(size[0], size[1])
		for i, step := range steps {
			feed(&m, step...)
			screen := crewScreen(m)
			for y, row := range strings.Split(screen, "\n") {
				if n := len([]rune(row)); n > size[0] {
					t.Fatalf("%dx%d step %d row %d is %d wide: %q", size[0], size[1], i, y, n, row)
				}
			}
			for _, want := range []string{"crew · 3 workers", "api", "ui", "docs"} {
				if !strings.Contains(screen, want) {
					t.Errorf("%dx%d step %d: missing %q\n%s", size[0], size[1], i, want, screen)
				}
			}
			if strings.Contains(screen, ":free") || strings.Contains(screen, "nvidia/") {
				t.Errorf("model name not shortened:\n%s", screen)
			}
			if i >= 2 && !strings.Contains(screen, "crew · 3 workers + review") {
				t.Errorf("step %d: header lacks review:\n%s", i, screen)
			}
			if i == 1 && !strings.Contains(screen, "writing api.go") {
				t.Errorf("running note not shown:\n%s", screen)
			}
			if i == 3 {
				for _, want := range []string{"✓", "✕", "–", "1 of 3 workers done, 2 did not finish", "nemotron-3-ultra"} {
					if !strings.Contains(screen, want) {
						t.Errorf("%dx%d final: missing %q\n%s", size[0], size[1], want, screen)
					}
				}
				for _, row := range strings.Split(screen, "\n") {
					if strings.Contains(row, "build the checkout api") {
						long[size[0]] = len([]rune(row))
					}
				}
				t.Logf("%dx%d\n%s", size[0], size[1], screen)
			}
		}
	}
	if long[140] <= long[80] {
		t.Errorf("wider terminal shows no more task text: 80=%d 140=%d", long[80], long[140])
	}
}

func TestCrewPanelStaysInsideTheColumn(t *testing.T) {
	c := newCrewBlock(evCrewStart{crewDemoTasks})
	c.worker(evCrewWorker{id: "api", model: "a/very-long-model-name-that-keeps-going:free", status: "running", steps: 120, note: strings.Repeat("note ", 40)})
	c.worker(evCrewWorker{id: "review", model: "auto:review", status: "queued"})
	c.finish(strings.Repeat("summary ", 30))
	for width := 30; width <= 100; width += 5 {
		for i, l := range crewLines(themes[0], c, width, 500) {
			n := l.indent
			for _, s := range l.segs {
				n += len([]rune(s.text))
			}
			if n > width-2 {
				t.Errorf("width %d line %d is %d wide", width, i, n)
			}
		}
	}
}

func TestCrewStopEndsRowsCancelled(t *testing.T) {
	m := crewTestModel(80, 24)
	feed(&m, evToolStart{"c1", "crew", "x"}, evCrewStart{crewDemoTasks},
		evCrewWorker{id: "api", model: "auto:code", status: "running"},
		evCrewWorker{id: "ui", model: "auto:fast", status: "queued"},
		evDone{context.Canceled})
	for _, r := range m.entries[1].crew.rows {
		if r.Status != "cancelled" {
			t.Errorf("%s ended %s, want cancelled", r.ID, r.Status)
		}
	}
	if strings.ContainsAny(crewScreen(m), string(crewSpinner)) {
		t.Error("a spinner is still drawn after stop")
	}
}

func TestCrewEventsTurnTheToolRowIntoTheBlock(t *testing.T) {
	m := crewTestModel(80, 24)
	feed(&m, evToolStart{"c1", "crew", "x"}, evCrewStart{crewDemoTasks})
	if len(m.entries) != 2 || m.entries[1].kind != "crew" {
		t.Fatalf("entries = %+v", m.entries)
	}
	if text, _ := m.speaking(); text != "splitting the light." {
		t.Errorf("shard said %q", text)
	}
	if m.mood() != moodWorking {
		t.Errorf("mood = %v, want working", m.mood())
	}
	feed(&m, evCrewDone{"3 of 3 workers done"}, evToolDone{"c1", toolResult{ok: true}})
	if text, _ := m.speaking(); text != "converged." {
		t.Errorf("shard said %q", text)
	}
	if !m.entries[1].done || !m.entries[1].crew.done {
		t.Error("crew entry not finished")
	}
}

func TestCrewApprovalShowsWorkerAndQueue(t *testing.T) {
	m := crewTestModel(80, 24)
	feed(&m, evToolStart{"c1", "crew", "x"}, evCrewStart{crewDemoTasks})
	first, second := make(chan answer, 1), make(chan answer, 1)
	feed(&m, evApprove{approval{tool: "bash", arg: "worker ui: npm test"}, first}, evApprove{approval{tool: "edit", arg: "worker docs: README.md"}, second})
	screen := crewScreen(m)
	for _, want := range []string{"worker ui", "wants to run this command", "npm test", "+1 waiting"} {
		if !strings.Contains(screen, want) {
			t.Errorf("missing %q\n%s", want, screen)
		}
	}
	m.answer(answerYes)
	if <-first != answerYes || m.pending == nil || m.pending.reply != second || len(m.queued) != 0 {
		t.Fatalf("queue did not advance: %+v", m.pending)
	}
	if strings.Contains(crewScreen(m), "waiting") {
		t.Error("queue note remains with nothing queued")
	}
}

func TestCrewHistoryKeepsFinalState(t *testing.T) {
	args := string(mustJSON(map[string]any{"tasks": crewDemoTasks, "review": true}))
	output := "[api] done (code, 5 steps) files: api.go\nall good\n\n[ui] failed (fast, 3 steps) files: ui.js\nrouter gave up\n\n[docs] cancelled (code, 0 steps) files: README.md\nstopped\n\nReview:\nlooks fine\n\nNow run the project's checks yourself."
	messages := []Message{
		{Role: "user", Content: "build it"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "crew", Arguments: args}}}},
		{Role: "tool", ToolCallID: "c1", Content: output},
	}
	entries := entriesFrom(messages)
	if len(entries) != 2 || entries[1].kind != "crew" || !entries[1].crew.done {
		t.Fatalf("entries = %+v", entries)
	}
	want := map[string]string{"api": "done", "ui": "failed", "docs": "cancelled", "review": "done"}
	for _, r := range entries[1].crew.rows {
		if want[r.ID] != r.Status {
			t.Errorf("%s = %s, want %s", r.ID, r.Status, want[r.ID])
		}
	}
	if got := entries[1].crew.summary; got != "1 of 3 workers done, 2 did not finish" {
		t.Errorf("summary = %q", got)
	}
	m := crewTestModel(80, 24)
	m.busy, m.entries = false, entries
	if screen := crewScreen(m); !strings.Contains(screen, "crew · 3 workers + review") || !strings.Contains(screen, "✕") {
		t.Errorf("history block not drawn:\n%s", screen)
	}
	// a plan the tool refused stays an ordinary tool row
	refused := entriesFrom([]Message{messages[0], messages[1], {Role: "tool", ToolCallID: "c1", Content: "Error: the crew plan has problems"}})
	if refused[1].kind != "tool" {
		t.Errorf("refused plan became %s", refused[1].kind)
	}
}

func TestCrewEventsAsJSON(t *testing.T) {
	cases := []struct {
		event any
		want  string
	}{
		{evCrewStart{[]crewTask{{ID: "api", Role: "code", Task: "build <b>x</b>"}}}, `{"tasks":[{"files":[],"id":"api","role":"code","task":"build \u003cb\u003ex\u003c/b\u003e"}],"type":"crew_start"}`},
		{evCrewWorker{id: "review", model: "auto:review", status: "running", steps: 2, note: "reading"}, `{"id":"review","model":"auto:review","note":"reading","status":"running","steps":2,"type":"crew_worker"}`},
		{evCrewDone{"2 of 2 workers done"}, `{"summary":"2 of 2 workers done","type":"crew_done"}`},
	}
	for _, c := range cases {
		v, ok := crewWire(c.event)
		if !ok {
			t.Fatalf("%T not wired", c.event)
		}
		data, _ := json.Marshal(v)
		if string(data) != c.want {
			t.Errorf("%T\n got %s\nwant %s", c.event, data, c.want)
		}
	}
	if _, ok := crewWire(evText{"x"}); ok {
		t.Error("a text event was treated as a crew event")
	}
}

func TestCrewHistoryOverTheWire(t *testing.T) {
	_, s, _ := testServer(t)
	args := string(mustJSON(map[string]any{"tasks": crewDemoTasks[:1]}))
	s.agent.messages = append(s.agent.messages,
		Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "crew", Arguments: args}}}},
		Message{Role: "tool", ToolCallID: "c1", Content: "[api] done (code, 4 steps) files: api.go\nok"})
	s.mu.Lock()
	snap := s.snapshot()
	s.mu.Unlock()
	last := snap[len(snap)-1]
	data, _ := json.Marshal(last)
	var got struct {
		Kind string `json:"kind"`
		Crew struct {
			Rows []map[string]any `json:"rows"`
		} `json:"crew"`
	}
	if err := json.Unmarshal(data, &got); err != nil || got.Kind != "crew" || len(got.Crew.Rows) != 1 || got.Crew.Rows[0]["status"] != "done" || fmt.Sprint(got.Crew.Rows[0]["steps"]) != "4" {
		t.Fatalf("history entry = %s", data)
	}
}
