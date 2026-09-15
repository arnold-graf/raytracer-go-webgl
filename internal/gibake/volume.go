// Package gibake is the on-disk format for a baked probe volume.
//
// The runtime probe field is limited by one number: a software BVH traverses a
// ray in about 56 ns, so a frame can afford tens of thousands of them. Every
// artifact that field has — the shimmer, the pop as cascades scroll, the
// undersampled depth map — is a consequence of that budget rather than of the
// algorithm. Offline the budget does not exist: the same pass can spend
// hundreds of rays per probe and take as long as it likes, and what comes out
// needs no update at runtime at all.
//
// The volume is sparse. Covering a level with no culling means a grid over the
// whole scene, and at a useful spacing that is far too many cells to store
// densely — the villa at one metre is 3.3 million, which is 9 GB of probe
// records. Almost all of them are empty air. So a dense index grid of one u32
// per cell points into probe records that exist only where there is geometry
// to light, which on the office is a tenth of the cells and on the villa,
// whose bounds are mostly terrain, a two-hundredth.
package gibake

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"

	"raytracer/internal/vec"
)

// Magic and Version head every file. The version is checked strictly: a probe
// record whose layout has changed silently is a field that is wrong rather
// than a file that fails to load.
const (
	Magic   = "RTGIBAKE"
	Version = 1
)

// Empty marks an index-grid cell that holds no probe. Mirrors GI_PROBE_EMPTY.
const Empty uint32 = 0xffffffff

// Volume is a baked probe field.
type Volume struct {
	// Min is the grid origin and Spacing the distance between probes.
	Min     vec.V
	Spacing float64
	Dim     [3]uint32

	// Index is one entry per cell, either a slot into Probes or Empty.
	Index []uint32
	// Cells is the reverse: one packed cell coordinate per slot, so a pass
	// walking probes by slot knows where each one stands.
	Cells []uint32
	// Probes is Count records of Floats each, laid out exactly as the shader
	// reads them.
	Probes []float32

	// Rays and Iterations record what produced this, for provenance in the
	// header and so a bake can be compared against another honestly.
	Rays       uint32
	Iterations uint32
}

// Count is the number of probes actually stored.
func (v *Volume) Count() int { return len(v.Cells) }

// PackCell packs a cell coordinate into the 10-bits-per-axis form the shader
// unpacks. That caps a volume at 1024 cells on an axis, which at half a metre
// is 512 m — past any scene here, and checked on write rather than trusted.
func PackCell(x, y, z uint32) uint32 { return x | y<<10 | z<<20 }

// Cell returns the world-space centre of a cell.
func (v *Volume) Cell(x, y, z uint32) vec.V {
	return vec.V{
		X: v.Min.X + (float64(x)+0.5)*v.Spacing,
		Y: v.Min.Y + (float64(y)+0.5)*v.Spacing,
		Z: v.Min.Z + (float64(z)+0.5)*v.Spacing,
	}
}

// Build lays out the index grid and the slot list from a predicate saying
// which cells are worth a probe.
//
// The predicate is what makes the volume affordable, and it has to be generous
// rather than exact: the read interpolates over the eight probes around a
// point, so a surface needs probes on both sides of it, and a cell that only
// just misses the test is one its neighbours still depend on.
func Build(min vec.V, spacing float64, dim [3]uint32, keep func(x, y, z uint32) bool) (*Volume, error) {
	for i, d := range dim {
		if d == 0 || d > 1024 {
			return nil, fmt.Errorf("axis %d has %d cells; the packed cell coordinate holds 1..1024", i, d)
		}
	}
	v := &Volume{Min: min, Spacing: spacing, Dim: dim}
	n := int(dim[0]) * int(dim[1]) * int(dim[2])
	v.Index = make([]uint32, n)
	for z := uint32(0); z < dim[2]; z++ {
		for y := uint32(0); y < dim[1]; y++ {
			for x := uint32(0); x < dim[0]; x++ {
				li := (z*dim[1]+y)*dim[0] + x
				if !keep(x, y, z) {
					v.Index[li] = Empty
					continue
				}
				v.Index[li] = uint32(len(v.Cells))
				v.Cells = append(v.Cells, PackCell(x, y, z))
			}
		}
	}
	return v, nil
}

// Alloc sizes the probe records once the layout is known.
func (v *Volume) Alloc(floatsPerProbe int) {
	v.Probes = make([]float32, len(v.Cells)*floatsPerProbe)
}

// Write serialises the volume. The index grid dominates the file for a sparse
// bake and compresses poorly as raw u32, but it is also the part that makes
// the probe data small, so it is stored plainly rather than cleverly.
func (v *Volume) Write(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)
	if _, err := w.WriteString(Magic); err != nil {
		return err
	}
	hdr := []uint32{
		Version,
		v.Dim[0], v.Dim[1], v.Dim[2],
		uint32(len(v.Cells)),
		uint32(len(v.Probes)),
		v.Rays, v.Iterations,
		math.Float32bits(float32(v.Min.X)),
		math.Float32bits(float32(v.Min.Y)),
		math.Float32bits(float32(v.Min.Z)),
		math.Float32bits(float32(v.Spacing)),
	}
	for _, h := range hdr {
		if err := binary.Write(w, binary.LittleEndian, h); err != nil {
			return err
		}
	}
	if err := binary.Write(w, binary.LittleEndian, v.Index); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, v.Cells); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, v.Probes); err != nil {
		return err
	}
	return w.Flush()
}

// Read loads a volume written by Write.
func Read(path string) (*Volume, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	magic := make([]byte, len(Magic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return nil, err
	}
	if string(magic) != Magic {
		return nil, fmt.Errorf("%s is not a baked GI volume", path)
	}
	hdr := make([]uint32, 12)
	if err := binary.Read(r, binary.LittleEndian, hdr); err != nil {
		return nil, err
	}
	if hdr[0] != Version {
		return nil, fmt.Errorf("%s is version %d, this build reads %d — rebake it", path, hdr[0], Version)
	}
	v := &Volume{
		Dim:        [3]uint32{hdr[1], hdr[2], hdr[3]},
		Rays:       hdr[6],
		Iterations: hdr[7],
		Min: vec.V{
			X: float64(math.Float32frombits(hdr[8])),
			Y: float64(math.Float32frombits(hdr[9])),
			Z: float64(math.Float32frombits(hdr[10])),
		},
		Spacing: float64(math.Float32frombits(hdr[11])),
	}
	v.Index = make([]uint32, int(v.Dim[0])*int(v.Dim[1])*int(v.Dim[2]))
	v.Cells = make([]uint32, hdr[4])
	v.Probes = make([]float32, hdr[5])
	if err := binary.Read(r, binary.LittleEndian, v.Index); err != nil {
		return nil, err
	}
	if err := binary.Read(r, binary.LittleEndian, v.Cells); err != nil {
		return nil, err
	}
	if err := binary.Read(r, binary.LittleEndian, v.Probes); err != nil {
		return nil, err
	}
	return v, nil
}
