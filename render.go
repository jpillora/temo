package main

import (
	"math"
	"strconv"
	"strings"
)

// Each terminal cell is drawn as "▀" so it carries two pixels: the foreground
// colors the top half, the background the bottom. An overlay layer of runes
// (scroller, toasts, help) is stamped on top of the pixel field per cell.

var itoa [256]string

func init() {
	for i := range itoa {
		itoa[i] = strconv.Itoa(i)
	}
}

// composeFrame turns scene palette indices into final RGB pixels, applying
// the scene-wipe blend, beat-pulse brightness, vignette and CRT scanlines.
func (m *model) composeFrame() {
	lut := &m.pal.lutF
	bright := 1 + 0.12*math.Max(0, m.pulsePos)
	fadeIn := clamp01(m.t / 1.1)
	bright *= fadeIn * fadeIn

	var edge, soft, span float64
	if m.transitioning {
		soft = float64(m.pw) * 0.18
		span = float64(m.pw + m.ph)
		edge = clamp01(m.transPos)*(span+2*soft) - soft
	}

	i := 0
	for y := 0; y < m.ph; y++ {
		rf := m.rowF[y] * bright
		for x := 0; x < m.pw; x++ {
			var c RGBf
			f := m.vig[i] * rf
			if m.transitioning {
				d := float64(x + y)
				if m.wipeFlip {
					d = span - d
				}
				a := clamp01((edge-d)/soft + 0.5)
				co := lut[m.bufPrev[i]]
				cn := lut[m.bufCur[i]]
				c = RGBf{lerp(co.R, cn.R, a), lerp(co.G, cn.G, a), lerp(co.B, cn.B, a)}
				if g := math.Abs(edge - d); g < soft*0.22 {
					f *= 1 + 0.5*(1-g/(soft*0.22))
				}
			} else {
				c = lut[m.bufCur[i]]
			}
			m.frame[i] = RGB8{u8(c.R * f), u8(c.G * f), u8(c.B * f)}
			i++
		}
	}
}

func dim8(c RGB8) RGB8 {
	return RGB8{uint8(uint16(c.R) * 77 >> 8), uint8(uint16(c.G) * 77 >> 8), uint8(uint16(c.B) * 77 >> 8)}
}

// renderCanvas serializes the pixel field + overlay into ANSI truecolor,
// emitting escape codes only when the color actually changes.
func (m *model) renderCanvas() string {
	m.sb.Reset()
	m.sb.Grow(m.w * m.h * 24)
	rows := m.h - 1
	for cy := 0; cy < rows; cy++ {
		var lastFg, lastBg RGB8
		fgOK, bgOK, bold := false, false, false
		emitFg := func(c RGB8) {
			if fgOK && lastFg == c {
				return
			}
			m.sb.WriteString("\x1b[38;2;")
			m.sb.WriteString(itoa[c.R])
			m.sb.WriteByte(';')
			m.sb.WriteString(itoa[c.G])
			m.sb.WriteByte(';')
			m.sb.WriteString(itoa[c.B])
			m.sb.WriteByte('m')
			lastFg, fgOK = c, true
		}
		emitBg := func(c RGB8) {
			if bgOK && lastBg == c {
				return
			}
			m.sb.WriteString("\x1b[48;2;")
			m.sb.WriteString(itoa[c.R])
			m.sb.WriteByte(';')
			m.sb.WriteString(itoa[c.G])
			m.sb.WriteByte(';')
			m.sb.WriteString(itoa[c.B])
			m.sb.WriteByte('m')
			lastBg, bgOK = c, true
		}
		for x := 0; x < m.w; x++ {
			top := m.frame[(2*cy)*m.pw+x]
			bot := m.frame[(2*cy+1)*m.pw+x]
			oi := cy*m.w + x
			if m.ovDim[oi] {
				top, bot = dim8(top), dim8(bot)
			}
			if r := m.ovRune[oi]; r != 0 {
				if !bold {
					m.sb.WriteString("\x1b[1m")
					bold = true
				}
				emitFg(m.pal.lut8[m.ovCol[oi]])
				emitBg(bot)
				m.sb.WriteRune(r)
			} else {
				if bold {
					m.sb.WriteString("\x1b[22m")
					bold = false
				}
				emitFg(top)
				emitBg(bot)
				m.sb.WriteRune('▀')
			}
		}
		m.sb.WriteString("\x1b[0m\n")
	}
	return m.sb.String()
}

// ── overlay text layer ───────────────────────────────────────────────────

