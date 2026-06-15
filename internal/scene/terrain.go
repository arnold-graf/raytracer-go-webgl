package scene

import (
	"math"

	"raytracer/internal/texture"
	"raytracer/internal/vec"
)

// TerrainFeature is a single smooth peak (positive Height) or valley (negative
// Height) added to the terrain height field. Width is its radius of influence,
// Steepness the falloff exponent (2 = Gaussian, higher = flatter top / steeper
// sides), and ExtendX/ExtendZ stretch it into a ridge. Angle rotates the
// ellipse (radians) in the X/Z plane.
type TerrainFeature struct {
	PosX, PosZ       float64
	Height           float64
	Width            float64
	Steepness        float64
	ExtendX, ExtendZ float64
	Angle            float64
}

// TerrainPad flattens a rectangular building site into the height field: inside
// the inner rectangle (CenterX/Z ± HalfX/Z) the terrain is forced to Level, and
// over a Margin-wide ring outside it the natural terrain is smoothly blended
// down to that level. It lets a building/road/plaza sit on flat, seam-free
// ground without the surrounding relief poking through its floor. The effect is
// baked into the height cache, so it costs nothing at render time.
type TerrainPad struct {
	CenterX, CenterZ float64
	HalfX, HalfZ     float64
	Level            float64
	Margin           float64
}

// Terrain is a single-valued height field y = Height(x,z) over a rectangular
// footprint, rendered by ray marching. Material is blended between a grass, a
// rock, and a snow texture by slope and altitude.
type Terrain struct {
	OriginX, OriginZ float64
	SizeX, SizeZ     float64
	Base             float64
	Detail           float64 // fBm amplitude of fine relief
	DetailScale      float64 // fBm frequency
	Features         []TerrainFeature
	Pads             []TerrainPad

	Grass, Rock, Snow          int   // texture ids
	GrassCol, RockCol, SnowCol vec.V // per-layer tint
	SlopeLo, SlopeHi           float64
	SnowLo, SnowHi             float64

	Step       float64 // base marching step
	GridCell   float64 // world units per cache cell (0 = auto)
	MinY, MaxY float64 // precomputed vertical band (set by Prepare)

	// Precomputed height/normal cache, built by Prepare. Height and Normal read
	// from these via bilinear interpolation instead of re-evaluating the (very
	// expensive) analytic field per sample.
	gnx, gnz     int
	invDx, invDz float64 // cells per world unit
	hgrid        []float64
	ngrid        []vec.V

	// Coarse per-cell max-height grid for empty-space skipping while marching.
	cgnx, cgnz     int
	cwx, cwz       float64 // coarse cell world size
	cInvDx, cInvDz float64
	cmin, cmax     []float64
}

// TerrainCacheSnapshot is a read-only copy of the prepared terrain grids for
// renderer backends that upload the CPU-baked height field to another device.
type TerrainCacheSnapshot struct {
	GNX, GNZ     int
	InvDx, InvDz float64
	Height       []float64
	Normal       []vec.V
}

// CacheSnapshot returns copies of the prepared height and normal grids. It calls
// Prepare lazily when needed so scene loaders that have not explicitly prepared
// a terrain still get a complete snapshot.
func (t *Terrain) CacheSnapshot() TerrainCacheSnapshot {
	if t.hgrid == nil || t.ngrid == nil {
		t.Prepare()
	}
	h := append([]float64(nil), t.hgrid...)
	n := append([]vec.V(nil), t.ngrid...)
	return TerrainCacheSnapshot{
		GNX: t.gnx, GNZ: t.gnz, InvDx: t.invDx, InvDz: t.invDz,
		Height: h, Normal: n,
	}
}

// Prepare computes the conservative vertical bounds, fills defaults, and builds
// the height/normal cache. Call once after construction.
func (t *Terrain) Prepare() {
	maxY := t.Base + math.Abs(t.Detail)
	minY := t.Base - math.Abs(t.Detail)
	for i := range t.Features {
		if h := t.Features[i].Height; h > 0 {
			maxY += h
		} else {
			minY += h
		}
	}
	// Pads can clamp the field to an arbitrary Level; widen the band to include
	// it so the marching slab never misses a flattened region.
	for i := range t.Pads {
		if l := t.Pads[i].Level; l > maxY {
			maxY = l
		} else if l < minY {
			minY = l
		}
	}
	t.MinY = minY - 0.5
	t.MaxY = maxY + 0.5
	if t.Step <= 0 {
		t.Step = 0.3
	}
	t.buildCache()
}

