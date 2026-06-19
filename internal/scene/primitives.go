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
	// Transmit (0..1) is the glass transparency: 0 is opaque, 1 is fully
	// transparent. The glass tint comes from Albedo. Ignored by other materials.
	Transmit float64
	// Xform, when non-nil, maps the primitive from local space into world space.
	// Intersection and normals are evaluated in local space and transformed back.
	Xform *Transform
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

// AABB is an axis-aligned box region, used to carve rectangular openings out of
// a Box (see Box.Holes).
type AABB struct {
	Min vec.V
	Max vec.V
}

// Box is an axis-aligned bounding box. When Holes is non-empty each listed AABB
// is subtracted from the box (constructive solid geometry), producing genuine
// see-through openings: windows, doorways, etc. The holes are expressed in the
// box's own (untransformed) coordinates and should poke fully through whatever
// faces they pierce.
type Box struct {
	Min   vec.V
	Max   vec.V
	Holes []AABB
	Surface
}

// WorldBounds returns the box's axis-aligned bounds in world space. For an
// untransformed box this is just (Min, Max); for a rotated/translated box it is
// the AABB enclosing the eight transformed corners. Holes only shrink the solid,
// so they never enlarge the bounds. Used by collision, which works in world
// space.
func (b *Box) WorldBounds() (vec.V, vec.V) {
	if b.Xform == nil {
		return b.Min, b.Max
	}
	wmin := vec.V{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)}
	wmax := vec.V{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}
	for _, dx := range [2]float64{0, 1} {
		for _, dy := range [2]float64{0, 1} {
			for _, dz := range [2]float64{0, 1} {
				c := b.Xform.ToWorld(vec.V{
					X: b.Min.X + dx*(b.Max.X-b.Min.X),
					Y: b.Min.Y + dy*(b.Max.Y-b.Min.Y),
					Z: b.Min.Z + dz*(b.Max.Z-b.Min.Z),
				})
				wmin = vec.V{X: math.Min(wmin.X, c.X), Y: math.Min(wmin.Y, c.Y), Z: math.Min(wmin.Z, c.Z)}
				wmax = vec.V{X: math.Max(wmax.X, c.X), Y: math.Max(wmax.Y, c.Y), Z: math.Max(wmax.Z, c.Z)}
			}
		}
	}
	return wmin, wmax
}

// TopOpenAt reports whether a hole breaches the box's top face at world (x,z),
// so the box offers no standing surface there and the player falls through (e.g.
// a stairwell opening cut into an upper floor slab). Holes are authored in the
// box's local coordinates, so the query point is mapped into the box frame; the
// hole must reach the top face to count (a shallow recess that stops short of
// the top still leaves a floor to stand on).
func (b *Box) TopOpenAt(x, z float64) bool {
	if len(b.Holes) == 0 {
		return false
	}
	p := b.Xform.ToLocal(vec.V{X: x, Y: b.Max.Y, Z: z})
	const eps = 1e-4
	for i := range b.Holes {
		h := b.Holes[i]
		if h.Max.Y < b.Max.Y-eps {
			continue // hole does not reach the top face
		}
		if p.X > h.Min.X && p.X < h.Max.X && p.Z > h.Min.Z && p.Z < h.Max.Z {
			return true
		}
	}
	return false
}

