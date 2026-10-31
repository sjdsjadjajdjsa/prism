package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type webCmd struct {
	s      *server
	w      http.ResponseWriter
	r      *http.Request
	a      *Agent
	fields []string
	text   string
	busy   bool
}

var terminalOnly = map[string]string{
	"/editor": "/editor works in the terminal · here, click a file in Files, then Edit",
	"/rename": "/rename works in the terminal · the tab names it sets show here",
	"/ask":    "/ask works in the terminal · here, /tabs switches to another chat",
	"/quit":   "nothing to quit here · close this browser tab to leave",
	"/exit":   "nothing to quit here · close this browser tab to leave",
}

var webCommands = map[string]func(*webCmd){
	"/models":   (*webCmd).models,
	"/status":   (*webCmd).models,
	"/model":    (*webCmd).model,
	"/key":      (*webCmd).keys,
	"/keys":     (*webCmd).keys,
	"/login":    (*webCmd).login,
	"/logins":   (*webCmd).login,
	"/skills":   (*webCmd).skills,
	"/commands": (*webCmd).commands,
	"/help":     (*webCmd).help,
	"/mcp":      (*webCmd).mcp,
	"/tree":     (*webCmd).tree,
	"/beam":     (*webCmd).setting,
	"/quiet":    (*webCmd).setting,
	"/notify":   (*webCmd).setting,
	"/usage":    (*webCmd).usage,
	"/memory":   (*webCmd).memory,
	"/forget":   (*webCmd).memory,
	"/undo":     (*webCmd).undo,
	"/clear":    (*webCmd).clear,
	"/resume":   (*webCmd).resume,
	"/continue": (*webCmd).resume,
	"/init":     (*webCmd).initChat,
	"/rules":    func(c *webCmd) { c.dirView(rulesView) },
	"/hooks":    func(c *webCmd) { c.dirView(hooksView) },
	"/plan":     (*webCmd).plan,
}

func (c *webCmd) info(title string, lines []string) {
	writeJSON(c.w, map[string]any{"kind": "info", "title": title, "lines": lines})
}

func (c *webCmd) note(text string) {
	writeJSON(c.w, map[string]any{"kind": "note", "text": text})
}

func (c *webCmd) fail(text string) {
	writeJSON(c.w, map[string]any{"kind": "error", "text": text})
}

func (c *webCmd) dirView(view func(dir string, args []string) (string, []string, string)) {
	if (c.busy || c.s.tabsWorking(c.r, c.a, true)) && len(c.fields) > 1 {
		c.fail("wait until prism finishes, then " + strings.Join(c.fields, " "))
		return
	}
	title, lines, footer := view(c.a.dir, c.fields[1:])
	if footer != "" {
		lines = append(lines, footer)
	}
	c.info(title, lines)
}

func (c *webCmd) models() {
	w, a, fields := c.w, c.a, c.fields
	rows, err := fetchRoutes(a.api)
	if err != nil {
		c.fail(err.Error())
		return
	}
	filter := strings.Join(fields[1:], " ")
	kept, more := filterRoutes(rows, filter)
	writeJSON(w, modelsTable(kept, more, filter))
}

func (c *webCmd) model() {
	w, a, fields, busy := c.w, c.a, c.fields, c.busy
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
		c.fail("wait until prism finishes, then pin a model")
		return
	}
	text := pinModel(a, fields[1])
	writeJSON(w, map[string]any{"kind": "model", "model": a.model, "source": answeredBy("", a.model), "text": text})
}

func (c *webCmd) keys() {
	a, fields := c.a, c.fields
	if len(fields) == 1 {
		list, err := fetchKeys(a.api)
		if err != nil {
			c.fail(err.Error())
			return
		}
		c.info("keys", keyLines(list))
		return
	}
	var id, value string
	if fields[1] == "rm" || fields[1] == "remove" {
		if len(fields) != 3 {
			c.fail("usage: /key rm <name>")
			return
		}
		id = fields[2]
	} else if len(fields) == 3 {
		id, value = fields[1], fields[2]
	} else {
		c.fail("usage: /key <name> <secret>")
		return
	}
	msg, err := postKey(a.api, id, value)
	if err != nil {
		c.fail(err.Error())
		return
	}
	if msg.masked == "" {
		c.info("removed the "+id+" key", []string{})
		return
	}
	lines := []string{"saved · " + msg.masked}
	if msg.models > 0 {
		lines = append(lines, countModels(msg.models)+" ready · pick one with /model")
	}
	if msg.note != "" {
		lines = append(lines, msg.note)
	}
	c.info(id+" key", lines)
}

func (c *webCmd) login() {
	s, w, a, fields := c.s, c.w, c.a, c.fields
	s.login(w, a, fields)
}

