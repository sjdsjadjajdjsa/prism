package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	{"/skills", "playbooks prism can follow · add your own in .prism/skills/"},
	{"/memory", "see what prism remembers · /memory forget"},
	{"/usage", "free AI used today"},
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
		if len(fields) > 1 {
			k, err := strconv.Atoi(fields[1])
			if err != nil || k < 1 || k > len(others) {
				m.entries = append(m.entries, &entry{kind: "note", text: "no chat with that number · try /resume"})
				return nil
			}
			m.agent.resume(others[k-1])
			m.entries = append(entriesFrom(m.agent.messages), &entry{kind: "note", text: "chat reopened · keep going"})
			m.tokens, m.scroll = m.agent.tokens, 0
			return nil
		}
		if len(others) == 0 {
			m.entries = append(m.entries, &entry{kind: "note", text: "no earlier chats in this folder yet"})
			return nil
		}
		var lines []string
		for k, s := range others[:min(len(others), 9)] {
			lines = append(lines, fmt.Sprintf("%d  %-11s %s", k+1, ago(s.Updated), s.Title))
		}
		lines = append(lines, "", "type /resume <number> to reopen one")
		m.entries = append(m.entries, &entry{kind: "info", text: "earlier chats", lines: lines})
	case "/init":
		m.input = []rune(initPrompt)
		return m.submit()
	case "/memory", "/forget":
		if name == "/forget" || (len(fields) > 1 && fields[1] == "forget") {
			if err := m.agent.store.forget(); err != nil {
				m.entries = append(m.entries, &entry{kind: "error", text: err.Error()})
				return nil
			}
			m.agent.messages[0].Content = m.agent.systemPrompt()
			m.entries = append(m.entries, &entry{kind: "note", text: "memory cleared for this folder"})
			return nil
		}
		mem := m.agent.store.memory()
		lines := strings.Split(mem, "\n")
		if mem == "" {
			lines = []string{"nothing yet · prism saves what it learns after each task"}
		}
		if file, _ := projectRules(m.agent.dir); file != "" {
			lines = append(lines, "", "also following the rules in "+file)
		}
		lines = append(lines, "", "/memory forget clears it")
		m.entries = append(m.entries, &entry{kind: "info", text: "what prism remembers here", lines: lines})
	case "/skills":
		var lines []string
		for _, s := range m.agent.skills {
			lines = append(lines, fmt.Sprintf("%-15s %s  (%s)", s.name, s.description, s.source))
		}
		if len(lines) == 0 {
			lines = []string{"no skills found"}
		}
		lines = append(lines, "", "prism picks the right one by itself · add your own: .prism/skills/<name>.md in a project, or ~/.config/prism/skills/")
		m.entries = append(m.entries, &entry{kind: "info", text: "skills", lines: lines})
	case "/usage":
		u := todayUsage()
		lines := []string{
			fmt.Sprintf("today       %d requests · %s tokens sent", u.Requests, short(u.Tokens)),
			fmt.Sprintf("this chat   %d requests · %s tokens sent", m.agent.requests, short(m.agent.sent)),
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
