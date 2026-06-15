package probe

import (
	"math"
	"runtime"
	"sync"

	"raytracer/internal/gpuscene"
	"raytracer/internal/vec"
)

// Baked ambient-occlusion volume.
//
// Instead of casting stochastic occlusion probes per pixel each frame (which is
// expensive and, because the dither is screen-anchored, visibly crawls as the
// camera moves), occlusion is precomputed once over a regular grid covering the
// scene's finite geometry. The scene is static, so the result is reused every
// frame and is perfectly stable in world space.
//
// A single scalar per cell cannot express that a floor is open "upward" but a
// ceiling is open "downward", so each cell stores six values: the cosine-
// weighted openness of the hemisphere around each axis (+x,-x,+y,-y,+z,-z) -- a
// Valve-style "ambient cube". The GPU shader trilinearly interpolates the cube
// and blends its three relevant faces by the surface normal, reconstructing a
// normal-aware occlusion term with no per-pixel rays.
const (
	aoVolTargetCell = 0.45      // desired grid cell size (world units)
	aoVolMaxAxis    = 128       // hard cap on cells along any axis
	aoVolMaxCells   = 1_000_000 // hard cap on total cells (memory + bake time)
	aoVolBakeDirs   = 32        // sphere probe rays per cell during baking
	aoVolRadius     = gpuscene.AOMaxDist // occlusion probe range
	aoVolMinVis     = 0.45      // clamp: never darken below this multiplier
	aoVolContrast   = 1.3       // >1 deepens crevices while keeping open areas bright
)

// AOData is the baked ambient-occlusion volume, ready for upload to the GPU.
// Data is NX*NY*NZ*6 float32s, face-major within each cell (face order
// +x,-x,+y,-y,+z,-z). Min is the world position of cell (0,0,0)'s center minus
// half a cell; Inv is 1/Cell; Bias is the normal offset applied when sampling.
type AOData struct {
	Min             vec.V
	Inv, Cell, Bias float64
	NX, NY, NZ      int
	Data            []float32
}

// bakeDirs is a fixed Fibonacci-sphere set of probe directions, computed once.
// A deterministic set keeps the bake reproducible.
var bakeDirs = func() []vec.V {
	dirs := make([]vec.V, aoVolBakeDirs)
	ga := math.Pi * (3 - math.Sqrt(5)) // golden angle
	for i := range dirs {
		z := 1 - 2*(float64(i)+0.5)/float64(aoVolBakeDirs)
		r := math.Sqrt(math.Max(0, 1-z*z))
		th := ga * float64(i)
		st, ct := math.Sincos(th)
		dirs[i] = vec.V{X: r * ct, Y: z, Z: r * st}
	}
	return dirs
}()

// BakeAO probes occlusion over a grid covering the scene's finite geometry and
// returns the volume for upload. ok is false when the scene has no finite
// geometry to occlude against. The bake parallelises across z-slices.
func (p *Probe) BakeAO() (AOData, bool) {
	bmin, bmax, ok := p.accel.Bounds()
	if !ok {
		return AOData{}, false // no finite geometry to occlude against
	}
	// Pad by the probe radius so surfaces lying on the geometry's boundary are
	// surrounded by open-space cells (sampling steps off the surface by ~1 cell).
	pad := vec.V{X: aoVolRadius, Y: aoVolRadius, Z: aoVolRadius}
	bmin = bmin.Sub(pad)
	ext := bmax.Add(pad).Sub(bmin)

	// Pick a uniform cell size honouring the target, the per-axis cap and the
	// total-cell cap.
	cell := aoVolTargetCell
	cell = math.Max(cell, math.Max(ext.X, math.Max(ext.Y, ext.Z))/aoVolMaxAxis)
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

	v := AOData{
		Min:  bmin,
		Inv:  1 / cell,
		Cell: cell,
		Bias: cell, // step a full cell off the surface into open space
		NX:   nx,
		NY:   ny,
		NZ:   nz,
		Data: make([]float32, nx*ny*nz*6),
	}

	workers := runtime.NumCPU()
	if workers > nz {
		workers = nz
	}
	var wg sync.WaitGroup
	var next sliceCounter
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				iz := next.add()
				if iz >= nz {
					return
				}
				p.bakeSlice(&v, iz)
			}
		}()
	}
	wg.Wait()
	return v, true
}

// bakeSlice fills one z-slice (all x,y at the given iz) of the volume.
func (p *Probe) bakeSlice(v *AOData, iz int) {
	occ := make([]float64, len(bakeDirs))
	for iy := 0; iy < v.NY; iy++ {
		for ix := 0; ix < v.NX; ix++ {
			pt := vec.V{
				X: v.Min.X + (float64(ix)+0.5)*v.Cell,
				Y: v.Min.Y + (float64(iy)+0.5)*v.Cell,
				Z: v.Min.Z + (float64(iz)+0.5)*v.Cell,
			}
			// One probe per direction, shared across all six faces.
			for d := range bakeDirs {
				t := p.nearest(vec.Ray{Origin: pt, Dir: bakeDirs[d]}, aoVolRadius)
				if t < aoVolRadius {
					occ[d] = 1 - t/aoVolRadius
				} else {
					occ[d] = 0
				}
			}
			base := (((iz*v.NY)+iy)*v.NX + ix) * 6
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
				v.Data[base+f] = float32(vis)
			}
		}
	}
}

// faceAxis returns the unit axis vector for face index f
// (0:+x 1:-x 2:+y 3:-y 4:+z 5:-z).
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

// sliceCounter is a tiny atomic work dispenser for the bake goroutines.
type sliceCounter struct {
	mu sync.Mutex
	n  int
}

func (c *sliceCounter) add() int {
	c.mu.Lock()
	v := c.n
	c.n++
	c.mu.Unlock()
	return v
}
