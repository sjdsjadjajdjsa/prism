package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// filterRoutes narrows the /models list to rows whose route or provider matches
// the text, and caps the list so a huge provider (OpenRouter probes list ~300)
// stays readable. Returns the rows to show and how many were hidden.
func filterRoutes(rows []routeRow, filter string) ([]routeRow, int) {
	filter = strings.ToLower(strings.TrimSpace(filter))
	var kept []routeRow
	for _, r := range rows {
		if filter != "" && !strings.Contains(strings.ToLower(r.Route), filter) {
			continue
		}
		kept = append(kept, r)
	}
	const cap = 60
	if len(kept) <= cap {
		return kept, 0
	}
	return kept[:cap], len(kept) - cap
}

// resolveModel accepts what the user sees: the full route id
// ("chatgpt/gpt-6.1-sol") or the short model name the /models list displays
// ("gpt-6.1-sol"). A short name that matches several routes is left as-is so
// the router reports it, and an unknown name is returned unchanged so the
// pin note can say the router does not list it.
func resolveModel(name string, rows []routeRow) string {
	for _, r := range rows {
		if r.Route == name {
			return name
		}
	}
	var matches []string
	for _, r := range rows {
		if modelName(r.Route) == name {
			matches = append(matches, r.Route)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return name
}

type modelMsg struct {
	name  string
	found bool
	err   error
}

// modelCommand runs /model: pin the model for this chat, or go back to auto.
func (m *model) modelCommand(fields []string) tea.Cmd {
	if len(fields) == 1 {
		if m.agent.model == "" {
			m.entries = append(m.entries, &entry{kind: "note", text: "this chat uses auto · /model <name> pins one · /models lists them"})
		} else {
			m.entries = append(m.entries, &entry{kind: "note", text: "this chat uses " + m.agent.model + " · /model auto lets prism choose again"})
		}
		return nil
	}
	name := fields[1]
	if name == "auto" || name == "auto:code" || name == "auto:fast" || name == "auto:review" || name == "auto:vision" {
		m.agent.model = ""
		if name != "auto" {
			m.agent.model = name
		}
		setting := "auto"
		if name != "auto" {
			setting = name
		}
		m.entries = append(m.entries, &entry{kind: "note", text: "model: " + setting})
		return nil
	}
	api := m.agent.api
	return func() tea.Msg {
		rows, err := fetchRoutes(api)
		if err != nil {
			m.agent.model = name
			return modelMsg{name: name, err: err}
		}
		full := resolveModel(name, rows)
		m.agent.model = full
		found := false
		for _, r := range rows {
			if r.Route == full {
				found = true
				break
			}
		}
		return modelMsg{name: full, found: found}
	}
}
