package main

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/neovim/go-client/nvim"
)

//go:embed nvim/init.lua
var nvimInit []byte

// editor is an embedded Neovim. It runs headless (--embed) and sends its
// screen as grid updates, which Prism draws next to the chat.
type editor struct {
	nv   *nvim.Nvim
	jobs chan func(*nvim.Nvim)
	done chan struct{}
	err  error

	flushed chan struct{}

	mu      sync.Mutex
	w, h    int
	grid    []gridCell
	hl      map[int]hlAttr
	fg, bg  rgb
	cx, cy  int
	mode    string
	visible bool // false while Neovim is busy and hides the cursor
}

type gridCell struct {
	ch rune // 0 for the right half of a wide character
	hl int
}

type hlAttr struct {
	fg, bg        rgb
	hasFg, hasBg  bool
	bold, reverse bool
}

type editorFlushMsg struct{ e *editor }

type editorExitMsg struct {
	e   *editor
	err error
}

func startEditor(dir string, w, h int, t theme) (*editor, error) {
	if _, err := exec.LookPath("nvim"); err != nil {
		if inScreenshotContainer() {
			return nil, errors.New("Neovim isn't installed in Prism's box yet · run: prism update")
		}
		return nil, errors.New("Neovim isn't installed on this computer · install the neovim package")
	}
	init := filepath.Join(os.TempDir(), "prism-nvim-init.lua")
	if err := os.WriteFile(init, nvimInit, 0o644); err != nil {
		return nil, err
	}
	nv, err := nvim.NewChildProcess(
		nvim.ChildProcessArgs("--embed", "-u", init, "-i", "NONE"),
		nvim.ChildProcessDir(dir),
		nvim.ChildProcessServe(false),
		nvim.ChildProcessLogf(func(string, ...any) {}),
	)
	if err != nil {
		return nil, fmt.Errorf("Neovim didn't start: %w", err)
	}
	e := &editor{
		nv: nv, jobs: make(chan func(*nvim.Nvim), 256), done: make(chan struct{}), flushed: make(chan struct{}, 1),
		hl: map[int]hlAttr{}, fg: t.fg, bg: t.bg, mode: "normal", visible: true,
	}
	e.resizeGrid(w, h)
	if err := nv.RegisterHandler("redraw", e.redraw); err != nil {
		nv.Close()
		return nil, err
	}
	go func() {
		e.err = nv.Serve()
		close(e.done)
	}()
	if err := nv.AttachUI(w, h, map[string]any{"rgb": true, "ext_linegrid": true}); err != nil {
		nv.Close()
		return nil, fmt.Errorf("Neovim didn't start: %w", err)
	}
	e.theme(t)
	go e.work()
	return e, nil
}

// work runs calls into Neovim one at a time and in order, off the UI thread.
func (e *editor) work() {
	for {
		select {
		case job := <-e.jobs:
			job(e.nv)
		case <-e.done:
			return
		}
	}
}

func (e *editor) do(job func(*nvim.Nvim)) {
	select {
	case e.jobs <- job:
	case <-e.done:
	default:
	}
}

func (e *editor) alive() bool {
	select {
	case <-e.done:
		return false
	default:
		return true
	}
}

func (e *editor) close() {
	e.do(func(v *nvim.Nvim) { v.Command("qall!") })
	go func() {
		<-e.done
		e.nv.Close()
	}()
}

func (e *editor) theme(t theme) {
	hexOf := func(c rgb) string { return fmt.Sprintf("#%02x%02x%02x", c.r, c.g, c.b) }
	cmd := fmt.Sprintf("set background=dark | hi Normal guibg=%s guifg=%s | hi NormalNC guibg=%s | hi EndOfBuffer guifg=%s | hi LineNr guifg=%s | hi StatusLine guibg=%s guifg=%s | hi StatusLineNC guibg=%s guifg=%s",
		hexOf(t.bg), hexOf(t.fg), hexOf(t.bg), hexOf(t.bg), hexOf(t.faint), hexOf(t.panel), hexOf(t.fg), hexOf(t.bg), hexOf(t.muted))
	e.do(func(v *nvim.Nvim) { v.Command(cmd) })
}

func (e *editor) open(path string) {
	e.do(func(v *nvim.Nvim) { v.Command("edit " + escapeVimPath(path)) })
}

