package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-runewidth"
)

const handoffIdle = 8 * time.Second

type webSession struct {
	srv     *http.Server
	server  *server
	url     string
	started time.Time
	openID  atomic.Uint64
	failed  atomic.Bool
}

func (m *model) startWeb() {
	if m.web != nil {
		m.web.started = time.Now()
		m.web.failed.Store(false)
		m.web.openID.Add(1)
		return
	}
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
	w := &webSession{server: s, url: "http://127.0.0.1:" + port, started: time.Now()}
	w.openID.Store(uint64(time.Now().UnixNano()))
	next := s.handler(port)
	handler := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/handoff-open" {
			switch r.Method {
			case http.MethodGet:
				fmt.Fprint(rw, w.openID.Load())
			case http.MethodPost:
				w.failed.Store(r.URL.Query().Get("failed") == "1")
				rw.WriteHeader(http.StatusNoContent)
			default:
				rw.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		next.ServeHTTP(rw, r)
	})
	w.srv = &http.Server{Handler: localOnly(port, handler)}
	go w.srv.Serve(ln)
	m.web = w
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

// linkCopiedAt is when the last "copy link" command ran (unix nanoseconds).
var linkCopiedAt atomic.Int64

const linkCopiedFor = 6 * time.Second

// copyWebLink asks the terminal to put url on the clipboard (OSC 52 through
// bubbletea) and notes the time so the handoff screen can say so.
func copyWebLink(url string) tea.Cmd {
	return tea.Batch(tea.SetClipboard(url), func() tea.Msg {
		linkCopiedAt.Store(time.Now().UnixNano())
		return nil
	})
}

func handoffLine(cv *canvas, y int, s string, color rgb, bold bool, url string) {
	s = runewidth.Truncate(s, max(0, cv.w-2), "…")
	x := (cv.w - runewidth.StringWidth(s)) / 2
	if url != "" {
		cv.link(x, y, s, url, color, bold)
	} else {
		cv.text(x, y, s, color, bold)
	}
}

func drawHandoff(cv *canvas, t theme, m model) {
	n := ms(m.now)
	w := m.web
	cy := (max(0, cv.h-7))/2 - 1
	if cv.h >= 18 && cv.w >= 26 {
		cy = cv.h/2 + 1
		drawShard(cv, t, (cv.w-26)/2, cy-9, moodIdle, n)
	}
	title := runewidth.Truncate("continuing in your browser", max(0, cv.w-2), "…")
	cv.grad((cv.w-runewidth.StringWidth(title))/2, cy+1, title, t, n/5000, 18, true)
	handoffLine(cv, cy+3, w.url, t.at(0.6), true, w.url)
	status := "if the page didn't open, copy this link into your browser"
	if w.failed.Load() {
		status = "couldn't open your browser, copy the link above"
	} else if at := linkCopiedAt.Load(); at != 0 && time.Since(time.Unix(0, at)) < linkCopiedFor {
		status = "link copied · if nothing was copied, select the link above"
	} else if seen, busy := w.server.state(); !seen.IsZero() {
		status = "the browser is connected"
		if busy {
			status = "prism is working in the browser"
		}
	}
	handoffLine(cv, cy+5, status, t.muted, false, "")
	handoffLine(cv, cy+6, "o open again  ·  c copy link", t.muted, false, "")
	handoffLine(cv, cy+7, "press enter here to come back", t.muted, false, "")
}
