package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// mediaView is a picture or a video in the /tree viewer. Pictures are drawn
// with half blocks (two pixels per cell), and as a real picture in terminals
// that speak the kitty graphics protocol. Videos play as half blocks.
type mediaView struct {
	kind string
	abs  string
	img  image.Image
	err  string

	cacheW, cacheH int
	cache          []cell

	video *videoPlayer
}

const maxPictureBytes = 64 << 20

func newMediaViewer(dir, p string) *fileViewer {
	abs, rel, err := projectPath(dir, p)
	v := &fileViewer{path: rel}
	if err != nil {
		v.note = "✕ " + err.Error()
		return v
	}
	mv := &mediaView{kind: mediaKind(rel), abs: abs}
	v.media = mv
	st, err := os.Stat(abs)
	switch {
	case err != nil:
		mv.err = pathErr(rel, err).Error()
	case mv.kind == "image" && st.Size() > maxPictureBytes:
		mv.err = fmt.Sprintf("too large to show · %s", sizeText(st.Size()))
	case mv.kind == "image":
		f, err := os.Open(abs)
		if err != nil {
			mv.err = pathErr(rel, err).Error()
			break
		}
		mv.img, _, err = image.Decode(bufio.NewReader(f))
		f.Close()
		if err != nil {
			mv.err = "couldn't read this picture"
		}
	}
	return v
}

// fit is the largest w×h cell box, inside cols×rows, that keeps the picture's
// shape. A cell is about twice as tall as wide, so one cell holds two pixels.
func fit(pw, ph, cols, rows int) (w, h int) {
	if pw <= 0 || ph <= 0 || cols <= 0 || rows <= 0 {
		return 0, 0
	}
	w, h = cols, cols*ph/pw/2
	if h > rows {
		w, h = rows*2*pw/ph, rows
	}
	return max(1, w), max(1, h)
}

// halfBlocks scales img to w×2h pixels and packs each pair of rows into ▀
// cells: the top pixel is the text colour, the bottom one the background.
func halfBlocks(img image.Image, w, h int, bg rgb) []cell {
	dst := image.NewRGBA(image.Rect(0, 0, w, h*2))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
	out := make([]cell, w*h)
	px := func(x, y int) rgb {
		i := dst.PixOffset(x, y)
		a := float64(dst.Pix[i+3]) / 255
		return mix(bg, rgb{dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2]}, a)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out[y*w+x] = cell{ch: '▀', fg: px(x, y*2), bg: px(x, y*2+1)}
		}
	}
	return out
}

func (mv *mediaView) cells(w, h int, bg rgb) []cell {
	if mv.cacheW != w || mv.cacheH != h || mv.cache == nil {
		mv.cacheW, mv.cacheH, mv.cache = w, h, halfBlocks(mv.img, w, h, bg)
	}
	return mv.cache
}

func blit(cv *canvas, x, y, w, h int, cells []cell) {
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			if c := cv.get(x+i, y+j); c != nil {
				src := cells[j*w+i]
				c.ch, c.fg, c.bg, c.bold, c.url = src.ch, src.fg, src.bg, false, ""
			}
		}
	}
}

// mediaBox is where the picture or video sits inside the viewer's area.
func (mv *mediaView) box(x, y, cols, rows int) (bx, by, w, h int) {
	pw, ph := 0, 0
	switch {
	case mv.img != nil:
		pw, ph = mv.img.Bounds().Dx(), mv.img.Bounds().Dy()
	case mv.video != nil:
		pw, ph = mv.video.width, mv.video.height
	}
	w, h = fit(pw, ph, cols, rows)
	return x + (cols-w)/2, y + (rows-h)/2, w, h
}

func drawMedia(cv *canvas, t theme, mv *mediaView, x, y, cols, rows int) {
	cv.fill(x, y, cols, rows, t.bg)
	if mv.err != "" {
		return
	}
	bx, by, w, h := mv.box(x, y, cols, rows)
	if w == 0 {
		return
	}
	if mv.img != nil {
		blit(cv, bx, by, w, h, mv.cells(w, h, t.bg))
		return
	}
	if p := mv.video; p != nil {
		if frame, fw, fh := p.frame(); frame != nil && fw == w && fh == h {
			blit(cv, bx, by, w, h, frame)
		}
	}
}

// --- real pictures through the kitty graphics protocol ---

const kittyImageID = 7391

