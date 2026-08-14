package webgpu

import (
	"testing"

	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func TestGPUBlockerBVHSouthWall(t *testing.T) {
	sc, err := sceneio.Load("../../scenes/office-sunset/index.toml")
	if err != nil {
		t.Fatal(err)
	}
	blockers := PackBlockers(sc)
	nodes := PackBVH(blockers)
	origin := vec.V{X: 2.0, Y: 201.0, Z: 19.5}
	out := vec.V{X: 2.0, Y: 204.0, Z: 30.0}
	dir := out.Sub(origin)
	maxT := dir.Len()
	dir = dir.Scale(1.0 / maxT)
	ray := vec.Ray{Origin: origin, Dir: dir}

	if !blockerBVHAnyHit(nodes, blockers, ray, maxT) {
		t.Fatal("GPU-packed blocker BVH should hit south wall on exit ray")
	}
	if blockerBVHAnyHitOrdered(nodes, blockers, ray, maxT) != blockerBVHAnyHit(nodes, blockers, ray, maxT) {
		t.Fatal("ordered traversal must match unordered for shadow occlusion")
	}
}

func blockerBVHAnyHit(nodes []GPUBVHNode, blockers []GPUPrimitive, r vec.Ray, maxT float64) bool {
	return blockerBVHAnyHitTraverse(nodes, blockers, r, maxT, false)
}

func blockerBVHAnyHitOrdered(nodes []GPUBVHNode, blockers []GPUPrimitive, r vec.Ray, maxT float64) bool {
	return blockerBVHAnyHitTraverse(nodes, blockers, r, maxT, true)
}

func blockerBVHAnyHitTraverse(nodes []GPUBVHNode, blockers []GPUPrimitive, r vec.Ray, maxT float64, ordered bool) bool {
	if len(nodes) == 0 {
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
		n := &nodes[stack[sp]]
		if !cpuSlabHit(vecFrom4(n.Min), vecFrom4(n.Max), r, inv, maxT) {
			continue
		}
		count := n.Info[3]
		if count > 0 {
			for j := uint32(0); j < count; j++ {
				idx := n.Info[j]
				if int(idx) >= len(blockers) {
					continue
				}
				t := blockerPrimHit(&blockers[idx], r)
				if t > 1e-4 && t < limit {
					return true
				}
			}
		} else if sp+2 <= 64 {
			left := int(n.Info[0])
			right := int(n.Info[1])
			if ordered {
				near, far := bvhChildOrder(nodes, left, right, r, inv)
				stack[sp] = int32(far)
				sp++
				stack[sp] = int32(near)
				sp++
			} else {
				stack[sp] = int32(left)
				sp++
				stack[sp] = int32(right)
				sp++
			}
		}
	}
	return false
}

func bvhChildOrder(nodes []GPUBVHNode, left, right int, r vec.Ray, inv vec.V) (near, far int) {
	tL := cpuSlabNear(vecFrom4(nodes[left].Min), vecFrom4(nodes[left].Max), r, inv)
	tR := cpuSlabNear(vecFrom4(nodes[right].Min), vecFrom4(nodes[right].Max), r, inv)
	if tL <= tR {
		return left, right
	}
	return right, left
}

func blockerPrimHit(p *GPUPrimitive, r vec.Ray) float64 {
	ref, ok := primBounds(0, p)
	if !ok {
		return 1e30
	}
	inv := vec.V{X: 1 / r.Dir.X, Y: 1 / r.Dir.Y, Z: 1 / r.Dir.Z}
	tn := cpuSlabNear(ref.min, ref.max, r, inv)
	if tn > 1e20 {
		return 1e30
	}
	return tn
}

func cpuSlabNear(bmin, bmax vec.V, r vec.Ray, inv vec.V) float64 {
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
	tn := max3(t1, t3, t5)
	tf := min3(t2, t4, t6)
	if tf < tn || tf < 1e-4 {
		return 1e30
	}
	return tn
}

func cpuSlabHit(bmin, bmax vec.V, r vec.Ray, inv vec.V, tMax float64) bool {
	tn := cpuSlabNear(bmin, bmax, r, inv)
	return tn <= tMax && tn < 1e20
}

func max3(a, b, c float64) float64 {
	if a > b {
		if a > c {
			return a
		}
		return c
	}
	if b > c {
		return b
	}
	return c
}

func min3(a, b, c float64) float64 {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
