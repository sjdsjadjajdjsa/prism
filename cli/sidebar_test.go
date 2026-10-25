package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func sideProject(t *testing.T) (*Agent, string) {
	t.Helper()
	dir := t.TempDir()
	for p, body := range map[string]string{
		"README.md":     "See [[ideas]] and [setup](docs/setup.md).\n![shot](docs/shot.png)\n",
		"docs/ideas.md": "# Ideas\n",
		"docs/setup.md": "# Setup\n",
		"docs/shot.png": "png",
		"src/main.go":   "package main\n",
	} {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte(body), 0o644)
	}
	tree := newProjectTree(dir)
	tree.scan()
	return &Agent{dir: dir, tree: tree}, dir
}

func TestMarkdownLinksInTheMap(t *testing.T) {
	a, _ := sideProject(t)
	got := map[string][]string{}
	for _, n := range a.tree.snapshot()["nodes"].([]treeNode) {
		got[n.Path] = n.Imports
	}
	if strings.Join(got["README.md"], ",") != "docs/ideas.md,docs/setup.md,docs/shot.png" {
		t.Fatalf("README links = %v", got["README.md"])
	}
}

func TestFileOps(t *testing.T) {
	a, dir := sideProject(t)
	if p, err := fileOp(a, "file", "notes/todo.md", ""); err != nil || p != "notes/todo.md" {
		t.Fatalf("new file: %q %v", p, err)
	}
	if _, err := fileOp(a, "file", "notes/todo.md", ""); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a second create should say it exists: %v", err)
	}
	if _, err := fileOp(a, "folder", "empty", ""); err != nil {
		t.Fatal(err)
	}
	if p, err := fileOp(a, "rename", "notes/todo.md", "notes/done.md"); err != nil || p != "notes/done.md" {
		t.Fatalf("rename: %q %v", p, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes/done.md")); err != nil {
		t.Fatal("renamed file missing")
	}
	if _, err := fileOp(a, "delete", "notes", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes")); !os.IsNotExist(err) {
		t.Fatal("folder not deleted")
	}
	for _, c := range []struct{ op, p, to string }{
		{"delete", "../outside", ""},
		{"delete", ".", ""},
		{"file", ".git/hooks/x", ""},
		{"rename", "README.md", "../README.md"},
		{"chmod", "README.md", ""},
	} {
		if _, err := fileOp(a, c.op, c.p, c.to); err == nil {
			t.Errorf("%s %s %s should be refused", c.op, c.p, c.to)
		}
	}
	for _, n := range a.tree.snapshot()["nodes"].([]treeNode) {
		if strings.HasPrefix(n.Path, "notes") {
			t.Fatalf("the tree still lists %s", n.Path)
		}
	}
}

func sideModel(t *testing.T) model {
	a, _ := sideProject(t)
	m := model{tab: &tab{agent: a, events: make(chan any, 8)}, w: 140, h: 40, chat: true, es: editorState{split: 0.5}}
	m.toggleSidebar()
	nodes, _ := a.tree.snapshot()["nodes"].([]treeNode)
	m, _ = m.sideMsg(sideTreeMsg{nodes: nodes, version: 1, waited: true})
	return m
}

func sidePress(m model, keys ...string) model {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		default:
			msg = tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
		}
		m, _ = m.sideKey(msg)
	}
	return m
}

func TestSidebarKeys(t *testing.T) {
	m := sideModel(t)
	if !m.sideOn() || !m.side.focus || m.chatX() == 0 || m.chatW()+m.chatX() != m.w {
		t.Fatalf("layout: on %v focus %v x %d w %d", m.sideOn(), m.side.focus, m.chatX(), m.chatW())
	}
	rows := m.sideRows()
	if rows[0].path != "docs" || !rows[0].dir {
		t.Fatalf("first row = %+v", rows[0])
	}
	m = sidePress(m, "enter")
	if !m.side.expanded["docs"] || len(m.sideRows()) != len(rows)+3 {
		t.Fatalf("enter should open docs: %v", m.sideRows())
	}
	m = sidePress(m, "a")
	for _, r := range "new.md" {
		m = sidePress(m, string(r))
	}
	m = sidePress(m, "enter")
	if _, err := os.Stat(filepath.Join(m.agent.dir, "docs/new.md")); err != nil {
		t.Fatalf("a in a folder should make the file there: %v %q", err, m.side.note)
	}
	m = sidePress(m, "/", "m", "a", "i", "n", "enter")
	if r, _ := m.sideSelected(); r.path != "src/main.go" {
		t.Fatalf("find: %+v", r)
	}
	m = sidePress(m, "@")
	if string(m.input) != "`src/main.go` " || m.side.focus {
		t.Fatalf("mention: %q focus %v", string(m.input), m.side.focus)
	}
}

func TestSidebarOpensPictures(t *testing.T) {
	m := sideModel(t)
	m.revealInSide("docs/shot.png")
	r, _ := m.sideSelected()
	if r.path != "docs/shot.png" || !m.side.expanded["docs"] {
		t.Fatalf("reveal: %+v", r)
	}
	m.sideOpen(r)
	if !m.fb.open || !m.fb.only || m.fb.view == nil || m.fb.view.media == nil {
		t.Fatalf("a picture opens in the viewer: %+v", m.fb)
	}
	next, _ := m.mediaKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if next.(model).fb.open {
		t.Fatal("esc closes a viewer opened from the tree completely")
	}
}

func TestShiftMouse(t *testing.T) {
	got := shiftMouse(tea.MouseClickMsg{X: 40, Y: 3}, -30).(tea.MouseClickMsg)
	if got.X != 10 || got.Y != 3 {
		t.Fatalf("%+v", got)
	}
}

func TestFileChangeEndpoint(t *testing.T) {
	h, s, port := testServer(t)
	host := "127.0.0.1:" + port
	origin := "http://" + host
	res := decode(t, do(t, h, "POST", "/api/fs", host, origin, `{"op":"file","path":"a/b.txt"}`))
	if res["path"] != "a/b.txt" {
		t.Fatalf("create: %v", res)
	}
	if _, err := os.Stat(filepath.Join(s.agent.dir, "a/b.txt")); err != nil {
		t.Fatal(err)
	}
	if r := do(t, h, "POST", "/api/fs", host, origin, `{"op":"delete","path":"../x"}`); r.Code != 403 {
		t.Fatalf("outside: %d", r.Code)
	}
	if r := do(t, h, "POST", "/api/fs", host, "http://evil.example", `{"op":"delete","path":"a"}`); r.Code < 400 {
		t.Fatalf("another site must not change files: %d", r.Code)
	}
	if _, err := os.Stat(filepath.Join(s.agent.dir, "a/b.txt")); err != nil {
		t.Fatal("the cross-site request deleted the file")
	}
}
