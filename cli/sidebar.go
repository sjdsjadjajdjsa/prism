package main

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The sidebar is the project tree on the left, like Obsidian's file list.
// It stays open beside the chat and the editor and follows changes live.
type sidebarState struct {
	shown, focus bool
	nodes        []treeNode
	version      int
	loaded       bool
	err          string
	expanded     map[string]bool
	sel, off     int
	filter       []rune
	filtering    bool
	prompt       *sidePrompt
	note         string
	at           time.Time // last refresh while Prism works
	watching     bool      // a wait for the next tree change is running
	reveal       string    // highlight this path once it appears
	following    string    // the editor's file, last time the tree moved to it
}

// sidePrompt is a question the sidebar is asking: a name for a new file,
// folder or rename, or a yes/no before deleting.
type sidePrompt struct {
	op, target string
	text       []rune
}

type sideTreeMsg struct {
	nodes   []treeNode
	version int
	err     error
	waited  bool
}

func (m model) sideOn() bool { return m.side.shown && m.w >= minSideW+minChatW+1 }

const minSideW = 22

// sideW is the sidebar's width, its divider included.
func (m model) sideW() int {
	if !m.sideOn() {
		return 0
	}
	return clamp(m.w/5, minSideW, 34) + 1
}

func (m model) chatX() int { return m.sideW() }

func sideTree(a *Agent, version int, wait bool) tea.Cmd {
	return func() tea.Msg {
		if a.mapReady != nil {
			<-a.mapReady
		}
		if a.tree == nil {
			return sideTreeMsg{err: fmt.Errorf("the project map is not available")}
		}
		a.tree.startWatching()
		if wait {
			a.tree.wait(context.Background(), version, 25*time.Second)
		}
		snap := a.tree.snapshot()
		nodes, _ := snap["nodes"].([]treeNode)
		v, _ := snap["version"].(int)
		return sideTreeMsg{nodes: nodes, version: v, waited: wait}
	}
}

func (m *model) toggleSidebar() tea.Cmd {
	if m.side.shown {
		m.side.shown, m.side.focus = false, false
		saveSetting("sidebar", "off")
		return nil
	}
	return m.showSidebar()
}

func (m *model) showSidebar() tea.Cmd {
	if m.w > 0 && m.w < minSideW+minChatW+1 {
		m.note("the window is too narrow for the file tree · make it wider")
		return nil
	}
	if m.side.expanded == nil {
		m.side.expanded = map[string]bool{}
	}
	first := !m.side.shown
	m.side.shown, m.side.focus = true, true
	m.es.focus = false
	saveSetting("sidebar", "on")
	if first {
		return sideTree(m.agent, 0, false)
	}
	return nil
}

func (m model) sideMsg(msg sideTreeMsg) (model, tea.Cmd) {
	if msg.waited {
		m.side.watching = false
	}
	if msg.err != nil {
		m.side.err = msg.err.Error()
		return m, nil
	}
	if !m.side.loaded {
		for _, n := range msg.nodes {
			if n.Git != "" && !n.Dir {
				for d := path.Dir(n.Path); d != "."; d = path.Dir(d) {
					m.side.expanded[d] = true
				}
			}
		}
	}
	m.side.nodes, m.side.version, m.side.loaded, m.side.err = msg.nodes, msg.version, true, ""
	if !m.side.shown || m.side.watching {
		return m, nil
	}
	m.side.watching = true
	return m, sideTree(m.agent, m.side.version, true)
}

func (m model) sideRows() []fileRow {
	return filesState{nodes: m.side.nodes, expanded: m.side.expanded, filter: m.side.filter}.rows()
}

func (m model) sideNode(p string) *treeNode {
	for i := range m.side.nodes {
		if m.side.nodes[i].Path == p {
			return &m.side.nodes[i]
		}
	}
	return nil
}