func (m *model) ovClear() {
	for i := range m.ovRune {
		m.ovRune[i] = 0
		m.ovCol[i] = 0
		m.ovDim[i] = false
	}
}

// ovText stamps s at cell (x,y); spaces are skipped so the art shows through.
func (m *model) ovText(x, y int, s string, col uint8) {
	rows := m.h - 1
	if y < 0 || y >= rows {
		return
	}
	for _, r := range s {
		if x >= m.w {
			return
		}
		if x >= 0 && r != ' ' {
			oi := y*m.w + x
			m.ovRune[oi] = r
			m.ovCol[oi] = col
		}
		x++
	}
}

func (m *model) ovDimRect(x0, y0, x1, y1 int) {
	rows := m.h - 1
	for y := max(0, y0); y <= min(rows-1, y1); y++ {
		for x := max(0, x0); x <= min(m.w-1, x1); x++ {
			m.ovDim[y*m.w+x] = true
		}
	}
}

// ── scroller ─────────────────────────────────────────────────────────────

var scrollText = []rune("✦ T E M O ✦ A TERMINAL DEMOSCENE IN PURE GO ✦ BUBBLE TEA × LIP GLOSS × HARMONICA ✦ GREETINGS TO ALL TERMINAL DWELLERS ✦ YOUR COLOR · YOUR DEMO :: temo -primary '#00D8FF' ✦ PRESS SPACE FOR THE NEXT EFFECT ✦ STAY AWHILE AND RENDER ✦ ")

func (m *model) drawScroller() {
	if !m.scrollerOn || m.w < 24 {
		return
	}
	rows := m.h - 1
	basePos := float64(rows) - 2.6
	amp := 1.15 + 0.85*math.Max(0, m.pulsePos)
	off := int(m.t * 14)
	n := len(scrollText)
	for x := 0; x < m.w; x++ {
		r := scrollText[(off+x)%n]
		if r == ' ' {
			continue
		}
		y := int(basePos + math.Sin(float64(x)*0.22+m.t*2.8)*amp)
		if y < 0 || y >= rows {
			continue
		}
		col := uint8(190 + int(60*math.Sin(float64(x)*0.13-m.t*3.1)))
		oi := y*m.w + x
		m.ovRune[oi] = r
		m.ovCol[oi] = col
	}
}

// ── scene-name toast ─────────────────────────────────────────────────────

func (m *model) drawToast() {
	const dur = 1.6
	if m.sceneClock >= dur || m.showHelp {
		return
	}
	name := m.scenes[m.cur].Name()
	spaced := make([]rune, 0, len(name)*2)
	for _, r := range name {
		spaced = append(spaced, r, ' ')
	}
	s := strings.TrimRight(string(spaced), " ")
	n := len([]rune(s))
	x := (m.w - n) / 2
	y := (m.h - 1) / 3
	cf := 1 - m.sceneClock/dur
	col := uint8(150 + int(105*cf))
	m.ovDimRect(x-3, y-1, x+n+2, y+1)
	m.ovText(x, y, s, col)
}

// ── help panel ───────────────────────────────────────────────────────────

func padRunes(s string, n int) string {
	r := []rune(s)
	for len(r) < n {
		r = append(r, ' ')
	}
	return string(r[:n])
}

func centerDashes(title string, width int) string {
	t := []rune(title)
	left := (width - len(t)) / 2
	right := width - len(t) - left
	return strings.Repeat("─", left) + title + strings.Repeat("─", right)
}

func (m *model) drawHelp() {
	if !m.showHelp {
		return
	}
	items := [][2]string{
		{"SPACE / →", "next scene"},
		{"←", "previous scene"},
		{"1-7", "jump to scene"},
		{"P", "pause"},
		{"C", "crt scanlines"},
		{"S", "scroller"},
		{"H / ?", "toggle help"},
		{"Q / ESC", "quit"},
	}
	const kw, vw = 9, 14
	inner := kw + 1 + vw
	lines := []string{"╭" + centerDashes(" CONTROLS ", inner+2) + "╮"}
	for _, it := range items {
		lines = append(lines, "│ "+padRunes(it[0], kw)+" "+padRunes(it[1], vw)+" │")
	}
	lines = append(lines, "╰"+strings.Repeat("─", inner+2)+"╯")

	bw := inner + 4
	x := (m.w - bw) / 2
	y := (m.h - 1 - len(lines)) / 2
	m.ovDimRect(x-2, y-1, x+bw+1, y+len(lines))
	for i, ln := range lines {
		m.ovText(x, y+i, ln, 235)
	}
}
