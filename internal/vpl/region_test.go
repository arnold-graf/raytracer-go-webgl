package vpl

import (
	"math"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// twoRooms builds a far-apart pair of lit boxes: a dim one at the origin and a
// much brighter one 200 m away. It is the shape of the problem regions exist
// for — on the night villa the bright thing is a campfire up a mountain, and a
// global power ranking hands it the entire budget.
func twoRooms() *scene.Scene {
	diffuse := scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.8, 0.8, 0.8)}
	box := func(c vec.V) []scene.Box {
		return []scene.Box{
			{Min: c.Add(vec.New(-5, -0.2, -5)), Max: c.Add(vec.New(5, 0, 5)), Surface: diffuse},
			{Min: c.Add(vec.New(-5, 0, -5.2)), Max: c.Add(vec.New(5, 4, -5)), Surface: diffuse},
			{Min: c.Add(vec.New(-5, 0, 5)), Max: c.Add(vec.New(5, 4, 5.2)), Surface: diffuse},
			{Min: c.Add(vec.New(-5.2, 0, -5)), Max: c.Add(vec.New(-5, 4, 5)), Surface: diffuse},
			{Min: c.Add(vec.New(5, 0, -5)), Max: c.Add(vec.New(5.2, 4, 5)), Surface: diffuse},
		}
	}
	near, far := vec.New(0, 0, 0), vec.New(200, 0, 0)
	s := &scene.Scene{}
	s.Boxes = append(s.Boxes, box(near)...)
	s.Boxes = append(s.Boxes, box(far)...)
	s.Lights = []scene.Light{
		{Pos: near.Add(vec.New(0, 2, 0)), Color: vec.New(1, 0.8, 0.5), Range: 20},
		{Pos: far.Add(vec.New(0, 2, 0)), Color: vec.New(40, 32, 20), Range: 20},
	}
	return s
}

// nearRegion is a box around the dim room only.
func nearRegion(n int) scene.VPLRegion {
	return scene.VPLRegion{
		Min: vec.New(-6, -1, -6), Max: vec.New(6, 5, 6),
		Lights: n, Label: "near",
	}
}

func inBox(r scene.VPLRegion, ls []scene.Light) int {
	n := 0
	for _, l := range ls {
		if r.Contains(l.Pos) {
			n++
		}
	}
	return n
}

// The motivating measurement, as a test. Without a region the bright far room
// takes essentially the whole budget; with one, the dim room gets what it asked
// for. On the night villa this was eleven of twenty-four slots going to a
// campfire 160 m away.
func TestRegionWinsSlotsAGlobalRankingGivesAway(t *testing.T) {
	o := opts()
	o.Count = 8

	global := Generate(twoRooms(), o)
	globalNear := inBox(nearRegion(0), global)

	s := twoRooms()
	s.VPLRegions = []scene.VPLRegion{nearRegion(8)}
	scoped := Generate(s, o)
	scopedNear := inBox(nearRegion(0), scoped)

	if globalNear >= scopedNear {
		t.Fatalf("region bought nothing: %d lights in the near room globally, %d with a region",
			globalNear, scopedNear)
	}
	if scopedNear < 4 {
		t.Errorf("region asked for 8 lights and got %d in the box", scopedNear)
	}
	t.Logf("near room: %d/%d lights globally, %d/%d with a region",
		globalNear, len(global), scopedNear, len(scoped))
}

// The question a region has to answer correctly, and the one that decides
// whether the feature is safe to use: a region selects candidates by where the
// photon LANDED, not by where its emitter sits. An emitter outside the box that
// shines into it must still light it — otherwise declaring a region on a
// building would switch off every fire in its garden.
func TestRegionKeepsBounceFromAnEmitterOutsideIt(t *testing.T) {
	diffuse := scene.Surface{Mat: scene.MatDiffuse, Albedo: vec.New(0.8, 0.8, 0.8)}
	// A back wall inside the region, and a light well outside it, aimed so the
	// only way to the wall is straight through the open side.
	s := &scene.Scene{
		Boxes: []scene.Box{
			{Min: vec.New(-5, 0, -5.2), Max: vec.New(5, 4, -5), Surface: diffuse},
			{Min: vec.New(-5, -0.2, -5), Max: vec.New(5, 0, 5), Surface: diffuse},
		},
		Lights: []scene.Light{
			{Pos: vec.New(0, 2, 30), Color: vec.New(8, 6, 4), Range: 60},
		},
	}
	region := scene.VPLRegion{
		Min: vec.New(-6, -1, -6), Max: vec.New(6, 5, 6),
		Lights: 8, Label: "room",
	}
	if region.Contains(s.Lights[0].Pos) {
		t.Fatal("test is not testing anything: the emitter is inside the region")
	}
	s.VPLRegions = []scene.VPLRegion{region}

	got := Generate(s, opts())
	if n := inBox(region, got); n == 0 {
		t.Fatal("an emitter outside the region lit nothing inside it; " +
			"selection is keying on the emitter rather than on where the photon landed")
	}
}

