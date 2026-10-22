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
	return m.now-m.sayAt < 500*time.Millisecond || m.waving() || m.now < m.surprisedUntil ||
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

func drawBuddy(cv *canvas, t theme, m model, cx, colW, boxY int) {
	n := ms(m.now)
	wide := m.buddyWide(cv.w)
	sx, sy := cx+colW+2, boxY-1
	bx, by := cx+colW, boxY-1
	if wide {
		bx, by = sx, sy+2
	}
	beaming := m.beamOn && m.beamAt > 0 && m.now-m.beamAt < beamFor
	if beaming {
		drawBeam(cv, t, bx, by, -1, ms(m.now-m.beamAt))
	}
	if wide {
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
	x := min(sx+4, cv.w-w)
	if x < cx+colW-2 {
		return
	}
	drawBubble(cv, t, x, sy-4, text, reveal)
}
