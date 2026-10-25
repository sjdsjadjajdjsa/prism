package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
	// Tests may run inside a container; pretend the store is kept.
	t.Setenv("PRISM_MEMORY_KEEP", "1")
	return openStore(project)
}

func TestStorePersistsNeedsKeepInsideContainer(t *testing.T) {
	s := tempStore(t, "/proj/keep")
	if !s.persists() {
		t.Fatal("PRISM_MEMORY_KEEP=1 must keep memory")
	}
	t.Setenv("PRISM_MEMORY_KEEP", "")
	_, err := os.Stat("/.dockerenv")
	inContainer := err == nil
	if got := s.persists(); got == inContainer {
		t.Fatalf("persists() = %v with container=%v and no PRISM_MEMORY_KEEP", got, inContainer)
	}
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

func TestMemoryItems(t *testing.T) {
	tests := []struct {
		name, text string
		want       []string
	}{
		{"empty", " \n\t", nil},
		{"plain", "first fact\nsecond fact", []string{"first fact", "second fact"}},
		{"bullets", "- first fact\n* second fact\n• third fact", []string{"first fact", "second fact", "third fact"}},
		{"numbered", "1. first fact\n2) second fact", []string{"first fact", "second fact"}},
		{"whitespace", "  - first fact  \r\n\n\t* second fact\t\n", []string{"first fact", "second fact"}},
		{"punctuation", "- use foo-bar\n- *keep emphasis*", []string{"use foo-bar", "*keep emphasis*"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := memoryItems(tc.text); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("memoryItems(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

func TestForgetItems(t *testing.T) {
	const initial = "- first fact\n- second fact\n- third fact"
	tests := []struct {
		name, args, want string
		removed          []string
		wantErr          bool
	}{
		{"one", "2", "- first fact\n- third fact", []string{"second fact"}, false},
		{"several", "3 1", "- second fact", []string{"first fact", "third fact"}, false},
		{"duplicate", "2 2", "- first fact\n- third fact", []string{"second fact"}, false},
		{"every number", "1 2 3", "", []string{"first fact", "second fact", "third fact"}, false},
		{"all", "all", "", nil, false},
		{"no args", "", initial, nil, true},
		{"zero", "0", initial, nil, true},
		{"negative", "-1", initial, nil, true},
		{"out of range", "4", initial, nil, true},
		{"invalid after valid", "2 5", initial, nil, true},
		{"not a number", "two", initial, nil, true},
		{"mixed all", "all 2", initial, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := tempStore(t, "/proj/items")
			if err := s.setMemory(initial); err != nil {
				t.Fatal(err)
			}
			job := &memoryJob{store: s, current: initial, gen: s.generation()}
			removed, err := s.forgetItems(strings.Fields(tc.args))
			if (err != nil) != tc.wantErr {
				t.Fatalf("forgetItems(%q): %v", tc.args, err)
			}
			if !reflect.DeepEqual(removed, tc.removed) {
				t.Errorf("removed = %q, want %q", removed, tc.removed)
			}
			if got := s.memory(); got != tc.want {
				t.Fatalf("memory = %q, want %q", got, tc.want)
			}
			wantGen := job.gen + 1
			if tc.wantErr {
				wantGen = job.gen
			}
			if got := s.generation(); got != wantGen {
				t.Fatalf("generation = %d, want %d", got, wantGen)
			}
			if !tc.wantErr {
				wrote, err := job.store.setMemoryIf(job.current, job.gen, job.current)
				if err != nil || wrote {
					t.Fatalf("stale job write = %v, %v", wrote, err)
				}
				if got := s.memory(); got != tc.want {
					t.Fatalf("stale job changed memory to %q", got)
				}
				wrote, err = s.setMemoryIf(tc.want, s.generation(), tc.want)
				if err != nil || !wrote {
					t.Fatalf("fresh job write = %v, %v", wrote, err)
				}
			}
		})
	}
}

func TestForgetItemsEmpty(t *testing.T) {
	for _, arg := range []string{"1", "all"} {
		t.Run(arg, func(t *testing.T) {
			s := tempStore(t, "/proj/empty")
			_, err := s.forgetItems([]string{arg})
			if (err != nil) != (arg == "1") {
				t.Fatalf("forgetItems(%q) on empty memory: %v", arg, err)
			}
		})
	}
}

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
	wrote, err := s.setMemoryIf("- stale fact", gen, "- remember this")
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
	wrote, err = s.setMemoryIf("- fresh fact", newGen, "")
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

	addUsage(100, false)
	addUsage(200, true)

	u := todayUsage()
	if u.Requests != 2 {
		t.Errorf("expected 2 requests, got %d", u.Requests)
	}
	if u.Tokens != 300 {
		t.Errorf("expected 300 tokens, got %d", u.Tokens)
	}
	if u.Yours != 1 {
		t.Errorf("expected 1 request on your plans or keys, got %d", u.Yours)
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

// ---- stale numbers, base check, structure kept ------------------------------

func TestNumberedForgetRefusedAfterMemoryChanged(t *testing.T) {
	s := tempStore(t, "/proj/stale")
	if err := s.setMemory("- keep me\n- wrong fact\n- other"); err != nil {
		t.Fatal(err)
	}
	if _, lines, _, err := s.memoryCommand([]string{"/memory"}); err != nil || !strings.Contains(strings.Join(lines, "\n"), "2  wrong fact") {
		t.Fatalf("list = %q, %v", lines, err)
	}
	// A background rewrite shifts the numbers after the list was shown.
	if err := s.setMemory("- brand new\n- keep me\n- wrong fact\n- other"); err != nil {
		t.Fatal(err)
	}
	_, _, changed, err := s.memoryCommand([]string{"/memory", "forget", "2"})
	if err == nil || !isUsageError(err) || changed || !strings.Contains(err.Error(), "memory just changed · run /memory again for the new numbers") {
		t.Fatalf("forget after change: changed=%v err=%v", changed, err)
	}
	if got := s.memory(); got != "- brand new\n- keep me\n- wrong fact\n- other" {
		t.Fatalf("memory modified: %q", got)
	}
	// Listing again accepts the new numbers.
	s.memoryCommand([]string{"/memory"})
	title, _, changed, err := s.memoryCommand([]string{"/memory", "forget", "3"})
	if err != nil || !changed || title != "forgot: wrong fact" {
		t.Fatalf("forget after relist: %q %v %v", title, changed, err)
	}
	// After our own forget the numbers moved again, so a second one needs a new list.
	if _, _, _, err := s.memoryCommand([]string{"/memory", "forget", "3"}); err == nil || !isUsageError(err) {
		t.Fatalf("forget with stale numbers after own forget: %v", err)
	}
}

func TestSetMemoryIfChecksDiskBase(t *testing.T) {
	s := tempStore(t, "/proj/base")
	if err := s.setMemory("- a"); err != nil {
		t.Fatal(err)
	}
	gen := s.generation()
	// Another process rewrites the file without touching this store's generation.
	if err := writeAtomic(s.path("memory.md"), []byte("- b\n")); err != nil {
		t.Fatal(err)
	}
	wrote, err := s.setMemoryIf("- c", gen, "- a")
	if err != nil || wrote {
		t.Fatalf("write over changed disk = %v, %v", wrote, err)
	}
	if got := s.memory(); got != "- b" {
		t.Fatalf("memory = %q", got)
	}
	wrote, err = s.setMemoryIf("- c", gen, "- b")
	if err != nil || !wrote || s.memory() != "- c" {
		t.Fatalf("matching base: %v %v %q", wrote, err, s.memory())
	}
}

func TestForgetKeepsStructure(t *testing.T) {
	s := tempStore(t, "/proj/structure")
	if err := s.setMemory("# Notes\n- one\n  wrapped text\n\n* two\n3. three"); err != nil {
		t.Fatal(err)
	}
	// items: 1 "# Notes", 2 "one", 3 "wrapped text", 4 "two", 5 "three"
	removed, err := s.forgetItems([]string{"2"})
	if err != nil || !reflect.DeepEqual(removed, []string{"one"}) {
		t.Fatalf("removed %q, %v", removed, err)
	}
	if got, want := s.memory(), "# Notes\n  wrapped text\n\n* two\n3. three"; got != want {
		t.Fatalf("memory = %q, want %q", got, want)
	}
}

func TestForgetMessages(t *testing.T) {
	s := tempStore(t, "/proj/messages")
	_, _, _, err := s.memoryCommand([]string{"/memory", "forget", "1"})
	if err == nil || !isUsageError(err) || err.Error() != "nothing to forget yet" {
		t.Fatalf("empty forget: %v", err)
	}
	s.setMemory("- a\n- b")
	for _, args := range [][]string{{"all", "2"}, {"2", "all"}} {
		_, err = s.forgetItems(args)
		if err == nil || !isUsageError(err) || err.Error() != `"all" can't be mixed with numbers` {
			t.Fatalf("forgetItems(%v): %v", args, err)
		}
	}
	if isUsageError(fmt.Errorf("disk full")) || isUsageError(nil) {
		t.Fatal("plain errors are not usage errors")
	}
}

// ---- memoryJob.run: never store fragments -------------------------------------

func TestMemoryLinesDropsFragments(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		cutOff bool
		want   []string
	}{
		{"clean", "- Go project\n- Tests run with: `go test ./...`", false, []string{"- Go project", "- Tests run with: `go test ./...`"}},
		{"unclosed backtick at end", "- Go project\n- Tests run with: `", false, []string{"- Go project"}},
		{"unclosed backtick mid-line", "- Go project\n- Run `go te\n- Uses SQLite", false, []string{"- Go project", "- Uses SQLite"}},
		{"open quote", "- Go project\n- The greeting is \"hello", false, []string{"- Go project"}},
		{"open paren", "- Go project\n- Entry point (main.go", false, []string{"- Go project"}},
		{"ends on colon", "- Go project\n- Tests run with:", false, []string{"- Go project"}},
		{"ends on connecting word", "- Go project\n- Tests are run with", false, []string{"- Go project"}},
		{"cut off drops last even if it looks fine", "- Go project\n- Uses SQLite", true, []string{"- Go project"}},
		{"only fragment", "- Tests run with: `", false, nil},
		{"star and dot bullets", "* Go project\n• Uses SQLite.", false, []string{"- Go project", "- Uses SQLite."}},
		{"path ending in slash is complete", "- Source lives in src/", false, []string{"- Source lives in src/"}},
		{"prose ignored", "Here is the memory:\n- Go project", false, []string{"- Go project"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := memoryLines(tc.text, tc.cutOff); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("memoryLines = %q, want %q", got, tc.want)
			}
		})
	}
}

func memoryReplyServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMemoryJobRunDropsTruncatedLine(t *testing.T) {
	s := tempStore(t, "/proj/cut")
	srv := memoryReplyServer(t, "- Project is a FizzBuzz demo.\n- Tests run with: `")
	job := &memoryJob{api: srv.URL, journal: "USER: hi", store: s, gen: s.generation()}
	changed, err := job.run(t.Context())
	if err != nil || !changed {
		t.Fatalf("run = %v, %v", changed, err)
	}
	if got, want := s.memory(), "- Project is a FizzBuzz demo."; got != want {
		t.Fatalf("memory = %q, want %q", got, want)
	}
}

func TestMemoryJobRunStoresNothingFromOnlyFragments(t *testing.T) {
	s := tempStore(t, "/proj/frag")
	if err := s.setMemory("- Existing fact"); err != nil {
		t.Fatal(err)
	}
	cur, gen := s.snapshot()
	srv := memoryReplyServer(t, "- Tests run with: `")
	job := &memoryJob{api: srv.URL, journal: "USER: hi", current: cur, store: s, gen: gen}
	changed, err := job.run(t.Context())
	if err != nil || changed {
		t.Fatalf("run = %v, %v; want no change", changed, err)
	}
	if got := s.memory(); got != "- Existing fact" {
		t.Fatalf("memory = %q, want it untouched", got)
	}
}

func TestMemoryJobRunDropsLastLineWhenReplyFillsTokenCap(t *testing.T) {
	var b strings.Builder
	for i := 0; b.Len() < memoryMaxTokens*4; i++ {
		fmt.Fprintf(&b, "- Fact number %d about the project layout and its conventions.\n", i)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	s := tempStore(t, "/proj/cap")
	srv := memoryReplyServer(t, strings.Join(lines, "\n"))
	job := &memoryJob{api: srv.URL, journal: "USER: hi", store: s, gen: s.generation()}
	if changed, err := job.run(t.Context()); err != nil || !changed {
		t.Fatalf("run = %v, %v", changed, err)
	}
	got := strings.Split(s.memory(), "\n")
	if len(got) != 25 || got[len(got)-1] != lines[24] {
		t.Fatalf("stored %d lines, last %q", len(got), got[len(got)-1])
	}
}

func TestStorePersists(t *testing.T) {
	if (&store{}).persists() {
		t.Fatal("a store without a directory must not persist")
	}
	var nilStore *store
	if nilStore.persists() {
		t.Fatal("a nil store must not persist")
	}
	s := tempStore(t, "/proj/persist")
	if !s.persists() {
		t.Fatal("a writable store should persist")
	}
	if entries, _ := os.ReadDir(s.dir); len(entries) != 0 {
		t.Fatalf("probe left files behind: %v", entries)
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if (&store{dir: filepath.Join(blocked, "sub")}).persists() {
		t.Fatal("a store whose directory cannot be created must not persist")
	}
}

func TestMemoryJobSkippedWhenStoreCannotPersist(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	a := &Agent{store: &store{dir: filepath.Join(blocked, "sub")}, messages: []Message{
		{Role: "system"}, {Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{{}}},
	}}
	if job := a.memoryJob(); job != nil {
		t.Fatal("memory job must be skipped when nothing can be saved")
	}
	b := &Agent{store: tempStore(t, "/proj/job"), messages: a.messages}
	if job := b.memoryJob(); job == nil {
		t.Fatal("memory job expected for a persisting store")
	}
}

func TestMemoryJobRequestIsCheap(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "UNCHANGED"}}}})
	}))
	defer srv.Close()
	s := tempStore(t, "/proj/cheap")
	job := &memoryJob{api: srv.URL, journal: "USER: hi", store: s, gen: s.generation()}
	if _, err := job.run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got["prism_quick"] != true || got["reasoning_effort"] != "low" || got["max_tokens"] != float64(memoryMaxTokens) {
		t.Fatalf("memory request not cheap: quick=%v effort=%v max_tokens=%v", got["prism_quick"], got["reasoning_effort"], got["max_tokens"])
	}
}

