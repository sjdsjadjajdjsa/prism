package main

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRichUpload(t *testing.T) {
	h, s, port := testServer(t)
	host := "127.0.0.1:" + port

	for _, tc := range []struct {
		name, dir, file, origin string
		status                  int
		path                    string
	}{
		{"attachments", "docs", "pic.png", "http://" + host, 200, "docs/attachments/pic.png"},
		{"collision", "docs", "pic.png", "http://" + host, 200, "docs/attachments/pic-1.png"},
		{"svg", "docs", "pic.svg", "http://" + host, 415, ""},
		{"parent dir", "../outside", "pic.png", "http://" + host, 400, ""},
		{"parent name", "docs", "../pic.png", "http://" + host, 415, ""},
		{"controls", "docs", "a\x00\n\t.png", "http://" + host, 200, "docs/attachments/a___.png"},
		{"leading dot", "docs", ".hidden.png", "http://" + host, 200, "docs/attachments/hidden.png"},
		{"only dot stem", "docs", ".png", "http://" + host, 200, "docs/attachments/media.png"},
		{"long name", "docs", strings.Repeat("x", 300) + ".png", "http://" + host, 200, "docs/attachments/" + strings.Repeat("x", 100) + ".png"},
		{"bidi", "docs", "a\u202eb\u2066.png", "http://" + host, 200, "docs/attachments/a_b_.png"},
		{"git folder", ".git", "pic.png", "http://" + host, 400, ""},
		{"hidden folder", "docs/.private", "pic.png", "http://" + host, 400, ""},
		{"cross origin", "docs", "pic.png", "https://evil.example", 403, ""},
		{"missing origin", "docs", "pic.png", "", 403, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := url.Values{"dir": {tc.dir}, "name": {tc.file}}
			rec := do(t, h, http.MethodPost, "/api/upload?"+query.Encode(), host, tc.origin, "picture")
			if rec.Code != tc.status {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if tc.path == "" {
				return
			}
			if got := decode(t, rec)["path"]; got != tc.path {
				t.Fatalf("path = %v, want %s", got, tc.path)
			}
			if got := decode(t, rec)["relative"]; got != "attachments/"+filepath.Base(tc.path) {
				t.Fatalf("relative = %v", got)
			}
			data, err := os.ReadFile(filepath.Join(s.agent.dir, filepath.FromSlash(tc.path)))
			if err != nil || string(data) != "picture" {
				t.Fatalf("saved media: %q, %v", data, err)
			}
		})
	}
}

func TestRichUploadSymlinkFolder(t *testing.T) {
	h, s, port := testServer(t)
	if err := os.MkdirAll(filepath.Join(s.agent.dir, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(s.agent.dir, "alias")); err != nil {
		t.Fatal(err)
	}
	host := "127.0.0.1:" + port
	rec := do(t, h, http.MethodPost, "/api/upload?dir=alias&name=pic.png", host, "http://"+host, "raw picture")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	got := decode(t, rec)
	if got["path"] != "real/attachments/pic.png" || got["relative"] != "attachments/pic.png" {
		t.Fatalf("symlink upload paths: %v", got)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(s.agent.dir, "escape")); err != nil {
		t.Fatal(err)
	}
	rec = do(t, h, http.MethodPost, "/api/upload?dir=escape&name=pic.png", host, "http://"+host, "raw picture")
	if rec.Code != 400 {
		t.Fatalf("outside symlink status %d: %s", rec.Code, rec.Body.String())
	}
}
