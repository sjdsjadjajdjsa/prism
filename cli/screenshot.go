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
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

const (
	screenshotVisionSide  = 1280    // longest side sent to the vision model
	screenshotVisionBytes = 1500000 // largest JPEG sent to the vision model
	noVisionText          = "no model that can see images was available, so I could not look at it. Pin a plan model with /model (ChatGPT, Claude, Grok or Gemini), or add a free Gemini key with prism setup"
)

var errScreenshotPublic = errors.New("public URL screenshot requires approval")

type screenshotEnv struct {
	dir, api string
	model    string // the chat's pinned model, tried first for screenshots
	system   bool
	approve  func(ctx context.Context, tool, arg string) bool
}

// screenshot is called by the agent loop so approvals and system mode are the real ones.
func (a *Agent) screenshot(ctx context.Context, raw string) toolResult {
	return screenshotTool(ctx, screenshotEnv{dir: a.dir, api: a.api, model: a.model, system: a.system, approve: a.approve}, raw)
}

// screenshotFallbackEnv is used when runTool is called without an agent: no system mode and
// no way to ask the user, so desktop capture and public URLs are refused.
func screenshotFallbackEnv(dir string) screenshotEnv {
	api := strings.TrimRight(os.Getenv("PRISM_API"), "/")
	if api == "" {
		api = "http://127.0.0.1:4848/v1"
	}
	return screenshotEnv{dir: dir, api: api}
}

func (e screenshotEnv) ask(ctx context.Context, text string) bool {
	return e.approve != nil && e.approve(ctx, "screenshot", text)
}

var inScreenshotContainer = func() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	data, _ := os.ReadFile("/proc/1/cgroup")
	return strings.Contains(string(data), "docker") || strings.Contains(string(data), "kubepods")
}

func screenshotTool(ctx context.Context, env screenshotEnv, raw string) toolResult {
	args := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			return fail("invalid screenshot arguments: " + err.Error())
		}
	}
	parent := ctx
	desktop, _ := args["desktop"].(bool)
	target, file := sarg(args, "url"), sarg(args, "file")
	only := ""
	count := 0
	for _, set := range []bool{desktop, target != "", file != ""} {
		if set {
			count++
		}
	}
	if count != 1 {
		return fail("provide exactly one of url, file, or desktop:true")
	}
	width, height := iarg(args, "width", 1280), iarg(args, "height", 800)
	if width > 3840 || height > 2160 {
		return fail("viewport must be at most 3840 x 2160")
	}
	full, _ := args["full_page"].(bool)

	public := false
	switch {
	case desktop:
		if inScreenshotContainer() {
			return fail("desktop capture is not available in the container; run prism fix (host system mode) to look at the real desktop")
		}
		if !env.system {
			return fail("desktop capture only works in host system mode (prism fix)")
		}
		if !env.ask(ctx, "take a screenshot of your whole desktop (every open window and notification) and send it to a free vision model") {
			return fail("desktop screenshot was not approved")
		}
	case target != "":
		err := screenshotURL(ctx, target, false)
		if errors.Is(err, errScreenshotPublic) {
			if err := screenshotURL(ctx, target, true); err != nil {
				return fail(err.Error())
			}
			if !env.ask(ctx, "open the public web page "+target+" in a headless browser and send its screenshot to a free vision model") {
				return fail("public URL screenshot was not approved (only localhost, private network addresses and project files are allowed without approval)")
			}
			public = true
		} else if err != nil {
			return fail(err.Error())
		}
	}

	root, err := filepath.Abs(env.dir)
	if err != nil {
		return fail(err.Error())
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return fail(err.Error())
	}
	if file != "" {
		served, stop, err := serveProjectFile(root, file)
		if err != nil {
			return fail(err.Error())
		}
		defer stop()
		target, only = served, hostOf(served)
	}
	outDir, err := screenshotPath(root, ".prism/screenshots")
	if err != nil {
		return fail(err.Error())
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return fail(err.Error())
	}
	output := filepath.Join(outDir, time.Now().UTC().Format("20060102T150405.000000000")+"-"+randomSuffix()+".png")

	// The browser is found (or downloaded once) before the capture clock starts: a first download
	// on a slow connection must not eat the 75 seconds the capture itself gets.
	browser, browserNote := "", ""
	if !desktop {
		browser, browserNote, err = screenshotBrowser(parent)
		if err != nil {
			os.Remove(output)
			return fail("screenshot failed: " + err.Error())
		}
	}
	captureCtx, cancel := context.WithTimeout(parent, 75*time.Second)
	defer cancel()
	if desktop {
		err = screenshotDesktop(captureCtx, output)
	} else {
		err = screenshotChromium(captureCtx, browser, target, only, output, width, height, full, public)
	}
	if err != nil {
		os.Remove(output)
		return fail("screenshot failed: " + err.Error())
	}
	rel, _ := filepath.Rel(root, output)
	rel = filepath.ToSlash(rel)

	saved := browserNote + "Saved screenshot: " + rel + "\n"
	data, err := screenshotVisionImage(output)
	if err != nil {
		return toolResult{ok: true, summary: rel + " (not inspected)", output: saved + "I could not look at the image: " + err.Error() + ". Do not guess what it shows."}
	}
	question := sarg(args, "question")
	if question == "" {
		question = "Describe this page and point out any visible layout or rendering problems."
	}
	visionCtx, cancelVision := context.WithTimeout(parent, 90*time.Second)
	defer cancelVision()
	description, err := screenshotVision(visionCtx, env.api, env.model, question, data)
	if err != nil {
		var ve *visionError
		if errors.As(err, &ve) && ve.unavailable {
			return toolResult{ok: true, summary: rel + " (not inspected)", output: saved + "Vision unavailable: " + noVisionText + " (" + ve.Error() + "). Tell the user this; do not describe or guess what the screenshot shows."}
		}
		return toolResult{ok: true, summary: rel + " (not inspected)", output: saved + "Vision failed, I could not look at the image: " + err.Error() + ". Do not describe or guess what the screenshot shows."}
	}
	return toolResult{ok: true, summary: rel, output: saved + "Vision model's description:\n" + description}
}

