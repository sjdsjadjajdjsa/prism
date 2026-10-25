package main

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-runewidth"
)

// pane is the editor beside the chat: Prism's own simple editor or Neovim.
type pane interface {
	open(path string)
	resize(w, h int)
	theme(t theme)
	key(msg tea.KeyPressMsg) tea.Cmd
	mouse(button, action, mods string, row, col int)
	paste(text string)
	draw(cv *canvas, x, y int) (int, int, tea.CursorShape, bool)
	unsaved() bool
	close()
	file() string
	save()
	refresh()
	saveKey() string
}

const tabWidth = 4

// simpleEditor works like a notes app: what you type goes in, and the keys
// are the ones every other program uses.
type simpleEditor struct {
	dir     string
	bufs    []*buffer
	cur     *buffer
	w, h    int
	t       theme
	clip    string
	note    string
	finding bool
	find    []rune
	checked time.Time
	drag    bool
}

type buffer struct {
	rel, abs   string
	lines      [][]rune
	row, col   int
	want       int // the column up and down aim for
	top, left  int
	sel        bool
	arow, acol int
	dirty      bool
	crlf       bool
	endNL      bool
	useTabs    bool
	mod        time.Time
	size       int64
	undo, redo []snap
	lastEdit   string
	lastAt     time.Time
}

type snap struct {
	lines    [][]rune
	row, col int
}

func newSimpleEditor(dir string, w, h int, t theme) *simpleEditor {
	return &simpleEditor{dir: dir, w: w, h: h, t: t}
}

func (e *simpleEditor) file() string {
	if e.cur == nil {
		return ""
	}
	return e.cur.rel
}

func (e *simpleEditor) saveKey() string      { return "ctrl+s" }
func (e *simpleEditor) resize(w, h int)      { e.w, e.h = w, h }
func (e *simpleEditor) theme(t theme)        { e.t = t }
func (e *simpleEditor) close()               {}
func (e *simpleEditor) textRows() int        { return max(1, e.h-1) }
func (e *simpleEditor) gutter(b *buffer) int { return len(fmt.Sprint(len(b.lines))) + 2 }

func (e *simpleEditor) unsaved() bool {
	for _, b := range e.bufs {
		if b.dirty {
			return true
		}
	}
	return false
}

func (e *simpleEditor) open(p string) {
	abs, rel, err := projectPath(e.dir, p)
	if err != nil {
		e.note = "✕ " + err.Error()
		return
	}
	for _, b := range e.bufs {
		if b.rel == rel {
			e.cur, e.note = b, ""
			return
		}
	}
	b := &buffer{rel: rel, abs: abs, want: -1}
	if err := b.load(); err != nil {
		e.note = "✕ " + err.Error()
		return
	}
	e.bufs = append(e.bufs, b)
	e.cur, e.note = b, ""
}

func (b *buffer) load() error {
	st, err := os.Stat(b.abs)
	if err != nil {
		return pathErr(b.rel, err)
	}
	if st.IsDir() {
		return fmt.Errorf("%s is a folder", b.rel)
	}
	if st.Size() > viewLimit {
		return fmt.Errorf("%s is over 1 MB, too large to edit here", b.rel)
	}
	data, err := os.ReadFile(b.abs)
	if err != nil {
		return pathErr(b.rel, err)
	}
	if isBinary(data) {
		return fmt.Errorf("%s isn't a text file", b.rel)
	}
	text := string(data)
	b.crlf = strings.Contains(text, "\r\n")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	b.endNL = strings.HasSuffix(text, "\n")
	text = strings.TrimSuffix(text, "\n")
	b.lines = nil
	b.useTabs = false
	for _, l := range strings.Split(text, "\n") {
		b.lines = append(b.lines, []rune(l))
		if strings.HasPrefix(l, "\t") {
			b.useTabs = true
		}
	}
	b.mod, b.size, b.dirty = st.ModTime(), st.Size(), false
	b.row = clamp(b.row, 0, len(b.lines)-1)
	b.col = clamp(b.col, 0, len(b.lines[b.row]))
	return nil
}

