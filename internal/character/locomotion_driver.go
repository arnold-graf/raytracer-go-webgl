package character

import "raytracer/internal/vec"

// LocomotionDriver advances procedural locomotion and produces skeleton poses.
type LocomotionDriver interface {
	Update(dt float64, rig *Rig, world FootWorld)
	HipPos() vec.V
	GroundY() float64
	Heading() float64
	Speed() float64
	SetHeading(float64)
	SetSpeed(float64)
	TranslateXZ(vec.V)
	ComputePose(rig *Rig, poseName string, world FootWorld) SkeletonPose
	// Locomotor returns kinematic state for nav, debug, and pose dumps.
	Locomotor() *Locomotor
	// AlwaysAnimate is true when the driver should tick even at zero speed (physics bodies).
	AlwaysAnimate() bool
}

// KinematicDriver wraps the biped kinematic Locomotor.
type KinematicDriver struct {
	Loc  Locomotor
	Pose string
}

func NewKinematicDriver(rig *Rig, spawn vec.V, heading, speed float64, world FootWorld, pose string) *KinematicDriver {
	return &KinematicDriver{
		Loc:  NewLocomotor(rig, spawn, heading, speed, world),
		Pose: pose,
	}
}

func (k *KinematicDriver) Update(dt float64, rig *Rig, world FootWorld) {
	if k == nil || k.Loc.Speed < 0.05 || world == nil {
		return
	}
	k.Loc.Update(dt, rig, world)
}

func (k *KinematicDriver) HipPos() vec.V {
	if k == nil {
		return vec.V{}
	}
	return k.Loc.HipPos
}

func (k *KinematicDriver) GroundY() float64 {
	if k == nil {
		return 0
	}
	return k.Loc.GroundY
}

func (k *KinematicDriver) Heading() float64 {
	if k == nil {
		return 0
	}
	return k.Loc.Heading
}

func (k *KinematicDriver) Speed() float64 {
	if k == nil {
		return 0
	}
	return k.Loc.Speed
}

func (k *KinematicDriver) SetHeading(h float64) {
	if k != nil {
		k.Loc.Heading = h
	}
}

func (k *KinematicDriver) SetSpeed(s float64) {
	if k != nil {
		k.Loc.Speed = s
	}
}

func (k *KinematicDriver) TranslateXZ(d vec.V) {
	if k != nil {
		k.Loc.HipPos.X += d.X
		k.Loc.HipPos.Z += d.Z
	}
}

func (k *KinematicDriver) ComputePose(rig *Rig, poseName string, world FootWorld) SkeletonPose {
	if k == nil {
		return SkeletonPose{}
	}
	if poseName == "" {
		poseName = k.Pose
	}
	return ComputeLocomotionPose(rig, &k.Loc, poseName, world)
}

func (k *KinematicDriver) Locomotor() *Locomotor {
	if k == nil {
		return nil
	}
	return &k.Loc
}

func (k *KinematicDriver) AlwaysAnimate() bool { return false }

// PhysicsDriver wraps multiped physics locomotion (spider and similar rigs).
type PhysicsDriver struct {
	Body *SpiderLocomotor
	Pose string
	nav  Locomotor
}

func NewPhysicsDriver(rig *Rig, spawn vec.V, heading, speed float64, world FootWorld, pose string) *PhysicsDriver {
	spider := NewSpiderLocomotor(rig, spawn, heading, speed, world)
	return &PhysicsDriver{
		Body: &spider,
		Pose: pose,
		nav: Locomotor{
			HipPos:  spider.Body.Pos,
			GroundY: spider.GroundY,
			Heading: heading,
			Speed:   speed,
		},
	}
}

func (p *PhysicsDriver) syncNav() {
	if p == nil || p.Body == nil {
		return
	}
	p.nav.HipPos = p.Body.Body.Pos
	p.nav.GroundY = p.Body.GroundY
	p.nav.Heading = p.Body.Heading
	p.nav.Speed = p.Body.Speed
}

func (p *PhysicsDriver) Update(dt float64, rig *Rig, world FootWorld) {
	if p == nil || p.Body == nil {
		return
	}
	p.Body.Speed = p.nav.Speed
	p.Body.Heading = p.nav.Heading
	p.Body.Update(dt, rig, world)
	p.syncNav()
}

func (p *PhysicsDriver) HipPos() vec.V {
	if p == nil || p.Body == nil {
		return vec.V{}
	}
	return p.Body.Body.Pos
}

func (p *PhysicsDriver) GroundY() float64 {
	if p == nil || p.Body == nil {
		return 0
	}
	return p.Body.GroundY
}

func (p *PhysicsDriver) Heading() float64 {
	if p == nil || p.Body == nil {
		return 0
	}
	return p.Body.Heading
}

func (p *PhysicsDriver) Speed() float64 {
	if p == nil || p.Body == nil {
		return 0
	}
	return p.Body.Speed
}

func (p *PhysicsDriver) SetHeading(h float64) {
	if p != nil {
		p.nav.Heading = h
		if p.Body != nil {
			p.Body.Heading = h
		}
	}
}

func (p *PhysicsDriver) SetSpeed(s float64) {
	if p != nil {
		p.nav.Speed = s
		if p.Body != nil {
			p.Body.Speed = s
		}
	}
}

func (p *PhysicsDriver) TranslateXZ(d vec.V) {
	if p == nil || p.Body == nil {
		return
	}
	p.Body.Body.Pos.X += d.X
	p.Body.Body.Pos.Z += d.Z
	p.syncNav()
}

func (p *PhysicsDriver) ComputePose(rig *Rig, poseName string, world FootWorld) SkeletonPose {
	if p == nil || p.Body == nil {
		return SkeletonPose{}
	}
	_ = poseName
	_ = world
	return p.Body.ComputePose(rig)
}

func (p *PhysicsDriver) Locomotor() *Locomotor {
	if p == nil {
		return nil
	}
	p.syncNav()
	return &p.nav
}

func (p *PhysicsDriver) AlwaysAnimate() bool { return true }

// NewLocomotionDriver picks the driver implementation for a rig.
func NewLocomotionDriver(rig *Rig, spawn vec.V, heading, speed float64, world FootWorld, pose string) LocomotionDriver {
	if rig != nil && rig.IsMultiped() {
		return NewPhysicsDriver(rig, spawn, heading, speed, world, pose)
	}
	return NewKinematicDriver(rig, spawn, heading, speed, world, pose)
}
