package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// matching()
// ---------------------------------------------------------------------------

func TestMemoryCommands(t *testing.T) {
	const initial = "- first fact\n- second fact\n- third fact\n- fourth fact\n- fifth fact"
	facts := []string{"first fact", "second fact", "third fact", "fourth fact", "fifth fact"}
	tests := []struct {
		command, want, title string
		lines                []string
		wantErr              bool
	}{
		{"/memory", initial, "what prism remembers here", []string{"1  first fact", "5  fifth fact", "/memory forget 2 removes one", "/memory forget all"}, false},
		{"/memory forget", initial, "what prism remembers here", []string{"2  second fact", "/memory forget 2 removes one", "/memory forget all"}, false},
		{"/forget", initial, "what prism remembers here", []string{"1  first fact", "/memory forget 2 removes one", "/memory forget all"}, false},
		{"/memory forget 2", "- first fact\n- third fact\n- fourth fact\n- fifth fact", "forgot: second fact", nil, false},
		{"/forget 2", "- first fact\n- third fact\n- fourth fact\n- fifth fact", "forgot: second fact", nil, false},
		{"/memory forget 2 5", "- first fact\n- third fact\n- fourth fact", "forgot: second fact · fifth fact", nil, false},
		{"/forget 2 5", "- first fact\n- third fact\n- fourth fact", "forgot: second fact · fifth fact", nil, false},
		{"/memory forget all", "", "memory cleared for this folder", nil, false},
		{"/forget all", "", "memory cleared for this folder", nil, false},
		{"/memory forget 6", initial, "no memory number", nil, true},
		{"/forget 0", initial, "no memory number", nil, true},
		{"/memory forget 2 nope", initial, "no memory number", nil, true},
		{"/memory forget all 2", initial, "can't be mixed with numbers", nil, true},
		{"/memory all", initial, "use /memory forget <n>", nil, true},
	}
	for _, ui := range []string{"terminal", "web"} {
		t.Run(ui, func(t *testing.T) {
			for _, tc := range tests {
				t.Run(tc.command, func(t *testing.T) {
					t.Setenv("HOME", t.TempDir())
					t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "cfg"))
					dir := t.TempDir()
					a := &Agent{dir: dir, store: openStore(dir), messages: []Message{{Role: "system"}}}
					if err := a.store.setMemory(initial); err != nil {
						t.Fatal(err)
					}
					// Start from a real system prompt that contains every fact, so the
					// check below proves the command refreshed it.
					a.messages[0].Content = a.systemPrompt()
					for _, fact := range facts {
						if !strings.Contains(a.messages[0].Content, fact) {
							t.Fatalf("system prompt lacks %q before the command", fact)
						}
					}
					var kind, title string
					var lines []string
					if ui == "terminal" {
						m := &model{tab: &tab{agent: a}}
						m.command(tc.command)
						e := m.entries[len(m.entries)-1]
						kind, title, lines = e.kind, e.text, e.lines
						if kind == "help" {
							kind = "error"
						}
					} else {
						s := &server{agent: a}
						body, err := json.Marshal(map[string]string{"text": tc.command})
						if err != nil {
							t.Fatal(err)
						}
						w := httptest.NewRecorder()
						s.command(w, httptest.NewRequest("POST", "/command", strings.NewReader(string(body))))
						var response struct {
							Kind, Title, Text string
							Lines             []string
						}
						if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
							t.Fatal(err)
						}
						kind, title, lines = response.Kind, response.Title, response.Lines
						if kind == "error" {
							title = response.Text
						} else if kind != "info" || response.Lines == nil {
							t.Fatalf("unexpected web shape: %s", w.Body.String())
						}
					}
					if (kind == "error") != tc.wantErr || !strings.Contains(title, tc.title) {
						t.Errorf("response = %q %q, want error=%v, title containing %q", kind, title, tc.wantErr, tc.title)
					}
					for _, want := range tc.lines {
						if !strings.Contains(strings.Join(lines, "\n"), want) {
							t.Errorf("lines %q do not contain %q", lines, want)
						}
					}
					if got := a.store.memory(); got != tc.want {
						t.Errorf("memory = %q, want %q", got, tc.want)
					}
					for _, fact := range facts {
						if kept := strings.Contains(tc.want, fact); kept != strings.Contains(a.messages[0].Content, fact) {
							t.Errorf("system prompt contains %q = %v, want %v", fact, !kept, kept)
						}
					}
				})
			}
		})
	}
}

