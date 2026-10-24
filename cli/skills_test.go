package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// skillsEnv isolates the user config dir so loadSkills never reads the real
// ~/.config/prism/skills.
func skillsEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	return home
}

func writeSkillFile(t *testing.T, dir, file, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSkillParseValid(t *testing.T) {
	data := "---\nname: demo\ndescription: Does a demo thing.\n---\n\n# Steps\n1. Do it\n"
	s, ok := parseSkill(data, "test")
	if !ok {
		t.Fatal("expected valid skill")
	}
	if s.name != "demo" {
		t.Errorf("name = %q", s.name)
	}
	if s.description != "Does a demo thing." {
		t.Errorf("description = %q", s.description)
	}
	if s.body != "# Steps\n1. Do it" {
		t.Errorf("body = %q", s.body)
	}
	if s.source != "test" {
		t.Errorf("source = %q", s.source)
	}
}

func TestSkillParseMissingFrontMatter(t *testing.T) {
	cases := map[string]string{
		"no front matter": "# Just a heading\nbody\n",
		"unclosed":        "---\nname: demo\ndescription: x\nbody without closing\n",
		"no name":         "---\ndescription: x\n---\nbody\n",
		"empty body":      "---\nname: demo\ndescription: x\n---\n",
	}
	for label, data := range cases {
		if _, ok := parseSkill(data, "test"); ok {
			t.Errorf("%s: expected parse to fail", label)
		}
	}
}

func TestSkillParseBOM(t *testing.T) {
	data := "\ufeff---\nname: bom-skill\ndescription: With a byte order mark.\n---\nDo the thing.\n"
	s, ok := parseSkill(data, "test")
	if !ok {
		t.Fatal("expected BOM-prefixed skill to parse")
	}
	if s.name != "bom-skill" {
		t.Errorf("name = %q", s.name)
	}
	if s.body != "Do the thing." {
		t.Errorf("body = %q", s.body)
	}
}

func TestSkillBuiltinsLoad(t *testing.T) {
	skillsEnv(t)
	entries, err := builtinSkills.ReadDir("skills")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".md") || name == "README.md" {
			continue
		}
		data, err := builtinSkills.ReadFile("skills/" + name)
		if err != nil {
			t.Fatal(err)
		}
		s, ok := parseSkill(string(data), "built in")
		if !ok || s.name != strings.TrimSuffix(name, ".md") {
			t.Fatalf("invalid embedded skill %q: parsed name %q", name, s.name)
		}
		want = append(want, s.name)
	}
	for _, name := range []string{"prove-it", "handoff-note", "retry-ladder", "game-rewrite"} {
		if _, ok := findSkill(loadSkills(t.TempDir()), name); !ok {
			t.Errorf("new built-in %q did not load without custom skills", name)
		}
	}

	skills := loadSkills(t.TempDir())
	if len(skills) != len(want) {
		t.Fatalf("loadSkills returned %d skills, want %d valid embedded skills", len(skills), len(want))
	}
	for _, name := range want {
		s, ok := findSkill(skills, name)
		if !ok {
			t.Errorf("built-in %q did not load", name)
			continue
		}
		if s.source != "built in" {
			t.Errorf("%s: source = %q", name, s.source)
		}
		if s.description == "" {
			t.Errorf("%s: empty description", name)
		}
		if len(s.description) >= 120 {
			t.Errorf("%s: description is %d chars, want < 120", name, len(s.description))
		}
		if s.body == "" {
			t.Errorf("%s: empty body", name)
		}
	}
}

