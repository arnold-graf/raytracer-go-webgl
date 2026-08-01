package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

// buildingScene mimics the indoor-outdoor layout: terrain with a hill, a raised
// stone foundation, and a tall wall with a door gap.
func buildingScene() *Scene {
	ter := Terrain{
		OriginX: -40, OriginZ: -40, SizeX: 80, SizeZ: 80,
		Base: 0, Detail: 0, DetailScale: 0.1, Step: 0.3,
		Features: []TerrainFeature{
			{PosX: -20, PosZ: 0, Height: 5, Width: 8, Steepness: 2, ExtendX: 1, ExtendZ: 1},
		},
	}
	ter.Prepare()

	return &Scene{
		Terrains: []Terrain{ter},
		Boxes: []Box{
			// Foundation / floor slab, top at y=0.4 over x[10,20], z[-8,4].
			{Min: vec.New(10, -0.3, -8), Max: vec.New(20, 0.4, 4)},
			// South wall, left of the door (door gap x[14.5,16.5]).
			{Min: vec.New(10, 0, 3.5), Max: vec.New(14.5, 6.3, 3.9)},
			// South wall, right of the door.
			{Min: vec.New(16.5, 0, 3.5), Max: vec.New(20, 6.3, 3.9)},
		},
	}
}

func TestGroundHeightFoundationOverridesTerrain(t *testing.T) {
	s := buildingScene()
	if g := s.GroundHeight(15, 0, 2.0); math.Abs(g-0.4) > 1e-9 {
		t.Fatalf("ground inside building = %v, want 0.4 (foundation)", g)
	}
}

func TestGroundHeightFollowsTerrain(t *testing.T) {
	s := buildingScene()
	peak := s.GroundHeight(-20, 0, 100)
	if peak < 4.5 {
		t.Fatalf("ground at hilltop = %v, want ~5", peak)
	}
	flat := s.GroundHeight(0, 20, 100)
	if math.Abs(flat) > 0.2 {
		t.Fatalf("ground on flat terrain = %v, want ~0", flat)
	}
	if peak <= flat {
		t.Fatalf("terrain not followed: peak %v should exceed flat %v", peak, flat)
	}
}

// TestBlockedHonorsBoxTransform guards against the regression where collision
// used a box's local Min/Max and ignored its world transform, producing a
// phantom wall at the origin and none at the real (translated) location.
func TestGroundHeightIgnoresCeilingAboveHead(t *testing.T) {
	s := &Scene{
		Boxes: []Box{
			{Min: vec.New(0, 0, 0), Max: vec.New(10, 0.2, 10)},
			{Min: vec.New(0, 9, 0), Max: vec.New(10, 9.2, 10)},
		},
	}
	if g := s.GroundHeight(5, 5, 2.0); math.Abs(g-0.2) > 1e-9 {
		t.Fatalf("ground below ceiling = %v, want 0.2", g)
	}
}

func TestBlockedHonorsBoxTransform(t *testing.T) {
	xf := NewInstanceTransform(0, 0, 0, vec.New(16, 0, -2))
	s := &Scene{Boxes: []Box{
		// A wall authored in local space (around the origin), placed at world
		// (16,0,-2) via the include transform.
		{Min: vec.New(-1, 0, -1), Max: vec.New(1, 6, 1), Surface: Surface{Xform: xf}},
	}}
	feetY, headY, r, step := 0.0, 2.0, 0.3, 0.45

	// The wall is really at world (16,0,-2); local coords would have it at origin.
	if !s.Blocked(16, -2, feetY, headY, r, step) {
		t.Fatalf("expected the wall to block at its world position (16,-2)")
	}
	if s.Blocked(0, 0, feetY, headY, r, step) {
		t.Fatalf("origin must be clear (no phantom wall from local coords)")
	}
}

