package main

import (
	"bytes"
	"crypto/sha256"
	hexenc "encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"unicode"
)

type risk struct{ key, why string }

var riskReasons = map[string]string{
	"delete folders":                 "deletes files and folders",
	"throw away changes":             "throws away work that may not be saved anywhere",
	"git push":                       "sends your code to GitHub or another server",
	"force push":                     "overwrites the history on the server",
	"publish":                        "puts something online where other people see it",
	"run a script from the internet": "runs code downloaded from the internet",
	"remote computer":                "connects to another computer",
	"database delete":                "deletes data in a database",
	"system":                         "can break the computer or the box Prism runs in",
}

// Each entry is one executable command, never an entire line containing several
// commands. Keep nested commands separate so an allow cannot hide another action.
type riskCommand struct {
	words    []string
	text     string
	at       int
	internet bool
	system   bool
	database bool
}

type riskWord struct {
	text     string
	at       int
	download bool
	dynamic  bool
}

type riskParser struct{ commands []riskCommand }

func riskProgram(s string) string { return filepath.Base(s) }

func riskAssignment(s string) bool {
	name, _, ok := strings.Cut(s, "=")
	if !ok || name == "" {
		return false
	}
	for i, c := range name {
		if c != '_' && !unicode.IsLetter(c) && (i == 0 || !unicode.IsDigit(c)) {
			return false
		}
	}
	return true
}

// Strip only command-position wrappers, not ordinary arguments containing names
// such as "git push". Options with values must not become the command name.
func riskUnwrap(w []riskWord) []riskWord {
	for len(w) > 0 {
		if riskAssignment(w[0].text) {
			w = w[1:]
			continue
		}
		switch riskProgram(w[0].text) {
		case "then", "do", "else", "if", "elif", "while", "until", "!", "{", "}":
			w = w[1:]
		case "env", "sudo", "doas", "nohup", "time", "xargs", "command", "builtin", "exec", "nice", "timeout", "ionice", "stdbuf", "chrt", "setsid", "flock", "watch":
			wrapper := riskProgram(w[0].text)
			w = w[1:]
			for len(w) > 0 {
				s := w[0].text
				if s == "--" {
					w = w[1:]
					break
				}
				if wrapper == "env" && riskAssignment(s) {
					w = w[1:]
					continue
				}
				if !strings.HasPrefix(s, "-") || s == "-" {
					break
				}
				if wrapper == "command" && (s == "-v" || s == "-V") {
					return nil // A lookup, not an execution.
				}
				w = w[1:]
				takesValue := false
				switch wrapper {
				case "sudo", "doas":
					takesValue = s == "-u" || s == "-g" || s == "-h" || s == "-p" || s == "-C" || s == "-T" || s == "-r" || s == "-t" || s == "--user" || s == "--group" || s == "--host" || s == "--prompt" || s == "--chdir"
				case "env":
					takesValue = s == "-u" || s == "--unset" || s == "-C" || s == "--chdir" || s == "-S" || s == "--split-string"
				case "time":
					takesValue = s == "-o" || s == "--output" || s == "-f" || s == "--format"
				case "exec":
					takesValue = s == "-a"
				case "nice":
					takesValue = s == "-n" || s == "--adjustment"
				case "timeout":
					takesValue = s == "-s" || s == "--signal" || s == "-k" || s == "--kill-after"
				case "ionice":
					takesValue = s == "-c" || s == "--class" || s == "-n" || s == "--classdata"
				case "stdbuf":
					takesValue = s == "-i" || s == "-o" || s == "-e" || s == "--input" || s == "--output" || s == "--error"
				case "chrt":
					takesValue = s == "-T" || s == "-P" || s == "-D"
				case "flock":
					takesValue = s == "-w" || s == "--timeout" || s == "-E" || s == "--conflict-exit-code"
				case "watch":
					takesValue = s == "-n" || s == "--interval"
				case "xargs":
					takesValue = s == "-I" || s == "-n" || s == "-P" || s == "-L" || s == "-s" || s == "-d" || s == "-E" || s == "--replace" || s == "--max-args" || s == "--max-procs" || s == "--delimiter"
				}
				if takesValue && len(w) > 0 {
					w = w[1:]
				}
			}
			// These wrappers have an operand before the executable.
			if (wrapper == "timeout" || wrapper == "chrt" || wrapper == "flock") && len(w) > 0 {
				w = w[1:]
			}
			if wrapper == "watch" && len(w) > 0 {
				var text []string
				for _, word := range w {
					text = append(text, word.text)
				}
				return []riskWord{{text: "sh", at: w[0].at}, {text: "-c", at: w[0].at}, {text: strings.Join(text, " "), at: w[0].at}}
			}
		default:
			return w
		}
	}
	return w
}

