package character

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestTwoBoneMaxReach(t *testing.T) {
	l1, l2 := 0.40, 0.34
	full := l1 + l2
	reach := twoBoneMaxReach(l1, l2, 22)
	if reach >= full-1e-6 {
		t.Fatalf("22° min bend should shorten reach: full=%v reach=%v", full, reach)
	}
	if reach <= l1-l2 {
		t.Fatalf("reach=%v should stay above folded minimum", reach)
	}
}

func TestSolveTwoBoneMinBend(t *testing.T) {
	root := vec.New(0, 0.8, 0)
	l1, l2 := 0.40, 0.34
	// Target that would fully extend the chain without a bend limit.
	target := root.Add(vec.V{Y: -0.74})
	pole := vec.New(0.2, 0.5, 0.1)

	straight := SolveTwoBone(root, target, pole, l1, l2)
	bent := SolveTwoBoneMinBend(root, target, pole, l1, l2, 22)
	if !straight.OK || !bent.OK {
		t.Fatal("expected OK")
	}

	straightFlex := kneeFlex(root, straight.Mid, straight.End)
	bentFlex := kneeFlex(root, bent.Mid, bent.End)
	if bentFlex >= straightFlex {
		t.Fatalf("min bend should reduce flex: straight=%v bent=%v", straightFlex, bentFlex)
	}
	if bentFlex > 1+math.Cos(22*math.Pi/180)+0.02 {
		t.Fatalf("bent flex=%v exceeds 22° limit", bentFlex)
	}
}

func kneeFlex(hip, knee, ankle vec.V) float64 {
	thighDir := knee.Sub(hip).Normalize()
	shinDir := ankle.Sub(knee).Normalize()
	return 1 + thighDir.Dot(shinDir) // 2 = straight, lower = more bent
}