func hostOf(target string) string {
	if u, err := url.Parse(target); err == nil {
		return u.Host
	}
	return ""
}

// routerAddrs are the addresses of the AI router (PRISM_API or the default), which pages must never reach.
func routerAddrs(ctx context.Context) []string {
	api := os.Getenv("PRISM_API")
	if api == "" {
		api = "http://127.0.0.1:4848/v1"
	}
	u, err := url.Parse(api)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"https": "443"}[u.Scheme]
		if port == "" {
			port = "80"
		}
	}
	out := []string{net.JoinHostPort(u.Hostname(), port)}
	if ips, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname()); err == nil {
		for _, ip := range ips {
			out = append(out, net.JoinHostPort(ip.IP.String(), port))
		}
	}
	return out
}

// serveProjectFile serves the project on a local port and returns the URL of one .html file in it.
func serveProjectFile(root, file string) (string, func(), error) {
	path, err := screenshotPath(root, file)
	if err != nil {
		return "", nil, err
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext != ".html" && ext != ".htm" {
		return "", nil, errors.New("file must be a project .html file")
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return "", nil, errors.New("HTML file does not exist or is not a regular file")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	server := &http.Server{Handler: screenshotFiles(root), ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	rel, _ := filepath.Rel(root, path)
	return "http://" + listener.Addr().String() + (&url.URL{Path: "/" + filepath.ToSlash(rel)}).EscapedPath(), func() { server.Close() }, nil
}

func screenshotPath(root, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = resolveDeepest(filepath.Clean(path))
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errors.New("screenshot path is outside the project")
	}
	return path, nil
}

// screenshotFiles serves the project to the local browser, never anything outside it
// and never dot-files or dot-folders such as .git or .env.
func screenshotFiles(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/")
		for _, part := range strings.Split(rel, "/") {
			if strings.HasPrefix(part, ".") {
				http.Error(w, "hidden files are not served", http.StatusForbidden)
				return
			}
		}
		path, err := screenshotPath(root, filepath.FromSlash(rel))
		if err != nil {
			http.Error(w, "outside the project", http.StatusForbidden)
			return
		}
		if real, err := filepath.Rel(root, path); err == nil && slices.ContainsFunc(strings.Split(filepath.ToSlash(real), "/"), func(part string) bool { return strings.HasPrefix(part, ".") && part != "." }) {
			http.Error(w, "hidden files are not served", http.StatusForbidden)
			return
		}
		f, err := os.Open(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
	})
}

func screenshotPrivate(ip net.IP) bool { return ip.IsLoopback() || ip.IsPrivate() }

func screenshotAddresses(ctx context.Context, host string) ([]net.IPAddr, error) {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("cannot resolve screenshot host %q", host)
	}
	return ips, nil
}

