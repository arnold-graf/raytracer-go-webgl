package scene

import "raytracer/internal/vec"

// VPLRegion is an authored budget for virtual point lights over one box of the
// world: "spend this many bounce lights in here, ranked against each other".
//
// It exists because the VPL budget is otherwise global. Clusters are ranked by
// power across the whole scene, so on a large level the strongest bounces win
// every slot wherever they happen to be — measured on the night villa, eleven of
// twenty-four slots went to a mountain campfire 160 m away and 80 m up, and the
// apartment got none at any count. Ranking inside a region is the per-region cut
// docs/vpl.md argues for: a room's own bounces compete only with each other, so
// a small budget lands where the author says it matters.
//
// **A region selects by where a photon landed, not by which emitter sent it.**
// That is the whole point and it is worth stating twice: a campfire standing in
// the garden still lights the hall it shines into, because the bounce it leaves
// on the hall floor is inside the hall's box even though the fire is not. A
// region never removes an emitter from the trace, so it can only move a slot,
// never delete light.
type VPLRegion struct {
	Min, Max vec.V

	// Lights is how many VPLs this region keeps. It is the region's whole
	// budget, independent of the scene-wide count.
	Lights int

	// Gain and Reach override the generator's defaults inside the region; 0
	// means inherit. Reach is the one that costs: a bounce confined to a room
	// touches far fewer light-grid cells than one carrying the default 24 m.
	Gain  float64
	Reach float64

	// ShadowLevels overrides how many display levels a VPL here must be worth
	// before its shadow is traced; 0 inherits. This is the cost knob that does
	// not change how many lights there are or how far they carry — see
	// scene.Light.ShadowLevels.
	ShadowLevels float64

	// Label names the region in the generator's log, so an author can see which
	// file claimed which slots.
	Label string
}

// Contains reports whether p is inside the region.
func (r VPLRegion) Contains(p vec.V) bool {
	return p.X >= r.Min.X && p.X <= r.Max.X &&
		p.Y >= r.Min.Y && p.Y <= r.Max.Y &&
		p.Z >= r.Min.Z && p.Z <= r.Max.Z
}

// Valid reports whether the region has a non-degenerate extent and a budget to
// spend. A region asking for no lights is not an error; it is simply not a
// region, and dropping it here keeps the generator from having to care.
func (r VPLRegion) Valid() bool {
	return r.Lights > 0 &&
		r.Max.X > r.Min.X && r.Max.Y > r.Min.Y && r.Max.Z > r.Min.Z
}

// Volume is the region's extent, used to break ties when regions overlap: the
// smallest containing region wins, so a box drawn around one room takes
// precedence over one drawn around the building holding it.
func (r VPLRegion) Volume() float64 {
	return (r.Max.X - r.Min.X) * (r.Max.Y - r.Min.Y) * (r.Max.Z - r.Min.Z)
}
