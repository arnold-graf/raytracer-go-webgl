package webgpu

import (
	"testing"
	"unsafe"

	"raytracer/internal/camera"
	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/texture"
	"raytracer/internal/trace"
	"raytracer/internal/vec"
)

// TestGPUPrimitiveLayout guards the std430 packing contract: GPUPrimitive must
// stay a tight block with no padding so primBytes can reinterpret it.
func TestGPUPrimitiveLayout(t *testing.T) {
	if got := unsafe.Sizeof(GPUPrimitive{}); got != primStride {
		t.Fatalf("GPUPrimitive size = %d, want %d", got, primStride)
	}
	var p GPUPrimitive
	if off := unsafe.Offsetof(p.GeoB); off != 16 {
		t.Fatalf("GeoB offset = %d, want 16", off)
	}
	if off := unsafe.Offsetof(p.Albedo); off != 32 {
		t.Fatalf("Albedo offset = %d, want 32", off)
	}
	if off := unsafe.Offsetof(p.Albedo2); off != 48 {
		t.Fatalf("Albedo2 offset = %d, want 48", off)
	}
	if off := unsafe.Offsetof(p.Params); off != 64 {
		t.Fatalf("Params offset = %d, want 64", off)
	}
	if off := unsafe.Offsetof(p.Meta); off != 80 {
		t.Fatalf("Meta offset = %d, want 80", off)
	}
	if off := unsafe.Offsetof(p.Xf0); off != 96 {
		t.Fatalf("Xf0 offset = %d, want 96", off)
	}
	if off := unsafe.Offsetof(p.Xf1); off != 112 {
		t.Fatalf("Xf1 offset = %d, want 112", off)
	}
	if off := unsafe.Offsetof(p.Xf2); off != 128 {
		t.Fatalf("Xf2 offset = %d, want 128", off)
	}
}

func TestGPUHoleLayout(t *testing.T) {
	if got := unsafe.Sizeof(GPUHole{}); got != holeStride {
		t.Fatalf("GPUHole size = %d, want %d", got, holeStride)
	}
	var hh GPUHole
	if off := unsafe.Offsetof(hh.Max); off != 16 {
		t.Fatalf("Max offset = %d, want 16", off)
	}
}

func TestGPUCampfireLayout(t *testing.T) {
	if got := unsafe.Sizeof(GPUCampfire{}); got != campfireStride {
		t.Fatalf("GPUCampfire size = %d, want %d", got, campfireStride)
	}
}

func TestGPULightLayout(t *testing.T) {
	if got := unsafe.Sizeof(GPULight{}); got != lightStride {
		t.Fatalf("GPULight size = %d, want %d", got, lightStride)
	}
	var l GPULight
	if off := unsafe.Offsetof(l.Color); off != 16 {
		t.Fatalf("Color offset = %d, want 16", off)
	}
	if off := unsafe.Offsetof(l.Falloff); off != 32 {
		t.Fatalf("Falloff offset = %d, want 32", off)
	}
}

func TestGPUBVHNodeLayout(t *testing.T) {
	if got := unsafe.Sizeof(GPUBVHNode{}); got != nodeStride {
		t.Fatalf("GPUBVHNode size = %d, want %d", got, nodeStride)
	}
	var n GPUBVHNode
	if off := unsafe.Offsetof(n.Max); off != 16 {
		t.Fatalf("Max offset = %d, want 16", off)
	}
	if off := unsafe.Offsetof(n.Info); off != 32 {
		t.Fatalf("Info offset = %d, want 32", off)
	}
}

func TestGPUTerrainAndWaterLayout(t *testing.T) {
	if got := unsafe.Sizeof(GPUTerrain{}); got != terrainStride {
		t.Fatalf("GPUTerrain size = %d, want %d", got, terrainStride)
	}
	if got := unsafe.Sizeof(GPUWater{}); got != waterStride {
		t.Fatalf("GPUWater size = %d, want %d", got, waterStride)
	}
}

func TestPackPrimitivesKinds(t *testing.T) {
	sc := &scene.Scene{
		Spheres: []scene.Sphere{{Center: vec.New(1, 2, 3), Radius: 0.5,
			Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.9, 0.1, 0.1)}}},
		Planes: []scene.Plane{{N: vec.New(0, 1, 0), D: 0,
			Surface: scene.Surface{Mat: scene.MatChecker, Albedo: vec.New(0.7, 0.7, 0.7)}}},
		Boxes: []scene.Box{{Min: vec.New(-1, 0, -1), Max: vec.New(1, 2, 1),
			Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.2, 0.5, 0.8)}}},
	}
	prims := PackPrimitives(sc)
	if len(prims) != 3 {
		t.Fatalf("packed %d prims, want 3", len(prims))
	}
	if prims[0].Meta[0] != primSphere || prims[0].GeoA != [4]float32{1, 2, 3, 0.5} {
		t.Fatalf("sphere packed wrong: %+v", prims[0])
	}
	if prims[1].Meta[0] != primPlane || prims[1].GeoA != [4]float32{0, 1, 0, 0} {
		t.Fatalf("plane packed wrong: %+v", prims[1])
	}
	if prims[2].Meta[0] != primBox || prims[2].GeoB != [4]float32{1, 2, 1, 0} {
		t.Fatalf("box packed wrong: %+v", prims[2])
	}
}

