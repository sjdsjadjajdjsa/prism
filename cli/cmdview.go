package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// cmdBlock is a titled group of items, used by /skills, /models, /memory and
// others. A block with no items is skipped.
type cmdBlock struct {
	title   string // empty means no heading
	items   []cmdItem
	nameMax int // widest name column (0 = 20)
}

type cmdItem struct {
	name   string
	desc   string // wrapped to terminal width
	sub    string // muted secondary info (source, provider, status…)
	dot    string // optional status glyph
	dotOK  bool   // green dot, else muted or red
	dotErr bool   // red dot
}

type cmdView struct {
	title  string
	blocks []cmdBlock
	footer string // one next-action hint at the bottom
}

// cmdLines renders a cmdView. width already leaves out the outer indent.
func cmdLines(t theme, v cmdView, width int) []line {
	var out []line

	out = append(out, line{indent: 2, segs: []seg{
		{text: v.title, fg: t.fg, bold: true},
	}})

	for _, blk := range v.blocks {
		out = append(out, line{})

		if blk.title != "" {
			out = append(out, line{indent: 4, segs: []seg{
				{text: blk.title, fg: t.at(0.55), bold: true},
			}})
		}

		// Name column width: at least 8, at most nameMax.
		nameW := 8
		for _, it := range blk.items {
			if n := utf8.RuneCountInString(it.name); n > nameW {
				nameW = n
			}
		}
		nameMax := blk.nameMax
		if nameMax == 0 {
			nameMax = 20
		}
		if nameW > nameMax {
			nameW = nameMax
		}

		for _, it := range blk.items {
			dotSeg := seg{text: "  "}
			if it.dot != "" {
				col := t.muted
				if it.dotOK {
					col = t.green
				} else if it.dotErr {
					col = t.red
				}
				dotSeg = seg{text: it.dot + " ", fg: col}
			}

			// Long names are clipped so they never push the description out of line.
			nameSeg := seg{text: padRunes(clipWidth(it.name, nameW), nameW+2), fg: t.at(0.5), bold: true}

			descWidth := width - 4 - 2 - nameW - 2 // indent(4) + dot(2) + name + gap
			if descWidth < 20 {
				descWidth = 20
			}
			descLines := wrapWords(it.desc, descWidth)
			hangIndent := 4 + 2 + nameW + 2 // matches the prefix width

			for j, dl := range descLines {
				if j == 0 {
					out = append(out, line{indent: 4, segs: []seg{
						dotSeg,
						nameSeg,
						{text: dl, fg: t.fg},
					}})
				} else {
					out = append(out, line{indent: hangIndent, segs: []seg{
						{text: dl, fg: t.fg},
					}})
				}
			}
			if len(descLines) == 0 {
				out = append(out, line{indent: 4, segs: []seg{dotSeg, nameSeg}})
			}

			if it.sub != "" {
				subLines := wrapWords(it.sub, width-hangIndent)
				for _, sl := range subLines {
					out = append(out, line{indent: hangIndent, segs: []seg{
						{text: sl, fg: t.muted},
					}})
				}
			}
		}
	}

	if v.footer != "" {
		out = append(out, line{})
		for _, fl := range wrapWords(v.footer, width-4) {
			out = append(out, line{indent: 4, segs: []seg{{text: fl, fg: t.muted}}})
		}
	}

	return out
}

// wrapWords keeps existing newlines and only splits a word longer than width.
func wrapWords(s string, width int) []string {
	if width < 8 {
		width = 8
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		var cur []rune
		for _, word := range words {
			r := []rune(word)
			for len(r) > width {
				if len(cur) > 0 {
					out, cur = append(out, string(cur)), nil
				}
				out, r = append(out, string(r[:width])), r[width:]
			}
			if len(cur) > 0 && len(cur)+1+len(r) > width {
				out, cur = append(out, string(cur)), nil
			}
			if len(cur) > 0 {
				cur = append(cur, ' ')
			}
			cur = append(cur, r...)
		}
		if len(cur) > 0 {
			out = append(out, string(cur))
		}
	}
	return out
}

// skillsView builds the /skills summary: one line per group with
// its count and the first few names. /skills <name> shows one skill in full.
func skillsView(skills []skill) cmdView {
	blk := cmdBlock{nameMax: 26}
	for _, g := range groupSkills(skills) {
		blk.items = append(blk.items, cmdItem{
			name: fmt.Sprintf("%s (%d)", g.label, len(g.skills)),
			desc: skillNames(showcaseOrder(g.skills), 44),
		})
	}
	footer := "prism picks the right skill by itself · /skills <name> shows one · /skills new <name> writes your own"
	if len(skills) == 0 {
		blk = cmdBlock{items: []cmdItem{{name: "(none)", desc: "no skills found"}}}
		footer = "add a skill: /skills new <name>"
	}
	return cmdView{
		title:  fmt.Sprintf("skills  (%d)", len(skills)),
		blocks: []cmdBlock{blk},
		footer: footer,
	}
}

