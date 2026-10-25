package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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

// ---------------------------------------------------------------------------
// the suggestion list: ranking, keys, mouse, layout (web/commands.js mirrors it)
// ---------------------------------------------------------------------------

func suggestNames(rows []suggestion) []string {
	var out []string
	for _, r := range rows {
		name := r.name
		if r.close {
			name = "~" + name
		}
		out = append(out, name)
	}
	return out
}

func TestSuggestRanking(t *testing.T) {
	dir, mine := savedEnv(t)
	writeCmd(t, mine, "mreview", "---\ndescription: review my change\n---\nReview it")
	tests := []struct {
		input string
		want  []string // the first rows, in order; "~" marks a close match
	}{
		{"/model", []string{"/model", "/models"}},
		{"/mo", []string{"/model", "/models", "~/memory"}},
		{"/m", []string{"/mcp", "/map", "/model", "/models", "/memory", "/mreview"}},
		{"/mdl", []string{"~/model", "~/models"}},
		{"/odel", []string{"~/model", "~/models"}},
		{"/MODEL", []string{"/model", "/models"}},
		{"/", []string{"/models", "/model", "/key", "/login"}},
		{"/xyz", nil},
		{"/x", nil}, // one letter only matches as a prefix
	}
	for _, tc := range tests {
		got := suggestNames(suggestIn(dir, tc.input))
		if len(got) < len(tc.want) || strings.Join(got[:len(tc.want)], " ") != strings.Join(tc.want, " ") || (tc.want == nil && len(got) > 0) {
			t.Errorf("suggestIn(%q) = %v, want it to start %v", tc.input, got, tc.want)
		}
	}
	for _, r := range suggestIn(dir, "/mrev") {
		if r.name == "/mreview" && !r.saved {
			t.Errorf("saved command not tagged: %+v", r)
		}
	}
	if desc, usage := splitHelp(commands[1].help); desc != "pick the model for this chat" || usage != "/model <name> · /model auto" {
		t.Errorf("splitHelp = %q, %q", desc, usage)
	}
}

func suggestModel(t *testing.T, w, h int) model {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return model{tab: &tab{agent: &Agent{}, events: make(chan any, 8)}, w: w, h: h, chat: true, ticking: true, chatAt: -time.Hour, now: time.Second, lastTextAt: -time.Hour, sayAt: -time.Hour}
}

func hit(t *testing.T, m model, code rune) model {
	t.Helper()
	next, _ := m.Update(tea.KeyPressMsg{Code: code})
	return next.(model)
}

// highlighted is the command on the screen row marked "›" in the list.
func highlighted(t *testing.T, m model) string {
	t.Helper()
	for _, row := range strings.Split(crewScreen(m), "\n") {
		if f := strings.Fields(row); len(f) > 1 && f[0] == "›" && strings.HasPrefix(f[1], "/") {
			return f[1]
		}
	}
	return ""
}

func TestSuggestKeys(t *testing.T) {
	m := typeText(t, suggestModel(t, 80, 24), "/mo")
	if got := highlighted(t, m); got != "/model" {
		t.Fatalf("typing highlights %q, want the best match /model\n%s", got, crewScreen(m))
	}
	if v := m.View(); v.MouseMode != tea.MouseModeAllMotion {
		t.Errorf("an open list needs pointer motion for hover")
	}
	if screen := crewScreen(m); !strings.Contains(screen, "/model <name> · /model auto") || !strings.Contains(screen, "close matches") {
		t.Errorf("no usage hint or divider:\n%s", screen)
	}
	for _, step := range []struct {
		key  rune
		want string
	}{{tea.KeyDown, "/models"}, {tea.KeyDown, "/memory"}, {tea.KeyDown, "/model"}, {tea.KeyUp, "/memory"}} {
		m = hit(t, m, step.key)
		if got := highlighted(t, m); got != step.want {
			t.Fatalf("after a key the highlight is %q, want %q (wraps)\n%s", got, step.want, crewScreen(m))
		}
	}
	m = typeText(t, m, "d")
	if got := highlighted(t, m); got != "/model" {
		t.Errorf("typing must put the highlight back on the best match, got %q", got)
	}
	m = hit(t, m, tea.KeyEscape)
	if string(m.input) != "/mod" || strings.Contains(crewScreen(m), "close matches") || highlighted(t, m) != "" {
		t.Errorf("esc must close the list and keep the input %q", string(m.input))
	}
	m = hit(t, m, tea.KeyTab)
	if string(m.input) != "/mod" {
		t.Errorf("tab with the list closed changed the input to %q", string(m.input))
	}
	m = typeText(t, m, "e")
	if highlighted(t, m) != "/model" {
		t.Errorf("typing after esc must reopen the list")
	}

	// tab writes a command that takes words; one with a menu opens it at once
	m = typeText(t, suggestModel(t, 80, 24), "/comp")
	if m = hit(t, m, tea.KeyTab); string(m.input) != "/compact " {
		t.Errorf("tab on /comp = %q", string(m.input))
	}
	m = typeText(t, suggestModel(t, 80, 24), "/them")
	if m = hit(t, m, tea.KeyTab); !m.menu.open || m.menu.kind != "theme" || len(m.input) != 0 {
		t.Errorf("tab on /theme must open its menu: open=%v kind=%q input=%q", m.menu.open, m.menu.kind, string(m.input))
	}
	// enter takes the highlighted command, partial or not
	m = typeText(t, suggestModel(t, 80, 24), "/mdl")
	if m = hit(t, m, tea.KeyEnter); !m.menu.open || m.menu.kind != "model" {
		t.Errorf("enter on /mdl must open the /model menu: %+v", m.entries)
	}
	m = typeText(t, suggestModel(t, 80, 24), "/model")
	m = hit(t, m, tea.KeyDown)
	if next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil || next.(model).menu.open || len(next.(model).input) != 0 {
		t.Errorf("enter must run the highlighted /models (a fetch), not /model's menu")
	}
	// an alias with no row of its own runs as typed
	m = typeText(t, suggestModel(t, 80, 24), "/exit")
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil {
		t.Errorf("enter on /exit did nothing")
	}
}