// buildCache samples the analytic field onto a regular grid and precomputes
// per-vertex normals from finite differences, so rendering only does cheap
// bilinear lookups.
func (t *Terrain) buildCache() {
	cell := t.GridCell
	if cell <= 0 {
		cell = 0.25
	}
	t.gnx = int(math.Ceil(t.SizeX/cell)) + 1
	t.gnz = int(math.Ceil(t.SizeZ/cell)) + 1
	if t.gnx < 2 {
		t.gnx = 2
	}
	if t.gnz < 2 {
		t.gnz = 2
	}
	dx := t.SizeX / float64(t.gnx-1)
	dz := t.SizeZ / float64(t.gnz-1)
	t.invDx = 1 / dx
	t.invDz = 1 / dz

	t.hgrid = make([]float64, t.gnx*t.gnz)
	for j := 0; j < t.gnz; j++ {
		z := t.OriginZ + float64(j)*dz
		row := j * t.gnx
		for i := 0; i < t.gnx; i++ {
			t.hgrid[row+i] = t.heightAnalytic(t.OriginX+float64(i)*dx, z)
		}
	}

	t.ngrid = make([]vec.V, t.gnx*t.gnz)
	for j := 0; j < t.gnz; j++ {
		row := j * t.gnx
		jm := j - 1
		if jm < 0 {
			jm = 0
		}
		jp := j + 1
		if jp >= t.gnz {
			jp = t.gnz - 1
		}
		for i := 0; i < t.gnx; i++ {
			im := i - 1
			if im < 0 {
				im = 0
			}
			ip := i + 1
			if ip >= t.gnx {
				ip = t.gnx - 1
			}
			hl := t.hgrid[row+im]
			hr := t.hgrid[row+ip]
			hd := t.hgrid[jm*t.gnx+i]
			hu := t.hgrid[jp*t.gnx+i]
			t.ngrid[row+i] = vec.New((hl-hr)/(2*dx), 1, (hd-hu)/(2*dz)).Normalize()
		}
	}

	t.buildCoarse()
}

// buildCoarse builds a coarse grid holding the maximum terrain height in each
// coarse cell, used to skip empty space while marching.
func (t *Terrain) buildCoarse() {
	const coarse = 4.0 // target world size per coarse cell
	t.cgnx = int(math.Ceil(t.SizeX / coarse))
	t.cgnz = int(math.Ceil(t.SizeZ / coarse))
	if t.cgnx < 1 {
		t.cgnx = 1
	}
	if t.cgnz < 1 {
		t.cgnz = 1
	}
	t.cwx = t.SizeX / float64(t.cgnx)
	t.cwz = t.SizeZ / float64(t.cgnz)
	t.cInvDx = 1 / t.cwx
	t.cInvDz = 1 / t.cwz
	t.cmin = make([]float64, t.cgnx*t.cgnz)
	t.cmax = make([]float64, t.cgnx*t.cgnz)

	fnx := float64(t.gnx - 1)
	fnz := float64(t.gnz - 1)
	for cz := 0; cz < t.cgnz; cz++ {
		j0 := int(float64(cz) * fnz / float64(t.cgnz))
		j1 := int(math.Ceil(float64(cz+1)*fnz/float64(t.cgnz))) + 1
		if j1 > t.gnz-1 {
			j1 = t.gnz - 1
		}
		for cx := 0; cx < t.cgnx; cx++ {
			i0 := int(float64(cx) * fnx / float64(t.cgnx))
			i1 := int(math.Ceil(float64(cx+1)*fnx/float64(t.cgnx))) + 1
			if i1 > t.gnx-1 {
				i1 = t.gnx - 1
			}
			lo := math.Inf(1)
			hi := math.Inf(-1)
			for j := j0; j <= j1; j++ {
				row := j * t.gnx
				for i := i0; i <= i1; i++ {
					if h := t.hgrid[row+i]; h < lo {
						lo = h
					}
					if h := t.hgrid[row+i]; h > hi {
						hi = h
					}
				}
			}
			idx := cz*t.cgnx + cx
			t.cmin[idx] = lo - 1e-3
			t.cmax[idx] = hi + 1e-3
		}
	}
}

