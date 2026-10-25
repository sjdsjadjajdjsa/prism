package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func editorWith(t *testing.T, name, text string) (*simpleEditor, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(text), 0o644)
	e := newSimpleEditor(dir, 60, 12, themes[0])
	e.open(name)
	if e.cur == nil {
		t.Fatalf("open: %s", e.note)
	}
	return e, p
}

func typeKeys(e *simpleEditor, keys ...string) {
	for _, k := range keys {
		e.key(keyFor(k))
	}
}

func keyFor(k string) tea.KeyPressMsg {
	var mod tea.KeyMod
	for _, m := range []struct {
		p string
		m tea.KeyMod
	}{{"ctrl+", tea.ModCtrl}, {"shift+", tea.ModShift}} {
		for strings.HasPrefix(k, m.p) {
			mod |= m.m
			k = strings.TrimPrefix(k, m.p)
		}
	}
	codes := map[string]rune{"enter": tea.KeyEnter, "backspace": tea.KeyBackspace, "delete": tea.KeyDelete, "tab": tea.KeyTab, "esc": tea.KeyEscape,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight, "home": tea.KeyHome, "end": tea.KeyEnd}
	if c, ok := codes[k]; ok {
		return tea.KeyPressMsg{Code: c, Mod: mod}
	}
	if mod != 0 {
		return tea.KeyPressMsg{Code: []rune(k)[0], Mod: mod}
	}
	return tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
}

func lines(e *simpleEditor) string {
	var out []string
	for _, l := range e.cur.lines {
		out = append(out, string(l))
	}
	return strings.Join(out, "|")
}

func TestSimpleEditorTypingAndSave(t *testing.T) {
	e, p := editorWith(t, "notes.md", "# Title\n  item\n")
	typeKeys(e, "down", "end", "enter", "n", "e", "w")
	if got := lines(e); got != "# Title|  item|  new" {
		t.Fatalf("enter keeps the indent: %q", got)
	}
	if !e.unsaved() {
		t.Fatal("typing marks the file unsaved")
	}
	typeKeys(e, "ctrl+s")
	data, _ := os.ReadFile(p)
	if string(data) != "# Title\n  item\n  new\n" || e.unsaved() {
		t.Fatalf("saved %q", data)
	}
	typeKeys(e, "backspace", "backspace", "backspace", "backspace", "backspace", "backspace")
	if got := lines(e); got != "# Title|  item" {
		t.Fatalf("backspace joins lines: %q", got)
	}
	typeKeys(e, "ctrl+z")
	if got := lines(e); got != "# Title|  item|  new" {
		t.Fatalf("undo: %q", got)
	}
	typeKeys(e, "ctrl+shift+z")
	if got := lines(e); got != "# Title|  item" {
		t.Fatalf("redo: %q", got)
	}
}

func TestSimpleEditorSelectCopyPaste(t *testing.T) {
	e, _ := editorWith(t, "a.txt", "hello world\nsecond")
	typeKeys(e, "shift+end", "ctrl+c", "down", "end", "ctrl+v")
	if got := lines(e); got != "hello world|secondhello world" {
		t.Fatalf("copy and paste: %q", got)
	}
	typeKeys(e, "ctrl+a", "x")
	if got := lines(e); got != "x" {
		t.Fatalf("select all then type replaces: %q", got)
	}
	typeKeys(e, "ctrl+z", "ctrl+home", "ctrl+x")
	if got := lines(e); got != "secondhello world" {
		t.Fatalf("cut with nothing selected cuts the line: %q", got)
	}
}

func TestSimpleEditorFindAndTabs(t *testing.T) {
	e, _ := editorWith(t, "main.go", "package main\n\nfunc main() {\n\tprintln(1)\n}\n")
	typeKeys(e, "ctrl+f", "p", "r", "i", "n", "t")
	if e.cur.row != 3 || e.cur.selected() != "print" {
		t.Fatalf("find: row %d sel %q", e.cur.row, e.cur.selected())
	}
	typeKeys(e, "esc", "esc", "home", "tab")
	if got := string(e.cur.lines[3]); got != "\t\tprintln(1)" {
		t.Fatalf("a Go file indents with tabs: %q", got)
	}
	if visCol(e.cur.lines[3], 2) != 8 {
		t.Fatalf("tabs are 4 wide: %d", visCol(e.cur.lines[3], 2))
	}
}

func TestSimpleEditorReloadsOutsideChanges(t *testing.T) {
	e, p := editorWith(t, "a.txt", "one\n")
	time.Sleep(20 * time.Millisecond)
	os.WriteFile(p, []byte("one\ntwo\n"), 0o644)
	e.checked = time.Time{}
	e.refresh()
	if lines(e) != "one|two" {
		t.Fatalf("an outside change reloads: %q", lines(e))
	}
	typeKeys(e, "x")
	os.WriteFile(p, []byte("changed again\n"), 0o644)
	e.checked = time.Time{}
	e.refresh()
	if !strings.HasPrefix(lines(e), "x") {
		t.Fatalf("unsaved edits are never thrown away: %q", lines(e))
	}
}

func TestSimpleEditorRefusesBinary(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "b.bin"), []byte{0, 1, 2}, 0o644)
	e := newSimpleEditor(dir, 60, 12, themes[0])
	e.open("b.bin")
	if e.cur != nil || !strings.Contains(e.note, "isn't a text file") {
		t.Fatalf("binary: %q", e.note)
	}
	e.open("../x")
	if e.cur != nil || e.note == "" {
		t.Fatal("outside the project is refused")
	}
}

func TestSimpleEditorDrawAndClick(t *testing.T) {
	e, _ := editorWith(t, "a.txt", "alpha\nbeta\n")
	cv := newCanvas(60, 12, rgb{}, rgb{})
	cx, cy, _, show := e.draw(cv, 0, 0)
	if !show || cx != e.gutter(e.cur) || cy != 0 {
		t.Fatalf("cursor at %d,%d", cx, cy)
	}
	e.mouse("left", "press", "", 1, e.gutter(e.cur)+2)
	e.mouse("left", "release", "", 1, e.gutter(e.cur)+2)
	if e.cur.row != 1 || e.cur.col != 2 {
		t.Fatalf("click moves the cursor: %d,%d", e.cur.row, e.cur.col)
	}
}
