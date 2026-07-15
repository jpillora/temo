package main

import "math"

// Scene is one demo effect. Render fills out (len w*h) with palette indices
// 0..255 for a w×h half-block pixel grid. Stateful scenes advance in Step so
// pausing freezes them; Render must not mutate state.
type Scene interface {
	Name() string
	Resize(w, h int)
	Step(dt, t float64)
	Render(t float64, out []uint8)
}

type base struct{ w, h int }

func (b *base) Resize(w, h int)    { b.w, b.h = w, h }
func (b *base) Step(dt, t float64) {}

func idx8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(v * 255)
}

// rng32 is a tiny xorshift PRNG — cheap enough to call twice per pixel.
type rng32 uint32

func (r *rng32) next() uint32 {
	x := uint32(*r)
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	*r = rng32(x)
	return x
}

func (r *rng32) f() float64 { return float64(r.next()%65536) / 65536 }

func splat(f []float64, w, h, x, y int, b float64) {
	add := func(i int, v float64) {
		f[i] += v
		if f[i] > 1.25 {
			f[i] = 1.25
		}
	}
	if x < 0 || y < 0 || x >= w || y >= h {
		return
	}
	add(y*w+x, b)
	g := b * 0.28
	if x > 0 {
		add(y*w+x-1, g)
	}
	if x < w-1 {
		add(y*w+x+1, g)
	}
	if y > 0 {
		add((y-1)*w+x, g)
	}
	if y < h-1 {
		add((y+1)*w+x, g)
	}
}

// ── 1. PLASMA ────────────────────────────────────────────────────────────
// Layered sine fields plus a wandering radial wave, with palette cycling.

type plasma struct{ base }

func (p *plasma) Name() string { return "PLASMA" }

func (p *plasma) Render(t float64, out []uint8) {
	w, h := p.w, p.h
	cx, cy := float64(w)/2, float64(h)/2
	wx := cx + 0.45*cx*math.Sin(t*0.35)
	wy := cy + 0.45*cy*math.Cos(t*0.28)
	i := 0
	for y := 0; y < h; y++ {
		fy := float64(y)
		sy := math.Sin(fy*0.081 - t*0.9)
		for x := 0; x < w; x++ {
			fx := float64(x)
			v := math.Sin(fx*0.055+t*1.1) + sy + math.Sin((fx+fy)*0.047+t*0.6)
			dx, dy := fx-wx, fy-wy
			v += math.Sin(math.Sqrt(dx*dx+dy*dy)*0.10 - t*1.7)
			out[i] = uint8(int((v+4)*31.9+t*26) & 255)
			i++
		}
	}
}

// ── 2. TUNNEL ────────────────────────────────────────────────────────────
// Fly through a twisting checkered tube; the eye of the tunnel wanders.

type tunnel struct{ base }

func (tn *tunnel) Name() string { return "TUNNEL" }

func (tn *tunnel) Render(t float64, out []uint8) {
	w, h := tn.w, tn.h
	cx := float64(w)*0.5 + float64(w)*0.16*math.Sin(t*0.53)
	cy := float64(h)*0.5 + float64(h)*0.16*math.Cos(t*0.41)
	i := 0
	for y := 0; y < h; y++ {
		dy := float64(y) - cy
		for x := 0; x < w; x++ {
			dx := float64(x) - cx
			d := math.Sqrt(dx*dx + dy*dy)
			if d < 1 {
				d = 1
			}
			r := 140.0 / d
			a := math.Atan2(dy, dx)
			u := r*1.6 + t*22
			v := (a/math.Pi+1)*13 + t*1.5 + r*0.06
			band := (int(u/7) + int(v)) & 1
			sh := 1.0 / (1.0 + r*0.045)
			out[i] = idx8(sh * (0.34 + 0.62*float64(band)))
			i++
		}
	}
}

// ── 3. METABALLS ─────────────────────────────────────────────────────────
// Five blobs on Lissajous orbits; their fields sum into a molten glow.

type metaballs struct{ base }

func (mb *metaballs) Name() string { return "METABALLS" }