// TestSlantedRoofDoesNotBlock checks hypothesis B: a high tilted roof box stays
// above the player's head in world space and never blocks walking.
func TestSlantedRoofDoesNotBlock(t *testing.T) {
	roof := NewTransform(-12, 0, 0, vec.New(0, 5.9, 0))
	place := NewInstanceTransform(0, 0, 0, vec.New(16, 0, -2))
	s := &Scene{Boxes: []Box{
		{Min: vec.New(-4.7, 5.8, -5.7), Max: vec.New(4.7, 6.1, 5.7),
			Surface: Surface{Xform: place.Compose(roof)}},
	}}
	if s.Blocked(16, -2, 0.0, 2.0, 0.3, 0.45) {
		t.Fatalf("slanted roof (overhead) should not block a walking player")
	}
}

// TestStepMaterialAtPicksTopSurface verifies footstep material follows the
// highest surface underfoot: terrain reads as grass, a textured box floor reads
// by its texture, and a transformed (included) floor is honored in world space.
func TestStepMaterialAtPicksTopSurface(t *testing.T) {
	ter := Terrain{OriginX: -40, OriginZ: -40, SizeX: 80, SizeZ: 80, Base: 0, Step: 0.3}
	ter.Prepare()
	marbleFloor := Box{Min: vec.New(10, -0.3, -8), Max: vec.New(20, 0.4, 4),
		Surface: Surface{Tex: 5 /* texture.Marble */}}
	s := &Scene{Terrains: []Terrain{ter}, Boxes: []Box{marbleFloor}}

	if m := s.StepMaterialAt(0, 20, 2.0); m != StepGrass {
		t.Fatalf("open terrain should be grass, got %d", m)
	}
	if m := s.StepMaterialAt(15, 0, 2.0); m != StepHard {
		t.Fatalf("marble floor should be hard, got %d", m)
	}
}

// TestGroundHeightFallsThroughFloorHole checks that a stairwell opening cut into
// an upper floor slab (Box.Holes) removes the standing surface there, so the
// player drops to the surface below instead of floating on the hole.
func TestGroundHeightFallsThroughFloorHole(t *testing.T) {
	s := &Scene{Boxes: []Box{
		// Ground floor slab, top at y=0.4.
		{Min: vec.New(-5, 0, -5), Max: vec.New(5, 0.4, 5)},
		// Upper deck, top at y=4.0, with a stairwell hole over x[-2,0], z[-2,2].
		{Min: vec.New(-5, 3.9, -5), Max: vec.New(5, 4.0, 5),
			Holes: []AABB{{Min: vec.New(-2, 3.85, -2), Max: vec.New(0, 4.05, 2)}}},
	}}

	// Over solid upper deck: stand on it.
	if g := s.GroundHeight(3, 0, 100); math.Abs(g-4.0) > 1e-9 {
		t.Fatalf("on solid upper deck = %v, want 4.0", g)
	}
	// Over the stairwell hole: fall to the ground floor slab below.
	if g := s.GroundHeight(-1, 0, 100); math.Abs(g-0.4) > 1e-9 {
		t.Fatalf("over stairwell hole = %v, want 0.4 (floor below)", g)
	}
}

// TestBlockedHonorsBoxHole checks that a doorway cut into a single wall box (via
// Box.Holes / CSG) is walkable, while the solid wall on either side still blocks.
func TestBlockedHonorsBoxHole(t *testing.T) {
	// A wall facing +Z (thin in Z) spanning x[-4,4], with a door hole at
	// x[-1,1] piercing the full thickness from the floor up to y=2.5.
	wall := Box{
		Min: vec.New(-4, 0, 3.5), Max: vec.New(4, 6, 3.9),
		Holes: []AABB{{Min: vec.New(-1, 0, 3.4), Max: vec.New(1, 2.5, 4.0)}},
	}
	s := &Scene{Boxes: []Box{wall}}
	feetY, headY, r, step := 0.0, 2.0, 0.3, 0.45

	if !s.Blocked(3, 3.7, feetY, headY, r, step) {
		t.Fatalf("solid part of the wall should block")
	}
	if s.Blocked(0, 3.7, feetY, headY, r, step) {
		t.Fatalf("door hole should be passable")
	}
	// Too close to the jamb to fit the player radius.
	if !s.Blocked(0.85, 3.7, feetY, headY, r, step) {
		t.Fatalf("hole edge should block (player radius does not clear the jamb)")
	}
}