func TestPackBVHSkipsPlanesAndReferencesFinitePrims(t *testing.T) {
	prims := []GPUPrimitive{
		{GeoA: [4]float32{0, 1, 0, 1}, Meta: [4]uint32{primSphere, uint32(scene.MatDiffuse), 0, 0}},
		{GeoA: [4]float32{0, 1, 0, 0}, Meta: [4]uint32{primPlane, uint32(scene.MatDiffuse), 0, 0}},
		{GeoA: [4]float32{-2, 0, -1, 0}, GeoB: [4]float32{-1, 1, 0, 0}, Meta: [4]uint32{primBox, uint32(scene.MatDiffuse), 0, 0}},
	}
	nodes := PackBVH(prims)
	if len(nodes) == 0 {
		t.Fatal("expected BVH nodes")
	}
	root := nodes[0]
	if root.Info[3] == 0 {
		t.Fatalf("expected a leaf for two finite primitives, got interior: %+v", root)
	}
	if root.Info[0] == 1 || root.Info[1] == 1 {
		t.Fatalf("plane primitive included in finite BVH leaf: %+v", root.Info)
	}
	if root.Min[0] != -2 || root.Min[1] != 0 || root.Min[2] != -1 {
		t.Fatalf("root min packed wrong: %+v", root.Min)
	}
}

func TestPackLightsAndBlockers(t *testing.T) {
	sc := &scene.Scene{
		Spheres: []scene.Sphere{
			{Center: vec.New(0, 1, 0), Radius: 1,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(1, 1, 1)}},
			{Center: vec.New(2, 1, 0), Radius: 1,
				Surface: scene.Surface{Mat: scene.MatEmit, Albedo: vec.New(5, 5, 5)}},
			{Center: vec.New(4, 1, 0), Radius: 1,
				Surface: scene.Surface{Mat: scene.MatGlass, Albedo: vec.New(1, 1, 1)}},
		},
		Planes: []scene.Plane{{N: vec.New(0, 1, 0), D: 0,
			Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(1, 1, 1)}}},
		Lights: []scene.Light{{Pos: vec.New(1, 2, 3), Color: vec.New(2, 1, 0.5), Range: 4}},
	}
	blockers := PackBlockers(sc)
	if len(blockers) != 2 { // diffuse sphere + plane; emit/glass spheres skipped
		t.Fatalf("packed %d blockers, want 2", len(blockers))
	}
	lights := PackLights(sc)
	if len(lights) != 1 {
		t.Fatalf("packed %d lights, want 1", len(lights))
	}
	if lights[0].Pos != [4]float32{1, 2, 3, 0} || lights[0].Falloff[0] != 16 || lights[0].Falloff[1] != 1.0/16.0 {
		t.Fatalf("light packed wrong: %+v", lights[0])
	}
}

func TestPackTerrainAndWater(t *testing.T) {
	trn := scene.Terrain{
		OriginX: -2, OriginZ: -3, SizeX: 4, SizeZ: 6,
		Base: 0.2, Step: 0.2, GridCell: 1,
		Grass: 6, Rock: 3, Snow: 8,
		GrassCol: vec.New(1, 0.8, 0.7), RockCol: vec.New(0.9, 0.9, 0.9), SnowCol: vec.New(1, 1, 1),
		SlopeLo: 0.2, SlopeHi: 0.7, SnowLo: 3, SnowHi: 5,
	}
	trn.Prepare()
	sc := &scene.Scene{
		Terrains: []scene.Terrain{trn},
		Waters: []scene.WaterPool{{CX: 1, CZ: 2, Radius: 3, Level: 0.4,
			Surface: scene.Surface{Mat: scene.MatMirror, Albedo: vec.New(0.2, 0.3, 0.4), Reflect: 0.8}}},
	}
	terrains, samples := PackTerrains(sc)
	if len(terrains) != 1 || len(samples) == 0 {
		t.Fatalf("terrain pack failed: terrains=%d samples=%d", len(terrains), len(samples))
	}
	if terrains[0].Bounds0 != [4]float32{-2, -3, 4, 6} {
		t.Fatalf("terrain bounds packed wrong: %+v", terrains[0].Bounds0)
	}
	waters := PackWaters(sc)
	if len(waters) != 1 || waters[0].Geom != [4]float32{1, 2, 3, 0.4} {
		t.Fatalf("water packed wrong: %+v", waters)
	}
}

