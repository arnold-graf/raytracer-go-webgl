// packdump writes a frame's bindings using internal/webgpu's device-free
// Packer, in the same layout RT_DUMP_BUFFERS produces.
//
// It exists to prove one thing before a Metal backend is built on the Packer:
// that packing a scene without a GPU yields byte-for-byte what the wgpu upload
// path sends. Run the harness against both dumps; the frames must be identical.
//
//	go run ./tools/metal-rt/packdump -scene scenes/office-sunset/index.toml \
//	    -w 512 -h 320 -cam-x 44.9 -cam-y 201.3 -cam-z 33.1 \
//	    -yaw-deg 91.3 -pitch-deg -0.52 -out tmp/.../bufs
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"raytracer/internal/camera"
	"raytracer/internal/probe"
	"raytracer/internal/render"
	"raytracer/internal/sceneio"
	"raytracer/internal/webgpu"
)

type entry struct {
	Binding uint32 `json:"binding"`
	File    string `json:"file"`
	Size    int    `json:"size"`
}

func main() {
	scenePath := flag.String("scene", "scenes/office-sunset/index.toml", "scene")
	out := flag.String("out", "", "output directory (bufs)")
	w := flag.Int("w", 512, "width")
	h := flag.Int("h", 320, "height")
	camX := flag.Float64("cam-x", math.NaN(), "camera x")
	camY := flag.Float64("cam-y", math.NaN(), "camera y")
	camZ := flag.Float64("cam-z", math.NaN(), "camera z")
	yawDeg := flag.Float64("yaw-deg", math.NaN(), "yaw in degrees")
	pitchDeg := flag.Float64("pitch-deg", math.NaN(), "pitch in degrees")
	quant := flag.Int("quant", 3, "colour quantization")
	depth := flag.Int("depth", 4, "max bounce depth")
	aa := flag.Bool("aa", true, "adaptive AA")
	flag.Parse()
	if *out == "" {
		die(fmt.Errorf("-out is required"))
	}

	sc, err := sceneio.Load(*scenePath)
	if err != nil {
		die(err)
	}
	cam := camera.New()
	cam.Pos, cam.Yaw, cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
	if !math.IsNaN(*camX) {
		cam.Pos.X = *camX
	}
	if !math.IsNaN(*camY) {
		cam.Pos.Y = *camY
	}
	if !math.IsNaN(*camZ) {
		cam.Pos.Z = *camZ
	}
	if !math.IsNaN(*yawDeg) {
		cam.Yaw = *yawDeg * math.Pi / 180
	}
	if !math.IsNaN(*pitchDeg) {
		cam.Pitch = *pitchDeg * math.Pi / 180
	}

	pb := probe.New(sc)
	aoData, aoOK := pb.BakeAO()
	giMin, giMax, giOK := pb.LitBounds()
	view := &render.View{
		Scene: sc, Shadow: true, Mirror: true, AO: true,
		AOData: aoData, AOok: aoOK,
		GIMin: giMin, GIMax: giMax, GIBoundsOK: giOK,
		AdaptiveAA: *aa, ColorQuant: uint32(*quant), MaxBounceDepth: uint32(*depth),
	}

	maxDim := *w
	if *h > maxDim {
		maxDim = *h
	}
	f := webgpu.NewPacker(maxDim).Pack(cam, view, *w, *h)

	if err := os.MkdirAll(*out, 0o755); err != nil {
		die(err)
	}
	var man []entry
	for b, size := range f.Sizes {
		buf := make([]byte, size)
		copy(buf, f.Bindings[b])
		name := fmt.Sprintf("b%02d.bin", b)
		if err := os.WriteFile(filepath.Join(*out, name), buf, 0o644); err != nil {
			die(err)
		}
		man = append(man, entry{Binding: b, File: name, Size: int(size)})
	}
	j, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		die(err)
	}
	if err := os.WriteFile(filepath.Join(*out, "bindings.json"), j, 0o644); err != nil {
		die(err)
	}
	fmt.Printf("packed %d bindings, %d carry data, %dx%d\n", len(f.Sizes), len(f.Bindings), *w, *h)
	fmt.Printf("accel: %d nodes, %d templates, %d instances, blocker section at %d\n",
		len(f.Nodes), len(f.Templates), len(f.Instances), f.BlockerSectionStart)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "packdump:", err)
	os.Exit(1)
}