// screenshotURL checks the scheme and, unless public is true, that every address of the host
// is loopback or on a private network. A public host returns errScreenshotPublic.
func screenshotURL(ctx context.Context, target string, public bool) error {
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return errors.New("screenshot URL must be http(s) without credentials; use file for project HTML, file:// is never allowed")
	}
	ips, err := screenshotAddresses(ctx, u.Hostname())
	if err != nil {
		return err
	}
	for _, ip := range ips {
		if !public && !screenshotPrivate(ip.IP) {
			return errScreenshotPublic
		}
	}
	return nil
}

// screenshotProxy is a tiny forward proxy Chromium is forced through. Every connection,
// including redirects and subresources, is pinned to an address that was checked here, so
// a private page cannot pull in public content (or the reverse) behind the user's back.
//
// only, when set, is the one private host:port the page may use (the local file server for a
// project file); deny lists addresses no page may reach (the AI router). Link-local addresses
// such as cloud metadata are always blocked, and an approved public page still cannot reach
// private addresses.
func screenshotProxy(ctx context.Context, public bool, only string, deny ...string) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := screenshotAddresses(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			target := net.JoinHostPort(ip.IP.String(), port)
			switch {
			case slices.Contains(deny, target) || slices.Contains(deny, address):
				return nil, errors.New("pages may not talk to the AI router")
			case ip.IP.IsLinkLocalUnicast() || ip.IP.IsLinkLocalMulticast():
				return nil, errors.New("link-local network request blocked")
			case screenshotPrivate(ip.IP) && only != "" && target != only && address != only:
				return nil, errors.New("a project page may only load files from the project")
			case screenshotPrivate(ip.IP) && only == "" && public:
				return nil, errors.New("an approved public page may not reach private addresses")
			case !screenshotPrivate(ip.IP) && !public:
				return nil, errors.New("public network request blocked; approval required")
			}
		}
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	transport := &http.Transport{DialContext: dial, ResponseHeaderTimeout: 15 * time.Second}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			upstream, err := dial(ctx, "tcp", r.Host)
			if err != nil {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
			client, buffered, err := w.(http.Hijacker).Hijack()
			if err != nil {
				upstream.Close()
				return
			}
			defer client.Close()
			defer upstream.Close()
			io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
			go func() { io.Copy(upstream, buffered); upstream.Close() }()
			done := make(chan struct{})
			defer close(done)
			go func() {
				select {
				case <-ctx.Done():
					client.Close()
					upstream.Close()
				case <-done:
				}
			}()
			io.Copy(client, upstream)
			return
		}
		if r.URL.Scheme != "http" {
			http.Error(w, "only HTTP(S) is allowed", http.StatusForbidden)
			return
		}
		req := r.Clone(ctx)
		req.RequestURI = ""
		req.Header.Del("Proxy-Connection")
		res, err := transport.RoundTrip(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		defer res.Body.Close()
		for key, values := range res.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(res.StatusCode)
		io.Copy(w, res.Body)
	})
	go server.Serve(listener)
	return "http://" + listener.Addr().String(), func() { server.Close(); transport.CloseIdleConnections() }, nil
}

