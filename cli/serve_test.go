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

func TestHostAllowlistVariants(t *testing.T) {
	h, _, port := testServer(t)
	for _, tc := range []struct {
		name string
		host string
		want int
	}{
		{"ipv6 loopback ok", "[::1]:" + port, http.StatusOK},
		{"uppercase localhost ok", "LOCALHOST:" + port, http.StatusOK},
		{"wrong port forbidden", "127.0.0.1:1", http.StatusForbidden},
		{"no port forbidden", "127.0.0.1", http.StatusForbidden},
		{"empty host forbidden", "", http.StatusForbidden},
		{"rebind subdomain forbidden", "127.0.0.1.evil.com:" + port, http.StatusForbidden},
		{"0.0.0.0 forbidden", "0.0.0.0:" + port, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := do(t, h, http.MethodGet, "/api/info", tc.host, "", "").Code; got != tc.want {
				t.Fatalf("host %q: got %d, want %d", tc.host, got, tc.want)
			}
		})
	}
}

func TestHostRejectedBeforeAnyHandlerRuns(t *testing.T) {
	h, s, port := testServer(t)
	body := `{"text":"/usage"}`
	if got := do(t, h, http.MethodPost, "/api/command", "evil.com", "http://127.0.0.1:"+port, body).Code; got != http.StatusForbidden {
		t.Fatalf("bad host with good origin: got %d, want 403", got)
	}
	if got := do(t, h, http.MethodGet, "/api/ping", "evil.com", "", "").Code; got != http.StatusForbidden {
		t.Fatalf("ping on bad host: got %d, want 403", got)
	}
	if last, _ := s.state(); !last.IsZero() {
		t.Fatal("rejected requests must not update lastSeen")
	}
}

func TestOriginChecks(t *testing.T) {
	h, _, port := testServer(t)
	host := "127.0.0.1:" + port
	body := `{"text":"/usage"}`
	for _, tc := range []struct {
		name   string
		method string
		origin string
		want   int
	}{
		{"localhost origin ok", http.MethodPost, "http://localhost:" + port, http.StatusOK},
		{"uppercase origin ok", http.MethodPost, "HTTP://LOCALHOST:" + port, http.StatusOK},
		{"https origin forbidden", http.MethodPost, "https://127.0.0.1:" + port, http.StatusForbidden},
		{"wrong port origin forbidden", http.MethodPost, "http://127.0.0.1:1", http.StatusForbidden},
		{"origin without port forbidden", http.MethodPost, "http://127.0.0.1", http.StatusForbidden},
		{"null origin forbidden", http.MethodPost, "null", http.StatusForbidden},
		{"ipv6 origin forbidden", http.MethodPost, "http://[::1]:" + port, http.StatusForbidden},
		{"suffix trick forbidden", http.MethodPost, "http://127.0.0.1:" + port + ".evil.com", http.StatusForbidden},
		{"GET ignores origin", http.MethodGet, "http://evil.com", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := "/api/command"
			b := body
			if tc.method == http.MethodGet {
				target, b = "/api/info", ""
			}
			if got := do(t, h, tc.method, target, host, tc.origin, b).Code; got != tc.want {
				t.Fatalf("origin %q: got %d, want %d", tc.origin, got, tc.want)
			}
		})
	}
}

func TestOriginRejectionOnAllPostEndpoints(t *testing.T) {
	h, _, port := testServer(t)
	host := "127.0.0.1:" + port
	for _, p := range []string{"/api/command", "/api/chat", "/api/stop", "/api/new"} {
		t.Run(p, func(t *testing.T) {
			if got := do(t, h, http.MethodPost, p, host, "http://evil.com", `{"text":"x"}`).Code; got != http.StatusForbidden {
				t.Fatalf("POST %s evil origin: got %d, want 403", p, got)
			}
			if got := do(t, h, http.MethodPost, p, host, "", `{"text":"x"}`).Code; got != http.StatusForbidden {
				t.Fatalf("POST %s no origin: got %d, want 403", p, got)
			}
		})
	}
}

func TestSecurityHeaderAndStaticFiles(t *testing.T) {
	h, _, port := testServer(t)
	host := "127.0.0.1:" + port
	rec := do(t, h, http.MethodGet, "/", host, "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "prism") {
		t.Fatalf("GET /: got %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := do(t, h, http.MethodGet, "/missing.js", host, "", "").Code; got != http.StatusNotFound {
		t.Fatalf("GET /missing.js: got %d, want 404", got)
	}
}

func TestPublicPort(t *testing.T) {
	t.Setenv("PRISM_PUBLIC_PORT", "")
	for _, tc := range []struct{ addr, want string }{
		{"127.0.0.1:4849", "4849"},
		{"0.0.0.0:8080", "8080"},
		{"[::1]:9000", "9000"},
		{":4849", "4849"},
	} {
		if got := publicPort(tc.addr); got != tc.want {
			t.Fatalf("publicPort(%q) = %q, want %q", tc.addr, got, tc.want)
		}
	}
	t.Setenv("PRISM_PUBLIC_PORT", " 5000 ")
	if got := publicPort("0.0.0.0:4849"); got != "5000" {
		t.Fatalf("publicPort with env = %q, want 5000", got)
	}
}

func TestCommandBadRequests(t *testing.T) {
	h, _, port := testServer(t)
	host := "127.0.0.1:" + port
	origin := "http://" + host
	for _, tc := range []struct {
		name string
		body string
	}{
		{"not json", "hello"},
		{"wrong type", `{"text": 5}`},
		{"oversized body", `{"text":"` + strings.Repeat("a", 1<<17) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := do(t, h, http.MethodPost, "/api/command", host, origin, tc.body).Code; got != http.StatusBadRequest {
				t.Fatalf("got %d, want 400", got)
			}
		})
	}
	if got := do(t, h, http.MethodGet, "/api/command", host, origin, "").Code; got != http.StatusNotFound {
		t.Fatalf("GET /api/command: got %d, want 404 (file server, never the command handler)", got)
	}
}

