package main

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

//go:embed mcp_catalog.json
var mcpCatalogJSON []byte

type mcpNeed struct {
	Name  string `json:"name"`
	Where string `json:"where"`
}

type mcpCatalogEntry struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	About   string            `json:"about"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Needs   []mcpNeed         `json:"needs,omitempty"`
	Try     string            `json:"try"`
}

func (e mcpCatalogEntry) config() mcpServerConfig {
	return mcpServerConfig{Command: e.Command, Args: e.Args, Env: e.Env, URL: e.URL, Headers: e.Headers}
}

func mcpCatalog() []mcpCatalogEntry {
	var list []mcpCatalogEntry
	if err := json.Unmarshal(mcpCatalogJSON, &list); err != nil {
		return nil
	}
	return list
}

func mcpFindCatalog(arg string) (mcpCatalogEntry, bool) {
	list := mcpCatalog()
	arg = strings.ToLower(strings.TrimSpace(arg))
	var n int
	if _, err := fmt.Sscanf(arg, "%d", &n); err == nil && fmt.Sprint(n) == arg {
		if n >= 1 && n <= len(list) {
			return list[n-1], true
		}
		return mcpCatalogEntry{}, false
	}
	for _, e := range list {
		if e.ID == arg {
			return e, true
		}
	}
	return mcpCatalogEntry{}, false
}

func mcpWhere(name string) string {
	for _, e := range mcpCatalog() {
		for _, n := range e.Needs {
			if n.Name == name {
				return n.Where
			}
		}
	}
	return ""
}

// keys.conf feeds only the router container (the AI model keys). The agent
// container, where MCP servers run, never sees it, so keys for MCP servers live
// in mcp.env inside prism's own settings folder instead. That folder is a
// Docker volume, so it survives restarts, and it is never part of a project.

var mcpVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func mcpEnvPath() string {
	dir := configDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "mcp.env")
}

func mcpSecrets() map[string]string {
	out := map[string]string{}
	f, err := os.Open(mcpEnvPath())
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out
}

func mcpLookup(name string) (string, bool) {
	if v := os.Getenv(name); v != "" {
		return v, true
	}
	v, ok := mcpSecrets()[name]
	return v, ok && v != ""
}

func mcpExpand(s string) string {
	return os.Expand(s, func(k string) string {
		v, _ := mcpLookup(k)
		return v
	})
}

func mcpSetKey(name, value string) error {
	if !mcpVarName.MatchString(name) {
		return errors.New("a key name uses letters, numbers and underscores, like GITHUB_TOKEN")
	}
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return errors.New("the key must be one line of text")
	}
	path := mcpEnvPath()
	if path == "" {
		return errors.New("prism has no settings folder on this computer")
	}
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			if k, _, ok := strings.Cut(l, "="); ok && strings.TrimSpace(k) == name {
				continue
			}
			if l != "" {
				lines = append(lines, l)
			}
		}
	}
	lines = append(lines, name+"="+value)
	return mcpWriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func mcpVars(s string) []string {
	var names []string
	os.Expand(s, func(k string) string {
		names = append(names, k)
		return ""
	})
	return names
}

func mcpConfigVars(cfg mcpServerConfig) []string {
	seen := map[string]bool{}
	var out []string
	add := func(m map[string]string) {
		for _, v := range m {
			for _, n := range mcpVars(v) {
				if !seen[n] {
					seen[n] = true
					out = append(out, n)
				}
			}
		}
	}
	add(cfg.Env)
	add(cfg.Headers)
	sort.Strings(out)
	return out
}

func mcpMissingVars(cfg mcpServerConfig) []string {
	var missing []string
	for _, n := range mcpConfigVars(cfg) {
		if _, ok := mcpLookup(n); !ok {
			missing = append(missing, n)
		}
	}
	return missing
}

type mcpMissingEnvError struct{ names []string }

func (e *mcpMissingEnvError) Error() string {
	var parts []string
	for _, n := range e.names {
		p := "needs the key " + n + " (not set yet)."
		if where := mcpWhere(n); where != "" {
			p += " Get it at " + where + "."
		}
		p += " Then type: /mcp key " + n + " <paste it here>"
		parts = append(parts, p)
	}
	return strings.Join(parts, " ")
}

func mcpProjectPath(dir string) string { return filepath.Join(dir, ".prism", "mcp.json") }

// mcpWriteFile replaces path atomically (temp file in the same folder, fsync,
// rename) so a crash never leaves a half-written config.
func mcpWriteFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".mcp-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	fail := func(err error) error {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Chmod(mode); err != nil {
		return fail(err)
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// mcpReadProject returns the whole project file (so unknown keys survive) and
// its server entries kept as raw JSON (so hand-written entries are never
// rewritten). A missing file is empty; a broken one is an error, never overwritten.
func mcpReadProject(dir string) (map[string]json.RawMessage, map[string]json.RawMessage, error) {
	top := map[string]json.RawMessage{}
	servers := map[string]json.RawMessage{}
	data, err := os.ReadFile(mcpProjectPath(dir))
	if errors.Is(err, os.ErrNotExist) || (err == nil && strings.TrimSpace(string(data)) == "") {
		return top, servers, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, nil, fmt.Errorf(".prism/mcp.json isn't valid JSON (%v). Fix it first (usually a missing comma or quote), then try again", err)
	}
	if raw, ok := top["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, nil, fmt.Errorf(`"mcpServers" in .prism/mcp.json must be an object of servers (%v)`, err)
		}
	}
	return top, servers, nil
}

func mcpWriteProject(dir string, top, servers map[string]json.RawMessage) error {
	raw, err := json.Marshal(servers)
	if err != nil {
		return err
	}
	top["mcpServers"] = raw
	data, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	return mcpWriteFile(mcpProjectPath(dir), append(data, '\n'), 0o644)
}

// mcpAddServer adds a catalog entry to the project file under its id. An
// existing entry is left as it is. Reports whether it was newly written.
func mcpAddServer(dir string, e mcpCatalogEntry) (bool, error) {
	top, servers, err := mcpReadProject(dir)
	if err != nil {
		return false, err
	}
	if _, ok := servers[e.ID]; ok {
		return false, nil
	}
	entry, err := json.Marshal(struct {
		Command string            `json:"command,omitempty"`
		Args    []string          `json:"args,omitempty"`
		Env     map[string]string `json:"env,omitempty"`
		URL     string            `json:"url,omitempty"`
		Headers map[string]string `json:"headers,omitempty"`
	}{e.Command, e.Args, e.Env, e.URL, e.Headers})
	if err != nil {
		return false, err
	}
	servers[e.ID] = entry
	return true, mcpWriteProject(dir, top, servers)
}

func mcpRemoveServer(dir, name string) (bool, error) {
	top, servers, err := mcpReadProject(dir)
	if err != nil {
		return false, err
	}
	if _, ok := servers[name]; !ok {
		return false, nil
	}
	delete(servers, name)
	return true, mcpWriteProject(dir, top, servers)
}

// holds reports whether s is still one of the hub's servers. Call under h.mu.
func (h *mcpHub) holds(s *mcpServer) bool {
	for _, x := range h.servers {
		if x == s {
			return true
		}
	}
	return false
}

func (h *mcpHub) restart(name string, cfg mcpServerConfig) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	s := &mcpServer{name: name, config: cfg, state: "starting"}
	if cfg.Off {
		s.state = "off"
	}
	replaced := false
	for i, old := range h.servers {
		if old.name == name {
			if old.conn != nil {
				old.conn.close()
			}
			h.servers[i], replaced = s, true
			break
		}
	}
	if !replaced {
		h.servers = append(h.servers, s)
	}
	if !cfg.Off {
		go h.connect(s)
	}
}

func (h *mcpHub) drop(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, s := range h.servers {
		if s.name == name {
			if s.conn != nil {
				s.conn.close()
			}
			h.servers = append(h.servers[:i:i], h.servers[i+1:]...)
			return true
		}
	}
	return false
}

// await waits until the named server is no longer starting, up to limit.
func (h *mcpHub) await(name string, limit time.Duration) (mcpStatus, bool) {
	deadline := time.Now().Add(limit)
	for {
		for _, st := range h.status() {
			if st.name == name && st.state != "starting" {
				return st, true
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, st := range h.status() {
		if st.name == name {
			return st, false
		}
	}
	return mcpStatus{name: name, state: "error", err: "not found"}, true
}

func mcpExplain(name, raw string) string {
	low := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(raw, "needs the key "):
		return raw
	case strings.Contains(raw, "isn't installed in prism's box"):
		cmd, _, _ := strings.Cut(raw, " ")
		switch cmd {
		case "npx", "node", "npm":
			return fmt.Sprintf("%s isn't installed. Prism's Docker box includes Node.js; on your own computer install it from https://nodejs.org, then /mcp test %s", cmd, name)
		case "uvx", "uv", "pipx", "python", "python3":
			return fmt.Sprintf("%s isn't available here. Pick a server that starts with npx (/mcp add lists some), then /mcp test %s", cmd, name)
		}
		return fmt.Sprintf("%s isn't installed here. Install it, or pick another server with /mcp add (%s)", cmd, raw)
	case strings.Contains(low, "deadline exceeded") || strings.Contains(low, "timed out") || strings.Contains(low, "timeout"):
		return fmt.Sprintf("took too long to start (%s). The first run downloads it, so check your internet and try /mcp test %s", raw, name)
	case strings.Contains(low, "http 401") || strings.Contains(low, "http 403") || strings.Contains(low, "missing required authorization"):
		return fmt.Sprintf("refused the key (%s). Check the key is right and hasn't expired, then set it again with /mcp key <NAME> <new key>", raw)
	case strings.Contains(low, "http 404"):
		return fmt.Sprintf("the address wasn't found (%s). Check the url in .prism/mcp.json", raw)
	case strings.Contains(low, "connection refused"), strings.Contains(low, "no such host"), strings.Contains(low, "dial tcp"), strings.Contains(low, "network is unreachable"):
		return fmt.Sprintf("can't reach it (%s). Check your internet connection, then /mcp test %s", raw, name)
	case strings.Contains(low, "the tool stopped") || strings.Contains(low, "broken pipe") || strings.Contains(low, "file already closed") || strings.HasSuffix(low, "eof"):
		return fmt.Sprintf("crashed right after starting (%s). That is usually a wrong or missing key, or no internet; fix it, then /mcp test %s", raw, name)
	case strings.Contains(raw, `needs a "command" or a "url"`):
		return `the entry has no "command" or "url". Fix it in .prism/mcp.json or /mcp remove ` + name
	}
	return raw
}

type mcpReply struct {
	title  string
	lines  []string
	failed bool
}

func (r mcpReply) oneLine() string {
	var parts []string
	for _, l := range append([]string{r.title}, r.lines...) {
		if strings.TrimSpace(l) != "" {
			parts = append(parts, strings.TrimSpace(l))
		}
	}
	return strings.Join(parts, " · ")
}

func mcpFail(msg string, hints ...string) (mcpReply, func(time.Duration) (mcpReply, bool)) {
	return mcpReply{title: msg, lines: hints, failed: true}, nil
}

// mcpCommand runs /mcp <sub> .... The second value, when set, waits for a
// server that was just (re)started and reports ready or the plain-language
// problem; done is false when it was still starting at the limit.
func mcpCommand(a *Agent, fields []string) (mcpReply, func(time.Duration) (mcpReply, bool)) {
	if a.mcp == nil {
		a.mcp = &mcpHub{}
	}
	sub := ""
	if len(fields) > 1 {
		sub = strings.ToLower(fields[1])
	}
	arg := ""
	if len(fields) > 2 {
		arg = fields[2]
	}
	switch sub {
	case "", "list", "status":
		return mcpReply{title: "connected tools (MCP)", lines: mcpLines(a.mcp.status())}, nil
	case "add":
		if arg == "" {
			return mcpCatalogReply(a.dir), nil
		}
		e, ok := mcpFindCatalog(arg)
		if !ok {
			return mcpFail("no server called "+arg, "/mcp add shows the list, then use its name or number")
		}
		added, err := mcpAddServer(a.dir, e)
		if err != nil {
			return mcpFail("couldn't save "+e.ID, err.Error())
		}
		msg := "added " + e.ID + " to .prism/mcp.json · " + e.About
		if !added {
			msg = e.ID + " is already in .prism/mcp.json (left as you have it) · reconnecting"
		}
		cfg, _ := loadMCPConfig(a.dir)
		a.mcp.restart(e.ID, cfg[e.ID])
		return mcpReply{title: msg}, mcpFollow(a.mcp, e.ID)
	case "key":
		if arg == "" || len(fields) < 4 {
			return mcpFail("usage: /mcp key NAME value",
				"example: /mcp key GITHUB_TOKEN ghp_abc123",
				"it is saved only on this computer, in prism's settings folder (mcp.env), and never sent to the AI",
				"keys.conf is only for the AI model keys, MCP servers don't see it")
		}
		if err := mcpSetKey(arg, fields[3]); err != nil {
			return mcpFail("couldn't save the key", err.Error())
		}
		cfg, _ := loadMCPConfig(a.dir)
		var names []string
		for name, c := range cfg {
			if c.Off {
				continue
			}
			for _, v := range mcpConfigVars(c) {
				if v == arg {
					names = append(names, name)
					break
				}
			}
		}
		sort.Strings(names)
		for _, n := range names {
			a.mcp.restart(n, cfg[n])
		}
		if len(names) == 0 {
			return mcpReply{title: "saved " + arg + " · no server uses it yet, /mcp add shows servers"}, nil
		}
		return mcpReply{title: "saved " + arg + " · reconnecting " + strings.Join(names, ", ")}, mcpFollow(a.mcp, names...)
	case "remove", "rm", "delete":
		if arg == "" {
			return mcpFail("usage: /mcp remove <name>", "/mcp shows the names")
		}
		found, err := mcpRemoveServer(a.dir, arg)
		if err != nil {
			return mcpFail("couldn't remove "+arg, err.Error())
		}
		dropped := a.mcp.drop(arg)
		if found {
			return mcpReply{title: "removed " + arg + " from .prism/mcp.json and disconnected it"}, nil
		}
		if cfg, _ := loadMCPConfig(a.dir); cfg != nil {
			if _, ok := cfg[arg]; ok {
				return mcpFail(arg+" isn't in this project's .prism/mcp.json",
					"it comes from ~/.config/prism/mcp.json: delete its entry there, or set \"disabled\": true")
			}
		}
		if dropped {
			return mcpReply{title: "disconnected " + arg}, nil
		}
		return mcpFail("no server called "+arg, "/mcp shows what is connected")
	case "test", "reconnect":
		cfg, _ := loadMCPConfig(a.dir)
		if arg == "" {
			var names []string
			for n := range cfg {
				names = append(names, n)
			}
			sort.Strings(names)
			return mcpFail("usage: /mcp test <name>", "servers: "+strings.Join(names, ", "))
		}
		c, ok := cfg[arg]
		if !ok {
			return mcpFail("no server called "+arg, "/mcp add lists servers you can add")
		}
		if c.Off {
			return mcpFail(arg+` is switched off ("disabled": true in its mcp.json entry)`, "remove that line to turn it on")
		}
		a.mcp.restart(arg, c)
		return mcpReply{title: "testing " + arg}, mcpFollow(a.mcp, arg)
	}
	return mcpFail("/mcp doesn't know \""+sub+"\"", "/mcp · /mcp add · /mcp add <name or number> · /mcp key NAME value · /mcp remove <name> · /mcp test <name>")
}

// openMCPMenu is /mcp add with no name: pick a catalog server to add.
func (m *model) openMCPMenu() {
	cfg, _ := loadMCPConfig(m.agent.dir)
	var items []menuItem
	for _, e := range mcpCatalog() {
		it := menuItem{label: e.ID, detail: e.About, dot: "◌"}
		if _, on := cfg[e.ID]; on {
			it.dot, it.dotTone, it.state, it.stateTone = "●", toneOK, "added", toneOK
		} else if len(e.Needs) > 0 {
			it.state = "needs a free key"
		}
		id := e.ID
		it.run = func(m *model) tea.Cmd { return m.command("/mcp add " + id) }
		items = append(items, it)
	}
	m.openMenu(menuState{
		kind:       "mcp",
		title:      "add a tool server",
		hint:       "↑↓ move · enter add · esc close",
		filterable: true,
		noun:       "server",
		empty:      "the catalog is empty",
		items:      items,
	})
}

func mcpCatalogReply(dir string) mcpReply {
	cfg, _ := loadMCPConfig(dir)
	var lines []string
	for i, e := range mcpCatalog() {
		tag := ""
		if _, on := cfg[e.ID]; on {
			tag = "  [added]"
		} else if len(e.Needs) > 0 {
			tag = "  [needs a free key]"
		}
		lines = append(lines, fmt.Sprintf("%d  %-9s %s%s", i+1, e.ID, e.About, tag))
	}
	lines = append(lines, "", "add one: /mcp add 1  (or /mcp add fetch) · it is saved in this project's .prism/mcp.json", "remove: /mcp remove <name> · recheck: /mcp test <name>")
	return mcpReply{title: "MCP servers you can add (all free)", lines: lines}
}

func mcpFollow(h *mcpHub, names ...string) func(time.Duration) (mcpReply, bool) {
	return func(limit time.Duration) (mcpReply, bool) {
		var lines []string
		done := true
		deadline := time.Now().Add(limit)
		for _, name := range names {
			st, ok := h.await(name, max(time.Until(deadline), 0))
			done = done && ok
			lines = append(lines, mcpReport(name, st, ok)...)
		}
		return mcpReply{lines: lines}, done
	}
}

func mcpReport(name string, st mcpStatus, done bool) []string {
	switch {
	case !done:
		return []string{fmt.Sprintf("◌ %s is still starting (the first run downloads it) · /mcp shows when it is ready", name)}
	case st.state == "ready":
		out := []string{fmt.Sprintf("● %s is ready · %d tools: %s", name, len(st.tools), clipText(strings.Join(st.tools, ", "), 70))}
		if e, ok := mcpFindCatalog(name); ok && e.Try != "" {
			out = append(out, "try asking: "+e.Try)
		}
		return out
	case st.state == "off":
		return []string{fmt.Sprintf("◌ %s is switched off", name)}
	}
	return []string{fmt.Sprintf("✕ %s %s", name, mcpExplain(name, st.err))}
}