// A region moves slots between pools. It must not create or destroy bounce, or
// the feature would be a brightness knob wearing a budget's clothes.
func TestRegionConservesPowerAgainstTheSameBudget(t *testing.T) {
	sum := func(ls []scene.Light) float64 {
		t := 0.0
		for _, l := range ls {
			t += l.Color.X + l.Color.Y + l.Color.Z
		}
		return t
	}
	o := opts()
	o.Count = 4096
	o.MinLevels = 0

	flat := sum(Generate(twoRooms(), o))

	s := twoRooms()
	s.VPLRegions = []scene.VPLRegion{nearRegion(4096)}
	split := sum(Generate(s, o))

	if flat == 0 {
		t.Fatal("no power generated")
	}
	if r := split / flat; r < 0.95 || r > 1.05 {
		t.Errorf("partitioning moved total power: %.4f flat, %.4f split (ratio %.3f)", flat, split, r)
	}
}

// A region's gain scales that region and nothing else. This is why gain is
// applied in toLights rather than folded into the photon weight in shoot: a
// candidate cannot know which region will claim it until it has landed.
func TestRegionGainScalesOnlyItsOwnLights(t *testing.T) {
	region := nearRegion(8)
	near := func(g float64) (in, out float64) {
		s := twoRooms()
		r := region
		r.Gain = g
		s.VPLRegions = []scene.VPLRegion{r}
		for _, l := range Generate(s, opts()) {
			p := l.Color.X + l.Color.Y + l.Color.Z
			if region.Contains(l.Pos) {
				in += p
			} else {
				out += p
			}
		}
		return in, out
	}
	base := DefaultOptions().Gain
	in1, out1 := near(base)
	in2, out2 := near(base * 4)

	if in1 == 0 || out1 == 0 {
		t.Fatalf("need light on both sides to compare: in=%.4f out=%.4f", in1, out1)
	}
	if r := in2 / in1; r < 3.6 || r > 4.4 {
		t.Errorf("region gain x4 scaled its own lights by %.2f, want about 4", r)
	}
	if r := out2 / out1; r < 0.98 || r > 1.02 {
		t.Errorf("region gain leaked outside the region: scale %.3f, want 1", r)
	}
}

// Overlapping regions resolve smallest-first, so a box around one room beats the
// box around the building containing it. Include order must not decide it.
func TestSmallestOverlappingRegionWins(t *testing.T) {
	inner := scene.VPLRegion{
		Min: vec.New(-6, -1, -6), Max: vec.New(6, 5, 6), Lights: 2, Label: "inner",
	}
	outer := scene.VPLRegion{
		Min: vec.New(-300, -50, -300), Max: vec.New(300, 50, 300), Lights: 16, Label: "outer",
	}
	for _, order := range [][]scene.VPLRegion{{inner, outer}, {outer, inner}} {
		s := twoRooms()
		s.VPLRegions = order
		got := Generate(s, opts())
		if n := inBox(inner, got); n > 2 {
			t.Errorf("order %s/%s: inner region budget is 2 but it kept %d lights",
				order[0].Label, order[1].Label, n)
		}
	}
}

// A region with no budget, or a degenerate box, is not a region. Dropping it at
// the edge keeps the generator from having to carry the case.
func TestInvalidRegionsAreIgnored(t *testing.T) {
	s := twoRooms()
	s.VPLRegions = []scene.VPLRegion{
		{Min: vec.New(-6, -1, -6), Max: vec.New(6, 5, 6), Lights: 0},
		{Min: vec.New(1, 1, 1), Max: vec.New(1, 1, 1), Lights: 8},
	}
	if rs := validRegions(s, opts()); len(rs) != 0 {
		t.Fatalf("kept %d invalid regions", len(rs))
	}
	// And the result must match the no-region path exactly.
	a := Generate(twoRooms(), opts())
	b := Generate(s, opts())
	if len(a) != len(b) {
		t.Fatalf("invalid regions changed the result: %d lights vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Pos != b[i].Pos || a[i].Color != b[i].Color {
			t.Fatalf("light %d differs", i)
		}
	}
}

