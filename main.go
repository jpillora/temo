// Command temo is a terminal demoscene: seven animated effects rendered as
// half-block "pixels" at double vertical resolution, all themed from a single
// configurable primary color. Bubble Tea runs the loop, Harmonica springs
// drive the beat pulse and scene wipes, Lip Gloss dresses the chrome.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/lipgloss"
)

type tickMsg time.Time

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

	sceneClock float64

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

func (m *model) Init() tea.Cmd { return tick(m.fpsCap) }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)

	case tea.KeyMsg:
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

	case tickMsg:
		now := time.Time(msg)
		dt := 1.0 / float64(m.fpsCap)
		if m.haveLast {
			if d := now.Sub(m.last).Seconds(); d > 0 && d < 0.1 {
				dt = d
			}
		}
		m.last, m.haveLast = now, true
		m.fpsEMA = m.fpsEMA*0.92 + (1/dt)*0.08

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
	m.drawScroller()
	m.drawHelp()
	m.composeFrame()
	return m.renderCanvas() + m.statusBar()
}

func (m *model) statusBar() string {
	badge := m.styBadge.Render(" ◆ TEMO ")
	state := fmt.Sprintf("%d/%d %s", m.cur+1, len(m.scenes), m.scenes[m.cur].Name())
	if m.paused {
		state += "  ·  PAUSED"
	} else {
		state += fmt.Sprintf("  ·  %3.0f FPS", m.fpsEMA)
	}
	info := m.styBar.Render("  " + state + "  ·  " + m.pal.Primary.Hex() + "  ")
	keys := m.styDim.Render("SPACE next · H help · Q quit ")
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

	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "temo:", err)
		os.Exit(1)
	}
	bye := m.styBadge.Render(" TEMO ") + lipgloss.NewStyle().
		Foreground(lipgloss.Color(prim.Hex())).Render(" over and out ♥ "+prim.Hex())
	fmt.Println(bye)
}
