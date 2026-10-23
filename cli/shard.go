package main

import (
	"math"
	"strings"
)

type mood int

const (
	moodIdle mood = iota
	moodThinking
	moodWorking
	moodHappy
	moodSleepy
	moodSurprised
	moodProud
)

var shardBody = []string{
	"......#......",
	".....###.....",
	"....#####....",
	"...#######...",
	"..#########..",
	".###########.",
	"#############",
}

const (
	shardEyeRow   = 4
	shardMouthRow = 5
)

type pixel struct {
	on  bool
	col rgb
}

func shardPixels(t theme, md mood, n, charge float64) [][]pixel {
	h, w := len(shardBody), len(shardBody[0])
	center := w / 2
	white := rgb{255, 255, 255}
	grid := make([][]pixel, h)
	glow := 0.0
	switch md {
	case moodWorking:
		glow = 0.18 + 0.12*math.Sin(n/140)
	case moodThinking:
		glow = 0.06
	case moodProud:
		glow = 0.12 + 0.08*math.Sin(n/200)
	}
	glow += charge * (0.22 + 0.06*math.Sin(n/90))
	dim := 0.0
	if md == moodSleepy {
		dim = 0.5
	}
	speed := 1700.0
	if md == moodThinking || md == moodWorking {
		speed = 900
	}
	band := math.Mod(n/speed, 1.8)*float64(w+h) - float64(h)
	for y, row := range shardBody {
		grid[y] = make([]pixel, w)
		for x, ch := range row {
			if ch != '#' {
				continue
			}
			edge := y == h-1 || x == 0 || row[x-1] == '.' || x+1 >= w || row[x+1] == '.'
			apex := y == 0
			base := t.at(float64(x)/float64(w)*0.68 + float64(y)/float64(h)*0.1 + math.Sin(n/9000)*0.04)
			var col rgb
			switch {
			case apex:
				col = mix(base, white, 0.75)
			case x == center:
				col = mix(base, white, 0.45)
			case x < center:
				col = mix(base, white, 0.22+0.04*float64(center-x)/float64(center))
			default:
				col = mix(base, t.bg, 0.12+0.18*float64(x-center)/float64(center))
			}
			if edge && !apex {
				col = mix(col, white, 0.18)
			}
			beam := math.Exp(-math.Pow(float64(x)-float64(y)*0.6-band, 2) / 2.5)
			col = mix(mix(col, white, beam*0.3), white, glow)
			grid[y][x] = pixel{true, mix(col, t.bg, dim)}
		}
	}
	return grid
}

func eyeGlyphs(md mood, n float64) (rune, int) {
	blink := math.Mod(n, 4200) < 260
	switch {
	case md == moodSurprised:
		return '█', 0
	case md == moodSleepy:
		return '▄', 0
	case md == moodProud:
		return '▀', 0
	case md == moodHappy:
		return '▀', 0
	case blink:
		return '▄', 0
	case md == moodThinking:
		return '█', int(math.Round(math.Sin(n/700) * 1))
	}
	return '█', 0
}

