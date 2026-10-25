package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	if res["kind"] != "view" {
		t.Fatalf("/skills kind = %v, want view", res["kind"])
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
	for _, p := range []string{"/api/command", "/api/chat", "/api/stop", "/api/new", "/api/approve"} {
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
	for _, cmd := range []string{"/models", "/status"} {
		body, _ := json.Marshal(map[string]string{"text": cmd})
		res := decode(t, do(t, h, http.MethodPost, "/api/command", host, origin, string(body)))
		if res["kind"] != "table" || res["title"] != "models, in the order prism tries them" {
			t.Fatalf("%s: got %v", cmd, res)
		}
		all := fmt.Sprint(res["rows"])
		if !strings.Contains(all, "ready") || !strings.Contains(all, "back in 12s") || !strings.Contains(all, "model-a") {
			t.Fatalf("%s: rows missing route states: %q", cmd, all)
		}
	}
	// /models <text> narrows, like the TUI filterRoutes
	res := decode(t, do(t, h, http.MethodPost, "/api/command", host, origin, `{"text":"/models model-b"}`))
	rows, _ := res["rows"].([]any)
	if len(rows) != 2 || !strings.Contains(fmt.Sprint(rows[0]), "free models") || !strings.Contains(fmt.Sprint(rows[1]), "model-b") || !strings.Contains(res["title"].(string), "model-b") {
		t.Fatalf("/models model-b: %v", res)
	}
}

func TestModelsTableCapsAndTags(t *testing.T) {
	var rows []routeRow
	for i := 0; i < 70; i++ {
		rows = append(rows, routeRow{Route: fmt.Sprintf("p/m%d", i), Ready: true})
	}
	rows = append(rows, routeRow{Route: "chatgpt/gpt-x", Ready: true, Source: "subscription"}, routeRow{Route: "openai/gpt-y", Ready: true, Source: "yours"})
	kept, more := filterRoutes(rows, "")
	out := modelsTable(kept, more, "")
	if got := len(out["rows"].([]map[string]any)); got != 45 {
		t.Fatalf("rows = %d, want 40 free + 2 paid + 3 group heads", got)
	}
	var heads []string
	for _, r := range out["rows"].([]map[string]any) {
		if h, ok := r["head"].(string); ok {
			heads = append(heads, h+":"+r["tone"].(string))
		}
	}
	if got := strings.Join(heads, ","); got != "free models:free,your plans:plan,your own keys:yours" {
		t.Fatalf("heads = %s", got)
	}
	if f := strings.Join(out["footer"].([]string), "\n"); !strings.Contains(f, "+30 more · /models <text> narrows") {
		t.Fatalf("footer = %q", f)
	}
	kept, more = filterRoutes(rows, "gpt")
	out = modelsTable(kept, more, "gpt")
	if all := fmt.Sprint(out["rows"]); !strings.Contains(all, "plan") || !strings.Contains(all, "yours") {
		t.Fatalf("paid rows not tagged: %s", all)
	}
	// an empty router says what to do, not just a title
	out = modelsTable(nil, 0, "")
	if f := strings.Join(out["footer"].([]string), "\n"); !strings.Contains(f, "no models ready yet · add one: prism setup or /key") {
		t.Fatalf("empty footer = %q", f)
	}
	out = modelsTable(nil, 0, "zzz")
	if f := strings.Join(out["footer"].([]string), "\n"); strings.Contains(f, "no models ready yet") || !strings.Contains(f, `no model matches "zzz"`) {
		t.Fatalf("filtered empty footer = %q", f)
	}
}

// the web /model auto:<role> keeps the role auto on the agent, like the TUI modelCommand
func TestPinModelRoleAutos(t *testing.T) {
	for _, name := range []string{"auto:code", "auto:fast", "auto:review", "auto:vision"} {
		a := &Agent{model: "some/pinned"}
		if note := pinModel(a, name); a.model != name || note != "model: "+name {
			t.Errorf("pinModel(%q): model=%q note=%q", name, a.model, note)
		}
	}
	a := &Agent{model: "auto:code"}
	if note := pinModel(a, "auto"); a.model != "" || note != "model: auto" {
		t.Errorf("pinModel(auto): model=%q note=%q", a.model, note)
	}
}

func TestCommandsEndpoint(t *testing.T) {
	h, _, port := testServer(t)
	host := "127.0.0.1:" + port
	rec := do(t, h, http.MethodGet, "/api/commands", host, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/commands: %d", rec.Code)
	}
	var list []struct {
		Name string `json:"name"`
		Help string `json:"help"`
		Web  bool   `json:"web"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != len(commands) {
		t.Fatalf("got %d commands, want %d", len(list), len(commands))
	}
	for i, c := range commands {
		if list[i].Name != c.name || list[i].Help != c.help || list[i].Web != c.web {
			t.Errorf("entry %d = %+v, want %+v", i, list[i], c)
		}
	}
	// every command the list marks web:true gets a real answer from the
	// server or is handled in web/app.js; web:false ones get a one-line note
	for _, c := range commands {
		if c.web {
			continue
		}
		body, _ := json.Marshal(map[string]string{"text": c.name})
		res := decode(t, do(t, h, http.MethodPost, "/api/command", host, "http://"+host, string(body)))
		if res["kind"] != "note" || res["text"] == "" {
			t.Errorf("%s (not on the web): got %v", c.name, res)
		}
	}
}

func TestCommandModelPin(t *testing.T) {
	h, s, port := testServer(t)
	host := "127.0.0.1:" + port
	post := func(text string) map[string]any {
		body, _ := json.Marshal(map[string]string{"text": text})
		return decode(t, do(t, h, http.MethodPost, "/api/command", host, "http://"+host, string(body)))
	}
	res := post("/model")
	if res["kind"] != "pick" || res["pick"] != "model" {
		t.Fatalf("/model: %v", res)
	}
	var labels, runs []string
	for _, it := range res["items"].([]any) {
		m := it.(map[string]any)
		labels = append(labels, m["label"].(string))
		runs = append(runs, m["run"].(string))
	}
	if got := strings.Join(labels, ","); got != "auto,auto:code,auto:fast,auto:review,model-a,model-b" {
		t.Fatalf("picker rows = %s", got)
	}
	if runs[4] != "/model fake/model-a:free" {
		t.Fatalf("run = %q", runs[4])
	}
	if dim, _ := res["items"].([]any)[5].(map[string]any)["dim"].(bool); !dim {
		t.Fatalf("resting row should be dim: %v", res["items"].([]any)[5])
	}

	res = post("/model model-a")
	if res["kind"] != "model" || res["model"] != "fake/model-a:free" || !strings.Contains(res["text"].(string), "every request in this chat uses it") {
		t.Fatalf("/model model-a: %v", res)
	}
	if s.agent.model != "fake/model-a:free" {
		t.Fatalf("agent.model = %q", s.agent.model)
	}
	if info := decode(t, do(t, h, http.MethodGet, "/api/info", host, "", "")); info["model"] != "fake/model-a:free" {
		t.Fatalf("/api/info model = %v", info["model"])
	}
	// the picker marks the pinned row current
	if res = post("/model"); !strings.Contains(fmt.Sprint(res["items"].([]any)[4]), "current") {
		t.Fatalf("pinned row not current: %v", res["items"].([]any)[4])
	}
	res = post("/model auto:fast")
	if s.agent.model != "auto:fast" || res["model"] != "auto:fast" {
		t.Fatalf("auto:fast: %v / %q", res, s.agent.model)
	}
	res = post("/model auto")
	if s.agent.model != "" || res["text"] != "model: auto" {
		t.Fatalf("auto: %v / %q", res, s.agent.model)
	}
	s.mu.Lock()
	s.busy = true
	s.mu.Unlock()
	if res = post("/model model-a"); res["kind"] != "error" || s.agent.model != "" {
		t.Fatalf("pin while busy: %v", res)
	}
}

func TestCommandUndo(t *testing.T) {
	h, s, port := testServer(t)
	host := "127.0.0.1:" + port
	post := func(text string) map[string]any {
		body, _ := json.Marshal(map[string]string{"text": text})
		return decode(t, do(t, h, http.MethodPost, "/api/command", host, "http://"+host, string(body)))
	}
	if res := post("/undo"); res["kind"] != "note" || res["text"] != "nothing to undo" {
		t.Fatalf("empty /undo: %v", res)
	}
	path := filepath.Join(s.agent.dir, "made.txt")
	turn := s.agent.undoStack.beginTurn()
	s.agent.undoStack.recordBefore(turn, path)
	if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.agent.undoStack.recordAfter(turn, path)

	s.mu.Lock()
	s.busy = true
	s.mu.Unlock()
	if res := post("/undo"); res["kind"] != "error" {
		t.Fatalf("/undo while busy: %v", res)
	}
	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()

	res := post("/undo")
	if res["kind"] != "info" || res["title"] != "undo" || !strings.Contains(fmt.Sprint(res["lines"]), "✓ made.txt") {
		t.Fatalf("/undo: %v", res)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("made.txt should be gone after /undo: %v", err)
	}
}

func TestCommandSkillsSummaryAndOne(t *testing.T) {
	h, s, port := testServer(t)
	s.agent.skills = loadSkills(s.agent.dir)
	host := "127.0.0.1:" + port
	post := func(text string) map[string]any {
		body, _ := json.Marshal(map[string]string{"text": text})
		return decode(t, do(t, h, http.MethodPost, "/api/command", host, "http://"+host, string(body)))
	}
	res := post("/skills")
	if res["kind"] != "view" || !strings.HasPrefix(res["title"].(string), "skills") {
		t.Fatalf("/skills: %v", res)
	}
	if f := res["footer"].(string); strings.Contains(f, "terminal") || !strings.Contains(f, "/skills new") {
		t.Fatalf("footer = %q", f)
	}
	blocks := res["blocks"].([]any)
	items := blocks[0].(map[string]any)["items"].([]any)
	first := items[0].(map[string]any)
	if !strings.HasPrefix(first["name"].(string), "building things (") || !strings.HasPrefix(first["desc"].(string), "website · ") {
		t.Fatalf("first group = %v", first)
	}

	res = post("/skills fix-bug")
	if res["kind"] != "view" || res["title"] != "skill · fix-bug" {
		t.Fatalf("/skills fix-bug: %v", res)
	}
	all := fmt.Sprint(res["blocks"])
	if !strings.Contains(all, "built in") || !strings.Contains(all, "steps") {
		t.Fatalf("/skills fix-bug blocks: %s", all)
	}
	if res = post("/skills nope-nope"); res["kind"] != "note" || !strings.Contains(res["text"].(string), "no skill called nope-nope") {
		t.Fatalf("/skills nope-nope: %v", res)
	}
	if res = post("/help"); res["kind"] != "view" || strings.Contains(fmt.Sprint(res["blocks"]), "/quit") {
		t.Fatalf("/help on the web should list only web commands: %v", res)
	}
}

func TestLoginOnWeb(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	polls := 0
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		switch {
		case r.Method == http.MethodGet:
			polls++
			signed := polls > 1
			json.NewEncoder(w).Encode(map[string]any{"subscriptions": []map[string]any{
				{"id": "chatgpt", "name": "ChatGPT", "signedIn": signed, "finishing": !signed, "models": 3},
				{"id": "copilot", "name": "GitHub Copilot", "signedIn": false, "models": 0},
			}})
		case body["poll"] == true:
			w.Write([]byte(`{"status":"ok","models":["a","b"]}`))
		case body["id"] == "chatgpt":
			polls = 0
			w.Write([]byte(`{"url":"https://auth.example/authorize?x=1"}`))
		default:
			w.Write([]byte(`{"userCode":"ABCD-1234","verificationURI":"https://github.com/login/device","expires":900}`))
		}
	}))
	t.Cleanup(router.Close)
	web := t.TempDir()
	os.WriteFile(filepath.Join(web, "index.html"), []byte("x"), 0o644)
	s := newServer(newAgent(router.URL, t.TempDir(), "prism"), web, "prism")
	h, host := s.handler("4849"), "127.0.0.1:4849"
	post := func(path, body string) map[string]any {
		return decode(t, do(t, h, http.MethodPost, path, host, "http://"+host, body))
	}
	res := post("/api/command", `{"text":"/login"}`)
	if res["kind"] != "pick" || res["pick"] != "login" || !strings.Contains(fmt.Sprint(res["items"]), "run:/login copilot") {
		t.Fatalf("/login picker: %v", res)
	}
	res = post("/api/command", `{"text":"/login chatgpt"}`)
	if res["kind"] != "login" || res["browser"] != true || res["url"] != "https://auth.example/authorize?x=1" || !strings.Contains(fmt.Sprint(res["lines"]), "if nothing opens: https://auth.example") {
		t.Fatalf("browser login start: %v", res)
	}
	// first GET still finishing, so pending; then signed in with its count
	if res = post("/api/login", `{"id":"chatgpt","browser":true}`); res["status"] != "pending" {
		t.Fatalf("finishing poll: %v", res)
	}
	if res = post("/api/login", `{"id":"chatgpt","browser":true}`); res["status"] != "ok" || res["text"] != "signed in · 3 models" {
		t.Fatalf("browser poll: %v", res)
	}
	res = post("/api/command", `{"text":"/login copilot"}`)
	if res["kind"] != "login" || res["browser"] != false || res["code"] != "ABCD-1234" {
		t.Fatalf("device login start: %v", res)
	}
	if res = post("/api/login", `{"id":"copilot"}`); res["status"] != "ok" || res["text"] != "signed in · 2 models" {
		t.Fatalf("device poll: %v", res)
	}
}

// a pasted code from the sign-in page goes to the router as POST /logins
// {id, code}; both reply shapes come back to the web as {ok, models, note}
// or {ok:false, error}
func TestBrowserLoginUI(t *testing.T) {
	// Run the actual browser sign-in controller against a small DOM/timer fake.
	const script = `
const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const mode = process.argv[1], nodes = [], sleeps = [];
let calls = 0;
function el(tag, cls, text) {
  const n = {tag, className: cls, textContent: text, removed: false, value: 'code', handlers: {},
    classList: {add() {}}, setAttribute() {}, append() {}, appendChild() {},
    addEventListener(k, f) {this.handlers[k] = f;}, querySelector() {return {focus() {}};},
    remove() {this.removed = true;}, focus() {}, select() {}};
  nodes.push(n); return n;
}
const c = vm.createContext({el, loginPolls: 0, tabQuery: () => '',
  addInfo: () => el('section'), signInLink: () => el('a'), document: {createTextNode: x => x},
  window: {open() {}}, matchMedia: () => ({matches: true}), scrollDown() {},
  refreshInfo() {}, $: () => ({focus() {}}),
  sleep: () => new Promise(resolve => sleeps.push(resolve)),
  api: async () => {calls++; return {status: 'pending', message: 'bad code; retry'};}});
const source = fs.readFileSync('../web/app.js', 'utf8');
vm.runInContext(source.slice(source.indexOf('let loginSeq = 0;'), source.indexOf('async function refreshInfo()')), c);
(async () => {
  const running = c.startLogin({id: 'grok', browser: true, url: 'https://auth.test', expires: 60});
  const form = nodes.find(n => n.tag === 'form'), status = nodes.find(n => n.className === 'login-state');
  if (mode === 'pending-error') {
    sleeps.shift()(); await new Promise(setImmediate);
    assert.equal(status.textContent, 'bad code; retry');
    assert.equal(form.removed, false);
  }
  c.loginPolls++;
  if (mode === 'stale-submit') {
    await form.handlers.submit({preventDefault() {}});
    assert.equal(calls, 0, 'superseded paste form sent a code');
  }
  sleeps.shift()(); await running;
  assert.equal(form.removed, true, 'superseded paste form remained live');
})().catch(e => {console.error(e); process.exitCode = 1;});`
	for _, mode := range []string{"pending-error", "stale-form", "stale-submit"} {
		t.Run(mode, func(t *testing.T) {
			out, err := exec.Command("node", "-e", script, mode).CombinedOutput()
			if err != nil {
				t.Fatalf("browser controller: %v\n%s", err, out)
			}
		})
	}
}

func TestWebLoginRetryAndTypedCode(t *testing.T) {
	testEnv(t)
	pending := true
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]any{"subscriptions": []map[string]any{{"id": "grok", "pending": pending, "signedIn": !pending, "models": 2, "error": "that code did not work; copy it again"}}})
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["code"] == "good#state" {
			pending = false
			fmt.Fprint(w, `{"ok":true,"models":["a","b"]}`)
		} else {
			fmt.Fprint(w, `{"error":{"message":"that code did not work; copy it again"}}`)
		}
	}))
	defer router.Close()
	s := newServer(newAgent(router.URL, t.TempDir(), "prism"), t.TempDir(), "prism")
	h, host := s.handler("4849"), "127.0.0.1:4849"
	post := func(path, body string) map[string]any {
		return decode(t, do(t, h, http.MethodPost, path, host, "http://"+host, body))
	}
	t.Run("retryable poll", func(t *testing.T) {
		res := post("/api/login", `{"id":"grok","browser":true}`)
		if res["status"] != "pending" || res["message"] != "that code did not work; copy it again" {
			t.Fatalf("retryable poll: %v", res)
		}
	})
	t.Run("typed code", func(t *testing.T) {
		res := post("/api/command", `{"text":"/login grok good#state"}`)
		if res["kind"] != "note" || res["text"] != "signed in · 2 models" {
			t.Fatalf("typed code: %v", res)
		}
	})
	t.Run("signed out poll", func(t *testing.T) {
		router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"subscriptions":[{"id":"grok"}]}`) }))
		defer router.Close()
		s.agent.api = router.URL
		res := post("/api/login", `{"id":"grok","browser":true}`)
		if res["status"] != "error" || res["message"] != "the sign-in was cancelled" {
			t.Fatalf("signed out poll: %v", res)
		}
	})
}

