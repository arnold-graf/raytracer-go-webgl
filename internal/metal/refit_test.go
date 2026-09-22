package metal

import (
	"os"
	"path/filepath"
	"testing"

	"raytracer/internal/camera"
	"raytracer/internal/probe"
	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
)

// TestRefitAfterTransformChange is the dynamic-scene path: a scene whose
// instance transforms move must refit the acceleration structures rather than
// rebuild them, and must keep rendering. A rebuild every frame would work and
// be slow, so the assertion is on which path ran, not only on the pixels.
func TestRefitAfterTransformChange(t *testing.T) {
	// Both scene shapes, because they exercise different structures: a
	// non-instanced scene refits one BLAS pair, an instanced one refits
	// fifteen plus two top-level structures. The instanced case is the one
	// that hung the GPU when the structures were not built with
	// MTLAccelerationStructureUsageRefit.
	for _, scenePath := range []string{"default.toml", "office-sunset/index.toml"} {
		t.Run(scenePath, func(t *testing.T) { refitOnce(t, scenePath) })
	}
}

func refitOnce(t *testing.T, sceneName string) {
	if !Supported() {
		t.Skip("no ray-tracing capable Metal device")
	}
	root := filepath.Join("..", "..")
	scenePath := filepath.Join(root, "scenes", sceneName)
	if _, err := os.Stat(scenePath); err != nil {
		t.Skip("villa scene unavailable")
	}
	sc, err := sceneio.Load(scenePath)
	if err != nil {
		t.Fatal(err)
	}
	// Dynamic bodies are what actually move at runtime: NPC limbs are
	// primitives the cache repacks in spans, not instance placements, which
	// are static scenery. The NPC system registers them at spawn, so the test
	// registers one over existing spheres -- the same structure, without
	// pulling in physics.
	if len(sc.Spheres) < 2 {
		t.Skip("scene has too few spheres to stand in for a dynamic body")
	}
	sc.DynamicBodies = append(sc.DynamicBodies, scene.DynamicBody{
		Name: "refit-test", Spheres: [2]int{0, 2},
	})
	sc.Touch()
	body := sc.DynamicBodies[0]

	const w, h = 128, 80
	r, err := New(w, h)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer r.Release()

	cam := camera.New()
	cam.Pos, cam.Yaw, cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
	pb := probe.New(sc)
	aoData, aoOK := pb.BakeAO()
	giMin, giMax, giOK := pb.LitBounds()
	view := &render.View{
		Scene: sc, Shadow: true, Mirror: true, AO: true,
		AOData: aoData, AOok: aoOK, GIMin: giMin, GIMax: giMax, GIBoundsOK: giOK,
		AdaptiveAA: true, ColorQuant: 3, MaxBounceDepth: 4,
	}
	buf := make([]byte, w*h*4)

	r.Render(buf, cam, view, 1)
	if err := r.Err(); err != nil {
		t.Fatalf("first frame: %v", err)
	}
	builds, refits := r.AccelStats()
	if builds != 1 || refits != 0 {
		t.Fatalf("first frame: builds=%d refits=%d, want 1/0", builds, refits)
	}
	first := append([]byte(nil), buf...)

	sc.Spheres[body.Spheres[0]].Center.Y += 0.35
	sc.TouchTransforms()

	r.Render(buf, cam, view, 1)
	if err := r.Err(); err != nil {
		t.Fatalf("second frame: %v", err)
	}
	builds, refits = r.AccelStats()
	if builds != 1 {
		t.Errorf("builds=%d after a transform-only change, want 1 (a refit should have covered it)", builds)
	}
	if refits == 0 {
		t.Errorf("refits=%d, want at least 1", refits)
	}

	// A third frame with nothing moving must do neither.
	r.Render(buf, cam, view, 1)
	if b, f := r.AccelStats(); b != builds || f != refits {
		t.Errorf("static frame rebuilt or refit: builds %d->%d refits %d->%d", builds, b, refits, f)
	}
	_ = first
}

// TestRefitMatchesRebuild checks the thing "it did not hang" does not: that a
// refit structure still traces the scene correctly. A refit keeps the tree's
// topology, so it can only get worse as geometry travels; after one small move
// it should agree with a structure rebuilt from the same state.
func TestRefitMatchesRebuild(t *testing.T) {
	if !Supported() {
		t.Skip("no ray-tracing capable Metal device")
	}
	const w, h = 256, 160
	render := func(refit bool) []byte {
		t.Setenv("RT_METAL_REFIT", map[bool]string{true: "1", false: "0"}[refit])
		sc, view, cam := loadRefitScene(t, "office-sunset/index.toml")
		r, err := New(w, h)
		if err != nil {
			t.Fatalf("new: %v", err)
		}
		defer r.Release()
		buf := make([]byte, w*h*4)
		r.Render(buf, cam, view, 1)
		sc.Spheres[0].Center.Y += 0.25
		sc.TouchTransforms()
		r.Render(buf, cam, view, 1)
		if err := r.Err(); err != nil {
			t.Fatalf("render (refit=%v): %v", refit, err)
		}
		b, f := r.AccelStats()
		if refit && f == 0 {
			t.Fatalf("expected a refit, got builds=%d refits=%d", b, f)
		}
		if !refit && f != 0 {
			t.Fatalf("expected no refit, got builds=%d refits=%d", b, f)
		}
		return append([]byte(nil), buf...)
	}

	a, b := render(true), render(false)
	diff := 0
	for i := 0; i+3 < len(a); i += 4 {
		d := 0
		for k := 0; k < 3; k++ {
			if v := int(a[i+k]) - int(b[i+k]); v > d {
				d = v
			} else if -v > d {
				d = -v
			}
		}
		if d >= 8 {
			diff++
		}
	}
	px := len(a) / 4
	// Some drift is expected: a refit tree has different bounds than a rebuilt
	// one, so traversal can break a near-equal-distance tie differently.
	if diff*100 > px*2 {
		t.Errorf("refit vs rebuild: %d of %d pixels differ by >=8 (%.1f%%), want under 2%%",
			diff, px, 100*float64(diff)/float64(px))
	} else {
		t.Logf("refit vs rebuild: %d of %d pixels differ by >=8 (%.2f%%)",
			diff, px, 100*float64(diff)/float64(px))
	}
}

// loadRefitScene builds a scene with a dynamic body registered over existing
// spheres, which is what the NPC system does at spawn and what actually marks
// transforms dirty at runtime.
func loadRefitScene(t *testing.T, name string) (*scene.Scene, *render.View, *camera.Camera) {
	t.Helper()
	sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", name))
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Spheres) < 2 {
		t.Skip("scene has too few spheres to stand in for a dynamic body")
	}
	sc.DynamicBodies = append(sc.DynamicBodies, scene.DynamicBody{
		Name: "refit-test", Spheres: [2]int{0, 2},
	})
	sc.Touch()
	cam := camera.New()
	cam.Pos, cam.Yaw, cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
	pb := probe.New(sc)
	aoData, aoOK := pb.BakeAO()
	giMin, giMax, giOK := pb.LitBounds()
	return sc, &render.View{
		Scene: sc, Shadow: true, Mirror: true, AO: true,
		AOData: aoData, AOok: aoOK, GIMin: giMin, GIMax: giMax, GIBoundsOK: giOK,
		AdaptiveAA: true, ColorQuant: 3, MaxBounceDepth: 4,
	}, cam
}