func (e *simpleEditor) save() {
	b := e.cur
	if b == nil {
		return
	}
	parts := make([]string, len(b.lines))
	for i, l := range b.lines {
		parts[i] = string(l)
	}
	nl := "\n"
	if b.crlf {
		nl = "\r\n"
	}
	text := strings.Join(parts, nl)
	if b.endNL {
		text += nl
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(b.abs); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.WriteFile(b.abs, []byte(text), mode); err != nil {
		e.note = "✕ couldn't save: " + pathErr(b.rel, err).Error()
		return
	}
	if st, err := os.Stat(b.abs); err == nil {
		b.mod, b.size = st.ModTime(), st.Size()
	}
	b.dirty = false
	e.note = "saved"
}

// refresh picks up changes made outside the editor (by Prism, say) to files
// that have no unsaved edits.
func (e *simpleEditor) refresh() {
	if time.Since(e.checked) < 500*time.Millisecond {
		return
	}
	e.checked = time.Now()
	for _, b := range e.bufs {
		st, err := os.Stat(b.abs)
		if err != nil || b.dirty || (st.ModTime().Equal(b.mod) && st.Size() == b.size) {
			continue
		}
		b.load()
		if b == e.cur {
			e.note = "reloaded: the file changed"
		}
	}
}

// --- editing ---

func (b *buffer) remember(kind string) {
	if kind != "" && kind == b.lastEdit && time.Since(b.lastAt) < time.Second {
		b.lastAt = time.Now()
		return
	}
	cp := make([][]rune, len(b.lines))
	for i, l := range b.lines {
		cp[i] = append([]rune(nil), l...)
	}
	b.undo = append(b.undo, snap{cp, b.row, b.col})
	if len(b.undo) > 300 {
		b.undo = b.undo[1:]
	}
	b.redo, b.lastEdit, b.lastAt = nil, kind, time.Now()
}

func (b *buffer) restore(from, to *[]snap) {
	if len(*from) == 0 {
		return
	}
	s := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	*to = append(*to, snap{b.lines, b.row, b.col})
	b.lines, b.row, b.col, b.sel, b.lastEdit, b.dirty = s.lines, s.row, s.col, false, "", true
}

func less(r1, c1, r2, c2 int) bool { return r1 < r2 || (r1 == r2 && c1 < c2) }

// span is the selection, start before end.
func (b *buffer) span() (r1, c1, r2, c2 int) {
	r1, c1, r2, c2 = b.arow, b.acol, b.row, b.col
	if less(r2, c2, r1, c1) {
		r1, c1, r2, c2 = r2, c2, r1, c1
	}
	return
}

func (b *buffer) hasSel() bool { return b.sel && (b.arow != b.row || b.acol != b.col) }

func (b *buffer) selected() string {
	if !b.hasSel() {
		return ""
	}
	r1, c1, r2, c2 := b.span()
	if r1 == r2 {
		return string(b.lines[r1][c1:c2])
	}
	parts := []string{string(b.lines[r1][c1:])}
	for r := r1 + 1; r < r2; r++ {
		parts = append(parts, string(b.lines[r]))
	}
	return strings.Join(append(parts, string(b.lines[r2][:c2])), "\n")
}

func (b *buffer) deleteSel() {
	if !b.hasSel() {
		b.sel = false
		return
	}
	r1, c1, r2, c2 := b.span()
	line := append(append([]rune(nil), b.lines[r1][:c1]...), b.lines[r2][c2:]...)
	b.lines = append(append(b.lines[:r1:r1], line), b.lines[r2+1:]...)
	b.row, b.col, b.sel, b.dirty = r1, c1, false, true
}

func (b *buffer) insert(text string) {
	b.deleteSel()
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	parts := strings.Split(text, "\n")
	line := b.lines[b.row]
	head, tail := append([]rune(nil), line[:b.col]...), append([]rune(nil), line[b.col:]...)
	if len(parts) == 1 {
		b.lines[b.row] = append(append(head, []rune(parts[0])...), tail...)
		b.col += len([]rune(parts[0]))
	} else {
		mid := make([][]rune, 0, len(parts))
		mid = append(mid, append(head, []rune(parts[0])...))
		for _, p := range parts[1 : len(parts)-1] {
			mid = append(mid, []rune(p))
		}
		last := []rune(parts[len(parts)-1])
		mid = append(mid, append(last, tail...))
		b.lines = append(append(append([][]rune(nil), b.lines[:b.row]...), mid...), b.lines[b.row+1:]...)
		b.row += len(parts) - 1
		b.col = len(last)
	}
	b.dirty = true
}

func (b *buffer) indentText() string {
	if b.useTabs {
		return "\t"
	}
	return "  "
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

func (b *buffer) wordLeft() {
	if b.col == 0 {
		if b.row > 0 {
			b.row--
			b.col = len(b.lines[b.row])
		}
		return
	}
	l := b.lines[b.row]
	for b.col > 0 && !isWord(l[b.col-1]) {
		b.col--
	}
	for b.col > 0 && isWord(l[b.col-1]) {
		b.col--
	}
}

func (b *buffer) wordRight() {
	l := b.lines[b.row]
	if b.col == len(l) {
		if b.row < len(b.lines)-1 {
			b.row, b.col = b.row+1, 0
		}
		return
	}
	for b.col < len(l) && !isWord(l[b.col]) {
		b.col++
	}
	for b.col < len(l) && isWord(l[b.col]) {
		b.col++
	}
}

// visCol is the screen column of rune index col on a line, with tabs expanded.
func visCol(line []rune, col int) int {
	x := 0
	for _, r := range line[:min(col, len(line))] {
		x += runeCells(r, x)
	}
	return x
}

func runeCells(r rune, x int) int {
	if r == '\t' {
		return tabWidth - x%tabWidth
	}
	return max(1, runewidth.RuneWidth(r))
}

// colAt is the rune index nearest screen column x.
func colAt(line []rune, x int) int {
	at := 0
	for i, r := range line {
		w := runeCells(r, at)
		if at+w > x {
			if x-at >= (w+1)/2 {
				return i + 1
			}
			return i
		}
		at += w
	}
	return len(line)
}

func (e *simpleEditor) key(msg tea.KeyPressMsg) tea.Cmd {
	b := e.cur
	k := msg.String()
	if b == nil {
		return nil
	}
	if e.finding {
		switch k {
		case "esc":
			e.finding = false
		case "enter", "ctrl+f":
			e.findNext()
		case "backspace":
			if len(e.find) > 0 {
				e.find = e.find[:len(e.find)-1]
			}
		default:
			if msg.Text != "" {
				e.find = append(e.find, []rune(msg.Text)...)
				b.col = max(0, b.col-len(e.find)+len([]rune(msg.Text)))
				e.findNext()
			}
		}
		return nil
	}
	e.note = ""
	shift := strings.Contains(k, "shift+")
	move := func(f func()) {
		if shift && !b.sel {
			b.sel, b.arow, b.acol = true, b.row, b.col
		}
		if !shift {
			b.sel = false
		}
		f()
		b.want = -1
	}
	vertical := func(d int) {
		if b.want < 0 {
			b.want = visCol(b.lines[b.row], b.col)
		}
		b.row = clamp(b.row+d, 0, len(b.lines)-1)
		b.col = colAt(b.lines[b.row], b.want)
	}
	page := e.textRows() - 1
	base := strings.TrimPrefix(strings.ReplaceAll(k, "shift+", ""), "")
	switch base {
	case "up", "down", "pgup", "pgdown":
		if shift && !b.sel {
			b.sel, b.arow, b.acol = true, b.row, b.col
		}
		if !shift {
			b.sel = false
		}
		d := map[string]int{"up": -1, "down": 1, "pgup": -page, "pgdown": page}[base]
		vertical(d)
		return nil
	case "left":
		move(func() {
			if b.hasSel() && !shift {
				return
			}
			if b.col > 0 {
				b.col--
			} else if b.row > 0 {
				b.row--
				b.col = len(b.lines[b.row])
			}
		})
		return nil
	case "right":
		move(func() {
			if b.col < len(b.lines[b.row]) {
				b.col++
			} else if b.row < len(b.lines)-1 {
				b.row, b.col = b.row+1, 0
			}
		})
		return nil
	case "ctrl+left":
		move(b.wordLeft)
		return nil
	case "ctrl+right":
		move(b.wordRight)
		return nil
	case "home":
		move(func() {
			ind := 0
			for ind < len(b.lines[b.row]) && (b.lines[b.row][ind] == ' ' || b.lines[b.row][ind] == '\t') {
				ind++
			}
			if b.col == ind {
				ind = 0
			}
			b.col = ind
		})
		return nil
	case "end":
		move(func() { b.col = len(b.lines[b.row]) })
		return nil
	case "ctrl+home":
		move(func() { b.row, b.col = 0, 0 })
		return nil
	case "ctrl+end":
		move(func() { b.row = len(b.lines) - 1; b.col = len(b.lines[b.row]) })
		return nil
	}
	b.want = -1
	switch k {
	case "ctrl+s":
		e.save()
	case "ctrl+z":
		b.restore(&b.undo, &b.redo)
	case "ctrl+shift+z", "ctrl+y":
		b.restore(&b.redo, &b.undo)
	case "ctrl+a":
		b.sel, b.arow, b.acol = true, 0, 0
		b.row = len(b.lines) - 1
		b.col = len(b.lines[b.row])
	case "ctrl+c", "ctrl+x":
		text := b.selected()
		whole := text == ""
		if whole {
			text = string(b.lines[b.row]) + "\n"
		}
		e.clip = text
		if k == "ctrl+x" {
			b.remember("")
			if whole {
				if len(b.lines) > 1 {
					b.lines = append(b.lines[:b.row:b.row], b.lines[b.row+1:]...)
					b.row = min(b.row, len(b.lines)-1)
				} else {
					b.lines[0] = nil
				}
				b.col, b.dirty = 0, true
			} else {
				b.deleteSel()
			}
		}
		e.note = "copied"
		return tea.SetClipboard(text)
	case "ctrl+v":
		if e.clip != "" {
			b.remember("")
			if strings.HasSuffix(e.clip, "\n") && !b.hasSel() && strings.Count(e.clip, "\n") == 1 {
				b.col = 0
			}
			b.insert(e.clip)
		}
	case "ctrl+f":
		e.finding, e.find = true, nil
		if s := b.selected(); s != "" && !strings.Contains(s, "\n") {
			e.find = []rune(s)
		}
	case "ctrl+d":
		b.remember("")
		line := append([]rune(nil), b.lines[b.row]...)
		b.lines = append(b.lines[:b.row+1:b.row+1], append([][]rune{line}, b.lines[b.row+1:]...)...)
		b.row++
		b.dirty = true
	case "esc":
		b.sel = false
	case "enter":
		b.remember("")
		ind := []rune{}
		for _, r := range b.lines[b.row] {
			if r != ' ' && r != '\t' {
				break
			}
			ind = append(ind, r)
		}
		if b.col < len(ind) {
			ind = ind[:b.col]
		}
		b.insert("\n" + string(ind))
	case "tab", "shift+tab":
		b.remember("")
		if b.hasSel() || k == "shift+tab" {
			r1, r2 := b.row, b.row
			if b.hasSel() {
				r1, _, r2, _ = b.span()
			}
			for r := r1; r <= r2; r++ {
				b.indentLine(r, k == "tab")
			}
		} else {
			b.insert(b.indentText())
		}
	case "backspace", "ctrl+h":
		b.remember("delete")
		switch {
		case b.hasSel():
			b.deleteSel()
		case b.col > 0:
			b.lines[b.row] = append(b.lines[b.row][:b.col-1:b.col-1], b.lines[b.row][b.col:]...)
			b.col--
			b.dirty = true
		case b.row > 0:
			prev := len(b.lines[b.row-1])
			b.lines[b.row-1] = append(b.lines[b.row-1], b.lines[b.row]...)
			b.lines = append(b.lines[:b.row:b.row], b.lines[b.row+1:]...)
			b.row--
			b.col, b.dirty = prev, true
		}
		b.sel = false
	case "delete":
		b.remember("delete")
		switch {
		case b.hasSel():
			b.deleteSel()
		case b.col < len(b.lines[b.row]):
			b.lines[b.row] = append(b.lines[b.row][:b.col:b.col], b.lines[b.row][b.col+1:]...)
			b.dirty = true
		case b.row < len(b.lines)-1:
			b.lines[b.row] = append(b.lines[b.row], b.lines[b.row+1]...)
			b.lines = append(b.lines[:b.row+1:b.row+1], b.lines[b.row+2:]...)
			b.dirty = true
		}
		b.sel = false
	default:
		if msg.Text != "" && msg.Key().Mod&(tea.ModCtrl|tea.ModAlt|tea.ModMeta|tea.ModSuper) == 0 {
			b.remember("type")
			b.insert(msg.Text)
		}
	}
	return nil
}

func (b *buffer) indentLine(r int, in bool) {
	line := b.lines[r]
	if in {
		b.lines[r] = append([]rune(b.indentText()), line...)
		if r == b.row {
			b.col += len([]rune(b.indentText()))
		}
		if b.sel && r == b.arow {
			b.acol += len([]rune(b.indentText()))
		}
		b.dirty = true
		return
	}
	n := 0
	if len(line) > 0 && line[0] == '\t' {
		n = 1
	} else {
		for n < len(line) && n < 2 && line[n] == ' ' {
			n++
		}
	}
	if n == 0 {
		return
	}
	b.lines[r] = line[n:]
	if r == b.row {
		b.col = max(0, b.col-n)
	}
	if b.sel && r == b.arow {
		b.acol = max(0, b.acol-n)
	}
	b.dirty = true
}

func (e *simpleEditor) findNext() {
	b := e.cur
	if len(e.find) == 0 {
		return
	}
	q := strings.ToLower(string(e.find))
	n := len(b.lines)
	for i := 0; i <= n; i++ {
		r := (b.row + i) % n
		from := 0
		if i == 0 {
			from = b.col
		}
		line := []rune(strings.ToLower(string(b.lines[r])))
		if from > len(line) {
			continue
		}
		if at := strings.Index(string(line[from:]), q); at >= 0 {
			c := from + len([]rune(string(line[from:])[:at]))
			b.sel, b.arow, b.acol = true, r, c
			b.row, b.col = r, c+len(e.find)
			return
		}
	}
	e.note = "not found"
}

func (e *simpleEditor) paste(text string) {
	if e.cur == nil {
		return
	}
	e.cur.remember("")
	e.cur.insert(text)
}

func (e *simpleEditor) mouse(button, action, mods string, row, col int) {
	b := e.cur
	if b == nil {
		return
	}
	if button == "wheel" {
		d := map[string]int{"up": -3, "down": 3}[action]
		b.top = clamp(b.top+d, 0, max(0, len(b.lines)-e.textRows()))
		return
	}
	if button != "left" || row >= e.textRows() {
		return
	}
	r := clamp(b.top+row, 0, len(b.lines)-1)
	c := colAt(b.lines[r], max(0, col-e.gutter(b))+b.left)
	switch action {
	case "press":
		e.note = ""
		if strings.Contains(mods, "S") {
			if !b.sel {
				b.sel, b.arow, b.acol = true, b.row, b.col
			}
		} else {
			b.sel, b.arow, b.acol = true, r, c
		}
		b.row, b.col, b.want, e.drag = r, c, -1, true
	case "drag":
		if e.drag {
			b.row, b.col = r, c
		}
	case "release":
		e.drag = false
		if !b.hasSel() {
			b.sel = false
		}
	}
}

// keepCursor scrolls so the cursor is on screen.
func (e *simpleEditor) keepCursor() {
	b := e.cur
	rows := e.textRows()
	if b.row < b.top {
		b.top = b.row
	}
	if b.row >= b.top+rows {
		b.top = b.row - rows + 1
	}
	b.top = clamp(b.top, 0, max(0, len(b.lines)-1))
	textW := max(1, e.w-e.gutter(b))
	x := visCol(b.lines[b.row], b.col)
	if x < b.left {
		b.left = max(0, x-textW/4)
	}
	if x >= b.left+textW {
		b.left = x - textW + textW/4
	}
}

func (e *simpleEditor) draw(cv *canvas, x, y int) (int, int, tea.CursorShape, bool) {
	t := e.t
	cv.fill(x, y, e.w, e.h, t.bg)
	for j := 0; j < e.h; j++ {
		for i := 0; i < e.w; i++ {
			if c := cv.get(x+i, y+j); c != nil {
				c.ch, c.fg, c.bold, c.url, c.act = ' ', t.fg, false, "", ""
			}
		}
	}
	b := e.cur
	if b == nil {
		cv.text(x+2, y+1, clipWidth("no file open", e.w-4), t.muted, true)
		cv.text(x+2, y+3, clipWidth("ctrl+b, then a file · or /editor <file>", e.w-4), t.muted, false)
		if e.note != "" {
			cv.text(x+2, y+5, clipWidth(e.note, e.w-4), t.red, false)
		}
		return 0, 0, tea.CursorBar, false
	}
	e.keepCursor()
	g := e.gutter(b)
	rows := e.textRows()
	selBg := mix(t.bg, t.at(0.6), 0.32)
	lineBg := mix(t.bg, t.panel, 0.7)
	var r1, c1, r2, c2 int
	if b.hasSel() {
		r1, c1, r2, c2 = b.span()
	}
	md := strings.HasSuffix(strings.ToLower(b.rel), ".md")
	for j := 0; j < rows; j++ {
		ln := b.top + j
		if ln >= len(b.lines) {
			break
		}
		if ln == b.row && !b.hasSel() {
			cv.fill(x, y+j, e.w, 1, lineBg)
		}
		num := fmt.Sprintf("%*d ", g-1, ln+1)
		numCol := mix(t.bg, t.muted, 0.55)
		if ln == b.row {
			numCol = t.muted
		}
		cv.text(x, y+j, num, numCol, false)
		line := b.lines[ln]
		fg, bold := t.fg, false
		if md && len(line) > 0 && line[0] == '#' {
			fg, bold = t.at(0.55), true
		}
		vx := 0
		for i, r := range line {
			w := runeCells(r, vx)
			sx := vx - b.left
			vx += w
			if sx < 0 {
				continue
			}
			if sx >= e.w-g {
				break
			}
			inSel := b.hasSel() && !less(ln, i, r1, c1) && less(ln, i, r2, c2)
			if inSel {
				cv.fill(x+g+sx, y+j, min(w, e.w-g-sx), 1, selBg)
			}
			if r == '\t' {
				continue
			}
			cv.put(x+g+sx, y+j, r, fg, bold)
		}
		if b.hasSel() && ln >= r1 && ln < r2 && vx-b.left >= 0 && vx-b.left < e.w-g {
			cv.fill(x+g+vx-b.left, y+j, 1, 1, selBg)
		}
	}
	sy := y + e.h - 1
	cv.fill(x, sy, e.w, 1, t.panel)
	left := " " + b.rel
	if b.dirty {
		left += " ●"
	}
	right := fmt.Sprintf("%d:%d  ctrl+s save · ctrl+e close ", b.row+1, b.col+1)
	if e.finding {
		right = " ctrl+f/enter next · esc done "
		left = " find: " + string(e.find) + "▏"
	} else if e.note != "" {
		right = e.note + "  " + right
	}
	if len([]rune(left))+len([]rune(right)) > e.w {
		right = fmt.Sprintf("%d:%d ", b.row+1, b.col+1)
	}
	lx := cv.text(x, sy, clipWidth(left, max(1, e.w-len([]rune(right))-1)), t.fg, true)
	if b.dirty && !e.finding {
		if c := cv.get(lx-1, sy); c != nil {
			c.fg = warnColor(t)
		}
	}
	cv.text(x+e.w-len([]rune(right)), sy, right, t.muted, false)
	cx := x + g + visCol(b.lines[b.row], b.col) - b.left
	return cx, y + b.row - b.top, tea.CursorBar, !e.finding
}
