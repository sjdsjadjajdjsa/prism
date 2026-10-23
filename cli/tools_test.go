package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

// TestToolsPathEscapeRejected checks every file tool refuses paths that leave
// the project, including look-alike sibling folders and dotdot tricks, and
// still accepts paths that only appear to leave but resolve back inside.
func TestToolsPathEscapeRejected(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "in.txt"), []byte("inside\n"), 0o644)
	sibling := dir + "-evil"
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sibling) })
	os.WriteFile(filepath.Join(sibling, "s.txt"), []byte("secret\n"), 0o644)
	siblingRel := "../" + filepath.Base(sibling) + "/s.txt"
	siblingAbs := filepath.Join(sibling, "s.txt")
	inAbs := filepath.Join(dir, "in.txt")

	cases := []struct {
		name, tool, args string
		ok               bool
		has              string
	}{
		{"read sibling prefix", "read", `{"path":"` + siblingRel + `"}`, false, "outside"},
		{"read sibling absolute", "read", `{"path":"` + siblingAbs + `"}`, false, "outside"},
		{"read parent", "read", `{"path":".."}`, false, "outside"},
		{"read dotdot inside", "read", `{"path":"sub/../in.txt"}`, true, "inside"},
		{"read absolute inside", "read", `{"path":"` + inAbs + `"}`, true, "inside"},
		{"edit outside", "edit", `{"path":"` + siblingRel + `","old_string":"secret","new_string":"x"}`, false, "outside"},
		{"edit absolute outside", "edit", `{"path":"` + siblingAbs + `","old_string":"secret","new_string":"x"}`, false, "outside"},
		{"write absolute outside", "write", `{"path":"` + filepath.Join(sibling, "w.txt") + `","content":"no"}`, false, "outside"},
		{"write deep dotdot", "write", `{"path":"a/b/../../../w.txt","content":"no"}`, false, "outside"},
		{"list parent", "list", `{"path":".."}`, false, "outside"},
		{"list root", "list", `{"path":"/"}`, false, "outside"},
		{"grep outside", "grep", `{"pattern":"secret","path":"` + siblingRel + `"}`, false, "outside"},
		{"grep parent", "grep", `{"pattern":"secret","path":".."}`, false, "outside"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := tool(t, dir, c.tool, c.args)
			if r.ok != c.ok || !strings.Contains(strings.ToLower(r.output), strings.ToLower(c.has)) {
				t.Fatalf("ok=%v want %v, output %q should contain %q", r.ok, c.ok, r.output, c.has)
			}
		})
	}
	if data, _ := os.ReadFile(siblingAbs); string(data) != "secret\n" {
		t.Fatalf("file outside the project was changed: %q", data)
	}
	for _, p := range []string{filepath.Join(sibling, "w.txt"), filepath.Join(filepath.Dir(dir), "w.txt")} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("write escaped the project: %s exists", p)
		}
	}
}

// TestToolsPathEscapeAnyPath checks the anyPath switch lifts the restriction.
func TestToolsPathEscapeAnyPath(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(other, "o.txt"), []byte("other\n"), 0o644)
	if !outside(dir, filepath.Join(other, "o.txt")) {
		t.Fatal("outside should be true for another temp dir")
	}
	anyPath = true
	t.Cleanup(func() { anyPath = false })
	r := tool(t, dir, "read", `{"path":"`+filepath.Join(other, "o.txt")+`"}`)
	if !r.ok || !strings.Contains(r.output, "other") {
		t.Fatalf("anyPath read: ok=%v output=%q", r.ok, r.output)
	}
}

// TestToolsEditReplaceAllCases covers replace_all on multi-line text, CRLF
// files, deletions, single matches and the error branches around it.
func TestToolsEditReplaceAllCases(t *testing.T) {
	cases := []struct {
		name, content, args string
		ok                  bool
		has, want           string
	}{
		{"multi-line", "x\ny\nmid\nx\ny\n", `{"path":"f","old_string":"x\ny","new_string":"z","replace_all":true}`, true, "Edited", "z\nmid\nz\n"},
		{"crlf", "k = 1\r\nk = 1\r\n", `{"path":"f","old_string":"k = 1\n","new_string":"k = 2\n","replace_all":true}`, true, "Edited", "k = 2\r\nk = 2\r\n"},
		{"delete every match", "a-b-c", `{"path":"f","old_string":"-","new_string":"","replace_all":true}`, true, "Edited", "abc"},
		{"single match", "only once\n", `{"path":"f","old_string":"once","new_string":"twice","replace_all":true}`, true, "Edited", "only twice\n"},
		{"explicit false ambiguous", "a a\n", `{"path":"f","old_string":"a","new_string":"b","replace_all":false}`, false, "appears 2 times", "a a\n"},
		{"non-bool ignored", "a a\n", `{"path":"f","old_string":"a","new_string":"b","replace_all":"yes"}`, false, "appears 2 times", "a a\n"},
		{"not found", "abc\n", `{"path":"f","old_string":"zzz","new_string":"b","replace_all":true}`, false, "not found", "abc\n"},
		{"empty old_string", "abc\n", `{"path":"f","old_string":"","new_string":"b","replace_all":true}`, false, "old_string is empty", "abc\n"},
		{"overlapping", "aaaa", `{"path":"f","old_string":"aa","new_string":"b","replace_all":true}`, true, "Edited", "bb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "f"), []byte(c.content), 0o644)
			r := tool(t, dir, "edit", c.args)
			if r.ok != c.ok || !strings.Contains(r.output, c.has) {
				t.Fatalf("ok=%v want %v, output %q should contain %q", r.ok, c.ok, r.output, c.has)
			}
			if data, _ := os.ReadFile(filepath.Join(dir, "f")); string(data) != c.want {
				t.Fatalf("file is %q, want %q", data, c.want)
			}
		})
	}
}