func (mb *metaballs) Render(t float64, out []uint8) {
	w, h := mb.w, mb.h
	cx, cy := float64(w)/2, float64(h)/2
	m := math.Min(float64(w), float64(h))
	var bx, by, r2 [5]float64
	for k := 0; k < 5; k++ {
		fk := float64(k)
		bx[k] = cx + cx*0.62*math.Sin(t*(0.37+0.11*fk)+fk*2.4)
		by[k] = cy + cy*0.62*math.Sin(t*(0.51+0.09*fk)+fk*1.7)
		rr := m * (0.13 + 0.05*math.Sin(t*0.8+fk*2.1))
		r2[k] = rr * rr
	}
	i := 0
	for y := 0; y < h; y++ {
		fy := float64(y)
		for x := 0; x < w; x++ {
			fx := float64(x)
			f := 0.035
			for k := 0; k < 5; k++ {
				dx, dy := fx-bx[k], fy-by[k]
				f += r2[k] / (dx*dx + dy*dy + 4)
			}
			out[i] = idx8(f * 0.47)
			i++
		}
	}
}

// ── 4. TORUS KNOT ────────────────────────────────────────────────────────
// A (2,3) torus knot of light points tumbling in 3D, drawn into a fade
// buffer for motion trails, with a bright comet chasing along the curve.

type knot struct {
	base
	fade []float64
}

func (k *knot) Name() string { return "TORUS KNOT" }

func (k *knot) Resize(w, h int) {
	k.base.Resize(w, h)
	k.fade = make([]float64, w*h)
}

func (k *knot) Step(dt, t float64) {
	if k.w == 0 || len(k.fade) == 0 {
		return
	}
	for i := range k.fade {
		k.fade[i] *= 0.86
	}
	w, h := k.w, k.h
	cx, cy := float64(w)/2, float64(h)/2
	scale := 0.24 * math.Min(float64(w), float64(h))
	sy1, cy1 := math.Sincos(t * 0.7)
	sx1, cx1 := math.Sincos(t * 0.45)
	const N = 900
	head := int(t*160) % N
	for i := 0; i < N; i++ {
		th := float64(i) / N * 2 * math.Pi
		r := math.Cos(3*th) + 2
		x := r * math.Cos(2*th)
		y := r * math.Sin(2*th)
		z := -math.Sin(3 * th)
		x, z = x*cy1+z*sy1, -x*sy1+z*cy1
		y, z = y*cx1-z*sx1, y*sx1+z*cx1
		s := scale * 4.4 / (z + 4.4)
		px := int(cx + x*s)
		py := int(cy + y*s)
		b := clamp01(0.62 - z*0.18)
		if di := (i - head + N) % N; di < 16 {
			b = 1.1
		}
		splat(k.fade, w, h, px, py, b*0.75)
	}
}

func (k *knot) Render(t float64, out []uint8) {
	for i, f := range k.fade {
		out[i] = idx8(f * 0.92)
	}
}

// ── 5. ROTOZOOM ──────────────────────────────────────────────────────────
// The classic: an infinite procedural texture rotating and breathing.

type rotozoom struct{ base }

func (rz *rotozoom) Name() string { return "ROTOZOOM" }

func (rz *rotozoom) Render(t float64, out []uint8) {
	w, h := rz.w, rz.h
	cx, cy := float64(w)/2, float64(h)/2
	z := 1.0 / (1.7 + 0.85*math.Sin(t*0.31))
	cs := math.Cos(t*0.4) * z
	sn := math.Sin(t*0.4) * z
	panu, panv := t*9, t*3.7
	i := 0
	for y := 0; y < h; y++ {
		v0 := float64(y) - cy
		for x := 0; x < w; x++ {
			u0 := float64(x) - cx
			u := u0*cs - v0*sn + panu
			v := u0*sn + v0*cs + panv
			m := math.Sin(u*0.29)*math.Sin(v*0.29) + math.Sin(u*0.083+t*0.9)*math.Sin(v*0.083)
			out[i] = uint8(128 + 63*m)
			i++
		}
	}
}

// ── 6. STARFIELD ─────────────────────────────────────────────────────────
// Hyperspace: stars stream past with motion-blur trails.

type starfield struct {
	base
	fade       []float64
	sx, sy, sz []float64
	rng        rng32
}

func (sf *starfield) Name() string { return "STARFIELD" }

