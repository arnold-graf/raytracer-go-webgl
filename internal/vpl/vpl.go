// Package vpl approximates a scene's first bounce of indirect light with a
// small set of virtual point lights.
//
// The idea is Instant Radiosity's: shoot photons from the emitters, and wherever
// one lands on a diffuse surface, leave a point light behind carrying what that
// surface would re-emit. The difference here is the budget. Instant Radiosity
// wants thousands of VPLs and resamples them per pixel; this keeps a few dozen,
// clustered by how much light they actually carry, because a shadow ray in the
// megakernel measures 7.9 ns and a VPL that every pixel evaluates is a real
// fraction of the frame.
//
// Why this shape rather than another probe field: everything in docs/live-gi.md
// that went wrong with DDGI here went wrong because a software BVH cannot feed
// a probe grid enough rays to resolve a room — leaking, diamonds, rosettes, pop.
// A VPL has no resolution to run out of. It is a light, and the renderer's light
// path is already clustered (internal/webgpu/lightgrid.go), cull-bounded,
// penumbra-filtered, and gated on whether a shadow ray would be visible at all
// (SHADOW_SKIP_LEVELS in types.wesl). Generating VPLs adds scene data, not a
// subsystem, and the result is a [[light]] an author can move, retint or delete.
//
// Off unless RAYTRACER_VPL is set. With it unset Generate is never called and
// the scene is untouched.
package vpl

