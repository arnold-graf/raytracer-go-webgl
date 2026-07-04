package util

import "raytracer/internal/vec"

// DefaultPenetration is the overlap depth required before two AABBs count as
// penetrating. Face-touch and sub-centimetre grazing contacts are ignored.
const DefaultPenetration = 0.04

// Overlap reports whether two axis-aligned boxes overlap by more than penetration.
func Overlap(pmin, pmax, omin, omax vec.V, penetration float64) bool {
	if pmin.X >= omax.X-penetration || pmax.X <= omin.X+penetration {
		return false
	}
	if pmin.Y >= omax.Y-penetration || pmax.Y <= omin.Y+penetration {
		return false
	}
	if pmin.Z >= omax.Z-penetration || pmax.Z <= omin.Z+penetration {
		return false
	}
	return true
}
