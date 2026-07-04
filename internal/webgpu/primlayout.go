package webgpu

import (
	"sort"

	"raytracer/internal/scene"
)

// primLayout maps scene primitive slice indices to packed GPU primitive indices.
// PackPrimitives emits spheres, planes, boxes, cylinders, cones, tori, then rings.
type primLayout struct {
	nSphere, nPlane, nBox, nCylinder, nCone, nTorus, nRing, nLens int
	gpu gpuIndexMap // set for instanced scenes with dynamic NPC geometry
}

func computePrimLayout(s *scene.Scene) primLayout {
	if s == nil {
		return primLayout{}
	}
	return primLayout{
		nSphere:   len(s.Spheres),
		nPlane:    len(s.Planes),
		nBox:      len(s.Boxes),
		nCylinder: len(s.Cylinders),
		nCone:     len(s.Cones),
		nTorus:    len(s.Tori),
		nRing:     len(s.Rings),
		nLens:     len(s.Lenses),
	}
}

func (l primLayout) count() int {
	return l.nSphere + l.nPlane + l.nBox + l.nCylinder + l.nCone + l.nTorus + l.nRing + l.nLens
}

func (l primLayout) sphereGPU(sceneIdx int) (int, bool) {
	if len(l.gpu.sphere) > 0 {
		gi, ok := l.gpu.sphere[sceneIdx]
		return gi, ok
	}
	if sceneIdx >= 0 && sceneIdx < l.nSphere {
		return sceneIdx, true
	}
	return 0, false
}

func (l primLayout) boxGPU(sceneIdx int) (int, bool) {
	if len(l.gpu.box) > 0 {
		gi, ok := l.gpu.box[sceneIdx]
		return gi, ok
	}
	if sceneIdx >= 0 && sceneIdx < l.nBox {
		return l.nSphere + l.nPlane + sceneIdx, true
	}
	return 0, false
}

func (l primLayout) cylinderGPU(sceneIdx int) (int, bool) {
	if len(l.gpu.cylinder) > 0 {
		gi, ok := l.gpu.cylinder[sceneIdx]
		return gi, ok
	}
	if sceneIdx >= 0 && sceneIdx < l.nCylinder {
		return l.nSphere + l.nPlane + l.nBox + sceneIdx, true
	}
	return 0, false
}

func (l primLayout) lensGPU(sceneIdx int) (int, bool) {
	if len(l.gpu.lens) > 0 {
		gi, ok := l.gpu.lens[sceneIdx]
		return gi, ok
	}
	if sceneIdx >= 0 && sceneIdx < l.nLens {
		return l.nSphere + l.nPlane + l.nBox + l.nCylinder + l.nCone + l.nTorus + l.nRing + sceneIdx, true
	}
	return 0, false
}

func dynamicGPUIndices(s *scene.Scene, l primLayout) []int {
	if s == nil {
		return nil
	}
	var out []int
	for _, db := range s.DynamicBodies {
		for i := db.Spheres[0]; i < db.Spheres[1]; i++ {
			if gi, ok := l.sphereGPU(i); ok {
				out = append(out, gi)
			}
		}
		for i := db.Boxes[0]; i < db.Boxes[1]; i++ {
			if gi, ok := l.boxGPU(i); ok {
				out = append(out, gi)
			}
		}
		for i := db.Cylinders[0]; i < db.Cylinders[1]; i++ {
			if gi, ok := l.cylinderGPU(i); ok {
				out = append(out, gi)
			}
		}
		for i := db.Lenses[0]; i < db.Lenses[1]; i++ {
			if gi, ok := l.lensGPU(i); ok {
				out = append(out, gi)
			}
		}
	}
	return out
}

func repackGPUPrim(s *scene.Scene, l primLayout, gpuIdx int, dst *GPUPrimitive) {
	if s == nil || dst == nil || gpuIdx < 0 {
		return
	}

	if gpuIdx < l.nSphere {
		sp := &s.Spheres[gpuIdx]
		*dst = GPUPrimitive{
			GeoA:   [4]float32{f(sp.Center.X), f(sp.Center.Y), f(sp.Center.Z), f(sp.Radius)},
			Albedo: albedo(sp.Albedo),
			Params: surfaceParams(sp.Surface),
			Meta:   [4]uint32{primSphere, uint32(sp.Mat), uint32(sp.Tex), 0},
		}
		setXform(dst, sp.Xform)
		return
	}
	gpuIdx -= l.nSphere
	if gpuIdx < l.nPlane {
		pl := &s.Planes[gpuIdx]
		*dst = GPUPrimitive{
			GeoA:    [4]float32{f(pl.N.X), f(pl.N.Y), f(pl.N.Z), f(pl.D)},
			Albedo:  albedo(pl.Albedo),
			Albedo2: albedo(pl.Albedo2),
			Params:  surfaceParams(pl.Surface),
			Meta:    [4]uint32{primPlane, uint32(pl.Mat), uint32(pl.Tex), 0},
		}
		return
	}
	gpuIdx -= l.nPlane
	if gpuIdx < l.nBox {
		*dst = boxPrim(&s.Boxes[gpuIdx], boxHoleStart(s, gpuIdx))
		return
	}
	gpuIdx -= l.nBox
	if gpuIdx < l.nCylinder {
		*dst = cylinderPrim(&s.Cylinders[gpuIdx])
		return
	}
	gpuIdx -= l.nCylinder
	if gpuIdx < l.nCone {
		*dst = conePrim(&s.Cones[gpuIdx])
		return
	}
	gpuIdx -= l.nCone
	if gpuIdx < l.nTorus {
		*dst = torusPrim(&s.Tori[gpuIdx])
		return
	}
	gpuIdx -= l.nTorus
	if gpuIdx < l.nRing {
		*dst = ringPrim(&s.Rings[gpuIdx])
		return
	}
	gpuIdx -= l.nRing
	if gpuIdx < l.nLens {
		*dst = lensPrim(&s.Lenses[gpuIdx])
	}
}

func boxHoleStart(s *scene.Scene, boxIndex int) uint32 {
	var start uint32
	for i := 0; i < boxIndex && i < len(s.Boxes); i++ {
		start += uint32(len(s.Boxes[i].Holes))
	}
	return start
}

// coalesceIndices merges GPU primitive indices into contiguous [start,end) spans.
func coalesceIndices(idxs []int) [][2]int {
	if len(idxs) == 0 {
		return nil
	}
	sorted := append([]int(nil), idxs...)
	sort.Ints(sorted)
	var spans [][2]int
	start, prev := sorted[0], sorted[0]
	for _, idx := range sorted[1:] {
		if idx == prev+1 {
			prev = idx
			continue
		}
		spans = append(spans, [2]int{start, prev + 1})
		start, prev = idx, idx
	}
	spans = append(spans, [2]int{start, prev + 1})
	return spans
}
