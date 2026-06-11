package scene

import (
	"math"

	"raytracer/internal/vec"
)

// Inf is returned by Intersect methods when a ray misses the primitive.
var Inf = math.Inf(1)

// Surface bundles the shading attributes shared by every primitive.
type Surface struct {
	Mat    int
	Albedo vec.V
	Rough  float64
	IOR    float64
	Tex    int // procedural texture id (0 = none); see package texture
	// Reflect (0..1) adds a mirror reflection on top of a diffuse/checker
	// surface without replacing its shading: the final color is a blend of the
	// normal (textured, lit) result and a reflected ray, weighted by Reflect.
	// 0 is a pure diffuse surface; 1 is (almost) a mirror. Rough blurs the
	// reflection just as it does for mirror/metal. Ignored by materials that are
	// already reflective/refractive (mirror, metal, glass, emit).
	Reflect float64
}

// Sphere is a simple analytic sphere.
type Sphere struct {
	Center vec.V
	Radius float64
	Surface
}

// Intersect returns the nearest positive hit distance, or Inf on a miss.
func (s *Sphere) Intersect(r vec.Ray) float64 {
	b := r.Origin.Sub(s.Center)
	bd := b.Dot(r.Dir)
	c := b.LenSq() - s.Radius*s.Radius
	disc := bd*bd - c
	if disc < 0 {
		return Inf
	}
	sq := math.Sqrt(disc)
	t := -bd - sq
	if t < eps {
		t = -bd + sq
	}
	if t < eps {
		return Inf
	}
	return t
}

// Normal returns the outward unit normal at surface point p.
func (s *Sphere) Normal(p vec.V) vec.V {
	return p.Sub(s.Center).Scale(1 / s.Radius)
}

// Plane is an infinite plane defined by n·x + D = 0. For checker materials a
// second albedo is used on alternating unit cells.
type Plane struct {
	N vec.V
	D float64
	Surface
	Albedo2 vec.V
}

// Intersect returns the nearest positive hit distance, or Inf on a miss.
func (p *Plane) Intersect(r vec.Ray) float64 {
	denom := r.Dir.Dot(p.N)
	if math.Abs(denom) < 1e-6 {
		return Inf
	}
	t := -(r.Origin.Dot(p.N) + p.D) / denom
	if t < eps {
		return Inf
	}
	return t
}

// AlbedoAt returns the albedo at point hit, honoring the checkerboard pattern.
func (p *Plane) AlbedoAt(hit vec.V) vec.V {
	if p.Mat == MatChecker {
		cx := math.Floor(hit.X + 0.5)
		cz := math.Floor(hit.Z + 0.5)
		if (int(cx)+int(cz))&1 != 0 {
			return p.Albedo2
		}
	}
	return p.Albedo
}

// Box is an axis-aligned bounding box.
type Box struct {
	Min vec.V
	Max vec.V
	Surface
}

// Intersect returns the nearest positive hit distance, or Inf on a miss.
func (b *Box) Intersect(r vec.Ray) float64 {
	invx, invy, invz := 1/r.Dir.X, 1/r.Dir.Y, 1/r.Dir.Z
	t1, t2 := (b.Min.X-r.Origin.X)*invx, (b.Max.X-r.Origin.X)*invx
	if t1 > t2 {
		t1, t2 = t2, t1
	}
	t3, t4 := (b.Min.Y-r.Origin.Y)*invy, (b.Max.Y-r.Origin.Y)*invy
	if t3 > t4 {
		t3, t4 = t4, t3
	}
	t5, t6 := (b.Min.Z-r.Origin.Z)*invz, (b.Max.Z-r.Origin.Z)*invz
	if t5 > t6 {
		t5, t6 = t6, t5
	}
	tmin := t1
	if t3 > tmin {
		tmin = t3
	}
	if t5 > tmin {
		tmin = t5
	}
	tmax := t2
	if t4 < tmax {
		tmax = t4
	}
	if t6 < tmax {
		tmax = t6
	}
	if tmax < tmin || tmax < eps {
		return Inf
	}
	if tmin < eps {
		return tmax
	}
	return tmin
}