// heightAnalytic evaluates the exact (expensive) terrain height at world (x,z).
// Used only when building the cache.
func (t *Terrain) heightAnalytic(x, z float64) float64 {
	h := t.Base
	for i := range t.Features {
		f := &t.Features[i]
		dx, dz := x-f.PosX, z-f.PosZ
		if f.Angle != 0 {
			c, s := math.Cos(f.Angle), math.Sin(f.Angle)
			dx, dz = dx*c+dz*s, -dx*s+dz*c
		}
		ex, ez, w := f.ExtendX, f.ExtendZ, f.Width
		if ex == 0 {
			ex = 1
		}
		if ez == 0 {
			ez = 1
		}
		if w == 0 {
			w = 1
		}
		ax, az := dx/(w*ex), dz/(w*ez)
		d := math.Sqrt(ax*ax + az*az)
		st := f.Steepness
		if st == 0 {
			st = 2
		}
		h += f.Height * math.Exp(-math.Pow(d, st))
	}
	if t.Detail != 0 {
		h += t.Detail * texture.FBM(x*t.DetailScale, 0, z*t.DetailScale, 4)
	}
	// Building pads flatten the site and blend the surrounding relief down to
	// Level over a Margin-wide ring. Applied last so they override features and
	// detail, and applied in order so overlapping pads layer predictably.
	for i := range t.Pads {
		p := &t.Pads[i]
		dx := math.Abs(x-p.CenterX) - p.HalfX
		dz := math.Abs(z-p.CenterZ) - p.HalfZ
		if dx < 0 {
			dx = 0
		}
		if dz < 0 {
			dz = 0
		}
		var w float64
		if p.Margin <= 0 {
			if dx == 0 && dz == 0 {
				w = 1
			}
		} else {
			w = 1 - smoothstep(0, p.Margin, math.Hypot(dx, dz))
		}
		h += (p.Level - h) * w
	}
	return h
}

// Height returns the cached terrain height at world (x,z) via bilinear
// interpolation (clamped to the footprint).
func (t *Terrain) Height(x, z float64) float64 {
	if t.hgrid == nil {
		return t.heightAnalytic(x, z)
	}
	fx := (x - t.OriginX) * t.invDx
	fz := (z - t.OriginZ) * t.invDz
	maxX := float64(t.gnx - 1)
	maxZ := float64(t.gnz - 1)
	if fx < 0 {
		fx = 0
	} else if fx > maxX {
		fx = maxX
	}
	if fz < 0 {
		fz = 0
	} else if fz > maxZ {
		fz = maxZ
	}
	ix := int(fx)
	iz := int(fz)
	if ix >= t.gnx-1 {
		ix = t.gnx - 2
	}
	if iz >= t.gnz-1 {
		iz = t.gnz - 2
	}
	tx := fx - float64(ix)
	tz := fz - float64(iz)
	i00 := iz*t.gnx + ix
	h00 := t.hgrid[i00]
	h10 := t.hgrid[i00+1]
	h01 := t.hgrid[i00+t.gnx]
	h11 := t.hgrid[i00+t.gnx+1]
	a := h00 + (h10-h00)*tx
	b := h01 + (h11-h01)*tx
	return a + (b-a)*tz
}

