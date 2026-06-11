// Package bvh provides a bounding-volume hierarchy over a scene's finite
// analytic primitives (spheres, boxes, cylinders, cones, tori), replacing the
// per-ray linear scan with an O(log N) traversal. Planes, terrain and water are
// not boundable / are handled separately by the tracer.
//
// Leaves reference primitives by (kind, idx) using the same kind codes the
// tracer's dispatch switch uses (0 sphere, 2 box, 3 cylinder, 4 cone, 5 torus),
// so the result plugs directly into the existing shading path.
package bvh

import (
	"math"
	"sort"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

const eps = 1e-4 // matches scene's ray epsilon

// sahBins is the number of buckets used by the binned surface-area-heuristic
// split. 12 is the usual sweet spot between build cost and tree quality.
const sahBins = 12

// Kind codes, matching trace's dispatch switch.
const (
	KindSphere   = 0
	KindBox      = 2
	KindCylinder = 3
	KindCone     = 4
	KindTorus    = 5
)

const leafSize = 2 // max primitives per leaf

type primRef struct {
	kind     int
	idx      int
	min, max vec.V
	centroid vec.V
}

type node struct {
	min, max     vec.V
	left, right  int32 // child node indices (when count == 0)
	start, count int32 // primitive range (when count > 0)
}

// BVH is an immutable acceleration structure built once for a static scene.
type BVH struct {
	s     *scene.Scene
	nodes []node
	prims []primRef
	// blockers is set for trees built by NewBlockers: every primitive in such a
	// tree is already a valid shadow caster, so AnyHit can skip its per-leaf
	// emissive/torus filter.
	blockers bool
}

// New builds a BVH over all finite primitives of s, used for nearest-hit
// queries. It is safe to call on a scene with no finite primitives (queries
// then simply report misses).
func New(s *scene.Scene) *BVH { return build(s, false) }

// NewBlockers builds a BVH containing only shadow-casting primitives: emissive
// spheres and tori are excluded because the renderer never counts them as
// occluders. The resulting tree is smaller and tighter than the full one, so
// shadow rays (AnyHit) traverse fewer nodes. Shadow results are identical to
// querying the full tree, since AnyHit filtered those same primitives out
// per-leaf anyway.
func NewBlockers(s *scene.Scene) *BVH { return build(s, true) }

func build(s *scene.Scene, blockersOnly bool) *BVH {
	b := &BVH{s: s, blockers: blockersOnly}

	for i := range s.Spheres {
		o := &s.Spheres[i]
		if blockersOnly && o.Mat == scene.MatEmit {
			continue // emissive spheres cast no shadow
		}
		rad := vec.V{X: o.Radius, Y: o.Radius, Z: o.Radius}
		b.add(KindSphere, i, o.Center.Sub(rad), o.Center.Add(rad))
	}
	for i := range s.Boxes {
		o := &s.Boxes[i]
		b.add(KindBox, i, o.Min, o.Max)
	}
	for i := range s.Cylinders {
		o := &s.Cylinders[i]
		b.add(KindCylinder, i,
			vec.V{X: o.CX - o.Radius, Y: o.YMin, Z: o.CZ - o.Radius},
			vec.V{X: o.CX + o.Radius, Y: o.YMax, Z: o.CZ + o.Radius})
	}
	for i := range s.Cones {
		o := &s.Cones[i]
		b.add(KindCone, i,
			vec.V{X: o.CX - o.RBase, Y: o.YBase, Z: o.CZ - o.RBase},
			vec.V{X: o.CX + o.RBase, Y: o.YTip, Z: o.CZ + o.RBase})
	}
	if !blockersOnly {
		for i := range s.Tori {
			o := &s.Tori[i]
			rxz := o.R + o.Rm
			b.add(KindTorus, i,
				vec.V{X: o.Center.X - rxz, Y: o.Center.Y - o.Rm, Z: o.Center.Z - rxz},
				vec.V{X: o.Center.X + rxz, Y: o.Center.Y + o.Rm, Z: o.Center.Z + rxz})
		}
	}

	if len(b.prims) > 0 {
		b.nodes = make([]node, 0, 2*len(b.prims))
		b.buildRange(0, len(b.prims))
	}
	return b
}

// Bounds returns the AABB enclosing all finite primitives. ok is false when the
// scene has no boundable geometry.
func (b *BVH) Bounds() (min, max vec.V, ok bool) {
	if len(b.nodes) == 0 {
		return vec.V{}, vec.V{}, false
	}
	return b.nodes[0].min, b.nodes[0].max, true
}

func (b *BVH) add(kind, idx int, min, max vec.V) {
	b.prims = append(b.prims, primRef{
		kind: kind, idx: idx, min: min, max: max,
		centroid: min.Add(max).Scale(0.5),
	})
}

// buildRange recursively builds the subtree covering prims[start:end] and
// returns the index of its root node.
func (b *BVH) buildRange(start, end int) int32 {
	ni := int32(len(b.nodes))
	b.nodes = append(b.nodes, node{})

	bmin, bmax := b.prims[start].min, b.prims[start].max
	for i := start + 1; i < end; i++ {
		bmin = minV(bmin, b.prims[i].min)
		bmax = maxV(bmax, b.prims[i].max)
	}

	count := end - start
	if count <= leafSize {
		b.nodes[ni] = node{min: bmin, max: bmax, start: int32(start), count: int32(count)}
		return ni
	}

	mid, ok := b.sahSplit(start, end)
	if !ok {
		// Degenerate centroids (everything in one bin): fall back to a balanced
		// median split so the recursion always makes progress.
		mid = b.medianSplit(start, end)
	}

	left := b.buildRange(start, mid)
	right := b.buildRange(mid, end)
	b.nodes[ni] = node{min: bmin, max: bmax, left: left, right: right}
	return ni
}

// sahSplit partitions prims[start:end] in place using a binned surface-area
// heuristic and returns the split index. ok is false when no axis has any
// centroid extent (all primitives coincide), in which case the caller should
// fall back to a median split.
func (b *BVH) sahSplit(start, end int) (mid int, ok bool) {
	cmin, cmax := b.prims[start].centroid, b.prims[start].centroid
	for i := start + 1; i < end; i++ {
		cmin = minV(cmin, b.prims[i].centroid)
		cmax = maxV(cmax, b.prims[i].centroid)
	}

	bestAxis, bestBin := -1, 0
	bestCost := math.Inf(1)
	for axis := 0; axis < 3; axis++ {
		lo := axisVal(cmin, axis)
		extent := axisVal(cmax, axis) - lo
		if extent < 1e-9 {
			continue
		}
		scale := sahBins / extent

		var cnt [sahBins]int
		var bmin, bmax [sahBins]vec.V
		for k := range bmin {
			bmin[k] = vec.V{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)}
			bmax[k] = vec.V{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}
		}
		for i := start; i < end; i++ {
			k := int((axisVal(b.prims[i].centroid, axis) - lo) * scale)
			if k >= sahBins {
				k = sahBins - 1
			}
			cnt[k]++
			bmin[k] = minV(bmin[k], b.prims[i].min)
			bmax[k] = maxV(bmax[k], b.prims[i].max)
		}

		// Prefix sweep: left side area/count for a split after bin k.
		var leftArea [sahBins]float64
		var leftCnt [sahBins]int
		accMin := vec.V{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)}
		accMax := vec.V{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}
		c := 0
		for k := 0; k < sahBins; k++ {
			if cnt[k] > 0 {
				accMin = minV(accMin, bmin[k])
				accMax = maxV(accMax, bmax[k])
			}
			c += cnt[k]
			leftCnt[k] = c
			leftArea[k] = surfaceArea(accMin, accMax)
		}

		// Suffix sweep: right side area/count for everything from bin k on.
		var rightArea [sahBins]float64
		var rightCnt [sahBins]int
		accMin = vec.V{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)}
		accMax = vec.V{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}
		c = 0
		for k := sahBins - 1; k >= 0; k-- {
			if cnt[k] > 0 {
				accMin = minV(accMin, bmin[k])
				accMax = maxV(accMax, bmax[k])
			}
			c += cnt[k]
			rightCnt[k] = c
			rightArea[k] = surfaceArea(accMin, accMax)
		}

		for k := 0; k < sahBins-1; k++ {
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
	scale := sahBins / (axisVal(cmax, bestAxis) - lo)
	mid = start
	for i := start; i < end; i++ {
		k := int((axisVal(b.prims[i].centroid, bestAxis) - lo) * scale)
		if k >= sahBins {
			k = sahBins - 1
		}
		if k <= bestBin {
			b.prims[mid], b.prims[i] = b.prims[i], b.prims[mid]
			mid++
		}
	}
	if mid == start || mid == end {
		return 0, false
	}
	return mid, true
}

