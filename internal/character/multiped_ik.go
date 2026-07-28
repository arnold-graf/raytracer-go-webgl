package character

import (
	"math"

	"raytracer/internal/vec"
)

func clampReachTarget(root, target vec.V, maxReach, minReach float64) vec.V {
	d := target.Sub(root)
	dist := d.Len()
	if dist < 1e-9 {
		return root.Add(vec.V{Y: -minReach})
	}
	if dist > maxReach {
		return root.Add(d.Scale(maxReach / dist))
	}
	if dist < minReach {
		return root.Add(d.Scale(minReach / dist))
	}
	return target
}

func blendDir(a, b vec.V, t float64) vec.V {
	if a.LenSq() < 1e-12 {
		a = vec.V{Y: -1}
	}
	if b.LenSq() < 1e-12 {
		b = vec.V{Y: -1}
	}
	an, bn := a.Normalize(), b.Normalize()
	out := an.Scale(1 - t).Add(bn.Scale(t))
	if out.LenSq() < 1e-12 {
		return vec.V{Y: -1}
	}
	return out.Normalize()
}

const (
	ikCoxaContinuityWeight = 0.35
	ikMinCoxaUpStance      = 0.28
	ikMinCoxaUpSwing       = 0.22
	ikMaxCoxaHorizAngle    = math.Pi / 2 // 90° from body-center outward radial
)

type tripodLegSolve struct {
	j1, j2, end vec.V
	planeN      vec.V
	endErr      float64
	ok          bool
}

func coxaLateralOut(radial, fwd vec.V) vec.V {
	lat := vec.V{
		X: radial.X - fwd.X*(radial.X*fwd.X+radial.Z*fwd.Z),
		Z: radial.Z - fwd.Z*(radial.X*fwd.X+radial.Z*fwd.Z),
	}
	if lat.LenSq() < 1e-12 {
		return radial
	}
	return lat.Normalize()
}

func coxaRestHint(radial, fwd vec.V) vec.V {
	dir := coxaLateralOut(radial, fwd).Add(vec.V{Y: 0.85})
	if dir.LenSq() < 1e-12 {
		return vec.V{Y: 1}
	}
	return dir.Normalize()
}

func horizToTarget(hipSocket, target vec.V) vec.V {
	h := vec.V{X: target.X - hipSocket.X, Z: target.Z - hipSocket.Z}
	if h.LenSq() < 1e-12 {
		return vec.V{}
	}
	return h.Normalize()
}

func clampCoxaDirHoriz(dir, radial, fwd vec.V, maxBackward float64) (vec.V, bool) {
	if dir.LenSq() < 1e-12 {
		return vec.V{}, false
	}
	horiz := vec.V{X: dir.X, Z: dir.Z}
	hl := horiz.Len()
	if hl < 1e-12 {
		return coxaLateralOut(radial, fwd), true
	}
	horiz = horiz.Scale(1 / hl)
	fwdDot := horiz.X*fwd.X + horiz.Z*fwd.Z
	if fwdDot < maxBackward {
		horiz = vec.V{
			X: horiz.X - fwd.X*(fwdDot-maxBackward),
			Z: horiz.Z - fwd.Z*(fwdDot-maxBackward),
		}
		hl = math.Hypot(horiz.X, horiz.Z)
		if hl < 1e-12 {
			horiz = coxaLateralOut(radial, fwd)
		} else {
			horiz = horiz.Scale(1 / hl)
		}
	}
	return horiz, true
}

// clampCoxaDir limits the coxa ball joint so its horizontal aim stays within
// 90° of the leg's outward radial and never points backward along the body.
func clampCoxaDir(dir, radial, fwd vec.V, minUp float64) (vec.V, bool) {
	if dir.LenSq() < 1e-12 {
		return vec.V{}, false
	}
	d := dir.Normalize()
	horiz, ok := clampCoxaDirHoriz(d, radial, fwd, -0.02)
	if !ok {
		return vec.V{}, false
	}
	dot := horiz.X*radial.X + horiz.Z*radial.Z
	minDot := math.Cos(ikMaxCoxaHorizAngle)
	fwdDot := horiz.X*fwd.X + horiz.Z*fwd.Z
	if dot >= minDot-1e-6 && fwdDot >= -0.04 {
		elev := d.Y
		if elev < minUp {
			elev = minUp
		}
		horizScale := math.Sqrt(1 - elev*elev)
		out := vec.V{X: horiz.X * horizScale, Y: elev, Z: horiz.Z * horizScale}
		if out.LenSq() < 1e-12 {
			return vec.V{}, false
		}
		return out.Normalize(), true
	}
	// Past 90° from outward radial: clamp to the forward lateral boundary.
	tangent := coxaArcPerp(coxaLateralOut(radial, fwd), fwd)
	if tangent.LenSq() < 1e-12 {
		tangent = coxaLateralOut(radial, fwd)
	}
	elev := d.Y
	if elev < minUp {
		elev = minUp
	}
	horizScale := math.Sqrt(1 - elev*elev)
	out := vec.V{X: tangent.X * horizScale, Y: elev, Z: tangent.Z * horizScale}
	if out.LenSq() < 1e-12 {
		return vec.V{}, false
	}
	return out.Normalize(), true
}

