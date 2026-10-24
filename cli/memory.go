package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	keepRecent      = 10
	pruneOver       = 600
	pruneKeepLines  = 6
	rulesLimit      = 8000
	memoryLimit     = 4000
	keepSessions    = 50
	memoryMaxTokens = 1200
	maxOpenTabs     = 9
)

const memoryPrompt = `You keep a short memory file for a coding assistant that works on one project. Below is the current memory and a log of the latest work.

Return the updated memory file: one fact per line as a plain bullet ("- ..."), at most 25 lines, only durable facts that will help in future sessions: what the project is, how it is laid out, how to run and test it, conventions, the user's stated preferences, and decisions made. Drop anything outdated or one-off. No headings, no commentary.
If nothing worth keeping changed, reply with exactly: UNCHANGED`

type store struct {
	dir string
	mu  sync.Mutex
	gen uint64 // bumped by every memory write; a memory job started before the bump must not write

	// The numbered list last shown by /memory, and the generation it was shown at.
	// A numbered forget is refused when the memory changed since then.
	listed    []string
	listedGen uint64
	hasListed bool
}

// usageError is a mistake in how a /memory command was typed or a request that
// no longer fits the memory (as opposed to a disk failure).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usageErrorf(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

// isUsageError reports whether err came from a bad /memory command (show it as a
// note); any other error from memoryCommand is a real disk failure (show it as an error).
func isUsageError(err error) bool {
	var u *usageError
	return errors.As(err, &u)
}

type session struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Updated  time.Time `json:"updated"`
	Messages []Message `json:"messages"`
}

// sessionInfo is the listing view of a session file: no messages are decoded.
type sessionInfo struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Updated time.Time `json:"updated"`
}

// openTab is one tab of a multi-tab Prism window, saved per project so a
// restarted window can restore its tabs next to the sessions.
type openTab struct {
	Session string `json:"session"`
	Name    string `json:"name"`
	Active  bool   `json:"active"`
}

type usageDay struct {
	Day      string `json:"day"`
	Requests int    `json:"requests"`
	Tokens   int    `json:"tokens"`
}

var usageMu sync.Mutex

// tmpSeq makes writeAtomic's temp names unique across goroutines in one process.
var tmpSeq atomic.Uint64

func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "prism")
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func openStore(project string) *store {
	root := configDir()
	if root == "" {
		return &store{}
	}
	h := fnv.New32a()
	h.Write([]byte(project))
	name := strings.Trim(unsafeName.ReplaceAllString(filepath.Base(project), "-"), "-")
	return &store{dir: filepath.Join(root, "projects", fmt.Sprintf("%s-%08x", name, h.Sum32()))}
}

func (s *store) path(parts ...string) string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(append([]string{s.dir}, parts...)...)
}

func (s *store) memory() string {
	data, _ := os.ReadFile(s.path("memory.md"))
	return strings.TrimSpace(string(data))
}

func (s *store) setMemory(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer lockFile(s.path("memory.lock"))()
	s.gen++
	return writeAtomic(s.path("memory.md"), []byte(strings.TrimSpace(text)+"\n"))
}

// setMemoryIf writes the memory only if nothing wrote it since generation gen and
// the memory on disk (checked under the file lock, so another prism process counts)
// still equals base, the content the caller started from. It reports whether the
// write was made.
func (s *store) setMemoryIf(text string, gen uint64, base string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != gen {
		return false, nil
	}
	defer lockFile(s.path("memory.lock"))()
	if strings.TrimSpace(s.memory()) != strings.TrimSpace(base) {
		return false, nil
	}
	s.gen++
	return true, writeAtomic(s.path("memory.md"), []byte(strings.TrimSpace(text)+"\n"))
}

func (s *store) generation() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen
}

func (s *store) snapshot() (string, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memory(), s.gen
}

func (s *store) forget() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer lockFile(s.path("memory.lock"))()
	s.gen++
	if err := os.Remove(s.path("memory.md")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

var memoryBullet = regexp.MustCompile(`^(?:[-*•]\s+|[0-9]+[.)]\s+)`)

func memoryItems(text string) []string {
	var items []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(memoryBullet.ReplaceAllString(strings.TrimSpace(line), ""))
		if line != "" {
			items = append(items, line)
		}
	}
	return items
}

