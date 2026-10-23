package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testPNG(t *testing.T, w, h int, noise bool) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(1))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255}
			if noise {
				c = color.RGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func script(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

type shotEnv struct {
	dir, bin, log string
	router        *httptest.Server
	requests      chan map[string]any
	env           screenshotEnv
}

// newShotEnv puts a fake chromium first on PATH and starts a fake router.
// status 200 answers "a blue square"; any other status returns the router's JSON error.
func newShotEnv(t *testing.T, status int) *shotEnv {
	t.Helper()
	s := &shotEnv{dir: t.TempDir(), bin: t.TempDir(), requests: make(chan map[string]any, 8)}
	s.log = filepath.Join(s.bin, "chromium.log")
	fixture := filepath.Join(s.bin, "fixture.png")
	os.WriteFile(fixture, testPNG(t, 2000, 1000, false), 0o644)
	script(t, s.bin, "chromium", `for a in "$@"; do case "$a" in --screenshot=*) out="${a#--screenshot=}";; esac; last="$a"; done
echo "$@" >> `+s.log+`
cp `+fixture+` "$out"`)
	t.Setenv("PATH", s.bin+":/usr/bin:/bin")
	old := inScreenshotContainer
	inScreenshotContainer = func() bool { return false }
	t.Cleanup(func() { inScreenshotContainer = old })
	s.router = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		s.requests <- body
		if status != 200 {
			w.WriteHeader(status)
			io.WriteString(w, `{"error":{"message":"no vision model is available right now"}}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"a blue square"}}]}`)
	}))
	t.Cleanup(s.router.Close)
	s.env = screenshotEnv{dir: s.dir, api: s.router.URL + "/v1"}
	return s
}

func (s *shotEnv) run(args string) toolResult {
	return screenshotTool(context.Background(), s.env, args)
}

func (s *shotEnv) chromiumCalls() string {
	data, _ := os.ReadFile(s.log)
	return string(data)
}

