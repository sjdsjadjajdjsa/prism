package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type server struct {
	agent  *Agent
	webDir string
	theme  string
	mu     sync.Mutex
	cancel context.CancelFunc
	busy   bool
	snap   []wireEntry    // history snapshot served while a chat is running
	memWG  sync.WaitGroup // outstanding background memory job
}

type wireEntry struct {
	Kind    string     `json:"kind"`
	Text    string     `json:"text,omitempty"`
	Tool    string     `json:"tool,omitempty"`
	Arg     string     `json:"arg,omitempty"`
	OK      bool       `json:"ok"`
	Summary string     `json:"summary,omitempty"`
	Added   int        `json:"added,omitempty"`
	Removed int        `json:"removed,omitempty"`
	Diff    []wireDiff `json:"diff,omitempty"`
}

type wireDiff struct {
	Kind string `json:"kind"`
	Num  int    `json:"num"`
	Text string `json:"text"`
}

func wire(diff []diffLine) []wireDiff {
	out := make([]wireDiff, len(diff))
	for i, d := range diff {
		out[i] = wireDiff{string(d.kind), d.num, d.text}
	}
	return out
}

func serve(agent *Agent, addr, webDir, theme string) error {
	port := publicPort(addr)
	fmt.Printf("Prism web is running: http://127.0.0.1:%s\nPress Ctrl+C to stop.\n", port)
	return http.ListenAndServe(addr, newHandler(agent, port, webDir, theme))
}

// publicPort is the port browsers reach the server on: PRISM_PUBLIC_PORT when
// set (e.g. Docker listens on 0.0.0.0:4849 but is published on 127.0.0.1:4849),
// otherwise the port of the listen address.
func publicPort(addr string) string {
	if p := strings.TrimSpace(os.Getenv("PRISM_PUBLIC_PORT")); p != "" {
		return p
	}
	if _, p, err := net.SplitHostPort(addr); err == nil {
		return p
	}
	return addr[strings.LastIndex(addr, ":")+1:]
}

func newHandler(agent *Agent, port, webDir, theme string) http.Handler {
	s := &server{agent: agent, webDir: webDir, theme: theme}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", s.info)
	mux.HandleFunc("GET /api/history", s.history)
	mux.HandleFunc("POST /api/chat", s.chat)
	mux.HandleFunc("POST /api/stop", s.stop)
	mux.HandleFunc("POST /api/new", s.reset)
	mux.Handle("GET /", noDirListing(webDir, http.FileServer(http.Dir(webDir))))
	return localOnly(port, mux)
}

