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
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"raytracer/internal/camera"
	"raytracer/internal/npc"
	"raytracer/internal/probe"
	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
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
	views := flag.Int("views", camera.PreviewViewCount, "number of orbit screenshots (use 1 with -view)")
	viewName := flag.String("view", "", "single named view: front|back|left|right|side|top|low")
	zoom := flag.Float64("zoom", 1, "orbit distance multiplier (>1 zooms in, <1 pulls back)")
	elevDeg := flag.Float64("elev", 25, "orbit ring elevation in degrees")
	camPos := flag.String("cam", "", "manual camera position x,y,z (overrides auto orbit)")
	camYaw := flag.Float64("yaw", 0, "camera yaw in degrees (with -cam)")
	camPitch := flag.Float64("pitch", 0, "camera pitch in degrees (with -cam)")
	flag.Parse()

	sc := scene.Default()
	var npcs *npc.Manager
	if *scenePath != "" {
		loaded, err := sceneio.Load(*scenePath)
		if err != nil {
			log.Fatal(err)
		}
		sc = loaded
		if len(sc.NPCSpawns) > 0 {
			npcs = npc.NewManager()
			footWorld := npc.FootWorld(sc)
			if err := npcs.Instantiate(sc, footWorld); err != nil {
				log.Fatal(err)
			}
			for _, sp := range sc.NPCSpawns {
				if sp.Speed > 0.05 || len(sp.Patrol) > 0 {
					for i := 0; i < 180; i++ {
						npcs.Update(sc, footWorld, 1.0/60.0)
					}
					break
				}
			}
		}
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
	var flames scene.FlameSystem
	flameTime := *atTime
	if fires := scene.FlameCampfires(sc.Campfires); len(fires) > 0 {
		if flameTime < 1.5 {
			flameTime = 1.5 // pre-warm so still previews show a lively fire
		}
		flames.SimulateTo(fires, flameTime)
	}
	view := &render.View{
		Scene:  sc,
		Time:   *atTime,
		Shadow: true,
		Mirror: true,
		AO:     true,
		AOData: aoData,
		AOok:   aoOK,
		Flames: &flames,
	}

	buf := make([]byte, (*w)*(*h)*4)
	aspect := float64(*w) / float64(*h)
	prefix := outputPrefix(*out, *scenePath)
	elevRad := *elevDeg * math.Pi / 180

	dirs := previewDirections(*viewName, *views, elevRad)
	if len(dirs) == 0 {
		log.Fatal("no preview views to render")
	}

	boundsMin, boundsMax, haveBounds := camera.PreviewSubjectBounds(sc)
	manualCam, hasManualCam := parseCameraPos(*camPos)
	for i, dir := range dirs {
		cam := camera.New()
		if hasManualCam {
			cam.Pos = manualCam
			cam.Yaw = *camYaw
			cam.Pitch = *camPitch
		} else if haveBounds {
			pose := camera.OrbitPoseZoom(boundsMin, boundsMax, dir, aspect, *zoom)
			cam.SetPose(pose)
		} else if sc.Start.Set {
			cam.Pos, cam.Yaw, cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
		}

		ren.Render(buf, cam, view, *pix)

		path := fmt.Sprintf("%s-%02d.png", prefix, i)
		if len(dirs) == 1 {
			path = prefix + ".png"
		}
		if err := writePNG(path, buf, *w, *h); err != nil {
			log.Fatal(err)
		}
		log.Printf("wrote %s (%dx%d)", path, *w, *h)
	}
}

func previewDirections(viewName string, views int, elevRad float64) []vec.V {
	if viewName != "" {
		dir, ok := camera.PreviewNamedDirection(viewName, elevRad)
		if !ok {
			log.Fatalf("unknown -view %q (try front|back|left|right|side|top|low)", viewName)
		}
		return []vec.V{dir}
	}
	if views <= 0 {
		views = 1
	}
	return camera.PreviewOrbitDirections(views, elevRad)
}

func parseCameraPos(s string) (vec.V, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return vec.V{}, false
	}
	parts := strings.Split(s, ",")
	if len(parts) != 3 {
		log.Fatalf("-cam must be x,y,z got %q", s)
	}
	var xyz [3]float64
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			log.Fatalf("-cam: %v", err)
		}
		xyz[i] = v
	}
	return vec.V{X: xyz[0], Y: xyz[1], Z: xyz[2]}, true
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