func screenshotChromium(ctx context.Context, binary, target, only, output string, width, height int, full, public bool) error {
	profile, err := os.MkdirTemp("", "prism-chromium-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(profile)
	proxy, stop, err := screenshotProxy(ctx, public, only, routerAddrs(ctx)...)
	if err != nil {
		return err
	}
	defer stop()
	args := browserArgs(profile, proxy, width, height)
	if full {
		return screenshotFullPage(ctx, binary, args, target, output, width, height)
	}
	args = append(args, "--hide-scrollbars", "--timeout=20000", "--virtual-time-budget=3000", "--screenshot="+output, target)
	if data, err := exec.CommandContext(ctx, binary, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("Chromium: %w: %.1000s", err, data)
	}
	if info, err := os.Stat(output); err != nil || info.Size() == 0 {
		return errors.New("Chromium finished but wrote no image")
	}
	return nil
}

// screenshotFullPage drives Chromium over the DevTools pipe to capture the whole scrollable page.
func screenshotFullPage(ctx context.Context, binary string, args []string, target, output string, width, height int) error {
	c, stop, err := startCDP(ctx, binary, args)
	if err != nil {
		return err
	}
	defer stop()
	metrics := map[string]any{"width": width, "height": height, "deviceScaleFactor": 1, "mobile": false}
	if err := c.call("Emulation.setDeviceMetricsOverride", metrics, nil); err != nil {
		return err
	}
	var navigation struct {
		ErrorText string `json:"errorText"`
	}
	if err := c.call("Page.navigate", map[string]any{"url": target}, &navigation); err != nil {
		return err
	}
	if navigation.ErrorText != "" {
		return errors.New(navigation.ErrorText)
	}
	if err := c.settle(ctx); err != nil {
		return err
	}
	if err := c.eval("new Promise(r=>setTimeout(r,350)).then(()=>true)", nil); err != nil {
		return err
	}
	var layout struct {
		CSSContentSize struct{ Width, Height float64 } `json:"cssContentSize"`
	}
	if err := c.call("Page.getLayoutMetrics", map[string]any{}, &layout); err != nil {
		return err
	}
	w, h := layout.CSSContentSize.Width, layout.CSSContentSize.Height
	if w < 1 || h < 1 || w > 8192 || h > 32768 || w*h > 64000000 {
		return errors.New("full page exceeds safe capture size (64 megapixels, 32768px tall)")
	}
	var shot struct {
		Data string `json:"data"`
	}
	clip := map[string]any{"x": 0, "y": 0, "width": w, "height": h, "scale": 1}
	if err := c.call("Page.captureScreenshot", map[string]any{"format": "png", "captureBeyondViewport": true, "clip": clip}, &shot); err != nil {
		return err
	}
	data, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return err
	}
	return os.WriteFile(output, data, 0o600)
}

func screenshotWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		return true
	}
	data, _ := os.ReadFile("/proc/version")
	return strings.Contains(strings.ToLower(string(data)), "microsoft")
}

// desktopCommands lists the capture commands to try, best first, for this operating system and session.
func desktopCommands(goos string, getenv func(string) string, output string) [][]string {
	if goos == "darwin" {
		return [][]string{{"screencapture", "-x", output}}
	}
	if goos != "linux" {
		return nil
	}
	desktop := strings.ToUpper(getenv("XDG_CURRENT_DESKTOP"))
	wayland := getenv("WAYLAND_DISPLAY") != "" || getenv("XDG_SESSION_TYPE") == "wayland"
	grim := []string{"grim", output}
	spectacle := []string{"spectacle", "-b", "-n", "-f", "-o", output}
	gnome := []string{"gnome-screenshot", "-f", output}
	var cmds [][]string
	if wayland {
		switch {
		case strings.Contains(desktop, "KDE"):
			cmds = append(cmds, spectacle, grim, gnome)
		case strings.Contains(desktop, "GNOME"):
			cmds = append(cmds, gnome, grim, spectacle)
		default:
			cmds = append(cmds, grim, spectacle, gnome)
		}
	}
	if getenv("DISPLAY") != "" {
		cmds = append(cmds, []string{"maim", output}, []string{"scrot", output}, []string{"import", "-window", "root", output})
		if !wayland {
			cmds = append(cmds, gnome, spectacle)
		}
	}
	return cmds
}

