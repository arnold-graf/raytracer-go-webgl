package scene

import "raytracer/internal/vec"

// PlacementTransform builds world(p) = R·(p_local − origin) + at. The anchor
// point origin in local space maps to at in the parent/world frame.
func PlacementTransform(degX, degY, degZ float64, at, origin vec.V) *Transform {
	if degX == 0 && degY == 0 && degZ == 0 && at == origin {
		return nil
	}
	fwd := rotation(degX, degY, degZ)
	return &Transform{
		fwd:         fwd,
		inv:         fwd.transpose(),
		t:           at.Sub(fwd.mul(origin)),
		anchorLocal: origin,
	}
}

// AnchorLocal returns the local-space placement anchor (transform_origin). Zero
// when unset or identity.
func (x *Transform) AnchorLocal() vec.V {
	if x == nil {
		return vec.V{}
	}
	return x.anchorLocal
}

// PlacementAnchor returns the world position of the placement anchor.
func (x *Transform) PlacementAnchor() vec.V {
	if x == nil {
		return vec.V{}
	}
	return x.ToWorld(x.anchorLocal)
}

// LocalBoundsCenter returns the centroid of the union AABB of all finite
// geometry in s (template-local space, including per-primitive Xform).
func LocalBoundsCenter(s *Scene) (vec.V, bool) {
	if s == nil {
		return vec.V{}, false
	}
	var min, max vec.V
	first := true
	forEachTemplateBounds(s, false, func(lmin, lmax vec.V) {
		if first {
			min, max = lmin, lmax
			first = false
		} else {
			min = minV(min, lmin)
			max = maxV(max, lmax)
		}
	})
	if first {
		return vec.V{}, false
	}
	return vec.V{
		X: (min.X + max.X) / 2,
		Y: (min.Y + max.Y) / 2,
		Z: (min.Z + max.Z) / 2,
	}, true
}

// MigratedIncludeAt returns the at value that preserves world placement when
// switching an include from origin-pivot to center-pivot semantics.
func MigratedIncludeAt(oldAt vec.V, degX, degY, degZ float64, center vec.V) vec.V {
	fwd := rotation(degX, degY, degZ)
	return oldAt.Add(fwd.mul(center))
}
