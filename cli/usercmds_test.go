package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// savedEnv isolates the user config dir and returns (project dir, user commands dir).
func savedEnv(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return t.TempDir(), filepath.Join(home, ".config", "prism", "commands")
}

func writeCmd(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name+".md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseUserCommand(t *testing.T) {
	for _, tc := range []struct{ in, desc, body string }{
		{"---\ndescription: review the diff\n---\n\nReview $ARGUMENTS\n", "review the diff", "Review $ARGUMENTS"},
		{"\ufeff---\r\ndescription: \"quoted\"\r\n---\r\nbody\r\n", "quoted", "body"},
		{"# Fix the build\n\nRun the tests and fix what fails.", "Fix the build", "# Fix the build\n\nRun the tests and fix what fails."},
		{"---\n---\nonly body", "only body", "only body"},
		{"---\ndescription: x\n---\n", "x", ""},
	} {
		desc, body := parseUserCommand(tc.in)
		if desc != tc.desc || body != tc.body {
			t.Errorf("parse %q = (%q, %q), want (%q, %q)", tc.in, desc, body, tc.desc, tc.body)
		}
	}
}

func TestExpandUserCommand(t *testing.T) {
	for _, tc := range []struct{ body, args, want string }{
		{"Review $ARGUMENTS carefully", "the login page", "Review the login page carefully"},
		{"Fix issue $1 in $2, then $3", "42 api.go", "Fix issue 42 in api.go, then "},
		{"Rename $1 to $2", `"old name" new`, "Rename old name to new"},
		{"Write tests.", "for parser.go", "Write tests.\n\nfor parser.go"},
		{"Write tests.", "", "Write tests."},
		{"Use $ARGUMENTS and $ARGUMENTS", "x y", "Use x y and x y"},
		{"Line\n$ARGUMENTS", "two\nlines", "Line\ntwo\nlines"},
	} {
		if got := expandUserCommand(tc.body, tc.args); got != tc.want {
			t.Errorf("expand(%q, %q) = %q, want %q", tc.body, tc.args, got, tc.want)
		}
	}
}

func TestLoadUserCommandsProjectWinsAndClashes(t *testing.T) {
	dir, mine := savedEnv(t)
	proj := filepath.Join(dir, ".prism", "commands")
	writeCmd(t, mine, "review", "---\ndescription: my review\n---\nmine")
	writeCmd(t, mine, "standup", "Write my standup notes.")
	writeCmd(t, proj, "review", "---\ndescription: project review\n---\nproject")
	writeCmd(t, proj, "models", "clashes with a built in")
	writeCmd(t, proj, "Bad_Name", "ignored name")
	writeCmd(t, proj, "empty", "---\ndescription: nothing\n---\n")

	list := loadUserCommands(dir, 0)
	byKey := map[string]userCommand{}
	for _, c := range list {
		byKey[c.source+" "+c.name] = c
	}
	if c, ok := findUserCommand(dir, "/review"); !ok || c.body != "project" || c.source != "this project" {
		t.Fatalf("project should win: %+v %v", c, ok)
	}
	if c := byKey["yours /review"]; c.problem == "" || !strings.Contains(c.problem, "this project") {
		t.Errorf("shadowed user command should say so: %+v", c)
	}
	if c, ok := findUserCommand(dir, "/standup"); !ok || c.help != "Write my standup notes." {
		t.Errorf("standup: %+v %v", c, ok)
	}
	if c := byKey["this project /models"]; !strings.Contains(c.problem, "built in") {
		t.Errorf("clash should warn: %+v", c)
	}
	if _, ok := findUserCommand(dir, "/models"); ok {
		t.Error("a file must never replace a built-in command")
	}
	if _, ok := findUserCommand(dir, "/empty"); ok {
		t.Error("an empty file is not runnable")
	}
	found := false
	for _, c := range list {
		if strings.Contains(c.file, "Bad_Name") {
			found = true
			if c.problem == "" {
				t.Errorf("bad name should warn: %+v", c)
			}
		}
	}
	if !found {
		t.Error("a badly named file should still be listed with a warning")
	}
}

