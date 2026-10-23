package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a test helper that writes content to path (creating dirs).
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// simulateWrite records before, writes new content, records after — mimicking
// what tools.go does via the undoStack hooks.
func simulateWrite(t *testing.T, s *undoStack, turn *undoTurn, path, content string) {
	t.Helper()
	s.recordBefore(turn, path)
	writeFile(t, path, content)
	s.recordAfter(turn, path)
}

// TestUndoCreate: agent creates a new file; /undo deletes it.
func TestUndoCreate(t *testing.T) {
	dir := t.TempDir()
	s := newUndoStack()
	turn := s.beginTurn()

	path := filepath.Join(dir, "new.txt")
	simulateWrite(t, s, turn, path, "hello")

	if _, err := os.Stat(path); err != nil {
		t.Fatal("file should exist after write")
	}

	res, err := s.pop(dir)
	if err != nil {
		t.Fatalf("pop: %v", err)
	}
	if len(res.refused) != 0 {
		t.Fatalf("unexpected refusals: %v", res.refused)
	}
	if len(res.restored) != 1 {
		t.Fatalf("want 1 restored, got %v", res.restored)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file should be deleted after undo")
	}
}

// TestUndoModify: agent edits an existing file; /undo restores original.
func TestUndoModify(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	writeFile(t, path, "original")

	s := newUndoStack()
	turn := s.beginTurn()
	simulateWrite(t, s, turn, path, "modified")

	if got := readFile(t, path); got != "modified" {
		t.Fatalf("want modified, got %q", got)
	}

	res, err := s.pop(dir)
	if err != nil {
		t.Fatalf("pop: %v", err)
	}
	if len(res.refused) != 0 {
		t.Fatalf("unexpected refusals: %v", res.refused)
	}
	if got := readFile(t, path); got != "original" {
		t.Fatalf("want original after undo, got %q", got)
	}
}

// TestUndoDelete: agent deletes a file (writes empty then removes); /undo restores it.
// We model "agent deletes" as: recordBefore captures content, then file is removed.
func TestUndoDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.txt")
	writeFile(t, path, "was here")

	s := newUndoStack()
	turn := s.beginTurn()

	// Record before (file exists), then remove it (simulating agent deletion).
	s.recordBefore(turn, path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// recordAfter: file is gone, so afterSet stays false — that's fine, pop will
	// still restore beforeContent since afterSet==false means no user-change check.
	s.recordAfter(turn, path) // no-op since file is missing

	res, err := s.pop(dir)
	if err != nil {
		t.Fatalf("pop: %v", err)
	}
	if len(res.refused) != 0 {
		t.Fatalf("unexpected refusals: %v", res.refused)
	}
	if got := readFile(t, path); got != "was here" {
		t.Fatalf("want 'was here' after undo, got %q", got)
	}
}

// TestUndoUserChangedRefusal: user edits the file after the agent; /undo refuses.
func TestUndoUserChangedRefusal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.txt")
	writeFile(t, path, "original")

	s := newUndoStack()
	turn := s.beginTurn()
	simulateWrite(t, s, turn, path, "agent wrote this")

	// User edits the file after the agent.
	writeFile(t, path, "user changed this")

	res, err := s.pop(dir)
	if err != nil {
		t.Fatalf("pop: %v", err)
	}
	if len(res.refused) == 0 {
		t.Fatal("expected refusal for user-changed file")
	}
	if len(res.restored) != 0 {
		t.Fatalf("expected no restores, got %v", res.restored)
	}
	// File should still have user's content.
	if got := readFile(t, path); got != "user changed this" {
		t.Fatalf("user content should be preserved, got %q", got)
	}
}

// TestUndoMultipleTurns: /undo goes back one turn at a time.
func TestUndoMultipleTurns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	writeFile(t, path, "v0")

	s := newUndoStack()

	turn1 := s.beginTurn()
	simulateWrite(t, s, turn1, path, "v1")

	turn2 := s.beginTurn()
	simulateWrite(t, s, turn2, path, "v2")

	if s.depth() != 2 {
		t.Fatalf("want depth 2, got %d", s.depth())
	}

	// First /undo: v2 → v1
	res, err := s.pop(dir)
	if err != nil {
		t.Fatalf("pop 1: %v", err)
	}
	if len(res.refused) != 0 {
		t.Fatalf("unexpected refusals: %v", res.refused)
	}
	if got := readFile(t, path); got != "v1" {
		t.Fatalf("after first undo want v1, got %q", got)
	}

	// Second /undo: v1 → v0
	// But now the file has v1 which matches turn1's afterContent, so it should work.
	res, err = s.pop(dir)
	if err != nil {
		t.Fatalf("pop 2: %v", err)
	}
	if len(res.refused) != 0 {
		t.Fatalf("unexpected refusals on second undo: %v", res.refused)
	}
	if got := readFile(t, path); got != "v0" {
		t.Fatalf("after second undo want v0, got %q", got)
	}

	// Third /undo: nothing left.
	_, err = s.pop(dir)
	if err == nil {
		t.Fatal("expected error on empty stack")
	}
}

