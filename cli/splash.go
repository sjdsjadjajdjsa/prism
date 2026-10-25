package main

import (
	"math"
)

var logo = []string{
	"####..####..#####..####.#...#",
	"#...#.#...#...#...#.....##.##",
	"####..####....#....###..#.#.#",
	"#.....#..#....#.......#.#...#",
	"#.....#...#.#####.####..#...#",
}

func drawSplash(cv *canvas, t theme, m model) {
	n := ms(m.now)
	compact := cv.w < 82 || cv.h < 20
	lw, lh := len(logo[0])*2, len(logo)
	lx, ly := (cv.w-lw)/2, cv.h/2-lh/2-2
	cx, cy := float64(cv.w)/2, float64(cv.h)/2

	for y := 0; y < cv.h; y++ {
		for x := 0; x+1 < cv.w; x += 2 {
			dx, dy := math.Abs(float64(x)-cx)/cx, math.Abs(float64(y)-cy)/cy
			edge := math.Max(dx, dy*0.9)
			if hash(x, y, 7) > 0.04+0.55*math.Pow(edge, 2.4) {
				continue
			}
			reveal := ease((n - 600*edge) / 900)
			glow := 0.22
			if hash(x, y, int(n/380)) < 0.025 {
				cv.set(x, y, '▪', mix(t.bg, t.at(float64(x)/float64(cv.w)), reveal*0.85), false)
				continue
			}
			cv.set(x, y, '▪', mix(t.bg, t.muted, reveal*glow+0.05*edge), false)
		}
	}

	if compact {
		cyTitle := max(2, cv.h/2-3)
		for y := cyTitle - 1; y <= cyTitle+5; y++ {
			for x := 0; x < cv.w; x++ {
				if c := cv.get(x, y); c != nil {
					c.ch = ' '
				}
			}
		}

		title := "◆ prism"
		tx := max(1, (cv.w-len([]rune(title)))/2)
		cv.grad(tx, cyTitle, title, t, n/3000, 10, true)

		tag := "free ai that codes, beautifully"
		fade := ease((n - 1900) / 600)
		cv.text(max(1, (cv.w-len(tag))/2), cyTitle+2, tag, mix(t.bg, t.muted, fade), false)

		motto := []rune("inspired by the best · built to be the best")
		mx := max(1, (cv.w-len(motto))/2)
		for i, r := range motto {
			reveal := ease((n - 2100 - float64(i)*22) / 260)
			if reveal <= 0 {
				break
			}
			cv.set(mx+i, cyTitle+4, r, mix(t.bg, t.at(float64(i)/float64(len(motto))*0.9+n/9000), reveal), true)
		}

		if n > 3200 && cyTitle+6 < cv.h-2 {
			pulse := 0.55 + 0.45*math.Sin(n/420)
			hint := "press any key"
			x := max(1, (cv.w-len(hint)-4)/2)
			x = cv.grad(x, cyTitle+6, "◆ ", t, n/3000, 8, false)
			cv.text(x, cyTitle+6, hint, mix(t.bg, t.fg, pulse*ease((n-3200)/500)), false)
		}
	} else {
		for y := ly - 2; y < ly+lh+6; y++ {
			for x := lx - 30; x < lx+lw+30; x++ {
				if c := cv.get(x, y); c != nil {
					c.ch = ' '
				}
			}
		}

		if lx >= 27 {
			beam := ease((n - 250) / 700)
			for i := 0; i < int(beam*24); i++ {
				cv.set(lx-27+i, ly+2, '━', mix(t.bg, t.fg, 0.35+0.65*float64(i)/24), false)
			}
		}

		shimmer := math.Mod(n/2200, 1.4)*float64(len(logo[0])+16) - 8
		for py, row := range logo {
			for px, on := range row {
				if on != '#' {
					continue
				}
				delay := 700 + hash(px, py, 3)*700 + float64(px)*14
				base := t.at(float64(px)/float64(len(row))*0.92 + 0.02)
				x, y := lx+px*2, ly+py
				switch {
				case n < delay-260:
				case n < delay:
					glyph := []rune("░▒▓")[int(hash(px, py, int(n/60))*3)]
					col := mix(t.bg, base, 0.6)
					cv.set(x, y, glyph, col, false)
					cv.set(x+1, y, glyph, col, false)
				default:
					flash := 1 - ease((n-delay)/260)
					hl := math.Exp(-math.Pow(float64(px)-shimmer, 2) / 6)
					col := mix(mix(base, t.fg, 0.55*hl), rgb{255, 255, 255}, flash*0.8)
					cv.set(x, y, '█', col, false)
					cv.set(x+1, y, '█', col, false)
				}
			}
		}

		if lx+lw+3+24 <= cv.w {
			rays := ease((n - 1500) / 700)
			for i := 0; i < lh; i++ {
				ray := t.at(float64(i) / float64(lh))
				for j := 0; j < int(rays*24); j++ {
					cv.set(lx+lw+3+j, ly+i, '━', mix(ray, t.bg, float64(j)/30), false)
				}
			}
		}

		tag := "free ai that codes, beautifully"
		fade := ease((n - 1900) / 600)
		cv.text((cv.w-len(tag))/2, ly+lh+2, tag, mix(t.bg, t.muted, fade), false)

		motto := []rune("inspired by the best · built to be the best")
		mx := (cv.w - len(motto)) / 2
		for i, r := range motto {
			reveal := ease((n - 2100 - float64(i)*22) / 260)
			if reveal <= 0 {
				break
			}
			cv.set(mx+i, ly+lh+4, r, mix(t.bg, t.at(float64(i)/float64(len(motto))*0.9+n/9000), reveal), true)
		}

		if n > 3200 {
			pulse := 0.55 + 0.45*math.Sin(n/420)
			hint := "press any key"
			x := (cv.w - len(hint) - 4) / 2
			x = cv.grad(x, ly+lh+7, "◆ ", t, n/3000, 8, false)
			cv.text(x, ly+lh+7, hint, mix(t.bg, t.fg, pulse*ease((n-3200)/500)), false)
		}

		if sy, sx := ly-10, (cv.w-26)/2; sy >= 1 && n > 2300 {
			greeting := "light in. rainbow out."
			for y := sy - 1; y < sy+8; y++ {
				for x := sx - 2; x < sx+28+len(greeting)+6; x++ {
					if c := cv.get(x, y); c != nil {
						c.ch = ' '
					}
				}
			}
			fade := ease((n - 2300) / 500)
			mini := newCanvas(cv.w, cv.h, t.bg, t.fg)
			drawShard(mini, t, sx, sy, moodIdle, n)
			for idx, c := range mini.cells {
				if c.ch != ' ' {
					cv.cells[idx] = cell{ch: c.ch, fg: mix(t.bg, c.fg, fade), bg: mix(t.bg, c.bg, fade), bold: c.bold}
				}
			}
			if n > 2900 {
				drawBubble(cv, t, sx+24, sy-1, greeting, (n-2900)/400)
			}
		}
	}

	status, dot := "looking for free models…", t.muted
	switch {
	case m.ready > 0:
		status, dot = countModels(m.ready)+" ready", t.green
	case m.ready == 0 || m.offline:
		status, dot = "no models ready · run: prism status", t.red
	}
	statusY := cv.h - 2
	if statusY >= 0 {
		themeX := max(0, cv.w-len(t.name)-3)
		themeStr := clipWidth(t.name, cv.w)
		cv.text(themeX, statusY, themeStr, mix(t.bg, t.muted, ease((n-2200)/500)), false)

		maxStatusW := themeX - 6
		if maxStatusW > 0 {
			cv.text(3, statusY, "●", mix(t.bg, dot, ease((n-2200)/500)), false)
			statusStr := clipWidth(status, maxStatusW)
			cv.text(5, statusY, statusStr, mix(t.bg, t.muted, ease((n-2200)/500)), false)
		}
	}
}