// PassableThroughHole reports whether a player standing at world (x,z), with its
// blocking body spanning the vertical band [bandLo, headY] and the given radius,
// is lined up with an opening (one of Box.Holes) that pierces this box — a
// doorway or low window the player can walk through. Holes are authored in the
// box's local coordinates, so the query column is mapped into the box frame
// first; rotation is orthonormal, so the world radius carries over unchanged.
//
// An opening qualifies when (1) it spans the player's whole vertical band (no
// solid lintel or sill in the way) and (2) the player's footprint clears the
// opening's side jambs by `radius` along the wall's wide horizontal axis. The
// thin horizontal axis is the direction of travel through the wall, where the
// hole pokes fully through, so no radius margin is required there.
func (b *Box) PassableThroughHole(x, z, bandLo, headY, radius float64) bool {
	if len(b.Holes) == 0 {
		return false
	}
	lo := b.Xform.ToLocal(vec.V{X: x, Y: bandLo, Z: z})
	hi := b.Xform.ToLocal(vec.V{X: x, Y: headY, Z: z})
	yLo := math.Min(lo.Y, hi.Y)
	yHi := math.Max(lo.Y, hi.Y)
	// The wall is thin along one horizontal axis; that axis is the travel
	// direction through the opening, the other is the opening's width.
	travelAlongX := (b.Max.X - b.Min.X) <= (b.Max.Z - b.Min.Z)
	const eps = 1e-4
	for i := range b.Holes {
		h := b.Holes[i]
		if h.Min.Y > yLo+eps || h.Max.Y < yHi-eps {
			continue // a sill or lintel still crosses the player's body
		}
		// Along the travel (thin) axis the opening pierces the whole wall, so the
		// span is widened by `radius`: the world-space AABB collision test also
		// pads the wall by `radius`, and without the same pad here the player
		// would be stopped a radius short of the opening and never reach it.
		if travelAlongX {
			if lo.X < h.Min.X-radius-eps || lo.X > h.Max.X+radius+eps {
				continue
			}
			if lo.Z > h.Min.Z+radius && lo.Z < h.Max.Z-radius {
				return true
			}
		} else {
			if lo.Z < h.Min.Z-radius-eps || lo.Z > h.Max.Z+radius+eps {
				continue
			}
			if lo.X > h.Min.X+radius && lo.X < h.Max.X-radius {
				return true
			}
		}
	}
	return false
}

// slabInterval returns the [tmin, tmax] parametric span over which the ray is
// inside the AABB [min, max], or ok=false on a miss. tmax may be negative when
// the box is entirely behind the origin.
func slabInterval(min, max vec.V, r vec.Ray) (tmin, tmax float64, ok bool) {
	invx, invy, invz := 1/r.Dir.X, 1/r.Dir.Y, 1/r.Dir.Z
	t1, t2 := (min.X-r.Origin.X)*invx, (max.X-r.Origin.X)*invx
	if t1 > t2 {
		t1, t2 = t2, t1
	}
	t3, t4 := (min.Y-r.Origin.Y)*invy, (max.Y-r.Origin.Y)*invy
	if t3 > t4 {
		t3, t4 = t4, t3
	}
	t5, t6 := (min.Z-r.Origin.Z)*invz, (max.Z-r.Origin.Z)*invz
	if t5 > t6 {
		t5, t6 = t6, t5
	}
	tmin = t1
	if t3 > tmin {
		tmin = t3
	}
	if t5 > tmin {
		tmin = t5
	}
	tmax = t2
	if t4 < tmax {
		tmax = t4
	}
	if t6 < tmax {
		tmax = t6
	}
	if tmax < tmin {
		return 0, 0, false
	}
	return tmin, tmax, true
}

// Intersect returns the nearest positive hit distance, or Inf on a miss.
func (b *Box) Intersect(r vec.Ray) float64 {
	tmin, tmax, ok := slabInterval(b.Min, b.Max, r)
	if !ok || tmax < eps {
		return Inf
	}
	if len(b.Holes) == 0 {
		if tmin < eps {
			return tmax
		}
		return tmin
	}

	// CSG difference: the solid is [tmin, tmax] with each hole's span removed.
	// Walk the disjoint solid segments and return the nearest boundary >= eps.
	// segs holds up to a few [lo, hi] spans; holes rarely overlap so this stays
	// tiny in practice.
	type span struct{ lo, hi float64 }
	segs := [8]span{{tmin, tmax}}
	n := 1
	for hi := range b.Holes {
		h0, h1, hok := slabInterval(b.Holes[hi].Min, b.Holes[hi].Max, r)
		if !hok || h1 <= h0 {
			continue
		}
		m := 0
		var next [8]span
		for i := 0; i < n; i++ {
			s := segs[i]
			if h1 <= s.lo || h0 >= s.hi { // no overlap
				next[m] = s
				m++
				continue
			}
			if h0 > s.lo && m < len(next) { // solid part before the hole
				next[m] = span{s.lo, h0}
				m++
			}
			if h1 < s.hi && m < len(next) { // solid part after the hole
				next[m] = span{h1, s.hi}
				m++
			}
		}
		segs = next
		n = m
	}

	best := Inf
	for i := 0; i < n; i++ {
		s := segs[i]
		switch {
		case s.lo >= eps:
			if s.lo < best {
				best = s.lo
			}
		case s.hi >= eps: // origin sits inside this solid segment
			if s.hi < best {
				best = s.hi
			}
		}
	}
	return best
}

