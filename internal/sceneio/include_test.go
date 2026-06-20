package sceneio

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"raytracer/internal/texture"
	"raytracer/internal/vec"
)

// TestIncludedPrimitivesPlacedOnce guards against double-applying an include's
// instance transform to non-box primitives. Included geometry keeps its local
// coordinates and carries the composed Xform; the renderer maps local->world
// via Xform.ToWorld, so ToWorld(center) must equal the intended world position
// (a regression here previously translated spheres/cylinders twice).
func TestIncludedPrimitivesPlacedOnce(t *testing.T) {
	dir := t.TempDir()
	obj := filepath.Join(dir, "obj.toml")
	if err := os.WriteFile(obj, []byte(`
[[sphere]]
center = [0.0, 0.0, 0.0]
radius = 0.5
material = "diffuse"
albedo = [1.0, 1.0, 1.0]

[[cylinder]]
cx = 0.0
cz = 0.0
radius = 0.1
ymin = 0.0
ymax = 1.0
material = "diffuse"
albedo = [1.0, 1.0, 1.0]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "scene.toml")
	if err := os.WriteFile(parent, []byte(`
[[include]]
file = "obj.toml"
at = [10.0, 2.0, -3.0]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Load(parent)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(s.Spheres) != 1 || len(s.Cylinders) != 1 {
		t.Fatalf("got %d spheres, %d cylinders; want 1 each", len(s.Spheres), len(s.Cylinders))
	}

	sp := s.Spheres[0]
	wantSphere := vec.New(10, 2, -3) // local origin + at, applied exactly once
	if got := sp.Xform.ToWorld(sp.Center); !approxV(got, wantSphere) {
		t.Fatalf("sphere world center = %v, want %v (double transform?)", got, wantSphere)
	}

	cy := s.Cylinders[0]
	wantBase := vec.New(10, 2, -3) // local (cx, ymin, cz) = (0,0,0) -> at
	if got := cy.Xform.ToWorld(vec.New(cy.CX, cy.YMin, cy.CZ)); !approxV(got, wantBase) {
		t.Fatalf("cylinder world base = %v, want %v (double transform?)", got, wantBase)
	}
}

func approxV(a, b vec.V) bool {
	const eps = 1e-9
	return math.Abs(a.X-b.X) < eps && math.Abs(a.Y-b.Y) < eps && math.Abs(a.Z-b.Z) < eps
}

// LoadDeps must report included sub-scenes so the hot-reload watcher can detect
// edits to them (e.g. building.toml), not just the top-level file.
// TestIncludeParamsTemplate checks that a parameterized object reads include
// params, derives geometry with the math helpers, and that the resulting
// primitives are placed correctly in world space.
func TestIncludeParamsTemplate(t *testing.T) {
	dir := t.TempDir()
	obj := filepath.Join(dir, "lamp.toml")
	if err := os.WriteFile(obj, []byte(`
{{- $stem := or .stem_len 1.5 -}}
{{- $orb := or .orb_radius 0.4 -}}
[[cylinder]]
cx = 0.0
cz = 0.0
radius = 0.045
ymax = 0.0
ymin = {{neg $stem}}
material = "metal"
albedo = [1.0, 1.0, 1.0]

[[sphere]]
center = [0.0, {{neg (add $stem $orb)}}, 0.0]
radius = {{$orb}}
material = "glass"
albedo = [1.0, 1.0, 1.0]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1) Explicit params override the defaults and drive derived geometry.
	withParams := filepath.Join(dir, "with.toml")
	if err := os.WriteFile(withParams, []byte(`
[[include]]
file = "lamp.toml"
at = [10.0, 5.0, 0.0]
params = { stem_len = 2.0, orb_radius = 0.5 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(withParams)
	if err != nil {
		t.Fatalf("load with params: %v", err)
	}
	if len(s.Cylinders) != 1 || len(s.Spheres) != 1 {
		t.Fatalf("got %d cylinders, %d spheres; want 1 each", len(s.Cylinders), len(s.Spheres))
	}
	cy := s.Cylinders[0]
	if math.Abs(cy.YMin-(-2.0)) > 1e-9 {
		t.Fatalf("stem ymin = %v, want -2.0 (stem_len param)", cy.YMin)
	}
	// stem base (0,-2,0) local -> (10,3,0) world.
	if got := cy.Xform.ToWorld(vec.New(cy.CX, cy.YMin, cy.CZ)); !approxV(got, vec.New(10, 3, 0)) {
		t.Fatalf("stem base world = %v, want (10,3,0)", got)
	}
	sp := s.Spheres[0]
	if math.Abs(sp.Radius-0.5) > 1e-9 {
		t.Fatalf("orb radius = %v, want 0.5 (orb_radius param)", sp.Radius)
	}
	// orb center local (0, -(2.0+0.5), 0) = (0,-2.5,0) -> world (10,2.5,0).
	if got := sp.Xform.ToWorld(sp.Center); !approxV(got, vec.New(10, 2.5, 0)) {
		t.Fatalf("orb center world = %v, want (10,2.5,0)", got)
	}

	// 2) No params: the object's own `or .x <default>` defaults apply.
	noParams := filepath.Join(dir, "no.toml")
	if err := os.WriteFile(noParams, []byte(`
[[include]]
file = "lamp.toml"
at = [0.0, 0.0, 0.0]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	s2, err := Load(noParams)
	if err != nil {
		t.Fatalf("load without params: %v", err)
	}
	if math.Abs(s2.Cylinders[0].YMin-(-1.5)) > 1e-9 {
		t.Fatalf("default stem ymin = %v, want -1.5", s2.Cylinders[0].YMin)
	}
	if math.Abs(s2.Spheres[0].Radius-0.4) > 1e-9 {
		t.Fatalf("default orb radius = %v, want 0.4", s2.Spheres[0].Radius)
	}
}

// TestSphereLampDefaultsUnchanged pins the real lamp object's geometry when
// included with no params, so parameterizing it didn't shift the existing
// indoor-outdoor lamps.
func TestSphereLampDefaultsUnchanged(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "scene.toml")
	lamp := repoFile("scenes/objects/otto-wagner-sphere-lamp.toml")
	if err := os.WriteFile(parent, []byte(
		"[[include]]\nfile = "+strconv.Quote(lamp)+"\nat = [0.0, 0.0, 0.0]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(parent)
	if err != nil {
		t.Fatalf("load lamp: %v", err)
	}
	if len(s.Spheres) != 2 || len(s.Cylinders) != 2 || len(s.Lights) != 1 {
		t.Fatalf("lamp shape changed: %d spheres, %d cylinders, %d lights",
			len(s.Spheres), len(s.Cylinders), len(s.Lights))
	}
	// Glass orb is the first sphere: default center y = -1.85, radius 0.4.
	orb := s.Spheres[0]
	if math.Abs(orb.Center.Y-(-1.85)) > 1e-9 || math.Abs(orb.Radius-0.4) > 1e-9 {
		t.Fatalf("orb default drifted: center.y=%v radius=%v, want -1.85 / 0.4", orb.Center.Y, orb.Radius)
	}
	// Stem (second cylinder) drops to -1.5 by default.
	if math.Abs(s.Cylinders[1].YMin-(-1.5)) > 1e-9 {
		t.Fatalf("stem default ymin=%v, want -1.5", s.Cylinders[1].YMin)
	}
}

// TestStaircaseSeqTemplate checks the real staircase object: default 8 steps
// without params, and a custom step count via seq/range.
func TestStaircaseSeqTemplate(t *testing.T) {
	stairPath := repoFile("scenes/objects/staircase.toml")

	// Defaults: 8 filled steps, last tread top at y=3.0, run ends at x=4.0.
	dir := t.TempDir()
	parent := filepath.Join(dir, "scene.toml")
	if err := os.WriteFile(parent, []byte(
		"[[include]]\nfile = "+strconv.Quote(stairPath)+"\nat = [0.0, 0.0, 0.0]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(parent)
	if err != nil {
		t.Fatalf("load default staircase: %v", err)
	}
	if len(s.Boxes) != 8 {
		t.Fatalf("default steps = %d, want 8", len(s.Boxes))
	}
	last := s.Boxes[7]
	if math.Abs(last.Max.X-4.0) > 1e-9 || math.Abs(last.Max.Y-3.0) > 1e-9 {
		t.Fatalf("last step max = %v, want (4,3,z)", last.Max)
	}

	// Custom step count and string texture via params.
	custom := filepath.Join(dir, "custom.toml")
	if err := os.WriteFile(custom, []byte(
		"[[include]]\nfile = "+strconv.Quote(stairPath)+"\nat = [0.0, 0.0, 0.0]\nparams = { steps = 4, run = 1.0, rise = 0.5, width = 2.0, texture = \"wood\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s2, err := Load(custom)
	if err != nil {
		t.Fatalf("load custom staircase: %v", err)
	}
	if len(s2.Boxes) != 4 {
		t.Fatalf("custom steps = %d, want 4", len(s2.Boxes))
	}
	b := s2.Boxes[3]
	if math.Abs(b.Min.X-3.0) > 1e-9 || math.Abs(b.Max.X-4.0) > 1e-9 ||
		math.Abs(b.Max.Y-2.0) > 1e-9 || math.Abs(b.Max.Z-2.0) > 1e-9 {
		t.Fatalf("step 3 bounds min=%v max=%v, want x in [3,4] y top 2 z width 2", b.Min, b.Max)
	}
	if b.Tex != texture.Wood {
		t.Fatalf("step 3 texture = %d, want wood (%d)", b.Tex, texture.Wood)
	}
}

func TestObjectTemplateVec3Param(t *testing.T) {
	dir := t.TempDir()
	obj := filepath.Join(dir, "obj.toml")
	if err := os.WriteFile(obj, []byte(`
{{$albedo := orVec3 .albedo 0.1 0.2 0.3}}
[[box]]
min = [0.0, 0.0, 0.0]
max = [1.0, 1.0, 1.0]
material = "diffuse"
albedo = {{$albedo}}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("default", func(t *testing.T) {
		parent := filepath.Join(dir, "default.toml")
		if err := os.WriteFile(parent, []byte("[[include]]\nfile = \"obj.toml\"\nat = [0.0, 0.0, 0.0]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := Load(parent)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(s.Boxes) != 1 {
			t.Fatalf("got %d boxes", len(s.Boxes))
		}
		want := vec.New(0.1, 0.2, 0.3)
		if got := s.Boxes[0].Albedo; !approxV(got, want) {
			t.Fatalf("albedo = %v, want %v", got, want)
		}
	})

	t.Run("param", func(t *testing.T) {
		parent := filepath.Join(dir, "custom.toml")
		if err := os.WriteFile(parent, []byte(`
[[include]]
file = "obj.toml"
at = [0.0, 0.0, 0.0]
params = { albedo = [0.8, 0.7, 0.6] }
`), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := Load(parent)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		want := vec.New(0.8, 0.7, 0.6)
		if got := s.Boxes[0].Albedo; !approxV(got, want) {
			t.Fatalf("albedo = %v, want %v", got, want)
		}
	})
}

func TestLoadDepsIncludesSubScenes(t *testing.T) {
	_, deps, err := LoadDeps(repoFile("scenes/indoor-outdoor.toml"))
	if err != nil {
		t.Fatalf("LoadDeps: %v", err)
	}
	var hasParent, hasBuilding bool
	for _, d := range deps {
		switch filepath.Base(d) {
		case "indoor-outdoor.toml":
			hasParent = true
		case "building.toml":
			hasBuilding = true
		}
		if !filepath.IsAbs(d) {
			t.Errorf("dep not absolute: %q", d)
		}
	}
	if !hasParent || !hasBuilding {
		t.Fatalf("deps missing parent/include: %s", strings.Join(deps, ", "))
	}
}

func TestBuildingIncludeLoads(t *testing.T) {
	s, err := Load(repoFile("scenes/indoor-outdoor.toml"))
	if err != nil {
		t.Fatalf("load indoor-outdoor: %v", err)
	}
	if len(s.Boxes) < 15 {
		t.Fatalf("expected building boxes from include, got %d", len(s.Boxes))
	}
	var glass, roof int
	for i := range s.Boxes {
		b := &s.Boxes[i]
		if b.Mat == 4 { // MatGlass
			glass++
		}
		if b.Xform != nil && b.Tex != 0 {
			roof++ // slanted cement roof has a transform
		}
	}
	// The building include brings at least the glass window pane (the round
	// stove adds a glass firebox door too), so require >= 1 rather than pinning
	// an exact count that scene edits would churn.
	if glass < 1 {
		t.Fatalf("expected at least one glass box from the building include, got %d", glass)
	}
	if roof < 1 {
		t.Fatalf("expected at least one transformed roof panel")
	}
}

func TestIncludeMergesTerrainFeatures(t *testing.T) {
	dir := t.TempDir()
	mountains := filepath.Join(dir, "mountains.toml")
	if err := os.WriteFile(mountains, []byte(`
[[terrain]]
  [[terrain.feature]]
  kind = "peak"
  pos = [0.0, 0.0]
  height = 10.0
  width = 8.0
`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "scene.toml")
	if err := os.WriteFile(parent, []byte(`
[[terrain]]
origin = [-50.0, 0.0, -50.0]
size = [100.0, 100.0]
base = 0.0
detail = 0.0

[[include]]
file = "mountains.toml"
at = [30.0, 0.0, 40.0]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(parent)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(s.Terrains) != 1 {
		t.Fatalf("got %d terrains", len(s.Terrains))
	}
	if len(s.Terrains[0].Features) != 1 {
		t.Fatalf("got %d features, want 1 merged", len(s.Terrains[0].Features))
	}
	f := s.Terrains[0].Features[0]
	if math.Abs(f.PosX-30) > 1e-9 || math.Abs(f.PosZ-40) > 1e-9 {
		t.Fatalf("feature pos = (%v,%v), want (30,40)", f.PosX, f.PosZ)
	}
	center, ok := s.TerrainHeightAt(30, 40)
	if !ok || center < 8 {
		t.Fatalf("height at merged peak center = %v ok=%v, want ~10", center, ok)
	}
}

func TestStormVillaSceneLoads(t *testing.T) {
	s, err := Load(repoFile("scenes/outdoors-night-villa.toml"))
	if err != nil {
		t.Fatalf("load outdoors-night-villa: %v", err)
	}
	if len(s.Terrains) == 0 {
		t.Fatal("expected scene terrain")
	}
	// The villa object declares a local [[terrain.pad]] at center [0,0]; the
	// include places it at [0,0,-10], so it must land at world z = -10.
	var found bool
	for _, p := range s.Terrains[0].Pads {
		if p.CenterX == 0 && p.CenterZ == -10 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected villa pad merged at world [0,-10], pads=%+v", s.Terrains[0].Pads)
	}
	if len(s.Boxes) < 20 {
		t.Fatalf("expected villa geometry from include, got %d boxes", len(s.Boxes))
	}
	// Villa origin (grade) should sit on its pad level (0), not on wild terrain.
	for i := range s.Boxes {
		mn, mx := s.Boxes[i].WorldBounds()
		if mn.Y >= -0.05 && mn.Y <= 0.05 && mx.Y >= 1.0 {
			return // stone plinth base at grade
		}
	}
	t.Fatal("expected villa plinth base near y=0")
}

func TestRotatedIncludePadHonorsYaw(t *testing.T) {
	s, err := Load(repoFile("scenes/outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	const wantYaw = -45 * math.Pi / 180
	var found bool
	for _, p := range s.Terrains[0].Pads {
		if p.CenterX == 50 && p.CenterZ == -10 {
			if math.Abs(p.Angle-wantYaw) > 0.01 {
				t.Fatalf("second villa pad angle = %v, want %v", p.Angle, wantYaw)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("expected rotated second-villa pad at (50,-10), pads=%+v", s.Terrains[0].Pads)
	}
}

func TestIncludeWithPadUsesPadLevelNotTerrain(t *testing.T) {
	dir := t.TempDir()
	villa := filepath.Join(dir, "site.toml")
	if err := os.WriteFile(villa, []byte(`
[[terrain]]
[[terrain.pad]]
center = [0.0, 0.0]
half = [5.0, 5.0]
level = 0.0
margin = 2.0

[[box]]
min = [0.0, 0.0, 0.0]
max = [1.0, 1.0, 1.0]
material = "diffuse"
albedo = [1.0, 1.0, 1.0]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "scene.toml")
	if err := os.WriteFile(parent, []byte(`
[[terrain]]
origin = [-20.0, 0.0, -20.0]
size = [40.0, 40.0]
base = 0.0
detail = 0.0

  [[terrain.feature]]
  kind = "peak"
  pos = [0.0, -10.0]
  height = 4.0
  width = 6.0

[[include]]
file = "site.toml"
at = [0.0, 0.0, -10.0]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Load(parent)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(s.Boxes) != 1 {
		t.Fatalf("got %d boxes, want 1", len(s.Boxes))
	}
	mn, _ := s.Boxes[0].WorldBounds()
	if mn.Y < -0.05 || mn.Y > 0.05 {
		t.Fatalf("pad object base y = %v, want ~0 (must not snap to wild terrain above pad)", mn.Y)
	}
}

func TestIncludeAtYOffsetAboveTerrain(t *testing.T) {
	dir := t.TempDir()
	obj := filepath.Join(dir, "obj.toml")
	if err := os.WriteFile(obj, []byte(`
[[sphere]]
center = [0.0, 1.0, 0.0]
radius = 0.2
material = "diffuse"
albedo = [1.0, 1.0, 1.0]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "scene.toml")
	if err := os.WriteFile(parent, []byte(`
[[terrain]]
origin = [-20.0, 0.0, -20.0]
size = [40.0, 40.0]
base = 0.0
detail = 0.0

  [[terrain.feature]]
  kind = "peak"
  pos = [5.0, 0.0]
  height = 6.0
  width = 4.0

[[include]]
file = "obj.toml"
at = [5.0, 1.0, 0.0]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Load(parent)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(s.Spheres) != 1 {
		t.Fatalf("got %d spheres, want 1", len(s.Spheres))
	}
	sp := s.Spheres[0]
	ground, ok := s.TerrainHeightAt(5, 0)
	if !ok {
		t.Fatal("expected terrain height")
	}
	wantCenter := vec.New(5, ground+1+1.0, 0) // at.y=1 offset + local center y=1
	if got := sp.Xform.ToWorld(sp.Center); !approxV(got, wantCenter) {
		t.Fatalf("sphere center = %v, want %v (ground=%v)", got, wantCenter, ground)
	}
}

func TestNestedIncludeStaysRelativeToParent(t *testing.T) {
	dir := t.TempDir()
	lamp := filepath.Join(dir, "lamp.toml")
	if err := os.WriteFile(lamp, []byte(`
[[sphere]]
center = [0.0, 2.0, 0.0]
radius = 0.1
material = "diffuse"
albedo = [1.0, 1.0, 1.0]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	building := filepath.Join(dir, "building.toml")
	if err := os.WriteFile(building, []byte(`
[[include]]
file = "lamp.toml"
at = [0.0, 3.0, 0.0]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "scene.toml")
	if err := os.WriteFile(parent, []byte(`
[[terrain]]
origin = [-10.0, 0.0, -10.0]
size = [20.0, 20.0]
base = 0.0
detail = 0.0

[[include]]
file = "building.toml"
at = [0.0, 0.0, 0.0]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Load(parent)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(s.Spheres) != 1 {
		t.Fatalf("got %d spheres, want 1", len(s.Spheres))
	}
	sp := s.Spheres[0]
	want := vec.New(0, 5, 0) // lamp local y=2 + building at y=3, terrain flat at 0
	if got := sp.Xform.ToWorld(sp.Center); !approxV(got, want) {
		t.Fatalf("nested sphere = %v, want %v", got, want)
	}
}
