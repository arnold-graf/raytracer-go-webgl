package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestNewTransformYAxisMapsLocalY(t *testing.T) {
	origin := vec.New(0, 1, 0)
	tip := vec.New(0.3, 0.2, -0.4)
	xf := NewTransformYAxis(origin, tip)
	got := xf.ToWorld(vec.V{Y: 1}).Sub(origin)
	want := tip.Sub(origin).Normalize()
	if math.Abs(got.X-want.X) > 1e-9 || math.Abs(got.Y-want.Y) > 1e-9 || math.Abs(got.Z-want.Z) > 1e-9 {
		t.Fatalf("local +Y maps to %v, want %v", got, want)
	}
	tipWorld := xf.ToWorld(vec.V{Y: 0.42})
	expect := origin.Add(want.Scale(0.42))
	if tipWorld.Sub(expect).Len() > 1e-6 {
		t.Fatalf("bone tip = %v, want %v", tipWorld, expect)
	}
}

func TestIKLegSegmentsMeetAtKnee(t *testing.T) {
	hip := vec.New(0.14, 0.93, 0)
	dir := vec.V{X: 0.05, Y: -0.9, Z: -0.12}.Normalize()
	knee := hip.Add(dir.Scale(0.42))
	ankle := knee.Add(dir.Scale(0.40))

	thigh := NewTransformYAxis(hip, knee)
	shin := NewTransformYAxis(knee, ankle)

	thighTip := thigh.ToWorld(vec.V{Y: 0.42})
	shinBase := shin.Translation()
	if thighTip.Sub(knee).Len() > 0.01 {
		t.Fatalf("thigh tip %v should meet knee %v", thighTip, knee)
	}
	if shinBase.Sub(knee).Len() > 1e-6 {
		t.Fatalf("shin base %v should start at knee %v", shinBase, knee)
	}
}