// ---- generation guards --------------------------------------------------------

func TestStoreWritesBumpGeneration(t *testing.T) {
	s := tempStore(t, "/proj/genbump")
	g0 := s.generation()

	if err := s.setMemory("- one"); err != nil {
		t.Fatal(err)
	}
	g1 := s.generation()
	if g1 != g0+1 {
		t.Fatalf("setMemory: generation %d -> %d, want +1", g0, g1)
	}

	wrote, err := s.setMemoryIf("- two", g1, "- one")
	if err != nil || !wrote {
		t.Fatalf("setMemoryIf = %v, %v", wrote, err)
	}
	g2 := s.generation()
	if g2 != g1+1 {
		t.Fatalf("setMemoryIf: generation %d -> %d, want +1", g1, g2)
	}

	// A rejected conditional write must not bump the generation.
	if wrote, _ := s.setMemoryIf("- three", g2, "- not the current memory"); wrote {
		t.Fatal("setMemoryIf wrote although base did not match the disk")
	}
	if g := s.generation(); g != g2 {
		t.Fatalf("rejected write changed generation %d -> %d", g2, g)
	}

	if err := s.forget(); err != nil {
		t.Fatal(err)
	}
	if g := s.generation(); g != g2+1 {
		t.Fatalf("forget: generation %d -> %d, want +1", g2, g)
	}
	// forget on an already empty memory is fine and still bumps.
	if err := s.forget(); err != nil {
		t.Fatalf("forget on empty memory: %v", err)
	}
	if g := s.generation(); g != g2+2 {
		t.Fatalf("second forget: generation %d, want %d", g, g2+2)
	}
}

