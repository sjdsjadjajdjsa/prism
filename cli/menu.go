package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// The menu is the overlay every "pick one from a list" command uses (/model,
// /login, /key, /resume, /theme, /tabs, /mcp add). Each command builds its own
// rows and actions.

type tone int

const (
	toneMuted tone = iota
	toneOK
	toneErr
	toneAccent
	toneFg
)

func (t theme) tone(k tone) rgb {
	switch k {
	case toneOK:
		return t.green
	case toneErr:
		return t.red
	case toneAccent:
		return t.at(0.5)
	case toneFg:
		return t.fg
	}
	return t.muted
}

type menuItem struct {
	label, detail, state string
	dot                  string
	dotTone, stateTone   tone
	sub                  bool                   // an action row under the one above (sign out, remove)
	match                string                 // what the filter matches; label and detail when empty
	hover                func(m *model)         // the highlight landed here (theme preview)
	run                  func(m *model) tea.Cmd // enter or a click
}

// menuState is the open menu. kind names the command that owns it, so a late
// fetch reply for another menu (or a closed one) is dropped.
type menuState struct {
	open       bool
	kind       string
	title      string
	hint       string
	filterable bool
	noun       string // "no <noun> matches"
	empty      string // shown when the list has no rows at all
	loading    bool
	err        string
	items      []menuItem
	filter     []rune
	sel, off   int
	onClose    func(m *model) // esc or a click outside, not a pick
}

const menuMaxRows = 12

func (p menuState) visible() []menuItem {
	if len(p.filter) == 0 {
		return p.items
	}
	f := strings.ToLower(string(p.filter))
	var out []menuItem
	for _, it := range p.items {
		key := it.match
		if key == "" {
			key = it.label + " " + it.detail
		}
		if strings.Contains(strings.ToLower(key), f) {
			out = append(out, it)
		}
	}
	return out
}

// menuLayout is where the menu's lines land; drawing and mouse hit-testing
// both use it, so a row on screen always maps to the item drawn there.
type menuLayout struct {
	head  int // title (and filter) lines
	shown int // item rows on screen; 0 when a message shows instead
	off   int // first item shown
	lines int // every line: head, rows and the "more" line
}

func (p menuState) layout(y, top int) menuLayout {
	l := menuLayout{head: 1}
	if p.filterable {
		l.head = 2
	}
	n := len(p.visible())
	if n == 0 {
		l.lines = l.head + 1
		return l
	}
	l.shown = min(n, max(1, min(menuMaxRows, y-top-l.head-1)))
	l.off = keepInView(p.off, clamp(p.sel, 0, n-1), l.shown, n)
	l.lines = l.head + l.shown
	if n > l.shown {
		l.lines++
	}
	return l
}

// keepInView is the first row to show so that sel stays on screen, moving
// off as little as possible.
func keepInView(off, sel, shown, n int) int {
	off = clamp(off, 0, max(0, n-shown))
	if sel < off {
		off = sel
	}
	if sel >= off+shown {
		off = sel - shown + 1
	}
	return max(0, off)
}

// overlayArea is where the chat view puts overlays: the column's x and width,
// the input box row y (overlays end just above it) and the first chat row.
func (m model) overlayArea() (x, y, w, top int) {
	w = m.column()
	boxH := 3 // before the first resize there is no box to measure
	if w-6 > 0 {
		boxH = m.boxHeight()
	}
	return (m.chatW() - w) / 2, m.h - 3 - boxH, w, 4 + m.tabRows()
}

// openMenu shows p. Reopening the same kind keeps the filter and highlight.
func (m *model) openMenu(p menuState) {
	if m.menu.open && m.menu.kind == p.kind {
		p.filter, p.sel, p.off = m.menu.filter, m.menu.sel, m.menu.off
		if p.items == nil {
			p.items = m.menu.items // a refresh: keep the rows until the new ones land
		}
	}
	p.open = true
	m.fb = filesState{}
	m.menu = p
	m.menuSelect(p.sel)
}

