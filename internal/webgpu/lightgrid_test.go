package webgpu

import (
	"math"
	"math/rand"
	"testing"

	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

// cellOf mirrors light_span() in shade.wesl: the cell a world point lands in,
// or ok=false when the point is outside the grid.
//
// Deliberately computed in float32, because the shader does. Binning in float64
// on the Go side and looking up in float32 on the GPU is exactly how a light
// goes missing near a cell boundary, so the test has to be able to see it.
func cellOf(g *lightGrid, p vec.V) (int, bool) {
	axis := func(x, min, inv float64) int {
		return int(math.Floor(float64(float32(float32(x-min) * float32(inv)))))
	}
	i := axis(p.X, g.Min.X, g.InvCell.X)
	j := axis(p.Y, g.Min.Y, g.InvCell.Y)
	k := axis(p.Z, g.Min.Z, g.InvCell.Z)
	if i < 0 || j < 0 || k < 0 ||
		i >= int(g.Dim[0]) || j >= int(g.Dim[1]) || k >= int(g.Dim[2]) {
		return 0, false
	}
	return (k*int(g.Dim[1])+j)*int(g.Dim[0]) + i, true
}

// lightsAt returns the light indices the shader would evaluate at p: the wide
// prefix every point pays for, plus p's own cell.
func lightsAt(g *lightGrid, p vec.V) map[uint32]bool {
	got := map[uint32]bool{}
	for _, li := range g.Wide {
		got[li] = true
	}
	ci, ok := cellOf(g, p)
	if !ok {
		return got
	}
	for _, li := range g.Indices[g.Offsets[ci]:g.Offsets[ci+1]] {
		got[li] = true
	}
	return got
}

// TestLightGridIsConservative is the property the whole optimization rests on:
// every light that could actually affect a point must appear in that point's
// cell. If it can fail, lights pop in and out as the camera moves.
func TestLightGridIsConservative(t *testing.T) {
	s, err := sceneio.Load("../../scenes/office-sunset/index.toml")
	if err != nil {
		t.Skipf("scene unavailable: %v", err)
	}
	lights := PackLights(s)
	if len(lights) < 50 {
		t.Fatalf("expected a many-light scene, got %d", len(lights))
	}
	g := buildLightGrid(lights)
	if g.cellCount() < 2 {
		t.Fatalf("grid degenerated to %d cells for %d lights", g.cellCount(), len(lights))
	}

	// Sample the grid's own volume plus a margin, so points outside it are
	// covered too (those must legitimately see no lights).
	span := vec.V{
		X: float64(g.Dim[0]) / g.InvCell.X,
		Y: float64(g.Dim[1]) / g.InvCell.Y,
		Z: float64(g.Dim[2]) / g.InvCell.Z,
	}
	rng := rand.New(rand.NewSource(7))
	pick := func(lo, size float64) float64 { return lo - 0.1*size + rng.Float64()*1.2*size }

	for n := 0; n < 20000; n++ {
		p := vec.V{
			X: pick(g.Min.X, span.X),
			Y: pick(g.Min.Y, span.Y),
			Z: pick(g.Min.Z, span.Z),
		}
		got := lightsAt(&g, p)
		for li := range lights {
			lp := vec.V{
				X: float64(lights[li].Pos[0]),
				Y: float64(lights[li].Pos[1]),
				Z: float64(lights[li].Pos[2]),
			}
			d := vec.V{X: lp.X - p.X, Y: lp.Y - p.Y, Z: lp.Z - p.Z}
			d2 := d.X*d.X + d.Y*d.Y + d.Z*d.Z
			// Matches the cull in add_point_light_raw: d2 > cullR2 contributes
			// nothing, so only lights inside their radius must be present.
			if d2 <= float64(lights[li].Falloff[0]) && !got[uint32(li)] {
				t.Fatalf("light %d reaches (%.2f %.2f %.2f) (d2=%.3f cullR2=%.3f) but is not in its cell",
					li, p.X, p.Y, p.Z, d2, lights[li].Falloff[0])
			}
		}
	}
}

// TestLightGridShortensLists checks the optimization actually pays: the average
// cell must hold far fewer lights than the flat loop would scan.
func TestLightGridShortensLists(t *testing.T) {
	s, err := sceneio.Load("../../scenes/office-sunset/index.toml")
	if err != nil {
		t.Skipf("scene unavailable: %v", err)
	}
	lights := PackLights(s)
	g := buildLightGrid(lights)

	occupied, sum, worst := 0, 0, 0
	for c := 0; c < g.cellCount(); c++ {
		n := int(g.Offsets[c+1]-g.Offsets[c]) + len(g.Wide)
		sum += n
		if int(g.Offsets[c+1]-g.Offsets[c]) > 0 {
			occupied++
		}
		if n > worst {
			worst = n
		}
	}
	mean := 0.0
	if g.cellCount() > 0 {
		mean = float64(sum) / float64(g.cellCount())
	}
	t.Logf("%d lights -> %d wide + %d cells (%d occupied), %d index entries; "+
		"mean %.1f / worst %d lights evaluated per cell",
		len(lights), len(g.Wide), g.cellCount(), occupied, len(g.Indices), mean, worst)

	if mean > float64(len(lights))/4 {
		t.Fatalf("mean %.1f lights per cell is not a useful reduction from %d", mean, len(lights))
	}
}

func TestLightGridEmptyAndSingleCellFallback(t *testing.T) {
	g := buildLightGrid(nil)
	if g.cellCount() != 1 || len(g.Indices) != 0 || len(g.Wide) != 0 {
		t.Fatalf("empty scene: cells=%d indices=%d", g.cellCount(), len(g.Indices))
	}
	if _, ok := cellOf(&g, vec.V{X: 1e6, Y: -1e6, Z: 5}); !ok {
		t.Fatal("degenerate grid must map every point to its single cell")
	}

	// An infinite cull radius cannot be binned, so it must fall back to the
	// single cell that holds everything rather than silently dropping lights.
	inf := []GPULight{{Falloff: [4]float32{float32(math.Inf(1))}}, {Falloff: [4]float32{4}}}
	g = buildLightGrid(inf)
	if g.cellCount() != 1 {
		t.Fatalf("infinite radius should degenerate to one cell, got %d", g.cellCount())
	}
	got := lightsAt(&g, vec.V{X: 123, Y: 456, Z: 789})
	if len(got) != 2 {
		t.Fatalf("single-cell grid must list every light, got %v", got)
	}
}