import (
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"raytracer/internal/bvh"
	"raytracer/internal/gpuscene"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// lightAttenKnee mirrors LIGHT_ATTEN_KNEE in shaders/modules/types.wesl. Keep in
// sync: a VPL's colour is only meaningful if the CPU that sizes it and the
// shader that evaluates it agree on the falloff curve.
const lightAttenKnee = 0.01

// Options controls generation. The zero value generates nothing.
type Options struct {
	// Count is how many VPLs survive clustering. This is the real budget knob:
	// every VPL is a light in the grid, and one that reaches the whole screen
	// costs a shadow ray per pixel unless it is dim enough for the renderer's
	// own SHADOW_SKIP_LEVELS gate to drop it.
	Count int

	// Rays is how many photons each emitter casts. It buys placement accuracy,
	// not brightness — a VPL's colour carries a 1/Rays factor, so doubling this
	// halves each candidate and leaves the total bounce where it was.
	Rays int

	// Gain scales the whole bounce. It exists because the renderer's falloff is
	// not inverse-square (see light_atten in shade.wesl: a bounded curve with a
	// knee, chosen to kill banding rather than to be physical), so there is no
	// exact constant that converts emitted flux into a VPL intensity. Calibrate
	// it against cmd/pathtrace -debug indirect.
	Gain float64

	// Radius is the emitter size each VPL declares, in metres, which is what the
	// soft-shadow pass sizes its penumbra from. Bounce light is broad, so this
	// wants to be much larger than a bulb's.
	Radius float64

	// Reach caps how far a VPL carries, in metres; each one otherwise inherits
	// the reach of the emitter whose bounce it is (see vplReach). 0 removes the
	// cap. A finite reach is what stops a bounce light from lighting the far side
	// of the wall it sits on, and is cheaper than asking it for visibility.
	//
	// 24 is measured, not picked: at that cap matching the path tracer's mean
	// wants gain 1.03 on the villa hearth and 1.40 on the office server room,
	// the closest the two scenes come to agreeing. At 12 they are 0.80 and 3.99.
	Reach float64

	// Cone is the emission cone around the bounce normal, in degrees. A bounce
	// leaves the hemisphere it landed on, so this should be wide but under 180 —
	// at 180 the renderer treats the light as omnidirectional and the VPL lights
	// through its own surface. 0 means omnidirectional.
	Cone float64

	// MinLevels drops a cluster whose brightest channel could not move a black
	// pixel by this many display levels. Below about one level a VPL cannot
	// change the image but still costs a slot in every light-grid cell it
	// touches.
	MinLevels float64

	// Seed fixes the photon directions. Generation is deterministic in it, which
	// is the property that makes the result a scene asset rather than noise:
	// same scene, same VPLs, same frame every time.
	Seed uint64

	// Dump, when set, writes the generated lights as a TOML fragment to this
	// path — the handoff from "generated" to "authored".
	Dump string

	// ShadowLevels is how many display levels a VPL must be worth before its
	// shadow is traced, overriding the shader's SHADOW_SKIP_LEVELS for these
	// lights only. It is the cost knob that does not change how many lights
	// there are.
	//
	// A shadow ray is what a light actually costs. Measured on the night villa
	// interior at 1024x640, 48 VPLs per room took shadow rays from 576k to 2.85M
	// a frame and the frame from 19.6 ms to 27.9 ms — and 99.8% of those rays
	// came back blocked, so nearly all of that traversal bought the answer "this
	// bounce does not reach here". Raising the bar concentrates the rays on the
	// VPLs bright enough for their shadow to be visible.
	//
	// A large value effectively turns VPL shadows off. That is a defensible end
	// of the range rather than an abuse of it: a VPL is a point standing in for
	// a lit patch, and a patch's shadow is soft and weak — the detail the
	// approximation is least entitled to.
	ShadowLevels float64

	// IgnoreRegions drops the scene's [vpl] regions and ranks the whole level
	// against one global budget, which is what this package did before regions
	// existed. It is the A/B switch: the case for a region is a measurement
	// against the same scene without one, and that comparison needs to be
	// available without editing the scene files.
	IgnoreRegions bool
}

// DefaultOptions are what RAYTRACER_VPL=<n> selects with nothing else set.
//
// Count 24 and Rays 512 come from the villa hearth: below about 16 the bounce
// pools into visible blobs, and above 512 rays the clusters stop moving.
//
// Gain is 2*pi, the solid angle a bounce leaves into. That is a plausible
// constant rather than a derived one — the renderer's falloff is not
// inverse-square, so there is no exact conversion — but it is the number the
// measurement landed on: against cmd/pathtrace -debug indirect on the villa
// hearth room, matching the reference's mean linear radiance wanted 6.61, and
// 2*pi is 6.28. Recalibrate with that view if the falloff constants move.
func DefaultOptions() Options {
	return Options{
		Count:     24,
		Rays:      512,
		Gain:      2 * math.Pi,
		Radius:    1.5,
		Reach:     24,
		Cone:      170,
		MinLevels: 1.0,
		// 16 is measured, not picked. On the villa hearth room at 1024x640 with
		// 48 VPLs each, exact shadows cost 129.9 ms; at 16 levels 72.3 ms; with
		// VPL shadows off entirely 59.1 ms against 53.1 ms for no VPLs at all.
		// 16 keeps the mechanism adaptive — a bounce bright enough to matter
		// still casts — for 6.5% more light in the room, where off is 9.2%.
		ShadowLevels: 16,
		Seed:         0x9e3779b97f4a7c15,
	}
}

// FromEnv reads the options from the environment.
//
// Count is the **global** budget: what candidates landing in no [vpl] region
// compete for. With RAYTRACER_VPL unset or zero it is 0, and the returned bool
// is false — meaning the environment asked for no scene-wide bounce. It does
// not mean nothing will be generated: a scene whose objects declare [vpl]
// regions still gets those, because an authored budget is scene data and not a
// debug flag. See optionsFor.
//
// Every other knob is read either way, so an author can retune a region's
// generation without switching a global budget on to do it.
func FromEnv() (Options, bool) {
	o := DefaultOptions()
	o.Count = 0
	global := false
	if raw := strings.TrimSpace(os.Getenv("RAYTRACER_VPL")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			o.Count, global = n, true
		}
	}
	envInt("RAYTRACER_VPL_RAYS", &o.Rays)
	envFloat("RAYTRACER_VPL_GAIN", &o.Gain)
	envFloat("RAYTRACER_VPL_RADIUS", &o.Radius)
	envFloat("RAYTRACER_VPL_REACH", &o.Reach)
	envFloat("RAYTRACER_VPL_CONE", &o.Cone)
	envFloat("RAYTRACER_VPL_MIN_LEVELS", &o.MinLevels)
	envFloat("RAYTRACER_VPL_SHADOW_LEVELS", &o.ShadowLevels)
	if s := os.Getenv("RAYTRACER_VPL_SEED"); s != "" {
		if v, err := strconv.ParseUint(s, 10, 64); err == nil {
			o.Seed = v
		}
	}
	o.Dump = os.Getenv("RAYTRACER_VPL_DUMP")
	if v := strings.TrimSpace(os.Getenv("RAYTRACER_VPL_REGIONS")); v == "0" || v == "false" {
		o.IgnoreRegions = true
	}
	return o, global
}

// optionsFor resolves what to generate for one scene: the environment's options,
// plus whether there is any work to do at all.
//
// There is work when the environment asked for a global budget, or when the
// scene's own objects declare [vpl] regions. That second clause is the whole
// point of an authored region — a building that says it wants twenty-four bounce
// lights is describing itself, the same way it describes its walls, and it
// should not need a launch flag to be lit. RAYTRACER_VPL then controls only the
// scene-wide leftover pool.
//
// RAYTRACER_VPL_REGIONS=0 drops the regions, so with it set and no global count
// this reports no work: that pair is the complete off switch.
func optionsFor(s *scene.Scene) (Options, bool) {
	o, global := FromEnv()
	if global {
		return o, true
	}
	return o, len(validRegions(s, o)) > 0
}

