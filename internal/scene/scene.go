// Package scene defines the renderable primitives and the default "DOS-geo"
// scene ported from the original realtime_raytracer_dos_geo.html.
package scene

import "raytracer/internal/vec"

// CameraStart is an optional initial camera pose supplied by a scene file.
type CameraStart struct {
	Set   bool
	Pos   vec.V
	Yaw   float64
	Pitch float64
}

// Scene holds every primitive and light in the world.
type Scene struct {
	Spheres   []Sphere
	Planes    []Plane
	Boxes     []Box
	Cylinders []Cylinder
	Cones     []Cone
	Tori      []Torus
	Terrains  []Terrain
	Waters    []WaterPool
	Lights    []Light
	Campfires []Campfire
	Start     CameraStart
	Env       Environment
}

// Sky variants. The zero value (SkyClear) reproduces the original clear-day
// gradient + sun used by every existing scene.
const (
	SkyClear      = iota // clear blue day with a bright sun
	SkyCloudy            // overcast daytime with fluffy clouds
	SkyNightStars        // clear night with a starfield
	SkyNightStorm        // dramatic moonlit storm clouds
	SkySunset            // warm sunset with dark, rim-lit clouds
)

// skyNames maps TOML sky identifiers to their constants.
var skyNames = map[string]int{
	"clear":       SkyClear,
	"cloudy":      SkyCloudy,
	"night_stars": SkyNightStars,
	"night_storm": SkyNightStorm,
	"sunset":      SkySunset,
}

// SkyID resolves a sky name to its constant, reporting whether it was known.
func SkyID(name string) (int, bool) {
	id, ok := skyNames[name]
	return id, ok
}

// Environment holds optional global lighting used mainly by outdoor scenes. All
// fields are zero by default, in which case the renderer keeps its original
// behaviour (a flat 0.04 ambient and no directional sun).
type Environment struct {
	// Sky selects the procedural sky variant (see Sky* constants).
	Sky int
	// AmbientSky/AmbientGround give a hemispheric ambient: surfaces facing up
	// receive AmbientSky, those facing down receive AmbientGround. Enabled when
	// AmbientSky is non-zero.
	AmbientSky    vec.V
	AmbientGround vec.V
	// SunDir is the (normalized) direction the sunlight travels. SunColor is its
	// radiance with no distance falloff. Enabled when SunColor is non-zero.
	SunDir   vec.V
	SunColor vec.V
}

// HasAmbient reports whether a hemispheric ambient was configured.
func (e Environment) HasAmbient() bool { return e.AmbientSky != (vec.V{}) }

// HasSun reports whether a directional sun was configured.
func (e Environment) HasSun() bool { return e.SunColor != (vec.V{}) }

func surf(mat int, r, g, b, rough, ior float64) Surface {
	return Surface{Mat: mat, Albedo: vec.New(r, g, b), Rough: rough, IOR: ior}
}

