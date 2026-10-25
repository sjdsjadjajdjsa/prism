package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

type diffLine struct {
	kind byte
	num  int
	text string
}

type toolResult struct {
	output         string
	summary        string
	ok             bool
	added, removed int
	diff           []diffLine
}

func spec(name, description string, props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        name,
			"description": description,
			"parameters":  map[string]any{"type": "object", "properties": props, "required": required},
		},
	}
}

func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func integer(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

var toolSpecs = []any{
	browseSpec,
	webSpec,
	spec("screenshot", "Screenshot a web page, project HTML file or (host system mode only, asks every time) the desktop, and ask a free vision model what it shows. Give exactly one of url, file or desktop. If no vision model was available, say so; never describe a screenshot you could not see.",
		map[string]any{"url": str("localhost or private-network address; public ones need approval"), "file": str("Project .html file"), "desktop": map[string]any{"type": "boolean", "description": "whole desktop"}, "width": integer("default 1280, max 3840"), "height": integer("default 800, max 2160"), "full_page": map[string]any{"type": "boolean", "description": "whole scrollable page"}, "question": str("what to check, e.g. does the layout break")}),
	spec("list", "List files and folders in the project, up to 4 levels deep.",
		map[string]any{"path": str("Folder to list, relative to the project. Defaults to the project root.")}),
	spec("grep", "Search file contents with a regular expression. Returns matching lines with file and line number.",
		map[string]any{"pattern": str("Regular expression to search for"), "path": str("File or folder to search. Defaults to the project root.")}, "pattern"),
	spec("read", "Read a text file. Returns numbered lines. A long file read without offset/limit starts with an outline of its symbols.",
		map[string]any{"path": str("File path, relative to the project"), "offset": integer("First line to read, starting at 1"), "limit": integer("How many lines to read (default 500)")}, "path"),
	spec("edit", "Replace an exact piece of text in a file. old_string must match the file exactly, including spaces, and be unique unless replace_all is true.",
		map[string]any{"path": str("File path, relative to the project"), "old_string": str("Exact text to replace"), "new_string": str("Text to put in its place"), "replace_all": map[string]any{"type": "boolean", "description": "Replace every match"}}, "path", "old_string", "new_string"),
	spec("write", "Create a file or replace its whole content. Creates missing folders.",
		map[string]any{"path": str("File path, relative to the project"), "content": str("The complete file content")}, "path", "content"),
	spec("bash", "Run a shell command in the project folder and return its output. Commands cannot read keyboard input.",
		map[string]any{"command": str("The command to run"), "timeout": integer("Seconds before the command is stopped (default 120, max 600)")}, "command"),
}

func runTool(ctx context.Context, dir, name, raw string) toolResult {
	args := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			return fail("the tool arguments were not valid JSON: " + err.Error())
		}
	}
	switch name {
	case "list":
		return listTool(dir, args)
	case "grep":
		return grepTool(ctx, dir, args)
	case "read":
		return readTool(dir, args)
	case "edit":
		return editTool(dir, args)
	case "write":
		return writeTool(dir, args)
	case "bash":
		return bashTool(ctx, dir, args)
	case "web":
		return webTool(ctx, raw)
	case "browse":
		return browseTool(ctx, screenshotFallbackEnv(dir), raw)
	case "screenshot":
		// The agent loop routes screenshot to (*Agent).screenshot so it keeps its system flag and approve
		// callback. Reaching this line means there is no agent: no approvals, so no desktop or public URLs.
		return screenshotTool(ctx, screenshotFallbackEnv(dir), raw)
	}
	return fail("unknown tool " + name + ". Use list, grep, read, edit, write, bash or screenshot.")
}

func fail(message string) toolResult {
	return toolResult{output: "Error: " + message, summary: message}
}

func argsOf(raw string) map[string]any {
	args := map[string]any{}
	json.Unmarshal([]byte(raw), &args)
	return args
}

