package webgpu

import (
	"raytracer/internal/render"
	"raytracer/internal/scene"
)

// sceneCache memoizes the scene-derived GPU buffers (everything that depends
// only on geometry/materials, not on the camera or animation clock) so a static
// scene is packed and uploaded once instead of every frame. The big wins are
// the primitive/BVH builds and the terrain heightfield, which can be megabytes.
//
// Invalidation is keyed on (scene pointer, scene.Generation()) for full rebuilds
// and scene.TransformGeneration() for pose-only updates. Touch() bumps both;
// TouchTransforms() bumps only TransformGeneration so dynamic NPC poses can
// partially re-upload + refit the BVH without re-packing static geometry.
type sceneCache struct {
	scene    *scene.Scene
	gen      uint64
	xformGen uint64
	valid    bool

	layout              primLayout
	partialPrimSpans    [][2]int // coalesced GPU prim index spans dirtied last partial update
	partialBlockerSpans [][2]int

	prims    []GPUPrimitive
	blockers []GPUPrimitive
	// planeIdx / blockerPlaneIdx list the indices of infinite planes within
	// prims / blockers. Planes are excluded from the BVH, so the shader walks
	// these lists rather than scanning the whole primitive buffer each ray.
	planeIdx         []uint32
	blockerPlaneIdx  []uint32
	bvhNodes         []GPUBVHNode
	bvhNodeCount     uint32
	blockerNodeCount uint32
	lights           []GPULight
	terrains         []GPUTerrain
	samples          []float32
	terrainFeatures  []GPUTerrainFeature
	terrainPads      []GPUTerrainPad
	waters           []GPUWater
	campfireParams   []CampfireParams
	holes            []GPUHole
	boxFaceTex       []uint32
	ao               AOVolume
	aoOK             bool
	aoVersion        uint64

	// TLAS/BLAS instancing (optional).
	instTemplates    []GPUTemplateRecord
	instPlacements   []GPUInstanceRecord
	instNodeBase     uint32
	instNodeCount    uint32
	blockerSecStart  uint32
	blockerInstBase  uint32
	blockerInstCount uint32
}

// fresh reports whether the cache already holds the static buffers for this
// view's scene at its current geometry generation.
func (c *sceneCache) fresh(v *render.View) bool {
	return c.valid && c.scene == v.Scene && c.gen == v.Scene.Generation()
}

// transformsFresh reports whether transform-only GPU data matches the scene.
func (c *sceneCache) transformsFresh(v *render.View) bool {
	return c.valid && c.scene == v.Scene && c.xformGen == v.Scene.TransformGeneration()
}

// rebuild re-packs every static buffer from the scene and records the cache key.
// It is the only place the expensive PackPrimitives/PackBVH/PackTerrains work
// runs; callers gate it behind fresh. The AO volume is baked by the caller (on
// the CPU, via internal/probe) and arrives in the view; here it is only packed
// for upload.
func (c *sceneCache) rebuild(v *render.View) {
	c.clearInstancing()
	if v.Scene != nil && v.Scene.HasInstancing() {
		if prims, blockers, nodes, bvhN, blkN, isp, dynGPU, ok := packInstancedScene(v.Scene); ok {
			c.prims = prims
			c.blockers = blockers
			c.bvhNodes = nodes
			c.bvhNodeCount = bvhN
			c.blockerNodeCount = blkN
			c.instTemplates = isp.templates
			c.instPlacements = isp.instances
			c.instNodeBase = isp.instNodeBase
			c.instNodeCount = isp.instNodeCount
			c.blockerSecStart = isp.blockerSectionStart
			c.blockerInstBase = isp.blockerInstBase
			c.blockerInstCount = isp.blockerInstCount
			c.layout = computePrimLayout(v.Scene)
			c.layout.gpu = dynGPU
			goto afterPack
		} else {
			c.rebuildFlat(v.Scene)
		}
	} else {
		c.rebuildFlat(v.Scene)
	}

afterPack:

	c.lights = PackLights(v.Scene)
	c.terrains, c.samples, c.terrainFeatures, c.terrainPads = PackTerrains(v.Scene)
	c.waters = PackWaters(v.Scene)
	c.campfireParams = PackCampfireParams(v.Scene)
	c.holes = PackHoles(v.Scene)
	c.boxFaceTex = PackSceneFaceTextures(v.Scene, c.prims)
	c.ao, c.aoOK = PackAOVolume(v)
	c.aoVersion = v.AOVersion

	c.planeIdx = planeIndices(c.prims)
	c.blockerPlaneIdx = planeIndices(c.blockers)

	c.scene = v.Scene
	c.gen = v.Scene.Generation()
	c.xformGen = v.Scene.TransformGeneration()
	if len(c.layout.gpu.sphere) == 0 && len(c.layout.gpu.box) == 0 && len(c.layout.gpu.cylinder) == 0 {
		c.layout = computePrimLayout(v.Scene)
	}
	c.partialPrimSpans = nil
	c.partialBlockerSpans = nil
	c.valid = true
}

