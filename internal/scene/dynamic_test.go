package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestNPCSpawnPlaced(t *testing.T) {
	sp := NPCSpawn{
		Pos:     vec.New(1, 0.2, 2),
		Yaw:     0,
		Heading: 0,
		Patrol:  []vec.V{{X: 8, Y: 0.2, Z: 10}},
	}
	xf := NewInstanceTransform(0, 0, 0, vec.New(0, 200, 0))
	got := sp.Placed(xf)
	if math.Abs(got.Pos.Y-200.2) > 1e-9 {
		t.Fatalf("pos = %v, want Y=200.2", got.Pos)
	}
	if math.Abs(got.Patrol[0].Y-200.2) > 1e-9 {
		t.Fatalf("patrol = %v, want Y=200.2", got.Patrol[0])
	}
}
