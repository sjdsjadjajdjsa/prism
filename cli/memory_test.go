package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// ---- helpers ----------------------------------------------------------------

func tempStore(t *testing.T, project string) *store {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	// On Linux os.UserConfigDir() uses XDG_CONFIG_HOME; on macOS it uses HOME/Library.
	// Force HOME too so macOS falls back correctly.
	t.Setenv("HOME", dir)
	return openStore(project)
}

// ---- openStore --------------------------------------------------------------

func TestOpenStorePaths(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	s1 := openStore("/home/user/projectA")
	s2 := openStore("/home/user/projectB")
	s3 := openStore("/home/user/projectA")

	if s1.dir == "" {
		t.Fatal("store dir should not be empty")
	}
	if s1.dir == s2.dir {
		t.Errorf("different projects should have different store dirs: both %q", s1.dir)
	}
	if s1.dir != s3.dir {
		t.Errorf("same project should have same store dir: %q vs %q", s1.dir, s3.dir)
	}
	// dir should be under the config root
	if !strings.Contains(s1.dir, "prism") {
		t.Errorf("store dir %q should be under prism config dir", s1.dir)
	}
}

// ---- setMemory / memory / forget --------------------------------------------

func TestSetMemoryAndRead(t *testing.T) {
	s := tempStore(t, "/proj/alpha")

	if got := s.memory(); got != "" {
		t.Errorf("fresh store memory should be empty, got %q", got)
	}

	if err := s.setMemory("- fact one\n- fact two"); err != nil {
		t.Fatalf("setMemory: %v", err)
	}
	got := s.memory()
	if !strings.Contains(got, "fact one") {
		t.Errorf("memory() should contain written text, got %q", got)
	}
}

func TestForgetClearsMemory(t *testing.T) {
	s := tempStore(t, "/proj/beta")

	if err := s.setMemory("- remember this"); err != nil {
		t.Fatalf("setMemory: %v", err)
	}
	if err := s.forget(); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if got := s.memory(); got != "" {
		t.Errorf("after forget memory should be empty, got %q", got)
	}
}

// ---- generation counter blocks stale write ----------------------------------

func TestGenerationBlocksStaleWrite(t *testing.T) {
	s := tempStore(t, "/proj/gamma")

	gen := s.generation()

	// Simulate a memory job that captured gen before a forget.
	if err := s.forget(); err != nil {
		t.Fatalf("forget: %v", err)
	}

	// setMemoryIf with the old generation must be rejected.
	wrote, err := s.setMemoryIf("- stale fact", gen)
	if err != nil {
		t.Fatalf("setMemoryIf: %v", err)
	}
	if wrote {
		t.Error("setMemoryIf should not write when generation has advanced")
	}
	if got := s.memory(); got != "" {
		t.Errorf("memory should still be empty after rejected write, got %q", got)
	}

	// setMemoryIf with the current generation must succeed.
	newGen := s.generation()
	wrote, err = s.setMemoryIf("- fresh fact", newGen)
	if err != nil {
		t.Fatalf("setMemoryIf current gen: %v", err)
	}
	if !wrote {
		t.Error("setMemoryIf should write when generation matches")
	}
	if got := s.memory(); !strings.Contains(got, "fresh fact") {
		t.Errorf("memory should contain fresh fact, got %q", got)
	}
}

// ---- saveSession + sessions() header listing + loadSession ------------------

func TestSaveAndLoadSession(t *testing.T) {
	s := tempStore(t, "/proj/delta")

	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello world"},
		{Role: "assistant", Content: "hi there"},
	}
	sess := session{
		ID:       "test-session-001",
		Title:    "hello world",
		Updated:  time.Now(),
		Messages: msgs,
	}
	if err := s.saveSession(sess); err != nil {
		t.Fatalf("saveSession: %v", err)
	}

	// sessions() should list it with header fields only.
	list := s.sessions()
	if len(list) != 1 {
		t.Fatalf("sessions() returned %d items, want 1", len(list))
	}
	if list[0].ID != "test-session-001" {
		t.Errorf("session ID mismatch: got %q", list[0].ID)
	}
	if list[0].Title != "hello world" {
		t.Errorf("session Title mismatch: got %q", list[0].Title)
	}
	if len(list[0].Messages) != 0 {
		t.Errorf("sessions() header should carry no messages, got %d", len(list[0].Messages))
	}

	// loadSession should return full messages.
	full, err := s.loadSession("test-session-001")
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	if len(full.Messages) != len(msgs) {
		t.Errorf("loadSession messages count: got %d, want %d", len(full.Messages), len(msgs))
	}
	if full.Messages[1].Content != "hello world" {
		t.Errorf("loadSession message content mismatch: %q", full.Messages[1].Content)
	}
}

