package scene

import (
	"math"

	"raytracer/internal/vec"
)

// Inf is returned by Intersect methods when a ray misses the primitive.
var Inf = math.Inf(1)

// Surface bundles the shading attributes shared by every primitive.
type Surface struct {
	Mat     int
	Albedo  vec.V
	Albedo2 vec.V // second checker color; ignored for other materials
	Rough   float64
	IOR     float64
	Tex     int // procedural texture id (0 = none); see package texture
	// Reflect (0..1) adds a mirror reflection on top of a diffuse/checker
	// surface without replacing its shading: the final color is a blend of the
	// normal (textured, lit) result and a reflected ray, weighted by Reflect.
	// 0 is a pure diffuse surface; 1 is (almost) a mirror. Rough blurs the
	// reflection just as it does for mirror/metal. Ignored by materials that are
	// already reflective/refractive (mirror, metal, glass, emit).
	Reflect float64
	// Specular (0..1) adds Blinn–Phong highlights from point lights on diffuse/checker
	// surfaces. Shininess is the Phong exponent (typical 8–128); defaults to 32 when
	// Specular > 0 and Shininess is omitted/zero. Ignored by mirror/metal/glass/emit.
	Specular  float64
	Shininess float64
	// Transmit (0..1) is the glass transparency: 0 is opaque, 1 is fully
	// transparent. The glass tint comes from Albedo. Ignored by other materials.
	Transmit float64
	// Thin marks glass as a single sheet: one transmission through the slab
	// (no separate enter/exit refraction). Defaults to true for glass at load
	// time; set thin = false in TOML for thick glass. Ignored by non-glass.
	Thin bool
	// TexU and TexV are texture-specific parameters (tile width/height for tiles).
	TexU float64
	TexV float64
	// TwoPane opts into the original thick-glass path: separate enter/exit trace
	// hits at both faces (full dual-pane refraction). Default glass is thin with
	// a ghost second-pane reflection instead. Ignored by non-glass materials.
	TwoPane bool
	// NoCollision opts the primitive out of player capsule tests (walking,
	// ground height, footsteps). Ray tracing is unaffected. Defaults to false.
	NoCollision bool
	// Xform, when non-nil, maps the primitive from local space into world space.
	// Intersection and normals are evaluated in local space and transformed back.
	Xform *Transform
}

// Collides reports whether the primitive participates in player capsule tests.
func (s Surface) Collides() bool { return !s.NoCollision }

// Sphere is a simple analytic sphere. CutOff (0..1) removes the bottom
// portion: cut_off = 0.5 keeps the top hemisphere with an open bottom.
type Sphere struct {
	Center vec.V
	Radius float64
	CutOff float64 // fraction of height at which the bottom is removed; 0 = full sphere
	Surface
}

// cutPlaneY returns the local Y of the horizontal cut plane. With CutOff == 0
// this is the sphere bottom (no geometry removed).
func (s *Sphere) cutPlaneY() float64 {
	if s.CutOff <= 0 {
		return s.Center.Y - s.Radius
	}
	if s.CutOff >= 1 {
		return s.Center.Y + s.Radius
	}
	return s.Center.Y - s.Radius + s.CutOff*(2*s.Radius)
}

// LocalBounds returns the sphere's axis-aligned bounds in local space,
// accounting for CutOff.
func (s *Sphere) LocalBounds() (vec.V, vec.V) {
	r := s.Radius
	ymin := s.cutPlaneY()
	if s.CutOff <= 0 {
		ymin = s.Center.Y - r
	}
	return vec.V{X: s.Center.X - r, Y: ymin, Z: s.Center.Z - r},
		vec.V{X: s.Center.X + r, Y: s.Center.Y + r, Z: s.Center.Z + r}
}

// WorldBounds returns the sphere's axis-aligned bounds in world space.
func (s *Sphere) WorldBounds() (vec.V, vec.V) {
	lmin, lmax := s.LocalBounds()
	return expandXformBounds(s.Xform, lmin, lmax)
}

// Intersect returns the nearest positive hit distance, or Inf on a miss.
func (s *Sphere) Intersect(r vec.Ray) float64 {
	b := r.Origin.Sub(s.Center)
	bd := b.Dot(r.Dir)
	c := b.LenSq() - s.Radius*s.Radius
	disc := bd*bd - c
	if disc < 0 && s.CutOff <= 0 {
		return Inf
	}
	best := Inf
	if disc >= 0 {
		sq := math.Sqrt(disc)
		for _, t := range []float64{-bd - sq, -bd + sq} {
			if t < eps {
				continue
			}
			if s.CutOff > 0 {
				hy := r.Origin.Y + r.Dir.Y*t
				if hy < s.cutPlaneY()-1e-6 {
					continue
				}
			}
			if t < best {
				best = t
			}
		}
	}
	return best
}