func sarg(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

func iarg(args map[string]any, key string, fallback int) int {
	switch v := args[key].(type) {
	case float64:
		if v > 0 {
			return int(v)
		}
	case string:
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func describe(raw string) string {
	args := map[string]any{}
	json.Unmarshal([]byte(raw), &args)
	for _, key := range []string{"path", "command", "pattern", "name", "url", "query"} {
		if s := sarg(args, key); s != "" {
			first, _, _ := strings.Cut(s, "\n")
			return first
		}
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := args[k].(string); ok && s != "" {
			first, _, _ := strings.Cut(s, "\n")
			return first
		}
	}
	return ""
}

var anyPath bool

func resolve(dir, path string) string {
	if path == "" {
		return dir
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(dir, path)
}

func resolvedDir(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return dir
}

// resolveDeepest resolves symlinks on the deepest existing ancestor of path,
// so it also works for a file that doesn't exist yet.
func resolveDeepest(path string) string {

	for p := path; ; {
		if r, err := filepath.EvalSymlinks(p); err == nil {

			suffix := path[len(p):]
			return filepath.Clean(r + suffix)
		}
		parent := filepath.Dir(p)
		if parent == p {
			// Hit the root without finding anything that exists.
			return path
		}
		p = parent
	}
}

func outside(dir, path string) bool {
	if anyPath {
		return false
	}
	rDir := resolvedDir(dir)
	rPath := resolveDeepest(path)
	rel, err := filepath.Rel(rDir, rPath)
	return err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

const outsideMsg = "that path is outside the project folder. Prism can only use files inside it"

func relative(dir, path string) string {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return rel
}

func plainError(dir string, err error) string {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return relative(dir, pathErr.Path) + ": " + pathErr.Err.Error()
	}
	return err.Error()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, ".venv": true, "venv": true, "__pycache__": true, ".next": true, "dist": true, "target": true}

func listTool(dir string, args map[string]any) toolResult {
	root := resolve(dir, sarg(args, "path"))
	if outside(dir, root) {
		return fail(outsideMsg)
	}
	info, err := os.Stat(root)
	if err != nil {
		return fail(plainError(dir, err))
	}
	if !info.IsDir() {
		return toolResult{ok: true, output: relative(dir, root), summary: "1 file"}
	}
	var out []string
	more := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == root {
			return nil
		}
		rel := relative(root, path)
		depth := strings.Count(rel, string(filepath.Separator))
		if d.IsDir() && (skipDirs[d.Name()] || depth >= 3) {
			if len(out) < 400 {
				out = append(out, rel+"/")
			}
			return filepath.SkipDir
		}
		if len(out) >= 400 {
			more++
			return nil
		}
		if d.IsDir() {
			rel += "/"
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return fail(plainError(dir, err))
	}
	if len(out) == 0 {
		return toolResult{ok: true, output: "The folder is empty.", summary: "empty"}
	}
	text := strings.Join(out, "\n")
	if more > 0 {
		text += fmt.Sprintf("\n… and %d more", more)
	}
	return toolResult{ok: true, output: text, summary: fmt.Sprintf("%d entries", len(out)+more)}
}

func grepTool(ctx context.Context, dir string, args map[string]any) toolResult {
	pattern := sarg(args, "pattern")
	if pattern == "" {
		return fail("pattern is empty")
	}
	target := resolve(dir, sarg(args, "path"))
	if outside(dir, target) {
		return fail(outsideMsg)
	}
	var cmd *exec.Cmd
	perFile := 0
	if _, err := exec.LookPath("rg"); err == nil {
		cmd = exec.CommandContext(ctx, "rg", "--line-number", "--no-heading", "--color=never", "--max-count=50", "--max-columns=300", "--max-columns-preview", "--", pattern, target)
	} else {
		cmd = exec.CommandContext(ctx, "grep", "-rnE", "--exclude-dir=.git", "--exclude-dir=node_modules", "--", pattern, target)
		perFile = grepPerFile
	}
	cmd.Dir = dir
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return toolResult{ok: true, output: "No matches.", summary: "no matches"}
	}
	if err != nil {
		msg := err.Error()
		if exitErr != nil && len(exitErr.Stderr) > 0 {
			msg = strings.TrimSpace(string(exitErr.Stderr))
		}
		return fail("search failed: " + msg)
	}
	lines := splitLines(strings.ReplaceAll(string(out), dir+"/", ""))
	total := len(lines)
	lines = capGrep(lines, perFile)
	return toolResult{ok: true, output: strings.Join(lines, "\n"), summary: fmt.Sprintf("%d matches", total)}
}

const (
	grepMaxLines = 100
	grepMaxCols  = 160
	grepPerFile  = 20
)

var grepPrefix = regexp.MustCompile(`^(.*?):\d+:`)

// capGrep keeps at most perFile matches per file (0 = no per-file cap),
// grepMaxLines lines in total and grepMaxCols runes per line, and ends with a
// note saying how many matches were dropped.
func capGrep(lines []string, perFile int) []string {
	var kept []string
	perCount := map[string]int{}
	dropped := 0
	for _, line := range lines {
		if perFile > 0 {
			if m := grepPrefix.FindStringSubmatch(line); m != nil {
				perCount[m[1]]++
				if perCount[m[1]] > perFile {
					dropped++
					continue
				}
			}
		}
		if len(kept) >= grepMaxLines {
			dropped++
			continue
		}
		kept = append(kept, clipRunes(line, grepMaxCols))
	}
	if dropped > 0 {
		kept = append(kept, fmt.Sprintf("… %d more matches not shown. Narrow the search.", dropped))
	}
	return kept
}

func clipRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i] + "…"
		}
		count++
	}
	return s
}

