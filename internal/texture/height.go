package texture

import (
	"math"

	"raytracer/internal/vec"
)

// TilesDisplacement is the GPU tiles protrusion amplitude in meters.
const TilesDisplacement = 0.018

// Height returns a 0..1 displacement height for supported textures (mortar low,
// detail high). Used for CPU parity checks; the GPU uses tiles_height_2d.
func Height(id int, p, n vec.V, texU, texV float64) float64 {
	if id == Tiles {
		u, v := tilesUV(p, n)
		return tilesHeight2D(texU, texV, u, v)
	}
	return 0
}

func tilesUV(p, n vec.V) (u, v float64) {
	aw := vec.New(math.Abs(n.X), math.Abs(n.Y), math.Abs(n.Z))
	if aw.Z >= aw.X && aw.Z >= aw.Y {
		return p.X, p.Y
	}
	if aw.X >= aw.Y {
		return p.Z, p.Y
	}
	return p.X, p.Z
}

func tilesHeight2D(tileW, tileH, u, v float64) float64 {
	if tileW <= 0 {
		tileW = 1
	}
	if tileH <= 0 {
		tileH = 1
	}
	fu := frac(u / tileW)
	fv := frac(v / tileH)
	edge := math.Min(math.Min(fu, 1-fu), math.Min(fv, 1-fv))
	mask := smoothstep(0.02, 0.08, edge)
	col := math.Floor(u / tileW)
	row := math.Floor(v / tileH)
	relief := 1 - 0.08*cellRand(col, row, 3)
	return mask * relief
}
