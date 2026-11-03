// Command temo is a terminal demoscene: seven animated effects rendered as
// half-block "pixels" at double vertical resolution, all themed from a single
// configurable primary color. Bubble Tea runs the loop, Harmonica springs
// drive the beat pulse and scene wipes, Lip Gloss dresses the chrome.
package main

import (
	"flag"
	"fmt"
	"image"
	"math"
	"os"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/lipgloss"
)

type tickMsg time.Time

const (
	impactDuration     = 1.35
	dragImpactSpacing  = 6.0
	dragImpactReach    = 0.32
	dragImpactStrength = 0.32
	maxImpacts         = 12
	laserDuration      = 0.34
	maxLasers          = 16
	modeToastDuration  = 1.1
	keyPressDuration   = 3.0
)

type effectMode uint8

const (
	effectRipple effectMode = iota
	effectLazers
)

func (e effectMode) String() string {
	if e == effectLazers {
		return "LAZERS"
	}
	return "RIPPLE"
}

func (e effectMode) Number() int { return int(e) + 1 }

// impact is a short-lived, palette-aware shockwave centered on a pointer gesture.
// Coordinates are in the double-height pixel grid, not terminal cells.
type impact struct {
	x, y     float64
	age      float64
	reach    float64
	strength float64
}

// laser is a beam from a random canvas edge to the pointer, in pixel-grid
// coordinates. It fades quickly so a drag reads as a volley rather than a web.
type laser struct {
	x0, y0 float64
	x1, y1 float64
	age    float64
}

// keyPress is one entry in the transient key history drawn across the top.
// Its lifetime advances even while the demo itself is paused.
type keyPress struct {
	label string
	age   float64
}

// version is replaced with the release tag by GoReleaser and Docker builds.
var version = "dev"

func tick(fps int) tea.Cmd {
	return tea.Tick(time.Second/time.Duration(fps), func(t time.Time) tea.Msg { return tickMsg(t) })
}

type model struct {
	w, h   int // terminal cells; the bottom row is the status bar
	pw, ph int // pixel grid: pw = w, ph = (h-1)*2

	t          float64
	last       time.Time
	haveLast   bool
	fpsEMA     float64
	paused     bool
	showHelp   bool
	crt        bool
	scrollerOn bool

	scenes    []Scene
	cur, prev int

	transitioning      bool
	transPos, transVel float64
	trSpring           harmonica.Spring
	wipeFlip           bool

	pulsePos, pulseVel float64
	plSpring           harmonica.Spring
	lastBeat           int
	impacts            []impact
	lasers             []laser
	effectMode         effectMode
	effectRNG          rng32
	modeToast          float64
	keyPresses         []keyPress
	dragging           bool
	dragX, dragY       float64

	sceneClock  float64
	pastedImage image.Image
	pasteFrame  []RGB8
	pasteUntil  time.Time

	pal *Palette

	bufCur, bufPrev []uint8
	frame           []RGB8
	vig             []float64
	rowF            []float64
	ovRune          []rune
	ovCol           []uint8
	ovDim           []bool

	sb strings.Builder

	fpsCap     int
	bpm, dwell float64

	styBadge, styBar, styDim lipgloss.Style
}

func newModel(pal *Palette, fpsCap int, bpm, dwell float64, start int, scroller bool) *model {
	m := &model{
		pal:        pal,
		fpsCap:     fpsCap,
		bpm:        bpm,
		dwell:      dwell,
		crt:        true,
		scrollerOn: scroller,
		fpsEMA:     float64(fpsCap),
		effectRNG:  0x1A2B3C4D,
	}
	m.scenes = []Scene{&plasma{}, &tunnel{}, &metaballs{}, &knot{}, &rotozoom{}, &starfield{}, &fire{}}
	if start >= 1 && start <= len(m.scenes) {
		m.cur = start - 1
	}
	m.trSpring = harmonica.NewSpring(harmonica.FPS(fpsCap), 4.2, 1.0)
	m.plSpring = harmonica.NewSpring(harmonica.FPS(fpsCap), 9.0, 0.55)
	m.applyStyles()
	return m
}

