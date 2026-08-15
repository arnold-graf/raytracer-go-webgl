package sceneio

import (
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func worldBoxCenter(b scene.Box) (float64, float64, float64) {
	cx := (b.Min.X + b.Max.X) / 2
	cy := (b.Min.Y + b.Max.Y) / 2
	cz := (b.Min.Z + b.Max.Z) / 2
	if b.Xform != nil {
		c := b.Xform.ToWorld(vec.V{X: cx, Y: cy, Z: cz})
		return c.X, c.Y, c.Z
	}
	return cx, cy, cz
}

func TestFrontOfficeWorldBoxCount(t *testing.T) {
	direct, err := Load("../../scenes/office-sunset/server-room-front-office.toml")
	if err != nil {
		t.Fatal(err)
	}
	viaIndex, err := Load("../../scenes/office-sunset/index.toml")
	if err != nil {
		t.Fatal(err)
	}
	directN := countWorldRegion(direct, 38, 52, 199, 206)
	indexN := countWorldRegion(viaIndex, 38, 52, 199, 206)
	t.Logf("world boxes in FO region: direct=%d viaIndex=%d", directN, indexN)
	if indexN < directN/2 {
		t.Fatalf("via index missing front office boxes: got %d want ~%d", indexN, directN)
	}
}

func countWorldRegion(s *scene.Scene, xmin, xmax, ymin, ymax float64) int {
	n := 0
	for _, b := range s.Boxes {
		x, y, _ := worldBoxCenter(b)
		if x >= xmin && x <= xmax && y >= ymin && y <= ymax {
			n++
		}
	}
	return n
}