// medianSplit sorts prims[start:end] along the widest centroid axis and splits
// down the middle. Used as a fallback when the SAH cannot separate the set.
func (b *BVH) medianSplit(start, end int) int {
	cmin, cmax := b.prims[start].centroid, b.prims[start].centroid
	for i := start + 1; i < end; i++ {
		cmin = minV(cmin, b.prims[i].centroid)
		cmax = maxV(cmax, b.prims[i].centroid)
	}
	ext := cmax.Sub(cmin)
	axis := 0
	if ext.Y > ext.X {
		axis = 1
	}
	if (axis == 0 && ext.Z > ext.X) || (axis == 1 && ext.Z > ext.Y) {
		axis = 2
	}
	sub := b.prims[start:end]
	sort.Slice(sub, func(i, j int) bool {
		return axisVal(sub[i].centroid, axis) < axisVal(sub[j].centroid, axis)
	})
	return (start + end) / 2
}

// surfaceArea returns the (half) surface area of an AABB; the constant 2 factor
// is omitted because only relative SAH costs matter. Empty boxes return 0.
func surfaceArea(min, max vec.V) float64 {
	d := max.Sub(min)
	if d.X < 0 || d.Y < 0 || d.Z < 0 {
		return 0
	}
	return d.X*d.Y + d.Y*d.Z + d.Z*d.X
}

