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
	{"/models", "see which free AI models are ready"},
	{"/new", "start a fresh chat"},
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
	{"/help", "show these commands · keys: ctrl+o open all steps, ctrl+t theme, esc stop"},
	{"/quit", "close prism"},
}

type routeRow struct {
	Route     string `json:"route"`
	Ready     bool   `json:"ready"`
	RetryIn   int    `json:"retryInSeconds"`
	LastError string `json:"lastError"`
}

type modelsMsg struct {
	rows []routeRow
	err  error
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
	case "/new", "/clear":
		m.agent.reset()
		m.entries, m.tokens, m.route = nil, 0, ""
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
		var lines []string
		for k, s := range others {
			lines = append(lines, fmt.Sprintf("%d  %-11s %s", k+1, ago(s.Updated), s.Title))
		}
		lines = append(lines, "", "type /resume <number> to reopen one")
		m.entries = append(m.entries, &entry{kind: "info", text: "earlier chats", lines: lines})
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
		if file, _ := projectRules(m.agent.dir); file != "" {
			lines = append(lines, "", "also following the rules in "+file)
		}
		m.entries = append(m.entries, &entry{kind: "info", text: title, lines: lines})
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
		m.entries = append(m.entries, &entry{kind: "info", text: res.title, lines: res.lines})
	case "/skills":
		if len(fields) > 2 && fields[1] == "new" {
			m.entries = append(m.entries, &entry{kind: "note", text: newSkill(m.agent, fields[2])})
			return nil
		}
		var lines []string
		for _, s := range m.agent.skills {
			lines = append(lines, fmt.Sprintf("%-15s %s  (%s)", s.name, s.description, s.source))
		}
		if len(lines) == 0 {
			lines = []string{"no skills found"}
		}
		lines = append(lines, "", "prism picks the right one by itself · add your own: .prism/skills/<name>.md in a project, or ~/.config/prism/skills/")
		m.entries = append(m.entries, &entry{kind: "info", text: "skills", lines: lines})
	case "/tree":
		title, lines := treeCommand(m.agent, strings.Join(fields[1:], " "))
		m.entries = append(m.entries, &entry{kind: "info", text: title, lines: lines})
	case "/usage":
		u := todayUsage()
		lines := []string{
			fmt.Sprintf("today       %d requests · %s tokens sent", u.Requests, short(u.Tokens)),
			fmt.Sprintf("this chat   %d requests · %s tokens sent · %s cached", m.agent.requests, short(m.agent.sent), short(m.agent.cached)),
			"",
			"all free · limits reset daily · more keys mean more room: prism setup",
		}
		m.entries = append(m.entries, &entry{kind: "info", text: "free AI usage", lines: lines})
	case "/help":
		m.entries = append(m.entries, &entry{kind: "help"})
	case "/models", "/model", "/status":
		api := m.agent.api
		return func() tea.Msg {
			rows, err := fetchRoutes(api)
			return modelsMsg{rows, err}
		}
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