func TestStoreSnapshotMatchesMemoryAndGeneration(t *testing.T) {
	s := tempStore(t, "/proj/snap")
	if mem, gen := s.snapshot(); mem != "" || gen != 0 {
		t.Fatalf("fresh snapshot = %q, %d", mem, gen)
	}
	if err := s.setMemory("  - fact  \n\n"); err != nil {
		t.Fatal(err)
	}
	mem, gen := s.snapshot()
	if mem != "- fact" || gen != s.generation() {
		t.Fatalf("snapshot = %q, %d; want %q, %d", mem, gen, "- fact", s.generation())
	}
}

func TestSetMemoryIfStaleAfterSetMemory(t *testing.T) {
	s := tempStore(t, "/proj/stale")
	if err := s.setMemory("- original"); err != nil {
		t.Fatal(err)
	}
	base, gen := s.snapshot()
	// Someone else writes after the snapshot, even with identical content.
	if err := s.setMemory(base); err != nil {
		t.Fatal(err)
	}
	wrote, err := s.setMemoryIf("- from old job", gen, base)
	if err != nil || wrote {
		t.Fatalf("setMemoryIf = %v, %v; want rejected", wrote, err)
	}
	if got := s.memory(); got != "- original" {
		t.Fatalf("memory = %q, want it untouched", got)
	}
}

