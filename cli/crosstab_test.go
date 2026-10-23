package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestTabHubAddRemoveList verifies basic add/remove/list lifecycle.
func TestTabHubAddRemoveList(t *testing.T) {
	h := newTabHub()
	id1 := h.add("alpha")
	id2 := h.add("beta")
	if id1 == id2 {
		t.Fatal("IDs must be unique")
	}
	tabs := h.list()
	if len(tabs) != 2 {
		t.Fatalf("want 2 tabs, got %d", len(tabs))
	}
	if tabs[0].ID > tabs[1].ID {
		t.Error("list must be sorted by ID")
	}
	h.remove(id1)
	tabs = h.list()
	if len(tabs) != 1 || tabs[0].ID != id2 {
		t.Fatalf("after remove want 1 tab with id %d, got %v", id2, tabs)
	}
}

// TestTabHubRename verifies rename updates the name.
func TestTabHubRename(t *testing.T) {
	h := newTabHub()
	id := h.add("old")
	h.rename(id, "new")
	tabs := h.list()
	if tabs[0].Name != "new" {
		t.Errorf("want name 'new', got %q", tabs[0].Name)
	}
}

// TestTabHubSetStatus verifies status/task updates.
func TestTabHubSetStatus(t *testing.T) {
	h := newTabHub()
	id := h.add("worker")
	h.setStatus(id, "working", "fixing bug")
	tabs := h.list()
	if tabs[0].Status != "working" {
		t.Errorf("want status 'working', got %q", tabs[0].Status)
	}
	if tabs[0].Task != "fixing bug" {
		t.Errorf("want task 'fixing bug', got %q", tabs[0].Task)
	}
}

