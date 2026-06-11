package trace

import (
	"math"
	"runtime"
	"sync"

	"raytracer/internal/vec"
)

// Baked ambient-occlusion volume.
//
// Instead of casting stochastic occlusion probes per pixel each frame (which is
// expensive and, because the dither is screen-anchored, visibly crawls as the
// camera moves), we precompute occlusion once over a regular grid covering the
// scene's finite geometry. The scene is static, so the result is reused every
// frame and is perfectly stable in world space.
//
// A single scalar per cell cannot express that a floor is open "upward" but a
// ceiling is open "downward", so each cell stores six values: the cosine-
// weighted openness of the hemisphere around each axis (+x,-x,+y,-y,+z,-z) -- a
// Valve-style "ambient cube". At shading time we trilinearly interpolate the
// cube and blend its three relevant faces by the surface normal, which
// reconstructs a normal-aware occlusion term with no per-pixel rays.
const (
	aoVolTargetCell = 0.45      // desired grid cell size (world units)
	aoVolMaxAxis    = 128       // hard cap on cells along any axis
	aoVolMaxCells   = 1_000_000 // hard cap on total cells (memory + bake time)
	aoVolBakeDirs   = 32        // sphere probe rays per cell during baking
	aoVolRadius     = aoMaxDist // occlusion probe range (matches the old ray AO)
	aoVolMinVis     = 0.45      // clamp: never darken below this multiplier
	aoVolContrast   = 1.3       // >1 deepens crevices while keeping open areas bright
)

// faceDir indexes: 0:+x 1:-x 2:+y 3:-y 4:+z 5:-z.
type aoVolume struct {
	min  vec.V   // world position of cell (0,0,0)'s center, minus half a cell
	inv  float64 // 1 / cell size (uniform)
	cell float64 // cell size (uniform, world units)
	bias float64 // normal offset applied when sampling, ~ one cell
	nx   int
	ny   int
	nz   int
	data []float32 // nx*ny*nz*6, face-major within each cell
}

// bakeDirs returns a fixed Fibonacci-sphere set of probe directions, computed
// once. Using a deterministic set keeps the bake reproducible.
var bakeDirs = func() []vec.V {
	dirs := make([]vec.V, aoVolBakeDirs)
	ga := math.Pi * (3 - math.Sqrt(5)) // golden angle
	for i := range dirs {
		z := 1 - 2*(float64(i)+0.5)/float64(aoVolBakeDirs)
		r := math.Sqrt(fmax(0, 1-z*z))
		th := ga * float64(i)
		st, ct := math.Sincos(th)
		dirs[i] = vec.V{X: r * ct, Y: z, Z: r * st}
	}
	return dirs
}()

// ambientOcclusion returns the baked AO multiplier in [aoVolMinVis, 1] for a
// surface point ep with normal n. The volume is built lazily on first use (so a
// runtime AO toggle still works) and then shared, immutable, across all render
// goroutines.
func (tr *Tracer) ambientOcclusion(ep, n vec.V) float64 {
	tr.aoOnce.Do(tr.bakeAOVolume)
	if tr.aoVol == nil {
		return 1
	}
	return tr.aoVol.sample(ep, n)
}

// Prepare performs any one-time setup (currently the AO volume bake) so the cost
// is paid up front rather than on the first rendered frame. It is safe to call
// multiple times and from any goroutine.
func (tr *Tracer) Prepare() {
	if tr.Opts.AO {
		tr.aoOnce.Do(tr.bakeAOVolume)
	}
}

// bakeAOVolume fills tr.aoVol by probing occlusion over a grid covering the
// finite geometry. It parallelises across z-slices.
func (tr *Tracer) bakeAOVolume() {
	bmin, bmax, ok := tr.accel.Bounds()
	if !ok {
		return // no finite geometry to occlude against
	}
	// Pad by the probe radius so surfaces lying on the geometry's boundary are
	// surrounded by open-space cells (sampling steps off the surface by ~1 cell).
	pad := vec.V{X: aoVolRadius, Y: aoVolRadius, Z: aoVolRadius}
	bmin = bmin.Sub(pad)
	ext := bmax.Add(pad).Sub(bmin)

	// Pick a uniform cell size honouring the target, the per-axis cap and the
	// total-cell cap.
	cell := aoVolTargetCell
	cell = fmax(cell, fmax(ext.X, fmax(ext.Y, ext.Z))/aoVolMaxAxis)
	var nx, ny, nz int
	for {
		nx = int(math.Ceil(ext.X/cell)) + 1
		ny = int(math.Ceil(ext.Y/cell)) + 1
		nz = int(math.Ceil(ext.Z/cell)) + 1
		if nx < 2 {
			nx = 2
		}
		if ny < 2 {
			ny = 2
		}
		if nz < 2 {
			nz = 2
		}
		if nx*ny*nz <= aoVolMaxCells {
			break
		}
		cell *= 1.15
	}

	v := &aoVolume{
		min:  bmin,
		inv:  1 / cell,
		cell: cell,
		bias: cell, // step a full cell off the surface into open space
		nx:   nx,
		ny:   ny,
		nz:   nz,
		data: make([]float32, nx*ny*nz*6),
	}

	workers := runtime.NumCPU()
	if workers > nz {
		workers = nz
	}
	var wg sync.WaitGroup
	var next int64Counter
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				iz := next.add()
				if iz >= nz {
					return
				}
				tr.bakeSlice(v, iz)
			}
		}()
	}
	wg.Wait()
	tr.aoVol = v
}

