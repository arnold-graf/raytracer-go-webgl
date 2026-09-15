package gibake

import (
	"path/filepath"
	"testing"

	"raytracer/internal/vec"
)

// TestRoundTrip checks a volume survives disk unchanged.
//
// Worth pinning because the failure is silent: a volume that loads with the
// right dimensions and the wrong probes renders as a scene with its ambient
// switched off, which looks plausible — darker, no leaking, no artifacts — and
// is nothing at all. That exact mistake cost a debugging session, so the baker
// now refuses to write an empty field and this checks the bytes.
func TestRoundTrip(t *testing.T) {
	min := vec.V{X: -3, Y: 1, Z: 7}
	dim := [3]uint32{4, 3, 5}
	// Keep a checkerboard, so the index grid carries both slots and holes.
	v, err := Build(min, 1.5, dim, func(x, y, z uint32) bool { return (x+y+z)%2 == 0 })
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want := int(dim[0]*dim[1]*dim[2]+1) / 2
	if v.Count() != want {
		t.Fatalf("kept %d probes, want %d", v.Count(), want)
	}
	v.Alloc(8)
	for i := range v.Probes {
		v.Probes[i] = float32(i) * 0.25
	}
	v.Rays, v.Iterations = 512, 16

	path := filepath.Join(t.TempDir(), "v.gi")
	if err := v.Write(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Dim != v.Dim || got.Spacing != v.Spacing || got.Min != v.Min {
		t.Errorf("grid changed: %v %v %v", got.Dim, got.Spacing, got.Min)
	}
	if got.Rays != v.Rays || got.Iterations != v.Iterations {
		t.Errorf("provenance changed: %d rays over %d iterations", got.Rays, got.Iterations)
	}
	for i := range v.Index {
		if got.Index[i] != v.Index[i] {
			t.Fatalf("index %d: got %d want %d", i, got.Index[i], v.Index[i])
		}
	}
	for i := range v.Cells {
		if got.Cells[i] != v.Cells[i] {
			t.Fatalf("cell %d: got %d want %d", i, got.Cells[i], v.Cells[i])
		}
	}
	for i := range v.Probes {
		if got.Probes[i] != v.Probes[i] {
			t.Fatalf("probe float %d: got %v want %v", i, got.Probes[i], v.Probes[i])
		}
	}
}

// TestIndexAndCellsAgree checks the index grid and the slot list are inverses.
//
// The update pass walks probes by slot and asks the list where each one is;
// the read walks cells and asks the grid which slot to use. If the two ever
// disagree, probes are lit from one place and sampled at another, which does
// not error — it just puts the light somewhere else.
func TestIndexAndCellsAgree(t *testing.T) {
	dim := [3]uint32{5, 4, 3}
	v, err := Build(vec.V{}, 1, dim, func(x, y, z uint32) bool { return x != 2 })
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for z := uint32(0); z < dim[2]; z++ {
		for y := uint32(0); y < dim[1]; y++ {
			for x := uint32(0); x < dim[0]; x++ {
				slot := v.Index[(z*dim[1]+y)*dim[0]+x]
				if x == 2 {
					if slot != Empty {
						t.Fatalf("cell %d,%d,%d should be empty, got slot %d", x, y, z, slot)
					}
					continue
				}
				if slot == Empty {
					t.Fatalf("cell %d,%d,%d should hold a probe", x, y, z)
				}
				if got := v.Cells[slot]; got != PackCell(x, y, z) {
					t.Fatalf("slot %d maps back to %#x, want cell %d,%d,%d", slot, got, x, y, z)
				}
			}
		}
	}
}
