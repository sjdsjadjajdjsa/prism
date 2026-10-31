package main

import (
	"math"
	"strconv"

	tea "charm.land/bubbletea/v2"
)

// The editor panel sits right of the chat. edSplit is its share of the
// window; the chat keeps at least minChatW columns.
const (
	minChatW   = 44
	minEditorW = 30
)

type editorState struct {
	ed     pane
	shown  bool
	focus  bool
	drag   bool
	split  float64
	theme  int
	quitOK bool // ctrl+c was pressed once with unsaved changes
}

func loadEditorSplit() float64 {
	if f, err := strconv.ParseFloat(loadSetting("editor_split"), 64); err == nil && f >= 0.2 && f <= 0.8 {
		return f
	}
	return 0.5
}

func (m model) editorOn() bool { return m.es.shown && m.es.ed != nil }

func (m model) chatW() int {
	if !m.editorOn() {
		return m.w - m.sideW()
	}
	return m.dividerX() - m.sideW()
}

func (m model) dividerX() int {
	least := m.sideW() + minChatW
	x := int(math.Round(float64(m.w) * (1 - m.es.split)))
	return clamp(x, least, max(least, m.w-minEditorW-1))
}

// editorRect is where the editor is drawn: right of the divider, full height.
func (m model) editorRect() (x, w int) {
	x = m.dividerX() + 1
	return x, m.w - x
}

func (m model) editorFits() bool { return m.w >= m.sideW()+minChatW+minEditorW+1 }

func (m *model) showEditor(path string) tea.Cmd {
	if !m.editorFits() {
		m.note("the window is too narrow for the editor · make it wider")
		return nil
	}
	m.fb = filesState{}
	var cmd tea.Cmd
	if m.es.ed == nil {
		if m.es.split == 0 {
			m.es.split = loadEditorSplit()
		}
		_, w := m.editorRect()
		if loadSetting("editor") == "nvim" && nvimInstalled() {
			ed, err := startEditor(m.agent.dir, w, m.h, themes[m.theme])
			if err != nil {
				m.note("✕ " + err.Error())
				return nil
			}
			m.es.ed = ed
			cmd = waitEditor(ed)
		} else {
			m.es.ed = newSimpleEditor(m.agent.dir, w, m.h, themes[m.theme])
		}
		m.es.theme = m.theme
	}
	m.es.shown, m.es.focus = true, true
	m.syncEditor()
	if path != "" {
		m.es.ed.open(path)
		if m.sideOn() {
			m.revealInSide(path)
		}
	}
	return cmd
}

// chooseEditor switches between Prism's simple editor and Neovim, and
// remembers the choice.
func (m *model) chooseEditor(kind string) tea.Cmd {
	if kind == "nvim" && !nvimInstalled() {
		m.note("Neovim isn't installed here · the simple editor stays on")
		return nil
	}
	if m.es.ed != nil && m.es.ed.unsaved() {
		m.note("save your changes in the editor first, then switch")
		return nil
	}
	saveSetting("editor", kind)
	file := ""
	if m.es.ed != nil {
		file = m.es.ed.file()
		m.es.ed.close()
	}
	m.es = editorState{split: m.es.split}
	name := map[string]string{"nvim": "Neovim", "simple": "the simple editor"}[kind]
	m.note("editor: " + name + " · /editor nvim or /editor simple switches")
	return m.showEditor(file)
}

func (m *model) hideEditor() {
	m.es.shown, m.es.focus, m.es.drag = false, false, false
}

func (m *model) note(text string) {
	m.entries = append(m.entries, &entry{kind: "note", text: text})
	m.scroll = 0
}

// syncEditor keeps Neovim's size and colours in step with the window.
func (m *model) syncEditor() {
	if !m.editorOn() {
		return
	}
	if !m.editorFits() {
		m.hideEditor()
		return
	}
	_, w := m.editorRect()
	m.es.ed.resize(w, m.h)
	m.es.ed.refresh()
	if m.es.theme != m.theme {
		m.es.theme = m.theme
		m.es.ed.theme(themes[m.theme])
	}
}

func (m model) editorMsg(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case editorFlushMsg:
		if msg.e == m.es.ed {
			if f := msg.e.file(); f != m.side.following {
				m.side.following = f
				m.revealInSide(f)
			}
			return m, waitEditor(msg.e)
		}
	case editorExitMsg:
		if msg.e == m.es.ed {
			m.es = editorState{split: m.es.split}
			m.note("editor closed")
		}
	}
	return m, nil
}

