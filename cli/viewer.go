package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// viewLimit is the largest file the viewer (terminal or browser) shows or saves.
const viewLimit = 1 << 20

var errOutside = errors.New(outsideMsg)

// projectPath resolves p inside dir and returns its absolute path and its
// slash-separated path relative to the project. It rejects any ".." part and
// anything that lands outside the project once symlinks are resolved. Unlike
// the tools' outside(), it always applies, even with --any-path.
func projectPath(dir, p string) (abs, rel string, err error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", "", errors.New("no path given")
	}
	for _, part := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", "", errOutside
		}
	}
	abs = resolve(dir, p)
	r, err := filepath.Rel(resolvedDir(dir), resolveDeepest(abs))
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return "", "", errOutside
	}
	return abs, filepath.ToSlash(r), nil
}

// fileContent is a project file ready to show: its text, or a one-line note
// when it cannot be shown (binary or too large).
type fileContent struct {
	rel  string
	text string
	note string
	size int64
}

// readProjectFile reads p for viewing, applying the path guard and size limit.
func readProjectFile(dir, p string) (fileContent, error) {
	abs, rel, err := projectPath(dir, p)
	if err != nil {
		return fileContent{}, err
	}
	fc := fileContent{rel: rel}
	st, err := os.Stat(abs)
	if err != nil {
		return fc, pathErr(rel, err)
	}
	if st.IsDir() {
		return fc, fmt.Errorf("%s is a folder", rel)
	}
	fc.size = st.Size()
	if st.Size() > viewLimit {
		fc.note = fmt.Sprintf("too large to show · %s (the limit is 1 MB)", sizeText(st.Size()))
		return fc, nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return fc, pathErr(rel, err)
	}
	fc.size = int64(len(data))
	switch {
	case len(data) > viewLimit:
		fc.note = fmt.Sprintf("too large to show · %s (the limit is 1 MB)", sizeText(fc.size))
	case isBinary(data):
		fc.note = fmt.Sprintf("binary file · %s · not shown", sizeText(fc.size))
	default:
		fc.text = string(data)
	}
	return fc, nil
}

func pathErr(rel string, err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("%s: %w", rel, pe.Err)
	}
	return err
}

func isBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 || !utf8.Valid(data)
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// viewLines splits text into display lines with control characters made
// harmless (they would otherwise reach the terminal raw).
func viewLines(text string) []string {
	lines := splitLines(strings.ReplaceAll(text, "\r\n", "\n"))
	for i, l := range lines {
		lines[i] = strings.Map(func(r rune) rune {
			if r == '\t' {
				return r
			}
			if r < 0x20 || r == 0x7f {
				return '·'
			}
			return r
		}, l)
	}
	return lines
}

// fileViewer is the read-only viewer: the file's lines and the first one shown.
type fileViewer struct {
	path  string
	lines []string
	note  string
	top   int
}

func newViewer(fc fileContent) *fileViewer {
	v := &fileViewer{path: fc.rel, note: fc.note}
	if fc.note == "" {
		v.lines = viewLines(fc.text)
		if len(v.lines) == 0 {
			v.note = "empty file"
		}
	}
	return v
}

// scroll moves the viewer by delta lines, keeping a full page in view.
func (v *fileViewer) scroll(delta, page int) {
	v.top = clamp(v.top+delta, 0, max(0, len(v.lines)-max(1, page)))
}

// filesState is the /files overlay: the project tree with expandable folders,
// narrowed by what the user types, and an optional viewer on top.
type filesState struct {
	open     bool
	loading  bool
	nodes    []treeNode
	err      string
	expanded map[string]bool
	sel, off int // highlighted row and first row shown
	filter   []rune
	view     *fileViewer
}

type filesMsg struct {
	nodes []treeNode
	err   error
}

// fileRow is one visible line of the browser.
type fileRow struct {
	path  string
	dir   bool
	depth int
	open  bool
	git   string
}

