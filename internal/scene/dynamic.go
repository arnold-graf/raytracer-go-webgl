package scene

import "raytracer/internal/vec"

// NPCSpawn is a scene-authored placement for a procedural character. Geometry
// is instantiated at runtime by the NPC manager after load.
type NPCSpawn struct {
	Rig     string
	Pose    string
	Pos     vec.V
	Yaw     float64
	Speed   float64 // locomotion speed (m/s); 0 = idle
	Heading float64 // walk direction (degrees); 0 uses Yaw
}

// DynamicBody records index ranges into scene primitive slices owned by one
// runtime character. Indices are half-open [start, end).
type DynamicBody struct {
	Name      string
	Boxes     [2]int
	Cylinders [2]int
	Spheres   [2]int
}