func escapeVimPath(p string) string {
	var b strings.Builder
	for _, r := range p {
		if strings.ContainsRune(" \\%#|\"'", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (e *editor) resize(w, h int) {
	if w < 1 || h < 1 {
		return
	}
	e.mu.Lock()
	same := e.w == w && e.h == h
	e.mu.Unlock()
	if !same {
		e.do(func(v *nvim.Nvim) { v.TryResizeUI(w, h) })
	}
}

func (e *editor) input(keys string) {
	if keys != "" {
		e.do(func(v *nvim.Nvim) { v.Input(keys) })
	}
}

func (e *editor) paste(text string) {
	e.do(func(v *nvim.Nvim) { v.Paste(text, true, -1) })
}

func (e *editor) mouse(button, action, mods string, row, col int) {
	e.do(func(v *nvim.Nvim) { v.InputMouse(button, action, mods, 0, row, col) })
}

// unsaved reports whether any buffer has changes that aren't written yet.
func (e *editor) unsaved() bool {
	if !e.alive() {
		return false
	}
	var n int
	if err := e.nv.Eval("len(getbufinfo({'bufmodified': 1}))", &n); err != nil {
		return false
	}
	return n > 0
}

func waitEditor(e *editor) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-e.flushed:
			return editorFlushMsg{e}
		case <-e.done:
			return editorExitMsg{e, e.err}
		}
	}
}

func (e *editor) resizeGrid(w, h int) {
	e.w, e.h = w, h
	e.grid = make([]gridCell, w*h)
	for i := range e.grid {
		e.grid[i] = gridCell{ch: ' '}
	}
	e.cx, e.cy = min(e.cx, max(0, w-1)), min(e.cy, max(0, h-1))
}

// redraw applies one batch of Neovim's UI events (:help ui-linegrid).
func (e *editor) redraw(updates ...[]any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, u := range updates {
		if len(u) == 0 {
			continue
		}
		name, _ := u[0].(string)
		for _, raw := range u[1:] {
			args, _ := raw.([]any)
			e.event(name, args)
		}
		if name == "flush" {
			select {
			case e.flushed <- struct{}{}:
			default:
			}
		}
	}
}

func (e *editor) event(name string, a []any) {
	switch name {
	case "grid_resize":
		if len(a) >= 3 {
			e.resizeGrid(toInt(a[1]), toInt(a[2]))
		}
	case "grid_clear":
		for i := range e.grid {
			e.grid[i] = gridCell{ch: ' '}
		}
	case "grid_cursor_goto":
		if len(a) >= 3 {
			e.cy, e.cx = toInt(a[1]), toInt(a[2])
		}
	case "grid_line":
		if len(a) >= 4 {
			e.line(toInt(a[1]), toInt(a[2]), a[3])
		}
	case "grid_scroll":
		if len(a) >= 7 {
			e.scroll(toInt(a[1]), toInt(a[2]), toInt(a[3]), toInt(a[4]), toInt(a[5]))
		}
	case "default_colors_set":
		if len(a) >= 2 {
			if fg := toInt(a[0]); fg >= 0 {
				e.fg = rgbOf(fg)
			}
			if bg := toInt(a[1]); bg >= 0 {
				e.bg = rgbOf(bg)
			}
		}
	case "hl_attr_define":
		if len(a) >= 2 {
			e.hl[toInt(a[0])] = attrOf(a[1])
		}
	case "mode_change":
		if len(a) >= 1 {
			e.mode, _ = a[0].(string)
		}
	case "busy_start":
		e.visible = false
	case "busy_stop":
		e.visible = true
	}
}

func (e *editor) line(row, col int, cells any) {
	list, _ := cells.([]any)
	if row < 0 || row >= e.h {
		return
	}
	hl := 0
	for _, c := range list {
		parts, _ := c.([]any)
		if len(parts) == 0 {
			continue
		}
		text, _ := parts[0].(string)
		if len(parts) > 1 {
			hl = toInt(parts[1])
		}
		repeat := 1
		if len(parts) > 2 {
			repeat = toInt(parts[2])
		}
		ch := rune(0)
		if text != "" {
			ch = []rune(text)[0]
		}
		for ; repeat > 0; repeat-- {
			if col >= 0 && col < e.w {
				e.grid[row*e.w+col] = gridCell{ch: ch, hl: hl}
			}
			col++
		}
	}
}