// rows is the visible tree (folders first, expanded ones walked) or, while
// filtering, every file whose path contains the filter.
func (f filesState) rows() []fileRow {
	var out []fileRow
	if len(f.filter) > 0 {
		q := strings.ToLower(string(f.filter))
		for _, n := range f.nodes {
			if !n.Dir && strings.Contains(strings.ToLower(n.Path), q) {
				out = append(out, fileRow{path: n.Path, git: n.Git})
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
		return out
	}
	kids := map[string][]treeNode{}
	for _, n := range f.nodes {
		p := parentOf(n.Path)
		kids[p] = append(kids[p], n)
	}
	for _, l := range kids {
		sort.Slice(l, func(i, j int) bool {
			if l[i].Dir != l[j].Dir {
				return l[i].Dir
			}
			return l[i].Path < l[j].Path
		})
	}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		for _, n := range kids[dir] {
			open := n.Dir && f.expanded[n.Path]
			out = append(out, fileRow{path: n.Path, dir: n.Dir, depth: depth, open: open, git: n.Git})
			if open {
				walk(n.Path, depth+1)
			}
		}
	}
	walk("", 0)
	return out
}

func loadFiles(a *Agent) tea.Cmd {
	return func() tea.Msg {
		if a.mapReady != nil {
			<-a.mapReady
		}
		if a.tree == nil {
			return filesMsg{err: errors.New("the project map is not available")}
		}
		nodes, _ := a.tree.snapshot()["nodes"].([]treeNode)
		return filesMsg{nodes: nodes}
	}
}

// openFiles shows the browser and (re)loads the tree, keeping the filter.
func (m *model) openFiles() tea.Cmd {
	if !m.fb.open {
		m.fb = filesState{open: true, expanded: map[string]bool{}}
	}
	m.fb.view = nil
	m.fb.loading = true
	return loadFiles(m.agent)
}

// filesLoaded fills the browser; a late reply after it closed is dropped.
func (m *model) filesLoaded(msg filesMsg) {
	if !m.fb.open {
		return
	}
	m.fb.loading = false
	m.fb.nodes, m.fb.err = msg.nodes, errText(msg.err)
	m.fb.sel = clamp(m.fb.sel, 0, max(0, len(m.fb.rows())-1))
}

// openFile runs /open <path>: the viewer opens straight away and the browser
// loads behind it, so esc lands there.
func (m *model) openFile(p string) tea.Cmd {
	fc, err := readProjectFile(m.agent.dir, p)
	if err != nil {
		m.entries = append(m.entries, &entry{kind: "note", text: "✕ " + err.Error()})
		m.scroll = 0
		return nil
	}
	var cmd tea.Cmd
	if !m.fb.open {
		cmd = m.openFiles()
	}
	m.fb.view = newViewer(fc)
	return cmd
}

// viewerPage is about how many file lines the viewer shows at this height.
func (m model) viewerPage() int {
	return max(3, m.h-m.boxHeight()-8)
}

// browserShown is how many tree rows fit above the input box.
func browserShown(n, avail int) int {
	return min(n, max(1, min(16, avail-3)))
}

// filesLayout mirrors drawFiles: every line drawn, the header lines, the
// rows shown (tree rows or file lines) and the first one shown.
func (m model) filesLayout() (lines, head, shown, off int) {
	_, y, _, top := m.overlayArea()
	avail := y - top
	if v := m.fb.view; v != nil {
		if v.note != "" {
			return 2, 1, 0, 0
		}
		n := len(v.lines)
		shown = min(n, max(1, avail-1))
		return 1 + shown, 1, shown, clamp(v.top, 0, max(0, n-shown))
	}
	rows := m.fb.rows()
	if len(rows) == 0 {
		return 3, 2, 0, 0
	}
	shown = browserShown(len(rows), avail)
	off = keepInView(m.fb.off, clamp(m.fb.sel, 0, len(rows)-1), shown, len(rows))
	lines = 2 + shown
	if len(rows) > shown {
		lines++
	}
	return lines, 2, shown, off
}

// filesHit maps a screen cell to a browser row (-1 for none, and always -1
// in the viewer) and says whether the cell is inside the overlay.
func (m model) filesHit(mx, my int) (idx int, inside bool) {
	x, y, w, top := m.overlayArea()
	lines, head, shown, off := m.filesLayout()
	if mx < x+1 || mx > x+w-2 || my < max(top, y-lines) || my >= y {
		return -1, false
	}
	if r := my - (y - lines + head); m.fb.view == nil && r >= 0 && r < shown {
		return off + r, true
	}
	return -1, true
}

// filesMouse: the wheel scrolls the tree or the file, the pointer highlights
// a tree row, a click opens a file or folds a folder, and a click outside
// steps back (viewer to tree, tree to closed).
func (m model) filesMouse(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		d := wheelDelta(msg)
		if d == 0 {
			break
		}
		if v := m.fb.view; v != nil {
			_, _, shown, _ := m.filesLayout()
			v.scroll(d, shown)
			break
		}
		_, _, shown, off := m.filesLayout()
		if shown == 0 {
			break
		}
		m.fb.off = clamp(off+d, 0, max(0, len(m.fb.rows())-shown))
		m.fb.sel = clamp(m.fb.sel, m.fb.off, m.fb.off+shown-1)
	case tea.MouseMotionMsg:
		mo := tea.Mouse(msg)
		if idx, _ := m.filesHit(mo.X, mo.Y); idx >= 0 {
			m.fb.sel = idx
		}
	case tea.MouseClickMsg:
		mo := tea.Mouse(msg)
		if mo.Button != tea.MouseLeft {
			break
		}
		idx, inside := m.filesHit(mo.X, mo.Y)
		switch {
		case !inside && m.fb.view != nil:
			m.fb.view = nil
		case !inside:
			m.fb = filesState{}
		case idx >= 0:
			m.fb.sel = idx
			return m.filesKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
	}
	return m, nil
}

func (m model) filesKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if v := m.fb.view; v != nil {
		page := m.viewerPage()
		switch msg.String() {
		case "esc":
			m.fb.view = nil
		case "up":
			v.scroll(-1, page)
		case "down":
			v.scroll(1, page)
		case "pgup":
			v.scroll(-page, page)
		case "pgdown", "space":
			v.scroll(page, page)
		case "home":
			v.top = 0
		case "end":
			v.scroll(len(v.lines), page)
		}
		return m, nil
	}
	rows := m.fb.rows()
	switch msg.String() {
	case "esc":
		m.fb = filesState{}
	case "up":
		if len(rows) > 0 {
			m.fb.sel = (m.fb.sel - 1 + len(rows)) % len(rows)
		}
	case "down":
		if len(rows) > 0 {
			m.fb.sel = (m.fb.sel + 1) % len(rows)
		}
	case "enter", "right", "left":
		if len(rows) == 0 {
			return m, nil
		}
		r := rows[clamp(m.fb.sel, 0, len(rows)-1)]
		if r.dir {
			want := !r.open
			if msg.String() != "enter" {
				want = msg.String() == "right"
			}
			if m.fb.expanded == nil {
				m.fb.expanded = map[string]bool{}
			}
			m.fb.expanded[r.path] = want
			return m, nil
		}
		if msg.String() != "enter" {
			return m, nil
		}
		fc, err := readProjectFile(m.agent.dir, r.path)
		v := newViewer(fc)
		if err != nil {
			v = &fileViewer{path: r.path, note: "✕ " + err.Error()}
		}
		m.fb.view = v
	case "backspace":
		if len(m.fb.filter) > 0 {
			m.fb.filter = m.fb.filter[:len(m.fb.filter)-1]
			m.fb.sel = 0
		}
	case "ctrl+u":
		m.fb.filter, m.fb.sel = nil, 0
	default:
		if msg.Text != "" && !strings.ContainsAny(msg.Text, "\n\r\t") {
			m.fb.filter = append(m.fb.filter, []rune(msg.Text)...)
			m.fb.sel = 0
		}
	}
	if m.fb.open && m.fb.view == nil {
		_, _, _, m.fb.off = m.filesLayout() // remember the scroll so rows stay put
	}
	return m, nil
}