func TestSetMemoryIfComparesTrimmedBase(t *testing.T) {
	s := tempStore(t, "/proj/trim")
	if err := s.setMemory("- fact"); err != nil {
		t.Fatal(err)
	}
	gen := s.generation()
	wrote, err := s.setMemoryIf("- new", gen, "\n  - fact \n")
	if err != nil || !wrote {
		t.Fatalf("setMemoryIf = %v, %v; want write", wrote, err)
	}
	data, err := os.ReadFile(s.path("memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "- new\n" {
		t.Fatalf("memory.md = %q, want trailing newline and no padding", data)
	}
}

func TestMemoryJobRunRefusedAfterForget(t *testing.T) {
	s := tempStore(t, "/proj/jobforget")
	if err := s.setMemory("- Old fact."); err != nil {
		t.Fatal(err)
	}
	cur, gen := s.snapshot()
	srv := memoryReplyServer(t, "- Old fact.\n- Resurrected fact.")
	job := &memoryJob{api: srv.URL, journal: "USER: hi", current: cur, store: s, gen: gen}

	// The user runs /memory forget while the job is in flight.
	if err := s.forget(); err != nil {
		t.Fatal(err)
	}
	changed, err := job.run(t.Context())
	if err != nil || changed {
		t.Fatalf("run = %v, %v; want no write", changed, err)
	}
	if got := s.memory(); got != "" {
		t.Fatalf("memory = %q, forgotten facts must stay forgotten", got)
	}
}

func TestMemoryJobRunUnchangedReplyKeepsGeneration(t *testing.T) {
	s := tempStore(t, "/proj/jobsame")
	if err := s.setMemory("- Fact."); err != nil {
		t.Fatal(err)
	}
	cur, gen := s.snapshot()
	for _, reply := range []string{"UNCHANGED", "- Fact.", "UNCHANGED because nothing new"} {
		srv := memoryReplyServer(t, reply)
		job := &memoryJob{api: srv.URL, journal: "USER: hi", current: cur, store: s, gen: gen}
		changed, err := job.run(t.Context())
		if err != nil || changed {
			t.Fatalf("reply %q: run = %v, %v; want no change", reply, changed, err)
		}
	}
	if g := s.generation(); g != gen {
		t.Fatalf("generation moved %d -> %d without a write", gen, g)
	}
}

func TestMemoryJobRunStoresUpdateWhenFresh(t *testing.T) {
	s := tempStore(t, "/proj/jobfresh")
	if err := s.setMemory("- Old fact."); err != nil {
		t.Fatal(err)
	}
	cur, gen := s.snapshot()
	srv := memoryReplyServer(t, "- Old fact.\n- New fact.")
	job := &memoryJob{api: srv.URL, journal: "USER: hi", current: cur, store: s, gen: gen}
	changed, err := job.run(t.Context())
	if err != nil || !changed {
		t.Fatalf("run = %v, %v; want write", changed, err)
	}
	if got, want := s.memory(), "- Old fact.\n- New fact."; got != want {
		t.Fatalf("memory = %q, want %q", got, want)
	}
}

func TestStoreWithoutDirIsInert(t *testing.T) {
	s := &store{}
	if s.path("memory.md") != "" {
		t.Fatal("path of a dir-less store must be empty")
	}
	if s.memory() != "" {
		t.Fatal("dir-less store has no memory")
	}
	s.pruneSessions(0) // must not panic or touch the working directory
	if list := s.sessions(); len(list) != 0 {
		t.Fatalf("dir-less store listed sessions: %v", list)
	}
	if _, err := s.loadSession("anything"); err == nil {
		t.Fatal("loadSession on a dir-less store must fail")
	}
}

// ---- session pruning and listing ----------------------------------------------

func TestPruneSessionsRemovesHeaderSidecars(t *testing.T) {
	s := tempStore(t, "/proj/prunehdr")
	base := time.Now().Add(-time.Hour)
	const total, keep = 8, 3
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("s%02d", i)
		ts := base.Add(time.Duration(i) * time.Minute)
		for _, name := range []string{id + ".json", id + ".hdr"} {
			p := s.path("sessions", name)
			if err := writeAtomic(p, []byte("{}")); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(p, ts, ts); err != nil {
				t.Fatal(err)
			}
		}
	}
	s.pruneSessions(keep)

	for i := 0; i < total; i++ {
		id := fmt.Sprintf("s%02d", i)
		wantKept := i >= total-keep
		for _, name := range []string{id + ".json", id + ".hdr"} {
			_, err := os.Stat(s.path("sessions", name))
			if wantKept && err != nil {
				t.Errorf("%s should have been kept: %v", name, err)
			}
			if !wantKept && err == nil {
				t.Errorf("%s should have been pruned", name)
			}
		}
	}
}

