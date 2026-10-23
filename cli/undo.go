package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// undoEntry records the state of a file before the first write/edit in a turn.
// beforeContent==nil means the file did not exist before the turn.
// afterContent is set after the turn ends (via sealTurn) to detect user changes.
type undoEntry struct {
	path          string
	beforeContent []byte // nil = file did not exist before the turn
	afterContent  []byte // content after the agent's last write in this turn
	afterSet      bool   // true once afterContent has been recorded
}

// undoTurn is the snapshot for one agent turn.
type undoTurn struct {
	files []*undoEntry
}

const (
	maxUndoTurns = 20
	maxUndoBytes = 32 * 1024 * 1024 // 32 MB total cap
)

// undoStack keeps per-turn snapshots for a single tab.
type undoStack struct {
	mu    sync.Mutex
	turns []*undoTurn // oldest first; capped at maxUndoTurns
	bytes int         // total bytes stored (before+after)
}

func newUndoStack() *undoStack { return &undoStack{} }

// beginTurn starts a new turn snapshot and returns it. The caller records
// files before each first write/edit per path via recordBefore, and calls
// recordAfter after each write/edit.
func (s *undoStack) beginTurn() *undoTurn {
	t := &undoTurn{}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turns = append(s.turns, t)
	if len(s.turns) > maxUndoTurns {
		old := s.turns[0]
		s.turns = s.turns[1:]
		for _, e := range old.files {
			s.bytes -= len(e.beforeContent) + len(e.afterContent)
		}
	}
	return t
}

// recordBefore saves the original content of path into turn t, but only the
// first time per path per turn. Must be called before the file is modified.
// File I/O is performed outside the lock to avoid holding it during disk reads.
func (s *undoStack) recordBefore(t *undoTurn, path string) {
	if t == nil {
		return
	}
	// Check (under lock) whether we already have a record for this path.
	s.mu.Lock()
	for _, e := range t.files {
		if e.path == path {
			s.mu.Unlock()
			return // already recorded this turn
		}
	}
	s.mu.Unlock()

	// Read the file outside the lock — this is the potentially slow I/O.
	data, err := os.ReadFile(path)
	e := &undoEntry{path: path}
	if err == nil {
		e.beforeContent = data
	}
	// beforeContent stays nil if file didn't exist.

	// Re-acquire the lock to append and enforce the memory cap.
	s.mu.Lock()
	defer s.mu.Unlock()
	// Double-check: another goroutine may have recorded the same path while we
	// were reading (unlikely but possible with concurrent tool calls).
	for _, existing := range t.files {
		if existing.path == path {
			return
		}
	}
	if err == nil {
		s.bytes += len(data)
		// enforce memory cap: drop oldest turns until under budget
		for s.bytes > maxUndoBytes && len(s.turns) > 1 {
			old := s.turns[0]
			s.turns = s.turns[1:]
			for _, oe := range old.files {
				s.bytes -= len(oe.beforeContent) + len(oe.afterContent)
			}
		}
	}
	t.files = append(t.files, e)
}

// recordAfter saves the content of path after the agent wrote it. Called after
// each successful write/edit so we can detect user changes on /undo.
func (s *undoStack) recordAfter(t *undoTurn, path string) {
	if t == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range t.files {
		if e.path != path {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		old := len(e.afterContent)
		e.afterContent = data
		e.afterSet = true
		s.bytes += len(data) - old
		return
	}
}

// undoResult describes what was restored.
type undoResult struct {
	restored []string
	refused  []string // files changed by user since the turn
}

// pop removes and restores the most recent turn. Returns an error if the stack
// is empty. Files whose current content differs from what the agent last wrote
// (afterContent) are refused with a clear message.
func (s *undoStack) pop(dir string) (undoResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.turns) == 0 {
		return undoResult{}, fmt.Errorf("nothing to undo")
	}
	t := s.turns[len(s.turns)-1]
	s.turns = s.turns[:len(s.turns)-1]
	for _, e := range t.files {
		s.bytes -= len(e.beforeContent) + len(e.afterContent)
	}

	var res undoResult
	for _, e := range t.files {
		rel := relPath(dir, e.path)

		// Check for user change: compare current disk content to afterContent.
		if e.afterSet {
			current, err := os.ReadFile(e.path)
			if err != nil && !os.IsNotExist(err) {
				res.refused = append(res.refused, rel+" (can't read)")
				continue
			}
			if !bytes.Equal(current, e.afterContent) {
				res.refused = append(res.refused, rel+" (changed since prism wrote it)")
				continue
			}
		}

		if e.beforeContent == nil {
			// agent created this file; delete it
			if err := os.Remove(e.path); err != nil && !os.IsNotExist(err) {
				res.refused = append(res.refused, rel+" (can't remove)")
				continue
			}
		} else {
			if err := os.WriteFile(e.path, e.beforeContent, 0o644); err != nil {
				res.refused = append(res.refused, rel+" (can't write)")
				continue
			}
		}
		res.restored = append(res.restored, rel)
	}
	return res, nil
}

// depth returns how many turns are on the stack.
func (s *undoStack) depth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.turns)
}

func relPath(dir, path string) string {
	rel, err := filepath.Rel(dir, path)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}
	return path
}
