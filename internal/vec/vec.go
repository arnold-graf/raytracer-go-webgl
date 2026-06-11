// Package vec provides a minimal 3D vector type and ray used throughout the
// renderer. Methods take and return values (not pointers) so the compiler can
// keep them on the stack and inline the hot-path arithmetic.
package vec

import "math"

// V is a 3D vector / point / color triple.
type V struct {
	X, Y, Z float64
}

// New builds a vector from its components.
func New(x, y, z float64) V { return V{x, y, z} }

// Add returns a + b.
func (a V) Add(b V) V { return V{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }

// Sub returns a - b.
func (a V) Sub(b V) V { return V{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }

// Scale returns a * s.
func (a V) Scale(s float64) V { return V{a.X * s, a.Y * s, a.Z * s} }

// Mul returns the component-wise product a * b (used for color modulation).
func (a V) Mul(b V) V { return V{a.X * b.X, a.Y * b.Y, a.Z * b.Z} }

// Neg returns -a.
func (a V) Neg() V { return V{-a.X, -a.Y, -a.Z} }

// Dot returns the dot product a · b.
func (a V) Dot(b V) float64 { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }

// Cross returns the cross product a × b.
func (a V) Cross(b V) V {
	return V{
		a.Y*b.Z - a.Z*b.Y,
		a.Z*b.X - a.X*b.Z,
		a.X*b.Y - a.Y*b.X,
	}
}

// LenSq returns the squared length, avoiding a sqrt when only comparisons are needed.
func (a V) LenSq() float64 { return a.X*a.X + a.Y*a.Y + a.Z*a.Z }

// Len returns the Euclidean length.
func (a V) Len() float64 { return math.Sqrt(a.LenSq()) }

// Normalize returns a unit-length copy of a. A zero vector is returned unchanged.
func (a V) Normalize() V {
	l := a.Len()
	if l == 0 {
		return a
	}
	return a.Scale(1 / l)
}

// Reflect returns a reflected about the unit normal n.
func (a V) Reflect(n V) V {
	return a.Sub(n.Scale(2 * a.Dot(n)))
}

// Ray is a half-line with an origin and (normally unit) direction.
type Ray struct {
	Origin V
	Dir    V
}

// At returns the point Origin + t*Dir.
func (r Ray) At(t float64) V { return r.Origin.Add(r.Dir.Scale(t)) }
