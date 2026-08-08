package probe_test

import (
	"fmt"
	"testing"

	"raytracer/internal/bvh"
	"raytracer/internal/probe"
	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func TestMoonShadowAnalysis(t *testing.T) {
	sc, err := sceneio.Load("../../scenes/office-sunset/index.toml")
	if err != nil {
		t.Fatal(err)
	}

	moonC := vec.V{X: 240, Y: 390, Z: 120}
	moonR := 20.0
	light := vec.V{X: 200, Y: 350, Z: 100}
	cam := vec.V{X: 10, Y: 201.8, Z: 8}

	blk := bvh.NewBlockers(sc)
	pb := probe.New(sc)

	toMoon := moonC.Sub(cam).Normalize()

	shadowed := 0
	total := 0

	for i := 0; i < 20; i++ {
		for j := 0; j < 20; j++ {
			u := (float64(i)/19 - 0.5) * 1.8
			v := (float64(j)/19 - 0.5) * 1.8
			if u*u+v*v > 1 {
				continue
			}
			hp := moonC.Add(toMoon.Scale(moonR * 0.95))
			up := vec.V{X: 0, Y: 1, Z: 0}
			right := toMoon.Cross(up).Normalize()
			up2 := right.Cross(toMoon).Normalize()
			hp = hp.Add(right.Scale(u * moonR * 0.5)).Add(up2.Scale(v * moonR * 0.5))
			n := hp.Sub(moonC).Normalize()
			if n.Dot(toMoon) < 0.3 {
				continue
			}

			ep := hp.Add(n.Scale(5e-4))
			ld := light.Sub(hp)
			dist := ld.Len()
			dir := ld.Scale(1 / dist)

			total++
			limit := dist - 0.05
			if hit := blk.AnyHitDist(vec.Ray{Origin: ep, Dir: dir}, limit); hit < limit && hit > 1e-4 {
				shadowed++
			}
		}
	}
	t.Logf("Moon shadow samples: %d/%d shadowed (%.1f%%)", shadowed, total, 100*float64(shadowed)/float64(total))

	ao, ok := pb.BakeAO()
	if ok {
		t.Logf("AO volume: %dx%dx%d, cell=%.3f, min=%v", ao.NX, ao.NY, ao.NZ, ao.Cell, ao.Min)
		hp := moonC.Add(toMoon.Scale(moonR))
		n := hp.Sub(moonC).Normalize()
		p := hp.Add(n.Scale(ao.Bias))
		fx := (p.X-ao.Min.X)*ao.Inv - 0.5
		fy := (p.Y-ao.Min.Y)*ao.Inv - 0.5
		fz := (p.Z-ao.Min.Z)*ao.Inv - 0.5
		t.Logf("Moon surface AO grid frac (%.2f, %.2f, %.2f)", fx, fy, fz)
	}

	full := bvh.New(sc)
	tHit := full.NearestDist(vec.Ray{Origin: cam, Dir: toMoon}, 1e6)
	t.Logf("Primary ray to moon: t=%.4f (expect ~%.1f)", tHit, cam.Dist(moonC)-moonR)

	hp := moonC.Add(toMoon.Scale(moonR))
	n := hp.Sub(moonC).Normalize()
	ep := hp.Add(n.Scale(5e-4))
	ld := light.Sub(hp)
	dir := ld.Normalize()
	tself := blk.AnyHitDist(vec.Ray{Origin: ep, Dir: dir}, ld.Len()-0.05)
	t.Logf("Self shadow test t=%.6f dist=%.2f", tself, ld.Len())

	// Per-pixel shadow from camera rays through moon disc
	misses := 0
	shadowPix := 0
	pixTotal := 0
	for py := 0; py < 40; py++ {
		for px := 0; px < 40; px++ {
			u := (float64(px)/39*2 - 1) * 0.04
			v := (float64(py)/39*2 - 1) * 0.025
			rd := vec.V{
				X: toMoon.X + u,
				Y: toMoon.Y + v,
				Z: toMoon.Z,
			}.Normalize()
			tr := full.NearestDist(vec.Ray{Origin: cam, Dir: rd}, 1e6)
			expect := cam.Dist(moonC) - moonR
			if tr > expect+5 {
				misses++
				continue
			}
			pixTotal++
			hp2 := cam.Add(rd.Scale(tr))
			n2 := hp2.Sub(moonC).Normalize()
			ep2 := hp2.Add(n2.Scale(5e-4))
			ld2 := light.Sub(hp2)
			d2 := ld2.Len()
			dir2 := ld2.Scale(1 / d2)
			if hit := blk.AnyHitDist(vec.Ray{Origin: ep2, Dir: dir2}, d2-0.05); hit < d2-0.05 && hit > 1e-4 {
				shadowPix++
			}
		}
	}
	t.Logf("Moon disc rays: %d misses, %d/%d lit pixels shadowed", misses, shadowPix, pixTotal)
	fmt.Printf("done\n")
}
