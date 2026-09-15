// Command ambientbake bakes per-region ambient cubes for the megakernel's
// [[ambient_zone]] feature, using the path tracer as the reference.
//
// The megakernel's whole indirect-light model is two authored constants
// blended by the surface normal's Y, so a desk under a lit atrium and a shelf
// in a windowless server room receive exactly the same ambient. A zone per
// region replaces that with six measured colours, and unlike a probe lattice
// it cannot leak between rooms, because the boundaries are the rooms.
//
// Zones come either from the scene's own [[ambient_zone]] bounds or, with
// -grid, from partitioning the scene's bounds. Output is TOML on stdout.
//
//	go run ./cmd/ambientbake -scene scenes/office-sunset/index.toml -grid 3,2,3
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"raytracer/internal/camera"
	"raytracer/internal/probe"
	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
	"raytracer/internal/webgpu"
)

func main() {
	scenePath := flag.String("scene", "scenes/office-sunset/index.toml", "scene to bake")
	grid := flag.String("grid", "", "partition into NX,NY,NZ zones instead of using authored ones")
	bounds := flag.String("bounds", "", "box to partition with -grid, as minX,minY,minZ,maxX,maxY,maxZ (default: whole scene)")
	samples := flag.Int("samples", 256, "cosine-weighted rays per probe face")
	depth := flag.Uint("depth", 6, "path depth while baking")
	clock := flag.Float64("time", 0, "animation clock")
	flag.Parse()

	sc, err := sceneio.Load(*scenePath)
	if err != nil {
		log.Fatal(err)
	}

	zones := sc.AmbientZones
	if *grid != "" {
		zones, err = gridZones(sc, *grid, *bounds)
		if err != nil {
			log.Fatal(err)
		}
	}
	if len(zones) == 0 {
		log.Fatalf("%s declares no [[ambient_zone]]; pass -grid NX,NY,NZ to partition its bounds", *scenePath)
	}

	// Probe at each zone's centre. A zone is meant to be a region of roughly
	// uniform indirect light, so one probe is the premise of the feature; if
	// one centre is not representative, the zone is drawn wrong.
	points := make([]vec.V, len(zones))
	for i, z := range zones {
		points[i] = z.Min.Add(z.Max).Scale(0.5)
	}

	// Small render target: the bake dispatches over probes, not pixels, so the
	// framebuffer is only here because the renderer owns the scene upload.
	r, err := webgpu.New(64, 64)
	if err != nil {
		log.Fatalf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	aoData, aoOK := probe.New(sc).BakeAO()
	view := &render.View{
		Scene: sc, Time: *clock, Shadow: true, Mirror: true,
		AO: true, AOData: aoData, AOok: aoOK, MaxBounceDepth: 4,
	}
	cam := camera.New()
	if sc.Start.Set {
		cam.Pos, cam.Yaw, cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
	}

	opts := webgpu.DefaultPTOptions()
	opts.MaxDepth = uint32(*depth)
	// Ambient must not include the thing it is replacing.
	opts.Ambient = false

	pt, err := webgpu.NewPathTracer(r, opts)
	if err != nil {
		log.Fatalf("path tracer: %v", err)
	}
	defer pt.Release()

	fmt.Fprintf(os.Stderr, "baking %d zones x 6 faces x %d samples...\n", len(zones), *samples)
	cubes, err := pt.BakeAmbientProbes(cam, view, points, *samples)
	if err != nil {
		log.Fatalf("bake: %v", err)
	}

	fmt.Printf("# Baked by cmd/ambientbake from %s\n", *scenePath)
	fmt.Printf("# %d zones, %d samples per face. Enable with RAYTRACER_AMBIENT_ZONES=1.\n\n", len(zones), *samples)
	for i, z := range zones {
		fmt.Printf("[[ambient_zone]]\n")
		fmt.Printf("min = [%.3f, %.3f, %.3f]\n", z.Min.X, z.Min.Y, z.Min.Z)
		fmt.Printf("max = [%.3f, %.3f, %.3f]\n", z.Max.X, z.Max.Y, z.Max.Z)
		for f, name := range scene.AmbientZoneFaceNames {
			c := cubes[i][f]
			fmt.Printf("%s = [%.4f, %.4f, %.4f]\n", name, c.X, c.Y, c.Z)
		}
		fmt.Println()
	}
}

func gridZones(sc *scene.Scene, spec, boundsSpec string) ([]scene.AmbientZone, error) {
	parts := strings.Split(spec, ",")
	if len(parts) != 3 {
		return nil, fmt.Errorf("-grid wants NX,NY,NZ, got %q", spec)
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || v < 1 {
			return nil, fmt.Errorf("-grid component %q is not a positive integer", p)
		}
		n[i] = v
	}
	mn, mx, ok := probe.New(sc).Bounds()
	if !ok {
		return nil, fmt.Errorf("scene has no finite bounds to partition")
	}
	if boundsSpec != "" {
		f, err := sixFloats(boundsSpec)
		if err != nil {
			return nil, err
		}
		mn = vec.V{X: f[0], Y: f[1], Z: f[2]}
		mx = vec.V{X: f[3], Y: f[4], Z: f[5]}
	}
	ext := mx.Sub(mn)
	step := vec.V{X: ext.X / float64(n[0]), Y: ext.Y / float64(n[1]), Z: ext.Z / float64(n[2])}
	var out []scene.AmbientZone
	for iz := 0; iz < n[2]; iz++ {
		for iy := 0; iy < n[1]; iy++ {
			for ix := 0; ix < n[0]; ix++ {
				lo := vec.V{
					X: mn.X + step.X*float64(ix),
					Y: mn.Y + step.Y*float64(iy),
					Z: mn.Z + step.Z*float64(iz),
				}
				out = append(out, scene.AmbientZone{Min: lo, Max: lo.Add(step)})
			}
		}
	}
	return out, nil
}

func sixFloats(spec string) ([6]float64, error) {
	var out [6]float64
	parts := strings.Split(spec, ",")
	if len(parts) != 6 {
		return out, fmt.Errorf("-bounds wants six numbers, got %q", spec)
	}
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return out, fmt.Errorf("-bounds component %q is not a number", p)
		}
		out[i] = v
	}
	return out, nil
}