func (m *model) applyStyles() {
	dark := m.pal.lut8[26].Hex()
	badgeFg := "#F8F8F8"
	if m.pal.Primary.luma() > 0.6 {
		badgeFg = "#101010"
	}
	m.styBadge = lipgloss.NewStyle().Bold(true).
		Background(lipgloss.Color(m.pal.Primary.Hex())).
		Foreground(lipgloss.Color(badgeFg))
	m.styBar = lipgloss.NewStyle().
		Background(lipgloss.Color(dark)).
		Foreground(lipgloss.Color(m.pal.lut8[224].Hex()))
	m.styDim = lipgloss.NewStyle().
		Background(lipgloss.Color(dark)).
		Foreground(lipgloss.Color(m.pal.lut8[130].Hex()))
}

func (m *model) resize(w, h int) {
	if w < 1 || h < 2 {
		return
	}
	m.w, m.h = w, h
	m.pasteFrame = nil
	rows := h - 1
	m.pw, m.ph = w, rows*2
	n := m.pw * m.ph
	m.bufCur = make([]uint8, n)
	m.bufPrev = make([]uint8, n)
	m.frame = make([]RGB8, n)
	m.vig = make([]float64, n)
	m.ovRune = make([]rune, w*rows)
	m.ovCol = make([]uint8, w*rows)
	m.ovDim = make([]bool, w*rows)
	m.impacts = nil
	m.lasers = nil
	m.dragging = false
	cx, cy := float64(m.pw)/2, float64(m.ph)/2
	i := 0
	for y := 0; y < m.ph; y++ {
		ny := (float64(y) - cy) / cy
		for x := 0; x < m.pw; x++ {
			nx := (float64(x) - cx) / cx
			d := (nx*nx + ny*ny) / 2
			v := 1 - 0.45*math.Pow(d, 1.3)
			if v < 0.5 {
				v = 0.5
			}
			m.vig[i] = v
			i++
		}
	}
	m.updateRowF()
	for _, s := range m.scenes {
		s.Resize(m.pw, m.ph)
	}
}

func (m *model) updateRowF() {
	m.rowF = make([]float64, m.ph)
	for y := range m.rowF {
		m.rowF[y] = 1
		if m.crt && y&1 == 1 {
			m.rowF[y] = 0.90
		}
	}
}

func (m *model) switchScene(to int) {
	n := len(m.scenes)
	to = ((to % n) + n) % n
	if to == m.cur {
		return
	}
	m.prev = m.cur
	m.cur = to
	m.transitioning = true
	m.transPos, m.transVel = 0, 0
	m.wipeFlip = !m.wipeFlip
	m.sceneClock = 0
}

func (m *model) pointerPixel(x, cellY int) (px, py float64, ok bool) {
	px, py = float64(x), float64(cellY*2)+0.5
	ok = x >= 0 && x < m.pw && cellY >= 0 && cellY < m.h-1
	return
}

func (m *model) emitImpact(x, y float64, kick bool) {
	if x < 0 || x >= float64(m.pw) || y < 0 || y >= float64(m.ph) {
		return
	}
	if len(m.impacts) >= maxImpacts {
		copy(m.impacts, m.impacts[1:])
		m.impacts = m.impacts[:maxImpacts-1]
	}
	reach, strength := 1.0, 1.0
	if !kick {
		reach, strength = dragImpactReach, dragImpactStrength
	}
	m.impacts = append(m.impacts, impact{x: x, y: y, reach: reach, strength: strength})
	if kick {
		// Give the initial drop a whole-screen kick as the local ring launches.
		m.pulsePos, m.pulseVel = 1, 0
	}
}

func (m *model) emitLaser(x, y float64, burst bool) {
	if x < 0 || x >= float64(m.pw) || y < 0 || y >= float64(m.ph) {
		return
	}
	count := 1
	if burst {
		count = 3
		m.pulsePos, m.pulseVel = 1, 0
	}
	for i := 0; i < count; i++ {
		var x0, y0 float64
		switch m.effectRNG.next() % 4 {
		case 0:
			x0, y0 = m.effectRNG.f()*float64(m.pw-1), 0
		case 1:
			x0, y0 = float64(m.pw-1), m.effectRNG.f()*float64(m.ph-1)
		case 2:
			x0, y0 = m.effectRNG.f()*float64(m.pw-1), float64(m.ph-1)
		default:
			x0, y0 = 0, m.effectRNG.f()*float64(m.ph-1)
		}
		if len(m.lasers) >= maxLasers {
			copy(m.lasers, m.lasers[1:])
			m.lasers = m.lasers[:maxLasers-1]
		}
		m.lasers = append(m.lasers, laser{x0: x0, y0: y0, x1: x, y1: y})
	}
}

