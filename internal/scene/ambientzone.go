package scene

import "raytracer/internal/vec"

// AmbientZone is an axis-aligned region with its own ambient cube: six RGB
// values, one per face direction, replacing the scene-wide
// [environment].ambient_sky / ambient_ground pair inside the region.
//
// The point is to remove the single worst approximation in the shipping
// renderer's lighting. `scene_ambient` blends two authored constants by the
// surface normal's Y, so every up-facing surface in a building — a desk under
// a lit atrium and a shelf in a windowless server room alike — receives
// exactly the same indirect light. A zone per room, baked from the path
// tracer, costs six colours and fixes that without any of the leaking a
// regular probe grid would bring, because the region boundaries are the room
// boundaries rather than an arbitrary lattice.
//
// Faces are ordered +X, -X, +Y, -Y, +Z, -Z, matching the ambient cube the AO
// volume already uses (see ao_face in shade.wesl). A normal weights the three
// faces it points toward by the square of its components, which sums to one.
type AmbientZone struct {
	Min, Max vec.V
	// Faces holds the six RGB values in +X, -X, +Y, -Y, +Z, -Z order.
	Faces [6]vec.V
}

// Contains reports whether p is inside the zone.
func (z AmbientZone) Contains(p vec.V) bool {
	return p.X >= z.Min.X && p.X <= z.Max.X &&
		p.Y >= z.Min.Y && p.Y <= z.Max.Y &&
		p.Z >= z.Min.Z && p.Z <= z.Max.Z
}

// Valid reports whether the zone has a non-degenerate extent.
func (z AmbientZone) Valid() bool {
	return z.Max.X > z.Min.X && z.Max.Y > z.Min.Y && z.Max.Z > z.Min.Z
}

// AmbientZoneFaceNames are the TOML keys for the six faces, in packing order.
var AmbientZoneFaceNames = [6]string{"px", "nx", "py", "ny", "pz", "nz"}
