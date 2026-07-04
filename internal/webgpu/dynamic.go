package webgpu

import "raytracer/internal/scene"

// gpuIndexMap maps scene primitive indices to packed GPU primitive indices.
// Used for instanced scenes where dynamic NPC geometry is appended after the
// static prefix (templates use separate BLAS entries).
type gpuIndexMap struct {
	sphere        map[int]int
	box           map[int]int
	cylinder      map[int]int
	lens          map[int]int
	primToBlocker map[int]int // prim GPU index -> blocker GPU index
}

func dynamicIndexSets(s *scene.Scene) (spheres, boxes, cylinders, lenses map[int]struct{}) {
	spheres, boxes, cylinders, lenses = map[int]struct{}{}, map[int]struct{}{}, map[int]struct{}{}, map[int]struct{}{}
	if s == nil {
		return
	}
	for _, db := range s.DynamicBodies {
		for i := db.Spheres[0]; i < db.Spheres[1]; i++ {
			spheres[i] = struct{}{}
		}
		for i := db.Boxes[0]; i < db.Boxes[1]; i++ {
			boxes[i] = struct{}{}
		}
		for i := db.Cylinders[0]; i < db.Cylinders[1]; i++ {
			cylinders[i] = struct{}{}
		}
		for i := db.Lenses[0]; i < db.Lenses[1]; i++ {
			lenses[i] = struct{}{}
		}
	}
	return
}

func appendDynamicBodyPrimitives(s *scene.Scene, prims []GPUPrimitive) ([]GPUPrimitive, gpuIndexMap) {
	sphSet, boxSet, cylSet, lensSet := dynamicIndexSets(s)
	m := gpuIndexMap{
		sphere:   map[int]int{},
		box:      map[int]int{},
		cylinder: map[int]int{},
		lens:     map[int]int{},
	}
	if s == nil {
		return prims, m
	}
	for i := range s.Spheres {
		if _, ok := sphSet[i]; !ok {
			continue
		}
		sp := &s.Spheres[i]
		m.sphere[i] = len(prims)
		p := GPUPrimitive{
			GeoA:   [4]float32{f(sp.Center.X), f(sp.Center.Y), f(sp.Center.Z), f(sp.Radius)},
			Albedo: albedo(sp.Albedo),
			Params: surfaceParams(sp.Surface),
			Meta:   [4]uint32{primSphere, uint32(sp.Mat), uint32(sp.Tex), 0},
		}
		setXform(&p, sp.Xform)
		prims = append(prims, p)
	}
	for i := range s.Boxes {
		if _, ok := boxSet[i]; !ok {
			continue
		}
		m.box[i] = len(prims)
		prims = append(prims, boxPrim(&s.Boxes[i], boxHoleStart(s, i)))
	}
	for i := range s.Cylinders {
		if _, ok := cylSet[i]; !ok {
			continue
		}
		m.cylinder[i] = len(prims)
		prims = append(prims, cylinderPrim(&s.Cylinders[i]))
	}
	for i := range s.Lenses {
		if _, ok := lensSet[i]; !ok {
			continue
		}
		m.lens[i] = len(prims)
		prims = append(prims, lensPrim(&s.Lenses[i]))
	}
	if len(prims) > maxPrims {
		prims = prims[:maxPrims]
	}
	return prims, m
}

func appendDynamicBodyBlockers(s *scene.Scene, blockers []GPUPrimitive) ([]GPUPrimitive, gpuIndexMap) {
	sphSet, boxSet, cylSet, _ := dynamicIndexSets(s)
	m := gpuIndexMap{
		sphere:   map[int]int{},
		box:      map[int]int{},
		cylinder: map[int]int{},
		lens:     map[int]int{},
	}
	if s == nil {
		return blockers, m
	}
	for i := range s.Spheres {
		if _, ok := sphSet[i]; !ok {
			continue
		}
		sp := &s.Spheres[i]
		if sp.Mat == scene.MatEmit || sp.Mat == scene.MatGlass {
			continue
		}
		m.sphere[i] = len(blockers)
		p := GPUPrimitive{
			GeoA:   [4]float32{f(sp.Center.X), f(sp.Center.Y), f(sp.Center.Z), f(sp.Radius)},
			Albedo: albedo(sp.Albedo),
			Params: surfaceParams(sp.Surface),
			Meta:   [4]uint32{primSphere, uint32(sp.Mat), uint32(sp.Tex), 0},
		}
		setXform(&p, sp.Xform)
		blockers = append(blockers, p)
	}
	for i := range s.Boxes {
		if _, ok := boxSet[i]; !ok {
			continue
		}
		bx := &s.Boxes[i]
		if bx.Mat == scene.MatGlass {
			continue
		}
		m.box[i] = len(blockers)
		blockers = append(blockers, boxPrim(bx, boxHoleStart(s, i)))
	}
	for i := range s.Cylinders {
		if _, ok := cylSet[i]; !ok {
			continue
		}
		cy := &s.Cylinders[i]
		if cy.Mat == scene.MatGlass {
			continue
		}
		m.cylinder[i] = len(blockers)
		blockers = append(blockers, cylinderPrim(cy))
	}
	return blockers, m
}

