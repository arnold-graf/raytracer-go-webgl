package webgpu

import (
	"os"
	"path/filepath"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
)

// The `shadow` key decides whether a primitive is packed as a shadow blocker.
// These live in package webgpu rather than sceneio because PackBlockers is the
// predicate that matters, and because the sceneio test binary does not build
// (terrain_grass_bump_test.go and vplregion_test.go reference fields and
// signatures that no longer exist) — unrelated to this key, but it would make
// these unrunnable.

func loadShadowScene(t *testing.T, body string) *scene.Scene {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := sceneio.Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return s
}

const glassBoxTOML = `
[[box]]
material = "glass"
pos_x = 0
pos_y = 0
pos_z = 0
width = 1
height = 1
depth = 1
`

const opaqueBoxTOML = `
[[box]]
material = "diffuse"
albedo = [1, 1, 1]
pos_x = 0
pos_y = 0
pos_z = 0
width = 1
height = 1
depth = 1
`

func TestGlassCastsNoShadowByDefault(t *testing.T) {
	s := loadShadowScene(t, glassBoxTOML)
	if got := s.Boxes[0].Shadow; got != scene.ShadowAuto {
		t.Fatalf("omitted shadow key: mode = %v, want ShadowAuto", got)
	}
	if s.Boxes[0].CastsShadow() {
		t.Fatal("glass should cast no shadow by default")
	}
	if n := len(PackBlockers(s)); n != 0 {
		t.Fatalf("blockers = %d, want 0", n)
	}
}

func TestGlassShadowTrueOptsIn(t *testing.T) {
	s := loadShadowScene(t, glassBoxTOML+"shadow = true\n")
	if !s.Boxes[0].CastsShadow() {
		t.Fatal("shadow = true should make glass cast a shadow")
	}
	if n := len(PackBlockers(s)); n != 1 {
		t.Fatalf("blockers = %d, want 1", n)
	}
}

// shadow = false on glass restates the default and must change nothing.
func TestGlassShadowFalseIsTheDefault(t *testing.T) {
	s := loadShadowScene(t, glassBoxTOML+"shadow = false\n")
	if s.Boxes[0].CastsShadow() {
		t.Fatal("shadow = false should keep glass out of the blocker set")
	}
	if n := len(PackBlockers(s)); n != 0 {
		t.Fatalf("blockers = %d, want 0", n)
	}
}

// The key resolves for every material, so opaque geometry can opt out too.
// Omitting it leaves opaque geometry blocking exactly as it did before.
func TestOpaqueDefaultsToCastingAndCanOptOut(t *testing.T) {
	s := loadShadowScene(t, opaqueBoxTOML)
	if !s.Boxes[0].CastsShadow() {
		t.Fatal("diffuse should cast a shadow by default")
	}
	if n := len(PackBlockers(s)); n != 1 {
		t.Fatalf("blockers = %d, want 1", n)
	}

	off := loadShadowScene(t, opaqueBoxTOML+"shadow = false\n")
	if off.Boxes[0].CastsShadow() {
		t.Fatal("shadow = false should drop a diffuse box from the blocker set")
	}
	if n := len(PackBlockers(off)); n != 0 {
		t.Fatalf("blockers with shadow = false = %d, want 0", n)
	}
}

// Every kind PackBlockers filters has to honour the key. The three blocker paths
// share one predicate, and a kind that missed it would silently keep the old
// material-only behaviour.
func TestShadowKeyAppliesToEveryBlockerKind(t *testing.T) {
	cases := []struct{ name, body string }{
		{"sphere", "[[sphere]]\nmaterial = \"glass\"\ncenter = [0,0,0]\nradius = 1\n"},
		{"box", "[[box]]\nmaterial = \"glass\"\npos_x = 0\npos_y = 0\npos_z = 0\nwidth = 1\nheight = 1\ndepth = 1\n"},
		{"cylinder", "[[cylinder]]\nmaterial = \"glass\"\ncenter_x = 0\ncenter_z = 0\npos_y = 0\nheight = 1\nwidth = 1\n"},
		{"cone", "[[cone]]\nmaterial = \"glass\"\ncenter_x = 0\ncenter_z = 0\npos_y = 0\nheight = 1\nwidth = 1\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if n := len(PackBlockers(loadShadowScene(t, c.body))); n != 0 {
				t.Fatalf("default: blockers = %d, want 0", n)
			}
			if n := len(PackBlockers(loadShadowScene(t, c.body+"shadow = true\n"))); n != 1 {
				t.Fatalf("shadow = true: blockers = %d, want 1", n)
			}
		})
	}
}

// Emissive primitives are filtered separately and stay out regardless, so
// shadow = true must not resurrect one as a blocker.
func TestEmissiveSphereStaysOutOfBlockers(t *testing.T) {
	body := "[[sphere]]\nmaterial = \"emit\"\nalbedo = [4,4,4]\ncenter = [0,0,0]\nradius = 1\n"
	for _, extra := range []string{"", "shadow = true\n"} {
		s := loadShadowScene(t, body+extra)
		if n := len(PackBlockers(s)); n != 0 {
			t.Fatalf("emissive sphere with %q: blockers = %d, want 0", extra, n)
		}
	}
}
