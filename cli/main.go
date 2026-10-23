package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

const (
	fastFrame = 33 * time.Millisecond
	slowFrame = 100 * time.Millisecond
)

type tickMsg time.Time
type agentMsg struct{ event any }
type statusMsg struct {
	ready int
	err   error
}

type entry struct {
	kind  string
	text  string
	id    string
	tool  string
	arg   string
	done  bool
	res   toolResult
	open  bool
	rows  []routeRow
	lines []string
	crew  *crewBlock
}

type model struct {
	w, h           int
	theme          int
	start          time.Time
	now            time.Duration
	chat           bool
	chatAt         time.Duration
	input          []rune
	entries        []*entry
	busy           bool
	busySince      time.Duration
	thinking       bool
	reasoning      string
	thinkSince     time.Duration
	waited         time.Duration
	cancel         context.CancelFunc
	events         chan any
	agent          *Agent
	route          string
	tokens         int
	window         int
	scroll         int
	ready          int
	offline        bool
	project        string
	gallery        string
	say            string
	sayAt          time.Duration
	doneAt         time.Duration
	sleepyUntil    time.Duration
	beamAt         time.Duration
	beamOn         bool
	quiet          bool
	turnTools      int
	turnTests      bool
	turnFailed     bool
	proud          bool
	lastTextAt     time.Duration
	surprisedUntil time.Duration
	draft          *evDraft
	answering      bool
	scrollPos      float64
	expandAll      bool
	web            *webSession
	pending        *evApprove
	queued         []*evApprove
	ticking        bool
	active         time.Duration
}

// idleAfter is how long the screen keeps animating after the last key, event or resize.
// After that the frame loop stops completely (no redraws, no CPU) until something happens.
const idleAfter = 3 * time.Second

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) wait() tea.Cmd {
	events := m.events
	return func() tea.Msg { return agentMsg{<-events} }
}

func (m model) Init() tea.Cmd { return tea.Batch(tick(fastFrame), m.wait(), fetchStatus(m.agent.api)) }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, isTick := msg.(tickMsg); !isTick && !m.ticking {
		m.now = time.Since(m.start) // the clock stood still while idle
	}
	next, cmd := m.update(msg)
	nm, ok := next.(model)
	if _, isTick := msg.(tickMsg); !ok || isTick {
		return next, cmd
	}
	nm.now = time.Since(nm.start)
	nm.active = nm.now
	if !nm.ticking {
		nm.ticking = true
		cmd = tea.Batch(cmd, tick(fastFrame))
	}
	return nm, cmd
}