// Reach is the measured cost driver (docs/vpl.md: "Cost scales with Reach, not
// really with Count"), so a region has to be able to lower it locally.
func TestRegionReachCapsItsOwnLights(t *testing.T) {
	s := twoRooms()
	r := nearRegion(8)
	r.Reach = 6
	s.VPLRegions = []scene.VPLRegion{r}
	for _, l := range Generate(s, opts()) {
		if r.Contains(l.Pos) && l.Range > 6+1e-6 {
			t.Errorf("region capped reach at 6 m but a light inside carries %.2f m", l.Range)
		}
	}
}

// Determinism is the property that makes a VPL set a scene asset rather than
// noise, and partitioning must not break it.
func TestRegionGenerationIsDeterministic(t *testing.T) {
	gen := func() []scene.Light {
		s := twoRooms()
		s.VPLRegions = []scene.VPLRegion{nearRegion(8)}
		return Generate(s, opts())
	}
	a, b := gen(), gen()
	if len(a) != len(b) {
		t.Fatalf("count differs between runs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Pos != b[i].Pos || a[i].Color != b[i].Color {
			t.Fatalf("light %d differs between runs", i)
		}
	}
}

// Gain moved from shoot to toLights. Pin that the move was value-preserving:
// the set generated with no regions must still be sized by Gain exactly as
// before, or every scene's calibration would silently shift.
func TestGlobalGainStillScalesTheWholeSet(t *testing.T) {
	sum := func(g float64) float64 {
		o := opts()
		o.Gain = g
		o.MinLevels = 0
		t := 0.0
		for _, l := range Generate(room(), o) {
			t += l.Color.X + l.Color.Y + l.Color.Z
		}
		return t
	}
	a, b := sum(1), sum(8)
	if a == 0 {
		t.Fatal("no power at gain 1")
	}
	if r := b / a; math.Abs(r-8) > 0.2 {
		t.Errorf("gain 8 scaled the set by %.3f, want 8", r)
	}
}

// The launch default: with RAYTRACER_VPL unset, a scene's own [vpl] regions
// still produce their lights, and nothing is placed outside them. An authored
// budget is scene data — the building describes itself the way it describes its
// walls — so it must not need a flag to be lit.
func TestRegionsGenerateWithNoGlobalFlag(t *testing.T) {
	t.Setenv("RAYTRACER_VPL", "")
	s := twoRooms()
	s.VPLRegions = []scene.VPLRegion{nearRegion(8)}

	o, ok := optionsFor(s)
	if !ok {
		t.Fatal("optionsFor reports no work for a scene carrying a [vpl] region")
	}
	if o.Count != 0 {
		t.Errorf("global count is %d with the flag unset, want 0", o.Count)
	}

	got := Generate(s, o)
	if len(got) == 0 {
		t.Fatal("a [vpl] region generated nothing with RAYTRACER_VPL unset")
	}
	region := nearRegion(0)
	for _, l := range got {
		if !region.Contains(l.Pos) {
			t.Errorf("light at %+v is outside every region; the global pool was placed "+
				"even though no global budget was asked for", l.Pos)
		}
	}
	if n := len(got); n > 8 {
		t.Errorf("region budget is 8 but %d lights were placed", n)
	}
}

// A scene with no [vpl] anywhere and no flag stays completely untouched. This is
// what keeps the feature off for every scene that has not opted in, and it is
// the guarantee the "byte-identical off" claim rests on.
func TestNoFlagAndNoRegionsGeneratesNothing(t *testing.T) {
	t.Setenv("RAYTRACER_VPL", "")
	s := twoRooms()

	o, ok := optionsFor(s)
	if ok {
		t.Error("optionsFor reports work for a scene with no regions and no flag")
	}
	if got := Generate(s, o); len(got) != 0 {
		t.Errorf("generated %d lights for a scene that asked for none", len(got))
	}
}

// RAYTRACER_VPL_REGIONS=0 with no global count is the complete off switch: it
// has to silence an opted-in scene too, or there would be no way to get a
// clean baseline out of one.
func TestRegionsOffSwitchSilencesAnOptedInScene(t *testing.T) {
	t.Setenv("RAYTRACER_VPL", "")
	t.Setenv("RAYTRACER_VPL_REGIONS", "0")
	s := twoRooms()
	s.VPLRegions = []scene.VPLRegion{nearRegion(8)}

	o, ok := optionsFor(s)
	if ok {
		t.Error("optionsFor reports work with regions ignored and no global budget")
	}
	if got := Generate(s, o); len(got) != 0 {
		t.Errorf("generated %d lights with the off switch set", len(got))
	}
}