// TestClearSkyMatchesCPU renders an empty scene (all sky) and compares against
// the CPU renderer pixel-for-pixel. No geometry means no silhouette aliasing,
// so the only differences are f32-vs-f64 tonemap rounding.
func TestClearSkyMatchesCPU(t *testing.T) {
	const w, h = 32, 18
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	cam := camera.New()
	cam.Yaw = 0.35
	cam.Pitch = -0.12
	sc := &scene.Scene{}
	tr := trace.New(sc)

	got := make([]byte, w*h*4)
	r.Render(got, cam, tr, 1)
	want := cpuFrame(t, sc, cam, w, h)

	mean, max, frac := compareFrames(got, want)
	if mean > 1.0 || frac > 0.0 {
		t.Fatalf("sky parity off: mean=%.3f max=%d fracOver=%.3f", mean, max, frac)
	}
}

// TestSkyVariantsMatchCPU renders the procedural sky variants (cloudy, sunset,
// night storm) full-frame and checks the GPU against the CPU. These share the
// parity-tested perlin/fbm/turbulence helpers, so only f32-vs-f64 rounding (and
// a few cloud-edge pixels where smoothstep thresholds straddle a boundary)
// should differ. The night-stars variant is excluded: its frac(sin) star hash
// diverges under f32, so star placement is intentionally not bit-identical.
func TestSkyVariantsMatchCPU(t *testing.T) {
	const w, h = 48, 27
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	cam := camera.New()
	cam.Yaw = 0.6
	cam.Pitch = 0.10 // tilt up so the frame is sky + horizon, no geometry

	for _, sky := range []string{"cloudy", "sunset", "night_storm"} {
		t.Run(sky, func(t *testing.T) {
			id, ok := scene.SkyID(sky)
			if !ok {
				t.Fatalf("unknown sky %q", sky)
			}
			sc := &scene.Scene{}
			sc.Env.Sky = id
			tr := trace.New(sc)
			tr.Time = 1.5 // exercise the animated cloud drift term

			got := make([]byte, w*h*4)
			r.Render(got, cam, tr, 1)
			want := cpuFrameFromTracer(t, tr, cam, w, h)

			mean, max, frac := compareFrames(got, want)
			t.Logf("%s sky parity: mean=%.4f max=%d fracOver=%.4f", sky, mean, max, frac)
			if mean > 1.0 {
				t.Fatalf("mean error too high: %.4f", mean)
			}
			if frac > 0.03 {
				t.Fatalf("too many outlier pixels (>4 LSB): %.4f", frac)
			}
		})
	}
}

// TestPrimitiveParityMatchesCPU renders diffuse primitives with mirror/shadow/AO
// off and checks the GPU frame against the CPU oracle. A handful of silhouette
// pixels may differ by a few LSBs due to f32 intersection, so the gate is on
// mean error plus the fraction of outlier pixels rather than a hard per-pixel
// bound.
func TestPrimitiveParityMatchesCPU(t *testing.T) {
	const w, h = 64, 48
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	sc := diffuseScene()
	tr := trace.New(sc) // Opts zero value: Mirror/Shadow/AO all off.
	cam := camera.New()

	got := make([]byte, w*h*4)
	r.Render(got, cam, tr, 1)
	want := cpuFrame(t, sc, cam, w, h)

	mean, max, frac := compareFrames(got, want)
	t.Logf("primitive parity: mean=%.4f max=%d fracOver=%.4f", mean, max, frac)
	if mean > 0.5 {
		t.Fatalf("mean error too high: %.4f", mean)
	}
	if frac > 0.02 {
		t.Fatalf("too many outlier pixels (>4 LSB): %.4f", frac)
	}
}

