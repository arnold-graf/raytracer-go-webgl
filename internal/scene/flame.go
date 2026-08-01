package scene

import (
	"math"

	"raytracer/internal/vec"
)

// FlameParticlesPerCampfire is the live particle pool size for each campfire.
const FlameParticlesPerCampfire = 32

// FlameSpawnRate is how many particles per second each campfire emits on average.
const FlameSpawnRate = 24.0

// flameSpawnYOffset places the visible flame below the light_flickering center so
// particles sit in the logs while the light cluster stays higher for shadows.
const flameSpawnYOffset = -0.40

// flameSlot is one simulated ember in the pool.
type flameSlot struct {
	active  bool
	fireIdx int
	pos     vec.V
	vel     vec.V
	life    float64 // 0 at birth, 1 at death
	lifeMax float64
	seed    float64
	ember   vec.V
	mid     vec.V
	tip     vec.V
	ash     vec.V
}

// FlameSystem simulates rising fire particles for scene campfires. Positions are
// advanced on the CPU each frame and ray-traced as small emissive spheres.
type FlameSystem struct {
	slots       []flameSlot
	spawnTimers []float64
	nFires      int
}

// Reset sizes the pool for the current campfire list.
func (fs *FlameSystem) Reset(fires []Campfire) {
	fs.nFires = len(fires)
	fs.spawnTimers = make([]float64, fs.nFires)
	capSlots := fs.nFires * FlameParticlesPerCampfire
	if cap(fs.slots) < capSlots {
		fs.slots = make([]flameSlot, capSlots)
	} else {
		fs.slots = fs.slots[:capSlots]
		for i := range fs.slots {
			fs.slots[i] = flameSlot{}
		}
	}
}

// SimulateTo advances the system deterministically to time t (for still previews).
func (fs *FlameSystem) SimulateTo(fires []Campfire, t float64) {
	fs.Reset(fires)
	const dt = 1.0 / 60.0
	elapsed := 0.0
	for elapsed < t {
		step := dt
		if elapsed+step > t {
			step = t - elapsed
		}
		fs.Update(fires, elapsed, step)
		elapsed += step
	}
}

// Update advances particles and spawns new ones.
func (fs *FlameSystem) Update(fires []Campfire, t, dt float64) {
	if len(fires) == 0 {
		return
	}
	if fs.nFires != len(fires) || len(fs.spawnTimers) != len(fires) {
		fs.Reset(fires)
	}
	for fi, fire := range fires {
		fs.spawnTimers[fi] += FlameSpawnRate * dt
		for fs.spawnTimers[fi] >= 1 {
			fs.spawnTimers[fi]--
			fs.spawnOne(fi, fire, t)
		}
	}
	for i := range fs.slots {
		s := &fs.slots[i]
		if !s.active {
			continue
		}
		s.life += dt / s.lifeMax
		if s.life >= 1 {
			s.active = false
			continue
		}
		phase := t*9.0 + s.seed
		sway := vec.V{
			X: 0.14 * math.Sin(phase*1.31),
			Z: 0.14 * math.Sin(phase*1.73 + 0.8),
		}
		s.vel.Y += 1.4 * dt
		s.vel = s.vel.Add(sway.Scale(0.35 * dt))
		s.pos = s.pos.Add(s.vel.Scale(dt))
	}
}

func (fs *FlameSystem) spawnOne(fi int, fire Campfire, t float64) {
	base := fi * FlameParticlesPerCampfire
	end := base + FlameParticlesPerCampfire
	for i := base; i < end && i < len(fs.slots); i++ {
		if !fs.slots[i].active {
			fs.initSlot(&fs.slots[i], fi, fire, t, float64(i))
			return
		}
	}
}

func (fs *FlameSystem) initSlot(s *flameSlot, fi int, fire Campfire, t, seed float64) {
	h1 := flameHash(seed, t*0.17)
	h2 := flameHash(seed, t*0.31+3.1)
	h3 := flameHash(seed, t*0.43+7.7)
	h4 := flameHash(seed, t*0.59+11.3)
	h5 := flameHash(seed, t*0.71+19.1)

	s.active = true
	s.fireIdx = fi
	s.seed = seed
	s.life = 0
	s.lifeMax = 0.55 + 0.55*h1
	s.ember, s.mid, s.tip, s.ash = fire.flamePalette()

	s.pos = vec.V{
		X: fire.Center.X + (h2-0.5)*0.32,
		Y: fire.Center.Y + flameSpawnYOffset + h3*0.14,
		Z: fire.Center.Z + (h4-0.5)*0.32,
	}
	s.vel = vec.V{
		X: (h2 - 0.5) * 0.35,
		Y: 0.95 + h1*1.1,
		Z: (h4 - 0.5) * 0.35,
	}
	_ = h5
}

// ActiveParticles returns live particle state for GPU packing.
func (fs *FlameSystem) ActiveParticles() []FlameParticle {
	if len(fs.slots) == 0 {
		return nil
	}
	out := make([]FlameParticle, 0, 32)
	for i := range fs.slots {
		s := &fs.slots[i]
		if !s.active {
			continue
		}
		radius, color := flameVisual(s.life, s.ember, s.mid, s.tip, s.ash)
		if radius <= 0 {
			continue
		}
		out = append(out, FlameParticle{
			Pos:    s.pos,
			Radius: radius,
			Color:  color,
		})
	}
	return out
}

// FlameParticle is one emissive sphere uploaded for ray tracing.
type FlameParticle struct {
	Pos    vec.V
	Radius float64
	Color  vec.V
}

func flameVisual(life float64, ember, mid, tip, ash vec.V) (radius float64, color vec.V) {
	if life >= 1 {
		return 0, vec.V{}
	}
	// Bell-shaped size: small ember, swell mid-flight, shrink as it fades.
	size := math.Sin(life * math.Pi)
	radius = 0.05 + 0.16*size*(1-life*0.2)

	var base vec.V
	switch {
	case life < 0.35:
		base = vecLerp(ember, mid, life/0.35)
	case life < 0.75:
		base = vecLerp(mid, tip, (life-0.35)/0.4)
	default:
		base = vecLerp(tip, ash, (life-0.75)/0.25)
	}

	// Moderate brightness — overlapping spheres add, so keep per-particle HDR low.
	fade := (1 - life) * (1 - life*0.4)
	bright := 0.35 + 0.35*size
	return radius, base.Scale(bright * fade)
}

func flameHash(seed, t float64) float64 {
	x := math.Sin(seed*12.9898+t*78.233) * 43758.5453
	return x - math.Floor(x)
}

func vecLerp(a, b vec.V, t float64) vec.V {
	return vec.V{
		X: a.X + (b.X-a.X)*t,
		Y: a.Y + (b.Y-a.Y)*t,
		Z: a.Z + (b.Z-a.Z)*t,
	}
}
