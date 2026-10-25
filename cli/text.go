package main

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// clipText trims s and shortens it to at most n bytes, cutting on a rune
// boundary and marking the cut with an ellipsis. Text that already fits is
// returned unchanged (after trimming).
func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// clipRunes shortens s to at most n runes, marking a cut with an ellipsis.
// Wide runes count as one, so a wide string can come back shorter than n
// bytes and never gets an ellipsis when it has n runes or fewer.
func clipRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i] + "…"
		}
		count++
	}
	return s
}

// clipWidth shortens s to at most width runes including the ellipsis, so the
// result never exceeds width. Tabs count as four spaces.
func clipWidth(s string, width int) string {
	r := []rune(strings.ReplaceAll(s, "\t", "    "))
	if width < 1 {
		return ""
	}
	if len(r) > width {
		return string(r[:width-1]) + "…"
	}
	return string(r)
}

// clipWords collapses runs of whitespace to single spaces, then clips to n
// runes like clipRunes.
func clipWords(s string, n int) string {
	return clipRunes(strings.Join(strings.Fields(s), " "), n)
}

// padRunes pads s with spaces on the right to n runes, so a clipped name
// ending in "…" still lines up.
func padRunes(s string, n int) string {
	if k := len([]rune(s)); k < n {
		return s + strings.Repeat(" ", n-k)
	}
	return s
}

// padBytes pads s with spaces on the right to n bytes.
func padBytes(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

// shortPath shows p relative to dir when possible, under ~ when it lives in
// the home directory, and unchanged otherwise.
func shortPath(p, dir string) string {
	if rel, err := filepath.Rel(dir, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~/" + filepath.ToSlash(strings.TrimPrefix(p, home+string(filepath.Separator)))
	}
	return p
}