func TestScreenshotSendsImageToAutoVision(t *testing.T) {
	s := newShotEnv(t, 200)
	os.WriteFile(filepath.Join(s.dir, "page.html"), []byte("<h1>hi</h1>"), 0o644)
	r := s.run(`{"file":"page.html","question":"is it blue?"}`)
	if !r.ok || !strings.Contains(r.output, "a blue square") || !strings.Contains(r.output, ".prism/screenshots/") {
		t.Fatalf("unexpected result: %+v", r)
	}
	body := <-s.requests
	if body["model"] != "auto:vision" {
		t.Fatalf("model = %v", body["model"])
	}
	parts := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(parts) != 2 || parts[0].(map[string]any)["type"] != "text" || parts[0].(map[string]any)["text"] != "is it blue?" || parts[1].(map[string]any)["type"] != "image_url" {
		t.Fatalf("content parts wrong: %v", parts)
	}
	dataURL := parts[1].(map[string]any)["image_url"].(map[string]any)["url"].(string)
	b64, ok := strings.CutPrefix(dataURL, "data:image/jpeg;base64,")
	if !ok {
		t.Fatalf("not a jpeg data URL: %.40s", dataURL)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) > 1500000 {
		t.Fatalf("bad image: %v, %d bytes", err, len(raw))
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width != 1280 || cfg.Height != 640 {
		t.Fatalf("image should be downscaled from 2000x1000 to 1280x640, got %+v (%v)", cfg, err)
	}
	files, _ := filepath.Glob(filepath.Join(s.dir, ".prism", "screenshots", "*.png"))
	if len(files) != 1 {
		t.Fatalf("expected one saved png, got %v", files)
	}
	call := s.chromiumCalls()
	if !strings.Contains(call, "--window-size=1280,800") || !strings.Contains(call, "http://127.0.0.1:") || !strings.Contains(call, "/page.html") {
		t.Fatalf("chromium args: %s", call)
	}
}

func TestScreenshotNoVisionModel(t *testing.T) {
	s := newShotEnv(t, 503)
	r := s.run(`{"url":"http://127.0.0.1:9/"}`)
	if !r.ok {
		t.Fatalf("the saved path should still be returned: %+v", r)
	}
	if !strings.Contains(r.output, noVisionText) || !strings.Contains(r.output, "Saved screenshot: .prism/screenshots/") || strings.Contains(r.output, "description:") {
		t.Fatalf("output must say it could not look: %q", r.output)
	}
	// the router is unreachable: still no pretending
	s.router.Close()
	r = s.run(`{"url":"http://127.0.0.1:9/"}`)
	if !r.ok || !strings.Contains(r.output, "could not look") || strings.Contains(r.output, "description:") {
		t.Fatalf("unreachable router: %q", r.output)
	}
}

func TestScreenshotURLPolicy(t *testing.T) {
	ctx := context.Background()
	for _, u := range []string{"http://127.0.0.1:8080/x", "http://localhost:3000", "http://192.168.1.20/", "http://10.0.0.5:81/", "https://172.16.3.4/"} {
		if err := screenshotURL(ctx, u, false); err != nil {
			t.Errorf("%s should be allowed: %v", u, err)
		}
	}
	for _, u := range []string{"http://8.8.8.8/", "https://1.1.1.1/", "http://169.254.169.254/latest"} {
		if err := screenshotURL(ctx, u, false); err != errScreenshotPublic {
			t.Errorf("%s should need approval, got %v", u, err)
		}
		if err := screenshotURL(ctx, u, true); err != nil {
			t.Errorf("%s should pass once approved: %v", u, err)
		}
	}
	for _, u := range []string{"file:///etc/passwd", "ftp://127.0.0.1/", "http://user:pw@127.0.0.1/", "javascript:alert(1)", "http:///x", "not a url"} {
		if err := screenshotURL(ctx, u, true); err == nil || err == errScreenshotPublic {
			t.Errorf("%s must be rejected outright, got %v", u, err)
		}
	}
}

func TestScreenshotPublicURLNeedsApproval(t *testing.T) {
	s := newShotEnv(t, 200)
	if r := s.run(`{"url":"http://8.8.8.8/"}`); r.ok || !strings.Contains(r.output, "not approved") {
		t.Fatalf("no approver must refuse: %+v", r)
	}
	var asked []string
	s.env.approve = func(ctx context.Context, tool, arg string) bool { asked = append(asked, tool+": "+arg); return false }
	if r := s.run(`{"url":"http://8.8.8.8/"}`); r.ok || len(asked) != 1 || !strings.Contains(asked[0], "http://8.8.8.8/") {
		t.Fatalf("declined approval must refuse: %+v %v", r, asked)
	}
	if s.chromiumCalls() != "" {
		t.Fatal("chromium must not run without approval")
	}
	s.env.approve = func(ctx context.Context, tool, arg string) bool { asked = append(asked, tool); return true }
	if r := s.run(`{"url":"http://8.8.8.8/"}`); !r.ok {
		t.Fatalf("approved public URL should work: %+v", r)
	}
	// private addresses never ask
	asked = nil
	if r := s.run(`{"url":"http://192.168.1.5/"}`); !r.ok || len(asked) != 0 {
		t.Fatalf("private LAN must not ask: %+v %v", r, asked)
	}
	if r := s.run(`{"url":"file:///etc/passwd"}`); r.ok {
		t.Fatal("file:// must be refused")
	}
}

func TestScreenshotArgsAndContainment(t *testing.T) {
	s := newShotEnv(t, 200)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.html"), []byte("secret"), 0o644)
	os.Symlink(outside, filepath.Join(s.dir, "link"))
	os.WriteFile(filepath.Join(s.dir, "notes.txt"), []byte("x"), 0o644)
	os.Mkdir(filepath.Join(s.dir, "dir.html"), 0o755)
	for name, args := range map[string]string{
		"none":         `{}`,
		"two":          `{"url":"http://127.0.0.1/","file":"a.html"}`,
		"dotdot":       `{"file":"../secret.html"}`,
		"absolute":     `{"file":"` + filepath.Join(outside, "secret.html") + `"}`,
		"symlink":      `{"file":"link/secret.html"}`,
		"not html":     `{"file":"notes.txt"}`,
		"missing":      `{"file":"missing.html"}`,
		"directory":    `{"file":"dir.html"}`,
		"too big":      `{"url":"http://127.0.0.1/","width":9000}`,
		"bad json":     `{`,
		"desktop+url":  `{"desktop":true,"url":"http://127.0.0.1/"}`,
		"desktop+file": `{"desktop":true,"file":"a.html"}`,
	} {
		if r := s.run(args); r.ok {
			t.Errorf("%s should fail: %q", name, r.output)
		}
	}
	if s.chromiumCalls() != "" {
		t.Fatalf("chromium must not have run: %s", s.chromiumCalls())
	}
	// .prism being a symlink out of the project must not let the PNG escape
	os.Symlink(outside, filepath.Join(s.dir, ".prism"))
	if r := s.run(`{"url":"http://127.0.0.1/"}`); r.ok {
		t.Fatalf("screenshots dir escaping the project must be refused: %q", r.output)
	}
	if m, _ := filepath.Glob(filepath.Join(outside, "screenshots", "*")); len(m) != 0 {
		t.Fatal("a screenshot was written outside the project")
	}
}

func TestScreenshotFileServer(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644)
	os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>index</h1>"), 0o644)
	os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=1"), 0o644)
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	os.WriteFile(filepath.Join(root, ".git", "config"), []byte("x"), 0o644)
	os.Symlink(outside, filepath.Join(root, "out"))
	root, _ = filepath.EvalSymlinks(root)
	srv := httptest.NewServer(screenshotFiles(root))
	defer srv.Close()
	get := func(path string) (int, string) {
		req, _ := http.NewRequest("GET", srv.URL, nil)
		req.URL.Path = path
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if code, body := get("/index.html"); code != 200 || !strings.Contains(body, "index") {
		t.Fatalf("index.html must be served as itself: %d %q", code, body)
	}
	for _, p := range []string{"/.env", "/.git/config", "/out/secret.txt", "/../" + filepath.Base(outside) + "/secret.txt", "/%2e%2e/etc/passwd", "/nope.html"} {
		if code, body := get(p); code == 200 || strings.Contains(body, "secret") || strings.Contains(body, "TOKEN") {
			t.Errorf("%s leaked: %d %q", p, code, body)
		}
	}
}

func TestScreenshotProxyPinsAddresses(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "local page") }))
	defer backend.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	addr, stop, err := screenshotProxy(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	proxy, _ := url.Parse(addr)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}, Timeout: 10 * time.Second}
	res, err := client.Get(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "local page" {
		t.Fatalf("private page through proxy: %q", body)
	}
	res, err = client.Get("http://8.8.8.8/")
	if err == nil {
		defer res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("public request must be blocked, got %d", res.StatusCode)
		}
	}
}