// sideList is where the rows go: the first row, how many fit, and the links
// box under them.
func (m model) sideList() (top, shown, links int) {
	top = 2
	if m.side.prompt != nil || m.side.filtering || len(m.side.filter) > 0 {
		top = 3
	}
	links = 0
	if r, ok := m.sideSelected(); ok && !r.dir {
		if n := m.sideNode(r.path); n != nil && len(n.Imports)+len(n.ImportedBy) > 0 {
			links = 1 + min(3, len(n.Imports)) + min(3, len(n.ImportedBy))
		}
	}
	shown = max(1, m.h-top-2-links)
	return top, shown, links
}

func (m model) sideSelected() (fileRow, bool) {
	rows := m.sideRows()
	if len(rows) == 0 {
		return fileRow{}, false
	}
	return rows[clamp(m.side.sel, 0, len(rows)-1)], true
}

func (m *model) sideScroll() {
	_, shown, _ := m.sideList()
	m.side.off = keepInView(m.side.off, m.side.sel, shown, len(m.sideRows()))
}

// sideOpen opens the highlighted row: a folder opens or closes, text goes to
// the editor, pictures and videos to the viewer.
func (m *model) sideOpen(r fileRow) tea.Cmd {
	if r.dir {
		m.side.expanded[r.path] = !m.side.expanded[r.path]
		return nil
	}
	if mediaKind(r.path) != "" {
		m.fb = filesState{open: true, only: true, expanded: map[string]bool{}, view: newMediaViewer(m.agent.dir, r.path)}
		m.side.focus = false
		return nil
	}
	fc, err := readProjectFile(m.agent.dir, r.path)
	if err == nil && fc.note == "" && m.editorFits() {
		m.side.focus = false
		return m.showEditor(r.path)
	}
	v := newViewer(fc)
	if err != nil {
		v = &fileViewer{path: r.path, note: "✕ " + err.Error()}
	}
	m.fb = filesState{open: true, only: true, expanded: map[string]bool{}, view: v}
	m.side.focus = false
	return nil
}

// sideFolder is the folder a new file or folder goes into: the highlighted
// folder, or the folder of the highlighted file.
func (m model) sideFolder() string {
	r, ok := m.sideSelected()
	switch {
	case !ok:
		return ""
	case r.dir:
		return r.path
	}
	if d := path.Dir(r.path); d != "." {
		return d
	}
	return ""
}

func (m model) sideKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	k := msg.String()
	if p := m.side.prompt; p != nil {
		return m.sidePromptKey(msg)
	}
	rows := m.sideRows()
	if m.side.filtering {
		switch k {
		case "esc":
			m.side.filtering, m.side.filter, m.side.sel = false, nil, 0
		case "enter", "down":
			m.side.filtering = false
		case "backspace":
			if len(m.side.filter) > 0 {
				m.side.filter = m.side.filter[:len(m.side.filter)-1]
			} else {
				m.side.filtering = false
			}
			m.side.sel = 0
		default:
			if msg.Text != "" && !strings.ContainsAny(msg.Text, "\n\r\t") {
				m.side.filter = append(m.side.filter, []rune(msg.Text)...)
				m.side.sel = 0
			}
		}
		m.sideScroll()
		return m, nil
	}
	_, shown, _ := m.sideList()
	move := func(d int) {
		if len(rows) > 0 {
			m.side.sel = clamp(m.side.sel+d, 0, len(rows)-1)
		}
	}
	r, ok := m.sideSelected()
	m.side.note = ""
	switch k {
	case "up", "k":
		move(-1)
	case "down", "j":
		move(1)
	case "pgup":
		move(-shown)
	case "pgdown":
		move(shown)
	case "home", "g":
		m.side.sel = 0
	case "end", "G":
		move(len(rows))
	case "enter", "l", "right":
		if ok {
			if r.dir && k == "right" && m.side.expanded[r.path] {
				break
			}
			cmd := m.sideOpen(r)
			m.sideScroll()
			return m, cmd
		}
	case "left", "h":
		if !ok {
			break
		}
		if r.dir && m.side.expanded[r.path] {
			m.side.expanded[r.path] = false
		} else if d := path.Dir(r.path); d != "." {
			m.side.expanded[d] = false
			for i, row := range m.sideRows() {
				if row.path == d {
					m.side.sel = i
				}
			}
		}
	case "/":
		m.side.filtering = true
	case "esc":
		if len(m.side.filter) > 0 {
			m.side.filter, m.side.sel = nil, 0
		} else {
			m.side.focus = false
		}
	case "tab":
		m.side.focus = false
	case "a":
		m.side.prompt = &sidePrompt{op: "file", target: m.sideFolder()}
	case "A":
		m.side.prompt = &sidePrompt{op: "folder", target: m.sideFolder()}
	case "r":
		if ok {
			m.side.prompt = &sidePrompt{op: "rename", target: r.path, text: []rune(path.Base(r.path))}
		}
	case "d", "delete":
		if ok {
			m.side.prompt = &sidePrompt{op: "delete", target: r.path}
		}
	case "@", "m":
		if ok {
			m.mention(r.path)
		}
	case "ctrl+e":
		m.side.focus = false
		return m, m.showEditor("")
	}
	m.sideScroll()
	return m, nil
}

