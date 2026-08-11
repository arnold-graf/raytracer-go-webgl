package scene

import (
	"math"

	"raytracer/internal/vec"
)

// CampfireLights is the number of flickering point lights a campfire emits.
const CampfireLights = 3

// Campfire is an animated light cluster: CampfireLights warm point lights whose
// intensity and position wobble over time to cast flickering shadows. It emits
// no geometry of its own -- pair it with a few emissive embers/logs for a
// visible source. The per-light variation is derived deterministically from the
// animation clock, so every render worker computes the same state within a
// frame (no shared mutable state, no per-frame races).
type Campfire struct {
	Center     vec.V   // fire core position; sub-lights sit just above it
	Color      vec.V   // base per-light warm color/intensity (HDR)
	Brightness float64 // overall intensity multiplier on Color (1 = as authored)
	Range      float64 // cull / falloff distance (0 = automatic reach)
	Jitter     float64 // position wobble radius in world units
	Flicker    float64 // intensity wobble amount in [0,1]
	Speed      float64 // flicker speed multiplier (1 = default)
	Seed       float64 // phase offset so multiple fires look different
	Lights     int     // sub-light count (renderer currently supports 3)
	Flame      bool    // procedural volumetric flame at the core
	// FlameEmber/Mid/Tip/Ash are linear HDR colors for particle life stages.
	// Unset in TOML defaults to DefaultFlameEmber etc.
	FlameEmber vec.V
	FlameMid   vec.V
	FlameTip   vec.V
	FlameAsh   vec.V
	// FlameScale shrinks the volumetric flame and sub-light spread (1 = default
	// campfire size). Use ~0.25–0.35 for a handheld torch.
	FlameScale float64
}

// Default flame particle colors (linear HDR). Override per [[light_flickering]].
var (
	DefaultFlameEmber = vec.V{X: 2.2, Y: 0.32, Z: 0.04}
	DefaultFlameMid   = vec.V{X: 2.0, Y: 0.85, Z: 0.08}
	DefaultFlameTip   = vec.V{X: 1.6, Y: 1.2, Z: 0.14}
	DefaultFlameAsh   = vec.V{X: 0.9, Y: 0.32, Z: 0.12}
)

// FlamePalette returns this campfire's flame colors, filling defaults for
// any channel left unset (all-zero vec).
func (f Campfire) FlamePalette() (ember, mid, tip, ash vec.V) {
	ember, mid, tip, ash = DefaultFlameEmber, DefaultFlameMid, DefaultFlameTip, DefaultFlameAsh
	if f.FlameEmber != (vec.V{}) {
		ember = f.FlameEmber
	}
	if f.FlameMid != (vec.V{}) {
		mid = f.FlameMid
	}
	if f.FlameTip != (vec.V{}) {
		tip = f.FlameTip
	}
	if f.FlameAsh != (vec.V{}) {
		ash = f.FlameAsh
	}
	return ember, mid, tip, ash
}

// campfireTint warms each sub-light differently: the low ember is red-orange,
// the body orange, the tip yellow -- giving the cluster depth. The X (red)
// channel is 1.0 for all three so Color's red sets the overall hue ceiling.
var campfireTint = [CampfireLights]vec.V{
	{X: 1.00, Y: 0.60, Z: 0.28}, // ember (low, red-orange)
	{X: 1.00, Y: 0.80, Z: 0.46}, // body (mid, orange)
	{X: 1.00, Y: 0.92, Z: 0.66}, // flame tip (high, yellow)
}

// campfireBase offsets each sub-light around the core: a small rising triangle
// (embers low and spread, flame tip high and central).
var campfireBase = [CampfireLights]vec.V{
	{X: 0.22, Y: 0.06, Z: 0.14},
	{X: -0.24, Y: 0.26, Z: -0.12},
	{X: 0.03, Y: 0.52, Z: 0.16},
}

// LightAt returns the current world position and HDR color of sub-light j at
// animation time t (seconds). The flicker is a sum of incommensurate sines, so
// it reads as smooth quasi-random fire motion rather than a periodic wobble.
func (f *Campfire) LightAt(j int, t float64) (pos, color vec.V) {
	sp := f.Speed
	if sp == 0 {
		sp = 1
	}
	bright := f.Brightness
	if bright == 0 {
		bright = 1
	}
	ts := t * sp
	ph := f.Seed + float64(j)*1.7

	fl := 0.6*math.Sin(ts*7.0+ph) + 0.3*math.Sin(ts*13.0+ph*2.1) + 0.1*math.Sin(ts*23.0+ph*3.7)
	intensity := bright * (1 + f.Flicker*fl)
	if intensity < 0.15*bright {
		intensity = 0.15 * bright
	}

	jx := f.Jitter * (0.7*math.Sin(ts*9.0+ph*1.3) + 0.3*math.Sin(ts*17.0+ph*2.7))
	jz := f.Jitter * (0.7*math.Sin(ts*11.0+ph*1.9) + 0.3*math.Sin(ts*19.0+ph*0.7))
	jy := f.Jitter * (0.4 + 0.4*math.Sin(ts*15.0+ph)) // mostly upward bob

	base := campfireBase[j]
	pos = vec.V{
		X: f.Center.X + base.X + jx,
		Y: f.Center.Y + base.Y + jy,
		Z: f.Center.Z + base.Z + jz,
	}
	color = f.Color.Mul(campfireTint[j]).Scale(intensity)
	return pos, color
}

// PeakChannel returns an upper bound on any sub-light's per-channel intensity,
// used to size the light-culling distance conservatively (so flicker peaks are
// never clipped by the cull radius).
func (f *Campfire) PeakChannel() float64 {
	bright := f.Brightness
	if bright == 0 {
		bright = 1
	}
	m := f.Color.X
	if f.Color.Y > m {
		m = f.Color.Y
	}
	if f.Color.Z > m {
		m = f.Color.Z
	}
	return m * bright * (1 + f.Flicker) // brightest the flicker reaches; tints are <= 1
}