func TestSkillLoadMergesAndProjectOverrides(t *testing.T) {
	home := skillsEnv(t)
	project := t.TempDir()
	builtinCount := len(loadSkills(t.TempDir()))

	// User-level skills: a new one, and an override of a built-in.
	userDir := filepath.Join(home, "prism", "skills")
	writeSkillFile(t, userDir, "my-user.md",
		"---\nname: my-user\ndescription: User skill.\n---\nUser body.\n")
	writeSkillFile(t, userDir, "fix-bug.md",
		"---\nname: fix-bug\ndescription: User version of fix-bug.\n---\nUser fix body.\n")

	// Project-level skills: a new one, and an override of the user's fix-bug.
	projDir := filepath.Join(project, ".prism", "skills")
	writeSkillFile(t, projDir, "my-proj.md",
		"---\nname: my-proj\ndescription: Project skill.\n---\nProject body.\n")
	writeSkillFile(t, projDir, "fix-bug.md",
		"---\nname: fix-bug\ndescription: Project version of fix-bug.\n---\nProject fix body.\n")
	writeSkillFile(t, projDir, "prove-it.md",
		"---\nname: prove-it\ndescription: Project proof.\n---\nProject proof body.\n")

	// Invalid file must be ignored.
	writeSkillFile(t, projDir, "broken.md", "no front matter here\n")

	skills := loadSkills(project)

	if s, ok := findSkill(skills, "my-user"); !ok || s.source != "yours" {
		t.Errorf("my-user: ok=%v source=%q", ok, s.source)
	}
	if s, ok := findSkill(skills, "my-proj"); !ok || s.source != "this project" {
		t.Errorf("my-proj: ok=%v source=%q", ok, s.source)
	}
	fix, ok := findSkill(skills, "fix-bug")
	if !ok {
		t.Fatal("fix-bug missing")
	}
	if fix.source != "this project" {
		t.Errorf("fix-bug source = %q, want project override", fix.source)
	}
	if fix.description != "Project version of fix-bug." {
		t.Errorf("fix-bug description = %q", fix.description)
	}
	if fix.body != "Project fix body." {
		t.Errorf("fix-bug body = %q", fix.body)
	}
	if proof, ok := findSkill(skills, "prove-it"); !ok || proof.source != "this project" || proof.body != "Project proof body." {
		t.Errorf("prove-it project override: ok=%v source=%q body=%q", ok, proof.source, proof.body)
	}
	if _, ok := findSkill(skills, "broken"); ok {
		t.Error("invalid skill file was loaded")
	}
	if len(skills) != builtinCount+2 {
		t.Errorf("total skills = %d, want %d (built-ins + 2 custom)", len(skills), builtinCount+2)
	}
}

func TestSkillUserOverridesBuiltin(t *testing.T) {
	home := skillsEnv(t)
	writeSkillFile(t, filepath.Join(home, "prism", "skills"), "refactor.md",
		"---\nname: refactor\ndescription: Mine.\n---\nMine body.\n")
	s, ok := findSkill(loadSkills(t.TempDir()), "refactor")
	if !ok {
		t.Fatal("refactor missing")
	}
	if s.source != "yours" || s.body != "Mine body." {
		t.Errorf("refactor source=%q body=%q, want user override", s.source, s.body)
	}
}

func TestSkillIndexListsSkills(t *testing.T) {
	skillsEnv(t)
	if got := skillIndex(nil); got != "" {
		t.Errorf("skillIndex(nil) = %q, want empty", got)
	}
	skills := loadSkills(t.TempDir())
	idx := skillIndex(skills)
	if !strings.Contains(idx, "Skills:") {
		t.Error("index missing header")
	}
	if strings.Contains(idx, "MUST call the skill tool first") || strings.Contains(idx, "Pick by the exact words") {
		t.Error("index still forces exact-word or mandatory upfront loading")
	}
	if !strings.Contains(idx, "matched by intent") || !strings.Contains(idx, "simple requests need none") {
		t.Error("index missing intent-based, optional loading policy")
	}
	// The workflow rules moved into the base prompt; they must still be sent.
	base := basePrompt("/p")
	for _, rule := range []string{"if a fix fails, read the error and change approach", "If you could not run a check, say so", "If you must stop early, say what is left", "never secrets"} {
		if !strings.Contains(base, rule) {
			t.Errorf("base prompt lost workflow rule %q", rule)
		}
	}
	if strings.Contains(idx, "Workflow:") {
		t.Error("skill index repeats workflow guidance the base prompt already gives")
	}
	for _, s := range skills {
		line := "- " + s.name + ": " + s.description
		if !strings.Contains(idx, line) {
			t.Errorf("index missing line %q", line)
		}
	}
}

// Size budgets for what every request carries with the built-in skills. Before the
// trim: index 2491 B, system prompt 4744 B. Raise only on purpose.
const (
	skillHeaderBudget = 160
	skillIndexBudget  = 2150
	systemBudget      = 4600
)

