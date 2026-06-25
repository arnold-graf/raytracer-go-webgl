package sceneio_test

import (
	"math"
	"path/filepath"
	"strings"
	"testing"

	"raytracer/internal/sceneio"
)

func TestInstancedVillaUsesPadLevel(t *testing.T) {
	root := filepath.Join("..", "..")
	villa := filepath.Join(root, "scenes", "outdoors-night-villa.toml")
	sc, err := sceneio.Load(villa)
	if err != nil {
		t.Fatal(err)
	}
	if !sc.HasInstancing() {
		t.Fatal("expected instancing")
	}
	cat := sc.Instancing()
	const padLevel = 3.0
	var villaPlacements int
	for _, pl := range cat.Placements {
		src := cat.Templates[pl.TemplateIndex].Source
		if !strings.Contains(src, "art-nouveau-villa.toml") {
			continue
		}
		villaPlacements++
		y := pl.Xform.Translation().Y
		if math.Abs(y-padLevel) > 0.01 {
			t.Fatalf("villa placement y=%v, want pad level %v", y, padLevel)
		}
	}
	if villaPlacements != 2 {
		t.Fatalf("villa placements = %d, want 2", villaPlacements)
	}
	// Materialized CPU geometry should match GPU instancing transforms.
	for i := range sc.Boxes {
		mn, mx := sc.Boxes[i].WorldBounds()
		if mn.Y >= padLevel-0.05 && mn.Y <= padLevel+0.05 && mx.Y >= padLevel+1.0 {
			return // stone plinth base at pad grade
		}
	}
	t.Fatal("expected materialized villa plinth base near pad level y=3")
}