func (e *editor) scroll(top, bot, left, right, rows int) {
	right = min(right, e.w)
	bot = min(bot, e.h)
	move := func(y int) {
		copy(e.grid[y*e.w+left:y*e.w+right], e.grid[(y+rows)*e.w+left:(y+rows)*e.w+right])
	}
	if rows > 0 {
		for y := top; y < bot-rows; y++ {
			move(y)
		}
	} else {
		for y := bot - 1; y >= top-rows; y-- {
			move(y)
		}
	}
}

// draw paints the editor at x, y and returns where the cursor is on screen.
func (e *editor) draw(cv *canvas, x, y int) (cx, cy int, shape tea.CursorShape, show bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for row := 0; row < e.h; row++ {
		for col := 0; col < e.w; col++ {
			g := e.grid[row*e.w+col]
			a := e.hl[g.hl]
			fg, bg := e.fg, e.bg
			if a.hasFg {
				fg = a.fg
			}
			if a.hasBg {
				bg = a.bg
			}
			if a.reverse {
				fg, bg = bg, fg
			}
			if c := cv.get(x+col, y+row); c != nil {
				c.ch, c.fg, c.bg, c.bold, c.url = g.ch, fg, bg, a.bold, ""
				if g.ch != 0 && (g.ch < 32 || g.ch == 0x7f) {
					c.ch = ' '
				}
			}
		}
	}
	shape = tea.CursorBlock
	switch {
	case strings.HasPrefix(e.mode, "insert"), e.mode == "cmdline_insert":
		shape = tea.CursorBar
	case strings.HasPrefix(e.mode, "replace"), e.mode == "cmdline_replace":
		shape = tea.CursorUnderline
	}
	return x + e.cx, y + e.cy, shape, e.visible
}

func attrOf(v any) hlAttr {
	m, _ := v.(map[string]any)
	var a hlAttr
	if c, ok := m["foreground"]; ok {
		a.fg, a.hasFg = rgbOf(toInt(c)), true
	}
	if c, ok := m["background"]; ok {
		a.bg, a.hasBg = rgbOf(toInt(c)), true
	}
	a.bold, _ = m["bold"].(bool)
	a.reverse, _ = m["reverse"].(bool)
	return a
}

func rgbOf(n int) rgb { return rgb{uint8(n >> 16), uint8(n >> 8), uint8(n)} }

func toInt(v any) int {
	switch n := v.(type) {
	case int64:
		return int(n)
	case uint64:
		return int(n)
	case int:
		return n
	case int32:
		return int(n)
	case uint32:
		return int(n)
	case int8:
		return int(n)
	case uint8:
		return int(n)
	case int16:
		return int(n)
	case uint16:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

var nvimKeyNames = map[string]string{
	"enter": "CR", "tab": "Tab", "backspace": "BS", "esc": "Esc", "space": "Space",
	"up": "Up", "down": "Down", "left": "Left", "right": "Right", "insert": "Insert",
	"delete": "Del", "pgup": "PageUp", "pgdown": "PageDown", "home": "Home", "end": "End",
}

// nvimKeys turns a key press into Neovim's key notation (:help key-notation).
func nvimKeys(msg tea.KeyPressMsg) string {
	k := msg.Key()
	if k.Text != "" && k.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModMeta|tea.ModSuper) == 0 {
		return strings.ReplaceAll(k.Text, "<", "<lt>")
	}
	s := msg.String()
	parts := strings.Split(s, "+")
	key := parts[len(parts)-1]
	if strings.HasSuffix(s, "++") || s == "+" {
		key = "+"
		parts = parts[:len(parts)-1]
	}
	mods := ""
	for _, p := range parts[:len(parts)-1] {
		switch p {
		case "ctrl":
			mods += "C-"
		case "alt", "meta":
			mods += "M-"
		case "shift":
			mods += "S-"
		case "super":
			mods += "D-"
		}
	}
	name, ok := nvimKeyNames[key]
	switch {
	case ok:
	case len(key) > 1 && key[0] == 'f':
		name = strings.ToUpper(key)
	case len([]rune(key)) == 1:
		name = key
		if key == "<" {
			name = "lt"
		}
		if mods == "" {
			if name == "lt" {
				return "<lt>"
			}
			return key
		}
	default:
		return ""
	}
	if mods == "" && len(name) == 1 {
		return name
	}
	return "<" + mods + name + ">"
}
