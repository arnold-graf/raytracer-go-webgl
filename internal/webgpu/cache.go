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
// Invalidation is keyed on (scene pointer, scene.Generation()): a hot-reload
// swaps the pointer, and any in-place geometry edit bumps the generation via
// scene.Touch(). When the upcoming animation system moves objects each tick it
// will call Touch, so this cache degrades gracefully to today's per-frame
// packing for fully dynamic scenes while costing nothing for static ones. The
// same generation signal is the intended hook for a future partial-update path
// (re-pack only the dirty primitives) without changing this contract.
type sceneCache struct {
	scene *scene.Scene
	gen   uint64
	valid bool

	prims            []GPUPrimitive
	blockers         []GPUPrimitive
	bvhNodes         []GPUBVHNode
	bvhNodeCount     uint32
	blockerNodeCount uint32
	lights           []GPULight
	terrains         []GPUTerrain
	samples          []float32
	waters           []GPUWater
	campfireParams   []CampfireParams
	holes            []GPUHole
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
// view's scene at its current generation.
func (c *sceneCache) fresh(v *render.View) bool {
	return c.valid && c.scene == v.Scene && c.gen == v.Scene.Generation()
}

// rebuild re-packs every static buffer from the scene and records the cache key.
// It is the only place the expensive PackPrimitives/PackBVH/PackTerrains work
// runs; callers gate it behind fresh. The AO volume is baked by the caller (on
// the CPU, via internal/probe) and arrives in the view; here it is only packed
// for upload.
func (c *sceneCache) rebuild(v *render.View) {
	c.clearInstancing()
	if v.Scene != nil && v.Scene.HasInstancing() {
		if prims, blockers, nodes, bvhN, blkN, isp, ok := packInstancedScene(v.Scene); ok {
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
		} else {
			c.rebuildFlat(v.Scene)
		}
	} else {
		c.rebuildFlat(v.Scene)
	}

	c.lights = PackLights(v.Scene)
	c.terrains, c.samples = PackTerrains(v.Scene)
	c.waters = PackWaters(v.Scene)
	c.campfireParams = PackCampfireParams(v.Scene)
	c.holes = PackHoles(v.Scene)
	c.ao, c.aoOK = PackAOVolume(v)
	c.aoVersion = v.AOVersion

	c.scene = v.Scene
	c.gen = v.Scene.Generation()
	c.valid = true
}

func (c *sceneCache) rebuildFlat(s *scene.Scene) {
	c.prims = PackPrimitives(s)
	c.blockers = PackBlockers(s)
	bvhNodes := PackBVH(c.prims)
	blkNodes := PackBVH(c.blockers)
	c.bvhNodes = append(append([]GPUBVHNode(nil), bvhNodes...), blkNodes...)
	c.bvhNodeCount = uint32(len(bvhNodes))
	c.blockerNodeCount = uint32(len(blkNodes))
	c.blockerSecStart = c.bvhNodeCount
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
