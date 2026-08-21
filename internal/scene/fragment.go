package scene

import "raytracer/internal/vec"

// CopyFragment overwrites dst span primitives with local (untransformed) content.
func CopyFragment(dst *Scene, span ReactiveSpan, local *Scene) {
	if dst == nil || local == nil {
		return
	}
	copySlice(dst.Spheres[span.Spheres[0]:span.Spheres[1]], local.Spheres)
	copySlice(dst.Boxes[span.Boxes[0]:span.Boxes[1]], local.Boxes)
	copySlice(dst.Cylinders[span.Cylinders[0]:span.Cylinders[1]], local.Cylinders)
	copySlice(dst.Cones[span.Cones[0]:span.Cones[1]], local.Cones)
	copySlice(dst.Tori[span.Tori[0]:span.Tori[1]], local.Tori)
	copySlice(dst.Rings[span.Rings[0]:span.Rings[1]], local.Rings)
	copySlice(dst.Lenses[span.Lenses[0]:span.Lenses[1]], local.Lenses)
	copySlice(dst.Lights[span.Lights[0]:span.Lights[1]], local.Lights)
	copySlice(dst.Campfires[span.Campfires[0]:span.Campfires[1]], local.Campfires)
	copySlice(dst.Ambiences[span.Ambiences[0]:span.Ambiences[1]], local.Ambiences)
}

func copySlice[T any](dst, src []T) {
	copy(dst, src)
}

// FragmentTouchLevel reports whether a fragment refresh needs full geometry or pose-only invalidation.
func FragmentTouchLevel(dst *Scene, span ReactiveSpan, local *Scene) (needGen, needXform bool) {
	if dst == nil || local == nil {
		return false, false
	}

	if span.Lights[1] > span.Lights[0] {
		old := dst.Lights[span.Lights[0]:span.Lights[1]]
		for i := range old {
			n := local.Lights[i]
			if old[i].Color != n.Color {
				needXform = true
			}
			if old[i].Interactive != n.Interactive || old[i].Hint != n.Hint ||
				old[i].Pos != n.Pos || old[i].Dir != n.Dir ||
				old[i].ConeDeg != n.ConeDeg || old[i].Range != n.Range || old[i].Radius != n.Radius {
				needGen = true
			}
		}
	}
	if span.Boxes[1] > span.Boxes[0] {
		old := dst.Boxes[span.Boxes[0]:span.Boxes[1]]
		for i := range old {
			n := local.Boxes[i]
			if old[i].Min != n.Min || old[i].Max != n.Max || len(old[i].Holes) != len(n.Holes) {
				needGen = true
				continue
			}
			if !surfaceShadingEqual(old[i].Surface, n.Surface) || old[i].FaceTex != n.FaceTex {
				needGen = true
			}
		}
	}
	if span.Spheres[1] > span.Spheres[0] {
		for i := range local.Spheres {
			o := dst.Spheres[span.Spheres[0]+i]
			n := local.Spheres[i]
			if o.Center != n.Center || o.Radius != n.Radius || !surfaceShadingEqual(o.Surface, n.Surface) {
				needGen = true
			}
		}
	}
	if span.Cylinders[1] > span.Cylinders[0] {
		for i := range local.Cylinders {
			o := dst.Cylinders[span.Cylinders[0]+i]
			n := local.Cylinders[i]
			if o.CX != n.CX || o.CZ != n.CZ || o.YMin != n.YMin || o.YMax != n.YMax ||
				o.Radius != n.Radius || o.RadiusTop != n.RadiusTop || !surfaceShadingEqual(o.Surface, n.Surface) {
				needGen = true
			}
		}
	}
	return needGen, needXform
}

func surfaceShadingEqual(a, b Surface) bool {
	return a.Mat == b.Mat && a.Albedo == b.Albedo && a.Albedo2 == b.Albedo2 &&
		a.Rough == b.Rough && a.IOR == b.IOR && a.Tex == b.Tex &&
		a.Reflect == b.Reflect && a.Transmit == b.Transmit && a.Thin == b.Thin && a.TwoPane == b.TwoPane &&
		a.NoCollision == b.NoCollision
}

func surfacesFromSpheres(ss []Sphere) []Surface {
	out := make([]Surface, len(ss))
	for i := range ss {
		out[i] = ss[i].Surface
	}
	return out
}

