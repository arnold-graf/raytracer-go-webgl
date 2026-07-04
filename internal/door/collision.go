package door

import (
	"math"

	"raytracer/internal/scene"
	"raytracer/internal/util"
	"raytracer/internal/vec"
)

const playerRadius = 0.3

// PlayerOverlapsPanel reports whether the player capsule intersects a door panel
// box at its current world pose.
func PlayerOverlapsPanel(sc *scene.Scene, boxIdx int, feetY, headY float64, pos vec.V) bool {
	return sc.PlayerOverlapsBox(boxIdx, feetY, headY, pos, playerRadius, 0.45)
}

// PanelHitsStatic reports whether the panel at boxIdx in its current pose
// intersects static scene geometry (excluding dynamic bodies and own panel).
func PanelHitsStatic(sc *scene.Scene, boxIdx int, skipBox func(int) bool) bool {
	return len(panelStaticHits(sc, boxIdx, skipBox)) > 0
}

func panelStaticHits(sc *scene.Scene, boxIdx int, skipBox func(int) bool) []int {
	if sc == nil || boxIdx < 0 || boxIdx >= len(sc.Boxes) {
		return nil
	}
	hits := sc.ProbeBoxStaticHits(boxIdx, skipBox)
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
	hits := panelStaticHits(sc, p.BoxIndex, skipBox)
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
	hit := PanelHitsStatic(sc, p.BoxIndex, skipBox)
	applyPanelXform(sc, a, p, p.Angle)
	return hit
}

func applyPanelXform(sc *scene.Scene, a *Agent, p *Panel, angle float64) {
	if p.BoxIndex < 0 || p.BoxIndex >= len(sc.Boxes) {
		return
	}
	rot := scene.RotationAboutAxis(a.Axis, angle*180/math.Pi, p.Hinge)
	// Rotate the placed panel about the world hinge: rot(placed(local)).
	sc.Boxes[p.BoxIndex].Xform = rot.Compose(p.ClosedBase)
}