func (m *model) fillMenu(kind string, items []menuItem, err error) {
	if !m.menu.open || m.menu.kind != kind {
		return
	}
	m.menu.loading = false
	m.menu.items, m.menu.err = items, errText(err)
	m.menuSelect(m.menu.sel)
}

func (m *model) menuSelect(i int) {
	vis := m.menu.visible()
	if len(vis) == 0 {
		m.menu.sel, m.menu.off = 0, 0
		return
	}
	m.menu.sel = clamp(i, 0, len(vis)-1)
	_, y, _, top := m.overlayArea()
	m.menu.off = m.menu.layout(y, top).off
	if h := vis[m.menu.sel].hover; h != nil {
		h(m)
	}
}

func (m *model) closeMenu() {
	done := m.menu.onClose
	m.menu = menuState{}
	if done != nil {
		done(m)
	}
}

func (m *model) menuRun() tea.Cmd {
	vis := m.menu.visible()
	if len(vis) == 0 {
		return nil
	}
	it := vis[clamp(m.menu.sel, 0, len(vis)-1)]
	m.menu = menuState{}
	if it.run == nil {
		return nil
	}
	return it.run(m)
}

func (m model) menuKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	n := len(m.menu.visible())
	_, y, _, top := m.overlayArea()
	page := max(1, m.menu.layout(y, top).shown)
	switch msg.String() {
	case "esc":
		m.closeMenu()
	case "up":
		if n > 0 {
			m.menuSelect((m.menu.sel - 1 + n) % n)
		}
	case "down":
		if n > 0 {
			m.menuSelect((m.menu.sel + 1) % n)
		}
	case "pgup":
		m.menuSelect(m.menu.sel - page)
	case "pgdown":
		m.menuSelect(m.menu.sel + page)
	case "home":
		m.menuSelect(0)
	case "end":
		m.menuSelect(n - 1)
	case "enter":
		return m, m.menuRun()
	case "backspace":
		if m.menu.filterable && len(m.menu.filter) > 0 {
			m.menu.filter = m.menu.filter[:len(m.menu.filter)-1]
			m.menu.off = 0
			m.menuSelect(0)
		}
	case "ctrl+u":
		if m.menu.filterable {
			m.menu.filter, m.menu.off = nil, 0
			m.menuSelect(0)
		}
	default:
		if m.menu.filterable && msg.Text != "" && !strings.ContainsAny(msg.Text, "\n\r\t") {
			m.menu.filter = append(m.menu.filter, []rune(msg.Text)...)
			m.menu.off = 0
			m.menuSelect(0)
		}
	}
	return m, nil
}

// menuHit maps a screen cell to the menu: the visible item drawn there (-1
// for none) and whether the cell is inside the menu at all.
func (m model) menuHit(mx, my int) (idx int, inside bool) {
	x, y, w, top := m.overlayArea()
	l := m.menu.layout(y, top)
	if mx < x+1 || mx > x+w-2 || my < max(top, y-l.lines) || my >= y {
		return -1, false
	}
	if r := my - (y - l.lines + l.head); r >= 0 && r < l.shown {
		return l.off + r, true
	}
	return -1, true
}

// wheelDelta is how many rows one wheel notch moves (negative is up).
func wheelDelta(msg tea.MouseWheelMsg) int {
	switch tea.Mouse(msg).Button {
	case tea.MouseWheelUp:
		return -3
	case tea.MouseWheelDown:
		return 3
	}
	return 0
}

// menuMouse: the wheel scrolls, the pointer highlights, a left click picks
// the row under it, a click outside closes the menu.
func (m model) menuMouse(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		d := wheelDelta(msg)
		_, y, _, top := m.overlayArea()
		l := m.menu.layout(y, top)
		if d == 0 || l.shown == 0 {
			break
		}
		m.menu.off = clamp(l.off+d, 0, max(0, len(m.menu.visible())-l.shown))
		m.menuSelect(clamp(m.menu.sel, m.menu.off, m.menu.off+l.shown-1))
	case tea.MouseMotionMsg:
		mo := tea.Mouse(msg)
		if idx, _ := m.menuHit(mo.X, mo.Y); idx >= 0 && idx != m.menu.sel {
			m.menuSelect(idx)
		}
	case tea.MouseClickMsg:
		mo := tea.Mouse(msg)
		if mo.Button != tea.MouseLeft {
			break
		}
		idx, inside := m.menuHit(mo.X, mo.Y)
		switch {
		case !inside:
			m.closeMenu()
		case idx >= 0:
			m.menu.sel = idx
			return m, m.menuRun()
		}
	}
	return m, nil
}

