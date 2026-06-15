package texture

import (
	"math"

	"raytracer/internal/gpuscene"
	"raytracer/internal/vec"
)

// Art-Nouveau "old-timey" wallpaper: a gilt motif on a dark ground, built from
// an ogee (onion-lattice) trellis with a stylised iris centred in each cell and
// long leaves arcing up from the base — repeated on a regular tile. Three
// colourways (navy, forest green and oxblood) are exposed as separate texture
// ids sharing this generator.
//
// The pattern is evaluated in a 2D tile space derived from world coordinates:
// the horizontal axis follows x+z (so it reads correctly on walls facing either
// X or Z) and the vertical axis follows y. Everything is drawn with smooth
// distance fields so the linework stays crisp at any resolution.

type wallpaperPalette struct {
	bg  vec.V // dark ground
	ink vec.V // gilt motif
}

var wallpaperPalettes = map[int]wallpaperPalette{
	WallpaperNavy:  {bg: vec.New(0.052, 0.073, 0.145), ink: vec.New(0.45, 0.36, 0.18)},
	WallpaperGreen: {bg: vec.New(0.058, 0.110, 0.085), ink: vec.New(0.46, 0.39, 0.22)},
	WallpaperRose:  {bg: vec.New(0.160, 0.060, 0.078), ink: vec.New(0.48, 0.36, 0.19)},
}

const (
	wpTileW = gpuscene.WallpaperTileW // world units per horizontal repeat
	wpTileH = gpuscene.WallpaperTileH // world units per vertical repeat
)

func wallpaper(p, tint vec.V, id int) vec.V {
	pal := wallpaperPalettes[id]

	u := (p.X + p.Z) / wpTileW
	v := p.Y / wpTileH
	g := wallpaperMotif(frac(u), frac(v)) * 0.68

	// Antique variation: faintly mottled ground and an unevenly burnished gilt.
	// Kept deliberately low-contrast so the fine pattern does not sparkle at
	// the renderer's low internal resolution.
	bg := pal.bg.Scale(0.96 + 0.06*(0.5+0.5*fbm(p.X*3.1, p.Y*3.1, p.Z*3.1, 3)))
	ink := pal.ink.Scale(0.88 + 0.12*(0.5+0.5*perlin(p.X*5+11, p.Y*5, p.Z*5)))

	return mix(bg, ink, g).Mul(tint)
}

// wallpaperMotif returns the gilt coverage (0..1) at tile coordinate (fu, fv),
// both in [0,1). The design is mirror-symmetric about the vertical centre line,
// so it is composed in the half-plane mx = |fu-0.5| and mirrors for free.
func wallpaperMotif(fu, fv float64) float64 {
	mx := math.Abs(fu - 0.5)
	ly := fv - 0.5

	g := 0.0

	// Ogee trellis: a sine curve bulging to the tile edges at mid-height and
	// pinching to a point at the top and bottom, where it meets the neighbours.
	sx := 0.5 * math.Sin(math.Pi*fv)
	d := math.Abs(mx - sx)
	g = fmax(g, wpLine(d, 0.013))
	g = fmax(g, 0.55*wpLine(math.Abs(d-0.040), 0.005)) // faint inner rule

	// Node where the ogee points meet (tile top & bottom centre).
	g = fmax(g, wpDot(mx, math.Abs(ly)-0.5, 0.034))

	// Central iris bloom and its arcing leaves, centred in the cell.
	g = fmax(g, wpIris(mx, ly))
	g = fmax(g, wpLeaves(mx, ly))

	if g > 1 {
		g = 1
	}
	return g
}

// wpIris draws a three-petal stylised iris centred at (0, 0) in tile space.
func wpIris(mx, ly float64) float64 {
	g := 0.0
	// Upright central petal (pointed teardrop), outlined.
	g = fmax(g, wpRing(mx, ly, 0.0, 0.085, 0.052, 0.150, 0.18))
	// Outer petals sweeping up and outward (mirrored through mx).
	rx, ry := wpRot(mx-0.012, ly-0.03, -0.62)
	g = fmax(g, wpRing(rx, ry, 0.105, 0.0, 0.120, 0.046, 0.22))
	// Calyx: a small filled wedge under the petals.
	g = fmax(g, wpFill(mx, ly, 0.0, -0.085, 0.030, 0.060))
	// Stem dropping toward the lower node.
	g = fmax(g, wpLine(mx, 0.010)*wpBand(ly, -0.40, -0.10))
	return g
}

// wpLeaves draws a symmetric pair of long iris leaves arcing up from the base.
func wpLeaves(mx, ly float64) float64 {
	rx, ry := wpRot(mx-0.035, ly+0.085, -0.52)
	return wpRing(rx, ry, 0.105, 0.0, 0.230, 0.034, 0.20)
}

// --- small distance-field helpers -------------------------------------------

// wpLine returns ~1 within half-width w of a curve and fades to 0 just past it.
func wpLine(d, w float64) float64 { return 1 - smoothstep(w, w+0.010, d) }

// wpDot returns a filled disc of radius r centred at (cx?, cy?)=(0,0) here.
func wpDot(x, y, r float64) float64 {
	return 1 - smoothstep(r, r+0.012, math.Hypot(x, y))
}

// wpRing returns the outline of an ellipse (centre cx,cy, radii rx,ry); t sets
// the ring thickness in normalised-radius units.
func wpRing(x, y, cx, cy, rx, ry, t float64) float64 {
	q := math.Hypot((x-cx)/rx, (y-cy)/ry)
	return 1 - smoothstep(t, t+0.20, math.Abs(q-1))
}

// wpFill returns the filled interior of an ellipse.
func wpFill(x, y, cx, cy, rx, ry float64) float64 {
	q := math.Hypot((x-cx)/rx, (y-cy)/ry)
	return 1 - smoothstep(0.86, 1.02, q)
}

// wpBand returns ~1 for ly inside [lo,hi] with soft edges (used to clip strokes).
func wpBand(x, lo, hi float64) float64 {
	return smoothstep(lo-0.03, lo+0.03, x) * (1 - smoothstep(hi-0.03, hi+0.03, x))
}

func wpRot(x, y, a float64) (float64, float64) {
	c, s := math.Cos(a), math.Sin(a)
	return x*c - y*s, x*s + y*c
}

func fmax(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
