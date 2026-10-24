package main

import (
	"context"
	"crypto/sha256"
	hexenc "encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func hooksTestProject(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	return t.TempDir()
}

func hooksTestWrite(t *testing.T, dir, text string) {
	t.Helper()
	path := filepath.Join(dir, ".prism", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hooksTestTrust(t *testing.T, dir, text string) hookSet {
	t.Helper()
	hooksTestWrite(t, dir, text)
	h, err := trustHooks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !h.active() {
		t.Fatalf("hooks not active: %+v", h)
	}
	return h
}

func TestHooksParsing(t *testing.T) {
	dir := hooksTestProject(t)
	h := loadHooks(dir)
	if h.found || h.active() || h.problem != "" || h.path != filepath.Join(dir, ".prism", "hooks.json") {
		t.Fatalf("missing file: %+v", h)
	}
	hooksTestWrite(t, dir, `{
		"before_bash": ["echo first", {"run":"echo second", "timeout":900, "must_pass":true}],
		"after_edit": [{"run":"gofmt -w \"$PRISM_FILE\"", "match":"*.go", "timeout":2}],
		"turn_end": ["echo done", {"run":"echo default", "timeout":-1}]
	}`)
	h = loadHooks(dir)
	if !h.found || h.trusted || h.active() || h.problem != "" {
		t.Fatalf("parsed hooks: %+v", h)
	}
	if len(h.before) != 2 || h.before[0].Run != "echo first" || h.before[0].Timeout != 60 || h.before[1].Timeout != 600 || !h.before[1].MustPass {
		t.Fatalf("before hooks: %+v", h.before)
	}
	if len(h.after) != 1 || h.after[0].Match != "*.go" || h.after[0].Timeout != 2 {
		t.Fatalf("after hooks: %+v", h.after)
	}
	if len(h.end) != 2 || h.end[0].Timeout != 300 || h.end[1].Timeout != 300 {
		t.Fatalf("end hooks: %+v", h.end)
	}
	for _, text := range []string{
		`{"before_bash":[{"run":"true", "timeout":0}]}`,
		`{"after_edit":["true"]}`,
	} {
		hooksTestWrite(t, dir, text)
		h = loadHooks(dir)
		entries := append(h.before, h.after...)
		if h.problem != "" || len(entries) != 1 || entries[0].Timeout != 60 {
			t.Fatalf("default timeout: %+v", h)
		}
	}
}

func TestHooksInvalidFile(t *testing.T) {
	dir := hooksTestProject(t)
	for name, text := range map[string]string{
		"bad JSON":      `{`,
		"trailing JSON": `{} {}`,
		"null":          `null`,
		"array":         `[]`,
		"unknown event": `{"before_bash":["true"],"before_edit":[]}`,
		"unknown field": `{"before_bash":["true"],"turn_end":[{"run":"true","typo":1}]}`,
		"wrong case":    `{"turn_end":[{"RUN":"true"}]}`,
		"empty run":     `{"turn_end":[{"run":"  "}]}`,
		"empty string":  `{"turn_end":[""]}`,
		"missing run":   `{"turn_end":[{"match":"*.go"}]}`,
		"bad type":      `{"turn_end":[42]}`,
		"bad timeout":   `{"turn_end":[{"run":"true","timeout":"1"}]}`,
		"bad list":      `{"turn_end":"true"}`,
		"null list":     `{"turn_end":null}`,
		"null entry":    `{"turn_end":[null]}`,
		"bad pattern":   `{"after_edit":[{"run":"true","match":"["}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			hooksTestWrite(t, dir, text)
			h := loadHooks(dir)
			if !h.found || h.problem == "" || h.active() || len(h.before)+len(h.after)+len(h.end) != 0 {
				t.Fatalf("invalid hooks not ignored: %+v", h)
			}
			if _, err := trustHooks(dir); err == nil {
				t.Fatal("trusted invalid hooks")
			}
		})
	}
	if err := os.Remove(filepath.Join(dir, ".prism", "hooks.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := trustHooks(dir); err == nil {
		t.Fatal("trusted missing hooks")
	}
}

func TestHooksTrustAndChanges(t *testing.T) {
	dir := hooksTestProject(t)
	text := `{"before_bash":["echo allowed"]}`
	hooksTestWrite(t, dir, text)
	if runs, blocked := loadHooks(dir).beforeBash(context.Background(), dir, "echo main"); len(runs) != 0 || blocked {
		t.Fatalf("untrusted hooks ran: %+v, %v", runs, blocked)
	}
	h, err := trustHooks(dir)
	if err != nil || !h.trusted || !h.active() {
		t.Fatalf("trust: %+v, %v", h, err)
	}
	path := filepath.Join(configDir(), "hooks-trust.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries map[string]string
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(text))
	if entries[dir] != hexenc.EncodeToString(sum[:]) {
		t.Fatalf("wrong trust hash: %s", data)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("trust permissions: %v, %v", info, err)
	}
	hooksTestWrite(t, dir, text+"\n")
	if h := loadHooks(dir); h.trusted || h.active() {
		t.Fatalf("changed file stayed trusted: %+v", h)
	}
	if runs, _ := h.beforeBash(context.Background(), dir, "true"); len(runs) != 0 {
		t.Fatalf("stale snapshot bypassed trust: %+v", runs)
	}
	if _, err := trustHooks(dir); err != nil || !loadHooks(dir).active() {
		t.Fatalf("retrust: %v", err)
	}
}

func TestHooksTrustStore(t *testing.T) {
	dir := hooksTestProject(t)
	other := t.TempDir()
	hooksTestTrust(t, dir, `{"turn_end":["true"]}`)
	hooksTestTrust(t, other, `{"turn_end":["echo other"]}`)
	if !loadHooks(dir).trusted || !loadHooks(other).trusted {
		t.Fatal("trusting another project lost existing trust")
	}
	title, _, _ := hooksView(dir, []string{"off"})
	if title != "hooks off" || loadHooks(dir).trusted || !loadHooks(other).trusted {
		t.Fatal("turning off one project changed another")
	}
	path := filepath.Join(configDir(), "hooks-trust.json")
	for _, corrupt := range []string{"{broken", "null", `{"bad":true}`} {
		if err := os.WriteFile(path, []byte(corrupt), 0o600); err != nil {
			t.Fatal(err)
		}
		if loadHooks(other).trusted {
			t.Fatal("corrupt store granted trust")
		}
		if _, err := trustHooks(dir); err != nil || !loadHooks(dir).trusted {
			t.Fatalf("could not replace corrupt store: %v", err)
		}
	}
	hooksTestWrite(t, dir, `{}`)
	h, err := trustHooks(dir)
	if err != nil || !h.trusted || h.active() {
		t.Fatalf("empty hooks: %+v, %v", h, err)
	}
}

func TestHooksNoConfigDir(t *testing.T) {
	dir := hooksTestProject(t)
	hooksTestWrite(t, dir, `{"turn_end":["true"]}`)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if configDir() != "" {
		t.Fatal("expected no settings folder")
	}
	if _, err := trustHooks(dir); err == nil || loadHooks(dir).trusted {
		t.Fatal("trust succeeded without settings folder")
	}
}

func TestHooksBeforeBash(t *testing.T) {
	dir := hooksTestProject(t)
	h := hooksTestTrust(t, dir, `{"before_bash":["echo first", "echo denied; exit 2", "touch should-not-exist"]}`)
	runs, blocked := h.beforeBash(context.Background(), dir, "rm something")
	if !blocked || len(runs) != 2 || !runs[0].ok || runs[1].ok || runs[1].code != 2 {
		t.Fatalf("before results: %+v, blocked %v", runs, blocked)
	}
	if runs[0].summary != "→ first" || runs[1].summary != "exit 2 · denied" {
		t.Fatalf("summaries: %+v", runs)
	}
	if _, err := os.Stat(filepath.Join(dir, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatalf("hook after failure ran: %v", err)
	}
	h = hooksTestTrust(t, dir, `{"before_bash":["printf ok", "true"]}`)
	if runs, blocked := h.beforeBash(context.Background(), dir, "true"); blocked || len(runs) != 2 || !runs[1].ok {
		t.Fatalf("successful hooks blocked: %+v, %v", runs, blocked)
	}
}

func TestHooksEnvironmentAndMatch(t *testing.T) {
	dir := hooksTestProject(t)
	h := hooksTestTrust(t, dir, `{
		"before_bash":["printf '%s\\n' \"$PRISM_EVENT\" \"$PRISM_PROJECT_DIR\" \"$PRISM_COMMAND\" \"$PWD\""],
		"after_edit":[
			{"run":"printf '%s\\n' \"$PRISM_EVENT\" \"$PRISM_PROJECT_DIR\" \"$PRISM_FILE\"", "match":"*.go"},
			{"run":"echo full", "match":"src/*.go"},
			{"run":"echo skip", "match":"*.md"},
			"echo every"
		],
		"turn_end":["echo \"$PRISM_EVENT\""]
	}`)
	command := "echo 'a command with spaces'"
	runs, blocked := h.beforeBash(context.Background(), dir, command)
	want := strings.Join([]string{"before_bash", dir, command, dir}, "\n")
	if blocked || len(runs) != 1 || runs[0].output != want {
		t.Fatalf("before env: %+v", runs)
	}
	runs = h.afterEdit(context.Background(), dir, "src/main.go")
	want = strings.Join([]string{"after_edit", dir, "src/main.go"}, "\n")
	if len(runs) != 3 || runs[0].output != want || runs[1].output != "full" || runs[2].output != "every" {
		t.Fatalf("after env/matches: %+v", runs)
	}
	runs = h.afterEdit(context.Background(), dir, "src/deep/main.go")
	if len(runs) != 2 || runs[1].output != "every" {
		t.Fatalf("glob crossed directory boundary: %+v", runs)
	}
	runs, failed := h.turnEnd(context.Background(), dir)
	if failed || len(runs) != 1 || runs[0].output != "turn_end" {
		t.Fatalf("turn env: %+v, %v", runs, failed)
	}
}

func TestHooksTurnEnd(t *testing.T) {
	dir := hooksTestProject(t)
	for _, mustPass := range []bool{false, true} {
		text := `{"turn_end":[{"run":"echo failed; exit 3","must_pass":false},"echo last"]}`
		if mustPass {
			text = strings.Replace(text, `"must_pass":false`, `"must_pass":true`, 1)
		}
		h := hooksTestTrust(t, dir, text)
		runs, failed := h.turnEnd(context.Background(), dir)
		if failed != mustPass || len(runs) != 2 || runs[0].ok || !runs[1].ok || runs[1].output != "last" {
			t.Fatalf("must_pass %v: %+v, failed %v", mustPass, runs, failed)
		}
	}
	h := hooksTestTrust(t, dir, `{"turn_end":[{"run":"true","must_pass":true}]}`)
	if _, failed := h.turnEnd(context.Background(), dir); failed {
		t.Fatal("successful must_pass hook failed")
	}
}

func TestHooksTimeoutAndCancel(t *testing.T) {
	dir := hooksTestProject(t)
	for _, canceled := range []bool{false, true} {
		h := hooksTestTrust(t, dir, `{"before_bash":[{"run":"echo started; sleep 30", "timeout":1}]}`)
		ctx, cancel := context.WithCancel(context.Background())
		var timer *time.Timer
		if canceled {
			timer = time.AfterFunc(100*time.Millisecond, cancel)
		}
		start := time.Now()
		runs, blocked := h.beforeBash(ctx, dir, "true")
		cancel()
		if timer != nil {
			timer.Stop()
		}
		if time.Since(start) > 4*time.Second || !blocked || len(runs) != 1 || runs[0].ok || !strings.Contains(runs[0].output, "started") {
			t.Fatalf("timeout/cancel %v: %+v, blocked %v, elapsed %s", canceled, runs, blocked, time.Since(start))
		}
		want := "stopped after 1s timeout"
		if canceled {
			want = "stopped by the user"
		}
		if runs[0].summary != want {
			t.Fatalf("summary = %q, want %q", runs[0].summary, want)
		}
	}
}

func TestHooksCanceledBeforeStart(t *testing.T) {
	dir := hooksTestProject(t)
	h := hooksTestTrust(t, dir, `{"before_bash":["touch ran"],"after_edit":["touch ran"],"turn_end":["touch ran"]}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if runs, blocked := h.beforeBash(ctx, dir, "true"); len(runs) != 0 || !blocked {
		t.Fatalf("canceled before: %+v, %v", runs, blocked)
	}
	if runs := h.afterEdit(ctx, dir, "main.go"); len(runs) != 0 {
		t.Fatalf("canceled after: %+v", runs)
	}
	if runs, _ := h.turnEnd(ctx, dir); len(runs) != 0 {
		t.Fatalf("canceled end: %+v", runs)
	}
	if _, err := os.Stat(filepath.Join(dir, "ran")); !os.IsNotExist(err) {
		t.Fatalf("canceled hooks ran: %v", err)
	}
}

func TestHooksOutputAndFeedback(t *testing.T) {
	dir := hooksTestProject(t)
	h := hooksTestTrust(t, dir, `{"turn_end":["printf head; head -c 12000 /dev/zero | tr '\\0' x; printf tail"]}`)
	runs, _ := h.turnEnd(context.Background(), dir)
	if len(runs) != 1 || !runs[0].ok || len(runs[0].output) > 6200 || !strings.HasPrefix(runs[0].output, "head") || !strings.HasSuffix(runs[0].output, "tail") || !strings.Contains(runs[0].output, "bytes omitted") {
		t.Fatalf("bounded output: %+v", runs)
	}
	if hookFeedback(nil) != "" {
		t.Fatal("empty feedback is not empty")
	}
	feedback := hookFeedback([]hookRun{
		{event: "before_bash", run: "echo yes", code: 0, output: "  yes\n"},
		{event: "turn_end", run: "exit 2", code: 2},
	})
	if feedback != "before_bash hook `echo yes` exit 0\nyes\n\nturn_end hook `exit 2` exit 2" {
		t.Fatalf("feedback: %q", feedback)
	}
}

func TestHooksView(t *testing.T) {
	dir := hooksTestProject(t)
	title, lines, footer := hooksView(dir, nil)
	if title != "hooks" || len(lines) != 3 || !strings.Contains(strings.Join(lines, "\n"), ".prism/hooks.json") || !strings.Contains(lines[2], "turn_end") || footer != "see the README for every option" {
		t.Fatalf("missing view: %q, %q, %q", title, lines, footer)
	}
	_, lines, _ = hooksView(dir, []string{"trust"})
	if len(lines) != 1 || !strings.Contains(lines[0], "no .prism/hooks.json") {
		t.Fatalf("missing trust view: %q", lines)
	}
	hooksTestWrite(t, dir, `{"before_bash":["./scripts/guard.sh"],"after_edit":[{"run":"gofmt -w \"$PRISM_FILE\"","match":"*.go"}],"turn_end":[{"run":"go test ./...","must_pass":true}]}`)
	want := "before command        ./scripts/guard.sh\nafter edit (*.go)     gofmt -w \"$PRISM_FILE\"\nturn end (must pass)  go test ./..."
	title, lines, footer = hooksView(dir, nil)
	if title != "hooks" || strings.Join(lines, "\n") != want || footer != "off until you trust them · /hooks trust" {
		t.Fatalf("untrusted view: %q, %q, %q", title, lines, footer)
	}
	title, lines, footer = hooksView(dir, []string{"trust"})
	if title != "hooks on" || strings.Join(lines, "\n") != want || footer != "on" {
		t.Fatalf("trust view: %q, %q, %q", title, lines, footer)
	}
	_, _, footer = hooksView(dir, nil)
	if footer != "on" {
		t.Fatalf("trusted footer: %q", footer)
	}
	title, _, footer = hooksView(dir, []string{"off"})
	if title != "hooks off" || footer != "off until you trust them · /hooks trust" || loadHooks(dir).trusted {
		t.Fatalf("off view: %q, %q", title, footer)
	}
	hooksTestWrite(t, dir, `{"oops":[]}`)
	for _, args := range [][]string{nil, {"trust"}} {
		_, lines, footer = hooksView(dir, args)
		if len(lines) != 1 || !strings.Contains(lines[0], "unknown hook event") || footer != "fix .prism/hooks.json" {
			t.Fatalf("problem view: %q, %q", lines, footer)
		}
	}
	_, lines, _ = hooksView(dir, []string{"unknown"})
	if len(lines) != 1 || !strings.Contains(lines[0], "use /hooks") {
		t.Fatalf("usage view: %q", lines)
	}
	hooksTestWrite(t, dir, `{}`)
	_, lines, _ = hooksView(dir, nil)
	if len(lines) != 1 || lines[0] != "no hook commands yet" {
		t.Fatalf("empty view: %q", lines)
	}
}