// Normal returns the outward unit normal at surface point p.
func (s *Sphere) Normal(p vec.V) vec.V {
	return p.Sub(s.Center).Scale(1 / s.Radius)
}

// Plane is an infinite plane defined by n·x + D = 0.
type Plane struct {
	N vec.V
	D float64
	Surface
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
	// FaceTex holds optional per-face texture ids (+X, -X, +Y, -Y, +Z, -Z).
	// Zero means no texture on that face.
	FaceTex [6]int
}

// expandXformBounds maps a local AABB through xf and returns its world-space
// enclosing AABB. A nil transform is treated as identity.
func expandXformBounds(xf *Transform, lmin, lmax vec.V) (vec.V, vec.V) {
	if xf == nil {
		return lmin, lmax
	}
	wmin := vec.V{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)}
	wmax := vec.V{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}
	for _, dx := range [2]float64{0, 1} {
		for _, dy := range [2]float64{0, 1} {
			for _, dz := range [2]float64{0, 1} {
				c := xf.ToWorld(vec.V{
					X: lmin.X + dx*(lmax.X-lmin.X),
					Y: lmin.Y + dy*(lmax.Y-lmin.Y),
					Z: lmin.Z + dz*(lmax.Z-lmin.Z),
				})
				wmin = vec.V{X: math.Min(wmin.X, c.X), Y: math.Min(wmin.Y, c.Y), Z: math.Min(wmin.Z, c.Z)}
				wmax = vec.V{X: math.Max(wmax.X, c.X), Y: math.Max(wmax.Y, c.Y), Z: math.Max(wmax.Z, c.Z)}
			}
		}
	}
	return wmin, wmax
}

// WorldBounds returns the box's axis-aligned bounds in world space. For an
// untransformed box this is just (Min, Max); for a rotated/translated box it is
// the AABB enclosing the eight transformed corners. Holes only shrink the solid,
// so they never enlarge the bounds. Used by collision, which works in world
// space.
func (b *Box) WorldBounds() (vec.V, vec.V) {
	return expandXformBounds(b.Xform, b.Min, b.Max)
}

// SolidFragments decomposes the box minus its holes into disjoint axis-aligned
// solid pieces in local space. A box without holes returns a single fragment.
func (b *Box) SolidFragments() []AABB {
	if len(b.Holes) == 0 {
		return []AABB{{Min: b.Min, Max: b.Max}}
	}
	solids := []AABB{{Min: b.Min, Max: b.Max}}
	for _, hole := range b.Holes {
		var next []AABB
		for _, s := range solids {
			next = append(next, subtractAABB(s, hole)...)
		}
		solids = next
	}
	const eps = 1e-6
	out := solids[:0]
	for _, s := range solids {
		if s.Min.X+eps >= s.Max.X || s.Min.Y+eps >= s.Max.Y || s.Min.Z+eps >= s.Max.Z {
			continue
		}
		out = append(out, s)
	}
	return out
}

// subtractAABB returns up to six axis-aligned boxes covering a \ b.
func subtractAABB(a, hole AABB) []AABB {
	inter, ok := aabbIntersect(a, hole)
	if !ok {
		return []AABB{a}
	}
	const eps = 1e-6
	var out []AABB
	add := func(mn, mx vec.V) {
		if mn.X+eps < mx.X && mn.Y+eps < mx.Y && mn.Z+eps < mx.Z {
			out = append(out, AABB{Min: mn, Max: mx})
		}
	}
	if a.Min.X < inter.Min.X {
		add(vec.New(a.Min.X, a.Min.Y, a.Min.Z), vec.New(inter.Min.X, a.Max.Y, a.Max.Z))
	}
	if inter.Max.X < a.Max.X {
		add(vec.New(inter.Max.X, a.Min.Y, a.Min.Z), vec.New(a.Max.X, a.Max.Y, a.Max.Z))
	}
	if a.Min.Y < inter.Min.Y {
		add(vec.New(inter.Min.X, a.Min.Y, a.Min.Z), vec.New(inter.Max.X, inter.Min.Y, a.Max.Z))
	}
	if inter.Max.Y < a.Max.Y {
		add(vec.New(inter.Min.X, inter.Max.Y, a.Min.Z), vec.New(inter.Max.X, a.Max.Y, a.Max.Z))
	}
	if a.Min.Z < inter.Min.Z {
		add(vec.New(inter.Min.X, inter.Min.Y, a.Min.Z), vec.New(inter.Max.X, inter.Max.Y, inter.Min.Z))
	}
	if inter.Max.Z < a.Max.Z {
		add(vec.New(inter.Min.X, inter.Min.Y, inter.Max.Z), vec.New(inter.Max.X, inter.Max.Y, a.Max.Z))
	}
	return out
}