func TestScreenshotNoChromium(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	// never reach the real network from a unit test
	fakeBrowserDownload(t, func(string, string) error { return errors.New("dial tcp: no route to host") })
	old := inScreenshotContainer
	inScreenshotContainer = func() bool { return false }
	defer func() { inScreenshotContainer = old }()
	r := screenshotTool(context.Background(), screenshotEnv{dir: t.TempDir()}, `{"url":"http://127.0.0.1:1/"}`)
	if r.ok || !strings.Contains(r.output, "Chromium is not installed") || !strings.Contains(r.output, "internet connection") {
		t.Fatalf("%+v", r)
	}
}

func TestScreenshotDesktopRefusals(t *testing.T) {
	s := newShotEnv(t, 200)
	approved := 0
	s.env.system = true
	s.env.approve = func(ctx context.Context, tool, arg string) bool { approved++; return true }
	inScreenshotContainer = func() bool { return true }
	r := s.run(`{"desktop":true}`)
	if r.ok || !strings.Contains(r.output, "not available in the container") || approved != 0 {
		t.Fatalf("container must refuse before asking: %+v approved=%d", r, approved)
	}
	inScreenshotContainer = func() bool { return false }
	s.env.system = false
	if r := s.run(`{"desktop":true}`); r.ok || !strings.Contains(r.output, "host system mode") || approved != 0 {
		t.Fatalf("non-system mode must refuse: %+v", r)
	}
	s.env.system = true
	s.env.approve = func(ctx context.Context, tool, arg string) bool { approved++; return false }
	if r := s.run(`{"desktop":true}`); r.ok || !strings.Contains(r.output, "not approved") || approved != 1 {
		t.Fatalf("declined desktop must refuse: %+v", r)
	}
	// no approver at all (runTool fallback) can never capture the desktop
	if r := screenshotTool(context.Background(), screenshotFallbackEnv(s.dir), `{"desktop":true}`); r.ok {
		t.Fatalf("fallback env must refuse desktop: %+v", r)
	}
	if r := runTool(context.Background(), s.dir, "screenshot", `{"desktop":true}`); r.ok {
		t.Fatalf("runTool must refuse desktop: %+v", r)
	}
}

