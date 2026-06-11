// Command preview renders a single frame of the scene to a PNG file without
// opening a window. Useful for headless verification, screenshots, and CI.
package main

import (
	"flag"
	"image"
	"image/png"
	"log"
	"os"

	"raytracer/internal/camera"
	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/trace"
)

func main() {
	w := flag.Int("w", 800, "render width")
	h := flag.Int("h", 600, "render height")
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

	ren := render.New(*w, *h)
	cam := camera.New()
	if sc.Start.Set {
		cam.Pos, cam.Yaw, cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
	}
	tr := trace.New(sc)
	tr.Opts = trace.Options{Mirror: true, Shadow: true, AO: true}
	tr.Time = *atTime
	tr.Prepare() // bake the AO volume up front rather than during the render

	buf := make([]byte, (*w)*(*h)*4)
	ren.Render(buf, cam, tr, *pix)

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
