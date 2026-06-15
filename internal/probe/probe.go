// Package probe provides non-rendering geometry queries over a static scene: a
// nearest-surface ray cast and a baked ambient-occlusion volume.
//
// These are the parts of the old CPU ray tracer that survive the move to a
// WebGPU-only renderer because they are not rendering. The ray cast drives the
// audio engine's room acoustics (reverb and ambient-source occlusion), and the
// AO volume is baked on the CPU once per scene and uploaded to the GPU, which
// samples the exact same ambient cube the shader expects.
//
// A Probe owns a bounding-volume hierarchy over the scene's finite primitives,
// so both the per-frame acoustic casts and the one-off AO bake are O(log N).
package probe

import (
	"raytracer/internal/bvh"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// Probe answers ray-vs-scene distance queries against a static scene.
type Probe struct {
	s     *scene.Scene
	accel *bvh.BVH
}

// New builds a probe for s, constructing a BVH over its finite primitives once.
func New(s *scene.Scene) *Probe {
	return &Probe{s: s, accel: bvh.New(s)}
}

// Distance returns the distance to the nearest solid surface (finite primitives
// via the BVH, plus planes) along a ray from origin in dir, capped at maxT.
//
// It is the acoustic ray query used to estimate room enclosure for reverb: rays
// that hit nearby walls in many directions imply an enclosed, reverberant space,
// while rays that fly off to maxT imply the open outdoors. Terrain is
// intentionally excluded (it is the ground, not a reflecting wall), which
// conveniently makes the outdoors read as dry.
func (p *Probe) Distance(origin, dir vec.V, maxT float64) float64 {
	return p.nearest(vec.Ray{Origin: origin, Dir: dir.Normalize()}, maxT)
}

// nearest returns the distance to the closest primitive along r, capped at maxT
// (anything farther is reported as maxT). Terrain is excluded: it is smooth and
// convex enough that self-occlusion is negligible, and marching it for every
// query is expensive. Objects above still occlude normally.
func (p *Probe) nearest(r vec.Ray, maxT float64) float64 {
	tmin := p.accel.NearestDist(r, maxT)
	for i := range p.s.Planes {
		if t := p.s.Planes[i].Intersect(r); t < tmin {
			tmin = t
		}
	}
	return tmin
}
