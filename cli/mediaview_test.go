package main

import (
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMediaKind(t *testing.T) {
	for p, want := range map[string]string{"a.PNG": "image", "b/c.jpeg": "image", "x.webp": "image", "v.mp4": "video", "v.MOV": "video", "s.svg": "", "r.md": "", "noext": ""} {
		if got := mediaKind(p); got != want {
			t.Errorf("%s: %q, want %q", p, got, want)
		}
	}
}

func TestFitKeepsShape(t *testing.T) {
	cases := []struct{ pw, ph, cols, rows, w, h int }{
		{400, 300, 80, 40, 80, 30},
		{300, 600, 80, 20, 20, 20},
		{1920, 1080, 100, 100, 100, 28},
		{10, 10, 0, 5, 0, 0},
	}
	for _, c := range cases {
		if w, h := fit(c.pw, c.ph, c.cols, c.rows); w != c.w || h != c.h {
			t.Errorf("fit(%d×%d in %d×%d) = %d×%d, want %d×%d", c.pw, c.ph, c.cols, c.rows, w, h, c.w, c.h)
		}
	}
}

func TestHalfBlocksTopAndBottom(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	img.Set(0, 1, color.RGBA{0, 0, 255, 255})
	c := halfBlocks(img, 1, 1, rgb{})[0]
	if c.ch != '▀' || c.fg != (rgb{255, 0, 0}) || c.bg != (rgb{0, 0, 255}) {
		t.Fatalf("cell = %+v", c)
	}
}

func TestParseProbe(t *testing.T) {
	out := "  Duration: 00:01:02.50, start: 0.0\n  Stream #0:0[0x1](und): Video: h264 (High) (avc1 / 0x31637661), yuv420p(progressive), 1920x1080 [SAR 1:1 DAR 16:9], 30 fps\n"
	w, h, d, err := parseProbe(out)
	if err != nil || w != 1920 || h != 1080 || d != 62.5 {
		t.Fatalf("%d×%d %.2f %v", w, h, d, err)
	}
	w, h, _, _ = parseProbe(out + "      displaymatrix: rotation of -90.00 degrees\n")
	if w != 1080 || h != 1920 {
		t.Fatalf("rotated: %d×%d", w, h)
	}
	if _, _, _, err := parseProbe("Stream #0:0: Audio: aac"); err == nil {
		t.Fatal("audio-only file should be refused")
	}
}

func TestKittyPlaceChunksAndRestoresCursor(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 7)
	}
	s := kittyPlace(img, 4, 2, 10, 5)
	if !strings.HasPrefix(s, "\x1b7\x1b[3;5H\x1b_Ga=T,f=100,q=2,C=1,i=7391,p=1,c=10,r=5,m=1;") || !strings.HasSuffix(s, "\x1b\\\x1b8") {
		t.Fatalf("bad framing: %q…%q", s[:60], s[len(s)-20:])
	}
	if !strings.Contains(s, "\x1b_Gm=0;") {
		t.Fatal("the last chunk must say m=0")
	}
}

func TestOpenPictureAndVideoInViewer(t *testing.T) {
	dir := t.TempDir()
	f, _ := os.Create(filepath.Join(dir, "p.png"))
	png.Encode(f, image.NewRGBA(image.Rect(0, 0, 40, 20)))
	f.Close()
	v := newMediaViewer(dir, "p.png")
	if v.media == nil || v.media.img == nil || v.media.err != "" {
		t.Fatalf("picture: %+v", v.media)
	}
	os.WriteFile(filepath.Join(dir, "bad.png"), []byte("not a png"), 0o644)
	if v := newMediaViewer(dir, "bad.png"); v.media.err == "" {
		t.Fatal("a broken picture should say so")
	}
	if v := newMediaViewer(dir, "../x.png"); v.note == "" {
		t.Fatal("paths outside the project are refused")
	}
}

// TestVideoPlays runs real ffmpeg: frames arrive, pause holds them, the end is noticed.
func TestVideoPlays(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	clip := filepath.Join(dir, "c.mp4")
	if err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=15", "-t", "1", "-pix_fmt", "yuv420p", clip).Run(); err != nil {
		t.Skip("ffmpeg can't make a test clip")
	}
	p, err := startVideo(clip, rgb{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.stop()
	if p.width != 160 || p.height != 90 || p.duration < 0.9 {
		t.Fatalf("probe: %d×%d %.2f", p.width, p.height, p.duration)
	}
	w, h := fit(p.width, p.height, 32, 20)
	p.play(w, h)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, ended, _ := p.status(); ended {
			cells, fw, fh := p.frame()
			if len(cells) != fw*fh || fw != w || cells[0].ch != '▀' {
				t.Fatalf("frame %d×%d, %d cells", fw, fh, len(cells))
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the video never ended")
}

func TestRawFileServesOnlyMedia(t *testing.T) {
	h, s, port := testServer(t)
	host := "127.0.0.1:" + port
	os.WriteFile(filepath.Join(s.agent.dir, "a.png"), []byte("\x89PNG fake"), 0o644)
	os.WriteFile(filepath.Join(s.agent.dir, "page.svg"), []byte("<svg/>"), 0o644)
	res := do(t, h, http.MethodGet, "/api/raw?path=a.png", host, "", "")
	if res.Code != 200 || res.Header().Get("Content-Type") != "image/png" || !strings.Contains(res.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("png: %d %v", res.Code, res.Header())
	}
	if res := do(t, h, http.MethodGet, "/api/raw?path=page.svg", host, "", ""); res.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("svg: %d", res.Code)
	}
	if res := do(t, h, http.MethodGet, "/api/raw?path=../../etc/passwd.png", host, "", ""); res.Code != http.StatusForbidden {
		t.Fatalf("outside: %d", res.Code)
	}
	info := decode(t, do(t, h, http.MethodGet, "/api/file?path=a.png", host, "", ""))
	if info["kind"] != "image" || info["editable"] != false {
		t.Fatalf("file info: %v", info)
	}
}
