package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	keepRecent     = 10
	pruneOver      = 600
	pruneKeepLines = 6
	rulesLimit     = 8000
	memoryLimit    = 4000
	keepSessions   = 50
)

const memoryPrompt = `You keep a short memory file for a coding assistant that works on one project. Below is the current memory and a log of the latest work.

Return the updated memory file: plain bullet lines ("- ..."), at most 25, only durable facts that will help in future sessions: what the project is, how it is laid out, how to run and test it, conventions, the user's stated preferences, and decisions made. Drop anything outdated or one-off. No headings, no commentary.
If nothing worth keeping changed, reply with exactly: UNCHANGED`

type store struct {
	dir string
	mu  sync.Mutex
	gen uint64 // bumped by forget(); a memory job started before the bump must not write
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

type usageDay struct {
	Day      string `json:"day"`
	Requests int    `json:"requests"`
	Tokens   int    `json:"tokens"`
}

var usageMu sync.Mutex

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
	return writeAtomic(s.path("memory.md"), []byte(strings.TrimSpace(text)+"\n"))
}

// setMemoryIf writes the memory only if no forget() happened since generation gen.
// It reports whether the write was made.
func (s *store) setMemoryIf(text string, gen uint64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != gen {
		return false, nil
	}
	return true, writeAtomic(s.path("memory.md"), []byte(strings.TrimSpace(text)+"\n"))
}

func (s *store) generation() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen
}

func (s *store) forget() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	if err := os.Remove(s.path("memory.md")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
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
	for _, old := range all[min(keep, len(all)):] {
		os.Remove(old.path)
	}
}

// sessions lists saved chats, newest first, decoding only the header fields.
// The returned sessions carry no Messages; resume loads the full file on demand.
func (s *store) sessions() []session {
	files, _ := filepath.Glob(s.path("sessions", "*.json"))
	var out []session
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var info sessionInfo
		if json.Unmarshal(data, &info) == nil && info.ID != "" {
			out = append(out, session{ID: info.ID, Title: info.Title, Updated: info.Updated})
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
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
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

func (a *Agent) prune() {
	if os.Getenv("PRISM_PRUNE") == "off" {
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
		if m := a.messages[i]; m.Role == "user" && !strings.HasPrefix(m.Content, summaryPrefix) {
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

func (a *Agent) memoryJob() *memoryJob {
	if a.store == nil || a.store.dir == "" || os.Getenv("PRISM_MEMORY") == "off" || !a.worthRemembering() {
		return nil
	}
	job := &memoryJob{api: a.api, journal: a.journal(), current: a.store.memory(), store: a.store, gen: a.store.generation()}
	a.memMark = len(a.messages)
	return job
}

func (j *memoryJob) run(ctx context.Context) (bool, error) {
	helper := &Agent{api: j.api, window: contextLimit}
	current := j.current
	if current == "" {
		current = "(empty)"
	}
	request := []Message{
		{Role: "system", Content: memoryPrompt},
		{Role: "user", Content: "CURRENT MEMORY:\n" + current + "\n\nLATEST WORK:\n" + j.journal},
	}
	reply, err := helper.complete(ctx, request, map[string]any{"tool_choice": "none", "max_tokens": 1200}, func(any) {})
	if err != nil {
		return false, err
	}
	text := strings.TrimSpace(reply.Content)
	if text == "" || strings.HasPrefix(text, "UNCHANGED") || text == strings.TrimSpace(j.current) {
		return false, nil
	}
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			kept = append(kept, "- "+strings.TrimSpace(line[2:]))
		}
	}
	if len(kept) == 0 {
		return false, nil
	}
	if len(kept) > 25 {
		kept = kept[:25]
	}
	// A /memory forget since this job started wins: don't resurrect the old facts.
	return j.store.setMemoryIf(strings.Join(kept, "\n"), j.gen)
}
