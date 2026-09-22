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
	if !Supported() {
		t.Skip("no ray-tracing capable Metal device")
	}
	root := filepath.Join("..", "..")
	scenePath := filepath.Join(root, "scenes", "default.toml")
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