// Nearest returns the nearest finite-primitive hit along r as (t, kind, idx),
// or (Inf, -1, -1) on a miss.
func (b *BVH) Nearest(r vec.Ray) (float64, int, int) {
	tmin := scene.Inf
	kind, idx := -1, -1
	if len(b.nodes) == 0 {
		return tmin, kind, idx
	}
	inv := vec.V{X: 1 / r.Dir.X, Y: 1 / r.Dir.Y, Z: 1 / r.Dir.Z}

	var stack [64]int32
	sp := 0
	stack[sp] = 0
	sp++
	for sp > 0 {
		sp--
		n := &b.nodes[stack[sp]]
		if !slabHit(n.min, n.max, r, inv, tmin) {
			continue
		}
		if n.count > 0 {
			for k := n.start; k < n.start+n.count; k++ {
				p := &b.prims[k]
				if t := b.primIntersect(p, r); t < tmin {
					tmin, kind, idx = t, p.kind, p.idx
				}
			}
		} else {
			stack[sp] = n.left
			sp++
			stack[sp] = n.right
			sp++
		}
	}
	return tmin, kind, idx
}

// NearestDist returns the nearest hit distance along r, capped at maxT (used by
// AO probes; normal/material work is skipped).
func (b *BVH) NearestDist(r vec.Ray, maxT float64) float64 {
	tmin := maxT
	if len(b.nodes) == 0 {
		return tmin
	}
	inv := vec.V{X: 1 / r.Dir.X, Y: 1 / r.Dir.Y, Z: 1 / r.Dir.Z}

	var stack [64]int32
	sp := 0
	stack[sp] = 0
	sp++
	for sp > 0 {
		sp--
		n := &b.nodes[stack[sp]]
		if !slabHit(n.min, n.max, r, inv, tmin) {
			continue
		}
		if n.count > 0 {
			for k := n.start; k < n.start+n.count; k++ {
				p := &b.prims[k]
				if t := b.primIntersect(p, r); t < tmin {
					tmin = t
				}
			}
		} else {
			stack[sp] = n.left
			sp++
			stack[sp] = n.right
			sp++
		}
	}
	return tmin
}

