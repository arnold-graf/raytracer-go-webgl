package character

import (
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

const (
	spiderStepTriggerFraction = 0.55
	spiderLookaheadFraction   = 0.55
	spiderStepDuration        = 0.36
	spiderLagSlowFraction     = 0.58
	spiderLagStopFraction     = 0.96
)

func (s *SpiderLocomotor) moveOnSurface(dt float64, rig *Rig, world FootWorld) {
	if s.Speed < 0.05 {
		s.Vel = vec.V{}
		s.Body.Vel.X = 0
		s.Body.Vel.Z = 0
		return
	}
	scale := s.bodyMoveScale(rig)
	if scale < 1e-6 {
		s.Vel = vec.V{}
		s.Body.Vel.X = 0
		s.Body.Vel.Z = 0
		return
	}
	fwd := s.surfaceForward()
	if fwd.LenSq() < 1e-12 {
		fwd = yawForward(s.Heading)
	}
	move := fwd.Scale(s.Speed * dt * scale)
	s.Body.Pos = s.Body.Pos.Add(move)
	if world != nil {
		s.followTerrainUnderBody(world)
	} else if s.Up.Y < 0.92 {
		s.snapBodyToSurface(world)
	}

	vel := fwd.Scale(s.Speed * scale)
	s.Vel = slerpVec(s.Vel, vel, 1-spiderWalkDrag)
	s.Body.Vel.X = s.Vel.X
	s.Body.Vel.Z = s.Vel.Z
}

func (s *SpiderLocomotor) bodyMoveScale(rig *Rig) float64 {
	lag := s.maxHipPlantHoriz(rig)
	lagSlow, lagStop := s.lagLimits()
	hardStop := spiderMaxHipPlantHoriz * 1.12
	if lag > hardStop {
		return 0
	}
	scale := 1.0
	if lag > lagStop {
		scale = 0.18
	} else if lag > lagSlow {
		t := (lag - lagSlow) / (lagStop - lagSlow)
		scale = 1.0 - t*0.85
	}
	if scale < 0.14 {
		scale = 0.14
	}
	return scale * s.stanceScale
}

// followTerrainUnderBody keeps hip height above the terrain sample under the body.
func (s *SpiderLocomotor) followTerrainUnderBody(world FootWorld) {
	if world == nil {
		return
	}
	headY := s.Body.Pos.Y + s.RestHeight + 0.5
	gy := world.GroundHeight(s.Body.Pos.X, s.Body.Pos.Z, headY)
	n := world.GroundNormal(s.Body.Pos.X, s.Body.Pos.Z, headY)
	if n.LenSq() > 1e-12 {
		n = n.Normalize()
		s.groundNormal = n
		if n.Y < 0.92 {
			s.Up = n
		} else if s.Up.Y < 0.92 {
			s.Up = vec.V{Y: 1}
		}
	}
	s.GroundY = gy
	goalY := gy + s.RestHeight
	const blend = 0.48
	s.Body.Pos.Y += (goalY - s.Body.Pos.Y) * blend
}

// snapBodyToSurface re-seats the body at rest height above the surface under it.
func (s *SpiderLocomotor) snapBodyToSurface(world FootWorld) {
	if world == nil {
		return
	}
	up := s.Up
	if up.LenSq() < 1e-12 {
		up = vec.V{Y: 1}
	} else {
		up = up.Normalize()
	}
	headY := s.Body.Pos.Y + s.RestHeight + 0.5
	hit := s.probeSurface(world, headY)
	if !hit.Hit {
		gy := world.GroundHeight(s.Body.Pos.X, s.Body.Pos.Z, headY)
		n := world.GroundNormal(s.Body.Pos.X, s.Body.Pos.Z, headY)
		if n.LenSq() < 1e-12 {
			n = vec.V{Y: 1}
		}
		hit = SurfaceHit{
			Point:  vec.V{X: s.Body.Pos.X, Y: gy, Z: s.Body.Pos.Z},
			Normal: n.Normalize(),
			Hit:    true,
		}
	}
	n := hit.Normal
	if n.LenSq() < 1e-12 {
		n = up
	} else {
		n = n.Normalize()
	}
	s.groundNormal = n
	s.GroundY = hit.Point.Y
	goal := hit.Point.Add(n.Scale(s.RestHeight))
	if n.Y > 0.92 {
		s.Body.Pos.Y = goal.Y
		return
	}
	const snapBlend = 0.45
	s.Body.Pos = s.Body.Pos.Scale(1 - snapBlend).Add(goal.Scale(snapBlend))
}

func (s *SpiderLocomotor) probeSurface(world FootWorld, headY float64) SurfaceHit {
	up := s.Up
	if up.LenSq() < 1e-12 {
		up = vec.V{Y: 1}
	} else {
		up = up.Normalize()
	}
	origin := s.Body.Pos.Add(up.Scale(spiderColliderRadius * 0.35))
	maxDist := spiderDownRayLength * spiderColliderLength
	hit := CastFromFootWorld(world, origin, up.Scale(-1), maxDist, headY)
	if hit.Hit {
		return hit
	}
	if s.groundRay == groundForward {
		fwd := s.surfaceForward()
		if fwd.LenSq() > 1e-12 {
			hit = CastFromFootWorld(world, s.Body.Pos, fwd, spiderForwardRayLength*spiderColliderLength, headY)
			if hit.Hit {
				return hit
			}
		}
	}
	return CastFromFootWorld(world, s.Body.Pos, up.Scale(-1), maxDist*1.5, headY)
}

func (s *SpiderLocomotor) advanceBody(dt float64, rig *Rig) {
	s.moveOnSurface(dt, rig, nil)
}

func (s *SpiderLocomotor) lagLimits() (slow, stop float64) {
	return spiderMaxHipPlantHoriz * spiderLagSlowFraction,
		spiderMaxHipPlantHoriz * spiderLagStopFraction
}

// updateChainFootMotion tracks per-foot target velocity for dynamic step timing.
func (s *SpiderLocomotor) updateChainFootMotion(dt float64) {
	if dt <= 0 {
		return
	}
	for i := range s.chains {
		if i >= len(s.Feet) || i >= len(s.steppers) {
			break
		}
		tip := s.Feet[i].World
		if s.steppers[i].isStepping || s.Feet[i].Phase == FootSwing {
			tip = s.chains[i].Target.Position
		}
		c := &s.chains[i]
		c.TipVelocity = tip.Sub(c.LastTip).Scale(1 / dt)
		c.LastTip = tip
	}
}

func (s *SpiderLocomotor) updateFeetDriver(dt float64, rig *Rig, world FootWorld) {
	s.updateFeetViaSteppers(dt, rig, world)
}

func (s *SpiderLocomotor) updateFeetViaSteppers(dt float64, rig *Rig, world FootWorld) {
	if s.Speed < 0.05 {
		for i := range s.Feet {
			if i < len(s.chains) {
				s.Feet[i].World = s.Feet[i].PlantWorld
				s.chains[i].setTarget(footTarget{
					Position: s.Feet[i].PlantWorld,
					Normal:   s.Up,
					Grounded: true,
				})
			}
		}
		for i := range s.steppers {
			s.steppers[i].advanceStep(dt, s, world)
			s.steppers[i].timeSinceStep += dt
		}
		s.syncFeetFromSteppers()
		return
	}

	s.prepareChains(dt, rig)
	pace := s.stepPace(rig, s.gaitStride(rig))
	s.stepMgr.tick(dt, s, rig, world, s.chains, s.steppers, pace.maxSwing)
	s.emergencyReplant(rig, world)
	s.syncFeetFromSteppers()
	s.updateChainFootMotionFromTargets(dt)
}

// updateChainFootMotionFromTargets records velocity from stepper chain targets.
func (s *SpiderLocomotor) updateChainFootMotionFromTargets(dt float64) {
	if dt <= 0 {
		return
	}
	for i := range s.chains {
		if i >= len(s.Feet) || i >= len(s.steppers) {
			break
		}
		tip := s.Feet[i].World
		if i < len(s.steppers) && (s.steppers[i].isStepping || s.Feet[i].Phase == FootSwing) {
			tip = s.chains[i].Target.Position
		}
		c := &s.chains[i]
		c.TipVelocity = tip.Sub(c.LastTip).Scale(1 / dt)
		c.LastTip = tip
	}
}

func (s *SpiderLocomotor) prepareChains(dt float64, rig *Rig) {
	bodyXf := s.rootTransform()
	legs := rig.LegDefs()
	for i, leg := range legs {
		if i >= len(s.chains) || i >= len(s.steppers) || i >= len(s.Feet) {
			break
		}
		hip := bodyXf.ToWorld(rig.JointLocal(leg.Proximal))
		refreshChainHipOnly(&s.chains[i], hip, s.Heading, rig, leg)

		st := &s.steppers[i]
		f := &s.Feet[i]
		if !st.isStepping && f.Initialized {
			s.chains[i].setTarget(footTarget{
				Position: f.PlantWorld,
				Normal:   s.Up,
				Grounded: true,
			})
		}
		if f.Solve.Valid {
			s.chains[i].Error = f.Solve.Foot.Sub(s.chains[i].Target.Position).Len()
		} else {
			s.chains[i].Error = s.chains[i].tip().Sub(s.chains[i].Target.Position).Len()
		}
		if !st.isStepping && f.Initialized {
			hipH := vec.V{X: hip.X, Z: hip.Z}
			plantH := vec.V{X: f.PlantWorld.X, Z: f.PlantWorld.Z}
			lagErr := horizDist(hipH, plantH) - spiderMaxHipPlantHoriz*0.52
			if lagErr > s.chains[i].Error {
				s.chains[i].Error = lagErr
			}
		}
	}
	_ = dt
}

func (s *SpiderLocomotor) syncFeetFromSteppers() {
	for i := range s.steppers {
		if i >= len(s.Feet) || i >= len(s.chains) {
			break
		}
		st := &s.steppers[i]
		f := &s.Feet[i]
		target := s.chains[i].Target
		if st.isStepping {
			f.Phase = FootSwing
			f.SwingT = st.stepT
			f.SwingFrom = st.stepFrom.Position
			f.SwingTo = st.stepTo.Position
			f.World = target.Position
		} else {
			f.Phase = FootMidStance
			f.SwingT = 0
			if target.Grounded {
				f.PlantWorld = target.Position
			}
			f.World = f.PlantWorld
		}
		f.Initialized = true
	}
}

func (s *SpiderLocomotor) applyPlantedFootForces(dt float64, rig *Rig) {
	s.stanceScale = 1
	if rig == nil || s.Speed < 0.05 || dt <= 0 {
		return
	}
	up := s.Up
	if up.LenSq() < 1e-12 {
		up = vec.V{Y: 1}
	} else {
		up = up.Normalize()
	}
	fwd := s.surfaceForward()
	if fwd.LenSq() < 1e-12 {
		fwd = yawForward(s.Heading)
	}

	bodyXf := s.rootTransform()
	legs := rig.LegDefs()
	planted := 0
	var totalPush vec.V
	for i, leg := range legs {
		if i >= len(s.Feet) {
			break
		}
		f := &s.Feet[i]
		if !f.Initialized || f.Phase == FootSwing {
			continue
		}
		planted++
		hip := bodyXf.ToWorld(rig.JointLocal(leg.Proximal))
		pull := projectOnPlane(hip.Sub(f.PlantWorld), up)
		if pull.LenSq() < 1e-10 {
			continue
		}
		stretch := pull.Len()
		if stretch > spiderMaxHipPlantHoriz*0.55 {
			stretch = spiderMaxHipPlantHoriz * 0.55
		}
		totalPush = totalPush.Add(pull.Normalize().Scale(stretch))
	}
	if planted < 3 {
		return
	}

	avgPush := totalPush.Scale(1 / float64(planted))
	force := avgPush.Scale(spiderPlantPushK)

	surfVel := projectOnPlane(s.Vel, up)
	if surfVel.LenSq() > 1e-12 {
		lateral := surfVel.Sub(fwd.Scale(surfVel.Dot(fwd)))
		force = force.Sub(lateral.Scale(spiderPlantLateralK))
	}

	force = projectOnPlane(force, up)
	s.Body.ApplyForce(force, dt)

	drag := surfVel.Scale(-spiderStanceDragK * 0.04)
	dragOppose := -drag.Dot(fwd)
	if dragOppose > 0 {
		s.stanceScale = clampScalar(1.0-dragOppose*0.00035, 0.82, 1.0)
	}
	if pushFwd := avgPush.Dot(fwd); pushFwd > 0.02 {
		s.stanceScale = clampScalar(s.stanceScale+pushFwd*0.06, 0.82, 1.0)
	}
}

func (s *SpiderLocomotor) stepTargetForLeg(i int, leg LegDef, bodyXf *scene.Transform, rig *Rig, world FootWorld, headY, stride float64) vec.V {
	base := s.desiredFootPos(leg, bodyXf, world, headY, stride)
	if i >= len(s.steppers) || i >= len(s.chains) || s.rig == nil {
		return base
	}
	st := &s.steppers[i]
	st.prediction = base
	st.chain.setTarget(footTarget{
		Position: s.Feet[i].PlantWorld,
		Normal:   s.Up,
		Grounded: true,
	})
	if hit := st.findTargetOnSurface(s, rig, world); hit.Grounded {
		base.Y = hit.Position.Y
		base = clampFootToLegSector(base, spiderBodyHip(bodyXf), leg, s.Heading)
		hip := bodyXf.ToWorld(s.rig.JointLocal(leg.Proximal))
		return clampFootToHipReach(base, hip, spiderMaxHipPlantHoriz*0.94)
	}
	return base
}

func (s *SpiderLocomotor) paceScale(rig *Rig) float64 {
	ref := 1.0
	if rig != nil {
		g := rig.GaitForSpeed(s.Speed)
		if g.Speed > 0 {
			ref = g.Speed
		}
	}
	scale := s.Speed / ref
	if scale < 0.25 {
		return 0.25
	}
	if scale > 6 {
		return 6
	}
	return scale
}

func (s *SpiderLocomotor) desiredFootPos(leg LegDef, bodyXf *scene.Transform, world FootWorld, headY, stride float64) vec.V {
	bodyHip := spiderBodyHip(bodyXf)
	anchor := bodyXf.ToWorld(leg.RestOffset)
	anchor = clampFootToLegSector(anchor, bodyHip, leg, s.Heading)

	if world == nil {
		return vec.V{X: anchor.X, Y: s.GroundY, Z: anchor.Z}
	}
	lookahead := stride * spiderLookaheadFraction
	if s.Speed > 1 {
		lookahead += (s.Speed - 1) * 0.06
	}
	fwd := yawForward(s.Heading)
	x := anchor.X + fwd.X*lookahead
	z := anchor.Z + fwd.Z*lookahead
	gy := world.GroundHeight(x, z, headY)
	target := vec.V{X: x, Y: gy, Z: z}
	target = clampFootToLegSector(target, bodyHip, leg, s.Heading)
	hip := bodyXf.ToWorld(s.rig.JointLocal(leg.Proximal))
	return clampFootToHipReach(target, hip, spiderMaxHipPlantHoriz*0.98)
}

func (s *SpiderLocomotor) swingingCount() int {
	n := 0
	for _, f := range s.Feet {
		if f.Initialized && f.Phase == FootSwing {
			n++
		}
	}
	return n
}