// updateDynamicTransforms re-packs moved dynamic primitives, refits the BVH, and
// records byte spans for a partial GPU upload. Called when only xformGen changed.
func (c *sceneCache) updateDynamicTransforms(s *scene.Scene) {
	if s == nil || !c.valid {
		return
	}
	var dirtyPrim []int
	var dirtyBlocker []int
	repack := func(sceneIdx int, primIdx int, repackFn func(*scene.Scene, int, *GPUPrimitive)) {
		if primIdx < 0 || primIdx >= len(c.prims) {
			return
		}
		repackFn(s, sceneIdx, &c.prims[primIdx])
		dirtyPrim = append(dirtyPrim, primIdx)
		if blkIdx, ok := c.layout.gpu.primToBlocker[primIdx]; ok && blkIdx >= 0 && blkIdx < len(c.blockers) {
			repackFn(s, sceneIdx, &c.blockers[blkIdx])
			dirtyBlocker = append(dirtyBlocker, blkIdx)
		}
	}
	for _, db := range s.DynamicBodies {
		for i := db.Spheres[0]; i < db.Spheres[1]; i++ {
			if gi, ok := c.layout.sphereGPU(i); ok {
				repack(i, gi, repackSphere)
			}
		}
		for i := db.Boxes[0]; i < db.Boxes[1]; i++ {
			if gi, ok := c.layout.boxGPU(i); ok {
				repack(i, gi, repackBox)
			}
		}
		for i := db.Cylinders[0]; i < db.Cylinders[1]; i++ {
			if gi, ok := c.layout.cylinderGPU(i); ok {
				repack(i, gi, repackCylinder)
			}
		}
		for i := db.Lenses[0]; i < db.Lenses[1]; i++ {
			if gi, ok := c.layout.lensGPU(i); ok {
				repack(i, gi, repackLens)
			}
		}
	}
	if len(dirtyPrim) == 0 {
		c.xformGen = s.TransformGeneration()
		c.partialPrimSpans = nil
		c.partialBlockerSpans = nil
		return
	}
	if c.hasInstancing() {
		if int(c.bvhNodeCount) <= len(c.bvhNodes) {
			RefitBVH(c.bvhNodes[:c.bvhNodeCount], c.prims)
		}
		if c.blockerNodeCount > 0 && int(c.blockerSecStart+c.blockerNodeCount) <= len(c.bvhNodes) {
			RefitBVH(c.bvhNodes[c.blockerSecStart:c.blockerSecStart+c.blockerNodeCount], c.blockers)
		}
	} else {
		RefitBVH(c.bvhNodes[:c.bvhNodeCount], c.prims)
		if c.blockerNodeCount > 0 {
			RefitBVH(c.bvhNodes[c.blockerSecStart:c.blockerSecStart+c.blockerNodeCount], c.blockers)
		}
	}
	c.partialPrimSpans = coalesceIndices(dirtyPrim)
	c.partialBlockerSpans = coalesceIndices(dirtyBlocker)
	c.xformGen = s.TransformGeneration()
}

func (c *sceneCache) rebuildFlat(s *scene.Scene) {
	if s != nil && len(s.DynamicBodies) > 0 {
		c.prims = packPrimitivesWithoutDynamic(s)
		c.blockers = packBlockersWithoutDynamic(s)
		var blkMap gpuIndexMap
		var primMap gpuIndexMap
		c.prims, primMap = appendDynamicBodyPrimitives(s, c.prims)
		c.blockers, blkMap = appendDynamicBodyBlockers(s, c.blockers)
		primMap.primToBlocker = linkPrimToBlocker(primMap, blkMap)
		c.layout = computePrimLayout(s)
		c.layout.gpu = primMap
	} else {
		c.prims = PackPrimitives(s)
		c.blockers = PackBlockers(s)
		c.layout = computePrimLayout(s)
	}
	bvhNodes := PackBVH(c.prims)
	blkNodes := PackBVH(c.blockers)
	c.bvhNodes = append(append([]GPUBVHNode(nil), bvhNodes...), blkNodes...)
	c.bvhNodeCount = uint32(len(bvhNodes))
	c.blockerNodeCount = uint32(len(blkNodes))
	c.blockerSecStart = c.bvhNodeCount
}

// planeIndices returns the indices of every infinite plane in prims, in
// ascending order — matching the order the shader's old full-buffer scan hit
// them, so switching to the index list is output-identical.
func planeIndices(prims []GPUPrimitive) []uint32 {
	var out []uint32
	for i := range prims {
		if prims[i].Meta[0] == primPlane {
			out = append(out, uint32(i))
		}
	}
	return out
}

func (c *sceneCache) clearInstancing() {
	c.instTemplates = nil
	c.instPlacements = nil
	c.instNodeBase = 0
	c.instNodeCount = 0
	c.blockerSecStart = 0
	c.blockerInstBase = 0
	c.blockerInstCount = 0
}

func (c *sceneCache) hasInstancing() bool {
	return len(c.instPlacements) > 0
}
