package trace

import (
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// floorScene is a single large box acting as a floor; surfaces just above it
// should read "open upward" and "occluded downward".
func floorScene() *scene.Scene {
	return &scene.Scene{
		Boxes: []scene.Box{
			{Min: vec.New(-5, -1, -5), Max: vec.New(5, 0, 5),
				Surface: scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.8, 0.8, 0.8)}},
		},
	}
}

func TestAOVolumeBakesAndStaysStable(t *testing.T) {
	tr := New(floorScene())
	tr.Opts.AO = true
	tr.Prepare()
	if tr.aoVol == nil {
		t.Fatal("AO volume was not baked")
	}

	p := vec.New(0, 0.01, 0) // a point on the floor
	up := vec.New(0, 1, 0)
	down := vec.New(0, -1, 0)

	visUp := tr.ambientOcclusion(p, up)
	visDown := tr.ambientOcclusion(p, down)

	// The upward-facing hemisphere is open; the downward-facing one stares into
	// the floor and must be more occluded.
	if visUp <= visDown {
		t.Errorf("expected up (%.3f) brighter than down (%.3f)", visUp, visDown)
	}
	// Open direction should be close to unoccluded; nothing is fully black.
	if visUp < 0.75 {
		t.Errorf("open upward visibility too dark: %.3f", visUp)
	}
	if visDown < aoVolMinVis-1e-6 {
		t.Errorf("visibility %.3f fell below the clamp %.3f", visDown, aoVolMinVis)
	}

	// Stability: the same world point + normal must return the same value
	// regardless of how many times (or "from where") it is queried.
	if again := tr.ambientOcclusion(p, up); again != visUp {
		t.Errorf("AO not stable: %.6f vs %.6f", again, visUp)
	}
}

func TestAOVolumeEmptySceneIsNoop(t *testing.T) {
	tr := New(&scene.Scene{})
	tr.Opts.AO = true
	tr.Prepare()
	if got := tr.ambientOcclusion(vec.New(0, 0, 0), vec.New(0, 1, 0)); got != 1 {
		t.Errorf("empty scene AO = %.3f, want 1 (no-op)", got)
	}
}
