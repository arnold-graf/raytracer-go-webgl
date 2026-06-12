// Package trace implements the path/ray evaluation: intersection dispatch,
// material shading (diffuse, mirror, metal, glass, emissive), direct lighting
// with shadow rays, a baked ambient-occlusion volume (see aovolume.go), the
// procedural sky, and the final tonemap. It is a port of the original JS
// renderer.
package trace

import (
	"math"
	"sync"

	"raytracer/internal/bvh"
	"raytracer/internal/scene"
	"raytracer/internal/texture"
	"raytracer/internal/vec"
)

// Options toggles the optional (and more expensive) shading features.
type Options struct {
	Mirror bool
	Shadow bool
	AO     bool
}

// Tracer renders a single scene with a set of feature toggles.
type Tracer struct {
	Scene *scene.Scene
	Opts  Options
	// Time is the animation clock in seconds (e.g. for water ripples). The
	// caller sets it before each frame.
	Time  float64
	accel *bvh.BVH
	// blockers is a BVH of only shadow-casting primitives (no emissive spheres
	// or tori), used for AnyHit shadow queries. See bvh.NewBlockers.
	blockers *bvh.BVH

	// Per-light culling data, precomputed once. lightCullR2[i] is the squared
	// distance beyond which light i is skipped (no shadow ray). lightInvR2[i] is
	// 1/Range^2 for windowed falloff, or 0 when the light has no explicit Range
	// (in which case the original inverse-square falloff is used unchanged).
	lightCullR2 []float64
	lightInvR2  []float64

	// Same culling data for campfires (one entry per campfire, shared by its
	// flickering sub-lights).
	fireCullR2 []float64
	fireInvR2  []float64

	// Baked ambient-occlusion volume, built lazily on first use (see aovolume.go).
	aoVol  *aoVolume
	aoOnce sync.Once
}

// lightCullEps is the per-channel radiance below which a point light's
// (unshadowed, best-case) contribution is treated as invisible and culled. It
// sits well under one 8-bit level after tonemapping, so auto-culling does not
// change the rendered image.
const lightCullEps = 0.0025

// New returns a tracer bound to a scene, building a BVH over its finite
// primitives once up front (the scene is static at runtime). It also
// precomputes per-light cull distances.
func New(s *scene.Scene) *Tracer {
	tr := &Tracer{Scene: s, accel: bvh.New(s), blockers: bvh.NewBlockers(s)}
	tr.buildLightCulling()
	return tr
}

// buildLightCulling fills lightCullR2/lightInvR2 from each light's intensity and
// optional Range. The attenuation model is att = 1/(0.5 + 0.08*d^2) (matching
// the shading loop), so the auto cull distance solves Cmax*att = lightCullEps.
func (tr *Tracer) buildLightCulling() {
	lights := tr.Scene.Lights
	tr.lightCullR2 = make([]float64, len(lights))
	tr.lightInvR2 = make([]float64, len(lights))
	for i := range lights {
		L := &lights[i]
		cmax := fmax(L.Color.X, fmax(L.Color.Y, L.Color.Z))

		// Auto cull distance: where the best-case contribution drops below eps.
		autoR2 := 0.0
		if cmax > lightCullEps*0.5 {
			autoR2 = (cmax/lightCullEps - 0.5) / 0.08
			if autoR2 < 0 {
				autoR2 = 0
			}
		}

		if L.Range > 0 {
			r2 := L.Range * L.Range
			tr.lightCullR2[i] = r2
			tr.lightInvR2[i] = 1 / r2
		} else {
			tr.lightCullR2[i] = autoR2
			tr.lightInvR2[i] = 0
		}
	}

	fires := tr.Scene.Campfires
	tr.fireCullR2 = make([]float64, len(fires))
	tr.fireInvR2 = make([]float64, len(fires))
	for i := range fires {
		cmax := fires[i].PeakChannel()
		autoR2 := 0.0
		if cmax > lightCullEps*0.5 {
			autoR2 = (cmax/lightCullEps - 0.5) / 0.08
			if autoR2 < 0 {
				autoR2 = 0
			}
		}
		if r := fires[i].Range; r > 0 {
			r2 := r * r
			tr.fireCullR2[i] = r2
			tr.fireInvR2[i] = 1 / r2
		} else {
			tr.fireCullR2[i] = autoR2
			tr.fireInvR2[i] = 0
		}
	}
}

