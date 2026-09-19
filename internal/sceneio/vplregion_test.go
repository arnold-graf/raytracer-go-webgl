package sceneio

import (
	"os"
	"path/filepath"
	"testing"

	"raytracer/internal/vec"
)

// writeScene drops a TOML file into a temp dir and returns its path.
func writeScene(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// [vpl] with no min/max means "this object", and the box has to come from the
// object's geometry after its own [[include]]s have landed — the villa's
// staircase and lamps are includes, and a box measured before they merge would
// miss them.
func TestVPLRegionBoundsComeFromTheObjectsGeometry(t *testing.T) {
	dir := t.TempDir()
	writeScene(t, dir, "obj.toml", `
[vpl]
lights = 12

[[box]]
pos_x = -2
pos_y = 0
pos_z = -2
width = 4
height = 3
depth = 4
albedo = [0.8, 0.8, 0.8]
material = "diffuse"
`)
	p := writeScene(t, dir, "world.toml", `
[[include]]
file = "obj.toml"
at = [10.0, 0.0, 20.0]
transform_origin = [0.0, 0.0, 0.0]
`)
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.VPLRegions) != 1 {
		t.Fatalf("got %d regions, want 1", len(s.VPLRegions))
	}
	r := s.VPLRegions[0]
	if r.Lights != 12 {
		t.Errorf("lights = %d, want 12", r.Lights)
	}
	// The box is local (-2,0,-2)..(2,3,2), placed at (10,0,20). The include
	// pins transform_origin, because without it an object is placed by its
	// own centre and the expected numbers would be pivot arithmetic, not a
	// test of the region bounds.
	want := [6]float64{8, 0, 18, 12, 3, 22}
	got := [6]float64{r.Min.X, r.Min.Y, r.Min.Z, r.Max.X, r.Max.Y, r.Max.Z}
	for i := range want {
		if d := got[i] - want[i]; d > 0.01 || d < -0.01 {
			t.Errorf("bounds %v, want %v", got, want)
			break
		}
	}
}

// Two [[include]]s of the same object are two places a player can stand, so
// each gets its own budget rather than sharing one. This is what makes the
// budget authorable on the object rather than on every scene that uses it.
func TestEachIncludeOfAnObjectGetsItsOwnRegion(t *testing.T) {
	dir := t.TempDir()
	writeScene(t, dir, "obj.toml", `
[vpl]
lights = 6

[[box]]
pos_x = -1
pos_y = 0
pos_z = -1
width = 2
height = 2
depth = 2
albedo = [0.8, 0.8, 0.8]
material = "diffuse"
`)
	p := writeScene(t, dir, "world.toml", `
[[include]]
file = "obj.toml"
at = [0.0, 0.0, 0.0]

[[include]]
file = "obj.toml"
at = [50.0, 0.0, 0.0]
`)
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.VPLRegions) != 2 {
		t.Fatalf("got %d regions, want 2", len(s.VPLRegions))
	}
	if a, b := s.VPLRegions[0], s.VPLRegions[1]; a.Min.X == b.Min.X {
		t.Errorf("both regions landed at x=%.1f; the include transform was not applied", a.Min.X)
	}
	for _, r := range s.VPLRegions {
		if r.Lights != 6 {
			t.Errorf("lights = %d, want 6 per instance", r.Lights)
		}
	}
}

// expand grows the box, because bounce belonging to a room routinely lands just
// outside its shell — on a porch or a step — and an exact box drops it into the
// global pool.
func TestVPLRegionExpandGrowsTheBox(t *testing.T) {
	dir := t.TempDir()
	writeScene(t, dir, "obj.toml", `
[vpl]
lights = 4
expand = 2.5

[[box]]
pos_x = 0
pos_y = 0
pos_z = 0
width = 2
height = 2
depth = 2
albedo = [0.8, 0.8, 0.8]
material = "diffuse"
`)
	p := writeScene(t, dir, "world.toml",
		"[[include]]\nfile = \"obj.toml\"\nat = [0.0, 0.0, 0.0]\ntransform_origin = [0.0, 0.0, 0.0]\n")
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.VPLRegions) != 1 {
		t.Fatalf("got %d regions, want 1", len(s.VPLRegions))
	}
	if r := s.VPLRegions[0]; r.Min.X > -2.4 || r.Max.X < 4.4 {
		t.Errorf("expand not applied: box x is %.2f..%.2f, want about -2.5..4.5", r.Min.X, r.Max.X)
	}
}

// min/max scope the region by hand, in the file's own local coordinates, so an
// author can aim at one hall instead of a whole villa.
func TestVPLRegionExplicitBoundsAreLocalAndTransformed(t *testing.T) {
	dir := t.TempDir()
	writeScene(t, dir, "obj.toml", `
[vpl]
lights = 4
min = [-1.0, 0.0, -1.0]
max = [1.0, 2.0, 1.0]

[[box]]
pos_x = -20
pos_y = 0
pos_z = -20
width = 40
height = 10
depth = 40
albedo = [0.8, 0.8, 0.8]
material = "diffuse"
`)
	p := writeScene(t, dir, "world.toml",
		"[[include]]\nfile = \"obj.toml\"\nat = [100.0, 0.0, 0.0]\ntransform_origin = [0.0, 0.0, 0.0]\n")
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	r := s.VPLRegions[0]
	// The authored box, not the 40 m geometry, and moved by the include.
	if r.Min.X < 98.9 || r.Max.X > 101.1 {
		t.Errorf("explicit bounds ignored or untransformed: x is %.2f..%.2f, want 99..101", r.Min.X, r.Max.X)
	}
}

// A file with no [vpl] declares no region, which is what keeps the feature off
// for every scene that has not opted in.
func TestNoVPLTableMeansNoRegion(t *testing.T) {
	dir := t.TempDir()
	p := writeScene(t, dir, "world.toml", `
[[box]]
pos_x = 0
pos_y = 0
pos_z = 0
width = 1
height = 1
depth = 1
albedo = [0.8, 0.8, 0.8]
material = "diffuse"
`)
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.VPLRegions) != 0 {
		t.Errorf("got %d regions from a file with no [vpl]", len(s.VPLRegions))
	}
}

// When a shipped scene does author [vpl], the regions must survive the include
// transform and land on the building.
//
// Whether any scene opts in is an authoring decision, not this package's, so
// the test skips rather than fails when none does — pinning it here would make
// deleting a [vpl] block from a scene look like a code regression. The
// object -> world plumbing itself is covered by the fixtures above, which do
// not depend on what the shipped scenes happen to declare.
func TestVillaSceneCarriesItsRegions(t *testing.T) {
	s, err := Load("../../scenes/outdoors-night-villa.toml")
	if err != nil {
		t.Skipf("villa scene unavailable: %v", err)
	}
	if len(s.VPLRegions) == 0 {
		t.Skip("the villa scene authors no [vpl] regions")
	}
	// Villa A is placed at (0,0,-8); its hearth room is around (-4, 2.5, -5).
	hearth := s.VPLRegions[0]
	found := false
	for _, r := range s.VPLRegions {
		if r.Contains(vec.New(-4, 2.5, -5)) {
			found, hearth = true, r
			break
		}
	}
	if !found {
		t.Fatalf("no region contains villa A's hearth room; boxes are %v", s.VPLRegions)
	}
	if hearth.Lights <= 0 {
		t.Errorf("villa region has no budget: %d", hearth.Lights)
	}
}