func TestPruneSessionsAtOrUnderLimitKeepsAll(t *testing.T) {
	s := tempStore(t, "/proj/pruneunder")
	for i := 0; i < 4; i++ {
		if err := writeAtomic(s.path("sessions", fmt.Sprintf("s%d.json", i)), []byte("{}")); err != nil {
			t.Fatal(err)
		}
	}
	for _, keep := range []int{4, 5, 100} {
		s.pruneSessions(keep)
		if files, _ := filepath.Glob(s.path("sessions", "*.json")); len(files) != 4 {
			t.Fatalf("pruneSessions(%d) left %d files, want 4", keep, len(files))
		}
	}
	// A store that never saved anything has no sessions dir; pruning is a no-op.
	empty := tempStore(t, "/proj/pruneempty")
	empty.pruneSessions(1)
}

func TestPruneSessionsIgnoresNonSessionFiles(t *testing.T) {
	s := tempStore(t, "/proj/pruneother")
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 3; i++ {
		p := s.path("sessions", fmt.Sprintf("s%d.json", i))
		if err := writeAtomic(p, []byte("{}")); err != nil {
			t.Fatal(err)
		}
		ts := base.Add(time.Duration(i) * time.Minute)
		os.Chtimes(p, ts, ts)
	}
	other := s.path("sessions", "notes.txt")
	if err := writeAtomic(other, []byte("keep me")); err != nil {
		t.Fatal(err)
	}
	s.pruneSessions(1)
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("non-session file was removed: %v", err)
	}
	files, _ := filepath.Glob(s.path("sessions", "*.json"))
	if len(files) != 1 || filepath.Base(files[0]) != "s2.json" {
		t.Fatalf("remaining sessions = %v, want only s2.json", files)
	}
}

