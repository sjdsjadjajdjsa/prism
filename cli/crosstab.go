package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type tabInfo struct {
	ID      int
	Name    string
	Status  string // idle|working|waiting|done|error
	Task    string
	Files   []string
	Updated time.Time
}

type tabEntry struct {
	tabInfo
	files map[string]bool // paths this tab has touched or claimed
}

// tabHub tracks the open tabs in one Prism window. Safe for concurrent use.
type tabHub struct {
	mu      sync.Mutex
	tabs    map[int]*tabEntry
	nextID  int
	claims  map[string]int // path to owning tab ID
	ver     uint64
	waiters []chan struct{}
	// posOf maps a hub id to its 1-based tab-bar position, so brief() and
	// claimFile() show the numbers the user sees.
	posOf func(id int) int
}

func newTabHub() *tabHub {
	return &tabHub{
		tabs:   make(map[int]*tabEntry),
		claims: make(map[string]int),
		nextID: 1,
	}
}

// bump increments the version and wakes long-poll waiters. Caller holds mu.
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

func (h *tabHub) remove(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.tabs[id]; !ok {
		return
	}
	for path, owner := range h.claims {
		if owner == id {
			delete(h.claims, path)
		}
	}
	delete(h.tabs, id)
	h.bump()
}

func (h *tabHub) rename(id int, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if e, ok := h.tabs[id]; ok {
		e.Name = name
		e.Updated = time.Now()
		h.bump()
	}
}

// setStatus sets a tab's status and task. It is a no-op on a nil hub.
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

// claim gives tab id exclusive ownership of path. It returns the owner and
// whether that is id. A removed tab's claim is not recorded.
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

// release drops tab id's file claims, when a turn ends or the tab closes.
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

func (h *tabHub) owner(path string) (int, bool) {
	if h == nil {
		return 0, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	id, ok := h.claims[path]
	return id, ok
}

func (h *tabHub) list() []tabInfo {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]tabInfo, 0, len(h.tabs))
	for _, e := range h.tabs {
		info := e.tabInfo
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

func (h *tabHub) version() uint64 {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ver
}

// wait blocks until the version passes since, max elapses or ctx is done.
// It reports whether a new version was seen.
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

// brief is a short summary of the OTHER tabs for the agent prompt, or "" when
// this tab is alone. Tab numbers match the tab bar when posOf is set, else hub
// IDs. Capped at about 600 bytes.
func (h *tabHub) brief(self int) string {
	if h == nil {
		return ""
	}
	tabs := h.list()
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
			if len(files) > 4 {
				files = files[:4]
			}
			line += " — files: " + strings.Join(files, ", ")
			if len(t.Files) > 4 {
				line += fmt.Sprintf(" (+%d more)", len(t.Files)-4)
			}
		}
		if sb.Len()+len(line) > 580 {
			sb.WriteString("\n  …")
			break
		}
		sb.WriteString(line)
	}
	return sb.String()
}
