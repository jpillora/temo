package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func pastePNG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{255, 40, 10, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestOSCPaste(t *testing.T) {
	encoded := pastePNG(t)
	for _, payload := range []string{
		"52;c;" + encoded,
		"52;c;data:image/png;base64," + encoded,
		"52;c;" + base64.StdEncoding.EncodeToString([]byte("data:image/png;base64,"+encoded)),
		"1337;File=name=aW1nLnBuZw==;inline=1:" + encoded,
	} {
		for _, terminator := range []string{"\a", "\x1b\\"} {
			wire := "a\x1b]" + payload + terminator + "\x1b[Cp"
			for chunkSize := 1; chunkSize <= len(wire); chunkSize++ {
				r := &imagePasteReader{}
				var keys []byte
				var messages []string
				for offset := 0; offset < len(wire); offset += chunkSize {
					out, msgs := r.filter([]byte(wire[offset:min(offset+chunkSize, len(wire))]))
					keys = append(keys, out...)
					messages = append(messages, msgs...)
				}
				r.stop()
				if string(keys) != "a\x1b[Cp" || len(messages) != 1 {
					t.Fatalf("chunk size %d: keys %q, messages %d", chunkSize, keys, len(messages))
				}
				if img := decodePasteImage(messages[0]); img == nil || img.Bounds().Dx() != 4 {
					t.Fatalf("chunk size %d: image did not decode", chunkSize)
				}
			}
		}
	}
	for _, payload := range []string{"52;c;?", "52;c;cQ==", "52;c;%%%", "0;q", "1337;File=oops"} {
		if decodePasteImage(payload) != nil {
			t.Fatalf("invalid image accepted: %q", payload)
		}
	}
}

func TestImagePreview(t *testing.T) {
	m := testModel()
	m.paused = true
	m.showHelp = true
	m.Update(pastedImageMsg{decodePasteImage("52;c;" + pastePNG(t))})
	deadline := m.pasteUntil
	before := m.t
	view := m.View()
	if !strings.Contains(view, "IMAGE PASTE") || strings.Count(view, "\n")+1 != m.h {
		t.Fatal("missing preview or invalid view geometry")
	}
	center := m.frame[(m.ph/2)*m.pw+m.pw/2]
	if center != (RGB8{255, 40, 10}) || m.frame[0] != (RGB8{}) {
		t.Fatalf("preview color/letterboxing: center=%v corner=%v", center, m.frame[0])
	}
	m.Update(tickMsg(deadline.Add(-time.Millisecond)))
	if m.pastedImage == nil || m.t != before {
		t.Fatal("preview expired early or scene advanced")
	}
	m.resize(44, 12)
	m.View()
	m.Update(tickMsg(deadline))
	if m.pastedImage != nil || !m.paused || m.t != before {
		t.Fatal("preview did not expire or failed to preserve pause state")
	}
	m.paused = false
	m.Update(tickMsg(deadline.Add(time.Second / 30)))
	if m.t <= before {
		t.Fatal("demo did not resume")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}, Paste: true})
	if m.keyPresses[0].label != "paste" {
		t.Fatal("text paste was interpreted as a control")
	}
}