// addPointLight accumulates one point light's diffuse contribution into lit:
// distance culling, an optional shadow ray, inverse-square attenuation and an
// optional windowed range falloff. It is shared by static lights and the
// flickering sub-lights of campfires.
func (tr *Tracer) addPointLight(lit, hp, albedo, n, ep, pos, color vec.V, cullR2, invR2 float64) vec.V {
	ld := pos.Sub(hp)
	d2 := ld.LenSq()
	if d2 > cullR2 {
		return lit
	}
	ldist := math.Sqrt(d2)
	if ldist == 0 {
		ldist = 1
	}
	ln := ld.Scale(1 / ldist)
	ndl := n.Dot(ln)
	if ndl < 0.001 {
		return lit
	}
	att := fmin(1, 1/(0.5+d2*0.08))
	if invR2 > 0 {
		x := d2 * invR2
		w := 1 - x*x
		if w < 0 {
			w = 0
		}
		att *= w * w
	}
	// Skip the (expensive) shadow ray when the best-case contribution is already
	// below one 8-bit level. albedo <= 1, so att*ndl*max(color) is an upper bound
	// on the brightest channel; this changes nothing visible but culls shadow
	// rays in a light's dim falloff tail.
	if att*ndl*fmax(color.X, fmax(color.Y, color.Z)) < lightCullEps {
		return lit
	}
	if tr.Opts.Shadow && tr.shadowed(ep, ln, ldist) {
		return lit
	}
	return lit.Add(color.Scale(att * ndl).Mul(albedo))
}

// Texel is one slot of the renderer's per-pixel texture cache. Procedural
// textures are pure functions of (id, hit point, base albedo) with no time
// dependence, so when a pixel's primary hit lands on exactly the same point
// across frames — i.e. whenever the camera and that geometry are static — the
// texture result is bit-for-bit identical and can be reused instead of
// re-evaluating the (often expensive) noise stack. The cache is keyed on the
// exact inputs, so a hit reproduces the uncached value precisely: standing
// still is cheap, moving simply misses and recomputes as before. The zero value
// is an empty slot.
type Texel struct {
	ok   bool
	tex  int
	p    vec.V
	base vec.V
	out  vec.V
}

// lookup returns the cached albedo for (tex, p, base) and true on an exact hit.
func (t *Texel) lookup(tex int, p, base vec.V) (vec.V, bool) {
	if t.ok && t.tex == tex && t.p == p && t.base == base {
		return t.out, true
	}
	return vec.V{}, false
}

func (t *Texel) store(tex int, p, base, out vec.V) {
	*t = Texel{ok: true, tex: tex, p: p, base: base, out: out}
}

// hit is the surface record produced by the nearest intersection.
type hit struct {
	t       float64
	p       vec.V
	n       vec.V
	albedo  vec.V
	mat     int
	rough   float64
	ior     float64
	reflect float64
	transmit float64
}