func (m *model) emitPointerEffect(x, y float64, initial bool) {
	if m.effectMode == effectLazers {
		m.emitLaser(x, y, initial)
		return
	}
	m.emitImpact(x, y, initial)
}

func (m *model) addImpact(x, cellY int) {
	px, py, ok := m.pointerPixel(x, cellY)
	if ok {
		m.emitImpact(px, py, true)
	}
}

func (m *model) beginDrag(x, cellY int) {
	px, py, ok := m.pointerPixel(x, cellY)
	m.dragging = ok
	if !ok {
		return
	}
	m.dragX, m.dragY = px, py
	m.emitPointerEffect(px, py, true)
}

func (m *model) dragImpact(x, cellY int) {
	if !m.dragging {
		return
	}
	px, py, ok := m.pointerPixel(x, cellY)
	if !ok {
		// Track the pointer outside the canvas without painting the status bar.
		m.dragX, m.dragY = px, py
		return
	}
	dx, dy := px-m.dragX, py-m.dragY
	distance := math.Hypot(dx, dy)
	steps := int(distance / dragImpactSpacing)
	if steps == 0 {
		return
	}
	for i := 1; i <= steps; i++ {
		f := dragImpactSpacing * float64(i) / distance
		m.emitPointerEffect(m.dragX+dx*f, m.dragY+dy*f, false)
	}
	// Retain any sub-spacing remainder for the next motion event. This keeps
	// ripple density even when terminals report movement at different rates.
	f := dragImpactSpacing * float64(steps) / distance
	m.dragX += dx * f
	m.dragY += dy * f
}

func (m *model) stepImpacts(dt float64) {
	active := m.impacts[:0]
	for _, in := range m.impacts {
		in.age += dt
		if in.age < impactDuration {
			active = append(active, in)
		}
	}
	m.impacts = active
}

func (m *model) stepLasers(dt float64) {
	active := m.lasers[:0]
	for _, beam := range m.lasers {
		beam.age += dt
		if beam.age < laserDuration {
			active = append(active, beam)
		}
	}
	m.lasers = active
}

func (m *model) addKeyPress(msg tea.KeyMsg) {
	label := msg.String()
	if msg.Paste {
		label = "paste"
	} else if msg.Type == tea.KeySpace || label == " " {
		label = "space"
	} else if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && unicode.IsUpper(msg.Runes[0]) {
		label = "shift+" + string(unicode.ToLower(msg.Runes[0]))
		if msg.Alt {
			label = "alt+" + label
		}
	}
	if label == "" {
		return
	}
	m.keyPresses = append(m.keyPresses, keyPress{})
	copy(m.keyPresses[1:], m.keyPresses)
	m.keyPresses[0] = keyPress{label: label}
}

func (m *model) stepKeyPresses(dt float64) {
	active := m.keyPresses[:0]
	for _, key := range m.keyPresses {
		key.age += dt
		if key.age < keyPressDuration {
			active = append(active, key)
		}
	}
	m.keyPresses = active
}

func (m *model) toggleEffectMode() {
	if m.effectMode == effectRipple {
		m.effectMode = effectLazers
	} else {
		m.effectMode = effectRipple
	}
	m.dragging = false
	m.modeToast = modeToastDuration
}

