package webgpu

import (
	"os"
	"strconv"

	"raytracer/internal/bvh"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// Occlusion culling for the light grid.
//
// The grid clusters lights by reach, which is a statement about distance and
// nothing else. A lamp on the far side of a wall is "in range" of this cell, so
// every pixel in it casts a shadow ray to rediscover the wall. Measured on the
// office-sunset server room: 3.24M shadow rays a frame, 75.3% of them blocked,
// for 57% of the frame. Those rays are asking a question the geometry answered
// once, at build time.
//
// So ask it once. For each (cell, light) pair already in the grid, sample the
// cell and see whether the light reaches any of it; if it reaches none, drop
// the pair. The light then costs that cell nothing at all -- no ray, no
// attenuation, no display-space gate.
//
// **Safety is the whole design.** Dropping a light that is in fact visible is a
// hard artifact: a lamp that stops lighting a room. Failing to drop one costs
// only the ray we were already paying. So every ambiguity resolves toward
// keeping the light, and the test is deliberately weaker than the renderer's:
//
//   - Glass never occludes, whatever its shadow flag says. The shader passes
//     GLASS_SHADOW_TRANSMIT through a casting pane rather than blocking it, so
//     treating one as opaque here would delete light that does arrive.
//   - Planes, terrain and instanced geometry are not consulted. All three can
//     block on the GPU; none is in this tree. That only ever keeps pairs.
//   - A pair survives if *any* sample point sees the light.
//
// What it cannot promise is conservatism in the formal sense: a cell whose
// samples are all blocked might still have a sliver of lit surface between
// them. That is why lightVisSamples covers the box rather than a corner or two,
// and why the A/B against an unculled render is a pixel diff rather than a
// glance -- see docs/light-culling.md.
const (
	// How far short of the sample point a ray stops, so a sample sitting on a
	// surface is not occluded by that surface.
	lightVisMargin = 1e-3

	// Glass panes to walk past before giving up and calling it blocked. A ray
	// crossing more than this many is in a hall of windows; treating it as
	// blocked there is the safe direction anyway.
	lightVisGlassDepth = 8
)

// lightVisSamples returns the points of a cell the test asks about: the eight
// corners, the six face centres and the middle. Corners alone miss a cell
// whose only lit part is a face -- a floor cell against a wall is the common
// case -- and the middle alone misses a cell mostly buried in geometry.
func lightVisSamples(lo, hi vec.V, out []vec.V) []vec.V {
	out = out[:0]
	mid := vec.V{X: (lo.X + hi.X) * 0.5, Y: (lo.Y + hi.Y) * 0.5, Z: (lo.Z + hi.Z) * 0.5}
	xs := [3]float64{lo.X, mid.X, hi.X}
	ys := [3]float64{lo.Y, mid.Y, hi.Y}
	zs := [3]float64{lo.Z, mid.Z, hi.Z}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			for k := 0; k < 3; k++ {
				// Keep corners, face centres and the middle; drop edge
				// midpoints, which sit between points already covered.
				n := 0
				if i == 1 {
					n++
				}
				if j == 1 {
					n++
				}
				if k == 1 {
					n++
				}
				if n == 1 {
					continue
				}
				out = append(out, vec.V{X: xs[i], Y: ys[j], Z: zs[k]})
			}
		}
	}
	return out
}

// lightVisBlocked reports whether opaque geometry separates the two points.
// Glass is walked past rather than treated as a blocker; see the note above.
func lightVisBlocked(s *scene.Scene, b *bvh.BVH, from, to vec.V) bool {
	d := to.Sub(from)
	dist := d.Len()
	if dist <= lightVisMargin {
		return false
	}
	dir := d.Scale(1 / dist)
	origin := from
	limit := dist - lightVisMargin
	for pass := 0; pass < lightVisGlassDepth; pass++ {
		t, kind, idx := b.Nearest(vec.Ray{Origin: origin, Dir: dir})
		if t <= 0 || t >= limit {
			return false
		}
		if !lightVisOpaque(s, kind, idx) {
			// Step past the pane and keep going: a wall behind a window still
			// blocks, and a window alone does not.
			origin = origin.Add(dir.Scale(t + lightVisMargin))
			limit -= t + lightVisMargin
			if limit <= 0 {
				return false
			}
			continue
		}
		return true
	}
	return true
}

