package texture_test

import (
	"testing"

	"raytracer/internal/texture"
	"raytracer/internal/vec"
)

func TestParquetFloorVariation(t *testing.T) {
	n := vec.New(0, 1, 0)
	tint := vec.New(1, 1, 1)
	c0 := texture.EvalWithNormal(texture.ParquetFloor, vec.New(0, 0, 0), n, tint)
	c1 := texture.EvalWithNormal(texture.ParquetFloor, vec.New(0.21, 0, 0.13), n, tint)
	dx := c0.X - c1.X
	dy := c0.Y - c1.Y
	dz := c0.Z - c1.Z
	if dx*dx+dy*dy+dz*dz < 0.0004 {
		t.Fatalf("expected color variation across planks, c0=%v c1=%v", c0, c1)
	}
}