func (s *store) forgetItems(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, usageErrorf("use /memory forget <n> or /memory forget all")
	}
	if len(args) == 1 && args[0] == "all" {
		return nil, s.forget()
	}
	for _, arg := range args {
		if arg == "all" {
			return nil, usageErrorf("\"all\" can't be mixed with numbers")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer lockFile(s.path("memory.lock"))()
	text := strings.TrimSpace(s.memory())
	items := memoryItems(text)
	if s.hasListed && (s.listedGen != s.gen || !slices.Equal(s.listed, items)) {
		return nil, usageErrorf("memory just changed · run /memory again for the new numbers")
	}
	if len(items) == 0 {
		return nil, usageErrorf("nothing to forget yet")
	}
	selected := make(map[int]bool)
	for _, arg := range args {
		n, err := strconv.Atoi(arg)
		if err != nil || n < 1 || n > len(items) {
			return nil, usageErrorf("there's no memory number %s · /memory shows the list", arg)
		}
		selected[n-1] = true
	}
	// Remove only the selected item lines; headings, wrapped lines and blank
	// lines stay exactly as they were.
	var kept, removed []string
	i := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			kept = append(kept, line)
			continue
		}
		if selected[i] {
			removed = append(removed, items[i])
		} else {
			kept = append(kept, line)
		}
		i++
	}
	if err := writeAtomic(s.path("memory.md"), []byte(strings.TrimSpace(strings.Join(kept, "\n"))+"\n")); err != nil {
		return nil, err
	}
	s.gen++
	return removed, nil
}

// showList records the items being shown so a later numbered forget can tell
// whether the numbers still match.
func (s *store) showList() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := memoryItems(strings.TrimSpace(s.memory()))
	s.listed, s.listedGen, s.hasListed = items, s.gen, true
	return items
}

// memoryCommand runs /memory and /forget. Errors for which isUsageError is true
// are mistakes in the command (show as a note); any other error is a disk failure.
func (s *store) memoryCommand(fields []string) (string, []string, bool, error) {
	args := fields[1:]
	if fields[0] == "/memory" && len(args) > 0 {
		if args[0] != "forget" {
			return "", nil, false, usageErrorf("use /memory forget <n> or /memory forget all")
		}
		args = args[1:]
	}
	if len(args) > 0 {
		removed, err := s.forgetItems(args)
		if err != nil {
			return "", nil, false, err
		}
		title := "memory cleared for this folder"
		if len(removed) > 0 {
			title = "forgot: " + strings.Join(removed, " · ")
		}
		return title, []string{}, true, nil
	}
	var lines []string
	for i, item := range s.showList() {
		lines = append(lines, fmt.Sprintf("%d  %s", i+1, item))
	}
	if len(lines) == 0 {
		lines = []string{"nothing yet · prism saves what it learns after each task"}
	}
	lines = append(lines, "", "/memory forget 2 removes one · /memory forget 2 5 removes several · /memory forget all clears it")
	return "what prism remembers here", lines, false, nil
}

func writeAtomic(path string, data []byte) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.%d.tmp", path, os.Getpid(), tmpSeq.Add(1))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	// Durability before the rename: a crash must not leave a zero-length file.
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (s *store) saveSession(sess session) error {
	data, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	if err := writeAtomic(s.path("sessions", sess.ID+".json"), data); err != nil {
		return err
	}
	// A tiny sidecar so sessions() can list chats without reading every full file.
	hdr, err := json.Marshal(sessionInfo{ID: sess.ID, Title: sess.Title, Updated: sess.Updated})
	if err != nil {
		return err
	}
	if err := writeAtomic(s.path("sessions", sess.ID+".hdr"), hdr); err != nil {
		return err
	}
	s.pruneSessions(keepSessions)
	return nil
}

// pruneSessions keeps only the newest keep session files by modification time.
func (s *store) pruneSessions(keep int) {
	if s.dir == "" {
		return
	}
	files, _ := filepath.Glob(s.path("sessions", "*.json"))
	if len(files) <= keep {
		return
	}
	type stamped struct {
		path string
		mod  time.Time
	}
	var all []stamped
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			all = append(all, stamped{f, info.ModTime()})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mod.After(all[j].mod) })
	// A session listed as an open tab is never pruned, even past the keep limit.
	open := map[string]bool{}
	for _, t := range s.loadOpenTabs() {
		open[t.Session] = true
	}
	for _, old := range all[min(keep, len(all)):] {
		if open[strings.TrimSuffix(filepath.Base(old.path), ".json")] {
			continue
		}
		os.Remove(old.path)
		os.Remove(strings.TrimSuffix(old.path, ".json") + ".hdr")
	}
}