func TestLoginCodeOnWeb(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	var got []map[string]any
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		got = append(got, body)
		if r.Method != http.MethodPost || r.URL.Path != "/logins" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch body["code"] {
		case "good#st":
			w.Write([]byte(`{"ok":true,"models":["grok-4.7","grok-code"],"note":"2 free"}`))
		case "https://127.0.0.1:56121/callback?code=old&state=zz":
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":{"message":"that code did not work; copy it again"}}`))
		default:
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"error":{"message":"no sign-in is waiting for grok; run /login grok first"}}`))
		}
	}))
	t.Cleanup(router.Close)
	web := t.TempDir()
	os.WriteFile(filepath.Join(web, "index.html"), []byte("x"), 0o644)
	s := newServer(newAgent(router.URL, t.TempDir(), "prism"), web, "prism")
	h, host := s.handler("4849"), "127.0.0.1:4849"
	post := func(body string) map[string]any {
		return decode(t, do(t, h, http.MethodPost, "/api/login", host, "http://"+host, body))
	}
	res := post(`{"id":"grok","code":"  good#st \n"}`)
	if res["ok"] != true || res["models"] != float64(2) || res["note"] != "2 free" || res["text"] != "signed in · 2 models" {
		t.Fatalf("good code: %v", res)
	}
	if len(got) != 1 || got[0]["id"] != "grok" || got[0]["code"] != "good#st" || got[0]["poll"] != nil {
		t.Fatalf("router got %v", got)
	}
	res = post(`{"id":"grok","code":"https://127.0.0.1:56121/callback?code=old&state=zz"}`)
	if res["ok"] != false || res["error"] != "that code did not work; copy it again" {
		t.Fatalf("bad code: %v", res)
	}
	if res = post(`{"id":"grok","code":"x"}`); res["ok"] != false || !strings.Contains(fmt.Sprint(res["error"]), "run /login grok first") {
		t.Fatalf("nothing waiting: %v", res)
	}
	// an empty code is a plain poll, not a code round
	got = nil
	post(`{"id":"grok","code":"  "}`)
	if len(got) != 1 || got[0]["code"] != nil || got[0]["poll"] != true {
		t.Fatalf("blank code should poll: %v", got)
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
	for text, kind := range map[string]string{"/usage": "info", "/skills": "view", "/model": "pick"} {
		body, _ := json.Marshal(map[string]string{"text": text})
		res := decode(t, do(t, h, http.MethodPost, "/api/command", host, origin, string(body)))
		if res["kind"] != kind {
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

func TestBeamAndQuietAreSaved(t *testing.T) {
	h, _, port := testServer(t)
	host := "127.0.0.1:" + port
	for _, c := range []struct{ text, name, want string }{
		{"/beam off", "beam", "off"},
		{"/quiet", "quiet", "on"},
		{"/beam", "beam", "on"},
	} {
		res := decode(t, do(t, h, http.MethodPost, "/api/command", host, "http://"+host, `{"text":"`+c.text+`"}`))
		if res["kind"] != "setting" || res["name"] != c.name || loadSetting(c.name) != c.want {
			t.Fatalf("%s: reply %v, saved %q", c.text, res, loadSetting(c.name))
		}
	}
	info := decode(t, do(t, h, http.MethodGet, "/api/info", host, "", ""))
	if info["beam"] != true || info["quiet"] != true {
		t.Fatalf("info after reload: %v", info)
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
	if len(lines) != 3 || !strings.Contains(lines[1].(string), "this chat: 3 requests") || !strings.Contains(lines[2].(string), "all free") {
		t.Fatalf("usage lines: %v", lines)
	}
}

// ---------- tab tests ----------

// testServerWithHub builds a server with a tabHub and two agents.
func testServerWithHub(t *testing.T) (http.Handler, *server, string) {
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
		})
	}))
	t.Cleanup(router.Close)

	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("<!doctype html>prism"), 0o644); err != nil {
		t.Fatal(err)
	}

	hub := newTabHub()
	id1 := hub.add("main")
	id2 := hub.add("refactor")
	hub.setStatus(id2, "working", "rewriting router")

	agent1 := newAgent(router.URL, t.TempDir(), "prism")
	agent2 := newAgent(router.URL, t.TempDir(), "prism")
	agents := map[int]*Agent{id1: agent1, id2: agent2}

	agentFor := func(id int) *Agent { return agents[id] }
	s := newServerWithHub(agent1, hub, agentFor, web, "prism")
	return s.handler("4849"), s, "4849"
}

func TestTabsEndpointSingleAgent(t *testing.T) {
	// Single-agent mode: /api/tabs returns a synthetic one-tab list.
	h, _, port := testServer(t)
	host := "127.0.0.1:" + port
	rec := do(t, h, http.MethodGet, "/api/tabs", host, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/tabs: got %d, want 200", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	tabs, _ := out["tabs"].([]any)
	if len(tabs) != 1 {
		t.Fatalf("single-agent: want 1 tab, got %d", len(tabs))
	}
	if out["version"] == nil {
		t.Fatal("version field missing")
	}
}

func TestTabsEndpointWithHub(t *testing.T) {
	h, _, port := testServerWithHub(t)
	host := "127.0.0.1:" + port
	rec := do(t, h, http.MethodGet, "/api/tabs", host, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/tabs: got %d, want 200", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	tabs, _ := out["tabs"].([]any)
	if len(tabs) != 2 {
		t.Fatalf("hub mode: want 2 tabs, got %d", len(tabs))
	}
	// Check that the second tab has status "working"
	found := false
	for _, raw := range tabs {
		tab, _ := raw.(map[string]any)
		if tab["Status"] == "working" || tab["status"] == "working" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a tab with status=working in %v", tabs)
	}
}

func TestTabsOriginCheck(t *testing.T) {
	// GET /api/tabs must pass the host allowlist check.
	h, _, _ := testServer(t)
	if got := do(t, h, http.MethodGet, "/api/tabs", "evil.com", "", "").Code; got != http.StatusForbidden {
		t.Fatalf("evil host on /api/tabs: got %d, want 403", got)
	}
}

func TestTabRoutingHistory(t *testing.T) {
	h, _, port := testServerWithHub(t)
	host := "127.0.0.1:" + port
	// ?tab=1 and ?tab=2 both return 200 with an entries array.
	for _, tab := range []string{"1", "2"} {
		rec := do(t, h, http.MethodGet, "/api/history?tab="+tab, host, "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/history?tab=%s: got %d", tab, rec.Code)
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("tab %s: bad JSON: %v", tab, err)
		}
		if _, ok := out["entries"]; !ok {
			t.Fatalf("tab %s: missing entries field", tab)
		}
	}
}

func TestTabRoutingUnknownReturns404(t *testing.T) {
	// ?tab=999 (unknown id) must return 404 when a hub is configured.
	h, _, port := testServerWithHub(t)
	host := "127.0.0.1:" + port
	rec := do(t, h, http.MethodGet, "/api/history?tab=999", host, "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown tab: got %d, want 404", rec.Code)
	}
}

func TestTabRoutingBadIdReturns400(t *testing.T) {
	// ?tab=abc (unparseable) must return 400 when a hub is configured.
	h, _, port := testServerWithHub(t)
	host := "127.0.0.1:" + port
	for _, bad := range []string{"abc", "0", "-1", ""} {
		path := "/api/history"
		if bad != "" {
			path += "?tab=" + bad
		}
		rec := do(t, h, http.MethodGet, path, host, "", "")
		if bad == "" {
			// no ?tab= param → fallback to default agent → 200
			if rec.Code != http.StatusOK {
				t.Fatalf("no tab param: got %d, want 200", rec.Code)
			}
		} else {
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("bad tab %q: got %d, want 400", bad, rec.Code)
			}
		}
	}
}

func TestTabRoutingNoHubFallsBack(t *testing.T) {
	// Single-agent mode (no hub): any ?tab= value falls back to default agent.
	h, _, port := testServer(t)
	host := "127.0.0.1:" + port
	for _, tab := range []string{"1", "999", "abc"} {
		rec := do(t, h, http.MethodGet, "/api/history?tab="+tab, host, "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("no-hub ?tab=%s: got %d, want 200", tab, rec.Code)
		}
	}
}

func TestTabsLongPollSince(t *testing.T) {
	// ?since=0 with a hub that already has version>0 returns immediately.
	h, _, port := testServerWithHub(t)
	host := "127.0.0.1:" + port
	rec := do(t, h, http.MethodGet, "/api/tabs?since=0", host, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("long-poll since=0: got %d, want 200", rec.Code)
	}
}

func TestNewServerWithHub(t *testing.T) {
	// Verify newServerWithHub wires hub and agentFor correctly.
	hub := newTabHub()
	id := hub.add("test")
	a := newAgent("http://127.0.0.1:1", t.TempDir(), "prism")
	agents := map[int]*Agent{id: a}
	s := newServerWithHub(a, hub, func(i int) *Agent { return agents[i] }, t.TempDir(), "prism")
	if s.hub == nil {
		t.Fatal("hub should be set")
	}
	if s.agentFor == nil {
		t.Fatal("agentFor should be set")
	}
	// agentTab with no ?tab= returns default agent
	req := httptest.NewRequest(http.MethodGet, "/api/history", nil)
	got, err := s.agentTab(req)
	if err != nil {
		t.Fatalf("agentTab with no tab param: unexpected error %v", err)
	}
	if got != a {
		t.Fatal("agentTab with no tab param should return default agent")
	}
	// agentTab with ?tab=<id> returns the mapped agent
	req2 := httptest.NewRequest(http.MethodGet, "/api/history?tab="+strconv.Itoa(id), nil)
	got2, err2 := s.agentTab(req2)
	if err2 != nil {
		t.Fatalf("agentTab with valid tab=%d: unexpected error %v", id, err2)
	}
	if got2 != a {
		t.Fatalf("agentTab with tab=%d should return agent for that tab", id)
	}
	// agentTab with unknown id returns 404 error
	req3 := httptest.NewRequest(http.MethodGet, "/api/history?tab=9999", nil)
	_, err3 := s.agentTab(req3)
	if err3 == nil {
		t.Fatal("agentTab with unknown tab should return error")
	}
	if he, ok := err3.(*httpError); !ok || he.code != http.StatusNotFound {
		t.Fatalf("agentTab unknown tab: want 404 httpError, got %v", err3)
	}
	// agentTab with bad id returns 400 error
	req4 := httptest.NewRequest(http.MethodGet, "/api/history?tab=notanint", nil)
	_, err4 := s.agentTab(req4)
	if err4 == nil {
		t.Fatal("agentTab with bad tab id should return error")
	}
	if he, ok := err4.(*httpError); !ok || he.code != http.StatusBadRequest {
		t.Fatalf("agentTab bad tab: want 400 httpError, got %v", err4)
	}
}

func TestHistoryRaceNonDefaultTab(t *testing.T) {
	// Regression: GET /api/history?tab=N during a run must not race-read
	// the running agent's messages. The server must serve the pre-run snapshot.
	hub := newTabHub()
	id1 := hub.add("main")
	id2 := hub.add("worker")
	a1 := newAgent("http://127.0.0.1:1", t.TempDir(), "prism")
	a2 := newAgent("http://127.0.0.1:1", t.TempDir(), "prism")
	agents := map[int]*Agent{id1: a1, id2: a2}
	s := newServerWithHub(a1, hub, func(i int) *Agent { return agents[i] }, t.TempDir(), "prism")

	// Simulate a run in progress on a2 (non-default agent).
	s.mu.Lock()
	s.busy = true
	s.busyAgent = a2
	if s.agentSnap == nil {
		s.agentSnap = make(map[*Agent][]wireEntry)
	}
	s.agentSnap[a2] = []wireEntry{{Kind: "user", Text: "hello"}}
	s.mu.Unlock()

	// GET /api/history?tab=<id2> must return the snapshot, not live messages.
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("<!doctype html>prism"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.webDir = web
	h := s.handler("4849")
	host := "127.0.0.1:4849"
	rec := do(t, h, http.MethodGet, "/api/history?tab="+strconv.Itoa(id2), host, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("history during run: got %d, want 200", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	entries, _ := out["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("expected 1 snapshot entry, got %d", len(entries))
	}
	entry, _ := entries[0].(map[string]any)
	if entry["kind"] != "user" || entry["text"] != "hello" {
		t.Fatalf("unexpected snapshot entry: %v", entry)
	}
}

func TestFileEndpointRead(t *testing.T) {
	h, s, port := testServer(t)
	host := "127.0.0.1:" + port
	dir := s.agent.dir
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "a.go"), []byte("package a\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "bin"), []byte{0, 1, 2}, 0o644)
	os.WriteFile(filepath.Join(dir, "big"), make([]byte, viewLimit+10), 0o644)

	rec := do(t, h, http.MethodGet, "/api/file?path=src/a.go", host, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
	}
	out := decode(t, rec)
	if out["content"] != "package a\n" || out["editable"] != true || out["path"] != "src/a.go" {
		t.Errorf("read body: %v", out)
	}
	for _, p := range []string{"bin", "big"} {
		out := decode(t, do(t, h, http.MethodGet, "/api/file?path="+p, host, "", ""))
		if out["editable"] != false || out["note"] == nil || out["content"] != nil {
			t.Errorf("%s: %v", p, out)
		}
	}
	if got := do(t, h, http.MethodGet, "/api/file?path=missing", host, "", "").Code; got != http.StatusNotFound {
		t.Errorf("missing: %d", got)
	}
	if got := do(t, h, http.MethodGet, "/api/file?path=src/a.go", "evil.com", "", "").Code; got != http.StatusForbidden {
		t.Errorf("bad host: %d", got)
	}
}

func TestFileEndpointRejectsEscapes(t *testing.T) {
	h, s, port := testServer(t)
	host := "127.0.0.1:" + port
	origin := "http://127.0.0.1:" + port
	dir := s.agent.dir
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	os.WriteFile(secret, []byte("keep"), 0o644)
	linked := os.Symlink(outside, filepath.Join(dir, "link")) == nil

	paths := []string{"../secret", "..%2Fsecret", "src%2F..%2F..%2Fx", secret}
	if linked {
		paths = append(paths, "link/secret")
	}
	for _, p := range paths {
		if got := do(t, h, http.MethodGet, "/api/file?path="+p, host, "", "").Code; got != http.StatusForbidden {
			t.Errorf("GET %s: %d, want 403", p, got)
		}
		raw, _ := url.QueryUnescape(p)
		body, _ := json.Marshal(map[string]string{"path": raw, "content": "pwned"})
		if got := do(t, h, http.MethodPost, "/api/file", host, origin, string(body)).Code; got != http.StatusForbidden {
			t.Errorf("POST %s: %d, want 403", p, got)
		}
	}
	if b, _ := os.ReadFile(secret); string(b) != "keep" {
		t.Fatalf("file outside the project was changed: %q", b)
	}
}

func TestFileEndpointSave(t *testing.T) {
	h, s, port := testServer(t)
	host := "127.0.0.1:" + port
	origin := "http://127.0.0.1:" + port
	dir := s.agent.dir
	target := filepath.Join(dir, "note.txt")
	os.WriteFile(target, []byte("old"), 0o600)
	os.WriteFile(filepath.Join(dir, "bin"), []byte{0, 1}, 0o644)
	body := `{"path":"note.txt","content":"new text\n"}`

	if got := do(t, h, http.MethodPost, "/api/file", host, "", body).Code; got != http.StatusForbidden {
		t.Errorf("no origin: %d", got)
	}
	if got := do(t, h, http.MethodPost, "/api/file", host, "http://evil.com", body).Code; got != http.StatusForbidden {
		t.Errorf("evil origin: %d", got)
	}
	if b, _ := os.ReadFile(target); string(b) != "old" {
		t.Fatalf("rejected POST still wrote: %q", b)
	}
	rec := do(t, h, http.MethodPost, "/api/file", host, origin, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if b, _ := os.ReadFile(target); string(b) != "new text\n" {
		t.Errorf("saved %q", b)
	}
	if st, _ := os.Stat(target); st.Mode().Perm() != 0o600 {
		t.Errorf("mode changed to %v", st.Mode().Perm())
	}
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"path":"missing.txt","content":"x"}`, http.StatusNotFound},
		{`{"path":"bin","content":"x"}`, http.StatusUnprocessableEntity},
		{`{"path":"note.txt"}`, http.StatusBadRequest},
		{`not json`, http.StatusBadRequest},
	} {
		if got := do(t, h, http.MethodPost, "/api/file", host, origin, tc.body).Code; got != tc.want {
			t.Errorf("%s: %d, want %d", tc.body, got, tc.want)
		}
	}
	big, _ := json.Marshal(map[string]string{"path": "note.txt", "content": strings.Repeat("a", viewLimit+1)})
	if got := do(t, h, http.MethodPost, "/api/file", host, origin, string(big)).Code; got != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize: %d", got)
	}
}