func desktopHelp(goos string, getenv func(string) string) string {
	switch goos {
	case "darwin":
		return "macOS blocks screen capture until your terminal is allowed: System Settings > Privacy & Security > Screen Recording, switch your terminal on, then restart it"
	case "linux":
		desktop := strings.ToUpper(getenv("XDG_CURRENT_DESKTOP"))
		switch {
		case getenv("WAYLAND_DISPLAY") != "" && strings.Contains(desktop, "GNOME"):
			return "GNOME on Wayland only allows screenshots through a system dialog: install gnome-screenshot and click Allow when GNOME asks (a silent capture is not possible)"
		case getenv("WAYLAND_DISPLAY") != "" && strings.Contains(desktop, "KDE"):
			return "install spectacle (KDE)"
		case getenv("WAYLAND_DISPLAY") != "":
			return "install grim (Sway, Hyprland and other wlroots desktops)"
		case getenv("DISPLAY") != "":
			return "install maim or scrot or ImageMagick (X11)"
		}
		return "no graphical session was found (DISPLAY and WAYLAND_DISPLAY are empty)"
	}
	return "desktop capture is not supported on " + goos
}

func validPNG(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	config, err := png.DecodeConfig(f)
	return err == nil && config.Width > 0 && config.Height > 0
}

func screenshotDesktop(ctx context.Context, output string) error {
	if runtime.GOOS == "linux" && screenshotWSL() {
		return screenshotWindows(ctx, output)
	}
	cmds := desktopCommands(runtime.GOOS, os.Getenv, output)
	var failures []string
	for _, command := range cmds {
		if _, err := exec.LookPath(command[0]); err != nil {
			continue
		}
		data, err := exec.CommandContext(ctx, command[0], command[1:]...).CombinedOutput()
		if err == nil && validPNG(output) {
			return nil
		}
		if err == nil {
			err = errors.New("wrote no usable image")
		}
		os.Remove(output)
		failures = append(failures, fmt.Sprintf("%s: %v %.200s", command[0], err, strings.TrimSpace(string(data))))
	}
	message := "desktop capture failed: " + desktopHelp(runtime.GOOS, os.Getenv)
	if len(failures) > 0 {
		message += " (" + strings.Join(failures, "; ") + ")"
	}
	return errors.New(message)
}

// screenshotWindows captures the Windows desktop from WSL: PowerShell saves a PNG to the
// Windows temp folder, wslpath turns that into a Linux path and we copy it.
func screenshotWindows(ctx context.Context, output string) error {
	script := `Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; ` +
		`$p = Join-Path $env:TEMP ('prism-' + [guid]::NewGuid().ToString('N') + '.png'); ` +
		`$r = [System.Windows.Forms.SystemInformation]::VirtualScreen; ` +
		`$b = New-Object System.Drawing.Bitmap $r.Width, $r.Height; ` +
		`$g = [System.Drawing.Graphics]::FromImage($b); ` +
		`$g.CopyFromScreen($r.Left, $r.Top, 0, 0, $b.Size); ` +
		`$b.Save($p, [System.Drawing.Imaging.ImageFormat]::Png); $g.Dispose(); $b.Dispose(); Write-Output $p`
	out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return fmt.Errorf("WSL desktop capture failed: %w (Windows interop must be enabled and a Windows desktop session must be running)", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	winPath := strings.TrimSpace(lines[len(lines)-1])
	if winPath == "" {
		return errors.New("WSL desktop capture: PowerShell returned no file")
	}
	linux, err := exec.CommandContext(ctx, "wslpath", "-u", winPath).Output()
	if err != nil {
		return fmt.Errorf("WSL desktop capture: wslpath: %w", err)
	}
	path := strings.TrimSpace(string(linux))
	defer os.Remove(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("WSL desktop capture: %w", err)
	}
	if err := os.WriteFile(output, data, 0o600); err != nil {
		return err
	}
	if !validPNG(output) {
		os.Remove(output)
		return errors.New("WSL desktop capture wrote no usable image")
	}
	return nil
}

// screenshotVisionImage decodes the PNG and returns a JPEG within the screenshotVision limits.
func screenshotVisionImage(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	config, err := png.DecodeConfig(f)
	if err != nil {
		return nil, err
	}
	if config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 64000000 {
		return nil, errors.New("image exceeds safe decode size")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	src, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	w, h := config.Width, config.Height
	if long := max(w, h); long > screenshotVisionSide {
		w, h = max(1, w*screenshotVisionSide/long), max(1, h*screenshotVisionSide/long)
	}
	for {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, shrink(src, w, h), &jpeg.Options{Quality: 80}); err != nil {
			return nil, err
		}
		if buf.Len() <= screenshotVisionBytes {
			return buf.Bytes(), nil
		}
		if w <= 160 && h <= 160 {
			return nil, errors.New("image cannot be reduced below 1.5 MB")
		}
		w, h = max(1, w*3/4), max(1, h*3/4)
	}
}

// shrink box-averages src into a w x h image, composited on white so transparency is not lost to black.
func shrink(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0, y1 := y*sh/h, max(y*sh/h+1, (y+1)*sh/h)
		for x := 0; x < w; x++ {
			x0, x1 := x*sw/w, max(x*sw/w+1, (x+1)*sw/w)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					pr, pg, pb, pa := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					r, g, bl, a, n = r+uint64(pr), g+uint64(pg), bl+uint64(pb), a+uint64(pa), n+1
				}
			}
			r, g, bl, a = r/n, g/n, bl/n, a/n
			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = uint8((r + 0xffff - a) >> 8)
			dst.Pix[i+1] = uint8((g + 0xffff - a) >> 8)
			dst.Pix[i+2] = uint8((bl + 0xffff - a) >> 8)
			dst.Pix[i+3] = 255
		}
	}
	return dst
}