// Default returns the scene used by the original renderer.
func Default() *Scene {
	return &Scene{
		Spheres: []Sphere{
			{Center: vec.New(0, 0.9, 0), Radius: 0.9, Surface: surf(MatMirror, 0.95, 0.95, 0.98, 0.0, 1.5)},
			{Center: vec.New(2.2, 0.5, 0.5), Radius: 0.5, Surface: surf(MatGlass, 0.88, 0.94, 1.0, 0.0, 1.5)},
			{Center: vec.New(-2.1, 0.45, -0.3), Radius: 0.45, Surface: surf(MatDiffuse, 0.9, 0.12, 0.1, 0.0, 1.5)},
			{Center: vec.New(-0.9, 0.35, 2.0), Radius: 0.35, Surface: surf(MatMetal, 1.0, 0.78, 0.2, 0.06, 1.5)},
			{Center: vec.New(1.6, 0.3, -2.0), Radius: 0.3, Surface: surf(MatDiffuse, 0.1, 0.25, 0.95, 0.0, 1.5)},
			{Center: vec.New(2.0, 0.22, 1.8), Radius: 0.22, Surface: surf(MatMirror, 0.9, 0.9, 0.9, 0.01, 1.5)},
			{Center: vec.New(1.0, 3.2, -1.0), Radius: 0.35, Surface: surf(MatEmit, 10, 8, 5, 0.0, 1.5)},
			{Center: vec.New(-1.5, 2.8, 1.5), Radius: 0.25, Surface: surf(MatEmit, 4, 6, 10, 0.0, 1.5)},
		},
		Planes: []Plane{
			{N: vec.New(0, 1, 0), D: 0, Surface: surf(MatChecker, 0.75, 0.7, 0.65, 0, 1.5), Albedo2: vec.New(0.15, 0.15, 0.18)},
			{N: vec.New(0, 0, 1), D: -5.5, Surface: surf(MatDiffuse, 0.5, 0.45, 0.75, 0, 1.5)},
			{N: vec.New(1, 0, 0), D: -4.5, Surface: surf(MatMirror, 0.85, 0.85, 0.90, 0.04, 1.5)},
			{N: vec.New(-1, 0, 0), D: -4.5, Surface: surf(MatDiffuse, 0.2, 0.65, 0.25, 0, 1.5)},
			{N: vec.New(0, -1, 0), D: -5.5, Surface: surf(MatDiffuse, 0.7, 0.7, 0.72, 0, 1.5)},
		},
		Boxes: []Box{
			{Min: vec.New(-4.2, 0, -1.5), Max: vec.New(-3.4, 3.5, -0.7), Surface: surf(MatDiffuse, 0.7, 0.6, 0.5, 0, 1.5)},
			{Min: vec.New(3.4, 0, -1.5), Max: vec.New(4.2, 3.5, -0.7), Surface: surf(MatDiffuse, 0.7, 0.6, 0.5, 0, 1.5)},
			{Min: vec.New(-0.5, 0, -0.5), Max: vec.New(0.5, 0.18, 0.5), Surface: surf(MatMetal, 0.8, 0.8, 0.85, 0.1, 1.5)},
			{Min: vec.New(-4.0, 0, -5.0), Max: vec.New(-1.5, 0.4, -3.5), Surface: surf(MatDiffuse, 0.55, 0.5, 0.65, 0, 1.5)},
			{Min: vec.New(-3.5, 0.4, -4.8), Max: vec.New(-2.0, 0.8, -3.7), Surface: surf(MatDiffuse, 0.6, 0.55, 0.7, 0, 1.5)},
			{Min: vec.New(-3.0, 0.8, -4.6), Max: vec.New(-2.5, 1.2, -3.9), Surface: surf(MatMetal, 0.9, 0.7, 0.2, 0.08, 1.5)},
			{Min: vec.New(2.8, 0, 0.2), Max: vec.New(4.3, 0.45, 2.0), Surface: surf(MatDiffuse, 0.6, 0.35, 0.25, 0, 1.5)},
			{Min: vec.New(-1.5, 0, 3.2), Max: vec.New(-0.5, 2.2, 3.6), Surface: surf(MatMirror, 0.92, 0.92, 0.96, 0.0, 1.5)},
			{Min: vec.New(-2.2, 0, -0.3), Max: vec.New(-1.6, 2.5, 0.3), Surface: surf(MatDiffuse, 0.65, 0.62, 0.7, 0, 1.5)},
			{Min: vec.New(1.6, 0, -0.3), Max: vec.New(2.2, 2.5, 0.3), Surface: surf(MatDiffuse, 0.65, 0.62, 0.7, 0, 1.5)},
			{Min: vec.New(-2.2, 2.3, -0.35), Max: vec.New(2.2, 2.9, 0.35), Surface: surf(MatDiffuse, 0.65, 0.62, 0.7, 0, 1.5)},
		},
		Cylinders: []Cylinder{
			{CX: -3.5, CZ: -3.5, Radius: 0.28, YMin: 0, YMax: 4, Surface: surf(MatDiffuse, 0.72, 0.68, 0.75, 0, 1.5)},
			{CX: 3.5, CZ: -3.5, Radius: 0.28, YMin: 0, YMax: 4, Surface: surf(MatDiffuse, 0.72, 0.68, 0.75, 0, 1.5)},
			{CX: -3.5, CZ: 3.5, Radius: 0.28, YMin: 0, YMax: 4, Surface: surf(MatMetal, 0.8, 0.82, 0.85, 0.05, 1.5)},
			{CX: 3.5, CZ: 3.5, Radius: 0.28, YMin: 0, YMax: 4, Surface: surf(MatMetal, 0.8, 0.82, 0.85, 0.05, 1.5)},
			{CX: 0, CZ: -3.8, Radius: 0.18, YMin: 0, YMax: 3.5, Surface: surf(MatMirror, 0.9, 0.88, 0.92, 0.02, 1.5)},
		},
		Cones: []Cone{
			{CX: -3.5, CZ: -3.5, YBase: 4, YTip: 4.9, RBase: 0.45, Surface: surf(MatDiffuse, 0.72, 0.68, 0.75, 0, 1.5)},
			{CX: 3.5, CZ: -3.5, YBase: 4, YTip: 4.9, RBase: 0.45, Surface: surf(MatDiffuse, 0.72, 0.68, 0.75, 0, 1.5)},
			{CX: -3.5, CZ: 3.5, YBase: 4, YTip: 4.9, RBase: 0.45, Surface: surf(MatMetal, 0.8, 0.82, 0.85, 0.05, 1.5)},
			{CX: 3.5, CZ: 3.5, YBase: 4, YTip: 4.9, RBase: 0.45, Surface: surf(MatMetal, 0.8, 0.82, 0.85, 0.05, 1.5)},
			{CX: -2.75, CZ: -4.25, YBase: 1.2, YTip: 2.4, RBase: 0.6, Surface: surf(MatDiffuse, 0.9, 0.2, 0.15, 0, 1.5)},
		},
		Tori: []Torus{
			{Center: vec.New(0, 1.5, -3.0), R: 0.8, Rm: 0.22, Surface: surf(MatMetal, 1.0, 0.6, 0.1, 0.05, 1.5)},
			{Center: vec.New(3.2, 0.5, -2.5), R: 0.45, Rm: 0.12, Surface: surf(MatMirror, 0.9, 0.9, 0.95, 0.01, 1.5)},
		},
		Lights: []Light{
			{Pos: vec.New(1.0, 3.2, -1.0), Color: vec.New(8, 6, 4), Radius: 0.35},
			{Pos: vec.New(-1.5, 2.8, 1.5), Color: vec.New(3, 4, 8), Radius: 0.25},
			{Pos: vec.New(0, 4, 2), Color: vec.New(2, 2, 2), Radius: 0.5},
		},
	}
}
