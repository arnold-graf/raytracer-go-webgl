package scene

import (
	"math"

	"raytracer/internal/vec"
)

// GroundHeight returns the height of the highest walkable surface at (x,z) whose
// top is at or below headY (so ceilings and overhangs are ignored). It accounts
// for the terrain plus the flat tops of axis-aligned boxes (floors, platforms,
// foundations) the player is standing over. Implements camera.World.
func (s *Scene) GroundHeight(x, z, headY float64) float64 {
	return s.groundHeight(x, z, headY, nil)
}

// GroundHeightStatic is like GroundHeight but ignores runtime NPC/dynamic body
// boxes so characters do not stand on their own limb geometry.
func (s *Scene) GroundHeightStatic(x, z, headY float64) float64 {
	return s.groundHeight(x, z, headY, s.isDynamicBox)
}

func (s *Scene) isDynamicBox(i int) bool {
	for _, db := range s.DynamicBodies {
		if i >= db.Boxes[0] && i < db.Boxes[1] {
			return true
		}
	}
	return false
}

func (s *Scene) isDynamicCylinder(i int) bool {
	for _, db := range s.DynamicBodies {
		if i >= db.Cylinders[0] && i < db.Cylinders[1] {
			return true
		}
	}
	return false
}

func (s *Scene) isDynamicSphere(i int) bool {
	for _, db := range s.DynamicBodies {
		if i >= db.Spheres[0] && i < db.Spheres[1] {
			return true
		}
	}
	return false
}

func (s *Scene) isDoorGhostBox(i int) bool {
	if s == nil || s.doorGhost == nil {
		return false
	}
	return s.doorGhost(i)
}

func (s *Scene) groundHeight(x, z, headY float64, skipBox func(int) bool) float64 {
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
		if skipBox != nil && skipBox(i) {
			continue
		}
		b := &s.Boxes[i]
		_, mx := b.WorldBounds()
		if mx.Y > headY {
			continue
		}
		// For rotated floors the world AABB is loose; require the point to lie
		// over the top face in the box's local frame.
		p := vec.V{X: x, Y: b.Max.Y, Z: z}
		if b.Xform != nil {
			p = b.Xform.ToLocal(p)
		}
		if p.X < b.Min.X || p.X > b.Max.X || p.Z < b.Min.Z || p.Z > b.Max.Z {
			continue
		}
		// A hole that breaches the top face (a stairwell/trap opening) means
		// there's no standing surface here, so the player falls through to
		// whatever lies below.
		if mx.Y > g && !b.TopOpenAt(x, z) {
			g = mx.Y
		}
	}

	if math.IsInf(g, -1) {
		return 0
	}
	return g
}

// GroundNormal returns an upward-pointing surface normal at (x,z) estimated by
// finite differences on GroundHeight. headY caps ceiling/overhang selection.
func (s *Scene) GroundNormal(x, z, headY float64) vec.V {
	return s.groundNormal(x, z, headY, nil)
}

// GroundNormalStatic is like GroundNormal but ignores dynamic body boxes.
func (s *Scene) GroundNormalStatic(x, z, headY float64) vec.V {
	return s.groundNormal(x, z, headY, s.isDynamicBox)
}

func (s *Scene) groundNormal(x, z, headY float64, skipBox func(int) bool) vec.V {
	const eps = 0.08
	hL := s.groundHeight(x-eps, z, headY, skipBox)
	hR := s.groundHeight(x+eps, z, headY, skipBox)
	hN := s.groundHeight(x, z-eps, headY, skipBox)
	hS := s.groundHeight(x, z+eps, headY, skipBox)
	n := vec.V{hL - hR, 2 * eps, hN - hS}
	if n.LenSq() < 1e-12 {
		return vec.V{Y: 1}
	}
	return n.Normalize()
}