func aabbIntersect(a, b AABB) (AABB, bool) {
	mn := vec.V{
		X: math.Max(a.Min.X, b.Min.X),
		Y: math.Max(a.Min.Y, b.Min.Y),
		Z: math.Max(a.Min.Z, b.Min.Z),
	}
	mx := vec.V{
		X: math.Min(a.Max.X, b.Max.X),
		Y: math.Min(a.Max.Y, b.Max.Y),
		Z: math.Min(a.Max.Z, b.Max.Z),
	}
	if mn.X >= mx.X || mn.Y >= mx.Y || mn.Z >= mx.Z {
		return AABB{}, false
	}
	return AABB{Min: mn, Max: mx}, true
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
	wy := b.Max.Y
	if b.Xform != nil {
		var ok bool
		wy, ok = b.Xform.WorldYForLocalY(x, z, b.Max.Y)
		if !ok {
			return false
		}
	}
	p := b.Xform.ToLocal(vec.V{X: x, Y: wy, Z: z})
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

// localPointInsideSolid reports whether a local-space point (with the given
// horizontal clearance radius) lies inside the box's solid volume. Holes are
// not subtracted here; PassableThroughHole handles doorway/window passage.
func (b *Box) localPointInsideSolid(p vec.V, radius float64) bool {
	const eps = 1e-4
	return p.X >= b.Min.X-radius-eps && p.X <= b.Max.X+radius+eps &&
		p.Y >= b.Min.Y-eps && p.Y <= b.Max.Y+eps &&
		p.Z >= b.Min.Z-radius-eps && p.Z <= b.Max.Z+radius+eps
}

// worldYOnLocalY maps a point on a horizontal plane at local Y to world Y at
// the column (wx, wz).
func (b *Box) worldYOnLocalY(wx, wz, localY float64) (float64, bool) {
	if b.Xform == nil {
		return localY, true
	}
	return b.Xform.WorldYForLocalY(wx, wz, localY)
}

// isFloorConnectedStep reports whether this box is a ground-anchored tread the
// player can walk onto in one stride (stairs), as opposed to a floating slab.
func (b *Box) isFloorConnectedStep(wx, wz, feetY, step float64) bool {
	const floorEps = 0.05
	if b.Min.Y > floorEps {
		return false
	}
	wy, ok := b.worldYOnLocalY(wx, wz, b.Max.Y)
	if !ok {
		return false
	}
	return wy <= feetY+step
}

// boxBlocksPlayer reports whether the player's capsule at (wx,wz) intersects
// this box in world space. Floor-connected steps are excluded so stairs remain
// walkable; floating slabs such as furniture shelves still block passage.
// Evaluated in the box's local frame so instance rotation is honored.
func (b *Box) boxBlocksPlayer(wx, wz, feetY, headY, playerR, step float64) bool {
	if b.isFloorConnectedStep(wx, wz, feetY, step) {
		return false
	}
	if b.blocksColumn(wx, wz, feetY, headY, playerR) {
		return true
	}
	// Thin slabs can fall between the few blocksColumn sample heights; probe the
	// oriented top and bottom faces on the player column.
	for _, ly := range []float64{b.Min.Y, b.Max.Y} {
		wy, ok := b.worldYOnLocalY(wx, wz, ly)
		if !ok || wy < feetY || wy > headY {
			continue
		}
		p := vec.V{X: wx, Y: wy, Z: wz}
		if b.Xform != nil {
			p = b.Xform.ToLocal(p)
		}
		if b.localPointInsideSolid(p, playerR) {
			return true
		}
	}
	return false
}

// PlayerOverlapsBox reports whether a vertical player capsule overlaps the box
// at world (x,z) between feetY and headY.
func (b *Box) PlayerOverlapsBox(x, z, feetY, headY, radius, step float64) bool {
	walkTop := feetY + step
	mn, mx := b.WorldBounds()
	if mx.Y <= walkTop || mn.Y >= headY {
		return false
	}
	if x <= mn.X-radius || x >= mx.X+radius || z <= mn.Z-radius || z >= mx.Z+radius {
		return false
	}
	return b.blocksColumn(x, z, walkTop, headY, radius)
}

// blocksColumn reports whether a player footprint at world (wx,wz) with vertical
// extent [bandLo,bandHi] and the given radius intersects the box's solid volume.
// The query is evaluated in the box's local frame so rotation is honored.
func (b *Box) blocksColumn(wx, wz, bandLo, bandHi, playerR float64) bool {
	for _, wy := range []float64{bandLo, bandHi, (bandLo + bandHi) * 0.5} {
		p := vec.V{X: wx, Y: wy, Z: wz}
		if b.Xform != nil {
			p = b.Xform.ToLocal(p)
		}
		if b.localPointInsideSolid(p, playerR) {
			return true
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
	CX, CZ     float64
	Radius     float64
	RadiusTop  float64
	YMin, YMax float64
	// OpenMin/OpenMax omit the flat end cap at YMin/YMax so the tube is hollow.
	OpenMin, OpenMax bool
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

// WorldBounds returns the cylinder's axis-aligned bounds in world space.
func (c *Cylinder) WorldBounds() (vec.V, vec.V) {
	r := c.MaxRadius()
	return expandXformBounds(c.Xform,
		vec.V{X: c.CX - r, Y: c.YMin, Z: c.CZ - r},
		vec.V{X: c.CX + r, Y: c.YMax, Z: c.CZ + r})
}

// minWalkableCapUpY is the minimum |world Y| of a cap's upward-facing normal
// for the cap to count as walkable (≈60° max slope).
const minWalkableCapUpY = 0.5

// worldYOnCap returns the world Y of a flat cap at local y=localY under (wx,wz),
// or ok=false when the column misses the cap disk or the cap is too steep.
func (c *Cylinder) worldYOnCap(wx, wz, localY, capR float64, standLocal vec.V) (wy float64, ok bool) {
	if capR <= 0 {
		return 0, false
	}
	n := standLocal
	if c.Xform != nil {
		n = c.Xform.WorldNormal(n)
	}
	if math.Abs(n.Y) < minWalkableCapUpY {
		return 0, false
	}
	if c.Xform == nil {
		dx, dz := wx-c.CX, wz-c.CZ
		if dx*dx+dz*dz > capR*capR {
			return 0, false
		}
		return localY, true
	}
	wy, ok = c.Xform.WorldYForLocalY(wx, wz, localY)
	if !ok {
		return 0, false
	}
	lp := c.Xform.ToLocal(vec.V{X: wx, Y: wy, Z: wz})
	if math.Abs(lp.Y-localY) > 0.05 {
		return 0, false
	}
	dx, dz := lp.X-c.CX, lp.Z-c.CZ
	if dx*dx+dz*dz > capR*capR {
		return 0, false
	}
	return wy, true
}

// capGroundHeight returns the highest walkable cap height at (wx,wz) at or below headY.
func (c *Cylinder) capGroundHeight(wx, wz, headY float64) (float64, bool) {
	best := math.Inf(-1)
	found := false
	try := func(localY, capR float64, open bool) {
		if open {
			return
		}
		for _, stand := range []vec.V{{Y: 1}, {Y: -1}} {
			wy, ok := c.worldYOnCap(wx, wz, localY, capR, stand)
			if !ok || wy > headY || wy <= best {
				continue
			}
			best, found = wy, true
		}
	}
	try(c.YMax, c.radiusTop(), c.OpenMax)
	try(c.YMin, c.Radius, c.OpenMin)
	if !found {
		return 0, false
	}
	return best, true
}

func (c *Cylinder) standingOnCap(wx, wz, bandLo, playerR float64) bool {
	wy, ok := c.capGroundHeight(wx, wz, bandLo+10)
	if !ok || bandLo < wy-0.08 || bandLo > wy+0.55 {
		return false
	}
	return (!c.OpenMax && c.onCapDisk(wx, wz, wy, c.YMax, c.radiusTop(), playerR)) ||
		(!c.OpenMin && c.onCapDisk(wx, wz, wy, c.YMin, c.Radius, playerR))
}

func (c *Cylinder) onCapDisk(wx, wz, worldY, localY, capR, playerR float64) bool {
	p := vec.V{X: wx, Y: worldY, Z: wz}
	if c.Xform != nil {
		p = c.Xform.ToLocal(p)
	}
	if math.Abs(p.Y-localY) > 0.05 {
		return false
	}
	rr := capR - playerR
	if rr < 0 {
		rr = 0
	}
	dx, dz := p.X-c.CX, p.Z-c.CZ
	return dx*dx+dz*dz <= rr*rr+1e-6
}

// blocksColumn reports whether a player footprint at world (wx,wz) with vertical
// extent [bandLo,bandHi] and the given radius intersects the cylinder. Flat caps
// are walkable and do not block when the player is standing on them.
func (c *Cylinder) blocksColumn(wx, wz, bandLo, bandHi, playerR float64) bool {
	if c.standingOnCap(wx, wz, bandLo, playerR) {
		return false
	}
	for _, wy := range []float64{bandLo, bandHi, (bandLo + bandHi) * 0.5} {
		p := vec.V{X: wx, Y: wy, Z: wz}
		if c.Xform != nil {
			p = c.Xform.ToLocal(p)
		}
		if p.Y < c.YMin || p.Y > c.YMax {
			continue
		}
		if !c.OpenMin && math.Abs(p.Y-c.YMin) < 0.01 {
			dx, dz := p.X-c.CX, p.Z-c.CZ
			if dx*dx+dz*dz <= c.Radius*c.Radius {
				continue
			}
		}
		if !c.OpenMax && math.Abs(p.Y-c.YMax) < 0.01 {
			dx, dz := p.X-c.CX, p.Z-c.CZ
			rt := c.radiusTop()
			if dx*dx+dz*dz <= rt*rt {
				continue
			}
		}
		rr := c.radiusAt(p.Y) + playerR
		dx, dz := p.X-c.CX, p.Z-c.CZ
		if dx*dx+dz*dz < rr*rr {
			return true
		}
	}
	return false
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
	b := 2 * (px*dx + pz*dz - A*B)
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
		if !c.OpenMin {
			if tb := c.capHit(r, c.YMin, r0); tb < best {
				best = tb
			}
		}
		if !c.OpenMax {
			if tt := c.capHit(r, c.YMax, r1); tt < best {
				best = tt
			}
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

// Cone is a finite Y-axis-aligned cone with its tip at the top. When Capped is
// true (the default), the base is a solid disk; when false the base is open.
type Cone struct {
	CX, CZ      float64
	YBase, YTip float64
	RBase       float64
	Capped      bool
	Surface
}

func (c *Cone) radiusAt(y float64) float64 {
	h := c.YTip - c.YBase
	if h <= 0 {
		return 0
	}
	return c.RBase * (c.YTip - y) / h
}

// WorldBounds returns the cone's axis-aligned bounds in world space.
func (c *Cone) WorldBounds() (vec.V, vec.V) {
	return expandXformBounds(c.Xform,
		vec.V{X: c.CX - c.RBase, Y: c.YBase, Z: c.CZ - c.RBase},
		vec.V{X: c.CX + c.RBase, Y: c.YTip, Z: c.CZ + c.RBase})
}

// minConeCapUpY is the minimum world-space Y component of a cone base cap's
// upward normal for the cap to count as walkable (≈60° max slope).
const minConeCapUpY = minWalkableCapUpY

// capUpNormal returns the world-space upward normal of the cone's base cap
// (the side you stand on when the cap is walkable).
func (c *Cone) capUpNormal() vec.V {
	n := vec.V{Y: 1}
	if c.Xform != nil {
		n = c.Xform.WorldNormal(n)
	}
	return n
}

// worldYOnBaseCap returns the world Y of the base cap under (wx,wz), or ok=false
// when the column misses the cap disk or the cap is too steep to walk on.
func (c *Cone) worldYOnBaseCap(wx, wz float64) (wy float64, ok bool) {
	if !c.Capped {
		return 0, false
	}
	h := c.YTip - c.YBase
	if h <= 0 || c.RBase <= 0 {
		return 0, false
	}
	if c.capUpNormal().Y < minConeCapUpY && c.capUpNormal().Y > -minConeCapUpY {
		return 0, false
	}
	if c.Xform == nil {
		dx, dz := wx-c.CX, wz-c.CZ
		if dx*dx+dz*dz > c.RBase*c.RBase {
			return 0, false
		}
		return c.YBase, true
	}
	wy, ok = c.Xform.WorldYForLocalY(wx, wz, c.YBase)
	if !ok {
		return 0, false
	}
	lp := c.Xform.ToLocal(vec.V{X: wx, Y: wy, Z: wz})
	if math.Abs(lp.Y-c.YBase) > 0.05 {
		return 0, false
	}
	dx, dz := lp.X-c.CX, lp.Z-c.CZ
	if dx*dx+dz*dz > c.RBase*c.RBase {
		return 0, false
	}
	return wy, true
}

// capGroundHeight returns the walkable height of the base cap at (wx,wz) when it
// lies at or below headY.
func (c *Cone) capGroundHeight(wx, wz, headY float64) (float64, bool) {
	wy, ok := c.worldYOnBaseCap(wx, wz)
	if !ok || wy > headY {
		return 0, false
	}
	return wy, true
}

func (c *Cone) onCapDisk(wx, wz, worldY, playerR float64) bool {
	p := vec.V{X: wx, Y: worldY, Z: wz}
	if c.Xform != nil {
		p = c.Xform.ToLocal(p)
	}
	if math.Abs(p.Y-c.YBase) > 0.05 {
		return false
	}
	rr := c.RBase - playerR
	if rr < 0 {
		rr = 0
	}
	dx, dz := p.X-c.CX, p.Z-c.CZ
	return dx*dx+dz*dz <= rr*rr+1e-6
}

// blocksColumn reports whether a player footprint at world (wx,wz) intersects
// the cone's volume within [bandLo,bandHi]. bandLo is walkTop (feet+step) in
// Blocked queries. The base cap disk is walkable and does not block; the sloped
// sides and interior still do.
func (c *Cone) blocksColumn(wx, wz, bandLo, bandHi, playerR float64) bool {
	capY, onCap := c.worldYOnBaseCap(wx, wz)
	if onCap && c.onCapDisk(wx, wz, capY, playerR) && bandLo >= capY-0.08 && bandLo <= capY+0.55 {
		// Feet are on the cap; the capsule may extend into the cone interior above.
		return false
	}
	for _, wy := range []float64{bandLo, bandHi, (bandLo + bandHi) * 0.5} {
		p := vec.V{X: wx, Y: wy, Z: wz}
		if c.Xform != nil {
			p = c.Xform.ToLocal(p)
		}
		if p.Y < c.YBase || p.Y > c.YTip {
			continue
		}
		if math.Abs(p.Y-c.YBase) < 0.01 {
			dx, dz := p.X-c.CX, p.Z-c.CZ
			if dx*dx+dz*dz <= c.RBase*c.RBase {
				continue
			}
		}
		rr := c.radiusAt(p.Y) + playerR
		dx, dz := p.X-c.CX, p.Z-c.CZ
		if dx*dx+dz*dz < rr*rr {
			return true
		}
	}
	return false
}

// Intersect returns the nearest positive hit distance, or Inf on a miss.
func (c *Cone) Intersect(r vec.Ray) float64 {
	h := c.YTip - c.YBase
	if h <= 0 {
		return Inf
	}
	k := c.RBase / h // radius per unit height below the tip
	ey := r.Origin.Y - c.YTip
	dx, dy, dz := r.Dir.X, r.Dir.Y, r.Dir.Z
	ox, oz := r.Origin.X-c.CX, r.Origin.Z-c.CZ
	a := dx*dx + dz*dz - dy*dy*k*k
	b := ox*dx + oz*dz - ey*dy*k*k
	cc := ox*ox + oz*oz - ey*ey*k*k

	best := Inf
	disc := b*b - a*cc
	if disc >= 0 && math.Abs(a) > 1e-12 {
		sq := math.Sqrt(disc)
		for _, t := range []float64{(-b - sq) / a, (-b + sq) / a} {
			if t < eps {
				continue
			}
			hy := r.Origin.Y + dy*t
			if hy >= c.YBase && hy <= c.YTip && t < best {
				best = t
			}
		}
	}

	// Base disk cap — only when Capped so open cones (e.g. tree foliage) have
	// no bottom surface.
	if c.Capped && math.Abs(dy) > 1e-6 {
		tc := (c.YBase - r.Origin.Y) / dy
		if tc >= eps && tc < best {
			hx := r.Origin.X + dx*tc
			hz := r.Origin.Z + dz*tc
			dd := (hx-c.CX)*(hx-c.CX) + (hz-c.CZ)*(hz-c.CZ)
			if dd <= c.RBase*c.RBase {
				best = tc
			}
		}
	}
	return best
}

// CapNormalWorld returns a shading normal for the base cap disk that faces the
// ray origin, using world-space traversal so rotated cones shade correctly.
func (c *Cone) CapNormalWorld(ro, hp vec.V) vec.V {
	outward := vec.V{Y: -1}
	if c.Xform != nil {
		outward = c.Xform.WorldNormal(outward)
	}
	toHit := hp.Sub(ro)
	if toHit.Dot(outward) < 0 {
		return outward
	}
	return outward.Neg()
}

// Normal returns the outward unit normal at surface point p for hit distance t.
func (c *Cone) Normal(p vec.V, r vec.Ray, t float64) vec.V {
	hy := r.Origin.Y + r.Dir.Y*t
	if c.Capped && math.Abs(hy-c.YBase) < 0.01 {
		if c.Xform != nil {
			hp := c.Xform.ToWorld(p)
			ro := c.Xform.ToWorld(r.Origin)
			return c.Xform.ToLocalDir(c.CapNormalWorld(ro, hp))
		}
		if r.Dir.Y > 0 {
			return vec.V{Y: -1}
		}
		return vec.V{Y: 1}
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

// RingShell is the radial hit tolerance for a ring hoop (no wall thickness).
const RingShell = 0.01

// DefaultRingHeight is the vertical band height when height is omitted (3 cm).
const DefaultRingHeight = 0.03

// Ring is a horizontal hoop centred at (CX, CY, CZ) with radius Radius and
// vertical extent Height. It has no radial wall thickness; RingShell gives
// rays a finite hit band on the side and cap edges.
type Ring struct {
	CX, CY, CZ float64
	Radius     float64
	Height     float64
	Surface
}

func (r *Ring) height() float64 {
	if r.Height > 0 {
		return r.Height
	}
	return DefaultRingHeight
}

func (r *Ring) HalfHeight() float64 {
	return r.height() * 0.5
}

func (r *Ring) Shell() float64 {
	s := RingShell
	if min := r.Radius * 0.02; s < min {
		s = min
	}
	return s
}

// WorldBounds returns the ring's axis-aligned bounds in world space.
func (r *Ring) WorldBounds() (vec.V, vec.V) {
	sh := r.Shell()
	half := r.HalfHeight()
	return expandXformBounds(r.Xform,
		vec.V{X: r.CX - r.Radius - sh, Y: r.CY - half - sh, Z: r.CZ - r.Radius - sh},
		vec.V{X: r.CX + r.Radius + sh, Y: r.CY + half + sh, Z: r.CZ + r.Radius + sh})
}

// Intersect returns the nearest positive hit distance, or Inf on a miss.
func (r *Ring) Intersect(ray vec.Ray) float64 {
	half := r.HalfHeight()
	ymin, ymax := r.CY-half, r.CY+half
	sh := r.Shell()
	ox, oz := ray.Origin.X-r.CX, ray.Origin.Z-r.CZ
	dx, dy, dz := ray.Dir.X, ray.Dir.Y, ray.Dir.Z
	best := Inf

	// Side wall: solve for |xz - centre| = Radius while y is inside the band.
	a := dx*dx + dz*dz
	if a > 1e-12 {
		b := 2 * (ox*dx + oz*dz)
		cc := ox*ox + oz*oz - r.Radius*r.Radius
		disc := b*b - 4*a*cc
		if disc >= 0 {
			sq := math.Sqrt(disc)
			for _, t := range []float64{(-b - sq) / (2 * a), (-b + sq) / (2 * a)} {
				if t < eps {
					continue
				}
				y := ray.Origin.Y + dy*t
				if y < ymin-eps || y > ymax+eps {
					continue
				}
				d := math.Hypot(ray.Origin.X+dx*t-r.CX, ray.Origin.Z+dz*t-r.CZ)
				if math.Abs(d-r.Radius) <= sh && t < best {
					best = t
				}
			}
		}
	}

	// Top/bottom cap edges.
	if math.Abs(dy) > 1e-6 {
		for _, yp := range []float64{ymin, ymax} {
			t := (yp - ray.Origin.Y) / dy
			if t < eps || t >= best {
				continue
			}
			px := ray.Origin.X + dx*t
			pz := ray.Origin.Z + dz*t
			if math.Abs(math.Hypot(px-r.CX, pz-r.CZ)-r.Radius) <= sh {
				best = t
			}
		}
	}
	return best
}

// Normal returns the outward unit normal at surface point p.
func (r *Ring) Normal(p vec.V) vec.V {
	half := r.HalfHeight()
	if p.Y <= r.CY-half+1e-4 {
		return vec.V{Y: -1}
	}
	if p.Y >= r.CY+half-1e-4 {
		return vec.V{Y: 1}
	}
	dx, dz := p.X-r.CX, p.Z-r.CZ
	l := math.Hypot(dx, dz)
	if l == 0 {
		return vec.V{X: 1}
	}
	return vec.V{X: dx / l, Y: 0, Z: dz / l}
}

// DefaultLensThickness is the centre thickness when thickness is omitted (4 mm).
const DefaultLensThickness = 0.004

// Lens is a biconvex glass element. Local +Y is the optical axis (view looks
// from −Y). RFront/RBack are the front/back surface curvature radii; Thickness
// is vertex-to-vertex along Y; Aperture is the clear radius in the XZ plane.
type Lens struct {
	CX, CY, CZ float64
	Aperture   float64
	RFront     float64
	RBack      float64
	Thickness  float64
	Surface
}

func (l *Lens) thickness() float64 {
	if l.Thickness > 0 {
		return l.Thickness
	}
	return DefaultLensThickness
}

func (l *Lens) halfThickness() float64 { return l.thickness() * 0.5 }

func (l *Lens) frontCenterY() float64 { return l.CY - l.halfThickness() + l.RFront }

func (l *Lens) backCenterY() float64 { return l.CY + l.halfThickness() - l.RBack }

// WorldBounds returns the lens's axis-aligned bounds in world space.
func (l *Lens) WorldBounds() (vec.V, vec.V) {
	half := l.halfThickness()
	ap := l.Aperture
	if ap <= 0 {
		ap = 0.01
	}
	r := math.Max(l.RFront, l.RBack)
	if r < ap {
		r = ap
	}
	return expandXformBounds(l.Xform,
		vec.V{X: l.CX - r, Y: l.CY - half - r, Z: l.CZ - r},
		vec.V{X: l.CX + r, Y: l.CY + half + r, Z: l.CZ + r})
}

func lensSphereHit(ray vec.Ray, cy, radius float64, negFacing bool) float64 {
	oc := vec.V{X: ray.Origin.X, Y: ray.Origin.Y - cy, Z: ray.Origin.Z}
	b := oc.Dot(ray.Dir)
	c := oc.LenSq() - radius*radius
	disc := b*b - c
	if disc < 0 {
		return Inf
	}
	sq := math.Sqrt(disc)
	best := Inf
	for _, t := range []float64{-b - sq, -b + sq} {
		if t < eps {
			continue
		}
		py := ray.Origin.Y + ray.Dir.Y*t
		if negFacing {
			if py > cy+1e-6 {
				continue
			}
		} else if py < cy-1e-6 {
			continue
		}
		if t < best {
			best = t
		}
	}
	return best
}

// Intersect returns the nearest positive hit on either convex cap, or Inf.
func (l *Lens) Intersect(ray vec.Ray) float64 {
	if l.Aperture <= 0 || l.RFront <= 0 || l.RBack <= 0 {
		return Inf
	}
	yf := l.frontCenterY()
	yb := l.backCenterY()
	best := Inf
	for _, hit := range []struct {
		t   float64
		cy  float64
		rad float64
	}{
		{lensSphereHit(ray, yf, l.RFront, true), yf, l.RFront},
		{lensSphereHit(ray, yb, l.RBack, false), yb, l.RBack},
	} {
		if hit.t >= best {
			continue
		}
		p := ray.Origin.Add(ray.Dir.Scale(hit.t))
		dx, dz := p.X-l.CX, p.Z-l.CZ
		if dx*dx+dz*dz > l.Aperture*l.Aperture+1e-9 {
			continue
		}
		best = hit.t
	}
	return best
}

// Normal returns the outward unit normal at surface point p.
func (l *Lens) Normal(p vec.V) vec.V {
	yf := l.frontCenterY()
	yb := l.backCenterY()
	if p.Y < l.CY {
		n := vec.V{X: p.X - l.CX, Y: p.Y - yf, Z: p.Z - l.CZ}
		if ln := n.Len(); ln > 1e-12 {
			return n.Scale(1 / ln)
		}
		return vec.V{Y: -1}
	}
	n := vec.V{X: p.X - l.CX, Y: p.Y - yb, Z: p.Z - l.CZ}
	if ln := n.Len(); ln > 1e-12 {
		return n.Scale(1 / ln)
	}
	return vec.V{Y: 1}
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

// Light is a point or spot light with per-channel intensity. Radius is
// informational (kept for parity with the source scene). Range, when > 0, is the
// distance at which the light's contribution is forced to zero: beyond it the
// renderer skips the light entirely (including its shadow ray), and within it the
// falloff is smoothly windowed down to zero at Range. Range == 0 means "auto":
// the light reaches as far as it meaningfully contributes.
//
// Spot lights set Dir to the emission axis (normalized at load time) and
// ConeDeg to the full outer cone angle in degrees. Omit Dir or cone_angle for
// an omnidirectional point light.
type Light struct {
	Pos     vec.V
	Color   vec.V
	Radius  float64
	Range   float64
	Dir     vec.V
	ConeDeg float64
	// Interactive lights can be toggled with the use key; Hint is shown when
	// the player aims at the light (default "lamp" when Interactive is true).
	Interactive bool
	Hint        string
}

// IsSpot reports whether the light casts a directional cone.
func (l Light) IsSpot() bool {
	return l.ConeDeg > 0 && l.Dir.LenSq() > 1e-12
}
