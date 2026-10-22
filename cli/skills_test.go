package main

import (
	"os"
	"path/filepath"
	"strings"
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

func findSkill(skills []skill, name string) (skill, bool) {
	for _, s := range skills {
		if s.name == name {
			return s, true
		}
	}
	return skill{}, false
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
		want = append(want, strings.TrimSuffix(name, ".md"))
	}
	if len(want) != 13 {
		t.Fatalf("embedded skill files = %d, want 13", len(want))
	}

	skills := loadSkills(t.TempDir())
	if len(skills) != 13 {
		t.Fatalf("loadSkills returned %d skills, want 13", len(skills))
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
	if _, ok := findSkill(skills, "broken"); ok {
		t.Error("invalid skill file was loaded")
	}
	if len(skills) != 15 {
		t.Errorf("total skills = %d, want 15 (13 built-in + 2 custom)", len(skills))
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
	for _, s := range skills {
		line := "- " + s.name + ": " + s.description
		if !strings.Contains(idx, line) {
			t.Errorf("index missing line %q", line)
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