// idle reports whether nothing on screen is moving and nothing is waiting on a timer.
func (m model) idle() bool {
	return m.web == nil && m.gallery == "" && !m.busy && !m.animating() && m.now-m.active > idleAfter &&
		m.now-m.sayAt > 5*time.Second && m.now >= m.sleepyUntil &&
		(m.chat || m.now > 15*time.Second) && m.scrollPos == float64(m.scroll)
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tickMsg:
		m.now = time.Since(m.start)
		if d := float64(m.scroll) - m.scrollPos; math.Abs(d) > 0.05 {
			m.scrollPos += d * 0.35
		} else {
			m.scrollPos = float64(m.scroll)
		}
		m.checkWeb()
		if m.idle() {
			m.ticking = false
			// Nothing will draw until the next key or event: hand freed memory back to the system.
			go debug.FreeOSMemory()
			return m, nil
		}
		frame := slowFrame
		if m.gallery != "" || (!m.chat && m.now < 4*time.Second) || m.busy || m.now-m.chatAt < time.Second || m.animating() {
			frame = fastFrame
		} else if !m.chat {
			frame = 50 * time.Millisecond
		}
		return m, tick(frame)
	case statusMsg:
		m.ready, m.offline = msg.ready, msg.err != nil
	case modelsMsg:
		m.entries = append(m.entries, &entry{kind: "models", rows: msg.rows, text: errText(msg.err)})
		m.scroll = 0
	case agentMsg:
		m.handle(msg.event)
		return m, m.wait()
	case tea.PasteMsg:
		if m.chat {
			m.input = append(m.input, []rune(strings.ReplaceAll(msg.Content, "\r\n", "\n"))...)
		}
	case tea.MouseClickMsg:
		if mouse := tea.Mouse(msg); m.chat && mouse.Button == tea.MouseLeft {
			lines, start, end, top, _ := m.visible(themes[m.theme])
			if idx := start + mouse.Y - top; mouse.Y >= top && idx < end {
				if ref := lines[idx].ref; ref != nil {
					ref.open = !ref.open
				}
			}
		}
	case tea.MouseWheelMsg:
		switch tea.Mouse(msg).Button {
		case tea.MouseWheelUp:
			m.scrollBy(3)
		case tea.MouseWheelDown:
			m.scrollBy(-3)
		}
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		if m.cancel != nil {
			m.cancel()
		}
		m.endWeb()
		return m, tea.Quit
	}
	if m.gallery != "" {
		if k == "ctrl+t" {
			m.theme = (m.theme + 1) % len(themes)
		}
		return m, nil
	}
	if !m.chat {
		m.openChat()
		return m, nil
	}
	if m.web != nil {
		switch k {
		case "enter":
			return m, m.endWeb()
		case "o":
			m.startWeb()
		case "c":
			return m, copyWebLink(m.web.url)
		}
		return m, nil
	}
	if m.pending != nil {
		switch k {
		case "y", "Y", "enter":
			m.answer(true)
		case "n", "N", "esc":
			m.answer(false)
		}
		return m, nil
	}
	switch k {
	case "esc":
		if m.cancel != nil {
			m.cancel()
		}
	case "ctrl+t":
		m.theme = (m.theme + 1) % len(themes)
		saveTheme(themes[m.theme].name)
	case "enter":
		return m, m.submit()
	case "tab":
		if found := matching(string(m.input)); len(found) > 0 {
			m.input = []rune(found[0].name + " ")
		}
	case "shift+enter", "ctrl+j":
		m.input = append(m.input, '\n')
	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	case "ctrl+u":
		m.input = nil
	case "ctrl+o":
		m.expandAll = !m.expandAll
	case "pgup":
		m.scrollBy(m.page())
	case "pgdown":
		m.scrollBy(-m.page())
	case "end":
		m.scroll = 0
	default:
		if msg.Text != "" {
			m.input = append(m.input, []rune(msg.Text)...)
		}
	}
	return m, nil
}

func (m *model) openChat() {
	m.chat = true
	m.chatAt = m.now
	m.greet()
}

func (m *model) submit() tea.Cmd {
	text := strings.TrimSpace(string(m.input))
	if text == "" || m.busy {
		return nil
	}
	m.input, m.scroll = nil, 0
	if strings.HasPrefix(text, "/") {
		return m.command(text)
	}
	m.entries = append(m.entries, &entry{kind: "user", text: text})
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel, m.busy, m.thinking, m.busySince, m.thinkSince, m.reasoning = cancel, true, true, m.now, m.now, ""
	m.turnTools, m.turnTests, m.turnFailed, m.answering = 0, false, false, false
	m.speak("refracting…")
	events, agent := m.events, m.agent
	go agent.Run(ctx, text, func(e any) { events <- e })
	return nil
}