// SpliceFragment replaces span primitives with local content, resizing slices when counts differ.
// Returns the count delta applied and updates span to cover the new range.
func SpliceFragment(dst *Scene, span *ReactiveSpan, local *Scene) (delta PrimitiveCounts, iaDelta int) {
	if dst == nil || span == nil || local == nil {
		return PrimitiveCounts{}, 0
	}
	old := span.Counts()
	newC := PrimitiveCounts{
		Spheres: len(local.Spheres), Boxes: len(local.Boxes),
		Cylinders: len(local.Cylinders), Cones: len(local.Cones), Tori: len(local.Tori),
		Rings: len(local.Rings), Lenses: len(local.Lenses),
		Lights: len(local.Lights), Campfires: len(local.Campfires), Ambiences: len(local.Ambiences),
	}
	delta = PrimitiveCounts{
		Spheres: newC.Spheres - old.Spheres,
		Boxes: newC.Boxes - old.Boxes, Cylinders: newC.Cylinders - old.Cylinders,
		Cones: newC.Cones - old.Cones, Tori: newC.Tori - old.Tori,
		Rings: newC.Rings - old.Rings, Lenses: newC.Lenses - old.Lenses,
		Lights: newC.Lights - old.Lights, Campfires: newC.Campfires - old.Campfires,
		Ambiences: newC.Ambiences - old.Ambiences,
	}
	oldIA := span.Interactables[1] - span.Interactables[0]
	iaDelta = len(local.Interactables) - oldIA

	spliceSlice(&dst.Spheres, span.Spheres, local.Spheres)
	spliceSlice(&dst.Boxes, span.Boxes, local.Boxes)
	spliceSlice(&dst.Cylinders, span.Cylinders, local.Cylinders)
	spliceSlice(&dst.Cones, span.Cones, local.Cones)
	spliceSlice(&dst.Tori, span.Tori, local.Tori)
	spliceSlice(&dst.Rings, span.Rings, local.Rings)
	spliceSlice(&dst.Lenses, span.Lenses, local.Lenses)
	spliceSlice(&dst.Lights, span.Lights, local.Lights)
	remapLightReferencesAfter(dst, span.Lights, delta.Lights)
	spliceSlice(&dst.Campfires, span.Campfires, local.Campfires)
	spliceSlice(&dst.Ambiences, span.Ambiences, local.Ambiences)

	span.Spheres[1] = span.Spheres[0] + newC.Spheres
	span.Boxes[1] = span.Boxes[0] + newC.Boxes
	span.Cylinders[1] = span.Cylinders[0] + newC.Cylinders
	span.Cones[1] = span.Cones[0] + newC.Cones
	span.Tori[1] = span.Tori[0] + newC.Tori
	span.Rings[1] = span.Rings[0] + newC.Rings
	span.Lenses[1] = span.Lenses[0] + newC.Lenses
	span.Lights[1] = span.Lights[0] + newC.Lights
	span.Campfires[1] = span.Campfires[0] + newC.Campfires
	span.Ambiences[1] = span.Ambiences[0] + newC.Ambiences

	spliceInteractables(dst, span, local)
	span.Interactables[1] = span.Interactables[0] + len(local.Interactables)
	return delta, iaDelta
}

func spliceSlice[T any](dst *[]T, span [2]int, local []T) {
	out := append([]T(nil), (*dst)[:span[0]]...)
	out = append(out, local...)
	out = append(out, (*dst)[span[1]:]...)
	*dst = out
}

func spliceInteractables(dst *Scene, span *ReactiveSpan, local *Scene) {
	if local == nil {
		return
	}
	// Drop old interact maps for removed indices.
	clearInteractMaps(dst, *span)
	out := append([]Interactable(nil), dst.Interactables[:span.Interactables[0]]...)
	for i := range local.Interactables {
		ia := local.Interactables[i]
		iaIdx := span.Interactables[0] + i
		ia.index = iaIdx
		out = append(out, ia)
	}
	out = append(out, dst.Interactables[span.Interactables[1]:]...)
	dst.Interactables = out
	remapInteractablesAfter(dst, span.Interactables[0]+len(local.Interactables), iaDelta(len(local.Interactables), span.Interactables[1]-span.Interactables[0]))
	applyFragmentInteractBindings(dst, *span, local)
}

func applyFragmentInteractBindings(dst *Scene, span ReactiveSpan, local *Scene) {
	iaSpan := span.Interactables
	dst.ApplyInteractBindings(local, InteractBindingOffsets{
		Boxes:         span.Boxes[0],
		Spheres:       span.Spheres[0],
		Lights:        span.Lights[0],
		Interactables: span.Interactables[0],
	}, &iaSpan)
}

func iaDelta(newCount, oldCount int) int {
	return newCount - oldCount
}

func remapLightReferencesAfter(dst *Scene, span [2]int, lightDelta int) {
	if dst == nil || lightDelta == 0 {
		return
	}
	if dst.lightInteract != nil {
		next := make(map[int]int, len(dst.lightInteract))
		for lightIdx, iaIdx := range dst.lightInteract {
			switch {
			case lightIdx >= span[0] && lightIdx < span[1]:
				continue
			case lightIdx >= span[1]:
				next[lightIdx+lightDelta] = iaIdx
			default:
				next[lightIdx] = iaIdx
			}
		}
		dst.lightInteract = next
	}
	for i := range dst.Interactables {
		li := dst.Interactables[i].LightIndex
		if li < 0 {
			continue
		}
		if li >= span[1] {
			dst.Interactables[i].LightIndex = li + lightDelta
		} else if li >= span[0] && li < span[1] {
			dst.Interactables[i].LightIndex = -1
		}
	}
	for i := range dst.DynamicBodies {
		shiftIndexRange(&dst.DynamicBodies[i].Lights, span, lightDelta)
	}
}