func drawShard(cv *canvas, t theme, x, y int, md mood, n float64) {
	bob := 0
	if md != moodSleepy && math.Sin(n/650) > 0.55 {
		bob = -1
	}
	y += bob
	grid := shardPixels(t, md, n, 0)
	for py, row := range grid {
		for px, p := range row {
			if p.on {
				cv.set(x+px*2, y+py, '█', p.col, false)
				cv.set(x+px*2+1, y+py, '█', p.col, false)
			}
		}
	}
	dark := mix(t.bg, rgb{0, 0, 0}, 0.3)
	eye, look := eyeGlyphs(md, n)
	for _, ex := range []int{4, 8} {
		cx := x + (ex+look)*2
		if c := cv.get(cx, y+shardEyeRow); c != nil {
			c.bg = cv.get(cx, y+shardEyeRow).fg
		}
		cv.set(cx, y+shardEyeRow, eye, dark, false)
		if c := cv.get(cx+1, y+shardEyeRow); c != nil {
			c.bg = c.fg
		}
		cv.set(cx+1, y+shardEyeRow, eye, dark, false)
	}
	mouth := map[mood]string{moodIdle: "▄  ▄", moodThinking: "▄▄", moodWorking: "▄  ▄", moodHappy: "▀▄▄▀", moodSleepy: "▄▄", moodSurprised: "██", moodProud: "▀▄▄▀"}[md]
	mx := x + 6*2 + 1 - len([]rune(mouth))/2
	cell := 0
	for _, r := range mouth {
		if r != ' ' {
			if c := cv.get(mx+cell, y+shardMouthRow); c != nil {
				c.bg = c.fg
				cv.set(mx+cell, y+shardMouthRow, r, dark, false)
			}
		}
		cell++
	}
	switch md {
	case moodSleepy:
		phase := math.Mod(n/900, 3)
		for i := 0; i < 3; i++ {
			if float64(i) <= phase {
				cv.set(x+27+i*2, y-i, 'z', mix(t.bg, t.muted, 0.9-0.2*float64(i)), false)
			}
		}
	case moodHappy, moodProud:
		for i := 0; i < 6; i++ {
			a := float64(i)/6*2*math.Pi + n/900
			sx, sy := x+13+int(math.Cos(a)*17), y+3+int(math.Sin(a)*4.5)
			if c := cv.get(sx, sy); c != nil && c.ch == ' ' && hash(i, int(n/200), 9) > 0.35 {
				cv.set(sx, sy, []rune("✦·✧")[i%3], t.at(float64(i)/6), false)
			}
		}
	}
}

type buddyPose struct {
	md     mood
	charge float64
	look   int
	wave   bool
}

func drawShardSmall(cv *canvas, t theme, x, y int, p buddyPose, n float64) {
	grid := shardPixels(t, p.md, n, p.charge)
	for py := 0; py < len(grid); py += 2 {
		for px := range grid[py] {
			top := grid[py][px]
			var bottom pixel
			if py+1 < len(grid) {
				bottom = grid[py+1][px]
			}
			c := cv.get(x+px, y+py/2)
			if c == nil || (!top.on && !bottom.on) {
				continue
			}
			switch {
			case top.on && bottom.on:
				c.ch, c.fg, c.bg = '▀', top.col, bottom.col
			case top.on:
				c.ch, c.fg = '▀', top.col
			default:
				c.ch, c.fg = '▄', bottom.col
			}
		}
	}
	eye, drift := eyeGlyphs(p.md, n)
	look := clamp(p.look+drift, -1, 1)
	dark := mix(t.bg, rgb{0, 0, 0}, 0.3)
	if eye != '▄' || p.md == moodHappy {
		for _, ex := range []int{4, 8} {
			if c := cv.get(x+ex+look, y+2); c != nil {
				c.fg = dark
				c.ch = '▀'
				if eye == '█' && p.md == moodSurprised {
					c.ch = '█'
				}
			}
		}
	}
	if p.charge > 0.05 {
		for i := 0; i < 3; i++ {
			a := n/260 + float64(i)*2.1
			px, py := x+6+int(math.Round(math.Cos(a)*(8-p.charge*3))), y+1+int(math.Round(math.Sin(a)*1.6))
			if c := cv.get(px, py); c != nil && c.ch == ' ' {
				cv.set(px, py, '·', mix(t.bg, t.at(float64(i)/3+n/4000), 0.4+0.6*p.charge), false)
			}
		}
	}
	switch p.md {
	case moodSleepy:
		for i := 0; i < 3; i++ {
			if float64(i) <= math.Mod(n/900, 3) {
				cv.set(x+12+i, y-1-i, 'z', mix(t.bg, t.muted, 0.9-0.2*float64(i)), false)
			}
		}
	case moodHappy, moodProud:
		for i := 0; i < 5; i++ {
			a := float64(i)/5*2*math.Pi + n/700
			px, py := x+6+int(math.Cos(a)*9), y+1+int(math.Sin(a)*2.2)
			if c := cv.get(px, py); c != nil && c.ch == ' ' && hash(i, int(n/180), 9) > 0.3 {
				cv.set(px, py, []rune("✦·✧")[i%3], t.at(float64(i)/5), false)
			}
		}
	case moodSurprised:
		cv.set(x+13, y-1, '!', t.at(0.05), true)
	}
	if p.wave {
		hand := []rune("▝▗")[int(n/170)%2]
		cv.set(x+13, y+1, hand, t.at(0.15+n/6000), false)
	}
}

