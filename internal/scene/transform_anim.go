package scene

import "raytracer/internal/vec"

// SmoothStep is a smooth 0..1 easing curve (Hermite smoothstep).
func SmoothStep(t float64) float64 { return t * t * (3 - 2*t) }

// Clone returns a shallow copy of x, or nil when x is nil.
func (x *Transform) Clone() *Transform {
	if x == nil {
		return nil
	}
	cpy := *x
	return &cpy
}

// LerpTransform linearly interpolates position and blends local Y/Z axes from a toward b.
// At t=0 the result matches a; at t=1 it matches b (position and facing).
func LerpTransform(a, b *Transform, t float64) *Transform {
	if a == nil {
		return b.Clone()
	}
	if b == nil {
		return a.Clone()
	}
	pos := a.Translation().Add(b.Translation().Sub(a.Translation()).Scale(t))
	aZ := a.RotateDir(vec.V{Z: 1})
	aY := a.RotateDir(vec.V{Y: 1})
	bZ := b.RotateDir(vec.V{Z: 1})
	bY := b.RotateDir(vec.V{Y: 1})
	z := aZ.Scale(1 - t).Add(bZ.Scale(t))
	if z.LenSq() < 1e-12 {
		z = vec.V{Z: 1}
	} else {
		z = z.Normalize()
	}
	y := aY.Scale(1 - t).Add(bY.Scale(t))
	return NewTransformYZ(pos, y, z)
}