func TestMatchingPrefixes(t *testing.T) {
	tests := []struct {
		input string
		want  []string // subset that must appear; nil means empty result
	}{
		{"/m", []string{"/models", "/memory", "/mcp"}},
		{"/models", []string{"/models"}},
		{"/q", []string{"/quiet", "/quit"}},
		{"/quit", []string{"/quit"}},
		{"/help", []string{"/help"}},
		{"/xyz", nil},
		{"hello", nil},   // no slash prefix
		{"/m odel", nil}, // space inside
		{"", nil},
	}
	for _, tc := range tests {
		got := matching(tc.input)
		if tc.want == nil {
			if len(got) != 0 {
				t.Errorf("matching(%q) = %v, want empty", tc.input, got)
			}
			continue
		}
		names := make(map[string]bool, len(got))
		for _, c := range got {
			names[c.name] = true
		}
		for _, w := range tc.want {
			if !names[w] {
				t.Errorf("matching(%q): missing %q in %v", tc.input, w, got)
			}
		}
	}
}

func TestMatchingNoSpaceInInput(t *testing.T) {
	// "/resume 1" has a space — must return nothing
	if got := matching("/resume 1"); len(got) != 0 {
		t.Errorf("matching with space should return nil, got %v", got)
	}
}

func TestMatchingNewline(t *testing.T) {
	if got := matching("/help\n"); len(got) != 0 {
		t.Errorf("matching with newline should return nil, got %v", got)
	}
}

func TestMatchingNewlineVariants(t *testing.T) {
	for _, in := range []string{"/m\n", "/models\nmore", "\n/help", "/\n"} {
		if got := matching(in); len(got) != 0 {
			t.Errorf("matching(%q) = %v, want empty", in, got)
		}
	}
}

func TestMatchingBareSlashListsAll(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no saved commands of yours here
	if got := matching("/"); len(got) != len(commands) {
		t.Errorf("matching(\"/\") returned %d commands, want %d", len(got), len(commands))
	}
}

// ---------------------------------------------------------------------------
// newSkill() — name validation and file creation
// ---------------------------------------------------------------------------

func TestNewSkillInvalidNames(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "cfg"))
	a := &Agent{dir: t.TempDir(), messages: []Message{{Role: "system"}}}
	bad := []string{
		"",
		"My-Skill", // uppercase
		"-bad",     // leading dash
		"bad name", // space
		"toolooooooooooooooooooooooooooooooooooooooooong", // >41 chars
		"has/slash",
		"has.dot",
	}
	for _, name := range bad {
		msg := newSkill(a, name)
		if !strings.Contains(msg, "lowercase") {
			t.Errorf("newSkill(%q) = %q, want rejection message", name, msg)
		}
	}
}

func TestNewSkillValidNameCreatesFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "cfg"))
	a := &Agent{dir: t.TempDir(), messages: []Message{{Role: "system"}}}
	msg := newSkill(a, "my-skill")
	if !strings.Contains(msg, "wrote") {
		t.Fatalf("newSkill returned unexpected message: %q", msg)
	}
	path := filepath.Join(a.dir, ".prism", "skills", "my-skill.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("skill file not created: %v", err)
	}
	if !strings.Contains(string(data), "my-skill") {
		t.Error("skill file does not contain the skill name")
	}
	// front matter must be present
	if !strings.HasPrefix(string(data), "---\n") {
		t.Error("skill file missing front matter")
	}
}

func TestNewSkillDuplicateRejected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "cfg"))
	a := &Agent{dir: t.TempDir(), messages: []Message{{Role: "system"}}}
	newSkill(a, "dup-skill")
	msg := newSkill(a, "dup-skill")
	if !strings.Contains(msg, "already exists") {
		t.Errorf("duplicate skill should be rejected, got: %q", msg)
	}
}

func TestNewSkillBoundaryName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "cfg"))
	a := &Agent{dir: t.TempDir(), messages: []Message{{Role: "system"}}}
	// exactly 41 chars: 1 leading + 40 more = valid
	name := "a" + strings.Repeat("b", 40)
	msg := newSkill(a, name)
	if strings.Contains(msg, "lowercase") {
		t.Errorf("41-char name should be valid, got: %q", msg)
	}
	// 42 chars: invalid
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "cfg"))
	a2 := &Agent{dir: t.TempDir(), messages: []Message{{Role: "system"}}}
	long := "a" + strings.Repeat("b", 41)
	msg2 := newSkill(a2, long)
	if !strings.Contains(msg2, "lowercase") {
		t.Errorf("42-char name should be invalid, got: %q", msg2)
	}
}

// ---------------------------------------------------------------------------
// mcpLines()
// ---------------------------------------------------------------------------

func TestMcpLinesEmpty(t *testing.T) {
	lines := mcpLines(nil)
	if len(lines) == 0 {
		t.Fatal("mcpLines(nil) returned empty slice")
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "none yet") {
		t.Errorf("empty mcpLines should mention 'none yet', got: %v", lines)
	}
	if !strings.Contains(joined, "mcp.json") {
		t.Errorf("empty mcpLines should mention mcp.json, got: %v", lines)
	}
}

