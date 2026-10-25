package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// fileOp makes, renames or deletes a file or folder from the file tree.
// Every path goes through projectPath, so nothing outside the project is touched.
func fileOp(a *Agent, op, p, to string) (string, error) {
	abs, rel, err := projectPath(a.dir, p)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == "" {
		return "", errors.New("pick a file or folder inside the project")
	}
	if rel == ".git" || strings.HasPrefix(rel, ".git/") {
		return "", errors.New("Prism doesn't change files inside .git")
	}
	done := rel
	switch op {
	case "file":
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return "", pathErr(rel, err)
		}
		f, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return "", existsErr(rel, err)
		}
		f.Close()
	case "folder":
		if _, err := os.Stat(abs); err == nil {
			return "", fmt.Errorf("%s already exists", rel)
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return "", pathErr(rel, err)
		}
	case "rename":
		toAbs, toRel, err := projectPath(a.dir, to)
		if err != nil {
			return "", err
		}
		if toRel == ".git" || strings.HasPrefix(toRel, ".git/") {
			return "", errors.New("Prism doesn't change files inside .git")
		}
		if _, err := os.Stat(toAbs); err == nil {
			return "", fmt.Errorf("%s already exists", toRel)
		}
		if err := os.MkdirAll(filepath.Dir(toAbs), 0o755); err != nil {
			return "", pathErr(toRel, err)
		}
		if err := os.Rename(abs, toAbs); err != nil {
			return "", pathErr(rel, err)
		}
		done = toRel
		defer a.treeChanged(toRel)
	case "delete":
		if _, err := os.Lstat(abs); err != nil {
			return "", pathErr(rel, err)
		}
		if err := os.RemoveAll(abs); err != nil {
			return "", pathErr(rel, err)
		}
	default:
		return "", fmt.Errorf("unknown change %q", op)
	}
	a.treeChanged(rel)
	return done, nil
}

func existsErr(rel string, err error) error {
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s already exists", rel)
	}
	return pathErr(rel, err)
}

func (a *Agent) treeChanged(rel string) {
	if a.tree != nil {
		a.tree.refresh([]string{rel})
	}
}

// fileChange serves POST /api/fs: {"op": "file|folder|rename|delete", "path", "to"}.
func (s *server) fileChange(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var req struct{ Op, Path, To string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	done, err := fileOp(s.agent, req.Op, req.Path, req.To)
	if err != nil {
		http.Error(w, err.Error(), fileStatus(err))
		return
	}
	writeJSON(w, map[string]any{"path": done})
}
