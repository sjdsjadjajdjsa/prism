package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func sampleProject(t *testing.T) string {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		".gitignore":          "*.log\nsecret/\n/build-out\n!keep.log\n",
		"app.py":              "from lib.util import helper\nimport lib.db\n\ndef main():\n    helper()\n\nclass App:\n    pass\n",
		"lib/__init__.py":     "",
		"lib/util.py":         "def helper():\n    return 1\n",
		"lib/db.py":           "from .util import helper\n\nasync def connect():\n    pass\n",
		"web/index.html":      "<html><head><title>Shop</title><script src=\"app.js\"></script><link href=\"style.css\" rel=stylesheet></head></html>",
		"web/app.js":          "import { cart } from './cart.js'\nexport function start() {}\nconst go = () => 1\n",
		"web/cart.js":         "export const cart = []\nexport class Cart {}\n",
		"web/style.css":       "body{}",
		"debug.log":           "noise",
		"keep.log":            "kept",
		"secret/key.txt":      "x",
		"build-out/a.txt":     "x",
		"node_modules/x/i.js": "x",
		".env":                "TOKEN=1",
		"README.md":           "# Shop\n## Setup\ntext\n",
	})
	return dir
}

func treeOf(t *testing.T, dir string) *projectTree {
	tr := newProjectTree(dir)
	tr.scan()
	return tr
}

func TestTreeScanIgnoresAndParses(t *testing.T) {
	tr := treeOf(t, sampleProject(t))
	for _, gone := range []string{"debug.log", "secret", "secret/key.txt", "build-out", "node_modules", ".env", ".gitignore"} {
		if tr.nodes[gone] != nil && gone != ".gitignore" {
			t.Errorf("%s should be ignored", gone)
		}
	}
	for _, want := range []string{"keep.log", "app.py", "lib", "lib/db.py", "web/cart.js", "README.md"} {
		if tr.nodes[want] == nil {
			t.Errorf("%s missing", want)
		}
	}
	app := tr.nodes["app.py"]
	if strings.Join(app.Symbols, ",") != "def main,class App" {
		t.Errorf("app.py symbols = %v", app.Symbols)
	}
	if strings.Join(app.Imports, ",") != "lib/db.py,lib/util.py" {
		t.Errorf("app.py imports = %v", app.Imports)
	}
	if got := tr.nodes["lib/db.py"].Imports; len(got) != 1 || got[0] != "lib/util.py" {
		t.Errorf("relative python import = %v", got)
	}
	if got := strings.Join(tr.nodes["web/index.html"].Imports, ","); got != "web/app.js,web/style.css" {
		t.Errorf("html imports = %q", got)
	}
	if got := tr.nodes["web/app.js"].Imports; len(got) != 1 || got[0] != "web/cart.js" {
		t.Errorf("js imports = %v", got)
	}
	util := tr.nodes["lib/util.py"]
	if util.Rank != 1 || len(util.ImportedBy) != 2 {
		t.Errorf("lib/util.py rank %v importedBy %v; want the top rank and 2 users", util.Rank, util.ImportedBy)
	}
	if tr.nodes["README.md"].Rank != 0 {
		t.Error("an unused file has no rank")
	}
	if s := tr.nodes["web/app.js"].Symbols; strings.Join(s, ",") != "function start,const go" {
		t.Errorf("js symbols = %v", s)
	}
}

func TestTreeRefreshIsIncremental(t *testing.T) {
	dir := sampleProject(t)
	tr := treeOf(t, dir)
	v := tr.version
	writeFiles(t, dir, map[string]string{"web/new/thing.js": "import './../cart.js'\nexport function thing() {}\n"})
	os.Remove(filepath.Join(dir, "lib", "db.py"))
	done := make(chan bool)
	go func() { done <- tr.wait(context.Background(), v, 5*time.Second) }()
	tr.refresh([]string{"web/new", "lib/db.py"})
	if !<-done {
		t.Fatal("wait did not wake on refresh")
	}
	if tr.nodes["web/new/thing.js"] == nil || tr.nodes["lib/db.py"] != nil {
		t.Fatalf("refresh missed changes: thing=%v db=%v", tr.nodes["web/new/thing.js"], tr.nodes["lib/db.py"])
	}
	if got := strings.Join(tr.nodes["web/cart.js"].ImportedBy, ","); got != "web/app.js,web/new/thing.js" {
		t.Errorf("importedBy after refresh = %q", got)
	}
	if tr.version != v+1 {
		t.Errorf("version %d, want %d", tr.version, v+1)
	}
}