func TestScreenshotDesktopCapture(t *testing.T) {
	if runtime.GOOS != "linux" || screenshotWSL() {
		t.Skip("needs plain Linux")
	}
	s := newShotEnv(t, 200)
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("XDG_CURRENT_DESKTOP", "sway")
	t.Setenv("DISPLAY", "")
	fixture := filepath.Join(s.bin, "fixture.png")
	script(t, s.bin, "grim", `cp `+fixture+` "$1"`)
	s.env.system = true
	asked := ""
	s.env.approve = func(ctx context.Context, tool, arg string) bool { asked = tool + ": " + arg; return true }
	r := s.run(`{"desktop":true,"question":"what is open?"}`)
	if !r.ok || !strings.Contains(r.output, "a blue square") || !strings.HasPrefix(asked, "screenshot: ") || !strings.Contains(asked, "desktop") {
		t.Fatalf("%+v asked=%q", r, asked)
	}
	// nothing installed: clear, actionable error
	os.Remove(filepath.Join(s.bin, "grim"))
	r = s.run(`{"desktop":true}`)
	if r.ok || !strings.Contains(r.output, "install grim") {
		t.Fatalf("%+v", r)
	}
	// a tool that exits 0 but writes nothing must not count
	script(t, s.bin, "grim", `exit 0`)
	if r = s.run(`{"desktop":true}`); r.ok {
		t.Fatalf("empty capture accepted: %+v", r)
	}
}

