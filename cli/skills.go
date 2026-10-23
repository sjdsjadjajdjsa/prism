package main

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed skills/*.md
var builtinSkills embed.FS

type skill struct {
	name, description, body, source string
}

func parseSkill(data, source string) (skill, bool) {
	rest, ok := strings.CutPrefix(strings.TrimLeft(data, "\ufeff\n"), "---\n")
	if !ok {
		return skill{}, false
	}
	head, body, ok := strings.Cut(rest, "\n---")
	if !ok {
		return skill{}, false
	}
	s := skill{body: strings.TrimSpace(strings.TrimPrefix(body, "\n")), source: source}
	for _, l := range strings.Split(head, "\n") {
		k, v, _ := strings.Cut(l, ":")
		switch strings.TrimSpace(k) {
		case "name":
			s.name = strings.TrimSpace(v)
		case "description":
			s.description = strings.TrimSpace(v)
		}
	}
	return s, s.name != "" && s.body != ""
}

func loadSkills(dir string) []skill {
	found := map[string]skill{}
	entries, _ := fs.Glob(builtinSkills, "skills/*.md")
	for _, f := range entries {
		data, _ := builtinSkills.ReadFile(f)
		if s, ok := parseSkill(string(data), "built in"); ok {
			found[s.name] = s
		}
	}
	for _, d := range []struct{ path, label string }{
		{filepath.Join(configDir(), "skills"), "yours"},
		{filepath.Join(dir, ".prism", "skills"), "this project"},
	} {
		files, _ := filepath.Glob(filepath.Join(d.path, "*.md"))
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			if s, ok := parseSkill(string(data), d.label); ok {
				found[s.name] = s
			}
		}
	}
	var out []skill
	for _, s := range found {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func skillIndex(skills []skill) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nWorkflow: Verify meaningful changes with existing checks and real behavior when possible; report unrun checks honestly. When a fix fails, inspect the error and revise the hypothesis before a bounded retry; ask only when truly blocked. For substantial unfinished work, leave a safe handoff with pending steps and test results, without overwriting unrelated files or including secrets.\nSkills: optional playbooks. Choose task-specific skills by intent, not exact words; load a relevant skill when its detailed steps help. Lifecycle skills (prove-it, retry-ladder, handoff-note) apply at the appropriate stage and need not be loaded at the start of a simple task.\n")
	for _, s := range skills {
		b.WriteString("- " + s.name + ": " + s.description + "\n")
	}
	return b.String()
}

var skillSpec = spec("skill", "Load a skill playbook by name and follow its steps.",
	map[string]any{"name": str("The skill name from the list in your instructions")}, "name")

func (a *Agent) useSkill(raw string) toolResult {
	name := sarg(argsOf(raw), "name")
	for _, s := range a.skills {
		if s.name == name {
			return toolResult{ok: true, output: s.body, summary: "loaded"}
		}
	}
	var names []string
	for _, s := range a.skills {
		names = append(names, s.name)
	}
	return fail("no skill called " + name + ". Available: " + strings.Join(names, ", "))
}

func (a *Agent) isSkill(name string) bool {
	for _, s := range a.skills {
		if s.name == name {
			return true
		}
	}
	return false
}
