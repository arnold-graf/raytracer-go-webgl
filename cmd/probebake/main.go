// Command probebake bakes a static indirect-light probe volume for a scene.
//
// The baker is the renderer's own probe pass, driven offline. It is not a
// second implementation of anything: the same dispatch that updates the live
// field updates this one, so every fix the live field has — the capped ray
// distance, the octahedral depth map, the cell-scaled bias, the campfires that
// a parallel shading path once dropped — is inherited rather than reproduced.
// A baker that re-derived the transport would be one more place for a light
// type to go quietly missing.
//
// What offline buys is the ray budget. A frame can afford tens of thousands of
// rays because a software BVH traverses one in about 56 ns; a bake can afford
// as many as it likes. Every artifact the live field has is downstream of that
// budget, so spending it freely is what removes them.
//
// Usage:
//
//	go run ./cmd/probebake -scene scenes/office-sunset/index.toml -spacing 1.0 -rays 512
//	RAYTRACER_GI_BAKE=scenes/office-sunset/index.gi RAYTRACER_LIVE_GI=3 go run .
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"raytracer/internal/camera"
	"raytracer/internal/gibake"
	"raytracer/internal/gpuscene"
	"raytracer/internal/probe"
	"raytracer/internal/render"
	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
	"raytracer/internal/webgpu"
)

// raysPerIteration mirrors GI_PROBE_RAYS in types.wesl. The shader's ray count
// is tied to the SIMD width and does not move; the bake's -rays budget is
// spent as iterations of it instead, each with a fresh rotation of the ray set.
const raysPerIteration = 32

func main() {
	scenePath := flag.String("scene", "scenes/office-sunset/index.toml", "TOML scene to bake")
	out := flag.String("o", "", "output volume (default: the scene path with .gi)")
	spacing := flag.Float64("spacing", 1.0, "probe spacing in metres — the quality/size knob")
	rays := flag.Int("rays", 512, "rays per probe, spent as rays/32 iterations")
	radius := flag.Float64("radius", 0, "keep probes within this distance of geometry, in metres (0 = two cells)")
	maxProbes := flag.Int("max-probes", 400_000, "refuse to bake more probes than this")
	flag.Parse()

	sc, err := sceneio.Load(*scenePath)
	if err != nil {
		log.Fatalf("load scene: %v", err)
	}
	dst := *out
	if dst == "" {
		dst = strings.TrimSuffix(*scenePath, filepath.Ext(*scenePath)) + ".gi"
	}

	// Two cells by default. The read interpolates over the eight probes around
	// a point, so one cell is the minimum that puts probes on the open side of
	// every surface, and a second covers the corner cases. Fixed in metres it
	// would mean something different at every spacing — at 0.5 m a 2.5 m
	// radius keeps five cells out from every wall, which is most of a room.
	if *radius <= 0 {
		*radius = 2 * *spacing
	}

	pb := probe.New(sc)
	aoData, aoOK := pb.BakeAO()
	mn, mx, ok := pb.LitBounds()
	if !ok {
		log.Fatalf("%s has no finite geometry to bake against", *scenePath)
	}
	// A margin of one cell on every side, because the read interpolates over
	// the eight probes around a point: a surface on the boundary still needs
	// probes outside it.
	pad := vec.V{X: *spacing, Y: *spacing, Z: *spacing}
	mn = mn.Sub(pad)
	mx = mx.Add(pad)
	dim := [3]uint32{
		uint32(math.Ceil((mx.X-mn.X)/(*spacing))) + 1,
		uint32(math.Ceil((mx.Y-mn.Y)/(*spacing))) + 1,
		uint32(math.Ceil((mx.Z-mn.Z)/(*spacing))) + 1,
	}
	fmt.Printf("scene %s\n  bounds %.0f x %.0f x %.0f m, grid %dx%dx%d at %.2f m\n",
		*scenePath, mx.X-mn.X, mx.Y-mn.Y, mx.Z-mn.Z, dim[0], dim[1], dim[2], *spacing)

	occ := newOccupancy(pb, mn, *spacing, dim, *radius)
	vol, err := gibake.Build(mn, *spacing, dim, occ.keep)
	if err != nil {
		log.Fatalf("lay out volume: %v", err)
	}
	total := int(dim[0]) * int(dim[1]) * int(dim[2])
	fmt.Printf("  %d of %d cells kept (%.1f%%) within %.1f m of geometry\n",
		vol.Count(), total, 100*float64(vol.Count())/float64(total), *radius)
	if vol.Count() == 0 {
		log.Fatalf("no cells near geometry; raise -radius")
	}
	if vol.Count() > *maxProbes {
		log.Fatalf("%d probes exceeds -max-probes %d; raise -spacing or lower -radius",
			vol.Count(), *maxProbes)
	}
	vol.Alloc(gpuscene.GIProbeFloats)
	iterations := (*rays + raysPerIteration - 1) / raysPerIteration
	vol.Rays = uint32(iterations * raysPerIteration)
	vol.Iterations = uint32(iterations)
	probeBytes := float64(len(vol.Probes)) * 4
	fmt.Printf("  %.0f MB of probes + %.0f MB of index, %d rays each over %d iterations\n",
		probeBytes/1e6, float64(len(vol.Index))*4/1e6, vol.Rays, iterations)

	// Write the empty volume first so the renderer can size its buffer from
	// it, then drive the pass and write it again with the solved field. The
	// renderer reads the file at device init, which is before it has a scene.
	if err := vol.Write(dst); err != nil {
		log.Fatalf("write volume: %v", err)
	}
	os.Setenv("RAYTRACER_GI_BAKE", dst)
	os.Setenv("RAYTRACER_LIVE_GI", "3")

	const w, h = 64, 64 // the probe pass does not care about the framebuffer
	r, err := webgpu.New(w, h)
	if err != nil {
		log.Fatalf("webgpu: %v", err)
	}
	defer r.Release()

	giMin, giMax, giOK := pb.LitBounds()
	view := &render.View{
		Scene: sc, Shadow: true, Mirror: true, AO: true, AOData: aoData, AOok: aoOK,
		GIMin: giMin, GIMax: giMax, GIBoundsOK: giOK,
		AdaptiveAA: false, ColorQuant: 3, MaxBounceDepth: 4,
	}
	cam := camera.New()
	if sc.Start.Set {
		cam.Pos, cam.Yaw, cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
	}
	buf := make([]byte, w*h*4)

	fmt.Printf("  baking")
	start := time.Now()
	for i := 0; i < iterations; i++ {
		// 1/(i+1) makes the blend an exact running mean, so the result is the
		// average of every ray cast rather than an exponential trail over the
		// last few iterations.
		r.DriveBake(float32(1.0 / float64(i+1)))
		r.Render(buf, cam, view, 1)
		if i%16 == 0 || i == iterations-1 {
			fmt.Printf(".")
		}
		if buf[0] == 255 && buf[1] == 0 && buf[2] == 255 {
			log.Fatalf("\nthe renderer failed this frame (it paints magenta on error)")
		}
	}
	r.StopBake()
	fmt.Printf(" %.1fs\n", time.Since(start).Seconds())

	if err := r.ReadBakedProbes(); err != nil {
		log.Fatalf("read back probes: %v", err)
	}
	// Write back the renderer's volume, not this one.
	//
	// webgpu.New loaded the placeholder file into a Volume of its own, and
	// that is the one the readback fills; the copy here still holds the zeros
	// it was written with. Writing it produced a file that loaded fine, made
	// the scene darker — an empty field simply switches the ambient off — and
	// scored beautifully on a leak metric while containing nothing at all.
	vol = r.BakedVolume()
	// A bake that read back zeros is a bake that did not happen, and it looks
	// plausible on screen: an empty field simply switches the ambient off,
	// which darkens the scene and scores well on any leak metric. Check.
	var nonzero int
	var peak float32
	for _, v := range vol.Probes {
		if v > 0 {
			nonzero++
			if v > peak {
				peak = v
			}
		}
	}
	fmt.Printf("  %d of %d probe floats non-zero, peak %.4f\n", nonzero, len(vol.Probes), peak)
	if nonzero == 0 {
		log.Fatalf("the bake produced an empty field")
	}
	if err := vol.Write(dst); err != nil {
		log.Fatalf("write volume: %v", err)
	}
	st, _ := os.Stat(dst)
	fmt.Printf("wrote %s (%.1f MB)\n", dst, float64(st.Size())/1e6)
	fmt.Printf("use it with: RAYTRACER_GI_BAKE=%s RAYTRACER_LIVE_GI=3\n", dst)
}