// coxaArcPerp returns the +90° horizontal limit on the forward side of radial.
func coxaArcPerp(radial, fwd vec.V) vec.V {
	tangent := vec.V{X: -radial.Z, Z: radial.X}
	if tangent.X*fwd.X+tangent.Z*fwd.Z < 0 {
		tangent = tangent.Scale(-1)
	}
	if tangent.LenSq() < 1e-12 {
		return vec.V{}
	}
	return tangent.Normalize()
}

func coxaBackwardPenalty(horiz, radial, fwd vec.V) float64 {
	if horiz.LenSq() < 1e-12 {
		return 0
	}
	h := horiz
	if hl := horiz.Len(); hl > 1e-12 {
		h = horiz.Scale(1 / hl)
	}
	hf := h.X*fwd.X + h.Z*fwd.Z
	if hf < 0 {
		return (-hf) * 10.0
	}
	rf := radial.X*fwd.X + radial.Z*fwd.Z
	if hf < rf-0.02 && rf > 0 {
		return (rf - hf) * 2.0
	}
	return 0
}

func elevatedDir(horiz vec.V, elev float64) vec.V {
	if horiz.LenSq() < 1e-12 {
		return vec.V{}
	}
	out := horiz.Add(vec.V{Y: elev})
	if out.LenSq() < 1e-12 {
		return vec.V{}
	}
	return out.Normalize()
}

// legBendPlaneNormal is the outward normal for femur/tibia plate joints: they
// may only bend in the plane spanned by the coxa and the foot target.
func legBendPlaneNormal(coxaDir, toTarget, radial vec.V) vec.V {
	n := coxaDir.Cross(toTarget)
	if n.LenSq() < 1e-12 {
		n = radial.Cross(vec.V{Y: 1})
	}
	if n.LenSq() < 1e-12 {
		return vec.V{X: 1}
	}
	n = n.Normalize()
	if n.Dot(radial) < 0 {
		n = n.Scale(-1)
	}
	return n
}

func solvePlateTwoBone(j1, target, planeN, radial, hipSocket, right vec.V, sideSign, minOutward float64, femurLen, tibiaLen, minBend float64) TwoBoneResult {
	poleA := tripodPlatePole(j1, planeN, hipSocket, right, sideSign, minOutward)
	res := SolveTwoBoneMinBend(j1, target, poleA, femurLen, tibiaLen, minBend)
	poleB := tripodPlatePole(j1, planeN.Scale(-1), hipSocket, right, sideSign, minOutward)
	alt := SolveTwoBoneMinBend(j1, target, poleB, femurLen, tibiaLen, minBend)
	if !res.OK {
		return alt
	}
	if !alt.OK {
		return res
	}
	outRes := kneeOutwardLateral(res.Mid, hipSocket, right, sideSign)
	outAlt := kneeOutwardLateral(alt.Mid, hipSocket, right, sideSign)
	minLat := minOutward * 0.85
	if outRes < minLat && outAlt > outRes+0.03 {
		return alt
	}
	if outAlt+0.02 < outRes {
		return alt
	}
	if alt.EndError(target)+0.002 < res.EndError(target) {
		return alt
	}
	if res.End.Y < target.Y-0.04 && alt.End.Y > res.End.Y {
		return alt
	}
	if alt.EndError(target) <= res.EndError(target)+0.02 && outAlt > outRes+0.04 {
		return alt
	}
	return res
}

func kneeOutwardLateral(knee, hipSocket, right vec.V, sideSign float64) float64 {
	lat := lateralAlongRight(knee, hipSocket, right)
	if sideSign > 0 {
		return lat
	}
	return -lat
}

func tripodPlatePole(j1, planeN, hipSocket, right vec.V, sideSign, minOutward float64) vec.V {
	lat := minOutward * 1.15
	if sideSign < 0 {
		lat = -minOutward * 1.15
	}
	out := j1.Add(right.Scale(lat)).Add(planeN.Scale(0.40))
	_ = hipSocket
	return out
}