// TestBlockedHoleWithTransform checks hole passage works after the box is placed
// by an include transform (holes are authored in local space).
func TestBlockedHoleWithTransform(t *testing.T) {
	xf := NewInstanceTransform(0, 0, 0, vec.New(16, 0, -2))
	wall := Box{
		Min: vec.New(-4, 0, -0.2), Max: vec.New(4, 6, 0.2),
		Holes:   []AABB{{Min: vec.New(-1, 0, -0.3), Max: vec.New(1, 2.5, 0.3)}},
		Surface: Surface{Xform: xf},
	}
	s := &Scene{Boxes: []Box{wall}}
	feetY, headY, r, step := 0.0, 2.0, 0.3, 0.45

	// Wall sits at world x[12,20], z≈-2; door centered at world (16,-2).
	if !s.Blocked(19, -2, feetY, headY, r, step) {
		t.Fatalf("solid wall should block at world (19,-2)")
	}
	if s.Blocked(16, -2, feetY, headY, r, step) {
		t.Fatalf("door hole should be passable at world (16,-2)")
	}
}

// TestBlockedHonorsBoxRotation checks that a wall rotated 45° blocks at its
// world-oriented face, not at the axis-aligned world AABB corners outside the
// solid volume.
func TestBlockedHonorsBoxRotation(t *testing.T) {
	xf := NewInstanceTransform(0, -45, 0, vec.New(50, 0, -10))
	wall := Box{
		Min:     vec.New(-4, 0, -0.2),
		Max:     vec.New(4, 6, 0.2),
		Surface: Surface{Xform: xf},
	}
	s := &Scene{Boxes: []Box{wall}}
	feetY, headY, r, step := 0.0, 2.0, 0.3, 0.45

	// On the wall face in world space (local +Z outward).
	face := xf.ToWorld(vec.New(0, 1, 0.5))
	if !s.Blocked(face.X, face.Z, feetY, headY, r, step) {
		t.Fatalf("rotated wall should block at its world face (%.2f, %.2f)", face.X, face.Z)
	}

	// Empty space outside the rotated OBB but inside its world AABB.
	outside := xf.ToWorld(vec.New(-6, 1, -6))
	if s.Blocked(outside.X, outside.Z, feetY, headY, r, step) {
		t.Fatalf("point outside rotated wall (%.2f, %.2f) must not block", outside.X, outside.Z)
	}
}

func TestBlockedAtWallButNotDoorOrFloor(t *testing.T) {
	s := buildingScene()
	feetY, headY, r, step := 0.4, 2.0, 0.3, 0.45

	if !s.Blocked(12, 3.7, feetY, headY, r, step) {
		t.Fatalf("expected the south wall to block movement")
	}
	if s.Blocked(15.5, 3.7, feetY, headY, r, step) {
		t.Fatalf("door gap should be passable")
	}
	if s.Blocked(15, 0, feetY, headY, r, step) {
		t.Fatalf("standing on the foundation should not be blocked")
	}
}

func TestBlockedHonorsCylinderTransform(t *testing.T) {
	xf := NewInstanceTransform(0, 0, 0, vec.New(12, 0, -5))
	s := &Scene{Cylinders: []Cylinder{{
		CX: 0, CZ: 0, Radius: 0.5, YMin: 0, YMax: 8,
		Surface: Surface{Xform: xf},
	}}}
	feetY, headY, r, step := 0.0, 2.0, 0.3, 0.45
	if !s.Blocked(12, -5, feetY, headY, r, step) {
		t.Fatal("trunk should block at its world position")
	}
	if s.Blocked(0, 0, feetY, headY, r, step) {
		t.Fatal("origin must be clear (no phantom trunk at local coords)")
	}
}