// intersect finds the nearest primitive along the ray and fills h. When tc is
// non-nil it is used as a one-slot texture cache for this hit's procedural
// texture evaluation (see Texel).
func (tr *Tracer) intersect(r vec.Ray, h *hit, tc *Texel) bool {
	s := tr.Scene

	// Finite analytic primitives (spheres, boxes, cylinders, cones, tori) come
	// from the BVH; planes, terrain and water are tested directly.
	tmin, kind, idx := tr.accel.Nearest(r)
	for i := range s.Planes {
		if t := s.Planes[i].Intersect(r); t < tmin {
			tmin, kind, idx = t, 1, i
		}
	}
	for i := range s.Terrains {
		// Cap the march at the nearest hit so far: terrain behind an already-hit
		// wall/floor needn't be marched at all.
		if t := s.Terrains[i].IntersectWithin(r, tmin); t < tmin {
			tmin, kind, idx = t, 6, i
		}
	}
	for i := range s.Waters {
		if t := s.Waters[i].Intersect(r); t < tmin {
			tmin, kind, idx = t, 7, i
		}
	}

	if kind < 0 {
		return false
	}

	h.t = tmin
	h.p = r.At(tmin)
	var tex int
	texP := h.p // procedural texture coords (local space when transformed)
	switch kind {
	case 0:
		o := &s.Spheres[idx]
		lp := h.p
		if o.Xform != nil {
			lp = o.Xform.LocalRay(r).At(tmin)
		}
		texP = lp
		h.n, h.albedo, h.mat, h.rough, h.ior, tex, h.reflect, h.transmit =
			o.Xform.WorldNormal(o.Normal(lp)), o.Albedo, o.Mat, o.Rough, o.IOR, o.Tex, o.Reflect, o.Transmit
	case 1:
		o := &s.Planes[idx]
		h.n, h.albedo, h.mat, h.rough, h.ior, tex, h.reflect, h.transmit =
			o.N, o.AlbedoAt(h.p), o.Mat, o.Rough, o.IOR, o.Tex, o.Reflect, o.Transmit
	case 2:
		o := &s.Boxes[idx]
		lp := h.p
		if o.Xform != nil {
			lp = o.Xform.LocalRay(r).At(tmin)
		}
		texP = lp
		h.n, h.albedo, h.mat, h.rough, h.ior, tex, h.reflect, h.transmit =
			o.Xform.WorldNormal(o.Normal(lp)), o.Albedo, o.Mat, o.Rough, o.IOR, o.Tex, o.Reflect, o.Transmit
	case 3:
		o := &s.Cylinders[idx]
		lr := r
		if o.Xform != nil {
			lr = o.Xform.LocalRay(r)
		}
		lp := lr.At(tmin)
		texP = lp
		h.n, h.albedo, h.mat, h.rough, h.ior, tex, h.reflect, h.transmit =
			o.Xform.WorldNormal(o.Normal(lp, lr, tmin)), o.Albedo, o.Mat, o.Rough, o.IOR, o.Tex, o.Reflect, o.Transmit
	case 4:
		o := &s.Cones[idx]
		lr := r
		if o.Xform != nil {
			lr = o.Xform.LocalRay(r)
		}
		lp := lr.At(tmin)
		texP = lp
		h.n, h.albedo, h.mat, h.rough, h.ior, tex, h.reflect, h.transmit =
			o.Xform.WorldNormal(o.Normal(lp, lr, tmin)), o.Albedo, o.Mat, o.Rough, o.IOR, o.Tex, o.Reflect, o.Transmit
	case 5:
		o := &s.Tori[idx]
		lp := h.p
		if o.Xform != nil {
			lp = o.Xform.LocalRay(r).At(tmin)
		}
		texP = lp
		h.n, h.albedo, h.mat, h.rough, h.ior, tex, h.reflect, h.transmit =
			o.Xform.WorldNormal(o.Normal(lp)), o.Albedo, o.Mat, o.Rough, o.IOR, o.Tex, o.Reflect, o.Transmit
	case 6:
		o := &s.Terrains[idx]
		n := o.Normal(h.p)
		h.n, h.albedo, h.mat, h.rough, h.ior, tex, h.reflect, h.transmit = n, o.AlbedoAt(h.p, n), scene.MatDiffuse, 0, 1.5, texture.None, 0, 0
	case 7:
		o := &s.Waters[idx]
		h.n, h.albedo, h.mat, h.rough, h.ior, tex, h.reflect, h.transmit =
			o.NormalAt(h.p, tr.Time), o.Albedo, o.Mat, o.Rough, o.IOR, o.Tex, o.Reflect, o.Transmit
	}
	if tex != texture.None {
		if tc != nil {
			if out, ok := tc.lookup(tex, texP, h.albedo); ok {
				h.albedo = out
			} else {
				out = texture.Eval(tex, texP, h.albedo)
				tc.store(tex, texP, h.albedo, out)
				h.albedo = out
			}
		} else {
			h.albedo = texture.Eval(tex, texP, h.albedo)
		}
	}
	return true
}

