package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFilterRoutes(t *testing.T) {
	rows := []routeRow{}
	for _, id := range []string{"nvidia/a", "nvidia/b", "groq/c", "mistral-paid/d"} {
		rows = append(rows, routeRow{Route: id, Ready: true})
	}
	kept, more := filterRoutes(rows, "nvidia")
	if len(kept) != 2 || more != 0 {
		t.Fatalf("nvidia filter: kept %d more %d", len(kept), more)
	}
	kept, more = filterRoutes(rows, "")
	if len(kept) != 4 || more != 0 {
		t.Fatalf("no filter: kept %d more %d", len(kept), more)
	}
	kept, more = filterRoutes(rows, "zzz")
	if len(kept) != 0 || more != 0 {
		t.Fatalf("no match: kept %d more %d", len(kept), more)
	}
	big := []routeRow{}
	for i := 0; i < 300; i++ {
		big = append(big, routeRow{Route: "openrouter-paid/m" + string(rune('a'+i%26)) + strings.Repeat("x", 0)})
	}
	kept, more = filterRoutes(big, "")
	if len(kept) != 40 || more != 260 {
		t.Fatalf("cap: kept %d more %d", len(kept), more)
	}
	mixed := []routeRow{{Route: "mine/a", Source: "yours"}, {Route: "claude/b", Source: "subscription"}, {Route: "nvidia/c"}}
	kept, _ = filterRoutes(append(big, mixed...), "")
	if len(kept) != 42 || kept[40].Route != "claude/b" || kept[41].Route != "mine/a" {
		t.Fatalf("groups: free, plans, own keys, each capped on its own: %v", kept[39:])
	}
}

func TestModelCommand(t *testing.T) {
	testEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.Write([]byte(`[{"route":"mistral-paid/mistral-large-4","ready":true},{"route":"groq/m","ready":true}]`))
	}))
	defer srv.Close()
	a := newTestAgent(t, srv.URL, t.TempDir())
	m := model{tab: &tab{agent: a}}

	m.modelCommand([]string{"/model"})
	if len(m.entries) != 1 || !strings.Contains(m.entries[0].text, "auto") {
		t.Fatalf("bare /model: %+v", m.entries)
	}

	cmd := m.modelCommand([]string{"/model", "mistral-paid/mistral-large-4"})
	msg := cmd().(modelMsg)
	if msg.err != nil || !msg.found {
		t.Fatalf("pin: %+v", msg)
	}
	if a.model != "mistral-paid/mistral-large-4" {
		t.Fatalf("agent.model = %q", a.model)
	}

	m.entries = nil
	m.modelCommand([]string{"/model"})
	if !strings.Contains(m.entries[0].text, "mistral-paid/mistral-large-4") {
		t.Fatalf("show pinned: %+v", m.entries)
	}

	m.entries = nil
	m.modelCommand([]string{"/model", "auto"})
	if a.model != "" {
		t.Fatalf("auto should clear, got %q", a.model)
	}

	cmd = m.modelCommand([]string{"/model", "nope/nope"})
	msg = cmd().(modelMsg)
	if msg.err != nil || msg.found {
		t.Fatalf("unknown pin: %+v", msg)
	}
	if a.model != "nope/nope" {
		t.Fatalf("unknown pin should still set: %q", a.model)
	}
}