func TestSkillPromptBudget(t *testing.T) {
	skillsEnv(t)
	dir := t.TempDir()
	a := &Agent{dir: dir, skills: loadSkills(dir)}
	idx, sys := skillIndex(a.skills), a.systemPrompt()
	lines := 0
	for _, s := range a.skills {
		lines += len("- " + s.name + ": " + s.description + "\n")
	}
	spec, _ := json.Marshal(skillSpec)
	t.Logf("skills=%d index=%d B (header %d B) (~%d tok), skill tool %d B, system=%d B (~%d tok)",
		len(a.skills), len(idx), len(idx)-lines, len(idx)/4, len(spec), len(sys), len(sys)/4)
	if h := len(idx) - lines; h > skillHeaderBudget {
		t.Errorf("skill index header is %d B; keep it under %d", h, skillHeaderBudget)
	}
	if len(idx) > skillIndexBudget {
		t.Errorf("skill index is %d B; keep it under %d (shorten skill descriptions)", len(idx), skillIndexBudget)
	}
	if len(sys) > systemBudget {
		t.Errorf("system prompt with built-in skills is %d B; keep it under %d", len(sys), systemBudget)
	}
	for _, s := range a.skills {
		if strings.Contains(sys, s.body) {
			t.Errorf("system prompt contains the body of skill %q; bodies load only through the skill tool", s.name)
		}
	}
}

// TestPromptStableAcrossTurns: system prompt and tools must be byte-identical on every
// request of a chat, each turn must resend the previous turn's request as an exact
// prefix (even with a tabs brief), and no skill body is sent unless the model asks.
func TestPromptStableAcrossTurns(t *testing.T) {
	skillsEnv(t)
	var mu sync.Mutex
	type req struct {
		Messages []Message `json:"messages"`
		Tools    []any     `json:"tools"`
	}
	var seen []req
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body req
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		seen = append(seen, body)
		mu.Unlock()
		w.Header().Set("content-type", "text/event-stream")
		w.Write([]byte(delta(`{"content":"hello"}`) + "\n" + sseDone + "\n"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	a := newAgent(srv.URL, dir, dir)
	a.skills = loadSkills(dir)
	a.messages[0].Content = a.systemPrompt()
	h := newTabHub()
	a.hub, a.tabID = h, h.add("self")
	other := h.add("other")
	h.setStatus(other, "working", "first task")
	if _, err := collect(a, "hi"); err != nil {
		t.Fatal(err)
	}
	h.setStatus(other, "done", "a different task")
	if _, err := collect(a, "thanks"); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatalf("requests = %d, want 2", len(seen))
	}
	sys0, sys1 := seen[0].Messages[0].Content, seen[1].Messages[0].Content
	if sys0 != sys1 {
		t.Errorf("system prompt changed between turns:\n%q\n%q", sys0, sys1)
	}
	t0, _ := json.Marshal(seen[0].Tools)
	t1, _ := json.Marshal(seen[1].Tools)
	if string(t0) != string(t1) {
		t.Error("tools changed between turns")
	}
	if !strings.Contains(string(t0), `"skill"`) {
		t.Error("skill tool not offered")
	}
	for i, m := range seen[0].Messages {
		if i >= len(seen[1].Messages) || seen[1].Messages[i].Content != m.Content || seen[1].Messages[i].Role != m.Role {
			t.Errorf("turn 2 does not resend turn 1's message %d byte-for-byte (cache prefix broken)", i)
		}
	}
	if !strings.Contains(seen[1].Messages[len(seen[1].Messages)-1].Content, "a different task") {
		t.Error("turn 2 lacks the fresh tabs brief")
	}
	for _, r := range seen {
		var all strings.Builder
		for _, m := range r.Messages {
			all.WriteString(m.Content)
		}
		for _, s := range a.skills {
			if strings.Contains(all.String(), s.body[:min(len(s.body), 80)]) {
				t.Errorf("skill %q body was sent without a skill tool call", s.name)
			}
		}
	}
}

func TestSkillUseKnownAndUnknown(t *testing.T) {
	skillsEnv(t)
	a := &Agent{skills: loadSkills(t.TempDir())}

	ok := a.useSkill(`{"name":"fix-bug"}`)
	if !ok.ok {
		t.Fatalf("fix-bug: not ok: %q", ok.output)
	}
	if want, _ := findSkill(a.skills, "fix-bug"); ok.output != want.body {
		t.Error("useSkill did not return the skill body")
	}

	res := a.useSkill(`{"name":"no-such-skill"}`)
	if res.ok {
		t.Fatal("unknown skill should not be ok")
	}
	if !strings.Contains(res.output, "no skill called no-such-skill") {
		t.Errorf("error = %q", res.output)
	}
	if !strings.Contains(res.output, "Available: ") {
		t.Errorf("error lacks Available list: %q", res.output)
	}
	for _, s := range a.skills {
		if !strings.Contains(res.output, s.name) {
			t.Errorf("error does not list %q", s.name)
		}
	}
}

func TestSkillParseFrontMatterEdgeCases(t *testing.T) {
	cases := []struct {
		label, data      string
		ok               bool
		name, desc, body string
	}{
		{"colon in value", "---\nname: demo\ndescription: Use when: tests fail\n---\nbody\n", true, "demo", "Use when: tests fail", "body"},
		{"unknown keys ignored", "---\nname: demo\nauthor: me\ndescription: d\ntags: a, b\n---\nbody\n", true, "demo", "d", "body"},
		{"extra whitespace", "---\n  name :   spaced  \ndescription:\t tabbed \t\n---\nbody\n", true, "spaced", "tabbed", "body"},
		{"missing description", "---\nname: demo\n---\nbody\n", true, "demo", "", "body"},
		{"empty description", "---\nname: demo\ndescription:\n---\nbody\n", true, "demo", "", "body"},
		{"line without colon", "---\nname: demo\njunk line\ndescription: d\n---\nbody\n", true, "demo", "d", "body"},
		{"leading blank lines", "\n\n---\nname: demo\ndescription: d\n---\nbody\n", true, "demo", "d", "body"},
		{"later rule kept in body", "---\nname: demo\ndescription: d\n---\nfirst\n\n---\n\nsecond\n", true, "demo", "d", "first\n\n---\n\nsecond"},
		{"body trimmed", "---\nname: demo\ndescription: d\n---\n\n\n  text  \n\n\n", true, "demo", "d", "text"},
		{"no trailing newline", "---\nname: demo\ndescription: d\n---\nbody", true, "demo", "d", "body"},
		{"empty name value", "---\nname:\ndescription: d\n---\nbody\n", false, "", "", ""},
		{"whitespace-only body", "---\nname: demo\ndescription: d\n---\n \n\t\n", false, "", "", ""},
		{"empty front matter", "---\n---\nbody\n", false, "", "", ""},
		{"empty input", "", false, "", "", ""},
		{"bom only", "\ufeff", false, "", "", ""},
		{"opening rule not at start", "text\n---\nname: demo\n---\nbody\n", false, "", "", ""},
		{"indented opening rule", " ---\nname: demo\n---\nbody\n", false, "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			s, ok := parseSkill(c.data, "test")
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v (skill %+v)", ok, c.ok, s)
			}
			if !ok {
				return
			}
			if s.name != c.name || s.description != c.desc || s.body != c.body {
				t.Errorf("got name=%q desc=%q body=%q, want name=%q desc=%q body=%q",
					s.name, s.description, s.body, c.name, c.desc, c.body)
			}
		})
	}
}