// localOnly rejects any request whose Host is not the loopback address on the
// public port (defeats DNS rebinding), and any POST whose Origin is not the
// loopback origin (defeats cross-site form posts and missing-Origin requests).
func localOnly(port string, next http.Handler) http.Handler {
	hosts := map[string]bool{"127.0.0.1:" + port: true, "localhost:" + port: true, "[::1]:" + port: true}
	origins := map[string]bool{"http://127.0.0.1:" + port: true, "http://localhost:" + port: true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hosts[strings.ToLower(r.Host)] {
			http.Error(w, "prism only answers to 127.0.0.1", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost && !origins[strings.ToLower(r.Header.Get("Origin"))] {
			http.Error(w, "cross-origin requests are not allowed", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// noDirListing answers 404 for directories that have no index.html instead of
// letting http.FileServer render a listing.
func noDirListing(root string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(root, filepath.FromSlash(path.Clean("/"+r.URL.Path)))
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			if _, err := os.Stat(filepath.Join(p, "index.html")); err != nil {
				http.NotFound(w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("content-type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (s *server) info(w http.ResponseWriter, r *http.Request) {
	rows, _ := fetchRoutes(s.agent.api)
	ready := 0
	for _, row := range rows {
		if row.Ready {
			ready++
		}
	}
	writeJSON(w, map[string]any{"project": projectName(s.agent.dir), "theme": s.theme, "models_ready": ready})
}

// snapshot converts the agent's messages to wire entries. Callers must hold
// s.mu and must not call it while a chat is running.
func (s *server) snapshot() []wireEntry {
	out := []wireEntry{}
	for _, e := range entriesFrom(s.agent.messages) {
		out = append(out, wireEntry{Kind: e.kind, Text: e.text, Tool: e.tool, Arg: e.arg, OK: e.res.ok, Summary: e.res.summary})
	}
	return out
}

func (s *server) history(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	var out []wireEntry
	if s.busy {
		// agent.Run is mutating messages outside the lock; serve the copy
		// taken before the run started instead of reading live state.
		out = s.snap
	} else {
		out = s.snapshot()
	}
	s.mu.Unlock()
	if out == nil {
		out = []wireEntry{}
	}
	writeJSON(w, map[string]any{"entries": out})
}

func (s *server) stop(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) reset(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		http.Error(w, "busy", http.StatusConflict)
		return
	}
	s.agent.reset()
	s.snap = nil
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) chat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil || strings.TrimSpace(body.Message) == "" {
		http.Error(w, "send JSON like {\"message\": \"...\"}", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		http.Error(w, "prism is already working on something", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	s.busy, s.cancel = true, cancel
	s.snap = s.snapshot()
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.snap = s.snapshot()
		s.busy, s.cancel = false, nil
		s.mu.Unlock()
	}()
	// A memory job from the previous chat may still be running; never let it
	// overlap with agent.Run.
	s.memWG.Wait()

	flusher, _ := w.(http.Flusher)
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	send := func(v map[string]any) {
		data, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	start, route, tools, tests := time.Now(), "", 0, false
	pending := map[string]string{}
	s.agent.Run(ctx, strings.TrimSpace(body.Message), func(event any) {
		switch e := event.(type) {
		case evRoute:
			route = e.route
			send(map[string]any{"type": "route", "route": e.route, "wait_ms": e.wait.Milliseconds()})
		case evThinking:
			send(map[string]any{"type": "thinking"})
		case evReasoning:
			send(map[string]any{"type": "reasoning", "text": e.text})
		case evText:
			send(map[string]any{"type": "text", "text": e.text})
		case evDraft:
			send(map[string]any{"type": "draft", "tool": e.name, "arg": e.arg, "chars": e.chars})
		case evToolStart:
			tools++
			pending[e.id] = e.arg
			send(map[string]any{"type": "tool_start", "id": e.id, "tool": e.name, "arg": e.arg})
		case evToolDone:
			if strings.Contains(pending[e.id], "test") && e.result.ok {
				tests = true
			}
			send(map[string]any{"type": "tool_done", "id": e.id, "ok": e.result.ok, "summary": e.result.summary, "added": e.result.added, "removed": e.result.removed, "diff": wire(e.result.diff)})
		case evNote:
			send(map[string]any{"type": "note", "text": e.text})
		case evUsage:
			send(map[string]any{"type": "usage", "tokens": e.tokens, "window": e.window})
		case evDone:
			switch {
			case e.err == nil:
				send(map[string]any{"type": "done", "seconds": time.Since(start).Seconds(), "model": modelName(route), "tools": tools, "tests_passed": tests})
			case errors.Is(e.err, context.Canceled):
				send(map[string]any{"type": "error", "headline": "stopped", "detail": ""})
			default:
				headline, detail := friendly(e.err)
				send(map[string]any{"type": "error", "headline": headline, "detail": detail})
			}
		}
	})
	if job := s.agent.memoryJob(); job != nil {
		s.memWG.Add(1)
		go func() {
			defer s.memWG.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			job.run(ctx)
		}()
	}
}

func webDir() string {
	for _, d := range []string{os.Getenv("PRISM_WEB"), "/usr/share/prism/web", "web", "../web"} {
		if d == "" {
			continue
		}
		if _, err := os.Stat(d + "/index.html"); err == nil {
			return d
		}
	}
	return ""
}
