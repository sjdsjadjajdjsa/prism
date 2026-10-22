package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

	m := &model{agent: newAgent(router.URL+"/v1", cfg, "handoff-test")}
	m.startWeb()
	if m.web == nil {
		t.Fatalf("startWeb did not start a web session: entries %v", m.entries)
	}

	// GET /api/info must advertise handoff:true.
	res, err := http.Get(m.web.url + "/api/info")
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
}