// With a global budget asked for, both pools are placed: the regions get theirs
// and the leftovers get the scene-wide count.
func TestGlobalFlagAddsTheLeftoverPoolOnTopOfRegions(t *testing.T) {
	t.Setenv("RAYTRACER_VPL", "")
	s := twoRooms()
	s.VPLRegions = []scene.VPLRegion{nearRegion(8)}
	o, _ := optionsFor(s)
	regionOnly := len(Generate(s, o))

	t.Setenv("RAYTRACER_VPL", "8")
	s2 := twoRooms()
	s2.VPLRegions = []scene.VPLRegion{nearRegion(8)}
	o2, ok := optionsFor(s2)
	if !ok || o2.Count != 8 {
		t.Fatalf("optionsFor: ok=%v count=%d, want true/8", ok, o2.Count)
	}
	both := len(Generate(s2, o2))

	if both <= regionOnly {
		t.Errorf("global budget added nothing: %d lights region-only, %d with RAYTRACER_VPL=8",
			regionOnly, both)
	}
	region := nearRegion(0)
	outside := 0
	for _, l := range Generate(s2, o2) {
		if !region.Contains(l.Pos) {
			outside++
		}
	}
	if outside == 0 {
		t.Error("no lights landed outside the region with a global budget of 8")
	}
}

// A shadow ray is what a light actually costs, so the generator has to be able
// to hand one to the shader with a raised bar. Measured on the villa hearth room
// at 1024x640 with 48 VPLs each: exact shadows 129.9 ms, at 16 levels 72.3 ms.
func TestGeneratedVPLsCarryTheShadowThreshold(t *testing.T) {
	o := opts()
	o.ShadowLevels = 16
	got := Generate(room(), o)
	if len(got) == 0 {
		t.Fatal("no VPLs generated")
	}
	for _, l := range got {
		if l.ShadowLevels != 16 {
			t.Fatalf("VPL carries ShadowLevels %.1f, want 16", l.ShadowLevels)
		}
	}
}

// And a region can set its own, so one room can buy its shadows back while the
// rest of the level stays cheap.
func TestRegionShadowLevelsOverrideTheDefault(t *testing.T) {
	s := twoRooms()
	r := nearRegion(8)
	r.ShadowLevels = 3
	s.VPLRegions = []scene.VPLRegion{r}

	o := opts()
	o.ShadowLevels = 16
	inRegion, outside := 0, 0
	for _, l := range Generate(s, o) {
		if r.Contains(l.Pos) {
			if l.ShadowLevels != 3 {
				t.Errorf("light inside the region carries %.1f, want the region's 3", l.ShadowLevels)
			}
			inRegion++
		} else {
			if l.ShadowLevels != 16 {
				t.Errorf("light outside carries %.1f, want the global 16", l.ShadowLevels)
			}
			outside++
		}
	}
	if inRegion == 0 || outside == 0 {
		t.Fatalf("need lights on both sides: %d in, %d out", inRegion, outside)
	}
}

// Key 9 must not be a back door around the launch rule. It used to fall back to
// the default count when no set had been registered, so on a scene with no [vpl]
// anywhere and no RAYTRACER_VPL a keypress conjured twenty-four globally-ranked
// lights — the exact thing the unflagged launch path exists to prevent. It now
// toggles what the scene asked for and nothing else.
func TestToggleDoesNothingWhenNothingOptedIn(t *testing.T) {
	t.Setenv("RAYTRACER_VPL", "")
	s := room()
	before := len(s.Lights)

	on, n := Toggle(s)
	if on || n != 0 {
		t.Errorf("toggle generated %d lights on a scene with no [vpl] and no flag (on=%v)", n, on)
	}
	if len(s.Lights) != before {
		t.Errorf("light list grew from %d to %d", before, len(s.Lights))
	}
	if Active(s) {
		t.Error("Active reports on after a toggle that placed nothing")
	}
}

// But a scene that declared a region toggles that region's lights, with no flag
// needed — the key follows the same rule the launch path does.
func TestToggleUsesRegionsWithNoFlag(t *testing.T) {
	t.Setenv("RAYTRACER_VPL", "")
	s := twoRooms()
	s.VPLRegions = []scene.VPLRegion{nearRegion(8)}
	before := len(s.Lights)

	on, n := Toggle(s)
	if !on || n == 0 {
		t.Fatalf("toggle: on=%v n=%d, want the region's lights", on, n)
	}
	region := nearRegion(0)
	for _, l := range s.Lights[before:] {
		if !region.Contains(l.Pos) {
			t.Errorf("toggle placed a light at %+v outside every region", l.Pos)
		}
	}
	if on, _ = Toggle(s); on {
		t.Error("second toggle left the set on")
	}
	if len(s.Lights) != before {
		t.Errorf("detach left %d lights, want %d", len(s.Lights), before)
	}
}
