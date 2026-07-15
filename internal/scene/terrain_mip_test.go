package scene

import (
	"testing"
)

func TestTerrainMipPyramidLevels(t *testing.T) {
	ter := Terrain{
		OriginX: -50, OriginZ: -50, SizeX: 100, SizeZ: 100,
		Base: 0, Detail: 0.1, DetailScale: 0.1,
	}
	ter.Prepare()
	levels, _, _, _, _ := ter.MipSnapshot()
	if len(levels) < 2 {
		t.Fatalf("expected mip pyramid depth >= 2, got %d", len(levels))
	}
	if levels[0].NX < 2 || levels[0].NZ < 2 {
		t.Fatalf("level 0 dims = %d×%d", levels[0].NX, levels[0].NZ)
	}
	last := levels[len(levels)-1]
	if last.NX != 1 || last.NZ != 1 {
		t.Fatalf("top mip = %d×%d, want 1×1", last.NX, last.NZ)
	}
	parent := levels[0]
	child := levels[1]
	for j := 0; j < child.NZ; j++ {
		for i := 0; i < child.NX; i++ {
			lo := float32(1e30)
			hi := float32(-1e30)
			for dz := 0; dz < 2; dz++ {
				for dx := 0; dx < 2; dx++ {
					ci, cj := i*2+dx, j*2+dz
					if ci >= parent.NX || cj >= parent.NZ {
						continue
					}
					idx := cj*parent.NX + ci
					if v := parent.MinMax[idx*2]; v < lo {
						lo = v
					}
					if v := parent.MinMax[idx*2+1]; v > hi {
						hi = v
					}
				}
			}
			idx := j*child.NX + i
			gotMin := child.MinMax[idx*2]
			gotMax := child.MinMax[idx*2+1]
			if gotMin != lo || gotMax != hi {
				t.Fatalf("child (%d,%d) min/max = %v,%v want %v,%v", i, j, gotMin, gotMax, lo, hi)
			}
		}
	}
}
