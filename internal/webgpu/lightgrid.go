package webgpu

import (
	"fmt"
	"math"
	"sort"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

const (
	// Grid resolution target, in cells per light. More cells means shorter
	// per-cell lists but a larger offset table and more duplication of lights
	// that straddle cell boundaries.
	lightGridCellsPerLight = 8

	// Caps. The offset table costs one u32 per cell plus one, and the index
	// list repeats a light in every cell its sphere touches, so a handful of
	// very large overlapping lights could otherwise blow up either one.
	lightGridMaxCells   = 32768
	lightGridMaxIndices = 1 << 18

	// A light covering more than 1/lightGridMaxCellShare of the grid goes in
	// the wide list instead of being replicated across that many cells.
	lightGridMaxCellShare = 16

	// The grid ships as [offsets | indices] inside the shared index-table
	// buffer, which also carries the two plane lists.
	lightGridBufWords = lightGridMaxCells + 1 + lightGridMaxIndices

	idxTablesBlockerPlaneBase = maxPrims
	idxTablesLightGridBase    = 2 * maxPrims
	idxTablesWords            = idxTablesLightGridBase + lightGridBufWords
)

// lightGrid is a clustered-shading index: a uniform grid over the union of the
// lights' influence spheres, where every cell lists the lights that can reach
// it. A lit point looks up its own cell instead of scanning every light.
//
// This is exact rather than an approximation. add_point_light_raw already
// returns the surface unchanged for any light farther away than its cull
// radius, so a cell list that includes every sphere overlapping that cell
// cannot change the image — only the work needed to produce it. Scenes here
// reach a few hundred lights (office-sunset has 266, mostly instanced desk and
// server lamps) and the loop ran at every ray hit, which measured at about half
// the frame.
type lightGrid struct {
	Min     vec.V
	InvCell vec.V
	Dim     [3]uint32

	// Wide holds lights that are not worth clustering: they reach across most
	// of the grid, or past its bounds. Every lit point evaluates all of them.
	//
	// Without this split a handful of scene-spanning lights would land in every
	// cell anyway, and worse, their radii would inflate the grid bounds and
	// make the cells too coarse to separate the many small lights. In
	// office-sunset three lights have radius 100-500 against a median of 0.8.
	Wide []uint32

	// Offsets is a CSR row table with cellCount()+1 entries: cell c owns
	// Indices[Offsets[c]:Offsets[c+1]].
	Offsets []uint32
	Indices []uint32
}

// flat lays the three tables out as [wide | offsets | indices] for the single
// region the shader reads. Metal allows only 32 buffer bindings per stage and
// the megakernel had used them all, so this shares one binding (idx_tables)
// with the plane lists; the shader finds the pieces via params.table_base.
func (g *lightGrid) flat() []uint32 {
	out := make([]uint32, 0, len(g.Wide)+len(g.Offsets)+len(g.Indices))
	out = append(out, g.Wide...)
	out = append(out, g.Offsets...)
	return append(out, g.Indices...)
}

// wideCount is how many always-evaluated lights lead flat().
func (g *lightGrid) wideCount() uint32 { return uint32(len(g.Wide)) }

// idxBase is where the clustered light indices start inside flat().
func (g *lightGrid) idxBase() uint32 { return uint32(len(g.Wide) + len(g.Offsets)) }

func (g *lightGrid) cellCount() int {
	return int(g.Dim[0]) * int(g.Dim[1]) * int(g.Dim[2])
}

// setLights repacks the light buffer together with its cluster grid. The grid
// stores indices into c.lights, so updating one without the other would point
// cells at the wrong lights.
func (c *sceneCache) setLights(s *scene.Scene) {
	c.lights = PackLights(s)
	c.lightGrid = buildLightGrid(c.lights)
}

// uploadLightGrid writes the cluster tables. The grid is only rebuilt when the
// lights change, so this rides along with the light buffer rather than running
// every frame.
func (r *Renderer) uploadLightGrid(g *lightGrid) error {
	flat := g.flat()
	if len(flat) == 0 {
		return nil
	}
	if err := r.queue.WriteBuffer(r.idxTables, idxTablesLightGridBase*4, u32Bytes(flat)); err != nil {
		return fmt.Errorf("upload light grid: %w", err)
	}
	return nil
}

// singleCellGrid holds every light in one cell that covers all of space, which
// reproduces the unclustered behavior. InvCell of zero maps any point to cell
// zero, so the shader needs no separate "grid disabled" path.
func singleCellGrid(n int) lightGrid {
	wide := make([]uint32, n)
	for i := range wide {
		wide[i] = uint32(i)
	}
	return lightGrid{
		Dim:     [3]uint32{1, 1, 1},
		Wide:    wide,
		Offsets: []uint32{0, 0},
	}
}

// buildLightGrid clusters lights by their influence spheres. It falls back to a
// single cell holding every light when there is nothing to gain or when the
// index list would grow past its cap.
func buildLightGrid(lights []GPULight) lightGrid {
	if len(lights) == 0 {
		return lightGrid{Dim: [3]uint32{1, 1, 1}, Offsets: []uint32{0, 0}}
	}

	radii := make([]float64, len(lights))
	pos := make([]vec.V, len(lights))
	for i := range lights {
		// Falloff.x is the squared cull radius. A light with no reach still
		// occupies the cell it sits in, so a surface exactly at the light
		// position keeps behaving as it did.
		r := math.Sqrt(float64(lights[i].Falloff[0]))
		if math.IsNaN(r) || math.IsInf(r, 0) {
			return singleCellGrid(len(lights))
		}
		radii[i] = r
		pos[i] = vec.V{X: float64(lights[i].Pos[0]), Y: float64(lights[i].Pos[1]), Z: float64(lights[i].Pos[2])}
		if math.IsNaN(pos[i].X) || math.IsInf(pos[i].X, 0) {
			return singleCellGrid(len(lights))
		}
	}

	// Bound the grid by the dense cluster of ordinary lights: their positions
	// plus one typical radius. Using every light instead would let a single
	// distant or scene-spanning one stretch the box far past the geometry,
	// leaving cells too coarse to separate anything — measured on
	// office-sunset, that produced 17 occupied cells instead of thousands.
	//
	// Excluding lights here is safe because anything not fully inside these
	// bounds is classified wide below and evaluated everywhere.
	margin := quantile(radii, 0.75)
	lo := vec.V{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)}
	hi := vec.V{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}
	local := 0
	for i := range lights {
		if radii[i] > margin {
			continue
		}
		lo = minVec(lo, vec.V{X: pos[i].X - margin, Y: pos[i].Y - margin, Z: pos[i].Z - margin})
		hi = maxVec(hi, vec.V{X: pos[i].X + margin, Y: pos[i].Y + margin, Z: pos[i].Z + margin})
		local++
	}
	if local == 0 || math.IsInf(lo.X, 0) {
		return singleCellGrid(len(lights))
	}

	// Grow the bounds by more than the binning pad. The lights that defined
	// these bounds sit exactly on them, and the containment test below adds the
	// pad to their radius, so without slack those lights land right on the
	// boundary and rounding decides whether they stay clustered. In
	// office-sunset that alone moved 89 of 266 lights into the wide list.
	if e := binEpsilon(lo, hi); e > 0 {
		lo = vec.V{X: lo.X - 4*e, Y: lo.Y - 4*e, Z: lo.Z - 4*e}
		hi = vec.V{X: hi.X + 4*e, Y: hi.Y + 4*e, Z: hi.Z + 4*e}
	}

	dim, cell := lightGridShape(lo, hi, local)
	// The grid's real extent is dim*cell, which float32 rounding can leave a
	// hair short of hi. Make that extent authoritative so the containment test
	// below agrees with the cells that actually exist; otherwise lights near
	// the upper edge get pushed into the wide list for no reason.
	hi = vec.V{
		X: lo.X + float64(dim[0])*cell.X,
		Y: lo.Y + float64(dim[1])*cell.Y,
		Z: lo.Z + float64(dim[2])*cell.Z,
	}
	nCells := int(dim[0]) * int(dim[1]) * int(dim[2])
	maxCellsPerLight := maxInt(1, nCells/lightGridMaxCellShare)

	// cellsOf lists the cells a light's sphere actually touches, or nil when it
	// escapes the grid or covers too much of it to be worth clustering.
	eps := binEpsilon(lo, hi)
	cellsOf := func(li int) []int {
		p, r := pos[li], radii[li]+eps
		if p.X-r < lo.X || p.Y-r < lo.Y || p.Z-r < lo.Z ||
			p.X+r > hi.X || p.Y+r > hi.Y || p.Z+r > hi.Z {
			return nil
		}
		i0, i1 := cellSpan(p.X-r, p.X+r, lo.X, cell.X, dim[0])
		j0, j1 := cellSpan(p.Y-r, p.Y+r, lo.Y, cell.Y, dim[1])
		k0, k1 := cellSpan(p.Z-r, p.Z+r, lo.Z, cell.Z, dim[2])
		if (i1-i0+1)*(j1-j0+1)*(k1-k0+1) > maxCellsPerLight {
			return nil
		}
		var out []int
		for k := k0; k <= k1; k++ {
			for j := j0; j <= j1; j++ {
				for i := i0; i <= i1; i++ {
					// The sphere's AABB overlaps this cell, but the sphere may
					// not; the exact test keeps corner cells out of the list
					// and is what holds the lists short.
					if sphereHitsCell(p, r, lo, cell, i, j, k) {
						out = append(out, (k*int(dim[1])+j)*int(dim[0])+i)
					}
				}
			}
		}
		return out
	}

	var wide []uint32
	cells := make([][]int, len(lights))
	total := 0
	for li := range lights {
		c := cellsOf(li)
		if c == nil {
			wide = append(wide, uint32(li))
			continue
		}
		cells[li] = c
		total += len(c)
	}
	if total > lightGridMaxIndices {
		return singleCellGrid(len(lights))
	}

	offsets := make([]uint32, nCells+1)
	for _, c := range cells {
		for _, ci := range c {
			offsets[ci+1]++
		}
	}
	for c := 0; c < nCells; c++ {
		offsets[c+1] += offsets[c]
	}
	indices := make([]uint32, total)
	cursor := make([]uint32, nCells)
	for li, c := range cells {
		for _, ci := range c {
			indices[offsets[ci]+cursor[ci]] = uint32(li)
			cursor[ci]++
		}
	}

	return lightGrid{
		Min:     lo,
		InvCell: vec.V{X: 1 / cell.X, Y: 1 / cell.Y, Z: 1 / cell.Z},
		Dim:     dim,
		Wide:    wide,
		Offsets: offsets,
		Indices: indices,
	}
}