func solveBallCoxaLeg(hipSocket, target, radial, fwd, right vec.V, sideSign, minOutward float64, foot *Foot, coxaLen, femurLen, tibiaLen, minBend float64) tripodLegSolve {
	minUp := ikMinCoxaUpStance
	if foot != nil && foot.Phase == FootSwing {
		minUp = ikMinCoxaUpSwing
	}

	tryDir := func(dir vec.V) tripodLegSolve {
		clamped, ok := clampCoxaDir(dir, radial, fwd, minUp)
		if !ok {
			return tripodLegSolve{}
		}
		dir = clamped
		j1 := hipSocket.Add(dir.Scale(coxaLen))
		toTarget := target.Sub(j1)
		planeN := legBendPlaneNormal(dir, toTarget, radial)
		maxReach := femurLen + tibiaLen - 0.02
		reachTarget := clampReachTarget(j1, target, maxReach, 0)
		res := solvePlateTwoBone(j1, reachTarget, planeN, radial, hipSocket, right, sideSign, minOutward, femurLen, tibiaLen, minBend)
		if !res.OK {
			return tripodLegSolve{}
		}
		return tripodLegSolve{j1: j1, j2: res.Mid, end: res.End, planeN: planeN, endErr: res.EndError(reachTarget), ok: true}
	}

	hint := coxaRestHint(radial, fwd)
	horiz := horizToTarget(hipSocket, target)
	arcFwd := coxaArcPerp(coxaLateralOut(radial, fwd), fwd)
	elev := 0.50
	if foot != nil && foot.Phase == FootSwing {
		elev = 0.30
	}
	elevFoot := elevatedDir(horiz, elev)
	if elevFoot.LenSq() > 1e-12 {
		if clamped, ok := clampCoxaDir(elevFoot, radial, fwd, minUp); ok {
			elevFoot = clamped
		}
	}

	best := tripodLegSolve{endErr: math.Inf(1)}
	bestDir := vec.V{}
	prevJ1 := vec.V{}
	hasPrev := foot != nil && foot.Solve.Valid
	if hasPrev {
		prevJ1 = foot.Solve.CoxaTip
	}

	record := func(alt tripodLegSolve, dir vec.V) {
		if !alt.ok {
			return
		}
		cont := 0.0
		if hasPrev {
			cont = alt.j1.Sub(prevJ1).Len()
		}
		horiz := vec.V{X: dir.X, Z: dir.Z}
		score := alt.endErr + ikCoxaContinuityWeight*cont + coxaBackwardPenalty(horiz, radial, fwd)
		bestScore := math.Inf(1)
		if best.ok {
			bestCont := 0.0
			if hasPrev {
				bestCont = best.j1.Sub(prevJ1).Len()
			}
			bh := vec.V{X: bestDir.X, Z: bestDir.Z}
			bestScore = best.endErr + ikCoxaContinuityWeight*bestCont + coxaBackwardPenalty(bh, radial, fwd)
		}
		if score < bestScore-1e-5 {
			best = alt
			bestDir = dir
		}
	}

	try := func(dir vec.V) {
		clamped, ok := clampCoxaDir(dir, radial, fwd, minUp)
		if !ok {
			return
		}
		record(tryDir(clamped), clamped)
	}

	if hasPrev {
		prevDir := prevJ1.Sub(hipSocket)
		if prevDir.LenSq() > 1e-12 {
			try(prevDir)
		}
	}

	try(hint)
	lateral := coxaLateralOut(radial, fwd)
	// Allowed arc: lateral-up hint → forward lateral limit (never backward of socket).
	if arcFwd.LenSq() > 1e-12 {
		for t := 0.0; t <= 1.001; t += 0.10 {
			try(elevatedDir(blendDir(lateral, arcFwd, t), elev))
		}
	}
	if elevFoot.LenSq() > 1e-12 {
		try(elevFoot)
		for t := 0.0; t <= 1.001; t += 0.08 {
			try(blendDir(hint, elevFoot, t))
		}
		if hasPrev {
			prevDir := prevJ1.Sub(hipSocket)
			if prevDir.LenSq() > 1e-12 {
				for t := 0.0; t <= 1.001; t += 0.12 {
					try(blendDir(prevDir, elevFoot, t))
				}
			}
		}
	}

	// Small azimuth sweep around hint, still clamped inside the arc.
	for _, deg := range []float64{-18, -10, 10, 18} {
		try(rotateY(hint, deg*math.Pi/180))
	}

	return best
}