func drawBubble(cv *canvas, t theme, x, y int, text string, reveal float64) {
	r := []rune(text)
	shown := string(r[:int(math.Round(float64(len(r))*math.Min(1, reveal)))])
	w := len(r) + 4
	border := mix(t.bg, t.muted, 0.7)
	cv.text(x, y, "╭"+strings.Repeat("─", w-2)+"╮", border, false)
	cv.text(x, y+1, "│", border, false)
	cv.text(x+w-1, y+1, "│", border, false)
	cv.text(x+2, y+1, shown, t.fg, false)
	cv.text(x, y+2, "╰─┬"+strings.Repeat("─", w-4)+"╯", border, false)
	cv.text(x+2, y+3, "╵", border, false)
}

func drawBeam(cv *canvas, t theme, sx, sy, dir int, n float64) {
	charge, converge, fire, fade := ease(n/350), ease((n-350)/450), ease((n-800)/250), ease((n-1250)/500)
	if n < 0 || fade >= 1 {
		return
	}
	white := rgb{255, 255, 255}
	if n < 1250 {
		dim := 0.35 * charge * (1 - fire*0.4)
		for i := range cv.cells {
			c := &cv.cells[i]
			c.fg, c.bg = mix(c.fg, t.bg, dim), mix(c.bg, mix(t.bg, rgb{0, 0, 0}, 0.6), dim)
		}
	}
	if flash := 1 - math.Abs(n-820)/90; flash > 0 {
		for i := range cv.cells {
			c := &cv.cells[i]
			c.bg = mix(c.bg, mix(t.bg, white, 0.35), flash*0.5)
		}
	}
	const rays = 7
	length := float64(cv.w - sx)
	if dir < 0 {
		length = float64(sx)
	}
	dimNow := 0.0
	if n < 1250 {
		dimNow = 0.35 * charge * (1 - fire*0.4)
	}
	shade := mix(t.bg, mix(t.bg, rgb{0, 0, 0}, 0.6), dimNow)
	if flash := 1 - math.Abs(n-820)/90; flash > 0 {
		shade = mix(shade, mix(t.bg, rgb{255, 255, 255}, 0.35), flash*0.5)
	}
	if fire == 0 {
		spread := (1 - converge) * 0.5
		for i := 0; i < rays; i++ {
			off := (float64(i)/(rays-1) - 0.5) * 2
			angle := off*spread + math.Sin(n/120+float64(i))*0.02*(1-converge)
			col := mix(t.at(float64(i)/rays), t.fg, 0.2+0.5*converge)
			for d := 0; d < int(length*charge); d++ {
				x, y := sx+dir*d, sy+int(math.Round(math.Tan(angle)*float64(d)*0.5))
				if c := cv.get(x, y); c != nil {
					c.bg = mix(c.bg, shade, 0.5)
				}
				cv.set(x, y, '━', col, true)
			}
		}
		if converge > 0 {
			glowCol := mix(t.at(n/800), white, 0.6)
			for r := 0; r < 3; r++ {
				x := sx + dir*r
				if c := cv.get(x, sy); c != nil {
					c.bg = shade
				}
				cv.set(x, sy, '█', mix(shade, glowCol, converge*(1-float64(r)*0.3)), false)
			}
		}
	}
	if fire > 0 {
		for d := 0; d < int(length*fire); d++ {
			x := sx + dir*d
			half := 1 + int(fire*1.5*(1-fade))
			pulse := 0.5 + 0.5*math.Sin(float64(d)/4-n/45)
			for dy := -half; dy <= half; dy++ {
				core := 1 - math.Abs(float64(dy))/float64(half+1)
				col := mix(t.at(float64(d)/18-n/300+float64(dy)*0.05), white, core*(0.5+0.3*pulse))
				ch := '█'
				if dy == -half {
					ch = '▄'
				} else if dy == half {
					ch = '▀'
				}
				if c := cv.get(x, sy+dy); c != nil {
					c.bg = mix(shade, t.bg, fade)
				}
				cv.set(x, sy+dy, ch, mix(col, t.bg, fade), false)
			}
		}
		age := (n - 800) / 1000
		for i := 0; i < 28; i++ {
			d := hash(i, 1, 2) * length * fire
			drift := age * (2 + hash(i, 8, 3)*5)
			py := sy + int(math.Round((hash(i, 3, 7)-0.5)*4+math.Copysign(drift, hash(i, 4, 1)-0.5)))
			px := sx + dir*int(d+age*hash(i, 6, 2)*6*float64(dir))
			life := 1 - math.Max(0, age-hash(i, 9, 9)*0.6)/0.9
			if life <= 0 {
				continue
			}
			if (px-sx)*dir < 0 {
				continue
			}
			if c := cv.get(px, py); c != nil && c.ch == ' ' {
				cv.set(px, py, []rune("✦·✧•")[i%4], mix(t.bg, t.at(hash(i, 5, 6)), life), false)
			}
		}
	}
}