// TestSceneCacheReusesStaticBuffers verifies the scene-buffer cache contract:
//   - a static scene stays "fresh" across frames (no re-pack), and an in-place
//     geometry edit is invisible until scene.Touch() invalidates the cache;
//   - calling Touch forces a rebuild and the new geometry shows up.
//
// This guards the optimization that lets the GPU backend skip per-frame packing
// while remaining correct for the upcoming moving-object animation path.
func TestSceneCacheReusesStaticBuffers(t *testing.T) {
	const w, h = 48, 36
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	sc := diffuseScene()
	tr := trace.New(sc)
	cam := camera.New()

	frame := func() []byte {
		b := make([]byte, w*h*4)
		r.Render(b, cam, tr, 1)
		return b
	}

	base := frame()
	gen := r.cache.gen
	if !r.cache.fresh(tr) {
		t.Fatal("cache should be fresh immediately after a render")
	}

	// Re-render unchanged: cache stays fresh and the generation is unchanged.
	_ = frame()
	if r.cache.gen != gen {
		t.Fatalf("static re-render bumped cache gen: %d -> %d", gen, r.cache.gen)
	}

	// Mutate geometry in place WITHOUT Touch: the cache must keep serving the
	// old buffers, so the image is byte-identical to the baseline.
	sc.Spheres[0].Center = vec.New(0, 1, 2.2) // slide the front sphere toward the camera
	stale := frame()
	if !bytesEqual(base, stale) {
		t.Fatal("edit without Touch changed the image; cache is not authoritative")
	}

	// Now Touch: the cache rebuilds, uploads the moved sphere, and the image
	// changes.
	sc.Touch()
	if r.cache.fresh(tr) {
		t.Fatal("cache should be stale right after Touch")
	}
	moved := frame()
	if !r.cache.fresh(tr) {
		t.Fatal("cache should be fresh again after the post-Touch render")
	}
	if bytesEqual(base, moved) {
		t.Fatal("moving a sphere + Touch did not change the image")
	}
}

// bytesEqual reports whether two byte slices are identical.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestPointLightShadowParityMatchesCPU enables CPU shadows and compares the
// GPU point-light/shadow path against a small scene with sphere/box/plane
// blockers. The GPU still uses a brute-force blocker loop; BVH traversal lands
// in Phase 4.
func TestPointLightShadowParityMatchesCPU(t *testing.T) {
	const w, h = 64, 48
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	sc := litShadowScene()
	tr := trace.New(sc)
	tr.Opts.Shadow = true
	cam := camera.New()

	got := make([]byte, w*h*4)
	r.Render(got, cam, tr, 1)
	want := cpuFrameFromTracer(t, tr, cam, w, h)

	mean, max, frac := compareFrames(got, want)
	t.Logf("point light shadow parity: mean=%.4f max=%d fracOver=%.4f", mean, max, frac)
	if mean > 0.75 {
		t.Fatalf("mean error too high: %.4f", mean)
	}
	if frac > 0.04 {
		t.Fatalf("too many outlier pixels (>4 LSB): %.4f", frac)
	}
}

func TestReflectionParityMatchesCPU(t *testing.T) {
	const w, h = 64, 48
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	sc := reflectiveScene()
	tr := trace.New(sc)
	tr.Opts.Mirror = true
	cam := camera.New()
	cam.Pos = vec.New(0, 1.4, 5)

	got := make([]byte, w*h*4)
	r.Render(got, cam, tr, 1)
	want := cpuFrameFromTracer(t, tr, cam, w, h)

	mean, max, frac := compareFrames(got, want)
	t.Logf("reflection parity: mean=%.4f max=%d fracOver=%.4f", mean, max, frac)
	if mean > 1.0 {
		t.Fatalf("mean error too high: %.4f", mean)
	}
	if frac > 0.04 {
		t.Fatalf("too many outlier pixels (>4 LSB): %.4f", frac)
	}
}

// TestGlassParityMatchesCPU exercises the two-lobe glass path: a clear pane
// both refracts the scene behind it and shows the Fresnel reflection of the
// world in front. The GPU now walks a bounded ray tree so both lobes are
// blended like the CPU, rather than picking a single lobe per hit.
func TestGlassParityMatchesCPU(t *testing.T) {
	const w, h = 64, 48
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	sc := glassScene()
	tr := trace.New(sc)
	tr.Opts.Mirror = true
	tr.Opts.Shadow = true
	cam := camera.New()
	cam.Pos = vec.New(1.1, 1.4, 5)

	got := make([]byte, w*h*4)
	r.Render(got, cam, tr, 1)
	want := cpuFrameFromTracer(t, tr, cam, w, h)

	mean, max, frac := compareFrames(got, want)
	t.Logf("glass parity: mean=%.4f max=%d fracOver=%.4f", mean, max, frac)
	if mean > 1.5 {
		t.Fatalf("mean error too high: %.4f", mean)
	}
	if frac > 0.06 {
		t.Fatalf("too many outlier pixels (>4 LSB): %.4f", frac)
	}
}

