package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// command is one slash command. help is the short phrase /help and the
// suggestion box show; web says whether the browser (cli/serve.go command and
// web/app.js runCommand) can run it too.
type command struct {
	name, help string
	web        bool
}

// commands is the one list of slash commands: the TUI, /help and the web UI
// (GET /api/commands) all read it.
var commands = []command{
	{"/models", "every model in the order prism tries them · /models <text> narrows", true},
	{"/model", "pick the model for this chat · /model <name> · /model auto", true},
	{"/key", "your provider keys · /key <name> <secret>", true},
	{"/login", "sign in to plans you already pay for", true},
	{"/new", "start a fresh chat", true},
	{"/clear", "clear the screen and start fresh", true},
	{"/compact", "summarise the chat to free up room · /compact <focus>", true},
	{"/undo", "undo prism's file changes from its last turn", true},
	{"/resume", "reopen an earlier chat in this folder", true},
	{"/init", "write PRISM.md, the rules prism follows here", true},
	{"/skills", "playbooks prism can follow · /skills <name> · /skills new <name>", true},
	{"/commands", "your saved prompts, run as /<name> · /commands new <name>", true},
	{"/web", "open this chat in your browser", true},
	{"/mcp", "connected tools · /mcp add picks a free one", true},
	{"/memory", "what prism remembers · /memory forget <n>", true},
	{"/usage", "free AI used today", true},
	{"/tree", "project files · /tree changed · /tree <text>", true},
	{"/files", "browse the project and read any file", true},
	{"/open", "read a file in the viewer · /open <path>", true},
	{"/theme", "switch the colour theme · /theme <name>", true},
	{"/beam", "turn the finish beam on or off", true},
	{"/quiet", "turn Shard's lines on or off", true},
	{"/tabs", "open chats and their status", true},
	{"/rename", "name this tab · /rename <name>", false},
	{"/ask", "ask another tab's chat a side question · /ask <n> <question>", false},
	{"/help", "show these commands", true},
	{"/quit", "close prism", false},
}

type routeRow struct {
	Route     string `json:"route"`
	Ready     bool   `json:"ready"`
	RetryIn   int    `json:"retryInSeconds"`
	LastError string `json:"lastError"`
	Source    string `json:"source"` // "yours" (a key you added) or "subscription" (/login)
	Paid      bool   `json:"paid"`
}

type modelsMsg struct {
	rows   []routeRow
	err    error
	filter string
	more   int
}

// commandAliases are the other names command() answers to; a saved command
// file can't take them either.
var commandAliases = []string{"/exit", "/status", "/continue", "/keys", "/logins", "/themes", "/forget"}

// isBuiltin says whether name (with the slash) is a built-in command.
func isBuiltin(name string) bool {
	for _, c := range commands {
		if c.name == name {
			return true
		}
	}
	for _, a := range commandAliases {
		if a == name {
			return true
		}
	}
	return false
}

// matching is the suggestion list for what is typed so far: built-in
// commands, then saved commands (usercmds.go) for the folder prism runs in.
func matching(input string) []command {
	if !strings.HasPrefix(input, "/") || strings.ContainsAny(input, " \n") {
		return nil
	}
	dir, _ := os.Getwd()
	return matchingIn(dir, input)
}

func matchingIn(dir, input string) []command {
	if !strings.HasPrefix(input, "/") || strings.ContainsAny(input, " \n") {
		return nil
	}
	var found []command
	for _, c := range commands {
		if strings.HasPrefix(c.name, input) {
			found = append(found, c)
		}
	}
	if dir == "" {
		return found
	}
	for _, u := range loadUserCommands(dir, time.Second) {
		if u.runnable() && strings.HasPrefix(u.name, input) {
			found = append(found, command{u.name, u.help, true})
		}
	}
	return found
}

