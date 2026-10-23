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
	"strconv"
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

	handoff  bool
	lastSeen time.Time
	requests int
	sent     int
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
	Crew    *wireCrew  `json:"crew,omitempty"`
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
	return newServer(agent, webDir, theme).handler(port)
}

func newServer(agent *Agent, webDir, theme string) *server {
	return &server{agent: agent, webDir: webDir, theme: theme}
}

func (s *server) handler(port string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/command", s.command)
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /api/info", s.info)
	mux.HandleFunc("GET /api/history", s.history)
	mux.HandleFunc("POST /api/chat", s.chat)
	mux.HandleFunc("POST /api/stop", s.stop)
	mux.HandleFunc("POST /api/new", s.reset)
	mux.Handle("GET /", noDirListing(s.webDir, http.FileServer(http.Dir(s.webDir))))
	return localOnly(port, s.seen(mux))
}

func (s *server) seen(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ping" || r.Method == http.MethodPost {
			s.mu.Lock()
			s.lastSeen = time.Now()
			s.mu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
}

// state reports when a browser last talked to the server and whether a chat is running.
func (s *server) state() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSeen, s.busy
}

// stopRun cancels a running chat and waits up to a few seconds for it to end.
func (s *server) stopRun() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	for i := 0; i < 50; i++ {
		if _, busy := s.state(); !busy {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
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
	s.mu.Lock()
	handoff := s.handoff
	s.mu.Unlock()
	writeJSON(w, map[string]any{"project": projectName(s.agent.dir), "theme": s.theme, "models_ready": ready, "handoff": handoff})
}

// snapshot converts the agent's messages to wire entries. Callers must hold
// s.mu and must not call it while a chat is running.
func (s *server) snapshot() []wireEntry {
	out := []wireEntry{}
	for _, e := range entriesFrom(s.agent.messages) {
		w := wireEntry{Kind: e.kind, Text: e.text, Tool: e.tool, Arg: e.arg, OK: e.res.ok, Summary: e.res.summary}
		if e.crew != nil {
			w.Crew = e.crew.wire()
		}
		out = append(out, w)
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
		s.busy, s.cancel, s.lastSeen = false, nil, time.Now()
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
		if v, ok := crewWire(event); ok {
			send(v)
			return
		}
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
			s.mu.Lock()
			s.requests, s.sent = s.agent.requests, s.agent.sent
			s.mu.Unlock()
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

func (s *server) command(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		http.Error(w, "send JSON like {\"text\": \"/models\"}", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	busy := s.busy
	fields := strings.Fields(body.Text)
	if len(fields) == 0 {
		writeJSON(w, map[string]any{"kind": "error", "text": "empty command"})
		return
	}
	info := func(title string, lines []string) {
		writeJSON(w, map[string]any{"kind": "info", "title": title, "lines": lines})
	}
	a := s.agent
	switch fields[0] {
	case "/models", "/model", "/status":
		rows, err := fetchRoutes(a.api)
		if err != nil {
			writeJSON(w, map[string]any{"kind": "error", "text": err.Error()})
			return
		}
		var lines []string
		for _, row := range rows {
			state := "ready"
			if !row.Ready {
				state = fmt.Sprintf("resting %ds", row.RetryIn)
			}
			lines = append(lines, fmt.Sprintf("%s  %s  %s", modelName(row.Route), provider(row.Route), state))
		}
		info("free models, in the order prism tries them", append(lines, "", "add more free models: prism setup"))
	case "/skills":
		if len(fields) > 2 && fields[1] == "new" {
			if busy {
				writeJSON(w, map[string]any{"kind": "error", "text": "wait until prism finishes"})
				return
			}
			info(newSkill(a, fields[2]), []string{})
			return
		}
		var lines []string
		for _, sk := range a.skills {
			lines = append(lines, fmt.Sprintf("%s: %s (%s)", sk.name, sk.description, sk.source))
		}
		info("skills", append(lines, "", "add your own: /skills new <name> in the terminal, or .prism/skills/<name>.md"))
	case "/mcp":
		res, follow := mcpCommand(a, fields)
		if follow != nil {
			late, _ := follow(15 * time.Second)
			res.lines = append(res.lines, late.lines...)
		}
		if res.failed {
			writeJSON(w, map[string]any{"kind": "error", "headline": res.title, "text": strings.Join(res.lines, "\n")})
			return
		}
		info(res.title, res.lines)
	case "/usage":
		u := todayUsage()
		info("free AI usage", []string{
			fmt.Sprintf("today: %d requests, %s tokens sent", u.Requests, short(u.Tokens)),
			fmt.Sprintf("this chat: %d requests, %s tokens sent", s.requests, short(s.sent)),
		})
	case "/memory", "/forget":
		if busy {
			writeJSON(w, map[string]any{"kind": "error", "text": "wait until prism finishes"})
			return
		}
		title, lines, changed, err := a.store.memoryCommand(fields)
		if err != nil {
			writeJSON(w, map[string]any{"kind": "error", "headline": "memory", "text": err.Error()})
			return
		}
		if changed {
			a.messages[0].Content = a.systemPrompt()
		}
		info(title, lines)
	case "/resume", "/continue":
		if busy {
			writeJSON(w, map[string]any{"kind": "error", "text": "wait until prism finishes"})
			return
		}
		var others []session
		for _, sess := range a.store.sessions() {
			if sess.ID != a.session {
				others = append(others, sess)
			}
		}
		if len(others) > resumeShown {
			others = others[:resumeShown]
		}
		if len(fields) > 1 {
			k, err := strconv.Atoi(fields[1])
			if err != nil || k < 1 || k > len(others) {
				writeJSON(w, map[string]any{"kind": "error", "text": "no chat with that number, try /resume"})
				return
			}
			full, err := a.store.loadSession(others[k-1].ID)
			if err != nil {
				writeJSON(w, map[string]any{"kind": "error", "text": err.Error()})
				return
			}
			a.resume(full)
			s.snap = nil
			writeJSON(w, map[string]any{"kind": "reload"})
			return
		}
		if len(others) == 0 {
			info("no earlier chats in this folder yet", []string{})
			return
		}
		var lines []string
		for k, sess := range others {
			lines = append(lines, fmt.Sprintf("%d  %s  %s", k+1, ago(sess.Updated), sess.Title))
		}
		info("earlier chats", append(lines, "", "type /resume <number> to reopen one"))
	case "/init":
		if busy {
			writeJSON(w, map[string]any{"kind": "error", "text": "wait until prism finishes, then run /init"})
			return
		}
		writeJSON(w, map[string]any{"kind": "send", "message": initPrompt})
	default:
		writeJSON(w, map[string]any{"kind": "error", "headline": "unknown command", "text": fields[0] + " isn't a command. /help lists them"})
	}
}