func TestSaveSessionPrunesBeyondKeepSessions(t *testing.T) {
	s := tempStore(t, "/proj/saveprune")
	base := time.Now().Add(-time.Hour)
	for i := 0; i < keepSessions+5; i++ {
		sess := session{
			ID:      fmt.Sprintf("sess-%03d", i),
			Title:   fmt.Sprintf("session %d", i),
			Updated: time.Now(),
			Messages: []Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "hi"},
			},
		}
		if err := s.saveSession(sess); err != nil {
			t.Fatalf("saveSession %d: %v", i, err)
		}
		// Coarse filesystem clocks can tie; make the age order explicit.
		ts := base.Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(s.path("sessions", sess.ID+".json"), ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	jsons, _ := filepath.Glob(s.path("sessions", "*.json"))
	hdrs, _ := filepath.Glob(s.path("sessions", "*.hdr"))
	if len(jsons) != keepSessions {
		t.Fatalf("%d session files, want %d", len(jsons), keepSessions)
	}
	if len(hdrs) != len(jsons) {
		t.Fatalf("%d header sidecars for %d session files", len(hdrs), len(jsons))
	}
	if got := len(s.sessions()); got != keepSessions {
		t.Fatalf("sessions() lists %d, want %d", got, keepSessions)
	}
	// The one just saved is never the one pruned.
	last := fmt.Sprintf("sess-%03d", keepSessions+4)
	if _, err := s.loadSession(last); err != nil {
		t.Fatalf("newest session pruned: %v", err)
	}
}