// Find the closing delimiter of a substitution, honoring quotes and nested
// substitutions. Quoted plain text is deliberately not parsed as commands.
func riskSubEnd(s string, start int, backtick bool) int {
	quote := byte(0)
	depth := 1
	for i := start; i < len(s); i++ {
		c := s[i]
		if c == '\\' && quote != '\'' {
			i++
			continue
		}
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			}
			continue
		}
		if backtick && c == '`' {
			return i
		}
		if c == '\'' && quote == 0 {
			quote = c
			continue
		}
		if c == '"' {
			if quote == '"' {
				quote = 0
			} else if quote == 0 {
				quote = c
			}
			continue
		}
		if c == '`' {
			i = riskSubEnd(s, i+1, true)
			continue
		}
		if c == '(' && (quote == 0 || (i > 0 && s[i-1] == '$')) {
			depth++
		}
		if c == ')' && (quote == 0 || depth > 1) {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(s)
}

func (p *riskParser) word(s string, i *int, base int) riskWord {
	w := riskWord{at: base + *i}
	var b strings.Builder
	quote := byte(0)
	for *i < len(s) {
		c := s[*i]
		if quote == 0 && (unicode.IsSpace(rune(c)) || strings.ContainsRune(";&|()", rune(c)) || ((c == '<' || c == '>') && (*i+1 == len(s) || s[*i+1] != '('))) {
			break
		}
		if c == '\'' && quote != '"' {
			if quote == '\'' {
				quote = 0
			} else {
				quote = '\''
			}
			*i++
			continue
		}
		if quote == '\'' {
			b.WriteByte(c)
			*i++
			continue
		}
		if c == '"' {
			if quote == '"' {
				quote = 0
			} else {
				quote = '"'
			}
			*i++
			continue
		}
		if c == '\\' && *i+1 < len(s) {
			next := s[*i+1]
			if next == '\n' {
				*i += 2
				continue
			}
			if quote == 0 || strings.ContainsRune("$`\"\\", rune(next)) {
				b.WriteByte(next)
				*i += 2
				continue
			}
		}
		sub := c == '`' || ((c == '$' || (quote == 0 && (c == '<' || c == '>'))) && *i+1 < len(s) && s[*i+1] == '(')
		if sub {
			start := *i + 2
			if c == '`' {
				start = *i + 1
			}
			end := riskSubEnd(s, start, c == '`')
			before := len(p.commands)
			p.parse(s[start:end], base+start)
			w.dynamic = true
			for _, cmd := range p.commands[before:] {
				if len(cmd.words) > 0 && (riskProgram(cmd.words[0]) == "curl" || riskProgram(cmd.words[0]) == "wget") {
					w.download = true
				}
			}
			stop := end
			if stop < len(s) {
				stop++
			}
			// Resolve a command-position lookup without pretending to execute
			// arbitrary substitution output. Keep all other substitutions intact.
			lookup := strings.Fields(s[start:end])
			resolved := ""
			if len(lookup) == 2 && riskProgram(lookup[0]) == "which" {
				resolved = lookup[1]
			} else if len(lookup) == 3 && lookup[0] == "command" && lookup[1] == "-v" {
				resolved = lookup[2]
			}
			if b.Len() == 0 && resolved != "" && !strings.ContainsAny(resolved, "$`;&|()") {
				b.WriteString(resolved)
			} else {
				b.WriteString(s[*i:stop])
			}
			*i = stop
			continue
		}
		if c == '$' {
			w.dynamic = true
		}
		b.WriteByte(c)
		*i++
	}
	w.text = b.String()
	return w
}