func glassScene() *scene.Scene {
	return &scene.Scene{
		Spheres: []scene.Sphere{
			// Behind the pane: seen through it (refracted lobe).
			{Center: vec.New(0, 1, -2.5), Radius: 0.9,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.2, 0.7, 0.35)}},
			// In front and to the side: shows up in the pane's reflection.
			{Center: vec.New(-2.4, 1.2, 2.6), Radius: 0.8,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.95, 0.35, 0.25)}},
		},
		Boxes: []scene.Box{
			{Min: vec.New(-1.5, 0, -0.08), Max: vec.New(1.5, 2.2, 0.08),
				Surface: scene.Surface{Mat: scene.MatGlass, Albedo: vec.New(0.85, 0.92, 0.9),
					IOR: 1.5, Transmit: 0.95}},
		},
		Planes: []scene.Plane{
			{N: vec.New(0, 1, 0), D: 0,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.5, 0.5, 0.55)}},
		},
		Lights: []scene.Light{
			{Pos: vec.New(3, 5, 4), Color: vec.New(8, 7.5, 7), Range: 16},
		},
	}
}

func TestTerrainWaterParityMatchesCPU(t *testing.T) {
	const w, h = 64, 48
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	sc := terrainWaterScene()
	tr := trace.New(sc)
	tr.Opts.Mirror = true
	cam := camera.New()
	cam.Pos = vec.New(0, 2.0, 5)
	cam.Pitch = -0.25

	got := make([]byte, w*h*4)
	r.Render(got, cam, tr, 1)
	want := cpuFrameFromTracer(t, tr, cam, w, h)

	mean, max, frac := compareFrames(got, want)
	t.Logf("terrain/water parity: mean=%.4f max=%d fracOver=%.4f", mean, max, frac)
	if mean > 1.5 {
		t.Fatalf("mean error too high: %.4f", mean)
	}
	if frac > 0.06 {
		t.Fatalf("too many outlier pixels (>4 LSB): %.4f", frac)
	}
}

// TestTextureParityMatchesCPU renders the Perlin-based procedural textures
// (wood, brick, stone, cement, marble, grass, dirt, snow, wallpaper) and checks
// GPU parity. Brick is now included: its per-brick cell hash was reworked into
// an integer bit-mix (see texture.cellRand) that the WGSL port reproduces
// exactly, so the wall matches the CPU.
func TestTextureParityMatchesCPU(t *testing.T) {
	const w, h = 64, 48
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	sc := texturedScene()
	tr := trace.New(sc)
	cam := camera.New()
	cam.Pos = vec.New(0, 1.2, 5)

	got := make([]byte, w*h*4)
	r.Render(got, cam, tr, 1)
	want := cpuFrameFromTracer(t, tr, cam, w, h)

	mean, max, frac := compareFrames(got, want)
	t.Logf("texture parity: mean=%.4f max=%d fracOver=%.4f", mean, max, frac)
	if mean > 1.5 {
		t.Fatalf("mean error too high: %.4f", mean)
	}
	if frac > 0.08 {
		t.Fatalf("too many outlier pixels (>4 LSB): %.4f", frac)
	}
}

// TestCheckerParityMatchesCPU isolates the analytic checker plane material.
func TestCheckerParityMatchesCPU(t *testing.T) {
	const w, h = 64, 48
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	sc := &scene.Scene{
		Planes: []scene.Plane{{N: vec.New(0, 1, 0), D: 0,
			Surface: scene.Surface{Mat: scene.MatChecker, Albedo: vec.New(0.85, 0.85, 0.85)},
			Albedo2: vec.New(0.1, 0.1, 0.1)}},
	}
	tr := trace.New(sc)
	cam := camera.New()
	cam.Pos = vec.New(0, 1.5, 4)
	cam.Pitch = -0.35

	got := make([]byte, w*h*4)
	r.Render(got, cam, tr, 1)
	want := cpuFrameFromTracer(t, tr, cam, w, h)

	mean, max, frac := compareFrames(got, want)
	t.Logf("checker parity: mean=%.4f max=%d fracOver=%.4f", mean, max, frac)
	if mean > 0.5 || frac > 0.02 {
		t.Fatalf("checker parity off: mean=%.4f frac=%.4f", mean, frac)
	}
}

// TestConicParityMatchesCPU exercises the analytic cylinder, cone and torus.
func TestConicParityMatchesCPU(t *testing.T) {
	const w, h = 64, 48
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	sc := conicScene()
	tr := trace.New(sc)
	tr.Opts.Shadow = true
	cam := camera.New()
	cam.Pos = vec.New(0, 1.6, 6)

	got := make([]byte, w*h*4)
	r.Render(got, cam, tr, 1)
	want := cpuFrameFromTracer(t, tr, cam, w, h)

	mean, max, frac := compareFrames(got, want)
	t.Logf("conic parity: mean=%.4f max=%d fracOver=%.4f", mean, max, frac)
	if mean > 1.5 {
		t.Fatalf("mean error too high: %.4f", mean)
	}
	if frac > 0.06 {
		t.Fatalf("too many outlier pixels (>4 LSB): %.4f", frac)
	}
}

