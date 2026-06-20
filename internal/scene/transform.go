package scene

import (
	"math"

	"raytracer/internal/vec"
)

// mat3 is a row-major 3x3 matrix: indices 0,1,2 are the first row, etc.
type mat3 [9]float64

func (m mat3) mul(v vec.V) vec.V {
	return vec.V{
		X: m[0]*v.X + m[1]*v.Y + m[2]*v.Z,
		Y: m[3]*v.X + m[4]*v.Y + m[5]*v.Z,
		Z: m[6]*v.X + m[7]*v.Y + m[8]*v.Z,
	}
}

// mulM returns the matrix product m*o (apply o first, then m).
func (m mat3) mulM(o mat3) mat3 {
	var r mat3
	for row := 0; row < 3; row++ {
		for col := 0; col < 3; col++ {
			r[row*3+col] = m[row*3+0]*o[0*3+col] +
				m[row*3+1]*o[1*3+col] +
				m[row*3+2]*o[2*3+col]
		}
	}
	return r
}

func (m mat3) transpose() mat3 {
	return mat3{
		m[0], m[3], m[6],
		m[1], m[4], m[7],
		m[2], m[5], m[8],
	}
}

// Transform is a rigid object→world transform: world = fwd*local + t, where fwd
// is an orthonormal rotation. Because fwd is orthonormal its inverse is simply
// its transpose, so world→local is inv*(p - t). All methods are nil-safe and
// treat a nil receiver as the identity, so primitives without a transform pay
// nothing on the hot path.
type Transform struct {
	fwd mat3  // local → world rotation
	inv mat3  // world → local rotation (= fwd transpose)
	t   vec.V // translation
}

// rotation builds R = Rz * Ry * Rx from degrees, i.e. it applies the X rotation
// first, then Y, then Z. This is the conventional yaw/pitch/roll-friendly order.
func rotation(degX, degY, degZ float64) mat3 {
	rx := degX * math.Pi / 180
	ry := degY * math.Pi / 180
	rz := degZ * math.Pi / 180
	sx, cx := math.Sincos(rx)
	sy, cy := math.Sincos(ry)
	sz, cz := math.Sincos(rz)

	mx := mat3{1, 0, 0, 0, cx, -sx, 0, sx, cx}
	my := mat3{cy, 0, sy, 0, 1, 0, -sy, 0, cy}
	mz := mat3{cz, -sz, 0, sz, cz, 0, 0, 0, 1}
	return mz.mulM(my).mulM(mx)
}

// NewTransform builds a transform that rotates a primitive in place about pivot
// by the given Euler angles (degrees). The pivot maps to itself, so an object's
// position is preserved and only its orientation changes.
func NewTransform(degX, degY, degZ float64, pivot vec.V) *Transform {
	fwd := rotation(degX, degY, degZ)
	// world = fwd*(local - pivot) + pivot = fwd*local + (pivot - fwd*pivot).
	return &Transform{fwd: fwd, inv: fwd.transpose(), t: pivot.Sub(fwd.mul(pivot))}
}

// NewInstanceTransform builds the transform applied to an included sub-scene:
// rotate about the sub-scene origin, then translate by at. It returns nil when
// the transform is the identity so callers can skip merging work entirely.
func NewInstanceTransform(degX, degY, degZ float64, at vec.V) *Transform {
	if degX == 0 && degY == 0 && degZ == 0 && at == (vec.V{}) {
		return nil
	}
	fwd := rotation(degX, degY, degZ)
	return &Transform{fwd: fwd, inv: fwd.transpose(), t: at}
}

// LocalRay maps a world-space ray into the primitive's local space. Because the
// rotation is orthonormal the direction keeps unit length, so the hit parameter
// t is identical in both spaces.
func (x *Transform) LocalRay(r vec.Ray) vec.Ray {
	if x == nil {
		return r
	}
	return vec.Ray{
		Origin: x.inv.mul(r.Origin.Sub(x.t)),
		Dir:    x.inv.mul(r.Dir),
	}
}

// WorldNormal rotates a local-space normal back into world space.
func (x *Transform) WorldNormal(n vec.V) vec.V {
	if x == nil {
		return n
	}
	return x.fwd.mul(n).Normalize()
}

// ToWorld maps a local-space point into world space.
func (x *Transform) ToWorld(p vec.V) vec.V {
	if x == nil {
		return p
	}
	return x.fwd.mul(p).Add(x.t)
}

// ToLocal maps a world-space point into the primitive's local space (the inverse
// of ToWorld): local = inv * (p - t).
func (x *Transform) ToLocal(p vec.V) vec.V {
	if x == nil {
		return p
	}
	return x.inv.mul(p.Sub(x.t))
}

// Translation returns the transform's world-space translation (zero for nil).
func (x *Transform) Translation() vec.V {
	if x == nil {
		return vec.V{}
	}
	return x.t
}

// YawRad returns the instance's rotation about +Y in radians (0 for nil/identity).
func (x *Transform) YawRad() float64 {
	if x == nil {
		return 0
	}
	return math.Atan2(x.fwd[2], x.fwd[0])
}

// GPUData returns the rows of the world→local rotation (inv) together with the
// world-space translation t, for uploading the transform to the GPU. A nil
// transform reports the identity. The shader maps a world point to local space
// as inv*(p - t) and a local normal back to world as inv^T * n (inv is
// orthonormal, so its transpose is the local→world rotation). Returning the
// inverse rows keeps the per-ray hot path a few dot products.
func (x *Transform) GPUData() (r0, r1, r2, t vec.V) {
	if x == nil {
		return vec.V{X: 1}, vec.V{Y: 1}, vec.V{Z: 1}, vec.V{}
	}
	return vec.V{X: x.inv[0], Y: x.inv[1], Z: x.inv[2]},
		vec.V{X: x.inv[3], Y: x.inv[4], Z: x.inv[5]},
		vec.V{X: x.inv[6], Y: x.inv[7], Z: x.inv[8]},
		x.t
}

// Compose returns the transform equivalent to applying inner first, then the
// receiver: result(p) = receiver(inner(p)). It is used when an already-rotated
// primitive inside an included sub-scene is placed by an outer instance
// transform. Either side may be nil (identity).
func (x *Transform) Compose(inner *Transform) *Transform {
	if x == nil {
		return inner
	}
	if inner == nil {
		return x
	}
	fwd := x.fwd.mulM(inner.fwd)
	return &Transform{
		fwd: fwd,
		inv: fwd.transpose(),
		t:   x.fwd.mul(inner.t).Add(x.t),
	}
}