func TestMcpLinesReady(t *testing.T) {
	list := []mcpStatus{
		{name: "myserver", state: "ready", tools: []string{"read_file", "write_file"}},
	}
	lines := mcpLines(list)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "myserver") {
		t.Errorf("ready server name missing: %v", lines)
	}
	if !strings.Contains(joined, "2 tools") {
		t.Errorf("tool count missing: %v", lines)
	}
	if !strings.Contains(joined, "read_file") {
		t.Errorf("tool names missing: %v", lines)
	}
	// trailing hint line
	if !strings.Contains(joined, "AI uses these tools") {
		t.Errorf("trailing hint missing: %v", lines)
	}
}

func TestMcpLinesError(t *testing.T) {
	list := []mcpStatus{
		{name: "broken", state: "error", err: "connection refused"},
	}
	lines := mcpLines(list)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "broken") {
		t.Errorf("error server name missing: %v", lines)
	}
	if !strings.Contains(joined, "connection refused") {
		t.Errorf("error message missing: %v", lines)
	}
	// error line should start with ✕
	found := false
	for _, l := range lines {
		if strings.HasPrefix(l, "✕") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("error line should start with ✕: %v", lines)
	}
}

func TestMcpLinesOtherState(t *testing.T) {
	list := []mcpStatus{
		{name: "pending", state: "starting"},
	}
	lines := mcpLines(list)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "pending") {
		t.Errorf("pending server name missing: %v", lines)
	}
	if !strings.Contains(joined, "starting") {
		t.Errorf("state missing: %v", lines)
	}
}

// ---------------------------------------------------------------------------
// ago()
// ---------------------------------------------------------------------------

func TestAgo(t *testing.T) {
	now := time.Now()
	tests := []struct {
		t    time.Time
		want string
	}{
		{now.Add(-10 * time.Second), "just now"},
		{now.Add(-59 * time.Second), "just now"},
		{now.Add(-2 * time.Minute), "2m ago"},
		{now.Add(-90 * time.Minute), "1h ago"},
		{now.Add(-25 * time.Hour), "1d ago"},
		{now.Add(-48 * time.Hour), "2d ago"},
	}
	for _, tc := range tests {
		got := ago(tc.t)
		if got != tc.want {
			t.Errorf("ago(%v) = %q, want %q", tc.t, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// short()
// ---------------------------------------------------------------------------

func TestShort(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{999999, "1000.0k"},
		{1000000, "1.0M"},
		{2500000, "2.5M"},
	}
	for _, tc := range tests {
		got := short(tc.n)
		if got != tc.want {
			t.Errorf("short(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// /resume number limits — tested via the resumeShown constant and the
// slice-cap logic in command(), exercised directly without a full TUI model.
// ---------------------------------------------------------------------------

func TestResumeShownConstant(t *testing.T) {
	// resumeShown must be positive and reasonable
	if resumeShown <= 0 {
		t.Errorf("resumeShown = %d, want > 0", resumeShown)
	}
	if resumeShown > 99 {
		t.Errorf("resumeShown = %d, suspiciously large", resumeShown)
	}
}

func TestResumeCapLogic(t *testing.T) {
	// Simulate the slice-cap logic from command() for /resume.
	// Build a list of 20 sessions; only the first resumeShown should be valid.
	all := make([]session, 20)
	for i := range all {
		all[i] = session{ID: "id", Title: "chat"}
	}
	capped := all[:min(len(all), resumeShown)]
	if len(capped) != resumeShown {
		t.Errorf("capped len = %d, want %d", len(capped), resumeShown)
	}
	// k=resumeShown is the last valid index (1-based)
	k := resumeShown
	if k < 1 || k > len(capped) {
		t.Errorf("k=%d out of range [1,%d]", k, len(capped))
	}
	// k=resumeShown+1 must be out of range
	k2 := resumeShown + 1
	if k2 >= 1 && k2 <= len(capped) {
		t.Errorf("k=%d should be out of range but is within [1,%d]", k2, len(capped))
	}
}

func TestMatchingPutsExactCommandFirst(t *testing.T) {
	got := matchingIn("", "/model")
	if len(got) < 2 || got[0].name != "/model" || got[1].name != "/models" {
		t.Fatalf("order = %v", got)
	}
}

func TestFriendlyPlanLimit(t *testing.T) {
	err := &routerError{failures: []routeFailure{{Route: "grok/grok-4.7", Status: 402, Message: "grok/grok-4.7 failed (402): {\"error\":\"Grok Build usage balance exhausted\"}"}}}
	head, detail := friendly(err)
	if !strings.Contains(head, "grok plan's usage is used up") || !strings.Contains(detail, "Grok Build usage balance exhausted") {
		t.Fatalf("got %q / %q", head, detail)
	}
}