// TestUndoMaxTurns: stack is capped at maxUndoTurns.
func TestUndoMaxTurns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	writeFile(t, path, "v0")

	s := newUndoStack()
	for i := range maxUndoTurns + 5 {
		turn := s.beginTurn()
		content := string(rune('a' + i))
		simulateWrite(t, s, turn, path, content)
	}
	if s.depth() != maxUndoTurns {
		t.Fatalf("want depth %d, got %d", maxUndoTurns, s.depth())
	}
}

// TestRelPathFix: relPath must not garble paths that share a prefix but are
// not actually inside dir (regression for the raw string-prefix bug).
func TestRelPathFix(t *testing.T) {
	// /home/user/proj and /home/user/project share a prefix but the second is
	// NOT inside the first.
	dir := "/home/user/proj"
	outside := "/home/user/project/foo.go"
	got := relPath(dir, outside)
	// Must return the original path, not a garbled "ect/foo.go".
	if got != outside {
		t.Errorf("relPath(%q, %q) = %q; want original path", dir, outside, got)
	}

	// A path that IS inside dir should be relativised.
	inside := "/home/user/proj/pkg/foo.go"
	got2 := relPath(dir, inside)
	if got2 != "pkg/foo.go" {
		t.Errorf("relPath(%q, %q) = %q; want %q", dir, inside, got2, "pkg/foo.go")
	}

	// dir with trailing slash should still work.
	dirSlash := "/home/user/proj/"
	got3 := relPath(dirSlash, inside)
	if got3 != "pkg/foo.go" {
		t.Errorf("relPath(%q, %q) = %q; want %q", dirSlash, inside, got3, "pkg/foo.go")
	}
}

// TestRecordBeforeOutsideLock: recordBefore must not hold the lock while doing
// file I/O (regression: I/O was previously done under the lock).
// We verify correctness by calling recordBefore concurrently on different paths.
func TestRecordBeforeOutsideLock(t *testing.T) {
	dir := t.TempDir()
	s := newUndoStack()
	turn := s.beginTurn()

	const n = 10
	paths := make([]string, n)
	for i := range n {
		p := filepath.Join(dir, fmt.Sprintf("f%d.txt", i))
		writeFile(t, p, fmt.Sprintf("content%d", i))
		paths[i] = p
	}

	// Call recordBefore concurrently — should not deadlock or race.
	done := make(chan struct{}, n)
	for _, p := range paths {
		p := p
		go func() {
			s.recordBefore(turn, p)
			done <- struct{}{}
		}()
	}
	for range n {
		<-done
	}
	if len(turn.files) != n {
		t.Errorf("want %d entries, got %d", n, len(turn.files))
	}
}

// TestUndoOnlyFirstWritePerTurnRecorded: multiple writes to same file in one
// turn only record the original before-content once.
func TestUndoOnlyFirstWritePerTurnRecorded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	writeFile(t, path, "original")

	s := newUndoStack()
	turn := s.beginTurn()

	// First write
	simulateWrite(t, s, turn, path, "first agent write")
	// Second write to same file in same turn
	simulateWrite(t, s, turn, path, "second agent write")

	// Only one entry should be in the turn.
	if len(turn.files) != 1 {
		t.Fatalf("want 1 entry, got %d", len(turn.files))
	}
	// beforeContent should be "original"
	if string(turn.files[0].beforeContent) != "original" {
		t.Fatalf("want original before, got %q", string(turn.files[0].beforeContent))
	}
	// afterContent should be "second agent write" (last write wins)
	if string(turn.files[0].afterContent) != "second agent write" {
		t.Fatalf("want second agent write as after, got %q", string(turn.files[0].afterContent))
	}

	// Undo should restore to "original"
	res, err := s.pop(dir)
	if err != nil {
		t.Fatalf("pop: %v", err)
	}
	if len(res.refused) != 0 {
		t.Fatalf("unexpected refusals: %v", res.refused)
	}
	if got := readFile(t, path); got != "original" {
		t.Fatalf("want original after undo, got %q", got)
	}
}