func (m *model) handle(event any) {
	switch e := event.(type) {
	case evRoute:
		m.route, m.waited, m.answering = e.route, e.wait, true
	case evReasoning:
		m.reasoning += e.text
		if len(m.reasoning) > 4000 {
			m.reasoning = m.reasoning[len(m.reasoning)-3000:]
		}
	case evThinking:
		m.thinking = true
	case evApprove:
		m.thinking = false
		if m.pending == nil {
			m.pending = &e
		} else {
			m.queued = append(m.queued, &e)
		}
		m.speak("may I?")
	case evCrewStart:
		m.thinking, m.reasoning, m.draft = false, "", nil
		block := newCrewBlock(e)
		placed := false
		for i := len(m.entries) - 1; i >= 0 && !placed; i-- {
			if en := m.entries[i]; en.kind == "tool" && en.tool == "crew" && !en.done {
				en.kind, en.crew, placed = "crew", block, true
			}
		}
		if !placed {
			m.entries = append(m.entries, &entry{kind: "crew", crew: block})
		}
		m.crewReact(e)
	case evCrewWorker:
		if c := m.activeCrew(); c != nil {
			c.worker(e)
		}
	case evCrewDone:
		if c := m.activeCrew(); c != nil {
			c.finish(e.summary)
		}
		m.crewReact(e)
	case evDraft:
		m.thinking, m.reasoning, m.draft = false, "", &e
	case evText:
		m.thinking, m.reasoning, m.lastTextAt, m.draft = false, "", m.now, nil
		if n := len(m.entries); n > 0 && m.entries[n-1].kind == "text" {
			m.entries[n-1].text += e.text
		} else {
			m.entries = append(m.entries, &entry{kind: "text", text: e.text})
		}
	case evToolStart:
		m.thinking, m.reasoning, m.draft = false, "", nil
		m.entries = append(m.entries, &entry{kind: "tool", id: e.id, tool: e.name, arg: e.arg})
		m.turnTools++
	case evToolDone:
		for i := len(m.entries) - 1; i >= 0; i-- {
			if en := m.entries[i]; (en.kind == "tool" || en.kind == "crew") && en.id == e.id && !en.done {
				en.done, en.res = true, e.result
				if en.tool == "bash" && e.result.ok && strings.Contains(en.arg, "test") {
					m.turnTests = true
				}
				if !e.result.ok {
					m.surprisedUntil = m.now + 1500*time.Millisecond
					if !m.turnFailed {
						m.speak("hm. a crack in the lens.")
					}
					m.turnFailed = true
				}
				break
			}
		}
		m.thinking, m.thinkSince, m.answering = true, m.now, false
	case evUsage:
		m.tokens, m.window = e.tokens, e.window
	case evNote:
		m.entries = append(m.entries, &entry{kind: "note", text: e.text})
		if strings.HasPrefix(e.text, "compacting") {
			m.speak("folding the light.")
		}
	case evRemembered:
		if e.changed {
			m.speak("noted.")
		}
	case evDone:
		m.busy, m.thinking, m.cancel, m.reasoning, m.draft, m.pending, m.queued = false, false, nil, "", nil, nil, nil
		m.settleCrews()
		switch {
		case e.err == nil:
			m.entries = append(m.entries, &entry{kind: "meta", text: fmt.Sprintf("%.1fs", (m.now - m.busySince).Seconds()), arg: m.route})
			m.finish(true, false)
			if job := m.agent.memoryJob(); job != nil {
				events := m.events
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					defer cancel()
					changed, _ := job.run(ctx)
					events <- evRemembered{changed}
				}()
			}
		case errors.Is(e.err, context.Canceled):
			m.entries = append(m.entries, &entry{kind: "note", text: "stopped"})
		default:
			headline, detail := friendly(e.err)
			m.entries = append(m.entries, &entry{kind: "error", text: headline, arg: detail})
			m.finish(false, strings.HasPrefix(headline, "Every free AI model is busy"))
		}
	}
}

// activeCrew is the crew block still receiving worker updates.
func (m *model) activeCrew() *crewBlock {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if c := m.entries[i].crew; c != nil && !c.done {
			return c
		}
	}
	return nil
}

// settleCrews ends any crew block the turn left unfinished (esc, an error) so
// its rows read cancelled instead of spinning.
func (m *model) settleCrews() {
	for _, en := range m.entries {
		if en.crew != nil && !en.crew.done {
			en.crew.settle()
		}
	}
}

// answer replies to the approval on screen and brings up the next one.
func (m *model) answer(ok bool) {
	m.pending.reply <- ok
	m.pending = nil
	if len(m.queued) > 0 {
		m.pending, m.queued = m.queued[0], m.queued[1:]
	}
}

func (m model) page() int { return max(1, (m.h-12)/2) }

