package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func viewerProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "src", "deep"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\nworld\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "src", "deep", "x.txt"), []byte("x"), 0o644))
	return dir
}

func TestProjectPathGuard(t *testing.T) {
	dir := viewerProject(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o644)
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Skip("no symlinks:", err)
	}
	os.Symlink(filepath.Join(dir, "README.md"), filepath.Join(dir, "inside-link"))

	for _, ok := range []string{"README.md", "src/main.go", "./src/deep/x.txt", filepath.Join(dir, "README.md"), "inside-link"} {
		if _, _, err := projectPath(dir, ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"../x", "src/../../x", "src/../README.md", "/etc/passwd", filepath.Join(outside, "secret"), "escape/secret", "escape", "", `..\x`} {
		if _, _, err := projectPath(dir, bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, rel, _ := projectPath(dir, "./src/main.go"); rel != "src/main.go" {
		t.Errorf("rel = %q", rel)
	}
	anyPath = true
	defer func() { anyPath = false }()
	if _, _, err := projectPath(dir, "escape/secret"); !errors.Is(err, errOutside) {
		t.Errorf("--any-path must not widen the viewer: %v", err)
	}
}

func TestReadProjectFileBinaryAndSize(t *testing.T) {
	dir := viewerProject(t)
	os.WriteFile(filepath.Join(dir, "bin"), []byte{0x7f, 'E', 'L', 'F', 0, 1, 2}, 0o644)
	os.WriteFile(filepath.Join(dir, "big"), make([]byte, viewLimit+1), 0o644)

	fc, err := readProjectFile(dir, "README.md")
	if err != nil || fc.note != "" || fc.text != "hello\nworld\n" {
		t.Fatalf("text: %+v %v", fc, err)
	}
	if fc, err := readProjectFile(dir, "bin"); err != nil || !strings.Contains(fc.note, "binary") || fc.text != "" {
		t.Errorf("binary: %+v %v", fc, err)
	}
	if fc, err := readProjectFile(dir, "big"); err != nil || !strings.Contains(fc.note, "too large") || fc.text != "" {
		t.Errorf("big: %+v %v", fc, err)
	}
	if _, err := readProjectFile(dir, "src"); err == nil {
		t.Error("folder accepted")
	}
	if _, err := readProjectFile(dir, "missing.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
	if _, err := readProjectFile(dir, "../x"); !errors.Is(err, errOutside) {
		t.Errorf("escape: %v", err)
	}
	if strings.ContainsAny(strings.Join(viewLines("a\x1b[2Jb\r\n"), ""), "\x1b\r") {
		t.Error("control characters reach the terminal")
	}
}

func TestViewerScrollBounds(t *testing.T) {
	v := newViewer(fileContent{rel: "f", text: strings.Repeat("line\n", 50)})
	if len(v.lines) != 50 {
		t.Fatalf("lines = %d", len(v.lines))
	}
	v.scroll(-5, 10)
	if v.top != 0 {
		t.Errorf("above start: %d", v.top)
	}
	v.scroll(1000, 10)
	if v.top != 40 {
		t.Errorf("past end: %d, want 40", v.top)
	}
	v.scroll(-10, 10)
	if v.top != 30 {
		t.Errorf("page up: %d", v.top)
	}
	short := newViewer(fileContent{rel: "s", text: "a\nb\n"})
	short.scroll(5, 10)
	if short.top != 0 {
		t.Errorf("short file scrolled: %d", short.top)
	}
	if e := newViewer(fileContent{rel: "e"}); e.note == "" {
		t.Error("empty file has no note")
	}
	if b := newViewer(fileContent{rel: "b", note: "binary file"}); len(b.lines) != 0 || b.note == "" {
		t.Error("note file should have no lines")
	}
}

func TestFilesRowsTreeAndFilter(t *testing.T) {
	f := filesState{open: true, expanded: map[string]bool{}, nodes: []treeNode{
		{Path: "README.md"}, {Path: "src", Dir: true}, {Path: "src/main.go"},
		{Path: "src/deep", Dir: true}, {Path: "src/deep/x.txt"}, {Path: "a.txt"},
	}}
	paths := func(rows []fileRow) string {
		var s []string
		for _, r := range rows {
			s = append(s, r.path)
		}
		return strings.Join(s, ",")
	}
	if got := paths(f.rows()); got != "src,README.md,a.txt" {
		t.Errorf("collapsed: %s", got)
	}
	f.expanded["src"] = true
	if got := paths(f.rows()); got != "src,src/deep,src/main.go,README.md,a.txt" {
		t.Errorf("expanded: %s", got)
	}
	if r := f.rows()[1]; r.depth != 1 || !r.dir || r.open {
		t.Errorf("src/deep row: %+v", r)
	}
	f.filter = []rune("X.T")
	if got := paths(f.rows()); got != "src/deep/x.txt" {
		t.Errorf("filter: %s", got)
	}
	f.filter = []rune("main")
	if got := paths(f.rows()); got != "src/main.go" {
		t.Errorf("filter main: %s", got)
	}
	f.filter = []rune("nothing")
	if len(f.rows()) != 0 {
		t.Error("filter should match nothing")
	}
}

func TestViewerLinesClipAtNarrowWidth(t *testing.T) {
	v := newViewer(fileContent{rel: "f", text: strings.Repeat("x", 500) + "\n"})
	lines := viewerLines(themes[0], v, 54, 20)
	for _, l := range lines[1:] {
		n := 0
		for _, s := range l.segs {
			n += len([]rune(s.text))
		}
		if n > 54 {
			t.Errorf("line is %d wide, want ≤ 54", n)
		}
	}
}