func TestCommandEmptyAndUnknown(t *testing.T) {
	h, _, port := testServer(t)
	host := "127.0.0.1:" + port
	origin := "http://" + host
	post := func(text string) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"text": text})
		rec := do(t, h, http.MethodPost, "/api/command", host, origin, string(body))
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %q: got %d", text, rec.Code)
		}
		return decode(t, rec)
	}
	for _, text := range []string{"", "   ", "\n\t"} {
		res := post(text)
		if res["kind"] != "error" || res["text"] != "empty command" {
			t.Fatalf("%q: got %v", text, res)
		}
	}
	res := post("/nope arg")
	if res["kind"] != "error" || res["headline"] != "unknown command" || !strings.Contains(res["text"].(string), "/nope") {
		t.Fatalf("unknown command: got %v", res)
	}
	// A bare word without a slash is not a command either.
	if res := post("usage"); res["kind"] != "error" {
		t.Fatalf("bare word: got %v", res)
	}
}

func TestCommandModelsListsRoutes(t *testing.T) {
	h, _, port := testServer(t)
	host := "127.0.0.1:" + port
	origin := "http://" + host
	for _, cmd := range []string{"/models", "/model", "/status"} {
		body, _ := json.Marshal(map[string]string{"text": cmd})
		res := decode(t, do(t, h, http.MethodPost, "/api/command", host, origin, string(body)))
		if res["kind"] != "info" {
			t.Fatalf("%s: kind = %v, want info", cmd, res["kind"])
		}
		lines, _ := res["lines"].([]any)
		var joined []string
		for _, l := range lines {
			joined = append(joined, l.(string))
		}
		all := strings.Join(joined, "\n")
		if !strings.Contains(all, "ready") || !strings.Contains(all, "resting 12s") {
			t.Fatalf("%s: lines missing route states: %q", cmd, all)
		}
	}
}

func TestCommandModelsRouterDown(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	agent := newAgent("http://127.0.0.1:1", t.TempDir(), "prism")
	s := newServer(agent, t.TempDir(), "prism")
	h := s.handler("4849")
	host := "127.0.0.1:4849"
	res := decode(t, do(t, h, http.MethodPost, "/api/command", host, "http://"+host, `{"text":"/models"}`))
	if res["kind"] != "error" {
		t.Fatalf("router down: kind = %v, want error", res["kind"])
	}
}

func TestCommandBusyRefusals(t *testing.T) {
	h, s, port := testServer(t)
	host := "127.0.0.1:" + port
	origin := "http://" + host
	s.mu.Lock()
	s.busy = true
	s.mu.Unlock()
	for _, text := range []string{"/init", "/resume", "/continue", "/memory", "/forget", "/skills new demo"} {
		body, _ := json.Marshal(map[string]string{"text": text})
		res := decode(t, do(t, h, http.MethodPost, "/api/command", host, origin, string(body)))
		if res["kind"] != "error" || !strings.Contains(res["text"].(string), "wait until prism finishes") {
			t.Fatalf("%s while busy: got %v", text, res)
		}
	}
	// Read-only commands still work while busy.
	for _, text := range []string{"/usage", "/skills"} {
		body, _ := json.Marshal(map[string]string{"text": text})
		res := decode(t, do(t, h, http.MethodPost, "/api/command", host, origin, string(body)))
		if res["kind"] != "info" {
			t.Fatalf("%s while busy: got %v", text, res)
		}
	}
}

func TestCommandResumeEmptyAndAliases(t *testing.T) {
	h, _, port := testServer(t)
	host := "127.0.0.1:" + port
	origin := "http://" + host
	for _, text := range []string{"/resume", "/continue"} {
		body, _ := json.Marshal(map[string]string{"text": text})
		res := decode(t, do(t, h, http.MethodPost, "/api/command", host, origin, string(body)))
		if res["kind"] != "info" || !strings.Contains(res["title"].(string), "no earlier chats") {
			t.Fatalf("%s with no sessions: got %v", text, res)
		}
	}
	for _, text := range []string{"/resume 0", "/resume -1", "/resume abc", "/resume 1"} {
		body, _ := json.Marshal(map[string]string{"text": text})
		res := decode(t, do(t, h, http.MethodPost, "/api/command", host, origin, string(body)))
		if res["kind"] != "error" {
			t.Fatalf("%s with no sessions: got %v", text, res)
		}
	}
}

func TestCommandUsageReportsThisChat(t *testing.T) {
	h, s, port := testServer(t)
	host := "127.0.0.1:" + port
	s.mu.Lock()
	s.requests = 3
	s.mu.Unlock()
	res := decode(t, do(t, h, http.MethodPost, "/api/command", host, "http://"+host, `{"text":"/usage"}`))
	lines, _ := res["lines"].([]any)
	if len(lines) != 2 || !strings.Contains(lines[1].(string), "this chat: 3 requests") {
		t.Fatalf("usage lines: %v", lines)
	}
}