// solveBallCoxaLegLow is a stance fallback with a lower minimum elevation when the
// standard arc cannot reach the planted foot.
func solveBallCoxaLegLow(hipSocket, target, radial, fwd, right vec.V, sideSign, minOutward float64, foot *Foot, coxaLen, femurLen, tibiaLen, minBend float64) tripodLegSolve {
	minUp := 0.08
	tryDir := func(dir vec.V) tripodLegSolve {
		horiz, ok := clampCoxaDirHoriz(dir, radial, fwd, -0.15)
		if !ok {
			return tripodLegSolve{}
		}
		elev := dir.Y
		if elev < minUp {
			elev = minUp
		}
		hs := math.Sqrt(1 - elev*elev)
		clamped := vec.V{X: horiz.X * hs, Y: elev, Z: horiz.Z * hs}
		if clamped.LenSq() < 1e-12 {
			return tripodLegSolve{}
		}
		clamped = clamped.Normalize()
		j1 := hipSocket.Add(clamped.Scale(coxaLen))
		toTarget := target.Sub(j1)
		planeN := legBendPlaneNormal(clamped, toTarget, radial)
		reachTarget := clampReachTarget(j1, target, femurLen+tibiaLen-0.02, 0)
		res := solvePlateTwoBone(j1, reachTarget, planeN, radial, hipSocket, right, sideSign, minOutward, femurLen, tibiaLen, 5)
		if !res.OK {
			return tripodLegSolve{}
		}
		return tripodLegSolve{j1: j1, j2: res.Mid, end: res.End, planeN: planeN, endErr: res.EndError(reachTarget), ok: true}
	}
	best := tripodLegSolve{endErr: math.Inf(1)}
	hint := coxaRestHint(radial, fwd)
	lateral := coxaLateralOut(radial, fwd)
	arcFwd := coxaArcPerp(lateral, fwd)
	candidates := []vec.V{hint, elevatedDir(lateral, 0.35), elevatedDir(lateral, 0.22)}
	if arcFwd.LenSq() > 1e-12 {
		candidates = append(candidates, elevatedDir(blendDir(lateral, arcFwd, 0.5), 0.28))
	}
	horiz := horizToTarget(hipSocket, target)
	if horiz.LenSq() > 1e-12 {
		candidates = append(candidates, elevatedDir(horiz, 0.25))
	}
	for _, dir := range candidates {
		if alt := tryDir(dir); alt.ok && alt.endErr < best.endErr {
			best = alt
		}
	}
	_ = foot
	return best
}

func rotateY(v vec.V, rad float64) vec.V {
	c, s := math.Cos(rad), math.Sin(rad)
	return vec.V{X: v.X*c - v.Z*s, Y: v.Y, Z: v.X*s + v.Z*c}
}

func smoothTripodLegIK(foot *Foot, res tripodLegSolve, hipSocket, target, radial, fwd, right vec.V, sideSign, minOutward float64, femurLen, tibiaLen, minBend float64) tripodLegSolve {
	if !foot.Solve.Valid {
		return res
	}
	blend := 0.18
	if foot.Phase == FootSwing {
		blend = 0.32
	}
	j1 := foot.Solve.CoxaTip.Scale(1-blend).Add(res.j1.Scale(blend))
	coxaDir := j1.Sub(hipSocket)
	if coxaDir.LenSq() < 1e-12 {
		return res
	}
	if clamped, ok := clampCoxaDir(coxaDir, radial, fwd, ikMinCoxaUpSwing); ok {
		coxaDir = clamped
		j1 = hipSocket.Add(coxaDir.Scale(j1.Sub(hipSocket).Len()))
	} else {
		coxaDir = coxaDir.Normalize()
	}
	toTarget := target.Sub(j1)
	planeN := legBendPlaneNormal(coxaDir, toTarget, radial)
	fix := solvePlateTwoBone(j1, target, planeN, radial, hipSocket, right, sideSign, minOutward, femurLen, tibiaLen, minBend)
	if !fix.OK {
		return res
	}
	if fix.EndError(target) > res.endErr+0.01 {
		return res
	}
	return tripodLegSolve{j1: j1, j2: fix.Mid, end: fix.End, planeN: planeN, endErr: fix.EndError(target), ok: true}
}

// applyTripodLegIK solves and applies tripod leg IK for kinematic multiped rigs.
func applyTripodLegIK(rig *Rig, pose *SkeletonPose, leg LegDef, foot *Foot, heading float64) {
	_ = heading
	hips := pose.Bones["hips"]
	if hips == nil || foot == nil {
		return
	}
	foot.Solve = SolveTripodLeg(rig, leg, foot, hips, foot.Solve, 1.0/60.0)
	ApplyTripodLegSolve(pose, leg, foot.Solve)
}