func (m *model) command(text string) tea.Cmd {
	fields := strings.Fields(text)
	name := fields[0]
	switch name {
	case "/quit", "/exit":
		return tea.Quit
	case "/new":
		m.agent.reset()
		m.entries, m.tokens, m.route = nil, 0, ""
	case "/clear":
		// If a turn is running, cancel it first so the goroutine stops before
		// we reset the agent (avoids a race on agent.messages).
		if m.busy && m.cancel != nil {
			m.cancel()
		}
		// Save the current session first (like /new), then clear screen + context.
		m.agent.saveSession()
		m.agent.reset()
		m.entries, m.tokens, m.route = nil, 0, ""
		m.busy, m.thinking, m.cancel, m.reasoning, m.draft, m.pending, m.queued = false, false, nil, "", nil, nil, nil
		return tea.ClearScreen
	case "/compact":
		focus := strings.Join(fields[1:], " ")
		m.compactCommand(focus)
	case "/undo":
		if m.busy {
			m.entries = append(m.entries, &entry{kind: "note", text: "wait until prism finishes, then run /undo"})
			return nil
		}
		title, lines, note := undoLast(m.agent)
		if note != "" {
			m.entries = append(m.entries, &entry{kind: "note", text: note})
			return nil
		}
		m.entries = append(m.entries, &entry{kind: "info", text: title, lines: lines})
	case "/resume", "/continue":
		list := m.agent.store.sessions()
		var others []session
		for _, s := range list {
			if s.ID != m.agent.session {
				others = append(others, s)
			}
		}
		// Only the numbers that /resume shows (at most resumeShown) are valid.
		others = others[:min(len(others), resumeShown)]
		if len(fields) > 1 {
			k, err := strconv.Atoi(fields[1])
			if err != nil || k < 1 || k > len(others) {
				m.entries = append(m.entries, &entry{kind: "note", text: "no chat with that number · try /resume"})
				return nil
			}
			m.reopen(others[k-1].ID)
			return nil
		}
		if len(others) == 0 {
			m.entries = append(m.entries, &entry{kind: "note", text: "no earlier chats in this folder yet"})
			return nil
		}
		m.openResumeMenu(others)
	case "/init":
		if m.busy {
			m.input = nil
			m.entries = append(m.entries, &entry{kind: "note", text: "wait until prism finishes, then run /init"})
			return nil
		}
		m.input = []rune(initPrompt)
		cmd := m.submit()
		m.input = nil // never leave the long prompt sitting in the box
		return cmd
	case "/memory", "/forget":
		title, lines, changed, err := m.agent.store.memoryCommand(fields)
		if err != nil {
			m.entries = append(m.entries, &entry{kind: "help", text: err.Error()})
			return nil
		}
		if changed {
			m.agent.messages[0].Content = m.agent.systemPrompt()
			m.entries = append(m.entries, &entry{kind: "note", text: title})
			return nil
		}
		projectFile, _ := projectRules(m.agent.dir)
		v := memoryView(title, lines, projectFile)
		// kind "info" keeps e.text+e.lines for web/test compat; view gives richer terminal rendering.
		m.entries = append(m.entries, &entry{kind: "info", text: title, lines: lines, view: &v})
	case "/web":
		m.startWeb()
	case "/mcp":
		if len(fields) == 2 && strings.ToLower(fields[1]) == "add" {
			m.openMCPMenu()
			return nil
		}
		res, follow := mcpCommand(m.agent, fields)
		if follow != nil {
			quick, done := follow(400 * time.Millisecond)
			res.lines = append(res.lines, quick.lines...)
			if !done {
				events := m.events
				go func() {
					late, _ := follow(100 * time.Second)
					events <- evNote{late.oneLine()}
				}()
				res.lines = append(res.lines, "still connecting, the result will show up here")
			}
		}
		if res.failed {
			m.entries = append(m.entries, &entry{kind: "error", text: res.title, arg: strings.Join(res.lines, " · ")})
			return nil
		}
		// Use structured view for /mcp list; fall back to info for add/remove/test results
		if len(fields) == 1 {
			var mcpList []mcpStatus
			if m.agent.mcp != nil {
				mcpList = m.agent.mcp.status()
			}
			v := mcpView(mcpList)
			m.entries = append(m.entries, &entry{kind: "cmd", view: &v})
		} else {
			m.entries = append(m.entries, &entry{kind: "info", text: res.title, lines: res.lines})
		}
	case "/skills":
		if len(fields) > 1 && fields[1] == "new" {
			if len(fields) == 2 {
				m.entries = append(m.entries, &entry{kind: "note", text: "usage: /skills new <name>"})
				return nil
			}
			m.entries = append(m.entries, &entry{kind: "note", text: newSkill(m.agent, fields[2])})
			return nil
		}
		if len(fields) > 1 {
			sk, ok := findSkill(m.agent.skills, fields[1])
			if !ok {
				m.entries = append(m.entries, &entry{kind: "note", text: "no skill called " + fields[1] + " · /skills lists them"})
				return nil
			}
			v := skillView(sk)
			m.entries = append(m.entries, &entry{kind: "cmd", view: &v})
			return nil
		}
		v := skillsView(m.agent.skills)
		m.entries = append(m.entries, &entry{kind: "cmd", view: &v})
	case "/commands":
		if len(fields) > 1 && fields[1] == "new" {
			if len(fields) == 2 {
				m.entries = append(m.entries, &entry{kind: "note", text: "usage: /commands new <name>"})
				return nil
			}
			m.entries = append(m.entries, &entry{kind: "note", text: newUserCommand(m.agent.dir, fields[2])})
			return nil
		}
		v := commandsView(loadUserCommands(m.agent.dir, 0))
		m.entries = append(m.entries, &entry{kind: "cmd", view: &v})
	case "/tree":
		title, lines := treeCommand(m.agent, strings.Join(fields[1:], " "))
		m.entries = append(m.entries, &entry{kind: "info", text: title, lines: lines})
	case "/usage":
		u := todayUsage()
		v := usageView(u, m.agent.requests, m.agent.sent, m.agent.cached)
		m.entries = append(m.entries, &entry{kind: "cmd", view: &v})
	case "/help":
		v := helpView()
		m.entries = append(m.entries, &entry{kind: "cmd", view: &v})
	case "/models", "/status":
		api := m.agent.api
		filter := strings.Join(fields[1:], " ")
		return func() tea.Msg {
			rows, err := fetchRoutes(api)
			if err != nil {
				return modelsMsg{err: err}
			}
			rows, more := filterRoutes(rows, filter)
			return modelsMsg{rows: rows, err: err, filter: filter, more: more}
		}
	case "/files":
		return m.openFiles()
	case "/open":
		if len(fields) == 1 {
			return m.openFiles()
		}
		return m.openFile(strings.Join(fields[1:], " "))
	case "/model":
		if len(fields) == 1 {
			return m.openPicker()
		}
		return m.modelCommand(fields)
	case "/key", "/keys":
		return m.keyCommand(fields)
	case "/login", "/logins":
		return m.loginCommand(fields)
	case "/theme", "/themes":
		if len(fields) == 1 {
			m.openThemeMenu()
			return nil
		}
		m.theme = themeIndex(fields[1])
		saveTheme(themes[m.theme].name)
	case "/beam":
		m.beamOn = !m.beamOn
		if len(fields) > 1 {
			m.beamOn = fields[1] != "off"
		}
		state := map[bool]string{true: "on", false: "off"}[m.beamOn]
		saveSetting("beam", state)
		m.entries = append(m.entries, &entry{kind: "note", text: "finish beam " + state})
		if m.beamOn {
			m.beamAt = m.now
		}
	case "/quiet":
		m.quiet = !m.quiet
		if len(fields) > 1 {
			m.quiet = fields[1] != "off"
		}
		saveSetting("quiet", map[bool]string{true: "on", false: "off"}[m.quiet])
		m.entries = append(m.entries, &entry{kind: "note", text: map[bool]string{true: "shard is quiet", false: "shard can talk again"}[m.quiet]})
		if !m.quiet {
			m.speak("light in. rainbow out.")
		}
	case "/tabs":
		m.tabsCommand()
	case "/rename":
		m.renameTab(strings.Join(fields[1:], " "))
	case "/ask":
		return m.askCommand(fields)
	default:
		if msg, ok := savedMessage(m.agent.dir, text); ok {
			if m.busy {
				m.entries = append(m.entries, &entry{kind: "note", text: "wait until prism finishes, then run " + name})
				return nil
			}
			if msg == "" || strings.HasPrefix(msg, "/") {
				m.entries = append(m.entries, &entry{kind: "note", text: name + " has nothing to send · /commands shows where its file is"})
				return nil
			}
			m.input = []rune(msg)
			cmd := m.submit()
			m.input = nil
			return cmd
		}
		m.entries = append(m.entries, &entry{kind: "help", text: name + " isn't a command"})
	}
	return nil
}

