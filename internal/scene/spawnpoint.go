package scene

import (
	"math"

	"raytracer/internal/vec"
)

// PlayerSpawnpoint marks where the player can be placed after a scene transition
// or other scripted teleport. Referenced by id from game code (e.g. exit portal).
//
// When UseFloor is true, Pos.X and Pos.Z are the horizontal location and FloorY
// is the walkable surface height (PlaceOnFloor adds eye height). Otherwise Pos
// is the camera eye position, like [camera].pos.
type PlayerSpawnpoint struct {
	ID       string
	Pos      vec.V
	Yaw      float64
	Pitch    float64
	FloorY   float64
	UseFloor bool
}

// Spawnpoint returns the spawn with the given id, or false if none exists.
func (s *Scene) Spawnpoint(id string) (PlayerSpawnpoint, bool) {
	if s == nil {
		return PlayerSpawnpoint{}, false
	}
	for _, sp := range s.Spawnpoints {
		if sp.ID == id {
			return sp, true
		}
	}
	return PlayerSpawnpoint{}, false
}

// Placed returns sp transformed into world space by an include instance xf.
func (sp PlayerSpawnpoint) Placed(xf *Transform) PlayerSpawnpoint {
	if xf == nil {
		return sp
	}
	out := sp
	out.Pos = xf.ToWorld(sp.Pos)
	localFwd := vec.V{X: math.Sin(sp.Yaw), Y: 0, Z: -math.Cos(sp.Yaw)}
	worldFwd := xf.RotateDir(localFwd)
	out.Yaw = math.Atan2(worldFwd.X, -worldFwd.Z)
	return out
}