// TestAOVolumeParityMatchesCPU enables the baked ambient-occlusion volume. The
// GPU uploads the exact volume the CPU baked, so only f32-vs-f64 trilinear
// rounding differs.
func TestAOVolumeParityMatchesCPU(t *testing.T) {
	const w, h = 64, 48
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	sc := litShadowScene()
	tr := trace.New(sc)
	tr.Opts.Shadow = true
	tr.Opts.AO = true
	cam := camera.New()

	got := make([]byte, w*h*4)
	r.Render(got, cam, tr, 1)
	want := cpuFrameFromTracer(t, tr, cam, w, h)

	mean, max, frac := compareFrames(got, want)
	t.Logf("ao volume parity: mean=%.4f max=%d fracOver=%.4f", mean, max, frac)
	if mean > 0.75 {
		t.Fatalf("mean error too high: %.4f", mean)
	}
	if frac > 0.04 {
		t.Fatalf("too many outlier pixels (>4 LSB): %.4f", frac)
	}
}

// TestCampfireParityMatchesCPU resolves a campfire's flickering sub-lights at a
// fixed animation time and checks the GPU cluster (shared core shadow early-out
// plus per-sub-light shadow rays) against the CPU.
func TestCampfireParityMatchesCPU(t *testing.T) {
	const w, h = 64, 48
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	sc := campfireScene()
	tr := trace.New(sc)
	tr.Opts.Shadow = true
	tr.Time = 1.234
	cam := camera.New()
	cam.Pos = vec.New(0, 1.5, 5)

	got := make([]byte, w*h*4)
	r.Render(got, cam, tr, 1)
	want := cpuFrameFromTracer(t, tr, cam, w, h)

	mean, max, frac := compareFrames(got, want)
	t.Logf("campfire parity: mean=%.4f max=%d fracOver=%.4f", mean, max, frac)
	if mean > 0.9 {
		t.Fatalf("mean error too high: %.4f", mean)
	}
	if frac > 0.05 {
		t.Fatalf("too many outlier pixels (>4 LSB): %.4f", frac)
	}
}

// TestTransformParityMatchesCPU rotates a box and a cylinder in place and
// checks that the GPU world->local ray transform and normal transform match the
// CPU. Shadows are on so the rotated geometry also has to cast correctly.
func TestTransformParityMatchesCPU(t *testing.T) {
	const w, h = 64, 48
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	sc := transformedScene()
	tr := trace.New(sc)
	tr.Opts.Shadow = true
	cam := camera.New()
	cam.Pos = vec.New(0, 1.8, 6)
	cam.Pitch = -0.1

	got := make([]byte, w*h*4)
	r.Render(got, cam, tr, 1)
	want := cpuFrameFromTracer(t, tr, cam, w, h)

	mean, max, frac := compareFrames(got, want)
	t.Logf("transform parity: mean=%.4f max=%d fracOver=%.4f", mean, max, frac)
	if mean > 1.5 {
		t.Fatalf("mean error too high: %.4f", mean)
	}
	if frac > 0.06 {
		t.Fatalf("too many outlier pixels (>4 LSB): %.4f", frac)
	}
}

// TestBoxHoleParityMatchesCPU carves a window and a doorway out of a wall box
// (CSG difference) and checks that the GPU sees through the openings and shades
// the inner cutout faces like the CPU. The wall is also rotated so the hole CSG
// and the transform must compose.
func TestBoxHoleParityMatchesCPU(t *testing.T) {
	const w, h = 64, 48
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	sc := holedBoxScene()
	tr := trace.New(sc)
	tr.Opts.Shadow = true
	cam := camera.New()
	cam.Pos = vec.New(0, 1.5, 6)

	got := make([]byte, w*h*4)
	r.Render(got, cam, tr, 1)
	want := cpuFrameFromTracer(t, tr, cam, w, h)

	mean, max, frac := compareFrames(got, want)
	t.Logf("box-hole parity: mean=%.4f max=%d fracOver=%.4f", mean, max, frac)
	if mean > 1.5 {
		t.Fatalf("mean error too high: %.4f", mean)
	}
	if frac > 0.06 {
		t.Fatalf("too many outlier pixels (>4 LSB): %.4f", frac)
	}
}

