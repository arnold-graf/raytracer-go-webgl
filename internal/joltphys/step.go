package joltphys

import (
	"raytracer/internal/scene"
)

func (w *World) SyncDynamicPoses(sc *scene.Scene) bool {
	if w == nil || sc == nil {
		return false
	}
	changed := false
	for i := range w.bindings.bindings {
		b := &w.bindings.bindings[i]
		if b.isDoor || b.kinematic || b.body == nil {
			continue
		}
		if !w.bi.IsActive(b.body) && !bodyMovedSinceSleep(w, b) {
			continue
		}
		pos := w.bi.GetPosition(b.body)
		rot := w.bi.GetRotation(b.body)
		current := transformFromJolt(pos, rot)
		for _, p := range b.prims {
			xf := current.Compose(p.rel)
			if applyPrimTransform(sc, p, xf) {
				changed = true
			}
		}
	}
	if changed {
		sc.TouchTransforms()
	}
	return changed
}

func bodyMovedSinceSleep(w *World, b *simBinding) bool {
	pos := w.bi.GetPosition(b.body)
	rot := w.bi.GetRotation(b.body)
	current := transformFromJolt(pos, rot)
	if b.rest == nil {
		return true
	}
	dt := current.Translation().Sub(b.rest.Translation())
	if dt.LenSq() > 1e-8 {
		return true
	}
	// Compare rotation via quaternion dot
	_, restRot := joltPoseFromTransform(b.rest)
	dot := float64(rot.X)*float64(restRot.X) + float64(rot.Y)*float64(restRot.Y) +
		float64(rot.Z)*float64(restRot.Z) + float64(rot.W)*float64(restRot.W)
	return mathAbs(1-dot) > 1e-6
}

func mathAbs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func applyPrimTransform(sc *scene.Scene, p primBinding, xf *scene.Transform) bool {
	switch p.kind {
	case 0:
		if p.index < 0 || p.index >= len(sc.Boxes) {
			return false
		}
		if transformsEqual(sc.Boxes[p.index].Xform, xf) {
			return false
		}
		sc.Boxes[p.index].Xform = xf
		return true
	case 1:
		if p.index < 0 || p.index >= len(sc.Spheres) {
			return false
		}
		if transformsEqual(sc.Spheres[p.index].Xform, xf) {
			return false
		}
		sc.Spheres[p.index].Xform = xf
		return true
	case 2:
		if p.index < 0 || p.index >= len(sc.Cylinders) {
			return false
		}
		if transformsEqual(sc.Cylinders[p.index].Xform, xf) {
			return false
		}
		sc.Cylinders[p.index].Xform = xf
		return true
	case 3:
		if p.index < 0 || p.index >= len(sc.Lights) {
			return false
		}
		l := &sc.Lights[p.index]
		newPos := xf.Translation()
		changed := false
		if l.Pos.Sub(newPos).LenSq() > 1e-10 {
			l.Pos = newPos
			changed = true
		}
		if p.dir.LenSq() > 1e-12 {
			newDir := xf.RotateDir(p.dir)
			if l.Dir.Sub(newDir).LenSq() > 1e-10 {
				l.Dir = newDir
				changed = true
			}
		}
		return changed
	}
	return false
}

func transformsEqual(a, b *scene.Transform) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	ta, tb := a.Translation(), b.Translation()
	if ta.Sub(tb).LenSq() > 1e-10 {
		return false
	}
	af, bf := a.Fwd(), b.Fwd()
	for i := 0; i < 9; i++ {
		if mathAbs(af[i]-bf[i]) > 1e-6 {
			return false
		}
	}
	return true
}
