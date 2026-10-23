package main

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// The /model picker: every route the router knows on the shared menu, with
// auto at the top and the pinned route marked current.

type pickerMsg struct {
	rows []routeRow
	err  error
}

// openPicker shows the picker and fetches the routes. Called again while open,
// it refreshes the rows and keeps the filter.
func (m *model) openPicker() tea.Cmd {
	m.openMenu(menuState{
		kind:       "model",
		title:      "model",
		hint:       "↑↓ move · enter pin · esc close",
		filterable: true,
		noun:       "model",
		loading:    true,
	})
	api := m.agent.api
	return func() tea.Msg {
		rows, err := fetchRoutes(api)
		return pickerMsg{rows: rows, err: err}
	}
}

// pickerLoaded fills the picker with fetched routes; a late reply after the
// picker closed is dropped.
func (m *model) pickerLoaded(msg pickerMsg) {
	var items []menuItem
	if msg.err == nil || len(msg.rows) > 0 {
		items = modelItems(msg.rows, m.agent.model)
	}
	m.fillMenu("model", items, msg.err)
}

// modelItems is the auto row plus one row per route. The filter matches the
// full route id; enter pins it.
func modelItems(rows []routeRow, current string) []menuItem {
	state := "prism chooses"
	if current == "" {
		state += "  · current"
	}
	items := []menuItem{{
		dot: "◆", dotTone: toneAccent, label: "auto", state: state, match: "auto",
		run: func(m *model) tea.Cmd {
			m.agent.model = ""
			m.entries = append(m.entries, &entry{kind: "note", text: "model: auto"})
			m.scroll = 0
			return nil
		},
	}}
	for _, r := range rows {
		it := menuItem{dot: "●", dotTone: toneOK, label: modelName(r.Route), detail: clip(provider(r.Route), 11),
			state: "ready", stateTone: toneOK, match: r.Route}
		if !r.Ready {
			it.dot, it.dotTone, it.state, it.stateTone = "◌", toneMuted, fmt.Sprintf("back in %ds", r.RetryIn), toneMuted
		}
		if r.Route == current {
			it.state += " · current"
		}
		route := r.Route
		it.run = func(m *model) tea.Cmd {
			m.agent.model = route
			m.entries = append(m.entries, &entry{kind: "note", text: "model: " + route + " · every request in this chat uses it"})
			m.scroll = 0
			return nil
		}
		items = append(items, it)
	}
	return items
}
