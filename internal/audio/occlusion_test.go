package audio

import (
	"math"
	"math/rand"
	"testing"

	"raytracer/internal/vec"
)

type box struct{ min, max vec.V }

// boxCast is a RayCast over axis-aligned boxes, standing in for the scene probe.
func boxCast(boxes ...box) RayCast {
	return func(o, d vec.V, maxT float64) float64 {
		best := maxT
		for _, b := range boxes {
			tmin, tmax := 0.0, best
			for _, ax := range [3][3]float64{{o.X, d.X, 0}, {o.Y, d.Y, 1}, {o.Z, d.Z, 2}} {
				lo, hi := b.min.X, b.max.X
				if ax[2] == 1 {
					lo, hi = b.min.Y, b.max.Y
				} else if ax[2] == 2 {
					lo, hi = b.min.Z, b.max.Z
				}
				if math.Abs(ax[1]) < 1e-12 {
					if ax[0] < lo || ax[0] > hi {
						tmin, tmax = 1, 0
					}
					continue
				}
				t0, t1 := (lo-ax[0])/ax[1], (hi-ax[0])/ax[1]
				if t0 > t1 {
					t0, t1 = t1, t0
				}
				tmin, tmax = math.Max(tmin, t0), math.Min(tmax, t1)
			}
			if tmin <= tmax {
				best = tmin
			}
		}
		return best
	}
}

// settle runs the probe for a second of 60 Hz updates and returns the result.
func settle(listener, source vec.V, cast RayCast) (gain, bright float64) {
	var p pathProbe
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 60; i++ {
		gain, bright = p.update(listener, source, cast, rng)
	}
	return gain, bright
}

var (
	listener = vec.V{X: 0, Y: 1.6, Z: 0}
	source   = vec.V{X: 0, Y: 0.5, Z: 8}
)

func TestPathClearLineOfSight(t *testing.T) {
	g, b := settle(listener, source, boxCast())
	if g != 1 || b != 1 {
		t.Fatalf("clear path: gain=%.2f bright=%.2f, want 1/1", g, b)
	}
}

// Room walls around the scene give the probe rays something to hit, as a real
// interior would, without opening any path between the two ends.
var room = []box{
	{vec.V{X: -6, Y: -1, Z: -3}, vec.V{X: 6, Y: 0, Z: 11}}, // floor
	{vec.V{X: -6, Y: 4, Z: -3}, vec.V{X: 6, Y: 5, Z: 11}},  // ceiling
}

func TestPathAroundSmallObstacle(t *testing.T) {
	// A staircase-sized block right in the line of sight.
	stairs := box{vec.V{X: -1, Y: 0, Z: 3}, vec.V{X: 1, Y: 2.2, Z: 4}}
	g, b := settle(listener, source, boxCast(append(room, stairs)...))
	if g < 0.6 {
		t.Errorf("small obstacle: gain=%.2f, sound should bend round it", g)
	}
	if b >= 1 {
		t.Errorf("small obstacle: bright=%.2f, a detour should dull it a little", b)
	}
}

func TestPathSealedWall(t *testing.T) {
	// Wider than detourReach both ways, so there is no way round its ends.
	wall := box{vec.V{X: -40, Y: -1, Z: 4}, vec.V{X: 40, Y: 5, Z: 4.3}}
	g, b := settle(listener, source, boxCast(append(room, wall)...))
	if g != blockedGain || b != blockedBright {
		t.Fatalf("sealed wall: gain=%.2f bright=%.2f, want blocked %.2f/%.2f", g, b, blockedGain, blockedBright)
	}
}

func TestPathThroughDistantDoorway(t *testing.T) {
	// The same wall with a doorway well off to the side: the sound gets through,
	// but quieter and duller than round a small obstacle.
	walls := append(room,
		box{vec.V{X: -6, Y: 0, Z: 4}, vec.V{X: 3, Y: 4, Z: 4.3}},
		box{vec.V{X: 4, Y: 0, Z: 4}, vec.V{X: 6, Y: 4, Z: 4.3}},
	)
	g, b := settle(listener, source, boxCast(walls...))
	stairs := box{vec.V{X: -1, Y: 0, Z: 3}, vec.V{X: 1, Y: 2.2, Z: 4}}
	sg, sb := settle(listener, source, boxCast(append(room, stairs)...))
	if g <= blockedGain || b <= blockedBright {
		t.Errorf("doorway: gain=%.2f bright=%.2f, want a path through the door", g, b)
	}
	if g >= sg || b >= sb {
		t.Errorf("doorway gain=%.2f bright=%.2f should be below small obstacle %.2f/%.2f", g, b, sg, sb)
	}
}

func TestLowpassBypassWhenClear(t *testing.T) {
	if a := lowpassAlpha(1); a != 1 {
		t.Fatalf("clear alpha=%v, want bypass", a)
	}
	if lowpassAlpha(0) >= lowpassAlpha(0.5) {
		t.Fatalf("muffled cutoff should be below half-bright cutoff")
	}
}
