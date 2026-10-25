package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// mediaTypes are the pictures and videos the viewers can show. SVG is left
// out on purpose: it can carry scripts.
var mediaTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp",
	".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime", ".mkv": "video/x-matroska", ".ogv": "video/ogg",
}

// mediaName keeps a small ASCII stem and an allowlisted extension.
func mediaName(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if name == "" || strings.ContainsAny(name, "/\\") || mediaTypes[ext] == "" {
		return ""
	}
	stem := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._ -", r) {
			return r
		}
		return '_'
	}, strings.TrimSuffix(name, filepath.Ext(name)))
	stem = strings.TrimLeft(stem, ".")
	if len(stem) > 100 {
		stem = stem[:100]
	}
	if strings.TrimSpace(stem) == "" {
		stem = "media"
	}
	return stem + ext
}

func hiddenMediaDir(dir string) bool {
	for _, part := range strings.Split(filepath.ToSlash(dir), "/") {
		if strings.HasPrefix(part, ".") && part != "." {
			return true
		}
	}
	return false
}

// uploadMedia streams media beside the document through a project-confined root.
func (s *server) uploadMedia(w http.ResponseWriter, r *http.Request) {
	const limit = 50 << 20
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	name := mediaName(r.URL.Query().Get("name"))
	if name == "" {
		http.Error(w, "invalid or unsupported media name", http.StatusUnsupportedMediaType)
		return
	}
	requested := r.URL.Query().Get("dir")
	if requested == "" {
		requested = "."
	}
	_, base, err := projectPath(s.agent.dir, requested)
	if err != nil || hiddenMediaDir(requested) || hiddenMediaDir(base) {
		http.Error(w, "invalid media folder", http.StatusBadRequest)
		return
	}
	_, dir, err := projectPath(s.agent.dir, filepath.Join(base, "attachments"))
	if err != nil || hiddenMediaDir(dir) {
		http.Error(w, "invalid attachment folder", http.StatusBadRequest)
		return
	}
	ignored := exec.Command("git", "check-ignore", "-q", "--", dir+"/", filepath.Join(requested, "attachments")+"/")
	ignored.Dir = s.agent.dir
	if ignored.Run() == nil {
		http.Error(w, "ignored attachment folder", http.StatusBadRequest)
		return
	}
	root, err := os.OpenRoot(s.agent.dir)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer root.Close()
	if err := root.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	ext := filepath.Ext(name)
	for i := 0; ; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(name, ext), i, ext)
		}
		rel := filepath.Join(dir, candidate)
		f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_, err = io.Copy(f, r.Body)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			root.Remove(rel)
			status := http.StatusInternalServerError
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			http.Error(w, "couldn't write media", status)
			return
		}
		s.agent.treeChanged(filepath.ToSlash(rel))
		writeJSON(w, map[string]string{"path": filepath.ToSlash(rel), "relative": "attachments/" + candidate})
		return
	}
}

// mediaKind is "image", "video" or "" for a path, by its extension.
func mediaKind(p string) string {
	t := mediaTypes[strings.ToLower(filepath.Ext(p))]
	kind, _, _ := strings.Cut(t, "/")
	return kind
}

// rawFile serves a picture or video from the project with range requests, so
// the browser can seek in long videos.
func (s *server) rawFile(w http.ResponseWriter, r *http.Request) {
	abs, rel, err := projectPath(s.agent.dir, r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), fileStatus(err))
		return
	}
	ctype := mediaTypes[strings.ToLower(filepath.Ext(rel))]
	if ctype == "" {
		http.Error(w, "only pictures and videos are served here", http.StatusUnsupportedMediaType)
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		http.Error(w, pathErr(rel, err).Error(), fileStatus(err))
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.Error(w, rel+" is not a file", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "", st.ModTime(), f)
}