// openResumeMenu is /resume with no number: pick an earlier chat (the same
// list and numbering /resume <number> uses); enter reopens it.
func (m *model) openResumeMenu(others []session) {
	m.openMenu(resumeMenu(others))
}

// resumeMenu is the /resume picker for both UIs: one row per earlier chat,
// numbered like /resume <number>; the web runs "/resume <number>" instead.
func resumeMenu(others []session) menuState {
	var items []menuItem
	for k, s := range others {
		title := s.Title
		if title == "" {
			title = "untitled chat"
		}
		id := s.ID
		items = append(items, menuItem{
			dot: fmt.Sprint(k + 1), dotTone: toneAccent, label: title, state: ago(s.Updated),
			match: title,
			run: func(m *model) tea.Cmd {
				m.reopen(id)
				return nil
			},
		})
	}
	return menuState{
		kind:       "resume",
		title:      "earlier chats",
		hint:       "↑↓ move · enter reopen · esc close",
		filterable: true,
		noun:       "chat",
		items:      items,
	}
}

// undoLast pops the newest /undo turn and describes it. note is set instead
// of lines when there is nothing to show (nothing to undo, or an error).
func undoLast(a *Agent) (title string, lines []string, note string) {
	if a.undoStack == nil || a.undoStack.depth() == 0 {
		return "", nil, "nothing to undo"
	}
	res, err := a.undoStack.pop(a.dir)
	if err != nil {
		return "", nil, err.Error()
	}
	for _, f := range res.restored {
		lines = append(lines, "✓ "+f)
	}
	for _, f := range res.refused {
		lines = append(lines, "✗ "+f)
	}
	if len(res.refused) > 0 {
		lines = append(lines, "", "files marked ✗ were changed after prism wrote them and were not restored")
	}
	if a.undoStack.depth() > 0 {
		lines = append(lines, "", fmt.Sprintf("%d earlier turn(s) available · /undo again to go further back", a.undoStack.depth()))
	}
	title = "undo"
	if len(res.restored) == 0 && len(res.refused) > 0 {
		title = "undo blocked"
	}
	return title, lines, ""
}