func envInt(name string, dst *int) {
	if s := os.Getenv(name); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			*dst = v
		}
	}
}

func envFloat(name string, dst *float64) {
	if s := os.Getenv(name); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			*dst = v
		}
	}
}

// Inject appends generated VPLs to s when RAYTRACER_VPL is set, and does nothing
// at all otherwise. It is called once at the end of scene load, so every
// consumer — the app, gpuprof, the path tracer — sees the same scene.
func Inject(s *scene.Scene) {
	if s == nil {
		return
	}
	o, ok := optionsFor(s)
	if !ok {
		return
	}
	start := time.Now()
	added, stats := generate(s, o)
	took := time.Since(start)
	if len(added) == 0 {
		log.Printf("vpl: no bounce found (no emitter reaches a diffuse surface)")
		return
	}
	if o.Dump != "" {
		if err := WriteTOML(o.Dump, added); err != nil {
			log.Printf("vpl: dump failed: %v", err)
		} else {
			log.Printf("vpl: wrote %d lights to %s", len(added), o.Dump)
		}
	}
	base := len(s.Lights)
	s.Lights = append(s.Lights, added...)
	s.Touch()
	// So a later Toggle knows how to take these off again.
	register(s, added, base)
	log.Printf("vpl: %d virtual lights in %s (%d rays/emitter, gain %.2f, reach %.0f m)",
		len(added), took.Round(time.Millisecond), o.Rays, o.Gain, o.Reach)
	// One line per region. A region whose box misses the room it meant to cover
	// shows up here as candidates 0 — which is otherwise invisible, because the
	// scene still renders, just without the bounce the author asked for.
	for _, st := range stats {
		log.Printf("vpl:   %-28s %2d/%-2d lights from %d candidates",
			st.label, st.kept, st.budget, st.cands)
	}
}

// emitter is one photon source: a scene light, or one of a campfire's
// sub-lights. Campfires are resolved through scene.Campfire.LightAt, the same
// function the renderer uses, rather than a second copy of the flicker maths —
// an independent shading path in this feature has already dropped campfires
// once (docs/live-gi.md).
type emitter struct {
	pos   vec.V
	color vec.V
	// cullR2 and invR2 are the falloff window, exactly as packLight computes it.
	cullR2 float64
	invR2  float64
	// axis and cosOuter restrict a spot to its cone; cosOuter <= 0 is omni.
	axis     vec.V
	cosOuter float64
}

// Generate traces the scene's first bounce and returns the VPLs standing in for
// it. It does not modify s.
func Generate(s *scene.Scene, o Options) []scene.Light {
	lights, _ := generate(s, o)
	return lights
}

// generate is Generate that also reports the per-region split, for logging.
func generate(s *scene.Scene, o Options) ([]scene.Light, []regionStat) {
	if s == nil || o.Rays <= 0 {
		return nil, nil
	}
	// A global budget, a region, or both. With neither there is nothing to
	// rank and the photon trace would be wasted work.
	regions := validRegions(s, o)
	if o.Count <= 0 && len(regions) == 0 {
		return nil, nil
	}
	ems := collectEmitters(s)
	if len(ems) == 0 {
		return nil, nil
	}
	accel := bvh.New(s)
	aim := newAimer(s, accel)
	rng := &pcg{state: o.Seed | 1}
	var cands []candidate
	for i := range ems {
		cands = append(cands, shoot(s, accel, &ems[i], aim, o, rng)...)
	}
	if len(cands) == 0 {
		return nil, nil
	}
	return generateByRegion(cands, regions, o)
}

// regionStat is one line of the generator's report: what a region asked for and
// what it got. Returned so Inject can log the split, which is the only way an
// author can tell a region that is working from one whose box misses the room.
type regionStat struct {
	label  string
	budget int
	cands  int
	kept   int
}

// validRegions returns the scene's usable VPL regions, smallest first.
//
// Smallest first is what makes overlap sane: assignment takes the first region
// that contains the point, so a box drawn around one hall wins over the box
// around the villa that contains it. Without an order the result would depend
// on include order, which is not something an author should have to reason
// about.
func validRegions(s *scene.Scene, o Options) []scene.VPLRegion {
	if o.IgnoreRegions {
		return nil
	}
	var out []scene.VPLRegion
	for _, r := range s.VPLRegions {
		if r.Valid() {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Volume() < out[j].Volume() })
	return out
}

