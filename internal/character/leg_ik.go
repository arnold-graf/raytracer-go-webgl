package character

import (
	"math"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// LegSolve holds solved joint positions for one leg chain.
type LegSolve struct {
	HipSocket vec.V
	CoxaTip   vec.V
	Knee      vec.V
	Foot      vec.V
	PlaneN    vec.V
	Valid     bool
}

const (
	tripodIKSmoothStance = 16.0
	tripodIKSmoothSwing  = 11.0
)

func bodyYawDeg(body *scene.Transform) float64 {
	if body == nil {
		return 0
	}
	return body.YawRad() * 180 / math.Pi
}

// SolveTripodLeg analytically solves coxa aim + femur/tibia IK for one tripod leg.
func SolveTripodLeg(rig *Rig, leg LegDef, foot *Foot, body *scene.Transform, prev LegSolve, dt float64) LegSolve {
	out := prev
	if rig == nil || foot == nil || body == nil {
		return out
	}

	hipSocket := body.ToWorld(rig.JointLocal(leg.Proximal))
	target := foot.World
	if foot.Phase != FootSwing {
		target = clampFootToHipReach(target, hipSocket, spiderMaxHipPlantHoriz*0.94)
	}
	coxaBone := rig.Bones[leg.Proximal]
	femurBone := rig.Bones[leg.Mid]
	tibiaBone := rig.Bones[leg.Distal]

	toTarget := target.Sub(hipSocket)
	targetDir := toTarget
	if targetDir.LenSq() < 1e-12 {
		targetDir = vec.V{Y: -1}
	} else {
		targetDir = targetDir.Normalize()
	}

	heading := bodyYawDeg(body)
	fwd := yawForward(heading)
	rest := hipWorldOffset(body.Translation(), hipSocket.Y, heading, leg.RestOffset)
	radial := legRadialDir(body.Translation(), rest, heading)

	minUp := ikMinCoxaUpStance
	if foot.Phase == FootSwing {
		minUp = ikMinCoxaUpSwing
	}
	restDir := coxaRadialHint(radial, minUp)

	aimDir := restDir
	if foot.Phase != FootSwing {
		if h := horizToTarget(hipSocket, target); h.LenSq() > 1e-12 {
			if h.X*fwd.X+h.Z*fwd.Z > 0.06 {
				aimDir = blendDir(restDir, targetDir, 0.28)
			}
		}
	} else {
		t := easeInOut(foot.SwingT)
		blend := spiderCoxaStanceBlend + (spiderCoxaSwingBlend-spiderCoxaStanceBlend)*t
		aimDir = blendDir(restDir, targetDir, blend)
	}

	coxaBlend := spiderCoxaStanceBlend
	if foot.Phase == FootSwing {
		t := easeInOut(foot.SwingT)
		coxaBlend = spiderCoxaStanceBlend + (spiderCoxaSwingBlend-spiderCoxaStanceBlend)*t
	}
	coxaDir := blendDir(restDir, aimDir, coxaBlend)
	if foot.Phase != FootSwing {
		if clamped, ok := clampSpiderCoxaDir(coxaDir, radial, minUp); ok {
			coxaDir = clamped
		}
	}
	coxaTip := hipSocket.Add(coxaDir.Scale(coxaBone.Length))

	pole := radial.Add(vec.V{Y: -0.35})
	if pole.LenSq() < 1e-12 {
		pole = vec.V{Y: -1}
	}

	minBend := rig.Locomotion.Knee.Stance
	if foot.Phase == FootSwing {
		minBend = rig.Locomotion.Knee.Swing
		if minBend <= 0 {
			minBend = 30
		}
	} else if minBend <= 0 {
		minBend = 20
	}

	reach := clampReachTarget(coxaTip, target, femurBone.Length+tibiaBone.Length-0.02, 0.08)
	res := SolveTwoBoneMinBend(coxaTip, reach, pole, femurBone.Length, tibiaBone.Length, minBend)
	if !res.OK && foot.Phase == FootSwing {
		res = SolveTwoBoneMinBend(coxaTip, reach, pole, femurBone.Length, tibiaBone.Length, 12)
	}
	if !res.OK {
		if prev.Valid {
			return prev
		}
		out.HipSocket = hipSocket
		out.CoxaTip = coxaTip
		return out
	}

	femurDir := res.Mid.Sub(coxaTip)
	tibiaDir := res.End.Sub(res.Mid)
	planeN := radial
	if femurDir.LenSq() > 1e-12 && tibiaDir.LenSq() > 1e-12 {
		n := femurDir.Cross(tibiaDir)
		if n.LenSq() > 1e-12 {
			planeN = n.Normalize()
		}
	}

	goal := LegSolve{
		HipSocket: hipSocket,
		CoxaTip:   coxaTip,
		Knee:      res.Mid,
		Foot:      res.End,
		PlaneN:    planeN,
		Valid:     true,
	}

	if !prev.Valid {
		return goal
	}

	smooth := tripodIKSmoothStance
	if foot.Phase == FootSwing {
		smooth = tripodIKSmoothSwing
		if foot.SwingT < 0.15 {
			smooth = tripodIKSmoothStance
		}
	}
	t := smoothExp(smooth, dt)

	out.HipSocket = hipSocket

	prevCoxaDir := prev.CoxaTip.Sub(prev.HipSocket)
	goalCoxaDir := goal.CoxaTip.Sub(hipSocket)
	smoothedCoxaDir := blendDir(prevCoxaDir, goalCoxaDir, t)
	if foot.Phase != FootSwing {
		if clamped, ok := clampSpiderCoxaDir(smoothedCoxaDir, radial, minUp); ok {
			smoothedCoxaDir = clamped
		}
	} else if clamped, ok := clampSpiderCoxaDir(smoothedCoxaDir, radial, minUp); ok {
		pull := 0.55
		if foot.SwingT > 0.2 && foot.SwingT < 0.8 {
			pull = 0.3
		}
		smoothedCoxaDir = blendDir(smoothedCoxaDir, clamped, pull)
	}
	out.CoxaTip = hipSocket.Add(smoothedCoxaDir.Scale(coxaBone.Length))

	out.Knee = prev.Knee.Scale(1 - t).Add(goal.Knee.Scale(t))
	out.Foot = prev.Foot.Scale(1 - t).Add(goal.Foot.Scale(t))

	maxStep := 0.13 * dt * 60
	if maxStep < 0.02 {
		maxStep = 0.02
	}
	if prev.Valid {
		out.Knee = clampJointStep(prev.Knee, out.Knee, maxStep)
		out.Foot = clampJointStep(prev.Foot, out.Foot, maxStep)
	}
	out.PlaneN = prev.PlaneN.Scale(1-t).Add(goal.PlaneN.Scale(t))
	if out.PlaneN.LenSq() > 1e-12 {
		out.PlaneN = out.PlaneN.Normalize()
	}
	coxaVec := out.CoxaTip.Sub(hipSocket)
	if coxaVec.LenSq() > 1e-12 {
		out.CoxaTip = hipSocket.Add(coxaVec.Normalize().Scale(coxaBone.Length))
	}
	out.Valid = true
	if math.IsNaN(out.Foot.X) || math.IsNaN(out.Foot.Y) || math.IsNaN(out.Foot.Z) {
		out.Valid = false
		if prev.Valid {
			return prev
		}
	}
	return out
}

const (
	spiderCoxaStanceBlend = 0.26
	spiderCoxaSwingBlend  = 0.45
)

func coxaRadialHint(radial vec.V, minUp float64) vec.V {
	horiz := vec.V{X: radial.X, Z: radial.Z}
	if horiz.LenSq() < 1e-12 {
		return vec.V{Y: 1}
	}
	horiz = horiz.Normalize()
	elev := 0.72
	if elev < minUp {
		elev = minUp
	}
	horizScale := math.Sqrt(1 - elev*elev)
	out := vec.V{X: horiz.X * horizScale, Y: elev, Z: horiz.Z * horizScale}
	if out.LenSq() < 1e-12 {
		return vec.V{Y: 1}
	}
	return out.Normalize()
}

func clampSpiderCoxaDir(dir, radial vec.V, minUp float64) (vec.V, bool) {
	if dir.LenSq() < 1e-12 || radial.LenSq() < 1e-12 {
		return vec.V{}, false
	}
	d := dir.Normalize()
	radH := vec.V{X: radial.X, Z: radial.Z}
	if radH.LenSq() < 1e-12 {
		return vec.V{}, false
	}
	radH = radH.Normalize()
	horiz := vec.V{X: d.X, Z: d.Z}
	if hl := horiz.Len(); hl > 1e-12 {
		horiz = horiz.Scale(1 / hl)
	} else {
		horiz = radH
	}
	dot := horiz.X*radH.X + horiz.Z*radH.Z
	minDot := math.Cos(ikMaxCoxaHorizAngle)
	if dot < minDot-1e-6 {
		horiz = blendDir(horiz, radH, 0.9)
		if horiz.LenSq() > 1e-12 {
			horiz = horiz.Normalize()
		}
	}
	elev := d.Y
	if elev < minUp {
		elev = minUp
	}
	if elev > 0.92 {
		elev = 0.92
	}
	horizScale := math.Sqrt(1 - elev*elev)
	out := vec.V{X: horiz.X * horizScale, Y: elev, Z: horiz.Z * horizScale}
	if out.LenSq() < 1e-12 {
		return vec.V{}, false
	}
	return out.Normalize(), true
}

func easeInOut(t float64) float64 {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return t * t * (3 - 2*t)
}

func clampJointStep(prev, next vec.V, maxStep float64) vec.V {
	d := next.Sub(prev)
	if dl := d.Len(); dl > maxStep && dl > 1e-12 {
		return prev.Add(d.Scale(maxStep / dl))
	}
	return next
}

// ApplyTripodLegSolve writes solved joint positions into a skeleton pose.
func ApplyTripodLegSolve(pose *SkeletonPose, leg LegDef, ik LegSolve) {
	if pose == nil || !ik.Valid {
		return
	}
	femurDir := ik.Knee.Sub(ik.CoxaTip)
	tibiaDir := ik.Foot.Sub(ik.Knee)
	if femurDir.LenSq() < 1e-12 || tibiaDir.LenSq() < 1e-12 {
		return
	}
	planeN := ik.PlaneN
	if planeN.LenSq() < 1e-12 {
		planeN = femurDir.Cross(tibiaDir)
		if planeN.LenSq() > 1e-12 {
			planeN = planeN.Normalize()
		}
	}
	pose.Bones[leg.Proximal] = scene.NewTransformYAxis(ik.HipSocket, ik.CoxaTip)
	pose.Bones[leg.Mid] = scene.NewTransformYZ(ik.CoxaTip, femurDir.Normalize(), planeN)
	pose.Bones[leg.Distal] = scene.NewTransformYZ(ik.Knee, tibiaDir.Normalize(), planeN)
}

func clampFootToHipReach(target, hip vec.V, maxReach float64) vec.V {
	delta := vec.V{X: target.X - hip.X, Z: target.Z - hip.Z}
	dist := delta.Len()
	if dist <= maxReach || dist < 1e-9 {
		return target
	}
	s := maxReach / dist
	return vec.V{
		X: hip.X + delta.X*s,
		Y: target.Y,
		Z: hip.Z + delta.Z*s,
	}
}
