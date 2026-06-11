// Package texture provides procedural, world-space surface textures evaluated
// at shade time. Every texture is a pure function of the hit point, so there
// are no texture images to store or UV-map: wood, brick, stone, cement and
// marble are all generated from layered Perlin noise.
//
// Textures are referenced by an integer id stored on scene.Surface; the engine
// calls Eval(id, hitPoint, baseAlbedo) for each shaded point. id 0 (None) is a
// no-op that returns baseAlbedo unchanged, so untextured surfaces are free.
package texture

import (
	"math"

	"raytracer/internal/vec"
)

// Texture ids. None must be 0 so the zero value means "untextured".
const (
	None = iota
	Wood
	Brick
	Stone
	Cement
	Marble
	Grass
	Dirt
	Snow
)

var byName = map[string]int{
	"none":   None,
	"wood":   Wood,
	"brick":  Brick,
	"stone":  Stone,
	"cement": Cement,
	"marble": Marble,
	"grass":  Grass,
	"dirt":   Dirt,
	"snow":   Snow,
}

// ID resolves a texture name to its id. Ok is false for unknown names.
func ID(name string) (int, bool) {
	id, ok := byName[name]
	return id, ok
}

// Eval returns the albedo at world point p for the given texture id. base is
// the surface's configured albedo, used as a tint so a texture can be recolored
// per-surface. For None it is returned unchanged.
func Eval(id int, p, base vec.V) vec.V {
	switch id {
	case Wood:
		return wood(p, base)
	case Brick:
		return brick(p, base)
	case Stone:
		return stone(p, base)
	case Cement:
		return cement(p, base)
	case Marble:
		return marble(p, base)
	case Grass:
		return grass(p, base)
	case Dirt:
		return dirt(p, base)
	case Snow:
		return snow(p, base)
	default:
		return base
	}
}

func mix(a, b vec.V, t float64) vec.V { return a.Add(b.Sub(a).Scale(t)) }

func smoothstep(e0, e1, x float64) float64 {
	t := (x - e0) / (e1 - e0)
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return t * t * (3 - 2*t)
}

func frac(x float64) float64 { return x - math.Floor(x) }

// cellRand returns a stable pseudo-random value in [0,1) for a grid cell,
// decorrelated by seed. Used to give each brick its own identity.
func cellRand(c, r, seed float64) float64 {
	s := math.Sin(c*127.1+r*311.7+seed*74.69) * 43758.5453
	return s - math.Floor(s)
}

// wood: concentric growth rings around the X axis, warped by turbulence so the
// grain wobbles. Grain runs along X (boards lying along X look natural).
func wood(p, tint vec.V) vec.V {
	const ringFreq = 2.2
	dist := math.Hypot(p.Y, p.Z)
	g := dist*ringFreq + 0.6*turbulence(p.X*0.6, p.Y*1.5, p.Z*1.5, 4)
	rings := 0.5 + 0.5*math.Sin(g*2*math.Pi*math.Pi)
	light := vec.New(0.58, 0.38, 0.19)
	dark := vec.New(0.33, 0.19, 0.09)
	// A little fine streaking along the grain.
	streak := 0.85 + 0.15*perlin(p.X*12, p.Y*2, p.Z*2)
	return mix(dark, light, rings).Scale(streak).Mul(tint)
}

// brickPalette is a set of dark, earthy fired-clay tones. Each brick draws one
// at random and then jitters it, so the wall reads as many individual bricks.
var brickPalette = [...]vec.V{
	{X: 0.28, Y: 0.10, Z: 0.07}, // standard red
	{X: 0.22, Y: 0.08, Z: 0.06}, // deep red-brown
	{X: 0.34, Y: 0.15, Z: 0.09}, // terracotta
	{X: 0.18, Y: 0.09, Z: 0.07}, // dark brown
	{X: 0.13, Y: 0.07, Z: 0.06}, // sooty / charred
	{X: 0.25, Y: 0.13, Z: 0.10}, // tan-brown
}

