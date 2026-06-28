package character

import (
	"math"

	"raytracer/internal/vec"
)

// TwoBoneResult holds solved joint positions for a two-segment chain in the
// plane defined by root, target, and pole.
type TwoBoneResult struct {
	Mid  vec.V
	End  vec.V
	OK   bool
}

// SolveTwoBone finds the elbow/knee position for a chain root→mid→end with
// segment lengths l1 and l2 reaching target. pole hints bend direction.
func SolveTwoBone(root, target, pole vec.V, l1, l2 float64) TwoBoneResult {
	return solveTwoBone(root, target, pole, l1, l2, 0)
}

// SolveTwoBoneMinBend is like SolveTwoBone but never fully extends the chain;
// minBendDeg is the minimum flex angle at the mid joint (0 = allow straight).
func SolveTwoBoneMinBend(root, target, pole vec.V, l1, l2, minBendDeg float64) TwoBoneResult {
	return solveTwoBone(root, target, pole, l1, l2, minBendDeg)
}

// twoBoneMaxReach returns the maximum root→target distance while keeping at
// least minBendDeg of flex at the mid joint.
func twoBoneMaxReach(l1, l2, minBendDeg float64) float64 {
	if minBendDeg <= 0 {
		return l1 + l2
	}
	b := minBendDeg * math.Pi / 180
	return math.Sqrt(l1*l1 + l2*l2 + 2*l1*l2*math.Cos(b))
}

func solveTwoBone(root, target, pole vec.V, l1, l2, minBendDeg float64) TwoBoneResult {
	d := target.Sub(root)
	dist := d.Len()
	maxReach := twoBoneMaxReach(l1, l2, minBendDeg) - 1e-6
	minReach := math.Abs(l1-l2) + 1e-6
	if dist > maxReach {
		d = d.Scale(maxReach / dist)
		target = root.Add(d)
		dist = maxReach
	} else if dist < minReach {
		if dist < 1e-9 {
			d = vec.V{Y: -1}
			dist = 1
		}
		d = d.Scale(minReach / dist)
		target = root.Add(d)
		dist = minReach
	}

	dir := d.Scale(1 / dist)
	// Law of cosines: distance along dir from root to mid joint.
	a := (l1*l1 - l2*l2 + dist*dist) / (2 * dist)
	h2 := l1*l1 - a*a
	if h2 < 0 {
		h2 = 0
	}
	h := math.Sqrt(h2)

	midOnLine := root.Add(dir.Scale(a))
	// Bend plane normal from root→target and pole hint.
	planeN := dir.Cross(pole.Sub(root))
	if planeN.LenSq() < 1e-12 {
		planeN = dir.Cross(vec.V{Y: 1})
	}
	if planeN.LenSq() < 1e-12 {
		planeN = dir.Cross(vec.V{X: 1})
	}
	planeN = planeN.Normalize()
	bendDir := planeN.Cross(dir).Normalize()

	mid := midOnLine.Add(bendDir.Scale(h))
	toTarget := target.Sub(mid)
	if toTarget.LenSq() < 1e-12 {
		toTarget = dir
	}
	end := mid.Add(toTarget.Normalize().Scale(l2))
	return TwoBoneResult{Mid: mid, End: end, OK: true}
}

// EndError returns the distance between the solved end and the target.
func (r TwoBoneResult) EndError(target vec.V) float64 {
	return r.End.Sub(target).Len()
}
