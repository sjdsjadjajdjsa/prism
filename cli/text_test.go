package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClipText(t *testing.T) {
	cases := []struct {
		s    string
		n    int
		want string
	}{
		{"", 10, ""},
		{"hello", 5, "hello"}, // exact length
		{"hello", 4, "hell…"}, // one over
		{"hello world", 5, "hello…"},
		{"  hi  ", 10, "hi"}, // trimmed first
		{"  hi  ", 2, "hi"},  // trim can make it fit
		{"世界", 6, "世界"},      // exact byte length
		{"世界", 5, "世…"},      // cut backs off to a rune start
		{"世界", 3, "世…"},
		{"世界", 2, "…"}, // no room for a full rune
		{"hello", 0, "…"},
		{"hello", 1, "h…"},
	}
	for _, tc := range cases {
		if got := clipText(tc.s, tc.n); got != tc.want {
			t.Errorf("clipText(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
		}
	}
}

func TestClipRunes(t *testing.T) {
	cases := []struct {
		s    string
		n    int
		want string
	}{
		{"", 5, ""},
		{"hello", 5, "hello"}, // exact length
		{"hello", 4, "hell…"}, // one over
		{"hello", 6, "hello"},
		{"世界", 2, "世界"}, // wide runes count as one
		{"世界", 1, "世…"},
		{"世界", 3, "世界"}, // more bytes than n, but only 2 runes
		{"a界b", 2, "a界…"},
		{"hello", 0, "…"},
		{"hello", 1, "h…"},
	}
	for _, tc := range cases {
		if got := clipRunes(tc.s, tc.n); got != tc.want {
			t.Errorf("clipRunes(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
		}
	}
}

func TestClipWidth(t *testing.T) {
	cases := []struct {
		s     string
		width int
		want  string
	}{
		{"", 5, ""},
		{"hello", 5, "hello"}, // exact length
		{"hello", 4, "hel…"},  // one over: ellipsis takes the last cell
		{"hello", 1, "…"},
		{"hello", 0, ""},
		{"hello", -3, ""},
		{"a\tb", 10, "a    b"}, // tabs count as four spaces
		{"a\tb", 3, "a …"},
		{"世界", 2, "世界"},
		{"世界", 1, "…"},
		{"世界世界", 3, "世界…"},
	}
	for _, tc := range cases {
		if got := clipWidth(tc.s, tc.width); got != tc.want {
			t.Errorf("clipWidth(%q, %d) = %q, want %q", tc.s, tc.width, got, tc.want)
		}
	}
}

func TestClipWords(t *testing.T) {
	cases := []struct {
		s    string
		n    int
		want string
	}{
		{"", 5, ""},
		{"hello", 5, "hello"},
		{"hello", 4, "hell…"},
		{"a  b\tc", 10, "a b c"}, // whitespace collapsed
		{"a  b\tc", 3, "a b…"},
		{"世界 世界", 2, "世界…"},
		{"hello", 0, "…"},
		{"hello", 1, "h…"},
	}
	for _, tc := range cases {
		if got := clipWords(tc.s, tc.n); got != tc.want {
			t.Errorf("clipWords(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
		}
	}
}

func TestPadRunes(t *testing.T) {
	cases := []struct {
		s    string
		n    int
		want string
	}{
		{"hi", 5, "hi   "},
		{"hello", 5, "hello"}, // exact length
		{"toolong", 3, "toolong"},
		{"世界", 3, "世界 "}, // padded by runes, not bytes
		{"", 3, "   "},
		{"hi", 0, "hi"},
		{"hi", -2, "hi"},
	}
	for _, tc := range cases {
		if got := padRunes(tc.s, tc.n); got != tc.want {
			t.Errorf("padRunes(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
		}
	}
}

func TestPadBytes(t *testing.T) {
	cases := []struct {
		s    string
		n    int
		want string
	}{
		{"hi", 5, "hi   "},
		{"hello", 5, "hello"}, // exact length
		{"toolong", 3, "toolong"},
		{"世界", 5, "世界"}, // 6 bytes already reach 5
		{"世界", 7, "世界 "},
		{"", 3, "   "},
		{"hi", 0, "hi"},
	}
	for _, tc := range cases {
		if got := padBytes(tc.s, tc.n); got != tc.want {
			t.Errorf("padBytes(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
		}
	}
}

func TestShortPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	dir := filepath.Join(home, "work", "proj")
	cases := []struct {
		p, d string
		want string
	}{
		{filepath.Join(dir, ".prism", "cmds", "x.md"), dir, ".prism/cmds/x.md"},
		{filepath.Join(home, ".prism", "x.md"), dir, "~/.prism/x.md"},
		{filepath.Join(string(filepath.Separator), "etc", "x.md"), dir, "/etc/x.md"},
		{filepath.Join(dir, "notes.md"), dir, "notes.md"},
	}
	for _, tc := range cases {
		if got := shortPath(tc.p, tc.d); got != tc.want {
			t.Errorf("shortPath(%q, %q) = %q, want %q", tc.p, tc.d, got, tc.want)
		}
	}
}
