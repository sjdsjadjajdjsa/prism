package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// cmdBlock is a titled group of items used by /skills, /models, /memory, etc.
// It renders as:
//
//	Title                          ← bold, accent colour
//	  Name   What it does…         ← name bold, description wrapped with hanging indent
//	  …
//	  (muted secondary info)       ← optional per-item secondary line
//
// A zero-value cmdBlock with no items is silently skipped.
type cmdBlock struct {
	title   string // group heading; empty = no heading
	items   []cmdItem
	nameMax int // widest name column (0 = 20)
}

type cmdItem struct {
	name   string // short label shown bold/accent
	desc   string // plain-English one-liner, wrapped to terminal width
	sub    string // muted secondary info (source, provider, status…)
	dot    string // optional status glyph: "●", "◌", "✕"
	dotOK  bool   // true → green dot, false → muted/red
	dotErr bool   // true → red dot
}

// cmdView is the full structured output for a command.
type cmdView struct {
	title  string     // top-level title (bold)
	blocks []cmdBlock // grouped content
	footer string     // one next-action hint at the bottom
}

// cmdLines converts a cmdView into []line for the transcript renderer.
// width is the available column width (already accounting for outer indent).
func cmdLines(t theme, v cmdView, width int) []line {
	var out []line

	// Title line
	out = append(out, line{indent: 2, segs: []seg{
		{text: v.title, fg: t.fg, bold: true},
	}})

	for _, blk := range v.blocks {
		// blank line before each block
		out = append(out, line{})

		if blk.title != "" {
			out = append(out, line{indent: 4, segs: []seg{
				{text: blk.title, fg: t.at(0.55), bold: true},
			}})
		}

		// measure name column width (max 20, min 8)
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
			// dot glyph
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

			// name segment (bold, accent); names past the column cap are
			// clipped so they never push the description out of line
			nameSeg := seg{text: padRune(clip(it.name, nameW), nameW+2), fg: t.at(0.5), bold: true}

			// description: wrap to remaining width with hanging indent
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

			// secondary info (muted, indented under name)
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

	// footer — wrap to available width
	if v.footer != "" {
		out = append(out, line{})
		for _, fl := range wrapWords(v.footer, width-4) {
			out = append(out, line{indent: 4, segs: []seg{{text: fl, fg: t.muted}}})
		}
	}

	return out
}

// padRune pads s to n runes with spaces.
func padRune(s string, n int) string {
	r := utf8.RuneCountInString(s)
	if r >= n {
		return s
	}
	return s + strings.Repeat(" ", n-r)
}

// wrapWords wraps text to width, returning one string per line.
// It respects existing newlines and never splits a word unless it exceeds width.
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
			// hard-break words longer than width
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

// ── helpers used by commands.go ──────────────────────────────────────────────

// skillsView builds the /skills summary: one line per group (from
// groupSkills) with its count and the first few names; /skills <name> shows
// one skill in full.
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

// resumeView builds a cmdView for /resume output.
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

// usageView builds a cmdView for /usage output.
func usageView(u usageDay, agentReqs int, agentSent, agentCached int) cmdView {
	blk := cmdBlock{
		items: []cmdItem{
			{name: "today", desc: fmt.Sprintf("%d requests · %s tokens sent", u.Requests, short(u.Tokens))},
			{name: "this chat", desc: fmt.Sprintf("%d requests · %s tokens sent · %s cached", agentReqs, short(agentSent), short(agentCached))},
		},
	}
	return cmdView{
		title:  "free AI usage",
		blocks: []cmdBlock{blk},
		footer: "all free · limits reset daily · add more keys for more room: prism setup",
	}
}

// memoryView builds a cmdView for /memory output.
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

// mcpView builds a cmdView for /mcp output.
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
	{"chat", []string{"/new", "/clear", "/resume", "/compact", "/undo", "/quit"}},
	{"models & keys", []string{"/models", "/model", "/key", "/login", "/usage"}},
	{"project", []string{"/init", "/files", "/open", "/tree", "/skills", "/memory", "/mcp", "/web"}},
	{"look & feel", []string{"/theme", "/beam", "/quiet", "/help"}},
	{"tabs", []string{"/tabs", "/rename", "/ask"}},
}

// The one keys line at the bottom of /help.
const (
	helpKeysTUI = "keys: enter send · esc stop · tab completes · ctrl+o open all steps · ctrl+y theme · ctrl+t new tab · ctrl+w close tab · ctrl+pgup/pgdown switch · alt+1..9 jump"
	helpKeysWeb = "keys: enter send · shift+enter new line · tab completes · ↑↓ pick · esc stop or close"
)

// helpView builds /help for the terminal.
func helpView() cmdView { return helpViewFor(false) }

// helpViewFor builds /help, grouped with one short phrase per command. For the
// browser (web) it lists only the commands the web can run.
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
	// a command added to the list but missing from the groups above still shows
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

// wire is the JSON form of a cmdView for the web (kind "view"), rendered as a
// grid by web/app.js addView.
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
