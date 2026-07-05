package scene

import (
	"testing"

	"raytracer/internal/vec"
)

func TestDocumentRestTransformFlatOnSurface(t *testing.T) {
	const (
		w   = 0.21
		h   = 0.297
		d   = 0.002
		sy  = 0.9
		eps = 1e-6
	)
	rest := DocumentRestTransform(vec.New(0, sy, 0), w, h, d, -90, 20, 0, nil)
	minY := worldMinY(rest, w, h, d)
	if minY < sy-eps || minY > sy+eps {
		t.Fatalf("flat paper minY = %v, want %v", minY, sy)
	}
}

func TestDocumentRestTransformUprightOnSurface(t *testing.T) {
	const (
		w   = 0.21
		h   = 0.297
		d   = 0.002
		sy  = 0.9
		eps = 1e-6
	)
	rest := DocumentRestTransform(vec.New(0, sy, 0), w, h, d, 0, 0, 0, nil)
	minY := worldMinY(rest, w, h, d)
	if minY < sy-eps || minY > sy+eps {
		t.Fatalf("upright paper minY = %v, want %v", minY, sy)
	}
}

func worldMinY(xf *Transform, w, h, d float64) float64 {
	half := vec.V{X: w / 2, Y: h / 2, Z: d / 2}
	minY := lowestRotatedY(half, 0, 0, 0) // unused; compute from world corners
	_ = minY
	min := 1e9
	for _, sx := range [2]float64{-1, 1} {
		for _, sy := range [2]float64{-1, 1} {
			for _, sz := range [2]float64{-1, 1} {
				p := xf.ToWorld(vec.New(sx*half.X, sy*half.Y, sz*half.Z))
				if p.Y < min {
					min = p.Y
				}
			}
		}
	}
	return min
}
