package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// tabInfo describes one open tab for list() and brief().
type tabInfo struct {
	ID      int
	Name    string
	Status  string // idle|working|waiting|done|error
	Task    string
	Files   []string
	Updated time.Time
}

// tabEntry is the internal record kept by tabHub.
type tabEntry struct {
	tabInfo
	files map[string]bool // set of paths this tab has touched/claimed
}

// tabHub is a thread-safe registry of open tabs in one Prism window.
type tabHub struct {
	mu      sync.Mutex
	tabs    map[int]*tabEntry
	nextID  int
	claims  map[string]int // path -> owning tab ID
	ver     uint64
	waiters []chan struct{}
	// posOf maps hub id → 1-based tab-bar position. Set by the model so that
	// brief() and claimFile() show numbers that match the tab bar.
	posOf func(id int) int
}

func newTabHub() *tabHub {
	return &tabHub{
		tabs:   make(map[int]*tabEntry),
		claims: make(map[string]int),
		nextID: 1,
	}
}

// bump increments the version and wakes all long-poll waiters. Must be called with mu held.
func (h *tabHub) bump() {
	h.ver++
	for _, ch := range h.waiters {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	h.waiters = h.waiters[:0]
}

// add registers a new tab and returns its ID.
func (h *tabHub) add(name string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.nextID
	h.nextID++
	h.tabs[id] = &tabEntry{
		tabInfo: tabInfo{
			ID:      id,
			Name:    name,
			Status:  "idle",
			Updated: time.Now(),
		},
		files: make(map[string]bool),
	}
	h.bump()
	return id
}

// remove unregisters a tab and releases all its file claims.
func (h *tabHub) remove(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.tabs[id]; !ok {
		return
	}
	// release all claims owned by this tab
	for path, owner := range h.claims {
		if owner == id {
			delete(h.claims, path)
		}
	}
	delete(h.tabs, id)
	h.bump()
}

// rename changes the display name of a tab.
func (h *tabHub) rename(id int, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if e, ok := h.tabs[id]; ok {
		e.Name = name
		e.Updated = time.Now()
		h.bump()
	}
}

// setStatus updates the status and current task description of a tab.
// Safe to call when hub is nil (no-op).
func (h *tabHub) setStatus(id int, status, task string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if e, ok := h.tabs[id]; ok {
		e.Status = status
		e.Task = task
		e.Updated = time.Now()
		h.bump()
	}
}

// touched records that a tab changed or read a file.
func (h *tabHub) touched(id int, path string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if e, ok := h.tabs[id]; ok {
		e.files[path] = true
		e.Updated = time.Now()
		h.bump()
	}
}

// claim attempts to give tab id exclusive ownership of path.
// Returns (ownerID, true) if id already owns it or it was unclaimed.
// Returns (existingOwner, false) if another tab owns it.
// If the tab id is no longer registered (removed), the claim is not recorded.
func (h *tabHub) claim(id int, path string) (int, bool) {
	if h == nil {
		return id, true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if owner, exists := h.claims[path]; exists {
		if owner == id {
			return id, true
		}
		return owner, false
	}
	// Don't record a claim for a tab that has already been removed.
	e, ok := h.tabs[id]
	if !ok {
		return id, true
	}
	h.claims[path] = id
	e.files[path] = true
	e.Updated = time.Now()
	h.bump()
	return id, true
}

// release drops all file claims held by tab id (called when a turn ends or tab closes).
func (h *tabHub) release(id int) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for path, owner := range h.claims {
		if owner == id {
			delete(h.claims, path)
		}
	}
	h.bump()
}

// owner returns the tab ID that currently claims path, if any.
func (h *tabHub) owner(path string) (int, bool) {
	if h == nil {
		return 0, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	id, ok := h.claims[path]
	return id, ok
}

// list returns a snapshot of all tabs sorted by ID.
func (h *tabHub) list() []tabInfo {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]tabInfo, 0, len(h.tabs))
	for _, e := range h.tabs {
		info := e.tabInfo
		// build Files slice from the set
		info.Files = make([]string, 0, len(e.files))
		for f := range e.files {
			info.Files = append(info.Files, f)
		}
		sort.Strings(info.Files)
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// version returns the current change counter (incremented on every mutation).
func (h *tabHub) version() uint64 {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ver
}

// wait blocks until the hub version advances past since, or until max elapses or ctx is done.
// Returns true if a new version was seen, false on timeout/cancel.
func (h *tabHub) wait(ctx context.Context, since uint64, max time.Duration) bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	if h.ver > since {
		h.mu.Unlock()
		return true
	}
	ch := make(chan struct{}, 1)
	h.waiters = append(h.waiters, ch)
	h.mu.Unlock()

	timer := time.NewTimer(max)
	defer timer.Stop()
	select {
	case <-ch:
		return true
	case <-timer.C:
		// Remove the stale channel from the waiters list so it doesn't leak.
		h.mu.Lock()
		for i, w := range h.waiters {
			if w == ch {
				h.waiters = append(h.waiters[:i], h.waiters[i+1:]...)
				break
			}
		}
		h.mu.Unlock()
		return false
	case <-ctx.Done():
		// Remove the stale channel from the waiters list so it doesn't leak.
		h.mu.Lock()
		for i, w := range h.waiters {
			if w == ch {
				h.waiters = append(h.waiters[:i], h.waiters[i+1:]...)
				break
			}
		}
		h.mu.Unlock()
		return false
	}
}

// brief returns a compact plain-text summary of all OTHER tabs for injection into
// the agent prompt. Returns "" when this tab is alone (no extra tokens).
// Tab numbers shown match the tab bar (1-based positions) when h.posOf is set;
// otherwise hub IDs are used as a fallback.
// The output is capped at ~600 bytes.
func (h *tabHub) brief(self int) string {
	if h == nil {
		return ""
	}
	tabs := h.list()
	// filter out self
	others := make([]tabInfo, 0, len(tabs))
	for _, t := range tabs {
		if t.ID != self {
			others = append(others, t)
		}
	}
	if len(others) == 0 {
		return ""
	}

	// Snapshot posOf under the lock so we don't call it while holding mu.
	h.mu.Lock()
	posOf := h.posOf
	h.mu.Unlock()

	pos := func(id int) int {
		if posOf != nil {
			return posOf(id)
		}
		return id
	}

	var sb strings.Builder
	sb.WriteString("Other open tabs in this window:")
	for _, t := range others {
		line := fmt.Sprintf("\n  tab %d (%s) [%s]", pos(t.ID), t.Name, t.Status)
		if t.Task != "" {
			line += ": " + t.Task
		}
		if len(t.Files) > 0 {
			files := t.Files
			// keep file list short
			if len(files) > 4 {
				files = files[:4]
			}
			line += " — files: " + strings.Join(files, ", ")
			if len(t.Files) > 4 {
				line += fmt.Sprintf(" (+%d more)", len(t.Files)-4)
			}
		}
		// cap total output
		if sb.Len()+len(line) > 580 {
			sb.WriteString("\n  …")
			break
		}
		sb.WriteString(line)
	}
	return sb.String()
}
