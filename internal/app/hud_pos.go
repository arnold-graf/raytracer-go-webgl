package app

import (
	"fmt"
	"time"

	"raytracer/internal/vec"
)

const hudPosInterval = 500 * time.Millisecond

func formatHUDPos(p vec.V) string {
	return fmt.Sprintf("[%.1f, %.1f, %.1f]", p.X, p.Z, p.Y)
}

func (g *Game) updateHUDPos() {
	if g.hudPos != "" && time.Since(g.hudPosAt) < hudPosInterval {
		return
	}
	g.hudPos = formatHUDPos(g.cam.Pos)
	g.hudPosAt = time.Now()
}
