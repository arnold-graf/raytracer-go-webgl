package webgpu

import "raytracer/internal/scene"

// Deriving Metal acceleration structures from the BVH this package already builds.
//
// The Metal backend does not build its own tree -- Apple's builder does -- but
// it needs the same *geometry*, cut the same way: which primitives exist, what
// their bounds are, and which of them belong to an instanced template rather
// than the static scene. All of that is already encoded in the packed BVH, so
// this walks it rather than recomputing bounds from primitives, which would be
// a second implementation of something subtle (transformed prims, CSG holes)
// and a source of silent divergence.
//
// The shape it produces:
//
//	Geom[0]      static geometry, leaves carrying absolute prims[] indices
//	Geom[1+t]    template t's geometry, leaves carrying absolute prims[] indices
//	Blockers[..] the same split over the blocker set
//	Instances[0] identity, referencing Geom[0]/Blockers[0]
//	Instances[k] placement k-1's transform, referencing its template
//
// Measured on office-sunset: 15 geometry structures (1 static + 14 templates)
// holding exactly 966 leaves for 966 packed primitives, and 15 blocker
// structures holding 957 leaves for 945 packed blockers -- 945 unique, with 12
// appearing in both the static structure and a template. The overlap mirrors
// the WGSL, whose static blocker tree and instanced blocker search are separate
// walks that can reach the same primitive; on an any-hit shadow query a second
// test cannot change the answer, so it costs work and not correctness. The
// geometry side, which is a nearest query and would care, has no duplicates.
//
// Leaf.Prim is absolute on purpose. Metal reports primitive_id local to its
// geometry and a bounding box intersection function cannot read instance_id, so
// the harness writes these indices into the structure as per-primitive data and
// the intersection function reads them back. See tools/metal-rt/mslpatch.

// AccelLeaf is one primitive: its absolute index and the bounds the BVH gave it.
type AccelLeaf struct {
	Prim     uint32
	Min, Max [3]float32
}

// AccelBLAS is one bottom-level structure's worth of primitives.
type AccelBLAS struct{ Leaves []AccelLeaf }

// AccelInstance is one top-level placement. Xf is object->world, 3 rows of 4,
// which is the layout MTLAccelerationStructureInstanceDescriptor wants and the
// inverse of the world->local transform the WGSL stores.
type AccelInstance struct {
	Xf   [12]float32
	BLAS uint32
}

// AccelPack is everything the Metal harness needs to build its structures.
type AccelPack struct {
	Geom      []AccelBLAS
	Blockers  []AccelBLAS
	Instances []AccelInstance
}

// collectLeaves walks one tree and returns its geometry leaves. TLAS nodes are
// descended but never collected: the primitives under them belong to a
// template's own structure, and Metal reaches them through an instance.
func collectLeaves(nodes []GPUBVHNode, root uint32) []AccelLeaf {
	if int(root) >= len(nodes) {
		return nil
	}
	var out []AccelLeaf
	stack := []uint32{root}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if int(i) >= len(nodes) {
			continue
		}
		n := nodes[i]
		count := n.Info[3]
		if n.Info[2] == bvhTagTLAS {
			if count == 0 {
				stack = append(stack, n.Info[0], n.Info[1])
			}
			continue // an instance leaf: its geometry lives in a template
		}
		if count == 0 {
			stack = append(stack, n.Info[0], n.Info[1])
			continue
		}
		for k := uint32(0); k < count; k++ {
			idx := n.Info[0]
			if k == 1 {
				idx = n.Info[1]
			}
			out = append(out, AccelLeaf{
				Prim: idx,
				Min:  [3]float32{n.Min[0], n.Min[1], n.Min[2]},
				Max:  [3]float32{n.Max[0], n.Max[1], n.Max[2]},
			})
		}
	}
	return out
}