// mention puts a file's path into the chat box so the AI knows which file
// you mean.
func (m *model) mention(p string) {
	if len(m.input) > 0 && m.input[len(m.input)-1] != ' ' && m.input[len(m.input)-1] != '\n' {
		m.input = append(m.input, ' ')
	}
	m.input = append(m.input, []rune("`"+p+"` ")...)
	m.side.focus, m.es.focus = false, false
}

func (m model) sidePromptKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	p := m.side.prompt
	k := msg.String()
	if p.op == "delete" {
		if k == "y" || k == "Y" {
			if _, err := fileOp(m.agent, "delete", p.target, ""); err != nil {
				m.side.note = "✕ " + err.Error()
			} else {
				m.side.note = "deleted " + p.target
			}
			m.side.sel = max(0, m.side.sel-1)
			m.side.prompt = nil
			return m, sideTree(m.agent, 0, false)
		}
		m.side.prompt = nil
		return m, nil
	}
	switch k {
	case "esc":
		m.side.prompt = nil
	case "enter":
		name := strings.TrimSpace(string(p.text))
		if name == "" {
			m.side.prompt = nil
			return m, nil
		}
		var done string
		var err error
		switch p.op {
		case "rename":
			done, err = fileOp(m.agent, "rename", p.target, path.Join(path.Dir(p.target), name))
		default:
			done, err = fileOp(m.agent, p.op, path.Join(p.target, name), "")
		}
		m.side.prompt = nil
		if err != nil {
			m.side.note = "✕ " + err.Error()
			return m, nil
		}
		for d := path.Dir(done); d != "."; d = path.Dir(d) {
			m.side.expanded[d] = true
		}
		m.side.reveal = done
		return m, sideTree(m.agent, 0, false)
	case "backspace":
		if len(p.text) > 0 {
			p.text = p.text[:len(p.text)-1]
		}
	case "ctrl+u":
		p.text = nil
	default:
		if msg.Text != "" && !strings.ContainsAny(msg.Text, "\n\r\t") {
			p.text = append(p.text, []rune(msg.Text)...)
		}
	}
	return m, nil
}

func (m model) sideMouse(msg tea.Msg) (model, tea.Cmd) {
	mouse := msg.(interface{ Mouse() tea.Mouse }).Mouse()
	top, shown, _ := m.sideList()
	rows := m.sideRows()
	switch msg.(type) {
	case tea.MouseWheelMsg:
		if d := wheelDelta(msg.(tea.MouseWheelMsg)); d != 0 {
			m.side.off = clamp(m.side.off+d, 0, max(0, len(rows)-shown))
			m.side.sel = clamp(m.side.sel, m.side.off, min(len(rows)-1, m.side.off+shown-1))
		}
	case tea.MouseClickMsg:
		m.side.focus, m.es.focus = true, false
		if mouse.Button != tea.MouseLeft {
			break
		}
		if i := m.side.off + mouse.Y - top; mouse.Y >= top && mouse.Y < top+shown && i < len(rows) {
			m.side.sel = i
			cmd := m.sideOpen(rows[i])
			if rows[i].dir {
				m.side.focus = true
			}
			return m, cmd
		}
	}
	return m, nil
}