// lightVisSurface returns the material of a primitive the blocker tree hit.
// Only the material is wanted, so unlike internal/vpl's version this needs no
// normal and no transform work. An unrecognised kind reports false, which the
// caller reads as "not known to be opaque" and so keeps the light.
func lightVisSurface(s *scene.Scene, kind, idx int) (scene.Surface, bool) {
	switch kind {
	case bvh.KindSphere:
		if idx < len(s.Spheres) {
			return s.Spheres[idx].Surface, true
		}
	case bvh.KindBox:
		if idx < len(s.Boxes) {
			return s.Boxes[idx].Surface, true
		}
	case bvh.KindCylinder:
		if idx < len(s.Cylinders) {
			return s.Cylinders[idx].Surface, true
		}
	case bvh.KindCone:
		if idx < len(s.Cones) {
			return s.Cones[idx].Surface, true
		}
	case bvh.KindRing:
		if idx < len(s.Rings) {
			return s.Rings[idx].Surface, true
		}
	case bvh.KindLens:
		if idx < len(s.Lenses) {
			return s.Lenses[idx].Surface, true
		}
	case bvh.KindTorus:
		if idx < len(s.Tori) {
			return s.Tori[idx].Surface, true
		}
	}
	return scene.Surface{}, false
}

// lightVisOpaque reports whether a primitive blocks light outright. Anything
// this cannot identify is called opaque only when the scene says so; unknown
// kinds report false, which keeps the light.
func lightVisOpaque(s *scene.Scene, kind, idx int) bool {
	surf, ok := lightVisSurface(s, kind, idx)
	if !ok {
		return false
	}
	return surf.Mat != scene.MatGlass
}

// cullOccludedLights removes grid entries whose light cannot reach any sampled
// point of the cell. It returns how many entries it dropped and how many it
// kept, for the log line that justifies the build cost.
func cullOccludedLights(g *lightGrid, lights []GPULight, s *scene.Scene) (dropped, kept int) {
	if s == nil || len(g.Offsets) < 2 || len(g.Indices) == 0 {
		return 0, len(g.Indices)
	}
	cellSize := vec.V{X: 1 / g.InvCell.X, Y: 1 / g.InvCell.Y, Z: 1 / g.InvCell.Z}
	if g.InvCell.X == 0 || g.InvCell.Y == 0 || g.InvCell.Z == 0 {
		// The degenerate one-cell grid covers all of space; nothing to cull.
		return 0, len(g.Indices)
	}
	blockers := bvh.NewBlockers(s)

	nx, ny := int(g.Dim[0]), int(g.Dim[1])
	nCells := g.cellCount()
	out := make([]uint32, 0, len(g.Indices))
	offsets := make([]uint32, nCells+1)
	samples := make([]vec.V, 0, 15)

	for c := 0; c < nCells; c++ {
		offsets[c] = uint32(len(out))
		i := c % nx
		j := (c / nx) % ny
		k := c / (nx * ny)
		lo := vec.V{
			X: g.Min.X + float64(i)*cellSize.X,
			Y: g.Min.Y + float64(j)*cellSize.Y,
			Z: g.Min.Z + float64(k)*cellSize.Z,
		}
		hi := lo.Add(cellSize)
		samples = lightVisSamples(lo, hi, samples)

		for e := g.Offsets[c]; e < g.Offsets[c+1]; e++ {
			li := g.Indices[e]
			lp := vec.V{
				X: float64(lights[li].Pos[0]),
				Y: float64(lights[li].Pos[1]),
				Z: float64(lights[li].Pos[2]),
			}
			visible := false
			for _, p := range samples {
				if !lightVisBlocked(s, blockers, lp, p) {
					visible = true
					break
				}
			}
			if visible {
				out = append(out, li)
				kept++
			} else {
				dropped++
			}
		}
	}
	offsets[nCells] = uint32(len(out))
	g.Offsets = offsets
	g.Indices = out
	return dropped, kept
}

// lightCullEnabled gates the pass. **Off by default, because it was measured
// not to pay**, and the reason is the useful part.
//
// The premise was that 75.3% of the server room's 3.24M shadow rays are
// blocked, so most are rediscovering static walls. The pass works -- it drops
// 17% of office-sunset's cell-light pairs and 23% of the villa's, and the frame
// is bit-identical with it on -- and it changes the shadow ray count by 0.003%
// (3,242,802 -> 3,242,700). Nothing.
//
// The composition log in setLights says why. Office-sunset has 315 lights, of
// which **74 are wide**: outside the grid bounds, so every shaded point
// evaluates them wherever it stands. The 2820 clustered pairs this pass works
// on average 1.2 cells per light -- they are already as tight as clustering can
// make them, and the lights it culls were being rejected by distance and by the
// display-space gate before they ever cast a ray.
//
// So a blocked shadow ray here is not waste. It comes from a light that is
// near, significant, and genuinely occluded, and the ray is what draws the
// shadow. The 75% figure measured how much of the scene is in shadow, not how
// much work is wasted -- which is the thing I should have checked first.
//
// Kept and correct, because a scene with real room separation and lights inside
// the grid bounds is exactly what it is for, and this one is not that.
// RAYTRACER_LIGHT_CULL=1 turns it on.
func lightCullEnabled() bool {
	if v, ok := os.LookupEnv("RAYTRACER_LIGHT_CULL"); ok {
		on, err := strconv.ParseBool(v)
		return err == nil && on
	}
	return false
}
