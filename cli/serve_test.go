package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testServer builds an Agent whose router is a fake /status endpoint, wires it
// into newServer on a temp web dir with an index.html, and returns the
// request-wrapped handler plus the public port.
func testServer(t *testing.T) (http.Handler, *server, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode([]routeRow{
			{Route: "fake/model-a:free", Ready: true},
			{Route: "fake/model-b:free", Ready: false, RetryIn: 12},
		})
	}))
	t.Cleanup(router.Close)

	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("<!doctype html>prism"), 0o644); err != nil {
		t.Fatal(err)
	}

	agent := newAgent(router.URL, t.TempDir(), "prism")
	s := newServer(agent, web, "prism")
	return s.handler("4849"), s, "4849"
}

func do(t *testing.T, h http.Handler, method, target, host, origin string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestHostAllowlist(t *testing.T) {
	h, _, port := testServer(t)
	good := "127.0.0.1:" + port
	for _, tc := range []struct {
		name string
		host string
		want int
	}{
		{"loopback ok", good, http.StatusOK},
		{"localhost ok", "localhost:" + port, http.StatusOK},
		{"evil.com forbidden", "evil.com", http.StatusForbidden},
		{"evil loopback port forbidden", "evil.com:" + port, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := do(t, h, http.MethodGet, "/", tc.host, "", "").Code; got != tc.want {
				t.Fatalf("GET / host %q: got %d, want %d", tc.host, got, tc.want)
			}
		})
	}
}

func TestPostOrigin(t *testing.T) {
	h, _, port := testServer(t)
	host := "127.0.0.1:" + port
	body := `{"text":"/usage"}`
	if got := do(t, h, http.MethodPost, "/api/command", host, "", body).Code; got != http.StatusForbidden {
		t.Fatalf("POST without Origin: got %d, want 403", got)
	}
	if got := do(t, h, http.MethodPost, "/api/command", host, "http://evil.com", body).Code; got != http.StatusForbidden {
		t.Fatalf("POST with evil Origin: got %d, want 403", got)
	}
	rec := do(t, h, http.MethodPost, "/api/command", host, "http://127.0.0.1:"+port, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST with loopback Origin: got %d, want 200", rec.Code)
	}
}

func TestPingUpdatesLastSeen(t *testing.T) {
	h, s, port := testServer(t)
	host := "127.0.0.1:" + port

	if last, _ := s.state(); !last.IsZero() {
		t.Fatal("lastSeen should start zero")
	}
	if got := do(t, h, http.MethodGet, "/api/info", host, "", "").Code; got != http.StatusOK {
		t.Fatalf("GET /api/info: got %d", got)
	}
	if last, _ := s.state(); !last.IsZero() {
		t.Fatal("GET /api/info must not update lastSeen")
	}
	if got := do(t, h, http.MethodGet, "/api/ping", host, "", "").Code; got != http.StatusNoContent {
		t.Fatalf("GET /api/ping: got %d, want 204", got)
	}
	last, _ := s.state()
	if last.IsZero() {
		t.Fatal("GET /api/ping must update lastSeen")
	}
}

func TestCommand(t *testing.T) {
	h, _, port := testServer(t)
	host := "127.0.0.1:" + port
	origin := "http://" + host
	post := func(text string) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"text": text})
		rec := do(t, h, http.MethodPost, "/api/command", host, origin, string(body))
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %q: got %d: %s", text, rec.Code, rec.Body.String())
		}
		return decode(t, rec)
	}

	if got := post("/usage"); got["kind"] != "info" {
		t.Fatalf("/usage kind = %v, want info", got["kind"])
	}
	res := post("/resume")
	if res["kind"] != "info" {
		t.Fatalf("/resume kind = %v, want info", res["kind"])
	}
	res = post("/resume 99")
	if res["kind"] != "error" {
		t.Fatalf("/resume 99 kind = %v, want error", res["kind"])
	}
	res = post("/init")
	if res["kind"] != "send" {
		t.Fatalf("/init kind = %v, want send", res["kind"])
	}
	if res["message"] != initPrompt {
		t.Fatalf("/init message mismatch")
	}
	res = post("/nope")
	if res["kind"] != "error" {
		t.Fatalf("/nope kind = %v, want error", res["kind"])
	}
	res = post("/skills")
	if res["kind"] != "info" {
		t.Fatalf("/skills kind = %v, want info", res["kind"])
	}
}

func TestInfoIncludesHandoff(t *testing.T) {
	h, s, port := testServer(t)
	host := "127.0.0.1:" + port
	get := func() map[string]any {
		rec := do(t, h, http.MethodGet, "/api/info", host, "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/info: got %d", rec.Code)
		}
		return decode(t, rec)
	}

	res := get()
	if _, ok := res["handoff"]; !ok {
		t.Fatal("/api/info must include handoff")
	}
	if res["handoff"] != false {
		t.Fatalf("handoff = %v, want false", res["handoff"])
	}
	if res["theme"] != "prism" {
		t.Fatalf("theme = %v, want prism", res["theme"])
	}
	if res["models_ready"] != float64(1) {
		t.Fatalf("models_ready = %v, want 1", res["models_ready"])
	}

	s.mu.Lock()
	s.handoff = true
	s.mu.Unlock()
	if res := get(); res["handoff"] != true {
		t.Fatalf("handoff = %v, want true", res["handoff"])
	}
}