// TestTabHubNilSafe verifies nil hub methods are no-ops.
func TestTabHubNilSafe(t *testing.T) {
	var h *tabHub
	h.setStatus(1, "working", "task") // must not panic
	h.touched(1, "file.go")           // must not panic
	h.release(1)                      // must not panic
	if _, ok := h.claim(1, "x"); !ok {
		t.Error("nil hub claim should return ok=true")
	}
	if _, ok := h.owner("x"); ok {
		t.Error("nil hub owner should return ok=false")
	}
	if h.list() != nil {
		t.Error("nil hub list should return nil")
	}
	if h.version() != 0 {
		t.Error("nil hub version should return 0")
	}
	if h.brief(1) != "" {
		t.Error("nil hub brief should return empty string")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if h.wait(ctx, 0, 10*time.Millisecond) {
		t.Error("nil hub wait should return false")
	}
}

// TestTabHubClaimAndRelease verifies file ownership.
func TestTabHubClaimAndRelease(t *testing.T) {
	h := newTabHub()
	id1 := h.add("tab1")
	id2 := h.add("tab2")

	// tab1 claims a file
	owner, ok := h.claim(id1, "main.go")
	if !ok || owner != id1 {
		t.Fatalf("tab1 should own main.go, got owner=%d ok=%v", owner, ok)
	}

	// tab2 cannot claim the same file
	owner2, ok2 := h.claim(id2, "main.go")
	if ok2 {
		t.Error("tab2 should not be able to claim main.go while tab1 owns it")
	}
	if owner2 != id1 {
		t.Errorf("want owner %d, got %d", id1, owner2)
	}

	// tab1 can re-claim its own file
	owner3, ok3 := h.claim(id1, "main.go")
	if !ok3 || owner3 != id1 {
		t.Error("tab1 should be able to re-claim its own file")
	}

	// release tab1's claims
	h.release(id1)
	ownerID, owned := h.owner("main.go")
	if owned {
		t.Errorf("after release, main.go should be unclaimed, got owner %d", ownerID)
	}

	// now tab2 can claim it
	_, ok4 := h.claim(id2, "main.go")
	if !ok4 {
		t.Error("tab2 should be able to claim main.go after tab1 released it")
	}
}

// TestTabHubOwner verifies owner() lookup.
func TestTabHubOwner(t *testing.T) {
	h := newTabHub()
	id := h.add("t")
	h.claim(id, "foo.go")
	if o, ok := h.owner("foo.go"); !ok || o != id {
		t.Errorf("want owner %d ok=true, got %d %v", id, o, ok)
	}
	if _, ok := h.owner("bar.go"); ok {
		t.Error("unclaimed file should have no owner")
	}
}

// TestTabHubTouched verifies touched() records files in list().
func TestTabHubTouched(t *testing.T) {
	h := newTabHub()
	id := h.add("t")
	h.touched(id, "a.go")
	h.touched(id, "b.go")
	tabs := h.list()
	if len(tabs[0].Files) != 2 {
		t.Errorf("want 2 files, got %d: %v", len(tabs[0].Files), tabs[0].Files)
	}
}

// TestTabHubRemoveReleasesClaims verifies that removing a tab releases its claims.
func TestTabHubRemoveReleasesClaims(t *testing.T) {
	h := newTabHub()
	id1 := h.add("t1")
	id2 := h.add("t2")
	h.claim(id1, "shared.go")
	h.remove(id1)
	// now tab2 should be able to claim it
	_, ok := h.claim(id2, "shared.go")
	if !ok {
		t.Error("after tab1 removed, tab2 should claim shared.go")
	}
}

// TestTabHubVersion verifies version increments on mutations.
func TestTabHubVersion(t *testing.T) {
	h := newTabHub()
	v0 := h.version()
	id := h.add("t")
	v1 := h.version()
	if v1 <= v0 {
		t.Error("version should increase after add")
	}
	h.rename(id, "new")
	v2 := h.version()
	if v2 <= v1 {
		t.Error("version should increase after rename")
	}
	h.setStatus(id, "working", "task")
	v3 := h.version()
	if v3 <= v2 {
		t.Error("version should increase after setStatus")
	}
	h.remove(id)
	v4 := h.version()
	if v4 <= v3 {
		t.Error("version should increase after remove")
	}
}

// TestTabHubWait verifies long-poll wait returns true when version advances.
func TestTabHubWait(t *testing.T) {
	h := newTabHub()
	since := h.version()

	go func() {
		time.Sleep(20 * time.Millisecond)
		h.add("new tab")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if !h.wait(ctx, since, 2*time.Second) {
		t.Error("wait should return true when version advances")
	}
}

// TestTabHubWaitTimeout verifies wait returns false on timeout.
func TestTabHubWaitTimeout(t *testing.T) {
	h := newTabHub()
	since := h.version()
	ctx := context.Background()
	if h.wait(ctx, since, 20*time.Millisecond) {
		t.Error("wait should return false on timeout with no changes")
	}
}

// TestTabHubWaitAlreadyAhead verifies wait returns immediately when already ahead.
func TestTabHubWaitAlreadyAhead(t *testing.T) {
	h := newTabHub()
	h.add("t")
	ctx := context.Background()
	// since=0 is behind current version
	if !h.wait(ctx, 0, time.Second) {
		t.Error("wait should return true immediately when already ahead of since")
	}
}

// TestTabHubBriefEmpty verifies brief returns "" when alone.
func TestTabHubBriefEmpty(t *testing.T) {
	h := newTabHub()
	id := h.add("solo")
	if b := h.brief(id); b != "" {
		t.Errorf("brief should be empty when alone, got %q", b)
	}
}

// TestTabHubBriefContent verifies brief lists other tabs.
func TestTabHubBriefContent(t *testing.T) {
	h := newTabHub()
	id1 := h.add("router fix")
	id2 := h.add("tests")
	h.setStatus(id2, "working", "adding unit tests")
	h.touched(id2, "foo_test.go")

	b := h.brief(id1)
	if b == "" {
		t.Fatal("brief should not be empty with 2 tabs")
	}
	if !strings.Contains(b, "tests") {
		t.Errorf("brief should mention tab name 'tests', got: %q", b)
	}
	if !strings.Contains(b, "working") {
		t.Errorf("brief should mention status 'working', got: %q", b)
	}
	if !strings.Contains(b, "foo_test.go") {
		t.Errorf("brief should mention touched file, got: %q", b)
	}
	// self should not appear
	if strings.Contains(b, "router fix") {
		t.Errorf("brief should not mention self tab, got: %q", b)
	}
}

// TestTabHubBriefSizeCap verifies brief stays under ~600 bytes.
func TestTabHubBriefSizeCap(t *testing.T) {
	h := newTabHub()
	self := h.add("self")
	// add many tabs with long names and many files
	for i := 0; i < 20; i++ {
		id := h.add("a very long tab name that goes on and on")
		h.setStatus(id, "working", "doing a very long task description that is quite verbose")
		for j := 0; j < 10; j++ {
			h.touched(id, "some/very/long/path/to/file_that_is_quite_long.go")
		}
	}
	b := h.brief(self)
	if len(b) > 620 {
		t.Errorf("brief too long: %d bytes (want ≤620)", len(b))
	}
}

// TestTabHubConcurrency verifies thread safety under concurrent access (-race).
func TestTabHubConcurrency(t *testing.T) {
	h := newTabHub()
	var wg sync.WaitGroup
	const goroutines = 20
	const ops = 50

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := h.add("tab")
			for i := 0; i < ops; i++ {
				h.setStatus(id, "working", "task")
				h.touched(id, "file.go")
				h.claim(id, "shared.go")
				h.list()
				h.version()
				h.brief(id)
				h.owner("shared.go")
				h.release(id)
			}
			h.remove(id)
		}(g)
	}
	wg.Wait()
}