// compactChat is /compact for the web: the same summary request and rebuilt
// history as the TUI compactCommand (cli/compact.go), returning its lines.
func compactChat(ctx context.Context, a *Agent, focus string) ([]string, error) {
	if len(a.messages) <= 1 {
		return nil, errors.New("nothing to compact yet")
	}
	if hasOrphanedToolCalls(a.messages) {
		return nil, errors.New("cannot compact: history has unanswered tool calls; wait for the turn to finish")
	}
	before := a.tokens
	if before == 0 {
		before = estimate(a.messages)
	}
	prompt := compactPrompt
	if focus != "" {
		prompt = fmt.Sprintf("Summarize this conversation for yourself so you can continue the work with no other context. Focus especially on: %s. Include the user's goal and latest request, key decisions, every file read or changed and how, commands run and their results, problems found, and exactly what remains to do next. Be concise but complete. Do not call tools.", focus)
	}
	request := append(append([]Message{}, a.messages...), Message{Role: "user", Content: prompt})
	light := a.light
	a.light = false
	reply, err := a.complete(ctx, request, map[string]any{"tool_choice": "none"}, func(any) {})
	a.light = light
	if err != nil {
		return nil, errors.New("compact failed: " + err.Error())
	}
	summary := strings.TrimSpace(reply.Content)
	if summary == "" {
		return nil, errors.New("compact failed: model returned empty summary")
	}
	if a.mapReady != nil {
		<-a.mapReady
	}
	files := a.mapProject()
	a.touchMu.Lock()
	a.files = files
	a.touchMu.Unlock()
	a.messages = []Message{
		{Role: "system", Content: a.systemPrompt()},
		{Role: "user", Content: summaryPrefix + " (earlier messages were compacted):\n\n" + summary + "\n\nThe user's latest request, word for word:\n" + a.latestRequest() + "\n\nContinue from where you left off."},
	}
	a.memMark = 1
	after := estimate(a.messages)
	a.tokens = after
	lines := []string{
		fmt.Sprintf("before  %s tokens  (%d messages)", short(before), len(request)-1),
		fmt.Sprintf("after   %s tokens  (1 summary message)", short(after)),
		fmt.Sprintf("saved   %s tokens", short(before-after)),
	}
	if focus != "" {
		lines = append(lines, "focus: "+focus)
	}
	return append(lines, "", "conversation compacted · keep going"), nil
}

