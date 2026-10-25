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
	agent     *Agent
	agentFor  func(id int) *Agent // nil in single-agent mode
	hub       *tabHub
	webDir    string
	theme     string
	mu        sync.Mutex
	cancel    context.CancelFunc
	busy      bool
	busyAgent *Agent                 // set under mu
	snap      []wireEntry            // history snapshot served while a chat is running
	agentSnap map[*Agent][]wireEntry // per-agent snapshots for non-default running agents
	memWG     sync.WaitGroup         // outstanding background memory job

	asks   map[int]chan answer // questions a running turn is waiting on
	askSeq int

	handoff  bool
	openPath string // a picture or video the terminal asked the browser to show
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

func newServerWithHub(agent *Agent, hub *tabHub, agentFor func(id int) *Agent, webDir, theme string) *server {
	return &server{agent: agent, hub: hub, agentFor: agentFor, webDir: webDir, theme: theme}
}

func (s *server) handler(port string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/command", s.command)
	mux.HandleFunc("GET /api/commands", s.commandList)
	mux.HandleFunc("POST /api/login", s.loginPoll)
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /api/info", s.info)
	mux.HandleFunc("GET /api/tree", s.tree)
	mux.HandleFunc("GET /api/file", s.readFile)
	mux.HandleFunc("POST /api/file", s.saveFile)
	mux.HandleFunc("GET /api/raw", s.rawFile)
	mux.HandleFunc("POST /api/fs", s.fileChange)
	mux.HandleFunc("GET /tree.js", treeAsset("tree.js", "text/javascript"))
	mux.HandleFunc("GET /tree.css", treeAsset("tree.css", "text/css"))
	mux.HandleFunc("GET /api/history", s.history)
	mux.HandleFunc("GET /api/tabs", s.tabs)
	mux.HandleFunc("POST /api/chat", s.chat)
	mux.HandleFunc("POST /api/stop", s.stop)
	mux.HandleFunc("POST /api/new", s.reset)
	mux.HandleFunc("POST /api/approve", s.answerAsk)
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
		w.Header().Set("cache-control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("content-type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// tree serves the live project map; ?wait=<version> holds the request until it changes (~25s).
func (s *server) tree(w http.ResponseWriter, r *http.Request) {
	a := s.agent
	if a.mapReady != nil {
		<-a.mapReady
	}
	if a.tree == nil {
		http.NotFound(w, r)
		return
	}
	a.tree.startWatching()
	if v := r.URL.Query().Get("wait"); v != "" {
		version, _ := strconv.Atoi(v)
		if !a.tree.wait(r.Context(), version, 25*time.Second) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	w.Header().Set("content-type", "application/json")
	w.Header().Set("cache-control", "no-store")
	w.Write(a.tree.json())
}

// fileStatus maps a viewer error to an HTTP status.
func fileStatus(err error) int {
	switch {
	case errors.Is(err, errOutside):
		return http.StatusForbidden
	case errors.Is(err, os.ErrNotExist):
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

// readFile serves one project file for the browser viewer: its text, or a
// note when it is binary or over the size limit (then it is not editable).
func (s *server) readFile(w http.ResponseWriter, r *http.Request) {
	fc, err := readProjectFile(s.agent.dir, r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), fileStatus(err))
		return
	}
	out := map[string]any{"path": fc.rel, "size": fc.size, "editable": fc.note == ""}
	if kind := mediaKind(fc.rel); kind != "" {
		out["kind"], out["editable"] = kind, false
	} else if fc.note != "" {
		out["note"] = fc.note
	} else {
		out["content"] = fc.text
	}
	w.Header().Set("cache-control", "no-store")
	writeJSON(w, out)
}

// saveFile writes an existing project file from the browser editor. The path
// guard and size limit match readFile; binary files cannot be overwritten.
func (s *server) saveFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 6*viewLimit+4096)
	var req struct {
		Path    string  `json:"path"`
		Content *string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Content == nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(*req.Content) > viewLimit {
		http.Error(w, "file is over the 1 MB limit", http.StatusRequestEntityTooLarge)
		return
	}
	fc, err := readProjectFile(s.agent.dir, req.Path)
	if err != nil {
		http.Error(w, err.Error(), fileStatus(err))
		return
	}
	if fc.note != "" {
		http.Error(w, "this file can't be edited here: "+fc.note, http.StatusUnprocessableEntity)
		return
	}
	abs, rel, err := projectPath(s.agent.dir, req.Path)
	if err != nil {
		http.Error(w, err.Error(), fileStatus(err))
		return
	}
	st, err := os.Stat(abs)
	if err != nil {
		http.Error(w, err.Error(), fileStatus(err))
		return
	}
	if err := os.WriteFile(abs, []byte(*req.Content), st.Mode().Perm()); err != nil {
		http.Error(w, pathErr(rel, err).Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"path": rel, "size": len(*req.Content)})
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
	handoff, open := s.handoff, s.openPath
	s.openPath = ""
	s.mu.Unlock()
	a, err := s.agentTab(r)
	if err != nil {
		a = s.agent
	}
	s.mu.Lock()
	pinned := a.model
	s.mu.Unlock()
	writeJSON(w, map[string]any{"project": projectName(s.agent.dir), "theme": s.theme, "models_ready": ready, "handoff": handoff, "open": open, "model": pinned, "source": answeredBy("", pinned), "plan": a.plan.Load(), "beam": loadSetting("beam") != "off", "quiet": loadSetting("quiet") == "on"})
}

// snapshot converts the agent's messages to wire entries. Callers must hold
// s.mu and must not call it while a chat is running.
func (s *server) snapshot() []wireEntry {
	return s.snapshotAgent(s.agent)
}

func (s *server) snapshotAgent(a *Agent) []wireEntry {
	out := []wireEntry{}
	for _, e := range entriesFrom(a.messages) {
		w := wireEntry{Kind: e.kind, Text: e.text, Tool: e.tool, Arg: e.arg, OK: e.res.ok, Summary: e.res.summary}
		if e.crew != nil {
			w.Crew = e.crew.wire()
		}
		out = append(out, w)
	}
	return out
}

func (s *server) history(w http.ResponseWriter, r *http.Request) {
	a, err := s.agentTab(r)
	if err != nil {
		if he, ok := err.(*httpError); ok {
			http.Error(w, he.msg, he.code)
			return
		}
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	var out []wireEntry
	if s.busy && a == s.busyAgent {
		// agent.Run is mutating messages outside the lock; serve the copy
		// taken before the run started instead of reading live state.
		if a == s.agent {
			out = s.snap
		} else if s.agentSnap != nil {
			out = s.agentSnap[a]
		}
	} else {
		out = s.snapshotAgent(a)
	}
	s.mu.Unlock()
	if out == nil {
		out = []wireEntry{}
	}
	writeJSON(w, map[string]any{"entries": out})
}

// agentTab returns the agent for ?tab=, or s.agent when there is no hub or no
// ?tab=. A bad id gives 400 and an unknown one 404.
func (s *server) agentTab(r *http.Request) (*Agent, error) {
	if s.hub == nil || s.agentFor == nil {
		return s.agent, nil
	}
	idStr := r.URL.Query().Get("tab")
	if idStr == "" {
		return s.agent, nil
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return nil, &httpError{http.StatusBadRequest, "invalid tab id"}
	}
	if a := s.agentFor(id); a != nil {
		return a, nil
	}
	return nil, &httpError{http.StatusNotFound, "tab not found"}
}

// askWeb sends a running turn's questions to the page as an approve event,
// answered with POST /api/approve. A closed page or esc stops the turn, which answers no.
func (s *server) askWeb(send func(map[string]any)) func(context.Context, approval) answer {
	return func(ctx context.Context, q approval) answer {
		reply := make(chan answer, 1)
		s.mu.Lock()
		s.askSeq++
		id := s.askSeq
		if s.asks == nil {
			s.asks = map[int]chan answer{}
		}
		s.asks[id] = reply
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.asks, id)
			s.mu.Unlock()
		}()
		send(map[string]any{"type": "approve", "id": id, "tool": q.tool, "arg": q.arg, "why": q.why, "chat": len(q.risks) > 0})
		select {
		case a := <-reply:
			return a
		case <-ctx.Done():
			return answerNo
		}
	}
}

func (s *server) answerAsk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID     int    `json:"id"`
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		http.Error(w, "send JSON like {\"id\": 1, \"answer\": \"yes\"}", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	reply := s.asks[body.ID]
	delete(s.asks, body.ID)
	s.mu.Unlock()
	if reply == nil {
		http.Error(w, "nothing is waiting for that answer", http.StatusNotFound)
		return
	}
	reply <- answerOf(body.Answer)
	w.WriteHeader(http.StatusNoContent)
}

// tabsWorking says whether a terminal tab sharing this page's hub is mid-turn: the request's
// tab (?tab=, or the page's own agent without one), or any of them when all is true.
func (s *server) tabsWorking(r *http.Request, a *Agent, all bool) bool {
	if s.hub == nil {
		return false
	}
	id := a.tabID
	if n, err := strconv.Atoi(r.URL.Query().Get("tab")); err == nil {
		id = n
	}
	for _, t := range s.hub.list() {
		if (t.Status == "working" || t.Status == "waiting") && (all || t.ID == id) {
			return true
		}
	}
	return false
}

type httpError struct {
	code int
	msg  string
}

func (e *httpError) Error() string { return e.msg }

// tabs serves GET /api/tabs: the tab list plus a version for long-polling.
// ?since=<version> holds the request until the list changes (up to 25 s).
func (s *server) tabs(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		// Single-agent mode: return a minimal single-tab list so the web UI
		// can always call this endpoint safely.
		writeJSON(w, map[string]any{
			"tabs":    []tabInfo{{ID: 1, Name: "main", Status: "idle"}},
			"version": uint64(1),
		})
		return
	}
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if since, err := strconv.ParseUint(sinceStr, 10, 64); err == nil {
			s.hub.wait(r.Context(), since, 25*time.Second)
		}
	}
	writeJSON(w, map[string]any{
		"tabs":    s.hub.list(),
		"version": s.hub.version(),
	})
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
	a, err := s.agentTab(r)
	if err != nil {
		if he, ok := err.(*httpError); ok {
			http.Error(w, he.msg, he.code)
			return
		}
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if s.tabsWorking(r, a, false) {
		http.Error(w, "busy", http.StatusConflict)
		return
	}
	a.reset()
	if a == s.agent {
		s.snap = nil
	}
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
	a, aerr := s.agentTab(r)
	if aerr != nil {
		if he, ok := aerr.(*httpError); ok {
			http.Error(w, he.msg, he.code)
			return
		}
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if s.busy || s.tabsWorking(r, a, false) {
		s.mu.Unlock()
		http.Error(w, "prism is already working on something", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	s.busy, s.cancel, s.busyAgent = true, cancel, a
	if a == s.agent {
		s.snap = s.snapshot()
	} else {
		if s.agentSnap == nil {
			s.agentSnap = make(map[*Agent][]wireEntry)
		}
		s.agentSnap[a] = s.snapshotAgent(a)
	}
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		if a == s.agent {
			s.snap = s.snapshot()
		} else if s.agentSnap != nil {
			delete(s.agentSnap, a)
		}
		s.busy, s.cancel, s.busyAgent, s.lastSeen = false, nil, nil, time.Now()
		s.mu.Unlock()
	}()
	// A memory job from the previous chat may still be running; never let it
	// overlap with agent.Run.
	s.memWG.Wait()

	flusher, _ := w.(http.Flusher)
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	var sendMu sync.Mutex // crew workers may ask from their own goroutines
	send := func(v map[string]any) {
		sendMu.Lock()
		defer sendMu.Unlock()
		data, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	start, route, source, tools, tests := time.Now(), "", "", 0, false
	pending := map[string]string{}
	// This turn's questions go to the page that started it, even on a terminal tab's agent.
	confirm, approve := a.confirm, a.approve
	a.confirm = s.askWeb(send)
	if a.system {
		a.approve = yesNo(a.confirm)
	}
	defer func() { a.confirm, a.approve = confirm, approve }()
	a.Run(ctx, strings.TrimSpace(body.Message), func(event any) {
		if v, ok := crewWire(event); ok {
			send(v)
			return
		}
		switch e := event.(type) {
		case evRoute:
			route, source = e.route, e.source
			send(map[string]any{"type": "route", "route": e.route, "wait_ms": e.wait.Milliseconds(), "source": answeredBy(e.source, "")})
		case evThinking:
			send(map[string]any{"type": "thinking"})
		case evReasoning:
			send(map[string]any{"type": "reasoning", "text": e.text})
		case evText:
			send(map[string]any{"type": "text", "text": e.text})
		case evDropText:
			send(map[string]any{"type": "drop_text", "text": e.text})
		case evDraft:
			send(map[string]any{"type": "draft", "tool": e.name, "arg": e.arg, "chars": e.chars})
		case evToolStart:
			tools++
			pending[e.id] = e.name + " " + e.arg
			send(map[string]any{"type": "tool_start", "id": e.id, "tool": e.name, "arg": e.arg})
		case evToolDone:
			if strings.HasPrefix(pending[e.id], "bash ") && strings.Contains(pending[e.id], "test") && e.result.ok {
				tests = true
			}
			send(map[string]any{"type": "tool_done", "id": e.id, "ok": e.result.ok, "summary": e.result.summary, "added": e.result.added, "removed": e.result.removed, "diff": wire(e.result.diff)})
		case evNote:
			send(map[string]any{"type": "note", "text": e.text})
		case evUsage:
			s.mu.Lock()
			s.requests, s.sent = a.requests, a.sent
			s.mu.Unlock()
			send(map[string]any{"type": "usage", "tokens": e.tokens, "window": e.window})
		case evDone:
			switch {
			case e.err == nil:
				done := map[string]any{"type": "done", "seconds": time.Since(start).Seconds(), "model": modelName(route), "source": answeredBy(source, a.model), "tools": tools, "tests_passed": tests, "plan": a.plan.Load()}
				if a.plan.Load() {
					done["pick"] = pickWire(planMenu(), func(it menuItem) string { return "/plan " + it.match })
				}
				send(done)
			case errors.Is(e.err, context.Canceled):
				send(map[string]any{"type": "error", "headline": "stopped", "detail": ""})
			default:
				headline, detail := friendly(e.err)
				send(map[string]any{"type": "error", "headline": headline, "detail": detail})
			}
		}
	})
	if job := a.memoryJob(); job != nil {
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

func toneName(t tone) string {
	switch t {
	case toneOK:
		return "ok"
	case toneErr:
		return "err"
	case toneAccent:
		return "accent"
	case toneFg:
		return "fg"
	}
	return "muted"
}

// pickWire turns a menu into JSON for the web picker. run gives the slash
// command a row runs when it is picked.
func pickWire(p menuState, run func(it menuItem) string) map[string]any {
	items := []map[string]any{}
	for _, it := range p.items {
		w := map[string]any{"label": it.label, "detail": it.detail, "state": it.state, "dot": it.dot,
			"dotTone": toneName(it.dotTone), "stateTone": toneName(it.stateTone), "sub": it.sub, "match": it.match, "run": run(it)}
		if it.dot == "◌" && it.dotTone == toneMuted && it.stateTone == toneMuted && p.kind == "model" {
			w["dim"] = true
		}
		items = append(items, w)
	}
	out := map[string]any{"kind": "pick", "pick": p.kind, "title": p.title, "hint": p.hint, "filterable": p.filterable,
		"noun": p.noun, "empty": p.empty, "sel": p.sel, "items": items}
	if p.err != "" {
		out["error"] = p.err
	}
	return out
}

// modelsTable is /models for the web: the rows filterRoutes kept, in columns
// (model, provider, state, yours/plan).
func modelsTable(rows []routeRow, more int, filter string) map[string]any {
	title := "models, in the order prism tries them"
	if filter != "" {
		title = fmt.Sprintf("models matching %q", filter)
	}
	out := []map[string]any{}
	group := -1
	for _, r := range rows {
		if g := groupOf(r); g != group {
			group = g
			out = append(out, map[string]any{"head": routeGroups[g].title, "tone": answeredBy(r.Source, "")})
		}
		dot, tone, state := "●", "ok", "ready"
		if !r.Ready {
			dot, tone, state = "◌", "muted", fmt.Sprintf("back in %ds", r.RetryIn)
		}
		out = append(out, map[string]any{"dot": dot, "tone": tone, "cells": []string{modelName(r.Route), provider(r.Route), state}})
	}
	var footer []string
	if more > 0 {
		footer = append(footer, fmt.Sprintf("+%d more · /models <text> narrows", more))
	}
	if len(rows) == 0 && filter != "" {
		footer = append(footer, fmt.Sprintf("no model matches %q · /models lists them all", filter))
	}
	if len(rows) == 0 && filter == "" {
		footer = append(footer, "no models ready yet · add one: prism setup or /key")
	}
	footer = append(footer, "pin one for this chat: /model · more free models: prism setup · your plans: /login · your own keys: /key")
	return map[string]any{"kind": "table", "title": title, "rows": out, "footer": footer}
}

// commandList serves GET /api/commands: every command, and which ones the
// browser can run.
func (s *server) commandList(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	for _, c := range commands {
		out = append(out, map[string]any{"name": c.name, "help": c.help, "web": c.web})
	}
	// Saved commands follow, re-read when their files change.
	a, _ := s.agentTab(r)
	if a == nil {
		a = s.agent
	}
	for _, c := range loadUserCommands(a.dir, 0) {
		if c.runnable() {
			out = append(out, map[string]any{"name": c.name, "help": c.help, "web": true, "saved": true, "source": c.source})
		}
	}
	w.Header().Set("cache-control", "no-store")
	writeJSON(w, out)
}

// modelCount reads the router's model count, sent as a number or a list.
func modelCount(raw json.RawMessage) int {
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return len(list)
	}
	return 0
}

// loginPoll serves POST /api/login {id, browser}: one check of a sign-in the
// web started with /login <id>. Browser accounts are done once GET /logins
// says so and they are no longer finishing; device-code accounts poll the
// router. status is pending, ok or error. With {id, code} it finishes a
// waiting browser sign-in with the code the user pasted.
func (s *server) loginPoll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string `json:"id"`
		Browser bool   `json:"browser"`
		Code    string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
		http.Error(w, "send JSON like {\"id\": \"copilot\"}", http.StatusBadRequest)
		return
	}
	a, err := s.agentTab(r)
	if err != nil {
		a = s.agent
	}
	if code := strings.TrimSpace(body.Code); code != "" {
		writeJSON(w, loginCode(a.api, body.ID, code))
		return
	}
	reply := func(status string, models int, note, message string, interval int) {
		out := map[string]any{"status": status, "models": models, "note": note, "message": message, "interval": interval}
		if status == "ok" {
			out["text"] = "signed in · " + countModels(models)
		}
		writeJSON(w, out)
	}
	if body.Browser {
		rows, err := fetchLogins(a.api)
		if err != nil {
			reply("error", 0, "", err.Error(), 0)
			return
		}
		for _, row := range rows {
			switch {
			case row.ID != body.ID:
				continue
			case row.Finishing || row.Pending:
				reply("pending", 0, "", row.Error, 3)
			case row.SignedIn:
				reply("ok", row.Models, row.Error, "", 0)
			case row.Error != "":
				reply("error", 0, "", row.Error, 0)
			default:
				reply("error", 0, "", "the sign-in was cancelled", 0)
			}
			return
		}
		reply("error", 0, "", "the router has no account called "+body.ID, 0)
		return
	}
	var out struct {
		Status   string          `json:"status"`
		Interval int             `json:"interval"`
		Models   json.RawMessage `json:"models"`
		Note     string          `json:"note"`
		Message  string          `json:"message"`
		Error    *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := loginRound(a.api, map[string]any{"id": body.ID, "poll": true}, &out); err != nil {
		reply("error", 0, "", err.Error(), 0)
		return
	}
	switch {
	case out.Error != nil:
		reply("error", 0, "", out.Error.Message, 0)
	case out.Status == "ok":
		reply("ok", modelCount(out.Models), out.Note, "", 0)
	case out.Status == "error":
		reply("error", 0, "", out.Message, 0)
	case out.Status == "idle":
		reply("error", 0, "", "the sign-in was not started; run /login "+body.ID+" again", 0)
	default:
		reply("pending", 0, "", "", out.Interval)
	}
}

// loginCode passes a pasted code (bare, code#state or the whole callback URL)
// to the router's POST /logins {id, code}, for providers whose page shows a
// code instead of redirecting back. It answers {ok, models, note, text} or
// {ok:false, error}.
func loginCode(api, id, code string) map[string]any {
	var out struct {
		OK     bool            `json:"ok"`
		Models json.RawMessage `json:"models"`
		Note   string          `json:"note"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	fail := func(msg string) map[string]any { return map[string]any{"ok": false, "error": msg} }
	if err := loginRound(api, map[string]any{"id": id, "code": code}, &out); err != nil {
		return fail(err.Error())
	}
	if out.Error != nil {
		msg := out.Error.Message
		if msg == "" {
			msg = "that code did not work; copy it again"
		}
		return fail(msg)
	}
	if !out.OK {
		return fail("the router did not finish the sign-in; run /login " + id + " again")
	}
	n := modelCount(out.Models)
	return map[string]any{"ok": true, "models": n, "note": out.Note, "text": "signed in · " + countModels(n)}
}

// compact is /compact [focus] for the web. It runs outside s.mu (the summary
// is a model request) but marks the server busy so no chat starts meanwhile,
// and /api/stop cancels it.
func (s *server) compact(w http.ResponseWriter, r *http.Request, a *Agent, focus string) {
	s.mu.Lock()
	if s.busy || s.tabsWorking(r, a, false) {
		s.mu.Unlock()
		writeJSON(w, map[string]any{"kind": "error", "text": "wait until prism finishes, then run /compact"})
		return
	}
	if len(a.messages) <= 1 {
		s.mu.Unlock()
		writeJSON(w, map[string]any{"kind": "note", "text": "nothing to compact yet"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	s.busy, s.cancel, s.busyAgent = true, cancel, a
	if a == s.agent {
		s.snap = s.snapshot()
	} else {
		if s.agentSnap == nil {
			s.agentSnap = make(map[*Agent][]wireEntry)
		}
		s.agentSnap[a] = s.snapshotAgent(a)
	}
	s.mu.Unlock()
	s.memWG.Wait()
	lines, err := compactChat(ctx, a, focus)
	s.mu.Lock()
	if a == s.agent {
		s.snap = s.snapshot()
	} else if s.agentSnap != nil {
		delete(s.agentSnap, a)
	}
	s.busy, s.cancel, s.busyAgent = false, nil, nil
	s.mu.Unlock()
	if err != nil {
		writeJSON(w, map[string]any{"kind": "error", "headline": "compact", "text": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"kind": "info", "title": "compact", "lines": lines, "tokens": a.tokens, "window": a.window})
}

func (s *server) command(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		http.Error(w, "send JSON like {\"text\": \"/models\"}", http.StatusBadRequest)
		return
	}
	fields := strings.Fields(body.Text)
	if len(fields) == 0 {
		writeJSON(w, map[string]any{"kind": "error", "text": "empty command"})
		return
	}
	a, _ := s.agentTab(r)
	if a == nil {
		a = s.agent
	}
	if fields[0] == "/compact" {
		s.compact(w, r, a, strings.Join(fields[1:], " "))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	busy := s.busy
	info := func(title string, lines []string) {
		writeJSON(w, map[string]any{"kind": "info", "title": title, "lines": lines})
	}
	note := func(text string) {
		writeJSON(w, map[string]any{"kind": "note", "text": text})
	}
	fail := func(text string) {
		writeJSON(w, map[string]any{"kind": "error", "text": text})
	}
	switch fields[0] {
	case "/models", "/status":
		rows, err := fetchRoutes(a.api)
		if err != nil {
			fail(err.Error())
			return
		}
		filter := strings.Join(fields[1:], " ")
		kept, more := filterRoutes(rows, filter)
		writeJSON(w, modelsTable(kept, more, filter))
	case "/model":
		if len(fields) == 1 {
			rows, err := fetchRoutes(a.api)
			p := menuState{kind: "model", title: "model", hint: "↑↓ move · enter pin · esc close", filterable: true, noun: "model"}
			if err != nil {
				p.err = err.Error()
			}
			if err == nil || len(rows) > 0 {
				p.items = modelMenu(rows, a.model)
			}
			writeJSON(w, pickWire(p, func(it menuItem) string { return "/model " + it.match }))
			return
		}
		if busy {
			fail("wait until prism finishes, then pin a model")
			return
		}
		text := pinModel(a, fields[1])
		writeJSON(w, map[string]any{"kind": "model", "model": a.model, "source": answeredBy("", a.model), "text": text})
	case "/key", "/keys":
		if len(fields) == 1 {
			list, err := fetchKeys(a.api)
			if err != nil {
				fail(err.Error())
				return
			}
			info("keys", keyLines(list))
			return
		}
		var id, value string
		if fields[1] == "rm" || fields[1] == "remove" {
			if len(fields) != 3 {
				fail("usage: /key rm <name>")
				return
			}
			id = fields[2]
		} else if len(fields) == 3 {
			id, value = fields[1], fields[2]
		} else {
			fail("usage: /key <name> <secret>")
			return
		}
		msg, err := postKey(a.api, id, value)
		if err != nil {
			fail(err.Error())
			return
		}
		if msg.masked == "" {
			info("removed the "+id+" key", []string{})
			return
		}
		lines := []string{"saved · " + msg.masked}
		if msg.models > 0 {
			lines = append(lines, countModels(msg.models)+" ready · pick one with /model")
		}
		if msg.note != "" {
			lines = append(lines, msg.note)
		}
		info(id+" key", lines)
	case "/login", "/logins":
		s.login(w, a, fields)
	case "/skills":
		if len(fields) > 1 && fields[1] == "new" {
			if busy {
				fail("wait until prism finishes")
				return
			}
			if len(fields) == 2 {
				note("usage: /skills new <name>")
				return
			}
			note(newSkill(a, fields[2]))
			return
		}
		if len(fields) > 1 {
			sk, ok := findSkill(a.skills, fields[1])
			if !ok {
				note("no skill called " + fields[1] + " · /skills lists them")
				return
			}
			writeJSON(w, skillView(sk).wire())
			return
		}
		writeJSON(w, skillsView(a.skills).wire())
	case "/commands":
		if len(fields) > 1 && fields[1] == "new" {
			if len(fields) == 2 {
				note("usage: /commands new <name>")
				return
			}
			note(newUserCommand(a.dir, fields[2]))
			return
		}
		writeJSON(w, commandsView(loadUserCommands(a.dir, 0)).wire())
	case "/help":
		writeJSON(w, helpViewFor(true).wire())
	case "/mcp":
		if len(fields) == 2 && strings.ToLower(fields[1]) == "add" {
			// The catalog text stays in lines (older pages print it); the web
			// opens the picker from "pick".
			cat := mcpCatalogReply(a.dir)
			writeJSON(w, map[string]any{"kind": "info", "title": cat.title, "lines": cat.lines,
				"pick": pickWire(mcpMenu(a.dir), func(it menuItem) string { return "/mcp add " + it.label })})
			return
		}
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
	case "/tree":
		title, lines := treeCommand(a, strings.Join(fields[1:], " "))
		info(title, lines)
	case "/beam", "/quiet":
		key := strings.TrimPrefix(fields[0], "/")
		on := loadSetting(key) == "on" || (key == "beam" && loadSetting(key) != "off")
		on = !on
		if len(fields) > 1 {
			on = fields[1] != "off"
		}
		state := map[bool]string{true: "on", false: "off"}[on]
		saveSetting(key, state)
		writeJSON(w, map[string]any{"kind": "setting", "name": key, "on": on})
	case "/usage":
		u := todayUsage()
		info("AI usage", []string{
			fmt.Sprintf("today: %d requests, %s tokens sent", u.Requests, short(u.Tokens)),
			fmt.Sprintf("this chat: %d requests, %s tokens sent, %s cached", s.requests, short(s.sent), short(s.agent.cached)),
			usageFooter(u),
		})
	case "/memory", "/forget":
		if busy {
			fail("wait until prism finishes")
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
	case "/undo":
		if busy {
			fail("wait until prism finishes, then run /undo")
			return
		}
		title, lines, msg := undoLast(a)
		if msg != "" {
			note(msg)
			return
		}
		info(title, lines)
	case "/clear":
		if busy {
			fail("wait until prism finishes, or press esc to stop it first")
			return
		}
		a.saveSession()
		a.reset()
		if a == s.agent {
			s.snap = nil
		}
		writeJSON(w, map[string]any{"kind": "clear"})
	case "/resume", "/continue":
		if busy {
			fail("wait until prism finishes")
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
				fail("no chat with that number, try /resume")
				return
			}
			full, err := a.store.loadSession(others[k-1].ID)
			if err != nil {
				fail(err.Error())
				return
			}
			a.resume(full)
			s.snap = nil
			writeJSON(w, map[string]any{"kind": "reload", "model": a.model, "source": answeredBy("", a.model)})
			return
		}
		if len(others) == 0 {
			info("no earlier chats in this folder yet", []string{})
			return
		}
		writeJSON(w, pickWire(resumeMenu(others), func(it menuItem) string { return "/resume " + it.dot }))
	case "/init":
		if busy {
			fail("wait until prism finishes, then run /init")
			return
		}
		writeJSON(w, map[string]any{"kind": "send", "message": initPrompt})
	case "/rules":
		if (busy || s.tabsWorking(r, a, true)) && len(fields) > 1 {
			fail("wait until prism finishes, then " + strings.Join(fields, " "))
			return
		}
		title, lines, footer := rulesView(a.dir, fields[1:])
		if footer != "" {
			lines = append(lines, footer)
		}
		info(title, lines)
	case "/hooks":
		if (busy || s.tabsWorking(r, a, true)) && len(fields) > 1 {
			fail("wait until prism finishes, then " + strings.Join(fields, " "))
			return
		}
		title, lines, footer := hooksView(a.dir, fields[1:])
		if footer != "" {
			lines = append(lines, footer)
		}
		info(title, lines)
	case "/plan":
		if busy || s.tabsWorking(r, a, false) {
			fail("wait until prism finishes, then /plan")
			return
		}
		on, text, message := planSwitch(a, commandArg(body.Text, fields[0]))
		writeJSON(w, map[string]any{"kind": "plan", "plan": on, "text": text, "message": message})
	case "/editor":
		note("/editor works in the terminal · here, click a file in Files, then Edit")
	case "/rename":
		note("/rename works in the terminal · the tab names it sets show here")
	case "/ask":
		note("/ask works in the terminal · here, /tabs switches to another chat")
	case "/quit", "/exit":
		note("nothing to quit here · close this browser tab to leave")
	default:
		if msg, ok := savedMessage(a.dir, body.Text); ok {
			if busy {
				fail("wait until prism finishes, then run " + fields[0])
				return
			}
			if msg == "" || strings.HasPrefix(msg, "/") {
				note(fields[0] + " has nothing to send · /commands shows where its file is")
				return
			}
			writeJSON(w, map[string]any{"kind": "send", "message": msg})
			return
		}
		writeJSON(w, map[string]any{"kind": "error", "headline": "unknown command", "text": fields[0] + " isn't a command. /help lists them"})
	}
}

// login is /login for the web: the account picker, sign-out, or starting a
// sign-in. Browser accounts answer with a URL the web
// opens; device-code accounts with a code. The web then polls /api/login.
func (s *server) login(w http.ResponseWriter, a *Agent, fields []string) {
	fail := func(text string) { writeJSON(w, map[string]any{"kind": "error", "text": text}) }
	if len(fields) == 1 {
		rows, err := fetchLogins(a.api)
		p := menuState{kind: "login", title: "subscriptions", hint: "↑↓ move · enter sign in · esc close", filterable: true,
			noun: "account", empty: "the router lists no subscription accounts", items: loginItems(rows)}
		if err != nil {
			p.err = err.Error()
		}
		last := ""
		writeJSON(w, pickWire(p, func(it menuItem) string {
			if it.sub {
				return "/login rm " + last
			}
			last = it.label
			return "/login " + it.label
		}))
		return
	}
	if fields[1] == "rm" || fields[1] == "remove" {
		if len(fields) != 3 {
			fail("usage: /login rm <name>")
			return
		}
		out := struct {
			OK    bool `json:"ok"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}{}
		if err := loginRound(a.api, map[string]any{"id": fields[2], "signOut": true}, &out); err != nil || out.Error != nil {
			msg := errText(err)
			if out.Error != nil {
				msg = out.Error.Message
			}
			fail(msg)
			return
		}
		writeJSON(w, map[string]any{"kind": "note", "text": "signed out of " + fields[2]})
		return
	}
	if len(fields) > 2 {
		out := loginCode(a.api, fields[1], strings.Join(fields[2:], " "))
		if out["ok"] != true {
			fail(fmt.Sprint(out["error"]))
			return
		}
		text := fmt.Sprint(out["text"])
		if note, _ := out["note"].(string); note != "" {
			text += " · " + note
		}
		writeJSON(w, map[string]any{"kind": "note", "text": text})
		return
	}
	id := fields[1]
	var out struct {
		UserCode        string `json:"userCode"`
		VerificationURI string `json:"verificationURI"`
		URL             string `json:"url"`
		Expires         int    `json:"expires"`
		Interval        int    `json:"interval"`
		Error           *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := loginRound(a.api, map[string]any{"id": id}, &out); err != nil {
		fail(err.Error())
		return
	}
	if out.Error != nil {
		fail(out.Error.Message)
		return
	}
	link := out.URL
	if link == "" {
		link = out.VerificationURI
	}
	if link == "" {
		fail("the router sent no sign-in link for " + id + "; try /login " + id + " again")
		return
	}
	if out.Expires <= 0 {
		out.Expires = 900
	}
	browser := out.UserCode == ""
	lines := []string{"open  " + link, "type  " + out.UserCode, "prism keeps checking; the models appear in /models when you are done"}
	if browser {
		lines = []string{"opening your browser…", "if nothing opens: " + link, "prism keeps checking; the models appear in /models when you are done"}
	}
	writeJSON(w, map[string]any{"kind": "login", "id": id, "browser": browser, "url": link, "code": out.UserCode,
		"expires": out.Expires, "interval": out.Interval, "title": "sign in to " + id, "lines": lines})
}

// mcpMenu is the /mcp add picker. Keep in sync with openMCPMenu in mcp_setup.go.
func mcpMenu(dir string) menuState {
	cfg, _ := loadMCPConfig(dir)
	var items []menuItem
	for _, e := range mcpCatalog() {
		it := menuItem{label: e.ID, detail: e.About, dot: "◌"}
		if _, on := cfg[e.ID]; on {
			it.dot, it.dotTone, it.state, it.stateTone = "●", toneOK, "added", toneOK
		} else if len(e.Needs) > 0 {
			it.state = "needs a free key"
		}
		items = append(items, it)
	}
	return menuState{kind: "mcp", title: "add a tool server", hint: "↑↓ move · enter add · esc close", filterable: true,
		noun: "server", empty: "the catalog is empty", items: items}
}