// commandsView is /commands: saved commands grouped yours / this project,
// one line each; a file that can't run says why.
func commandsView(list []userCommand) cmdView {
	var blocks []cmdBlock
	ready := 0
	for _, src := range []string{"yours", "this project"} {
		blk := cmdBlock{title: src}
		for _, c := range list {
			if c.source != src {
				continue
			}
			it := cmdItem{name: c.name, desc: c.help}
			if c.runnable() {
				ready++
			} else {
				it.dot, it.dotErr, it.desc = "!", true, c.problem
				if strings.HasSuffix(c.problem, "is used instead") {
					it.dot, it.dotErr = "◌", false
				}
			}
			blk.items = append(blk.items, it)
		}
		if len(blk.items) > 0 {
			blocks = append(blocks, blk)
		}
	}
	if len(blocks) == 0 {
		return cmdView{
			title:  "saved commands  (0)",
			blocks: []cmdBlock{{items: []cmdItem{{name: "none yet", desc: "save a prompt as .prism/commands/<name>.md (or ~/.config/prism/commands/), then type /<name>"}}}},
			footer: "/commands new <name> writes a starter file",
		}
	}
	return cmdView{
		title:  fmt.Sprintf("saved commands  (%d)", ready),
		blocks: blocks,
		footer: "type /<name> and any words · $ARGUMENTS in the file takes them · /commands new <name> writes one",
	}
}

// skillNames joins names with " · " while they fit in max runes, then "…".
func skillNames(skills []skill, max int) string {
	var out string
	for i, s := range skills {
		next := s.name
		if i > 0 {
			next = out + " · " + s.name
		}
		if i > 0 && utf8.RuneCountInString(next) > max {
			return out + " …"
		}
		out = next
	}
	return out
}

// skillStepLines is how many lines of a skill's steps /skills <name> shows.
const skillStepLines = 8

// skillView is /skills <name>: what the skill is for, where it comes from and
// the first lines of its steps.
func skillView(s skill) cmdView {
	source := sourceLabel(s.source)
	if source == "" {
		source = "built in"
	}
	info := cmdBlock{items: []cmdItem{{name: "use", desc: s.description}, {name: "from", desc: source}}}
	steps := cmdBlock{title: "steps"}
	var body []string
	for _, l := range strings.Split(s.body, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			body = append(body, l)
		}
	}
	for i, l := range body {
		if i == skillStepLines {
			steps.items = append(steps.items, cmdItem{desc: fmt.Sprintf("… %d more lines", len(body)-i)})
			break
		}
		steps.items = append(steps.items, cmdItem{desc: clipText(l, 160)})
	}
	footer := "prism loads it by itself when a task fits"
	switch s.source {
	case "built in":
		footer += " · to change it, save a copy as .prism/skills/" + s.name + ".md"
	case "this project":
		footer += " · edit .prism/skills/" + s.name + ".md"
	}
	blocks := []cmdBlock{info}
	if len(steps.items) > 0 {
		blocks = append(blocks, steps)
	}
	return cmdView{title: "skill · " + s.name, blocks: blocks, footer: footer}
}

func sourceLabel(src string) string {
	switch src {
	case "built in":
		return ""
	case "yours":
		return "your skill  (~/.config/prism/skills/)"
	case "this project":
		return "project skill  (.prism/skills/)"
	}
	return src
}

func resumeView(others []session) cmdView {
	blk := cmdBlock{}
	for k, s := range others {
		blk.items = append(blk.items, cmdItem{
			name: fmt.Sprintf("%d", k+1),
			desc: s.Title,
			sub:  ago(s.Updated),
		})
	}
	return cmdView{
		title:  "earlier chats",
		blocks: []cmdBlock{blk},
		footer: "type /resume <number> to reopen one",
	}
}

func usageView(u usageDay, agentReqs int, agentSent, agentCached int) cmdView {
	blk := cmdBlock{
		items: []cmdItem{
			{name: "today", desc: fmt.Sprintf("%d requests · %s tokens sent", u.Requests, short(u.Tokens))},
			{name: "this chat", desc: fmt.Sprintf("%d requests · %s tokens sent · %s cached", agentReqs, short(agentSent), short(agentCached))},
		},
	}
	return cmdView{
		title:  "AI usage",
		blocks: []cmdBlock{blk},
		footer: usageFooter(u),
	}
}

// usageFooter says how many of today's requests were free; the free limits
// reset daily, your plans and keys follow their own limits.
func usageFooter(u usageDay) string {
	if u.Yours == 0 {
		return "all free · limits reset daily · add more keys for more room: prism setup"
	}
	return fmt.Sprintf("%d free · %d on your plans or keys · free limits reset daily", u.Requests-u.Yours, u.Yours)
}

func memoryView(title string, lines []string, projectFile string) cmdView {
	blk := cmdBlock{}
	for _, l := range lines {
		if l == "" || strings.HasPrefix(l, "/memory") || strings.HasPrefix(l, "also following") {
			continue
		}
		// lines look like "1  fact text" or "5  fifth fact"
		num, rest, found := strings.Cut(l, "  ")
		if found && len(strings.TrimSpace(num)) <= 2 {
			blk.items = append(blk.items, cmdItem{
				name: strings.TrimSpace(num),
				desc: strings.TrimSpace(rest),
			})
		} else {
			blk.items = append(blk.items, cmdItem{desc: l})
		}
	}
	footer := "/memory forget <n> removes one · /memory forget all clears everything"
	if projectFile != "" {
		footer += " · also following rules in " + projectFile
	}
	return cmdView{
		title:  title,
		blocks: []cmdBlock{blk},
		footer: footer,
	}
}