func linkPrimToBlocker(prim, blocker gpuIndexMap) map[int]int {
	out := map[int]int{}
	for sceneIdx, primIdx := range prim.sphere {
		if blkIdx, ok := blocker.sphere[sceneIdx]; ok {
			out[primIdx] = blkIdx
		}
	}
	for sceneIdx, primIdx := range prim.box {
		if blkIdx, ok := blocker.box[sceneIdx]; ok {
			out[primIdx] = blkIdx
		}
	}
	for sceneIdx, primIdx := range prim.cylinder {
		if blkIdx, ok := blocker.cylinder[sceneIdx]; ok {
			out[primIdx] = blkIdx
		}
	}
	return out
}

func buildDynamicGPUMaps(s *scene.Scene) gpuIndexMap {
	if s == nil || len(s.DynamicBodies) == 0 {
		return gpuIndexMap{}
	}
	primPrefix := packPrimitivesWithoutDynamic(s)
	blkPrefix := packBlockersWithoutDynamic(s)
	_, primMap := appendDynamicBodyPrimitives(s, primPrefix)
	_, blkMap := appendDynamicBodyBlockers(s, blkPrefix)
	primMap.primToBlocker = linkPrimToBlocker(primMap, blkMap)
	return primMap
}

func packPrimitivesWithoutDynamic(s *scene.Scene) []GPUPrimitive {
	return packPrimitivesOmitDynamic(s, s)
}

func packPrimitivesOmitDynamic(s *scene.Scene, skipFrom *scene.Scene) []GPUPrimitive {
	if s == nil {
		return nil
	}
	sph, box, cyl, lens := dynamicIndexSets(skipFrom)
	out := make([]GPUPrimitive, 0, len(s.Spheres)+len(s.Planes)+len(s.Boxes))
	for i := range s.Spheres {
		if _, skip := sph[i]; skip {
			continue
		}
		sp := &s.Spheres[i]
		p := GPUPrimitive{
			GeoA:   [4]float32{f(sp.Center.X), f(sp.Center.Y), f(sp.Center.Z), f(sp.Radius)},
			Albedo: albedo(sp.Albedo),
			Params: surfaceParams(sp.Surface),
			Meta:   [4]uint32{primSphere, uint32(sp.Mat), uint32(sp.Tex), 0},
		}
		setXform(&p, sp.Xform)
		out = append(out, p)
	}
	for i := range s.Planes {
		pl := &s.Planes[i]
		out = append(out, GPUPrimitive{
			GeoA:    [4]float32{f(pl.N.X), f(pl.N.Y), f(pl.N.Z), f(pl.D)},
			Albedo:  albedo(pl.Albedo),
			Albedo2: albedo(pl.Albedo2),
			Params:  surfaceParams(pl.Surface),
			Meta:    [4]uint32{primPlane, uint32(pl.Mat), uint32(pl.Tex), 0},
		})
	}
	holeStart := uint32(0)
	for i := range s.Boxes {
		if _, skip := box[i]; skip {
			holeStart += uint32(len(s.Boxes[i].Holes))
			continue
		}
		out = append(out, boxPrim(&s.Boxes[i], holeStart))
		holeStart += uint32(len(s.Boxes[i].Holes))
	}
	for i := range s.Cylinders {
		if _, skip := cyl[i]; skip {
			continue
		}
		out = append(out, cylinderPrim(&s.Cylinders[i]))
	}
	for i := range s.Cones {
		out = append(out, conePrim(&s.Cones[i]))
	}
	for i := range s.Tori {
		out = append(out, torusPrim(&s.Tori[i]))
	}
	for i := range s.Rings {
		out = append(out, ringPrim(&s.Rings[i]))
	}
	for i := range s.Lenses {
		if _, skip := lens[i]; skip {
			continue
		}
		out = append(out, lensPrim(&s.Lenses[i]))
	}
	return out
}