// invertXf turns the WGSL's world->local transform into the object->world one
// Metal wants. The WGSL stores local = M*(world - t) with M's rows in xf0..xf2
// and t in their w components, so object->world is M^-1 with translation t.
func invertXf(r GPUInstanceRecord) ([12]float32, bool) {
	m := [3][3]float64{
		{float64(r.Xf0[0]), float64(r.Xf0[1]), float64(r.Xf0[2])},
		{float64(r.Xf1[0]), float64(r.Xf1[1]), float64(r.Xf1[2])},
		{float64(r.Xf2[0]), float64(r.Xf2[1]), float64(r.Xf2[2])},
	}
	det := m[0][0]*(m[1][1]*m[2][2]-m[1][2]*m[2][1]) -
		m[0][1]*(m[1][0]*m[2][2]-m[1][2]*m[2][0]) +
		m[0][2]*(m[1][0]*m[2][1]-m[1][1]*m[2][0])
	if det == 0 {
		return [12]float32{}, false
	}
	id := 1.0 / det
	var inv [3][3]float64
	inv[0][0] = (m[1][1]*m[2][2] - m[1][2]*m[2][1]) * id
	inv[0][1] = (m[0][2]*m[2][1] - m[0][1]*m[2][2]) * id
	inv[0][2] = (m[0][1]*m[1][2] - m[0][2]*m[1][1]) * id
	inv[1][0] = (m[1][2]*m[2][0] - m[1][0]*m[2][2]) * id
	inv[1][1] = (m[0][0]*m[2][2] - m[0][2]*m[2][0]) * id
	inv[1][2] = (m[0][2]*m[1][0] - m[0][0]*m[1][2]) * id
	inv[2][0] = (m[1][0]*m[2][1] - m[1][1]*m[2][0]) * id
	inv[2][1] = (m[0][1]*m[2][0] - m[0][0]*m[2][1]) * id
	inv[2][2] = (m[0][0]*m[1][1] - m[0][1]*m[1][0]) * id

	// Rows of 4: [ inv | t ].
	t := [3]float64{float64(r.Xf0[3]), float64(r.Xf1[3]), float64(r.Xf2[3])}
	var xf [12]float32
	for row := 0; row < 3; row++ {
		xf[row*4+0] = float32(inv[row][0])
		xf[row*4+1] = float32(inv[row][1])
		xf[row*4+2] = float32(inv[row][2])
		xf[row*4+3] = float32(t[row])
	}
	return xf, true
}

// AccelPackFrom builds the pack from an already-packed scene, which is how both
// the harness and the tests reach it without re-running the scene loader.
func AccelPackFrom(nodes []GPUBVHNode, templates []GPUTemplateRecord, instances []GPUInstanceRecord, blockerSectionStart uint32) *AccelPack {
	p := &AccelPack{}
	p.Geom = append(p.Geom, AccelBLAS{Leaves: collectLeaves(nodes, 0)})
	p.Blockers = append(p.Blockers, AccelBLAS{Leaves: collectLeaves(nodes, blockerSectionStart)})
	for _, t := range templates {
		p.Geom = append(p.Geom, AccelBLAS{Leaves: collectLeaves(nodes, t.BlasRoot)})
		p.Blockers = append(p.Blockers, AccelBLAS{Leaves: collectLeaves(nodes, t.BlockerBlasRoot)})
	}
	// Instance 0 is the static set at identity; mslpatch's generated code maps
	// instance_id 0 to HIT_NO_INSTANCE and k to the WGSL's instance k-1.
	p.Instances = append(p.Instances, AccelInstance{
		Xf:   [12]float32{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0},
		BLAS: 0,
	})
	for _, r := range instances {
		xf, ok := invertXf(r)
		if !ok {
			continue // a singular placement transform cannot be inverted
		}
		p.Instances = append(p.Instances, AccelInstance{Xf: xf, BLAS: r.TemplateID + 1})
	}
	return p
}

// AccelPackForScene builds the Metal structures for a scene, using the same
// packing the WGSL backend uses so the two cannot disagree about geometry.
// ok is false for a scene without instancing, which the harness handles with a
// single static structure and no placements.
func AccelPackForScene(s *scene.Scene) (*AccelPack, []GPUPrimitive, []GPUPrimitive, bool) {
	prims, blockers, nodes, _, _, isp, _, ok := packInstancedScene(s)
	if !ok {
		return nil, nil, nil, false
	}
	return AccelPackFrom(nodes, isp.templates, isp.instances, isp.blockerSectionStart), prims, blockers, true
}