// slab clips the ray to the terrain's axis-aligned bounding box. Returns the
// entry/exit distances and whether the ray intersects the box at all.
func (t *Terrain) slab(r vec.Ray) (tEnter, tExit float64, ok bool) {
	t1, t2 := (t.OriginX-r.Origin.X)/r.Dir.X, (t.OriginX+t.SizeX-r.Origin.X)/r.Dir.X
	if t1 > t2 {
		t1, t2 = t2, t1
	}
	t3, t4 := (t.MinY-r.Origin.Y)/r.Dir.Y, (t.MaxY-r.Origin.Y)/r.Dir.Y
	if t3 > t4 {
		t3, t4 = t4, t3
	}
	t5, t6 := (t.OriginZ-r.Origin.Z)/r.Dir.Z, (t.OriginZ+t.SizeZ-r.Origin.Z)/r.Dir.Z
	if t5 > t6 {
		t5, t6 = t6, t5
	}
	tEnter = t1
	if t3 > tEnter {
		tEnter = t3
	}
	if t5 > tEnter {
		tEnter = t5
	}
	tExit = t2
	if t4 < tExit {
		tExit = t4
	}
	if t6 < tExit {
		tExit = t6
	}
	if tExit < tEnter || tExit < eps {
		return 0, 0, false
	}
	return tEnter, tExit, true
}

// Intersect ray-marches the height field and returns the nearest hit distance,
// or Inf, refining the crossing with a bisection (used for primary/visible hits).
func (t *Terrain) Intersect(r vec.Ray) float64 {
	return t.march(r, Inf, true)
}

// IntersectWithin is Intersect capped at maxT: anything farther is irrelevant
// because a closer surface was already found, so the march can stop early. This
// avoids marching the full terrain footprint behind walls/floors.
func (t *Terrain) IntersectWithin(r vec.Ray, maxT float64) float64 {
	return t.march(r, maxT, true)
}

// Occlude is a cheaper variant for shadow/AO probes: it caps the march at maxT
// and skips the bisection refinement, returning the approximate crossing
// distance (or Inf). Visual precision isn't needed for occlusion.
func (t *Terrain) Occlude(r vec.Ray, maxT float64) float64 {
	return t.march(r, maxT, false)
}

// march walks the height field between the ray's entry/exit with the box. It
// first DDA-walks the coarse min/max height cells and only runs the precise
// height sampler inside cells whose vertical range can actually contain a hit.
func (t *Terrain) march(r vec.Ray, maxT float64, refine bool) float64 {
	tEnter, tExit, ok := t.slab(r)
	if !ok {
		return Inf
	}
	if tEnter < eps {
		tEnter = eps
	}
	if tExit > maxT {
		tExit = maxT
	}
	if tEnter >= tExit {
		return Inf
	}

	if t.cmin != nil && t.cmax != nil && (math.Abs(r.Dir.X) > 1e-9 || math.Abs(r.Dir.Z) > 1e-9) {
		return t.marchCoarse(r, tEnter, tExit, refine)
	}
	return t.marchFine(r, tEnter, tExit, refine)
}

