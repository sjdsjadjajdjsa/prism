package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	hexenc "encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type hook struct {
	Run      string `json:"run"`
	Match    string `json:"match,omitempty"`
	MustPass bool   `json:"must_pass,omitempty"`
	Timeout  int    `json:"timeout,omitempty"`
}

type hookSet struct {
	path               string
	found, trusted     bool
	problem            string
	before, after, end []hook
}

var hooksTrustMu sync.Mutex

func loadHooks(dir string) hookSet {
	h, _ := readHooks(dir)
	return h
}

// Keep the hash beside the parsed bytes so trust never covers a different read.
func readHooks(dir string) (hookSet, string) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return hookSet{problem: "could not find this folder: " + err.Error()}, ""
	}
	h := hookSet{path: filepath.Join(abs, ".prism", "hooks.json")}
	data, err := readHooksFile(h.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			h.found, h.problem = true, err.Error()
		}
		return h, ""
	}
	h.found = true
	sum := sha256.Sum256(data)
	hash := hexenc.EncodeToString(sum[:])
	if root := configDir(); root != "" {
		h.trusted = readHooksTrust(root)[abs] == hash
	}
	var events map[string]json.RawMessage
	if err := json.Unmarshal(data, &events); err != nil || events == nil {
		h.problem = "could not read hooks: .prism/hooks.json must contain a JSON object"
		return h, hash
	}
	for event := range events {
		if event != "before_bash" && event != "after_edit" && event != "turn_end" {
			h.problem = "unknown hook event: " + event
			return h, hash
		}
	}
	for _, event := range []string{"before_bash", "after_edit", "turn_end"} {
		raw, exists := events[event]
		if !exists {
			continue
		}
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			h.problem = event + " must be a list of commands"
			break
		}
		var hooks []hook
		for _, item := range items {
			var entry hook
			if err := json.Unmarshal(item, &entry.Run); err != nil {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(item, &fields); err != nil {
					h.problem = event + " hook must be a command or an object with run"
					break
				}
				for field := range fields {
					if field != "run" && field != "match" && field != "must_pass" && field != "timeout" {
						h.problem = "unknown hook field: " + field
						break
					}
				}
				if h.problem != "" {
					break
				}
				decoder := json.NewDecoder(bytes.NewReader(item))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&entry); err != nil {
					h.problem = "could not read " + event + " hook: " + err.Error()
					break
				}
			}
			if strings.TrimSpace(entry.Run) == "" {
				h.problem = event + " hook needs a command in run"
				break
			}
			if entry.Match != "" {
				if _, err := filepath.Match(entry.Match, ""); err != nil {
					h.problem = event + " hook has an invalid file pattern: " + entry.Match
					break
				}
			}
			if entry.Timeout <= 0 {
				entry.Timeout = 60
				if event == "turn_end" {
					entry.Timeout = 300
				}
			}
			entry.Timeout = min(entry.Timeout, 600)
			hooks = append(hooks, entry)
		}
		if h.problem != "" {
			break
		}
		switch event {
		case "before_bash":
			h.before = hooks
		case "after_edit":
			h.after = hooks
		case "turn_end":
			h.end = hooks
		}
	}
	if h.problem != "" {
		h.before, h.after, h.end = nil, nil, nil
	}
	return h, hash
}

const maxHooksFile = 64 << 10

// readHooksFile reads hooks.json only when it is a plain, small file: a cloned repo could
// point it at a pipe or a device and hang prism before anyone trusts anything.
func readHooksFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, errors.New("could not read .prism/hooks.json: " + err.Error())
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New(".prism/hooks.json must be a plain file")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("could not read .prism/hooks.json: " + err.Error())
	}
	defer f.Close()
	if info, err = f.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, errors.New(".prism/hooks.json must be a plain file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxHooksFile+1))
	if err != nil {
		return nil, errors.New("could not read .prism/hooks.json: " + err.Error())
	}
	if len(data) > maxHooksFile {
		return nil, errors.New(".prism/hooks.json is too big (over 64 KB)")
	}
	return data, nil
}