func (m *model) scrollBy(n int) {
	if !m.chat {
		return
	}
	limit := len(m.transcript(themes[m.theme], m.column())) - m.viewHeight()
	m.scroll = clamp(m.scroll+n, 0, max(0, limit))
}

func (m model) View() tea.View {
	if m.w == 0 {
		return tea.NewView("")
	}
	t := themes[m.theme]
	cv := newCanvas(m.w, m.h, t.bg, t.fg)
	switch {
	case m.web != nil:
		drawHandoff(cv, t, m)
	case strings.HasPrefix(m.gallery, "beam:"):
		freeze, _ := strconv.Atoi(strings.TrimPrefix(m.gallery, "beam:"))
		m.chat, m.beamOn, m.chatAt = true, true, -time.Hour
		m.beamAt = m.now - time.Duration(freeze)*time.Millisecond
		if m.beamAt <= 0 {
			m.beamAt = 1
			m.now = m.beamAt + time.Duration(freeze)*time.Millisecond
		}
		m.entries = []*entry{{kind: "user", text: "fix the bug in shop.py"}, {kind: "text", text: "Fixed. total() now multiplies price by quantity."}, {kind: "meta", text: "4.2s", arg: "nvidia/nemotron-3-ultra"}}
		drawChat(cv, t, m)
	case m.gallery != "":
		drawShardGallery(cv, t, ms(m.now), m.gallery == "beam")
	case m.chat:
		drawChat(cv, t, m)
	default:
		drawSplash(cv, t, m)
	}
	v := tea.NewView(cv.String())
	v.AltScreen = true
	v.WindowTitle = "prism"
	if m.chat {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

func fetchStatus(api string) tea.Cmd {
	return func() tea.Msg {
		rows, err := fetchRoutes(api)
		ready := 0
		for _, row := range rows {
			if row.Ready {
				ready++
			}
		}
		return statusMsg{ready: ready, err: err}
	}
}

func clamp(v, lo, hi int) int { return max(lo, min(hi, v)) }

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func hash(a, b, c int) float64 {
	x := uint32(a*374761393 + b*668265263 + c*1442695041)
	x = (x ^ (x >> 13)) * 1274126177
	x ^= x >> 16
	return float64(x%10000) / 10000
}

func ease(v float64) float64 {
	v = math.Max(0, math.Min(1, v))
	return 1 - math.Pow(1-v, 3)
}

func settingPath(name string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "prism", name)
}

func loadSetting(name string) string {
	data, _ := os.ReadFile(settingPath(name))
	return strings.TrimSpace(string(data))
}

func saveSetting(name, value string) {
	if path := settingPath(name); path != "" {
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(value+"\n"), 0o644)
	}
}

func loadTheme() string { return loadSetting("theme") }

func saveTheme(name string) { saveSetting("theme", name) }

func themeIndex(name string) int {
	for i, t := range themes {
		if t.name == name {
			return i
		}
	}
	return 0
}

