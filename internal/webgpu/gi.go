// gi.go — sizing and scheduling for the traced indirect-light probe field.
//
// The field is a grid of probes, each holding an octahedral irradiance map and
// an octahedral map of distance moments. Probes cast rays and shade what they
// hit with the megakernel's own lighting; shading reads the field back in
// place of the authored ambient constant. See docs/live-gi.md.
//
// Two forms, both off unless asked for:
//
//   - live, `RAYTRACER_LIVE_GI=1`: two cascades, a fine one that follows the
//     viewer and a coarse one over the scene, refreshed a slice at a time.
//   - baked, `RAYTRACER_GI_BAKE=<file>`: one sparse volume solved offline by
//     cmd/probebake and read at runtime. Nothing updates, nothing is culled.
//
// Everything here is inert with both off, which is the point of keeping it in
// one file: the megakernel renders byte-identically and nothing in the hot
// path has to think about it.
package webgpu

import (
	"math"

	"raytracer/internal/gibake"
	"raytracer/internal/vec"
)

// giGrid is the world-to-cell transform the shader needs.
type giGrid struct {
	Min  vec.V
	Inv  float64 // 1 / cell
	Cell float64
	Dim  [3]uint32
}

// cells is the number of cells the grid spans.
func (g giGrid) cells() uint32 { return g.Dim[0] * g.Dim[1] * g.Dim[2] }

// The live field's budget, in probes refreshed per frame.
//
// Each probe casts GI_PROBE_RAYS (32) rays, and a ray costs about 56 ns on an
// M2 Max, so this is roughly 65,000 rays and 4 ms. Half of each cascade goes
// per frame, which puts both on a two-frame cycle: what matters is that the
// cycle is short, because probes stepping together on a long one shows up as
// the update's error crossing walls in slow waves.
const (
	giProbesPerFrame     = 2048
	giFineProbesPerFrame = 1024
)

// Must match GI_PROBES_PER_WG in types.wesl.
const giProbesPerWorkgroup = 1

// probeDispatchWidth is the width of the probe pass's 2D workgroup grid.
//
// Comfortably under the 65,535 a single dimension allows. Going over that does
// not return an error — it aborts the process with a bare Metal encoder
// assertion, which reads like a driver bug rather than a limit.
const probeDispatchWidth = 32768

// The fine cascade: camera-anchored, and fine enough to resolve room-scale
// bounce. 2 m over 32 x 16 x 32 m.
const (
	giFineCell = 2.0
	giFineDimX = 16
	giFineDimY = 8
	giFineDimZ = 16
)

// giFineProbes is the fine cascade's probe count; it must not exceed GI_C0_MAX
// in types.wesl, which is where the coarse cascade's storage starts.
const giFineProbes = giFineDimX * giFineDimY * giFineDimZ

// giMaxProbes caps the coarse cascade.
const giMaxProbes = 2048

// giProbeSpacingMin is the finest the coarse cascade is allowed to get, in
// metres. Below this a level-sized scene blows past giMaxProbes.
const giProbeSpacingMin = 1.5

// giForwardBias pushes the fine cascade along the view direction, as a
// fraction of its half-extent.
//
// Centred on the camera, half the box sits behind the viewer and it reaches
// only its half-extent into the scene. Biasing forward trades reach behind,
// which only matters for bounce off surfaces out of frame, for reach ahead,
// which is everything the viewer can see. Not one: at a full bias the box
// starts at the near plane and light bouncing off the floor underfoot — the
// strongest indirect term in most interiors — falls out of it entirely.
const giForwardBias = 0.35

// buildGIFineGrid is the camera-anchored cascade, snapped to its own lattice
// so a probe keeps its world position — and so, through the toroidal slot
// mapping, its history — as the viewer moves.
func buildGIFineGrid(cam, fwd vec.V) giGrid {
	half := vec.V{
		X: giFineDimX * giFineCell * 0.5,
		Y: giFineDimY * giFineCell * 0.5,
		Z: giFineDimZ * giFineCell * 0.5,
	}
	c := cam.Add(fwd.Scale(half.Z * giForwardBias))
	snap := func(v float64) float64 { return math.Floor(v/giFineCell) * giFineCell }
	return giGrid{
		Min:  vec.V{X: snap(c.X - half.X), Y: snap(c.Y - half.Y), Z: snap(c.Z - half.Z)},
		Inv:  1 / giFineCell,
		Cell: giFineCell,
		Dim:  [3]uint32{giFineDimX, giFineDimY, giFineDimZ},
	}
}

// buildGIProbeGrid is the coarse cascade: the whole scene, never moving.
//
// Spacing is chosen as the coarsest of the per-axis needs so the probe count
// fits giMaxProbes. It is very coarse on a large level — ten metres or more —
// and that is what it is for: the far field, behind a fine cascade that
// resolves the room the viewer is standing in.
func buildGIProbeGrid(mn, mx vec.V) giGrid {
	ext := vec.V{X: mx.X - mn.X, Y: mx.Y - mn.Y, Z: mx.Z - mn.Z}
	cell := giProbeSpacingMin
	for {
		d := probeDims(ext, cell)
		if uint64(d[0])*uint64(d[1])*uint64(d[2]) <= giMaxProbes {
			// Centre the grid on the scene: the dims are rounded up, so there
			// is usually a little slack to share between the two sides.
			pad := vec.V{
				X: (float64(d[0])*cell - ext.X) * 0.5,
				Y: (float64(d[1])*cell - ext.Y) * 0.5,
				Z: (float64(d[2])*cell - ext.Z) * 0.5,
			}
			return giGrid{
				Min:  vec.V{X: mn.X - pad.X, Y: mn.Y - pad.Y, Z: mn.Z - pad.Z},
				Inv:  1 / cell,
				Cell: cell,
				Dim:  d,
			}
		}
		cell *= 1.25
	}
}