// bakeSlice fills one z-slice (all x,y at the given iz) of the volume.
func (tr *Tracer) bakeSlice(v *aoVolume, iz int) {
	occ := make([]float64, len(bakeDirs))
	for iy := 0; iy < v.ny; iy++ {
		for ix := 0; ix < v.nx; ix++ {
			p := vec.V{
				X: v.min.X + (float64(ix)+0.5)*v.cell,
				Y: v.min.Y + (float64(iy)+0.5)*v.cell,
				Z: v.min.Z + (float64(iz)+0.5)*v.cell,
			}
			// One probe per direction, shared across all six faces.
			for d := range bakeDirs {
				t := tr.nearestDist(vec.Ray{Origin: p, Dir: bakeDirs[d]}, aoVolRadius)
				if t < aoVolRadius {
					occ[d] = 1 - t/aoVolRadius
				} else {
					occ[d] = 0
				}
			}
			base := (((iz*v.ny)+iy)*v.nx + ix) * 6
			for f := 0; f < 6; f++ {
				axis := faceAxis(f)
				var num, den float64
				for d := range bakeDirs {
					w := bakeDirs[d].Dot(axis)
					if w <= 0 {
						continue
					}
					num += w * occ[d]
					den += w
				}
				occA := 0.0
				if den > 0 {
					occA = num / den
				}
				open := math.Pow(1-occA, aoVolContrast)
				vis := aoVolMinVis + (1-aoVolMinVis)*open
				v.data[base+f] = float32(vis)
			}
		}
	}
}

// sample trilinearly interpolates the ambient cube around p and blends faces by
// the surface normal. p is first nudged off the surface along n so we read
// open-space cells rather than cells buried inside solids.
func (v *aoVolume) sample(p, n vec.V) float64 {
	p = p.Add(n.Scale(v.bias))

	fx := (p.X-v.min.X)*v.inv - 0.5
	fy := (p.Y-v.min.Y)*v.inv - 0.5
	fz := (p.Z-v.min.Z)*v.inv - 0.5

	ix0, tx := cellFrac(fx, v.nx)
	iy0, ty := cellFrac(fy, v.ny)
	iz0, tz := cellFrac(fz, v.nz)
	ix1 := minInt(ix0+1, v.nx-1)
	iy1 := minInt(iy0+1, v.ny-1)
	iz1 := minInt(iz0+1, v.nz-1)

	// Pre-resolve which face of each axis the normal looks toward, and weights.
	fxIdx, wx := faceForNormal(n.X, 0)
	fyIdx, wy := faceForNormal(n.Y, 2)
	fzIdx, wz := faceForNormal(n.Z, 4)

	corner := func(ix, iy, iz int) float64 {
		base := (((iz*v.ny)+iy)*v.nx + ix) * 6
		return wx*float64(v.data[base+fxIdx]) +
			wy*float64(v.data[base+fyIdx]) +
			wz*float64(v.data[base+fzIdx])
	}

	c000 := corner(ix0, iy0, iz0)
	c100 := corner(ix1, iy0, iz0)
	c010 := corner(ix0, iy1, iz0)
	c110 := corner(ix1, iy1, iz0)
	c001 := corner(ix0, iy0, iz1)
	c101 := corner(ix1, iy0, iz1)
	c011 := corner(ix0, iy1, iz1)
	c111 := corner(ix1, iy1, iz1)

	c00 := c000 + (c100-c000)*tx
	c10 := c010 + (c110-c010)*tx
	c01 := c001 + (c101-c001)*tx
	c11 := c011 + (c111-c011)*tx
	c0 := c00 + (c10-c00)*ty
	c1 := c01 + (c11-c01)*ty
	return c0 + (c1-c0)*tz
}

// faceAxis returns the unit axis vector for face index f.
func faceAxis(f int) vec.V {
	switch f {
	case 0:
		return vec.V{X: 1}
	case 1:
		return vec.V{X: -1}
	case 2:
		return vec.V{Y: 1}
	case 3:
		return vec.V{Y: -1}
	case 4:
		return vec.V{Z: 1}
	default:
		return vec.V{Z: -1}
	}
}

// faceForNormal selects the positive or negative face for an axis given the
// normal's component along it, and returns the cube weight (component squared).
func faceForNormal(comp float64, posFace int) (int, float64) {
	if comp >= 0 {
		return posFace, comp * comp
	}
	return posFace + 1, comp * comp
}

// cellFrac clamps a fractional grid coordinate to [0, n-1] and returns the base
// integer cell and the in-cell fraction in [0,1].
func cellFrac(f float64, n int) (int, float64) {
	if f <= 0 {
		return 0, 0
	}
	if f >= float64(n-1) {
		return n - 1, 0
	}
	i := int(f)
	return i, f - float64(i)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// int64Counter is a tiny atomic work dispenser for the bake goroutines.
type int64Counter struct {
	mu sync.Mutex
	n  int
}

func (c *int64Counter) add() int {
	c.mu.Lock()
	v := c.n
	c.n++
	c.mu.Unlock()
	return v
}
