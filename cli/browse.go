package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

var browseSpec = spec("browse", "Use a web page like a person, in a fresh headless browser (no saved data, so list every step from the start), and check the result. Targets: role and name as the outline shows them (button \"Add\"), visible text (\"Active\"), a role inside a row (checkbox in \"Buy milk\"), or a ref (e12). Returns each step's result, page errors, and the page outline afterwards. Example steps: [{\"type\":\"Buy milk\",\"into\":\"textbox\"},{\"press\":\"Enter\"},{\"click\":\"checkbox in \\\"Buy milk\\\"\"},{\"click\":\"Active\"},{\"expect_not\":\"Buy milk\"},{\"reload\":true}]",
	map[string]any{
		"url":  str("localhost or private-network address"),
		"file": str("Project .html file"),
		"steps": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"click":            str("target"),
					"type":             str("text to type"),
					"into":             str("field to clear and type into"),
					"press":            str("Enter, Tab, Escape, Backspace, Delete, Space, an arrow key, or one letter or digit"),
					"select":           str("option text"),
					"in":               str("dropdown"),
					"reload":           map[string]any{"type": "boolean", "description": "keeps localStorage"},
					"expect":           str("exact text that must be visible"),
					"expect_not":       str("exact text that must not be visible"),
					"expect_count":     integer("number of visible matches of of"),
					"of":               str("target"),
					"expect_checked":   str("target"),
					"expect_unchecked": str("target"),
				},
			},
		},
		"width":  integer("default 1280"),
		"height": integer("default 800"),
	})

type browseStep struct {
	Click           string `json:"click"`
	Type            string `json:"type"`
	Into            string `json:"into"`
	Press           string `json:"press"`
	Select          string `json:"select"`
	In              string `json:"in"`
	Reload          bool   `json:"reload"`
	Expect          string `json:"expect"`
	ExpectNot       string `json:"expect_not"`
	ExpectCount     *int   `json:"expect_count"`
	Of              string `json:"of"`
	ExpectChecked   string `json:"expect_checked"`
	ExpectUnchecked string `json:"expect_unchecked"`
}

const (
	browseMaxSteps  = 40
	browseKeepShots = 20
)

func (a *Agent) browse(ctx context.Context, raw string) toolResult {
	return browseTool(ctx, screenshotEnv{dir: a.dir, api: a.api, system: a.system, approve: a.approve}, raw)
}

func browseTool(ctx context.Context, env screenshotEnv, raw string) toolResult {
	var args struct {
		URL    string       `json:"url"`
		File   string       `json:"file"`
		Steps  []browseStep `json:"steps"`
		Width  int          `json:"width"`
		Height int          `json:"height"`
	}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			return fail("invalid browse arguments: " + err.Error() + `. Example: {"file":"index.html","steps":[{"type":"Buy milk","into":"textbox"},{"press":"Enter"},{"expect":"Buy milk"}]}`)
		}
	}
	if (args.URL == "") == (args.File == "") {
		return fail("provide exactly one of url or file")
	}
	if len(args.Steps) > browseMaxSteps {
		return fail(fmt.Sprintf("at most %d steps per call", browseMaxSteps))
	}
	for i, s := range args.Steps {
		if s.kind() == "" {
			return fail(fmt.Sprintf("step %d has no action: use click, type, press, select, reload, expect, expect_not, expect_count, expect_checked or expect_unchecked", i+1))
		}
	}
	width, height := args.Width, args.Height
	if width <= 0 {
		width = 1280
	}
	if height <= 0 {
		height = 800
	}
	if width > 3840 || height > 2160 {
		return fail("viewport must be at most 3840 x 2160")
	}
	page, err := openPage(ctx, env, args.URL, args.File)
	if err != nil {
		return fail(err.Error())
	}
	defer page.close()
	binary, note, err := screenshotBrowser(ctx)
	if err != nil {
		return fail("browse failed: " + err.Error())
	}
	runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	report, err := runBrowse(runCtx, binary, page, args.Steps, width, height)
	if err != nil {
		return fail("browse failed: " + err.Error())
	}
	report.out = note + report.out
	return toolResult{ok: report.failed == 0, output: report.out, summary: report.summary()}
}

type browseReport struct {
	out            string
	checks, failed int
	checkFails     int
	actions        int
	stoppedAt      string
}

func (r browseReport) summary() string {
	parts := []string{}
	if r.actions > 0 {
		parts = append(parts, fmt.Sprintf("%d actions", r.actions))
	}
	if r.checks > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d checks passed", r.checks-r.checkFails, r.checks))
	}
	if r.stoppedAt != "" {
		parts = append(parts, "stopped: "+r.stoppedAt)
	}
	if len(parts) == 0 {
		return "page outline"
	}
	return strings.Join(parts, " · ")
}

