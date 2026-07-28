package npc

import "raytracer/internal/character"

// LocomotorState returns kinematic nav/debug state for any driver type.
func (a *Agent) LocomotorState() *character.Locomotor {
	if a == nil || a.Driver == nil {
		return nil
	}
	return a.Driver.Locomotor()
}

// SpiderBody returns the physics locomotor when the agent uses one.
func (a *Agent) SpiderBody() *character.SpiderLocomotor {
	if a == nil {
		return nil
	}
	if pd, ok := a.Driver.(*character.PhysicsDriver); ok {
		return pd.Body
	}
	return nil
}
