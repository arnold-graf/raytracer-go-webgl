package webgpu

import (
	"math"
	"sort"
	"testing"

	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

// TestBVHQualityReport is a diagnostic (not an assertion): it prints the SAH
// cost of the packed static BVH plus the primitives whose AABBs dominate it, so
// a human can see whether tree quality or primitive shape is the problem.
//
//	go test ./internal/webgpu -run TestBVHQualityReport -v
func TestBVHQualityReport(t *testing.T) {
	sc, err := sceneio.Load("../../scenes/office-sunset/index.toml")
	if err != nil {
		t.Fatal(err)
	}
	prims := PackPrimitives(sc)
	nodes := PackBVH(prims)
	if len(nodes) == 0 {
		t.Fatal("no nodes")
	}

	rootArea := surfaceArea(
		vecOf(nodes[0].Min), vecOf(nodes[0].Max))

	// SAH cost: sum of hit-probability-weighted work over the tree.
	var interior, leaves, leafPrims float64
	var cost float64
	var walk func(i uint32)
	walk = func(i uint32) {
		n := nodes[i]
		a := surfaceArea(vecOf(n.Min), vecOf(n.Max)) / rootArea
		if n.Info[3] > 0 {
			leaves++
			leafPrims += float64(n.Info[3])
			cost += a * float64(n.Info[3])
			return
		}
		interior++
		cost += a * 1.0
		walk(n.Info[0])
		walk(n.Info[1])
	}
	walk(0)

	t.Logf("static BVH: %d nodes (%.0f interior, %.0f leaves, %.2f prims/leaf)",
		len(nodes), interior, leaves, leafPrims/leaves)
	t.Logf("root AABB surface area: %.1f", rootArea)
	t.Logf("SAH cost (expected node visits + prim tests per random ray): %.2f", cost)

	// Which primitives have the largest AABBs? A handful of building-shell boxes
	// spanning the whole scene would inflate every ancestor node they land in.
	type sized struct {
		idx  int
		area float64
		kind uint32
	}
	var all []sized
	for i := range prims {
		ref, ok := primBounds(uint32(i), &prims[i])
		if !ok {
			continue
		}
		all = append(all, sized{i, surfaceArea(ref.min, ref.max), prims[i].Meta[0]})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].area > all[j].area })

	var totalArea float64
	for _, s := range all {
		totalArea += s.area
	}
	t.Logf("%d finite prims, summed AABB area %.1f (root %.1f)", len(all), totalArea, rootArea)
	t.Logf("largest primitive AABBs (area, %% of root):")
	for i := 0; i < 16 && i < len(all); i++ {
		s := all[i]
		t.Logf("  prim %4d kind %d  area %10.1f  %6.1f%% of root",
			s.idx, s.kind, s.area, 100*s.area/rootArea)
	}

	// Distribution: how many prims exceed a given fraction of the root area?
	for _, frac := range []float64{0.5, 0.25, 0.1, 0.05, 0.01} {
		n := 0
		for _, s := range all {
			if s.area >= frac*rootArea {
				n++
			}
		}
		t.Logf("  prims with AABB >= %5.2f%% of root: %d", frac*100, n)
	}
	_ = math.Inf
}

func vecOf(a [4]float32) vec.V {
	return vec.V{X: float64(a[0]), Y: float64(a[1]), Z: float64(a[2])}
}
