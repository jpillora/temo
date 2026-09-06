package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func testModel() *model {
	m := newModel(BuildPalette(RGB8{0xFF, 0x5F, 0xD2}), 30, 128, 12, 1, true)
	m.resize(120, 36)
	m.t = 5 // past the fade-in
	return m
}

func TestScenesProduceOutput(t *testing.T) {
	m := testModel()
	for si, s := range m.scenes {
		tt := 5.0
		for f := 0; f < 90; f++ {
			tt += 1.0 / 30
			s.Step(1.0/30, tt)
		}
		s.Render(tt, m.bufCur)
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
		mean := float64(sum) / float64(len(m.bufCur))
		if mx-mn < 64 {
			t.Errorf("scene %d %s: flat output (min=%d max=%d)", si+1, s.Name(), mn, mx)
		}
		if mean < 2 || mean > 240 {
			t.Errorf("scene %d %s: degenerate mean %.1f", si+1, s.Name(), mean)
		}
	}
}

func TestParseColor(t *testing.T) {
	cases := []struct {
		in   string
		want RGB8
		ok   bool
	}{
		{"#FF5FD2", RGB8{0xFF, 0x5F, 0xD2}, true},
		{"ff5fd2", RGB8{0xFF, 0x5F, 0xD2}, true},
		{"#F0A", RGB8{0xFF, 0x00, 0xAA}, true},
		{"cyan", RGB8{0x22, 0xD3, 0xEE}, true},
		{"CYAN", RGB8{0x22, 0xD3, 0xEE}, true},
		{"nope", RGB8{}, false},
		{"#12345", RGB8{}, false},
	}
	for _, c := range cases {
		got, err := parseColor(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("parseColor(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("parseColor(%q) should have failed", c.in)
		}
	}
}

func TestViewGeometry(t *testing.T) {
	m := testModel()
	if bar := m.statusBar(); !strings.Contains(bar, "36×120") {
		t.Fatalf("status bar does not show rows×cols: %q", bar)
	}
	frame := m.View()
	lines := 1
	for _, r := range frame {
		if r == '\n' {
			lines++
		}
	}
	if lines != m.h {
		t.Errorf("View produced %d lines, want %d", lines, m.h)
	}
	// transition path and tiny terminals must not panic
	m.switchScene(1)
	m.transPos = 0.5
	_ = m.View()
	m.resize(44, 12)
	_ = m.View()
}

func TestKeyRow(t *testing.T) {
	m := testModel()
	if len(m.keyPresses) != 0 {
		t.Fatal("key row should start hidden")
	}

	keys := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'a'}},
		{Type: tea.KeyCtrlC},
		{Type: tea.KeyShiftLeft, Alt: true},
		{Type: tea.KeyRunes, Runes: []rune{'K'}, Alt: true},
		{Type: tea.KeySpace},
	}
	for _, key := range keys {
		m.addKeyPress(key)
	}
	want := []string{"space", "alt+shift+k", "alt+shift+left", "ctrl+c", "a"}
	if len(m.keyPresses) != len(want) {
		t.Fatalf("key row has %d entries, want %d", len(m.keyPresses), len(want))
	}
	for i, label := range want {
		if m.keyPresses[i].label != label {
			t.Errorf("key %d = %q, want %q", i, m.keyPresses[i].label, label)
		}
	}

	m.keyPresses[0].age = 2
	m.stepKeyPresses(1)
	if len(m.keyPresses) != 4 || m.keyPresses[0].label != "alt+shift+k" {
		t.Fatalf("individual key expiry left row %+v", m.keyPresses)
	}
	m.stepKeyPresses(keyPressDuration)
	if len(m.keyPresses) != 0 {
		t.Fatalf("expired key row was retained: %+v", m.keyPresses)
	}
}