func (c *webCmd) skills() {
	w, a, fields, busy := c.w, c.a, c.fields, c.busy
	if len(fields) > 1 && fields[1] == "new" {
		if busy {
			c.fail("wait until prism finishes")
			return
		}
		if len(fields) == 2 {
			c.note("usage: /skills new <name>")
			return
		}
		c.note(newSkill(a, fields[2]))
		return
	}
	if len(fields) > 1 {
		sk, ok := findSkill(a.skills, fields[1])
		if !ok {
			c.note("no skill called " + fields[1] + " · /skills lists them")
			return
		}
		writeJSON(w, skillView(sk).wire())
		return
	}
	writeJSON(w, skillsView(a.skills).wire())
}

func (c *webCmd) commands() {
	w, a, fields := c.w, c.a, c.fields
	if len(fields) > 1 && fields[1] == "new" {
		if len(fields) == 2 {
			c.note("usage: /commands new <name>")
			return
		}
		c.note(newUserCommand(a.dir, fields[2]))
		return
	}
	writeJSON(w, commandsView(loadUserCommands(a.dir, 0)).wire())
}

func (c *webCmd) help() {
	w := c.w
	writeJSON(w, helpViewFor(true).wire())
}

func (c *webCmd) mcp() {
	w, a, fields := c.w, c.a, c.fields
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
	c.info(res.title, res.lines)
}

func (c *webCmd) tree() {
	a, fields := c.a, c.fields
	title, lines := treeCommand(a, strings.Join(fields[1:], " "))
	c.info(title, lines)
}

func (c *webCmd) setting() {
	w, fields := c.w, c.fields
	key := strings.TrimPrefix(fields[0], "/")
	on := loadSetting(key) == "on" || ((key == "beam" || key == "notify") && loadSetting(key) != "off")
	on = !on
	if len(fields) > 1 {
		on = fields[1] != "off"
	}
	state := map[bool]string{true: "on", false: "off"}[on]
	saveSetting(key, state)
	writeJSON(w, map[string]any{"kind": "setting", "name": key, "on": on})
}

func (c *webCmd) usage() {
	s := c.s
	u := todayUsage()
	c.info("AI usage", []string{
		fmt.Sprintf("today: %d requests, %s tokens sent", u.Requests, short(u.Tokens)),
		fmt.Sprintf("this chat: %d requests, %s tokens sent, %s cached", s.requests, short(s.sent), short(s.agent.cached)),
		usageFooter(u),
	})
}

func (c *webCmd) memory() {
	w, a, fields, busy := c.w, c.a, c.fields, c.busy
	if busy {
		c.fail("wait until prism finishes")
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
	c.info(title, lines)
}

func (c *webCmd) undo() {
	a, busy := c.a, c.busy
	if busy {
		c.fail("wait until prism finishes, then run /undo")
		return
	}
	title, lines, msg := undoLast(a)
	if msg != "" {
		c.note(msg)
		return
	}
	c.info(title, lines)
}

func (c *webCmd) clear() {
	w, a, busy := c.w, c.a, c.busy
	if busy {
		c.fail("wait until prism finishes, or press esc to stop it first")
		return
	}
	a.saveSession()
	a.reset()
	writeJSON(w, map[string]any{"kind": "clear"})
}

func (c *webCmd) resume() {
	w, a, fields, busy := c.w, c.a, c.fields, c.busy
	if busy {
		c.fail("wait until prism finishes")
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
			c.fail("no chat with that number, try /resume")
			return
		}
		full, err := a.store.loadSession(others[k-1].ID)
		if err != nil {
			c.fail(err.Error())
			return
		}
		a.resume(full)
		writeJSON(w, map[string]any{"kind": "reload", "model": a.model, "source": answeredBy("", a.model)})
		return
	}
	if len(others) == 0 {
		c.info("no earlier chats in this folder yet", []string{})
		return
	}
	writeJSON(w, pickWire(resumeMenu(others), func(it menuItem) string { return "/resume " + it.dot }))
}

func (c *webCmd) initChat() {
	w, busy := c.w, c.busy
	if busy {
		c.fail("wait until prism finishes, then run /init")
		return
	}
	writeJSON(w, map[string]any{"kind": "send", "message": initPrompt})
}

func (c *webCmd) plan() {
	w, a := c.w, c.a
	if c.busy || c.s.tabsWorking(c.r, a, false) {
		c.fail("wait until prism finishes, then /plan")
		return
	}
	on, text, message := planSwitch(a, commandArg(c.text, c.fields[0]))
	writeJSON(w, map[string]any{"kind": "plan", "plan": on, "text": text, "message": message})
}

func (c *webCmd) saved() {
	w, a, fields, busy, text := c.w, c.a, c.fields, c.busy, c.text
	if msg, ok := savedMessage(a.dir, text); ok {
		if busy {
			c.fail("wait until prism finishes, then run " + fields[0])
			return
		}
		if msg == "" || strings.HasPrefix(msg, "/") {
			c.note(fields[0] + " has nothing to send · /commands shows where its file is")
			return
		}
		writeJSON(w, map[string]any{"kind": "send", "message": msg})
		return
	}
	writeJSON(w, map[string]any{"kind": "error", "headline": "unknown command", "text": fields[0] + " isn't a command. /help lists them"})
}
