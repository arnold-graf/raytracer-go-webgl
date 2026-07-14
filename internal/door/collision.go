package door

import (
	"math"

	"raytracer/internal/scene"
	"raytracer/internal/util"
	"raytracer/internal/vec"
)

const playerRadius = 0.3

// PlayerOverlapsPanel reports whether the player capsule intersects any box in
// the panel at its current world pose.
func PlayerOverlapsPanel(sc *scene.Scene, p Panel, feetY, headY float64, pos vec.V) bool {
	for _, idx := range p.boxIndices() {
		if sc.PlayerOverlapsBox(idx, feetY, headY, pos, playerRadius, 0.45) {
			return true
		}
	}
	return false
}

// PanelHitsStatic reports whether the panel's primary box in its current pose
// intersects static scene geometry (excluding dynamic bodies and own panel).
func PanelHitsStatic(sc *scene.Scene, p Panel, skipBox func(int) bool) bool {
	return len(panelStaticHits(sc, p, skipBox)) > 0
}

func panelStaticHits(sc *scene.Scene, p Panel, skipBox func(int) bool) []int {
	idx := p.Geom.PrimaryBox()
	if idx < 0 {
		return nil
	}
	hits := sc.ProbeBoxStaticHits(idx, skipBox)
	out := make([]int, 0, len(hits))
	for _, h := range hits {
		if h.Box >= 0 {
			out = append(out, h.Box)
		} else if h.Cylinder >= 0 {
			out = append(out, -(h.Cylinder + 1))
		}
	}
	return out
}

func staticHitsAt(sc *scene.Scene, a *Agent, p *Panel, angle float64, skipBox func(int) bool) map[int]bool {
	applyPanelXform(sc, a, p, angle)
	hits := panelStaticHits(sc, *p, skipBox)
	set := make(map[int]bool, len(hits))
	for _, h := range hits {
		set[h] = true
	}
	applyPanelXform(sc, a, p, p.Angle)
	return set
}

func clampAngle(sc *scene.Scene, a *Agent, p *Panel, proposed float64, skipBox func(int) bool) float64 {
	current := p.Angle
	at := func(angle float64) map[int]bool {
		return staticHitsAt(sc, a, p, angle, skipBox)
	}
	return util.ClampNewContacts(current, proposed, at)
}

func PanelHitsStaticAt(sc *scene.Scene, a *Agent, p *Panel, angle float64, skipBox func(int) bool) bool {
	applyPanelXform(sc, a, p, angle)
	hit := PanelHitsStatic(sc, *p, skipBox)
	applyPanelXform(sc, a, p, p.Angle)
	return hit
}

func applyPanelXform(sc *scene.Scene, a *Agent, p *Panel, offset float64) {
	if a.isSliding() {
		applySlidePanelXform(sc, a, p, offset)
		return
	}
	rot := scene.RotationAboutAxis(a.Axis, offset*180/math.Pi, p.Hinge)
	for i, base := range p.closed.boxes {
		if i < 0 || i >= len(sc.Boxes) {
			continue
		}
		sc.Boxes[i].Xform = rot.Compose(base)
	}
	for i, base := range p.closed.spheres {
		if i < 0 || i >= len(sc.Spheres) {
			continue
		}
		sc.Spheres[i].Xform = rot.Compose(base)
	}
	for i, base := range p.closed.cylinders {
		if i < 0 || i >= len(sc.Cylinders) {
			continue
		}
		sc.Cylinders[i].Xform = rot.Compose(base)
	}
}

func applySlidePanelXform(sc *scene.Scene, a *Agent, p *Panel, offset float64) {
	delta := a.SlideDir.Scale(offset)
	slide := scene.Translation(delta)
	for i, base := range p.closed.boxes {
		if i < 0 || i >= len(sc.Boxes) {
			continue
		}
		if slide == nil {
			sc.Boxes[i].Xform = base
			continue
		}
		sc.Boxes[i].Xform = slide.Compose(base)
	}
	for i, base := range p.closed.spheres {
		if i < 0 || i >= len(sc.Spheres) {
			continue
		}
		if slide == nil {
			sc.Spheres[i].Xform = base
			continue
		}
		sc.Spheres[i].Xform = slide.Compose(base)
	}
	for i, base := range p.closed.cylinders {
		if i < 0 || i >= len(sc.Cylinders) {
			continue
		}
		if slide == nil {
			sc.Cylinders[i].Xform = base
			continue
		}
		sc.Cylinders[i].Xform = slide.Compose(base)
	}
}
