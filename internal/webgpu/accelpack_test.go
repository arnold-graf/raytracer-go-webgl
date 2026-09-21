package webgpu

import (
	"math"
	"testing"
)

// TestAccelPackCoversEveryPrimitive is the property that matters: every
// primitive the WGSL can reach through the BVH must appear in exactly one
// Metal structure, with the absolute index the intersection function will use.
// A primitive missing here is geometry that silently stops existing on the
// Metal path; a duplicate is one tested twice.
func TestAccelPackCoversEveryPrimitive(t *testing.T) {
	nodes := []GPUBVHNode{
		// 0: root, two children
		{Info: [4]uint32{1, 2, 0, 0}},
		// 1: leaf with two prims
		{Min: [4]float32{-1, -1, -1}, Max: [4]float32{1, 1, 1}, Info: [4]uint32{10, 11, 0, 2}},
		// 2: TLAS internal, descends to 3 and 4
		{Info: [4]uint32{3, 4, bvhTagTLAS, 0}},
		// 3: TLAS instance leaf -- must NOT be collected as static geometry
		{Info: [4]uint32{0, 0, bvhTagTLAS, 1}},
		// 4: ordinary leaf under the TLAS internal node
		{Min: [4]float32{2, 2, 2}, Max: [4]float32{3, 3, 3}, Info: [4]uint32{12, 0, 0, 1}},
	}
	got := collectLeaves(nodes, 0, 0)
	want := map[uint32][2][3]float32{
		10: {{-1, -1, -1}, {1, 1, 1}},
		11: {{-1, -1, -1}, {1, 1, 1}},
		12: {{2, 2, 2}, {3, 3, 3}},
	}
	if len(got) != len(want) {
		t.Fatalf("collected %d leaves, want %d: %+v", len(got), len(want), got)
	}
	for _, l := range got {
		w, ok := want[l.Prim]
		if !ok {
			t.Fatalf("unexpected prim %d", l.Prim)
		}
		if l.Min != w[0] || l.Max != w[1] {
			t.Errorf("prim %d bounds = %v..%v, want %v..%v", l.Prim, l.Min, l.Max, w[0], w[1])
		}
		delete(want, l.Prim)
	}
}

// TestInvertXfRoundTrips checks the transform hand-off. The WGSL stores
// world->local and Metal wants object->world; getting this backwards places
// every instance at the inverse of where it belongs, which looks like a scene
// bug rather than a transform bug.
func TestInvertXfRoundTrips(t *testing.T) {
	// A rotation about Y by 30 degrees, with a translation.
	c, s := math.Cos(math.Pi/6), math.Sin(math.Pi/6)
	rec := GPUInstanceRecord{
		Xf0: [4]float32{float32(c), 0, float32(s), 7},
		Xf1: [4]float32{0, 1, 0, 2},
		Xf2: [4]float32{float32(-s), 0, float32(c), -3},
	}
	xf, ok := invertXf(rec)
	if !ok {
		t.Fatal("invertXf reported a singular matrix for a rotation")
	}
	// Round trip a point: world -> local (as the WGSL does) -> world (as Metal
	// does) must return the original.
	world := [3]float64{1.5, -4, 2.25}
	tr := [3]float64{float64(rec.Xf0[3]), float64(rec.Xf1[3]), float64(rec.Xf2[3])}
	v := [3]float64{world[0] - tr[0], world[1] - tr[1], world[2] - tr[2]}
	local := [3]float64{
		float64(rec.Xf0[0])*v[0] + float64(rec.Xf0[1])*v[1] + float64(rec.Xf0[2])*v[2],
		float64(rec.Xf1[0])*v[0] + float64(rec.Xf1[1])*v[1] + float64(rec.Xf1[2])*v[2],
		float64(rec.Xf2[0])*v[0] + float64(rec.Xf2[1])*v[1] + float64(rec.Xf2[2])*v[2],
	}
	var back [3]float64
	for row := 0; row < 3; row++ {
		back[row] = float64(xf[row*4+0])*local[0] +
			float64(xf[row*4+1])*local[1] +
			float64(xf[row*4+2])*local[2] +
			float64(xf[row*4+3])
	}
	for i := 0; i < 3; i++ {
		if math.Abs(back[i]-world[i]) > 1e-4 {
			t.Fatalf("round trip = %v, want %v", back, world)
		}
	}
}

// TestAccelPackInstanceNumbering pins the convention mslpatch's generated code
// depends on: instance 0 is the static set, and instance k is the WGSL's k-1.
func TestAccelPackInstanceNumbering(t *testing.T) {
	nodes := []GPUBVHNode{{Info: [4]uint32{5, 0, 0, 1}}}
	recs := []GPUInstanceRecord{
		{Xf0: [4]float32{1, 0, 0, 0}, Xf1: [4]float32{0, 1, 0, 0}, Xf2: [4]float32{0, 0, 1, 0}, TemplateID: 0},
		{Xf0: [4]float32{1, 0, 0, 9}, Xf1: [4]float32{0, 1, 0, 0}, Xf2: [4]float32{0, 0, 1, 0}, TemplateID: 1},
	}
	p := AccelPackFrom(nodes, []GPUTemplateRecord{{BlasRoot: 0}, {BlasRoot: 0}}, recs, 0)
	if len(p.Instances) != 3 {
		t.Fatalf("instances = %d, want 3 (static + 2)", len(p.Instances))
	}
	if p.Instances[0].BLAS != 0 {
		t.Errorf("instance 0 BLAS = %d, want 0 (the static set)", p.Instances[0].BLAS)
	}
	if p.Instances[1].BLAS != 1 || p.Instances[2].BLAS != 2 {
		t.Errorf("template BLAS ids = %d,%d, want 1,2", p.Instances[1].BLAS, p.Instances[2].BLAS)
	}
	if p.Instances[2].Xf[3] != 9 {
		t.Errorf("instance 2 translation = %v, want 9", p.Instances[2].Xf[3])
	}
}

// TestBlockerSectionUsesRelativeChildren pins the numbering the blocker tree
// actually uses. Its internal nodes hold section-relative child indices while
// its leaves hold absolute primitive indices, mirroring blocker_bvh_any_hit's
// `blocker_off + n.info.x` push against untouched leaf slots. Reading them as
// absolute silently walks into the main tree and returns geometry leaves for
// the blocker structure, which shadows every ray against the wrong set.
func TestBlockerSectionUsesRelativeChildren(t *testing.T) {
	// nodes 0..1 are a decoy "main tree"; the blocker section starts at 2.
	nodes := []GPUBVHNode{
		{Info: [4]uint32{999, 0, 0, 1}}, // main-tree leaf, must not be collected
		{Info: [4]uint32{998, 0, 0, 1}},
		{Info: [4]uint32{1, 2, 0, 0}}, // 2: blocker root, children RELATIVE -> 3, 4
		{Min: [4]float32{0, 0, 0}, Max: [4]float32{1, 1, 1}, Info: [4]uint32{7, 0, 0, 1}}, // 3
		{Min: [4]float32{1, 1, 1}, Max: [4]float32{2, 2, 2}, Info: [4]uint32{8, 0, 0, 1}}, // 4
	}
	got := collectLeaves(nodes, 2, 2)
	if len(got) != 2 {
		t.Fatalf("collected %d leaves, want 2: %+v", len(got), got)
	}
	for _, l := range got {
		if l.Prim == 999 || l.Prim == 998 {
			t.Fatalf("walked into the main tree and collected prim %d", l.Prim)
		}
		if l.Prim != 7 && l.Prim != 8 {
			t.Errorf("unexpected prim %d", l.Prim)
		}
	}
}
