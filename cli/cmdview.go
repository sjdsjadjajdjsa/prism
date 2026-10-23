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
	title string // group heading; empty = no heading
	items []cmdItem
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
		if nameW > 20 {
			nameW = 20
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

// skillsView builds a cmdView for /skills output.
func skillsView(skills []skill) cmdView {
	groups := groupSkills(skills)
	var blocks []cmdBlock
	for _, g := range groups {
		blk := cmdBlock{title: g.label}
		for _, s := range g.skills {
			blk.items = append(blk.items, cmdItem{
				name: s.name,
				desc: s.description,
				sub:  sourceLabel(s.source),
			})
		}
		blocks = append(blocks, blk)
	}
	footer := "prism picks the right skill automatically · add your own: /skills new <name>"
	if len(skills) == 0 {
		blocks = []cmdBlock{{items: []cmdItem{{name: "(none)", desc: "no skills found"}}}}
		footer = "add a skill: /skills new <name>"
	}
	return cmdView{
		title:  fmt.Sprintf("skills  (%d)", len(skills)),
		blocks: blocks,
		footer: footer,
	}
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

// helpView builds a cmdView for /help output.
func helpView() cmdView {
	groups := []struct {
		label string
		names []string
	}{
		{"chat", []string{"/new", "/resume", "/clear", "/compact", "/undo", "/quit"}},
		{"tabs", []string{"/tabs", "/rename", "/ask"}},
		{"project", []string{"/init", "/tree", "/memory", "/skills"}},
		{"tools", []string{"/mcp", "/web", "/usage", "/models"}},
		{"look", []string{"/theme", "/beam", "/quiet", "/help"}},
	}
	var blocks []cmdBlock
	for _, g := range groups {
		blk := cmdBlock{title: g.label}
		for _, name := range g.names {
			for _, c := range commands {
				if c.name == name {
					blk.items = append(blk.items, cmdItem{
						name: c.name,
						desc: c.help,
					})
					break
				}
			}
		}
		blocks = append(blocks, blk)
	}
	return cmdView{
		title:  "commands",
		blocks: blocks,
		footer: "keys: ctrl+t new tab · ctrl+w close tab · ctrl+pgup/pgdown switch · alt+1..9 jump · ctrl+o open all steps · ctrl+y theme · esc stop",
	}
}