func TestSkillParseBOMVariants(t *testing.T) {
	const fm = "---\nname: demo\ndescription: d\n---\nbody\n"
	cases := map[string]string{
		"bom":                 "\ufeff" + fm,
		"bom then newline":    "\ufeff\n" + fm,
		"newline then bom":    "\n\ufeff" + fm,
		"repeated bom":        "\ufeff\ufeff" + fm,
		"no bom (control)":    fm,
		"bom, blank line x 2": "\ufeff\n\n" + fm,
	}
	for label, data := range cases {
		s, ok := parseSkill(data, "test")
		if !ok {
			t.Errorf("%s: expected parse to succeed", label)
			continue
		}
		if s.name != "demo" || s.description != "d" || s.body != "body" {
			t.Errorf("%s: got %+v", label, s)
		}
	}

	// A BOM inside the front matter is not stripped, so it must not leak into
	// the body or silently succeed for a mangled key.
	s, ok := parseSkill("---\n\ufeffname: demo\ndescription: d\n---\nbody\n", "test")
	if ok {
		t.Errorf("mid-file BOM before key should leave name empty and fail, got %+v", s)
	}
}

func TestSkillLoadBOMFileFromDisk(t *testing.T) {
	home := skillsEnv(t)
	project := t.TempDir()
	writeSkillFile(t, filepath.Join(home, "prism", "skills"), "bom-user.md",
		"\ufeff---\nname: bom-user\ndescription: BOM user skill.\n---\nUser body.\n")
	writeSkillFile(t, filepath.Join(project, ".prism", "skills"), "bom-proj.md",
		"\ufeff---\nname: bom-proj\ndescription: BOM project skill.\n---\nProject body.\n")

	skills := loadSkills(project)
	if s, ok := findSkill(skills, "bom-user"); !ok || s.source != "yours" || s.body != "User body." {
		t.Errorf("bom-user: ok=%v %+v", ok, s)
	}
	if s, ok := findSkill(skills, "bom-proj"); !ok || s.source != "this project" || s.body != "Project body." {
		t.Errorf("bom-proj: ok=%v %+v", ok, s)
	}
}

