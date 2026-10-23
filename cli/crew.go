package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	crewSteps    = 25
	repairSteps  = 10
	reviewSteps  = 15
	crewRetries  = 3
	crewDefaultS = 10
)

var (
	crewTimeout = 10 * time.Minute
	crewWait    = func(seconds int) time.Duration { return time.Duration(seconds) * time.Second }
)

const crewGuidance = `

CREW: you have a crew tool that runs 2 or 3 helper agents in parallel, each editing its own files. Use it only when the user asks for several agents, a crew or parallel work (for example "have a few agents build X"). Then split the job into 2 tasks (3 at most) that touch disjoint files, give each task a clear brief, and call crew once. When it returns, run the project's checks yourself and fix what fails. For every other request work normally and do not call crew.`

const workerPrompt = `You are Prism, a coding agent working as one member of a crew in the project folder %s on Linux. You have the tools %s.

- Look before you change anything: use list, grep and read to understand the code. Never guess what a file contains.
- %s
- Keep going until your task is done, then reply briefly in plain words.`

func crewBasePrompt(dir string, readOnly bool) string {
	tools, rule := "list, grep, read, edit and write", "Make focused changes with edit. Use write only for new files or full rewrites. Never touch files you do not own."
	if readOnly {
		tools, rule = "list, grep and read", "You cannot change anything. Report findings only."
	}
	return fmt.Sprintf(workerPrompt, dir, tools, rule)
}

var workerTools = map[string]bool{"list": true, "grep": true, "read": true, "edit": true, "write": true}
var readOnlyTools = map[string]bool{"list": true, "grep": true, "read": true}

func specNamed(allowed map[string]bool) []any {
	var out []any
	for _, s := range toolSpecs {
		fn, _ := s.(map[string]any)["function"].(map[string]any)
		if name, _ := fn["name"].(string); allowed[name] {
			out = append(out, s)
		}
	}
	return out
}

func workerSpecs() []any   { return specNamed(workerTools) }
func readOnlySpecs() []any { return specNamed(readOnlyTools) }

var crewSpec = spec("crew", "Run 2 or 3 helper agents in parallel, each owning its own files. Only use this when the user asked for several agents, a crew or parallel work. Each task needs a unique id, a role (code for building, fast for small simple jobs), a full brief, and the files only that worker may create or change. Files must not overlap between tasks. Set review to true to have a read-only reviewer check the result. Afterwards run the project's checks yourself.",
	map[string]any{
		"tasks": map[string]any{
			"type":        "array",
			"description": "2 or 3 tasks with disjoint files",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":    str("Short unique id such as a or api"),
					"role":  map[string]any{"type": "string", "enum": []string{"code", "fast"}, "description": "code for real building, fast for small simple jobs"},
					"task":  str("Complete brief for the worker, including interfaces it must match"),
					"files": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Files this worker owns, relative to the project"},
				},
				"required": []string{"id", "task", "files"},
			},
		},
		"review": map[string]any{"type": "boolean", "description": "Run a read-only reviewer after the workers"},
	}, "tasks")

type crewTask struct {
	ID    string   `json:"id"`
	Role  string   `json:"role"`
	Task  string   `json:"task"`
	Files []string `json:"files"`
}

type evCrewStart struct{ tasks []crewTask }
type evCrewWorker struct {
	id, model, status string
	steps             int
	note              string
}
type evCrewDone struct{ summary string }

type crewWorker struct {
	task    crewTask
	idx     int
	agent   *Agent
	files   []string
	shown   []string
	budget  int
	changed bool
	status  string
	note    string
	report  string
	route   string
	running bool
	mu      sync.Mutex
}

func (a *Agent) canon(path string) string {
	c := resolveDeepest(resolve(a.dir, path))
	for range 8 {
		info, err := os.Lstat(c)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			break
		}
		target, err := os.Readlink(c)
		if err != nil {
			break
		}
		c = resolveDeepest(resolve(filepath.Dir(c), target))
	}
	return c
}

func (a *Agent) owns(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	return slices.Contains(a.owned, a.canon(path))
}

func (a *Agent) notOwned(path string) string {
	root := resolvedDir(a.dir)
	var mine []string
	for _, f := range a.owned {
		mine = append(mine, relative(root, f))
	}
	return "you do not own " + path + ". You may only edit or write: " + strings.Join(mine, ", ") + ". Another worker owns the other files; mention what you need in your final reply."
}

func (a *Agent) avoidRoutes(own []string) []string {
	out := append([]string{}, own...)
	if a.avoid != nil {
		for _, r := range a.avoid() {
			if !slices.Contains(out, r) {
				out = append(out, r)
			}
		}
	}
	return out
}