func (p *riskParser) parse(s string, base int) {
	var words []riskWord
	forks := make(map[int]string)
	for _, loc := range riskFork.FindAllStringIndex(s, -1) {
		forks[base+loc[0]] = s[loc[0]:loc[1]]
	}
	piped, previousDownload := false, false
	var input *riskWord
	var previousOutput *riskWord
	hereStart, hereEnd := -1, -1
	flush := func() {
		w := riskUnwrap(words)
		if len(w) == 0 {
			words, input, previousOutput = nil, nil, nil
			return
		}
		cmd := riskCommand{at: w[0].at}
		for _, word := range w {
			cmd.words = append(cmd.words, word.text)
		}
		cmd.text = strings.Join(cmd.words, " ")
		if bomb, ok := forks[cmd.at]; ok && cmd.words[0] == ":" {
			cmd.system, cmd.text = true, bomb
		}
		program := riskProgram(w[0].text)
		code, stdin := riskInterpreterMode(w)
		text := input
		if text == nil && piped {
			text = previousOutput
		}
		cmd.database = riskDatabaseClientText(w, strings.Join(cmd.words[1:], " "))
		if text != nil {
			cmd.database = cmd.database || riskDatabaseClientText(w, text.text)
		}
		interpreter := riskIsInterpreter(program) || program == "eval" || program == "source" || program == "."
		cmd.internet = interpreter && piped && previousDownload && stdin
		for _, word := range w[1:] {
			cmd.internet = cmd.internet || (interpreter && word.download)
		}
		commandIndex := len(p.commands)
		p.commands = append(p.commands, cmd)
		previousDownload = program == "curl" || program == "wget" || (piped && previousDownload)
		if riskIsShell(program) {
			if code != nil {
				p.parse(code.text, code.at)
			} else if stdin && text != nil {
				p.parse(text.text, text.at)
			}
		} else if program == "eval" && len(w) > 1 {
			p.parse(strings.Join(cmd.words[1:], " "), w[1].at)
		} else if riskIsInterpreter(program) {
			if code == nil && stdin {
				code = text
			}
			if code != nil {
				database := p.interpreterCode(code.text, code.at)
				p.commands[commandIndex].database = database
			}
		}
		previousOutput = riskLiteralOutput(w)
		words, input = nil, nil
	}
	for i := 0; i < len(s); {
		if i == hereStart {
			i = hereEnd
			hereStart, hereEnd = -1, -1
			continue
		}
		c := s[i]
		if c == '\\' && i+1 < len(s) && s[i+1] == '\n' {
			i += 2
			continue
		}
		// Recognize the whole operator before command separators: the ampersand
		// in &>, >& and <& is not a background-command boundary.
		if end, op := riskRedirect(s, i); op != "" {
			i = end
			stripTabs := op == "<<" && i < len(s) && s[i] == '-'
			if stripTabs {
				i++
			}
			for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
				i++
			}
			if i < len(s) {
				word := p.word(s, &i, base)
				if op == "<<<" {
					input = &word
				} else if op == "<<" {
					if newline := strings.IndexByte(s[i:], '\n'); newline >= 0 {
						hereStart = i + newline + 1
						hereEnd = len(s)
						var body strings.Builder
						for pos := hereStart; pos < len(s); {
							end := strings.IndexByte(s[pos:], '\n')
							if end < 0 {
								end = len(s) - pos
							}
							line := s[pos : pos+end]
							if stripTabs {
								line = strings.TrimLeft(line, "\t")
							}
							pos += end
							if pos < len(s) {
								pos++
							}
							if line == word.text {
								hereEnd = pos
								break
							}
							body.WriteString(line + "\n")
						}
						input = &riskWord{text: body.String(), at: base + hereStart}
					}
				}
			}
			continue
		}
		if c == '\n' || strings.ContainsRune(";&|()", rune(c)) {
			flush()
			piped = c == '|' && (i+1 == len(s) || s[i+1] != '|')
			if (c == '|' || c == '&') && i+1 < len(s) && s[i+1] == c {
				i++
			}
			if !piped {
				previousDownload = false
			}
			i++
			continue
		}
		if unicode.IsSpace(rune(c)) {
			i++
			continue
		}
		if c == '#' {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		words = append(words, p.word(s, &i, base))
	}
	flush()
}

// A descriptor is an operator prefix only at a token boundary. word stops at
// unquoted < and >, preserving quoted/escaped literals and process substitutions.
func riskRedirect(s string, start int) (int, string) {
	i := start
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == start && strings.HasPrefix(s[i:], "&>") {
		i += 2
		if i < len(s) && s[i] == '>' {
			i++
		}
		return i, s[start:i]
	}
	if i == len(s) || (s[i] != '<' && s[i] != '>') || (i+1 < len(s) && s[i+1] == '(') {
		return start, ""
	}
	opStart := i
	c := s[i]
	i++
	if i < len(s) {
		switch {
		case s[i] == c:
			i++
			if c == '<' && i < len(s) && s[i] == '<' {
				i++
			}
		case s[i] == '&', c == '<' && s[i] == '>', c == '>' && s[i] == '|':
			i++
		}
	}
	return i, s[opStart:i]
}

// Share execution mode between internet-pipe detection and literal code parsing.
// A script/module/eval operand makes stdin data, not the interpreter's program.
func riskInterpreterMode(w []riskWord) (code *riskWord, stdin bool) {
	if len(w) == 0 {
		return nil, false
	}
	program := riskProgram(w[0].text)
	if riskIsShell(program) {
		index, reads := riskShellMode(w)
		if index > 0 {
			return &w[index], false
		}
		return nil, reads
	}
	if !riskIsInterpreter(program) {
		return nil, false
	}
	for i := 1; i < len(w); i++ {
		a := w[i].text
		if a == "--" {
			return nil, i+1 == len(w) || w[i+1].text == "-"
		}
		if a == "-" {
			return nil, true
		}
		if !strings.HasPrefix(a, "-") {
			return nil, false
		}
		flags := []string{"-e", "-E"}
		if program == "python" || program == "python3" {
			flags = []string{"-c"}
			if strings.HasPrefix(a, "-m") {
				return nil, false
			}
		} else if program == "node" {
			flags = []string{"-e", "-p", "--eval", "--print"}
		}
		for _, flag := range flags {
			if a == flag {
				if i+1 < len(w) {
					return &w[i+1], false
				}
				return nil, false
			}
			prefix := flag
			if strings.HasPrefix(flag, "--") {
				prefix += "="
			}
			if strings.HasPrefix(a, prefix) && len(a) > len(prefix) {
				word := w[i]
				word.text = a[len(prefix):]
				return &word, false
			}
		}
		// Options whose following operand is configuration, not a script.
		takesValue := false
		switch program {
		case "python", "python3":
			takesValue = a == "-W" || a == "-X" || a == "--check-hash-based-pycs"
		case "node":
			takesValue = a == "-r" || a == "--require" || a == "--import" || a == "--loader" || a == "--input-type"
		case "perl":
			takesValue = a == "-I" || a == "-M" || a == "-m" || a == "-F"
		case "ruby":
			takesValue = a == "-I" || a == "-r" || a == "-C" || a == "-E"
		}
		if takesValue {
			i++
		}
	}
	return nil, true
}