const (
	readDefaultLimit = 500
	outlineMax       = 60
)

var outlineRe = regexp.MustCompile(`^ {0,4}(?:async def|def|class|func|function|export function|export class|type|interface)\s`)

func outline(lines []string) string {
	var b strings.Builder
	n := 0
	for i, line := range lines {
		if !outlineRe.MatchString(line) {
			continue
		}
		n++
		if n <= outlineMax {
			fmt.Fprintf(&b, "%6d\t%s\n", i+1, clipRunes(strings.TrimRight(line, " \t{:"), grepMaxCols))
		}
	}
	if n == 0 {
		return ""
	}
	head := fmt.Sprintf("Outline (%d lines in file, %d symbols):\n", len(lines), n)
	if n > outlineMax {
		fmt.Fprintf(&b, "… %d more symbols\n", n-outlineMax)
	}
	return head + b.String() + "\n"
}

func readTool(dir string, args map[string]any) toolResult {
	path := resolve(dir, sarg(args, "path"))
	if outside(dir, path) {
		return fail(outsideMsg)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fail(plainError(dir, err))
	}
	switch {
	case info.IsDir():
		return fail(relative(dir, path) + " is a folder. Use list to see what's inside")
	case !info.Mode().IsRegular():
		return fail(relative(dir, path) + " is not a normal file")
	case info.Size() > 8<<20:
		return fail(fmt.Sprintf("%s is too big to read (%d MB). Use grep or bash with head/tail instead", relative(dir, path), info.Size()>>20))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fail(plainError(dir, err))
	}
	if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		return fail(relative(dir, path) + " is a binary file")
	}
	lines := splitLines(string(data))
	if len(lines) == 0 {
		return toolResult{ok: true, output: "The file is empty.", summary: "empty"}
	}
	start := iarg(args, "offset", 1) - 1
	if start >= len(lines) {
		return fail(fmt.Sprintf("offset %d is past the end: the file has %d lines", start+1, len(lines)))
	}
	end := min(len(lines), start+iarg(args, "limit", readDefaultLimit))
	var b strings.Builder
	_, hasOffset := args["offset"]
	_, hasLimit := args["limit"]
	if !hasOffset && !hasLimit && len(lines) > readDefaultLimit {
		b.WriteString(outline(lines))
	}
	for i := start; i < end; i++ {
		line := lines[i]
		if len(line) > 2000 {
			line = line[:2000] + "…"
		}
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, line)
	}
	if end < len(lines) {
		fmt.Fprintf(&b, "… %d more lines. Read again with offset=%d to continue.\n", len(lines)-end, end+1)
	}
	return toolResult{ok: true, output: b.String(), summary: fmt.Sprintf("%d lines", end-start)}
}