// Blocked reports whether a vertical capsule of the given radius, standing on
// feetY with its head at headY, would intersect solid geometry at (x,z).
// Geometry whose top is no higher than feetY+step is treated as a walkable step
// rather than a wall, and geometry that floats above the player (overhangs,
// canopies, ceilings) does not block. Implements camera.World.
func (s *Scene) Blocked(x, z, feetY, headY, radius, step float64) bool {
	walkTop := feetY + step

	for i := range s.Boxes {
		if s.isDoorGhostBox(i) {
			continue
		}
		b := &s.Boxes[i]
		mn, mx := b.WorldBounds()
		if mx.Y <= walkTop || mn.Y >= headY {
			continue
		}
		if x > mn.X-radius && x < mx.X+radius && z > mn.Z-radius && z < mx.Z+radius {
			if !b.blocksColumn(x, z, walkTop, headY, radius) {
				continue
			}
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
		mn, mx := c.WorldBounds()
		if mx.Y <= walkTop || mn.Y >= headY {
			continue
		}
		if x > mn.X-radius && x < mx.X+radius && z > mn.Z-radius && z < mx.Z+radius {
			if c.blocksColumn(x, z, walkTop, headY, radius) {
				return true
			}
		}
	}

	for i := range s.Cones {
		co := &s.Cones[i]
		mn, mx := co.WorldBounds()
		if mx.Y <= walkTop || mn.Y >= headY {
			continue
		}
		if x > mn.X-radius && x < mx.X+radius && z > mn.Z-radius && z < mx.Z+radius {
			if co.blocksColumn(x, z, walkTop, headY, radius) {
				return true
			}
		}
	}

	// Spheres only block when they rest near the floor (e.g. a ball on the
	// ground), so floating spheres like tree canopies can be walked under.
	for i := range s.Spheres {
		sp := &s.Spheres[i]
		if sp.Mat == MatEmit {
			continue
		}
		center := sp.Center
		if sp.Xform != nil {
			center = sp.Xform.ToWorld(center)
		}
		bottom := center.Y - sp.Radius
		top := center.Y + sp.Radius
		if top <= walkTop || bottom >= headY || bottom > walkTop {
			continue
		}
		dx, dz := x-center.X, z-center.Z
		rr := radius + sp.Radius
		if dx*dx+dz*dz < rr*rr {
			return true
		}
	}

	return false
}

// BlockedStatic is like Blocked but ignores runtime NPC/dynamic body geometry so
// pathfinding does not treat other characters as walls.
func (s *Scene) BlockedStatic(x, z, feetY, headY, radius, step float64) bool {
	walkTop := feetY + step

	for i := range s.Boxes {
		if s.isDynamicBox(i) {
			continue
		}
		b := &s.Boxes[i]
		mn, mx := b.WorldBounds()
		if mx.Y <= walkTop || mn.Y >= headY {
			continue
		}
		if x > mn.X-radius && x < mx.X+radius && z > mn.Z-radius && z < mx.Z+radius {
			if !b.blocksColumn(x, z, walkTop, headY, radius) {
				continue
			}
			if b.PassableThroughHole(x, z, walkTop, headY, radius) {
				continue
			}
			return true
		}
	}

	for i := range s.Cylinders {
		if s.isDynamicCylinder(i) {
			continue
		}
		c := &s.Cylinders[i]
		mn, mx := c.WorldBounds()
		if mx.Y <= walkTop || mn.Y >= headY {
			continue
		}
		if x > mn.X-radius && x < mx.X+radius && z > mn.Z-radius && z < mx.Z+radius {
			if c.blocksColumn(x, z, walkTop, headY, radius) {
				return true
			}
		}
	}

	for i := range s.Cones {
		co := &s.Cones[i]
		mn, mx := co.WorldBounds()
		if mx.Y <= walkTop || mn.Y >= headY {
			continue
		}
		if x > mn.X-radius && x < mx.X+radius && z > mn.Z-radius && z < mx.Z+radius {
			if co.blocksColumn(x, z, walkTop, headY, radius) {
				return true
			}
		}
	}

	for i := range s.Spheres {
		if s.isDynamicSphere(i) {
			continue
		}
		sp := &s.Spheres[i]
		if sp.Mat == MatEmit {
			continue
		}
		center := sp.Center
		if sp.Xform != nil {
			center = sp.Xform.ToWorld(center)
		}
		bottom := center.Y - sp.Radius
		top := center.Y + sp.Radius
		if top <= walkTop || bottom >= headY || bottom > walkTop {
			continue
		}
		dx, dz := x-center.X, z-center.Z
		rr := radius + sp.Radius
		if dx*dx+dz*dz < rr*rr {
			return true
		}
	}

	return false
}
