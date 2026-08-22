package joltphys

import (
	"math"
	"strings"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

const attachDetachedMaxDist = 0.75

// attachDetachedBoxes links runtime-spawned screen and document panels to the
// nearest dynamic physics body so they move with props like laptops.
func (w *World) attachDetachedBoxes(sc *scene.Scene) {
	if w == nil || sc == nil {
		return
	}
	for _, db := range sc.DynamicBodies {
		if !isDetachedPanelBody(db.Name) {
			continue
		}
		if db.Boxes[1] <= db.Boxes[0] {
			continue
		}
		boxIdx := db.Boxes[0]
		if w.isBoxBound(boxIdx) {
			continue
		}
		center := boxWorldCenter(sc, boxIdx)
		bind := w.findNearestDynamicBinding(sc, center)
		if bind == nil {
			continue
		}
		xf := sc.Boxes[boxIdx].Xform
		if xf == nil {
			continue
		}
		restInv := bind.rest.Inverse()
		bind.prims = append(bind.prims, primBinding{
			kind:  0,
			index: boxIdx,
			rel:   restInv.Compose(copyTransform(xf)),
		})
		w.attachNearbyLights(sc, bind, center)
	}
	w.attachOrphanLights(sc)
}

func isDetachedPanelBody(name string) bool {
	return strings.HasPrefix(name, "screen_") || strings.HasPrefix(name, "document_")
}

func (w *World) isBoxBound(boxIdx int) bool {
	for i := range w.bindings.bindings {
		for _, p := range w.bindings.bindings[i].prims {
			if p.kind == 0 && p.index == boxIdx {
				return true
			}
		}
	}
	return false
}

func (w *World) isLightBound(lightIdx int) bool {
	for i := range w.bindings.bindings {
		for _, p := range w.bindings.bindings[i].prims {
			if p.kind == 3 && p.index == lightIdx {
				return true
			}
		}
	}
	return false
}

func (w *World) attachOrphanLights(sc *scene.Scene) {
	for i, l := range sc.Lights {
		if w.isLightBound(i) {
			continue
		}
		bind := w.findNearestDynamicBinding(sc, l.Pos)
		if bind == nil {
			continue
		}
		restInv := bind.rest.Inverse()
		bind.prims = append(bind.prims, lightPrimBinding(restInv, l, i))
	}
}

func (w *World) attachNearbyLights(sc *scene.Scene, bind *simBinding, center vec.V) {
	if bind == nil || bind.rest == nil {
		return
	}
	restInv := bind.rest.Inverse()
	maxSq := attachDetachedMaxDist * attachDetachedMaxDist
	for i, l := range sc.Lights {
		if w.isLightBound(i) {
			continue
		}
		if l.Pos.Sub(center).LenSq() > maxSq {
			continue
		}
		bind.prims = append(bind.prims, lightPrimBinding(restInv, l, i))
	}
}

func (w *World) findNearestDynamicBinding(sc *scene.Scene, center vec.V) *simBinding {
	var best *simBinding
	bestDist := math.Inf(1)
	maxSq := attachDetachedMaxDist * attachDetachedMaxDist
	for i := range w.bindings.bindings {
		b := &w.bindings.bindings[i]
		if b.kinematic || b.isDoor || b.body == nil {
			continue
		}
		for _, p := range b.prims {
			c := primWorldCenter(sc, p)
			d := center.Sub(c).LenSq()
			if d < bestDist {
				bestDist = d
				best = b
			}
		}
	}
	if best != nil && bestDist <= maxSq {
		return best
	}
	return nil
}

func boxWorldCenter(sc *scene.Scene, i int) vec.V {
	if sc == nil || i < 0 || i >= len(sc.Boxes) {
		return vec.V{}
	}
	b := sc.Boxes[i]
	if b.Xform != nil {
		mn, mx := b.WorldBounds()
		return vec.New((mn.X+mx.X)*0.5, (mn.Y+mx.Y)*0.5, (mn.Z+mx.Z)*0.5)
	}
	mn, mx := b.Min, b.Max
	return vec.New((mn.X+mx.X)*0.5, (mn.Y+mx.Y)*0.5, (mn.Z+mx.Z)*0.5)
}

func primWorldCenter(sc *scene.Scene, p primBinding) vec.V {
	switch p.kind {
	case 0:
		return boxWorldCenter(sc, p.index)
	case 1:
		if p.index < 0 || p.index >= len(sc.Spheres) {
			return vec.V{}
		}
		sp := sc.Spheres[p.index]
		if sp.Xform != nil {
			return sp.Xform.ToWorld(sp.Center)
		}
		return sp.Center
	case 2:
		if p.index < 0 || p.index >= len(sc.Cylinders) {
			return vec.V{}
		}
		c := sc.Cylinders[p.index]
		center := vec.New(c.CX, (c.YMin+c.YMax)*0.5, c.CZ)
		if c.Xform != nil {
			return c.Xform.ToWorld(center)
		}
		return center
	case 3:
		if p.index < 0 || p.index >= len(sc.Lights) {
			return vec.V{}
		}
		return sc.Lights[p.index].Pos
	}
	return vec.V{}
}