// writeToolWithUndo adds file claims and undo to writeTool. A cross-tab
// ownership warning goes in front of the output.
func (a *Agent) writeToolWithUndo(args map[string]any) toolResult {
	// Claim the symlink-resolved path so two paths to the same file collide.
	path := resolveDeepest(resolve(a.dir, sarg(args, "path")))
	// Record before the write, outside the lock (see recordBefore).
	if a.undoStack != nil {
		a.undoStack.recordBefore(a.currentTurn, path)
	}
	result := writeTool(a.dir, args)
	if result.ok {
		// Claim only after a successful write. A contested claim only warns.
		if warn := a.claimFile(path); warn != "" {
			result.output = "Warning: " + warn + "\n" + result.output
		}
		if a.undoStack != nil {
			a.undoStack.recordAfter(a.currentTurn, path)
		}
	}
	return result
}

func (a *Agent) editToolWithUndo(args map[string]any) toolResult {

	path := resolveDeepest(resolve(a.dir, sarg(args, "path")))

	if a.undoStack != nil {
		a.undoStack.recordBefore(a.currentTurn, path)
	}
	result := editTool(a.dir, args)
	if result.ok {

		if warn := a.claimFile(path); warn != "" {
			result.output = "Warning: " + warn + "\n" + result.output
		}
		if a.undoStack != nil {
			a.undoStack.recordAfter(a.currentTurn, path)
		}
	}
	return result
}

func writeTool(dir string, args map[string]any) toolResult {
	path := resolve(dir, sarg(args, "path"))
	if outside(dir, path) {
		return fail(outsideMsg)
	}
	content := sarg(args, "content")
	old, readErr := os.ReadFile(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fail(plainError(dir, err))
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fail(plainError(dir, err))
	}
	result := changed(string(old), content)
	result.output = fmt.Sprintf("Wrote %s (%d lines).", relative(dir, path), len(splitLines(content)))
	if readErr != nil {
		result.summary = "new file"
	}
	return result
}

func editTool(dir string, args map[string]any) toolResult {
	path := resolve(dir, sarg(args, "path"))
	if outside(dir, path) {
		return fail(outsideMsg)
	}
	oldText, newText := sarg(args, "old_string"), sarg(args, "new_string")
	if oldText == "" {
		return fail("old_string is empty. Use write to create a file.")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fail(plainError(dir, err))
	}
	text := string(data)
	count := strings.Count(text, oldText)
	if count == 0 && strings.Contains(text, "\r\n") && !strings.Contains(oldText, "\r\n") {
		crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
		if c := strings.Count(text, crlf(oldText)); c > 0 {
			oldText, newText, count = crlf(oldText), crlf(newText), c
		}
	}
	replaceAll, _ := args["replace_all"].(bool)
	switch {
	case count == 0:
		return fail("old_string was not found. Read the file again and copy the exact text, including spaces.")
	case count > 1 && !replaceAll:
		return fail(fmt.Sprintf("old_string appears %d times. Include more surrounding lines so it is unique, or set replace_all.", count))
	}
	n := 1
	if replaceAll {
		n = -1
	}
	updated := strings.Replace(text, oldText, newText, n)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return fail(plainError(dir, err))
	}
	result := changed(text, updated)
	result.output = fmt.Sprintf("Edited %s.", relative(dir, path))
	return result
}

func changed(before, after string) toolResult {
	a, b := splitLines(before), splitLines(after)
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var diff []diffLine
	for i := max(0, pre-1); i < pre; i++ {
		diff = append(diff, diffLine{' ', i + 1, a[i]})
	}
	for i := pre; i < len(a)-suf; i++ {
		diff = append(diff, diffLine{'-', i + 1, a[i]})
	}
	for i := pre; i < len(b)-suf; i++ {
		diff = append(diff, diffLine{'+', i + 1, b[i]})
	}
	if j := len(b) - suf; suf > 0 {
		diff = append(diff, diffLine{' ', j + 1, b[j]})
	}
	if before != after && len(a)-suf-pre == 0 && len(b)-suf-pre == 0 && len(b) > 0 {
		last := len(b) - 1
		diff = append(diff[:0], diffLine{'-', last + 1, a[min(last, len(a)-1)]}, diffLine{'+', last + 1, b[last]})
		return toolResult{ok: true, added: 1, removed: 1, diff: diff, summary: "+1 -1"}
	}
	added, removed := len(b)-suf-pre, len(a)-suf-pre
	return toolResult{ok: true, added: added, removed: removed, diff: diff, summary: fmt.Sprintf("+%d -%d", added, removed)}
}

