package sceneio_test

import (
	"math"
	"path/filepath"
	"testing"

	"raytracer/internal/sceneio"
)

func TestFrontOfficeBoxesNearCenter(t *testing.T) {
	path := filepath.Join("..", "..", "scenes", "office-sunset", "server-room-front-office.toml")
	s, err := sceneio.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cx, cz := 5.0, 10.0
	for i, b := range s.Boxes {
		if b.Max.Y < 0.05 || b.Min.Y > 2.5 {
			continue
		}
		mx := (b.Min.X + b.Max.X) / 2
		mz := (b.Min.Z + b.Max.Z) / 2
		w := b.Max.X - b.Min.X
		d := b.Max.Z - b.Min.Z
		h := b.Max.Y - b.Min.Y
		if mx < -0.5 || mx > 10.5 || mz < -0.5 || mz > 20.5 {
			continue
		}
		dist := math.Hypot(mx-cx, mz-cz)
		t.Logf("box[%d] center=(%.2f,%.2f,%.2f) size=(%.2f,%.2f,%.2f) mat=%v collides=%v dist=%.1f",
			i, mx, (b.Min.Y+b.Max.Y)/2, mz, w, h, d, b.Mat, b.Collides(), dist)
	}
}
