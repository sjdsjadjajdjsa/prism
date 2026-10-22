package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tool(t *testing.T, dir, name, args string) toolResult {
	t.Helper()
	return runTool(context.Background(), dir, name, args)
}

func TestTools(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "crlf.txt"), []byte("x = 1\r\ny = 2\r\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "bin"), []byte{0, 1, 2}, 0o644)
	os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755)

	cases := []struct {
		name, tool, args string
		ok               bool
		has              string
	}{
		{"read offset", "read", `{"path":"a.txt","offset":2,"limit":1}`, true, "two"},
		{"read past end", "read", `{"path":"a.txt","offset":9}`, false, "past the end"},
		{"read binary", "read", `{"path":"bin"}`, false, "binary"},
		{"read folder", "read", `{"path":"node_modules"}`, false, "folder"},
		{"read outside", "read", `{"path":"../../etc/passwd"}`, false, "outside"},
		{"read absolute outside", "read", `{"path":"/etc/hostname"}`, false, "outside"},
		{"list missing", "list", `{"path":"nope"}`, false, "no such file"},
		{"list file", "list", `{"path":"a.txt"}`, true, "a.txt"},
		{"list skips node_modules", "list", `{}`, true, "node_modules/"},
		{"grep none", "grep", `{"pattern":"zzz"}`, true, "No matches"},
		{"edit missing", "edit", `{"path":"a.txt","old_string":"zzz","new_string":"y"}`, false, "not found"},
		{"edit crlf", "edit", `{"path":"crlf.txt","old_string":"x = 1\ny = 2","new_string":"x = 3\ny = 4"}`, true, "Edited"},
		{"write nested", "write", `{"path":"deep/new.txt","content":"hi\n"}`, true, "Wrote"},
		{"write outside", "write", `{"path":"../x.txt","content":"no"}`, false, "outside"},
		{"bash exit", "bash", `{"command":"exit 3"}`, false, "exit code 3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := tool(t, dir, c.tool, c.args)
			if r.ok != c.ok || !strings.Contains(strings.ToLower(r.output), strings.ToLower(c.has)) {
				t.Fatalf("ok=%v want %v, output %q should contain %q", r.ok, c.ok, r.output, c.has)
			}
		})
	}
	data, _ := os.ReadFile(filepath.Join(dir, "crlf.txt"))
	if string(data) != "x = 3\r\ny = 4\r\n" {
		t.Fatalf("crlf edit gave %q", data)
	}
}

func TestEditReplaceAll(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f"), []byte("a a a\n"), 0o644)
	if r := tool(t, dir, "edit", `{"path":"f","old_string":"a","new_string":"b"}`); r.ok {
		t.Fatal("ambiguous edit should fail")
	}
	r := tool(t, dir, "edit", `{"path":"f","old_string":"a","new_string":"b","replace_all":true}`)
	data, _ := os.ReadFile(filepath.Join(dir, "f"))
	if !r.ok || string(data) != "b b b\n" || r.added != 1 || r.removed != 1 {
		t.Fatalf("got %q %+v", data, r)
	}
}

func TestTrailingNewlineDiff(t *testing.T) {
	r := changed("a\nb", "a\nb\n")
	if r.added+r.removed == 0 {
		t.Fatal("a newline-only change should still show in the diff")
	}
}

func TestBashBackgroundAndTimeout(t *testing.T) {
	start := time.Now()
	r := tool(t, t.TempDir(), "bash", `{"command":"echo hi; sleep 30 &"}`)
	if !r.ok || time.Since(start) > 6*time.Second {
		t.Fatalf("background: ok=%v took %v", r.ok, time.Since(start))
	}
	start = time.Now()
	r = tool(t, t.TempDir(), "bash", `{"command":"sleep 30","timeout":1}`)
	if r.ok || time.Since(start) > 6*time.Second || !strings.Contains(r.output, "timeout") {
		t.Fatalf("timeout: ok=%v took %v out %q", r.ok, time.Since(start), r.output)
	}
}

// TestSymlinkEscape verifies that a symlink inside the project pointing outside
// cannot be used to read or write files outside the project root.
func TestSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	// Create a symlink inside the project that points to /tmp (outside).
	symTarget := t.TempDir() // a real dir outside `dir`
	os.WriteFile(filepath.Join(symTarget, "secret.txt"), []byte("secret"), 0o644)
	os.Symlink(symTarget, filepath.Join(dir, "escape"))

	// Reading through the symlink should be blocked.
	r := tool(t, dir, "read", `{"path":"escape/secret.txt"}`)
	if r.ok || !strings.Contains(strings.ToLower(r.output), "outside") {
		t.Fatalf("symlink escape read: ok=%v output=%q", r.ok, r.output)
	}
	// Writing through the symlink should also be blocked.
	r = tool(t, dir, "write", `{"path":"escape/new.txt","content":"pwned"}`)
	if r.ok || !strings.Contains(strings.ToLower(r.output), "outside") {
		t.Fatalf("symlink escape write: ok=%v output=%q", r.ok, r.output)
	}
}

// TestBashRedirectedBackground verifies that `sleep 30 >/dev/null 2>&1 &`
// returns quickly (the shell exits immediately) and the background process
// is killed so it doesn't leak.
func TestBashRedirectedBackground(t *testing.T) {
	start := time.Now()
	r := tool(t, t.TempDir(), "bash", `{"command":"sleep 30 >/dev/null 2>&1 &"}`)
	elapsed := time.Since(start)
	if !r.ok {
		t.Fatalf("redirected background: ok=%v output=%q", r.ok, r.output)
	}
	if elapsed > 6*time.Second {
		t.Fatalf("redirected background took too long: %v", elapsed)
	}
	// The output should mention that a background process was stopped.
	if !strings.Contains(r.output, "background process was stopped") {
		t.Fatalf("expected background-stopped note, got: %q", r.output)
	}
}
