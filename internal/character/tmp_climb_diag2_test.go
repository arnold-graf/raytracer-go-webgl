package character

import (
	"fmt"
	"testing"
	"raytracer/internal/vec"
)

func TestDiagClimbUpper(t *testing.T) {
	r, _ := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	world := steppedGround{run: 0.4, rise: 0.25, base: 2.0}
	loc := NewLocomotor(r, vec.V{X: -1, Z: 0}, 270, 1.0, world)
	dt := 1.0 / 60.0
	for i := 0; i < 420; i++ {
		loc.Update(dt, r, world)
		if i < 240 || loc.HipPos.X < 3.5 {
			continue
		}
		pose := ComputeLocomotionPose(r, &loc, "idle", world)
		for _, side := range []struct {
			name string; f Foot; thigh, shin string
		}{
			{"L", loc.Left, "thigh_l", "shin_l"},
			{"R", loc.Right, "thigh_r", "shin_r"},
		} {
			f := side.f
			if f.Phase != FootSwing || f.SwingTo.Y-f.PlantGroundY < 0.10 {
				continue
			}
			flex := kneeFlexDeg(r, pose, side.thigh, side.shin)
			fmt.Printf("f=%3d x=%.2f %s t=%.2f plant=%.2f to=%.2f foot=%.2f gy=%.2f flex=%.1f\n",
				i, loc.HipPos.X, side.name, f.SwingT, f.PlantGroundY, f.SwingTo.Y, f.World.Y, loc.GroundY, flex)
		}
	}
}