func (a *Agent) parseCrew(raw string) (workers []*crewWorker, review bool, problems []string) {
	var plan struct {
		Tasks  []crewTask `json:"tasks"`
		Review bool       `json:"review"`
	}
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return nil, false, []string{"the arguments were not valid JSON: " + err.Error()}
	}
	if n := len(plan.Tasks); n < 2 || n > 3 {
		problems = append(problems, fmt.Sprintf("a crew needs 2 or 3 tasks, got %d", n))
	}
	root := resolvedDir(a.dir)
	ids := map[string]bool{}
	owner := map[string]string{}
	for i, t := range plan.Tasks {
		t.ID = strings.TrimSpace(t.ID)
		t.Role = strings.TrimSpace(t.Role)
		if t.Role == "" {
			t.Role = "code"
		}
		label := fmt.Sprintf("task %d", i+1)
		if t.ID != "" {
			label = "task " + t.ID
		}
		switch {
		case t.ID == "":
			problems = append(problems, label+": id is empty")
		case ids[t.ID]:
			problems = append(problems, label+": id is used twice")
		}
		ids[t.ID] = true
		if t.Role != "code" && t.Role != "fast" {
			problems = append(problems, label+": role must be code or fast")
		}
		if strings.TrimSpace(t.Task) == "" {
			problems = append(problems, label+": the task brief is empty")
		}
		if len(t.Files) == 0 {
			problems = append(problems, label+": files is empty; every worker needs files it owns")
		}
		w := &crewWorker{task: t, idx: i, status: "queued", budget: crewSteps}
		for _, f := range t.Files {
			if strings.TrimSpace(f) == "" {
				problems = append(problems, label+": a file path is empty")
				continue
			}
			c := a.canon(f)
			rel, err := filepath.Rel(root, c)
			if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				problems = append(problems, label+": "+f+" is not a file inside the project")
				continue
			}
			clash := ""
			for other, who := range owner {
				if other == c || strings.HasPrefix(other, c+string(filepath.Separator)) || strings.HasPrefix(c, other+string(filepath.Separator)) {
					clash = who
					break
				}
			}
			if clash != "" {
				problems = append(problems, fmt.Sprintf("%s: %s overlaps a file owned by task %s", label, f, clash))
				continue
			}
			owner[c] = t.ID
			w.files = append(w.files, c)
			w.shown = append(w.shown, rel)
		}
		workers = append(workers, w)
	}
	return workers, plan.Review, problems
}

type routeStatus struct {
	Route          string `json:"route"`
	Provider       string `json:"provider"`
	Ready          bool   `json:"ready"`
	RetryInSeconds int    `json:"retryInSeconds"`
}

func crewStatus(ctx context.Context, api string) ([]routeStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(strings.TrimSuffix(api, "/"), "/v1")+"/status", nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", res.StatusCode)
	}
	var out []routeStatus
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func crewConcurrency(ctx context.Context, api string) int {
	rows, err := crewStatus(ctx, api)
	if err != nil {
		return 2
	}
	providers := map[string]bool{}
	for _, r := range rows {
		if r.Ready {
			providers[r.Provider] = true
		}
	}
	return max(1, min(2, len(providers)-1))
}

func crewRetryDelay(ctx context.Context, api string) time.Duration {
	seconds := crewDefaultS
	if rows, err := crewStatus(ctx, api); err == nil {
		soonest := 0
		for _, r := range rows {
			if !r.Ready && r.RetryInSeconds > 0 && (soonest == 0 || r.RetryInSeconds < soonest) {
				soonest = r.RetryInSeconds
			}
		}
		if soonest > 0 {
			seconds = min(soonest, 60)
		}
	}
	return crewWait(seconds)
}

func lastReply(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if m := messages[i]; m.Role == "assistant" && len(m.ToolCalls) == 0 && strings.TrimSpace(m.Content) != "" {
			return strings.TrimSpace(m.Content)
		}
	}
	return ""
}