func kittyGraphics() bool {
	term, prog := os.Getenv("TERM"), strings.ToLower(os.Getenv("TERM_PROGRAM"))
	return strings.Contains(term, "kitty") || strings.Contains(term, "ghostty") || prog == "ghostty" || prog == "wezterm" || os.Getenv("KITTY_WINDOW_ID") != ""
}

// kittyPlace sends img to the terminal and shows it over the w×h cell box at
// x, y. The cursor is saved and restored so the screen renderer isn't moved.
func kittyPlace(img image.Image, x, y, w, h int) string {
	long := max(img.Bounds().Dx(), img.Bounds().Dy())
	if long > 1600 {
		sw, sh := img.Bounds().Dx()*1600/long, img.Bounds().Dy()*1600/long
		small := image.NewRGBA(image.Rect(0, 0, max(1, sw), max(1, sh)))
		draw.ApproxBiLinear.Scale(small, small.Bounds(), img, img.Bounds(), draw.Src, nil)
		img = small
	}
	var buf bytes.Buffer
	(&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img)
	data := base64.StdEncoding.EncodeToString(buf.Bytes())
	var b strings.Builder
	fmt.Fprintf(&b, "\x1b7\x1b[%d;%dH", y+1, x+1)
	for i := 0; i < len(data); i += 4096 {
		chunk, more := data[i:min(len(data), i+4096)], 1
		if i+4096 >= len(data) {
			more = 0
		}
		if i == 0 {
			fmt.Fprintf(&b, "\x1b_Ga=T,f=100,q=2,C=1,i=%d,p=1,c=%d,r=%d,m=%d;%s\x1b\\", kittyImageID, w, h, more, chunk)
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}
	b.WriteString("\x1b8")
	return b.String()
}

func kittyClear() string { return fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", kittyImageID) }

type kittyMsg struct{ key string }

// --- video ---

const videoFPS = 15

// videoPlayer runs ffmpeg and keeps the newest frame, already packed into
// half-block cells. Pausing simply stops reading; ffmpeg then waits on the pipe.
type videoPlayer struct {
	abs           string
	width, height int // the video's own size in pixels
	duration      float64
	bg            rgb

	ticks chan struct{}
	done  chan struct{}

	mu      sync.Mutex
	cells   []cell
	fw, fh  int
	pos     float64
	paused  bool
	ended   bool
	err     string
	cancel  context.CancelFunc
	resume  chan struct{}
	stopped bool
}

type videoFrameMsg struct{ p *videoPlayer }

var (
	probeDuration = regexp.MustCompile(`Duration: (\d+):(\d+):(\d+(?:\.\d+)?)`)
	probeSize     = regexp.MustCompile(`Video: .*?, (\d{2,5})x(\d{2,5})`)
	probeRotation = regexp.MustCompile(`rotation of -?(\d+)`)
)

// probeVideo reads the size and length that ffmpeg prints about its input.
func probeVideo(abs string) (w, h int, dur float64, err error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		if inScreenshotContainer() {
			return 0, 0, 0, errors.New("videos need ffmpeg in Prism's box · run: prism update")
		}
		return 0, 0, 0, errors.New("videos need ffmpeg · install the ffmpeg package")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-i", abs).CombinedOutput()
	return parseProbe(string(out))
}

func parseProbe(out string) (w, h int, dur float64, err error) {
	m := probeSize.FindStringSubmatch(out)
	if m == nil {
		return 0, 0, 0, errors.New("couldn't read this video")
	}
	w, _ = strconv.Atoi(m[1])
	h, _ = strconv.Atoi(m[2])
	// Phone videos are often stored sideways with a rotation flag that ffmpeg applies.
	if r := probeRotation.FindStringSubmatch(out); r != nil && (r[1] == "90" || r[1] == "270") {
		w, h = h, w
	}
	if d := probeDuration.FindStringSubmatch(out); d != nil {
		hh, _ := strconv.ParseFloat(d[1], 64)
		mm, _ := strconv.ParseFloat(d[2], 64)
		ss, _ := strconv.ParseFloat(d[3], 64)
		dur = hh*3600 + mm*60 + ss
	}
	return w, h, dur, nil
}

func startVideo(abs string, bg rgb) (*videoPlayer, error) {
	w, h, dur, err := probeVideo(abs)
	if err != nil {
		return nil, err
	}
	return &videoPlayer{abs: abs, width: w, height: h, duration: dur, bg: bg, ticks: make(chan struct{}, 1), done: make(chan struct{}), resume: make(chan struct{})}, nil
}

func waitVideo(p *videoPlayer) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-p.ticks:
			return videoFrameMsg{p}
		case <-p.done:
			return nil
		}
	}
}

