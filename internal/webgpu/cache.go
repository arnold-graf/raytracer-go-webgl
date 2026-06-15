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
	holes            []GPUHole
	ao               AOVolume
	aoOK             bool
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
	c.prims = PackPrimitives(v.Scene)
	c.blockers = PackBlockers(v.Scene)
	bvhNodes := PackBVH(c.prims)
	blkNodes := PackBVH(c.blockers)
	c.bvhNodes = append(append([]GPUBVHNode(nil), bvhNodes...), blkNodes...)
	c.bvhNodeCount = uint32(len(bvhNodes))
	c.blockerNodeCount = uint32(len(blkNodes))
	c.lights = PackLights(v.Scene)
	c.terrains, c.samples = PackTerrains(v.Scene)
	c.waters = PackWaters(v.Scene)
	c.holes = PackHoles(v.Scene)
	c.ao, c.aoOK = PackAOVolume(v)

	c.scene = v.Scene
	c.gen = v.Scene.Generation()
	c.valid = true
}