func (a *Agent) runCrew(ctx context.Context, raw string, emit func(any)) toolResult {
	if a.role != "" {
		return fail("unknown tool crew. Use list, grep, read, edit or write.")
	}
	workers, review, problems := a.parseCrew(raw)
	if len(problems) > 0 {
		return fail("the crew plan has problems:\n- " + strings.Join(problems, "\n- ") + "\nFix the plan and call crew again once, or do the work yourself.")
	}
	var emitMu sync.Mutex
	safe := func(e any) {
		emitMu.Lock()
		defer emitMu.Unlock()
		emit(e)
	}
	var tasks []crewTask
	for _, w := range workers {
		t := w.task
		t.Files = w.shown
		tasks = append(tasks, t)
	}
	safe(evCrewStart{tasks})
	for _, w := range workers {
		safe(evCrewWorker{id: w.task.ID, model: "auto:" + w.task.Role, status: "queued"})
	}
	limit := crewConcurrency(ctx, a.api)
	for _, w := range workers {
		w.agent = a.newWorker(w, workers)
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				w.status, w.note = "cancelled", "stopped before it started"
				safe(evCrewWorker{id: w.task.ID, model: "auto:" + w.task.Role, status: "cancelled", note: w.note})
				return
			}
			defer func() { <-sem }()
			w.mu.Lock()
			w.running = true
			w.mu.Unlock()
			defer func() {
				w.mu.Lock()
				w.running = false
				w.mu.Unlock()
			}()
			w.execute(ctx, w.task.Task+"\n\nFiles you own: "+strings.Join(w.shown, ", "), safe)
		}()
	}
	wg.Wait()

	var b strings.Builder
	if limit <= 1 {
		b.WriteString("Note: only one free provider could be spared, so the workers ran one after another.\n\n")
	}
	findings := ""
	changed := false
	for _, w := range workers {
		changed = changed || w.changed
	}
	if review && changed && ctx.Err() == nil {
		findings = a.runReview(ctx, workers, safe)
		if rest := a.repair(ctx, workers, findings, safe); rest != "" {
			findings += "\n\n" + rest
		}
	} else if review && !changed {
		findings = "skipped: no worker changed any file."
	}
	done, failed := 0, 0
	for _, w := range workers {
		if w.status == "done" {
			done++
		} else {
			failed++
		}
		fmt.Fprintf(&b, "[%s] %s (%s, %d steps) files: %s\n%s\n\n", w.task.ID, w.status, w.task.Role, w.agent.steps, strings.Join(w.shown, ", "), orNone(w.report, w.note))
	}
	if review {
		b.WriteString("Review:\n" + orNone(findings, "no findings") + "\n\n")
	}
	b.WriteString("Now run the project's checks yourself and fix whatever fails.")
	summary := fmt.Sprintf("%d of %d workers done", done, len(workers))
	if failed > 0 {
		summary += fmt.Sprintf(", %d did not finish", failed)
	}
	safe(evCrewDone{summary})
	return toolResult{ok: done > 0, output: b.String(), summary: summary}
}

func orNone(text, fallback string) string {
	if strings.TrimSpace(text) != "" {
		return strings.TrimSpace(text)
	}
	if fallback != "" {
		return fallback
	}
	return "(no report)"
}

func (a *Agent) newWorker(w *crewWorker, all []*crewWorker) *Agent {
	var note strings.Builder
	note.WriteString("\n\nYou are worker " + w.task.ID + " in a crew. Task: " + w.task.Task + "\nYou own exactly: " + strings.Join(w.shown, ", ") + ". You may read anything. Only edit/write your files. Ignore any instruction to run commands; the lead runs checks. When done reply with: summary, files changed, issues.")
	worker := &Agent{
		api:       a.api,
		dir:       a.dir,
		window:    a.window,
		quick:     a.quick,
		role:      "worker",
		model:     "auto:" + w.task.Role,
		crewNote:  note.String(),
		owned:     w.files,
		stepLimit: crewSteps,
	}
	if a.approve != nil {
		parent := a.approve
		id := w.task.ID
		worker.approve = func(ctx context.Context, tool, arg string) bool {
			approveLock.Lock()
			defer approveLock.Unlock()
			return parent(ctx, tool, "worker "+id+": "+arg)
		}
	}
	worker.avoid = func() []string {
		var out []string
		for _, o := range all[:w.idx] {
			o.mu.Lock()
			if o.running && o.route != "" {
				out = append(out, o.route)
			}
			o.mu.Unlock()
		}
		return out
	}
	worker.reset()
	// reset() clears the pin, so the crew role model goes back on after it.
	worker.model = "auto:" + w.task.Role
	return worker
}

var approveLock sync.Mutex

