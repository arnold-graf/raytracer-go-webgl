package scene

import "math"

// GroundHeight returns the height of the highest walkable surface at (x,z) whose
// top is at or below headY (so ceilings and overhangs are ignored). It accounts
// for the terrain plus the flat tops of axis-aligned boxes (floors, platforms,
// foundations) the player is standing over. Implements camera.World.
func (s *Scene) GroundHeight(x, z, headY float64) float64 {
	g := math.Inf(-1)

	for i := range s.Terrains {
		t := &s.Terrains[i]
		if x >= t.OriginX && x <= t.OriginX+t.SizeX && z >= t.OriginZ && z <= t.OriginZ+t.SizeZ {
			if h := t.Height(x, z); h > g {
				g = h
			}
		}
	}

	for i := range s.Boxes {
		b := &s.Boxes[i]
		mn, mx := b.WorldBounds()
		if mx.Y <= headY && x >= mn.X && x <= mx.X && z >= mn.Z && z <= mx.Z {
			// A hole that breaches the top face (a stairwell/trap opening) means
			// there's no standing surface here, so the player falls through to
			// whatever lies below.
			if mx.Y > g && !b.TopOpenAt(x, z) {
				g = mx.Y
			}
		}
	}

	if math.IsInf(g, -1) {
		return 0
	}
	return g
}

// Blocked reports whether a vertical capsule of the given radius, standing on
// feetY with its head at headY, would intersect solid geometry at (x,z).
// Geometry whose top is no higher than feetY+step is treated as a walkable step
// rather than a wall, and geometry that floats above the player (overhangs,
// canopies, ceilings) does not block. Implements camera.World.
func (s *Scene) Blocked(x, z, feetY, headY, radius, step float64) bool {
	walkTop := feetY + step

	for i := range s.Boxes {
		b := &s.Boxes[i]
		mn, mx := b.WorldBounds()
		if mx.Y <= walkTop || mn.Y >= headY {
			continue
		}
		if x > mn.X-radius && x < mx.X+radius && z > mn.Z-radius && z < mx.Z+radius {
			// A doorway/window cut into the wall (Box.Holes) lets the player
			// pass when their body lines up with the opening.
			if b.PassableThroughHole(x, z, walkTop, headY, radius) {
				continue
			}
			return true
		}
	}

	for i := range s.Cylinders {
		c := &s.Cylinders[i]
		if c.YMax <= walkTop || c.YMin >= headY {
			continue
		}
		dx, dz := x-c.CX, z-c.CZ
		rr := radius + c.Radius
		if dx*dx+dz*dz < rr*rr {
			return true
		}
	}

	// Spheres only block when they rest near the floor (e.g. a ball on the
	// ground), so floating spheres like tree canopies can be walked under.
	for i := range s.Spheres {
		sp := &s.Spheres[i]
		if sp.Mat == MatEmit {
			continue
		}
		bottom := sp.Center.Y - sp.Radius
		top := sp.Center.Y + sp.Radius
		if top <= walkTop || bottom >= headY || bottom > walkTop {
			continue
		}
		dx, dz := x-sp.Center.X, z-sp.Center.Z
		rr := radius + sp.Radius
		if dx*dx+dz*dz < rr*rr {
			return true
		}
	}

	return false
}
