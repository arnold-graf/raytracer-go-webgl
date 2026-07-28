package npc

import (
	"math"

	"raytracer/internal/character"
	"raytracer/internal/vec"
)

const (
	sepPairMargin      = 0.25 // extra gap between clearance radii
	sepSteerRangeScale = 2.2  // steer away within this * pair separation
	sepSteerBlend      = 0.65
	sepSlowDistScale   = 1.15 // ease speed inside this * pair separation
)

func agentBodyRadius(a *Agent) float64 {
	if a == nil || a.Rig == nil {
		return 0.45
	}
	n := a.Rig.Navigation
	if n.ClearanceRadius > 0 {
		return n.ClearanceRadius
	}
	if n.Radius > 0 {
		return n.Radius
	}
	return 0.45
}

func pairSeparation(a, b *Agent) float64 {
	return agentBodyRadius(a) + agentBodyRadius(b) + sepPairMargin
}

// separationHeading blends an avoidance heading when peers are nearby.
func separationHeading(hip vec.V, baseHeading float64, self *Agent, peers []*Agent) float64 {
	var sx, sz float64
	for _, p := range peers {
		if p == nil || p == self || p.Driver == nil {
			continue
		}
		other := p.Driver.HipPos()
		dx, dz := hip.X-other.X, hip.Z-other.Z
		dist := math.Hypot(dx, dz)
		minDist := pairSeparation(self, p)
		influence := minDist * sepSteerRangeScale
		if dist < 1e-6 {
			sx += 1
			continue
		}
		if dist >= influence {
			continue
		}
		push := (influence - dist) / influence
		sx += (dx / dist) * push
		sz += (dz / dist) * push
	}
	if sx*sx+sz*sz < 1e-10 {
		return baseHeading
	}
	avoid := navHeadingFromDelta(sx, sz)
	blend := sepSteerBlend
	if mag := math.Hypot(sx, sz); mag*0.4 < blend {
		blend = mag * 0.4
	}
	return blendAngleDegrees(baseHeading, avoid, blend)
}

// separationSpeedScale returns a multiplier in (0,1] when peers are too close.
func separationSpeedScale(hip vec.V, self *Agent, peers []*Agent) float64 {
	scale := 1.0
	for _, p := range peers {
		if p == nil || p == self || p.Driver == nil {
			continue
		}
		other := p.Driver.HipPos()
		dist := horizDist(hip, other)
		minDist := pairSeparation(self, p)
		if dist >= minDist*sepSlowDistScale {
			continue
		}
		s := dist / (minDist * sepSlowDistScale)
		if s < 0.25 {
			s = 0.25
		}
		if s < scale {
			scale = s
		}
	}
	return scale
}

func (m *Manager) resolveSeparation(world character.FootWorld) {
	const passes = 3
	for range passes {
		for i := range m.agents {
			for j := i + 1; j < len(m.agents); j++ {
				resolveAgentPair(&m.agents[i], &m.agents[j], world)
			}
		}
	}
}

func resolveAgentPair(a, b *Agent, world character.FootWorld) {
	if a == nil || b == nil || a.Driver == nil || b.Driver == nil {
		return
	}
	pa := a.Driver.HipPos()
	pb := b.Driver.HipPos()
	dx, dz := pa.X-pb.X, pa.Z-pb.Z
	dist := math.Hypot(dx, dz)
	minDist := pairSeparation(a, b)
	if dist >= minDist {
		return
	}
	var nx, nz float64
	overlap := minDist - dist
	if dist < 1e-6 {
		nx, nz = 1, 0
	} else {
		nx, nz = dx/dist, dz/dist
	}
	push := overlap * 0.5
	a.Driver.TranslateXZ(vec.V{X: nx * push, Z: nz * push})
	b.Driver.TranslateXZ(vec.V{X: -nx * push, Z: -nz * push})
	groundAfterNudge(a, world)
	groundAfterNudge(b, world)
}

func groundAfterNudge(a *Agent, world character.FootWorld) {
	if a == nil || a.Driver == nil || a.Rig == nil || world == nil {
		return
	}
	hip := a.Driver.HipPos()
	headY := hip.Y + a.Rig.HipHeight + 0.5
	gy := world.GroundHeight(hip.X, hip.Z, headY)
	if pd, ok := a.Driver.(*character.PhysicsDriver); ok && pd.Body != nil {
		pd.Body.GroundY = gy
		pd.Body.Body.Pos.Y = gy + pd.Body.RestHeight
		return
	}
	if loc := a.Driver.Locomotor(); loc != nil {
		loc.GroundY = gy
		loc.HipPos = character.HipPositionFromGround(hip.X, gy, hip.Z, a.Rig.HipHeight)
	}
}
