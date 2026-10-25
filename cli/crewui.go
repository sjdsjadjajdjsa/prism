package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// crewRow is one worker line of the live crew panel. The json tags are the
// shape the browser receives, both live (crew_worker) and from history.
type crewRow struct {
	ID     string `json:"id"`
	Role   string `json:"role,omitempty"`
	Task   string `json:"task,omitempty"`
	Model  string `json:"model,omitempty"`
	Status string `json:"status"`
	Steps  int    `json:"steps,omitempty"`
	Note   string `json:"note,omitempty"`
}

type crewBlock struct {
	rows    []crewRow
	review  bool
	summary string
	done    bool
}

type wireCrew struct {
	Rows    []crewRow `json:"rows"`
	Review  bool      `json:"review,omitempty"`
	Summary string    `json:"summary,omitempty"`
	Done    bool      `json:"done,omitempty"`
}

func newCrewBlock(e evCrewStart) *crewBlock {
	c := &crewBlock{}
	for _, t := range e.tasks {
		c.rows = append(c.rows, crewRow{ID: t.ID, Role: t.Role, Task: t.Task, Model: "auto:" + t.Role, Status: "queued"})
	}
	return c
}

func (c *crewBlock) worker(e evCrewWorker) {
	for i := range c.rows {
		if c.rows[i].ID == e.id {
			r := &c.rows[i]
			r.Model, r.Status, r.Steps, r.Note = e.model, e.status, e.steps, e.note
			return
		}
	}
	row := crewRow{ID: e.id, Model: e.model, Status: e.status, Steps: e.steps, Note: e.note}
	if e.id == "review" {
		c.review = true
		row.Task = "review what changed"
	}
	c.rows = append(c.rows, row)
}

func (c *crewBlock) finish(summary string) {
	c.summary, c.done = summary, true
}

// settle ends every row that never reached a final state as cancelled, so a
// stopped crew does not keep spinning.
func (c *crewBlock) settle() {
	for i := range c.rows {
		if s := c.rows[i].Status; s == "queued" || s == "running" {
			c.rows[i].Status = "cancelled"
		}
	}
	if !c.done {
		c.done = true
	}
}

func (c *crewBlock) workers() int {
	n := len(c.rows)
	if c.review && n > 0 {
		n--
	}
	return n
}

func (c *crewBlock) wire() *wireCrew {
	rows := append([]crewRow{}, c.rows...)
	return &wireCrew{Rows: rows, Review: c.review, Summary: c.summary, Done: c.done}
}

// crewWire turns the three crew events into the JSON events the browser reads.
func crewWire(event any) (map[string]any, bool) {
	switch e := event.(type) {
	case evCrewStart:
		tasks := make([]map[string]any, 0, len(e.tasks))
		for _, t := range e.tasks {
			files := t.Files
			if files == nil {
				files = []string{}
			}
			tasks = append(tasks, map[string]any{"id": t.ID, "role": t.Role, "task": t.Task, "files": files})
		}
		return map[string]any{"type": "crew_start", "tasks": tasks}, true
	case evCrewWorker:
		return map[string]any{"type": "crew_worker", "id": e.id, "model": e.model, "status": e.status, "steps": e.steps, "note": e.note}, true
	case evCrewDone:
		return map[string]any{"type": "crew_done", "summary": e.summary}, true
	}
	return nil, false
}

// crewModel is the short model name: no provider prefix, no :free.
func crewModel(model string) string {
	model = model[strings.LastIndex(model, "/")+1:]
	return strings.TrimSuffix(model, ":free")
}

var crewResultLine = regexp.MustCompile(`(?m)^\[([^\]\n]+)\] (\w+) \((\w+), (\d+) steps\)`)

// crewFromHistory rebuilds a finished crew block from the tool call and its result.
func crewFromHistory(args, output string) *crewBlock {
	var plan struct {
		Tasks  []crewTask `json:"tasks"`
		Review bool       `json:"review"`
	}
	if json.Unmarshal([]byte(args), &plan) != nil || len(plan.Tasks) == 0 {
		return nil
	}
	found := crewResultLine.FindAllStringSubmatch(output, -1)
	if len(found) == 0 {
		return nil
	}
	c := newCrewBlock(evCrewStart{tasks: plan.Tasks})
	for _, m := range found {
		steps, _ := strconv.Atoi(m[4])
		for i := range c.rows {
			if c.rows[i].ID == m[1] {
				c.rows[i].Status, c.rows[i].Steps = m[2], steps
			}
		}
	}
	done, failed := 0, 0
	for i := range c.rows {
		if c.rows[i].Status == "queued" {
			c.rows[i].Status = "cancelled"
		}
		if c.rows[i].Status == "done" {
			done++
		} else {
			failed++
		}
	}
	if plan.Review {
		status := "done"
		if _, after, ok := strings.Cut(output, "Review:\n"); !ok || strings.HasPrefix(strings.TrimSpace(after), "skipped") {
			status = "cancelled"
		}
		c.worker(evCrewWorker{id: "review", model: "auto:review", status: status})
	}
	summary := fmt.Sprintf("%d of %d workers done", done, done+failed)
	if failed > 0 {
		summary += fmt.Sprintf(", %d did not finish", failed)
	}
	c.finish(summary)
	return c
}