type pickLine struct {
	fill rgb
	segs []seg
}

func drawMenu(cv *canvas, t theme, m model, x, y, w, top int) {
	p := m.menu
	accent := t.at(0.5)
	inner := w - 6
	l := p.layout(y, top)
	hint := p.hint
	if hint == "" {
		hint = "↑↓ move · enter pick · esc close"
	}
	lines := []pickLine{{fill: t.panel, segs: []seg{{text: p.title, fg: t.fg, bold: true}, {text: "  " + hint, fg: t.muted}}}}
	if p.filterable {
		filter := []seg{{text: "› ", fg: accent, bold: true}}
		if len(p.filter) == 0 {
			filter = append(filter, seg{text: "▏type to filter", fg: t.muted})
		} else {
			filter = append(filter, seg{text: string(p.filter), fg: t.fg}, seg{text: "▏", fg: t.fg})
		}
		lines = append(lines, pickLine{fill: t.faint, segs: filter})
	}
	vis := p.visible()
	msg := func(text string, col rgb) {
		lines = append(lines, pickLine{fill: t.panel, segs: []seg{{text: text, fg: col}}})
	}
	switch {
	case p.loading && len(p.items) == 0:
		msg("loading…", t.muted)
	case p.err != "" && len(p.items) == 0:
		msg("✕ "+p.err, t.red)
	case len(vis) == 0 && len(p.filter) > 0:
		noun := p.noun
		if noun == "" {
			noun = "row"
		}
		msg(fmt.Sprintf("no %s matches %q", noun, string(p.filter)), t.muted)
	case len(vis) == 0:
		empty := p.empty
		if empty == "" {
			empty = "nothing here yet"
		}
		msg(empty, t.muted)
	default:
		rows := vis[l.off : l.off+l.shown]
		nameW, detW := 10, 0
		for _, it := range rows {
			if !it.sub {
				nameW = max(nameW, len([]rune(it.label)))
				detW = max(detW, len([]rune(it.detail)))
			}
		}
		nameW = max(6, min(nameW, inner-28))
		detW = min(detW, max(0, inner-nameW-16))
		for i, it := range rows {
			hi := l.off+i == p.sel
			fill, nameCol := t.panel, t.fg
			if hi {
				fill, nameCol = mix(t.panel, accent, 0.14), accent
			}
			var segs []seg
			if it.sub {
				segs = []seg{{text: "  ↳ ", fg: t.muted}, {text: it.label, fg: nameCol, bold: hi}}
			} else {
				dot := it.dot
				if dot == "" {
					dot = " "
				}
				segs = []seg{
					{text: dot + " ", fg: t.tone(it.dotTone)},
					{text: padRunes(clip(it.label, nameW), nameW+2), fg: nameCol, bold: hi},
				}
				if detW > 0 {
					segs = append(segs, seg{text: padRunes(clip(it.detail, detW), detW+2), fg: t.muted})
				}
				if it.state != "" {
					segs = append(segs, seg{text: it.state, fg: t.tone(it.stateTone)})
				}
			}
			lines = append(lines, pickLine{fill: fill, segs: segs})
		}
		if hidden := len(vis) - l.shown; hidden > 0 {
			more := fmt.Sprintf("%d more · scroll", hidden)
			if p.filterable {
				more += " or type to narrow"
			}
			msg(more, t.muted)
		}
	}
	drawPickLines(cv, t, lines, x, y, w, top)
}

// drawPickLines draws overlay lines so the last one sits just above row y,
// clipped to the column and never above top.
func drawPickLines(cv *canvas, t theme, all []pickLine, x, y, w, top int) {
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