// ProbeDistance returns the distance to the nearest solid surface (finite
// primitives via the BVH, plus planes) along a ray from origin in dir, capped at
// maxT. It is the acoustic ray query used to estimate room enclosure for
// reverb: rays that hit nearby walls in many directions imply an enclosed,
// reverberant space, while rays that fly off to maxT imply the open outdoors.
// Terrain is intentionally excluded (it is the ground, not a reflecting wall),
// which conveniently makes the outdoors read as dry.
func (tr *Tracer) ProbeDistance(origin, dir vec.V, maxT float64) float64 {
	return tr.nearestDist(vec.Ray{Origin: origin, Dir: dir.Normalize()}, maxT)
}

// nearestDist returns the distance to the closest primitive along r, capped at
// maxT (anything farther is reported as maxT). It skips normal/material work,
// making it cheaper than intersect for occlusion probes (AO).
func (tr *Tracer) nearestDist(r vec.Ray, maxT float64) float64 {
	s := tr.Scene
	tmin := tr.accel.NearestDist(r, maxT)
	for i := range s.Planes {
		if t := s.Planes[i].Intersect(r); t < tmin {
			tmin = t
		}
	}
	// Terrain is intentionally excluded from AO probes: it is smooth and convex
	// enough that self-occlusion is negligible, and marching it for every AO ray
	// is expensive. Objects above still occlude AO normally.
	return tmin
}

// shadowed reports whether anything blocks the segment from origin toward dir
// up to maxT. Emissive spheres and tori are skipped, matching the original.
func (tr *Tracer) shadowed(origin, dir vec.V, maxT float64) bool {
	r := vec.Ray{Origin: origin, Dir: dir}
	s := tr.Scene
	if tr.blockers.AnyHit(r, maxT) {
		return true
	}
	for i := range s.Planes {
		if t := s.Planes[i].Intersect(r); t > 1e-4 && t < maxT-0.05 {
			return true
		}
	}
	for i := range s.Terrains {
		if t := s.Terrains[i].Occlude(r, maxT); t > 1e-4 && t < maxT-0.05 {
			return true
		}
	}
	// Tori and water are intentionally skipped in shadow tests.
	return false
}

