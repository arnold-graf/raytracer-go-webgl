package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestWorldYForLocalYOnRotatedCone(t *testing.T) {
	center := vec.New(30, (12+15)/2, 30)
	xf := PlacementTransform(0, 0, 180, center, center)
	for _, wy := range []float64{14, 15} {
		lp := xf.ToLocal(vec.V{X: 30, Y: wy, Z: 30})
		t.Logf("wy=%v local=%v", wy, lp)
	}
	tw := xf.ToWorld(vec.V{X: 30, Y: 12, Z: 30})
	t.Logf("ToWorld base center=%v", tw)
	wy, ok := xf.WorldYForLocalY(30, 30, 12)
	if !ok {
		t.Fatal("expected ok")
	}
	if math.Abs(wy-14) > 1e-9 {
		t.Fatalf("wy=%v want 14", wy)
	}
}
