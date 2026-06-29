package npc

import (
	"path/filepath"
	"testing"

	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func TestFindPathCrossesStairsNotAround(t *testing.T) {
	sc, err := sceneio.Load(repoFile("scenes/npc-test.toml"))
	if err != nil {
		t.Fatal(err)
	}
	start := vec.V{X: -1, Z: 0}
	goal := vec.V{X: 6.5, Z: 0}
	path := FindPath(sc, start, goal, 0)
	if len(path) < 2 {
		t.Fatalf("path = %v, want polyline across scene", path)
	}
	maxZ := 0.0
	minZ := 0.0
	for _, p := range path {
		if p.Z > maxZ {
			maxZ = p.Z
		}
		if p.Z < minZ {
			minZ = p.Z
		}
	}
	if maxZ > 0.35 || minZ < -0.35 {
		t.Fatalf("path detours around stairs (z range %.2f..%.2f): %v", minZ, maxZ, path)
	}
}

func repoFile(rel string) string {
	return filepath.Join("..", "..", rel)
}
