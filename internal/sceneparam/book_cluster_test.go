package sceneparam

import "testing"

func TestBookClusterPack(t *testing.T) {
	seed, width, gap := 7.0, 0.42, 0.004
	minT, maxT := 0.024, 0.034
	n := bookClusterCount(seed, width, gap, minT, maxT)
	if n < 2 {
		t.Fatalf("count = %d, want at least 2", n)
	}
	prevRight := -width / 2
	for i := 0; i < n; i++ {
		thick := bookThickness(seed, float64(i), minT, maxT)
		if thick < minT-1e-9 || thick > maxT+1e-9 {
			t.Fatalf("book %d thickness %.4f out of [%g, %g]", i, thick, minT, maxT)
		}
		cx := bookClusterX(seed, float64(i), width, gap, minT, maxT)
		left := cx - thick/2
		if left < prevRight-1e-9 {
			t.Fatalf("book %d overlaps previous (left=%.4f prevRight=%.4f)", i, left, prevRight)
		}
		if i > 0 && left-prevRight > gap+1e-6 {
			t.Fatalf("book %d gap too large (%.4f)", i, left-prevRight)
		}
		prevRight = cx + thick/2
	}
	if prevRight > width/2+1e-6 {
		t.Fatalf("row extends past width (right=%.4f)", prevRight)
	}
}