// generateByRegion is the per-region budget: every candidate is assigned to the
// region it landed in, and each region clusters and ranks only its own.
//
// The problem it solves is that a global power ranking is not a ranking of
// usefulness. Measured on the night villa at Count 24, eleven slots went to a
// mountain campfire 160 m away and 80 m up, four to the second villa, and the
// apartment got none at any count — so getting 24 lights into the room you are
// standing in meant paying for about 80. Ranking inside a box makes a slot cost
// what it is worth locally.
//
// **Assignment is by where the photon landed, never by where its emitter was.**
// A campfire in the garden is traced exactly as before; the bounce it leaves on
// the hall floor is inside the hall's box, so the hall's budget pays for it and
// the hall gets lit. A region can move a slot between pools. It cannot remove an
// emitter from the trace, and so cannot delete light that would otherwise exist.
func generateByRegion(cands []candidate, regions []scene.VPLRegion, o Options) ([]scene.Light, []regionStat) {
	if len(regions) == 0 {
		if o.Count <= 0 {
			return nil, nil
		}
		return toLights(cluster(cands, o.Count, o.Reach), o), nil
	}
	buckets := make([][]candidate, len(regions))
	var rest []candidate
	for _, c := range cands {
		if i := regionFor(regions, c.pos); i >= 0 {
			buckets[i] = append(buckets[i], c)
			continue
		}
		rest = append(rest, c)
	}

	var out []scene.Light
	stats := make([]regionStat, 0, len(regions)+1)
	for i := range regions {
		ro := o.forRegion(regions[i])
		got := toLights(cluster(buckets[i], ro.Count, ro.Reach), ro)
		out = append(out, got...)
		stats = append(stats, regionStat{
			label: regions[i].Label, budget: ro.Count, cands: len(buckets[i]), kept: len(got),
		})
	}
	// Everything outside every region competes for the scene-wide count, so
	// declaring a region narrows where the global budget goes without switching
	// the rest of the level off. With no global budget asked for, the leftovers
	// are simply not placed: an unflagged launch gets exactly the lights the
	// scene's objects authored, and nothing scattered across the rest of the
	// level. Skipped rather than clustered to zero, because clustering a few
	// thousand leftover candidates to keep none of them is pure waste.
	if o.Count > 0 {
		got := toLights(cluster(rest, o.Count, o.Reach), o)
		out = append(out, got...)
		stats = append(stats, regionStat{label: "(scene)", budget: o.Count, cands: len(rest), kept: len(got)})
	}
	return out, stats
}

// regionFor returns the index of the first region containing p, or -1. regions
// must be sorted smallest-first; see validRegions.
func regionFor(regions []scene.VPLRegion, p vec.V) int {
	for i := range regions {
		if regions[i].Contains(p) {
			return i
		}
	}
	return -1
}

// forRegion overlays a region's overrides on the generator's options. An unset
// (zero) override inherits, so `[vpl] lights = 24` alone changes only the budget
// and leaves the calibrated gain and reach exactly where they were.
func (o Options) forRegion(r scene.VPLRegion) Options {
	ro := o
	ro.Count = r.Lights
	if r.Gain > 0 {
		ro.Gain = r.Gain
	}
	if r.Reach > 0 {
		ro.Reach = r.Reach
	}
	if r.ShadowLevels > 0 {
		ro.ShadowLevels = r.ShadowLevels
	}
	return ro
}

// collectEmitters lists every photon source, with the same falloff window the
// renderer will evaluate it through.
func collectEmitters(s *scene.Scene) []emitter {
	var out []emitter
	for i := range s.Lights {
		l := &s.Lights[i]
		cullR2, invR2 := lightCull(l.Color, l.Range)
		if cullR2 <= 0 {
			continue
		}
		e := emitter{pos: l.Pos, color: l.Color, cullR2: cullR2, invR2: invR2}
		if l.IsSpot() {
			e.axis = l.Dir.Normalize()
			e.cosOuter = math.Cos(l.ConeDeg * 0.5 * math.Pi / 180)
		}
		out = append(out, e)
	}
	// Campfires are the case this feature exists for on the villa: the hearth is
	// a campfire, not a [[light]], and its bounce is the red the scene wants.
	for i := range s.Campfires {
		cf := &s.Campfires[i]
		cullR2, invR2 := campfireCull(cf)
		if cullR2 <= 0 {
			continue
		}
		n := cf.LightCount()
		for j := 0; j < n; j++ {
			// t = 0: the VPL set is a static scene asset, so it is generated at
			// the animation's zero phase rather than chasing the flicker. The
			// flicker is +-Flicker about that, which moves the bounce's
			// brightness and not the geometry it comes from.
			pos, col := cf.LightAt(j, 0)
			if col.LenSq() <= 0 {
				continue
			}
			out = append(out, emitter{pos: pos, color: col, cullR2: cullR2, invR2: invR2})
		}
	}
	return out
}

// candidate is one photon's landing: a point on a diffuse surface and the
// radiance it re-emits.
type candidate struct {
	pos    vec.V
	normal vec.V
	color  vec.V
	power  float64
	// reach is how far this bounce should carry, inherited from the emitter that
	// produced it. See vplReach.
	reach float64
}