var crewSpinner = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// crewLines draws the crew block: a header, one row per worker, and the final
// summary. width is the transcript column; n is the animation clock in ms.
func crewLines(t theme, c *crewBlock, width int, n float64) []line {
	avail := width - 4
	count := len(c.rows)
	noun := "workers"
	if c.workers() == 1 {
		noun = "worker"
	}
	tail := fmt.Sprintf(" · %d %s", c.workers(), noun)
	if c.review {
		tail += " + review"
	}
	tail = clipWidth(tail, avail-6)
	out := []line{{indent: 2, segs: []seg{
		{text: "◆ ", grad: true, phase: 0.55 + n/9000},
		{text: "crew", grad: true, phase: 0.55 + n/9000, bold: true},
		{text: tail, fg: t.muted},
	}}}

	idW, modelW := 2, 0
	for _, r := range c.rows {
		idW = max(idW, len([]rune(r.ID)))
		modelW = max(modelW, len([]rune(crewModel(r.Model))))
	}
	idW, modelW = min(idW, 10), min(modelW, 18)
	// glyph, id, task, [model], status, steps
	rest := func(model bool) int {
		w := 2 + idW + 2 + 2 + 1 + 4
		if model {
			w += 2 + modelW
		}
		return avail - w
	}
	showModel := modelW > 0 && rest(true) >= 18
	taskW := rest(showModel)
	if taskW < 6 {
		idW = max(2, idW-(6-taskW))
		taskW = rest(showModel)
	}
	taskW = max(taskW, 4)
	dim := mix(t.fg, t.muted, 0.4)

	for i, r := range c.rows {
		hue := t.at(float64(i) / float64(max(1, count)))
		status, sc := "·", t.muted
		switch r.Status {
		case "running":
			status, sc = string(crewSpinner[int(n/80)%len(crewSpinner)]), hue
		case "done":
			status, sc = "✓", t.green
		case "failed":
			status, sc = "✕", t.red
		case "cancelled":
			status, sc = "–", t.muted
		}
		idCol := t.fg
		if r.Status == "queued" || r.Status == "cancelled" {
			idCol = dim
		}
		task := clipWidth(strings.Join(strings.Fields(r.Task), " "), taskW)
		segs := []seg{
			{text: "◆ ", fg: hue},
			{text: padBytes(clipWidth(r.ID, idW), idW), fg: idCol, bold: r.Status == "running"},
			{text: "  " + padBytes(task, taskW), fg: dim},
		}
		if showModel {
			segs = append(segs, seg{text: "  " + padBytes(clipWidth(crewModel(r.Model), modelW), modelW), fg: t.muted})
		}
		steps := "    "
		if r.Steps > 0 {
			steps = fmt.Sprintf(" %3d", r.Steps)
		}
		segs = append(segs, seg{text: "  "}, seg{text: status, fg: sc}, seg{text: steps, fg: mix(t.bg, t.muted, 0.8)})
		out = append(out, line{indent: 2, segs: segs})
		if r.Note != "" && (r.Status == "running" || r.Status == "failed") {
			note := clipWidth(strings.Join(strings.Fields(r.Note), " "), avail-6-idW)
			out = append(out, line{indent: 2, segs: []seg{{text: strings.Repeat(" ", 4+idW) + "↳ ", fg: mix(t.bg, t.muted, 0.5)}, {text: note, fg: mix(t.bg, t.muted, 0.7)}}})
		}
	}
	if c.done && c.summary != "" {
		mark, col := "✓ ", t.green
		for _, r := range c.rows {
			if r.Status != "done" {
				mark, col = "– ", t.muted
				if r.Status == "failed" {
					mark, col = "✕ ", t.red
					break
				}
			}
		}
		out = append(out, line{indent: 2, segs: []seg{{text: mark, fg: col}, {text: clipWidth(c.summary, avail-2), fg: t.muted}}})
	}
	return out
}