// Normal returns the outward unit normal at surface point p.
func (b *Box) Normal(p vec.V) vec.V {
	c := b.Min.Add(b.Max).Scale(0.5)
	e := b.Max.Sub(b.Min).Scale(0.5)
	lx := math.Abs((p.X - c.X) / e.X)
	ly := math.Abs((p.Y - c.Y) / e.Y)
	lz := math.Abs((p.Z - c.Z) / e.Z)
	switch {
	case lx >= ly && lx >= lz:
		return vec.V{X: math.Copysign(1, p.X-c.X)}
	case ly >= lx && ly >= lz:
		return vec.V{Y: math.Copysign(1, p.Y-c.Y)}
	default:
		return vec.V{Z: math.Copysign(1, p.Z-c.Z)}
	}
}

// Cylinder is a finite Y-axis-aligned cylinder with flat caps.
type Cylinder struct {
	CX, CZ     float64
	Radius     float64
	YMin, YMax float64
	Surface
}

// Intersect returns the nearest positive hit distance, or Inf on a miss.
func (c *Cylinder) Intersect(r vec.Ray) float64 {
	ex, ez := r.Origin.X-c.CX, r.Origin.Z-c.CZ
	a := r.Dir.X*r.Dir.X + r.Dir.Z*r.Dir.Z
	if a < 1e-8 {
		return Inf
	}
	b := 2 * (ex*r.Dir.X + ez*r.Dir.Z)
	cc := ex*ex + ez*ez - c.Radius*c.Radius
	disc := b*b - 4*a*cc
	if disc < 0 {
		return Inf
	}
	sq := math.Sqrt(disc)
	t := (-b - sq) / (2 * a)
	if t < eps {
		t = (-b + sq) / (2 * a)
	}
	if t < eps {
		return Inf
	}
	hy := r.Origin.Y + r.Dir.Y*t
	if hy < c.YMin || hy > c.YMax {
		// Side missed within the vertical extent; try the end caps.
		tc := Inf
		if math.Abs(r.Dir.Y) > 1e-6 {
			if tb := c.capHit(r, c.YMin); tb < tc {
				tc = tb
			}
			if tt := c.capHit(r, c.YMax); tt < tc {
				tc = tt
			}
		}
		return tc
	}
	return t
}

func (c *Cylinder) capHit(r vec.Ray, y float64) float64 {
	t := (y - r.Origin.Y) / r.Dir.Y
	if t <= eps {
		return Inf
	}
	hx := r.Origin.X + r.Dir.X*t
	hz := r.Origin.Z + r.Dir.Z*t
	dd := (hx-c.CX)*(hx-c.CX) + (hz-c.CZ)*(hz-c.CZ)
	if dd <= c.Radius*c.Radius {
		return t
	}
	return Inf
}

// Normal returns the outward unit normal at surface point p for hit distance t.
func (c *Cylinder) Normal(p vec.V, r vec.Ray, t float64) vec.V {
	hy := r.Origin.Y + r.Dir.Y*t
	if hy <= c.YMin+1e-3 {
		return vec.V{Y: -1}
	}
	if hy >= c.YMax-1e-3 {
		return vec.V{Y: 1}
	}
	d := vec.V{X: p.X - c.CX, Z: p.Z - c.CZ}
	l := d.Len()
	if l == 0 {
		l = 1
	}
	return vec.V{X: d.X / l, Z: d.Z / l}
}

// Cone is a finite Y-axis-aligned cone with its tip at the top and a base cap.
type Cone struct {
	CX, CZ      float64
	YBase, YTip float64
	RBase       float64
	Surface
}

// Intersect returns the nearest positive hit distance, or Inf on a miss.
func (c *Cone) Intersect(r vec.Ray) float64 {
	h := c.YTip - c.YBase
	k := c.RBase / h // radius per unit height below the tip
	ey := r.Origin.Y - c.YTip
	dx, dy, dz := r.Dir.X, r.Dir.Y, r.Dir.Z
	ox, oz := r.Origin.X-c.CX, r.Origin.Z-c.CZ
	a := dx*dx + dz*dz - dy*dy*k*k
	b := ox*dx + oz*dz - ey*dy*k*k
	cc := ox*ox + oz*oz - ey*ey*k*k
	disc := b*b - a*cc
	if disc < 0 {
		return Inf
	}
	sq := math.Sqrt(disc)
	t := (-b - sq) / a
	hy := r.Origin.Y + dy*t
	if t < eps || hy < c.YBase || hy > c.YTip {
		t = (-b + sq) / a
		hy2 := r.Origin.Y + dy*t
		if t < eps || hy2 < c.YBase || hy2 > c.YTip {
			// Fall back to the base disk cap.
			if math.Abs(dy) < 1e-6 {
				return Inf
			}
			tc := (c.YBase - r.Origin.Y) / dy
			if tc < eps {
				return Inf
			}
			hx := r.Origin.X + dx*tc
			hz := r.Origin.Z + dz*tc
			dd := (hx-c.CX)*(hx-c.CX) + (hz-c.CZ)*(hz-c.CZ)
			if dd <= c.RBase*c.RBase {
				return tc
			}
			return Inf
		}
	}
	return t
}