func TestSessionsListsLegacyFilesWithoutHeader(t *testing.T) {
	s := tempStore(t, "/proj/legacy")
	now := time.Now()

	// Legacy: full file only.
	legacy, _ := json.Marshal(session{ID: "old", Title: "legacy chat", Updated: now.Add(-time.Hour),
		Messages: []Message{{Role: "system"}, {Role: "user", Content: "x"}}})
	if err := writeAtomic(s.path("sessions", "old.json"), legacy); err != nil {
		t.Fatal(err)
	}
	// Modern: saved with a sidecar.
	if err := s.saveSession(session{ID: "new", Title: "modern chat", Updated: now,
		Messages: []Message{{Role: "system"}, {Role: "user", Content: "y"}}}); err != nil {
		t.Fatal(err)
	}
	// Garbage in either form is skipped, not fatal.
	if err := writeAtomic(s.path("sessions", "bad.hdr"), []byte("not json")); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(s.path("sessions", "bad2.json"), []byte("not json")); err != nil {
		t.Fatal(err)
	}

	list := s.sessions()
	if len(list) != 2 {
		t.Fatalf("sessions() = %+v, want 2 entries", list)
	}
	if list[0].ID != "new" || list[1].ID != "old" {
		t.Fatalf("order = %q, %q; want new, old", list[0].ID, list[1].ID)
	}
	if list[1].Title != "legacy chat" {
		t.Fatalf("legacy title = %q", list[1].Title)
	}
	for _, e := range list {
		if len(e.Messages) != 0 {
			t.Fatalf("listing must not carry messages: %+v", e)
		}
	}
}

func TestSessionsHeaderWinsOverFullFile(t *testing.T) {
	s := tempStore(t, "/proj/hdrwins")
	if err := s.saveSession(session{ID: "a", Title: "from save", Updated: time.Now(),
		Messages: []Message{{Role: "system"}, {Role: "user", Content: "x"}}}); err != nil {
		t.Fatal(err)
	}
	list := s.sessions()
	if len(list) != 1 {
		t.Fatalf("a session with both .json and .hdr must be listed once, got %d", len(list))
	}
}

func TestLoadSessionErrors(t *testing.T) {
	s := tempStore(t, "/proj/loaderr")
	short, _ := json.Marshal(session{ID: "short", Updated: time.Now(), Messages: []Message{{Role: "system"}}})
	if err := writeAtomic(s.path("sessions", "short.json"), short); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(s.path("sessions", "garbage.json"), []byte("{")); err != nil {
		t.Fatal(err)
	}
	full, _ := json.Marshal(session{ID: "ok", Updated: time.Now(),
		Messages: []Message{{Role: "system"}, {Role: "user", Content: "x"}}})
	if err := writeAtomic(s.path("sessions", "ok.json"), full); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, id string
		wantErr  bool
	}{
		{"empty id", "", true},
		{"missing", "nope", true},
		{"too few messages", "short", true},
		{"invalid json", "garbage", true},
		{"valid", "ok", false},
		// filepath.Base strips directory parts, so traversal can't leave sessions/.
		{"traversal stays inside sessions", "../../ok", false},
		{"traversal to missing", "../memory", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.loadSession(tc.id)
			if (err != nil) != tc.wantErr {
				t.Fatalf("loadSession(%q) error = %v, wantErr %v", tc.id, err, tc.wantErr)
			}
		})
	}
}

// ---- open tabs ---------------------------------------------------------------

func saveTabSession(t *testing.T, s *store, id string) {
	t.Helper()
	sess := session{
		ID:      id,
		Title:   id,
		Updated: time.Now(),
		Messages: []Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hi"},
		},
	}
	if err := s.saveSession(sess); err != nil {
		t.Fatalf("saveSession %s: %v", id, err)
	}
}

func TestOpenTabsRoundtrip(t *testing.T) {
	s := tempStore(t, "/proj/tabs")
	saveTabSession(t, s, "tab-a")
	saveTabSession(t, s, "tab-b")

	tabs := []openTab{
		{Session: "tab-a", Name: "router fix", Active: true},
		{Session: "tab-b", Name: "add tests", Active: false},
	}
	if err := s.saveOpenTabs(tabs); err != nil {
		t.Fatalf("saveOpenTabs: %v", err)
	}
	got := s.loadOpenTabs()
	if !reflect.DeepEqual(got, tabs) {
		t.Fatalf("loadOpenTabs = %+v, want %+v", got, tabs)
	}

	// The file lives per project, next to the sessions, and leaves no tmp files.
	if _, err := os.Stat(s.path("tabs.json")); err != nil {
		t.Errorf("tabs.json missing: %v", err)
	}
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover tmp file: %s", e.Name())
		}
	}
}

func TestLoadOpenTabsSkipsMissingSessions(t *testing.T) {
	s := tempStore(t, "/proj/tabs-missing")
	saveTabSession(t, s, "keep-me")

	tabs := []openTab{
		{Session: "keep-me", Name: "kept", Active: true},
		{Session: "gone", Name: "deleted session"},
		{Session: "", Name: "no session id"},
	}
	if err := s.saveOpenTabs(tabs); err != nil {
		t.Fatalf("saveOpenTabs: %v", err)
	}
	got := s.loadOpenTabs()
	if len(got) != 1 {
		t.Fatalf("loadOpenTabs returned %d tabs, want 1: %+v", len(got), got)
	}
	if got[0].Session != "keep-me" || got[0].Name != "kept" || !got[0].Active {
		t.Errorf("surviving tab = %+v, want keep-me/kept/active", got[0])
	}
}