func TestDesktopCommands(t *testing.T) {
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	first := func(goos string, e func(string) string) string {
		c := desktopCommands(goos, e, "/o.png")
		if len(c) == 0 {
			return ""
		}
		return strings.Join(c[0], " ")
	}
	for _, c := range []struct {
		name, goos string
		env        func(string) string
		want       string
	}{
		{"mac", "darwin", env(), "screencapture -x /o.png"},
		{"sway", "linux", env("WAYLAND_DISPLAY", "w0", "XDG_CURRENT_DESKTOP", "sway"), "grim /o.png"},
		{"kde wayland", "linux", env("WAYLAND_DISPLAY", "w0", "XDG_CURRENT_DESKTOP", "KDE"), "spectacle -b -n -f -o /o.png"},
		{"gnome wayland", "linux", env("XDG_SESSION_TYPE", "wayland", "XDG_CURRENT_DESKTOP", "ubuntu:GNOME"), "gnome-screenshot -f /o.png"},
		{"x11", "linux", env("DISPLAY", ":0"), "maim /o.png"},
		{"no session", "linux", env(), ""},
		{"windows", "windows", env(), ""},
	} {
		if got := first(c.goos, c.env); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	x11 := desktopCommands("linux", env("DISPLAY", ":0"), "/o.png")
	if len(x11) < 3 || x11[1][0] != "scrot" || strings.Join(x11[2], " ") != "import -window root /o.png" {
		t.Errorf("x11 fallbacks: %v", x11)
	}
	if h := desktopHelp("darwin", env()); !strings.Contains(h, "Screen Recording") {
		t.Errorf("mac help: %s", h)
	}
	if h := desktopHelp("linux", env("WAYLAND_DISPLAY", "w0", "XDG_CURRENT_DESKTOP", "GNOME")); !strings.Contains(h, "dialog") {
		t.Errorf("gnome help: %s", h)
	}
}

func TestScreenshotWSL(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip()
	}
	bin := t.TempDir()
	winShot := filepath.Join(bin, "win-temp.png")
	os.WriteFile(winShot, testPNG(t, 64, 48, false), 0o644)
	script(t, bin, "powershell.exe", `echo "warning noise"; echo 'C:\Users\x\AppData\Local\Temp\prism-1.png'`)
	script(t, bin, "wslpath", `[ "$1" = "-u" ] && echo "`+winShot+`"`)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	out := filepath.Join(t.TempDir(), "o.png")
	if err := screenshotWindows(context.Background(), out); err != nil || !validPNG(out) {
		t.Fatalf("wsl capture: %v", err)
	}
	if _, err := os.Stat(winShot); err == nil {
		t.Error("the Windows temp file should be removed")
	}
	os.Remove(filepath.Join(bin, "powershell.exe"))
	if err := screenshotWindows(context.Background(), out); err == nil || !strings.Contains(err.Error(), "WSL") {
		t.Errorf("missing powershell: %v", err)
	}
}

func TestScreenshotVisionImageLimits(t *testing.T) {
	dir := t.TempDir()
	check := func(name string, w, h int, noise bool) {
		path := filepath.Join(dir, name)
		os.WriteFile(path, testPNG(t, w, h, noise), 0o644)
		data, err := screenshotVisionImage(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width > 1280 || cfg.Height > 1280 || len(data) > 1500000 {
			t.Fatalf("%s: %+v %d bytes (%v)", name, cfg, len(data), err)
		}
	}
	check("small", 300, 200, false)
	check("wide", 3840, 1200, false)
	check("tall", 1280, 6000, false)
	check("noise", 1280, 1280, true)
	small, _ := screenshotVisionImage(filepath.Join(dir, "small"))
	if cfg, _ := jpeg.DecodeConfig(bytes.NewReader(small)); cfg.Width != 300 || cfg.Height != 200 {
		t.Errorf("small images must not be resized: %+v", cfg)
	}
	os.WriteFile(filepath.Join(dir, "bad"), []byte("not a png"), 0o644)
	if _, err := screenshotVisionImage(filepath.Join(dir, "bad")); err == nil {
		t.Error("garbage accepted")
	}
}

func TestScreenshotToolIsRegistered(t *testing.T) {
	found := false
	for _, s := range toolSpecs {
		if s.(map[string]any)["function"].(map[string]any)["name"] == "screenshot" {
			found = true
		}
	}
	if !found {
		t.Fatal("screenshot missing from toolSpecs")
	}
	if r := runTool(context.Background(), t.TempDir(), "screenshot", `{}`); r.ok || !strings.Contains(r.output, "exactly one") {
		t.Fatalf("runTool dispatch: %+v", r)
	}
}

// TestScreenshotReal drives the real Chromium. Run it inside the agent image:
// PRISM_REAL_SHOT=<project dir containing page.html> go test -run TestScreenshotReal
func TestScreenshotReal(t *testing.T) {
	dir := os.Getenv("PRISM_REAL_SHOT")
	if dir == "" {
		t.Skip("set PRISM_REAL_SHOT to a project folder with page.html")
	}
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		parts := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
		url := parts[1].(map[string]any)["image_url"].(map[string]any)["url"].(string)
		t.Logf("router got model=%v image data URL of %d chars", body["model"], len(url))
		io.WriteString(w, `{"choices":[{"message":{"content":"fake vision reply"}}]}`)
	}))
	defer router.Close()
	for _, args := range []string{`{"file":"page.html"}`, `{"file":"page.html","full_page":true,"width":800,"height":400}`} {
		r := screenshotTool(context.Background(), screenshotEnv{dir: dir, api: router.URL + "/v1"}, args)
		t.Logf("%s -> ok=%v\n%s", args, r.ok, r.output)
		if !r.ok {
			t.Fail()
		}
	}
}

