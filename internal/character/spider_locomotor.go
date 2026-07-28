package character

import (
	"math"

	"raytracer/internal/physics"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// Spider tuning — Unity PhilS94 port + physics feedback.
const (
	spiderScale              = 1.0
	spiderColliderRadius     = 0.45
	spiderColliderLength     = 1.2
	spiderDefaultMass        = 85.0
	spiderWalkSpeed          = 1.0
	spiderRunSpeed           = 4.0
	spiderWalkDrag           = 0.15
	spiderTurnSpeed          = 180.0 // deg/s
	spiderGravityMultiplier  = 3.0
	spiderGroundAdjustSpeed  = 2.5
	spiderForwardAdjustSpeed = 4.0
	spiderLegCentroidSpeed   = 8.0
	spiderLegCentroidNormW   = 0.85
	spiderLegCentroidTanW    = 0.15
	spiderLegNormalSpeed     = 6.0
	spiderLegNormalWeight    = 0.35
	spiderBodyOffsetHeight   = 0.12
	spiderMaxHipPlantHoriz   = 1.02
	spiderHoverK             = 520.0
	spiderHoverDamp          = 42.0
	spiderOrientK            = 140.0
	spiderOrientDamp         = 22.0
	spiderLinDrag            = 2.5
	spiderAngDrag            = 4.0
	spiderMaxTiltDeg         = 75.0
	spiderUprightMaxRollDeg  = 12.0
	spiderUprightMinUpY      = 0.92
	spiderStanceDragK        = 32.0
	spiderPlantPushK         = 38.0
	spiderPlantLateralK      = 22.0
	spiderLegPoseWeightFlat  = 1.0
	spiderDownRaySize        = 0.9
	spiderForwardRaySize     = 0.66
	spiderDownRayLength      = 1.1
	spiderForwardRayLength   = 0.85
	spiderGravityOffDist     = 0.35
)

// SpiderLocomotor drives procedural wall-walking spider locomotion.
type SpiderLocomotor struct {
	Body         physics.Body
	Up           vec.V
	Vel          vec.V
	Feet         []Foot
	chains       []legChain
	steppers     []legStepper
	stepMgr      legStepManager
	bodyCentroid vec.V
	groundDist   float64
	grounded     bool
	groundRay    groundRayKind
	groundNormal vec.V
	GroundY      float64
	Heading      float64
	Speed        float64
	RestHeight   float64
	rig          *Rig
	timeStill    float64
	stanceScale  float64
}

type groundRayKind int

const (
	groundNone groundRayKind = iota
	groundDown
	groundForward
)

// NewSpiderLocomotor spawns a spider grounded at spawn with feet from idle FK.
func NewSpiderLocomotor(rig *Rig, spawn vec.V, heading, speed float64, world FootWorld) SpiderLocomotor {
	headY := spawn.Y + rig.HipHeight + 0.5
	gy := spawn.Y
	if world != nil {
		gy = world.GroundHeight(spawn.X, spawn.Z, headY)
	}
	hip := HipPositionFromGround(spawn.X, gy, spawn.Z, rig.HipHeight)
	up := vec.V{Y: 1}
	if world != nil {
		up = world.GroundNormal(spawn.X, spawn.Z, headY)
		if up.LenSq() < 1e-12 {
			up = vec.V{Y: 1}
		}
	}

	s := SpiderLocomotor{
		Body:       physics.NewBody(hip, heading, spiderDefaultMass),
		Up:         up.Normalize(),
		GroundY:    gy,
		Heading:    heading,
		Speed:      speed,
		RestHeight: rig.HipHeight,
		rig:        rig,
	}
	bodyXf := s.rootTransform()
	legs := rig.LegDefs()
	s.chains = make([]legChain, len(legs))
	s.steppers = make([]legStepper, len(legs))
	s.Feet = make([]Foot, len(legs))
	mode := legStepQueueNoWait
	if rig != nil {
		mode = legStepModeFromConfig(rig.Locomotion.MultipedStepMode)
	}
	s.stepMgr = newLegStepManager(legs, mode)

	for i, leg := range legs {
		chain := initLegChain(rig, leg, bodyXf)
		if world != nil {
			tip := chain.tip()
			gy := world.GroundHeight(tip.X, tip.Z, headY)
			if tip.Y > gy+0.05 {
				tip.Y = gy
			}
			bodyHip := spiderBodyHip(bodyXf)
			tip = clampFootToLegSector(tip, bodyHip, leg, heading)
			chain.Target = footTarget{Position: tip, Normal: up, Grounded: true}
			chain.Points[3] = tip
		}
		s.chains[i] = chain
		s.steppers[i] = newLegStepper(&s.chains[i], leg, spiderAsyncNeighbors(legs, i))
		s.Feet[i] = Foot{
			World: tipFromChain(chain), PlantWorld: tipFromChain(chain),
			Phase: FootMidStance, Initialized: true,
		}
	}
	s.bodyCentroid = s.defaultCentroid()
	s.groundCheck(world)
	s.solveAllLegIK(1.0/60.0, rig, world)
	for i := range s.chains {
		if i < len(s.Feet) && s.Feet[i].Solve.Valid {
			s.chains[i].Error = s.Feet[i].Solve.Foot.Sub(s.chains[i].Target.Position).Len()
			s.chains[i].LastTip = s.Feet[i].Solve.Foot
		}
	}
	return s
}

func (s *SpiderLocomotor) solveAllLegIK(dt float64, rig *Rig, world FootWorld) {
	if s == nil || rig == nil {
		return
	}
	bodyXf := s.rootTransform()
	headY := s.Body.Pos.Y + s.RestHeight + 0.5
	legs := rig.LegDefs()
	for i, leg := range legs {
		if i >= len(s.Feet) || i >= len(s.chains) {
			break
		}
		foot := &s.Feet[i]
		prev := foot.Solve
		foot.Solve = SolveTripodLeg(rig, leg, foot, bodyXf, prev, dt)
		if world != nil && foot.Phase != FootSwing {
			gy := world.GroundHeight(foot.Solve.Foot.X, foot.Solve.Foot.Z, headY)
			if foot.Solve.Foot.Y < gy-0.02 {
				foot.Solve.Foot.Y = gy
			}
		}
		s.chains[i].Error = foot.Solve.Foot.Sub(foot.World).Len()
	}
}

// Update advances locomotion, IK, and stepping.
func (s *SpiderLocomotor) Update(dt float64, rig *Rig, world FootWorld) {
	if s == nil || rig == nil {
		return
	}
	s.rig = rig
	if rig != nil {
		s.stepMgr.mode = legStepModeFromConfig(rig.Locomotion.MultipedStepMode)
	}
	if sw, ok := world.(SpiderWorld); ok {
		s.groundCheck(sw)
		if s.Speed >= 0.05 {
			s.alignToSurface(dt)
		}
	} else {
		s.Up = vec.V{Y: 1}
	}
	s.stanceScale = 1
	s.moveOnSurface(dt, rig, world)
	if s.Up.Y < 0.92 {
		s.applyStickForce(dt, world)
	}
	s.updateFeetDriver(dt, rig, world)
	s.solveAllLegIK(dt, rig, world)

	for i := range s.Feet {
		s.syncFoot(i)
	}

	s.adjustBodyFromLegs(dt)
	s.applyPlantedFootForces(dt, rig)
	s.stepPhysics(dt, rig, world)
	s.Body.Yaw = s.Heading
}

func (s *SpiderLocomotor) emergencyReplant(rig *Rig, world FootWorld) {
	if s.Speed < 0.05 || rig == nil {
		return
	}
	swinging := 0
	for i := range s.steppers {
		if s.steppers[i].isStepping {
			swinging++
		}
	}
	pace := s.stepPace(rig, s.gaitStride(rig))
	if swinging >= pace.maxSwing {
		return
	}
	if s.maxHipPlantHoriz(rig) < spiderMaxHipPlantHoriz*0.76 {
		return
	}
	bodyXf := s.rootTransform()
	worst, worstD := -1, 0.0
	for i, leg := range rig.LegDefs() {
		if i >= len(s.Feet) || i >= len(s.steppers) {
			break
		}
		if s.Feet[i].Phase == FootSwing || s.steppers[i].isStepping {
			continue
		}
		hip := bodyXf.ToWorld(rig.JointLocal(leg.Proximal))
		d := horizDist(vec.V{X: hip.X, Z: hip.Z}, vec.V{X: s.Feet[i].PlantWorld.X, Z: s.Feet[i].PlantWorld.Z})
		if d > worstD {
			worstD = d
			worst = i
		}
	}
	if worstD < spiderMaxHipPlantHoriz*0.78 {
		return
	}
	if worst < 0 {
		return
	}
	st := &s.steppers[worst]
	if st.isStepping {
		return
	}
	st.beginStep(s.stepMgr.legStepTime(&s.chains[worst], st, s, rig), s, rig, world, worst)
}

func (s *SpiderLocomotor) syncFoot(i int) {
	if i >= len(s.Feet) {
		return
	}
	f := &s.Feet[i]
	if f.Phase != FootSwing {
		f.PlantWorld = f.World
	}
	f.Initialized = true
}

func (s *SpiderLocomotor) rootTransform() *scene.Transform {
	return s.Body.Transform()
}

func (s *SpiderLocomotor) surfaceForward() vec.V {
	fwd := yawForward(s.Heading)
	return projectOnPlane(fwd, s.Up).Normalize()
}

func (s *SpiderLocomotor) surfaceRight() vec.V {
	fwd := s.surfaceForward()
	if fwd.LenSq() < 1e-12 {
		return yawRight(s.Heading)
	}
	right := s.Up.Cross(fwd)
	if right.LenSq() < 1e-12 {
		return yawRight(s.Heading)
	}
	return right.Normalize()
}

func (s *SpiderLocomotor) groundCheck(world FootWorld) {
	s.grounded = false
	s.groundDist = math.Inf(1)
	headY := s.Body.Pos.Y + s.RestHeight + 0.5
	radius := spiderDownRaySize * spiderColliderRadius
	fwd := s.surfaceForward()
	if fwd.LenSq() < 1e-12 {
		fwd = yawForward(s.Heading)
	}

	down := s.Up
	if down.LenSq() < 1e-12 {
		down = vec.V{Y: 1}
	}
	down = down.Scale(-1)
	dHit := CastFromFootWorld(world, s.Body.Pos, down, spiderDownRayLength*spiderColliderLength, headY)

	fHit := CastFromFootWorld(world, s.Body.Pos, fwd, spiderForwardRayLength*spiderColliderLength, headY)
	useForward := false
	if fHit.Hit {
		n := fHit.Normal.Normalize()
		wallLike := n.Y < 0.65
		close := fHit.Dist < spiderColliderLength*0.6
		onWall := s.groundRay == groundForward && s.Up.Y < 0.85
		useForward = wallLike && (close || onWall)
	}
	// Upright on a floor: keep the down cast; ignore distant vertical faces ahead.
	if s.Up.Y > 0.92 && dHit.Hit {
		useForward = false
	}

	if useForward {
		s.grounded = true
		s.groundDist = fHit.Dist
		s.groundNormal = fHit.Normal.Normalize()
		s.Up = s.groundNormal
		s.groundRay = groundForward
		s.GroundY = fHit.Point.Y
		return
	}

	if dHit.Hit {
		s.grounded = true
		s.groundDist = dHit.Dist - radius
		s.groundNormal = dHit.Normal.Normalize()
		s.Up = s.groundNormal
		s.groundRay = groundDown
		s.GroundY = dHit.Point.Y
	}
	_ = radius
}

func (s *SpiderLocomotor) alignToSurface(dt float64) {
	if !s.grounded {
		return
	}
	targetUp := s.groundNormal
	if targetUp.LenSq() < 1e-12 {
		targetUp = vec.V{Y: 1}
	}
	// On flat floors keep the body upright; wall-walk tilt applies on steep surfaces.
	if targetUp.Y > spiderUprightMinUpY {
		s.Up = vec.V{Y: 1}
		s.Body.Pitch = 0
		s.Body.Roll = 0
		s.Body.AngVel.X = 0
		s.Body.AngVel.Z = 0
		return
	}
	speed := spiderGroundAdjustSpeed
	if s.groundRay == groundForward {
		speed = spiderForwardAdjustSpeed
	}
	t := 0.02 * speed * dt * 60
	if t > 1 {
		t = 1
	}
	s.Up = slerpVec(s.Up, targetUp, t)
	right := s.surfaceRight()
	if right.LenSq() < 1e-12 {
		return
	}
	goalPitch, goalRoll := tiltFromNormal(s.Up, s.Heading)
	s.Body.Pitch += (goalPitch - s.Body.Pitch) * t
	s.Body.Roll += (goalRoll - s.Body.Roll) * t
}

func tiltFromNormal(up vec.V, heading float64) (pitch, roll float64) {
	if up.LenSq() < 1e-12 {
		return 0, 0
	}
	up = up.Normalize()
	fwd := projectOnPlane(yawForward(heading), up)
	if fwd.LenSq() < 1e-12 {
		return 0, 0
	}
	fwd = fwd.Normalize()
	right := up.Cross(fwd).Normalize()
	// Pitch around right, roll around forward.
	pitch = math.Asin(clampScalar(-up.Dot(fwd), -1, 1)) * 180 / math.Pi
	roll = math.Asin(clampScalar(up.Dot(right), -1, 1)) * 180 / math.Pi
	return pitch, roll
}

func (s *SpiderLocomotor) moveLagScale() float64 {
	if s.rig == nil {
		return 1
	}
	lag := s.maxHipPlantHoriz(s.rig)
	lagSlow := spiderMaxHipPlantHoriz * 0.50
	lagStop := spiderMaxHipPlantHoriz * 0.90
	hardStop := spiderMaxHipPlantHoriz * 1.08
	if lag > hardStop {
		return 0.4
	}
	if lag > lagStop {
		return 0.55
	}
	if lag > lagSlow {
		t := (lag - lagSlow) / (lagStop - lagSlow)
		return 1.0 - t*0.45
	}
	return 1.0
}

func (s *SpiderLocomotor) applyStickForce(dt float64, world FootWorld) {
	if !s.grounded {
		return
	}
	if s.groundDist > spiderGravityOffDist*spiderColliderRadius {
		g := spiderGravityMultiplier * 0.0981 * spiderScale
		force := s.Up.Scale(-g * s.Body.Mass)
		s.Body.ApplyForce(force, dt)
	}
	_ = world
}

func (s *SpiderLocomotor) adjustBodyFromLegs(dt float64) {
	if s.Speed < 0.05 {
		return
	}
	centroid := s.legsCentroid()
	t := spiderLegCentroidSpeed * dt
	if t > 1 {
		t = 1
	}
	s.bodyCentroid = s.bodyCentroid.Scale(1-t).Add(centroid.Scale(t))

	legNormal := s.legsPlaneNormal()
	up := s.Up
	if up.LenSq() < 1e-12 {
		up = vec.V{Y: 1}
	}
	weight := spiderLegNormalWeight
	if up.Y > spiderUprightMinUpY {
		weight *= spiderLegPoseWeightFlat
	}
	right := s.surfaceRight()
	angleX := signedAngleDeg(projectOnPlane(up, right), projectOnPlane(legNormal, right), right) * spiderLegNormalSpeed * dt
	angleZ := signedAngleDeg(up, projectOnPlane(legNormal, s.surfaceForward()), s.surfaceForward()) * spiderLegNormalSpeed * dt
	s.Body.Pitch += angleX * weight
	if up.Y <= spiderUprightMinUpY {
		s.Body.Roll += angleZ * weight
	}
}

func (s *SpiderLocomotor) defaultCentroid() vec.V {
	return s.Body.Pos.Add(s.Up.Scale(spiderBodyOffsetHeight * spiderScale))
}

func (s *SpiderLocomotor) legsCentroid() vec.V {
	def := s.defaultCentroid()
	if len(s.Feet) == 0 {
		return def
	}
	sum := vec.V{}
	n := 0
	for i := range s.Feet {
		if !s.Feet[i].Initialized {
			continue
		}
		pos := s.footContactPoint(i)
		sum = sum.Add(pos)
		n++
	}
	if n == 0 {
		return def
	}
	c := sum.Scale(1 / float64(n))
	offset := projectOnPlane(def.Sub(s.colliderBottom()), s.Up)
	c = c.Add(offset)
	normalPart := projectOnPlane(c.Sub(def), s.Up).Scale(spiderLegCentroidNormW)
	tangentPart := c.Sub(def).Sub(projectOnPlane(c.Sub(def), s.Up)).Scale(spiderLegCentroidTanW)
	return def.Add(normalPart).Add(tangentPart)
}

func (s *SpiderLocomotor) footContactPoint(i int) vec.V {
	if i < 0 || i >= len(s.Feet) {
		return vec.V{}
	}
	f := &s.Feet[i]
	if f.Phase == FootSwing {
		return f.World
	}
	if f.Solve.Valid {
		return f.Solve.Foot
	}
	return f.PlantWorld
}

func (s *SpiderLocomotor) legsPlaneNormal() vec.V {
	var pts []vec.V
	for i := range s.Feet {
		if !s.Feet[i].Initialized {
			continue
		}
		pts = append(pts, s.footContactPoint(i))
	}
	if len(pts) < 3 {
		n := s.Up
		if n.LenSq() < 1e-12 {
			return vec.V{Y: 1}
		}
		return n.Normalize()
	}
	n, _ := physics.FitPlane(pts)
	if n.LenSq() < 1e-12 {
		return vec.V{Y: 1}
	}
	if s.Up.Y > spiderUprightMinUpY {
		n = spiderSagittalNormal(n, s.Heading)
	}
	return n.Normalize()
}

// spiderSagittalNormal removes lateral roll from a support-plane normal so stairs
// and mixed tread heights pitch the body forward/back only.
func spiderSagittalNormal(n vec.V, heading float64) vec.V {
	if n.LenSq() < 1e-12 {
		return vec.V{Y: 1}
	}
	n = n.Normalize()
	fwd := projectOnPlane(yawForward(heading), vec.V{Y: 1})
	if fwd.LenSq() < 1e-12 {
		return n
	}
	fwd = fwd.Normalize()
	right := vec.V{Y: 1}.Cross(fwd)
	if right.LenSq() < 1e-12 {
		return n
	}
	right = right.Normalize()
	flat := n.Sub(right.Scale(n.Dot(right)))
	if flat.LenSq() < 1e-12 {
		return vec.V{Y: 1}
	}
	return flat.Normalize()
}

func (s *SpiderLocomotor) colliderBottom() vec.V {
	return s.Body.Pos.Add(s.Up.Scale(-spiderColliderRadius))
}

// ComputePose builds the skeleton pose from CCD IK chains.
func (s *SpiderLocomotor) ComputePose(rig *Rig) SkeletonPose {
	if s == nil || rig == nil {
		return SkeletonPose{}
	}
	bodyXf := s.rootTransform()
	pose := SkeletonPose{Bones: make(map[string]*scene.Transform, len(rig.BoneOrder))}

	for _, name := range rig.BoneOrder {
		b := rig.Bones[name]
		angles := rig.PoseAngles("idle", name)
		if b.Parent == "" {
			pose.Bones[name] = bodyXf
			continue
		}
		parent := pose.Bones[b.Parent]
		if parent == nil {
			continue
		}
		jointLocal := rig.JointLocal(name)
		pose.Bones[name] = parent.ChildAt(jointLocal, angles.Pitch, angles.Yaw, angles.Roll)
	}

	legs := rig.LegDefs()
	for i, leg := range legs {
		if i >= len(s.Feet) {
			break
		}
		ApplyTripodLegSolve(&pose, leg, s.Feet[i].Solve)
	}
	return pose
}

func (s *SpiderLocomotor) stepPhysics(dt float64, rig *Rig, world FootWorld) {
	planted := s.plantedPoints()
	planeN := s.Up
	terrainY := s.GroundY
	if world != nil {
		headY := s.Body.Pos.Y + s.RestHeight + 0.5
		terrainY = world.GroundHeight(s.Body.Pos.X, s.Body.Pos.Z, headY)
		s.GroundY = terrainY
	}
	planeC := vec.V{X: s.Body.Pos.X, Y: terrainY, Z: s.Body.Pos.Z}
	if len(planted) >= 3 {
		n, c := physics.FitPlane(planted)
		if n.LenSq() > 1e-12 {
			planeN = n
			if s.Up.Y > spiderUprightMinUpY {
				planeN = spiderSagittalNormal(planeN, s.Heading)
			}
			// Foot plane sets tilt only; hover height follows terrain under the body.
			_ = c
		}
	} else if len(planted) > 0 {
		sumY := 0.0
		for _, p := range planted {
			sumY += p.Y
		}
		avg := sumY / float64(len(planted))
		if avg > terrainY {
			terrainY = avg
			planeC.Y = terrainY
		}
	}
	if planeN.Y > 0.92 {
		planeN = vec.V{Y: 1}
	}
	hoverPoint := planeC
	if len(planted) >= 4 && s.Up.Y < 0.92 {
		sum := vec.V{}
		for _, p := range planted {
			sum = sum.Add(p)
		}
		avg := sum.Scale(1 / float64(len(planted)))
		hoverPoint = vec.V{X: s.Body.Pos.X, Y: math.Max(terrainY, avg.Y), Z: s.Body.Pos.Z}
	}
	force := physics.HoverForce(&s.Body, hoverPoint, planeN, s.RestHeight, spiderHoverK, spiderHoverDamp)
	force = projectOnPlane(force, s.Up)
	s.Body.ApplyForce(force, dt)
	torque := physics.OrientTorque(&s.Body, planeN, spiderOrientK*0.7, spiderOrientDamp)
	s.Body.ApplyTorque(torque, dt)
	flatish := s.Up.Y > 0.85
	pos := s.Body.Pos
	vel := s.Body.Vel
	s.Body.Integrate(dt, spiderLinDrag, spiderAngDrag)
	if flatish {
		s.Body.Pos.X = pos.X
		s.Body.Pos.Z = pos.Z
		s.Body.Vel.X = vel.X
		s.Body.Vel.Z = vel.Z
	} else {
		s.Body.Pos = pos
		s.Body.Vel = vel
	}
	physics.ClampTilt(&s.Body, spiderMaxTiltDeg)
	if s.Up.Y > spiderUprightMinUpY {
		s.Body.Roll = clampScalar(s.Body.Roll, -spiderUprightMaxRollDeg, spiderUprightMaxRollDeg)
	}
	s.GroundY = terrainY
}

func (s *SpiderLocomotor) plantedPoints() []vec.V {
	var pts []vec.V
	for _, f := range s.Feet {
		if f.Initialized && f.Phase != FootSwing {
			pts = append(pts, f.PlantWorld)
		}
	}
	return pts
}

func (s *SpiderLocomotor) maxHipPlantHoriz(rig *Rig) float64 {
	bodyXf := s.rootTransform()
	maxD := 0.0
	for i, leg := range rig.LegDefs() {
		if i >= len(s.Feet) {
			break
		}
		if !s.Feet[i].Initialized || s.Feet[i].Phase == FootSwing {
			continue
		}
		hip := bodyXf.ToWorld(rig.JointLocal(leg.Proximal))
		d := horizDist(vec.V{X: hip.X, Z: hip.Z}, vec.V{X: s.Feet[i].PlantWorld.X, Z: s.Feet[i].PlantWorld.Z})
		if d > maxD {
			maxD = d
		}
	}
	return maxD
}

func (s *SpiderLocomotor) stepPace(rig *Rig, stride float64) spiderStepPace {
	pace := s.paceScale(rig)
	trigger := stride * spiderStepTriggerFraction / pace
	if pace > 1 {
		trigger /= math.Sqrt(pace)
	}
	swing := spiderStepDuration / math.Sqrt(pace)
	if swing < 0.14 {
		swing = 0.14
	}
	maxSwing := spiderMaxConcurrentSwing + int(math.Floor(pace*1.5))
	if maxSwing < spiderMaxConcurrentSwing {
		maxSwing = spiderMaxConcurrentSwing
	}
	cap := s.stepMgr.maxSwing()
	if maxSwing > cap {
		maxSwing = cap
	}
	return spiderStepPace{trigger: trigger, swingSeconds: swing, maxSwing: maxSwing}
}

func (s *SpiderLocomotor) gaitStride(rig *Rig) float64 {
	if rig == nil {
		return 0.48
	}
	g := rig.GaitForSpeed(s.Speed)
	base := g.Stride
	if base <= 0 {
		base = g.StepStride(g.TravelSpeed(s.Speed))
	}
	ref := g.Speed
	if ref <= 0 {
		ref = g.TravelSpeed(0)
	}
	travel := g.TravelSpeed(s.Speed)
	if ref > 0 && travel > ref {
		base *= travel / ref
	}
	max := spiderMaxHipPlantHoriz * 0.94
	if base > max {
		base = max
	}
	if base < 0.18 {
		base = 0.18
	}
	return base
}

type spiderStepPace struct {
	trigger      float64
	swingSeconds float64
	maxSwing     int
}

const spiderMaxConcurrentSwing = 4

func horizDist(a, b vec.V) float64 {
	return math.Hypot(a.X-b.X, a.Z-b.Z)
}

func slerpVec(a, b vec.V, t float64) vec.V {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	dot := clampScalar(a.Dot(b), -1, 1)
	if dot > 0.9995 {
		return a.Scale(1-t).Add(b.Scale(t))
	}
	theta := math.Acos(dot)
	sinTheta := math.Sin(theta)
	w1 := math.Sin((1-t)*theta) / sinTheta
	w2 := math.Sin(t*theta) / sinTheta
	return a.Scale(w1).Add(b.Scale(w2))
}

// rotationFromTo returns a quaternion-like rotation matrix application via FromToRotation.
type dirRot struct{ from, to vec.V }

func rotationFromTo(from, to vec.V) dirRot { return dirRot{from: from, to: to} }

func (r dirRot) Rotate(v vec.V) vec.V {
	from := r.from
	to := r.to
	if from.LenSq() < 1e-12 || to.LenSq() < 1e-12 {
		return v
	}
	from = from.Normalize()
	to = to.Normalize()
	axis := from.Cross(to)
	if axis.LenSq() < 1e-12 {
		return v
	}
	angle := math.Acos(clampScalar(from.Dot(to), -1, 1)) * 180 / math.Pi
	return rotateAround(v, vec.V{}, axis.Normalize(), angle)
}

// IsMultiped reports rigs with more than two locomotion legs.
func (r *Rig) IsMultiped() bool {
	return r.isMultiped()
}