func TestLoadUserCommandsReloadsOnChange(t *testing.T) {
	dir, _ := savedEnv(t)
	proj := filepath.Join(dir, ".prism", "commands")
	if _, ok := findUserCommand(dir, "/hello"); ok {
		t.Fatal("nothing saved yet")
	}
	p := writeCmd(t, proj, "hello", "say hi")
	if c, ok := findUserCommand(dir, "/hello"); !ok || c.body != "say hi" {
		t.Fatalf("new file not picked up: %+v", c)
	}
	if err := os.WriteFile(p, []byte("say hello there"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	os.Chtimes(p, later, later)
	if c, _ := findUserCommand(dir, "/hello"); c.body != "say hello there" {
		t.Fatalf("edit not picked up: %+v", c)
	}
	os.Remove(p)
	if _, ok := findUserCommand(dir, "/hello"); ok {
		t.Fatal("removed file still listed")
	}
}

func TestMatchingInListsSavedCommands(t *testing.T) {
	dir, mine := savedEnv(t)
	writeCmd(t, mine, "mreview", "---\ndescription: review my change\n---\nReview it")
	writeCmd(t, mine, "model", "clash")
	got := matchingIn(dir, "/m")
	names := map[string]int{}
	for _, c := range got {
		names[c.name]++
		if c.name == "/mreview" && (c.help != "review my change" || !c.web) {
			t.Errorf("saved row: %+v", c)
		}
	}
	if names["/mreview"] != 1 || names["/model"] != 1 || names["/models"] != 1 {
		t.Errorf("matching /m: %v", got)
	}
	if got := matchingIn(dir, "/mreview x"); len(got) != 0 {
		t.Errorf("space ends suggestions: %v", got)
	}
}

func TestNewUserCommand(t *testing.T) {
	dir, _ := savedEnv(t)
	for _, bad := range []string{"", "Bad", "a b", "../x", "-x"} {
		if msg := newUserCommand(dir, bad); !strings.Contains(msg, "lowercase") {
			t.Errorf("newUserCommand(%q) = %q", bad, msg)
		}
	}
	if msg := newUserCommand(dir, "models"); !strings.Contains(msg, "built in") {
		t.Errorf("built-in name: %q", msg)
	}
	msg := newUserCommand(dir, "/review-pr")
	if !strings.Contains(msg, "wrote .prism/commands/review-pr.md") {
		t.Fatalf("new: %q", msg)
	}
	c, ok := findUserCommand(dir, "/review-pr")
	if !ok || !strings.Contains(c.body, "$ARGUMENTS") || c.help == "" {
		t.Fatalf("starter file: %+v %v", c, ok)
	}
	if got := expandUserCommand(c.body, "the login code"); got != "Replace this line with your message. the login code" {
		t.Errorf("starter message = %q", got)
	}
	if msg := newUserCommand(dir, "review-pr"); !strings.Contains(msg, "already exists") {
		t.Errorf("duplicate: %q", msg)
	}
}

func TestCommandsViewGroups(t *testing.T) {
	dir, mine := savedEnv(t)
	writeCmd(t, mine, "standup", "---\ndescription: my standup notes\n---\nWrite them")
	writeCmd(t, filepath.Join(dir, ".prism", "commands"), "help", "clash")
	writeCmd(t, filepath.Join(dir, ".prism", "commands"), "ship", "---\ndescription: ship it\n---\nShip")
	v := commandsView(loadUserCommands(dir, 0))
	if len(v.blocks) != 2 || v.blocks[0].title != "yours" || v.blocks[1].title != "this project" {
		t.Fatalf("blocks: %+v", v.blocks)
	}
	out := renderCmdView(v, 80)
	for _, want := range []string{"/standup", "my standup notes", "/ship", "/help", "built in"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
	checkNoOverflow(t, "/commands", v, 60)
	empty := renderCmdView(commandsView(nil), 80)
	if !strings.Contains(empty, ".prism/commands") || !strings.Contains(empty, "/commands new") {
		t.Errorf("empty view should say how to add one:\n%s", empty)
	}
}

func TestTUISavedCommandSends(t *testing.T) {
	dir, _ := savedEnv(t)
	writeCmd(t, filepath.Join(dir, ".prism", "commands"), "explain", "Explain $ARGUMENTS simply.")
	m := tabTestModel(t, 100, 30, 1)
	m.agent.dir, m.agent.api = dir, "http://127.0.0.1:1"
	m.command("/explain the parser")
	var user string
	for _, e := range m.entries {
		if e.kind == "user" {
			user = e.text
		}
	}
	if user != "Explain the parser simply." || !m.busy {
		t.Fatalf("user=%q busy=%v entries=%+v", user, m.busy, m.entries)
	}
	if m.cancel != nil {
		m.cancel()
	}
	m2 := &model{tab: &tab{agent: &Agent{dir: dir, messages: []Message{{Role: "system"}}}}}
	m2.command("/commands")
	if len(m2.entries) == 0 || m2.entries[len(m2.entries)-1].view == nil || !strings.Contains(renderCmdView(*m2.entries[len(m2.entries)-1].view, 80), "/explain") {
		t.Fatalf("/commands: %+v", m2.entries)
	}
	m2.command("/commands new todo")
	if last := m2.entries[len(m2.entries)-1]; !strings.Contains(last.text, "wrote .prism/commands/todo.md") {
		t.Fatalf("/commands new: %+v", last)
	}
}

func TestServeSavedCommands(t *testing.T) {
	h, s, port := testServer(t)
	host := "127.0.0.1:" + port
	origin := "http://" + host
	proj := filepath.Join(s.agent.dir, ".prism", "commands")
	writeCmd(t, proj, "review", "---\ndescription: review the change\n---\nReview $ARGUMENTS now.")
	writeCmd(t, proj, "usage", "clash")

	var rows []map[string]any
	if err := json.Unmarshal(do(t, h, http.MethodGet, "/api/commands", host, "", "").Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	seen := map[string]map[string]any{}
	for _, r := range rows {
		seen[r["name"].(string)] = r
	}
	if r := seen["/review"]; r == nil || r["web"] != true || r["help"] != "review the change" {
		t.Fatalf("/api/commands missing saved command: %v", rows)
	}
	if r := seen["/commands"]; r == nil || r["web"] != true {
		t.Fatalf("/api/commands missing /commands: %v", rows)
	}
	post := func(text string) map[string]any {
		body, _ := json.Marshal(map[string]string{"text": text})
		return decode(t, do(t, h, http.MethodPost, "/api/command", host, origin, string(body)))
	}
	if res := post("/review the login form"); res["kind"] != "send" || res["message"] != "Review the login form now." {
		t.Fatalf("saved command: %v", res)
	}
	if res := post("/usage"); res["kind"] != "info" {
		t.Fatalf("built in must win: %v", res)
	}
	if res := post("/commands"); res["kind"] != "view" || !strings.Contains(fmtJSON(res), "/review") {
		t.Fatalf("/commands: %v", res)
	}
	if res := post("/commands new standup"); res["kind"] != "note" || !strings.Contains(res["text"].(string), "wrote") {
		t.Fatalf("/commands new: %v", res)
	}
	s.mu.Lock()
	s.busy = true
	s.mu.Unlock()
	if res := post("/review x"); res["kind"] != "error" {
		t.Fatalf("busy: %v", res)
	}
}

func fmtJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestSavedCommandsIgnoreLinksOutside(t *testing.T) {
	dir, _ := savedEnv(t)
	secret := filepath.Join(t.TempDir(), "secret.md")
	os.WriteFile(secret, []byte("private key"), 0o600)
	cmds := filepath.Join(dir, ".prism", "commands")
	os.MkdirAll(cmds, 0o755)
	os.Symlink(secret, filepath.Join(cmds, "leak.md"))
	outside := t.TempDir()
	writeCmd(t, outside, "away", "far away")
	os.RemoveAll(cmds)
	os.Symlink(outside, cmds)
	if _, ok := findUserCommand(dir, "/away"); ok {
		t.Error("a linked commands folder outside the project must be ignored")
	}
	os.Remove(cmds)
	os.MkdirAll(cmds, 0o755)
	os.Symlink(secret, filepath.Join(cmds, "leak.md"))
	if _, ok := findUserCommand(dir, "/leak"); ok {
		t.Error("a command file linking outside its folder must be ignored")
	}
}