// AnyHit reports whether any shadow-casting primitive blocks the segment
// (eps, maxT-0.05). Emissive spheres and tori are skipped, matching the
// renderer's original shadow rules.
func (b *BVH) AnyHit(r vec.Ray, maxT float64) bool {
	if len(b.nodes) == 0 {
		return false
	}
	limit := maxT - 0.05
	inv := vec.V{X: 1 / r.Dir.X, Y: 1 / r.Dir.Y, Z: 1 / r.Dir.Z}

	var stack [64]int32
	sp := 0
	stack[sp] = 0
	sp++
	for sp > 0 {
		sp--
		n := &b.nodes[stack[sp]]
		if !slabHit(n.min, n.max, r, inv, maxT) {
			continue
		}
		if n.count > 0 {
			for k := n.start; k < n.start+n.count; k++ {
				p := &b.prims[k]
				if !b.blockers {
					if p.kind == KindTorus {
						continue // shadows skip tori
					}
					if p.kind == KindSphere && b.s.Spheres[p.idx].Mat == scene.MatEmit {
						continue
					}
				}
				if t := b.primIntersect(p, r); t > eps && t < limit {
					return true
				}
			}
		} else {
			stack[sp] = n.left
			sp++
			stack[sp] = n.right
			sp++
		}
	}
	return false
}

func (b *BVH) primIntersect(p *primRef, r vec.Ray) float64 {
	s := b.s
	switch p.kind {
	case KindSphere:
		return s.Spheres[p.idx].Intersect(r)
	case KindBox:
		return s.Boxes[p.idx].Intersect(r)
	case KindCylinder:
		return s.Cylinders[p.idx].Intersect(r)
	case KindCone:
		return s.Cones[p.idx].Intersect(r)
	case KindTorus:
		return s.Tori[p.idx].Intersect(r)
	}
	return scene.Inf
}

// slabHit reports whether the ray interval [eps, tMax] intersects the AABB.
func slabHit(bmin, bmax vec.V, r vec.Ray, inv vec.V, tMax float64) bool {
	t1 := (bmin.X - r.Origin.X) * inv.X
	t2 := (bmax.X - r.Origin.X) * inv.X
	if t1 > t2 {
		t1, t2 = t2, t1
	}
	t3 := (bmin.Y - r.Origin.Y) * inv.Y
	t4 := (bmax.Y - r.Origin.Y) * inv.Y
	if t3 > t4 {
		t3, t4 = t4, t3
	}
	t5 := (bmin.Z - r.Origin.Z) * inv.Z
	t6 := (bmax.Z - r.Origin.Z) * inv.Z
	if t5 > t6 {
		t5, t6 = t6, t5
	}
	tn := t1
	if t3 > tn {
		tn = t3
	}
	if t5 > tn {
		tn = t5
	}
	tf := t2
	if t4 < tf {
		tf = t4
	}
	if t6 < tf {
		tf = t6
	}
	if tf < tn || tf < eps || tn > tMax {
		return false
	}
	return true
}

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

func minV(a, b vec.V) vec.V {
	return vec.V{X: fmin(a.X, b.X), Y: fmin(a.Y, b.Y), Z: fmin(a.Z, b.Z)}
}

func maxV(a, b vec.V) vec.V {
	return vec.V{X: fmax(a.X, b.X), Y: fmax(a.Y, b.Y), Z: fmax(a.Z, b.Z)}
}

func fmin(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func fmax(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
