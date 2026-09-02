package webgpu

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// GPU profile counter indices — keep in sync with shaders/modules/profile.wesl PROF_* constants.
const (
	profPixels = iota
	profPathSegs
	profHitPrim
	profHitInst
	profHitTerrain
	profHitWater
	profSky
	profShadowRays
	profShadowBlock
	profTerrainSteps
	profMirrorBounces
	profGlassBounces
	profDiffuseRefl
	profPriHitPrim
	profPriHitInst
	profPriHitTerrain
	profPriHitWater
	profPriSky
	profBVHSteps
	profPrimTests
	profMaxSegs
	profCounterCount
)

const profileCounterBytes = profCounterCount * 4

// GPUProfileCounters holds atomic counter readback from the trace shader.
// Counts accumulate over one profiled frame (all pixels × path segments).
type GPUProfileCounters struct {
	Pixels         uint32
	PathSegs       uint32
	HitPrim        uint32
	HitInst        uint32
	HitTerrain     uint32
	HitWater       uint32
	Sky            uint32
	ShadowRays     uint32
	ShadowBlock    uint32
	TerrainSteps   uint32
	MirrorBounces  uint32
	GlassBounces   uint32
	DiffuseRefl    uint32
	PriHitPrim     uint32
	PriHitInst     uint32
	PriHitTerrain  uint32
	PriHitWater    uint32
	PriSky         uint32
	BVHSteps       uint32
	PrimTests      uint32
	MaxSegs        uint32
}

func decodeProfileCounters(raw []byte) GPUProfileCounters {
	if len(raw) < profileCounterBytes {
		return GPUProfileCounters{}
	}
	var c GPUProfileCounters
	vals := make([]uint32, profCounterCount)
	for i := range vals {
		vals[i] = binary.LittleEndian.Uint32(raw[i*4:])
	}
	c.Pixels = vals[profPixels]
	c.PathSegs = vals[profPathSegs]
	c.HitPrim = vals[profHitPrim]
	c.HitInst = vals[profHitInst]
	c.HitTerrain = vals[profHitTerrain]
	c.HitWater = vals[profHitWater]
	c.Sky = vals[profSky]
	c.ShadowRays = vals[profShadowRays]
	c.ShadowBlock = vals[profShadowBlock]
	c.TerrainSteps = vals[profTerrainSteps]
	c.MirrorBounces = vals[profMirrorBounces]
	c.GlassBounces = vals[profGlassBounces]
	c.DiffuseRefl = vals[profDiffuseRefl]
	c.PriHitPrim = vals[profPriHitPrim]
	c.PriHitInst = vals[profPriHitInst]
	c.PriHitTerrain = vals[profPriHitTerrain]
	c.PriHitWater = vals[profPriHitWater]
	c.PriSky = vals[profPriSky]
	c.BVHSteps = vals[profBVHSteps]
	c.PrimTests = vals[profPrimTests]
	c.MaxSegs = vals[profMaxSegs]
	return c
}

// SetProfiling enables GPU atomic counters for the next Render call(s).
func (r *Renderer) SetProfiling(on bool) { r.profiling = on }

// LastGPUProfile returns counters from the most recent profiled frame.
func (r *Renderer) LastGPUProfile() GPUProfileCounters { return r.profileCounters }

// FormatGPUProfile renders a human-readable shader workload breakdown.
func FormatGPUProfile(c GPUProfileCounters, gpuMS float64) string {
	var b strings.Builder
	pix := float64(c.Pixels)
	if pix < 1 {
		pix = 1
	}
	fmt.Fprintf(&b, "GPU shader workload (one frame, %d pixels, %.1f ms GPU):\n", c.Pixels, gpuMS)
	fmt.Fprintf(&b, "  path segments:     %8d  (%.1f per pixel)\n", c.PathSegs, float64(c.PathSegs)/pix)
	fmt.Fprintf(&b, "  estimated GPU time mix (sky omitted):\n")
	terr, inst, water, prim := timeMixFromCounters(c)
	fmt.Fprintf(&b, "    terrain %.0f%%  inst %.0f%%  water %.0f%%  prim %.0f%%\n",
		terr, inst, water, prim)
	fmt.Fprintf(&b, "  primary screen coverage:\n")
	fmt.Fprintf(&b, "    sky               %8d  (%5.1f%% of pixels)\n", c.PriSky, 100*float64(c.PriSky)/pix)
	fmt.Fprintf(&b, "    terrain           %8d  (%5.1f%%)\n", c.PriHitTerrain, 100*float64(c.PriHitTerrain)/pix)
	fmt.Fprintf(&b, "    instanced prim    %8d  (%5.1f%%)\n", c.PriHitInst, 100*float64(c.PriHitInst)/pix)
	fmt.Fprintf(&b, "    static prim       %8d  (%5.1f%%)\n", c.PriHitPrim-c.PriHitInst, 100*float64(c.PriHitPrim-c.PriHitInst)/pix)
	fmt.Fprintf(&b, "    water             %8d  (%5.1f%%)\n", c.PriHitWater, 100*float64(c.PriHitWater)/pix)
	fmt.Fprintf(&b, "  all hits: prim %d (inst %d) terrain %d water %d sky %d\n",
		c.HitPrim, c.HitInst, c.HitTerrain, c.HitWater, c.Sky)
	if c.ShadowRays > 0 {
		fmt.Fprintf(&b, "  shadow rays:       %8d  (%d blocked, %.1f%%)\n",
			c.ShadowRays, c.ShadowBlock, 100*float64(c.ShadowBlock)/float64(c.ShadowRays))
	}
	if c.TerrainSteps > 0 {
		fmt.Fprintf(&b, "  terrain march steps: %6d  (%.1f per shadow/terrain test)\n",
			c.TerrainSteps, float64(c.TerrainSteps)/float64(maxU32(c.ShadowRays+c.HitTerrain, 1)))
	}
	fmt.Fprintf(&b, "  bounces: mirror %d  glass %d  diffuse_refl %d\n",
		c.MirrorBounces, c.GlassBounces, c.DiffuseRefl)
	rays := float64(maxU32(c.PathSegs+c.ShadowRays, 1))
	fmt.Fprintf(&b, "  bvh quality (RRS): %d steps  %d prim tests  over %d rays\n",
		c.BVHSteps, c.PrimTests, c.PathSegs+c.ShadowRays)
	fmt.Fprintf(&b, "    %.1f steps/ray  %.1f prim tests/ray  (lower = better BVH)\n",
		float64(c.BVHSteps)/rays, float64(c.PrimTests)/rays)
	return b.String()
}

func maxU32(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}
