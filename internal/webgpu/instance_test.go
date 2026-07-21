package webgpu

import (
	"math"
	"path/filepath"
	"testing"

	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func TestTrunkCylinderCPUIntersect(t *testing.T) {
	root := filepath.Join("..", "..")
	sc, err := sceneio.Load(filepath.Join(root, "scenes", "outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := sc.Instancing().Templates[0].Scene
	trunk := tmpl.Cylinders[0]
	r := vec.Ray{Origin: vec.New(3, 3, 0), Dir: vec.New(-1, 0, 0)}
	tHit := trunk.Intersect(r)
	if math.IsInf(tHit, 1) || tHit <= 0 {
		t.Fatalf("template-local trunk miss: t=%v", tHit)
	}
}

func TestTrunkPrimAtTemplateBase(t *testing.T) {
	root := filepath.Join("..", "..")
	sc, err := sceneio.Load(filepath.Join(root, "scenes", "outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	prims, _, _, _, _, isp, _, ok := packInstancedScene(sc)
	if !ok {
		t.Fatal("pack failed")
	}
	base := isp.templates[0].PrimBase
	p := prims[base]
	if p.Meta[0] != primCylinder {
		t.Fatalf("prim at base kind=%d want cylinder(%d)", p.Meta[0], primCylinder)
	}
	if p.GeoA[2] != 0.65 || p.GeoA[3] != 0.4 || p.GeoB[0] != 7.6 {
		t.Fatalf("trunk geo mismatch: geo_a=%v geo_b=%v", p.GeoA, p.GeoB)
	}
	if p.Meta[3]&primFlagTransformed != 0 {
		t.Fatal("trunk should not have transform flag in template")
	}
}

func TestPackInstancedVillaScene(t *testing.T) {
	root := filepath.Join("..", "..")
	villa := filepath.Join(root, "scenes", "outdoors-night-villa.toml")
	sc, err := sceneio.Load(villa)
	if err != nil {
		t.Fatal(err)
	}
	prims, _, nodes, bvhN, blkN, isp, _, ok := packInstancedScene(sc)
	if !ok {
		t.Fatal("packInstancedScene failed")
	}
	if len(prims) > 200 {
		t.Fatalf("prims = %d, want static + ~10 template (not full flat ~150 trees)", len(prims))
	}
	if len(isp.instances) != 75 {
		t.Fatalf("instances = %d, want 75 (pine clusters)", len(isp.instances))
	}
	if len(isp.templates) != 1 {
		t.Fatalf("templates = %d, want 1 (pine-tree.toml)", len(isp.templates))
	}
	if bvhN == 0 || isp.instNodeCount == 0 {
		t.Fatalf("static bvh=%d inst section=%d, want both non-zero", bvhN, isp.instNodeCount)
	}
	if blkN == 0 {
		t.Fatal("expected static blocker BVH")
	}
	if len(nodes) <= int(bvhN) {
		t.Fatalf("combined nodes = %d, want static + inst sections", len(nodes))
	}
	tmpl := sc.Instancing().Templates[0].Scene
	if len(tmpl.Cylinders) != 5 || len(tmpl.Cones) != 5 {
		t.Fatalf("pine template: cyl=%d cone=%d, want 5 each", len(tmpl.Cylinders), len(tmpl.Cones))
	}
	base := isp.templates[0].PrimBase
	var cyl, cone int
	for i := base; i < base+10; i++ {
		switch prims[i].Meta[0] {
		case primCylinder:
			cyl++
		case primCone:
			cone++
		}
	}
	if cyl != 5 || cone != 5 {
		t.Fatalf("packed template kinds: cyl=%d cone=%d", cyl, cone)
	}
}

func TestGPUTemplateAndInstanceLayout(t *testing.T) {
	if got := instTemplateStride; got != 16 {
		t.Fatalf("GPUTemplateRecord stride = %d, want 16", got)
	}
	if got := instanceStride; got != 64 {
		t.Fatalf("GPUInstanceRecord stride = %d, want 64", got)
	}
}

func TestOffsetBLASNodeLeafWithZeroIndex(t *testing.T) {
	n := GPUBVHNode{Info: [4]uint32{3, 0, 0, 2}}
	offsetBLASNode(&n, 100, 50)
	if n.Info[0] != 103 || n.Info[1] != 100 {
		t.Fatalf("leaf prim indices = %d,%d want 103,100", n.Info[0], n.Info[1])
	}
}

func TestInstancedBLASTrunkRayHit(t *testing.T) {
	root := filepath.Join("..", "..")
	sc, err := sceneio.Load(filepath.Join(root, "scenes", "outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	sc.ApplyInstanceTerrainFollow()
	prims, _, nodes, _, _, isp, _, ok := packInstancedScene(sc)
	if !ok {
		t.Fatal("pack failed")
	}
	tmpl := isp.templates[0]
	trunkIdx := tmpl.PrimBase

	// Template-local ray through trunk midsection (same as TestTrunkCylinderCPUIntersect).
	lro := vec.New(3, 3, 0)
	lrd := vec.New(-1, 0, 0)
	tDirect := hitGPUPrim(prims[trunkIdx], lro, lrd)
	if math.IsInf(tDirect, 1) || tDirect >= gpuTMiss {
		t.Fatalf("direct trunk hit miss: t=%v", tDirect)
	}

	tBLAS, idxBLAS := gpuBLASNearest(nodes, tmpl.BlasRoot, prims, lro, lrd, gpuTMiss)
	if idxBLAS != trunkIdx {
		t.Fatalf("BLAS hit idx=%d want trunk %d (t=%v)", idxBLAS, trunkIdx, tBLAS)
	}
	if math.Abs(tBLAS-tDirect) > 1e-3 {
		t.Fatalf("BLAS t=%v direct t=%v", tBLAS, tDirect)
	}

	// World-space ray through first placement's trunk.
	pl := sc.Instancing().Placements[0]
	wr := vec.Ray{
		Origin: pl.Xform.ToWorld(lro),
		Dir:    pl.Xform.RotateDir(lrd),
	}
	tWorld, idxWorld := gpuInstNearest(nodes, isp, prims, wr.Origin, wr.Dir, gpuTMiss)
	if idxWorld != trunkIdx {
		t.Fatalf("inst hit idx=%d want trunk %d (t=%v)", idxWorld, trunkIdx, tWorld)
	}
}

const gpuTMiss = 1e30
const gpuRayEps = 1e-4

func hitGPUPrim(p GPUPrimitive, ro, rd vec.V) float64 {
	lro, lrd := ro, rd
	if p.Meta[3]&primFlagTransformed != 0 {
		lro = xfToLocalPoint(p, ro)
		lrd = xfToLocalDir(p, rd)
	}
	switch p.Meta[0] {
	case primBox:
		return hitGPUBox(p, lro, lrd)
	case primCylinder:
		return hitGPUCylinder(p, lro, lrd)
	case primCone:
		return hitGPUCone(p, lro, lrd)
	default:
		return gpuTMiss
	}
}

func xfToLocalPoint(p GPUPrimitive, wp vec.V) vec.V {
	v := wp.Sub(vec.V{X: float64(p.Xf0[3]), Y: float64(p.Xf1[3]), Z: float64(p.Xf2[3])})
	return vec.V{
		X: float64(p.Xf0[0])*v.X + float64(p.Xf0[1])*v.Y + float64(p.Xf0[2])*v.Z,
		Y: float64(p.Xf1[0])*v.X + float64(p.Xf1[1])*v.Y + float64(p.Xf1[2])*v.Z,
		Z: float64(p.Xf2[0])*v.X + float64(p.Xf2[1])*v.Y + float64(p.Xf2[2])*v.Z,
	}
}

func xfToLocalDir(p GPUPrimitive, wd vec.V) vec.V {
	return vec.V{
		X: float64(p.Xf0[0])*wd.X + float64(p.Xf0[1])*wd.Y + float64(p.Xf0[2])*wd.Z,
		Y: float64(p.Xf1[0])*wd.X + float64(p.Xf1[1])*wd.Y + float64(p.Xf1[2])*wd.Z,
		Z: float64(p.Xf2[0])*wd.X + float64(p.Xf2[1])*wd.Y + float64(p.Xf2[2])*wd.Z,
	}
}

func hitGPUBox(p GPUPrimitive, ro, rd vec.V) float64 {
	min := vec.V{X: float64(p.GeoA[0]), Y: float64(p.GeoA[1]), Z: float64(p.GeoA[2])}
	max := vec.V{X: float64(p.GeoB[0]), Y: float64(p.GeoB[1]), Z: float64(p.GeoB[2])}
	inv := vec.V{X: 1 / rd.X, Y: 1 / rd.Y, Z: 1 / rd.Z}
	t1 := (min.X - ro.X) * inv.X
	t2 := (max.X - ro.X) * inv.X
	if t1 > t2 {
		t1, t2 = t2, t1
	}
	t3 := (min.Y - ro.Y) * inv.Y
	t4 := (max.Y - ro.Y) * inv.Y
	if t3 > t4 {
		t3, t4 = t4, t3
	}
	t5 := (min.Z - ro.Z) * inv.Z
	t6 := (max.Z - ro.Z) * inv.Z
	if t5 > t6 {
		t5, t6 = t6, t5
	}
	tn := math.Max(math.Max(t1, t3), t5)
	tf := math.Min(math.Min(t2, t4), t6)
	if tf < tn || tf < gpuRayEps {
		return gpuTMiss
	}
	if tn < gpuRayEps {
		return tf
	}
	return tn
}

func hitGPUCylinder(p GPUPrimitive, ro, rd vec.V) float64 {
	cx, cz, r0 := float64(p.GeoA[0]), float64(p.GeoA[1]), float64(p.GeoA[2])
	ymin, ymax, r1 := float64(p.GeoA[3]), float64(p.GeoB[0]), float64(p.GeoB[1])
	if r1 == 0 {
		r1 = r0
	}
	h := ymax - ymin
	if h <= 0 {
		return gpuTMiss
	}
	alpha := (r1 - r0) / h
	px, pz := ro.X-cx, ro.Z-cz
	A := r0 + alpha*(ro.Y-ymin)
	B := alpha * rd.Y
	a := rd.X*rd.X + rd.Z*rd.Z - B*B
	b := 2 * (px*rd.X + pz*rd.Z - A*B)
	cc := px*px + pz*pz - A*A
	best := gpuTMiss
	if math.Abs(a) > 1e-12 {
		disc := b*b - 4*a*cc
		if disc >= 0 {
			sq := math.Sqrt(disc)
			for _, t := range []float64{(-b - sq) / (2 * a), (-b + sq) / (2 * a)} {
				if t < gpuRayEps {
					continue
				}
				hy := ro.Y + rd.Y*t
				if hy >= ymin && hy <= ymax && t < best {
					best = t
				}
			}
		}
	}
	return best
}

func hitGPUCone(p GPUPrimitive, ro, rd vec.V) float64 {
	cx, cz, rb := float64(p.GeoA[0]), float64(p.GeoA[1]), float64(p.GeoA[2])
	yb, yt := float64(p.GeoA[3]), float64(p.GeoB[0])
	h := yt - yb
	if h <= 0 {
		return gpuTMiss
	}
	k := rb / h
	ey := ro.Y - yt
	ox, oz := ro.X-cx, ro.Z-cz
	a := rd.X*rd.X + rd.Z*rd.Z - rd.Y*rd.Y*k*k
	b := ox*rd.X + oz*rd.Z - ey*rd.Y*k*k
	cc := ox*ox + oz*oz - ey*ey*k*k

	best := gpuTMiss
	disc := b*b - a*cc
	if disc >= 0 && math.Abs(a) > 1e-12 {
		sq := math.Sqrt(disc)
		for _, t := range []float64{(-b - sq) / a, (-b + sq) / a} {
			if t < gpuRayEps {
				continue
			}
			hy := ro.Y + rd.Y*t
			if hy >= yb && hy <= yt && t < best {
				best = t
			}
		}
	}
	if math.Abs(rd.Y) > 1e-6 {
		tc := (yb - ro.Y) / rd.Y
		if tc >= gpuRayEps && tc < best {
			hx := ro.X + rd.X*tc
			hz := ro.Z + rd.Z*tc
			dd := (hx-cx)*(hx-cx) + (hz-cz)*(hz-cz)
			if dd <= rb*rb {
				best = tc
			}
		}
	}
	return best
}

func gpuSlabHit(nmin, nmax, ro, rd vec.V, tMax float64) bool {
	inv := vec.V{X: 1 / rd.X, Y: 1 / rd.Y, Z: 1 / rd.Z}
	t1 := (nmin.X - ro.X) * inv.X
	t2 := (nmax.X - ro.X) * inv.X
	if t1 > t2 {
		t1, t2 = t2, t1
	}
	t3 := (nmin.Y - ro.Y) * inv.Y
	t4 := (nmax.Y - ro.Y) * inv.Y
	if t3 > t4 {
		t3, t4 = t4, t3
	}
	t5 := (nmin.Z - ro.Z) * inv.Z
	t6 := (nmax.Z - ro.Z) * inv.Z
	if t5 > t6 {
		t5, t6 = t6, t5
	}
	tn := math.Max(math.Max(t1, t3), t5)
	tf := math.Min(math.Min(t2, t4), t6)
	return !(tf < tn || tf < gpuRayEps || tn > tMax)
}

func gpuBLASNearest(nodes []GPUBVHNode, root uint32, prims []GPUPrimitive, ro, rd vec.V, bestT float64) (float64, uint32) {
	stack := []uint32{root}
	best := bestT
	var bestIdx uint32 = 0xffffffff
	for len(stack) > 0 {
		sp := len(stack) - 1
		ni := stack[sp]
		stack = stack[:sp]
		n := nodes[ni]
		nmin := vec.V{X: float64(n.Min[0]), Y: float64(n.Min[1]), Z: float64(n.Min[2])}
		nmax := vec.V{X: float64(n.Max[0]), Y: float64(n.Max[1]), Z: float64(n.Max[2])}
		if !gpuSlabHit(nmin, nmax, ro, rd, best) {
			continue
		}
		count := n.Info[3]
		if count > 0 {
			for k := uint32(0); k < count; k++ {
				pidx := n.Info[0]
				if k == 1 {
					pidx = n.Info[1]
				}
				t := hitGPUPrim(prims[pidx], ro, rd)
				if t < best {
					best = t
					bestIdx = pidx
				}
			}
		} else if n.Info[2] != bvhTagTLAS {
			stack = append(stack, n.Info[1], n.Info[0])
		}
	}
	return best, bestIdx
}

func gpuInstNearest(nodes []GPUBVHNode, isp instScenePack, prims []GPUPrimitive, ro, rd vec.V, bestT float64) (float64, uint32) {
	stack := []uint32{isp.instNodeBase}
	best := bestT
	var bestIdx uint32 = 0xffffffff
	for len(stack) > 0 {
		sp := len(stack) - 1
		ni := stack[sp]
		stack = stack[:sp]
		n := nodes[ni]
		nmin := vec.V{X: float64(n.Min[0]), Y: float64(n.Min[1]), Z: float64(n.Min[2])}
		nmax := vec.V{X: float64(n.Max[0]), Y: float64(n.Max[1]), Z: float64(n.Max[2])}
		if !gpuSlabHit(nmin, nmax, ro, rd, best) {
			continue
		}
		if n.Info[2] == bvhTagTLAS && n.Info[3] > 0 {
			instIdx := n.Info[0]
			tmpl := isp.templates[isp.instances[instIdx].TemplateID]
			lro := instLocalOrigin(isp.instances[instIdx], ro)
			lrd := instLocalDir(isp.instances[instIdx], rd)
			t, idx := gpuBLASNearest(nodes, tmpl.BlasRoot, prims, lro, lrd, best)
			if t < best {
				best = t
				bestIdx = idx
			}
			continue
		}
		if n.Info[2] == bvhTagTLAS && n.Info[3] == 0 {
			stack = append(stack, n.Info[1], n.Info[0])
		}
	}
	return best, bestIdx
}

func instLocalOrigin(rec GPUInstanceRecord, ro vec.V) vec.V {
	v := ro.Sub(vec.V{X: float64(rec.Xf0[3]), Y: float64(rec.Xf1[3]), Z: float64(rec.Xf2[3])})
	return vec.V{
		X: float64(rec.Xf0[0])*v.X + float64(rec.Xf0[1])*v.Y + float64(rec.Xf0[2])*v.Z,
		Y: float64(rec.Xf1[0])*v.X + float64(rec.Xf1[1])*v.Y + float64(rec.Xf1[2])*v.Z,
		Z: float64(rec.Xf2[0])*v.X + float64(rec.Xf2[1])*v.Y + float64(rec.Xf2[2])*v.Z,
	}
}

func instLocalDir(rec GPUInstanceRecord, rd vec.V) vec.V {
	return vec.V{
		X: float64(rec.Xf0[0])*rd.X + float64(rec.Xf0[1])*rd.Y + float64(rec.Xf0[2])*rd.Z,
		Y: float64(rec.Xf1[0])*rd.X + float64(rec.Xf1[1])*rd.Y + float64(rec.Xf1[2])*rd.Z,
		Z: float64(rec.Xf2[0])*rd.X + float64(rec.Xf2[1])*rd.Y + float64(rec.Xf2[2])*rd.Z,
	}
}

// gpuInstBlockerAnyHit mirrors inst_bvh_any_hit in trace.wgsl (shadow blocker TLAS).
func gpuInstBlockerAnyHit(nodes []GPUBVHNode, isp instScenePack, prims []GPUPrimitive, ro, rd vec.V, maxT float64) bool {
	return gpuInstBlockerAnyHitTLAS(nodes, isp, prims, ro, rd, maxT, true)
}

func gpuInstBlockerAnyHitTLAS(nodes []GPUBVHNode, isp instScenePack, prims []GPUPrimitive, ro, rd vec.V, maxT float64, traverseTLASInternal bool) bool {
	stack := []uint32{isp.blockerInstBase}
	for len(stack) > 0 {
		sp := len(stack) - 1
		ni := stack[sp]
		stack = stack[:sp]
		n := nodes[ni]
		nmin := vec.V{X: float64(n.Min[0]), Y: float64(n.Min[1]), Z: float64(n.Min[2])}
		nmax := vec.V{X: float64(n.Max[0]), Y: float64(n.Max[1]), Z: float64(n.Max[2])}
		if !gpuSlabHit(nmin, nmax, ro, rd, maxT) {
			continue
		}
		if n.Info[2] == bvhTagTLAS && n.Info[3] > 0 {
			instIdx := n.Info[0]
			rec := isp.instances[instIdx]
			tmpl := isp.templates[rec.TemplateID]
			lro := instLocalOrigin(rec, ro)
			lrd := instLocalDir(rec, rd)
			if gpuBLASAnyHit(nodes, tmpl.BlockerBlasRoot, prims, lro, lrd, maxT) {
				return true
			}
			continue
		}
		if traverseTLASInternal && n.Info[2] == bvhTagTLAS && n.Info[3] == 0 {
			stack = append(stack, n.Info[1], n.Info[0])
		}
	}
	return false
}

func gpuBLASAnyHit(nodes []GPUBVHNode, root uint32, prims []GPUPrimitive, ro, rd vec.V, maxT float64) bool {
	limit := maxT - 0.05
	stack := []uint32{root}
	for len(stack) > 0 {
		sp := len(stack) - 1
		ni := stack[sp]
		stack = stack[:sp]
		n := nodes[ni]
		nmin := vec.V{X: float64(n.Min[0]), Y: float64(n.Min[1]), Z: float64(n.Min[2])}
		nmax := vec.V{X: float64(n.Max[0]), Y: float64(n.Max[1]), Z: float64(n.Max[2])}
		if !gpuSlabHit(nmin, nmax, ro, rd, maxT) {
			continue
		}
		count := n.Info[3]
		if count > 0 {
			for k := uint32(0); k < count; k++ {
				pidx := n.Info[0]
				if k == 1 {
					pidx = n.Info[1]
				}
				t := hitGPUPrim(prims[pidx], ro, rd)
				if t > gpuRayEps && t < limit {
					return true
				}
			}
		} else if n.Info[2] != bvhTagTLAS {
			stack = append(stack, n.Info[1], n.Info[0])
		}
	}
	return false
}

func TestInstancedBlockerTLASShadowTraversal(t *testing.T) {
	root := filepath.Join("..", "..")
	sc, err := sceneio.Load(filepath.Join(root, "scenes", "office-sunset", "index.toml"))
	if err != nil {
		t.Fatal(err)
	}
	blockers, _, nodes, _, _, isp, _, ok := packInstancedScene(sc)
	if !ok {
		t.Fatal("pack failed")
	}
	if len(isp.instances) < 2 {
		t.Fatalf("instances = %d, want >= 2 for internal blocker TLAS", len(isp.instances))
	}
	if isp.blockerInstCount == 0 {
		t.Fatal("expected instanced blocker BVH section")
	}
	blkRoot := nodes[isp.blockerInstBase]
	if blkRoot.Info[2] != bvhTagTLAS || blkRoot.Info[3] != 0 {
		t.Fatalf("blocker TLAS root tag=%d w=%d, want TLAS internal node", blkRoot.Info[2], blkRoot.Info[3])
	}

	// Downward ray; without internal-node descent the TLAS root is never entered.
	ro := vec.New(10, 220, 10)
	rd := vec.New(0, -1, 0)
	if gpuInstBlockerAnyHitTLAS(nodes, isp, blockers, ro, rd, 50, false) {
		t.Fatal("shadow blocker TLAS traversal without internal nodes should miss")
	}
}
