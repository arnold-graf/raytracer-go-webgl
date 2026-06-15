package webgpu

import (
	"math"
	"sort"
	"unsafe"

	"raytracer/internal/gpuscene"
	"raytracer/internal/vec"
)

const (
	maxBVHNodes = maxPrims * 2
	nodeStride  = 48
)

// GPUBVHNode mirrors struct BVHNode in trace.wgsl. Leaves have Info.z/w
// (start/count) set and interior nodes have Info.x/y (left/right) set.
type GPUBVHNode struct {
	Min  [4]float32
	Max  [4]float32
	Info [4]uint32
}

type gpuPrimRef struct {
	idx      uint32
	min, max vec.V
	centroid vec.V
}

// PackBVH builds a SAH BVH over the finite primitives WGSL can currently
// intersect. Infinite planes are intentionally excluded and tested in a separate
// loop, matching the CPU tracer's split between finite BVH and planes. Leaf
// nodes store up to BVHLeafSize primitive indices directly in Info.x/y so the
// shader does not need a separate primitive-reference buffer.
func PackBVH(prims []GPUPrimitive) []GPUBVHNode {
	refs := make([]gpuPrimRef, 0, len(prims))
	for i := range prims {
		ref, ok := primBounds(uint32(i), &prims[i])
		if ok {
			refs = append(refs, ref)
		}
	}
	if len(refs) == 0 {
		return nil
	}

	b := &gpuBVHBuilder{refs: refs, nodes: make([]GPUBVHNode, 0, len(refs)*2)}
	b.buildRange(0, len(refs))
	if len(b.nodes) > maxBVHNodes {
		b.nodes = b.nodes[:maxBVHNodes]
	}
	return b.nodes
}

type gpuBVHBuilder struct {
	refs  []gpuPrimRef
	nodes []GPUBVHNode
}

func (b *gpuBVHBuilder) buildRange(start, end int) int32 {
	ni := int32(len(b.nodes))
	b.nodes = append(b.nodes, GPUBVHNode{})

	bmin, bmax := b.refs[start].min, b.refs[start].max
	for i := start + 1; i < end; i++ {
		bmin = minVec(bmin, b.refs[i].min)
		bmax = maxVec(bmax, b.refs[i].max)
	}

	count := end - start
	if count <= gpuscene.BVHLeafSize {
		var info [4]uint32
		for i := 0; i < count && i < gpuscene.BVHLeafSize; i++ {
			info[i] = b.refs[start+i].idx
		}
		info[3] = uint32(count)
		b.nodes[ni] = GPUBVHNode{
			Min:  vec4(bmin),
			Max:  vec4(bmax),
			Info: info,
		}
		return ni
	}

	mid, ok := b.sahSplit(start, end)
	if !ok {
		mid = b.medianSplit(start, end)
	}
	left := b.buildRange(start, mid)
	right := b.buildRange(mid, end)
	b.nodes[ni] = GPUBVHNode{
		Min:  vec4(bmin),
		Max:  vec4(bmax),
		Info: [4]uint32{uint32(left), uint32(right), 0, 0},
	}
	return ni
}

