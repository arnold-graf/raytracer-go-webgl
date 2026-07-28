package character

import (
	"math"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

const spiderLegSectorMaxDeg = 22.0

// legHomeDirWorld returns the horizontal outward direction for a leg at heading.
func legHomeDirWorld(leg LegDef, heading float64) vec.V {
	socket := hipWorldOffset(vec.V{}, 0, heading, leg.RestOffset)
	dir := vec.V{X: socket.X, Z: socket.Z}
	if dir.LenSq() < 1e-12 {
		right := yawRight(heading)
		if leg.SideSign > 0 {
			return right
		}
		return right.Scale(-1)
	}
	return dir.Normalize()
}

// clampToSector limits target to a horizontal cone around homeDir from hipPos.
func clampToSector(target, hipPos, homeDir vec.V, maxAngleDeg float64) vec.V {
	delta := target.Sub(hipPos)
	horiz := vec.V{X: delta.X, Z: delta.Z}
	reach := horiz.Len()
	if reach < 1e-6 {
		return target
	}
	dir := horiz.Scale(1 / reach)

	home := vec.V{X: homeDir.X, Z: homeDir.Z}
	if home.LenSq() < 1e-12 {
		return target
	}
	home = home.Normalize()

	dot := clampScalar(dir.X*home.X+dir.Z*home.Z, -1, 1)
	maxRad := maxAngleDeg * math.Pi / 180
	angle := math.Acos(dot)
	if angle <= maxRad+1e-9 {
		return target
	}
	clamped := rotateHorizToward(home, dir, maxRad)
	return vec.V{
		X: hipPos.X + clamped.X*reach,
		Y: target.Y,
		Z: hipPos.Z + clamped.Z*reach,
	}
}

// clampFootToLegSector clamps a foot target for leg relative to the body hip.
func clampFootToLegSector(target, bodyHip vec.V, leg LegDef, heading float64) vec.V {
	return clampToSector(target, bodyHip, legHomeDirWorld(leg, heading), spiderLegSectorMaxDeg)
}

// spiderBodyHip returns the horizontal body reference for leg sector clamping.
func spiderBodyHip(bodyXf *scene.Transform) vec.V {
	if bodyXf == nil {
		return vec.V{}
	}
	return bodyXf.Translation()
}

func rotateHorizToward(from, to vec.V, maxRad float64) vec.V {
	cross := from.X*to.Z - from.Z*to.X
	dot := clampScalar(from.X*to.X+from.Z*to.Z, -1, 1)
	angle := math.Atan2(cross, dot)
	if angle > maxRad {
		angle = maxRad
	} else if angle < -maxRad {
		angle = -maxRad
	}
	c, s := math.Cos(angle), math.Sin(angle)
	out := vec.V{X: from.X*c - from.Z*s, Z: from.X*s + from.Z*c}
	if out.LenSq() < 1e-12 {
		return from
	}
	return out.Normalize()
}

func clampScalar(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}

// legHipSocket returns the world position of a leg's coxa/thigh socket.
func legHipSocket(bodyXf *scene.Transform, rig *Rig, leg LegDef) vec.V {
	if bodyXf == nil || rig == nil {
		return vec.V{}
	}
	return bodyXf.ToWorld(rig.JointLocal(leg.Proximal))
}