// TestClaimNoRecordAfterRemove: claim must not record a path for a tab that has
// already been removed (regression for the ghost-claim bug).
func TestClaimNoRecordAfterRemove(t *testing.T) {
	h := newTabHub()
	id := h.add("worker")
	h.remove(id)

	// Claim after remove: should succeed (ok=true) but must not persist the claim.
	owner, ok := h.claim(id, "ghost.go")
	if !ok {
		t.Errorf("claim after remove should return ok=true, got ok=false owner=%d", owner)
	}
	// The path must not be recorded in claims.
	if _, exists := h.owner("ghost.go"); exists {
		t.Error("claim after remove must not persist in the claims map")
	}
}

// TestWaitLeakCleanup: a timed-out or cancelled wait must remove its channel
// from the waiters slice so it doesn't accumulate (regression for waiter leak).
func TestWaitLeakCleanup(t *testing.T) {
	h := newTabHub()
	h.add("t")
	since := h.version()

	// Timeout path: wait with a very short duration.
	h.wait(context.Background(), since, time.Millisecond)

	h.mu.Lock()
	nAfterTimeout := len(h.waiters)
	h.mu.Unlock()
	if nAfterTimeout != 0 {
		t.Errorf("after timeout, waiters should be empty, got %d", nAfterTimeout)
	}

	// Context-cancel path.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	h.wait(ctx, since, time.Second)

	h.mu.Lock()
	nAfterCancel := len(h.waiters)
	h.mu.Unlock()
	if nAfterCancel != 0 {
		t.Errorf("after ctx cancel, waiters should be empty, got %d", nAfterCancel)
	}
}

// TestTabHubBriefOneOther verifies brief with exactly one other tab (no self).
func TestTabHubBriefOneOther(t *testing.T) {
	h := newTabHub()
	id1 := h.add("main work")
	id2 := h.add("side task")
	h.setStatus(id2, "idle", "")

	b1 := h.brief(id1)
	if b1 == "" {
		t.Error("tab1 brief should mention tab2")
	}
	b2 := h.brief(id2)
	if b2 == "" {
		t.Error("tab2 brief should mention tab1")
	}
	// each brief should not mention itself
	if strings.Contains(b1, "main work") {
		t.Error("tab1 brief should not mention itself")
	}
	if strings.Contains(b2, "side task") {
		t.Error("tab2 brief should not mention itself")
	}
}

// TestTabHubBriefUsesPositions verifies that brief() uses 1-based tab-bar
// positions (via posOf) rather than internal hub IDs.
func TestTabHubBriefUsesPositions(t *testing.T) {
	h := newTabHub()
	id1 := h.add("alpha")
	id2 := h.add("beta")
	id3 := h.add("gamma")

	// Remove id2 so positions shift: alpha=1, gamma=2 in the bar.
	h.remove(id2)

	// Wire posOf: alpha→1, gamma→2.
	h.mu.Lock()
	h.posOf = func(id int) int {
		switch id {
		case id1:
			return 1
		case id3:
			return 2
		}
		return id
	}
	h.mu.Unlock()

	// From alpha's perspective, brief should say "tab 2" for gamma (bar pos),
	// not "tab 3" (hub ID).
	b := h.brief(id1)
	if !strings.Contains(b, "tab 2") {
		t.Errorf("brief should use bar position 2 for gamma, got: %q", b)
	}
	if strings.Contains(b, "tab 3") {
		t.Errorf("brief must not use hub ID 3 for gamma, got: %q", b)
	}
}

// TestSyncHubPositions verifies that addTab and closeTab keep hub.posOf in sync.
func TestSyncHubPositions(t *testing.T) {
	m := tabTestModel(t, 100, 30, 3)
	// After 3 tabs: positions should be 1,2,3 for hub IDs 1,2,3.
	m.hub.mu.Lock()
	posOf := m.hub.posOf
	m.hub.mu.Unlock()
	if posOf == nil {
		t.Fatal("posOf not set after addTab")
	}
	for i, tb := range m.tabs {
		if got := posOf(tb.id); got != i+1 {
			t.Errorf("tab %d (id=%d): posOf=%d, want %d", i, tb.id, got, i+1)
		}
	}

	// Close the middle tab; positions should update.
	m.switchTo(1)
	m.closeTab(true)
	m.hub.mu.Lock()
	posOf = m.hub.posOf
	m.hub.mu.Unlock()
	for i, tb := range m.tabs {
		if got := posOf(tb.id); got != i+1 {
			t.Errorf("after close, tab %d (id=%d): posOf=%d, want %d", i, tb.id, got, i+1)
		}
	}
}