// PHOTON_SEGS bounds how far a photon is followed through specular surfaces
// before it is abandoned. Four is enough for a glass box (in one wall, out the
// other) with a mirror on either side of it.
const photonSegs = 4

// shoot casts o.Rays photons from one emitter and returns where they landed.
func shoot(s *scene.Scene, accel *bvh.BVH, e *emitter, aim aimer, o Options, rng *pcg) []candidate {
	out := make([]candidate, 0, o.Rays)
	reach := math.Sqrt(e.cullR2)
	// Each photon stands for 1/Rays of the emitter, so the total bounce this
	// emitter contributes does not depend on how finely it was sampled. The
	// aiming solid angle rides in the same factor: a photon fired into a narrow
	// cone stands for proportionally less of the sphere.
	//
	// Gain is deliberately *not* folded in here. It is applied in toLights, so
	// that a region declaring its own gain scales exactly its own candidates —
	// a candidate cannot know which region will claim it until it has landed.
	// Ranking is unaffected either way: gain is one positive constant across a
	// pool, and every comparison in clustering is within a pool.
	scale := 1 / float64(o.Rays)
	for i := 0; i < o.Rays; i++ {
		var dir vec.V
		w := scale
		switch {
		case e.cosOuter > 0:
			dir = sampleCone(e.axis, e.cosOuter, rng)
		default:
			var frac float64
			dir, frac = aim.sample(e.pos, rng)
			w *= frac
		}
		// Follow the photon through specular surfaces. Only a diffuse hit can
		// hold a VPL: a mirror sends the photon on and a pane of glass lets it
		// past, and stopping at either drops light that has not landed yet.
		// That is not a detail — office-sunset's skyway is a glass tube, so
		// every sun photon aimed at its floor died on the west wall and the
		// whole bridge generated no bounce at all.
		ro, rd := e.pos, dir
		tw := vec.New(1, 1, 1)
		travelled := 0.0
		for seg := 0; seg < photonSegs; seg++ {
			t, kind, idx := accel.Nearest(vec.Ray{Origin: ro, Dir: rd})
			p, ok := nearestPlane(s, vec.Ray{Origin: ro, Dir: rd})
			if ok && p.t < t {
				t, kind, idx = p.t, kindPlane, p.idx
			}
			if kind < 0 || t <= 0 || math.IsInf(t, 1) {
				break
			}
			travelled += t
			if travelled*travelled > e.cullR2 {
				break
			}
			hit := ro.Add(rd.Scale(t))
			sf, n, ok := surfaceAt(s, kind, idx, hit, vec.Ray{Origin: ro, Dir: rd}, t)
			if !ok {
				break
			}
			switch sf.Mat {
			case scene.MatMirror, scene.MatMetal:
				tw = tw.Mul(sf.Albedo)
				ro = hit.Add(n.Scale(surfEps))
				rd = rd.Sub(n.Scale(2 * rd.Dot(n)))
				continue
			case scene.MatGlass:
				// Straight through rather than refracted, matching
				// gi_probe_trace: which part of the garden a window sees is not
				// detail a point light can hold either.
				transmit := sf.Transmit
				if transmit <= 0 {
					transmit = 0.9
				}
				tw = tw.Mul(sf.Albedo).Scale(transmit)
				ro = hit.Add(rd.Scale(surfEps))
				continue
			case scene.MatEmit:
				// Already a light; a VPL here would double count it.
				seg = photonSegs
				continue
			}
			if sf.Mat != scene.MatDiffuse && sf.Mat != scene.MatChecker {
				break
			}
			// Orientation only. The incidence cosine must *not* scale the
			// photon: a grazing patch intercepts fewer photons per unit area
			// already, because it subtends a smaller solid angle at the emitter.
			// Applying it again is the same double count as the falloff below.
			cos := -n.Dot(rd)
			if cos <= 0 {
				n = n.Scale(-1)
				cos = -cos
			}
			if cos <= 1e-4 {
				break
			}
			d2 := travelled * travelled
			att := attenNorm(d2) * rangeWindow(d2, e.invR2)
			if att <= 0 {
				break
			}
			col := e.color.Mul(sf.Albedo).Mul(tw).Scale(att * w)
			pw := peak(col)
			if pw > 0 {
				out = append(out, candidate{pos: hit, normal: n, color: col, power: pw, reach: reach})
			}
			break
		}
	}
	return out
}

// surfEps nudges a photon off the surface it just hit, matching the tracer's own
// ray epsilon.
const surfEps = 1e-3

