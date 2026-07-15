package sceneio_test

import (
	"math"
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
	var padLevel float64
	var padX, padZ float64
	for _, p := range sc.Terrains[0].Pads {
		if math.Abs(p.CenterX) < 0.01 && math.Abs(p.Angle) < 0.01 {
			padLevel = p.Level
			padX, padZ = p.CenterX, p.CenterZ
			break
		}
	}
	natural, ok := sc.NaturalTerrainHeightAt(padX, padZ)
	if !ok {
		t.Fatal("expected natural terrain under villa pad")
	}
	if padLevel < natural+2.5 || padLevel > natural+3.5 {
		t.Fatalf("pad level = %v, want natural(%v)+3", padLevel, natural)
	}
}