func TestTreeWatchSeesChanges(t *testing.T) {
	dir := sampleProject(t)
	tr := treeOf(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tr.watch(ctx)
	time.Sleep(100 * time.Millisecond)
	steps := []struct {
		do    func()
		check func() bool
		what  string
	}{
		{func() {
			writeFiles(t, dir, map[string]string{"lib/extra.py": "from .util import helper\ndef extra(): pass\n"})
		},
			func() bool { n := tr.nodes["lib/extra.py"]; return n != nil && len(n.Symbols) == 1 }, "new file"},
		{func() { writeFiles(t, dir, map[string]string{"newdir/deep/x.js": "export function x() {}\n"}) },
			func() bool { return tr.nodes["newdir/deep/x.js"] != nil }, "new nested folder"},
		{func() { writeFiles(t, dir, map[string]string{"newdir/deep/y.js": "import './x.js'\n"}) },
			func() bool { n := tr.nodes["newdir/deep/x.js"]; return n != nil && len(n.ImportedBy) == 1 }, "file in a folder created after the watch began"},
		{func() { os.Remove(filepath.Join(dir, "app.py")) }, func() bool { return tr.nodes["app.py"] == nil }, "delete"},
	}
	for _, s := range steps {
		tr.mu.Lock()
		v := tr.version
		tr.mu.Unlock()
		s.do()
		deadline := time.Now().Add(5 * time.Second)
		for {
			tr.wait(context.Background(), v, 200*time.Millisecond)
			tr.mu.Lock()
			ok := s.check()
			tr.mu.Unlock()
			if ok {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("watch missed: %s", s.what)
			}
		}
	}
	writeFiles(t, dir, map[string]string{"x.log": "ignored"})
	time.Sleep(400 * time.Millisecond)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.nodes["x.log"] != nil {
		t.Error("ignored file was added")
	}
}

func TestTreeGitStatus(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := sampleProject(t)
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=a@b", "-c", "user.name=a"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	run("add", ".")
	run("commit", "-qm", "x")
	writeFiles(t, dir, map[string]string{"app.py": "def main(): pass\n", "new.py": "x = 1\n"})
	os.Remove(filepath.Join(dir, "README.md"))
	tr := treeOf(t, dir)
	for p, want := range map[string]string{"app.py": "M", "new.py": "?", "README.md": "D", "web/app.js": ""} {
		if n := tr.nodes[p]; n == nil || n.Git != want {
			t.Errorf("%s git = %+v, want %q", p, n, want)
		}
	}
}

func TestTreePromptMapAndAscii(t *testing.T) {
	if got := treeOf(t, t.TempDir()).promptMap(promptMapBudget); !strings.Contains(got, "the folder is empty") {
		t.Errorf("empty: %q", got)
	}
	tr := treeOf(t, sampleProject(t))
	m := tr.promptMap(promptMapBudget)
	for _, want := range []string{"lib/", "  util.py (", "util.py (27 B) ★: def helper", "app.py (", "def main"} {
		if !strings.Contains(m, want) {
			t.Errorf("prompt map lacks %q:\n%s", want, m)
		}
	}
	big := t.TempDir()
	files := map[string]string{}
	for i := range 400 {
		files[fmt.Sprintf("pkg%d/f%03d.go", i%8, i)] = "package p\nfunc F() {}\n"
	}
	writeFiles(t, big, files)
	bm := treeOf(t, big).promptMap(promptMapBudget)
	if len(bm) > promptMapBudget+300 || !strings.Contains(bm, "files") {
		t.Errorf("big map is %d chars:\n%s", len(bm), bm)
	}
	lines := tr.asciiTree("", map[string]string{"web/app.js": "edit", "app.py": "read"}, 100)
	text := strings.Join(lines, "\n")
	for _, want := range []string{"├─ lib/", "│  └─ util.py ★", "app.js ●", "app.py ◦"} {
		if !strings.Contains(text, want) {
			t.Errorf("ascii lacks %q:\n%s", want, text)
		}
	}
	if f := strings.Join(tr.asciiTree("cart", nil, 100), "\n"); !strings.Contains(f, "cart.js") || strings.Contains(f, "util.py") {
		t.Errorf("filter: %s", f)
	}
}

func TestTreeSnapshotJSON(t *testing.T) {
	tr := treeOf(t, sampleProject(t))
	tr.agent = func() map[string]string { return map[string]string{"app.py": "edit"} }
	var got struct {
		Version int
		Stats   map[string]int
		Nodes   []treeNode
	}
	if err := json.Unmarshal(tr.json(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Stats["touched"] != 1 || got.Stats["files"] < 8 || got.Version != 1 {
		t.Errorf("stats %v version %d", got.Stats, got.Version)
	}
	for i := 1; i < len(got.Nodes); i++ {
		if got.Nodes[i-1].Path >= got.Nodes[i].Path {
			t.Fatal("nodes not sorted by path")
		}
	}
}

func TestPromptBudget(t *testing.T) {
	testEnv(t)
	dir := sampleProject(t)
	a := newAgent("http://x", dir, dir)
	a.useMap()
	if !strings.Contains(a.messages[0].Content, "util.py") {
		t.Fatal("the first request's system prompt lacks the project map")
	}
	tools, _ := json.Marshal(a.tools())
	total := len(tools) + len(a.systemPrompt())
	t.Logf("request prefix: tools %d B + system %d B = %d B (~%d tokens)", len(tools), len(a.systemPrompt()), total, total/4)
	if total > 10000 {
		t.Errorf("static prefix grew to %d bytes; keep tools + system prompt under 10000", total)
	}
}