func TestToolsEditReplaceAllMissingFile(t *testing.T) {
	r := tool(t, t.TempDir(), "edit", `{"path":"nope.txt","old_string":"a","new_string":"b","replace_all":true}`)
	if r.ok || !strings.Contains(r.output, "nope.txt") {
		t.Fatalf("missing file: ok=%v output=%q", r.ok, r.output)
	}
}

// TestToolsBashTimeoutCases covers string timeouts, keeping output produced
// before the timeout, and the timeout not flagging a non-zero exit summary.
func TestToolsBashTimeoutCases(t *testing.T) {
	cases := []struct {
		name, args string
		ok         bool
		has        []string
	}{
		{"string timeout", `{"command":"sleep 30","timeout":"1"}`, false, []string{"[stopped after 1s timeout]"}},
		{"output kept", `{"command":"echo before; sleep 30","timeout":1}`, false, []string{"before", "1s timeout"}},
		{"fast under timeout", `{"command":"echo quick","timeout":1}`, true, []string{"quick", "[exit code 0]"}},
		{"bad timeout falls back", `{"command":"echo ok","timeout":-5}`, true, []string{"ok", "[exit code 0]"}},
		{"empty command", `{"command":"   "}`, false, []string{"command is empty"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start := time.Now()
			r := tool(t, t.TempDir(), "bash", c.args)
			if elapsed := time.Since(start); elapsed > 6*time.Second {
				t.Fatalf("took %v", elapsed)
			}
			if r.ok != c.ok {
				t.Fatalf("ok=%v want %v, output %q", r.ok, c.ok, r.output)
			}
			for _, h := range c.has {
				if !strings.Contains(r.output, h) {
					t.Fatalf("output %q should contain %q", r.output, h)
				}
			}
			if strings.Contains(r.output, "timeout]") && strings.HasPrefix(r.summary, "exit ") {
				t.Fatalf("timeout summary should not report an exit code: %q", r.summary)
			}
		})
	}
}

func TestToolsBashStoppedByUser(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	r := runTool(ctx, t.TempDir(), "bash", `{"command":"sleep 30","timeout":20}`)
	if r.ok || time.Since(start) > 6*time.Second || !strings.Contains(r.output, "[stopped by the user]") {
		t.Fatalf("cancel: ok=%v took %v output=%q", r.ok, time.Since(start), r.output)
	}
}

// TestToolsBashBackgroundCases covers background processes that keep the
// output pipe open (WaitDelay path) and those that do not, with exit codes.
func TestToolsBashBackgroundCases(t *testing.T) {
	cases := []struct {
		name, command string
		ok            bool
		has           []string
	}{
		{"pipe held open", "echo hi; sleep 30 &", true, []string{"hi", "[exit code 0]", "background process was stopped"}},
		// A non-zero exit wins over ErrWaitDelay in cmd.Run, so no background note here.
		{"pipe held, non-zero exit", "sleep 30 & exit 4", false, []string{"[exit code 4]"}},
		{"redirected, non-zero exit", "sleep 30 >/dev/null 2>&1 & exit 5", false, []string{"[exit code 5]"}},
		{"no background", "echo plain", true, []string{"plain", "[exit code 0]"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start := time.Now()
			r := tool(t, t.TempDir(), "bash", `{"command":"`+c.command+`","timeout":20}`)
			if elapsed := time.Since(start); elapsed > 6*time.Second {
				t.Fatalf("took %v", elapsed)
			}
			if r.ok != c.ok {
				t.Fatalf("ok=%v want %v, output %q", r.ok, c.ok, r.output)
			}
			for _, h := range c.has {
				if !strings.Contains(r.output, h) {
					t.Fatalf("output %q should contain %q", r.output, h)
				}
			}
			if c.name == "no background" && strings.Contains(r.output, "background") {
				t.Fatalf("plain command should not mention background: %q", r.output)
			}
		})
	}
}