func TestSessionsNewestFirst(t *testing.T) {
	s := tempStore(t, "/proj/epsilon")

	base := time.Now()
	for i := 0; i < 3; i++ {
		sess := session{
			ID:      fmt.Sprintf("sess-%02d", i),
			Title:   fmt.Sprintf("session %d", i),
			Updated: base.Add(time.Duration(i) * time.Second),
			Messages: []Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: fmt.Sprintf("msg %d", i)},
			},
		}
		if err := s.saveSession(sess); err != nil {
			t.Fatalf("saveSession %d: %v", i, err)
		}
		// Small sleep to ensure distinct mtime on filesystems with 1s resolution.
		time.Sleep(5 * time.Millisecond)
	}

	list := s.sessions()
	if len(list) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(list))
	}
	// Newest (sess-02) should be first.
	if list[0].ID != "sess-02" {
		t.Errorf("expected newest session first, got %q", list[0].ID)
	}
}

// ---- pruning keeps newest 50 ------------------------------------------------

func TestPruneSessionsKeepsNewest50(t *testing.T) {
	s := tempStore(t, "/proj/zeta")

	base := time.Now()
	for i := 0; i < 60; i++ {
		sess := session{
			ID:      fmt.Sprintf("sess-%03d", i),
			Title:   fmt.Sprintf("session %d", i),
			Updated: base.Add(time.Duration(i) * time.Millisecond),
			Messages: []Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: fmt.Sprintf("msg %d", i)},
			},
		}
		data, _ := json.Marshal(sess)
		path := s.path("sessions", fmt.Sprintf("sess-%03d.json", i))
		if err := writeAtomic(path, data); err != nil {
			t.Fatalf("writeAtomic: %v", err)
		}
		// Touch with distinct mtime.
		ts := base.Add(time.Duration(i) * time.Millisecond)
		os.Chtimes(path, ts, ts)
	}

	s.pruneSessions(keepSessions)

	files, _ := filepath.Glob(s.path("sessions", "*.json"))
	if len(files) != keepSessions {
		t.Errorf("after prune expected %d sessions, got %d", keepSessions, len(files))
	}

	// The oldest sessions (000–009) should be gone.
	for i := 0; i < 10; i++ {
		p := s.path("sessions", fmt.Sprintf("sess-%03d.json", i))
		if _, err := os.Stat(p); err == nil {
			t.Errorf("old session %d should have been pruned", i)
		}
	}
}

// ---- writeAtomic leaves no tmp files ----------------------------------------

func TestWriteAtomicNoTmpFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sub", "file.txt")

	if err := writeAtomic(target, []byte("hello")); err != nil {
		t.Fatalf("writeAtomic: %v", err)
	}

	// Check the file was written.
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("file content: got %q, want %q", data, "hello")
	}

	// No .tmp files should remain.
	entries, _ := os.ReadDir(filepath.Dir(target))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover tmp file: %s", e.Name())
		}
	}
}

// ---- addUsage / todayUsage --------------------------------------------------

func TestAddUsageAndTodayUsage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	u0 := todayUsage()
	if u0.Requests != 0 || u0.Tokens != 0 {
		t.Errorf("fresh usage should be zero, got %+v", u0)
	}

	addUsage(100)
	addUsage(200)

	u := todayUsage()
	if u.Requests != 2 {
		t.Errorf("expected 2 requests, got %d", u.Requests)
	}
	if u.Tokens != 300 {
		t.Errorf("expected 300 tokens, got %d", u.Tokens)
	}
	if u.Day != time.Now().Format("2006-01-02") {
		t.Errorf("day mismatch: %q", u.Day)
	}
}

// ---- projectRules -----------------------------------------------------------

func TestProjectRulesPRISMFirst(t *testing.T) {
	dir := t.TempDir()

	// Write both PRISM.md and AGENTS.md; PRISM.md should win.
	if err := os.WriteFile(filepath.Join(dir, "PRISM.md"), []byte("prism rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agents rules"), 0o644); err != nil {
		t.Fatal(err)
	}

	name, text := projectRules(dir)
	if name != "PRISM.md" {
		t.Errorf("expected PRISM.md, got %q", name)
	}
	if !strings.Contains(text, "prism rules") {
		t.Errorf("expected prism rules content, got %q", text)
	}
}

