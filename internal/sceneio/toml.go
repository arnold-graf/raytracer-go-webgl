// Package sceneio loads a Scene from a human-editable TOML description. The
// format mirrors the primitives in package scene: each primitive kind is an
// array of tables (e.g. [[box]]), and materials are referenced by name.
//
// Numbers must be written as floats (use 0.0, not 0) because vectors decode
// into fixed [3]float64 arrays.
package sceneio

import (
	"fmt"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"raytracer/internal/scene"
	"raytracer/internal/texture"
	"raytracer/internal/vec"
)

// defaultIOR is applied when a surface omits "ior", matching scene.surf.
const defaultIOR = 1.5

// materialByName maps the TOML material strings to the engine's material ids.
var materialByName = map[string]int{
	"diffuse": scene.MatDiffuse,
	"mirror":  scene.MatMirror,
	"metal":   scene.MatMetal,
	"glass":   scene.MatGlass,
	"emit":    scene.MatEmit,
	"checker": scene.MatChecker,
}

type vec3 [3]float64

func (v vec3) toV() vec.V { return vec.New(v[0], v[1], v[2]) }

// surfaceDTO holds the shading fields shared by every primitive table.
type surfaceDTO struct {
	Material string   `toml:"material"`
	Albedo   vec3     `toml:"albedo"`
	Rough    float64  `toml:"rough"`
	IOR      *float64 `toml:"ior"`
	Texture  string   `toml:"texture"`
	Reflect  float64  `toml:"reflect"`
}

func (s surfaceDTO) toSurface() (scene.Surface, error) {
	mat, ok := materialByName[s.Material]
	if !ok {
		return scene.Surface{}, fmt.Errorf("unknown material %q", s.Material)
	}
	tex := texture.None
	if s.Texture != "" {
		if tex, ok = texture.ID(s.Texture); !ok {
			return scene.Surface{}, fmt.Errorf("unknown texture %q", s.Texture)
		}
	}
	ior := defaultIOR
	if s.IOR != nil {
		ior = *s.IOR
	}
	return scene.Surface{Mat: mat, Albedo: s.Albedo.toV(), Rough: s.Rough, IOR: ior, Tex: tex, Reflect: s.Reflect}, nil
}

type sphereDTO struct {
	Center vec3    `toml:"center"`
	Radius float64 `toml:"radius"`
	surfaceDTO
}

type planeDTO struct {
	Normal  vec3    `toml:"normal"`
	D       float64 `toml:"d"`
	Albedo2 vec3    `toml:"albedo2"`
	surfaceDTO
}

type boxDTO struct {
	Min vec3 `toml:"min"`
	Max vec3 `toml:"max"`
	surfaceDTO
}

type cylinderDTO struct {
	CX     float64 `toml:"cx"`
	CZ     float64 `toml:"cz"`
	Radius float64 `toml:"radius"`
	YMin   float64 `toml:"ymin"`
	YMax   float64 `toml:"ymax"`
	surfaceDTO
}

type coneDTO struct {
	CX    float64 `toml:"cx"`
	CZ    float64 `toml:"cz"`
	YBase float64 `toml:"ybase"`
	YTip  float64 `toml:"ytip"`
	RBase float64 `toml:"rbase"`
	surfaceDTO
}

type torusDTO struct {
	Center vec3    `toml:"center"`
	Major  float64 `toml:"major"`
	Minor  float64 `toml:"minor"`
	surfaceDTO
}

type lightDTO struct {
	Pos    vec3    `toml:"pos"`
	Color  vec3    `toml:"color"`
	Radius float64 `toml:"radius"`
	Range  float64 `toml:"range"`
	// Brightness scales the light's intensity independently of its color/range
	// (1 = as authored), mirroring the campfire's brightness knob. It is folded
	// into the color at load time, so culling and shading honor it for free.
	Brightness float64 `toml:"brightness"`
}

// build resolves a light, applying the brightness multiplier (default 1) to the
// color so the rest of the engine only sees a single effective intensity.
func (d lightDTO) build() scene.Light {
	b := d.Brightness
	if b == 0 {
		b = 1
	}
	return scene.Light{Pos: d.Pos.toV(), Color: d.Color.toV().Scale(b), Radius: d.Radius, Range: d.Range}
}

type campfireDTO struct {
	Center     vec3    `toml:"center"`
	Color      vec3    `toml:"color"`
	Brightness float64 `toml:"brightness"`
	Range      float64 `toml:"range"`
	Jitter     float64 `toml:"jitter"`
	Flicker    float64 `toml:"flicker"`
	Speed      float64 `toml:"speed"`
	Seed       float64 `toml:"seed"`
}

