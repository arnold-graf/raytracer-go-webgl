package sceneio

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"raytracer/internal/scene"
)

// repoFile resolves a path relative to the repository root from this test file.
func repoFile(rel string) string {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(root, rel)
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
