package scene

import (
	"raytracer/internal/util"
	"raytracer/internal/vec"
)

// StaticHit is one overlap between a probe AABB and static scene geometry.
// Box indices are non-negative; cylinder indices are encoded as -(i+1).
type StaticHit struct {
	Box      int // >= 0 when a box was hit
	Cylinder int // >= 0 when a cylinder was hit
}

// StaticHits reports static boxes and cylinders whose world bounds overlap the
// probe AABB. skipBox excludes box indices (e.g. moving parts of the same body).
func (s *Scene) StaticHits(pmin, pmax vec.V, skipBox func(int) bool) []StaticHit {
	if s == nil {
		return nil
	}
	var hits []StaticHit
	for i := range s.Boxes {
		if skipBox != nil && skipBox(i) {
			continue
		}
		b := &s.Boxes[i]
		if len(b.Holes) > 0 {
			if b.OverlapsSolid(pmin, pmax, util.DefaultPenetration) {
				hits = append(hits, StaticHit{Box: i})
			}
			continue
		}
		omn, omx := b.WorldBounds()
		if !util.Overlap(pmin, pmax, omn, omx, util.DefaultPenetration) {
			continue
		}
		hits = append(hits, StaticHit{Box: i})
	}
	for i := range s.Cylinders {
		if s.IsDynamicCylinder(i) {
			continue
		}
		omn, omx := s.Cylinders[i].WorldBounds()
		if !util.Overlap(pmin, pmax, omn, omx, util.DefaultPenetration) {
			continue
		}
		hits = append(hits, StaticHit{Cylinder: i})
	}
	return hits
}

// BoxStaticHits returns box indices overlapping the probe. Convenience wrapper
// for callers that only care about boxes.
func (s *Scene) BoxStaticHits(pmin, pmax vec.V, skipBox func(int) bool) []int {
	hits := s.StaticHits(pmin, pmax, skipBox)
	var out []int
	for _, h := range hits {
		if h.Box >= 0 {
			out = append(out, h.Box)
		}
	}
	return out
}

// StaticHitSet converts hits to a set keyed by box index or -(cylinder+1).
func StaticHitSet(hits []StaticHit) map[int]bool {
	set := make(map[int]bool, len(hits))
	for _, h := range hits {
		if h.Box >= 0 {
			set[h.Box] = true
		} else if h.Cylinder >= 0 {
			set[-(h.Cylinder + 1)] = true
		}
	}
	return set
}

// ProbeBoxStaticHits returns static hits for a box at its current world pose.
func (s *Scene) ProbeBoxStaticHits(boxIdx int, skipBox func(int) bool) []StaticHit {
	if s == nil || boxIdx < 0 || boxIdx >= len(s.Boxes) {
		return nil
	}
	pmin, pmax := s.Boxes[boxIdx].WorldBounds()
	skip := func(i int) bool {
		if i == boxIdx {
			return true
		}
		return skipBox != nil && skipBox(i)
	}
	return s.StaticHits(pmin, pmax, skip)
}

// PlayerOverlapsBox reports whether the player capsule overlaps a box at its
// current world pose.
func (s *Scene) PlayerOverlapsBox(boxIdx int, feetY, headY float64, pos vec.V, radius, step float64) bool {
	if s == nil || boxIdx < 0 || boxIdx >= len(s.Boxes) {
		return false
	}
	return s.Boxes[boxIdx].PlayerOverlapsBox(pos.X, pos.Z, feetY, headY, radius, step)
}
