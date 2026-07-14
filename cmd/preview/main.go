// Command preview renders a scene to PNG files without opening a window, using
// the WebGPU renderer. It auto-frames the subject and writes twelve evenly
// spaced orbit screenshots. Useful for headless verification, screenshots, and
// CI (requires a working WebGPU adapter).
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"

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
	out := flag.String("o", "preview", "output path prefix (writes prefix-00.png … prefix-11.png)")
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
	aspect := float64(*w) / float64(*h)
	prefix := outputPrefix(*out, *scenePath)
	dirs := camera.PreviewOrbitDirections(camera.PreviewViewCount, camera.PreviewElevationRad)

	boundsMin, boundsMax, haveBounds := camera.PreviewSubjectBounds(sc)
	for i, dir := range dirs {
		cam := camera.New()
		if haveBounds {
			pose := camera.OrbitPose(boundsMin, boundsMax, dir, aspect)
			cam.SetPose(pose)
		} else if sc.Start.Set {
			cam.Pos, cam.Yaw, cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
		}

		ren.Render(buf, cam, view, *pix)

		path := fmt.Sprintf("%s-%02d.png", prefix, i)
		if err := writePNG(path, buf, *w, *h); err != nil {
			log.Fatal(err)
		}
		log.Printf("wrote %s (%dx%d)", path, *w, *h)
	}
}

func outputPrefix(flagVal, scenePath string) string {
	prefix := strings.TrimSpace(flagVal)
	if prefix == "" {
		prefix = "preview"
	}
	if strings.HasSuffix(strings.ToLower(prefix), ".png") {
		prefix = strings.TrimSuffix(prefix, filepath.Ext(prefix))
	}
	if prefix == "preview" && scenePath != "" {
		base := strings.TrimSuffix(filepath.Base(scenePath), filepath.Ext(scenePath))
		if base != "" {
			prefix = base
		}
	}
	return prefix
}

func writePNG(path string, buf []byte, w, h int) error {
	img := &image.RGBA{Pix: buf, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return err
	}
	return nil
}
