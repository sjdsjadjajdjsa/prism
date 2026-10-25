package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// mediaTypes are the pictures and videos the viewers can show. SVG is left
// out on purpose: it can carry scripts.
var mediaTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp",
	".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime", ".mkv": "video/x-matroska", ".ogv": "video/ogg",
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