// cluster folds the candidates down to at most count VPLs.
//
// Energy is conserved rather than averaged: merging sums the colours and moves
// the representative to the power-weighted centroid. Averaging would make the
// result depend on how many photons happened to land, which is the bug the live
// GI grid's gather had in the other direction (docs/live-gi.md, "the gather must
// average, not sum" — there the quantity was an incident radiance and here it is
// emitted power, so the rule inverts).
//
// The merge radius comes from the VPL's own reach, not from the candidate
// cloud's extent. Seeding it from the cloud was the obvious choice and it is
// wrong on any scene with an outdoor emitter: office-sunset's sun scatters
// photons across the whole city, so the cloud is hundreds of metres wide, the
// first radius is metres, and a server room's distinct bounces merge into one
// light before the search has taken a step. Reach is the scale that matters —
// two candidates further apart than a VPL can carry are not interchangeable
// however big the level is.
//
// There is deliberately no radius search. Growing the radius until the set fits
// returns the *first* radius that does, which overshoots badly: on office-sunset
// one step took the count from 40-odd clusters to 14, so ten slots went unused
// and the survivors were coarser than they needed to be. One fine pass and an
// explicit fold-down uses the whole budget and keeps the finer positions.
func cluster(cands []candidate, count int, reach float64) []candidate {
	if len(cands) <= count {
		return cands
	}
	r := reach / 16
	if r <= 0 {
		r = 0.5
	}
	out := mergeWithin(cands, r)
	if len(out) <= count {
		return out
	}

	// Keep the strongest and fold each leftover into the nearest survivor, so
	// its light is not simply dropped. The fold is capped at one reach: beyond
	// that the nearest survivor is not somewhere the leftover could have lit, and
	// moving its energy there would be inventing light in a new place rather than
	// approximating it. What that discards is bounded by construction — these are
	// the weakest clusters in the scene.
	sort.SliceStable(out, func(i, j int) bool { return out[i].power > out[j].power })
	kept := out[:count]
	maxFold := reach * reach
	for _, c := range out[count:] {
		best, bestD := -1, math.Inf(1)
		for i := range kept {
			if d := kept[i].pos.Sub(c.pos).LenSq(); d < bestD {
				best, bestD = i, d
			}
		}
		if best >= 0 && bestD <= maxFold {
			kept[best] = merge(kept[best], c)
		}
	}
	return kept
}

// mergeWithin is one greedy pass: strongest candidate first, absorbing every
// later one that is within r and facing the same way.
//
// The normal test matters more than it looks. Two points a metre apart on
// opposite faces of the same wall carry completely different bounce, and merging
// them puts a light inside the wall that leaks into both rooms.
func mergeWithin(cands []candidate, r float64) []candidate {
	order := make([]candidate, len(cands))
	copy(order, cands)
	sort.SliceStable(order, func(i, j int) bool { return order[i].power > order[j].power })

	r2 := r * r
	var out []candidate
	for _, c := range order {
		hit := -1
		for i := range out {
			if out[i].pos.Sub(c.pos).LenSq() <= r2 && out[i].normal.Dot(c.normal) > 0.5 {
				hit = i
				break
			}
		}
		if hit < 0 {
			out = append(out, c)
			continue
		}
		out[hit] = merge(out[hit], c)
	}
	return out
}

func merge(a, b candidate) candidate {
	w := a.power + b.power
	if w <= 0 {
		return a
	}
	return candidate{
		pos:    a.pos.Scale(a.power / w).Add(b.pos.Scale(b.power / w)),
		normal: a.normal.Scale(a.power / w).Add(b.normal.Scale(b.power / w)).Normalize(),
		color:  a.color.Add(b.color),
		power:  peak(a.color.Add(b.color)),
		reach:  a.reach*(a.power/w) + b.reach*(b.power/w),
	}
}

// toLights turns clusters into scene lights, dropping the ones too dim to see.
func toLights(cs []candidate, o Options) []scene.Light {
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].power > cs[j].power })
	out := make([]scene.Light, 0, len(cs))
	for _, c := range cs {
		// Gain lands here rather than in shoot so that a region's own gain
		// applies to exactly the candidates that region kept.
		col := c.color.Scale(o.Gain)
		pw := c.power * o.Gain
		// What this VPL is worth against black, in display levels, judged at half
		// its reach rather than point blank.
		//
		// At zero distance the test passes almost everything: the tonemap is so
		// steep near black that 6e-5 of linear radiance is still three display
		// levels, so a cluster the sun scattered across the far side of the city
		// counted as visible and took a slot. Half the reach is where a VPL does
		// its work, and asking the question there is what separates a bounce
		// that lights a room from one that is merely non-zero.
		d := o.Reach * 0.5
		if d <= 0 {
			d = 6
		}
		if displayLevels(pw*lightAtten(d*d)) < o.MinLevels {
			continue
		}
		l := scene.Light{
			// Lifted off the surface it bounced from: at zero offset the VPL sits
			// exactly on the geometry and every shadow ray toward it starts by
			// hitting that geometry.
			Pos:    c.pos.Add(c.normal.Scale(0.05)),
			Color:  col,
			Radius: o.Radius,
			Range:  vplReach(c.reach, o.Reach),
			// What this bounce must be worth before it pays for a shadow ray.
			ShadowLevels: o.ShadowLevels,
		}
		if o.Cone > 0 && o.Cone < 180 {
			l.Dir = c.normal
			l.ConeDeg = o.Cone
		}
		out = append(out, l)
	}
	return out
}