// fmin, fmax, clamp are inlinable scalar helpers. Unlike math.Min/Max they
// compile to simple comparisons (no NaN/signed-zero handling), which matters a
// lot on the per-ray hot path.
func fmin(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func fmax(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

// sky returns the procedural sky/sun radiance for a ray direction, dispatching
// on the scene's selected sky variant.
func (tr *Tracer) sky(d vec.V) vec.V {
	switch tr.Scene.Env.Sky {
	case scene.SkyCloudy:
		return cloudySky(d, tr.Time)
	case scene.SkyNightStars:
		return nightStarsSky(d)
	case scene.SkyNightStorm:
		return nightStormSky(d, tr.Time)
	case scene.SkySunset:
		return sunsetSky(d, tr.Time)
	default:
		return clearSky(d)
	}
}

// clearSky is the original clear-day gradient with a bright sun (unchanged).
func clearSky(d vec.V) vec.V {
	t := clamp(d.Y*0.5+0.5, 0, 1)
	sun := fmax(0, d.X*0.4+d.Y*0.8+d.Z*-0.45)
	// sun^64 via repeated squaring (cheaper than math.Pow on the hot path).
	s2 := sun * sun
	s4 := s2 * s2
	s8 := s4 * s4
	s16 := s8 * s8
	s32 := s16 * s16
	sun64 := s32 * s32
	return vec.V{
		X: 0.05 + 0.10*t + sun64*3,
		Y: 0.07 + 0.12*t + sun64*2.5,
		Z: 0.12 + 0.22*t + sun64*1.5,
	}
}

// cloudySky: overcast blue daytime with soft white/grey clouds.
func cloudySky(d vec.V, time float64) vec.V {
	up := smoothstepf(-0.05, 0.7, d.Y)
	base := mixV(vec.V{X: 0.72, Y: 0.80, Z: 0.88}, vec.V{X: 0.30, Y: 0.50, Z: 0.80}, up)

	drift := time * 0.04
	density := texture.FBM(d.X*2.4+drift, d.Y*2.4, d.Z*2.4, 5)*0.5 + 0.5
	cover := smoothstepf(0.42, 0.72, density) * smoothstepf(-0.02, 0.12, d.Y)
	shade := texture.FBM(d.X*1.2+drift, d.Y*1.2+9, d.Z*1.2, 3)*0.5 + 0.5
	cloud := mixV(vec.V{X: 0.50, Y: 0.53, Z: 0.58}, vec.V{X: 1.05, Y: 1.05, Z: 1.04}, shade)

	col := mixV(base, cloud, cover)

	// Soft, hazy sun.
	sun := fmax(0, d.X*0.4+d.Y*0.85+d.Z*-0.4)
	s2 := sun * sun
	s4 := s2 * s2
	s8 := s4 * s4
	glow := s8 * s8 // sun^16
	return col.Add(vec.V{X: 0.5, Y: 0.48, Z: 0.42}.Scale(glow * (1 - 0.7*cover)))
}

// nightStarsSky: clear night with a deep-blue gradient and a sparse starfield.
func nightStarsSky(d vec.V) vec.V {
	up := smoothstepf(0, 0.7, d.Y)
	base := mixV(vec.V{X: 0.020, Y: 0.030, Z: 0.060}, vec.V{X: 0.004, Y: 0.008, Z: 0.022}, up)
	if d.Y > 0 {
		s := starField(d, 90)
		base = base.Add(vec.V{X: s, Y: s, Z: s * 1.08})
	}
	// Faint cool moon glow high in the sky.
	moon := fmax(0, d.X*-0.3+d.Y*0.85+d.Z*-0.4)
	m2 := moon * moon
	m4 := m2 * m2
	m8 := m4 * m4
	base = base.Add(vec.V{X: 0.10, Y: 0.12, Z: 0.16}.Scale(m8 * m8))
	return base
}

// nightStormSky: dramatic moonlit storm clouds — dark and high-contrast, with
// moonlight breaking through the thin gaps near the moon (cf. the reference).
func nightStormSky(d vec.V, time float64) vec.V {
	moonColor := vec.V{X: 0.62, Y: 0.70, Z: 0.92}
	moonDir := vec.V{X: 0.08, Y: 0.42, Z: -0.90}.Normalize()
	g := clamp(d.Dot(moonDir), 0, 1)
	g2 := g * g
	g4 := g2 * g2
	g8 := g4 * g4
	broad := g8     // g^8 halo, falls off quickly
	core := g8 * g8 // g^16 bright disc

	// The lit sky seen through cloud gaps: dark, with a tight glow at the moon.
	base := mixV(vec.V{X: 0.018, Y: 0.026, Z: 0.045}, vec.V{X: 0.005, Y: 0.009, Z: 0.020}, smoothstepf(0, 0.8, d.Y))
	skyGlow := base.Add(moonColor.Scale(0.7*broad + 1.2*core))

	drift := time * 0.025
	n := texture.Turbulence(d.X*1.8+drift, d.Y*1.8+3, d.Z*1.8, 6) // billowy ~[0,1]
	cover := smoothstepf(0.06, 0.26, n)                           // heavy storm coverage

	// Dark cloud body, faintly backlit by the moon near the halo.
	cloud := vec.V{X: 0.012, Y: 0.017, Z: 0.030}.Add(moonColor.Scale(0.16 * broad))

	return mixV(skyGlow, cloud, cover)
}

// sunsetSky: warm horizon fading to deep blue, with dark clouds whose edges are
// rimmed by the low sun ("vanilla sky").
func sunsetSky(d vec.V, time float64) vec.V {
	warm := vec.V{X: 1.20, Y: 0.45, Z: 0.18}
	mid := vec.V{X: 0.55, Y: 0.28, Z: 0.42}
	zen := vec.V{X: 0.08, Y: 0.10, Z: 0.26}
	base := mixV(warm, mid, smoothstepf(-0.05, 0.28, d.Y))
	base = mixV(base, zen, smoothstepf(0.18, 0.75, d.Y))

	sunDir := vec.V{X: 0.30, Y: 0.05, Z: -0.95}.Normalize()
	sd := clamp(d.Dot(sunDir), 0, 1)
	sd2 := sd * sd
	sd4 := sd2 * sd2
	sd8 := sd4 * sd4
	disc := sd8 * sd8 * sd8 // tight sun disc/glow
	base = base.Add(vec.V{X: 1.6, Y: 0.9, Z: 0.4}.Scale(disc + 0.25*sd8))

	drift := time * 0.03
	density := texture.FBM(d.X*2.1+drift, d.Y*2.1+5, d.Z*2.1, 5)*0.5 + 0.5
	cover := smoothstepf(0.4, 0.7, density) * smoothstepf(-0.03, 0.15, d.Y)
	rim := smoothstepf(0.38, 0.52, density) * (0.25 + 0.75*sd4)
	cloud := vec.V{X: 0.08, Y: 0.05, Z: 0.09} // dark silhouette
	cloud = cloud.Add(vec.V{X: 1.4, Y: 0.65, Z: 0.28}.Scale(rim))

	return mixV(base, cloud, cover)
}

// starField returns the brightness of a sparse procedural starfield along d.
// Each cell of a quantized direction grid may hold one jittered star.
func starField(d vec.V, scale float64) float64 {
	x, y, z := d.X*scale, d.Y*scale, d.Z*scale
	ix, iy, iz := math.Floor(x), math.Floor(y), math.Floor(z)
	h := hash3(ix, iy, iz)
	if h < 0.94 {
		return 0
	}
	// Jittered star position within the cell (round, in all three axes).
	jx := hash3(ix+1.3, iy, iz)
	jy := hash3(ix, iy+5.1, iz)
	jz := hash3(ix, iy, iz+2.7)
	dx := x - ix - jx
	dy := y - iy - jy
	dz := z - iz - jz
	d2 := dx*dx + dy*dy + dz*dz
	bright := (h - 0.94) / 0.06 // 0..1 across the populated cells
	return bright * bright * fmax(0, 1-d2*16)
}

// hash3 hashes an integer-ish lattice point to a pseudo-random value in [0,1).
func hash3(x, y, z float64) float64 {
	s := math.Sin(x*127.1+y*311.7+z*74.7) * 43758.5453
	return s - math.Floor(s)
}

func mixV(a, b vec.V, t float64) vec.V { return a.Add(b.Sub(a).Scale(t)) }

func smoothstepf(e0, e1, x float64) float64 {
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

// Trace evaluates the radiance for a primary ray with no texture caching.
func (tr *Tracer) Trace(r vec.Ray) vec.V { return tr.trace(r, nil) }

// TracePixel is Trace with a per-pixel texture cache slot (see Texel). The
// renderer passes a stable per-pixel slot so a static view reuses primary-hit
// texture evaluations across frames. tc may be nil.
func (tr *Tracer) TracePixel(r vec.Ray, tc *Texel) vec.V { return tr.trace(r, tc) }

func (tr *Tracer) trace(r vec.Ray, tc *Texel) vec.V { return tr.li(r, 0, tc) }

// maxBounces caps the reflection/refraction recursion depth (depths 0..2).
const maxBounces = 3

// li returns the incoming radiance along ray r at the given bounce depth. Only
// the primary ray (depth 0) carries the per-pixel texture cache tc; reflection
// and refraction rays wander, so caching them would mostly thrash — they pass
// nil and evaluate textures directly.
func (tr *Tracer) li(r vec.Ray, depth int, tc *Texel) vec.V {
	var h hit
	if !tr.intersect(r, &h, tc) {
		return tr.sky(r.Dir)
	}
	if h.mat == scene.MatEmit {
		return h.albedo
	}

	n := h.n
	if n.Dot(r.Dir) > 0 {
		n = n.Neg()
	}
	ep := h.p.Add(n.Scale(5e-4))
	reflective := depth < maxBounces-1 && tr.Opts.Mirror

	// Reflective materials: mirror and metal.
	if (h.mat == scene.MatMirror || h.mat == scene.MatMetal) && reflective {
		rd := tr.reflectDir(r.Dir, n, h.p, h.rough)
		refl := tr.li(vec.Ray{Origin: ep, Dir: rd}, depth+1, nil)
		return h.albedo.Scale(0.96).Mul(refl)
	}

	// Tinted, rough glass. Reflection is driven purely by the Fresnel term, so a
	// clear pane (transmit=1) is fully see-through head-on and only turns
	// mirror-like at grazing angles, exactly like real glass. transmit (0..1)
	// lowers clarity by raising the reflective floor: 1 is a clean window, lower
	// values look progressively frostier/more reflective. Albedo tints the
	// transmitted light; rough frosts both the reflected and transmitted lobes.
	if h.mat == scene.MatGlass && reflective {
		ior := h.ior
		if ior == 0 {
			ior = 1.5
		}
		cosi := fmax(0, -r.Dir.Dot(n))
		// Schlick reflectance at normal incidence (R0) for the given IOR.
		r0 := (1 - ior) / (1 + ior)
		r0 = r0 * r0
		fres := r0 + (1-r0)*math.Pow(1-cosi, 5)

		t := h.transmit
		if t == 0 {
			t = 0.9 // default: a mostly-clear pane
		}
		reflectance := fres + (1-fres)*(1-t)

		// Refraction ratio for an air→glass crossing is 1/ior (the pane is treated
		// as a single thin interface). k < 0 is true total internal reflection,
		// which for eta < 1 only occurs in degenerate cases.
		eta := 1.0 / ior
		k := 1 - eta*eta*(1-cosi*cosi)
		tir := k < 0
		w := reflectance
		if tir {
			w = 1
		}

		// Transmitted (see-through) lobe. Skipped near grazing angles where it is
		// almost fully reflected anyway — saving the expensive ray that often
		// marches the whole scene behind the glass.
		var refr vec.V
		if !tir && w < 0.98 {
			cost := math.Sqrt(k)
			rr := r.Dir.Scale(eta).Add(n.Scale(eta*cosi - cost)).Normalize()
			// Refraction magnifies angular jitter far more than reflection (the
			// transmitted ray travels deep into the scene), so frost the
			// transmitted lobe more gently for the same authored roughness.
			rr = tr.jitterDir(rr, h.p, h.rough*0.35)
			refr = h.albedo.Mul(tr.li(vec.Ray{Origin: h.p.Sub(n.Scale(5e-4)), Dir: rr}, depth+1, nil))
		}

		// Reflected lobe. Skip it when its Fresnel weight is negligible, and keep
		// only strong (grazing) reflections at deeper bounces so the ray tree
		// stays bounded — secondary glass reflections contribute little but cost a
		// full recursive trace each.
		reflMin := 0.02
		if depth > 0 {
			reflMin = 0.2
		}
		var refl vec.V
		if w > reflMin {
			rd := tr.reflectDir(r.Dir, n, h.p, h.rough)
			refl = tr.li(vec.Ray{Origin: ep, Dir: rd}, depth+1, nil)
		}

		if tir {
			return refl
		}
		return mixV(refr, refl, reflectance)
	}

	lit := tr.shade(&h, n, ep)

	// Semi-reflective diffuse/checker surfaces blend a reflection ray on top of
	// their normal (textured, lit) shading, so the surface keeps its structure
	// while picking up a partial mirror image (e.g. a glossy tiled floor).
	if h.reflect > 0 && reflective && (h.mat == scene.MatDiffuse || h.mat == scene.MatChecker) {
		rd := tr.reflectDir(r.Dir, n, h.p, h.rough)
		refl := tr.li(vec.Ray{Origin: ep, Dir: rd}, depth+1, nil)
		lit = mixV(lit, refl, h.reflect)
	}
	return lit
}

// jitterDir perturbs a (unit) direction by a cheap position-hashed offset when
// rough > 0, giving a blurry/glossy lobe without the cost of stochastic
// sampling. The same hash is used for reflection and refraction so a rough
// surface frosts both what it mirrors and what it sees through.
func (tr *Tracer) jitterDir(d, p vec.V, rough float64) vec.V {
	if rough <= 0 {
		return d
	}
	return d.Add(vec.V{
		X: math.Sin(p.X*73.1+p.Y*17.3) * 0.5 * rough,
		Y: math.Sin(p.Y*91.7+p.Z*37.1) * 0.5 * rough,
		Z: math.Sin(p.Z*53.3+p.X*61.7) * 0.5 * rough,
	}).Normalize()
}

// reflectDir returns the mirror reflection of dir about n, perturbed by rough.
func (tr *Tracer) reflectDir(dir, n, p vec.V, rough float64) vec.V {
	return tr.jitterDir(dir.Reflect(n), p, rough)
}

// shade computes the diffuse direct lighting at a hit: ambient (flat or
// hemispheric), the directional sun, point lights and flickering campfires,
// scaled by baked ambient occlusion.
func (tr *Tracer) shade(h *hit, n, ep vec.V) vec.V {
	// Ambient is either the original flat term or, for outdoor scenes, a
	// hemispheric sky/ground ambient.
	env := &tr.Scene.Env
	var lit vec.V
	if env.HasAmbient() {
		up := 0.5 + 0.5*n.Y
		amb := env.AmbientSky.Scale(up).Add(env.AmbientGround.Scale(1 - up))
		lit = h.albedo.Mul(amb)
	} else {
		lit = h.albedo.Scale(0.04)
	}
	// Directional sun (no distance falloff), with a hard shadow.
	if env.HasSun() {
		toSun := env.SunDir.Neg()
		if ndl := n.Dot(toSun); ndl > 0 {
			if !(tr.Opts.Shadow && tr.shadowed(ep, toSun, 1e4)) {
				lit = lit.Add(env.SunColor.Scale(ndl).Mul(h.albedo))
			}
		}
	}
	for i := range tr.Scene.Lights {
		L := &tr.Scene.Lights[i]
		lit = tr.addPointLight(lit, h.p, h.albedo, n, ep, L.Pos, L.Color, tr.lightCullR2[i], tr.lightInvR2[i])
	}
	// Campfires expand into a few warm sub-lights whose position and intensity
	// flicker with the animation clock. Each sub-light casts its own shadow ray
	// so its jittering position makes the shadows dance.
	//
	// As a fast early-out we first test a single ray toward the fire core: if
	// that is blocked (e.g. the fire is behind a wall), every sub-light is
	// occluded too, so we skip the whole cluster with one ray instead of three.
	// When the core is visible we fall through to the per-sub-light shadow rays
	// that produce the dancing.
	for ci := range tr.Scene.Campfires {
		f := &tr.Scene.Campfires[ci]
		cr2, inv := tr.fireCullR2[ci], tr.fireInvR2[ci]
		cl := f.Center.Sub(h.p)
		cd2 := cl.LenSq()
		if cd2 > cr2 {
			continue
		}
		if tr.Opts.Shadow {
			cdist := math.Sqrt(cd2)
			if cdist == 0 {
				cdist = 1
			}
			if tr.shadowed(ep, cl.Scale(1/cdist), cdist) {
				continue
			}
		}
		for j := 0; j < scene.CampfireLights; j++ {
			pos, color := f.LightAt(j, tr.Time)
			lit = tr.addPointLight(lit, h.p, h.albedo, n, ep, pos, color, cr2, inv)
		}
	}

	if tr.Opts.AO {
		lit = lit.Scale(tr.ambientOcclusion(ep, n))
	}
	return lit
}

// aoMaxDist is the occlusion probe range; hits beyond it are ignored. It is the
// radius used when baking the AO volume (see aovolume.go).
const aoMaxDist = 0.9

// tonemapChannel applies the ACES-style filmic curve used by the original.
func tonemapChannel(x float64) float64 {
	return clamp((x*(2.51*x+0.03))/(x*(2.43*x+0.59)+0.14), 0, 1)
}

// gammaLUT precomputes the 1/2.2 gamma encode over the [0,1] domain (which is
// exactly the range tonemapChannel produces), replacing a per-pixel math.Pow.
const gammaLUTSize = 4096

var gammaLUT [gammaLUTSize]float64

func init() {
	for i := range gammaLUT {
		x := float64(i) / (gammaLUTSize - 1)
		gammaLUT[i] = math.Pow(x, 1/2.2)
	}
}

// gamma applies the 1/2.2 gamma encode via the lookup table. Inputs are
// expected in [0,1]; values outside are clamped.
func gamma(x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	return gammaLUT[int(x*(gammaLUTSize-1)+0.5)]
}

// ToneMap converts a linear HDR color to a gamma-encoded [0,1] color.
func ToneMap(c vec.V) vec.V {
	return vec.V{
		X: gamma(tonemapChannel(c.X)),
		Y: gamma(tonemapChannel(c.Y)),
		Z: gamma(tonemapChannel(c.Z)),
	}
}
