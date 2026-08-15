package probe

import (
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestProbeSyncAfterStructuralEdit(t *testing.T) {
	sc := &scene.Scene{
		Spheres: []scene.Sphere{
			{Center: vec.New(0, 0, 0), Radius: 0.1},
		},
	}
	pb := New(sc)
	if d := pb.Distance(vec.New(0, 0, -1), vec.New(0, 0, 1), 10); d <= 0 || d > 10 {
		t.Fatalf("initial distance = %v", d)
	}

	sc.Spheres = append(sc.Spheres, scene.Sphere{Center: vec.New(2, 0, 0), Radius: 0.1})
	sc.Touch()

	// Stale BVH would panic indexing the new primitive ref.
	pb.Sync(sc)
	if d := pb.Distance(vec.New(2, 0, -1), vec.New(0, 0, 1), 10); d <= 0 || d > 10 {
		t.Fatalf("distance after sync = %v", d)
	}
}
