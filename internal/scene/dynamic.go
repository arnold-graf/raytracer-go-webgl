package scene

import (
	"math"

	"raytracer/internal/vec"
)

// NPCSpawn is a scene-authored placement for a procedural character. Geometry
// is instantiated at runtime by the NPC manager after load.
type NPCSpawn struct {
	Rig     string
	Pose    string
	Pos     vec.V
	Yaw     float64
	Speed   float64 // locomotion speed (m/s); 0 = idle
	Heading float64 // walk direction (degrees); 0 uses Yaw
	Patrol       []vec.V // closed-loop waypoints; Y is optional feet height hint (0 = auto)
	Goal         *vec.V  // single target; Y is optional feet height hint (0 = auto)
	TargetRadius float64 // horizontal arrival radius for patrol/goal (0 = default 0.75 m, scaled up by rig size/speed)
}

// Placed returns sp transformed into world space by an include instance xf.
func (sp NPCSpawn) Placed(xf *Transform) NPCSpawn {
	if xf == nil {
		return sp
	}
	out := sp
	out.Pos = xf.ToWorld(sp.Pos)
	if len(sp.Patrol) > 0 {
		out.Patrol = make([]vec.V, len(sp.Patrol))
		for i, p := range sp.Patrol {
			out.Patrol[i] = xf.ToWorld(p)
		}
	}
	if sp.Goal != nil {
		g := xf.ToWorld(*sp.Goal)
		out.Goal = &g
	}
	out.Yaw = rotateYawDegrees(sp.Yaw, xf)
	out.Heading = rotateYawDegrees(sp.Heading, xf)
	return out
}

func rotateYawDegrees(deg float64, xf *Transform) float64 {
	localFwd := vec.V{X: math.Sin(deg * math.Pi / 180), Y: 0, Z: -math.Cos(deg * math.Pi / 180)}
	worldFwd := xf.RotateDir(localFwd)
	return math.Atan2(worldFwd.X, -worldFwd.Z) * 180 / math.Pi
}

// DynamicBody records index ranges into scene primitive slices owned by one
// runtime character. Indices are half-open [start, end).
type DynamicBody struct {
	Name      string
	Boxes     [2]int
	Cylinders [2]int
	Spheres   [2]int
	Lenses    [2]int
	Lights    [2]int
}

// IsDynamicCylinder reports whether cylinder index i belongs to a DynamicBody.
func (s *Scene) IsDynamicCylinder(i int) bool {
	if s == nil {
		return false
	}
	for _, db := range s.DynamicBodies {
		if i >= db.Cylinders[0] && i < db.Cylinders[1] {
			return true
		}
	}
	return false
}

// Attached reports whether this body's index ranges still fit the scene slices.
func (db DynamicBody) Attached(s *Scene) bool {
	if s == nil {
		return false
	}
	return inRange(db.Boxes, len(s.Boxes)) &&
		inRange(db.Cylinders, len(s.Cylinders)) &&
		inRange(db.Spheres, len(s.Spheres)) &&
		inRange(db.Lenses, len(s.Lenses)) &&
		inRange(db.Lights, len(s.Lights))
}

func inRange(span [2]int, n int) bool {
	if span[1] < span[0] {
		return false
	}
	if span[0] == span[1] {
		return true
	}
	return span[0] >= 0 && span[1] <= n
}

// RemoveDynamicBody splices out one runtime body's primitives and shifts later
// bodies' index ranges. Bodies are expected to own disjoint append-only spans.
func (s *Scene) RemoveDynamicBody(db DynamicBody) {
	if s == nil {
		return
	}
	s.Boxes = spliceRange(s.Boxes, db.Boxes[0], db.Boxes[1])
	s.Cylinders = spliceRange(s.Cylinders, db.Cylinders[0], db.Cylinders[1])
	s.Spheres = spliceRange(s.Spheres, db.Spheres[0], db.Spheres[1])
	s.Lenses = spliceRange(s.Lenses, db.Lenses[0], db.Lenses[1])
	s.Lights = spliceRange(s.Lights, db.Lights[0], db.Lights[1])

	out := s.DynamicBodies[:0]
	for _, b := range s.DynamicBodies {
		if b.Name == db.Name {
			continue
		}
		out = append(out, shiftDynamicBody(b, db))
	}
	s.DynamicBodies = out
}

func spliceRange[T any](slice []T, start, end int) []T {
	if start >= end || start < 0 || end > len(slice) {
		return slice
	}
	return append(slice[:start], slice[end:]...)
}

func shiftDynamicBody(b, removed DynamicBody) DynamicBody {
	b.Boxes = shiftSpan(b.Boxes, removed.Boxes)
	b.Cylinders = shiftSpan(b.Cylinders, removed.Cylinders)
	b.Spheres = shiftSpan(b.Spheres, removed.Spheres)
	b.Lenses = shiftSpan(b.Lenses, removed.Lenses)
	b.Lights = shiftSpan(b.Lights, removed.Lights)
	return b
}

func shiftSpan(span, removed [2]int) [2]int {
	if span[1] <= span[0] {
		return span
	}
	if removed[1] <= removed[0] {
		return span
	}
	if span[0] >= removed[1] {
		n := removed[1] - removed[0]
		return [2]int{span[0] - n, span[1] - n}
	}
	return span
}

// TouchTransforms marks primitive transforms as changed without invalidating
// static geometry. NPC pose updates use this so the GPU backend can partially
// re-upload and refit the BVH instead of a full scene rebuild.
func (s *Scene) TouchTransforms() {
	if s == nil {
		return
	}
	s.xformGen++
}

// TransformGeneration bumps on TouchTransforms; renderers use it for partial
// transform uploads without rebuilding static geometry.
func (s *Scene) TransformGeneration() uint64 { return s.xformGen }

// SetDoorGhost registers the callback used by Blocked to skip door panels that
// are ghosting through the player. Pass nil to clear.
func (s *Scene) SetDoorGhost(fn DoorGhostBox) {
	s.doorGhost = fn
}
