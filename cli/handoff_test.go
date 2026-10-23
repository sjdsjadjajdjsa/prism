package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestHandoff drives the browser handoff end to end: startWeb opens the local
// web server, /api/info advertises the handoff, a heartbeat ping keeps the
// session alive, an idle browser hands control back to the terminal, and
// endWeb shuts the server down for good.
func TestHandoff(t *testing.T) {
	cfg := testEnv(t)

	// Fake router so /api/info's route fetch has somewhere to talk to.
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/status") {
			w.Write([]byte(`[{"route":"fake-model","ready":true}]`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(router.Close)

	// Web files: PRISM_WEB must point at a dir containing index.html.
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("<h1>prism</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRISM_WEB", web)

	// Find a free port, close it, and hand it to startWeb via PRISM_WEB_LISTEN.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	t.Setenv("PRISM_WEB_LISTEN", addr)

	m := &model{tab: &tab{agent: newAgent(router.URL+"/v1", cfg, "handoff-test")}}
	m.startWeb()
	if m.web == nil {
		t.Fatalf("startWeb did not start a web session: entries %v", m.entries)
	}
	first := m.web
	url := first.url
	oldToken := first.openID.Load()
	m.command("/web")
	if m.web != first || m.web.url != url || m.web.openID.Load() == oldToken {
		t.Fatal("/web did not reuse the server and request another browser open")
	}
	res, err := http.Get(url + "/api/handoff-open")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || string(body) == "" {
		t.Fatalf("browser open token: %s, %v", body, err)
	}

	// GET /api/info must advertise handoff:true.
	res, err = http.Get(m.web.url + "/api/info")
	if err != nil {
		t.Fatalf("GET /api/info: %v", err)
	}
	var info struct {
		Handoff bool `json:"handoff"`
	}
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		res.Body.Close()
		t.Fatalf("decode /api/info: %v", err)
	}
	res.Body.Close()
	if !info.Handoff {
		t.Fatal("/api/info returned handoff:false, want true")
	}
	m.chat = true
	if _, cmd := m.key(tea.KeyPressMsg{Code: 'o', Text: "o"}); cmd != nil || first.openID.Load() == oldToken+1 {
		t.Fatal("o did not request another browser open")
	}
	copyURL := m.web.url
	_, cmd := m.key(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if cmd == nil {
		t.Fatal("c did not provide a clipboard command")
	}
	t.Cleanup(func() { linkCopiedAt.Store(0) })
	var clipboard string
	var msgs []tea.Msg
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				msgs = append(msgs, c())
			}
		}
	}
	for _, msg := range msgs {
		if fmt.Sprintf("%T", msg) == "tea.setClipboardMsg" {
			clipboard = fmt.Sprint(msg)
		}
	}
	if clipboard != copyURL {
		t.Fatalf("clipboard = %q, want %q (msgs %#v)", clipboard, copyURL, msgs)
	}
	if linkCopiedAt.Load() == 0 {
		t.Fatal("copy did not record the status time")
	}
	req, err := http.NewRequest(http.MethodPost, url+"/api/handoff-open?failed=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", url)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent || !m.web.failed.Load() {
		t.Fatal("browser launch failure was not reported to the handoff")
	}

	// With no ping the browser was never seen, so checkWeb must keep the session.
	if cmd := m.checkWeb(); cmd != nil {
		t.Fatal("checkWeb ended the session before any /api/ping")
	}
	if m.web == nil {
		t.Fatal("web session is nil before any /api/ping")
	}

	// A heartbeat ping marks the browser as seen; a fresh ping keeps the session.
	pres, err := http.Get(m.web.url + "/api/ping")
	if err != nil {
		t.Fatalf("GET /api/ping: %v", err)
	}
	pres.Body.Close()
	if pres.StatusCode != http.StatusNoContent {
		t.Fatalf("/api/ping status = %d, want 204", pres.StatusCode)
	}
	if cmd := m.checkWeb(); cmd != nil {
		t.Fatal("checkWeb ended the session right after a ping")
	}

	// Simulate an idle browser: last ping and session start both 10s ago.
	m.web.started = time.Now().Add(-10 * time.Second)
	m.web.server.mu.Lock()
	m.web.server.lastSeen = time.Now().Add(-10 * time.Second)
	m.web.server.mu.Unlock()

	if cmd := m.checkWeb(); cmd != nil {
		t.Fatal("checkWeb returned an unexpected command")
	}
	if m.web != nil {
		t.Fatal("checkWeb did not end the idle session")
	}
	found := false
	for _, e := range m.entries {
		if e.kind == "note" && strings.Contains(e.text, "back in the terminal") {
			found = true
		}
	}
	if !found {
		t.Fatalf("entries missing the 'back in the terminal' note: %v", m.entries)
	}

	// The server must be shut down: a new connection is refused.
	if _, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
		t.Fatal("server still accepts connections after the session ended")
	}
	m.startWeb()
	if m.web == nil {
		t.Fatal("could not restart web after returning")
	}
	updated, cmd := m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || updated.(model).web != nil {
		t.Fatal("enter did not return to the terminal")
	}
}

// TestAgentFor verifies the process-wide agentByID map is updated when tabs
// open and close, and that agentFor is safe to call from any goroutine.
func TestAgentFor(t *testing.T) {
	cfg := testEnv(t)
	a1 := newAgent("http://x/v1", cfg, "tab-test-1")
	a2 := newAgent("http://x/v1", cfg, "tab-test-2")

	// Register two agents manually (addTab does this in production).
	registerAgent(10, a1)
	registerAgent(11, a2)
	t.Cleanup(func() { unregisterAgent(10); unregisterAgent(11) })

	if got := agentFor(10); got != a1 {
		t.Errorf("agentFor(10) = %p, want %p", got, a1)
	}
	if got := agentFor(11); got != a2 {
		t.Errorf("agentFor(11) = %p, want %p", got, a2)
	}
	if got := agentFor(99); got != nil {
		t.Errorf("agentFor(99) = %p, want nil", got)
	}

	// Unregister one; the other must still be reachable.
	unregisterAgent(10)
	if got := agentFor(10); got != nil {
		t.Errorf("agentFor(10) after unregister = %p, want nil", got)
	}
	if got := agentFor(11); got != a2 {
		t.Errorf("agentFor(11) after unregister of 10 = %p, want %p", got, a2)
	}
}

