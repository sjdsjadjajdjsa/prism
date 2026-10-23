package main

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// renderCmdView converts a cmdView to plain text at the given column width,
// stripping colour/bold — useful for snapshot tests and width checks.
func renderCmdView(v cmdView, width int) string {
	t := themes[0] // prism theme
	lines := cmdLines(t, v, width)
	var sb strings.Builder
	for _, l := range lines {
		var row strings.Builder
		for i := 0; i < l.indent; i++ {
			row.WriteByte(' ')
		}
		for _, s := range l.segs {
			row.WriteString(s.text)
		}
		sb.WriteString(strings.TrimRight(row.String(), " "))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// lineWidth returns the rune width of a string (no ANSI codes here).
func lineWidth(s string) int { return utf8.RuneCountInString(s) }

// checkNoOverflow asserts no rendered line exceeds width+indent.
func checkNoOverflow(t *testing.T, label string, v cmdView, width int) {
	t.Helper()
	rendered := renderCmdView(v, width)
	for i, line := range strings.Split(rendered, "\n") {
		if w := lineWidth(line); w > width+6 { // +6 for outer indent
			t.Errorf("%s @%d: line %d is %d chars (limit ~%d): %q", label, width, i+1, w, width+6, line)
		}
	}
}

// ── /skills ──────────────────────────────────────────────────────────────────

func TestSkillsViewGrouping(t *testing.T) {
	skills := []skill{
		{name: "fix-bug", description: "Fix a bug, crash, or wrong output.", source: "built in"},
		{name: "refactor", description: "Restructure code without changing behaviour.", source: "built in"},
		{name: "website", description: "Build a polished single-page website.", source: "built in"},
		{name: "my-skill", description: "My custom skill.", source: "yours"},
	}
	v := skillsView(skills)
	if v.title == "" {
		t.Error("title should not be empty")
	}
	// Should have at least 3 groups (building, fixing, improving, yours)
	if len(v.blocks) < 3 {
		t.Errorf("expected ≥3 groups, got %d", len(v.blocks))
	}
	// "your skills" group should contain my-skill
	found := false
	for _, blk := range v.blocks {
		if blk.title == "your skills" {
			for _, it := range blk.items {
				if it.name == "my-skill" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("my-skill should appear in 'your skills' group")
	}
}

func TestSkillsViewEmpty(t *testing.T) {
	v := skillsView(nil)
	if !strings.Contains(v.footer, "/skills new") {
		t.Errorf("empty footer should mention /skills new, got: %q", v.footer)
	}
}

func TestSkillsViewNoOverflow(t *testing.T) {
	skills := []skill{
		{name: "fix-bug", description: "Fix a bug, crash, or wrong output. Reproduce first, then patch.", source: "built in"},
		{name: "accessibility-review", description: "Check a page or app for accessibility issues and report exact fixes.", source: "built in"},
		{name: "my-very-long-skill-name", description: "A custom skill with a longer description that might wrap at narrow widths.", source: "yours"},
	}
	v := skillsView(skills)
	for _, width := range []int{60, 100, 140} {
		checkNoOverflow(t, "/skills", v, width)
	}
}

// ── /resume ───────────────────────────────────────────────────────────────────

func TestResumeView(t *testing.T) {
	sessions := []session{
		{ID: "a", Title: "Fix the login bug", Updated: time.Now().Add(-2 * time.Hour)},
		{ID: "b", Title: "Add dark mode to the settings page", Updated: time.Now().Add(-25 * time.Hour)},
	}
	v := resumeView(sessions)
	if v.title != "earlier chats" {
		t.Errorf("title: got %q", v.title)
	}
	if len(v.blocks) == 0 || len(v.blocks[0].items) != 2 {
		t.Errorf("expected 2 items, got blocks=%v", v.blocks)
	}
	// Items numbered 1, 2
	if v.blocks[0].items[0].name != "1" {
		t.Errorf("first item name should be '1', got %q", v.blocks[0].items[0].name)
	}
	for _, width := range []int{60, 100, 140} {
		checkNoOverflow(t, "/resume", v, width)
	}
}

// ── /usage ────────────────────────────────────────────────────────────────────

func TestUsageView(t *testing.T) {
	u := usageDay{Day: "2025-01-01", Requests: 42, Tokens: 150000}
	v := usageView(u, 7, 12000, 3000)
	rendered := renderCmdView(v, 80)
	if !strings.Contains(rendered, "42") {
		t.Errorf("should contain request count 42: %s", rendered)
	}
	if !strings.Contains(rendered, "150.0k") {
		t.Errorf("should contain token count 150.0k: %s", rendered)
	}
	for _, width := range []int{60, 100, 140} {
		checkNoOverflow(t, "/usage", v, width)
	}
}

// ── /memory ───────────────────────────────────────────────────────────────────

func TestMemoryView(t *testing.T) {
	lines := []string{
		"1  always use tabs for indentation",
		"2  run go test ./... before committing",
		"3  the main entry point is cli/main.go",
	}
	v := memoryView("what prism remembers here", lines, "")
	rendered := renderCmdView(v, 80)
	if !strings.Contains(rendered, "always use tabs") {
		t.Errorf("should contain memory fact: %s", rendered)
	}
	for _, width := range []int{60, 100, 140} {
		checkNoOverflow(t, "/memory", v, width)
	}
}

func TestMemoryViewWithProjectFile(t *testing.T) {
	v := memoryView("what prism remembers here", []string{"1  some fact"}, "PRISM.md")
	if !strings.Contains(v.footer, "PRISM.md") {
		t.Errorf("footer should mention project file, got: %q", v.footer)
	}
}

// ── /mcp ──────────────────────────────────────────────────────────────────────

func TestMcpViewEmpty(t *testing.T) {
	v := mcpView(nil)
	rendered := renderCmdView(v, 80)
	if !strings.Contains(rendered, "none yet") {
		t.Errorf("empty mcp should say 'none yet': %s", rendered)
	}
}

func TestMcpViewReady(t *testing.T) {
	list := []mcpStatus{
		{name: "filesystem", state: "ready", tools: []string{"read_file", "write_file", "list_dir"}},
		{name: "github", state: "error", err: "connection refused"},
	}
	v := mcpView(list)
	rendered := renderCmdView(v, 80)
	if !strings.Contains(rendered, "filesystem") {
		t.Errorf("should contain 'filesystem': %s", rendered)
	}
	if !strings.Contains(rendered, "3 tools") {
		t.Errorf("should contain '3 tools': %s", rendered)
	}
	for _, width := range []int{60, 100, 140} {
		checkNoOverflow(t, "/mcp", v, width)
	}
}

// ── /help ─────────────────────────────────────────────────────────────────────

func TestHelpViewGroups(t *testing.T) {
	v := helpView()
	labels := map[string]bool{}
	for _, blk := range v.blocks {
		labels[blk.title] = true
	}
	for _, want := range []string{"chat", "models and accounts", "project", "settings"} {
		if !labels[want] {
			t.Errorf("helpView missing group %q; got %v", want, labels)
		}
	}
	// Footer should mention new key bindings
	if !strings.Contains(v.footer, "ctrl+t new tab") {
		t.Errorf("footer should mention ctrl+t new tab, got: %q", v.footer)
	}
	if !strings.Contains(v.footer, "ctrl+y theme") {
		t.Errorf("footer should mention ctrl+y theme, got: %q", v.footer)
	}
	for _, width := range []int{60, 100, 140} {
		checkNoOverflow(t, "/help", v, width)
	}
}

func TestHelpViewListsEveryCommand(t *testing.T) {
	v := helpView()
	seen := map[string]int{}
	for _, blk := range v.blocks {
		if blk.title == "more" {
			t.Errorf("command missing from the help groups: %+v", blk.items)
		}
		for _, it := range blk.items {
			seen[it.name]++
		}
	}
	for _, c := range commands {
		if seen[c.name] != 1 {
			t.Errorf("help lists %s %d times, want once", c.name, seen[c.name])
		}
	}
	for _, width := range []int{60, 100} {
		rendered := renderCmdView(v, width)
		for _, c := range commands {
			if !strings.Contains(rendered, c.name) {
				t.Errorf("@%d: rendered help is missing %s", width, c.name)
			}
		}
	}
}

func TestHelpViewNoOverflow60(t *testing.T) {
	checkNoOverflow(t, "/help", helpView(), 60)
}

// ── wrapWords ─────────────────────────────────────────────────────────────────

func TestWrapWords(t *testing.T) {
	cases := []struct {
		s     string
		width int
		want  int // expected line count
	}{
		{"hello world", 20, 1},
		{"hello world", 5, 2},
		{"", 20, 0},
		{"one two three four five six seven eight nine ten", 20, 3},
	}
	for _, c := range cases {
		got := wrapWords(c.s, c.width)
		if len(got) != c.want {
			t.Errorf("wrapWords(%q, %d): got %d lines, want %d: %v", c.s, c.width, len(got), c.want, got)
		}
		for _, line := range got {
			if w := lineWidth(line); w > c.width {
				t.Errorf("wrapWords(%q, %d): line %q exceeds width %d", c.s, c.width, line, c.width)
			}
		}
	}
}

func TestPadRune(t *testing.T) {
	if got := padRune("hi", 5); got != "hi   " {
		t.Errorf("padRune: got %q", got)
	}
	if got := padRune("hello", 5); got != "hello" {
		t.Errorf("padRune exact: got %q", got)
	}
	if got := padRune("toolong", 3); got != "toolong" {
		t.Errorf("padRune over: got %q", got)
	}
}

// ── snapshot: before/after at 60, 100, 140 ───────────────────────────────────

// TestSkillsSnapshot prints before/after text for manual review.
// Run with -v to see the output.
