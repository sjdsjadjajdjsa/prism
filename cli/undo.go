package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// undoEntry is a file as it was before the turn first wrote to it.
// afterContent lets /undo spot changes the user made since.
type undoEntry struct {
	path          string
	beforeContent []byte // nil = file did not exist before the turn
	afterContent  []byte // content after the agent's last write in this turn
	afterSet      bool   // true once afterContent has been recorded
}

type undoTurn struct {
	files []*undoEntry
}

const (
	maxUndoTurns = 20
	maxUndoBytes = 32 * 1024 * 1024 // across all turns
)

type undoStack struct {
	mu    sync.Mutex
	turns []*undoTurn // oldest first; capped at maxUndoTurns
	bytes int         // total bytes stored (before+after)
}

func newUndoStack() *undoStack { return &undoStack{} }

// beginTurn starts a new turn. Call recordBefore before each write and
// recordAfter after it.
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

// recordBefore saves path's content the first time turn t touches it. Call it
// before the file changes. The read happens outside the lock.
func (s *undoStack) recordBefore(t *undoTurn, path string) {
	if t == nil {
		return
	}

	s.mu.Lock()
	for _, e := range t.files {
		if e.path == path {
			s.mu.Unlock()
			return
		}
	}
	s.mu.Unlock()

	data, err := os.ReadFile(path)
	e := &undoEntry{path: path}
	if err == nil {
		e.beforeContent = data
	}
	// beforeContent stays nil if the file didn't exist.

	s.mu.Lock()
	defer s.mu.Unlock()
	// A parallel tool call may have recorded the same path while we read.
	for _, existing := range t.files {
		if existing.path == path {
			return
		}
	}
	if err == nil {
		s.bytes += len(data)
		// Drop the oldest turns until under the memory cap.
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

// recordAfter saves path's content after the agent wrote it, so /undo can
// spot later user changes.
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

type undoResult struct {
	restored []string
	refused  []string // files changed by user since the turn
}

// pop restores and removes the newest turn. Files changed since the agent
// last wrote them are refused.
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

		// Has the user changed it since?
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