func TestLoadOpenTabsCapsAtNine(t *testing.T) {
	s := tempStore(t, "/proj/tabs-cap")
	var tabs []openTab
	for i := 0; i < maxOpenTabs+3; i++ {
		id := fmt.Sprintf("cap-%02d", i)
		saveTabSession(t, s, id)
		tabs = append(tabs, openTab{Session: id, Name: id})
	}
	if err := s.saveOpenTabs(tabs); err != nil {
		t.Fatalf("saveOpenTabs: %v", err)
	}
	got := s.loadOpenTabs()
	if len(got) != maxOpenTabs {
		t.Fatalf("loadOpenTabs returned %d tabs, want %d", len(got), maxOpenTabs)
	}
	for i, tb := range got {
		if want := fmt.Sprintf("cap-%02d", i); tb.Session != want {
			t.Errorf("tab %d = %q, want %q (order preserved, oldest dropped)", i, tb.Session, want)
		}
	}
}

func TestLoadOpenTabsMissingOrCorruptFile(t *testing.T) {
	s := tempStore(t, "/proj/tabs-absent")
	if got := s.loadOpenTabs(); got != nil {
		t.Errorf("no tabs.json: loadOpenTabs = %+v, want nil", got)
	}
	if err := writeAtomic(s.path("tabs.json"), []byte("{")); err != nil {
		t.Fatal(err)
	}
	if got := s.loadOpenTabs(); got != nil {
		t.Errorf("corrupt tabs.json: loadOpenTabs = %+v, want nil", got)
	}
}

func TestPruneSessionsKeepsOpenTabs(t *testing.T) {
	s := tempStore(t, "/proj/tabs-prune")
	base := time.Now().Add(-time.Hour)
	for i := 0; i < keepSessions+5; i++ {
		id := fmt.Sprintf("sess-%03d", i)
		sess := session{
			ID:      id,
			Title:   id,
			Updated: base.Add(time.Duration(i) * time.Second),
			Messages: []Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "hi"},
			},
		}
		data, _ := json.Marshal(sess)
		path := s.path("sessions", id+".json")
		if err := writeAtomic(path, data); err != nil {
			t.Fatalf("writeAtomic: %v", err)
		}
		ts := base.Add(time.Duration(i) * time.Second)
		os.Chtimes(path, ts, ts)
	}
	// The oldest session is an open tab and must survive pruning.
	if err := s.saveOpenTabs([]openTab{{Session: "sess-000", Name: "pinned", Active: true}}); err != nil {
		t.Fatalf("saveOpenTabs: %v", err)
	}

	s.pruneSessions(keepSessions)

	if _, err := os.Stat(s.path("sessions", "sess-000.json")); err != nil {
		t.Errorf("open tab session sess-000 was pruned: %v", err)
	}
	for i := 1; i <= 4; i++ {
		p := s.path("sessions", fmt.Sprintf("sess-%03d.json", i))
		if _, err := os.Stat(p); err == nil {
			t.Errorf("session %d should have been pruned", i)
		}
	}
	jsons, _ := filepath.Glob(s.path("sessions", "*.json"))
	if len(jsons) != keepSessions+1 {
		t.Errorf("after prune expected %d session files (keep + 1 protected), got %d", keepSessions+1, len(jsons))
	}
}

func TestOpenTabsNilStore(t *testing.T) {
	var s *store
	if err := s.saveOpenTabs([]openTab{{Session: "x", Name: "x"}}); err != nil {
		t.Errorf("nil store saveOpenTabs: %v", err)
	}
	if got := s.loadOpenTabs(); got != nil {
		t.Errorf("nil store loadOpenTabs = %+v, want nil", got)
	}

	empty := &store{} // no dir configured
	if err := empty.saveOpenTabs([]openTab{{Session: "x"}}); err != nil {
		t.Errorf("dir-less store saveOpenTabs: %v", err)
	}
	if got := empty.loadOpenTabs(); got != nil {
		t.Errorf("dir-less store loadOpenTabs = %+v, want nil", got)
	}
}