func TestSuggestEditResets(t *testing.T) {
	t.Run("highlight", func(t *testing.T) {
		m := typeText(t, suggestModel(t, 80, 24), "/mo")
		m = hit(t, hit(t, m, tea.KeyDown), tea.KeyDown)
		if got := highlighted(t, m); got != "/memory" {
			t.Fatalf("before editing highlight = %q, want /memory", got)
		}
		m = hit(t, typeText(t, m, "d"), tea.KeyBackspace)
		if got := highlighted(t, m); got != "/model" {
			t.Fatalf("after edit and backspace highlight = %q, want /model", got)
		}
		m = hit(t, m, tea.KeyEnter)
		if !m.menu.open || m.menu.kind != "model" {
			t.Errorf("enter after editing must open /model, got menu %q", m.menu.kind)
		}
	})
	t.Run("closed", func(t *testing.T) {
		m := typeText(t, suggestModel(t, 80, 24), "/mo")
		m = hit(t, m, tea.KeyEscape)
		m = hit(t, typeText(t, m, "d"), tea.KeyBackspace)
		if got := highlighted(t, m); got != "/model" {
			t.Errorf("editing back to closed text must reopen /model, got %q", got)
		}
	})
	t.Run("submit", func(t *testing.T) {
		m := typeText(t, suggestModel(t, 80, 24), "/mo")
		m = hit(t, hit(t, m, tea.KeyEscape), tea.KeyEnter)
		if m.sug != (suggestState{}) {
			t.Errorf("submit retained suggestion state: %+v", m.sug)
		}
		m = typeText(t, m, "/mo")
		if got := highlighted(t, m); got != "/model" {
			t.Errorf("typing a submitted command must reopen /model, got %q", got)
		}
	})
}

func TestSuggestNarrowHint(t *testing.T) {
	for _, width := range []int{50, 60} {
		for _, text := range []string{"/model", "/skills"} {
			m := typeText(t, suggestModel(t, width, 24), text)
			l := m.suggesting()
			hint := suggestHint(l.rows[l.sel])
			if screen := crewScreen(m); !strings.Contains(screen, hint) {
				t.Errorf("width %d, %s: usage %q is cut off:\n%s", width, text, hint, screen)
			}
		}
		m := hit(t, typeText(t, suggestModel(t, width, 16), "/"), tea.KeyDown)
		if screen := crewScreen(m); !strings.Contains(screen, suggestHint(m.suggesting().rows[1])) || !strings.Contains(screen, "more") {
			t.Errorf("width %d: a narrow long list must keep usage and hidden count:\n%s", width, screen)
		}
	}
}

func TestSuggestBusyEnter(t *testing.T) {
	m := typeText(t, suggestModel(t, 80, 24), "/mdl")
	m.busy = true
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if string(m.input) != "/mdl" || m.menu.open || cmd != nil {
		t.Errorf("busy enter changed input or ran a command: input=%q menu=%v cmd=%v", string(m.input), m.menu.open, cmd != nil)
	}
}