// faceAxis returns the outward axis normal of the [min,max] box face nearest to
// point p, plus the distance to that face.
func faceAxis(min, max, p vec.V) (vec.V, float64) {
	dxl, dxh := math.Abs(p.X-min.X), math.Abs(p.X-max.X)
	dyl, dyh := math.Abs(p.Y-min.Y), math.Abs(p.Y-max.Y)
	dzl, dzh := math.Abs(p.Z-min.Z), math.Abs(p.Z-max.Z)
	n := vec.V{X: -1}
	d := dxl
	if dxh < d {
		n, d = vec.V{X: 1}, dxh
	}
	if dyl < d {
		n, d = vec.V{Y: -1}, dyl
	}
	if dyh < d {
		n, d = vec.V{Y: 1}, dyh
	}
	if dzl < d {
		n, d = vec.V{Z: -1}, dzl
	}
	if dzh < d {
		n, d = vec.V{Z: 1}, dzh
	}
	return n, d
}

// Normal returns the outward unit normal at surface point p. For a holed box the
// point may lie on the inner face of a cutout, whose outward normal points into
// the opening (the negative of the hole's own face normal).
func (b *Box) Normal(p vec.V) vec.V {
	bestN, bestD := faceAxis(b.Min, b.Max, p)
	for i := range b.Holes {
		if n, d := faceAxis(b.Holes[i].Min, b.Holes[i].Max, p); d < bestD {
			bestN, bestD = n.Neg(), d
		}
	}
	return bestN
}

// Cylinder is a finite Y-axis-aligned cylinder with flat caps. Radius is the
// radius at YMin; RadiusTop is the radius at YMax (0 means same as Radius).
type Cylinder struct {
	CX, CZ         float64
	Radius         float64
	RadiusTop      float64
	YMin, YMax     float64
	Surface
}

func (c *Cylinder) radiusTop() float64 {
	if c.RadiusTop == 0 {
		return c.Radius
	}
	return c.RadiusTop
}

func (c *Cylinder) radiusAt(y float64) float64 {
	h := c.YMax - c.YMin
	if h <= 0 {
		return c.Radius
	}
	t := (y - c.YMin) / h
	return c.Radius + (c.radiusTop()-c.Radius)*t
}

func (c *Cylinder) MaxRadius() float64 {
	rt := c.radiusTop()
	if rt > c.Radius {
		return rt
	}
	return c.Radius
}

// Intersect returns the nearest positive hit distance, or Inf on a miss.
func (c *Cylinder) Intersect(r vec.Ray) float64 {
	h := c.YMax - c.YMin
	if h <= 0 {
		return Inf
	}
	r0, r1 := c.Radius, c.radiusTop()
	alpha := (r1 - r0) / h

	px, pz := r.Origin.X-c.CX, r.Origin.Z-c.CZ
	dx, dy, dz := r.Dir.X, r.Dir.Y, r.Dir.Z
	A := r0 + alpha*(r.Origin.Y-c.YMin)
	B := alpha * dy

	a := dx*dx + dz*dz - B*B
	b := 2 * (px*dx+pz*dz - A*B)
	cc := px*px + pz*pz - A*A

	best := Inf
	if math.Abs(a) > 1e-12 {
		disc := b*b - 4*a*cc
		if disc >= 0 {
			sq := math.Sqrt(disc)
			for _, t := range []float64{(-b - sq) / (2 * a), (-b + sq) / (2 * a)} {
				if t < eps {
					continue
				}
				hy := r.Origin.Y + dy*t
				if hy >= c.YMin && hy <= c.YMax && t < best {
					best = t
				}
			}
		}
	}

	if math.Abs(dy) > 1e-6 {
		if tb := c.capHit(r, c.YMin, r0); tb < best {
			best = tb
		}
		if tt := c.capHit(r, c.YMax, r1); tt < best {
			best = tt
		}
	}
	return best
}

func (c *Cylinder) capHit(r vec.Ray, y, capR float64) float64 {
	t := (y - r.Origin.Y) / r.Dir.Y
	if t <= eps {
		return Inf
	}
	hx := r.Origin.X + r.Dir.X*t
	hz := r.Origin.Z + r.Dir.Z*t
	dd := (hx-c.CX)*(hx-c.CX) + (hz-c.CZ)*(hz-c.CZ)
	if dd <= capR*capR {
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
	h := c.YMax - c.YMin
	alpha := (c.radiusTop() - c.Radius) / h
	ry := c.radiusAt(hy)
	dx, dz := p.X-c.CX, p.Z-c.CZ
	return vec.V{X: dx, Y: -ry * alpha, Z: dz}.Normalize()
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