func transformedScene() *scene.Scene {
	box := scene.Box{
		Min: vec.New(-0.8, 0, -0.8), Max: vec.New(0.8, 1.6, 0.8),
		Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.8, 0.5, 0.3)},
	}
	box.Xform = scene.NewTransform(0, 35, 0, vec.New(0, 0.8, 0))
	cyl := scene.Cylinder{
		CX: 2, CZ: -0.5, Radius: 0.5, YMin: 0, YMax: 1.8,
		Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.3, 0.6, 0.85)},
	}
	cyl.Xform = scene.NewTransform(20, 0, 15, vec.New(2, 0.9, -0.5))
	return &scene.Scene{
		Boxes:     []scene.Box{box},
		Cylinders: []scene.Cylinder{cyl},
		Planes: []scene.Plane{
			{N: vec.New(0, 1, 0), D: 0,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.6, 0.6, 0.65)}},
		},
		Lights: []scene.Light{
			{Pos: vec.New(-2.5, 4.5, 3.5), Color: vec.New(8.0, 6.5, 5.0), Range: 10},
		},
	}
}

func holedBoxScene() *scene.Scene {
	// A thin wall (in Z) with a window and a doorway punched through it.
	wall := scene.Box{
		Min: vec.New(-2.2, 0, -0.15), Max: vec.New(2.2, 2.6, 0.15),
		Holes: []scene.AABB{
			{Min: vec.New(-1.6, 1.2, -0.3), Max: vec.New(-0.4, 2.1, 0.3)}, // window
			{Min: vec.New(0.5, 0, -0.3), Max: vec.New(1.4, 2.0, 0.3)},     // doorway
		},
		Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.75, 0.3, 0.2)},
	}
	wall.Xform = scene.NewTransform(0, 18, 0, vec.New(0, 1.3, 0))
	return &scene.Scene{
		Spheres: []scene.Sphere{
			// Sits behind the wall, visible only through the openings.
			{Center: vec.New(0, 1, -2), Radius: 0.8,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.3, 0.8, 0.4)}},
		},
		Boxes: []scene.Box{wall},
		Planes: []scene.Plane{
			{N: vec.New(0, 1, 0), D: 0,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.55, 0.55, 0.6)}},
		},
		Lights: []scene.Light{
			{Pos: vec.New(2.5, 4.5, 4.0), Color: vec.New(8.0, 7.0, 6.0), Range: 14},
		},
	}
}

func texturedScene() *scene.Scene {
	return &scene.Scene{
		Spheres: []scene.Sphere{
			{Center: vec.New(-1.6, 1, 0), Radius: 1,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(1, 1, 1), Tex: texture.Marble}},
			{Center: vec.New(1.6, 1, 0), Radius: 1,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(1, 1, 1), Tex: texture.Stone}},
		},
		Boxes: []scene.Box{
			{Min: vec.New(-0.7, 0, -0.7), Max: vec.New(0.7, 1.4, 0.7),
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(1, 1, 1), Tex: texture.Wood}},
			{Min: vec.New(-3.2, 0, -1), Max: vec.New(-2.4, 2.2, 1),
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(1, 1, 1), Tex: texture.WallpaperNavy}},
			{Min: vec.New(2.4, 0, -1), Max: vec.New(3.2, 2.2, 1),
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(1, 1, 1), Tex: texture.Cement}},
			{Min: vec.New(-1.9, 0, -0.9), Max: vec.New(-0.9, 2.2, -0.7),
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(1, 1, 1), Tex: texture.Brick}},
		},
		Planes: []scene.Plane{
			{N: vec.New(0, 1, 0), D: 0,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.8, 0.8, 0.8), Tex: texture.Grass}},
		},
	}
}

func conicScene() *scene.Scene {
	return &scene.Scene{
		Cylinders: []scene.Cylinder{
			{CX: -1.8, CZ: 0, Radius: 0.7, YMin: 0, YMax: 1.8,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.8, 0.4, 0.3)}},
		},
		Cones: []scene.Cone{
			{CX: 1.8, CZ: 0, YBase: 0, YTip: 1.9, RBase: 0.8,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.3, 0.7, 0.4)}},
		},
		Tori: []scene.Torus{
			{Center: vec.New(0, 1.0, 0), R: 0.8, Rm: 0.28,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.4, 0.5, 0.9)}},
		},
		Planes: []scene.Plane{
			{N: vec.New(0, 1, 0), D: 0,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.6, 0.6, 0.65)}},
		},
		Lights: []scene.Light{
			{Pos: vec.New(-2.5, 4.5, 3.5), Color: vec.New(8.0, 6.5, 5.0), Range: 9},
		},
	}
}