// sessions lists saved chats, newest first, decoding only the header fields.
// The returned sessions carry no Messages; resume loads the full file on demand.
func (s *store) sessions() []session {
	seen := map[string]bool{}
	var out []session
	add := func(info sessionInfo) {
		if info.ID == "" || seen[info.ID] {
			return
		}
		seen[info.ID] = true
		out = append(out, session{ID: info.ID, Title: info.Title, Updated: info.Updated})
	}
	hdrs, _ := filepath.Glob(s.path("sessions", "*.hdr"))
	for _, f := range hdrs {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var info sessionInfo
		if json.Unmarshal(data, &info) == nil {
			add(info)
		}
	}
	// Sessions saved before sidecars existed have only the full file.
	files, _ := filepath.Glob(s.path("sessions", "*.json"))
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".json")
		if seen[id] {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var info sessionInfo
		if json.Unmarshal(data, &info) == nil {
			add(info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// loadSession reads one full session file, messages included.
func (s *store) loadSession(id string) (session, error) {
	var sess session
	if s.dir == "" || id == "" {
		return sess, os.ErrNotExist
	}
	data, err := os.ReadFile(s.path("sessions", filepath.Base(id)+".json"))
	if err != nil {
		return sess, err
	}
	if err := json.Unmarshal(data, &sess); err != nil {
		return sess, err
	}
	if len(sess.Messages) < 2 {
		return sess, fmt.Errorf("session %s has no messages", id)
	}
	return sess, nil
}

// saveOpenTabs atomically writes the window's open tabs, per project, next to
// the sessions. A nil or dir-less store is a no-op.
func (s *store) saveOpenTabs(tabs []openTab) error {
	if s == nil || s.dir == "" {
		return nil
	}
	data, err := json.Marshal(tabs)
	if err != nil {
		return err
	}
	return writeAtomic(s.path("tabs.json"), data)
}

// loadOpenTabs reads the saved open tabs. Tabs whose session file no longer
// exists are skipped, and at most maxOpenTabs tabs are returned. A nil or
// dir-less store, a missing file, or corrupt JSON yields no tabs.
func (s *store) loadOpenTabs() []openTab {
	if s == nil || s.dir == "" {
		return nil
	}
	data, err := os.ReadFile(s.path("tabs.json"))
	if err != nil {
		return nil
	}
	var tabs []openTab
	if json.Unmarshal(data, &tabs) != nil {
		return nil
	}
	out := make([]openTab, 0, min(len(tabs), maxOpenTabs))
	for _, t := range tabs {
		if t.Session == "" {
			continue
		}
		if _, err := os.Stat(s.path("sessions", filepath.Base(t.Session)+".json")); err != nil {
			continue // session pruned or never saved; drop the tab
		}
		out = append(out, t)
		if len(out) >= maxOpenTabs {
			break
		}
	}
	return out
}

func usagePath() string {
	if root := configDir(); root != "" {
		return filepath.Join(root, "usage.json")
	}
	return ""
}

func todayUsage() usageDay {
	var u usageDay
	data, _ := os.ReadFile(usagePath())
	json.Unmarshal(data, &u)
	if u.Day != time.Now().Format("2006-01-02") {
		return usageDay{Day: time.Now().Format("2006-01-02")}
	}
	return u
}

// lockFile takes an exclusive advisory lock on path; the returned func releases it.
// Lock failures are ignored: the caller proceeds unlocked rather than losing the update.
func lockFile(path string) func() {
	if path == "" {
		return func() {}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return func() {}
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return func() {}
	}
	// Non-blocking: a holder suspended mid-update must not park this goroutine
	// forever (addUsage runs on every response, outside any cancellable context).
	// After a short wait, proceed unlocked rather than losing the update.
	fd := int(f.Fd())
	locked := false
	for i := 0; i < 20; i++ {
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			locked = true
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !locked {
		f.Close()
		return func() {}
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
}

func addUsage(tokens int) {
	usageMu.Lock()
	defer usageMu.Unlock()
	if p := usagePath(); p != "" {
		defer lockFile(p + ".lock")()
	}
	u := todayUsage()
	u.Requests++
	u.Tokens += tokens
	data, _ := json.Marshal(u)
	writeAtomic(usagePath(), data)
}

func projectRules(dir string) (string, string) {
	for _, name := range []string{"PRISM.md", "AGENTS.md", "CLAUDE.md"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil && strings.TrimSpace(string(data)) != "" {
			text := string(data)
			if len(text) > rulesLimit {
				text = text[:rulesLimit] + "\n… (cut)"
			}
			return name, strings.TrimSpace(text)
		}
	}
	return "", ""
}

// prune trims old tool outputs in one batch once the conversation passes a quarter of the
// window. Trimming changes earlier messages, which throws away the provider's prompt cache from
// that point on, so short tasks are never touched and long ones only now and then.
func (a *Agent) prune() {
	if os.Getenv("PRISM_PRUNE") == "off" {
		return
	}
	if a.window > 0 && estimate(a.messages) <= a.window/4 {
		return
	}
	for i := 1; i < len(a.messages)-keepRecent; i++ {
		m := &a.messages[i]
		if m.Role != "tool" || len(m.Content) <= pruneOver || strings.HasSuffix(m.Content, prunedNote) {
			continue
		}
		lines := strings.Split(m.Content, "\n")
		if len(lines) <= pruneKeepLines+1 {
			m.Content = m.Content[:pruneOver] + "…\n" + prunedNote
			continue
		}
		m.Content = strings.Join(lines[:pruneKeepLines], "\n") + fmt.Sprintf("\n… %d more lines", len(lines)-pruneKeepLines) + "\n" + prunedNote
	}
}

const prunedNote = "[older output trimmed to save space; run the tool again if you need it]"

func (a *Agent) latestRequest() string {
	for i := len(a.messages) - 1; i > 0; i-- {
		if m := a.messages[i]; m.Role == "user" && !strings.HasPrefix(m.Content, summaryPrefix) && !strings.HasPrefix(m.Content, hookRetryPrefix) {
			return m.Content
		}
	}
	return ""
}

const summaryPrefix = "Summary of the work so far"

func (a *Agent) saveSession() {
	if a.store == nil || len(a.messages) < 2 {
		return
	}
	title := ""
	for _, m := range a.messages[1:] {
		if m.Role == "user" && !strings.HasPrefix(m.Content, summaryPrefix) {
			title = m.Content
			break
		}
	}
	if title == "" {
		title = "continued chat"
	}
	if r := []rune(strings.Join(strings.Fields(title), " ")); len(r) > 70 {
		title = string(r[:69]) + "…"
	} else {
		title = string(r)
	}
	a.store.saveSession(session{ID: a.session, Title: title, Updated: time.Now(), Messages: a.messages})
}

// resume switches to an earlier chat. A header-only session (from sessions())
// is loaded from disk first; if that fails the current chat is left untouched.
func (a *Agent) resume(sess session) {
	if len(sess.Messages) == 0 && a.store != nil {
		full, err := a.store.loadSession(sess.ID)
		if err != nil {
			return
		}
		sess = full
	}
	if len(sess.Messages) == 0 {
		return
	}
	a.messages = append([]Message{{Role: "system", Content: a.systemPrompt()}}, sess.Messages[1:]...)
	a.session = sess.ID
	a.memMark = len(a.messages)
	a.tokens = estimate(a.messages)
}

func (a *Agent) worthRemembering() bool {
	for _, m := range a.messages[min(a.memMark, len(a.messages)):] {
		if len(m.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

func (a *Agent) journal() string {
	start := min(a.memMark, len(a.messages))
	var b strings.Builder
	for _, m := range a.messages[max(1, start):] {
		switch m.Role {
		case "user":
			fmt.Fprintf(&b, "USER: %s\n", clipText(m.Content, 1500))
		case "assistant":
			if strings.TrimSpace(m.Content) != "" {
				fmt.Fprintf(&b, "ASSISTANT: %s\n", clipText(m.Content, 1200))
			}
			for _, c := range m.ToolCalls {
				fmt.Fprintf(&b, "TOOL %s %s\n", c.Function.Name, clipText(c.Function.Arguments, 200))
			}
		case "tool":
			fmt.Fprintf(&b, "RESULT: %s\n", clipText(m.Content, 300))
		}
	}
	text := b.String()
	if len(text) > 14000 {
		text = "…" + text[len(text)-14000:]
	}
	return text
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

type memoryJob struct {
	api, journal, current string
	store                 *store
	gen                   uint64 // store generation when the job was created
}

// persists reports whether a memory write could be saved: the store has a
// directory, a file can be created in it, and it is kept somewhere: either the
// launcher says so (PRISM_MEMORY_KEEP=1, it mounts the persistent config
// volume) or we run outside a container. A throwaway container loses the file
// on exit, so the memory request is skipped there. When nothing can be saved,
// a memory update would be wasted work, so callers skip the job.
func (s *store) persists() bool {
	if s == nil || s.dir == "" {
		return false
	}
	if os.Getenv("PRISM_MEMORY_KEEP") != "1" {
		if _, err := os.Stat("/.dockerenv"); err == nil {
			return false
		}
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return false
	}
	f, err := os.CreateTemp(s.dir, ".probe-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

func (a *Agent) memoryJob() *memoryJob {
	if a.store == nil || a.store.dir == "" || os.Getenv("PRISM_MEMORY") == "off" || !a.worthRemembering() || !a.store.persists() {
		return nil
	}
	current, gen := a.store.snapshot()
	job := &memoryJob{api: a.api, journal: a.journal(), current: current, store: a.store, gen: gen}
	a.memMark = len(a.messages)
	return job
}

func (j *memoryJob) run(ctx context.Context) (bool, error) {
	// The helper's answer is a few bullets: ask the router for the cheap,
	// no-thinking route and the lowest reasoning effort.
	helper := &Agent{api: j.api, window: contextLimit, quick: true}
	current := j.current
	if current == "" {
		current = "(empty)"
	}
	request := []Message{
		{Role: "system", Content: memoryPrompt},
		{Role: "user", Content: "CURRENT MEMORY:\n" + current + "\n\nLATEST WORK:\n" + j.journal},
	}
	reply, err := helper.complete(ctx, request, map[string]any{"tool_choice": "none", "max_tokens": memoryMaxTokens, "reasoning_effort": "low"}, func(any) {})
	if err != nil {
		return false, err
	}
	text := strings.TrimSpace(reply.Content)
	if text == "" || strings.HasPrefix(text, "UNCHANGED") || text == strings.TrimSpace(j.current) {
		return false, nil
	}
	kept := memoryLines(text, memoryCutOff(reply))
	if len(kept) == 0 {
		return false, nil
	}
	if len(kept) > 25 {
		kept = kept[:25]
	}
	// A /memory forget since this job started wins: don't resurrect the old facts.
	return j.store.setMemoryIf(strings.Join(kept, "\n"), j.gen, j.current)
}

// memoryCutOff reports whether the helper's reply probably hit its token cap.
// complete() does not surface finish_reason, so a reply that fills nearly all
// of max_tokens (about 4 characters each) counts as cut off.
func memoryCutOff(reply Message) bool {
	return len(reply.Content) >= memoryMaxTokens*4*9/10
}

// memoryLines turns the model's reply into memory bullets and never returns a
// fragment: a bullet with unbalanced backticks is dropped, and the last bullet is
// also dropped when the reply was cut off or it visibly stops mid-thought
// (open quote or bracket, or ending on a colon, comma, dash or connecting word).
func memoryLines(text string, cutOff bool) []string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		var body string
		switch {
		case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "), strings.HasPrefix(line, "• "):
			_, body, _ = strings.Cut(line, " ")
		default:
			continue
		}
		body = strings.TrimSpace(body)
		if body == "" || strings.Count(body, "`")%2 != 0 {
			continue
		}
		kept = append(kept, "- "+body)
	}
	if n := len(kept); n > 0 && (cutOff || looksUnfinished(kept[n-1][2:])) {
		kept = kept[:n-1]
	}
	return kept
}

var danglingWords = map[string]bool{"a": true, "an": true, "and": true, "as": true, "at": true, "by": true, "for": true, "from": true, "in": true, "of": true, "on": true, "or": true, "the": true, "to": true, "with": true, "via": true, "using": true, "is": true, "are": true}

// looksUnfinished reports whether a bullet reads like it was cut off.
func looksUnfinished(body string) bool {
	body = strings.TrimSpace(body)
	if strings.Count(body, "`")%2 != 0 || strings.Count(body, `"`)%2 != 0 {
		return true
	}
	if strings.Count(body, "(") > strings.Count(body, ")") || strings.Count(body, "[") > strings.Count(body, "]") || strings.Count(body, "{") > strings.Count(body, "}") {
		return true
	}
	if body == "" || strings.ContainsAny(body[len(body)-1:], ":,;-–—(=") {
		return true
	}
	last := strings.ToLower(body[strings.LastIndexAny(body, " \t")+1:])
	return danglingWords[last]
}