func TestGroundHeightCylinderTopCap(t *testing.T) {
	s := &Scene{Cylinders: []Cylinder{{
		CX: 0, CZ: 0, Radius: 5, YMin: 10, YMax: 12,
	}}}
	if g := s.GroundHeight(0, 0, 20); math.Abs(g-12) > 1e-9 {
		t.Fatalf("top cap = %v, want 12", g)
	}
	if g := s.GroundHeight(4, 0, 20); math.Abs(g-12) > 1e-9 {
		t.Fatalf("on cap disk = %v, want 12", g)
	}
	if g := s.GroundHeight(6, 0, 20); g > 11.5 {
		t.Fatalf("outside cap disk = %v, want below cap", g)
	}
}

func TestStandingOnCylinderCapNotBlocked(t *testing.T) {
	s := &Scene{Cylinders: []Cylinder{{
		CX: 0, CZ: 0, Radius: 5, YMin: 10, YMax: 12,
	}}}
	feetY, headY, r, step := 12.0, 13.6, 0.3, 0.45
	if s.Blocked(0, 0, feetY, headY, r, step) {
		t.Fatal("standing on cylinder top cap should not block")
	}
}

func TestCylinderInteriorBlocksAboveCap(t *testing.T) {
	s := &Scene{Cylinders: []Cylinder{{
		CX: 0, CZ: 0, Radius: 5, YMin: 10, YMax: 12,
	}}}
	feetY, headY, r, step := 11.0, 12.6, 0.3, 0.45
	if !s.Blocked(2, 0, feetY, headY, r, step) {
		t.Fatal("cylinder wall should block inside the tube")
	}
}

func TestGroundHeightCylinderTopCapWithTransform(t *testing.T) {
	center := vec.New(0, 11, 0)
	xf := PlacementTransform(0, 0, 180, center, center)
	s := &Scene{Cylinders: []Cylinder{{
		CX: 0, CZ: 0, Radius: 5, YMin: 10, YMax: 12,
		Surface: Surface{Xform: xf},
	}}}
	if g := s.GroundHeight(0, 0, 20); math.Abs(g-12) > 0.05 {
		t.Fatalf("inverted top cap = %v, want ~12", g)
	}
	if s.Blocked(0, 0, 12, 13.6, 0.3, 0.45) {
		t.Fatal("should not block on inverted top cap")
	}
}

func TestBlockedHonorsConeTransform(t *testing.T) {
	xf := NewInstanceTransform(0, 0, 0, vec.New(-18, 0, -6))
	s := &Scene{Cones: []Cone{{
		CX: 0, CZ: 0, YBase: -0.7, YTip: 0.9, RBase: 1.45,
		Surface: Surface{Xform: xf},
	}}}
	feetY, headY, r, step := 0.0, 2.0, 0.3, 0.45
	if !s.Blocked(-18, -6, feetY, headY, r, step) {
		t.Fatal("root flare should block at the tree base")
	}
	if s.Blocked(0, 0, feetY, headY, r, step) {
		t.Fatal("origin must be clear")
	}
}

func TestGroundHeightConeBaseCap(t *testing.T) {
	s := &Scene{Cones: []Cone{{
		CX: 30, CZ: 30, YBase: 12, YTip: 15, RBase: 10, Capped: true,
	}}}
	if g := s.GroundHeight(30, 30, 20); math.Abs(g-12) > 1e-9 {
		t.Fatalf("cap center = %v, want 12", g)
	}
	if g := s.GroundHeight(39, 30, 20); math.Abs(g-12) > 1e-9 {
		t.Fatalf("on cap disk = %v, want 12", g)
	}
	if g := s.GroundHeight(41, 30, 20); g > 11.5 {
		t.Fatalf("outside cap disk = %v, want below cap", g)
	}
}

func TestStandingOnConeCapNotBlocked(t *testing.T) {
	s := &Scene{Cones: []Cone{{
		CX: 30, CZ: 30, YBase: 12, YTip: 15, RBase: 10, Capped: true,
	}}}
	feetY, headY, r, step := 12.0, 13.6, 0.3, 0.45
	if s.Blocked(30, 30, feetY, headY, r, step) {
		t.Fatal("standing on cone cap should not block")
	}
}