func (b *gpuBVHBuilder) sahSplit(start, end int) (mid int, ok bool) {
	cmin, cmax := b.refs[start].centroid, b.refs[start].centroid
	for i := start + 1; i < end; i++ {
		cmin = minVec(cmin, b.refs[i].centroid)
		cmax = maxVec(cmax, b.refs[i].centroid)
	}

	bestAxis, bestBin := -1, 0
	bestCost := math.Inf(1)
	for axis := 0; axis < 3; axis++ {
		lo := axisVal(cmin, axis)
		extent := axisVal(cmax, axis) - lo
		if extent < 1e-9 {
			continue
		}
		scale := gpuscene.BVHSAHBins / extent

		var cnt [gpuscene.BVHSAHBins]int
		var binMin, binMax [gpuscene.BVHSAHBins]vec.V
		for k := range binMin {
			binMin[k] = vec.V{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)}
			binMax[k] = vec.V{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}
		}
		for i := start; i < end; i++ {
			k := int((axisVal(b.refs[i].centroid, axis) - lo) * scale)
			if k >= gpuscene.BVHSAHBins {
				k = gpuscene.BVHSAHBins - 1
			}
			cnt[k]++
			binMin[k] = minVec(binMin[k], b.refs[i].min)
			binMax[k] = maxVec(binMax[k], b.refs[i].max)
		}

		var leftArea [gpuscene.BVHSAHBins]float64
		var leftCnt [gpuscene.BVHSAHBins]int
		accMin := vec.V{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)}
		accMax := vec.V{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}
		c := 0
		for k := 0; k < gpuscene.BVHSAHBins; k++ {
			if cnt[k] > 0 {
				accMin = minVec(accMin, binMin[k])
				accMax = maxVec(accMax, binMax[k])
			}
			c += cnt[k]
			leftCnt[k] = c
			leftArea[k] = surfaceArea(accMin, accMax)
		}

		var rightArea [gpuscene.BVHSAHBins]float64
		var rightCnt [gpuscene.BVHSAHBins]int
		accMin = vec.V{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)}
		accMax = vec.V{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}
		c = 0
		for k := gpuscene.BVHSAHBins - 1; k >= 0; k-- {
			if cnt[k] > 0 {
				accMin = minVec(accMin, binMin[k])
				accMax = maxVec(accMax, binMax[k])
			}
			c += cnt[k]
			rightCnt[k] = c
			rightArea[k] = surfaceArea(accMin, accMax)
		}

		for k := 0; k < gpuscene.BVHSAHBins-1; k++ {
			if leftCnt[k] == 0 || rightCnt[k+1] == 0 {
				continue
			}
			cost := leftArea[k]*float64(leftCnt[k]) + rightArea[k+1]*float64(rightCnt[k+1])
			if cost < bestCost {
				bestCost, bestAxis, bestBin = cost, axis, k
			}
		}
	}
	if bestAxis < 0 {
		return 0, false
	}

	lo := axisVal(cmin, bestAxis)
	scale := gpuscene.BVHSAHBins / (axisVal(cmax, bestAxis) - lo)
	mid = start
	for i := start; i < end; i++ {
		k := int((axisVal(b.refs[i].centroid, bestAxis) - lo) * scale)
		if k >= gpuscene.BVHSAHBins {
			k = gpuscene.BVHSAHBins - 1
		}
		if k <= bestBin {
			b.refs[mid], b.refs[i] = b.refs[i], b.refs[mid]
			mid++
		}
	}
	if mid == start || mid == end {
		return 0, false
	}
	return mid, true
}

func (b *gpuBVHBuilder) medianSplit(start, end int) int {
	cmin, cmax := b.refs[start].centroid, b.refs[start].centroid
	for i := start + 1; i < end; i++ {
		cmin = minVec(cmin, b.refs[i].centroid)
		cmax = maxVec(cmax, b.refs[i].centroid)
	}
	ext := cmax.Sub(cmin)
	axis := 0
	if ext.Y > ext.X {
		axis = 1
	}
	if (axis == 0 && ext.Z > ext.X) || (axis == 1 && ext.Z > ext.Y) {
		axis = 2
	}
	sub := b.refs[start:end]
	sort.Slice(sub, func(i, j int) bool {
		return axisVal(sub[i].centroid, axis) < axisVal(sub[j].centroid, axis)
	})
	return (start + end) / 2
}

