package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// pickerState is the /model picker overlay: every route the router knows,
// narrowed by what the user types, with one highlighted row.
type pickerState struct {
	open    bool
	loading bool
	rows    []routeRow
	err     string
	sel     int
	filter  []rune
}

type pickerMsg struct {
	rows []routeRow
	err  error
}

// pickItem is one selectable row: the auto entry or a route.
type pickItem struct {
	auto bool
	row  routeRow
}

// openPicker shows the picker and fetches the routes. Called again while open,
// it refreshes the rows and keeps the filter.
func (m *model) openPicker() tea.Cmd {
	if !m.pick.open {
		m.pick = pickerState{open: true}
	}
	m.pick.loading = true
	api := m.agent.api
	return func() tea.Msg {
		rows, err := fetchRoutes(api)
		return pickerMsg{rows: rows, err: err}
	}
}

// pickerLoaded fills the picker with fetched routes; a late reply after the
// picker closed is dropped.
func (m *model) pickerLoaded(msg pickerMsg) {
	if !m.pick.open {
		return
	}
	m.pick.loading = false
	m.pick.rows, m.pick.err = msg.rows, errText(msg.err)
	m.pick.sel = clamp(m.pick.sel, 0, max(0, len(m.pick.items())-1))
}

// items is the auto entry plus every route whose id contains the filter.
func (p pickerState) items() []pickItem {
	f := strings.ToLower(string(p.filter))
	var out []pickItem
	if strings.Contains("auto", f) {
		out = append(out, pickItem{auto: true})
	}
	for _, r := range p.rows {
		if strings.Contains(strings.ToLower(r.Route), f) {
			out = append(out, pickItem{row: r})
		}
	}
	return out
}

func (m model) pickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	items := m.pick.items()
	switch msg.String() {
	case "esc":
		m.pick = pickerState{}
	case "up":
		if len(items) > 0 {
			m.pick.sel = (m.pick.sel - 1 + len(items)) % len(items)
		}
	case "down":
		if len(items) > 0 {
			m.pick.sel = (m.pick.sel + 1) % len(items)
		}
	case "enter":
		if len(items) == 0 {
			return m, nil
		}
		it := items[clamp(m.pick.sel, 0, len(items)-1)]
		m.pick = pickerState{}
		if it.auto {
			m.agent.model = ""
			m.entries = append(m.entries, &entry{kind: "note", text: "model: auto"})
		} else {
			m.agent.model = it.row.Route
			m.entries = append(m.entries, &entry{kind: "note", text: "model: " + it.row.Route + " · every request in this chat uses it"})
		}
		m.scroll = 0
	case "backspace":
		if len(m.pick.filter) > 0 {
			m.pick.filter = m.pick.filter[:len(m.pick.filter)-1]
			m.pick.sel = 0
		}
	case "ctrl+u":
		m.pick.filter, m.pick.sel = nil, 0
	default:
		if msg.Text != "" && !strings.ContainsAny(msg.Text, "\n\r\t") {
			m.pick.filter = append(m.pick.filter, []rune(msg.Text)...)
			m.pick.sel = 0
		}
	}
	return m, nil
}

type pickLine struct {
	fill rgb
	segs []seg
}

// drawPicker draws the picker just above the input box at y, in the
// drawSuggestions style, never above top.
func drawPicker(cv *canvas, t theme, m model, x, y, w, top int) {
	p := m.pick
	accent := t.at(0.5)
	inner := w - 6
	head := []pickLine{
		{fill: t.panel, segs: []seg{{text: "model", fg: t.fg, bold: true}, {text: "  ↑↓ move · enter pin · esc close", fg: t.muted}}},
	}
	filter := []seg{{text: "› ", fg: accent, bold: true}}
	if len(p.filter) == 0 {
		filter = append(filter, seg{text: "▏type to filter", fg: t.muted})
	} else {
		filter = append(filter, seg{text: string(p.filter), fg: t.fg}, seg{text: "▏", fg: t.fg})
	}
	head = append(head, pickLine{fill: t.faint, segs: filter})

	var body []pickLine
	items := p.items()
	switch {
	case p.loading && len(p.rows) == 0:
		body = append(body, pickLine{fill: t.panel, segs: []seg{{text: "loading…", fg: t.muted}}})
	case p.err != "" && len(p.rows) == 0:
		body = append(body, pickLine{fill: t.panel, segs: []seg{{text: "✕ ", fg: t.red}, {text: p.err, fg: t.red}}})
	case len(items) == 0:
		body = append(body, pickLine{fill: t.panel, segs: []seg{{text: fmt.Sprintf("no model matches %q", string(p.filter)), fg: t.muted}}})
	default:
		avail := y - top - len(head)
		shown := min(len(items), max(1, min(12, avail-1)))
		off := 0
		if p.sel >= shown {
			off = p.sel - shown + 1
		}
		nameW := 10
		for _, it := range items[off : off+shown] {
			nameW = max(nameW, len([]rune(modelName(it.row.Route))))
		}
		nameW = max(6, min(nameW, inner-28))
		for i, it := range items[off : off+shown] {
			hi := off+i == p.sel
			fill, nameCol := t.panel, t.fg
			if hi {
				fill, nameCol = mix(t.panel, accent, 0.14), accent
			}
			var segs []seg
			if it.auto {
				state := ""
				if m.agent.model == "" {
					state = "  · current"
				}
				segs = []seg{
					{text: "◆ ", fg: accent},
					{text: pad("auto", nameW+2), fg: nameCol, bold: hi},
					{text: "prism chooses" + state, fg: t.muted},
				}
			} else {
				r := it.row
				dot, state, col := "● ", "ready", t.green
				if !r.Ready {
					dot, state, col = "◌ ", fmt.Sprintf("back in %ds", r.RetryIn), t.muted
				}
				if r.Route == m.agent.model {
					state += " · current"
				}
				segs = []seg{
					{text: dot, fg: col},
					{text: padRunes(clip(modelName(r.Route), nameW), nameW+2), fg: nameCol, bold: hi},
					{text: padRunes(clip(provider(r.Route), 11), 12), fg: t.muted},
					{text: state, fg: col},
				}
			}
			body = append(body, pickLine{fill: fill, segs: segs})
		}
		if hidden := len(items) - shown; hidden > 0 {
			body = append(body, pickLine{fill: t.panel, segs: []seg{{text: fmt.Sprintf("%d more · type to narrow", hidden), fg: t.muted}}})
		}
	}

	all := append(head, body...)
	for i, l := range all {
		row := y - len(all) + i
		if row < top {
			continue
		}
		cv.fill(x+1, row, w-2, 1, l.fill)
		for k := x + 1; k < x+w-1; k++ {
			cv.set(k, row, ' ', t.fg, false)
		}
		lx, end := x+3, x+w-3
		for _, s := range l.segs {
			if lx >= end {
				break
			}
			lx = cv.text(lx, row, clip(s.text, end-lx), s.fg, s.bold)
		}
	}
}

// padRunes pads by runes, so a clipped name ending in "…" still lines up.
func padRunes(s string, n int) string {
	if k := len([]rune(s)); k < n {
		return s + strings.Repeat(" ", n-k)
	}
	return s
}
