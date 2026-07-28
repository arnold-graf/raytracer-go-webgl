package character

import (
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// footTarget is the step/IK goal for one leg (position + surface normal).
type footTarget struct {
	Position vec.V
	Normal   vec.V
	Grounded bool
}

// legHinge is one rotational DOF metadata slot in a leg chain (stepper state).
type legHinge struct {
	Point    vec.V
	Axis     vec.V
	MinAngle float64
	MaxAngle float64
	Angle    float64
	Weight   float64
	Length   float64
}

// legChain tracks per-leg joint positions and step targets for multiped stepping.
type legChain struct {
	Hinges      [3]legHinge
	Points      [4]vec.V
	Target      footTarget
	Error       float64
	Tolerance   float64
	MinChange   float64
	Singularity float64
	HasFoot     bool
	Paused      bool
	LastTip     vec.V
	TipVelocity vec.V
}

func (c *legChain) chainLength() float64 {
	return c.Hinges[0].Length + c.Hinges[1].Length + c.Hinges[2].Length
}

func (c *legChain) tip() vec.V { return c.Points[3] }

func (c *legChain) setTarget(t footTarget) { c.Target = t }

func (c *legChain) advance(dt float64) {
	if dt <= 0 {
		return
	}
	tip := c.tip()
	c.TipVelocity = tip.Sub(c.LastTip).Scale(1 / dt)
	c.LastTip = tip
}

// initLegChain builds a leg chain from rig idle FK.
func initLegChain(rig *Rig, leg LegDef, bodyXf *scene.Transform) legChain {
	coxaBone := rig.Bones[leg.Proximal]
	femurBone := rig.Bones[leg.Mid]
	tibiaBone := rig.Bones[leg.Distal]

	idle := rig.ComputeFK("idle", bodyXf.Translation(), bodyYawDeg(bodyXf))
	hip := bodyXf.ToWorld(rig.JointLocal(leg.Proximal))
	coxaTip := rig.BoneTip(idle, leg.Proximal)
	knee := rig.BoneTip(idle, leg.Mid)
	foot := rig.BoneTip(idle, leg.Distal)

	coxaAxis := idle.Bones[leg.Proximal].WorldNormal(vec.V{Y: 1})
	femurAxis := idle.Bones[leg.Mid].WorldNormal(vec.V{X: 1})
	tibiaAxis := idle.Bones[leg.Distal].WorldNormal(vec.V{X: 1})

	chain := legChain{
		Points:      [4]vec.V{hip, coxaTip, knee, foot},
		Tolerance:   0.12,
		MinChange:   0.001,
		Singularity: 0.02,
		HasFoot:     true,
	}
	chain.Hinges[0] = legHinge{
		Point: hip, Axis: coxaAxis, MinAngle: -75, MaxAngle: 75,
		Weight: 1, Length: coxaBone.Length,
	}
	chain.Hinges[1] = legHinge{
		Point: coxaTip, Axis: femurAxis, MinAngle: -120, MaxAngle: 30,
		Weight: 1, Length: femurBone.Length,
	}
	chain.Hinges[2] = legHinge{
		Point: knee, Axis: tibiaAxis, MinAngle: -130, MaxAngle: 10,
		Weight: 1, Length: tibiaBone.Length,
	}
	chain.Target = footTarget{Position: foot, Normal: vec.V{Y: 1}, Grounded: true}
	chain.Error = foot.Sub(chain.Target.Position).Len()
	chain.LastTip = foot
	return chain
}

func refreshChainRoot(c *legChain, hip vec.V, heading float64, rig *Rig, leg LegDef) {
	delta := hip.Sub(c.Points[0])
	if delta.LenSq() < 1e-18 {
		return
	}
	for i := range c.Points {
		c.Points[i] = c.Points[i].Add(delta)
	}
	c.Hinges[0].Point = hip
	c.Hinges[1].Point = c.Points[1]
	c.Hinges[2].Point = c.Points[2]

	bodyXf := scene.NewRigidTransform(0, heading, 0, hip)
	idle := rig.ComputeFK("idle", hip, heading)
	coxaAxis := idle.Bones[leg.Proximal].WorldNormal(vec.V{Y: 1})
	if coxaAxis.LenSq() > 1e-12 {
		c.Hinges[0].Axis = coxaAxis.Normalize()
	}
	_ = bodyXf
}

func refreshChainHipOnly(c *legChain, hip vec.V, heading float64, rig *Rig, leg LegDef) {
	c.Points[0] = hip
	c.Hinges[0].Point = hip
	bodyXf := scene.NewRigidTransform(0, heading, 0, hip)
	idle := rig.ComputeFK("idle", hip, heading)
	coxaAxis := idle.Bones[leg.Proximal].WorldNormal(vec.V{Y: 1})
	if coxaAxis.LenSq() > 1e-12 {
		c.Hinges[0].Axis = coxaAxis.Normalize()
	}
	_ = bodyXf
}

func easeStepArc(t float64) float64 {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return 4 * t * (1 - t)
}

func tipFromChain(c legChain) vec.V { return c.tip() }
