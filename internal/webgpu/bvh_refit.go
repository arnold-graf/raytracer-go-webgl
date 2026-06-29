package webgpu

import (
	"raytracer/internal/gpuscene"
	"raytracer/internal/vec"
)

// RefitBVH updates leaf AABBs for moved primitives and refits interior nodes
// bottom-up. Topology is unchanged so this is O(nodes) vs a full SAH rebuild.
func RefitBVH(nodes []GPUBVHNode, prims []GPUPrimitive) {
	if len(nodes) == 0 || len(prims) == 0 {
		return
	}
	for i := range nodes {
		count := nodes[i].Info[3]
		if count == 0 || count > gpuscene.BVHLeafSize {
			continue
		}
		refitLeaf(&nodes[i], prims)
	}
	for i := len(nodes) - 1; i >= 0; i-- {
		count := nodes[i].Info[3]
		if count != 0 && count <= gpuscene.BVHLeafSize {
			continue
		}
		left := nodes[i].Info[0]
		right := nodes[i].Info[1]
		if int(left) >= len(nodes) || int(right) >= len(nodes) {
			continue
		}
		l, r := nodes[left], nodes[right]
		bmin := minVec(vecFrom4(l.Min), vecFrom4(r.Min))
		bmax := maxVec(vecFrom4(l.Max), vecFrom4(r.Max))
		nodes[i].Min = vec4(bmin)
		nodes[i].Max = vec4(bmax)
	}
}

func refitLeaf(node *GPUBVHNode, prims []GPUPrimitive) {
	count := node.Info[3]
	bmin := vec.V{X: 1e30, Y: 1e30, Z: 1e30}
	bmax := vec.V{X: -1e30, Y: -1e30, Z: -1e30}
	ok := false
	for j := uint32(0); j < count; j++ {
		idx := node.Info[j]
		if int(idx) >= len(prims) {
			continue
		}
		ref, boundsOK := primBounds(idx, &prims[idx])
		if !boundsOK {
			continue
		}
		bmin = minVec(bmin, ref.min)
		bmax = maxVec(bmax, ref.max)
		ok = true
	}
	if !ok {
		return
	}
	node.Min = vec4(bmin)
	node.Max = vec4(bmax)
}

func vecFrom4(v [4]float32) vec.V {
	return vec.V{X: float64(v[0]), Y: float64(v[1]), Z: float64(v[2])}
}