func riskCommands(command string) []riskCommand {
	p := riskParser{}
	p.parse(command, 0)
	sort.SliceStable(p.commands, func(i, j int) bool { return p.commands[i].at < p.commands[j].at })
	return p.commands
}

// A shell executes stdin only without -c or a script operand (-s permits
// positional parameters). Do not mistake a script's input for shell code.
func riskShellMode(w []riskWord) (code int, stdin bool) {
	if len(w) == 0 || !riskIsShell(riskProgram(w[0].text)) {
		return 0, false
	}
	stdin = true
	forceStdin := false
	for i := 1; i < len(w); i++ {
		a := w[i].text
		if a == "--" {
			return 0, forceStdin || i+1 == len(w)
		}
		if a == "-o" || a == "-O" {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			if !strings.HasPrefix(a, "--") && strings.Contains(a, "c") {
				if i+1 < len(w) {
					return i + 1, false
				}
				return 0, false
			}
			forceStdin = forceStdin || (!strings.HasPrefix(a, "--") && strings.Contains(a, "s"))
			continue
		}
		return 0, forceStdin || a == "-"
	}
	return 0, stdin
}

func riskIsShell(s string) bool {
	return s == "sh" || s == "bash" || s == "zsh" || s == "dash" || s == "ksh"
}