func TestMouseImpact(t *testing.T) {
	m := testModel()
	m.scenes[m.cur].Render(m.t, m.bufCur)
	m.composeFrame()
	before := append([]RGB8(nil), m.frame...)

	_, _ = m.Update(tea.MouseMsg{
		X:      17,
		Y:      8,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	if len(m.impacts) != 1 {
		t.Fatalf("left click created %d impacts, want 1", len(m.impacts))
	}
	if got := m.impacts[0]; got.x != 17 || got.y != 16.5 {
		t.Fatalf("impact coordinates = (%.1f, %.1f), want (17, 16.5)", got.x, got.y)
	}
	if !m.dragging {
		t.Fatal("left press did not begin a drag")
	}

	// Isolate the local post-processing effect from the global pulse kick.
	m.pulsePos = 0
	m.composeFrame()
	changed := 0
	for i := range m.frame {
		if m.frame[i] != before[i] {
			changed++
		}
	}
	if changed == 0 {
		t.Fatal("click impact did not change the composed frame")
	}

	// Tiny movements are accumulated; a fast event is interpolated into an
	// evenly spaced trail rather than leaving a gap.
	_, _ = m.Update(tea.MouseMsg{X: 20, Y: 8, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	if len(m.impacts) != 1 {
		t.Fatalf("sub-spacing drag created %d impacts, want 1", len(m.impacts))
	}
	_, _ = m.Update(tea.MouseMsg{X: 36, Y: 8, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	if len(m.impacts) != 4 {
		t.Fatalf("interpolated drag created %d impacts, want 4", len(m.impacts))
	}

	// Release ends the wake; the middle button and status bar do nothing.
	_, _ = m.Update(tea.MouseMsg{X: 36, Y: 8, Action: tea.MouseActionRelease, Button: tea.MouseButtonNone})
	if m.dragging {
		t.Fatal("mouse release did not end the drag")
	}
	count := len(m.impacts)
	ignored := []tea.MouseMsg{
		{X: 60, Y: 8, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft},
		{X: 17, Y: 8, Action: tea.MouseActionPress, Button: tea.MouseButtonMiddle},
		{X: 17, Y: m.h - 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft},
	}
	for _, msg := range ignored {
		_, _ = m.Update(msg)
	}
	if len(m.impacts) != count {
		t.Fatalf("ignored mouse events changed impact count to %d", len(m.impacts))
	}

	for i := 0; i < maxImpacts+2; i++ {
		m.addImpact(i, 1)
	}
	if len(m.impacts) != maxImpacts {
		t.Fatalf("impact count = %d after click burst, want cap of %d", len(m.impacts), maxImpacts)
	}

	m.stepImpacts(impactDuration)
	if len(m.impacts) != 0 {
		t.Fatalf("expired impact was retained: %+v", m.impacts)
	}
}

func TestLaserMode(t *testing.T) {
	m := testModel()
	_, _ = m.Update(tea.MouseMsg{X: 40, Y: 10, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	if m.effectMode != effectLazers || m.modeToast != modeToastDuration {
		t.Fatalf("right click selected mode %v with toast %.2f", m.effectMode, m.modeToast)
	}

	_, _ = m.Update(tea.MouseMsg{X: 40, Y: 10, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if len(m.lasers) != 3 {
		t.Fatalf("laser click created %d beams, want 3", len(m.lasers))
	}
	for i, beam := range m.lasers {
		if beam.x1 != 40 || beam.y1 != 20.5 {
			t.Errorf("beam %d target = (%.1f, %.1f), want (40, 20.5)", i, beam.x1, beam.y1)
		}
		onEdge := beam.x0 == 0 || beam.x0 == float64(m.pw-1) || beam.y0 == 0 || beam.y0 == float64(m.ph-1)
		if !onEdge {
			t.Errorf("beam %d source (%.1f, %.1f) is not on an edge", i, beam.x0, beam.y0)
		}
	}

	m.scenes[m.cur].Render(m.t, m.bufCur)
	m.composeFrame()
	changed := false
	for _, c := range m.frame {
		if c == m.pal.lut8[255] {
			changed = true
			break
		}
	}
	if !changed {
		t.Fatal("laser beams did not produce a hot pixel in the composed frame")
	}

	_, _ = m.Update(tea.MouseMsg{X: 58, Y: 10, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	if len(m.lasers) != 6 {
		t.Fatalf("laser drag created %d beams, want 6", len(m.lasers))
	}
	m.stepLasers(laserDuration)
	if len(m.lasers) != 0 {
		t.Fatalf("expired lasers were retained: %+v", m.lasers)
	}

	_, _ = m.Update(tea.MouseMsg{X: 40, Y: 10, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	if m.effectMode != effectRipple {
		t.Fatalf("second right click selected %v, want RIPPLE", m.effectMode)
	}
}

// TestDumpPNG writes one PNG per scene through the full compose pipeline
// (palette, vignette, scanlines) for visual tuning. Skipped unless
// TEMO_PNG_DIR is set; TEMO_PNG_COLOR overrides the primary:
//
//	TEMO_PNG_DIR=/tmp/temo TEMO_PNG_COLOR=cyan go test -run DumpPNG
func TestDumpPNG(t *testing.T) {
	dir := os.Getenv("TEMO_PNG_DIR")
	if dir == "" {
		t.Skip("TEMO_PNG_DIR not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := testModel()
	if c := os.Getenv("TEMO_PNG_COLOR"); c != "" {
		prim, err := parseColor(c)
		if err != nil {
			t.Fatal(err)
		}
		m.pal = BuildPalette(prim)
	}
	for si := range m.scenes {
		m.cur = si
		m.t = 5
		for f := 0; f < 150; f++ {
			m.t += 1.0 / 30
			m.scenes[si].Step(1.0/30, m.t)
		}
		m.scenes[si].Render(m.t, m.bufCur)
		m.composeFrame()

		const scale = 6
		img := image.NewRGBA(image.Rect(0, 0, m.pw*scale, m.ph*scale))
		for y := 0; y < m.ph; y++ {
			for x := 0; x < m.pw; x++ {
				c := m.frame[y*m.pw+x]
				for dy := 0; dy < scale; dy++ {
					row := img.PixOffset(x*scale, (y*scale + dy))
					for dx := 0; dx < scale; dx++ {
						p := row + dx*4
						img.Pix[p+0] = c.R
						img.Pix[p+1] = c.G
						img.Pix[p+2] = c.B
						img.Pix[p+3] = 255
					}
				}
			}
		}
		name := filepath.Join(dir, fmt.Sprintf("scene%d.png", si+1))
		f, err := os.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
		t.Logf("wrote %s", name)
	}
}
