package sceneio

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// repoFile resolves a path relative to the repository root from this test file.
func repoFile(rel string) string {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(root, rel)
}

func TestPineTreeConesUncapped(t *testing.T) {
	s, err := Load(repoFile("scenes/objects/pine-tree.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Cones) != 5 {
		t.Fatalf("got %d cones, want 5", len(s.Cones))
	}
	for i, c := range s.Cones {
		if c.Capped {
			t.Fatalf("cone[%d]: pine-tree cones should set capped = false", i)
		}
	}
}

// TestDefaultTOMLMatchesDefaultScene guarantees scenes/default.toml is a 1:1
// representation of scene.Default(): if either drifts, this fails.
func TestDefaultTOMLMatchesDefaultScene(t *testing.T) {
	data, err := os.ReadFile(repoFile("scenes/default.toml"))
	if err != nil {
		t.Fatalf("read default.toml: %v", err)
	}

	got, err := Decode(data)
	if err != nil {
		t.Fatalf("decode default.toml: %v", err)
	}
	want := scene.Default()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded scene does not match scene.Default()\n got: %+v\nwant: %+v", got, want)
	}
}

func TestBoxPosSizeDecodes(t *testing.T) {
	data := []byte(`
[[box]]
pos_x = -1.0
pos_y = -1.0
pos_z = -1.0
width = 4.0
height = 6.0
depth = 8.0
material = "diffuse"
albedo = [1.0, 1.0, 1.0]
`)
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Boxes) != 1 {
		t.Fatalf("got %d boxes", len(got.Boxes))
	}
	b := got.Boxes[0]
	wantMin := vec.New(-1, -1, -1)
	wantMax := vec.New(3, 5, 7)
	if b.Min != wantMin || b.Max != wantMax {
		t.Fatalf("bounds = (%v, %v), want (%v, %v)", b.Min, b.Max, wantMin, wantMax)
	}
}

func TestConePosSizeDecodes(t *testing.T) {
	data := []byte(`
[[cone]]
pos_x = -0.45
pos_y = 4.4
pos_z = -0.45
width = 0.9
height = 0.9
material = "metal"
albedo = [1.0, 1.0, 1.0]
`)
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Cones) != 1 {
		t.Fatalf("got %d cones", len(got.Cones))
	}
	c := got.Cones[0]
	if math.Abs(c.CX) > 1e-9 || math.Abs(c.CZ) > 1e-9 || math.Abs(c.YBase-4.4) > 1e-9 ||
		math.Abs(c.YTip-5.3) > 1e-9 || math.Abs(c.RBase-0.45) > 1e-9 {
		t.Fatalf("cone = cx=%v cz=%v ybase=%v ytip=%v rbase=%v, want 0 0 4.4 5.3 0.45",
			c.CX, c.CZ, c.YBase, c.YTip, c.RBase)
	}
}

func TestBoxRotatesAboutCenter(t *testing.T) {
	data := []byte(`
[[box]]
pos_x = 1.0
pos_y = 0.0
pos_z = 0.0
width = 2.0
height = 1.0
depth = 1.0
rotate_z = 90.0
material = "diffuse"
albedo = [1.0, 1.0, 1.0]
`)
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	b := got.Boxes[0]
	center := boxCenter(b.Min, b.Max)
	world := b.Xform.ToWorld(center)
	if math.Abs(world.X-center.X) > 1e-9 || math.Abs(world.Y-center.Y) > 1e-9 || math.Abs(world.Z-center.Z) > 1e-9 {
		t.Fatalf("center moved under rotation: local %v -> world %v", center, world)
	}
}

func TestEnvironmentSunBodyDecodes(t *testing.T) {
	data := []byte(`
[environment]
sky = "night_stars"
sun_dir = [0.0, -1.0, 0.0]

[environment.sun]
color = [0.8, 0.9, 1.1]
size = 4.0
`)
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	sun := got.Env.Sun
	if !sun.Visible() {
		t.Fatalf("sun body should be visible: %+v", sun)
	}
	if sun.Color.X != 0.8 || sun.Color.Y != 0.9 || sun.Color.Z != 1.1 || sun.Size != 4.0 {
		t.Fatalf("sun fields wrong: %+v", sun)
	}
	// Glow omitted -> default 1.0 so a configured body has a normal halo.
	if sun.Glow != 1.0 {
		t.Fatalf("default glow = %v, want 1.0", sun.Glow)
	}
}

func TestOutdoorSkyPresetsExtendOutdoorScene(t *testing.T) {
	base, err := Load(repoFile("scenes/outdoors.toml"))
	if err != nil {
		t.Fatalf("load outdoors.toml: %v", err)
	}

	tests := []struct {
		file string
		sky  int
	}{
		{"scenes/outdoors-cloudy.toml", scene.SkyCloudy},
		{"scenes/outdoors-night-stars.toml", scene.SkyNightStars},
		{"scenes/outdoors-night-storm.toml", scene.SkyNightStorm},
		{"scenes/outdoors-sunset.toml", scene.SkySunset},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got, err := Load(repoFile(tt.file))
			if err != nil {
				t.Fatalf("load preset: %v", err)
			}
			if got.Env.Sky != tt.sky {
				t.Fatalf("sky id = %d, want %d", got.Env.Sky, tt.sky)
			}
			if len(got.Terrains) != len(base.Terrains) || len(got.Waters) != len(base.Waters) {
				t.Fatalf("preset did not inherit outdoor terrain/water")
			}
			if len(got.Campfires) != 1 {
				t.Fatalf("preset campfires = %d, want replacement campfire", len(got.Campfires))
			}
		})
	}
}
