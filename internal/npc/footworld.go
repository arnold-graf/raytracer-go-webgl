package npc

import (
	"raytracer/internal/character"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// staticFootWorld wraps a scene for NPC foot placement, excluding dynamic body
// geometry so characters never ground-query their own limbs.
type staticFootWorld struct {
	sc *scene.Scene
}

func (w staticFootWorld) GroundHeight(x, z, headY float64) float64 {
	if w.sc == nil {
		return 0
	}
	return w.sc.GroundHeightStatic(x, z, headY)
}

func (w staticFootWorld) GroundNormal(x, z, headY float64) vec.V {
	if w.sc == nil {
		return vec.V{Y: 1}
	}
	return w.sc.GroundNormalStatic(x, z, headY)
}

var _ character.FootWorld = staticFootWorld{}

func footWorld(sc *scene.Scene) staticFootWorld {
	return staticFootWorld{sc: sc}
}

// FootWorld returns a FootWorld that ignores dynamic NPC geometry in the scene.
func FootWorld(sc *scene.Scene) character.FootWorld {
	if sc == nil {
		return staticFootWorld{}
	}
	return character.SceneSpiderWorld{Sc: sc}
}
