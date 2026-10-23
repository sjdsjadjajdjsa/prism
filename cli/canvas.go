package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-runewidth"
)

type cell struct {
	ch     rune
	fg, bg rgb
	bold   bool
	url    string
}

type canvas struct {
	w, h  int
	cells []cell
}

func newCanvas(w, h int, bg, fg rgb) *canvas {
	cv := &canvas{w: w, h: h, cells: make([]cell, w*h)}
	for i := range cv.cells {
		cv.cells[i] = cell{ch: ' ', fg: fg, bg: bg}
	}
	return cv
}

func (cv *canvas) get(x, y int) *cell {
	if x < 0 || y < 0 || x >= cv.w || y >= cv.h {
		return nil
	}
	return &cv.cells[y*cv.w+x]
}

func (cv *canvas) set(x, y int, ch rune, fg rgb, bold bool) {
	if ch < 32 || ch == 0x7f {
		ch = ' '
	}
	if c := cv.get(x, y); c != nil {
		c.ch, c.fg, c.bold = ch, fg, bold
	}
}

func (cv *canvas) put(x, y int, r rune, fg rgb, bold bool) int {
	w := runewidth.RuneWidth(r)
	if w == 0 {
		return x
	}
	if w == 2 && x+1 >= cv.w {
		r, w = ' ', 1
	}
	cv.set(x, y, r, fg, bold)
	if w == 2 {
		if c := cv.get(x+1, y); c != nil {
			c.ch = 0
		}
	}
	return x + w
}

func (cv *canvas) text(x, y int, s string, fg rgb, bold bool) int {
	for _, r := range s {
		x = cv.put(x, y, r, fg, bold)
	}
	return x
}

func (cv *canvas) link(x, y int, s, url string, fg rgb, bold bool) {
	// A URL with control bytes could break out of the OSC 8 sequence.
	if strings.IndexFunc(url, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		url = ""
	}
	for _, r := range s {
		start := x
		x = cv.put(x, y, r, fg, bold)
		for i := start; i < x; i++ {
			if c := cv.get(i, y); c != nil {
				c.url = url
			}
		}
	}
}

func (cv *canvas) grad(x, y int, s string, t theme, phase, span float64, bold bool) int {
	for i, r := range []rune(s) {
		x = cv.put(x, y, r, t.at(phase+float64(i)/span), bold)
	}
	return x
}

func (cv *canvas) fill(x, y, w, h int, bg rgb) {
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			if c := cv.get(i, j); c != nil {
				c.bg = bg
			}
		}
	}
}

func (cv *canvas) String() string {
	var b strings.Builder
	term := os.Getenv("TERM")
	hyperlinks := term != "dumb" && term != "linux" && term != ""
	for y := 0; y < cv.h; y++ {
		var last cell
		active := ""
		for x := 0; x < cv.w; x++ {
			c := cv.cells[y*cv.w+x]
			if hyperlinks && c.url != active {
				if active != "" {
					b.WriteString("\x1b]8;;\x1b\\")
				}
				if c.url != "" {
					fmt.Fprintf(&b, "\x1b]8;;%s\x1b\\", c.url)
				}
				active = c.url
			}
			if x == 0 || c.fg != last.fg || c.bg != last.bg || c.bold != last.bold {
				bold := ""
				if c.bold {
					bold = "1;"
				}
				fmt.Fprintf(&b, "\x1b[0;%s38;2;%d;%d;%d;48;2;%d;%d;%dm", bold, c.fg.r, c.fg.g, c.fg.b, c.bg.r, c.bg.g, c.bg.b)
				last = c
			}
			if c.ch != 0 {
				b.WriteRune(c.ch)
			}
		}
		if active != "" {
			b.WriteString("\x1b]8;;\x1b\\")
		}
		b.WriteString("\x1b[0m")
		if y < cv.h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
