package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// matching()
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// newSkill() — name validation and file creation
// ---------------------------------------------------------------------------

func makeTestAgent(t *testing.T) *Agent {
	t.Helper()
	dir := t.TempDir()
	// openStore needs a config dir; point it somewhere harmless
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "cfg"))
	a := &Agent{
		dir:   dir,
		store: openStore(dir),
		mcp:   &mcpHub{},
	}
	a.skills = loadSkills(dir)
	// newSkill() calls a.messages[0] — must be initialised or it panics (bug in commands.go:344)
	a.messages = []Message{{Role: "system", Content: ""}}
	return a
}

func TestNewSkillInvalidNames(t *testing.T) {
	a := makeTestAgent(t)
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
	a := makeTestAgent(t)
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
	a := makeTestAgent(t)
	newSkill(a, "dup-skill")
	msg := newSkill(a, "dup-skill")
	if !strings.Contains(msg, "already exists") {
		t.Errorf("duplicate skill should be rejected, got: %q", msg)
	}
}

func TestNewSkillBoundaryName(t *testing.T) {
	a := makeTestAgent(t)
	// exactly 41 chars: 1 leading + 40 more = valid
	name := "a" + strings.Repeat("b", 40)
	msg := newSkill(a, name)
	if strings.Contains(msg, "lowercase") {
		t.Errorf("41-char name should be valid, got: %q", msg)
	}
	// 42 chars: invalid
	a2 := makeTestAgent(t)
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
