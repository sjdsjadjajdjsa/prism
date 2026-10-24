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

// roleAutos are the role-ranked auto choices the /model picker offers after
// plain auto, with what each one is for.
var roleAutos = []struct{ name, what string }{
	{"auto:code", "best at code"},
	{"auto:fast", "quickest answers"},
	{"auto:review", "careful reviewer"},
}

// byProvider groups routes by provider, providers in the order the router
// tries their first route and routes in router order inside each group.
func byProvider(rows []routeRow) []routeRow {
	var order []string
	groups := map[string][]routeRow{}
	for _, r := range rows {
		p := provider(r.Route)
		if _, ok := groups[p]; !ok {
			order = append(order, p)
		}
		groups[p] = append(groups[p], r)
	}
	out := make([]routeRow, 0, len(rows))
	for _, p := range order {
		out = append(out, groups[p]...)
	}
	return out
}

// routeTag labels a route that is not one of the free built-ins: "yours" for
// a key you added, "plan" for a subscription you signed in to.
func routeTag(r routeRow) string {
	switch r.Source {
	case "subscription":
		return "plan"
	case "yours":
		return "yours"
	}
	return ""
}

// modelMenu is the /model picker: auto, the role autos, then every route
// grouped by provider. Each row's match is what "/model <match>" pins.
func modelMenu(rows []routeRow, current string) []menuItem {
	items := modelItems(nil, current)
	for _, ra := range roleAutos {
		it := menuItem{dot: "◆", dotTone: toneAccent, label: ra.name, state: ra.what, match: ra.name}
		if current == ra.name {
			it.state += "  · current"
		}
		items = append(items, it)
	}
	routes := modelItems(byProvider(rows), current)[1:]
	for i, r := range byProvider(rows) {
		if tag := routeTag(r); tag != "" {
			routes[i].state += " · " + tag
		}
	}
	return append(items, routes...)
}

// pinModel is /model <name> for the web: auto and the role autos set
// directly; anything else is resolved against the router's routes like the
// TUI modelCommand. It returns the note the TUI shows for the same pin.
func pinModel(a *Agent, name string) string {
	switch name {
	case "auto":
		a.model = ""
		return "model: auto"
	case "auto:code", "auto:fast", "auto:review", "auto:vision":
		a.model = name
		return "model: " + name
	}
	rows, err := fetchRoutes(a.api)
	if err != nil {
		a.model = name
		return "pinned " + name + " · " + err.Error()
	}
	full := resolveModel(name, rows)
	a.model = full
	for _, r := range rows {
		if r.Route == full {
			return "model: " + full + " · every request in this chat uses it"
		}
	}
	return "pinned " + full + " · the router does not list it yet (it may appear once its key is set)"
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