func (s browseStep) kind() string {
	switch {
	case s.Click != "":
		return "click"
	case s.Type != "":
		return "type"
	case s.Press != "":
		return "press"
	case s.Select != "":
		return "select"
	case s.Reload:
		return "reload"
	case s.Expect != "":
		return "expect"
	case s.ExpectNot != "":
		return "expect_not"
	case s.ExpectCount != nil:
		return "expect_count"
	case s.ExpectChecked != "":
		return "expect_checked"
	case s.ExpectUnchecked != "":
		return "expect_unchecked"
	}
	return ""
}

func (s browseStep) String() string {
	switch s.kind() {
	case "click":
		return "click " + s.Click
	case "type":
		if s.Into != "" {
			return fmt.Sprintf("type %q into %s", s.Type, s.Into)
		}
		return fmt.Sprintf("type %q", s.Type)
	case "press":
		return "press " + s.Press
	case "select":
		return fmt.Sprintf("select %q in %s", s.Select, s.In)
	case "reload":
		return "reload"
	case "expect":
		return fmt.Sprintf("expect %q", s.Expect)
	case "expect_not":
		return fmt.Sprintf("expect no %q", s.ExpectNot)
	case "expect_count":
		return fmt.Sprintf("expect %d × %s", *s.ExpectCount, s.Of)
	case "expect_checked":
		return "expect checked " + s.ExpectChecked
	case "expect_unchecked":
		return "expect unchecked " + s.ExpectUnchecked
	}
	return "?"
}

type page struct {
	url, root string
	only      string
	public    bool
	close     func()
}

// openPage turns a url or project file argument into a URL Chromium may open, with the
// same rules as the screenshot tool: project files are served locally, private addresses
// are free, a public address needs the user's approval.
func openPage(ctx context.Context, env screenshotEnv, target, file string) (*page, error) {
	root, err := filepath.Abs(env.dir)
	if err != nil {
		return nil, err
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return nil, err
	}
	p := &page{url: target, root: root, close: func() {}}
	if target != "" {
		err := screenshotURL(ctx, target, false)
		if errors.Is(err, errScreenshotPublic) {
			if err := screenshotURL(ctx, target, true); err != nil {
				return nil, err
			}
			if !env.ask(ctx, "open the public web page "+target+" in a headless browser") {
				return nil, errors.New("opening a public URL was not approved (only localhost, private network addresses and project files are allowed without approval)")
			}
			p.public = true
		} else if err != nil {
			return nil, err
		}
		return p, nil
	}
	served, stop, err := serveProjectFile(root, file)
	if err != nil {
		return nil, err
	}
	p.url, p.close, p.only = served, stop, hostOf(served)
	return p, nil
}

// cdp is a Chrome DevTools Protocol client over --remote-debugging-pipe.
type cdp struct {
	mu      sync.Mutex
	in      *os.File
	reader  *bufio.Reader
	id      int
	session string
	onEvent func(method string, params json.RawMessage)
}

func startCDP(ctx context.Context, binary string, args []string) (*cdp, func(), error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, nil, err
	}
	cmd := exec.CommandContext(ctx, binary, append(args, "--remote-debugging-pipe", "about:blank")...)
	cmd.ExtraFiles = []*os.File{inR, outW}
	if err := cmd.Start(); err != nil {
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
		return nil, nil, err
	}
	inR.Close()
	outW.Close()
	stop := func() { inW.Close(); outR.Close(); cmd.Process.Kill(); cmd.Wait() }
	go func() { <-ctx.Done(); outR.Close(); inW.Close() }()
	c := &cdp{in: inW, reader: bufio.NewReader(outR)}
	var created struct {
		TargetID string `json:"targetId"`
	}
	if err := c.call("Target.createTarget", map[string]any{"url": "about:blank"}, &created); err != nil {
		stop()
		return nil, nil, err
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.call("Target.attachToTarget", map[string]any{"targetId": created.TargetID, "flatten": true}, &attached); err != nil {
		stop()
		return nil, nil, err
	}
	c.session = attached.SessionID
	return c, stop, nil
}

func (c *cdp) send(method string, params any) (int, error) {
	c.id++
	msg := map[string]any{"id": c.id, "method": method, "params": params}
	if c.session != "" {
		msg["sessionId"] = c.session
	}
	data, _ := json.Marshal(msg)
	_, err := c.in.Write(append(data, 0))
	return c.id, err
}

