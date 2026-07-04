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
	Patrol  []vec.V // closed-loop waypoints; Y is optional feet height hint (0 = auto)
	Goal    *vec.V  // single target; Y is optional feet height hint (0 = auto)
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