func probeDims(ext vec.V, cell float64) [3]uint32 {
	axis := func(e float64) uint32 {
		n := uint32(math.Ceil(e/cell)) + 1
		if n < 2 {
			return 2
		}
		return n
	}
	return [3]uint32{axis(ext.X), axis(ext.Y), axis(ext.Z)}
}

// giBounds picks the box the coarse cascade spans.
//
// probe.LitBounds filters out the handful of outsized primitives that dominate
// a scene's raw extent; it matters more than it sounds, because the villa's
// raw bounds are 826 x 2202 x 895 m, which at any affordable probe count puts
// probes tens of metres apart and they see nothing but sky. Filtered they are
// 163 x 93 x 217 m. The AO volume's own box is the fallback, since it is sized
// the same way.
func giBounds(giMin, giMax vec.V, ok bool, ao aoBoundsSource) (vec.V, vec.V, bool) {
	if ok {
		return giMin, giMax, true
	}
	if !ao.valid() {
		return vec.V{}, vec.V{}, false
	}
	return ao.min, ao.max(), true
}

type aoBoundsSource struct {
	min        vec.V
	cell       float64
	nx, ny, nz int
	present    bool
}

func (a aoBoundsSource) valid() bool { return a.present && a.nx > 0 }

func (a aoBoundsSource) max() vec.V {
	return vec.V{
		X: a.min.X + float64(a.nx-1)*a.cell,
		Y: a.min.Y + float64(a.ny-1)*a.cell,
		Z: a.min.Z + float64(a.nz-1)*a.cell,
	}
}

// giCellsOf is the probe pass's dispatch size, zero when the field is off.
func giCellsOf(p renderParams) uint32 {
	if p.giMode == 0 {
		return 0
	}
	return p.probeCount + p.probeCount2
}

// giSchedule fills in which probes the pass refreshes this frame.
//
// A loaded bake is already solved, so it dispatches nothing unless the baker
// is driving it. The live cascades walk half of each per frame.
func (r *Renderer) giSchedule(rp *renderParams, gi giGrid) {
	if r.baked != nil {
		rp.bake = gi
		rp.bakeIndexBase = uint32(r.bakedIndexBase)
		rp.bakeCellBase = uint32(r.bakedCellBase)
		rp.bakeProbes = uint32(r.baked.Count())
		rp.bakeBlend = r.bakeBlend
		if r.bakeDrive {
			rp.probeCount = uint32(r.baked.Count())
		}
		return
	}
	fine := uint32(giFineProbes)
	rp.probeCount = (fine + 1) / 2
	rp.probeBase = uint32((uint64(r.giFrame) * uint64(rp.probeCount)) % uint64(max32(fine, 1)))
	coarse := gi.cells()
	rp.probeCount2 = (coarse + 1) / 2
	rp.probeBase2 = uint32((uint64(r.giFrame) * uint64(rp.probeCount2)) % uint64(max32(coarse, 1)))
	// The ray set is rotated per frame, so successive updates of the same
	// probe are different samples rather than the same 32 directions.
	rp.probeSeed = uint32(r.giFrame)
	r.giFrame++
}

// giParams writes the probe field's share of the uniform block.
func giParams(out []byte, p renderParams, fine giGrid) {
	putVec4(out[416:432], fine.Min)
	putF32(out[428:432], float32(fine.Inv))
	putU32(out[432:436], fine.Dim[0])
	putU32(out[436:440], fine.Dim[1])
	putU32(out[440:444], fine.Dim[2])
	putU32(out[444:448], p.giMode)
	putVec4(out[448:464], p.gi.Min)
	putF32(out[460:464], float32(p.gi.Inv))
	putU32(out[464:468], p.gi.Dim[0])
	putU32(out[468:472], p.gi.Dim[1])
	putU32(out[472:476], p.gi.Dim[2])
	putU32(out[480:484], p.probeBase)
	putU32(out[484:488], p.probeCount)
	putU32(out[488:492], p.probeSeed)
	putU32(out[492:496], p.probeBase2)
	putU32(out[496:500], p.probeCount2)
	putU32(out[500:504], p.probeDispatchW)
	putVec4(out[512:528], p.bake.Min)
	putF32(out[524:528], float32(p.bake.Inv))
	putU32(out[528:532], p.bake.Dim[0])
	putU32(out[532:536], p.bake.Dim[1])
	putU32(out[536:540], p.bake.Dim[2])
	putU32(out[544:548], p.bakeIndexBase)
	putU32(out[548:552], p.bakeCellBase)
	putU32(out[552:556], p.bakeProbes)
	putF32(out[556:560], p.bakeBlend)
}

// giVolume is the Renderer's probe state, all of it inert when GI is off.
type giVolume struct {
	// baked is the loaded volume, nil unless RAYTRACER_GI_BAKE is set.
	// bakedUp records that it has reached the GPU; bakeDrive makes the pass
	// run over every probe in it, which only cmd/probebake asks for.
	baked          *gibake.Volume
	bakedIndexBase uint64
	bakedCellBase  uint64
	bakedUp        bool
	bakeDrive      bool
	bakeBlend      float32
	giFrame        uint64
}
