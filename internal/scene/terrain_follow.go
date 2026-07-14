package scene

import "raytracer/internal/vec"

// PrimitiveCounts tracks how many of each primitive type are in a scene.
type PrimitiveCounts struct {
	Spheres, Boxes, Cylinders, Cones, Tori, Rings, Lenses int
	Lights, Campfires, Ambiences int
}

// CountPrimitives returns current primitive slice lengths in s.
func CountPrimitives(s *Scene) PrimitiveCounts {
	if s == nil {
		return PrimitiveCounts{}
	}
	return PrimitiveCounts{
		Spheres: len(s.Spheres), Boxes: len(s.Boxes),
		Cylinders: len(s.Cylinders), Cones: len(s.Cones), Tori: len(s.Tori), Rings: len(s.Rings),
		Lenses: len(s.Lenses),
		Lights: len(s.Lights), Campfires: len(s.Campfires),
		Ambiences: len(s.Ambiences),
	}
}

// TerrainFollowPlacement records one [[include]] merge that should sit on the
// terrain surface after the full scene height field is prepared. YOffset is
// added above the sampled ground (the include's at.y). Anchor is the include
// origin in world space before snapping; every primitive in the range moves by
// the same vertical delta so nested parts stay aligned (rigid assembly).
type TerrainFollowPlacement struct {
	YOffset float64
	Anchor  vec.V
	SphereStart, SphereEnd       int
	BoxStart, BoxEnd             int
	CylinderStart, CylinderEnd   int
	ConeStart, ConeEnd           int
	TorusStart, TorusEnd         int
	RingStart, RingEnd           int
	LightStart, LightEnd         int
	CampfireStart, CampfireEnd   int
	AmbienceStart, AmbienceEnd   int
}

// PlacementFromRange builds a follow record for primitives appended to dst
// between before and after merge counts.
func PlacementFromRange(before, after PrimitiveCounts, yOffset float64) TerrainFollowPlacement {
	return TerrainFollowPlacement{
		YOffset: yOffset,
		SphereStart: before.Spheres, SphereEnd: after.Spheres,
		BoxStart: before.Boxes, BoxEnd: after.Boxes,
		CylinderStart: before.Cylinders, CylinderEnd: after.Cylinders,
		ConeStart: before.Cones, ConeEnd: after.Cones,
		TorusStart: before.Tori, TorusEnd: after.Tori,
		RingStart: before.Rings, RingEnd: after.Rings,
		LightStart: before.Lights, LightEnd: after.Lights,
		CampfireStart: before.Campfires, CampfireEnd: after.Campfires,
		AmbienceStart: before.Ambiences, AmbienceEnd: after.Ambiences,
	}
}

// OffsetPlacements shifts every index in placements by off (after merging the
// recorded sub-scene into a parent).
func OffsetPlacements(placements []TerrainFollowPlacement, off PrimitiveCounts) {
	for i := range placements {
		p := &placements[i]
		p.SphereStart += off.Spheres
		p.SphereEnd += off.Spheres
		p.BoxStart += off.Boxes
		p.BoxEnd += off.Boxes
		p.CylinderStart += off.Cylinders
		p.CylinderEnd += off.Cylinders
		p.ConeStart += off.Cones
		p.ConeEnd += off.Cones
		p.TorusStart += off.Tori
		p.TorusEnd += off.Tori
		p.RingStart += off.Rings
		p.RingEnd += off.Rings
		p.LightStart += off.Lights
		p.LightEnd += off.Lights
		p.CampfireStart += off.Campfires
		p.CampfireEnd += off.Campfires
		p.AmbienceStart += off.Ambiences
		p.AmbienceEnd += off.Ambiences
	}
}

// ApplyTerrainFollow adjusts every recorded include so its anchor rests on the
// terrain at (anchor.x, anchor.z), plus YOffset. All primitives in the merge
// range shift by the same amount.
func (s *Scene) ApplyTerrainFollow(placements []TerrainFollowPlacement) {
	if s == nil || len(placements) == 0 {
		return
	}
	for _, p := range placements {
		p.snapEach(s)
	}
}

func (p TerrainFollowPlacement) snapEach(s *Scene) {
	h, ok := s.TerrainHeightAt(p.Anchor.X, p.Anchor.Z)
	if !ok {
		return
	}
	dy := h + p.YOffset - p.Anchor.Y
	if dy == 0 {
		return
	}
	shift := NewInstanceTransform(0, 0, 0, vec.New(0, dy, 0))
	for i := p.ConeStart; i < p.ConeEnd; i++ {
		s.Cones[i].Xform = shift.Compose(s.Cones[i].Xform)
	}
	for i := p.CylinderStart; i < p.CylinderEnd; i++ {
		s.Cylinders[i].Xform = shift.Compose(s.Cylinders[i].Xform)
	}
	for i := p.SphereStart; i < p.SphereEnd; i++ {
		s.Spheres[i].Xform = shift.Compose(s.Spheres[i].Xform)
	}
	for i := p.BoxStart; i < p.BoxEnd; i++ {
		s.Boxes[i].Xform = shift.Compose(s.Boxes[i].Xform)
	}
	for i := p.TorusStart; i < p.TorusEnd; i++ {
		s.Tori[i].Xform = shift.Compose(s.Tori[i].Xform)
	}
	for i := p.RingStart; i < p.RingEnd; i++ {
		s.Rings[i].Xform = shift.Compose(s.Rings[i].Xform)
	}
	for i := p.LightStart; i < p.LightEnd; i++ {
		s.Lights[i].Pos.Y += dy
	}
	for i := p.CampfireStart; i < p.CampfireEnd; i++ {
		s.Campfires[i].Center.Y += dy
	}
	for i := p.AmbienceStart; i < p.AmbienceEnd; i++ {
		s.Ambiences[i].Pos.Y += dy
	}
}