func canvasText(cv *canvas) string {
	var b strings.Builder
	for _, c := range cv.cells {
		if c.ch != 0 {
			b.WriteRune(c.ch)
		}
	}
	return b.String()
}

func TestHandoffCanvasLayout(t *testing.T) {
	url := "http://127.0.0.1:4849"
	labels := []string{"continuing in your browser", url, "if the page didn't open", "o open again", "press enter here to come back"}
	for _, size := range [][2]int{{80, 24}, {100, 30}, {140, 40}, {60, 18}, {60, 17}, {60, 16}, {36, 10}} {
		for _, now := range []time.Duration{0, time.Second} {
			w, h := size[0], size[1]
			t.Run(fmt.Sprintf("%dx%d/%s", w, h, now), func(t *testing.T) {
				cv := newCanvas(w, h, themes[0].bg, themes[0].fg)
				drawHandoff(cv, themes[0], model{web: &webSession{url: url, server: &server{}}, now: now})
				first, last, shardRows := h, -1, 0
				rows := make([]string, h)
				for y := 0; y < h; y++ {
					var row strings.Builder
					for _, c := range cv.cells[y*w : (y+1)*w] {
						if c.ch != 0 {
							row.WriteRune(c.ch)
						}
					}
					rows[y] = row.String()
					if strings.ContainsRune(rows[y], '█') {
						shardRows++
					}
					if strings.TrimSpace(rows[y]) != "" {
						first = min(first, y)
						last = y
					}
				}
				for _, label := range labels {
					found := false
					for _, row := range rows {
						found = found || strings.Contains(row, label)
					}
					if !found {
						t.Errorf("missing %q", label)
					}
				}
				if h >= 18 && shardRows != len(shardBody) {
					t.Errorf("Shard occupies %d rows, want %d", shardRows, len(shardBody))
				}
				if h < 18 && shardRows != 0 {
					t.Errorf("Shard overflows small screen on %d rows", shardRows)
				}
				delta := first + last - (h - 1)
				if first < 0 || last >= h || delta < -2 || delta > 2 {
					t.Errorf("occupied rows %d..%d not centred in height %d", first, last, h)
				}
			})
		}
	}
}

func TestHandoffCanvas(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	url := "http://127.0.0.1:4849"
	w := &webSession{url: url, server: &server{}}
	m := model{web: w, w: 80, h: 24}
	cv := newCanvas(m.w, m.h, themes[0].bg, themes[0].fg)
	drawHandoff(cv, themes[0], m)
	view := cv.String()
	if !strings.Contains(view, "\x1b]8;;"+url+"\x1b\\") || !strings.Contains(view, "\x1b]8;;\x1b\\") {
		t.Fatal("browser link is missing OSC 8 boundaries")
	}
	for _, label := range []string{"continuing in your browser", "o open again", "c copy link", "press enter here to come back"} {
		if !strings.Contains(canvasText(cv), label) {
			t.Fatalf("handoff is missing %q", label)
		}
	}
	w.failed.Store(true)
	cv = newCanvas(80, 24, themes[0].bg, themes[0].fg)
	drawHandoff(cv, themes[0], m)
	if !strings.Contains(canvasText(cv), "couldn't open your browser, copy the link above") {
		t.Fatal("failed browser launch does not show the copy hint")
	}
	w.failed.Store(false)
	linkCopiedAt.Store(time.Now().UnixNano())
	cv = newCanvas(80, 24, themes[0].bg, themes[0].fg)
	drawHandoff(cv, themes[0], m)
	if !strings.Contains(canvasText(cv), "link copied · if nothing was copied, select the link above") {
		t.Fatal("copy did not show the status line")
	}
	linkCopiedAt.Store(0)
	t.Setenv("TERM", "dumb")
	if strings.Contains(cv.String(), "\x1b]8;") || !strings.Contains(cv.String(), url) {
		t.Fatal("plain terminal must display the url without OSC 8")
	}
	m.w, m.h = 36, 10
	cv = newCanvas(m.w, m.h, themes[0].bg, themes[0].fg)
	drawHandoff(cv, themes[0], m)
	if !strings.Contains(canvasText(cv), "press enter here to come back") {
		t.Fatal("small handoff lost its return hint")
	}
}

func TestCanvasLink(t *testing.T) {
	fg, bg := themes[0].fg, themes[0].bg
	cv := newCanvas(10, 1, bg, fg)
	cv.link(0, 0, "a世b", "http://x", fg, false)
	for i := 0; i < 4; i++ {
		if cv.cells[i].url != "http://x" {
			t.Fatalf("cell %d has no url (wide char must cover both cells)", i)
		}
	}
	if cv.cells[4].url != "" {
		t.Fatal("url leaked past the text")
	}
	for _, bad := range []string{"http://x\x1b\\evil", "http://x\x07", "http://x\x7f", "http://x\n"} {
		cv = newCanvas(10, 1, bg, fg)
		cv.link(0, 0, "ab", bad, fg, false)
		if cv.cells[0].url != "" || cv.cells[1].url != "" || cv.cells[0].ch != 'a' {
			t.Fatalf("url %q was not ignored", bad)
		}
	}
}