// quantile returns the q-th quantile of vals without disturbing the caller's
// order.
func quantile(vals []float64, q float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	cp := make([]float64, len(vals))
	copy(cp, vals)
	sort.Float64s(cp)
	i := int(q * float64(len(cp)-1))
	return cp[i]
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// lightGridShape picks cell counts per axis. Cells are kept close to cubic so a
// flat scene does not end up with pancake cells that overlap many lights, and
// the total is held under lightGridMaxCells.
func lightGridShape(lo, hi vec.V, nLights int) (dim [3]uint32, cell vec.V) {
	ext := [3]float64{
		math.Max(hi.X-lo.X, 1e-4),
		math.Max(hi.Y-lo.Y, 1e-4),
		math.Max(hi.Z-lo.Z, 1e-4),
	}
	target := nLights * lightGridCellsPerLight
	if target > lightGridMaxCells {
		target = lightGridMaxCells
	}
	if target < 1 {
		target = 1
	}
	// Cubic cell size that would yield `target` cells over this volume.
	size := math.Cbrt(ext[0] * ext[1] * ext[2] / float64(target))
	if size <= 0 || math.IsNaN(size) || math.IsInf(size, 0) {
		size = math.Max(ext[0], math.Max(ext[1], ext[2]))
	}
	for {
		for a := 0; a < 3; a++ {
			d := int(math.Ceil(ext[a] / size))
			if d < 1 {
				d = 1
			}
			dim[a] = uint32(d)
		}
		if int(dim[0])*int(dim[1])*int(dim[2]) <= lightGridMaxCells {
			break
		}
		size *= 1.3
	}
	// The shader receives 1/cell as float32 and multiplies in float32, so bin
	// against the size that reciprocal actually represents. Otherwise Go and
	// the shader disagree about where a cell boundary is, and a light whose
	// sphere ends near one gets filed in the wrong cell.
	//
	// Rounding can shrink a cell, so grow the axis by one to keep dim*cell
	// covering the requested extent.
	for a := 0; a < 3; a++ {
		c := f32CellSize(ext[a] / float64(dim[a]))
		if float64(dim[a])*c < ext[a] {
			dim[a]++
			c = f32CellSize(ext[a] / float64(dim[a]))
		}
		switch a {
		case 0:
			cell.X = c
		case 1:
			cell.Y = c
		case 2:
			cell.Z = c
		}
	}
	return dim, cell
}

// f32CellSize rounds a cell size to what its float32 reciprocal represents,
// matching the shader's arithmetic.
func f32CellSize(size float64) float64 {
	return 1 / float64(float32(1/size))
}

// binEpsilon pads a light's sphere before binning. Go bins in float64 and the
// shader locates cells in float32, so a sphere ending within a few float32 ULPs
// of a cell boundary could otherwise be filed in one cell and looked up in its
// neighbor — a missing light. The pad scales with the coordinates because
// float32 spacing does.
func binEpsilon(lo, hi vec.V) float64 {
	m := math.Max(math.Abs(lo.X), math.Abs(hi.X))
	m = math.Max(m, math.Max(math.Abs(lo.Y), math.Abs(hi.Y)))
	m = math.Max(m, math.Max(math.Abs(lo.Z), math.Abs(hi.Z)))
	return math.Max(1e-3, 1e-4*m)
}

// cellSpan clamps a world-space interval to the grid's cell index range.
func cellSpan(a, b, origin, cell float64, dim uint32) (int, int) {
	i0 := int(math.Floor((a - origin) / cell))
	i1 := int(math.Floor((b - origin) / cell))
	if i0 < 0 {
		i0 = 0
	}
	if i1 > int(dim)-1 {
		i1 = int(dim) - 1
	}
	return i0, i1
}

// sphereHitsCell reports whether a light's influence sphere reaches any part of
// one cell, via the squared distance from the center to the cell box.
func sphereHitsCell(p vec.V, r float64, origin, cell vec.V, i, j, k int) bool {
	d2 := axisGap(p.X, origin.X, cell.X, i) +
		axisGap(p.Y, origin.Y, cell.Y, j) +
		axisGap(p.Z, origin.Z, cell.Z, k)
	return d2 <= r*r
}

// axisGap is the squared distance from p to a cell's extent along one axis, or
// zero when p lies inside it.
func axisGap(p, origin, cell float64, idx int) float64 {
	lo := origin + float64(idx)*cell
	if p < lo {
		return (lo - p) * (lo - p)
	}
	if hi := lo + cell; p > hi {
		return (p - hi) * (p - hi)
	}
	return 0
}