// build resolves a campfire, filling in sensible defaults for any omitted
// flicker parameters so a bare [[campfire]] with just a center already looks
// like a fire.
func (d campfireDTO) build() scene.Campfire {
	c := scene.Campfire{
		Center:     d.Center.toV(),
		Color:      d.Color.toV(),
		Brightness: d.Brightness,
		Range:      d.Range,
		Jitter:     d.Jitter,
		Flicker:    d.Flicker,
		Speed:      d.Speed,
		Seed:       d.Seed,
	}
	if c.Color == (vec.V{}) {
		c.Color = vec.New(3.6, 1.7, 0.55) // warm default
	}
	if c.Brightness == 0 {
		c.Brightness = 1
	}
	if c.Jitter == 0 {
		c.Jitter = 0.16
	}
	if c.Flicker == 0 {
		c.Flicker = 0.45
	}
	if c.Speed == 0 {
		c.Speed = 1
	}
	return c
}

type waterDTO struct {
	Pos         [2]float64 `toml:"pos"`
	Radius      float64    `toml:"radius"`
	Level       float64    `toml:"level"`
	Ripple      float64    `toml:"ripple"`
	RippleSpeed float64    `toml:"ripple_animation_speed"`
	RippleDir   [2]float64 `toml:"ripple_direction"`
	surfaceDTO
}

type terrainFeatureDTO struct {
	Kind      string     `toml:"kind"`
	Pos       [2]float64 `toml:"pos"`
	Height    float64    `toml:"height"`
	Width     float64    `toml:"width"`
	Steepness float64    `toml:"steepness"`
	Extend    [2]float64 `toml:"extend"`
	Angle     float64    `toml:"angle"`
}

type terrainDTO struct {
	Origin      vec3       `toml:"origin"`
	Size        [2]float64 `toml:"size"`
	Base        float64    `toml:"base"`
	Detail      float64    `toml:"detail"`
	DetailScale float64    `toml:"detail_scale"`
	Step        float64    `toml:"step"`
	GridCell    float64    `toml:"grid_cell"`

	Grass    string  `toml:"grass"`
	Rock     string  `toml:"rock"`
	Snow     string  `toml:"snow"`
	GrassCol vec3    `toml:"grass_col"`
	RockCol  vec3    `toml:"rock_col"`
	SnowCol  vec3    `toml:"snow_col"`
	SlopeLo  float64 `toml:"slope_lo"`
	SlopeHi  float64 `toml:"slope_hi"`
	SnowLo   float64 `toml:"snow_lo"`
	SnowHi   float64 `toml:"snow_hi"`

	Feature []terrainFeatureDTO `toml:"feature"`
	Pad     []terrainPadDTO     `toml:"pad"`
}

// terrainPadDTO flattens a building site into the terrain. center/half are the
// inner flat rectangle (X/Z); level is the flattened height; margin is the
// width of the smooth blend ring around it.
type terrainPadDTO struct {
	Center [2]float64 `toml:"center"`
	Half   [2]float64 `toml:"half"`
	Level  float64    `toml:"level"`
	Margin float64    `toml:"margin"`
}

type cameraDTO struct {
	Pos   vec3    `toml:"pos"`
	Yaw   float64 `toml:"yaw"`
	Pitch float64 `toml:"pitch"`
}

type environmentDTO struct {
	Sky           string `toml:"sky"`
	AmbientSky    vec3   `toml:"ambient_sky"`
	AmbientGround vec3   `toml:"ambient_ground"`
	SunDir        vec3   `toml:"sun_dir"`
	SunColor      vec3   `toml:"sun_color"`
}

type sceneDTO struct {
	Extends     string          `toml:"extends"`
	Camera      *cameraDTO      `toml:"camera"`
	Environment *environmentDTO `toml:"environment"`
	Sphere      []sphereDTO     `toml:"sphere"`
	Plane       []planeDTO      `toml:"plane"`
	Box         []boxDTO        `toml:"box"`
	Cylinder    []cylinderDTO   `toml:"cylinder"`
	Cone        []coneDTO       `toml:"cone"`
	Torus       []torusDTO      `toml:"torus"`
	Terrain     []terrainDTO    `toml:"terrain"`
	Water       []waterDTO      `toml:"water"`
	Light       []lightDTO      `toml:"light"`
	Campfire    []campfireDTO   `toml:"campfire"`
}