func TestProjectRulesFallsBackToAGENTS(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agents rules"), 0o644); err != nil {
		t.Fatal(err)
	}

	name, text := projectRules(dir)
	if name != "AGENTS.md" {
		t.Errorf("expected AGENTS.md, got %q", name)
	}
	if !strings.Contains(text, "agents rules") {
		t.Errorf("expected agents rules content, got %q", text)
	}
}

func TestProjectRulesNone(t *testing.T) {
	dir := t.TempDir()
	name, text := projectRules(dir)
	if name != "" || text != "" {
		t.Errorf("expected empty results, got name=%q text=%q", name, text)
	}
}

// ---- prune() trims old tool outputs but keeps recent ones ------------------

func makeAgent(msgs []Message) *Agent {
	return &Agent{messages: msgs}
}

func TestPruneTrimsOldToolOutputs(t *testing.T) {
	// Build a message list: system + many tool messages + keepRecent recent ones.
	// prune() skips messages[0] (system) and the last keepRecent messages.
	longContent := strings.Repeat("x", pruneOver+100)

	var msgs []Message
	msgs = append(msgs, Message{Role: "system", Content: "sys"})

	// Add old tool messages that should be pruned (index 1..N-keepRecent).
	for i := 0; i < 20; i++ {
		msgs = append(msgs, Message{Role: "tool", Content: longContent})
	}
	// Add keepRecent recent messages that must NOT be pruned.
	for i := 0; i < keepRecent; i++ {
		msgs = append(msgs, Message{Role: "tool", Content: longContent})
	}

	a := makeAgent(msgs)
	a.prune()

	// Old tool messages (indices 1..20) should be trimmed.
	for i := 1; i <= 20; i++ {
		if len(a.messages[i].Content) >= len(longContent) {
			t.Errorf("message[%d] should have been pruned but wasn't", i)
		}
		if !strings.Contains(a.messages[i].Content, prunedNote) {
			t.Errorf("message[%d] should contain prunedNote", i)
		}
	}

	// Recent messages (last keepRecent) should be untouched.
	for i := len(msgs) - keepRecent; i < len(msgs); i++ {
		if a.messages[i].Content != longContent {
			t.Errorf("recent message[%d] should not be pruned", i)
		}
	}
}

func TestPruneKeepsShortToolOutputs(t *testing.T) {
	shortContent := strings.Repeat("y", pruneOver-1)

	var msgs []Message
	msgs = append(msgs, Message{Role: "system", Content: "sys"})
	for i := 0; i < 20; i++ {
		msgs = append(msgs, Message{Role: "tool", Content: shortContent})
	}
	for i := 0; i < keepRecent; i++ {
		msgs = append(msgs, Message{Role: "tool", Content: shortContent})
	}

	a := makeAgent(msgs)
	a.prune()

	for i := 1; i <= 20; i++ {
		if a.messages[i].Content != shortContent {
			t.Errorf("short message[%d] should not be pruned", i)
		}
	}
}

// ---- clipText is UTF-8 safe -------------------------------------------------

func TestClipTextUTF8Safe(t *testing.T) {
	// Build a string with multi-byte runes.
	s := strings.Repeat("日本語テスト", 100) // each rune is 3 bytes

	clipped := clipText(s, 50)

	// Result must be valid UTF-8.
	if !utf8.ValidString(clipped) {
		t.Errorf("clipText result is not valid UTF-8")
	}
	// Result must not exceed n bytes (plus the ellipsis).
	if len(clipped) > 50+4 { // 4 bytes for "…" (3 bytes) + some slack
		t.Errorf("clipText result too long: %d bytes", len(clipped))
	}
}

func TestClipTextShortPassthrough(t *testing.T) {
	s := "hello"
	if got := clipText(s, 100); got != s {
		t.Errorf("clipText short string: got %q, want %q", got, s)
	}
}

func TestClipTextEmpty(t *testing.T) {
	if got := clipText("", 10); got != "" {
		t.Errorf("clipText empty: got %q", got)
	}
}

func TestClipTextExactBoundary(t *testing.T) {
	s := "abcde"
	// Clip at exactly len(s) — should return unchanged.
	if got := clipText(s, 5); got != s {
		t.Errorf("clipText exact boundary: got %q, want %q", got, s)
	}
}

func TestClipTextMultibyteAtBoundary(t *testing.T) {
	// "日" is 3 bytes (0xe6 0x97 0xa5). Clip at 2 should not split the rune.
	s := "日本"
	clipped := clipText(s, 2)
	if !utf8.ValidString(clipped) {
		t.Errorf("clipText multibyte boundary: invalid UTF-8: %q", clipped)
	}
}