// marchCoarse walks the terrain footprint in coarse X/Z cells. A cell can be
// skipped if the ray segment through it is wholly above or below that cell's
// conservative height band.
func (t *Terrain) marchCoarse(r vec.Ray, tEnter, tExit float64, refine bool) float64 {
	tc := tEnter
	px := r.Origin.X + r.Dir.X*tc
	pz := r.Origin.Z + r.Dir.Z*tc
	cx := int((px - t.OriginX) * t.cInvDx)
	cz := int((pz - t.OriginZ) * t.cInvDz)
	if cx < 0 {
		cx = 0
	} else if cx >= t.cgnx {
		cx = t.cgnx - 1
	}
	if cz < 0 {
		cz = 0
	} else if cz >= t.cgnz {
		cz = t.cgnz - 1
	}

	tNextX, tDeltaX, stepX := Inf, Inf, 0
	if r.Dir.X > 1e-9 {
		stepX = 1
		tNextX = (t.OriginX + float64(cx+1)*t.cwx - r.Origin.X) / r.Dir.X
		tDeltaX = t.cwx / r.Dir.X
	} else if r.Dir.X < -1e-9 {
		stepX = -1
		tNextX = (t.OriginX + float64(cx)*t.cwx - r.Origin.X) / r.Dir.X
		tDeltaX = -t.cwx / r.Dir.X
	}

	tNextZ, tDeltaZ, stepZ := Inf, Inf, 0
	if r.Dir.Z > 1e-9 {
		stepZ = 1
		tNextZ = (t.OriginZ + float64(cz+1)*t.cwz - r.Origin.Z) / r.Dir.Z
		tDeltaZ = t.cwz / r.Dir.Z
	} else if r.Dir.Z < -1e-9 {
		stepZ = -1
		tNextZ = (t.OriginZ + float64(cz)*t.cwz - r.Origin.Z) / r.Dir.Z
		tDeltaZ = -t.cwz / r.Dir.Z
	}

	for tc < tExit && cx >= 0 && cx < t.cgnx && cz >= 0 && cz < t.cgnz {
		tn := tNextX
		if tNextZ < tn {
			tn = tNextZ
		}
		if tn > tExit {
			tn = tExit
		}
		if tn < tc {
			tn = tc
		}

		idx := cz*t.cgnx + cx
		y0 := r.Origin.Y + r.Dir.Y*tc
		y1 := r.Origin.Y + r.Dir.Y*tn
		segMinY, segMaxY := y0, y1
		if segMinY > segMaxY {
			segMinY, segMaxY = segMaxY, segMinY
		}
		if segMaxY >= t.cmin[idx] && segMinY <= t.cmax[idx] {
			if hit := t.marchFine(r, tc, tn, refine); hit < Inf {
				return hit
			}
		}

		if tn >= tExit {
			break
		}

		crossX, crossZ := tNextX <= tNextZ, tNextZ <= tNextX
		if crossX {
			cx += stepX
			tNextX += tDeltaX
		}
		if crossZ {
			cz += stepZ
			tNextZ += tDeltaZ
		}
		tc = tn + 1e-5
	}
	return Inf
}

// marchFine precisely marches one interval, enlarging steps when well above the
// smooth surface and refining visible crossings by bisection.
func (t *Terrain) marchFine(r vec.Ray, tEnter, tExit float64, refine bool) float64 {
	base := t.Step
	tc := tEnter
	px := r.Origin.X + r.Dir.X*tc
	py := r.Origin.Y + r.Dir.Y*tc
	pz := r.Origin.Z + r.Dir.Z*tc
	fc := py - t.Height(px, pz)

	for tc < tExit {
		step := base
		if fc > 0 {
			if s := fc * 0.7; s > step {
				step = s
			}
			if step > base*20 {
				step = base * 20
			}
		}
		tn := tc + step
		if tn > tExit {
			tn = tExit
		}
		nx := r.Origin.X + r.Dir.X*tn
		ny := r.Origin.Y + r.Dir.Y*tn
		nz := r.Origin.Z + r.Dir.Z*tn
		fn := ny - t.Height(nx, nz)

		if fn <= 0 && fc > 0 {
			if !refine {
				return tn
			}
			lo, hi := tc, tn
			for i := 0; i < 10; i++ {
				m := (lo + hi) * 0.5
				pm := r.At(m)
				if pm.Y-t.Height(pm.X, pm.Z) <= 0 {
					hi = m
				} else {
					lo = m
				}
			}
			return (lo + hi) * 0.5
		}
		tc, px, py, pz, fc = tn, nx, ny, nz, fn
		if tn >= tExit {
			break
		}
	}
	return Inf
}