// Normal returns the outward unit normal at surface point p for hit distance t.
func (c *Cone) Normal(p vec.V, r vec.Ray, t float64) vec.V {
	hy := r.Origin.Y + r.Dir.Y*t
	if math.Abs(hy-c.YBase) < 0.01 {
		return vec.V{Y: -1}
	}
	h := c.YTip - c.YBase
	k := c.RBase / h
	lx, lz := p.X-c.CX, p.Z-c.CZ
	lr := math.Hypot(lx, lz)
	if lr == 0 {
		lr = 1e-9
	}
	ny := k / math.Sqrt(1+k*k)
	ns := 1 / math.Sqrt(1+k*k)
	return vec.V{X: lx / lr * ns, Y: ny, Z: lz / lr * ns}
}

// Torus is a Y-axis-aligned torus with major radius R and minor radius r.
type Torus struct {
	Center vec.V
	R      float64 // major radius
	Rm     float64 // minor radius
	Surface
}

// Intersect solves the quartic numerically by scanning for a sign change and
// bisecting, matching the original renderer's robust-but-cheap approach.
func (tr *Torus) Intersect(r vec.Ray) float64 {
	e := r.Origin.Sub(tr.Center)
	dd := r.Dir.LenSq()
	ed := e.Dot(r.Dir)
	ee := e.LenSq()

	// Bounding-sphere reject: the torus fits inside a sphere of radius R+Rm.
	// This skips the costly quartic solve for the rays that miss it entirely.
	rad := tr.R + tr.Rm
	if ed*ed-dd*(ee-rad*rad) < 0 {
		return Inf
	}

	R2 := tr.R * tr.R
	r2 := tr.Rm * tr.Rm
	a4 := dd * dd
	a3 := 4 * dd * ed
	a2 := 2*dd*(ee-r2-R2) + 4*ed*ed + 4*R2*r.Dir.Y*r.Dir.Y
	a1 := 4*ed*(ee-r2-R2) + 8*R2*e.Y*r.Dir.Y
	a0 := (ee-r2-R2)*(ee-r2-R2) - 4*R2*(r2-e.Y*e.Y)

	poly := func(t float64) float64 {
		return ((a4*t+a3)*t+a2)*t*t + a1*t + a0
	}

	const steps = 64
	const tmax = 12.0
	prev := poly(eps)
	for i := 1; i <= steps; i++ {
		t := eps + float64(i)*(tmax/steps)
		v := poly(t)
		if prev*v < 0 {
			lo, hi := t-tmax/steps, t
			for j := 0; j < 16; j++ {
				m := (lo + hi) * 0.5
				if poly(m)*prev < 0 {
					hi = m
				} else {
					lo = m
				}
			}
			tr2 := (lo + hi) * 0.5
			if tr2 > eps {
				return tr2
			}
		}
		prev = v
	}
	return Inf
}

// Normal returns the outward unit normal at surface point p.
func (tr *Torus) Normal(p vec.V) vec.V {
	e := p.Sub(tr.Center)
	l := math.Hypot(e.X, e.Z)
	if l == 0 {
		l = 1e-9
	}
	c := vec.V{X: e.X / l * tr.R, Z: e.Z / l * tr.R}
	return e.Sub(c).Normalize()
}

// Light is a point light with per-channel intensity. Radius is informational
// (kept for parity with the source scene). Range, when > 0, is the distance at
// which the light's contribution is forced to zero: beyond it the renderer
// skips the light entirely (including its shadow ray), and within it the
// falloff is smoothly windowed down to zero at Range. Range == 0 means "auto":
// the light reaches as far as it meaningfully contributes.
type Light struct {
	Pos    vec.V
	Color  vec.V
	Radius float64
	Range  float64
}
