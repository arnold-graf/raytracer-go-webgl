package scene

import "raytracer/internal/vec"

// PrimitiveCounts tracks how many of each primitive type are in a scene.
type PrimitiveCounts struct {
	Spheres, Boxes, Cylinders, Cones, Tori int
	Lights, Campfires, Ambiences, Interacts int
}

// CountPrimitives returns current primitive slice lengths in s.
func CountPrimitives(s *Scene) PrimitiveCounts {
	if s == nil {
		return PrimitiveCounts{}
	}
	return PrimitiveCounts{
		Spheres: len(s.Spheres), Boxes: len(s.Boxes),
		Cylinders: len(s.Cylinders), Cones: len(s.Cones), Tori: len(s.Tori),
		Lights: len(s.Lights), Campfires: len(s.Campfires),
		Ambiences: len(s.Ambiences), Interacts: len(s.Interactables),
	}
}

// TerrainFollowPlacement records one [[include]] merge that should sit on the
// terrain surface after the full scene height field is prepared. YOffset is
// added above the sampled ground (the include's at.y).
type TerrainFollowPlacement struct {
	YOffset float64
	SphereStart, SphereEnd       int
	BoxStart, BoxEnd             int
	CylinderStart, CylinderEnd   int
	ConeStart, ConeEnd           int
	TorusStart, TorusEnd         int
	LightStart, LightEnd         int
	CampfireStart, CampfireEnd   int
	AmbienceStart, AmbienceEnd   int
	InteractStart, InteractEnd   int
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
		LightStart: before.Lights, LightEnd: after.Lights,
		CampfireStart: before.Campfires, CampfireEnd: after.Campfires,
		AmbienceStart: before.Ambiences, AmbienceEnd: after.Ambiences,
		InteractStart: before.Interacts, InteractEnd: after.Interacts,
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
		p.LightStart += off.Lights
		p.LightEnd += off.Lights
		p.CampfireStart += off.Campfires
		p.CampfireEnd += off.Campfires
		p.AmbienceStart += off.Ambiences
		p.AmbienceEnd += off.Ambiences
		p.InteractStart += off.Interacts
		p.InteractEnd += off.Interacts
	}
}

// ApplyTerrainFollow adjusts every recorded include so each object's local
// origin (0,0,0) rests on the terrain at its own world (x,z), plus YOffset.
func (s *Scene) ApplyTerrainFollow(placements []TerrainFollowPlacement) {
	if s == nil || len(placements) == 0 {
		return
	}
	for _, p := range placements {
		p.snapEach(s)
	}
}

func (p TerrainFollowPlacement) snapEach(s *Scene) {
	for i := p.ConeStart; i < p.ConeEnd; i++ {
		snapByOrigin(s, p.YOffset, func(dy float64) {
			shift := vec.New(0, dy, 0)
			xf := NewInstanceTransform(0, 0, 0, shift)
			s.Cones[i].Xform = xf.Compose(s.Cones[i].Xform)
		}, s.Cones[i].Xform)
	}
	for i := p.CylinderStart; i < p.CylinderEnd; i++ {
		snapByOrigin(s, p.YOffset, func(dy float64) {
			shift := vec.New(0, dy, 0)
			xf := NewInstanceTransform(0, 0, 0, shift)
			s.Cylinders[i].Xform = xf.Compose(s.Cylinders[i].Xform)
		}, s.Cylinders[i].Xform)
	}
	for i := p.SphereStart; i < p.SphereEnd; i++ {
		snapByOrigin(s, p.YOffset, func(dy float64) {
			shift := vec.New(0, dy, 0)
			xf := NewInstanceTransform(0, 0, 0, shift)
			s.Spheres[i].Xform = xf.Compose(s.Spheres[i].Xform)
		}, s.Spheres[i].Xform)
	}
	for i := p.BoxStart; i < p.BoxEnd; i++ {
		snapByOrigin(s, p.YOffset, func(dy float64) {
			shift := vec.New(0, dy, 0)
			xf := NewInstanceTransform(0, 0, 0, shift)
			s.Boxes[i].Xform = xf.Compose(s.Boxes[i].Xform)
		}, s.Boxes[i].Xform)
	}
	for i := p.TorusStart; i < p.TorusEnd; i++ {
		snapByOrigin(s, p.YOffset, func(dy float64) {
			shift := vec.New(0, dy, 0)
			xf := NewInstanceTransform(0, 0, 0, shift)
			s.Tori[i].Xform = xf.Compose(s.Tori[i].Xform)
		}, s.Tori[i].Xform)
	}
	for i := p.LightStart; i < p.LightEnd; i++ {
		snapPoint(s, p.YOffset, func(dy float64) {
			s.Lights[i].Pos.Y += dy
		}, s.Lights[i].Pos)
	}
	for i := p.CampfireStart; i < p.CampfireEnd; i++ {
		snapPoint(s, p.YOffset, func(dy float64) {
			s.Campfires[i].Center.Y += dy
		}, s.Campfires[i].Center)
	}
	for i := p.AmbienceStart; i < p.AmbienceEnd; i++ {
		snapPoint(s, p.YOffset, func(dy float64) {
			s.Ambiences[i].Pos.Y += dy
		}, s.Ambiences[i].Pos)
	}
	for i := p.InteractStart; i < p.InteractEnd; i++ {
		snapPoint(s, p.YOffset, func(dy float64) {
			s.Interactables[i].Center.Y += dy
		}, s.Interactables[i].Center)
	}
}

func snapByOrigin(s *Scene, yOffset float64, apply func(dy float64), xf *Transform) {
	snapPoint(s, yOffset, apply, originWorld(xf))
}

func snapPoint(s *Scene, yOffset float64, apply func(dy float64), anchor vec.V) {
	h, ok := s.TerrainHeightAt(anchor.X, anchor.Z)
	if !ok {
		return
	}
	dy := h + yOffset - anchor.Y
	if dy == 0 {
		return
	}
	apply(dy)
}

func originWorld(xf *Transform) vec.V {
	if xf == nil {
		return vec.V{}
	}
	return xf.ToWorld(vec.V{})
}
