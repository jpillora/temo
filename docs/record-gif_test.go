//go:build gifrecord

package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestRecordTiledGIFFrames renders the raw animation frames consumed by
// record-gif.sh. The script copies this file beside the application sources
// in a temporary module so it can exercise TEMO's real, unexported renderer.
func TestRecordTiledGIFFrames(t *testing.T) {
	dir := os.Getenv("TEMO_GIF_FRAMES_DIR")
	if dir == "" {
		t.Skip("TEMO_GIF_FRAMES_DIR not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	tiles := []struct {
		color string
		scene int
		warm  int
	}{
		{"#FF5FD2", 0, 60},
		{"#00D8FF", 1, 60},
		{"#A3FF12", 2, 60},
		{"#FF9F1C", 3, 60},
		{"#9D4EDD", 4, 60},
		{"#3A86FF", 5, 60},
		{"#FF3355", 6, 60},
		{"#FFD60A", 0, 120},
	}

	for tileIndex, tile := range tiles {
		primary, err := parseColor(tile.color)
		if err != nil {
			t.Fatal(err)
		}
		m := newModel(BuildPalette(primary), 12, 128, 0, tile.scene+1, false)
		m.resize(100, 31)
		m.t = 5 + float64(tileIndex)*0.17
		scene := m.scenes[tile.scene]

		for i := 0; i < tile.warm; i++ {
			m.t += 1.0 / 30
			scene.Step(1.0/30, m.t)
		}
		for frameIndex := 0; frameIndex < 12; frameIndex++ {
			m.t += 1.0 / 12
			scene.Step(1.0/12, m.t)
			scene.Render(m.t, m.bufCur)
			m.composeFrame()

			const scale = 2
			img := image.NewRGBA(image.Rect(0, 0, m.pw*scale, m.ph*scale))
			for y := 0; y < m.ph; y++ {
				for x := 0; x < m.pw; x++ {
					color := m.frame[y*m.pw+x]
					for dy := 0; dy < scale; dy++ {
						row := img.PixOffset(x*scale, y*scale+dy)
						for dx := 0; dx < scale; dx++ {
							pixel := row + dx*4
							img.Pix[pixel+0] = color.R
							img.Pix[pixel+1] = color.G
							img.Pix[pixel+2] = color.B
							img.Pix[pixel+3] = 255
						}
					}
				}
			}

			name := filepath.Join(dir, fmt.Sprintf("tile-%d-frame-%02d.png", tileIndex+1, frameIndex))
			file, err := os.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(file, img); err != nil {
				file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}