func TestSkillProjectOverridesUserOnly(t *testing.T) {
	home := skillsEnv(t)
	project := t.TempDir()
	writeSkillFile(t, filepath.Join(home, "prism", "skills"), "shared.md",
		"---\nname: shared\ndescription: From user.\n---\nUser body.\n")
	writeSkillFile(t, filepath.Join(project, ".prism", "skills"), "shared.md",
		"---\nname: shared\ndescription: From project.\n---\nProject body.\n")

	s, ok := findSkill(loadSkills(project), "shared")
	if !ok {
		t.Fatal("shared missing")
	}
	if s.source != "this project" || s.description != "From project." || s.body != "Project body." {
		t.Errorf("project should win over user: %+v", s)
	}

	// Another project without the override still sees the user version.
	s, ok = findSkill(loadSkills(t.TempDir()), "shared")
	if !ok || s.source != "yours" || s.body != "User body." {
		t.Errorf("user skill should apply elsewhere: ok=%v %+v", ok, s)
	}
}

func TestSkillOverrideKeyedByNameNotFilename(t *testing.T) {
	home := skillsEnv(t)
	project := t.TempDir()
	writeSkillFile(t, filepath.Join(home, "prism", "skills"), "alpha.md",
		"---\nname: keyed\ndescription: User.\n---\nUser body.\n")
	writeSkillFile(t, filepath.Join(project, ".prism", "skills"), "zzz-other-file.md",
		"---\nname: keyed\ndescription: Project.\n---\nProject body.\n")

	skills := loadSkills(project)
	count := 0
	for _, s := range skills {
		if s.name == "keyed" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("keyed appears %d times, want 1", count)
	}
	if s, _ := findSkill(skills, "keyed"); s.source != "this project" || s.body != "Project body." {
		t.Errorf("project should override by name: %+v", s)
	}
}

func TestSkillInvalidProjectFileDoesNotShadowUser(t *testing.T) {
	home := skillsEnv(t)
	project := t.TempDir()
	writeSkillFile(t, filepath.Join(home, "prism", "skills"), "keep.md",
		"---\nname: keep\ndescription: User.\n---\nUser body.\n")
	// Same name but empty body: invalid, so it must not replace the user skill.
	writeSkillFile(t, filepath.Join(project, ".prism", "skills"), "keep.md",
		"---\nname: keep\ndescription: Project.\n---\n")
	// Non-markdown files are ignored.
	writeSkillFile(t, filepath.Join(project, ".prism", "skills"), "notes.txt",
		"---\nname: txt-skill\ndescription: d\n---\nbody\n")

	skills := loadSkills(project)
	if s, ok := findSkill(skills, "keep"); !ok || s.source != "yours" || s.body != "User body." {
		t.Errorf("invalid project file shadowed user skill: ok=%v %+v", ok, s)
	}
	if _, ok := findSkill(skills, "txt-skill"); ok {
		t.Error(".txt file was loaded as a skill")
	}
}

func TestSkillLoadSortedByName(t *testing.T) {
	home := skillsEnv(t)
	project := t.TempDir()
	writeSkillFile(t, filepath.Join(home, "prism", "skills"), "a.md",
		"---\nname: zz-last\ndescription: d\n---\nbody\n")
	writeSkillFile(t, filepath.Join(project, ".prism", "skills"), "b.md",
		"---\nname: 00-first\ndescription: d\n---\nbody\n")

	skills := loadSkills(project)
	for i := 1; i < len(skills); i++ {
		if skills[i-1].name >= skills[i].name {
			t.Fatalf("skills not strictly sorted: %q before %q", skills[i-1].name, skills[i].name)
		}
	}
	if skills[0].name != "00-first" || skills[len(skills)-1].name != "zz-last" {
		t.Errorf("unexpected ends: first=%q last=%q", skills[0].name, skills[len(skills)-1].name)
	}
}
