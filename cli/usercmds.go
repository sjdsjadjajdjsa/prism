package main

// Saved commands: a markdown file .prism/commands/<name>.md (this project) or
// <config>/prism/commands/<name>.md (yours) becomes /<name>. Typing
// "/<name> some words" sends the file's text as a chat message, with
// $ARGUMENTS replaced by the words and $1, $2 … by single words. The idea
// comes from opencode commands, pi prompt templates and Gemini CLI custom
// commands; the code is prism's own.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type userCommand struct {
	name    string // with the slash, like /review
	help    string // description: from the front matter, else the first line
	body    string
	source  string // "yours" or "this project"
	file    string // where it lives, for /commands
	problem string // set when the file is listed but /<name> does not run it
}

func (c userCommand) runnable() bool { return c.problem == "" }

// parseUserCommand reads optional front matter (only description: is used)
// and returns the description and the message text.
func parseUserCommand(data string) (desc, body string) {
	data = strings.ReplaceAll(strings.TrimPrefix(data, "\ufeff"), "\r\n", "\n")
	if rest, ok := strings.CutPrefix(data, "---\n"); ok {
		if head, after, ok := strings.Cut("\n"+rest, "\n---"); ok {
			for _, l := range strings.Split(head, "\n") {
				if k, v, ok := strings.Cut(l, ":"); ok && strings.TrimSpace(k) == "description" {
					desc = strings.Trim(strings.TrimSpace(v), `"'`)
				}
			}
			_, data, _ = strings.Cut(after, "\n")
		}
	}
	body = strings.TrimSpace(data)
	if desc == "" {
		for _, l := range strings.Split(body, "\n") {
			if l = strings.TrimSpace(strings.TrimLeft(l, "# ")); l != "" {
				desc = l
				break
			}
		}
	}
	return clipText(desc, 70), body
}

var cmdPlaceholder = regexp.MustCompile(`\$(ARGUMENTS|[1-9][0-9]?)`)

// expandUserCommand fills in what was typed after the command name. With no
// placeholder in the text, the words go on a new paragraph at the end.
func expandUserCommand(body, args string) string {
	args = strings.TrimSpace(args)
	if !cmdPlaceholder.MatchString(body) {
		if args == "" {
			return body
		}
		return body + "\n\n" + args
	}
	words := splitArgs(args)
	return cmdPlaceholder.ReplaceAllStringFunc(body, func(m string) string {
		if m == "$ARGUMENTS" {
			return args
		}
		if n, _ := strconv.Atoi(m[1:]); n <= len(words) {
			return words[n-1]
		}
		return ""
	})
}

// splitArgs splits on spaces; "double" or 'single' quotes keep words together.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	quote, in := rune(0), false
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '"' || r == '\''):
			quote, in = r, true
		case quote == 0 && (r == ' ' || r == '\t' || r == '\n'):
			if in {
				out, in = append(out, cur.String()), false
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if in {
		out = append(out, cur.String())
	}
	return out
}

// userCommandDirs are where saved commands live, lowest priority first.
func userCommandDirs(dir string) [][2]string {
	var out [][2]string
	if c := configDir(); c != "" {
		out = append(out, [2]string{filepath.Join(c, "commands"), "yours"})
	}
	return append(out, [2]string{filepath.Join(dir, ".prism", "commands"), "this project"})
}

// The cache is keyed by folder and a signature of every file's name, size
// and modification time, so an added, edited or deleted file is seen on the
// next use without a watcher.
var savedCache struct {
	sync.Mutex
	dir, sig string
	at       time.Time
	list     []userCommand
}