func TestSuggestBusyClickAndTab(t *testing.T) {
	m := typeText(t, suggestModel(t, 80, 24), "/mdl")
	m.busy = true
	x, y, _, _ := m.overlayArea()
	first := y - m.suggesting().lines
	next, cmd := m.Update(tea.MouseClickMsg{X: x + 6, Y: first + 1, Button: tea.MouseLeft})
	if m = next.(model); string(m.input) != "/mdl" || m.menu.open || cmd != nil {
		t.Errorf("busy click changed input or ran a command: input=%q menu=%v cmd=%v", string(m.input), m.menu.open, cmd != nil)
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m = next.(model); string(m.input) != "/mdl" || m.menu.open || cmd != nil {
		t.Errorf("busy tab on a picker command changed input or ran it: input=%q menu=%v cmd=%v", string(m.input), m.menu.open, cmd != nil)
	}
}

func TestSuggestListFitsAndScrolls(t *testing.T) {
	m := typeText(t, suggestModel(t, 80, 16), "/")
	rows := strings.Split(crewScreen(m), "\n")
	if !strings.Contains(rows[1], "◆ prism") || !strings.Contains(rows[2], "────") {
		t.Fatalf("the list covers the header:\n%s", strings.Join(rows, "\n"))
	}
	_, y, _, _ := m.overlayArea()
	if !strings.Contains(rows[y], "╭") || !strings.Contains(rows[y+1], "› /") {
		t.Fatalf("the list covers the input box:\n%s", strings.Join(rows, "\n"))
	}
	if !strings.Contains(crewScreen(m), "more · ↑↓") {
		t.Errorf("a long list must say how many rows are hidden")
	}
	for range 12 {
		m = hit(t, m, tea.KeyDown)
	}
	if got, want := highlighted(t, m), commands[12].name; got != want {
		t.Errorf("after 12 downs the highlight shows %q, want %q scrolled into view\n%s", got, want, crewScreen(m))
	}
	for range 12 {
		m = hit(t, m, tea.KeyUp)
	}
	if got := highlighted(t, m); got != commands[0].name {
		t.Errorf("back at the top the highlight is %q", got)
	}
	m = hit(t, m, tea.KeyUp) // wraps to the last row
	if got := highlighted(t, m); got != commands[len(commands)-1].name {
		t.Errorf("up from the top wraps to %q", got)
	}
}

func TestSuggestMouse(t *testing.T) {
	m := typeText(t, suggestModel(t, 80, 24), "/mo")
	x, y, _, _ := m.overlayArea()
	l := m.suggesting()
	first := y - l.lines
	next, _ := m.Update(tea.MouseMotionMsg{X: x + 6, Y: first + 1})
	m = next.(model)
	if got := highlighted(t, m); got != "/models" {
		t.Fatalf("hover highlights %q, want /models", got)
	}
	next, cmd := m.Update(tea.MouseClickMsg{X: x + 6, Y: first + 1, Button: tea.MouseLeft})
	if m = next.(model); len(m.input) != 0 || cmd == nil || m.menu.open {
		t.Errorf("a click must run /models: input %q", string(m.input))
	}
	// a click on the chat above the list does not touch the input
	m = typeText(t, suggestModel(t, 80, 24), "/mo")
	next, _ = m.Update(tea.MouseClickMsg{X: x + 6, Y: 5, Button: tea.MouseLeft})
	if m = next.(model); string(m.input) != "/mo" {
		t.Errorf("click outside changed the input to %q", string(m.input))
	}
}

func TestOptionSuggestions(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"/editor ", []string{"/editor simple", "/editor nvim"}},
		{"/editor n", []string{"/editor nvim"}},
		{"/editor im", []string{"/editor simple", "/editor nvim"}},
		{"/beam o", []string{"/beam on", "/beam off"}},
		{"/editor README.md", nil},
		{"/editor nvim x", nil},
		{"/help ", nil},
	}
	for _, c := range cases {
		got := suggestNames(suggestions(c.input))
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%q: %v, want %v", c.input, got, c.want)
		}
	}
	if rows := suggestions("/theme "); len(rows) != len(themes) || !rows[0].option {
		t.Fatalf("/theme lists every theme: %v", suggestNames(rows))
	}
}

func TestOptionEnterAndTab(t *testing.T) {
	m := suggestModel(t, 120, 30)
	m.input = []rune("/beam ")
	if _, ok := m.suggestKey("enter"); ok {
		t.Fatal("enter on \"/beam \" with nothing picked runs it as typed")
	}
	m.input = []rune("/editor n")
	m.suggestKey("tab")
	if string(m.input) != "/editor nvim " {
		t.Fatalf("tab fills the option: %q", string(m.input))
	}
}
