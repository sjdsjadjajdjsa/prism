package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFileBirths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(env []string, args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	git(nil, "init", "-q")
	commit := func(when, file string) {
		os.WriteFile(filepath.Join(dir, file), []byte(file), 0o644)
		git(nil, "add", file)
		git([]string{"GIT_AUTHOR_DATE=" + when, "GIT_COMMITTER_DATE=" + when}, "-c", "user.email=a@b", "-c", "user.name=a", "commit", "-qm", file)
	}
	os.MkdirAll(filepath.Join(dir, "sub", "@scope"), 0o755)
	commit("2026-01-01T00:00:00Z", "a.go")
	commit("2026-02-01T00:00:00Z", "sub/café.md")
	commit("2026-03-01T00:00:00Z", "sub/@scope/x.ts")
	commit("2026-03-02T00:00:00Z", "gone.txt")
	os.Remove(filepath.Join(dir, "gone.txt"))
	ctx := context.Background()
	got, err := fileBirths(ctx, dir)
	if err != nil || len(got) != 3 || got[0].Path != "a.go" || got[1].Path != "sub/café.md" || got[2].Path != "sub/@scope/x.ts" || got[0].T >= got[1].T || got[2].T == 0 {
		t.Fatalf("%+v %v", got, err)
	}
	sub, _ := fileBirths(ctx, filepath.Join(dir, "sub"))
	if len(sub) != 2 || sub[0].Path != "café.md" {
		t.Fatalf("paths are relative to the folder Prism runs in: %+v", sub)
	}
	if got, _ := fileBirths(ctx, t.TempDir()); len(got) != 0 {
		t.Fatalf("no git: %v", got)
	}
}