// occupancy decides which cells get a probe.
//
// Generously: a probe volume that covers only the cells geometry sits in is
// useless, because the read interpolates over the eight probes surrounding a
// point and a surface needs probes on the open side of it too. The test is
// therefore "within radius of something", not "inside something".
type occupancy struct {
	pb      *probe.Probe
	min     vec.V
	spacing float64
	dim     [3]uint32
	radius  float64
	near    []bool
}

func newOccupancy(pb *probe.Probe, min vec.V, spacing float64, dim [3]uint32, radius float64) *occupancy {
	o := &occupancy{pb: pb, min: min, spacing: spacing, dim: dim, radius: radius}
	n := int(dim[0]) * int(dim[1]) * int(dim[2])
	o.near = make([]bool, n)
	// Mark the cells each primitive's bounds touch, dilated by the radius.
	// Walking geometry rather than cells keeps this linear in scene size
	// instead of in volume, which matters: the villa's grid is 3.3M cells.
	pb.EachPrimBounds(func(bmin, bmax vec.V) {
		lo := o.cellOf(bmin.Sub(vec.V{X: radius, Y: radius, Z: radius}))
		hi := o.cellOf(bmax.Add(vec.V{X: radius, Y: radius, Z: radius}))
		for z := lo[2]; z <= hi[2]; z++ {
			for y := lo[1]; y <= hi[1]; y++ {
				for x := lo[0]; x <= hi[0]; x++ {
					o.near[(z*int(dim[1])+y)*int(dim[0])+x] = true
				}
			}
		}
	})
	return o
}

func (o *occupancy) cellOf(p vec.V) [3]int {
	c := [3]int{
		int(math.Floor((p.X - o.min.X) / o.spacing)),
		int(math.Floor((p.Y - o.min.Y) / o.spacing)),
		int(math.Floor((p.Z - o.min.Z) / o.spacing)),
	}
	for i := range c {
		if c[i] < 0 {
			c[i] = 0
		}
		if c[i] >= int(o.dim[i]) {
			c[i] = int(o.dim[i]) - 1
		}
	}
	return c
}

func (o *occupancy) keep(x, y, z uint32) bool {
	return o.near[(int(z)*int(o.dim[1])+int(y))*int(o.dim[0])+int(x)]
}
