package main

import (
	"math"
	"time"
)

const (
	sayFor  = 4500 * time.Millisecond
	beamFor = 1800 * time.Millisecond
)

func (m model) mood() mood {
	switch {
	case m.now < m.surprisedUntil:
		return moodSurprised
	case m.busy && m.thinking:
		return moodThinking
	case m.busy:
		return moodWorking
	case m.now < m.sleepyUntil:
		return moodSleepy
	case m.doneAt > 0 && m.now-m.doneAt < 3*time.Second && m.proud:
		return moodProud
	case m.doneAt > 0 && m.now-m.doneAt < 3*time.Second:
		return moodHappy
	case m.waving():
		return moodHappy
	}
	return moodIdle
}

func (m *model) speak(text string) {
	if !m.quiet {
		m.say, m.sayAt = text, m.now
	}
}

func (m model) speaking() (string, float64) {
	if m.say == "" || m.now-m.sayAt > sayFor {
		return "", 0
	}
	return m.say, ms(m.now-m.sayAt) / 350
}

func (m model) waving() bool { return m.chat && m.now-m.chatAt < 2200*time.Millisecond }

func (m model) pose() buddyPose {
	p := buddyPose{md: m.mood(), wave: m.waving()}
	if m.busy {
		p.charge = math.Min(1, (m.now-m.busySince).Seconds()/20)
	}
	if m.now-m.lastTextAt < 700*time.Millisecond {
		p.look = -1
	}
	return p
}

func (m model) animating() bool {
	return math.Abs(float64(m.scroll)-m.scrollPos) > 0.05 || m.now-m.sayAt < 500*time.Millisecond || m.waving() || m.now < m.surprisedUntil ||
		(m.beamAt > 0 && m.now-m.beamAt < beamFor) ||
		(m.doneAt > 0 && m.now-m.doneAt < 3*time.Second)
}

func (m *model) greet() {
	hour := time.Now().Hour()
	switch {
	case hash(int(m.start.UnixNano()%100000), 7, 1) < 0.05:
		m.speak("something stirs deep below.")
	case hour < 5:
		m.speak("the moon's out.")
	}
}

func (m *model) finish(ok bool, busy bool) {
	switch {
	case ok && m.turnTools > 0:
		m.doneAt = m.now
		if m.beamOn {
			m.beamAt = m.now
		}
		m.proud = m.turnTests
		if m.turnTests {
			m.speak("green across the spectrum.")
		} else {
			m.speak("converged.")
		}
	case ok:
		m.doneAt, m.proud = m.now, false
	case busy:
		m.sleepyUntil = m.now + 8*time.Second
		m.speak("all lenses fogged. give it a minute.")
	default:
		m.speak("that one didn't refract.")
	}
}

func (m model) buddyWide(w int) bool { return (w-m.column())/2 >= 17 }

// shardSize is Shard's size in cells: two pixel rows per cell.
func shardSize() (w, h int) { return len(shardBody[0]), (len(shardBody) + 1) / 2 }

// buddyGap is the room between the chat column and Shard beside it. The
// browser keeps about 9 columns; a tight terminal gives up to 2.
func buddyGap(w, colW int) int {
	sw, _ := shardSize()
	return clamp((w-colW)/2-sw-6, 2, 9)
}

// buddySpot is where Shard sits, the same place as in the browser: in the
// right margin with its feet on the input line, or on a narrow screen just
// above the input box's right end. ok is false when Shard would cover chat
// text or an open overlay there.
func (m model) buddySpot(cv *canvas, t theme, cx, colW, boxY int) (x, y int, ok bool) {
	sw, sh := shardSize()
	if m.noBuddy {
		return 0, 0, false
	}
	if m.buddyWide(cv.w) {
		return cx + colW + buddyGap(cv.w, colW), cv.h - 4 - sh, true
	}
	if m.menu.open || m.fb.open || len(m.suggesting().rows) > 0 {
		return 0, 0, false
	}
	x, y = cx+colW-1-sw, boxY-sh
	for row := y - 3; row < y+sh; row++ {
		for col := x; col < cx+colW; col++ {
			if c := cv.get(col, row); c == nil || c.ch != ' ' || c.bg != t.bg {
				return 0, 0, false
			}
		}
	}
	return x, y, true
}

func drawBuddy(cv *canvas, t theme, m model, cx, colW, boxY int) {
	n := ms(m.now)
	sx, sy, shown := m.buddySpot(cv, t, cx, colW, boxY)
	bx, by := cx+colW-1, boxY+1
	if shown {
		bx, by = sx-1, sy+2
	}
	beaming := m.beamOn && m.beamAt > 0 && m.now-m.beamAt < beamFor
	if beaming {
		drawBeam(cv, t, bx, by, -1, ms(m.now-m.beamAt))
	}
	if shown {
		pose := m.pose()
		if beaming {
			pose.md = moodWorking
			pose.charge = 1
		}
		drawShardSmall(cv, t, sx, sy, pose, n)
		drawSpeech(cv, t, m, cx, colW, sx, sy)
	}
	if beaming {
		cv.shake(beamShake(ms(m.now - m.beamAt)))
	}
}

func drawSpeech(cv *canvas, t theme, m model, cx, colW, sx, sy int) {
	text, reveal := m.speaking()
	if text == "" {
		return
	}
	w := len([]rune(text)) + 4
	x := min(sx+4, cv.w-w-2)
	if x < cx+colW-2 {
		return
	}
	drawBubble(cv, t, x, sy-4, text, reveal)
}

// crewReact is Shard's reaction to the crew events: the busy mood already
// reads as working while the crew runs.
func (m *model) crewReact(event any) {
	switch event.(type) {
	case evCrewStart:
		m.speak("splitting the light.")
	case evCrewDone:
		m.speak("converged.")
	}
}