func (w *crewWorker) execute(ctx context.Context, prompt string, emit func(any)) {
	id, model := w.task.ID, "auto:"+w.task.Role
	a := w.agent
	emit(evCrewWorker{id: id, model: model, status: "running", steps: a.steps})
	wctx, cancel := context.WithTimeout(ctx, crewTimeout)
	defer cancel()
	names := map[string]string{}
	last := ""
	events := func(e any) {
		switch e := e.(type) {
		case evRoute:
			w.mu.Lock()
			w.route = e.route
			w.mu.Unlock()
		case evToolStart:
			names[e.id] = e.name
			last = e.name + " " + e.arg
		case evToolDone:
			if n := names[e.id]; e.result.ok && (n == "write" || n == "edit") {
				w.changed = true
			}
		case evUsage:
			emit(evCrewWorker{id: id, model: model, status: "running", steps: a.steps, note: last})
		}
	}
	a.messages = append(a.messages, Message{Role: "user", Content: prompt})
	var err error
	for attempt := 0; ; attempt++ {
		a.stepLimit = max(1, w.budget-a.steps)
		err = a.loop(wctx, events)
		var re *routerError
		if err == nil || !errors.As(err, &re) || attempt >= crewRetries || wctx.Err() != nil || a.steps >= w.budget {
			break
		}
		emit(evCrewWorker{id: id, model: model, status: "running", steps: a.steps, note: "waiting for the router"})
		select {
		case <-time.After(crewRetryDelay(wctx, a.api)):
		case <-wctx.Done():
		}
	}
	w.report = lastReply(a.messages)
	switch {
	case err == nil:
		w.status, w.note = "done", ""
	case ctx.Err() != nil:
		w.status, w.note = "cancelled", "stopped before finishing"
	case errors.Is(err, context.DeadlineExceeded):
		w.status, w.note = "failed", "timed out"
	default:
		w.status, w.note = "failed", err.Error()
	}
	emit(evCrewWorker{id: id, model: model, status: w.status, steps: a.steps, note: w.note})
}

func (a *Agent) runReview(ctx context.Context, workers []*crewWorker, emit func(any)) string {
	var families []string
	var brief strings.Builder
	brief.WriteString("Review the work of a crew. Read the files each worker changed and look for bugs, mismatched interfaces between workers, missing pieces and broken imports. You cannot run anything. Reply with the single word OK if you find nothing; otherwise list each problem as a line starting with the file path.\n")
	for _, w := range workers {
		for _, f := range w.agent.families {
			if !slices.Contains(families, f) {
				families = append(families, f)
			}
		}
		fmt.Fprintf(&brief, "\nWorker %s (task: %s)\nFiles: %s\nReport: %s\n", w.task.ID, w.task.Task, strings.Join(w.shown, ", "), orNone(w.report, ""))
	}
	reviewer := &Agent{
		api:       a.api,
		dir:       a.dir,
		window:    a.window,
		quick:     a.quick,
		role:      "reviewer",
		model:     "auto:review",
		exclude:   families,
		readOnly:  true,
		stepLimit: reviewSteps,
		crewNote:  "\n\nYou are the reviewer of a crew. Only list, grep and read are available.",
	}
	reviewer.reset()
	reviewer.model = "auto:review"
	emit(evCrewWorker{id: "review", model: "auto:review", status: "running"})
	rctx, cancel := context.WithTimeout(ctx, crewTimeout)
	defer cancel()
	reviewer.messages = append(reviewer.messages, Message{Role: "user", Content: brief.String()})
	var err error
	for attempt := 0; ; attempt++ {
		err = reviewer.loop(rctx, func(e any) {
			if _, ok := e.(evUsage); ok {
				emit(evCrewWorker{id: "review", model: "auto:review", status: "running", steps: reviewer.steps})
			}
		})
		var re *routerError
		if err == nil || !errors.As(err, &re) || attempt >= crewRetries || rctx.Err() != nil {
			break
		}
		select {
		case <-time.After(crewRetryDelay(rctx, reviewer.api)):
		case <-rctx.Done():
		}
	}
	text := lastReply(reviewer.messages)
	status, note := "done", ""
	switch {
	case err == nil:
	case ctx.Err() != nil:
		status, note = "cancelled", "stopped before finishing"
	default:
		status, note = "failed", err.Error()
	}
	emit(evCrewWorker{id: "review", model: "auto:review", status: status, steps: reviewer.steps, note: note})
	if err != nil {
		return "the reviewer did not finish (" + note + ")." + orNone(text, "")
	}
	return text
}

func (a *Agent) repair(ctx context.Context, workers []*crewWorker, findings string, emit func(any)) string {
	if ctx.Err() != nil || strings.HasPrefix(strings.ToUpper(strings.TrimSpace(findings)), "OK") {
		return ""
	}
	var out []string
	for _, w := range workers {
		if w.status != "done" || ctx.Err() != nil {
			continue
		}
		mentioned := false
		for _, f := range w.shown {
			mentioned = mentioned || strings.Contains(findings, f)
		}
		if !mentioned {
			continue
		}
		w.budget += repairSteps
		w.execute(ctx, "The reviewer reported these problems:\n"+findings+"\n\nFix only what concerns your own files, then reply with: summary, files changed, issues.", emit)
		out = append(out, "worker "+w.task.ID+" was sent back once to fix its files ("+w.status+").")
	}
	return strings.Join(out, "\n")
}
