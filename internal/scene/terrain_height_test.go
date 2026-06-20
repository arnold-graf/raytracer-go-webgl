package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestTerrainHeightAtNoFootprint(t *testing.T) {
	s := &Scene{Terrains: []Terrain{{Pads: []TerrainPad{{CenterX: 1, CenterZ: 2}}}}}
	if _, ok := s.TerrainHeightAt(0, 0); ok {
		t.Fatal("stub terrain without size should not drive placement")
	}
}

func TestTerrainHeightAtPeak(t *testing.T) {
	ter := Terrain{
		OriginX: -10, OriginZ: -10, SizeX: 20, SizeZ: 20, Base: 0,
		Features: []TerrainFeature{{PosX: 0, PosZ: 0, Height: 8, Width: 4}},
	}
	ter.Prepare()
	s := &Scene{Terrains: []Terrain{ter}}
	h, ok := s.TerrainHeightAt(0, 0)
	if !ok {
		t.Fatal("expected height field")
	}
	if h < 7.5 || h > 8.5 {
		t.Fatalf("peak height = %v, want ~8", h)
	}
}

func TestRotatedTerrainPad(t *testing.T) {
	const yaw = -45 * math.Pi / 180
	base := Terrain{
		OriginX: -50, OriginZ: -50, SizeX: 120, SizeZ: 120,
		Base: 0, Detail: 0,
		Features: []TerrainFeature{{PosX: 60, PosZ: -2, Height: 3, Width: 3}},
	}

	flat := base
	flat.Pads = []TerrainPad{{
		CenterX: 50, CenterZ: -10, HalfX: 8.5, HalfZ: 8, Level: 0, Margin: 0,
	}}
	flat.Prepare()

	rot := base
	rot.Pads = []TerrainPad{{
		CenterX: 50, CenterZ: -10, HalfX: 8.5, HalfZ: 8, Level: 0, Margin: 0, Angle: yaw,
	}}
	rot.Prepare()

	xf := NewInstanceTransform(0, -45, 0, vec.New(50, 0, -10))
	inside := xf.ToWorld(vec.New(7, 0, 0))
	if h := rot.Height(inside.X, inside.Z); math.Abs(h) > 0.01 {
		t.Fatalf("inside rotated pad = %v, want 0", h)
	}

	// (58,-2) is inside an axis-aligned pad but outside the same pad rotated -45°.
	if h := flat.Height(58, -2); h > 0.5 {
		t.Fatalf("unrotated pad should flatten, got %v", h)
	}
	if h := rot.Height(58, -2); h < 1.5 {
		t.Fatalf("rotated pad should leave terrain intact, got %v", h)
	}
}
