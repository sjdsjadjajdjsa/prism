package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type fileBirth struct {
	Path string `json:"path"`
	T    int64  `json:"t"` // unix seconds of the first commit that added it
}

// fileBirths lists when git first saw each file that still exists, oldest
// first, for the map's time-lapse. Paths are relative to dir, like the map's.
func fileBirths(ctx context.Context, dir string) ([]fileBirth, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// -z splits records with NUL; a NUL before each date can't clash with a path.
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "-c", "core.quotePath=false", "log", "--reverse", "--diff-filter=A",
		"--name-only", "--relative", "-z", "--format=%x00%ct", "--no-renames")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var births []fileBirth
	seen := map[string]bool{}
	var at int64
	date := false
	for _, f := range strings.Split(string(out), "\x00") {
		f = strings.TrimPrefix(f, "\n")
		switch {
		case f == "":
			date = true
		case date:
			at, _ = strconv.ParseInt(strings.TrimSpace(f), 10, 64)
			date = false
		case !seen[f]:
			seen[f] = true
			if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(f))); err == nil {
				births = append(births, fileBirth{f, at})
			}
		}
	}
	return births, nil
}

// births serves GET /api/births: {"files": [{path, t}]}.
func (s *server) births(w http.ResponseWriter, r *http.Request) {
	a, err := s.agentTab(r)
	if err != nil {
		a = s.agent
	}
	files, err := fileBirths(r.Context(), a.dir)
	if err != nil {
		files = []fileBirth{}
	}
	writeJSON(w, map[string]any{"files": files})
}
