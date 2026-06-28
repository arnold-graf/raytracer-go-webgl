// Command preview renders a single frame of the scene to a PNG file without
// opening a window, using the WebGPU renderer. Useful for headless verification,
// screenshots, and CI (requires a working WebGPU adapter).
package main

import (
	"flag"
	"image"
	"image/png"
	"log"
	"os"

	"raytracer/internal/camera"
	"raytracer/internal/probe"
	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/webgpu"
)

// Default render size matches main.go (512 wide, 5:8 aspect).
const (
	defaultRenderW = 512
	defaultRenderH = defaultRenderW * 625 / 1000 // 320
)

func main() {
	w := flag.Int("w", defaultRenderW, "render width")
	h := flag.Int("h", defaultRenderH, "render height")
	pix := flag.Int("pix", 1, "pixel block size (1 = full quality)")
	out := flag.String("o", "preview.png", "output PNG path")
	scenePath := flag.String("scene", "", "path to a TOML scene file (default: built-in scene)")
	atTime := flag.Float64("time", 0, "animation time in seconds (e.g. for water ripples)")
	skyName := flag.String("sky", "", "override sky variant: clear|cloudy|night_stars|night_storm|sunset")
	flag.Parse()

	sc := scene.Default()
	if *scenePath != "" {
		loaded, err := sceneio.Load(*scenePath)
		if err != nil {
			log.Fatal(err)
		}
		sc = loaded
	}
	if *skyName != "" {
		id, ok := scene.SkyID(*skyName)
		if !ok {
			log.Fatalf("unknown sky %q", *skyName)
		}
		sc.Env.Sky = id
	}

	cam := camera.New()
	if sc.Start.Set {
		cam.Pos, cam.Yaw, cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
	}

	ren, err := webgpu.New(*w, *h)
	if err != nil {
		log.Fatalf("webgpu renderer unavailable: %v", err)
	}
	defer ren.Release()

	aoData, aoOK := probe.New(sc).BakeAO()
	view := &render.View{
		Scene:  sc,
		Time:   *atTime,
		Shadow: true,
		Mirror: true,
		AO:     true,
		AOData: aoData,
		AOok:   aoOK,
	}

	buf := make([]byte, (*w)*(*h)*4)
	ren.Render(buf, cam, view, *pix)

	img := &image.RGBA{Pix: buf, Stride: (*w) * 4, Rect: image.Rect(0, 0, *w, *h)}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s (%dx%d)", *out, *w, *h)
}
