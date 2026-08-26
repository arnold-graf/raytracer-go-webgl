package scene

import "testing"

func TestZoneRectVertices(t *testing.T) {
	verts := ZoneRectVertices([2]float64{0, 0}, [2]float64{2, 1}, 0)
	if len(verts) != 4 {
		t.Fatalf("len = %d, want 4", len(verts))
	}
	if verts[0].X != -2 || verts[0].Z != -1 {
		t.Fatalf("corner 0 = %v", verts[0])
	}
	if verts[2].X != 2 || verts[2].Z != 1 {
		t.Fatalf("corner 2 = %v", verts[2])
	}
}

func TestZonePathVertices(t *testing.T) {
	verts, err := ZonePathVertices([][2]float64{{0, 0}, {0, 10}}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(verts) != 4 {
		t.Fatalf("len = %d, want 4", len(verts))
	}
	// Straight road along +Z, width 4 → x = ±2.
	if verts[0].X != -2 || verts[0].Z != 0 {
		t.Fatalf("start left = %v", verts[0])
	}
	if verts[1].X != -2 || verts[1].Z != 10 {
		t.Fatalf("end left = %v", verts[1])
	}
}
