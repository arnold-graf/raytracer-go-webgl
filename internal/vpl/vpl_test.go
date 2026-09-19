package vpl

import (
	"math"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// room builds a small closed box with one light in it: a floor and four walls
// the photons can land on, and nothing else to complicate the accounting.
func room() *scene.Scene {
	diffuse := scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.8, 0.8, 0.8)}
	s := &scene.Scene{
		Boxes: []scene.Box{
			{Min: vec.New(-5, -0.2, -5), Max: vec.New(5, 0, 5), Surface: diffuse},
			{Min: vec.New(-5, 0, -5.2), Max: vec.New(5, 4, -5), Surface: diffuse},
			{Min: vec.New(-5, 0, 5), Max: vec.New(5, 4, 5.2), Surface: diffuse},
			{Min: vec.New(-5.2, 0, -5), Max: vec.New(-5, 4, 5), Surface: diffuse},
			{Min: vec.New(5, 0, -5), Max: vec.New(5.2, 4, 5), Surface: diffuse},
		},
		Lights: []scene.Light{
			{Pos: vec.New(0, 2, 0), Color: vec.New(4, 3, 2), Range: 20},
		},
	}
	return s
}

func opts() Options {
	o := DefaultOptions()
	o.Count = 8
	o.Rays = 256
	return o
}

// The VPL set is a scene asset, not a sample: the same scene must produce the
// same lights every run, or the image would flicker between loads.
func TestGenerateIsDeterministic(t *testing.T) {
	a := Generate(room(), opts())
	b := Generate(room(), opts())
	if len(a) == 0 {
		t.Fatal("no VPLs generated in a closed lit room")
	}
	if len(a) != len(b) {
		t.Fatalf("count differs between runs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Pos != b[i].Pos || a[i].Color != b[i].Color {
			t.Fatalf("vpl %d differs between runs: %+v vs %+v", i, a[i], b[i])
		}
	}
}

// Rays buys placement accuracy, not brightness. Doubling it must leave the total
// bounce where it was, or the knob would be a second gain and every scene would
// need retuning when it moved.
func TestTotalPowerIsStableInRayCount(t *testing.T) {
	total := func(rays int) float64 {
		o := opts()
		o.Rays = rays
		// Clustering is exercised separately; here keep every candidate so the
		// comparison is of the emitted total alone.
		o.Count = 4096
		o.MinLevels = 0
		sum := 0.0
		for _, l := range Generate(room(), o) {
			sum += l.Color.X + l.Color.Y + l.Color.Z
		}
		return sum
	}
	lo, hi := total(256), total(1024)
	if lo == 0 {
		t.Fatal("no power generated")
	}
	if r := hi / lo; r < 0.9 || r > 1.1 {
		t.Fatalf("total power moved with ray count: %.4f at 256 rays, %.4f at 1024 (ratio %.3f)", lo, hi, r)
	}
}

// Clustering merges rather than averages, so folding N candidates into fewer
// lights must not lose their light. This is the failure mode the live GI grid
// hit from the other side (docs/live-gi.md).
func TestClusteringConservesPower(t *testing.T) {
	o := opts()
	o.Count = 4096
	o.MinLevels = 0
	full := Generate(room(), o)

	o.Count = 4
	few := Generate(room(), o)
	if len(few) > 4 {
		t.Fatalf("clustering returned %d lights, want at most 4", len(few))
	}

	sum := func(ls []scene.Light) float64 {
		s := 0.0
		for _, l := range ls {
			s += l.Color.X + l.Color.Y + l.Color.Z
		}
		return s
	}
	a, b := sum(full), sum(few)
	if r := b / a; r < 0.95 || r > 1.05 {
		t.Fatalf("power not conserved by clustering: %.4f unclustered, %.4f clustered (ratio %.3f)", a, b, r)
	}
}

// A VPL stands on a surface and lights the hemisphere it bounced from. Facing
// it into that surface would light the far side of the wall it sits on, which is
// the leak every approach in this project has had to argue with.
func TestVPLsFaceAwayFromTheirSurface(t *testing.T) {
	for _, l := range Generate(room(), opts()) {
		if !l.IsSpot() {
			t.Fatal("expected a cone; the default Cone is under 180")
		}
		// Inside this room every surface faces the middle, so every bounce
		// normal must have a positive component toward the centre.
		toCentre := vec.New(0, 2, 0).Sub(l.Pos).Normalize()
		if d := l.Dir.Normalize().Dot(toCentre); d <= 0 {
			t.Fatalf("vpl at %+v faces into its own surface (dot %.3f)", l.Pos, d)
		}
	}
}