// Evaluate only literal echo/printf output, never arbitrary pipeline programs.
func riskLiteralOutput(w []riskWord) *riskWord {
	if len(w) < 2 {
		return nil
	}
	for _, word := range w[1:] {
		if word.dynamic {
			return nil
		}
	}
	args := make([]string, 0, len(w)-1)
	for _, word := range w[1:] {
		args = append(args, word.text)
	}
	decode := func(s string) string {
		return strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\r`, "\r", `\\`, `\`).Replace(s)
	}
	text := ""
	switch riskProgram(w[0].text) {
	case "echo":
		escapes := false
		for len(args) > 0 && (args[0] == "-n" || args[0] == "-e" || args[0] == "-E") {
			escapes = args[0] == "-e" || (escapes && args[0] != "-E")
			args = args[1:]
		}
		text = strings.Join(args, " ")
		if escapes {
			text = decode(text)
		}
	case "printf":
		if args[0] == "--" {
			args = args[1:]
		}
		if len(args) == 0 {
			return nil
		}
		format := decode(args[0])
		args = args[1:]
		// printf repeats its format until all arguments have been consumed.
		for {
			before := len(args)
			for i := 0; i < len(format); i++ {
				if format[i] != '%' {
					text += string(format[i])
					continue
				}
				i++
				if i == len(format) {
					return nil
				}
				if format[i] == '%' {
					text += "%"
				} else if format[i] == 's' || format[i] == 'b' {
					if len(args) > 0 {
						value := args[0]
						if format[i] == 'b' {
							value = decode(value)
						}
						text += value
						args = args[1:]
					}
				} else {
					return nil
				}
			}
			if len(args) == 0 || len(args) == before {
				break
			}
		}
	default:
		return nil
	}
	return &riskWord{text: text, at: w[1].at}
}

// Only execution APIs (and language backticks) authorize interpreting literals
// as commands. Ordinary print/log strings must not become executable commands.
var riskCodeCall = regexp.MustCompile(`\b(?:os\.system|os\.popen|subprocess\.(?:run|call|Popen|check_call|check_output)|exec|execSync|execFile|execFileSync|spawn|spawnSync|system|popen)\s*\(`)
var riskCodeLiteral = regexp.MustCompile("(?s)'(?:\\\\.|[^'\\\\])*'|\"(?:\\\\.|[^\"\\\\])*\"|`(?:\\\\.|[^`\\\\])*`")

var riskSQLCall = regexp.MustCompile(`\b(?:execute|executemany|executescript|query)\s*\(`)
var riskMongoDropCall = regexp.MustCompile(`\bdropDatabase\s*\(`)

func (p *riskParser) interpreterCode(code string, base int) bool {
	// Mask strings before finding calls so a printed API name is not a call.
	masked := []byte(code)
	literals := riskCodeLiteral.FindAllStringIndex(code, -1)
	for _, loc := range literals {
		for i := loc[0]; i < loc[1]; i++ {
			masked[i] = ' '
		}
		if code[loc[0]] == '`' {
			p.parse(code[loc[0]+1:loc[1]-1], base+loc[0]+1)
		}
	}
	database := riskMongoDropCall.Match(masked)
	for _, call := range riskSQLCall.FindAllIndex(masked, -1) {
		end := riskSubEnd(string(masked), call[1], false)
		for _, loc := range literals {
			if loc[0] >= call[1] && loc[1] <= end {
				database = database || riskDatabaseText(code[loc[0]+1:loc[1]-1])
			}
		}
	}
	for _, call := range riskCodeCall.FindAllIndex(masked, -1) {
		end := riskSubEnd(string(masked), call[1], false)
		var list []string
		listAt, depth := 0, 0
		pos := call[1]
		for _, loc := range literals {
			if loc[0] < call[1] || loc[1] > end {
				continue
			}
			for ; pos < loc[0]; pos++ {
				if code[pos] == '[' {
					depth++
				} else if code[pos] == ']' {
					depth--
				}
			}
			text := code[loc[0]+1 : loc[1]-1]
			text = strings.NewReplacer(`\'`, "'", `\"`, `"`, `\\`, `\`).Replace(text)
			if depth > 0 {
				if len(list) == 0 {
					listAt = base + loc[0] + 1
				}
				list = append(list, text)
			} else {
				p.parse(text, base+loc[0]+1)
			}
			pos = loc[1]
		}
		if len(list) > 0 {
			p.parse(strings.Join(list, " "), listAt)
		}
	}
	return database
}

func riskIsInterpreter(s string) bool {
	switch s {
	case "sh", "bash", "zsh", "dash", "ksh", "python", "python3", "node", "perl", "ruby":
		return true
	}
	return false
}

var riskSQL = regexp.MustCompile(`(?i)\b(?:DROP\s+(?:TABLE|DATABASE|SCHEMA)\b|TRUNCATE\s+(?:TABLE\s+)?[a-z_"\x60]|DELETE\s+FROM\s+)`)
var riskWhere = regexp.MustCompile(`(?i)\bWHERE\b`)
var riskFork = regexp.MustCompile(`:\s*\(\s*\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;?\s*:`)

// Only database programs consume SQL text. Redirected filenames remain unknown;
// literal stdin is inspected separately by the parser, never as shell arguments.
func riskDatabaseClientText(w []riskWord, text string) bool {
	switch riskProgram(w[0].text) {
	case "psql", "mysql", "mariadb", "sqlite3", "sqlcmd", "clickhouse-client", "duckdb":
		return riskDatabaseText(text)
	case "cockroach":
		return len(w) > 1 && w[1].text == "sql" && riskDatabaseText(text)
	case "mongosh", "mongo":
		return riskMongoDropCall.MatchString(text)
	}
	return false
}

func riskDatabaseText(s string) bool {
	for _, loc := range riskSQL.FindAllStringIndex(s, -1) {
		match := strings.ToUpper(s[loc[0]:loc[1]])
		if !strings.HasPrefix(match, "DELETE") {
			return true
		}
		rest := s[loc[1]:]
		if end := strings.IndexAny(rest, ";\n"); end >= 0 {
			rest = rest[:end]
		}
		if !riskWhere.MatchString(rest) {
			return true
		}
	}
	return false
}

func riskFlag(args []string, long, short string) bool {
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == long || strings.HasPrefix(a, long+"=") {
			return true
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a[1:], short) {
			return true
		}
	}
	return false
}

func riskSafeRemove(s string) bool {
	if s == "" || strings.ContainsAny(s, "$`~") {
		return false
	}
	parts := strings.Split(strings.TrimRight(s, "/"), "/")
	for _, part := range parts {
		if part == ".." {
			return false
		}
	}
	if strings.HasPrefix(s, "/tmp/") {
		return true
	}
	if filepath.IsAbs(s) {
		return false
	}
	safeFolder := func(name string) bool {
		switch name {
		case "node_modules", "dist", "build", "out", "target", ".next", ".nuxt", ".cache", ".parcel-cache", ".turbo", "coverage", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", "tmp", ".tmp":
			return true
		}
		return false
	}
	for i, part := range parts {
		if i > 0 && strings.ContainsAny(part, "*?[") && safeFolder(parts[i-1]) {
			return true
		}
	}
	return safeFolder(parts[len(parts)-1])
}

func riskGitArgs(args []string) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		a := args[0]
		args = args[1:]
		if (a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" || a == "--namespace") && len(args) > 0 {
			args = args[1:]
		}
	}
	return args
}

var riskGraphQLMutation = regexp.MustCompile(`(?s)^\s*(?:#[^\n]*\n\s*)*mutation\b`)

func riskGitHubAPIWrite(args []string) bool {
	method, endpoint, query := "", "", ""
	payload := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, value, inline := strings.Cut(a, "=")
		kind := ""
		switch {
		case name == "--method" || strings.HasPrefix(a, "-X"):
			kind = "method"
			if strings.HasPrefix(a, "-X") && len(a) > 2 {
				value, inline = a[2:], true
			}
		case name == "--field" || name == "--raw-field" || strings.HasPrefix(a, "-f") || strings.HasPrefix(a, "-F"):
			kind = "field"
			if (strings.HasPrefix(a, "-f") || strings.HasPrefix(a, "-F")) && len(a) > 2 {
				value, inline = a[2:], true
			}
		case name == "--input":
			kind = "input"
		case name == "--hostname" || name == "--header" || name == "-H" || name == "--jq" || name == "-q" || name == "--template" || name == "-t" || name == "--cache":
			kind = "option"
		default:
			if endpoint == "" && !strings.HasPrefix(a, "-") {
				endpoint = strings.Trim(a, "/")
			}
			continue
		}
		if !inline && i+1 < len(args) {
			i++
			value = args[i]
		}
		switch kind {
		case "method":
			method = strings.ToUpper(value)
		case "input":
			payload = true
		case "field":
			payload = true
			if key, text, ok := strings.Cut(value, "="); ok && key == "query" {
				query = text
			}
		}
	}
	if method != "" {
		return method != "GET" && method != "HEAD" && method != "OPTIONS"
	}
	if endpoint == "graphql" {
		return riskGraphQLMutation.MatchString(query)
	}
	return payload
}

func riskKinds(c riskCommand) []risk {
	var out []risk
	add := func(key string) {
		for _, r := range out {
			if r.key == key {
				return
			}
		}
		out = append(out, risk{key, riskReasons[key]})
	}
	if len(c.words) == 0 {
		return nil
	}
	prog, args := riskProgram(c.words[0]), c.words[1:]
	if prog == "git" {
		args = riskGitArgs(args)
	}
	has := func(s string) bool {
		for _, a := range args {
			if a == s {
				return true
			}
		}
		return false
	}
	first := func(s string) bool { return len(args) > 0 && args[0] == s }
	if c.internet {
		add("run a script from the internet")
	}
	if c.system {
		add("system")
	}
	switch prog {
	case "rm":
		if riskFlag(args, "--recursive", "rR") {
			targets := 0
			safe := true
			flags := true
			for _, a := range args {
				if a == "--" && flags {
					flags = false
					continue
				}
				if flags && strings.HasPrefix(a, "-") {
					continue
				}
				targets++
				safe = safe && riskSafeRemove(a)
			}
			if targets == 0 || !safe {
				add("delete folders")
			}
		}
	case "find":
		if has("-delete") {
			add("delete folders")
		}
		for i, a := range args {
			if (a == "-exec" || a == "-execdir") && i+1 < len(args) && riskProgram(args[i+1]) == "rm" {
				add("delete folders")
			}
		}
	case "shred":
		add("delete folders")
	case "git":
		if first("clean") && riskFlag(args[1:], "--force", "f") {
			add("delete folders")
		}
		if (first("reset") && has("--hard")) || (first("checkout") && (has("--") || has("."))) || (first("restore") && (!has("--staged") && !riskFlag(args[1:], "--staged", "S") || riskFlag(args[1:], "--worktree", "W"))) || (first("stash") && len(args) > 1 && (args[1] == "drop" || args[1] == "clear")) || (first("branch") && riskFlag(args[1:], "--delete-force", "D")) {
			add("throw away changes")
		}
		if first("push") {
			force := riskFlag(args[1:], "--force", "f") || riskFlag(args[1:], "--force-with-lease", "") || riskFlag(args[1:], "--mirror", "")
			deleting := riskFlag(args[1:], "--delete", "d")
			for _, arg := range args[1:] {
				force = force || strings.HasPrefix(arg, "+")
				deleting = deleting || (strings.HasPrefix(arg, ":") && len(arg) > 1)
			}
			if force {
				add("force push")
			} else {
				add("git push")
			}
			if deleting {
				out[len(out)-1].why = "deletes a branch on the server"
			}
		}
	case "npm", "yarn", "pnpm", "bun", "cargo":
		if first("publish") {
			add("publish")
		}
	case "twine":
		if first("upload") {
			add("publish")
		}
	case "gem", "docker":
		if first("push") {
			add("publish")
		}
	case "gh":
		if first("api") && riskGitHubAPIWrite(args[1:]) {
			add("publish")
		}
		if len(args) > 1 {
			allowed := map[string]string{"pr": " create merge close comment review ", "issue": " create close comment edit ", "release": " create delete ", "repo": " create delete edit ", "workflow": " run "}
			if strings.Contains(allowed[args[0]], " "+args[1]+" ") {
				add("publish")
			}
		}
	case "vercel":
		if first("deploy") || has("--prod") || len(args) == 0 {
			add("publish")
		}
	case "netlify", "fly", "firebase":
		if first("deploy") {
			add("publish")
		}
	case "kubectl":
		if len(args) > 0 && strings.Contains(" apply delete create replace patch ", " "+args[0]+" ") {
			add("publish")
		}
	case "terraform":
		if first("apply") || first("destroy") {
			add("publish")
		}
	case "helm":
		if first("install") || first("upgrade") || first("uninstall") {
			add("publish")
		}
	case "ssh", "scp", "sftp":
		add("remote computer")
	case "rsync":
		for _, a := range args {
			if !strings.HasPrefix(a, "-") && strings.Contains(a, ":") && !strings.HasPrefix(a, "/") && !strings.HasPrefix(a, "./") && !strings.HasPrefix(a, "../") {
				add("remote computer")
			}
		}
	case "dropdb":
		add("database delete")
	case "redis-cli":
		for _, a := range args {
			if strings.EqualFold(a, "flushall") || strings.EqualFold(a, "flushdb") {
				add("database delete")
			}
		}
	case "prisma":
		if first("migrate") && len(args) > 1 && args[1] == "reset" {
			add("database delete")
		}
	case "rails":
		if has("db:drop") || has("db:reset") {
			add("database delete")
		}
	case "dd":
		for _, a := range args {
			if strings.HasPrefix(a, "of=") {
				add("system")
			}
		}
	case "shutdown", "reboot", "halt", "poweroff", "killall":
		add("system")
	case "kill":
		if has("-9") && has("-1") {
			add("system")
		}
	case "chmod", "chown":
		if riskFlag(args, "--recursive", "R") {
			for _, a := range args {
				if strings.HasPrefix(a, "/") || strings.HasPrefix(a, "~") {
					add("system")
				}
			}
		}
	}
	if strings.HasPrefix(prog, "mkfs") {
		add("system")
	}
	if c.database {
		add("database delete")
	}
	return out
}

func commandRisks(command string) []risk { return (ruleSet{}).check(command).risks }

type ruleFile struct {
	path, from        string
	found, trusted    bool
	problem           string
	allow, ask, block []string
}
type ruleSet struct{ user, project ruleFile }

func readRulesFile(path, from string) (f ruleFile, hash string) {
	f = ruleFile{path: path, from: from}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	f.found = true
	if err != nil {
		f.problem = "could not read rules.json"
		return
	}
	if !info.Mode().IsRegular() {
		f.problem = "rules.json must be a plain file"
		return
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		f.problem = "could not read rules.json"
		return
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.problem = "rules.json must be a plain file"
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		f.problem = "could not read rules.json"
		return
	}
	if len(data) > 64<<10 {
		f.problem = "rules.json is too big (over 64 KB)"
		return
	}
	sum := sha256.Sum256(data)
	hash = hexenc.EncodeToString(sum[:])
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err != nil || fields == nil {
		f.problem = "rules.json must contain a list of rules in braces"
		return
	}
	for key := range fields {
		if key != "allow" && key != "ask" && key != "block" {
			f.problem = "unknown rule: " + key
			return
		}
	}
	parsed := make(map[string][]string)
	for _, key := range []string{"allow", "ask", "block"} {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			f.problem = key + " must be a list of words in quotes"
			return
		}
		for _, item := range items {
			var s string
			if json.Unmarshal(item, &s) != nil || strings.TrimSpace(s) == "" {
				f.problem = key + " needs a rule in quotes, not an empty line"
				return
			}
			parsed[key] = append(parsed[key], s)
		}
	}
	f.allow, f.ask, f.block = parsed["allow"], parsed["ask"], parsed["block"]
	return
}

func loadRules(dir string) ruleSet { r, _ := readRules(dir); return r }

func readRules(dir string) (r ruleSet, hash string) {
	r.user.from = "your settings"
	r.project.from = "this project"
	root := configDir()
	if root != "" {
		r.user, _ = readRulesFile(filepath.Join(root, "rules.json"), "your settings")
		r.user.trusted = r.user.problem == ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		r.project.problem = "could not find this folder"
		return
	}
	r.project, hash = readRulesFile(filepath.Join(abs, ".prism", "rules.json"), "this project")
	if root != "" && hash != "" && r.project.problem == "" {
		r.project.trusted = readHooksTrust(root)["rules:"+abs] == hash
	}
	return
}

func trustRules(dir string) (ruleSet, error) {
	hooksTrustMu.Lock()
	defer hooksTrustMu.Unlock()
	r, hash := readRules(dir)
	if r.project.problem != "" {
		return r, errors.New(r.project.problem)
	}
	if !r.project.found {
		return r, errors.New("no .prism/rules.json in this folder")
	}
	root := configDir()
	if root == "" {
		return r, errors.New("could not find your settings folder; allow rules stay off")
	}
	unlock, err := lockHooksTrust(root)
	if err != nil {
		return r, errors.New("could not save your rule choices")
	}
	defer unlock()
	entries := readHooksTrust(root)
	entries["rules:"+filepath.Dir(filepath.Dir(r.project.path))] = hash
	if err = writeHooksTrust(root, entries); err != nil {
		return r, errors.New("could not save your rule choices")
	}
	return loadRules(dir), nil
}

type check struct {
	block, ask string
	from       string
	risks      []risk
}

// Only '*' is special: unlike filepath.Match it also crosses spaces and '/'.
func riskGlob(pattern, text string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == text
	}
	if !strings.HasPrefix(text, parts[0]) {
		return false
	}
	text = text[len(parts[0]):]
	for _, part := range parts[1 : len(parts)-1] {
		at := strings.Index(text, part)
		if at < 0 {
			return false
		}
		text = text[at+len(part):]
	}
	return strings.HasSuffix(text, parts[len(parts)-1])
}

// check is the rules a turn started with (Agent.startRules), made stricter by the files as they
// are now: an edit or /rules off mid-turn can add a block or take an allow away, never the reverse.
func (r ruleSet) check(command string) check {
	c := r.checkRules(command)
	if r.project.path == "" {
		return c
	}
	now := loadRules(filepath.Dir(filepath.Dir(r.project.path))).checkRules(command)
	if c.block == "" && now.block != "" {
		c.block, c.ask, c.from = now.block, "", now.from
	}
	if c.block == "" && c.ask == "" && now.ask != "" {
		c.ask, c.from = now.ask, now.from
	}
	seen := map[string]bool{}
	for _, k := range c.risks {
		seen[k.key] = true
	}
	for _, k := range now.risks {
		if !seen[k.key] {
			c.risks = append(c.risks, k)
		}
	}
	return c
}

func (r ruleSet) checkRules(command string) check {
	var result check
	var order []risk
	uncovered := make(map[string]bool)
	seen := make(map[string]bool)
	commands := riskCommands(command)
	for _, c := range commands {
		allowed := false
		for fileIndex, f := range []ruleFile{r.user, r.project} {
			if f.problem != "" {
				continue
			}
			for _, pattern := range f.block {
				if result.block == "" && riskGlob(pattern, c.text) {
					result.block, result.from = pattern, f.from
				}
			}
			for _, pattern := range f.ask {
				if result.ask == "" && riskGlob(pattern, c.text) {
					result.ask = pattern
					if result.block == "" {
						result.from = f.from
					}
				}
			}
			if f.trusted || fileIndex == 0 {
				for _, pattern := range f.allow {
					allowed = allowed || riskGlob(pattern, c.text)
				}
			}
		}
		for _, kind := range riskKinds(c) {
			if !seen[kind.key] {
				order = append(order, kind)
				seen[kind.key] = true
			}
			if !allowed {
				uncovered[kind.key] = true
			}
		}
	}

	for _, kind := range order {
		if uncovered[kind.key] {
			result.risks = append(result.risks, kind)
			delete(uncovered, kind.key)
		}
	}
	if result.block != "" {
		result.ask = ""
	}
	return result
}

func rulesView(dir string, args []string) (title string, lines []string, footer string) {
	title = "rules"
	r := loadRules(dir)
	if len(args) > 0 {
		if len(args) != 1 || (args[0] != "trust" && args[0] != "off") {
			return title, []string{"use /rules, /rules trust, or /rules off"}, ""
		}
		if args[0] == "off" {
			if err := untrustRules(dir); err != nil {
				return title, []string{err.Error()}, ""
			}
			title = "rules off"
			r = loadRules(dir)
		} else {
			var err error
			r, err = trustRules(dir)
			if err != nil {
				return title, []string{err.Error()}, ""
			}
			if r.project.trusted {
				title = "rules on"
			}
		}
	}
	lines = []string{"prism asks before: deleting folders, throwing away changes, git push, publishing, scripts from the internet, other computers, database deletes, system commands"}
	if !r.user.found && !r.project.found && r.project.problem == "" {
		return title, append(lines, "add .prism/rules.json here or ~/.config/prism/rules.json everywhere.", `example: {"ask": ["docker *"]}`), ""
	}
	for _, f := range []ruleFile{r.user, r.project} {
		if f.problem != "" {
			lines = append(lines, f.from+": "+f.problem)
			continue
		}
		for _, group := range []struct {
			label    string
			patterns []string
		}{{"always allow", f.allow}, {"always ask", f.ask}, {"never allow", f.block}} {
			for _, pattern := range group.patterns {
				source := f.from
				if group.label == "always allow" && f.from == "this project" && !f.trusted {
					source += ", off until /rules trust"
					footer = "/rules trust turns on this project's allow rules"
				}
				lines = append(lines, fmt.Sprintf("%-12s  %s  (%s)", group.label, pattern, source))
			}
		}
	}
	return
}

func untrustRules(dir string) error {
	hooksTrustMu.Lock()
	defer hooksTrustMu.Unlock()
	root := configDir()
	if root == "" {
		return nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return errors.New("could not find this folder")
	}
	unlock, err := lockHooksTrust(root)
	if err != nil {
		return errors.New("could not turn allow rules off")
	}
	defer unlock()
	entries := readHooksTrust(root)
	delete(entries, "rules:"+abs)
	if writeHooksTrust(root, entries) != nil {
		return errors.New("could not turn allow rules off")
	}
	return nil
}
