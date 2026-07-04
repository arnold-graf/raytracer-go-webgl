package sceneio

import (
	"math"
	"testing"
)

func TestOfficeSunsetIndexLoadsGeometry(t *testing.T) {
	s, err := Load(repoFile("scenes/office-sunset/index.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Terrains) != 0 {
		t.Fatalf("terrains = %d, want 0 (office-sunset has no terrain)", len(s.Terrains))
	}
	if len(s.Boxes) < 4 {
		t.Fatalf("boxes = %d, want at least server room + cube walls", len(s.Boxes))
	}
	p, ok := s.PointByID("cube_lab_1")
	if !ok {
		t.Fatal("cube_lab_1 point missing")
	}
	// cube at (10,1,2) + spawn (1.5, _, 1.5) in room-local, server room at y=200
	wantX := 10.0 + 1.5
	wantZ := 2.0 + 1.5
	if math.Abs(p.Pos.X-wantX) > 1e-6 || math.Abs(p.Pos.Z-wantZ) > 1e-6 {
		t.Fatalf("point pos = %v, want x=%v z=%v", p.Pos, wantX, wantZ)
	}
	if !p.UseFloor || math.Abs(p.FloorY-(200.0+1.0+0.3)) > 1e-6 {
		t.Fatalf("floor_y = %v useFloor=%v, want %v true", p.FloorY, p.UseFloor, 200.0+1.0+0.3)
	}
	// Camera start in the server room: floor top at y=200.2, ceiling at ~209.2.
	const eyeY = 201.8
	g := s.GroundHeight(10, 8, eyeY)
	if math.Abs(g-200.2) > 0.05 {
		t.Fatalf("GroundHeight at camera = %v, want floor top ~200.2 (not ceiling)", g)
	}
}