func (p *videoPlayer) frame() ([]cell, int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cells, p.fw, p.fh
}

func (p *videoPlayer) status() (pos float64, paused, ended bool, err string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pos, p.paused, p.ended, p.err
}

// play (re)starts ffmpeg at the current position for a w×h cell box.
func (p *videoPlayer) play(w, h int) {
	p.mu.Lock()
	if p.stopped || w <= 0 || h <= 0 {
		p.mu.Unlock()
		return
	}
	if p.cancel != nil {
		p.cancel()
	}
	if p.ended {
		p.pos, p.ended = 0, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel, p.fw, p.fh, p.err = cancel, w, h, ""
	start := p.pos
	p.mu.Unlock()
	go p.run(ctx, start, w, h)
}

func (p *videoPlayer) run(ctx context.Context, start float64, w, h int) {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-ss", strconv.FormatFloat(start, 'f', 2, 64), "-i", p.abs,
		"-an", "-vf", fmt.Sprintf("fps=%d,scale=%d:%d:flags=area", videoFPS, w, h*2), "-f", "rawvideo", "-pix_fmt", "rgb24", "-")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	if cmd.Start() != nil {
		p.fail("couldn't play this video")
		return
	}
	defer cmd.Wait()
	buf := make([]byte, w*h*2*3)
	next := time.Now()
	for n := 0; ; n++ {
		if _, err := io.ReadFull(out, buf); err != nil {
			if ctx.Err() == nil {
				p.mu.Lock()
				p.ended = true
				p.mu.Unlock()
				p.tick()
			}
			return
		}
		cells := make([]cell, w*h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				t, b := ((y*2)*w+x)*3, ((y*2+1)*w+x)*3
				cells[y*w+x] = cell{ch: '▀', fg: rgb{buf[t], buf[t+1], buf[t+2]}, bg: rgb{buf[b], buf[b+1], buf[b+2]}}
			}
		}
		time.Sleep(time.Until(next))
		if ctx.Err() != nil {
			return
		}
		p.mu.Lock()
		p.cells, p.pos = cells, start+float64(n)/videoFPS
		p.mu.Unlock()
		p.tick()
		for {
			p.mu.Lock()
			paused, resume := p.paused, p.resume
			p.mu.Unlock()
			if !paused {
				break
			}
			select {
			case <-resume:
				next = time.Now()
			case <-ctx.Done():
				return
			}
		}
		next = next.Add(time.Second / videoFPS)
	}
}

func (p *videoPlayer) fail(msg string) {
	p.mu.Lock()
	p.err = msg
	p.mu.Unlock()
	p.tick()
}

func (p *videoPlayer) tick() {
	select {
	case p.ticks <- struct{}{}:
	default:
	}
}

func (p *videoPlayer) togglePause(w, h int) {
	p.mu.Lock()
	if p.ended {
		p.mu.Unlock()
		p.play(w, h)
		return
	}
	p.paused = !p.paused
	if !p.paused {
		close(p.resume)
		p.resume = make(chan struct{})
	}
	p.mu.Unlock()
}

func (p *videoPlayer) seek(delta float64, w, h int) {
	p.mu.Lock()
	p.pos = max(0, p.pos+delta)
	if p.duration > 0 {
		p.pos = min(p.pos, max(0, p.duration-0.5))
	}
	p.ended = false
	p.mu.Unlock()
	p.play(w, h)
}

func (p *videoPlayer) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	p.stopped = true
	if p.cancel != nil {
		p.cancel()
	}
	close(p.done)
}

func clock(s float64) string {
	s = max(0, s)
	return fmt.Sprintf("%d:%02d", int(s)/60, int(s)%60)
}

// --- the viewer around it ---

// mediaArea is the cell box the picture or video may fill, below the
// viewer's header line.
func (m model) mediaArea() (x, y, cols, rows int) {
	ox, oy, w, _ := m.overlayArea()
	lines, _, _, _ := m.filesLayout()
	return ox + 3, oy - lines + 1, max(1, w-6), max(1, lines-1)
}