func (m *model) Init() tea.Cmd { return tick(m.fpsCap) }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case pastedImageMsg:
		m.pastedImage = msg.image
		m.pasteFrame = nil
		m.pasteUntil = time.Now().Add(3 * time.Second)
		m.dragging = false
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)

	case tea.KeyMsg:
		m.addKeyPress(msg)
		if msg.Paste {
			return m, nil
		}
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case " ", "space", "right", "n":
			m.switchScene(m.cur + 1)
		case "left":
			m.switchScene(m.cur - 1)
		case "p":
			m.paused = !m.paused
		case "c":
			m.crt = !m.crt
			m.updateRowF()
		case "s":
			m.scrollerOn = !m.scrollerOn
		case "h", "?":
			m.showHelp = !m.showHelp
		default:
			if s := msg.String(); len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
				if i := int(s[0] - '1'); i < len(m.scenes) {
					m.switchScene(i)
				}
			}
		}

	case tea.MouseMsg:
		switch msg.Action {
		case tea.MouseActionPress:
			m.dragging = false
			switch msg.Button {
			case tea.MouseButtonLeft:
				m.beginDrag(msg.X, msg.Y)
			case tea.MouseButtonRight:
				m.toggleEffectMode()
			}
		case tea.MouseActionMotion:
			if msg.Button == tea.MouseButtonLeft {
				m.dragImpact(msg.X, msg.Y)
			}
		case tea.MouseActionRelease:
			m.dragging = false
		}

	case tickMsg:
		now := time.Time(msg)
		dt := 1.0 / float64(m.fpsCap)
		if m.haveLast {
			if d := now.Sub(m.last).Seconds(); d > 0 && d < 0.1 {
				dt = d
			}
		}
		m.last, m.haveLast = now, true
		if m.pastedImage != nil {
			if now.Before(m.pasteUntil) {
				return m, tick(m.fpsCap)
			}
			m.pastedImage = nil
			m.pasteFrame = nil
		}
		m.fpsEMA = m.fpsEMA*0.92 + (1/dt)*0.08
		m.stepImpacts(dt)
		m.stepLasers(dt)
		m.stepKeyPresses(dt)
		if m.modeToast > 0 {
			m.modeToast = math.Max(0, m.modeToast-dt)
		}

		if !m.paused {
			m.t += dt
			m.sceneClock += dt
			m.scenes[m.cur].Step(dt, m.t)
			if m.transitioning {
				m.scenes[m.prev].Step(dt, m.t)
			}
			if beat := int(m.t * m.bpm / 60); beat != m.lastBeat {
				m.lastBeat = beat
				m.pulsePos = 1
			}
			if m.dwell > 0 && m.sceneClock > m.dwell {
				m.switchScene(m.cur + 1)
			}
		}
		m.pulsePos, m.pulseVel = m.plSpring.Update(m.pulsePos, m.pulseVel, 0)
		if m.transitioning {
			m.transPos, m.transVel = m.trSpring.Update(m.transPos, m.transVel, 1)
			if m.transPos > 0.999 {
				m.transitioning = false
			}
		}
		return m, tick(m.fpsCap)
	}
	return m, nil
}

func (m *model) View() string {
	if m.pw == 0 {
		return ""
	}
	if m.pastedImage != nil {
		m.ovClear()
		m.renderPastedImage()
		return m.renderCanvas() + m.statusBar()
	}
	if m.w < 40 || m.h < 10 {
		return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center,
			m.styBadge.Render(" TEMO ")+" needs a bigger terminal")
	}
	m.scenes[m.cur].Render(m.t, m.bufCur)
	if m.transitioning {
		m.scenes[m.prev].Render(m.t, m.bufPrev)
	}
	m.ovClear()
	m.drawToast()
	m.drawModeToast()
	m.drawScroller()
	m.drawHelp()
	m.drawKeyRow()
	m.composeFrame()
	return m.renderCanvas() + m.statusBar()
}

func (m *model) statusBar() string {
	badge := m.styBadge.Render(" ◆ TEMO ")
	state := fmt.Sprintf("%d/%d %s · %s · %d×%d", m.cur+1, len(m.scenes), m.scenes[m.cur].Name(), m.effectMode, m.h, m.w)
	if m.pastedImage != nil {
		state = "IMAGE PASTE · preview 3s"
	}
	if m.paused {
		state += "  ·  PAUSED"
	} else {
		state += fmt.Sprintf("  ·  %3.0f FPS", m.fpsEMA)
	}
	info := m.styBar.Render("  " + state + "  ·  " + m.pal.Primary.Hex() + "  ")
	keys := m.styDim.Render("LMB paint · RMB mode · SPACE next · H help · Q quit ")
	gap := m.w - lipgloss.Width(badge) - lipgloss.Width(info) - lipgloss.Width(keys)
	if gap < 0 {
		keys = ""
		gap = m.w - lipgloss.Width(badge) - lipgloss.Width(info)
	}
	if gap < 0 {
		gap = 0
	}
	bar := badge + info + m.styBar.Render(strings.Repeat(" ", gap)) + keys
	return lipgloss.NewStyle().MaxWidth(m.w).Render(bar)
}