// revealInSide opens the folders above p and highlights it, so the tree
// follows the file the editor has open.
func (m *model) revealInSide(p string) {
	if p == "" || strings.HasPrefix(p, "..") || strings.HasPrefix(p, "/") {
		return
	}
	if m.side.expanded == nil {
		m.side.expanded = map[string]bool{}
	}
	for d := path.Dir(p); d != "."; d = path.Dir(d) {
		m.side.expanded[d] = true
	}
	m.side.filter, m.side.reveal = nil, p
	m.sideReveal()
	if m.side.reveal == "" {
		_, shown, _ := m.sideList()
		m.side.off = max(0, m.side.sel-shown/2)
	}
}

// sideReveal moves the highlight to a path once it shows up in the tree.
func (m *model) sideReveal() {
	if m.side.reveal == "" {
		return
	}
	for i, r := range m.sideRows() {
		if r.path == m.side.reveal {
			_, shown, _ := m.sideList()
			m.side.sel, m.side.reveal = i, ""
			if i < m.side.off || i >= m.side.off+shown {
				m.side.off = max(0, i-shown/2)
			}
			return
		}
	}
}

func drawSidebar(cv *canvas, t theme, m model) {
	w := m.sideW() - 1
	h := m.h
	accent := t.at(0.5)
	cv.fill(0, 0, w, h, t.panel)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cv.set(x, y, ' ', t.fg, false)
		}
	}
	div := t.faint
	if m.side.focus {
		div = accent
	}
	for y := 0; y < h; y++ {
		cv.set(w, y, '│', div, false)
	}
	x := cv.grad(1, 0, "files", t, 0.1, 10, true)
	cv.text(x+1, 0, clip(m.project, max(0, w-x-2)), t.muted, false)

	top, shown, links := m.sideList()
	switch p := m.side.prompt; {
	case p != nil && p.op == "delete":
		cv.text(1, 2, clip("delete "+path.Base(p.target)+"? y/n", w-2), t.red, true)
	case p != nil:
		label := map[string]string{"file": "new file: ", "folder": "new folder: ", "rename": "rename: "}[p.op]
		cv.fill(0, 2, w, 1, t.faint)
		x := cv.text(1, 2, label, t.muted, false)
		cv.text(x, 2, clipLeft(string(p.text), w-x-2)+"▏", t.fg, false)
	case m.side.filtering || len(m.side.filter) > 0:
		cv.fill(0, 2, w, 1, t.faint)
		x := cv.text(1, 2, "/ ", accent, true)
		caret := ""
		if m.side.filtering {
			caret = "▏"
		}
		cv.text(x, 2, clipLeft(string(m.side.filter), w-x-2)+caret, t.fg, false)
	}

	rows := m.sideRows()
	current := ""
	if m.es.ed != nil {
		current = m.es.ed.file()
	}
	switch {
	case m.side.err != "":
		cv.text(1, top, clip("✕ "+m.side.err, w-2), t.red, false)
	case !m.side.loaded:
		cv.text(1, top, "loading…", t.muted, false)
	case len(rows) == 0 && len(m.side.filter) > 0:
		cv.text(1, top, "nothing matches", t.muted, false)
	case len(rows) == 0:
		cv.text(1, top, "the folder is empty", t.muted, false)
	}
	sel := clamp(m.side.sel, 0, max(0, len(rows)-1))
	off := keepInView(m.side.off, sel, shown, len(rows))
	for i := 0; i < shown && off+i < len(rows); i++ {
		r := rows[off+i]
		y := top + i
		hi := off+i == sel
		if hi {
			fill := mix(t.panel, accent, 0.08)
			if m.side.focus {
				fill = mix(t.panel, accent, 0.2)
			}
			cv.fill(0, y, w, 1, fill)
		}
		depth := r.depth
		if len(m.side.filter) > 0 {
			depth = 0
		}
		x := 1 + min(depth, max(0, (w-10)/3))*2
		name := path.Base(r.path)
		if len(m.side.filter) > 0 {
			name = r.path
		}
		col, bold := t.fg, false
		switch {
		case r.dir:
			icon := "▸ "
			if r.open {
				icon = "▾ "
			}
			x = cv.text(x, y, icon, accent, false)
			bold = true
		case mediaKind(r.path) == "image":
			x = cv.text(x, y, "▣ ", t.at(0.35), false)
		case mediaKind(r.path) == "video":
			x = cv.text(x, y, "▶ ", t.at(0.85), false)
		default:
			x = cv.text(x, y, "  ", t.fg, false)
		}
		if r.path == current {
			col, bold = accent, true
		}
		marks := sideMarks(t, r)
		room := w - x - 1 - segsWidth(marks)
		x = cv.text(x, y, clip(name, max(1, room)), col, bold || hi)
		mx := w - 1 - segsWidth(marks)
		for _, s := range marks {
			mx = cv.text(mx, y, s.text, s.fg, s.bold)
		}
	}
	if links > 0 {
		if r, ok := m.sideSelected(); ok {
			n := m.sideNode(r.path)
			y := m.h - 2 - links
			cv.text(1, y, "links", t.muted, true)
			y++
			for _, p := range n.Imports[:min(3, len(n.Imports))] {
				cv.text(1, y, clip("→ "+p, w-2), t.muted, false)
				y++
			}
			for _, p := range n.ImportedBy[:min(3, len(n.ImportedBy))] {
				cv.text(1, y, clip("← "+p, w-2), t.muted, false)
				y++
			}
		}
	}
	hint := "a new · r rename · d delete · @ chat · / find"
	if !m.side.focus {
		hint = "ctrl+b files · click to open"
	}
	if m.side.note != "" {
		cv.text(1, m.h-1, clip(m.side.note, w-2), t.muted, false)
	} else {
		cv.text(1, m.h-1, clip(hint, w-2), mix(t.panel, t.muted, 0.8), false)
	}
}

