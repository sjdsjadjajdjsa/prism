package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

type seg struct {
	text  string
	fg    rgb
	bold  bool
	grad  bool
	phase float64
}

type line struct {
	segs   []seg
	fill   *rgb
	indent int
}

func wrap(s string, width int) []string {
	width = max(width, 8)
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var out []string
	var cur []rune
	for _, word := range words {
		r := []rune(word)
		for len(r) > width {
			if len(cur) > 0 {
				out, cur = append(out, string(cur)), nil
			}
			out, r = append(out, string(r[:width])), r[width:]
		}
		if len(cur) > 0 && len(cur)+1+len(r) > width {
			out, cur = append(out, string(cur)), nil
		}
		if len(cur) > 0 {
			cur = append(cur, ' ')
		}
		cur = append(cur, r...)
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

func clip(s string, width int) string {
	r := []rune(strings.ReplaceAll(s, "\t", "    "))
	if width < 1 {
		return ""
	}
	if len(r) > width {
		return string(r[:width-1]) + "…"
	}
	return string(r)
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func modelName(route string) string {
	if route == "" {
		return "auto"
	}
	name := route[strings.LastIndex(route, "/")+1:]
	if strings.Contains(name, "glm-5.3-flash") {
		name += " (Ox Alpha)"
	}
	return name
}

var toolHue = map[string]float64{"list": 0.5, "grep": 0.45, "read": 0.55, "edit": 0.7, "write": 0.75, "bash": 0.85}

func (m model) column() int { return min(m.w-6, 100) }

func (m model) inputRows(width int) []string {
	var rows []string
	for _, para := range strings.Split(string(m.input), "\n") {
		r := []rune(para)
		for len(r) > width {
			rows, r = append(rows, string(r[:width])), r[width:]
		}
		rows = append(rows, string(r))
	}
	return rows
}

func lastRows(rows []string) ([]string, int) {
	if len(rows) > 5 {
		return rows[len(rows)-5:], len(rows) - 5
	}
	return rows, 0
}

func (m model) boxHeight() int { return min(len(m.inputRows(m.column()-6)), 5) + 2 }

func (m model) viewHeight() int { return max(1, m.h-3-m.boxHeight()-1-4) }

func (m model) transcript(t theme, width int) []line {
	n := ms(m.now)
	var out []line
	gap := func() {
		if len(out) > 0 && (len(out[len(out)-1].segs) > 0 || out[len(out)-1].fill != nil) {
			out = append(out, line{})
		}
	}
	panel := t.panel
	red, green := mix(t.panel, t.red, 0.18), mix(t.panel, t.green, 0.15)
	for i, e := range m.entries {
		switch e.kind {
		case "user":
			gap()
			for j, row := range wrapAll(e.text, width-4) {
				prefix := seg{text: "  "}
				if j == 0 {
					prefix = seg{text: "▍ ", grad: true, phase: float64(i) * 0.13}
				}
				out = append(out, line{segs: []seg{prefix, {text: row, fg: t.fg, bold: true}}})
			}
		case "text":
			gap()
			out = append(out, markdown(t, e.text, width)...)
		case "tool":
			if i == 0 || m.entries[i-1].kind != "tool" {
				gap()
			}
			hue := toolHue[e.tool]
			glyph, gc := "◆ ", t.at(hue)
			if !e.done {
				glyph, gc = "◇ ", mix(t.muted, t.at(hue), 0.5+0.5*math.Sin(n/120))
			} else if !e.res.ok {
				gc = t.red
			}
			segs := []seg{{text: glyph, fg: gc}, {text: pad(e.tool, 6), fg: t.muted}, {text: clip(e.arg, width-34), fg: t.fg}}
			if e.done {
				segs = append(segs, summary(t, e.res, 26)...)
			}
			out = append(out, line{segs: segs, indent: 2})
			if e.done && e.res.ok && len(e.res.diff) > 0 {
				out = append(out, line{fill: &panel, indent: 4})
				shown := e.res.diff[:min(len(e.res.diff), 12)]
				for _, d := range shown {
					fill, fg := &panel, t.fg
					switch d.kind {
					case '-':
						fill, fg = &red, t.red
					case '+':
						fill, fg = &green, t.green
					}
					out = append(out, line{fill: fill, indent: 4, segs: []seg{
						{text: fmt.Sprintf("%4d %c  ", d.num, d.kind), fg: mix(fg, t.muted, 0.4)},
						{text: clip(d.text, width-18), fg: fg},
					}})
				}
				if extra := len(e.res.diff) - len(shown); extra > 0 {
					out = append(out, line{fill: &panel, indent: 4, segs: []seg{{text: fmt.Sprintf("       … %d more lines", extra), fg: t.muted}}})
				}
				out = append(out, line{fill: &panel, indent: 4})
			}
		case "note":
			gap()
			out = append(out, line{indent: 2, segs: []seg{{text: "· " + e.text, fg: t.muted}}})
		case "error":
			gap()
			for j, row := range wrapAll(e.text, width-6) {
				mark := "  "
				if j == 0 {
					mark = "✕ "
				}
				out = append(out, line{indent: 2, segs: []seg{{text: mark, fg: t.red}, {text: row, fg: t.red}}})
			}
			for _, row := range wrapAll(e.arg, width-6) {
				if row != "" {
					out = append(out, line{indent: 4, segs: []seg{{text: row, fg: t.muted}}})
				}
			}
		case "help":
			gap()
			if e.text != "" {
				out = append(out, line{indent: 2, segs: []seg{{text: "· " + e.text + " · try one of these:", fg: t.muted}}})
			}
			for k, cmd := range commands {
				out = append(out, line{indent: 2, segs: []seg{{text: pad(cmd.name, 10), fg: t.at(0.5 + float64(k)*0.08), bold: true}, {text: cmd.help, fg: t.muted}}})
			}
		case "info":
			gap()
			out = append(out, line{indent: 2, segs: []seg{{text: e.text, fg: t.fg, bold: true}}})
			for _, l := range e.lines {
				for _, row := range wrap(l, width-8) {
					if l == "" {
						row = ""
					}
					out = append(out, line{indent: 4, segs: []seg{{text: row, fg: t.muted}}})
				}
			}
		case "models":
			gap()
			out = append(out, modelLines(t, e, width)...)
		case "meta":
			gap()
			out = append(out, line{indent: 2, segs: []seg{
				{text: "◆ ", grad: true, phase: 0.1},
				{text: modelName(m.route), fg: t.muted},
				{text: "  ·  " + e.text + "  ·  ", fg: mix(t.bg, t.muted, 0.6)},
				{text: "free", fg: t.green},
			}})
		}
	}
	if m.busy && m.draft != nil {
		gap()
		d := m.draft
		verb := map[string]string{"write": "writing", "edit": "editing", "bash": "preparing", "read": "opening"}[d.name]
		if verb == "" {
			verb = "preparing " + d.name
		}
		dots := strings.Repeat(".", int(n/300)%4)
		out = append(out, line{indent: 2, segs: []seg{
			{text: "◇ ", fg: mix(t.muted, t.at(toolHue[d.name]), 0.5+0.5*math.Sin(n/120))},
			{text: verb + " ", grad: true, phase: n / 900},
			{text: clip(d.arg, width-40), fg: t.fg},
			{text: pad(dots, 4), fg: t.muted},
			{text: short(d.chars) + " chars", fg: mix(t.bg, t.muted, 0.8)},
		}})
	}
	if m.busy && m.thinking {
		gap()
		dots := strings.Repeat(".", int(n/300)%4)
		label, elapsed, hint := "thinking", (m.now - m.thinkSince).Seconds(), ""
		switch {
		case m.reasoning == "" && !m.answering && elapsed > 4:
			label = "waiting for a free model"
		case m.reasoning == "" && m.answering && elapsed > 6:
			label = "writing"
			if elapsed > 12 {
				hint = "  ·  big files show up when they're finished"
			}
		}
		out = append(out, line{indent: 2, segs: []seg{{text: label, grad: true, phase: n / 900}, {text: pad(dots, 4), fg: t.muted}, {text: fmt.Sprintf("%.0fs", elapsed), fg: mix(t.bg, t.muted, 0.7)}, {text: hint, fg: mix(t.bg, t.muted, 0.6)}}})
		if m.reasoning != "" {
			rows := wrapAll(strings.Join(strings.Fields(m.reasoning), " "), width-8)
			rows = rows[max(0, len(rows)-3):]
			for k, row := range rows {
				fade := 0.35 + 0.2*float64(k)
				out = append(out, line{indent: 4, segs: []seg{{text: row, fg: mix(t.bg, t.muted, fade)}}})
			}
		}
	}
	return out
}

func summary(t theme, r toolResult, width int) []seg {
	if !r.ok {
		return []seg{{text: "  " + clip(r.summary, width+20), fg: t.red}}
	}
	if r.diff != nil || r.added+r.removed > 0 {
		return []seg{{text: fmt.Sprintf("  +%d", r.added), fg: t.green}, {text: fmt.Sprintf(" -%d", r.removed), fg: t.red}}
	}
	if rest, ok := strings.CutPrefix(r.summary, "→ "); ok {
		return []seg{{text: "  → ", fg: t.muted}, {text: clip(rest, width), fg: t.green}}
	}
	return []seg{{text: "  " + clip(r.summary, width), fg: t.muted}}
}

func wrapAll(s string, width int) []string {
	var out []string
	for _, para := range strings.Split(strings.TrimSpace(s), "\n") {
		out = append(out, wrap(para, width)...)
	}
	return out
}

func markdown(t theme, text string, width int) []line {
	var out []line
	panel := t.panel
	code := false
	blank := true
	for _, raw := range strings.Split(strings.Trim(text, "\n"), "\n") {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") {
			code = !code
			out = append(out, line{fill: &panel, indent: 2})
			continue
		}
		if code {
			out = append(out, line{fill: &panel, indent: 2, segs: []seg{{text: " " + clip(raw, width-8), fg: t.fg}}})
			continue
		}
		if trimmed == "" {
			if !blank {
				out = append(out, line{})
			}
			blank = true
			continue
		}
		blank = false
		s, fg, bold, lead := trimmed, t.fg, false, ""
		switch {
		case strings.HasPrefix(s, "#"):
			s, bold = strings.TrimSpace(strings.TrimLeft(s, "#")), true
		case strings.HasPrefix(s, "- "), strings.HasPrefix(s, "* "):
			s, lead = s[2:], "• "
		}
		s = strings.NewReplacer("**", "", "`", "").Replace(s)
		for j, row := range wrap(s, width-6) {
			prefix := lead
			if j > 0 && lead != "" {
				prefix = "  "
			}
			out = append(out, line{indent: 2, segs: []seg{{text: prefix, fg: t.at(0.6)}, {text: row, fg: fg, bold: bold}}})
		}
	}
	return out
}

func drawChat(cv *canvas, t theme, m model) {
	n := ms(m.now)
	enter := ease(ms(m.now-m.chatAt) / 500)
	colW := m.column()
	cx := (cv.w - colW) / 2

	x := cv.grad(cx, 1, "◆ prism", t, n/6000, 14, true)
	cv.text(x, 1, "   "+clip(m.project, colW-30), mix(t.bg, t.muted, enter), false)
	right := t.name
	if text, _ := m.speaking(); text != "" && !m.buddyWide(cv.w) {
		right = "◭ " + text
	}
	if m.scroll > 0 {
		right = "↑ scrolled · end to jump back   " + right
	}
	cv.text(cx+colW-len([]rune(right)), 1, right, mix(t.bg, t.muted, enter), false)
	for i := 0; i < colW; i++ {
		cv.set(cx+i, 2, '─', mix(t.bg, t.faint, enter), false)
	}

	boxH := m.boxHeight()
	boxY := cv.h - 3 - boxH
	top, height := 4, m.viewHeight()
	lines := m.transcript(t, colW)
	if len(lines) == 0 {
		hello := []string{"what are we building today?", "", "try:  fix a bug  ·  make a website  ·  /help"}
		if m.offline || m.ready == 0 {
			hello = append(hello, "", "the free AI router isn't ready · run: prism status")
		}
		for i, s := range hello {
			col := t.muted
			if i == 0 {
				col = t.fg
			}
			if i == 4 {
				col = t.red
			}
			cv.text((cv.w-len([]rune(s)))/2, top+height/2-2+i, s, mix(t.bg, col, enter), i == 0)
		}
	}
	end := len(lines) - clamp(m.scroll, 0, max(0, len(lines)-height))
	start := max(0, end-height)
	for i, l := range lines[start:end] {
		y := top + i
		lx := cx + l.indent
		if l.fill != nil {
			cv.fill(lx, y, colW-l.indent-2, 1, *l.fill)
			lx++
		}
		for _, s := range l.segs {
			if s.grad {
				lx = cv.grad(lx, y, s.text, t, s.phase, 10, s.bold)
				continue
			}
			lx = cv.text(lx, y, s.text, s.fg, s.bold)
		}
	}
	for y := top; y < top+height; y++ {
		for i := colW - 1; i < colW+2; i++ {
			if c := cv.get(cx+i, y); c != nil && c.bg == t.bg {
				c.ch = ' '
			}
		}
	}

	drawSuggestions(cv, t, m, cx, boxY, colW)
	drawInput(cv, t, m, cx, boxY, colW, boxH, n, enter)
	drawStatus(cv, t, m, cx, cv.h-2, colW, enter)
	drawBuddy(cv, t, m, cx, colW, boxY)
}

func drawInput(cv *canvas, t theme, m model, x, y, w, h int, n, enter float64) {
	speed := 9000.0
	if m.busy {
		speed = 1400
	}
	phase := n / speed
	edge := func(i int) rgb { return mix(t.bg, t.at(phase+float64(i)/float64(w)), enter*0.9) }
	for i := 1; i < w-1; i++ {
		cv.set(x+i, y, '─', edge(i), false)
		cv.set(x+i, y+h-1, '─', edge(w-i), false)
	}
	for j := 1; j < h-1; j++ {
		cv.set(x, y+j, '│', edge(0), false)
		cv.set(x+w-1, y+j, '│', edge(w-1), false)
	}
	cv.set(x, y, '╭', edge(0), false)
	cv.set(x+w-1, y, '╮', edge(w-1), false)
	cv.set(x, y+h-1, '╰', edge(w), false)
	cv.set(x+w-1, y+h-1, '╯', edge(1), false)

	tx := cv.grad(x+2, y+1, "› ", t, phase, 4, true)
	if len(m.input) == 0 {
		hint := "ask anything…"
		if m.busy {
			hint = "working…  esc to stop"
		}
		cv.text(tx, y+1, hint, mix(t.bg, t.muted, enter), false)
		return
	}
	rows, hidden := lastRows(m.inputRows(w - 6))
	if hidden > 0 {
		cv.text(x+w-18, y, fmt.Sprintf(" ↑ %d more lines ", hidden), t.muted, false)
	}
	end := tx
	for i, row := range rows {
		end = cv.text(tx, y+1+i, row, t.fg, false)
	}
	if int(n/530)%2 == 0 {
		cv.set(end, y+len(rows), '▏', t.fg, false)
	}
}

func drawStatus(cv *canvas, t theme, m model, x, y, w int, enter float64) {
	muted := mix(t.bg, t.muted, enter)
	fx := cv.grad(x+1, y, "◆ ", t, 0.1, 8, false)
	fx = cv.text(fx, y, clip(modelName(m.route), w/2-24), mix(t.bg, t.fg, enter), false)
	if m.route != "" && m.waited > 1500*time.Millisecond {
		fx = cv.text(fx, y, fmt.Sprintf(" · queued %.0fs", m.waited.Seconds()), mix(t.bg, t.muted, enter*0.8), false)
	}
	fx = cv.text(fx, y, "  ·  ", mix(t.bg, t.faint, 1.5*enter), false)
	fx = cv.text(fx, y, "free", mix(t.bg, t.green, enter), false)

	used := float64(m.tokens) / float64(max(1, m.window))
	meter := 12
	mx := max(x+w/2-2, fx+3)
	for i := 0; i < meter; i++ {
		col, ch := mix(t.bg, t.faint, enter*1.6), '▱'
		if float64(i)/float64(meter) < used {
			col, ch = mix(t.bg, t.at(float64(i)/float64(meter)*0.5), enter), '▰'
		}
		cv.set(mx+i, y, ch, col, false)
	}
	cv.text(mx+meter+1, y, fmt.Sprintf("%d%%", int(math.Round(used*100))), muted, false)

	keys := []seg{{text: "ctrl+t", fg: t.fg}, {text: " theme   ", fg: t.muted}, {text: "ctrl+c", fg: t.fg}, {text: " quit", fg: t.muted}}
	if m.busy {
		keys[0].text, keys[1].text = "esc", " stop   "
	}
	total := 0
	for _, k := range keys {
		total += len(k.text)
	}
	kx := x + w - total - 1
	for _, k := range keys {
		kx = cv.text(kx, y, k.text, mix(t.bg, k.fg, enter), false)
	}
}

func modelLines(t theme, e *entry, width int) []line {
	if e.text != "" {
		return []line{{indent: 2, segs: []seg{{text: "✕ ", fg: t.red}, {text: e.text, fg: t.red}}}}
	}
	out := []line{{indent: 2, segs: []seg{{text: "free models", fg: t.fg, bold: true}, {text: "  in the order prism tries them", fg: t.muted}}}}
	nameW := 10
	for _, r := range e.rows {
		nameW = max(nameW, len([]rune(modelName(r.Route))))
	}
	for _, r := range e.rows {
		dot, state, col := "● ", "ready", t.green
		if !r.Ready {
			dot, state, col = "◌ ", fmt.Sprintf("resting %ds", r.RetryIn), t.muted
		}
		out = append(out, line{indent: 2, segs: []seg{
			{text: dot, fg: col},
			{text: pad(clip(modelName(r.Route), width-30), nameW+2), fg: t.fg},
			{text: pad(provider(r.Route), 12), fg: t.muted},
			{text: state, fg: col},
		}})
	}
	out = append(out, line{}, line{indent: 2, segs: []seg{{text: "add more free models: ", fg: t.muted}, {text: "prism setup", fg: t.at(0.6)}}})
	return out
}

func drawSuggestions(cv *canvas, t theme, m model, x, y, w int) {
	found := matching(string(m.input))
	for i, c := range found {
		row := y - len(found) + i
		cv.fill(x+1, row, w-2, 1, t.panel)
		for k := x + 1; k < x+w-1; k++ {
			cv.set(k, row, ' ', t.fg, false)
		}
		nx := cv.text(x+3, row, pad(c.name, 10), t.at(0.5+float64(i)*0.08), true)
		cv.text(nx, row, c.help, t.muted, false)
		if i == 0 {
			cv.text(x+w-12, row, "tab ↹", t.muted, false)
		}
	}
}