// vplReach is how far one VPL carries: the reach of the emitter whose bounce it
// is, capped at the configured maximum.
//
// A flat reach for every VPL is wrong, and the server room is where it shows.
// Those racks carry about 250 LEDs of range 0.8, so their light is deposited
// within a metre of each rack — and handing the resulting VPL a 12 m range
// re-emits it over roughly fifteen times the area it originally covered.
// Measured against the path tracer on that view, blue came out 5x the reference
// while red and green were inside 20%: the scene's one population of tiny
// lights, inflated by exactly this.
//
// Deriving it from the VPL's own brightness instead does not work, and it was
// the first thing tried. lightCull's automatic radius is the distance at which a
// light falls under LightCullEps, which for anything bright is hundreds of
// metres — every VPL simply hit the cap and nothing changed.
//
// The cap still matters in the other direction: a bounce off a sunlit floor
// inherits the sun's 200 m and would land in every cell of the light grid.
func vplReach(emitterReach, maxReach float64) float64 {
	if emitterReach <= 0 {
		return maxReach
	}
	if maxReach > 0 && emitterReach > maxReach {
		return maxReach
	}
	return emitterReach
}

// WriteTOML writes the lights as a scene fragment, ready to paste into the file
// that produced them. This is the point of generating lights rather than a
// field: the output is editable.
func WriteTOML(path string, lights []scene.Light) error {
	var b strings.Builder
	b.WriteString("# Generated by internal/vpl (RAYTRACER_VPL). Bounce light from\n")
	b.WriteString("# the scene's own emitters, approximated as point lights.\n")
	b.WriteString("# Safe to edit by hand: these are ordinary [[light]] entries.\n\n")
	for i, l := range lights {
		fmt.Fprintf(&b, "[[light]] # vpl %d\n", i)
		fmt.Fprintf(&b, "pos = [%.3f, %.3f, %.3f]\n", l.Pos.X, l.Pos.Y, l.Pos.Z)
		fmt.Fprintf(&b, "color = [%.5f, %.5f, %.5f]\n", l.Color.X, l.Color.Y, l.Color.Z)
		if l.Radius > 0 {
			fmt.Fprintf(&b, "radius = %.3f\n", l.Radius)
		}
		if l.Range > 0 {
			fmt.Fprintf(&b, "range = %.3f\n", l.Range)
		}
		if l.IsSpot() {
			fmt.Fprintf(&b, "dir = [%.4f, %.4f, %.4f]\n", l.Dir.X, l.Dir.Y, l.Dir.Z)
			fmt.Fprintf(&b, "cone_deg = %.1f\n", l.ConeDeg)
		}
		b.WriteString("\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// ---------------------------------------------------------------- falloff

// attenNorm is what a photon's flux should be scaled by on landing, and it is
// emphatically not light_atten.
//
// A photon carries constant flux however far it travels; the inverse-square
// falloff is already in the statistics, because a distant patch subtends a
// smaller solid angle at the emitter and therefore catches proportionally fewer
// photons. Multiplying by light_atten on top applies the falloff twice, and the
// deposited energy then drops as 1/d^4. Measured on office-sunset: the skyway
// floor sits 47 m from the only light that reaches it, and came out **570x**
// darker than it should — which is why that bridge generated no ceiling bounce
// at all, and most of why the scene was nine times short on the sun-lit view.
//
// What is left is the renderer's own departure from inverse square. light_atten
// is bounded rather than 1/d^2 — it tends to 1/(quadratic * d^2) far away and
// flattens to about 1 at contact — so this is the ratio between the two, which
// is 1 in the far field and tapers in close. That keeps a VPL sized against the
// curve the shader will actually evaluate rather than against the physics the
// shader does not implement.
func attenNorm(d2 float64) float64 {
	return lightAtten(d2) * d2 * gpuscene.LightAttenQuadratic
}

// lightAtten mirrors light_atten() in shaders/modules/shade.wesl, knee included.
func lightAtten(d2 float64) float64 {
	u := gpuscene.LightAttenBase + d2*gpuscene.LightAttenQuadratic
	e := u - 1
	return 1 / (0.5 * (u + 1 + math.Sqrt(e*e+lightAttenKnee)))
}

// rangeWindow is the authored-range term add_point_light_raw applies on top of
// the attenuation: a squared smooth cutoff that reaches zero at the range.
func rangeWindow(d2, invR2 float64) float64 {
	if invR2 <= 0 {
		return 1
	}
	x := d2 * invR2
	w := 1 - x*x
	if w < 0 {
		return 0
	}
	return w * w
}

// lightCull mirrors webgpu.lightCull: the falloff window an authored light gets.
func lightCull(color vec.V, rng float64) (cullR2, invR2 float64) {
	if rng > 0 {
		r2 := rng * rng
		return r2, 1 / r2
	}
	cmax := peak(color)
	if cmax <= gpuscene.LightCullEps*gpuscene.LightAttenBase {
		return 0, 0
	}
	r2 := (cmax/gpuscene.LightCullEps - gpuscene.LightAttenBase) / gpuscene.LightAttenQuadratic
	if r2 < 0 {
		r2 = 0
	}
	return r2, 0
}

// campfireCull mirrors campfire_cull() in trace.wgsl and webgpu.campfireCull.
func campfireCull(cf *scene.Campfire) (cullR2, invR2 float64) {
	if cf.Range > 0 {
		r2 := cf.Range * cf.Range
		return r2, 1 / r2
	}
	p := cf.PeakChannel()
	if p <= gpuscene.LightCullEps*gpuscene.LightAttenBase {
		return 0, 0
	}
	r2 := (p/gpuscene.LightCullEps - gpuscene.LightAttenBase) / gpuscene.LightAttenQuadratic
	if r2 < 0 {
		r2 = 0
	}
	return r2, 0
}

// displayLevels maps one linear radiance channel to its 0..255 output level,
// mirroring display_levels() in math.wesl. It is how MinLevels is judged: the
// question "can this VPL be seen" is a question about the tonemapped image.
func displayLevels(x float64) float64 {
	if x <= 0 {
		return 0
	}
	t := (x * (2.51*x + 0.03)) / (x*(2.43*x+0.59) + 0.14)
	if t <= 0 {
		return 0
	}
	if t > 1 {
		t = 1
	}
	return math.Pow(t, 1/2.2) * 255
}

func peak(v vec.V) float64 { return math.Max(v.X, math.Max(v.Y, v.Z)) }

func minV(a, b vec.V) vec.V {
	return vec.V{X: math.Min(a.X, b.X), Y: math.Min(a.Y, b.Y), Z: math.Min(a.Z, b.Z)}
}

func maxV(a, b vec.V) vec.V {
	return vec.V{X: math.Max(a.X, b.X), Y: math.Max(a.Y, b.Y), Z: math.Max(a.Z, b.Z)}
}

// ---------------------------------------------------------------- sampling

// pcg is a small deterministic generator. Generation must be reproducible: the
// VPL set is a scene asset, and a set that moved between runs would flicker.
type pcg struct{ state uint64 }

func (p *pcg) next() uint64 {
	p.state ^= p.state >> 33
	p.state *= 0xff51afd7ed558ccd
	p.state ^= p.state >> 33
	p.state *= 0xc4ceb9fe1a85ec53
	p.state ^= p.state >> 33
	return p.state
}

func (p *pcg) f64() float64 { return float64(p.next()>>11) * (1.0 / 9007199254740992.0) }

// sampleSphere draws a direction uniformly over the whole sphere.
func sampleSphere(rng *pcg) vec.V {
	z := 1 - 2*rng.f64()
	r := math.Sqrt(math.Max(0, 1-z*z))
	phi := 2 * math.Pi * rng.f64()
	return vec.V{X: r * math.Cos(phi), Y: r * math.Sin(phi), Z: z}
}

// sampleCone draws a direction uniformly inside the cone of half-angle
// acos(cosOuter) about axis, so a spot spends its photons where it actually
// shines instead of throwing most of them at its own back.
func sampleCone(axis vec.V, cosOuter float64, rng *pcg) vec.V {
	z := cosOuter + (1-cosOuter)*rng.f64()
	r := math.Sqrt(math.Max(0, 1-z*z))
	phi := 2 * math.Pi * rng.f64()
	u, v := basis(axis)
	return u.Scale(r * math.Cos(phi)).Add(v.Scale(r * math.Sin(phi))).Add(axis.Scale(z)).Normalize()
}

// basis returns two unit vectors perpendicular to n and to each other.
func basis(n vec.V) (vec.V, vec.V) {
	a := vec.V{X: 0, Y: 1, Z: 0}
	if math.Abs(n.Y) > 0.9 {
		a = vec.V{X: 1, Y: 0, Z: 0}
	}
	u := a.Cross(n).Normalize()
	return u, n.Cross(u).Normalize()
}