// sideMarks are the little signs after a name: git status and what Prism did.
func sideMarks(t theme, r fileRow) []seg {
	var out []seg
	switch r.git {
	case "M":
		out = append(out, seg{text: "M", fg: t.at(0.15), bold: true})
	case "A", "?":
		out = append(out, seg{text: "+", fg: t.green, bold: true})
	case "D":
		out = append(out, seg{text: "−", fg: t.red, bold: true})
	}
	switch r.agent {
	case "write", "edit":
		out = append(out, seg{text: " ●", fg: t.at(0.6)})
	case "read":
		out = append(out, seg{text: " ◦", fg: t.muted})
	}
	return out
}

func clipLeft(s string, n int) string {
	r := []rune(s)
	if n <= 0 {
		return ""
	}
	if len(r) > n {
		return "…" + string(r[len(r)-n+1:])
	}
	return s
}

func segsWidth(segs []seg) int {
	n := 0
	for _, s := range segs {
		n += len([]rune(s.text))
	}
	return n
}

// shiftMouse moves a mouse event dx cells sideways, so the chat sees its own
// coordinates when the file tree sits left of it.
func shiftMouse(msg tea.Msg, dx int) tea.Msg {
	if dx == 0 {
		return msg
	}
	switch e := msg.(type) {
	case tea.MouseClickMsg:
		e.X += dx
		return e
	case tea.MouseReleaseMsg:
		e.X += dx
		return e
	case tea.MouseMotionMsg:
		e.X += dx
		return e
	case tea.MouseWheelMsg:
		e.X += dx
		return e
	}
	return msg
}