// fakeBrowserDownload isolates the lazy browser download: an empty PATH, a temp cache folder, a pinned
// build whose checksum is not required to match, and a fake downloader (fetch gets url and dest).
// It returns the cache folder.
func fakeBrowserDownload(t *testing.T, fetch func(url, dest string) error) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the lazy browser download is Linux only")
	}
	cache := filepath.Join(t.TempDir(), "browser")
	oldDir, oldFetch, oldBuilds := screenshotBrowserDir, screenshotFetch, screenshotBrowsers
	screenshotBrowserDir = func() string { return cache }
	screenshotFetch = func(ctx context.Context, url, dest string) error { return fetch(url, dest) }
	builds := map[string]screenshotBrowserBuild{}
	for arch, build := range oldBuilds {
		build.url = "https://example.invalid/" + build.platform + ".zip"
		builds[arch] = build
	}
	screenshotBrowsers = builds
	t.Cleanup(func() { screenshotBrowserDir, screenshotFetch, screenshotBrowsers = oldDir, oldFetch, oldBuilds })
	return cache
}

// browserZip builds an archive shaped like Chrome for Testing's: one folder holding chrome-headless-shell.
func browserZip(t *testing.T, platform, script string, extra map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	add := func(name, body string, mode os.FileMode) {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(mode)
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(body))
	}
	add("chrome-headless-shell-"+platform+"/chrome-headless-shell", script, 0o755)
	add("chrome-headless-shell-"+platform+"/icudtl.dat", "data", 0o644)
	for name, body := range extra {
		add(name, body, 0o644)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pinBuild makes the fake archive the pinned download for this machine.
func pinBuild(data []byte) screenshotBrowserBuild {
	sum := sha256.Sum256(data)
	build := screenshotBrowsers[runtime.GOARCH]
	build.sha256 = fmt.Sprintf("%x", sum[:])
	screenshotBrowsers[runtime.GOARCH] = build
	return build
}

func TestScreenshotBrowserLazyDownload(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	calls := 0
	var data []byte
	cache := fakeBrowserDownload(t, func(url, dest string) error {
		calls++
		if !strings.HasPrefix(url, "https://") {
			t.Errorf("url %q", url)
		}
		return os.WriteFile(dest, data, 0o600)
	})
	build := screenshotBrowsers[runtime.GOARCH]
	data = browserZip(t, build.platform, "#!/bin/sh\nexit 0\n", nil)
	pinBuild(data)

	path, note, err := screenshotBrowser(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, cache) || !strings.HasSuffix(path, "/chrome-headless-shell") {
		t.Fatalf("path %q not under cache %q", path, cache)
	}
	if info, err := os.Stat(path); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("browser is not executable: %v", err)
	}
	if !strings.Contains(note, "downloaded the screenshot browser (about 126 MB, once)") {
		t.Fatalf("note %q", note)
	}
	// second use: found in the cache, no download, no note
	path2, note2, err := screenshotBrowser(context.Background())
	if err != nil || path2 != path || note2 != "" || calls != 1 {
		t.Fatalf("second use: %q %q %v calls=%d", path2, note2, err, calls)
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 1 {
		t.Fatalf("download left temp files behind: %v", entries)
	}
}

func TestScreenshotBrowserPrefersInstalled(t *testing.T) {
	s := newShotEnv(t, 200)
	fakeBrowserDownload(t, func(string, string) error { t.Fatal("must not download when a browser is on PATH"); return nil })
	path, note, err := screenshotBrowser(context.Background())
	if err != nil || note != "" || path != filepath.Join(s.bin, "chromium") {
		t.Fatalf("%q %q %v", path, note, err)
	}
}

func TestScreenshotBrowserChecksumMismatch(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	good := browserZip(t, screenshotBrowsers[runtime.GOARCH].platform, "#!/bin/sh\n", nil)
	cache := fakeBrowserDownload(t, func(url, dest string) error {
		bad := append([]byte{}, good...)
		bad[len(bad)/2] ^= 0xff
		return os.WriteFile(dest, bad, 0o600)
	})
	pinBuild(good)
	_, _, err := screenshotBrowser(context.Background())
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") || !strings.Contains(err.Error(), "Chromium is not installed") {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 0 {
		t.Fatalf("a bad download must leave nothing behind: %v", entries)
	}
}