// visionError is a failed vision request; unavailable means the router has no image-capable model.
type visionError struct {
	unavailable bool
	msg         string
}

func (e *visionError) Error() string { return e.msg }

func screenshotVision(ctx context.Context, api, model, question string, data []byte) (string, error) {
	if api == "" {
		return "", errors.New("router URL is not configured")
	}
	body := map[string]any{
		"model": "auto:vision", "stream": false, "max_tokens": 2048,
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": question},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)}},
		}}},
	}
	if model != "" && !strings.HasPrefix(model, "auto") {
		body["prism_prefer"] = model
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(api, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach the router at %s", api)
	}
	defer res.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		message := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &failure) == nil && failure.Error.Message != "" {
			message = failure.Error.Message
		}
		if len(message) > 300 {
			message = message[:300]
		}
		unavailable := res.StatusCode == http.StatusServiceUnavailable || res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusBadRequest
		return "", &visionError{unavailable: unavailable, msg: fmt.Sprintf("router HTTP %d: %s", res.StatusCode, message)}
	}
	var reply struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return "", err
	}
	if len(reply.Choices) == 0 || strings.TrimSpace(reply.Choices[0].Message.Content) == "" {
		return "", errors.New("the vision model returned no description")
	}
	return strings.TrimSpace(reply.Choices[0].Message.Content), nil
}

// The screenshot browser is chrome-headless-shell from Chrome for Testing, pinned to one version and
// checked against a SHA-256 before it is unpacked. It is not baked into the agent image (it would
// double the image): the first screenshot downloads it once into the persistent config folder.
// URLs and checksums come from googlechromelabs.github.io/chrome-for-testing (last-known-good, Stable).
const screenshotBrowserVersion = "156.0.8078.47"

type screenshotBrowserBuild struct {
	platform, url, sha256 string
	megabytes             int
}

var screenshotBrowsers = map[string]screenshotBrowserBuild{
	"amd64": {"linux64", "https://storage.googleapis.com/chrome-for-testing-public/" + screenshotBrowserVersion + "/linux64/chrome-headless-shell-linux64.zip",
		"0d5acc62c3e213f8c87d3c298d66ffcf9a236d12ea4b9f85318bf85dbc279daa", 126},
	"arm64": {"linux-arm64", "https://storage.googleapis.com/chrome-for-testing-public/" + screenshotBrowserVersion + "/linux-arm64/chrome-headless-shell-linux-arm64.zip",
		"955059fa7020d7be85eb503e21339fd4d718f51f5996d3b60633fbf908bda132", 126},
}

