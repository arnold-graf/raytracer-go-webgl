package app

import (
	"fmt"
	"math"
	"time"

	"raytracer/internal/vec"
)

const hudPosInterval = 500 * time.Millisecond

// formatHUDPos prints the camera pose as x, y, z then yaw and pitch in degrees —
// everything cmd/gpuprof needs to reproduce the view, in the order and units its
// flags take:
//
//	gpuprof -scene S -cam-x X -cam-y Y -cam-z Z -yaw-deg YAW -pitch-deg PITCH
//
// Position alone is not enough to reproduce a frame, and chasing a shading bug
// from screenshots without the heading cost several rounds of guessing. The
// order was also x, z, y at one point, which silently put the camera somewhere
// else entirely.
func formatHUDPos(p vec.V, yaw, pitch float64) string {
	return fmt.Sprintf("[%.1f, %.1f, %.1f] yaw %.1f pitch %.2f",
		p.X, p.Y, p.Z, yaw*180/math.Pi, pitch*180/math.Pi)
}

func (g *Game) updateHUDPos() {
	if g.hudPos != "" && time.Since(g.hudPosAt) < hudPosInterval {
		return
	}
	g.hudPos = formatHUDPos(g.cam.Pos, g.cam.Yaw, g.cam.Pitch)
	g.hudPosAt = time.Now()
}