func TestToolOutputCaps(t *testing.T) {
	dir := t.TempDir()
	var py strings.Builder
	for i := 1; i <= 1200; i++ {
		switch {
		case i%100 == 1:
			fmt.Fprintf(&py, "class C%d:\n", i)
		case i%50 == 2:
			fmt.Fprintf(&py, "    def f%d(self):\n", i)
		default:
			fmt.Fprintf(&py, "        x = %d\n", i)
		}
	}
	os.WriteFile(filepath.Join(dir, "big.py"), []byte(py.String()), 0o644)
	long := strings.Repeat("y", 400)
	for f := 0; f < 10; f++ {
		var b strings.Builder
		for i := 0; i < 30; i++ {
			fmt.Fprintf(&b, "needle %d %s\n", i, long)
		}
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("g%d.txt", f)), []byte(b.String()), 0o644)
	}

	cases := []struct {
		name, tool, args string
		ok               bool
		has, hasNot      []string
		check            func(t *testing.T, out string)
	}{
		{
			name: "bash keeps head and tail", tool: "bash",
			args: `{"command":"echo STAR; yes é | head -n 15000 | tr -d '\\n'; echo; echo END"}`,
			ok:   true, has: []string{"STAR\n", "END", "bytes omitted]", "[exit code 0]"},
			check: func(t *testing.T, out string) {
				if len(out) > 12500 {
					t.Fatalf("output %d bytes, want about 12000", len(out))
				}
				if !utf8.ValidString(out) {
					t.Fatal("output is not valid UTF-8")
				}
			},
		},
		{
			name: "bash short untouched", tool: "bash", args: `{"command":"echo hi; exit 2"}`,
			ok: false, has: []string{"hi", "[exit code 2]"}, hasNot: []string{"omitted"},
		},
		{
			name: "read long python shows outline", tool: "read", args: `{"path":"big.py"}`,
			ok: true, has: []string{"Outline", "class C1101", "def f1152(self)", "   500\t", "Read again with offset=501"},
			hasNot: []string{"   501\t        x"},
		},
		{
			name: "read with offset has no outline", tool: "read", args: `{"path":"big.py","offset":3}`,
			ok: true, has: []string{"     3\t", "offset=503"}, hasNot: []string{"Outline"},
		},
		{
			name: "read with limit has no outline", tool: "read", args: `{"path":"big.py","limit":10}`,
			ok: true, has: []string{"offset=11"}, hasNot: []string{"Outline"},
		},
		{
			name: "grep caps lines and columns", tool: "grep", args: `{"pattern":"needle"}`,
			ok: true, has: []string{"more matches not shown"},
			check: func(t *testing.T, out string) {
				lines := strings.Split(out, "\n")
				if len(lines) != 101 {
					t.Fatalf("got %d lines, want 100 + note", len(lines))
				}
				for _, l := range lines {
					if utf8.RuneCountInString(l) > 161+len("g0.txt:30:") {
						t.Fatalf("line too long (%d): %q", utf8.RuneCountInString(l), l)
					}
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := tool(t, dir, c.tool, c.args)
			if r.ok != c.ok {
				t.Fatalf("ok=%v want %v: %.500q", r.ok, c.ok, r.output)
			}
			for _, h := range c.has {
				if !strings.Contains(r.output, h) {
					t.Fatalf("output should contain %q: %.2000q", h, r.output)
				}
			}
			for _, h := range c.hasNot {
				if strings.Contains(r.output, h) {
					t.Fatalf("output should not contain %q", h)
				}
			}
			if c.check != nil {
				c.check(t, r.output)
			}
		})
	}
}

func TestCapGrepPerFile(t *testing.T) {
	var lines []string
	for f := 0; f < 3; f++ {
		for i := 1; i <= 30; i++ {
			lines = append(lines, fmt.Sprintf("f%d.go:%d:%s", f, i, strings.Repeat("é", 300)))
		}
	}
	cases := []struct {
		name    string
		perFile int
		kept    int
		dropped int
	}{
		{"fallback per file 20", 20, 60, 30},
		{"no per-file cap", 0, 90, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := capGrep(lines, c.perFile)
			kept := len(out)
			if c.dropped > 0 {
				kept--
				if want := fmt.Sprintf("… %d more matches", c.dropped); !strings.HasPrefix(out[len(out)-1], want) {
					t.Fatalf("note %q, want prefix %q", out[len(out)-1], want)
				}
			}
			if kept != c.kept {
				t.Fatalf("kept %d, want %d", kept, c.kept)
			}
			for _, l := range out[:kept] {
				if n := utf8.RuneCountInString(l); n > grepMaxCols+1 || !utf8.ValidString(l) {
					t.Fatalf("bad line (%d runes)", n)
				}
			}
		})
	}
	big := make([]string, 250)
	for i := range big {
		big[i] = fmt.Sprintf("a.go:%d:x", i+1)
	}
	if out := capGrep(big, 0); len(out) != 101 || !strings.Contains(out[100], "150 more") {
		t.Fatalf("total cap: %d lines, last %q", len(out), out[len(out)-1])
	}
}