func projectName(dir string) string {
	if name := os.Getenv("PRISM_PROJECT"); name != "" {
		return name
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(dir, home) {
		return "~" + strings.TrimPrefix(dir, home)
	}
	return dir
}

// plainMemoryWait caps how long `prism run` waits for the memory update after
// the answer is printed.
const plainMemoryWait = 15 * time.Second

func runPlain(agent *Agent, prompt string) int {
	if strings.TrimSpace(prompt) == "" {
		fmt.Fprintln(os.Stderr, "usage: prism run <message>")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	code, route := 0, ""
	start := time.Now()
	var first time.Duration
	seen := func() {
		if first == 0 {
			first = time.Since(start)
		}
	}
	agent.Run(ctx, prompt, func(event any) {
		switch event.(type) {
		case evText, evToolStart:
			seen()
		}
		switch e := event.(type) {
		case evRoute:
			if e.route != route {
				route = e.route
				fmt.Printf("\n[%s]\n", route)
			}
		case evText:
			fmt.Print(e.text)
		case evToolStart:
			fmt.Printf("\n◆ %s %s\n", e.name, e.arg)
		case evToolDone:
			mark := "✓"
			if !e.result.ok {
				mark = "✕"
			}
			fmt.Printf("  %s %s\n", mark, e.result.summary)
		case evNote:
			fmt.Printf("\n· %s\n", e.text)
		case evCrewStart:
			fmt.Printf("  crew · %d workers\n", len(e.tasks))
		case evCrewWorker:
			if mark := map[string]string{"done": "✓", "failed": "✕", "cancelled": "–"}[e.status]; mark != "" {
				fmt.Printf("  %s %s\n", mark, e.id)
			}
		case evCrewDone:
			fmt.Printf("  %s\n", e.summary)
		case evDone:
			fmt.Println()
			if e.err != nil {
				fmt.Fprintln(os.Stderr, "error:", e.err)
				code = 1
			}
		}
	})
	// The answer is done: report the run first, then (only if the memory can be
	// saved) update it, waiting at most plainMemoryWait.
	cached := ""
	if agent.cached > 0 {
		cached = fmt.Sprintf(" (%d cached)", agent.cached)
	}
	fmt.Printf("· %d requests · %d tokens sent%s · first word %.1fs · total %.1fs\n", agent.requests, agent.sent, cached, first.Seconds(), time.Since(start).Seconds())
	if job := agent.memoryJob(); job != nil {
		mctx, cancel := context.WithTimeout(context.Background(), plainMemoryWait)
		changed, err := job.run(mctx)
		cancel()
		switch {
		case err != nil:
			fmt.Fprintln(os.Stderr, "· memory not saved:", err)
		case changed:
			fmt.Println("· memory updated")
		}
	}
	return code
}

func main() {
	names := make([]string, len(themes))
	for i, t := range themes {
		names[i] = t.name
	}
	system := flag.Bool("system", false, "computer help mode: runs on this computer and asks before every change")
	resume := flag.Bool("continue", false, "continue the last chat in this folder")
	themeName := flag.String("theme", "", "color theme: "+strings.Join(names, ", "))
	gallery := flag.String("gallery", "", "")
	api := flag.String("api", "http://127.0.0.1:4848/v1", "free AI router address")
	flag.Parse()
	if env := os.Getenv("PRISM_API"); env != "" && !isFlagSet("api") {
		*api = env
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	project := os.Getenv("PRISM_PROJECT")
	if project == "" {
		project = dir
	}
	agent := newAgent(*api, dir, project)
	agent.skills = loadSkills(dir)
	agent.mcp = startMCP(dir)
	defer agent.mcp.close()
	agent.system = *system
	anyPath = *system
	agent.messages[0].Content = agent.systemPrompt()
	if *resume {
		if list := agent.store.sessions(); len(list) > 0 {
			agent.resume(list[0])
		}
	}
	if args := flag.Args(); len(args) > 0 && args[0] == "tree" {
		os.Exit(treeMain(agent, args[1:]))
	}
	if args := flag.Args(); len(args) > 0 && args[0] == "serve" {
		dir := webDir()
		if dir == "" {
			fmt.Fprintln(os.Stderr, "the web page files were not found")
			os.Exit(1)
		}
		addr := os.Getenv("PRISM_LISTEN")
		if addr == "" {
			addr = "127.0.0.1:4849"
		}
		if err := serve(agent, addr, dir, loadTheme()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if args := flag.Args(); len(args) > 0 && args[0] == "run" {
		agent.mcp.wait(90 * time.Second)
		if agent.system {
			agent.approve = askOnTerminal
		}
		os.Exit(runPlain(agent, strings.Join(args[1:], " ")))
	}
	if *themeName == "" {
		*themeName = loadTheme()
	}
	if agent.system {
		events := make(chan any, 256)
		agent.approve = func(ctx context.Context, tool, arg string) bool {
			reply := make(chan bool, 1)
			select {
			case events <- evApprove{tool, arg, reply}:
			case <-ctx.Done():
				return false
			}
			select {
			case ok := <-reply:
				return ok
			case <-ctx.Done():
				return false
			}
		}
		systemEvents = events
	}
	m := model{ticking: true, start: time.Now(), events: eventsChannel(), agent: agent, ready: -1, window: agent.window, project: projectName(dir), lastTextAt: -time.Hour, entries: entriesFrom(agent.messages), theme: themeIndex(*themeName), gallery: *gallery,
		beamOn: loadSetting("beam") != "off", quiet: loadSetting("quiet") == "on", sayAt: -time.Hour}
	var options []tea.ProgramOption
	if os.Getenv("COLORTERM") == "" && os.Getenv("TERM") != "linux" {
		options = append(options, tea.WithColorProfile(colorprofile.TrueColor))
	}
	if _, err := tea.NewProgram(m, options...).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func isFlagSet(name string) bool {
	set := false
	flag.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

type evRemembered struct{ changed bool }

func entriesFrom(messages []Message) []*entry {
	outputs := map[string]string{}
	for _, msg := range messages {
		if msg.Role == "tool" {
			outputs[msg.ToolCallID] = msg.Content
		}
	}
	var out []*entry
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			if strings.HasPrefix(msg.Content, summaryPrefix) {
				out = append(out, &entry{kind: "note", text: "earlier messages were compacted"})
				continue
			}
			out = append(out, &entry{kind: "user", text: msg.Content})
		case "assistant":
			if strings.TrimSpace(msg.Content) != "" {
				out = append(out, &entry{kind: "text", text: msg.Content})
			}
			for _, c := range msg.ToolCalls {
				output := outputs[c.ID]
				ok := !strings.HasPrefix(output, "Error:")
				if c.Function.Name == "browse" && strings.Contains(output, "\nRESULT: ") {
					ok = !strings.Contains(output, "steps failed")
				}
				if c.Function.Name == "crew" && ok {
					if block := crewFromHistory(c.Function.Arguments, output); block != nil {
						out = append(out, &entry{kind: "crew", id: c.ID, tool: "crew", done: true, crew: block, res: toolResult{ok: true}})
						continue
					}
				}
				out = append(out, &entry{kind: "tool", id: c.ID, tool: c.Function.Name, arg: describe(c.Function.Arguments), done: true,
					res: toolResult{ok: ok, summary: pastSummary(c.Function.Name, output, ok)}})
			}
		}
	}
	return out
}

var systemEvents chan any

func eventsChannel() chan any {
	if systemEvents != nil {
		return systemEvents
	}
	return make(chan any, 256)
}

func askOnTerminal(ctx context.Context, tool, arg string) bool {
	verb := map[string]string{"bash": "run", "write": "write", "edit": "edit"}[tool]
	if verb == "" {
		verb = "use " + tool
	}
	fmt.Printf("\n? prism wants to %s:\n    %s\n  allow? [y/N] ", verb, arg)
	var answer string
	fmt.Scanln(&answer)
	return strings.EqualFold(strings.TrimSpace(answer), "y")
}

func pastSummary(tool, output string, ok bool) string {
	lines := splitLines(strings.TrimSpace(output))
	switch {
	case len(lines) == 0:
		return ""
	case tool == "browse":
		if result := lines[len(lines)-1]; strings.HasPrefix(result, "RESULT: ") {
			return strings.TrimSuffix(strings.SplitN(strings.TrimPrefix(result, "RESULT: "), ". ", 2)[0], ".")
		}
		if !ok {
			return strings.TrimPrefix(lines[0], "Error: ")
		}
		return "page outline"
	case !ok:
		return strings.TrimPrefix(lines[0], "Error: ")
	case tool == "read":
		return fmt.Sprintf("%d lines", len(lines))
	case tool == "edit", tool == "write":
		return "done"
	case tool == "bash":
		for i := len(lines) - 1; i >= 0; i-- {
			if l := strings.TrimSpace(lines[i]); l != "" && !strings.HasPrefix(l, "[") {
				return "→ " + l
			}
		}
		return lines[len(lines)-1]
	}
	return lines[0]
}