func shiftIndexRange(r *[2]int, span [2]int, delta int) {
	if r[1] <= r[0] {
		return
	}
	if r[0] >= span[1] {
		r[0] += delta
		r[1] += delta
		return
	}
	if r[1] <= span[0] {
		return
	}
	// Range overlapped removed span; clamp to empty (caller should rebind).
	if r[0] >= span[0] && r[1] <= span[1] {
		r[0], r[1] = 0, 0
	}
}

func clearInteractMaps(dst *Scene, span ReactiveSpan) {
	if dst.boxInteract != nil {
		for i := span.Boxes[0]; i < span.Boxes[1]; i++ {
			delete(dst.boxInteract, i)
		}
	}
	if dst.lightInteract != nil {
		for i := span.Lights[0]; i < span.Lights[1]; i++ {
			delete(dst.lightInteract, i)
		}
	}
	if dst.sphereInteract != nil {
		for i := span.Spheres[0]; i < span.Spheres[1]; i++ {
			delete(dst.sphereInteract, i)
		}
	}
}

func remapInteractablesAfter(dst *Scene, startIA, iaShift int) {
	if iaShift == 0 {
		return
	}
	for i := startIA; i < len(dst.Interactables); i++ {
		dst.Interactables[i].index = i
	}
	if dst.boxInteract != nil {
		next := make(map[int]int, len(dst.boxInteract))
		for box, ia := range dst.boxInteract {
			if ia >= startIA {
				next[box] = ia + iaShift
			} else {
				next[box] = ia
			}
		}
		dst.boxInteract = next
	}
	if dst.lightInteract != nil {
		next := make(map[int]int, len(dst.lightInteract))
		for light, ia := range dst.lightInteract {
			if ia >= startIA {
				next[light] = ia + iaShift
			} else {
				next[light] = ia
			}
		}
		dst.lightInteract = next
	}
	if dst.sphereInteract != nil {
		next := make(map[int]int, len(dst.sphereInteract))
		for sphere, ia := range dst.sphereInteract {
			if ia >= startIA {
				next[sphere] = ia + iaShift
			} else {
				next[sphere] = ia
			}
		}
		dst.sphereInteract = next
	}
}

// RefreshFragmentInteractables overwrites interactables in span when structure is unchanged.
func RefreshFragmentInteractables(dst *Scene, span ReactiveSpan, local *Scene, xf *Transform) {
	if dst == nil || local == nil || len(local.Interactables) == 0 {
		return
	}
	clearInteractMaps(dst, span)
	for i := range local.Interactables {
		iaIdx := span.Interactables[0] + i
		if iaIdx >= len(dst.Interactables) {
			break
		}
		ia := local.Interactables[i]
		ia.index = iaIdx
		_ = xf
		dst.Interactables[iaIdx] = ia
	}
	applyFragmentInteractBindings(dst, span, local)
}

// ComposeFragment applies a placement transform to all primitives in local before merge/copy.
func ComposeFragment(local *Scene, xf *Transform) {
	if local == nil || xf == nil {
		return
	}
	for i := range local.Spheres {
		local.Spheres[i].Xform = xf.Compose(local.Spheres[i].Xform)
	}
	for i := range local.Planes {
		o := &local.Planes[i]
		o.Xform = xf.Compose(o.Xform)
		pp := planePointFromND(o.N, o.D)
		o.N = xf.WorldNormal(o.N)
		o.D = -o.N.Dot(xf.ToWorld(pp))
	}
	for i := range local.Boxes {
		local.Boxes[i].Xform = xf.Compose(local.Boxes[i].Xform)
	}
	for i := range local.Cylinders {
		local.Cylinders[i].Xform = xf.Compose(local.Cylinders[i].Xform)
	}
	for i := range local.Cones {
		local.Cones[i].Xform = xf.Compose(local.Cones[i].Xform)
	}
	for i := range local.Tori {
		local.Tori[i].Xform = xf.Compose(local.Tori[i].Xform)
	}
	for i := range local.Rings {
		local.Rings[i].Xform = xf.Compose(local.Rings[i].Xform)
	}
	for i := range local.Lenses {
		local.Lenses[i].Xform = xf.Compose(local.Lenses[i].Xform)
	}
	for i := range local.Lights {
		local.Lights[i].Pos = xf.ToWorld(local.Lights[i].Pos)
	}
	for i := range local.Campfires {
		local.Campfires[i].Center = xf.ToWorld(local.Campfires[i].Center)
	}
	for i := range local.Ambiences {
		local.Ambiences[i].Pos = xf.ToWorld(local.Ambiences[i].Pos)
	}
}

func planePointFromND(n vec.V, d float64) vec.V {
	return n.Scale(-d)
}