// screenshotBrowserDir is where the downloaded browser lives. In the agent container HOME is
// /home/prism and ~/.config/prism is the prism-config volume, so it survives across runs.
var screenshotBrowserDir = func() string {
	if dir := configDir(); dir != "" {
		return filepath.Join(dir, "browser")
	}
	return ""
}

// screenshotFetch is a variable so tests can fake the network.
var screenshotFetch = realScreenshotFetch

func realScreenshotFetch(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 30 * time.Second, TLSHandshakeTimeout: 20 * time.Second}}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("server answered %s", res.Status)
	}
	file, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, res.Body); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// screenshotBrowser returns the browser to run: one already on PATH, the downloaded copy, or a
// fresh download. note is non-empty when a download happened, so the user and model both see it.
func screenshotBrowser(ctx context.Context) (path, note string, err error) {
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome-headless-shell"} {
		if found, err := exec.LookPath(name); err == nil {
			return found, "", nil
		}
	}
	build, ok := screenshotBrowsers[runtime.GOARCH]
	if runtime.GOOS != "linux" || !ok {
		return "", "", errors.New("Chromium is not installed (install Chromium or Google Chrome and put it on PATH; the prism agent image downloads one by itself)")
	}
	root := screenshotBrowserDir()
	if root == "" {
		return "", "", errors.New("Chromium is not installed and there is no config folder to download the screenshot browser into")
	}
	home := filepath.Join(root, screenshotBrowserVersion+"-"+build.platform)
	binary := filepath.Join(home, "chrome-headless-shell-"+build.platform, "chrome-headless-shell")
	if info, err := os.Stat(binary); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
		return binary, "", nil
	}
	dlCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err := installScreenshotBrowser(dlCtx, build, root, home); err != nil {
		hint := "check the internet connection and try again"
		if ctx.Err() != nil {
			hint = "the screenshot was cancelled"
		}
		return "", "", fmt.Errorf("Chromium is not installed and the screenshot browser could not be downloaded (about %d MB, once): %w; %s. Or install Chromium yourself (apt install chromium)", build.megabytes, err, hint)
	}
	if info, err := os.Stat(binary); err != nil || info.Mode()&0o111 == 0 {
		os.RemoveAll(home)
		return "", "", errors.New("the downloaded screenshot browser archive did not contain chrome-headless-shell")
	}
	return binary, fmt.Sprintf("First screenshot: downloaded the screenshot browser (about %d MB, once); it is kept for next time.\n", build.megabytes), nil
}

// installScreenshotBrowser downloads, verifies and unpacks the archive into home, atomically.
func installScreenshotBrowser(ctx context.Context, build screenshotBrowserBuild, root, home string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %w", root, err)
	}
	work, err := os.MkdirTemp(root, ".download-")
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", root, err)
	}
	defer os.RemoveAll(work)
	archive := filepath.Join(work, "browser.zip")
	if err := screenshotFetch(ctx, build.url, archive); err != nil {
		return err
	}
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return err
	}
	if got := fmt.Sprintf("%x", hash.Sum(nil)); got != build.sha256 {
		return fmt.Errorf("checksum mismatch (got %.12s..., expected %.12s...), the download was discarded", got, build.sha256)
	}
	reader, err := zip.NewReader(file, size)
	if err != nil {
		return err
	}
	unpacked := filepath.Join(work, "unpacked")
	if err := extractScreenshotZip(reader, unpacked); err != nil {
		return err
	}
	if err := os.Rename(unpacked, home); err != nil {
		if _, statErr := os.Stat(home); statErr == nil {
			return nil // another prism finished the same download first
		}
		return err
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") && filepath.Join(root, entry.Name()) != home {
				os.RemoveAll(filepath.Join(root, entry.Name()))
			}
		}
	}
	return nil
}

func extractScreenshotZip(reader *zip.Reader, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	prefix := filepath.Clean(dest) + string(os.PathSeparator)
	for _, entry := range reader.File {
		target := filepath.Join(dest, entry.Name)
		if !strings.HasPrefix(target, prefix) {
			return fmt.Errorf("unsafe path in archive: %s", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			continue // no symlinks or devices
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := entry.Open()
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if entry.Mode()&0o111 != 0 {
			mode = 0o755
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			in.Close()
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}