// brick: staggered courses with mortar gaps. Every brick gets, from a stable
// per-cell hash: a palette color, brightness/desaturation jitter, a "decay"
// amount that drives weathering stains, surface grain, cracks, and eroded
// (irregular, widened) edges. Pattern lies in the X (horizontal) / Y (vertical)
// plane, suitable for walls.
func brick(p, tint vec.V) vec.V {
	const (
		brickW = 0.5
		brickH = 0.22
		mortar = 0.05
	)
	row := math.Floor(p.Y / brickH)
	x := p.X
	if int(row)&1 == 1 {
		x += brickW * 0.5 // offset every other course
	}
	col := math.Floor(x / brickW)

	// Per-brick stable randoms, each decorrelated by seed.
	pick := cellRand(col, row, 1)
	bright := cellRand(col, row, 2)
	desat := cellRand(col, row, 3)
	decay := cellRand(col, row, 4)
	decay *= decay // bias toward fewer heavily-decayed bricks

	// Base color: palette pick + per-brick brightness jitter (only ever darkens
	// below the palette value, never brightens it) and per-channel hue jitter.
	base := brickPalette[int(pick*float64(len(brickPalette)))]
	base = base.Scale(0.5 + 0.45*bright)
	base.X *= 0.88 + 0.24*cellRand(col, row, 5)
	base.Y *= 0.85 + 0.30*cellRand(col, row, 6)
	base.Z *= 0.85 + 0.30*cellRand(col, row, 7)
	g := (base.X + base.Y + base.Z) / 3
	base = mix(base, vec.New(g, g, g), 0.5*desat*decay)

	// Surface texture: two scales of mottling plus fine grain, then low-frequency
	// blotchy weathering that darkens decayed bricks unevenly.
	mottle := 0.78 + 0.22*fbm(p.X*9+col*4, p.Y*9+row*4, p.Z*9, 3)
	grain := 0.78 + 0.22*fbm(p.X*40, p.Y*40, p.Z*40, 4)
	stain := fbm(p.X*5+col*7.3, p.Y*5+row*3.1, p.Z*5, 4) // ~[-1,1]
	weather := 1 - decay*0.55*smoothstep(-0.3, 0.6, stain)
	face := base.Scale(mottle * grain * weather * (1 - 0.3*decay))

	// Cracks: thin dark fissures, far more prominent on decayed bricks.
	crack := smoothstep(0.5, 0.72, turbulence(p.X*9+col, p.Y*9+row, p.Z*9, 4))
	face = face.Scale(1 - 0.7*crack*(0.25+0.75*decay))

	// Mortar: dark, dirty gray, and noisy.
	mortarCol := vec.New(0.20, 0.19, 0.17).Scale(0.75 + 0.4*fbm(p.X*7, p.Y*7, p.Z*7, 3))

	// Brick face mask with eroded edges: decayed bricks have wider, irregular
	// mortar gaps as their edges crumble away.
	mx := mortar / brickW
	my := mortar / brickH
	erode := 1 + 3*decay
	ex := mx * erode * (0.8 + 0.4*perlin(p.X*15, p.Y*15, p.Z*15))
	ey := my * erode * (0.8 + 0.4*perlin(p.X*15+5, p.Y*15+5, p.Z*15))
	fx := frac(x / brickW)
	fy := frac(p.Y / brickH)
	mask := smoothstep(0, ex, fx) * smoothstep(0, ex, 1-fx) *
		smoothstep(0, ey, fy) * smoothstep(0, ey, 1-fy)

	return mix(mortarCol, face, mask).Mul(tint)
}

// stone: mottled, multi-scale fBm with subtle cell-edge darkening for a rough
// natural stone / granite look.
func stone(p, tint vec.V) vec.V {
	n := 0.5 + 0.5*fbm(p.X*1.5, p.Y*1.5, p.Z*1.5, 5)
	light := vec.New(0.60, 0.58, 0.54)
	dark := vec.New(0.34, 0.33, 0.31)
	c := mix(dark, light, n)
	// Darken thin seams where high-frequency noise crosses zero.
	seam := math.Abs(perlin(p.X*6, p.Y*6, p.Z*6))
	c = c.Scale(0.7 + 0.3*smoothstep(0.02, 0.12, seam))
	return c.Mul(tint)
}

// cement: low-contrast gray with fine speckle; a calm, flat surface.
func cement(p, tint vec.V) vec.V {
	n := fbm(p.X*4, p.Y*4, p.Z*4, 4)
	speck := 0.5 + 0.5*perlin(p.X*40, p.Y*40, p.Z*40)
	g := 0.62 + 0.06*n + 0.03*(speck-0.5)
	return vec.New(g, g, g*0.99).Mul(tint)
}

// marble: classic turbulence-warped sine veining.
func marble(p, tint vec.V) vec.V {
	t := turbulence(p.X*1.2, p.Y*1.2, p.Z*1.2, 5)
	veins := 0.5 + 0.5*math.Sin((p.X+p.Z)*1.5+6.0*t)
	base := vec.New(0.85, 0.85, 0.88)
	vein := vec.New(0.18, 0.18, 0.22)
	return mix(vein, base, math.Pow(veins, 0.6)).Mul(tint)
}

// grass: mottled lush/dry green with fine blade speckle. Tuned for large
// world-space areas (terrain), so it uses low base frequencies.
func grass(p, tint vec.V) vec.V {
	patch := fbm(p.X*0.6, p.Y*0.6, p.Z*0.6, 3) // large lush/dry patches
	blade := 0.5 + 0.5*perlin(p.X*9, p.Y*9, p.Z*9)
	lush := vec.New(0.12, 0.30, 0.08)
	dry := vec.New(0.36, 0.34, 0.13)
	c := mix(lush, dry, smoothstep(-0.25, 0.5, patch))
	c = c.Scale(0.78 + 0.22*blade)
	return c.Mul(tint)
}

// dirt: earthy brown with clumpy noise.
func dirt(p, tint vec.V) vec.V {
	n := 0.5 + 0.5*fbm(p.X*3, p.Y*3, p.Z*3, 4)
	base := vec.New(0.26, 0.17, 0.10)
	dark := vec.New(0.15, 0.10, 0.06)
	return mix(dark, base, n).Mul(tint)
}

// snow: bright, faintly blue, with subtle drift mottling and a sparse sparkle.
func snow(p, tint vec.V) vec.V {
	drift := 0.5 + 0.5*fbm(p.X*1.5, p.Y*1.5, p.Z*1.5, 3)
	sparkle := perlin(p.X*55, p.Y*55, p.Z*55)
	v := 0.86 + 0.10*drift
	c := vec.New(v*0.97, v*0.99, math.Min(1, v*1.04))
	if sparkle > 0.85 {
		c = c.Scale(1.12) // occasional glint
	}
	return c.Mul(tint)
}