func TestScreenshotBrowserRejectsUnsafeArchive(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	data := browserZip(t, screenshotBrowsers[runtime.GOARCH].platform, "#!/bin/sh\n", map[string]string{"../escape.txt": "x"})
	cache := fakeBrowserDownload(t, func(url, dest string) error { return os.WriteFile(dest, data, 0o600) })
	pinBuild(data)
	_, _, err := screenshotBrowser(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(cache), "escape.txt")); statErr == nil {
		t.Fatal("archive escaped the cache")
	}
}

func TestScreenshotBrowserOffline(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	// the real downloader against a port nothing listens on
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	cache := fakeBrowserDownload(t, nil)
	screenshotFetch = func(ctx context.Context, _, dest string) error {
		return realScreenshotFetch(ctx, deadURL+"/browser.zip", dest)
	}
	_, _, err := screenshotBrowser(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"Chromium is not installed", "could not be downloaded", "126 MB", "internet connection"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 0 {
		t.Fatalf("offline must leave nothing behind: %v", entries)
	}
}

func TestScreenshotFetchHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ok.zip" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, "payload")
	}))
	defer server.Close()
	dest := filepath.Join(t.TempDir(), "out")
	if err := realScreenshotFetch(context.Background(), server.URL+"/ok.zip", dest); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dest); string(data) != "payload" {
		t.Fatalf("got %q", data)
	}
	if err := realScreenshotFetch(context.Background(), server.URL+"/missing.zip", dest); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}

// The whole tool, with no browser installed: it downloads (fake) once, takes the shot, and says so.
func TestScreenshotToolDownloadsBrowserOnFirstUse(t *testing.T) {
	s := newShotEnv(t, 200)
	cp, err := exec.LookPath("cp")
	if err != nil {
		t.Skip("no cp")
	}
	os.Remove(filepath.Join(s.bin, "chromium")) // nothing on PATH any more
	script := "#!/bin/sh\nfor a in \"$@\"; do case \"$a\" in --screenshot=*) out=\"${a#--screenshot=}\";; esac; done\n" + cp + " " + filepath.Join(s.bin, "fixture.png") + " \"$out\"\n"
	var data []byte
	downloads := 0
	fakeBrowserDownload(t, func(url, dest string) error { downloads++; return os.WriteFile(dest, data, 0o600) })
	data = browserZip(t, screenshotBrowsers[runtime.GOARCH].platform, script, nil)
	pinBuild(data)
	t.Setenv("PATH", t.TempDir())
	os.WriteFile(filepath.Join(s.dir, "page.html"), []byte("<h1>hi</h1>"), 0o644)
	for i := 0; i < 2; i++ {
		r := s.run(`{"file":"page.html"}`)
		if !r.ok {
			t.Fatalf("run %d: %+v", i, r)
		}
		if got := strings.Contains(r.output, "downloaded the screenshot browser (about 126 MB, once)"); got != (i == 0) {
			t.Fatalf("run %d: download note present=%v\n%s", i, got, r.output)
		}
	}
	if downloads != 1 {
		t.Fatalf("downloads = %d, want 1", downloads)
	}
}
