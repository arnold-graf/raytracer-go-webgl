package sceneio_test

import (
	"path/filepath"
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
	const padLevel = 3.0
	// Villas are merged (not instanced); materialized CPU geometry should sit on pads.
	for i := range sc.Boxes {
		mn, mx := sc.Boxes[i].WorldBounds()
		if mn.Y >= padLevel-0.05 && mn.Y <= padLevel+0.05 && mx.Y >= padLevel+1.0 {
			return // stone plinth base at pad grade
		}
	}
	t.Fatal("expected materialized villa plinth base near pad level y=3")
}