// lockHooksTrust keeps two prism processes from writing the trust store at once.
func lockHooksTrust(root string) (func(), error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(root, "hooks-trust.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

func (h hookSet) active() bool {
	return h.found && h.trusted && h.problem == "" && len(h.before)+len(h.after)+len(h.end) > 0
}

func readHooksTrust(root string) map[string]string {
	entries := make(map[string]string)
	data, err := os.ReadFile(filepath.Join(root, "hooks-trust.json"))
	if err == nil {
		if err := json.Unmarshal(data, &entries); err != nil || entries == nil {
			entries = make(map[string]string)
		}
	}
	return entries
}

func writeHooksTrust(root string, entries map[string]string) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	// writeAtomic uses 0644; a private staging folder keeps it private until chmod.
	stage, err := os.MkdirTemp(root, ".hooks-trust-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	path := filepath.Join(stage, "hooks-trust.json")
	if err := writeAtomic(path, data); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(root, "hooks-trust.json"))
}

func trustHooks(dir string) (hookSet, error) {
	hooksTrustMu.Lock()
	defer hooksTrustMu.Unlock()
	h, hash := readHooks(dir)
	if h.problem != "" {
		return h, errors.New(h.problem)
	}
	if !h.found {
		return h, errors.New("no .prism/hooks.json in this folder")
	}
	root := configDir()
	if root == "" {
		return h, errors.New("could not find your settings folder; hooks stay off")
	}
	unlock, err := lockHooksTrust(root)
	if err != nil {
		return h, fmt.Errorf("could not save hook trust: %w", err)
	}
	defer unlock()
	entries := readHooksTrust(root)
	entries[filepath.Dir(filepath.Dir(h.path))] = hash
	if err := writeHooksTrust(root, entries); err != nil {
		return h, fmt.Errorf("could not save hook trust: %w", err)
	}
	// Re-read in case the file changed while trust was being saved.
	return loadHooks(dir), nil
}

type hookRun struct {
	event, run      string
	code            int
	ok              bool
	output, summary string
}

func runHook(ctx context.Context, dir, event string, entry hook, extra string) hookRun {
	r := hookRun{event: event, run: entry.Run}
	abs, err := filepath.Abs(dir)
	if err != nil {
		r.code, r.output, r.summary = -1, err.Error(), err.Error()
		return r
	}
	timeout := time.Duration(entry.Timeout) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "bash", "-c", entry.Run)
	cmd.Dir = abs
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = append(os.Environ(), "PRISM_EVENT="+event, "PRISM_PROJECT_DIR="+abs)
	if event == "before_bash" {
		cmd.Env = append(cmd.Env, "PRISM_COMMAND="+extra)
	} else if event == "after_edit" {
		cmd.Env = append(cmd.Env, "PRISM_FILE="+extra)
	}
	out := &headTailBuffer{headLimit: 2000, tailLimit: 4000}
	cmd.Stdout, cmd.Stderr = out, out
	err = cmd.Run()
	if cmd.Process != nil {
		// Hooks cannot leave child commands running after the shell has finished.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	r.output = strings.TrimSpace(out.String())
	if cmd.ProcessState != nil {
		r.code = cmd.ProcessState.ExitCode()
	} else if err != nil {
		r.code = -1
	}
	r.ok = err == nil && r.code == 0 && runCtx.Err() == nil
	r.summary = fmt.Sprintf("exit %d", r.code)
	if r.output != "" {
		lines := strings.Split(r.output, "\n")
		last := strings.TrimSpace(lines[len(lines)-1])
		if r.ok {
			r.summary = "→ " + last
		} else {
			r.summary += " · " + last
		}
	}
	if ctx.Err() != nil {
		r.summary = "stopped by the user"
	} else if runCtx.Err() != nil {
		r.summary = fmt.Sprintf("stopped after %s timeout", timeout)
	} else if err != nil && r.output == "" {
		r.output = err.Error()
	}
	return r
}

// Recheck the file before each event: an old snapshot must not bypass new trust.
func (h hookSet) current(dir string) hookSet {
	if !h.active() {
		return hookSet{}
	}
	return loadHooks(dir)
}

// beforeBash blocks with no runs when hooks that were on at the start are not trusted any more.
func (h hookSet) beforeBash(ctx context.Context, dir, command string) (runs []hookRun, blocked bool) {
	was := h.active()
	h = h.current(dir)
	if !h.active() {
		return nil, was
	}
	for _, entry := range h.before {
		if ctx.Err() != nil {
			return runs, true
		}
		r := runHook(ctx, dir, "before_bash", entry, command)
		runs = append(runs, r)
		if !r.ok {
			return runs, true
		}
	}
	return runs, false
}

func (h hookSet) afterEdit(ctx context.Context, dir, relPath string) []hookRun {
	h = h.current(dir)
	if !h.active() {
		return nil
	}
	var runs []hookRun
	for _, entry := range h.after {
		if ctx.Err() != nil {
			break
		}
		if entry.Match != "" {
			full, _ := filepath.Match(entry.Match, relPath)
			base, _ := filepath.Match(entry.Match, filepath.Base(relPath))
			if !full && !base {
				continue
			}
		}
		runs = append(runs, runHook(ctx, dir, "after_edit", entry, relPath))
	}
	return runs
}

// turnEnd fails with no runs when hooks that were on at the start are not trusted any more.
func (h hookSet) turnEnd(ctx context.Context, dir string) (runs []hookRun, failed bool) {
	was := h.active()
	h = h.current(dir)
	if !h.active() {
		return nil, was
	}
	for _, entry := range h.end {
		if ctx.Err() != nil {
			break
		}
		r := runHook(ctx, dir, "turn_end", entry, "")
		runs = append(runs, r)
		failed = failed || (entry.MustPass && !r.ok)
	}
	return runs, failed
}

func hookFeedback(runs []hookRun) string {
	var lines []string
	for _, r := range runs {
		line := fmt.Sprintf("%s hook `%s` exit %d", r.event, r.run, r.code)
		if output := strings.TrimSpace(r.output); output != "" {
			line += "\n" + output
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n\n")
}

func hooksView(dir string, args []string) (title string, lines []string, footer string) {
	title = "hooks"
	h := loadHooks(dir)
	if len(args) > 0 {
		if len(args) != 1 || (args[0] != "trust" && args[0] != "off") {
			return title, []string{"use /hooks, /hooks trust, or /hooks off"}, ""
		}
		if args[0] == "off" {
			hooksTrustMu.Lock()
			defer hooksTrustMu.Unlock()
			if root := configDir(); root != "" {
				abs, err := filepath.Abs(dir)
				if err != nil {
					return title, []string{"could not find this folder: " + err.Error()}, ""
				}
				unlock, err := lockHooksTrust(root)
				if err != nil {
					return title, []string{"could not turn hooks off: " + err.Error()}, ""
				}
				defer unlock()
				entries := readHooksTrust(root)
				delete(entries, abs)
				if err := writeHooksTrust(root, entries); err != nil {
					return title, []string{"could not turn hooks off: " + err.Error()}, ""
				}
			}
			return "hooks off", nil, "off until you trust them · /hooks trust"
		}
		var err error
		h, err = trustHooks(dir)
		if err != nil {
			return title, []string{err.Error()}, "fix .prism/hooks.json"
		}
		if h.trusted {
			title = "hooks on"
		}
	}
	if h.problem != "" {
		return title, []string{h.problem}, "fix .prism/hooks.json"
	}
	if !h.found {
		return title, []string{
			"hooks run your own commands before a command, after an edit, or when a turn ends.",
			"add .prism/hooks.json, then use /hooks trust to turn them on.",
			`example: {"turn_end": ["go test ./..."]}`,
		}, "see the README for every option"
	}
	var labels, runs []string
	for _, group := range []struct {
		label string
		hooks []hook
	}{{"before command", h.before}, {"after edit", h.after}, {"turn end", h.end}} {
		for _, entry := range group.hooks {
			label := group.label
			if entry.Match != "" {
				label += " (" + entry.Match + ")"
			}
			if entry.MustPass {
				label += " (must pass)"
			}
			labels, runs = append(labels, label), append(runs, entry.Run)
		}
	}
	width := 0
	for _, l := range labels {
		width = max(width, len([]rune(l)))
	}
	for i, l := range labels {
		lines = append(lines, padRunes(l, width+2)+runs[i])
	}
	if len(lines) == 0 {
		lines = []string{"no hook commands yet"}
	}
	footer = "off until you trust them · /hooks trust"
	if h.trusted {
		footer = "on"
	}
	return title, lines, footer
}

// hookRetryPrefix starts the message a failed turn_end check sends back to the model, so a
// reopened chat shows it as a note, not as something the user typed.
const hookRetryPrefix = "A turn_end check failed, so the work is not finished yet:"

const maxHookTries = 3

// startHooks loads this turn's hooks and says once when a hooks file is off or broken.
func (a *Agent) startHooks(emit func(any)) {
	a.hookTries, a.turnChanged, a.hooks = 0, false, hookSet{}
	if a.role != "" {
		return
	}
	a.hooks = loadHooks(a.dir)
	if a.hooksNoted {
		return
	}
	switch {
	case a.hooks.problem != "":
		emit(evNote{"hooks are off: " + a.hooks.problem + " · /hooks"})
	case a.hooks.found && !a.hooks.trusted:
		emit(evNote{"hooks in .prism/hooks.json are off until you run /hooks trust"})
	default:
		return
	}
	a.hooksNoted = true
}

// bashWithHooks runs the before_bash hooks; one that fails stops the command.
func (a *Agent) bashWithHooks(ctx context.Context, raw string) toolResult {
	if runs, blocked := a.hooks.beforeBash(ctx, a.dir, sarg(argsOf(raw), "command")); blocked {
		if ctx.Err() != nil {
			return fail("stopped by the user")
		}
		if len(runs) == 0 {
			r := fail("commands are stopped: .prism/hooks.json changed during this turn, so its checks are off until the user runs /hooks trust again")
			r.summary = "stopped: hooks changed"
			return r
		}
		r := fail("a before_bash hook stopped this command\n" + hookFeedback(runs))
		r.summary = "stopped by a hook"
		return r
	}
	if strings.TrimSpace(sarg(argsOf(raw), "command")) != "" {
		a.turnChanged = true
	}
	return runTool(ctx, a.dir, "bash", raw)
}

// afterTool runs the after_edit hooks on a file the AI just wrote and tells it what they did.
// Crew and MCP calls mark the turn changed in the agent loop, bash in bashWithHooks.
func (a *Agent) afterTool(ctx context.Context, call FunctionCall, result *toolResult) {
	if (call.Name != "edit" && call.Name != "write") || !result.ok {
		return
	}
	a.turnChanged = true
	path := sarg(argsOf(call.Arguments), "path")
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.dir, path)
	}
	rel, err := filepath.Rel(a.dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return
	}
	runs := a.hooks.afterEdit(ctx, a.dir, rel)
	if len(runs) == 0 {
		return
	}
	result.output += "\n\nafter_edit hooks ran (the file may have changed, read it again before editing it):\n" + hookFeedback(runs)
	for _, r := range runs {
		if !r.ok {
			result.summary += " · after-edit hook failed"
			break
		}
	}
}

// turnEndHooks runs the turn_end checks when the AI is about to finish a turn that changed
// something. It reports true when a must_pass check failed and the AI should keep working.
func (a *Agent) turnEndHooks(ctx context.Context, emit func(any)) bool {
	if a.role != "" || !a.turnChanged || a.plan.Load() || len(a.hooks.end) == 0 || !a.hooks.active() {
		return false
	}
	id := fmt.Sprintf("hooks-%d", a.steps)
	emit(evToolStart{id, "hooks", "turn end"})
	runs, failed := a.hooks.turnEnd(ctx, a.dir)
	if failed && len(runs) == 0 && ctx.Err() == nil {
		// An edit, even by the AI, must not quietly switch the checks off.
		emit(evToolDone{id, toolResult{summary: "skipped: hooks.json changed"}})
		emit(evNote{".prism/hooks.json changed during this turn, so the turn-end checks were skipped · /hooks trust turns them back on"})
		return false
	}
	emit(evToolDone{id, toolResult{ok: !failed, summary: hooksSummary(runs)}})
	if !failed || ctx.Err() != nil {
		return false
	}
	if a.hookTries >= maxHookTries {
		emit(evNote{fmt.Sprintf("turn-end checks still fail after %d tries · prism stopped there", maxHookTries)})
		return false
	}
	a.hookTries++
	var bad []hookRun
	for _, r := range runs {
		if !r.ok {
			bad = append(bad, r)
		}
	}
	a.messages = append(a.messages, Message{Role: "user", Content: hookRetryPrefix + "\n\n" + hookFeedback(bad) + "\n\nFix the cause, then finish."})
	a.rememberBrief()
	return true
}

func hooksSummary(runs []hookRun) string {
	for _, r := range runs {
		if !r.ok {
			return clipWidth(r.run, 32) + " · " + r.summary
		}
	}
	if len(runs) == 1 {
		return runs[0].summary
	}
	return fmt.Sprintf("%d checks passed", len(runs))
}
