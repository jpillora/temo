package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
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
