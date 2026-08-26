package webgpu

import (
	"testing"
	"unsafe"

	"raytracer/internal/camera"
	"raytracer/internal/probe"
	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/texture"
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
	if got := unsafe.Sizeof(CampfireParams{}); got != campfireStride {
		t.Fatalf("CampfireParams size = %d, want %d", got, campfireStride)
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

func TestPackBoxFaceTextures(t *testing.T) {
	sc := &scene.Scene{
		Spheres: []scene.Sphere{{Center: vec.New(0, 0, 0), Radius: 1}},
		Boxes: []scene.Box{{
			Min: vec.New(0, 0, 0), Max: vec.New(1, 1, 1),
			FaceTex: [6]int{0, 0, 0, 0, texture.CaptureForward, 0},
		}},
	}
	faces := PackBoxFaceTextures(sc)
	if len(faces) != 2*boxFacesPerPrim {
		t.Fatalf("len = %d, want %d", len(faces), 2*boxFacesPerPrim)
	}
	if faces[boxFacesPerPrim+texture.BoxFacePosZ] != uint32(texture.CaptureForward) {
		t.Fatalf("front face tex = %d", faces[boxFacesPerPrim+texture.BoxFacePosZ])
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
	terrains, samples, _, _, _, _, _ := PackTerrains(sc)
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

// viewFor builds a render.View for a static scene with all features on and the
// AO volume baked, mirroring what the app hands the renderer each frame.
func viewFor(sc *scene.Scene) *render.View {
	aoData, aoOK := probe.New(sc).BakeAO()
	return &render.View{
		Scene:  sc,
		Shadow: true,
		Mirror: true,
		AO:     true,
		AOData: aoData,
		AOok:   aoOK,
	}
}

// TestSceneCacheReusesStaticBuffers verifies the scene-buffer cache contract and
// doubles as an end-to-end GPU render smoke test:
//   - a static scene stays "fresh" across frames (no re-pack), and an in-place
//     geometry edit is invisible until scene.Touch() invalidates the cache;
//   - calling Touch forces a rebuild and the new geometry shows up.
//
// This guards the optimization that lets the GPU backend skip per-frame packing
// while remaining correct for the moving-object animation path.
func TestSceneCacheReusesStaticBuffers(t *testing.T) {
	const w, h = 48, 36
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	sc := diffuseScene()
	v := viewFor(sc)
	cam := camera.New()

	frame := func() []byte {
		b := make([]byte, w*h*4)
		r.Render(b, cam, v, 1)
		return b
	}

	base := frame()
	gen := r.cache.gen
	if !r.cache.fresh(v) {
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
	if r.cache.fresh(v) {
		t.Fatal("cache should be stale right after Touch")
	}
	moved := frame()
	if !r.cache.fresh(v) {
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

// diffuseScene is a small scene of diffuse spheres/box/plane (no lights, no
// emit, no transforms) used by the cache/render smoke test.
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