func campfireScene() *scene.Scene {
	sc := &scene.Scene{
		Boxes: []scene.Box{
			{Min: vec.New(-2.5, 0, -0.6), Max: vec.New(-1.9, 1.6, 0.6),
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.7, 0.7, 0.7)}},
		},
		Planes: []scene.Plane{
			{N: vec.New(0, 1, 0), D: 0,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.5, 0.45, 0.4)}},
		},
		Campfires: []scene.Campfire{
			{Center: vec.New(0, 0.3, 0), Color: vec.New(3.0, 1.5, 0.6),
				Brightness: 1, Range: 7, Jitter: 0.15, Flicker: 0.5, Speed: 1, Seed: 0.7},
		},
	}
	return sc
}

// diffuseScene is a controlled scene that only uses the primitives and shading
// the GPU implements so far: diffuse spheres/box/plane, no lights, no emit, no
// checker, no transforms, and a zero Environment (flat 0.04 ambient).
func diffuseScene() *scene.Scene {
	return &scene.Scene{
		Spheres: []scene.Sphere{
			{Center: vec.New(0, 1, 0), Radius: 1,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.9, 0.3, 0.2)}},
			{Center: vec.New(1.6, 0.5, -1), Radius: 0.5,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.2, 0.7, 0.9)}},
		},
		Boxes: []scene.Box{
			{Min: vec.New(-2.2, 0, -1.2), Max: vec.New(-1.2, 1.4, -0.2),
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.3, 0.8, 0.3)}},
		},
		Planes: []scene.Plane{
			{N: vec.New(0, 1, 0), D: 0,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.6, 0.6, 0.65)}},
		},
	}
}

func litShadowScene() *scene.Scene {
	sc := diffuseScene()
	sc.Lights = []scene.Light{
		{Pos: vec.New(-2.5, 4.5, 3.5), Color: vec.New(8.0, 6.5, 5.0), Range: 8},
		{Pos: vec.New(2.8, 2.5, 2.8), Color: vec.New(1.2, 1.8, 3.0), Range: 7},
	}
	return sc
}

func reflectiveScene() *scene.Scene {
	return &scene.Scene{
		Spheres: []scene.Sphere{
			{Center: vec.New(0, 1, 0), Radius: 1,
				Surface: scene.Surface{Mat: scene.MatMirror, Albedo: vec.New(0.9, 0.9, 0.95), IOR: 1.5}},
		},
		Planes: []scene.Plane{
			{N: vec.New(0, 1, 0), D: 0,
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.6, 0.6, 0.65), Reflect: 0.25, IOR: 1.5}},
		},
	}
}

func terrainWaterScene() *scene.Scene {
	trn := scene.Terrain{
		OriginX: -8, OriginZ: -8, SizeX: 16, SizeZ: 16,
		Base: 0, Step: 0.25, GridCell: 0.5,
		Grass: 0, Rock: 0, Snow: 0,
		GrassCol: vec.New(0.28, 0.55, 0.18),
		RockCol:  vec.New(0.28, 0.55, 0.18),
		SnowCol:  vec.New(0.28, 0.55, 0.18),
		SlopeLo:  1, SlopeHi: 2, SnowLo: 100, SnowHi: 101,
	}
	trn.Prepare()
	return &scene.Scene{
		Terrains: []scene.Terrain{trn},
		Waters: []scene.WaterPool{
			{CX: 0, CZ: 1.5, Radius: 1.2, Level: 0.03,
				Surface: scene.Surface{Mat: scene.MatMirror, Albedo: vec.New(0.55, 0.75, 0.9), IOR: 1.5}},
		},
	}
}

func cpuFrame(t *testing.T, sc *scene.Scene, cam *camera.Camera, w, h int) []byte {
	t.Helper()
	tr := trace.New(sc)
	return cpuFrameFromTracer(t, tr, cam, w, h)
}

func cpuFrameFromTracer(t *testing.T, tr *trace.Tracer, cam *camera.Camera, w, h int) []byte {
	t.Helper()
	cpu := render.New(w, h)
	buf := make([]byte, w*h*4)
	cpu.Render(buf, cam, tr, 1)
	return buf
}

// compareFrames returns the mean absolute per-byte error, the max per-byte
// error, and the fraction of pixels with any channel differing by more than 4.
func compareFrames(a, b []byte) (mean float64, max int, fracOver float64) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var sum, over float64
	px := 0
	for i := 0; i+4 <= n; i += 4 {
		px++
		bad := false
		for c := 0; c < 4; c++ {
			d := int(a[i+c]) - int(b[i+c])
			if d < 0 {
				d = -d
			}
			sum += float64(d)
			if d > max {
				max = d
			}
			if d > 4 {
				bad = true
			}
		}
		if bad {
			over++
		}
	}
	if n > 0 {
		mean = sum / float64(n)
	}
	if px > 0 {
		fracOver = over / float64(px)
	}
	return mean, max, fracOver
}