func packBlockersWithoutDynamic(s *scene.Scene) []GPUPrimitive {
	return packBlockersOmitDynamic(s, s)
}

func packBlockersOmitDynamic(s *scene.Scene, skipFrom *scene.Scene) []GPUPrimitive {
	if s == nil {
		return nil
	}
	sph, box, cyl, lens := dynamicIndexSets(skipFrom)
	out := make([]GPUPrimitive, 0, len(s.Spheres)+len(s.Planes)+len(s.Boxes))
	for i := range s.Spheres {
		if _, skip := sph[i]; skip {
			continue
		}
		sp := &s.Spheres[i]
		if sp.Mat == scene.MatEmit || sp.Mat == scene.MatGlass {
			continue
		}
		p := GPUPrimitive{
			GeoA:   [4]float32{f(sp.Center.X), f(sp.Center.Y), f(sp.Center.Z), f(sp.Radius)},
			Albedo: albedo(sp.Albedo),
			Params: surfaceParams(sp.Surface),
			Meta:   [4]uint32{primSphere, uint32(sp.Mat), uint32(sp.Tex), 0},
		}
		setXform(&p, sp.Xform)
		out = append(out, p)
	}
	for i := range s.Planes {
		pl := &s.Planes[i]
		out = append(out, GPUPrimitive{
			GeoA:    [4]float32{f(pl.N.X), f(pl.N.Y), f(pl.N.Z), f(pl.D)},
			Albedo:  albedo(pl.Albedo),
			Albedo2: albedo(pl.Albedo2),
			Params:  surfaceParams(pl.Surface),
			Meta:    [4]uint32{primPlane, uint32(pl.Mat), uint32(pl.Tex), 0},
		})
	}
	holeStart := uint32(0)
	for i := range s.Boxes {
		if _, skip := box[i]; skip {
			holeStart += uint32(len(s.Boxes[i].Holes))
			continue
		}
		bx := &s.Boxes[i]
		if bx.Mat != scene.MatGlass {
			out = append(out, boxPrim(bx, holeStart))
		}
		holeStart += uint32(len(bx.Holes))
	}
	for i := range s.Cylinders {
		if _, skip := cyl[i]; skip {
			continue
		}
		cy := &s.Cylinders[i]
		if cy.Mat == scene.MatGlass {
			continue
		}
		out = append(out, cylinderPrim(cy))
	}
	for i := range s.Cones {
		co := &s.Cones[i]
		if co.Mat == scene.MatGlass {
			continue
		}
		out = append(out, conePrim(co))
	}
	for i := range s.Lenses {
		if _, skip := lens[i]; skip {
			continue
		}
		ln := &s.Lenses[i]
		if ln.Mat == scene.MatGlass {
			continue
		}
		out = append(out, lensPrim(ln))
	}
	return out
}

func repackSphere(s *scene.Scene, sceneIdx int, dst *GPUPrimitive) {
	if s == nil || dst == nil || sceneIdx < 0 || sceneIdx >= len(s.Spheres) {
		return
	}
	sp := &s.Spheres[sceneIdx]
	*dst = GPUPrimitive{
		GeoA:   [4]float32{f(sp.Center.X), f(sp.Center.Y), f(sp.Center.Z), f(sp.Radius)},
		Albedo: albedo(sp.Albedo),
		Params: surfaceParams(sp.Surface),
		Meta:   [4]uint32{primSphere, uint32(sp.Mat), uint32(sp.Tex), 0},
	}
	setXform(dst, sp.Xform)
}

func repackBox(s *scene.Scene, sceneIdx int, dst *GPUPrimitive) {
	if s == nil || dst == nil || sceneIdx < 0 || sceneIdx >= len(s.Boxes) {
		return
	}
	*dst = boxPrim(&s.Boxes[sceneIdx], boxHoleStart(s, sceneIdx))
}

func repackCylinder(s *scene.Scene, sceneIdx int, dst *GPUPrimitive) {
	if s == nil || dst == nil || sceneIdx < 0 || sceneIdx >= len(s.Cylinders) {
		return
	}
	*dst = cylinderPrim(&s.Cylinders[sceneIdx])
}

func repackLens(s *scene.Scene, sceneIdx int, dst *GPUPrimitive) {
	if s == nil || dst == nil || sceneIdx < 0 || sceneIdx >= len(s.Lenses) {
		return
	}
	*dst = lensPrim(&s.Lenses[sceneIdx])
}