func TestConeInteriorBlocksAboveCap(t *testing.T) {
	s := &Scene{Cones: []Cone{{
		CX: 30, CZ: 30, YBase: 12, YTip: 15, RBase: 10, Capped: true,
	}}}
	feetY, headY, r, step := 13.0, 14.6, 0.3, 0.45
	if !s.Blocked(34, 30, feetY, headY, r, step) {
		t.Fatal("cone interior should block above the cap")
	}
}

func TestGroundHeightInvertedConeCap(t *testing.T) {
	center := vec.New(30, (12+15)/2, 30)
	xf := PlacementTransform(180, 0, 0, center, center)
	s := &Scene{Cones: []Cone{{
		CX: 30, CZ: 30, YBase: 12, YTip: 15, RBase: 10, Capped: true,
		Surface: Surface{Xform: xf},
	}}}
	if g := s.GroundHeight(30, 30, 20); math.Abs(g-14) > 0.05 {
		t.Fatalf("inverted cap = %v, want ~14", g)
	}
}

func TestGroundHeightConeCapWithRotateZ(t *testing.T) {
	center := vec.New(30, (12+15)/2, 30)
	xf := PlacementTransform(0, 0, 180, center, center)
	s := &Scene{Cones: []Cone{{
		CX: 30, CZ: 30, YBase: 12, YTip: 15, RBase: 10, Capped: true,
		Surface: Surface{Xform: xf},
	}}}
	// rotate_z=180 about the cone midpoint flips base and tip in world Y.
	if g := s.GroundHeight(30, 30, 20); math.Abs(g-14) > 0.05 {
		t.Fatalf("rotate_z cap = %v, want ~14", g)
	}
	if s.Blocked(30, 30, 14, 15.6, 0.3, 0.45) {
		t.Fatal("rotate_z should not block on inverted cap")
	}
}

func TestGroundHeightStaticIgnoresDynamicBoxes(t *testing.T) {
	s := &Scene{
		Boxes: []Box{
			{Min: vec.New(-2, 0, -2), Max: vec.New(2, 0.2, 2)},
			{Min: vec.New(-0.2, 0, -0.2), Max: vec.New(0.2, 1.5, 0.2)},
		},
		DynamicBodies: []DynamicBody{{Boxes: [2]int{1, 2}}},
	}
	if g := s.GroundHeight(0, 0, 2); math.Abs(g-1.5) > 1e-9 {
		t.Fatalf("full query = %v, want 1.5 (torso box)", g)
	}
	if g := s.GroundHeightStatic(0, 0, 2); math.Abs(g-0.2) > 1e-9 {
		t.Fatalf("static query = %v, want 0.2 (floor)", g)
	}
}

func TestGroundHeightStaticIgnoresDynamicCylinders(t *testing.T) {
	s := &Scene{
		Boxes: []Box{{Min: vec.New(-2, 0, -2), Max: vec.New(2, 0.08, 2)}},
		Cylinders: []Cylinder{
			{CX: 0, CZ: 0, Radius: 0.1, YMin: 0, YMax: 1.2},
		},
		DynamicBodies: []DynamicBody{{Cylinders: [2]int{0, 1}}},
	}
	if g := s.GroundHeight(0, 0, 2); math.Abs(g-1.2) > 1e-9 {
		t.Fatalf("full query = %v, want 1.2 (leg cylinder)", g)
	}
	if g := s.GroundHeightStatic(0, 0, 2); math.Abs(g-0.08) > 1e-9 {
		t.Fatalf("static query = %v, want 0.08 (floor)", g)
	}
}

func TestNoCollisionPrimitiveIgnored(t *testing.T) {
	s := &Scene{Boxes: []Box{{
		Min: vec.New(-1, 0, -1), Max: vec.New(1, 2, 1),
		Surface: Surface{Mat: MatDiffuse, NoCollision: true},
	}}}
	feetY, headY, r, step := 0.0, 1.7, 0.3, 0.45
	if s.Blocked(0, 0, feetY, headY, r, step) {
		t.Fatal("collision=false box should not block")
	}
	if g := s.GroundHeight(0, 0, 2); g > 0.01 {
		t.Fatalf("collision=false box should not be ground, got %v", g)
	}
}