func (c *cdp) call(method string, params any, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, err := c.send(method, params)
	if err != nil {
		return err
	}
	for {
		raw, err := c.reader.ReadBytes(0)
		if err != nil {
			return err
		}
		var msg struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(bytes.TrimSuffix(raw, []byte{0}), &msg); err != nil {
			return err
		}
		if msg.Method != "" {
			if c.onEvent != nil {
				c.onEvent(msg.Method, msg.Params)
			}
			continue
		}
		if msg.ID != id {
			continue
		}
		if len(msg.Error) != 0 {
			return fmt.Errorf("Chromium %s: %s", method, msg.Error)
		}
		if result != nil {
			return json.Unmarshal(msg.Result, result)
		}
		return nil
	}
}

func (c *cdp) eval(expression string, result any) error {
	var out struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := c.call("Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": true, "timeout": 10000}, &out); err != nil {
		return err
	}
	if out.ExceptionDetails != nil {
		return fmt.Errorf("page script error: %s %s", out.ExceptionDetails.Text, out.ExceptionDetails.Exception.Description)
	}
	if result != nil && len(out.Result.Value) > 0 {
		return json.Unmarshal(out.Result.Value, result)
	}
	return nil
}

// settle waits for the page to finish loading, then for two animation frames.
func (c *cdp) settle(ctx context.Context) error {
	for range 100 {
		var state string
		if err := c.eval("document.readyState", &state); err != nil {
			return err
		}
		if state == "complete" {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return c.eval("Promise.all([document.fonts.ready,new Promise(r=>setTimeout(r,150))]).then(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)))).then(()=>true)", nil)
}

func browserArgs(profile, proxy string, width, height int) []string {
	args := []string{"--headless=new", "--disable-gpu", "--disable-dev-shm-usage", "--no-first-run", "--no-default-browser-check",
		"--disable-background-networking", "--disable-extensions", "--disable-quic", "--disable-crash-reporter", "--force-device-scale-factor=1",
		"--user-data-dir=" + profile, "--proxy-server=" + proxy, "--proxy-bypass-list=<-loopback>",
		"--window-size=" + fmt.Sprint(width) + "," + fmt.Sprint(height)}
	// Chromium's own sandbox needs privileges a container does not give it; the container is the sandbox.
	if os.Geteuid() == 0 || inScreenshotContainer() {
		args = append(args, "--no-sandbox")
	}
	return args
}

type pageHit struct {
	OK    bool    `json:"ok"`
	Error string  `json:"error"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Desc  string  `json:"desc"`
	Count int     `json:"count"`
	Value bool    `json:"value"`
}

func runBrowse(ctx context.Context, binary string, p *page, steps []browseStep, width, height int) (browseReport, error) {
	var report browseReport
	profile, err := os.MkdirTemp("", "prism-browse-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(profile)
	proxy, stopProxy, err := screenshotProxy(ctx, p.public, p.only, routerAddrs(ctx)...)
	if err != nil {
		return report, err
	}
	defer stopProxy()
	c, stop, err := startCDP(ctx, binary, browserArgs(profile, proxy, width, height))
	if err != nil {
		return report, fmt.Errorf("Chromium did not start: %w", err)
	}
	defer stop()

	var problems []string
	var problemsMu sync.Mutex
	addProblem := func(text string) {
		problemsMu.Lock()
		defer problemsMu.Unlock()
		text = clipLine(text, 300)
		if len(problems) < 10 && !contains(problems, text) {
			problems = append(problems, text)
		}
	}
	c.onEvent = func(method string, params json.RawMessage) {
		switch method {
		case "Runtime.exceptionThrown":
			var e struct {
				ExceptionDetails struct {
					Text      string `json:"text"`
					Exception struct {
						Description string `json:"description"`
					} `json:"exception"`
				} `json:"exceptionDetails"`
			}
			json.Unmarshal(params, &e)
			text := e.ExceptionDetails.Exception.Description
			if text == "" {
				text = e.ExceptionDetails.Text
			}
			addProblem("uncaught " + text)
		case "Runtime.consoleAPICalled":
			var e struct {
				Type string `json:"type"`
				Args []struct {
					Value       any    `json:"value"`
					Description string `json:"description"`
				} `json:"args"`
			}
			json.Unmarshal(params, &e)
			if e.Type != "error" && e.Type != "assert" {
				return
			}
			parts := []string{}
			for _, a := range e.Args {
				if a.Value != nil {
					parts = append(parts, fmt.Sprint(a.Value))
				} else {
					parts = append(parts, a.Description)
				}
			}
			addProblem("console.error " + strings.Join(parts, " "))
		case "Log.entryAdded":
			var e struct {
				Entry struct {
					Level, Text, URL string
				} `json:"entry"`
			}
			json.Unmarshal(params, &e)
			if e.Entry.Level == "error" && !strings.Contains(e.Entry.URL, "/favicon.ico") {
				addProblem(strings.TrimSpace(e.Entry.Text + " " + e.Entry.URL))
			}
		case "Page.javascriptDialogOpening":
			var e struct {
				Message, Type string
			}
			json.Unmarshal(params, &e)
			addProblem(fmt.Sprintf("%s dialog %q (accepted)", e.Type, e.Message))
			c.send("Page.handleJavaScriptDialog", map[string]any{"accept": true})
		}
	}
	for _, method := range []string{"Runtime.enable", "Log.enable", "Page.enable"} {
		if err := c.call(method, map[string]any{}, nil); err != nil {
			return report, err
		}
	}
	c.call("Page.setDownloadBehavior", map[string]any{"behavior": "deny"}, nil)
	if err := c.call("Emulation.setDeviceMetricsOverride", map[string]any{"width": width, "height": height, "deviceScaleFactor": 1, "mobile": false}, nil); err != nil {
		return report, err
	}
	var navigation struct {
		ErrorText string `json:"errorText"`
	}
	if err := c.call("Page.navigate", map[string]any{"url": p.url}, &navigation); err != nil {
		return report, err
	}
	if navigation.ErrorText != "" {
		return report, fmt.Errorf("could not open the page: %s", navigation.ErrorText)
	}
	if err := c.settle(ctx); err != nil {
		return report, err
	}

	var out strings.Builder
	var failedSteps []string
	actionsDone := 0
	shown := p.url
	if rel, ok := strings.CutPrefix(p.url, "http://127.0.0.1"); ok && strings.Contains(rel, "/") {
		shown = rel[strings.Index(rel, "/")+1:]
	}
	fmt.Fprintf(&out, "Opened %s (%dx%d, empty browser).\n", shown, width, height)
	for i, step := range steps {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		line, ok, check, err := doStep(ctx, c, step)
		if err != nil {
			fmt.Fprintf(&out, "%d. %s ✗ the browser failed: %v\n", i+1, step, err)
			failedSteps = append(failedSteps, fmt.Sprint(i+1))
			report.failed++
			report.stoppedAt = fmt.Sprintf("step %d", i+1)
			break
		}
		mark := "✓"
		if !ok {
			mark = "✗"
		}
		fmt.Fprintf(&out, "%d. %s %s%s\n", i+1, step, mark, line)
		if !ok {
			failedSteps = append(failedSteps, fmt.Sprint(i+1))
		}
		if check {
			report.checks++
			if !ok {
				report.failed++
				report.checkFails++
			}
			continue
		}
		report.actions++
		if !ok {
			report.failed++
			report.stoppedAt = fmt.Sprintf("step %d", i+1)
			if actionsDone == 0 && strings.Contains(line, "nothing matches") {
				out.WriteString("   (Each browse call starts with an empty browser: if this appears only after earlier actions, include those steps first.)\n")
			}
			if i+1 < len(steps) {
				fmt.Fprintf(&out, "Stopped at step %d: the action could not be done, so steps %d-%d did not run.\n", i+1, i+2, len(steps))
			}
			break
		}
		actionsDone++
	}
	problemsMu.Lock()
	if len(problems) > 0 {
		out.WriteString("Page errors:\n")
		for _, p := range problems {
			out.WriteString("- " + p + "\n")
		}
	}
	problemsMu.Unlock()
	var outline string
	if err := c.eval(browseJS+"__prism.outline()", &outline); err != nil {
		outline = "(the page outline could not be read: " + err.Error() + ")"
	}
	out.WriteString("Page after the last step that ran:\n" + outline + "\n")
	if saved := browseEvidence(c, p.root); saved != "" {
		out.WriteString("Screenshot of the end state: " + saved + "\n")
	}
	switch {
	case report.failed > 0:
		fmt.Fprintf(&out, "RESULT: %d of %d steps failed (step %s). If the outline shows the element under another name, fix the step; if the page is wrong, fix the page. Then run browse again with all the steps.", report.failed, len(steps), strings.Join(failedSteps, ", "))
	case len(steps) > 0:
		fmt.Fprintf(&out, "RESULT: all %d steps passed.", len(steps))
	default:
		out.WriteString("No steps given, so nothing was checked yet. Call browse again with steps that do what a user would do here, naming elements as the outline shows them, and expect what they should then see.")
	}
	report.out = out.String()
	return report, nil
}

// browseEvidence saves a viewport screenshot under .prism/checks and returns its project path.
func browseEvidence(c *cdp, root string) string {
	dir, err := screenshotPath(root, ".prism/checks")
	if err != nil || os.MkdirAll(dir, 0o700) != nil {
		return ""
	}
	var shot struct {
		Data string `json:"data"`
	}
	if c.call("Page.captureScreenshot", map[string]any{"format": "png"}, &shot) != nil {
		return ""
	}
	data, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return ""
	}
	if old, _ := filepath.Glob(filepath.Join(dir, "*.png")); len(old) >= browseKeepShots {
		sort.Strings(old)
		for _, f := range old[:len(old)-browseKeepShots+1] {
			os.Remove(f)
		}
	}
	path := filepath.Join(dir, time.Now().UTC().Format("20060102T150405.000")+"-"+randomSuffix()+".png")
	if os.WriteFile(path, data, 0o600) != nil {
		return ""
	}
	rel, _ := filepath.Rel(root, path)
	return filepath.ToSlash(rel)
}

var browseKeys = map[string]struct {
	code string
	vk   int
	text string
}{
	"Enter": {"Enter", 13, "\r"}, "Tab": {"Tab", 9, ""}, "Escape": {"Escape", 27, ""}, "Backspace": {"Backspace", 8, ""},
	"Space": {"Space", 32, " "}, "ArrowUp": {"ArrowUp", 38, ""}, "ArrowDown": {"ArrowDown", 40, ""},
	"ArrowLeft": {"ArrowLeft", 37, ""}, "ArrowRight": {"ArrowRight", 39, ""}, "Delete": {"Delete", 46, ""},
}

// doStep runs one step. line is extra detail for the report, ok whether it worked, and check
// whether it was a check (checks never stop the run).
func doStep(ctx context.Context, c *cdp, s browseStep) (line string, ok, check bool, err error) {
	find := func(target string) (pageHit, error) {
		var f pageHit
		data, _ := json.Marshal(target)
		err := c.eval(browseJS+"__prism.find("+string(data)+")", &f)
		return f, err
	}
	click := func(target string) (string, bool, error) {
		f, err := find(target)
		if err != nil || !f.OK {
			return " " + f.Error, false, err
		}
		for _, kind := range []string{"mouseMoved", "mousePressed", "mouseReleased"} {
			event := map[string]any{"type": kind, "x": f.X, "y": f.Y, "button": "left", "clickCount": 1}
			if kind == "mouseMoved" {
				event["button"] = "none"
			}
			if err := c.call("Input.dispatchMouseEvent", event, nil); err != nil {
				return "", false, err
			}
		}
		detail := " (" + f.Desc + ")"
		if f.Count > 1 {
			detail = fmt.Sprintf(" (%s, first of %d matches)", f.Desc, f.Count)
		}
		return detail, true, c.settle(ctx)
	}
	switch s.kind() {
	case "click":
		line, ok, err = click(s.Click)
	case "type":
		if s.Into != "" {
			if line, ok, err = click(s.Into); err != nil || !ok {
				return line, ok, false, err
			}
			if err = c.eval(browseJS+"__prism.clear()", nil); err != nil {
				return "", false, false, err
			}
		}
		if err = c.call("Input.insertText", map[string]any{"text": s.Type}, nil); err == nil {
			ok, err = true, c.settle(ctx)
		}
	case "press":
		key, known := browseKeys[s.Press]
		if r := []rune(s.Press); !known && len(r) == 1 && r[0] > ' ' && r[0] < 127 {
			code := "Key" + strings.ToUpper(s.Press)
			if r[0] >= '0' && r[0] <= '9' {
				code = "Digit" + s.Press
			}
			key, known = struct {
				code string
				vk   int
				text string
			}{code, int(unicode.ToUpper(r[0])), s.Press}, true
		}
		if !known {
			return " unknown key; use Enter, Tab, Escape, Backspace, Space, Delete, an arrow key or one letter or digit", false, false, nil
		}
		down := map[string]any{"type": "keyDown", "key": s.Press, "code": key.code, "windowsVirtualKeyCode": key.vk}
		if s.Press == "Space" {
			down["key"] = " "
		}
		if key.text != "" {
			down["text"] = key.text
		}
		if err = c.call("Input.dispatchKeyEvent", down, nil); err == nil {
			up := map[string]any{"type": "keyUp", "key": down["key"], "code": key.code, "windowsVirtualKeyCode": key.vk}
			if err = c.call("Input.dispatchKeyEvent", up, nil); err == nil {
				ok, err = true, c.settle(ctx)
			}
		}
	case "select":
		var f pageHit
		a, _ := json.Marshal(s.In)
		b, _ := json.Marshal(s.Select)
		if err = c.eval(browseJS+"__prism.choose("+string(a)+","+string(b)+")", &f); err == nil {
			if !f.OK {
				return " " + f.Error, false, false, nil
			}
			ok, err, line = true, c.settle(ctx), " ("+f.Desc+")"
		}
	case "reload":
		var before float64
		if err = c.eval("performance.timeOrigin", &before); err != nil {
			break
		}
		if err = c.call("Page.reload", map[string]any{}, nil); err != nil {
			break
		}
		for range 100 {
			var now float64
			if c.eval("performance.timeOrigin", &now) == nil && now != before {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		ok, err = true, c.settle(ctx)
	default:
		check = true
		var f pageHit
		data, _ := json.Marshal(s)
		if err = c.eval(browseJS+"__prism.check("+string(data)+")", &f); err == nil {
			ok = f.Value
			if f.Error != "" {
				line = " " + f.Error
			}
		}
	}
	return line, ok, check, err
}

func clipLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// browseJS defines window.__prism in the page (again after a reload): an outline of what a
// user sees, with roles and accessible names, and target lookup by ref, role and name, or text.
const browseJS = `(window.__prism||(window.__prism=(()=>{
const TAGS={A:'link',BUTTON:'button',TEXTAREA:'textbox',SELECT:'combobox',H1:'heading',H2:'heading',H3:'heading',H4:'heading',H5:'heading',H6:'heading',LI:'listitem',UL:'list',OL:'list',IMG:'img',DIALOG:'dialog',SUMMARY:'button',OPTION:'option',TR:'row',PROGRESS:'progressbar'};
const INPUTS={checkbox:'checkbox',radio:'radio',button:'button',submit:'button',reset:'button',image:'button',range:'slider',color:'button',file:'button',hidden:''};
const SHOWN=new Set(['heading','button','link','textbox','searchbox','checkbox','radio','switch','combobox','listbox','slider','spinbutton','listitem','list','img','dialog','alertdialog','alert','status','tab','tablist','tabpanel','menuitem','option','row','progressbar','navigation','text']);
const NAMED=new Set(['button','link','heading','listitem','option','tab','menuitem','row','text','img']);
const clean=s=>String(s||'').replace(/\s+/g,' ').trim();
const clip=(s,n)=>s.length>n?s.slice(0,n)+'…':s;
const role=e=>{const r=e.getAttribute('role');if(r&&r!=='presentation'&&r!=='none')return r.split(' ')[0];if(e.tagName==='INPUT'){const t=(e.type||'text').toLowerCase();return t in INPUTS?INPUTS[t]:(t==='search'?'searchbox':'textbox')}if(e.tagName==='A')return e.hasAttribute('href')||e.onclick||e.getAttribute('onclick')?'link':'';if(e.hasAttribute('contenteditable')&&e.isContentEditable)return 'textbox';return TAGS[e.tagName]||''};
const own=e=>{let t='';for(const n of e.childNodes)if(n.nodeType===3)t+=n.nodeValue;return clean(t)};
const rowText=e=>{const r=e.closest('li,tr,[role=listitem],[role=row],label');return r?plain(r):''};
const plain=r=>{let t='';const w=document.createTreeWalker(r,NodeFilter.SHOW_TEXT);while(w.nextNode()){const p=w.currentNode.parentElement;if(p&&p.closest('button,a,[role=button]')&&r.contains(p.closest('button,a,[role=button]')))continue;t+=' '+w.currentNode.nodeValue}return clean(t)};
const name=(e,r)=>{const al=e.getAttribute('aria-label');if(al)return clean(al);const lb=e.getAttribute('aria-labelledby');if(lb)return clean(lb.split(' ').map(id=>{const x=document.getElementById(id);return x?x.innerText:''}).join(' '));
if(e.tagName==='INPUT'||e.tagName==='TEXTAREA'||e.tagName==='SELECT'){const ls=e.labels&&[...e.labels].map(l=>clean(l.innerText)).filter(Boolean);if(ls&&ls.length)return ls.join(' ');if(e.placeholder)return clean(e.placeholder);if(/^(button|submit|reset)$/.test(e.type)&&e.value)return clean(e.value);if(e.title)return clean(e.title);if(r==='checkbox'||r==='radio'||r==='switch')return rowText(e);return ''}
if(e.tagName==='IMG')return clean(e.alt||e.title);if(r==='text')return plain(e);if(r==='listitem'||r==='row')return plain(e)||clean(e.innerText);if(NAMED.has(r)||r==='dialog'||r==='alert'||r==='status')return clean(e.innerText||e.textContent);const t=clean(e.title);return !t&&(r==='checkbox'||r==='radio'||r==='switch')?rowText(e):t};
const hidden=e=>{if(e.closest('[hidden],[aria-hidden="true"]'))return true;const s=getComputedStyle(e);if(s.display==='none'||s.visibility==='hidden'||s.visibility==='collapse')return true;const b=e.getBoundingClientRect();return b.width===0&&b.height===0&&s.position!=='fixed'&&!e.getClientRects().length};
const clickable=e=>{if(!hidden(e)){const b=e.getBoundingClientRect();if(b.width>1&&b.height>1)return e}if(e.labels)for(const l of e.labels)if(!hidden(l))return l;return null};
let seq=0;const ref=e=>{if(!e.dataset.prismRef)e.dataset.prismRef='e'+(++seq);return e.dataset.prismRef};
const items=()=>{const out=[];const walk=(e,depth,inNamed)=>{for(const c of e.children){if(c.tagName==='SCRIPT'||c.tagName==='STYLE'||c.tagName==='TEMPLATE'||c.tagName==='NOSCRIPT')continue;const r0=role(c);const isInput=r0==='checkbox'||r0==='radio'||r0==='switch';if(hidden(c)&&!(isInput&&clickable(c)))continue;let r=r0;if(!r&&!inNamed&&own(c))r='text';if(r&&SHOWN.has(r)){const n=name(c,r);if(r==='text'&&!n){walk(c,depth,inNamed);continue}out.push({e:c,r,n,depth});walk(c,depth+1,inNamed||NAMED.has(r))}else walk(c,depth,inNamed)}};walk(document.body,0,false);return out};
const state=(e,r)=>{const s=[];if(r==='checkbox'||r==='radio'||r==='switch'){const v=e.checked??e.getAttribute('aria-checked')==='true';s.push(v?'checked':'unchecked')}if(e.getAttribute('aria-pressed')==='true')s.push('pressed');if(e.getAttribute('aria-selected')==='true'||e.getAttribute('aria-current')&&e.getAttribute('aria-current')!=='false')s.push('current');if(e.getAttribute('aria-expanded'))s.push(e.getAttribute('aria-expanded')==='true'?'expanded':'collapsed');if(e.disabled||e.getAttribute('aria-disabled')==='true')s.push('disabled');if((r==='textbox'||r==='searchbox')&&'value' in e&&e.value)s.push('value='+JSON.stringify(clip(e.value,60)));if(r==='combobox'&&e.tagName==='SELECT'&&e.selectedOptions[0])s.push('selected='+JSON.stringify(clean(e.selectedOptions[0].text)));return s};
const line=it=>' '.repeat(2*it.depth)+it.r+(it.n?' '+JSON.stringify(clip(it.n,80)):'')+state(it.e,it.r).map(x=>' '+x).join('')+' ['+ref(it.e)+']';
const parse=t=>{t=clean(t);let m;if(m=t.match(/^\[?(e\d+)\]?$/))return{ref:m[1]};if(m=t.match(/^([a-z]+)\s+["'\u201c](.*)["'\u201d\u2019]$/i))return{role:m[1].toLowerCase(),name:m[2]};if(m=t.match(/^["'\u201c](.*)["'\u201d\u2019]$/))return{name:m[1]};const w=t.toLowerCase();if(/^[a-z]+$/.test(w)&&(SHOWN.has(w)||ROLEALIAS[w]))return{role:w};return{name:t}};
const parseIn=t=>{const m=clean(t).match(/^(.+?)\s+in\s+(?:the\s+)?(?:(?:row|listitem|item|list item)\s+)?["'\u201c]?(.*?)["'\u201d\u2019]?$/i);return m&&m[2]?{...parse(m[1]),within:m[2]}:null};
const ROLEALIAS={input:'textbox',field:'textbox',textfield:'textbox',searchbox:'textbox',dropdown:'combobox',select:'combobox',item:'listitem',toggle:'checkbox'};
const said=i=>[i.n.toLowerCase(),clean(i.e.innerText||'').toLowerCase()];
const byName=(c,name,typed)=>{const n=name.toLowerCase();const exact=c.filter(i=>said(i).some(x=>x===n));return rank(exact.length?exact:c.filter(i=>said(i).some(x=>x&&x.includes(n))),typed)};
const byRole=(all,role)=>{const r=role&&(ROLEALIAS[role]||role);return all.filter(i=>!r||i.r===r||(r==='textbox'&&i.r==='searchbox'))};
const matchIn=(q,all)=>{let c=byRole(all,q.role);if(q.name!==undefined)c=byName(c,q.name,!!q.role);const w=q.within.toLowerCase();for(const test of [i=>i.n.toLowerCase()===w,i=>i.n.toLowerCase().includes(w)]){const boxes=all.filter(i=>i.r!=='text'&&test(i)).map(i=>i.e);const inside=c.filter(i=>(q.name===undefined&&test(i))||boxes.some(b=>b!==i.e&&b.contains(i.e)));if(inside.length)return inside}return[]};
const match=t=>{const all=items();const pin=parseIn(t);if(pin){const r=matchIn(pin,all);if(r.length)return r}const q=parse(t);if(q.ref){const it=all.find(i=>i.e.dataset.prismRef===q.ref);return it?[it]:[]}const c=byRole(all,q.role);return q.name===undefined?c:byName(c,q.name,!!q.role)};
const ACT=new Set(['button','link','checkbox','radio','switch','textbox','searchbox','combobox','tab','menuitem','option','slider']);
const rank=(l,typed)=>typed?l:[...l.filter(i=>ACT.has(i.r)),...l.filter(i=>!ACT.has(i.r)&&i.r!=='listitem'&&i.r!=='row'),...l.filter(i=>i.r==='listitem'||i.r==='row')];
const desc=it=>it.r+(it.n?' '+JSON.stringify(clip(it.n,60)):'')+' ['+ref(it.e)+']';
const notFound=t=>{const q=parseIn(t)||parse(t);const all=items();const near=all.filter(i=>!q.role||i.r===q.role).slice(0,12);const same=near.length>1&&near.every(i=>i.n===near[0].n);return 'nothing matches '+JSON.stringify(t)+(near.length?'. Visible '+(q.role?q.role+' elements':'elements')+': '+near.map(desc).join(', ')+(same||q.role?'. To pick the one in a row, write '+(q.role||near[0].r)+' in "row text"':''):(q.role?'. There is no '+q.role+' on the page (a div or span with a click handler shows as text; use a real control or give it a role and aria-label)':'. The page shows nothing that matches.'))};
return{
outline(){const l=items().map(line);const body=clean(document.body&&document.body.innerText);if(!l.length)return body?'(no headings, buttons, fields or lists; text: '+JSON.stringify(clip(body,300))+')':'(the page is blank)';return (l.length>200?l.slice(0,200).concat(['… '+(l.length-200)+' more']):l).join('\n')},
find(t){const m=match(t);if(!m.length)return{ok:false,error:notFound(t)};const it=m[0];const el=clickable(it.e)||it.e;el.scrollIntoView({block:'center',inline:'center'});const b=el.getBoundingClientRect();if(it.e.focus&&(it.r==='textbox'||it.r==='searchbox'||it.r==='combobox'))it.e.focus();window.__prismTarget=it.e;return{ok:true,x:b.left+b.width/2,y:b.top+b.height/2,desc:desc(it),count:m.length}},
clear(){const e=document.activeElement&&document.activeElement!==document.body?document.activeElement:window.__prismTarget;if(!e)return false;if('value' in e){e.value='';e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}))}else if(e.isContentEditable){e.textContent=''}e.focus();return true},
choose(t,option){const m=match(t).filter(i=>i.e.tagName==='SELECT');const sel=m.length?m[0]:match('combobox')[0];if(!sel)return{ok:false,error:notFound(t||'combobox')};const o=[...sel.e.options].find(o=>clean(o.text).toLowerCase()===clean(option).toLowerCase()||o.value===option)||[...sel.e.options].find(o=>clean(o.text).toLowerCase().includes(clean(option).toLowerCase()));if(!o)return{ok:false,error:'no option '+JSON.stringify(option)+'; options: '+[...sel.e.options].map(o=>JSON.stringify(clean(o.text))).join(', ')};sel.e.value=o.value;sel.e.dispatchEvent(new Event('input',{bubbles:true}));sel.e.dispatchEvent(new Event('change',{bubbles:true}));return{ok:true,desc:desc(sel)}},
check(s){const text=clean(document.body.innerText).toLowerCase();const has=v=>text.includes(clean(v).toLowerCase())||items().some(i=>(i.r==='textbox'||i.r==='searchbox')&&String(i.e.value||'').toLowerCase().includes(clean(v).toLowerCase()));
if(s.expect)return has(s.expect)?{value:true}:{value:false,error:'not found; the page text starts: '+JSON.stringify(clip(clean(document.body.innerText),200))};
if(s.expect_not)return has(s.expect_not)?{value:false,error:'still on the page'}:{value:true};
if(s.expect_count!=null){const n=s.of?match(s.of).length:0;return n===s.expect_count?{value:true}:{value:false,error:'found '+n}}
const t=s.expect_checked||s.expect_unchecked;const m=match(t).filter(i=>i.r==='checkbox'||i.r==='radio'||i.r==='switch');if(!m.length)return{value:false,error:notFound(t)};const v=!!(m[0].e.checked??m[0].e.getAttribute('aria-checked')==='true');const want=!!s.expect_checked;return v===want?{value:true}:{value:false,error:'it is '+(v?'checked':'unchecked')+' ('+desc(m[0])+')'}}
}})())),`
