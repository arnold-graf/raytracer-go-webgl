package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func invertedSiliconCone() Cone {
	center := vec.New(30, (12+15)/2, 30)
	return Cone{
		CX: 30, CZ: 30, YBase: 12, YTip: 15, RBase: 10, Capped: true,
		Surface: Surface{Xform: PlacementTransform(0, 0, 180, center, center)},
	}
}

func coneWorldNormal(co *Cone, ro, rd vec.V, t float64) vec.V {
	lr := co.Xform.LocalRay(vec.Ray{Origin: ro, Dir: rd})
	hp := lr.Origin.Add(lr.Dir.Scale(t))
	return co.CapNormalWorld(ro, co.Xform.ToWorld(hp))
}

func TestInvertedConeCapNormalFromBelow(t *testing.T) {
	co := invertedSiliconCone()
	ro := vec.New(30, 13, 30)
	rd := vec.New(0, 1, 0)
	lr := co.Xform.LocalRay(vec.Ray{Origin: ro, Dir: rd})
	tHit := co.Intersect(lr)
	if math.IsInf(tHit, 1) {
		t.Fatal("expected cap hit from below")
	}
	n := coneWorldNormal(&co, ro, rd, tHit)
	if n.Y > -0.5 {
		t.Fatalf("underside normal = %v, want downward (-Y)", n)
	}
	if rd.Dot(n) >= 0 {
		t.Fatalf("normal should face the ray, dot=%v", rd.Dot(n))
	}
}

func TestInvertedConeCapNormalFromAbove(t *testing.T) {
	co := invertedSiliconCone()
	ro := vec.New(30, 16, 30)
	rd := vec.New(0.3, -0.1, 0.2).Normalize()
	lr := co.Xform.LocalRay(vec.Ray{Origin: ro, Dir: rd})
	tHit := co.Intersect(lr)
	if math.IsInf(tHit, 1) {
		t.Fatal("expected cap hit from above")
	}
	n := coneWorldNormal(&co, ro, rd, tHit)
	if n.Y < 0.5 {
		t.Fatalf("top normal = %v, want upward (+Y)", n)
	}
	if rd.Dot(n) >= 0 {
		t.Fatalf("normal should face the ray, dot=%v", rd.Dot(n))
	}
}

func TestInvertedConeHitsAreCapNotSide(t *testing.T) {
	co := invertedSiliconCone()
	cases := []struct {
		name   string
		ro, rd vec.V
	}{
		{"center down", vec.New(30, 16, 30), vec.New(0, -1, 0)},
		{"grazing", vec.New(20, 14.5, 30), vec.New(1, -0.05, 0).Normalize()},
		{"offaxis", vec.New(25, 16, 28), vec.New(0.4, -0.8, 0.2).Normalize()},
		{"near rim", vec.New(30, 16, 30), vec.New(0.55, -0.8, 0.1).Normalize()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lr := co.Xform.LocalRay(vec.Ray{Origin: tc.ro, Dir: tc.rd})
			th := co.Intersect(lr)
			if math.IsInf(th, 1) {
				t.Fatal("miss")
			}
			lhp := lr.Origin.Add(lr.Dir.Scale(th))
			if math.Abs(lhp.Y-co.YBase) > 0.02 {
				t.Fatalf("hit local y=%.4f, want base cap at %.4f (got side surface)", lhp.Y, co.YBase)
			}
			whp := co.Xform.ToWorld(lhp)
			n := co.CapNormalWorld(tc.ro, whp)
			if n.Y < 0.5 {
				t.Fatalf("cap normal = %v, want +Y", n)
			}
		})
	}
}
