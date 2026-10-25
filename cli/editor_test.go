package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestNvimKeys(t *testing.T) {
	cases := []struct {
		key  tea.Key
		want string
	}{
		{tea.Key{Code: 'a', Text: "a"}, "a"},
		{tea.Key{Code: 'a', Text: "A", Mod: tea.ModShift}, "A"},
		{tea.Key{Code: '<', Text: "<"}, "<lt>"},
		{tea.Key{Code: tea.KeyEnter}, "<CR>"},
		{tea.Key{Code: tea.KeyEscape}, "<Esc>"},
		{tea.Key{Code: tea.KeyBackspace}, "<BS>"},
		{tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}, "<S-Tab>"},
		{tea.Key{Code: 'w', Mod: tea.ModCtrl}, "<C-w>"},
		{tea.Key{Code: 'x', Mod: tea.ModAlt}, "<M-x>"},
		{tea.Key{Code: tea.KeyUp}, "<Up>"},
		{tea.Key{Code: tea.KeyPgDown}, "<PageDown>"},
		{tea.Key{Code: tea.KeyF5}, "<F5>"},
		{tea.Key{Code: tea.KeySpace, Text: " "}, " "},
	}
	for _, c := range cases {
		if got := nvimKeys(tea.KeyPressMsg(c.key)); got != c.want {
			t.Errorf("%q: got %q, want %q", tea.KeyPressMsg(c.key).String(), got, c.want)
		}
	}
}

func gridText(e *editor, row int) string {
	var b strings.Builder
	for col := 0; col < e.w; col++ {
		if ch := e.grid[row*e.w+col].ch; ch != 0 {
			b.WriteRune(ch)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func TestEditorGridEvents(t *testing.T) {
	e := &editor{hl: map[int]hlAttr{}, flushed: make(chan struct{}, 1)}
	e.resizeGrid(10, 3)
	e.redraw(
		[]any{"hl_attr_define", []any{int64(1), map[string]any{"foreground": int64(0xff0000), "bold": true}}},
		[]any{"grid_line", []any{int64(1), int64(0), int64(0), []any{[]any{"a", int64(1)}, []any{"b"}, []any{" ", int64(0), int64(3)}, []any{"界"}, []any{""}}, false}},
		[]any{"grid_line", []any{int64(1), int64(1), int64(0), []any{[]any{"r", int64(0)}, []any{"o"}, []any{"w"}, []any{"1"}}, false}},
		[]any{"grid_cursor_goto", []any{int64(1), int64(1), int64(2)}},
		[]any{"flush"},
	)
	if got := gridText(e, 0); got != "ab   界" {
		t.Fatalf("row 0 = %q", got)
	}
	if e.grid[1].hl != 1 || !e.hl[1].bold || e.hl[1].fg != (rgb{255, 0, 0}) {
		t.Fatalf("highlight not carried to the next cell: %+v %+v", e.grid[1], e.hl[1])
	}
	if e.cx != 2 || e.cy != 1 {
		t.Fatalf("cursor = %d,%d", e.cx, e.cy)
	}
	select {
	case <-e.flushed:
	default:
		t.Fatal("flush did not signal a redraw")
	}
	e.redraw([]any{"grid_scroll", []any{int64(1), int64(0), int64(3), int64(0), int64(10), int64(1), int64(0)}})
	if got := gridText(e, 0); got != "row1" {
		t.Fatalf("after scrolling up row 0 = %q", got)
	}
	e.redraw([]any{"grid_resize", []any{int64(1), int64(4), int64(2)}}, []any{"grid_line", []any{int64(1), int64(5), int64(0), []any{[]any{"x", int64(0)}}, false}})
	if e.w != 4 || e.h != 2 || e.cy > 1 {
		t.Fatalf("resize: %dx%d cursor row %d", e.w, e.h, e.cy)
	}
}

// TestEditorRealNeovim drives a real Neovim: open a file, type, save.
func TestEditorRealNeovim(t *testing.T) {
	if _, err := exec.LookPath("nvim"); err != nil {
		t.Skip("nvim not installed")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	os.WriteFile(file, []byte("one\n"), 0o644)
	e, err := startEditor(dir, 60, 10, themes[0])
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()
	e.open("a.txt")
	e.input("Gotwo<Esc>:w<CR>")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(file)
		e.mu.Lock()
		shown := gridText(e, 1)
		e.mu.Unlock()
		if string(data) == "one\ntwo\n" && strings.Contains(shown, "two") {
			if _, err := os.Stat(filepath.Join(dir, ".a.txt.swp")); err == nil {
				t.Fatal("a swap file was left next to the code")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	data, _ := os.ReadFile(file)
	t.Fatalf("file = %q", data)
}
