package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
)

const handoffIdle = 8 * time.Second

type webSession struct {
	srv     *http.Server
	server  *server
	url     string
	started time.Time
}

func (m *model) startWeb() {
	dir := webDir()
	if dir == "" {
		m.entries = append(m.entries, &entry{kind: "error", text: "the browser page files are missing · reinstall prism, then try /web again"})
		return
	}
	addr := os.Getenv("PRISM_WEB_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:4849"
	}
	port := publicPort(addr)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		m.entries = append(m.entries, &entry{kind: "error", text: "couldn't open the web page: " + err.Error(), arg: "prism is already open in a browser somewhere · close that tab and try /web again"})
		return
	}
	s := newServer(m.agent, dir, themes[m.theme].name)
	s.handoff = true
	srv := &http.Server{Handler: s.handler(port)}
	go srv.Serve(ln)
	m.web = &webSession{srv: srv, server: s, url: "http://127.0.0.1:" + port, started: time.Now()}
	m.speak("see you in the browser.")
}

func (m *model) checkWeb() tea.Cmd {
	if m.web == nil {
		return nil
	}
	seen, busy := m.web.server.state()
	if !seen.IsZero() && !busy && time.Since(seen) > handoffIdle && time.Since(m.web.started) > handoffIdle {
		return m.endWeb()
	}
	return nil
}

func (m *model) endWeb() tea.Cmd {
	w := m.web
	if w == nil {
		return nil
	}
	m.web = nil
	w.server.stopRun()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := w.srv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		w.srv.Close()
	}
	m.agent.messages[0].Content = m.agent.systemPrompt()
	m.entries = append(entriesFrom(m.agent.messages), &entry{kind: "note", text: "back in the terminal · everything you did in the browser is here"})
	m.tokens, m.scroll, m.scrollPos = estimate(m.agent.messages), 0, 0
	m.speak("welcome back.")
	return nil
}

func drawHandoff(cv *canvas, t theme, m model) {
	n := ms(m.now)
	w := m.web
	cy := cv.h/2 + 1
	drawShard(cv, t, (cv.w-26)/2, cy-9, moodIdle, n)
	title := "continuing in your browser"
	cv.grad((cv.w-len(title))/2, cy+1, title, t, n/5000, 18, true)
	cv.text((cv.w-len(w.url))/2, cy+3, w.url, t.at(0.6), true)
	lines := []string{"if the page didn't open, copy this link into your browser", "", "press enter here to come back, or just close the browser tab"}
	if seen, busy := w.server.state(); !seen.IsZero() {
		lines[0] = "the browser is connected"
		if busy {
			lines[0] = "prism is working in the browser"
		}
	}
	for i, l := range lines {
		cv.text((cv.w-len([]rune(l)))/2, cy+5+i, l, t.muted, false)
	}
}
