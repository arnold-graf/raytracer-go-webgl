package webgpu

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"testing"

	"raytracer/internal/gpuscene"
)

// TestGIProbeStorage checks the two probe cascades fit the region they share.
//
// The fine cascade's size is set in Go and the offset the coarse one starts at
// is a literal in types.wesl, so growing the fine grid past GI_C0_MAX would
// silently overlap the two — probes writing over each other, which reads as a
// field that is merely wrong rather than as an error.
func TestGIProbeStorage(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(file), "shaders", "modules", "types.wesl"))
	if err != nil {
		t.Fatalf("read types.wesl: %v", err)
	}
	lit := func(name string) int {
		m := regexp.MustCompile(`(?m)^const ` + name + `: u32 = (\d+)u;`).FindSubmatch(src)
		if m == nil {
			t.Fatalf("types.wesl does not declare %s", name)
		}
		v, err := strconv.Atoi(string(m[1]))
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		return v
	}
	c0Max := lit("GI_C0_MAX")
	probeFloats := lit("GI_PROBE_FLOATS")
	probeBase := lit("GI_PROBE_BASE")
	moments := lit("GI_PROBE_MOMENTS")
	wg := lit("GI_PROBE_WG")
	rays := lit("GI_PROBE_RAYS")
	octIrr := lit("GI_OCT_IRR")
	octDepth := lit("GI_OCT_DEPTH")

	if giFineProbes > c0Max {
		t.Errorf("fine cascade is %d probes, GI_C0_MAX reserves %d", giFineProbes, c0Max)
	}
	// An octahedral irradiance map of RGB, then an octahedral depth map of
	// (mean, mean squared).
	if want := octIrr*octIrr*3 + octDepth*octDepth*2; probeFloats != want {
		t.Errorf("GI_PROBE_FLOATS is %d, want %d", probeFloats, want)
	}
	if want := octIrr * octIrr * 3; moments != want {
		t.Errorf("GI_PROBE_MOMENTS is %d, want %d (the depth map starts after the irradiance one)", moments, want)
	}
	if probeFloats != gpuscene.GIProbeFloats {
		t.Errorf("GI_PROBE_FLOATS is %d, Go has %d", probeFloats, gpuscene.GIProbeFloats)
	}
	if probeBase != gpuscene.AOVolumeFloats {
		t.Errorf("GI_PROBE_BASE is %d, want AOVolumeFloats %d", probeBase, gpuscene.AOVolumeFloats)
	}
	perWG := lit("GI_PROBES_PER_WG")
	// One lane per ray, and the workgroup carries a whole number of probes.
	if wg != rays*perWG {
		t.Errorf("GI_PROBE_WG is %d, want GI_PROBE_RAYS*GI_PROBES_PER_WG = %d", wg, rays*perWG)
	}
	if wg != giProbesPerWorkgroup*rays {
		t.Errorf("GI_PROBE_WG is %d, Go's giProbesPerWorkgroup implies %d", wg, giProbesPerWorkgroup*rays)
	}
	// A workgroup narrower than a SIMD group wastes the rest of it, and
	// tracing is the long pole — measured at 1.7x on the whole pass.
	if wg%32 != 0 {
		t.Errorf("GI_PROBE_WG is %d; it must be a multiple of the 32-lane SIMD group", wg)
	}
	// The texel update shares both maps out over the ray lanes, in equal runs.
	if (octIrr*octIrr)%rays != 0 {
		t.Errorf("%d irradiance texels do not divide evenly over %d ray lanes", octIrr*octIrr, rays)
	}
	// Each map is shared out over the ray lanes in equal whole runs.
	if (octDepth*octDepth)%rays != 0 {
		t.Errorf("%d depth texels do not divide evenly over %d ray lanes", octDepth*octDepth, rays)
	}
	// Occlusion needs more angular resolution than irradiance does: at 8x8 a
	// texel spans 45 degrees and the bilinear read blends four of them, which
	// is too coarse to resolve a wall a metre away.
	if octDepth < octIrr {
		t.Errorf("GI_OCT_DEPTH is %d, finer than GI_OCT_IRR at %d; depth should be at least as fine", octDepth, octIrr)
	}
	if c0Max+giMaxProbes > gpuscene.GIProbeMaxCells {
		t.Errorf("cascades need %d probes, GIProbeMaxCells reserves %d", c0Max+giMaxProbes, gpuscene.GIProbeMaxCells)
	}
	end := probeBase + (c0Max+giMaxProbes)*probeFloats
	if end > gpuscene.GIVolumeTotalFloats {
		t.Errorf("the cascades end at %d, past ao_volume's %d floats", end, gpuscene.GIVolumeTotalFloats)
	}
}
