package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// RGBf is a color with float channels in [0,255], used for per-pixel math.
type RGBf struct{ R, G, B float64 }

// RGB8 is an 8-bit color, used for terminal output.
type RGB8 struct{ R, G, B uint8 }

func (c RGB8) Hex() string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }

func (c RGB8) luma() float64 {
	return (0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)) / 255
}

// Palette is a 256-step ramp derived from a single primary color: near-black
// shadows sweep through the deep and pure primary up to a tinted white, with
// a little hue drift along the way so gradients feel rich, not flat.
type Palette struct {
	lutF    [256]RGBf
	lut8    [256]RGB8
	Primary RGB8
}

var namedColors = map[string]string{
	"red":     "#FF4B4B",
	"orange":  "#FF8A3D",
	"amber":   "#FFC53D",
	"yellow":  "#F2E85C",
	"lime":    "#A8E635",
	"green":   "#3DDC84",
	"teal":    "#2DD4BF",
	"cyan":    "#22D3EE",
	"blue":    "#4C8DFF",
	"indigo":  "#7C83FF",
	"violet":  "#A78BFA",
	"purple":  "#C084FC",
	"magenta": "#FF5FD2",
	"pink":    "#FF7AB8",
	"rose":    "#FB7185",
	"white":   "#F2F2F2",
}

func colorNames() string {
	names := make([]string, 0, len(namedColors))
	for n := range namedColors {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func parseColor(input string) (RGB8, error) {
	s := strings.ToLower(strings.TrimSpace(input))
	if hex, ok := namedColors[s]; ok {
		s = strings.ToLower(hex)
	}
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return RGB8{}, fmt.Errorf("%q is not a color I understand — try hex like \"#FF5FD2\" or a name: %s", input, colorNames())
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return RGB8{}, fmt.Errorf("%q is not valid hex — try \"#FF5FD2\" or a name: %s", input, colorNames())
	}
	return RGB8{uint8(v >> 16), uint8(v >> 8), uint8(v)}, nil
}

func rgbToHSL(c RGB8) (h, s, l float64) {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	max := math.Max(r, math.Max(g, b))
	min := math.Min(r, math.Min(g, b))
	l = (max + min) / 2
	if max == min {
		return 0, 0, l
	}
	d := max - min
	if l > 0.5 {
		s = d / (2 - max - min)
	} else {
		s = d / (max + min)
	}
	switch max {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	case b:
		h = (r-g)/d + 4
	}
	h *= 60
	return
}

func hue2rgb(p, q, t float64) float64 {
	if t < 0 {
		t++
	}
	if t > 1 {
		t--
	}
	switch {
	case t < 1.0/6:
		return p + (q-p)*6*t
	case t < 1.0/2:
		return q
	case t < 2.0/3:
		return p + (q-p)*(2.0/3-t)*6
	}
	return p
}

func hslToRGB(h, s, l float64) RGBf {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	if s <= 0 {
		v := l * 255
		return RGBf{v, v, v}
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	hk := h / 360
	return RGBf{
		hue2rgb(p, q, hk+1.0/3) * 255,
		hue2rgb(p, q, hk) * 255,
		hue2rgb(p, q, hk-1.0/3) * 255,
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func lerp(a, b, f float64) float64 { return a + (b-a)*f }

func u8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v)
}

type palStop struct{ pos, h, s, l float64 }

func BuildPalette(primary RGB8) *Palette {
	h, s, l := rgbToHSL(primary)
	stops := []palStop{
		{0.00, h - 26, s * 0.80, 0.045},
		{0.42, h - 9, math.Min(1, s*1.05), math.Max(0.16, l*0.55)},
		{0.70, h, s, l},
		{0.88, h + 8, s * 0.72, math.Min(0.92, l+0.24)},
		{1.00, h + 14, s * 0.30, 0.97},
	}
	p := &Palette{Primary: primary}
	for i := 0; i < 256; i++ {
		pos := float64(i) / 255
		k := 0
		for k < len(stops)-2 && pos > stops[k+1].pos {
			k++
		}
		a, b := stops[k], stops[k+1]
		f := clamp01((pos - a.pos) / (b.pos - a.pos))
		f = f * f * (3 - 2*f)
		c := hslToRGB(lerp(a.h, b.h, f), clamp01(lerp(a.s, b.s, f)), clamp01(lerp(a.l, b.l, f)))
		p.lutF[i] = c
		p.lut8[i] = RGB8{u8(c.R), u8(c.G), u8(c.B)}
	}
	return p
}
