package scene

// ReactiveSpan records a reactive object's primitive ranges inside a merged scene.
type ReactiveSpan struct {
	Spheres   [2]int
	Boxes     [2]int
	Cylinders [2]int
	Cones     [2]int
	Tori      [2]int
	Rings     [2]int
	Lenses    [2]int
	Lights    [2]int
	Campfires [2]int
	Ambiences [2]int

	Interactables [2]int
}

// OffsetReactiveSpan shifts a local fragment span into a parent scene after merge.
func OffsetReactiveSpan(sp ReactiveSpan, before PrimitiveCounts, iaBefore int) ReactiveSpan {
	return ReactiveSpan{
		Spheres:       [2]int{sp.Spheres[0] + before.Spheres, sp.Spheres[1] + before.Spheres},
		Boxes:         [2]int{sp.Boxes[0] + before.Boxes, sp.Boxes[1] + before.Boxes},
		Cylinders:     [2]int{sp.Cylinders[0] + before.Cylinders, sp.Cylinders[1] + before.Cylinders},
		Cones:         [2]int{sp.Cones[0] + before.Cones, sp.Cones[1] + before.Cones},
		Tori:          [2]int{sp.Tori[0] + before.Tori, sp.Tori[1] + before.Tori},
		Rings:         [2]int{sp.Rings[0] + before.Rings, sp.Rings[1] + before.Rings},
		Lenses:        [2]int{sp.Lenses[0] + before.Lenses, sp.Lenses[1] + before.Lenses},
		Lights:        [2]int{sp.Lights[0] + before.Lights, sp.Lights[1] + before.Lights},
		Campfires:     [2]int{sp.Campfires[0] + before.Campfires, sp.Campfires[1] + before.Campfires},
		Ambiences:     [2]int{sp.Ambiences[0] + before.Ambiences, sp.Ambiences[1] + before.Ambiences},
		Interactables: [2]int{sp.Interactables[0] + iaBefore, sp.Interactables[1] + iaBefore},
	}
}

// SpanFromMerge builds span indices from primitive counts before and after a merge.
func SpanFromMerge(before, after PrimitiveCounts, iaBefore, iaAfter int) ReactiveSpan {
	return ReactiveSpan{
		Spheres:       [2]int{before.Spheres, after.Spheres},
		Boxes:         [2]int{before.Boxes, after.Boxes},
		Cylinders:     [2]int{before.Cylinders, after.Cylinders},
		Cones:         [2]int{before.Cones, after.Cones},
		Tori:          [2]int{before.Tori, after.Tori},
		Rings:         [2]int{before.Rings, after.Rings},
		Lenses:        [2]int{before.Lenses, after.Lenses},
		Lights:        [2]int{before.Lights, after.Lights},
		Campfires:     [2]int{before.Campfires, after.Campfires},
		Ambiences:     [2]int{before.Ambiences, after.Ambiences},
		Interactables: [2]int{iaBefore, iaAfter},
	}
}

// Counts returns how many primitives of each type this span owns.
func (sp ReactiveSpan) Counts() PrimitiveCounts {
	return PrimitiveCounts{
		Spheres:   sp.Spheres[1] - sp.Spheres[0],
		Boxes:     sp.Boxes[1] - sp.Boxes[0],
		Cylinders: sp.Cylinders[1] - sp.Cylinders[0],
		Cones:     sp.Cones[1] - sp.Cones[0],
		Tori:      sp.Tori[1] - sp.Tori[0],
		Rings:     sp.Rings[1] - sp.Rings[0],
		Lenses:    sp.Lenses[1] - sp.Lenses[0],
		Lights:    sp.Lights[1] - sp.Lights[0],
		Campfires: sp.Campfires[1] - sp.Campfires[0],
		Ambiences: sp.Ambiences[1] - sp.Ambiences[0],
	}
}

// SameStructureAs reports whether local and span have matching primitive counts.
func (sp ReactiveSpan) SameStructureAs(local *Scene) bool {
	if local == nil {
		return false
	}
	c := sp.Counts()
	return c.Spheres == len(local.Spheres) &&
		c.Boxes == len(local.Boxes) &&
		c.Cylinders == len(local.Cylinders) &&
		c.Cones == len(local.Cones) &&
		c.Tori == len(local.Tori) &&
		c.Rings == len(local.Rings) &&
		c.Lenses == len(local.Lenses) &&
		c.Lights == len(local.Lights) &&
		c.Campfires == len(local.Campfires) &&
		c.Ambiences == len(local.Ambiences) &&
		(sp.Interactables[1]-sp.Interactables[0]) == len(local.Interactables)
}

// ShiftAfter adds delta to all span indices strictly after the edited range for kind.
func (sp *ReactiveSpan) ShiftAfter(delta PrimitiveCounts, iaDelta int) {
	sp.Spheres = shiftPairAfter(sp.Spheres, delta.Spheres)
	sp.Boxes = shiftPairAfter(sp.Boxes, delta.Boxes)
	sp.Cylinders = shiftPairAfter(sp.Cylinders, delta.Cylinders)
	sp.Cones = shiftPairAfter(sp.Cones, delta.Cones)
	sp.Tori = shiftPairAfter(sp.Tori, delta.Tori)
	sp.Rings = shiftPairAfter(sp.Rings, delta.Rings)
	sp.Lenses = shiftPairAfter(sp.Lenses, delta.Lenses)
	sp.Lights = shiftPairAfter(sp.Lights, delta.Lights)
	sp.Campfires = shiftPairAfter(sp.Campfires, delta.Campfires)
	sp.Ambiences = shiftPairAfter(sp.Ambiences, delta.Ambiences)
	sp.Interactables = shiftPairAfter(sp.Interactables, iaDelta)
}

func shiftPairAfter(pair [2]int, delta int) [2]int {
	if delta == 0 {
		return pair
	}
	return [2]int{pair[0], pair[1] + delta}
}

// ShiftAll adds delta to every index in the span (when earlier geometry was inserted).
func (sp *ReactiveSpan) ShiftAll(delta PrimitiveCounts, iaDelta int) {
	sp.Spheres[0] += delta.Spheres
	sp.Spheres[1] += delta.Spheres
	sp.Boxes[0] += delta.Boxes
	sp.Boxes[1] += delta.Boxes
	sp.Cylinders[0] += delta.Cylinders
	sp.Cylinders[1] += delta.Cylinders
	sp.Cones[0] += delta.Cones
	sp.Cones[1] += delta.Cones
	sp.Tori[0] += delta.Tori
	sp.Tori[1] += delta.Tori
	sp.Rings[0] += delta.Rings
	sp.Rings[1] += delta.Rings
	sp.Lenses[0] += delta.Lenses
	sp.Lenses[1] += delta.Lenses
	sp.Lights[0] += delta.Lights
	sp.Lights[1] += delta.Lights
	sp.Campfires[0] += delta.Campfires
	sp.Campfires[1] += delta.Campfires
	sp.Ambiences[0] += delta.Ambiences
	sp.Ambiences[1] += delta.Ambiences
	sp.Interactables[0] += iaDelta
	sp.Interactables[1] += iaDelta
}
