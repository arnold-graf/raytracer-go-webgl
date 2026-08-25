package texture_test

import (
	"testing"

	"raytracer/internal/texture"
	"raytracer/internal/vec"
)

func TestTilesHeightRange(t *testing.T) {
	for x := -2.0; x <= 2.0; x += 0.17 {
		for y := -2.0; y <= 2.0; y += 0.13 {
			p := vec.V{X: x, Y: y, Z: 0}
			h := texture.Height(texture.Tiles, p, vec.New(0, 0, 1), 0.3, 0.1)
			if h < 0 || h > 1 {
				t.Fatalf("height at %v = %v, want 0..1", p, h)
			}
		}
	}
}
