package scene

import (
	"testing"

	"raytracer/internal/vec"
)

func TestArtDecoLegPivotSymmetry(t *testing.T) {
	outer := NewInstanceTransform(0, -90, 0, vec.New(30, 0, 0))
	for _, width := range []float64{5, 8} {
		right := outer.Compose(PlacementTransform(-90, 180, 0, vec.V{}, vec.New(1, 0, 0)))
		left := outer.Compose(PlacementTransform(-90, 180, 0, vec.New(0, 0, width), vec.New(1, 0, 0)))
		rb := right.ToWorld(vec.V{})
		lb := left.ToWorld(vec.V{})
		rt := right.ToWorld(vec.New(1, 1, 4.5))
		lt := left.ToWorld(vec.New(1, 1, 4.5))
		if rb.Y > 0.01 || lb.Y > 0.01 {
			t.Errorf("width=%.0f: bottom right Y=%.3f left Y=%.3f", width, rb.Y, lb.Y)
		}
		if rt.Y < 4.4 || lt.Y < 4.4 {
			t.Errorf("width=%.0f: top right Y=%.3f left Y=%.3f", width, rt.Y, lt.Y)
		}
	}
}
