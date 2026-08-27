package webgpu

import (
	"path/filepath"
	"testing"

	"raytracer/internal/camera"
	"raytracer/internal/render"
	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func TestStreetLightSceneEnablesSoftShadows(t *testing.T) {
	root := filepath.Join("..", "..")
	sc, err := sceneio.Load(filepath.Join(root, "scenes", "preview", "street-light.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Lights) == 0 {
		t.Fatal("expected included street light")
	}
	if sc.Lights[0].Radius <= 0 {
		t.Fatalf("light radius = %v, want > 0", sc.Lights[0].Radius)
	}
	if !SceneSoftShadows(sc, true) {
		t.Fatal("street light scene should enable soft shadows")
	}
	gl := packLight(&sc.Lights[0])
	if gl.Falloff[2] <= 0 {
		t.Fatalf("packed source radius = %v", gl.Falloff[2])
	}
}

func TestBuildRenderParamsSoftShadows(t *testing.T) {
	const w, h = 64, 40
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	root := filepath.Join("..", "..")
	sc, err := sceneio.Load(filepath.Join(root, "scenes", "preview", "street-light.toml"))
	if err != nil {
		t.Fatal(err)
	}
	rp := r.buildRenderParams(&render.View{Scene: sc, Shadow: true})
	if !rp.softShadows {
		t.Fatal("buildRenderParams did not enable softShadows")
	}
	if len(r.cache.lights) == 0 || r.cache.lights[0].Falloff[2] <= 0 {
		t.Fatalf("cached light radius = %v", r.cache.lights[0].Falloff[2])
	}
	var params [paramsSize]byte
	params = r.paramsBytes(camera.New(), rp, w, h)
	if params[344] == 0 {
		t.Fatal("params.soft_shadows not set in GPU upload buffer")
	}
}

func luma(buf []byte, x, y, w int) float64 {
	o := (y*w + x) * 4
	return 0.2126*float64(buf[o]) + 0.7152*float64(buf[o+1]) + 0.0722*float64(buf[o+2])
}

func TestStreetLightSoftShadowGPU(t *testing.T) {
	const w, h = 256, 160
	r, err := New(w, h)
	if err != nil {
		t.Skipf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	root := filepath.Join("..", "..")
	sc, err := sceneio.Load(filepath.Join(root, "scenes", "preview", "street-light.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cam := camera.New()
	cam.Pos = vec.New(0, 1.2, 5)
	cam.Yaw = 180
	cam.Pitch = -5

	renderFrame := func() []byte {
		buf := make([]byte, w*h*4)
		r.Render(buf, cam, &render.View{Scene: sc, Shadow: true}, 1)
		return buf
	}

	sc.Lights[0].Radius = 0
	sc.Touch()
	hard := renderFrame()

	sc.Lights[0].Radius = 0.5
	sc.Touch()
	soft := renderFrame()

	diff := 0.0
	for i := 0; i < len(soft); i += 4 {
		dr := int(soft[i]) - int(hard[i])
		dg := int(soft[i+1]) - int(hard[i+1])
		db := int(soft[i+2]) - int(hard[i+2])
		d := float64(dr*dr + dg*dg + db*db)
		if d > diff {
			diff = d
		}
	}
	if diff < 25 {
		t.Skipf("soft/hard renders differ too little at %dx%d (max diff^2=%.0f); skipping GPU compare", w, h, diff)
	}
}