// loadUserCommands lists the saved commands for dir, re-reading the files
// when any changed. maxAge > 0 skips even the stat calls when the last check
// is that recent (the suggestion box redraws often).
func loadUserCommands(dir string, maxAge time.Duration) []userCommand {
	savedCache.Lock()
	defer savedCache.Unlock()
	if maxAge > 0 && savedCache.dir == dir && time.Since(savedCache.at) < maxAge {
		return savedCache.list
	}
	type found struct{ path, label string }
	var files []found
	var sig strings.Builder
	for _, d := range userCommandDirs(dir) {
		matches, _ := filepath.Glob(filepath.Join(d[0], "*.md"))
		sort.Strings(matches)
		root := resolvedDir(d[0])
		if d[1] == "this project" {
			if rel, err := filepath.Rel(resolvedDir(dir), root); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
		}
		for _, p := range matches {
			st, err := os.Stat(p)
			if err != nil || st.IsDir() || st.Size() > 64<<10 {
				continue
			}
			if real, err := filepath.EvalSymlinks(p); err != nil || filepath.Dir(real) != root {
				continue
			}
			files = append(files, found{p, d[1]})
			sig.WriteString(p + "|" + strconv.FormatInt(st.Size(), 10) + "|" + strconv.FormatInt(st.ModTime().UnixNano(), 10) + "\n")
		}
	}
	savedCache.at = time.Now()
	if savedCache.dir == dir && savedCache.sig == sig.String() {
		return savedCache.list
	}
	var list []userCommand
	for _, f := range files {
		data, err := os.ReadFile(f.path)
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(f.path), ".md")
		c := userCommand{name: "/" + base, source: f.label, file: shortPath(f.path, dir)}
		c.help, c.body = parseUserCommand(string(data))
		switch {
		case !skillName.MatchString(base) || !utf8.ValidString(base):
			c.problem = "names use lowercase letters, numbers and dashes · rename " + c.file
		case isBuiltin(c.name):
			c.problem = c.name + " is built in, so this file is never used · rename " + c.file
		case c.body == "":
			c.problem = "the file has no text to send yet · edit " + c.file
		case strings.HasPrefix(c.body, "/"):
			c.problem = "the text starts with /, so it would run as a command · start it with words"
		}
		list = append(list, c)
	}
	// This project wins over yours.
	for i := range list {
		if list[i].source != "yours" || !list[i].runnable() {
			continue
		}
		for _, p := range list {
			if p.source == "this project" && p.name == list[i].name && p.runnable() {
				list[i].problem = "this project's " + p.name + " is used instead"
			}
		}
	}
	savedCache.dir, savedCache.sig, savedCache.list = dir, sig.String(), list
	return list
}

func findUserCommand(dir, name string) (userCommand, bool) {
	for _, c := range loadUserCommands(dir, 0) {
		if c.name == name && c.runnable() {
			return c, true
		}
	}
	return userCommand{}, false
}

// savedMessage is the chat message for "/<name> args", or ok=false when name
// is not a saved command. text is the whole typed line.
func savedMessage(dir, text string) (msg string, ok bool) {
	text = strings.TrimSpace(text)
	name, args, _ := strings.Cut(text, " ")
	if i := strings.IndexAny(name, "\n\t"); i >= 0 {
		name, args = name[:i], text[i+1:]
	}
	c, ok := findUserCommand(dir, name)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(expandUserCommand(c.body, args)), true
}

// newUserCommand is /commands new <name>: a starter file in this project.
func newUserCommand(dir, name string) string {
	name = strings.TrimPrefix(name, "/")
	if !skillName.MatchString(name) {
		return "command names use lowercase letters, numbers and dashes, like review-pr"
	}
	if isBuiltin("/" + name) {
		return "/" + name + " is built in · pick another name"
	}
	rel := ".prism/commands/" + name + ".md"
	path := filepath.Join(dir, ".prism", "commands", name+".md")
	if _, err := os.Stat(path); err == nil {
		return rel + " already exists"
	}
	body := "---\ndescription: one line saying what /" + name + " does\n" +
		"# Below the second --- line, write the message prism gets when you type /" + name + ".\n" +
		"# $ARGUMENTS becomes the words you type after /" + name + "; $1, $2 are single words.\n---\n\n" +
		"Replace this line with your message. $ARGUMENTS\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err.Error()
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err.Error()
	}
	return "wrote " + rel + " · edit it, then type /" + name
}