func mediaLines(t theme, v *fileViewer, lines int) []pickLine {
	accent := t.at(0.5)
	head := []seg{{text: v.path, fg: accent, bold: true}}
	mv := v.media
	switch {
	case mv.err != "":
		head = append(head, seg{text: "  ✕ " + mv.err, fg: t.red})
	case mv.img != nil:
		b := mv.img.Bounds()
		head = append(head, seg{text: fmt.Sprintf("  %d×%d", b.Dx(), b.Dy()), fg: t.muted})
	case mv.video != nil:
		pos, paused, ended, err := mv.video.status()
		state := clock(pos) + " / " + clock(mv.video.duration)
		switch {
		case err != "":
			state = "✕ " + err
		case ended:
			state += " · ended"
		case paused:
			state += " · paused"
		}
		head = append(head, seg{text: "  " + state, fg: t.muted}, seg{text: "  space pause · ←→ 5s", fg: t.muted})
	}
	head = append(head, seg{text: "  · o browser · esc back", fg: t.muted})
	all := []pickLine{{fill: t.panel, segs: head}}
	for i := 1; i < lines; i++ {
		all = append(all, pickLine{fill: t.bg})
	}
	return all
}

func (m model) mediaKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	v := m.fb.view
	mv := v.media
	_, _, cols, rows := m.mediaArea()
	_, _, w, h := mv.box(0, 0, cols, rows)
	switch msg.String() {
	case "esc", "q":
		m.fb.view = nil
	case "space", "k":
		if mv.video != nil {
			mv.video.togglePause(w, h)
		}
	case "left", "j":
		if mv.video != nil {
			mv.video.seek(-5, w, h)
		}
	case "right", "l":
		if mv.video != nil {
			mv.video.seek(5, w, h)
		}
	case "o":
		if mv.video != nil {
			if _, paused, _, _ := mv.video.status(); !paused {
				mv.video.togglePause(w, h)
			}
		}
		m.startWeb()
		if m.web != nil {
			m.web.server.mu.Lock()
			m.web.server.openPath = v.path
			m.web.server.mu.Unlock()
		}
	}
	return m, nil
}

// syncMedia starts and stops the video player and places or clears the real
// picture, so they follow whatever the viewer shows right now.
func (m *model) syncMedia() tea.Cmd {
	var mv *mediaView
	if v := m.fb.view; m.fb.open && v != nil && v.media != nil && m.chat {
		mv = v.media
	}
	var cmds []tea.Cmd
	var player *videoPlayer
	if mv != nil && mv.kind == "video" && mv.err == "" {
		if mv.video == nil {
			p, err := startVideo(mv.abs, themes[m.theme].bg)
			if err != nil {
				mv.err = err.Error()
			} else {
				mv.video = p
				cmds = append(cmds, waitVideo(p))
			}
		}
		if p := mv.video; p != nil {
			player = p
			_, _, cols, rows := m.mediaArea()
			_, _, w, h := mv.box(0, 0, cols, rows)
			if _, fw, fh := p.frame(); fw != w || fh != h {
				if _, _, ended, _ := p.status(); !ended {
					p.play(w, h)
				}
			}
		}
	}
	if m.playing != nil && m.playing != player {
		m.playing.stop()
	}
	m.playing = player

	want := ""
	if mv != nil && mv.img != nil && kittyGraphics() && !m.menu.open && m.web == nil {
		x, y, cols, rows := m.mediaArea()
		bx, by, w, h := mv.box(x, y, cols, rows)
		want = fmt.Sprintf("%s|%d,%d,%d,%d", mv.abs, bx, by, w, h)
	}
	switch {
	case want == m.kittyShown:
	case want == "":
		m.kittyShown = ""
		cmds = append(cmds, tea.Raw(kittyClear()))
	default:
		cmds = append(cmds, tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg { return kittyMsg{want} }))
	}
	return tea.Batch(cmds...)
}

// kittyShow places the picture once the screen has settled on want.
func (m *model) kittyShow(msg kittyMsg) tea.Cmd {
	v := m.fb.view
	if !m.fb.open || v == nil || v.media == nil || v.media.img == nil || msg.key == m.kittyShown {
		return nil
	}
	x, y, cols, rows := m.mediaArea()
	bx, by, w, h := v.media.box(x, y, cols, rows)
	if fmt.Sprintf("%s|%d,%d,%d,%d", v.media.abs, bx, by, w, h) != msg.key {
		return nil
	}
	m.kittyShown = msg.key
	return tea.Raw(kittyClear() + kittyPlace(v.media.img, bx, by, w, h))
}