// reopen loads an earlier chat into this tab, like /resume <number>.
func (m *model) reopen(id string) {
	sess, err := m.agent.store.loadSession(id)
	if err != nil {
		m.entries = append(m.entries, &entry{kind: "error", text: "couldn't reopen that chat: " + err.Error()})
		return
	}
	m.agent.resume(sess)
	m.entries = append(entriesFrom(m.agent.messages), &entry{kind: "note", text: "chat reopened · keep going"})
	m.tokens, m.scroll = m.agent.tokens, 0
}

func fetchRoutes(api string) ([]routeRow, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(api, "/v1")+"/status", nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.New("can't reach the free AI router · run: prism status")
	}
	defer res.Body.Close()
	var rows []routeRow
	if err := json.NewDecoder(res.Body).Decode(&rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func friendly(err error) (string, string) {
	var re *routerError
	if !errors.As(err, &re) || len(re.failures) == 0 {
		return err.Error(), ""
	}
	busy, rejected := 0, 0
	var parts []string
	for _, f := range re.failures {
		switch {
		case f.Status == 401 || f.Status == 403:
			rejected++
		case f.Status == 0 || f.Status == 429 || f.Status >= 500:
			busy++
		}
		parts = append(parts, fmt.Sprintf("%s %s", modelName(f.Route), reason(f.Status)))
	}
	detail := strings.Join(parts, " · ")
	switch {
	case rejected > 0:
		return "A saved key was rejected. Run: prism setup", detail
	case busy == len(re.failures):
		return "Every free AI model is busy right now. Wait a minute and try again.", detail + "  ·  more free keys mean fewer waits: prism setup"
	}
	return "No free AI model could answer this time.", detail
}

func reason(status int) string {
	switch {
	case status == 0:
		return "unreachable"
	case status == 429:
		return "busy"
	case status == 401 || status == 403:
		return "key rejected"
	case status == 404:
		return "not available"
	case status >= 500:
		return "down"
	}
	return fmt.Sprintf("error %d", status)
}

func provider(route string) string {
	name, _, _ := strings.Cut(route, "/")
	return name
}

// resumeShown is how many earlier chats /resume lists; only those numbers can be reopened.
const resumeShown = 9

const initPrompt = `Look around this project (list, read the key files, check how it is run and tested). Then write PRISM.md in the project root: a short guide for an AI coding assistant with what the project is, how the folders are laid out, the exact commands to run, build and test it, and the code conventions you can see. Keep it under 60 lines. Do not change any other file.`

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func short(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return strconv.Itoa(n)
}

func mcpLines(list []mcpStatus) []string {
	if len(list) == 0 {
		return []string{"none yet · /mcp add sets one up in a single step (or edit .prism/mcp.json)", "", "guide: docs/MCP.md in the prism repo"}
	}
	var lines []string
	for _, s := range list {
		switch s.state {
		case "ready":
			lines = append(lines, fmt.Sprintf("● %-14s %d tools: %s", s.name, len(s.tools), clipText(strings.Join(s.tools, ", "), 70)))
		case "error":
			lines = append(lines, fmt.Sprintf("✕ %-14s %s", s.name, clipText(mcpExplain(s.name, s.err), 220)))
		default:
			lines = append(lines, fmt.Sprintf("◌ %-14s %s", s.name, s.state))
		}
	}
	return append(lines, "", "the AI uses these tools by itself when they fit the task", "add more: /mcp add · remove: /mcp remove <name> · recheck: /mcp test <name>")
}

var skillName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

func newSkill(a *Agent, name string) string {
	if !skillName.MatchString(name) {
		return "skill names use lowercase letters, numbers and dashes, like my-skill"
	}
	path := filepath.Join(a.dir, ".prism", "skills", name+".md")
	if _, err := os.Stat(path); err == nil {
		return ".prism/skills/" + name + ".md already exists"
	}
	body := "---\nname: " + name + "\ndescription: one line saying when prism should use this skill\n---\n\n# " + name + "\n\n- Step one: what to look at first.\n- Step two: what to change.\n- Check: the command that proves it worked.\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err.Error()
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err.Error()
	}
	a.skills = loadSkills(a.dir)
	if len(a.messages) > 0 {
		a.messages[0].Content = a.systemPrompt()
	}
	return "wrote .prism/skills/" + name + ".md · fill in the steps, prism picks it up right away"
}
