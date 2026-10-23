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

type command struct{ name, help string }

var commands = []command{
	{"/models", "see which free AI models are ready · /models <text> narrows the list"},
	{"/model", "the model for this chat · /model picks from a list · /model <name> pins one · /model auto lets prism choose"},
	{"/key", "provider keys · /key <name> <secret> adds one · /key rm <name> removes it"},
	{"/login", "plans you already pay for · /login <name> signs in · /login rm <name> signs out"},
	{"/new", "start a fresh chat"},
	{"/clear", "clear the screen and start fresh (saves first, like /new)"},
	{"/compact", "summarise the conversation to free up context · /compact <focus> to guide it"},
	{"/undo", "undo the file changes prism made in its last turn"},
	{"/resume", "reopen an earlier chat in this folder"},
	{"/init", "write PRISM.md, the rules prism always follows here"},
	{"/skills", "playbooks prism can follow · /skills new <name> to write your own"},
	{"/web", "open this chat in your browser · press enter here to come back"},
	{"/mcp", "connected tools · /mcp add picks a free one · /mcp remove <name> · /mcp test <name>"},
	{"/memory", "see what prism remembers · /memory forget 2 removes one"},
	{"/usage", "free AI used today"},
	{"/tree", "project files · /tree changed · /tree <text>"},
	{"/theme", "switch theme, or /theme <name>"},
	{"/beam", "turn the finish beam on or off"},
	{"/quiet", "turn Shard's lines on or off"},
	{"/tabs", "open chats with their status · ctrl+t new, ctrl+w close, alt+1..9 jump"},
	{"/rename", "name this tab · /rename <name>"},
	{"/ask", "ask about another tab's chat without disturbing it · /ask <n> <question>"},
	{"/help", "show these commands · keys: ctrl+t new tab, ctrl+w close tab, ctrl+pgup/pgdown switch, ctrl+o open all steps, ctrl+y theme, esc stop"},
	{"/quit", "close prism"},
}

type routeRow struct {
	Route     string `json:"route"`
	Ready     bool   `json:"ready"`
	RetryIn   int    `json:"retryInSeconds"`
	LastError string `json:"lastError"`
}

type modelsMsg struct {
	rows   []routeRow
	err    error
	filter string
	more   int
}

func matching(input string) []command {
	if !strings.HasPrefix(input, "/") || strings.ContainsAny(input, " \n") {
		return nil
	}
	var found []command
	for _, c := range commands {
		if strings.HasPrefix(c.name, input) {
			found = append(found, c)
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
		if m.agent.undoStack == nil || m.agent.undoStack.depth() == 0 {
			m.entries = append(m.entries, &entry{kind: "note", text: "nothing to undo"})
			return nil
		}
		res, err := m.agent.undoStack.pop(m.agent.dir)
		if err != nil {
			m.entries = append(m.entries, &entry{kind: "note", text: err.Error()})
			return nil
		}
		var lines []string
		for _, f := range res.restored {
			lines = append(lines, "✓ "+f)
		}
		for _, f := range res.refused {
			lines = append(lines, "✗ "+f)
		}
		if len(res.refused) > 0 {
			lines = append(lines, "", "files marked ✗ were changed after prism wrote them and were not restored")
		}
		if m.agent.undoStack.depth() > 0 {
			lines = append(lines, "", fmt.Sprintf("%d earlier turn(s) available · /undo again to go further back", m.agent.undoStack.depth()))
		}
		title := "undo"
		if len(res.restored) == 0 && len(res.refused) > 0 {
			title = "undo blocked"
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
			sess, err := m.agent.store.loadSession(others[k-1].ID)
			if err != nil {
				m.entries = append(m.entries, &entry{kind: "error", text: "couldn't reopen that chat: " + err.Error()})
				return nil
			}
			m.agent.resume(sess)
			m.entries = append(entriesFrom(m.agent.messages), &entry{kind: "note", text: "chat reopened · keep going"})
			m.tokens, m.scroll = m.agent.tokens, 0
			return nil
		}
		if len(others) == 0 {
			m.entries = append(m.entries, &entry{kind: "note", text: "no earlier chats in this folder yet"})
			return nil
		}
		v := resumeView(others)
		m.entries = append(m.entries, &entry{kind: "cmd", view: &v})
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
		if len(fields) > 2 && fields[1] == "new" {
			m.entries = append(m.entries, &entry{kind: "note", text: newSkill(m.agent, fields[2])})
			return nil
		}
		v := skillsView(m.agent.skills)
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
		if len(fields) > 1 {
			m.theme = themeIndex(fields[1])
		} else {
			m.theme = (m.theme + 1) % len(themes)
		}
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
		m.entries = append(m.entries, &entry{kind: "help", text: name + " isn't a command"})
	}
	return nil
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