// tailBuffer keeps roughly the last limit bytes written to it. Write runs on
// the process's pipe goroutine while tail is read from elsewhere, so both
// take the mutex.
type tailBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.data = append(t.data, p...)
	if len(t.data) > 2*t.limit {
		t.data = append([]byte(nil), t.data[len(t.data)-t.limit:]...)
	}
	return len(p), nil
}

// tail cuts on a rune boundary and marks the cut with "…".
func (t *tailBuffer) tail() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.data) <= t.limit {
		return string(t.data)
	}
	start := len(t.data) - t.limit
	for start < len(t.data) && !utf8.RuneStart(t.data[start]) {
		start++
	}
	return "…" + string(t.data[start:])
}

// headTailBuffer keeps the first head bytes and roughly the last tail bytes
// written to it, counting everything in between as omitted.
type headTailBuffer struct {
	mu         sync.Mutex
	head, tail []byte
	headLimit  int
	tailLimit  int
	total      int
}

func (h *headTailBuffer) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.total += len(p)
	rest := p
	if room := h.headLimit - len(h.head); room > 0 {
		n := min(room, len(rest))
		h.head = append(h.head, rest[:n]...)
		rest = rest[n:]
	}
	h.tail = append(h.tail, rest...)
	if len(h.tail) > 2*h.tailLimit {
		h.tail = append([]byte(nil), h.tail[len(h.tail)-h.tailLimit:]...)
	}
	return len(p), nil
}

// String cuts on rune boundaries and says how many middle bytes were left out.
func (h *headTailBuffer) String() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.total <= h.headLimit+h.tailLimit {
		return string(h.head) + string(h.tail)
	}
	head := h.head
	// Drop a rune cut in half at the end of the head.
	s := len(head)
	for s > 0 && len(head)-s < utf8.UTFMax && !utf8.RuneStart(head[s-1]) {
		s--
	}
	if s > 0 && !utf8.FullRune(head[s-1:]) {
		head = head[:s-1]
	}
	start := max(0, len(h.tail)-h.tailLimit)
	for start < len(h.tail) && !utf8.RuneStart(h.tail[start]) {
		start++
	}
	tail := h.tail[start:]
	omitted := h.total - len(head) - len(tail)
	return fmt.Sprintf("%s\n… [%d bytes omitted] …\n%s", head, omitted, tail)
}

func bashTool(ctx context.Context, dir string, args map[string]any) toolResult {
	command := sarg(args, "command")
	if strings.TrimSpace(command) == "" {
		return fail("command is empty")
	}
	timeout := time.Duration(min(iarg(args, "timeout", 120), 600)) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "bash", "-c", command)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	out := &headTailBuffer{headLimit: 3000, tailLimit: 9000}
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	leftover := false
	if errors.Is(err, exec.ErrWaitDelay) {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		leftover, err = true, nil
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() > 0 {
			err = &exec.ExitError{ProcessState: cmd.ProcessState}
		}
	} else if err == nil && cmd.Process != nil {
		// Kill any background processes that redirected their output and let
		// the shell exit cleanly (the pipe was already closed, so WaitDelay
		// never fires). ESRCH just means the group is already gone.
		if kerr := syscall.Kill(-cmd.Process.Pid, 0); kerr == nil {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			leftover = true
		}
	}
	text := out.String()
	code := 0
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	case err != nil:
		return fail(err.Error())
	}
	note := fmt.Sprintf("[exit code %d]", code)
	switch {
	case ctx.Err() != nil:
		note = "[stopped by the user]"
	case runCtx.Err() != nil:
		note = fmt.Sprintf("[stopped after %s timeout]", timeout)
	}
	if leftover {
		note += " [a background process was stopped: commands can't keep running after they finish, so don't start servers]"
	}
	summary := note
	if lines := splitLines(strings.TrimSpace(text)); len(lines) > 0 {
		summary = "→ " + lines[len(lines)-1]
	}
	if code != 0 && runCtx.Err() == nil {
		summary = fmt.Sprintf("exit %d · %s", code, strings.TrimPrefix(summary, "→ "))
	}
	return toolResult{ok: code == 0 && runCtx.Err() == nil, output: strings.TrimRight(text, "\n") + "\n" + note, summary: summary}
}