func (m model) editorKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	if msg.String() == "ctrl+e" {
		m.hideEditor()
		return m, nil
	}
	// Neovim keeps ctrl+b for paging; the simple editor hands it to the tree.
	if _, simple := m.es.ed.(*simpleEditor); simple && msg.String() == "ctrl+b" {
		return m, m.toggleSidebar()
	}
	return m, m.es.ed.key(msg)
}

// editorMouse handles the pointer while the editor is shown: the divider
// resizes, the editor gets its own clicks and the wheel, and a click in the
// chat moves the keyboard back there. ok is false when the chat should
// handle the event.
func (m model) editorMouse(msg tea.Msg) (model, bool) {
	mouse := msg.(interface{ Mouse() tea.Mouse }).Mouse()
	div := m.dividerX()
	ex, _ := m.editorRect()
	switch msg.(type) {
	case tea.MouseClickMsg:
		if mouse.X == div && mouse.Button == tea.MouseLeft {
			m.side.focus = false
			m.es.drag = true
			return m, true
		}
	case tea.MouseMotionMsg:
		if m.es.drag {
			m.es.split = math.Max(0.15, math.Min(0.8, 1-float64(mouse.X)/float64(max(1, m.w))))
			m.syncEditor()
			return m, true
		}
	case tea.MouseReleaseMsg:
		if m.es.drag {
			m.es.drag = false
			saveSetting("editor_split", strconv.FormatFloat(m.es.split, 'f', 2, 64))
			return m, true
		}
	}
	if mouse.X < ex {
		if _, click := msg.(tea.MouseClickMsg); click {
			m.es.focus = false
		}
		return m, false
	}
	row, col := mouse.Y, mouse.X-ex
	mods := ""
	if mouse.Mod&tea.ModCtrl != 0 {
		mods += "C"
	}
	if mouse.Mod&tea.ModShift != 0 {
		mods += "S"
	}
	if mouse.Mod&tea.ModAlt != 0 {
		mods += "A"
	}
	button := map[tea.MouseButton]string{tea.MouseLeft: "left", tea.MouseRight: "right", tea.MouseMiddle: "middle"}[mouse.Button]
	switch msg.(type) {
	case tea.MouseClickMsg:
		m.es.focus, m.side.focus = true, false
		if button != "" {
			m.es.ed.mouse(button, "press", mods, row, col)
		}
	case tea.MouseMotionMsg:
		if button != "" {
			m.es.ed.mouse(button, "drag", mods, row, col)
		}
	case tea.MouseReleaseMsg:
		if button != "" {
			m.es.ed.mouse(button, "release", mods, row, col)
		}
	case tea.MouseWheelMsg:
		dir := map[tea.MouseButton]string{tea.MouseWheelUp: "up", tea.MouseWheelDown: "down", tea.MouseWheelLeft: "left", tea.MouseWheelRight: "right"}[mouse.Button]
		if dir != "" {
			m.es.ed.mouse("wheel", dir, mods, row, col)
		}
	}
	return m, true
}

// drawLayout draws the file tree, the chat and the editor side by side, and
// says where the terminal cursor belongs.
func drawLayout(cv *canvas, t theme, m model) *tea.Cursor {
	cx, cw := m.chatX(), m.chatW()
	chat := m
	chat.w = cw
	chat.es.shown, chat.side.shown, chat.noBuddy = false, false, true
	sub := newCanvas(cw, m.h, t.bg, t.fg)
	drawChat(sub, t, chat)
	for y := 0; y < m.h; y++ {
		copy(cv.cells[y*cv.w+cx:y*cv.w+cx+cw], sub.cells[y*sub.w:(y+1)*sub.w])
	}
	if m.sideOn() {
		drawSidebar(cv, t, m)
	}
	if !m.editorOn() {
		return nil
	}
	div := t.faint
	if m.es.focus || m.es.drag {
		div = t.at(0.5)
	}
	for y := 0; y < m.h; y++ {
		cv.set(m.dividerX(), y, '│', div, false)
	}
	ex, _ := m.editorRect()
	x, y, shape, show := m.es.ed.draw(cv, ex, 0)
	if !m.es.focus || !show || m.menu.open || m.fb.open {
		return nil
	}
	c := tea.NewCursor(x, y)
	c.Shape = shape
	return c
}