func primBounds(idx uint32, p *GPUPrimitive) (gpuPrimRef, bool) {
	var min, max vec.V
	switch p.Meta[0] {
	case primSphere:
		c := vec.V{X: float64(p.GeoA[0]), Y: float64(p.GeoA[1]), Z: float64(p.GeoA[2])}
		r := float64(p.GeoA[3])
		rv := vec.V{X: r, Y: r, Z: r}
		min, max = c.Sub(rv), c.Add(rv)
	case primBox:
		min = vec.V{X: float64(p.GeoA[0]), Y: float64(p.GeoA[1]), Z: float64(p.GeoA[2])}
		max = vec.V{X: float64(p.GeoB[0]), Y: float64(p.GeoB[1]), Z: float64(p.GeoB[2])}
	case primCylinder:
		cx, cz, r := float64(p.GeoA[0]), float64(p.GeoA[1]), float64(p.GeoA[2])
		ymin, ymax := float64(p.GeoA[3]), float64(p.GeoB[0])
		min = vec.V{X: cx - r, Y: ymin, Z: cz - r}
		max = vec.V{X: cx + r, Y: ymax, Z: cz + r}
	case primCone:
		cx, cz, rb := float64(p.GeoA[0]), float64(p.GeoA[1]), float64(p.GeoA[2])
		ybase, ytip := float64(p.GeoA[3]), float64(p.GeoB[0])
		min = vec.V{X: cx - rb, Y: ybase, Z: cz - rb}
		max = vec.V{X: cx + rb, Y: ytip, Z: cz + rb}
	case primTorus:
		c := vec.V{X: float64(p.GeoA[0]), Y: float64(p.GeoA[1]), Z: float64(p.GeoA[2])}
		R, rm := float64(p.GeoA[3]), float64(p.GeoB[0])
		rxz := R + rm
		min = vec.V{X: c.X - rxz, Y: c.Y - rm, Z: c.Z - rxz}
		max = vec.V{X: c.X + rxz, Y: c.Y + rm, Z: c.Z + rxz}
	default:
		return gpuPrimRef{}, false
	}

	// For a transformed primitive the geo fields describe a local-space AABB;
	// enclose its eight transformed corners in world space so the bounds match
	// the rotated geometry (and the shader, which intersects in local space).
	if p.Meta[3]&primFlagTransformed != 0 {
		min, max = xformBounds(p, min, max)
	}
	return gpuPrimRef{idx: idx, min: min, max: max, centroid: min.Add(max).Scale(0.5)}, true
}

// xformBounds returns the world-space AABB enclosing the eight corners of the
// local AABB (lmin,lmax) under the primitive's local->world transform.
func xformBounds(p *GPUPrimitive, lmin, lmax vec.V) (vec.V, vec.V) {
	wmin := vec.V{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)}
	wmax := vec.V{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}
	for _, dx := range [2]float64{0, 1} {
		for _, dy := range [2]float64{0, 1} {
			for _, dz := range [2]float64{0, 1} {
				c := xfToWorld(p, vec.V{
					X: lmin.X + dx*(lmax.X-lmin.X),
					Y: lmin.Y + dy*(lmax.Y-lmin.Y),
					Z: lmin.Z + dz*(lmax.Z-lmin.Z),
				})
				wmin = minVec(wmin, c)
				wmax = maxVec(wmax, c)
			}
		}
	}
	return wmin, wmax
}

// xfToWorld maps a local-space point to world space using the stored transform
// (Xf0..Xf2 are the world->local rotation rows + translation in .w; the
// local->world rotation is their transpose). Mirrors xf_world_point in WGSL.
func xfToWorld(p *GPUPrimitive, v vec.V) vec.V {
	return vec.V{
		X: float64(p.Xf0[0])*v.X + float64(p.Xf1[0])*v.Y + float64(p.Xf2[0])*v.Z + float64(p.Xf0[3]),
		Y: float64(p.Xf0[1])*v.X + float64(p.Xf1[1])*v.Y + float64(p.Xf2[1])*v.Z + float64(p.Xf1[3]),
		Z: float64(p.Xf0[2])*v.X + float64(p.Xf1[2])*v.Y + float64(p.Xf2[2])*v.Z + float64(p.Xf2[3]),
	}
}

func nodeBytes(nodes []GPUBVHNode) []byte {
	if len(nodes) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&nodes[0])), len(nodes)*nodeStride)
}

func vec4(v vec.V) [4]float32 { return [4]float32{f(v.X), f(v.Y), f(v.Z), 0} }

func axisVal(v vec.V, axis int) float64 {
	switch axis {
	case 0:
		return v.X
	case 1:
		return v.Y
	default:
		return v.Z
	}
}

func minVec(a, b vec.V) vec.V {
	return vec.V{X: math.Min(a.X, b.X), Y: math.Min(a.Y, b.Y), Z: math.Min(a.Z, b.Z)}
}

func maxVec(a, b vec.V) vec.V {
	return vec.V{X: math.Max(a.X, b.X), Y: math.Max(a.Y, b.Y), Z: math.Max(a.Z, b.Z)}
}

func surfaceArea(min, max vec.V) float64 {
	d := max.Sub(min)
	if d.X < 0 || d.Y < 0 || d.Z < 0 {
		return 0
	}
	return d.X*d.Y + d.Y*d.Z + d.Z*d.X
}