// drawFiles draws the browser or the viewer just above the input box at y,
// in the picker's style, never above top.
func drawFiles(cv *canvas, t theme, m model, x, y, w, top int) {
	f := m.fb
	var all []pickLine
	if v := f.view; v != nil {
		all = viewerLines(t, v, w-6, y-top)
	} else {
		all = browserLines(t, f, w-6, y-top)
	}
	drawPickLines(cv, t, all, x, y, w, top)
}

func browserLines(t theme, f filesState, inner, avail int) []pickLine {
	accent := t.at(0.5)
	all := []pickLine{
		{fill: t.panel, segs: []seg{{text: "files", fg: t.fg, bold: true}, {text: "  ↑↓ move · enter open · esc close", fg: t.muted}}},
	}
	filter := []seg{{text: "› ", fg: accent, bold: true}}
	if len(f.filter) == 0 {
		filter = append(filter, seg{text: "▏type to filter by path", fg: t.muted})
	} else {
		filter = append(filter, seg{text: string(f.filter), fg: t.fg}, seg{text: "▏", fg: t.fg})
	}
	all = append(all, pickLine{fill: t.faint, segs: filter})
	rows := f.rows()
	switch {
	case f.loading && len(f.nodes) == 0:
		return append(all, pickLine{fill: t.panel, segs: []seg{{text: "loading…", fg: t.muted}}})
	case f.err != "" && len(f.nodes) == 0:
		return append(all, pickLine{fill: t.panel, segs: []seg{{text: "✕ ", fg: t.red}, {text: f.err, fg: t.red}}})
	case len(rows) == 0 && len(f.filter) > 0:
		return append(all, pickLine{fill: t.panel, segs: []seg{{text: fmt.Sprintf("no file matches %q", string(f.filter)), fg: t.muted}}})
	case len(rows) == 0:
		return append(all, pickLine{fill: t.panel, segs: []seg{{text: "the folder is empty", fg: t.muted}}})
	}
	shown := browserShown(len(rows), avail)
	sel := clamp(f.sel, 0, len(rows)-1)
	off := keepInView(f.off, sel, shown, len(rows))
	for i, r := range rows[off : off+shown] {
		hi := off+i == sel
		fill, col := t.panel, t.fg
		if hi {
			fill, col = mix(t.panel, accent, 0.14), accent
		}
		name := r.path
		if len(f.filter) == 0 {
			name = path.Base(r.path)
		}
		icon, iconCol := "  ", t.muted
		if r.dir {
			name += "/"
			icon, iconCol = "▸ ", accent
			if r.open {
				icon = "▾ "
			}
		}
		indent := strings.Repeat("  ", min(r.depth, max(0, (inner-12)/4)))
		segs := []seg{{text: indent}, {text: icon, fg: iconCol}, {text: name, fg: col, bold: hi || r.dir}}
		if r.git != "" {
			segs = append(segs, seg{text: "  " + r.git, fg: t.green})
		}
		all = append(all, pickLine{fill: fill, segs: segs})
	}
	if hidden := len(rows) - shown; hidden > 0 {
		all = append(all, pickLine{fill: t.panel, segs: []seg{{text: fmt.Sprintf("%d more · scroll or type to narrow", hidden), fg: t.muted}}})
	}
	return all
}

func viewerLines(t theme, v *fileViewer, inner, avail int) []pickLine {
	accent := t.at(0.5)
	n := len(v.lines)
	shown := min(n, max(1, avail-1))
	off := clamp(v.top, 0, max(0, n-shown))
	where := ""
	if n > 0 {
		where = fmt.Sprintf("  %d–%d of %d", off+1, off+shown, n)
	}
	all := []pickLine{
		{fill: t.panel, segs: []seg{{text: v.path, fg: accent, bold: true}, {text: where, fg: t.muted}, {text: "  ↑↓ pgup pgdn home end · esc back", fg: t.muted}}},
	}
	if v.note != "" {
		return append(all, pickLine{fill: t.faint, segs: []seg{{text: v.note, fg: t.muted}}})
	}
	gw := len(fmt.Sprint(n))
	for i := off; i < off+shown; i++ {
		text := clip(v.lines[i], max(1, inner-gw-2))
		all = append(all, pickLine{fill: t.faint, segs: []seg{
			{text: fmt.Sprintf("%*d  ", gw, i+1), fg: t.muted},
			{text: text, fg: t.fg},
		}})
	}
	return all
}