// ── selftest ─────────────────────────────────────────────────────────────
// Runs every scene headless for a few seconds and prints index statistics,
// then dumps one composed frame so truecolor output can be eyeballed.

func runSelftest(m *model) {
	m.resize(120, 36)
	for si, s := range m.scenes {
		for f := 0; f < 90; f++ {
			m.t += 1.0 / 30
			s.Step(1.0/30, m.t)
		}
		s.Render(m.t, m.bufCur)
		var mn, mx uint8 = 255, 0
		var sum int
		for _, v := range m.bufCur {
			if v < mn {
				mn = v
			}
			if v > mx {
				mx = v
			}
			sum += int(v)
		}
		fmt.Fprintf(os.Stderr, "scene %d %-11s idx min=%-3d max=%-3d mean=%.1f\n",
			si+1, s.Name(), mn, mx, float64(sum)/float64(len(m.bufCur)))
	}
	// exercise the transition blend path
	m.cur = 0
	m.switchScene(1)
	m.transPos = 0.5
	_ = m.View()
	// tiny-terminal bounds check
	m.resize(44, 12)
	_ = m.View()
	// dump one plasma frame for visual inspection
	m.resize(120, 36)
	m.transitioning = false
	m.cur = 0
	frame := m.View()
	colors := map[RGB8]bool{}
	for _, c := range m.frame {
		colors[c] = true
	}
	fmt.Fprintf(os.Stderr, "frame: %d lines, %d bytes, %d distinct colors\n",
		strings.Count(frame, "\n")+1, len(frame), len(colors))
	fmt.Println(frame)
}

// ── entry ────────────────────────────────────────────────────────────────

func main() {
	var (
		primary     string
		fpsCap      int
		bpm         float64
		dwell       float64
		start       int
		scroller    bool
		selftest    bool
		showVersion bool
	)
	flag.StringVar(&primary, "primary", "#FF5FD2", "primary color: hex like '#FF5FD2' or a name ("+colorNames()+")")
	flag.StringVar(&primary, "p", "#FF5FD2", "shorthand for -primary")
	flag.IntVar(&fpsCap, "fps", 30, "target frames per second (10-60)")
	flag.Float64Var(&bpm, "bpm", 128, "beat pulse tempo")
	flag.Float64Var(&dwell, "dwell", 12, "seconds per scene before auto-advance (0 = stay)")
	flag.IntVar(&start, "scene", 1, "scene to start on (1-7)")
	flag.BoolVar(&scroller, "scroller", true, "show the greetings scroller")
	flag.BoolVar(&selftest, "selftest", false, "render each scene headless, print stats and one frame")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.Parse()
	if showVersion {
		fmt.Println(version)
		return
	}

	if flag.NArg() > 0 {
		primary = flag.Arg(0)
	}
	if fpsCap < 10 {
		fpsCap = 10
	}
	if fpsCap > 60 {
		fpsCap = 60
	}

	prim, err := parseColor(primary)
	if err != nil {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F5F")).Bold(true)
		fmt.Fprintln(os.Stderr, errStyle.Render("temo: ")+err.Error())
		os.Exit(1)
	}

	m := newModel(BuildPalette(prim), fpsCap, bpm, dwell, start, scroller)

	if selftest {
		runSelftest(m)
		return
	}

	input := &imagePasteReader{File: os.Stdin}
	p := tea.NewProgram(m, tea.WithInput(input), tea.WithAltScreen(), tea.WithMouseCellMotion())
	input.send = p.Send
	defer input.stop()
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "temo:", err)
		os.Exit(1)
	}
	bye := m.styBadge.Render(" TEMO ") + lipgloss.NewStyle().
		Foreground(lipgloss.Color(prim.Hex())).Render(" over and out ♥ "+prim.Hex())
	fmt.Println(bye)
}
