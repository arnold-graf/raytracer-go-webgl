package webgpu

import (
	"math"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func testZone(mn, mx vec.V, tint float64) scene.AmbientZone {
	z := scene.AmbientZone{Min: mn, Max: mx}
	for i := range z.Faces {
		z.Faces[i] = vec.V{X: tint + float64(i), Y: tint, Z: tint - float64(i)}
	}
	return z
}

// TestPackAmbientZonesLayout pins the layout shade.wesl reads: one count word,
// then min, max and six RGB faces per zone. The shader indexes this by hand
// from idx_tables, so a silent stride change there is a silent lighting change
// everywhere zones are enabled.
func TestPackAmbientZonesLayout(t *testing.T) {
	zones := []scene.AmbientZone{
		testZone(vec.V{X: -1, Y: -2, Z: -3}, vec.V{X: 4, Y: 5, Z: 6}, 0.25),
		testZone(vec.V{X: 10, Y: 11, Z: 12}, vec.V{X: 20, Y: 21, Z: 22}, 1.5),
	}
	got := packAmbientZones(zones)

	wantLen := 1 + len(zones)*ambientZoneWords
	if len(got) != wantLen {
		t.Fatalf("packed %d words, want %d", len(got), wantLen)
	}
	if got[0] != uint32(len(zones)) {
		t.Fatalf("count word = %d, want %d", got[0], len(zones))
	}

	f := func(i int) float64 { return float64(math.Float32frombits(got[i])) }
	for zi, z := range zones {
		base := 1 + zi*ambientZoneWords
		for k, want := range []float64{z.Min.X, z.Min.Y, z.Min.Z, z.Max.X, z.Max.Y, z.Max.Z} {
			if f(base+k) != want {
				t.Errorf("zone %d bound %d = %v, want %v", zi, k, f(base+k), want)
			}
		}
		for face := 0; face < 6; face++ {
			o := base + 6 + face*3
			w := z.Faces[face]
			if f(o) != w.X || f(o+1) != w.Y || f(o+2) != w.Z {
				t.Errorf("zone %d face %d = (%v,%v,%v), want (%v,%v,%v)",
					zi, face, f(o), f(o+1), f(o+2), w.X, w.Y, w.Z)
			}
		}
	}
}

// TestPackAmbientZonesEmpty checks the off state, which the shader detects
// purely from the count word: with no zones it must still receive a zero, or
// it reads whatever the previously loaded scene left in the buffer.
func TestPackAmbientZonesEmpty(t *testing.T) {
	got := packAmbientZones(nil)
	if len(got) != 1 || got[0] != 0 {
		t.Fatalf("empty pack = %v, want [0]", got)
	}
}

func TestPackAmbientZonesClampsToCapacity(t *testing.T) {
	zones := make([]scene.AmbientZone, maxAmbientZones+10)
	got := packAmbientZones(zones)
	if got[0] != maxAmbientZones {
		t.Fatalf("count = %d, want it clamped to %d", got[0], maxAmbientZones)
	}
	if want := 1 + maxAmbientZones*ambientZoneWords; len(got) != want {
		t.Fatalf("packed %d words, want %d", len(got), want)
	}
}

// TestAmbientTableFitsIdxTables guards the region against overrunning the
// shared buffer it borrows. idx_tables has no binding of its own to spare:
// the megakernel already sits at Metal's 31-buffer limit.
func TestAmbientTableFitsIdxTables(t *testing.T) {
	need := idxTablesAmbientBase + 1 + maxAmbientZones*ambientZoneWords
	if need > idxTablesWords {
		t.Fatalf("ambient region needs %d words, idx_tables holds %d", need, idxTablesWords)
	}
}

func TestAmbientZoneContains(t *testing.T) {
	z := testZone(vec.V{X: 0, Y: 0, Z: 0}, vec.V{X: 2, Y: 2, Z: 2}, 0)
	if !z.Valid() {
		t.Fatal("zone should be valid")
	}
	for _, c := range []struct {
		p    vec.V
		want bool
	}{
		{vec.V{X: 1, Y: 1, Z: 1}, true},
		{vec.V{X: 0, Y: 0, Z: 0}, true},
		{vec.V{X: 2, Y: 2, Z: 2}, true},
		{vec.V{X: -0.01, Y: 1, Z: 1}, false},
		{vec.V{X: 1, Y: 2.01, Z: 1}, false},
	} {
		if got := z.Contains(c.p); got != c.want {
			t.Errorf("Contains(%v) = %v, want %v", c.p, got, c.want)
		}
	}
	degenerate := scene.AmbientZone{Min: vec.V{X: 1}, Max: vec.V{X: 1}}
	if degenerate.Valid() {
		t.Error("zero-extent zone should be invalid")
	}
}
