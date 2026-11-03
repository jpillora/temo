package main

import (
	"bytes"
	"encoding/base64"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/image/draw"
)

const maxPasteBytes = 32 << 20
const maxPastePixels = 32 << 20

type pastedImageMsg struct{ image image.Image }

// Intercept OSC before Bubble Tea's key parser, which otherwise interprets the
// payload as keystrokes. Embedding File preserves raw mode and cancellable input.
type imagePasteReader struct {
	*os.File
	send             func(tea.Msg)
	mu               sync.Mutex
	escape           bool
	osc              bool
	oscEscape        bool
	discard          bool
	payload          []byte
	pending          []byte
	escapeTimer      *time.Timer
	escapeGeneration uint64
}

func (r *imagePasteReader) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.escapeGeneration++
	if r.escapeTimer != nil {
		r.escapeTimer.Stop()
	}
}

func (r *imagePasteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(r.pending) > 0 {
		n := copy(p, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	}
	// Leave room for an Escape retained from the previous read. In particular,
	// do not leave buffered keys waiting behind cancelreader's fd readiness poll.
	readSize := max(1, len(p)-1)
	n, err := r.File.Read(p[:readSize])
	out, payloads := r.filter(p[:n])
	for _, payload := range payloads {
		if img := decodePasteImage(payload); img != nil {
			r.send(pastedImageMsg{img})
		}
	}
	n = copy(p, out)
	r.pending = out[n:]
	return n, err
}

// filter supports fragmented sequences and both BEL and ST terminators. All
// OSCs are consumed, including unsupported or oversized ones, so their contents
// can never activate demo controls.
func (r *imagePasteReader) filter(data []byte) ([]byte, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.escapeGeneration++
	if r.escapeTimer != nil {
		r.escapeTimer.Stop()
	}
	var out []byte
	var payloads []string
	for _, b := range data {
		if r.osc {
			if b == '\a' || (r.oscEscape && b == '\\') {
				if !r.discard {
					payload := r.payload
					if r.oscEscape && b == '\\' {
						payload = payload[:len(payload)-1]
					}
					payloads = append(payloads, string(payload))
				}
				r.osc, r.oscEscape, r.discard = false, false, false
				r.payload = nil
				continue
			}
			r.oscEscape = b == '\x1b'
			if !r.discard {
				if len(r.payload) >= maxPasteBytes {
					r.discard = true
					r.payload = nil
				} else {
					r.payload = append(r.payload, b)
				}
			}
			continue
		}
		if r.escape {
			r.escape = false
			if b == ']' {
				r.osc = true
				continue
			}
			out = append(out, '\x1b')
		}
		if b == '\x1b' {
			r.escape = true
		} else {
			out = append(out, b)
		}
	}
	if r.escape {
		generation := r.escapeGeneration
		// A lone Escape still quits promptly; allow a short window for a split
		// OSC introducer to arrive, just as terminal key parsers do.
		r.escapeTimer = time.AfterFunc(50*time.Millisecond, func() {
			r.mu.Lock()
			if generation != r.escapeGeneration {
				r.mu.Unlock()
				return
			}
			escape := r.escape
			r.escape = false
			r.mu.Unlock()
			if escape && r.send != nil {
				r.send(tea.KeyMsg{Type: tea.KeyEsc})
			}
		})
	}
	return out, payloads
}

func decodePasteImage(osc string) image.Image {
	var encoded string
	switch {
	case strings.HasPrefix(osc, "52;"):
		_, encoded, _ = strings.Cut(osc[len("52;"):], ";")
	case strings.HasPrefix(osc, "1337;File="):
		_, encoded, _ = strings.Cut(osc, ":")
	default:
		return nil
	}
	if strings.HasPrefix(encoded, "data:image/") {
		header, body, ok := strings.Cut(encoded, ",")
		if !ok || !strings.HasSuffix(header, ";base64") {
			return nil
		}
		encoded = body
	}
	if len(encoded) > maxPasteBytes {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil
	}
	// Some clipboard senders base64-encode a complete image data URL.
	if bytes.HasPrefix(data, []byte("data:image/")) {
		header, body, ok := strings.Cut(string(data), ",")
		if !ok || !strings.HasSuffix(header, ";base64") {
			return nil
		}
		data, err = base64.StdEncoding.DecodeString(body)
		if err != nil {
			return nil
		}
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxPastePixels/cfg.Height {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return img
}

func (m *model) renderPastedImage() {
	if len(m.pasteFrame) == len(m.frame) {
		copy(m.frame, m.pasteFrame)
		return
	}
	b := m.pastedImage.Bounds()
	scale := math.Min(float64(m.pw)/float64(b.Dx()), float64(m.ph)/float64(b.Dy()))
	w := max(1, int(math.Round(float64(b.Dx())*scale)))
	h := max(1, int(math.Round(float64(b.Dy())*scale)))
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), m.pastedImage, b, draw.Src, nil)
	clear(m.frame)
	x0, y0 := (m.pw-w)/2, (m.ph-h)/2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := dst.RGBAAt(x, y) // premultiplied RGB composites transparency on black
			m.frame[(y0+y)*m.pw+x0+x] = RGB8{c.R, c.G, c.B}
		}
	}
	m.pasteFrame = append([]RGB8(nil), m.frame...)
}