// Normal returns the surface normal from the cached normal grid (bilinearly
// interpolated for smooth shading).
func (t *Terrain) Normal(p vec.V) vec.V {
	if t.ngrid == nil {
		const e = 0.05
		hl := t.Height(p.X-e, p.Z)
		hr := t.Height(p.X+e, p.Z)
		hd := t.Height(p.X, p.Z-e)
		hu := t.Height(p.X, p.Z+e)
		return vec.New(hl-hr, 2*e, hd-hu).Normalize()
	}
	fx := (p.X - t.OriginX) * t.invDx
	fz := (p.Z - t.OriginZ) * t.invDz
	maxX := float64(t.gnx - 1)
	maxZ := float64(t.gnz - 1)
	if fx < 0 {
		fx = 0
	} else if fx > maxX {
		fx = maxX
	}
	if fz < 0 {
		fz = 0
	} else if fz > maxZ {
		fz = maxZ
	}
	ix := int(fx)
	iz := int(fz)
	if ix >= t.gnx-1 {
		ix = t.gnx - 2
	}
	if iz >= t.gnz-1 {
		iz = t.gnz - 2
	}
	tx := fx - float64(ix)
	tz := fz - float64(iz)
	i00 := iz*t.gnx + ix
	n00 := t.ngrid[i00]
	n10 := t.ngrid[i00+1]
	n01 := t.ngrid[i00+t.gnx]
	n11 := t.ngrid[i00+t.gnx+1]
	a := n00.Add(n10.Sub(n00).Scale(tx))
	b := n01.Add(n11.Sub(n01).Scale(tx))
	return a.Add(b.Sub(a).Scale(tz)).Normalize()
}

// AlbedoAt blends the grass / rock / snow layers by slope and altitude, with a
// little noise jitter so the transitions don't form perfect contour lines.
func (t *Terrain) AlbedoAt(p, n vec.V) vec.V {
	slope := 1 - n.Y
	j := 0.08 * texture.Perlin(p.X*0.7, 0, p.Z*0.7)
	rockW := smoothstep(t.SlopeLo, t.SlopeHi, slope+j)
	snowW := smoothstep(t.SnowLo, t.SnowHi, p.Y+2*j)

	c := texture.Eval(t.Grass, p, t.GrassCol)
	if rockW > 0.001 {
		c = mixV(c, texture.Eval(t.Rock, p, t.RockCol), rockW)
	}
	if snowW > 0.001 {
		c = mixV(c, texture.Eval(t.Snow, p, t.SnowCol), snowW)
	}
	return c
}

// WaterPool is a flat, reflective circular puddle at a fixed level. It reflects
// (or refracts) the surrounding terrain and sky via the normal material path.
type WaterPool struct {
	CX, CZ float64
	Radius float64
	Level  float64
	Ripple float64
	// RippleSpeed drifts the ripple field over time to simulate wind-driven
	// waves (0 = static). The drift direction is set by RippleDirX/RippleDirZ.
	RippleSpeed float64
	RippleDirX  float64
	RippleDirZ  float64
	Surface
}

// Intersect returns the hit distance with the puddle disk, or Inf.
func (w *WaterPool) Intersect(r vec.Ray) float64 {
	if math.Abs(r.Dir.Y) < 1e-6 {
		return Inf
	}
	t := (w.Level - r.Origin.Y) / r.Dir.Y
	if t < eps {
		return Inf
	}
	px := r.Origin.X + r.Dir.X*t
	pz := r.Origin.Z + r.Dir.Z*t
	dx, dz := px-w.CX, pz-w.CZ
	if dx*dx+dz*dz > w.Radius*w.Radius {
		return Inf
	}
	return t
}

// NormalAt returns the (optionally rippled) water normal at point p and time t
// (seconds). When RippleSpeed is non-zero the ripple field drifts along
// (RippleDirX, RippleDirZ), animating wind-induced waves.
func (w *WaterPool) NormalAt(p vec.V, t float64) vec.V {
	if w.Ripple <= 0 {
		return vec.V{Y: 1}
	}
	var dx, dz float64
	if w.RippleSpeed != 0 {
		phase := t * w.RippleSpeed
		dx = phase * w.RippleDirX
		dz = phase * w.RippleDirZ
	}
	n := vec.V{
		X: w.Ripple * texture.Perlin(p.X*2.5+dx, 0, p.Z*2.5+dz),
		Y: 1,
		Z: w.Ripple * texture.Perlin(p.X*2.5+dx+7, 0, p.Z*2.5+dz+3),
	}
	return n.Normalize()
}

func smoothstep(e0, e1, x float64) float64 {
	if e1 == e0 {
		if x < e0 {
			return 0
		}
		return 1
	}
	t := (x - e0) / (e1 - e0)
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return t * t * (3 - 2*t)
}

func mixV(a, b vec.V, t float64) vec.V { return a.Add(b.Sub(a).Scale(t)) }
