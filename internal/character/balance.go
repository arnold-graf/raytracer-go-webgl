package character

import (
	"raytracer/internal/vec"
)

// updateBodyBalance tilts the body to keep the center of mass over planted feet.
func (loc *Locomotor) updateBodyBalance(rig *Rig, dt float64) {
	legs := rig.LegDefs()
	if len(legs) < 3 {
		return
	}
	loc.ensureFeet(len(legs))

	var planted []vec.V
	for i := range legs {
		f := &loc.Feet[i]
		if f.Initialized && f.Phase != FootSwing {
			planted = append(planted, f.PlantWorld)
		}
	}
	if len(planted) < 2 {
		return
	}

	com := loc.HipPos
	fwd, right := yawForward(loc.Heading), yawRight(loc.Heading)

	var frontH, rearH, leftH, rightH float64
	var nFront, nRear, nLeft, nRight int
	for _, p := range planted {
		rel := vec.V{X: p.X - com.X, Z: p.Z - com.Z}
		f := rel.Dot(fwd)
		l := rel.Dot(right)
		if f < -0.05 {
			frontH += p.Y
			nFront++
		} else if f > 0.05 {
			rearH += p.Y
			nRear++
		}
		if l > 0.05 {
			leftH += p.Y
			nLeft++
		} else if l < -0.05 {
			rightH += p.Y
			nRight++
		}
	}

	targetPitch, targetRoll := 0.0, 0.0
	if nFront > 0 && nRear > 0 {
		targetPitch = clamp((rearH/float64(nRear)-frontH/float64(nFront))*18, -10, 10)
	}
	if nLeft > 0 && nRight > 0 {
		targetRoll = clamp((leftH/float64(nLeft)-rightH/float64(nRight))*14, -8, 8)
	}

	var centroid vec.V
	for _, p := range planted {
		centroid = centroid.Add(p)
	}
	centroid = centroid.Scale(1 / float64(len(planted)))
	latErr := lateralAlongRight(centroid, com, right)
	targetRoll += clamp(-latErr*10, -5, 5)

	blend := 4.0 * dt
	if blend > 1 {
		blend = 1
	}
	loc.BodyPitch += (targetPitch - loc.BodyPitch) * blend
	loc.BodyRoll += (targetRoll - loc.BodyRoll) * blend
}
