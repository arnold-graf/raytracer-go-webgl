package audio

import (
	"math"
	"math/rand"

	"raytracer/internal/vec"
)

// RayCast returns the distance to the nearest solid surface along a ray from
// origin in the unit direction dir, capped at maxT.
type RayCast func(origin, dir vec.V, maxT float64) float64

const (
	// detourTries is how many fresh directions each side (source and listener)
	// probes per update. The best path is kept between updates, so the search
	// spreads over frames instead of sweeping the whole sphere every tick.
	detourTries = 6
	// detourReach caps how far from an endpoint a detour point may sit.
	detourReach = 12.0
	// detourGainLen is the extra path length (m) that halves a detour's
	// loudness; detourBrightLen does the same for its high frequencies. Sound
	// bends around a staircase with little loss but comes muffled through the
	// far end of a hallway.
	detourGainLen   = 2.0
	detourBrightLen = 1.0
	// blockedGain and blockedBright are what's left when no path is found: a
	// dull thump through the walls rather than silence.
	blockedGain   = 0.1
	blockedBright = 0.0
	// endSlack treats a hit this close to the far endpoint as a clear path, so
	// geometry around the emitter itself (the logs of a campfire) doesn't count.
	endSlack = 0.3
)

// pathProbe finds how sound gets from an emitter to the listener. A clear line
// of sight is heard unchanged. Otherwise it looks for a one-bounce detour, a
// point in open space both ends can see, and the longer that detour is than
// the direct path the quieter and duller the sound. It remembers its best
// detour between updates, so a narrow gap found once stays found.
type pathProbe struct {
	best    vec.V
	hasBest bool
}

// update returns the path's gain and brightness (1 = clear, 0 = fully muffled)
// from listener to source.
func (p *pathProbe) update(listener, source vec.V, cast RayCast, rng *rand.Rand) (gain, bright float64) {
	direct := source.Sub(listener).Len()
	if visible(listener, source, cast) {
		p.hasBest = false
		return 1, 1
	}

	bestExcess := math.Inf(1)
	try := func(pt vec.V) {
		ex := pt.Sub(source).Len() + pt.Sub(listener).Len() - direct
		if ex < bestExcess && visible(pt, source, cast) && visible(pt, listener, cast) {
			bestExcess = ex
			p.best = pt
		}
	}
	if p.hasBest {
		try(p.best)
	}
	// Probe out from both ends: a point near the source catches sound spilling
	// over whatever hides it, a point near the listener catches it coming round
	// the corner the listener is standing behind.
	for _, from := range [2]vec.V{source, listener} {
		for i := 0; i < detourTries; i++ {
			dir := randomDir(rng)
			reach := cast(from, dir, detourReach)
			// Stay off the surface the ray hit; a random depth along the free
			// stretch keeps successive tries from resampling the same point.
			try(from.Add(dir.Scale(reach * (0.2 + 0.7*rng.Float64()))))
		}
	}

	if math.IsInf(bestExcess, 1) {
		p.hasBest = false
		return blockedGain, blockedBright
	}
	p.hasBest = true
	return 1 / (1 + bestExcess/detourGainLen), 1 / (1 + bestExcess/detourBrightLen)
}

// visible reports whether the segment a→b is free of geometry.
func visible(a, b vec.V, cast RayCast) bool {
	d := b.Sub(a)
	l := d.Len()
	if l < endSlack {
		return true
	}
	return cast(a, d.Scale(1/l), l) >= l-endSlack
}

// randomDir returns a uniformly distributed unit vector.
func randomDir(rng *rand.Rand) vec.V {
	y := rng.Float64()*2 - 1
	phi := rng.Float64() * 2 * math.Pi
	r := math.Sqrt(1 - y*y)
	return vec.V{X: r * math.Cos(phi), Y: y, Z: r * math.Sin(phi)}
}