func beamShake(n float64) int {
	if n < 820 || n > 1060 {
		return 0
	}
	return []int{1, -1, 1, 0}[int((n-820)/60)%4]
}

func (cv *canvas) shake(dx int) {
	if dx == 0 {
		return
	}
	for y := 0; y < cv.h; y++ {
		row := cv.cells[y*cv.w : (y+1)*cv.w]
		moved := make([]cell, cv.w)
		for x := range row {
			src := x - dx
			if src < 0 || src >= cv.w {
				moved[x] = cell{ch: ' ', fg: row[x].fg, bg: row[x].bg}
				continue
			}
			moved[x] = row[src]
		}
		copy(row, moved)
	}
}

func drawShardGallery(cv *canvas, t theme, n float64, beam bool) {
	if beam {
		sx, sy := 8, cv.h/2-4
		cycle := math.Mod(n, 2600)
		drawBeam(cv, t, sx+26, sy+3, 1, cycle-300)
		md := moodWorking
		if cycle > 1500 {
			md = moodHappy
			drawBubble(cv, t, sx+2, sy-5, "converged.", (cycle-1500)/300)
		}
		drawShard(cv, t, sx, sy, md, n)
		return
	}
	moods := []struct {
		md    mood
		label string
		line  string
	}{
		{moodIdle, "idle", "light in. rainbow out."},
		{moodThinking, "thinking", "refracting…"},
		{moodWorking, "working", "focusing the beam."},
		{moodHappy, "done", "converged."},
		{moodProud, "tests pass", "green across the spectrum."},
		{moodSurprised, "step failed", "hm. a crack in the lens."},
		{moodSleepy, "all models busy", "all lenses fogged."},
	}
	colW := cv.w / 4
	for i, m := range moods {
		x := (i%4)*colW + (colW-26)/2
		y := 1 + (i/4)*16
		drawBubble(cv, t, x, y, m.line, 1)
		drawShard(cv, t, x, y+5, m.md, n+float64(i)*700)
		cv.text(x+13-len(m.label)/2, y+14, m.label, t.muted, false)
	}
	small := []buddyPose{{md: moodHappy, wave: true}, {md: moodWorking, charge: 0.3}, {md: moodWorking, charge: 1}, {md: moodThinking, look: -1}, {md: moodProud}, {md: moodSurprised}, {md: moodSleepy}}
	names := []string{"hello", "working", "charged", "reading", "proud", "oops", "sleepy"}
	cv.text(4, cv.h-9, "small size, beside the input box", t.muted, false)
	for i, p := range small {
		x := 6 + i*(cv.w-12)/len(small)
		drawShardSmall(cv, t, x, cv.h-7, p, n)
		cv.text(x+6-len(names[i])/2, cv.h-1, names[i], mix(t.bg, t.muted, 0.8), false)
	}
}