// FromEnv's bool is about the GLOBAL budget, not about whether anything will be
// generated: with the flag unset a scene's own [vpl] regions still produce
// lights. Count must be 0 in that case, so the leftover pool places nothing.
func TestFromEnvGlobalBudgetOffByDefault(t *testing.T) {
	t.Setenv("RAYTRACER_VPL", "")
	o, ok := FromEnv()
	if ok {
		t.Error("FromEnv claims a global budget with RAYTRACER_VPL empty")
	}
	if o.Count != 0 {
		t.Errorf("global count is %d with the flag unset, want 0", o.Count)
	}
	// The other knobs still have to come through, or a region could not be
	// retuned without switching a global budget on to do it.
	if o.Rays != DefaultOptions().Rays || o.Gain != DefaultOptions().Gain {
		t.Errorf("defaults lost with the flag unset: rays=%d gain=%.3f", o.Rays, o.Gain)
	}

	t.Setenv("RAYTRACER_VPL", "0")
	if _, ok := FromEnv(); ok {
		t.Error("FromEnv claims a global budget with RAYTRACER_VPL=0")
	}

	t.Setenv("RAYTRACER_VPL", "12")
	o, ok = FromEnv()
	if !ok || o.Count != 12 {
		t.Fatalf("FromEnv: ok=%v count=%d, want true/12", ok, o.Count)
	}
}

// lightAtten mirrors a shader function; a drift here silently mis-sizes every
// VPL. Pin the two ends and the shape.
func TestLightAttenMatchesShaderForm(t *testing.T) {
	// At zero distance the unsoftened 1/(base + quad*d^2) would be 1/0.5 = 2.
	// The knee pulls it to about 0.995, and that factor of two is the whole
	// difference between a VPL sized against the shader and one sized against
	// the formula in the comment above it.
	if got := lightAtten(0); math.Abs(got-0.9951) > 0.001 {
		t.Errorf("lightAtten(0) = %.4f, want about 0.9951 (knee applied)", got)
	}
	if lightAtten(100) >= lightAtten(1) {
		t.Error("attenuation must fall with distance")
	}
}

// The key toggle has to leave the scene exactly as it found it, or repeated
// presses would grow the light list.
//
// The flag is what opts this scene in: the key toggles what the scene asked for
// and invents nothing, so a plain room with no [vpl] and no RAYTRACER_VPL has
// nothing to toggle. See TestToggleDoesNothingWhenNothingOptedIn.
func TestToggleIsReversible(t *testing.T) {
	t.Setenv("RAYTRACER_VPL", "8")
	s := room()
	before := len(s.Lights)

	on, n := Toggle(s)
	if !on || n == 0 {
		t.Fatalf("first toggle: on=%v n=%d, want on with lights", on, n)
	}
	if len(s.Lights) != before+n {
		t.Fatalf("attached %d lights, list grew by %d", n, len(s.Lights)-before)
	}
	if !Active(s) {
		t.Error("Active reports off while attached")
	}

	if on, _ = Toggle(s); on {
		t.Error("second toggle left the set on")
	}
	if len(s.Lights) != before {
		t.Fatalf("detach left %d lights, want %d", len(s.Lights), before)
	}
	if Active(s) {
		t.Error("Active reports on while detached")
	}

	// And again, to catch a set that regenerates or double-appends.
	for i := 0; i < 3; i++ {
		Toggle(s)
		if len(s.Lights) != before+n {
			t.Fatalf("cycle %d: attached list is %d, want %d", i, len(s.Lights), before+n)
		}
		Toggle(s)
		if len(s.Lights) != before {
			t.Fatalf("cycle %d: detached list is %d, want %d", i, len(s.Lights), before)
		}
	}
}

// A photon carries constant flux, so the energy a surface receives must fall as
// 1/d^2 — the solid angle it subtends — and not as 1/d^4. This is the bug that
// made office-sunset's skyway 570x too dark to bounce anything.
func TestDepositFallsAsInverseSquare(t *testing.T) {
	// One emitter and one patch of *fixed size*, measured at two distances. The
	// patch has to be small: a wall large enough to fill the hemisphere catches
	// the same photons however far away it is, and would show no falloff at all
	// — which is correct, and not what this test is about.
	deposit := func(dist float64) float64 {
		wall := scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(1, 1, 1)}
		s := &scene.Scene{
			Boxes: []scene.Box{{
				Min:     vec.New(-5, -5, dist),
				Max:     vec.New(5, 5, dist+1),
				Surface: wall,
			}},
			Lights: []scene.Light{{Pos: vec.New(0, 0, 0), Color: vec.New(10, 10, 10), Range: 400}},
		}
		o := DefaultOptions()
		o.Rays = 40000
		o.Count = 100000
		o.MinLevels = 0
		sum := 0.0
		for _, l := range Generate(s, o) {
			sum += l.Color.X
		}
		return sum
	}
	near, far := deposit(10), deposit(40)
	if near == 0 || far == 0 {
		t.Fatal("no deposit")
	}
	// Four times the distance is a sixteenth of the solid angle, so about 16x
	// less deposited. The bug this guards against applied the falloff a second
	// time and would land near 256x.
	ratio := near / far
	if ratio < 8 || ratio > 40 {
		t.Errorf("deposit ratio over 4x distance is %.1fx; want inverse-square (~16x), not 1/d^4 (~256x)", ratio)
	}
}