func (sf *starfield) Resize(w, h int) {
	sf.base.Resize(w, h)
	sf.fade = make([]float64, w*h)
	if sf.sx == nil {
		sf.rng = 0xC0FFEE
		const N = 320
		sf.sx = make([]float64, N)
		sf.sy = make([]float64, N)
		sf.sz = make([]float64, N)
		for i := 0; i < N; i++ {
			sf.respawn(i)
			sf.sz[i] = 0.05 + 0.95*sf.rng.f()
		}
	}
}

func (sf *starfield) respawn(i int) {
	sf.sx[i] = sf.rng.f()*2 - 1
	sf.sy[i] = sf.rng.f()*2 - 1
	sf.sz[i] = 1
}

func (sf *starfield) Step(dt, t float64) {
	if sf.w == 0 || len(sf.fade) == 0 {
		return
	}
	for i := range sf.fade {
		sf.fade[i] *= 0.82
	}
	w, h := sf.w, sf.h
	cx, cy := float64(w)/2, float64(h)/2
	for i := range sf.sx {
		sf.sz[i] -= dt * 0.55
		if sf.sz[i] < 0.05 {
			sf.respawn(i)
		}
		z := sf.sz[i]
		px := int(cx + sf.sx[i]/z*cx*0.85)
		py := int(cy + sf.sy[i]/z*cy*0.85)
		b := 0.18 + (1-z)*(1-z)*1.15
		if b > 1.2 {
			b = 1.2
		}
		splat(sf.fade, w, h, px, py, b)
	}
}

func (sf *starfield) Render(t float64, out []uint8) {
	for i, f := range sf.fade {
		out[i] = idx8(f * 1.1)
	}
}

// ── 7. INFERNO ───────────────────────────────────────────────────────────
// The DOOM PSX fire, tinted by the palette: heat seeds at the bottom row
// and convects upward, cooling and drifting as it rises.

type fire struct {
	base
	heat []int16
	rng  rng32
}

func (f *fire) Name() string { return "INFERNO" }

func (f *fire) Resize(w, h int) {
	f.base.Resize(w, h)
	f.heat = make([]int16, w*h)
	if f.rng == 0 {
		f.rng = 0xBEEF
	}
}

func (f *fire) Step(dt, t float64) {
	w, h := f.w, f.h
	if w == 0 || h < 3 || len(f.heat) == 0 {
		return
	}
	// cool every cell a little
	cool := uint32(700/h) + 2
	for i := range f.heat {
		c := int16(f.rng.next() % cool)
		if f.heat[i] > c {
			f.heat[i] -= c
		} else {
			f.heat[i] = 0
		}
	}
	// convect: heat drifts up through a 4-tap average of the rows below
	for y := 0; y < h-2; y++ {
		row := y * w
		below := row + w
		below2 := row + 2*w
		for x := 0; x < w; x++ {
			xl, xr := x-1, x+1
			if xl < 0 {
				xl = 0
			}
			if xr >= w {
				xr = w - 1
			}
			f.heat[row+x] = int16((int(f.heat[below+xl]) + int(f.heat[below+x]) +
				int(f.heat[below+xr]) + int(f.heat[below2+x])) / 4)
		}
	}
	// stoke the source: hot clumps wander along the bottom so tongues separate
	for x := 0; x < w; x++ {
		fx := float64(x)
		wave := math.Sin(fx*0.25+t*2.6) * math.Sin(fx*0.09-t*1.3)
		v := 205 + 50*wave + float64(f.rng.next()%40) - 20
		if v < 0 {
			v = 0
		} else if v > 255 {
			v = 255
		}
		f.heat[(h-1)*w+x] = int16(v)
		f.heat[(h-2)*w+x] = int16(v)
	}
}

// Render smooths the raw heat horizontally so the noise coheres into tongues.
func (f *fire) Render(t float64, out []uint8) {
	w, h := f.w, f.h
	i := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := int(f.heat[i]) * 2
			if x > 0 {
				v += int(f.heat[i-1])
			} else {
				v += int(f.heat[i])
			}
			if x < w-1 {
				v += int(f.heat[i+1])
			} else {
				v += int(f.heat[i])
			}
			v /= 4
			if v > 255 {
				v = 255
			}
			out[i] = uint8(v)
			i++
		}
	}
}