func mcpView(list []mcpStatus) cmdView {
	if len(list) == 0 {
		return cmdView{
			title: "connected tools",
			blocks: []cmdBlock{{
				items: []cmdItem{{
					name: "none yet",
					desc: "no MCP tools connected",
					sub:  "edit .prism/mcp.json or use /mcp add",
				}},
			}},
			footer: "/mcp add sets one up in a single step · guide: docs/MCP.md",
		}
	}
	blk := cmdBlock{}
	for _, s := range list {
		it := cmdItem{name: s.name}
		switch s.state {
		case "ready":
			it.dot, it.dotOK = "●", true
			it.desc = fmt.Sprintf("%d tools available", len(s.tools))
			if len(s.tools) > 0 {
				it.sub = clipText(strings.Join(s.tools, ", "), 80)
			}
		case "error":
			it.dot, it.dotErr = "✕", true
			it.desc = "connection failed"
			it.sub = clipText(mcpExplain(s.name, s.err), 120)
		default:
			it.dot = "◌"
			it.desc = s.state
		}
		blk.items = append(blk.items, it)
	}
	return cmdView{
		title:  "connected tools",
		blocks: []cmdBlock{blk},
		footer: "prism uses these automatically · /mcp add · /mcp remove <name> · /mcp test <name>",
	}
}

// helpGroups orders /help: every command in commands belongs to one group.
var helpGroups = []struct {
	label string
	names []string
}{
	{"chat", []string{"/new", "/clear", "/resume", "/compact", "/undo", "/plan", "/quit"}},
	{"models & keys", []string{"/models", "/model", "/key", "/login", "/usage"}},
	{"project", []string{"/init", "/files", "/open", "/tree", "/map", "/editor", "/skills", "/commands", "/hooks", "/rules", "/memory", "/mcp", "/web"}},
	{"look & feel", []string{"/theme", "/beam", "/quiet", "/notify", "/help"}},
	{"tabs", []string{"/tabs", "/rename", "/ask"}},
}

// The keys line at the bottom of /help.
const (
	helpKeysTUI = "keys: enter send / correction while working · alt+enter or ctrl+q queue · shift+enter new line · esc stop · tab completes · ctrl+b files · ctrl+e editor · ctrl+o open all steps · ctrl+y theme · ctrl+t new tab · ctrl+w close tab · ctrl+pgup/pgdown switch · alt+1..9 jump · pgup/pgdown/end scroll"
	helpKeysWeb = "keys: enter send / correction while working · alt+enter queue · shift+enter new line · tab completes · ↑↓ pick · ctrl+b files · esc stop or close"
)

func helpView() cmdView { return helpViewFor(false) }

// helpViewFor builds /help, grouped, one short phrase per command. With web
// set it lists only the commands the browser can run.
func helpViewFor(web bool) cmdView {
	byName := map[string]command{}
	for _, c := range commands {
		byName[c.name] = c
	}
	listed := map[string]bool{}
	var blocks []cmdBlock
	add := func(blk cmdBlock) {
		if len(blk.items) > 0 {
			blocks = append(blocks, blk)
		}
	}
	for _, g := range helpGroups {
		blk := cmdBlock{title: g.label}
		for _, name := range g.names {
			if c, ok := byName[name]; ok {
				listed[name] = true
				if !web || c.web {
					blk.items = append(blk.items, cmdItem{name: c.name, desc: c.help})
				}
			}
		}
		add(blk)
	}
	// A command missing from the groups above still shows.
	more := cmdBlock{title: "more"}
	for _, c := range commands {
		if !listed[c.name] && (!web || c.web) {
			more.items = append(more.items, cmdItem{name: c.name, desc: c.help})
		}
	}
	add(more)
	footer := helpKeysTUI
	if web {
		footer = helpKeysWeb
	}
	return cmdView{title: "commands", blocks: blocks, footer: footer}
}

// wire is the JSON form of a cmdView for the web (kind "view").
func (v cmdView) wire() map[string]any {
	blocks := []map[string]any{}
	for _, b := range v.blocks {
		items := []map[string]any{}
		for _, it := range b.items {
			w := map[string]any{"name": it.name, "desc": it.desc}
			if it.sub != "" {
				w["sub"] = it.sub
			}
			if it.dot != "" {
				w["dot"] = it.dot
				w["tone"] = map[bool]string{true: "ok", false: "muted"}[it.dotOK]
				if it.dotErr {
					w["tone"] = "err"
				}
			}
			items = append(items, w)
		}
		blocks = append(blocks, map[string]any{"title": b.title, "items": items})
	}
	return map[string]any{"kind": "view", "title": v.title, "blocks": blocks, "footer": v.footer}
}
