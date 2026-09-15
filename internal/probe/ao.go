package probe

import (
	"math"
	"runtime"
	"sync"

	"os"
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
	aoVolTargetCell = 0.45 // desired grid cell size (world units)
	aoVolMaxAxis    = 128  // hard cap on cells along any axis
	// With tight bounds the per-axis cap is what binds, not the cell budget,
	// so it is raised and aoVolMaxCells becomes the real limit.
	aoVolTightMaxAxis = 1024
	// aoSmallPrimMax is the largest primitive, on any axis, that the volume is
	// sized to cover. Anything bigger still occludes; it just does not get a
	// vote on resolution. 25 m keeps every wall, stair and piece of furniture
	// in these scenes and drops only mountains and sky shells.
	aoSmallPrimMax = 25.0
	aoVolMaxCells  = 1_000_000          // hard cap on total cells (memory + bake time)
	aoVolBakeDirs  = 32                 // sphere probe rays per cell during baking
	aoVolRadius    = gpuscene.AOMaxDist // occlusion probe range
	aoVolMinVis    = 0.45               // clamp: never darken below this multiplier
	aoVolContrast  = 1.3                // >1 deepens crevices while keeping open areas bright
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
	maxAxis := aoVolMaxAxis
	if aoTightBounds() {
		// Size the grid to the geometry that actually has crevices. Without
		// this the villa's bounds are 2,205 units tall because of one
		// primitive, which forces 17 m cells on a 10 m room; with it they are
		// 198, and the cells land near a metre.
		if tmin, tmax, tok := p.accel.BoundsBelow(aoSmallPrimMax); tok {
			bmin, bmax = tmin, tmax
			maxAxis = aoVolTightMaxAxis
		}
	}
	// Pad by the probe radius so surfaces lying on the geometry's boundary are
	// surrounded by open-space cells (sampling steps off the surface by ~1 cell).
	pad := vec.V{X: aoVolRadius, Y: aoVolRadius, Z: aoVolRadius}
	bmin = bmin.Sub(pad)
	ext := bmax.Add(pad).Sub(bmin)

	// Pick a uniform cell size honouring the target, the per-axis cap and the
	// total-cell cap.
	cell := aoVolTargetCell
	cell = math.Max(cell, math.Max(ext.X, math.Max(ext.Y, ext.Z))/float64(maxAxis))
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

// Bounds returns the scene's geometry bounds, as BakeAO uses them to size the
// AO volume. Exported so offline tools can partition the same space.
func (p *Probe) Bounds() (vec.V, vec.V, bool) {
	return p.accel.Bounds()
}

// LitBounds is the box the live GI probe grid should span: the geometry small
// enough to be part of the built environment, excluding the handful of huge
// primitives that set the raw bounds.
//
// It is the same filter BakeAO uses for its tight-bounds mode, exposed on its
// own because the probe grid needs it whether or not that mode is on. The
// difference is not marginal: the villa's raw bounds are 826 x 2202 x 895 m,
// which at any affordable probe count puts the probes tens of metres apart and
// they see nothing but sky. Filtered they are 163 x 93 x 217 m.
func (p *Probe) LitBounds() (vec.V, vec.V, bool) {
	return p.accel.BoundsBelow(aoSmallPrimMax)
}

// EachPrimBounds visits every primitive's world AABB.
//
// The probe baker uses it to decide which cells are near enough to geometry to
// be worth a probe. Walking the geometry rather than the cells keeps that
// linear in scene size instead of in volume, which is the difference between
// a moment and a minute on a grid of three million cells.
func (p *Probe) EachPrimBounds(fn func(min, max vec.V)) {
	p.accel.EachPrimBounds(fn)
}

// aoTightBounds sizes the AO volume to small-scale geometry only.
//
// Off by default: it changes every baked volume, and the volume feeds the
// ambient term the scenes were authored against. Meant to ship together with
// RAYTRACER_AO_INDIRECT_ONLY, which is what makes a finer volume land only on
// indirect light instead of double-darkening direct light.
func aoTightBounds() bool {
	aoTightOverride.once.Do(func() {
		aoTightOverride.on = os.Getenv("RAYTRACER_AO_TIGHT_BOUNDS") == "1"
	})
	return aoTightOverride.on
}

var aoTightOverride struct {
	once sync.Once
	on   bool
}
