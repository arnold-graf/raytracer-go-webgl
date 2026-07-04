package scene

import (
	"math"

	"raytracer/internal/vec"
)

// Point is a named location in scene space. Referenced by id from game systems
// (player spawn after a portal, future NPC goals, etc.).
//
// Pos is always the anchor position. Optional FloorY, Yaw, and Pitch are used
// when placing the player: when UseFloor is true, Pos.X/Z are horizontal and
// FloorY is the walkable surface (eye height is added). Otherwise Pos is the
// camera eye position.
type Point struct {
	ID       string
	Pos      vec.V
	Yaw      float64
	Pitch    float64
	FloorY   float64
	UseFloor bool
}

// PointByID returns the point with the given id, or false if none exists.
func (s *Scene) PointByID(id string) (Point, bool) {
	if s == nil {
		return Point{}, false
	}
	for _, p := range s.Points {
		if p.ID == id {
			return p, true
		}
	}
	return Point{}, false
}

// Placed returns p transformed into world space by an include instance xf.
func (p Point) Placed(xf *Transform) Point {
	if xf == nil {
		return p
	}
	out := p
	out.Pos = xf.ToWorld(p.Pos)
	if p.UseFloor {
		out.FloorY = xf.ToWorld(vec.New(p.Pos.X, p.FloorY, p.Pos.Z)).Y
	}
	localFwd := vec.V{X: math.Sin(p.Yaw), Y: 0, Z: -math.Cos(p.Yaw)}
	worldFwd := xf.RotateDir(localFwd)
	out.Yaw = math.Atan2(worldFwd.X, -worldFwd.Z)
	return out
}