// tintOrWhite returns v as a color, defaulting an omitted (all-zero) vector to
// white so a textured layer shows its natural colors.
func tintOrWhite(v vec3) vec.V {
	if v[0] == 0 && v[1] == 0 && v[2] == 0 {
		return vec.New(1, 1, 1)
	}
	return v.toV()
}

// texOrDefault resolves a texture name, falling back to def when empty.
func texOrDefault(name string, def int) (int, error) {
	if name == "" {
		return def, nil
	}
	id, ok := texture.ID(name)
	if !ok {
		return 0, fmt.Errorf("unknown texture %q", name)
	}
	return id, nil
}

// Load reads and decodes a TOML scene file from disk.
func Load(path string) (*scene.Scene, error) {
	return load(path, map[string]bool{})
}

func load(path string, seen map[string]bool) (*scene.Scene, error) {
	abs, err := filepath.Abs(path)
	if err == nil {
		if seen[abs] {
			return nil, fmt.Errorf("scene extends cycle at %q", path)
		}
		seen[abs] = true
		defer delete(seen, abs)
	}

	var dto sceneDTO
	if _, err := toml.DecodeFile(path, &dto); err != nil {
		return nil, fmt.Errorf("load scene %q: %w", path, err)
	}
	if dto.Extends != "" {
		basePath := dto.Extends
		if !filepath.IsAbs(basePath) {
			basePath = filepath.Join(filepath.Dir(path), basePath)
		}
		base, err := load(basePath, seen)
		if err != nil {
			return nil, err
		}
		if err := dto.applyOverrides(base); err != nil {
			return nil, fmt.Errorf("apply scene overrides %q: %w", path, err)
		}
		return base, nil
	}
	return dto.build()
}

// Decode decodes a TOML scene from an in-memory byte slice (used for the
// embedded default scene).
func Decode(data []byte) (*scene.Scene, error) {
	var dto sceneDTO
	if err := toml.Unmarshal(data, &dto); err != nil {
		return nil, fmt.Errorf("decode scene: %w", err)
	}
	return dto.build()
}

func (dto sceneDTO) applyOverrides(s *scene.Scene) error {
	if dto.Camera != nil {
		s.Start = scene.CameraStart{Set: true, Pos: dto.Camera.Pos.toV(), Yaw: dto.Camera.Yaw, Pitch: dto.Camera.Pitch}
	}
	if dto.Environment != nil {
		env, err := dto.Environment.build()
		if err != nil {
			return err
		}
		s.Env = env
	}
	if dto.Light != nil {
		s.Lights = s.Lights[:0]
		for _, d := range dto.Light {
			s.Lights = append(s.Lights, d.build())
		}
	}
	if dto.Campfire != nil {
		s.Campfires = s.Campfires[:0]
		for _, d := range dto.Campfire {
			s.Campfires = append(s.Campfires, d.build())
		}
	}
	return nil
}

func (dto sceneDTO) build() (*scene.Scene, error) {
	s := &scene.Scene{}

	for i, d := range dto.Sphere {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("sphere[%d]: %w", i, err)
		}
		s.Spheres = append(s.Spheres, scene.Sphere{Center: d.Center.toV(), Radius: d.Radius, Surface: surf})
	}
	for i, d := range dto.Plane {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("plane[%d]: %w", i, err)
		}
		s.Planes = append(s.Planes, scene.Plane{N: d.Normal.toV(), D: d.D, Surface: surf, Albedo2: d.Albedo2.toV()})
	}
	for i, d := range dto.Box {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("box[%d]: %w", i, err)
		}
		s.Boxes = append(s.Boxes, scene.Box{Min: d.Min.toV(), Max: d.Max.toV(), Surface: surf})
	}
	for i, d := range dto.Cylinder {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("cylinder[%d]: %w", i, err)
		}
		s.Cylinders = append(s.Cylinders, scene.Cylinder{CX: d.CX, CZ: d.CZ, Radius: d.Radius, YMin: d.YMin, YMax: d.YMax, Surface: surf})
	}
	for i, d := range dto.Cone {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("cone[%d]: %w", i, err)
		}
		s.Cones = append(s.Cones, scene.Cone{CX: d.CX, CZ: d.CZ, YBase: d.YBase, YTip: d.YTip, RBase: d.RBase, Surface: surf})
	}
	for i, d := range dto.Torus {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("torus[%d]: %w", i, err)
		}
		s.Tori = append(s.Tori, scene.Torus{Center: d.Center.toV(), R: d.Major, Rm: d.Minor, Surface: surf})
	}
	for i, d := range dto.Terrain {
		ter, err := d.build()
		if err != nil {
			return nil, fmt.Errorf("terrain[%d]: %w", i, err)
		}
		s.Terrains = append(s.Terrains, ter)
	}
	for i, d := range dto.Water {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("water[%d]: %w", i, err)
		}
		dirX, dirZ := d.RippleDir[0], d.RippleDir[1]
		if d.RippleSpeed != 0 && dirX == 0 && dirZ == 0 {
			dirX, dirZ = 1, 0.4 // default wind drift when a speed is set
		}
		s.Waters = append(s.Waters, scene.WaterPool{
			CX: d.Pos[0], CZ: d.Pos[1], Radius: d.Radius, Level: d.Level, Ripple: d.Ripple,
			RippleSpeed: d.RippleSpeed, RippleDirX: dirX, RippleDirZ: dirZ, Surface: surf,
		})
	}
	for _, d := range dto.Light {
		s.Lights = append(s.Lights, d.build())
	}
	for _, d := range dto.Campfire {
		s.Campfires = append(s.Campfires, d.build())
	}
	if dto.Camera != nil {
		s.Start = scene.CameraStart{Set: true, Pos: dto.Camera.Pos.toV(), Yaw: dto.Camera.Yaw, Pitch: dto.Camera.Pitch}
	}
	if e := dto.Environment; e != nil {
		env, err := e.build()
		if err != nil {
			return nil, err
		}
		s.Env = env
	}

	return s, nil
}

func (e *environmentDTO) build() (scene.Environment, error) {
	env := scene.Environment{
		AmbientSky:    e.AmbientSky.toV(),
		AmbientGround: e.AmbientGround.toV(),
		SunColor:      e.SunColor.toV(),
	}
	if sd := e.SunDir.toV(); sd != (vec.V{}) {
		env.SunDir = sd.Normalize()
	}
	if e.Sky != "" {
		id, ok := scene.SkyID(e.Sky)
		if !ok {
			return scene.Environment{}, fmt.Errorf("unknown sky %q", e.Sky)
		}
		env.Sky = id
	}
	return env, nil
}

func (d terrainDTO) build() (scene.Terrain, error) {
	grass, err := texOrDefault(d.Grass, texture.Grass)
	if err != nil {
		return scene.Terrain{}, err
	}
	rock, err := texOrDefault(d.Rock, texture.Stone)
	if err != nil {
		return scene.Terrain{}, err
	}
	snow, err := texOrDefault(d.Snow, texture.Snow)
	if err != nil {
		return scene.Terrain{}, err
	}

	ter := scene.Terrain{
		OriginX: d.Origin[0], OriginZ: d.Origin[2],
		SizeX: d.Size[0], SizeZ: d.Size[1],
		Base: d.Base, Detail: d.Detail, DetailScale: d.DetailScale, Step: d.Step, GridCell: d.GridCell,
		Grass: grass, Rock: rock, Snow: snow,
		GrassCol: tintOrWhite(d.GrassCol), RockCol: tintOrWhite(d.RockCol), SnowCol: tintOrWhite(d.SnowCol),
		SlopeLo: d.SlopeLo, SlopeHi: d.SlopeHi, SnowLo: d.SnowLo, SnowHi: d.SnowHi,
	}
	if ter.DetailScale == 0 {
		ter.DetailScale = 0.1
	}
	for _, f := range d.Feature {
		ext := f.Extend
		if ext[0] == 0 {
			ext[0] = 1
		}
		if ext[1] == 0 {
			ext[1] = 1
		}
		w := f.Width
		if w == 0 {
			w = 1
		}
		st := f.Steepness
		if st == 0 {
			st = 2
		}
		h := f.Height
		if f.Kind == "valley" && h > 0 {
			h = -h // allow positive magnitudes for valleys
		}
		ter.Features = append(ter.Features, scene.TerrainFeature{
			PosX: f.Pos[0], PosZ: f.Pos[1], Height: h, Width: w, Steepness: st,
			ExtendX: ext[0], ExtendZ: ext[1], Angle: f.Angle,
		})
	}
	for _, p := range d.Pad {
		ter.Pads = append(ter.Pads, scene.TerrainPad{
			CenterX: p.Center[0], CenterZ: p.Center[1],
			HalfX: p.Half[0], HalfZ: p.Half[1],
			Level: p.Level, Margin: p.Margin,
		})
	}
	ter.Prepare()
	return ter, nil
}
