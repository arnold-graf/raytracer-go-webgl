// Package sceneio loads a Scene from a human-editable TOML description. The
// format mirrors the primitives in package scene: each primitive kind is an
// array of tables (e.g. [[box]]), and materials are referenced by name.
//
// Numbers must be written as floats (use 0.0, not 0) because vectors decode
// into fixed [3]float64 arrays.
package sceneio

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"text/template"

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
	Transmit float64  `toml:"transmit"`
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
	return scene.Surface{
		Mat: mat, Albedo: s.Albedo.toV(), Rough: s.Rough, IOR: ior, Tex: tex,
		Reflect: s.Reflect, Transmit: s.Transmit,
	}, nil
}

// transformDTO holds optional per-primitive rotation (degrees) about the
// primitive's geometric center. Omitted angles default to zero (no rotation).
type transformDTO struct {
	RotateX float64 `toml:"rotate_x"`
	RotateY float64 `toml:"rotate_y"`
	RotateZ float64 `toml:"rotate_z"`
}

func (t transformDTO) buildAbout(center vec.V) *scene.Transform {
	if t.RotateX == 0 && t.RotateY == 0 && t.RotateZ == 0 {
		return nil
	}
	return scene.NewTransform(t.RotateX, t.RotateY, t.RotateZ, center)
}

func boxCenter(min, max vec.V) vec.V {
	return vec.V{
		X: (min.X + max.X) / 2,
		Y: (min.Y + max.Y) / 2,
		Z: (min.Z + max.Z) / 2,
	}
}

type sphereDTO struct {
	Center vec3    `toml:"center"`
	Radius float64 `toml:"radius"`
	transformDTO
	surfaceDTO
}

type planeDTO struct {
	Normal  vec3    `toml:"normal"`
	D       float64 `toml:"d"`
	Albedo2 vec3    `toml:"albedo2"`
	transformDTO
	surfaceDTO
}

// holeDTO is a rectangular opening subtracted from a box (see scene.AABB). It
// is authored as a [[box.hole]] sub-table and should pierce fully through the
// faces it cuts. Define with pos_x/pos_y/pos_z and width/height/depth (legacy
// min/max still accepted).
type holeDTO struct {
	boxExtentDTO
}

type boxDTO struct {
	boxExtentDTO
	Hole []holeDTO `toml:"hole"`
	transformDTO
	surfaceDTO
}

// boxExtentDTO accepts either a min corner + size (pos_x, pos_y, pos_z,
// width, height, depth) or legacy opposite corners (min, max).
type boxExtentDTO struct {
	PosX   float64 `toml:"pos_x"`
	PosY   float64 `toml:"pos_y"`
	PosZ   float64 `toml:"pos_z"`
	Width  float64 `toml:"width"`
	Height float64 `toml:"height"`
	Depth  float64 `toml:"depth"`
	Min    vec3    `toml:"min"`
	Max    vec3    `toml:"max"`
}

func (d boxExtentDTO) bounds() (min, max vec.V, err error) {
	if d.Width != 0 || d.Height != 0 || d.Depth != 0 {
		w := math.Abs(d.Width)
		h := math.Abs(d.Height)
		dep := math.Abs(d.Depth)
		if w <= 0 || h <= 0 || dep <= 0 {
			return vec.V{}, vec.V{}, fmt.Errorf("width, height, and depth must be positive")
		}
		return normalizeBounds(
			vec.New(d.PosX, d.PosY, d.PosZ),
			vec.New(d.PosX+w, d.PosY+h, d.PosZ+dep),
		)
	}
	return normalizeBounds(d.Min.toV(), d.Max.toV())
}

func normalizeBounds(a, b vec.V) (vec.V, vec.V, error) {
	min := vec.V{
		X: math.Min(a.X, b.X),
		Y: math.Min(a.Y, b.Y),
		Z: math.Min(a.Z, b.Z),
	}
	max := vec.V{
		X: math.Max(a.X, b.X),
		Y: math.Max(a.Y, b.Y),
		Z: math.Max(a.Z, b.Z),
	}
	min, max = snapBounds(min, max)
	if min.X >= max.X || min.Y >= max.Y || min.Z >= max.Z {
		return vec.V{}, vec.V{}, fmt.Errorf("invalid box bounds (min must be strictly less than max on each axis)")
	}
	return min, max, nil
}

func snapBounds(min, max vec.V) (vec.V, vec.V) {
	snap := func(x float64) float64 {
		return math.Round(x*1e6) / 1e6
	}
	return vec.V{X: snap(min.X), Y: snap(min.Y), Z: snap(min.Z)},
		vec.V{X: snap(max.X), Y: snap(max.Y), Z: snap(max.Z)}
}

type cylinderDTO struct {
	CX        float64 `toml:"cx"`
	CZ        float64 `toml:"cz"`
	Radius    float64 `toml:"radius"`
	RadiusTop float64 `toml:"radius_top"`
	YMin      float64 `toml:"ymin"`
	YMax      float64 `toml:"ymax"`
	transformDTO
	surfaceDTO
}

type coneDTO struct {
	CX    float64 `toml:"cx"`
	CZ    float64 `toml:"cz"`
	YBase float64 `toml:"ybase"`
	YTip  float64 `toml:"ytip"`
	RBase float64 `toml:"rbase"`
	transformDTO
	surfaceDTO
}

type torusDTO struct {
	Center vec3    `toml:"center"`
	Major  float64 `toml:"major"`
	Minor  float64 `toml:"minor"`
	transformDTO
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

type soundDTO struct {
	Sound  string  `toml:"sound"`
	At     vec3    `toml:"at"`
	Gain   float64 `toml:"gain"`
	Radius float64 `toml:"radius"`
}

// build resolves a spatial ambience emitter, filling in sensible defaults.
func (d soundDTO) build() (scene.Ambience, error) {
	if d.Sound == "" {
		return scene.Ambience{}, fmt.Errorf("missing sound")
	}
	gain := d.Gain
	if gain == 0 {
		gain = 0.3
	}
	radius := d.Radius
	if radius == 0 {
		radius = 20
	}
	return scene.Ambience{
		Sound:  d.Sound,
		Pos:    d.At.toV(),
		Gain:   gain,
		Radius: radius,
	}, nil
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
	Sky           string  `toml:"sky"`
	AmbientSky    vec3    `toml:"ambient_sky"`
	AmbientGround vec3    `toml:"ambient_ground"`
	SunDir        vec3    `toml:"sun_dir"`
	SunColor      vec3    `toml:"sun_color"`
	Sun           *sunDTO `toml:"sun"`
}

// sunDTO configures the visible celestial body (sun/moon disc). Its position is
// taken from the environment's sun_dir, so only its appearance lives here.
type sunDTO struct {
	Color vec3    `toml:"color"`
	Size  float64 `toml:"size"` // angular diameter in degrees
	Glow  float64 `toml:"glow"` // halo strength (omitted/0 -> default 1.0)
}

// includeDTO references another TOML file as a composite object. The included
// file's primitives are merged into the parent scene after applying the instance
// transform (rotate about the sub-scene origin, then translate by at).
//
// When the parent scene has a terrain height field, at.y is an offset above the
// ground at (at.x, at.z) — 0 places the object's origin on the ground. If the
// included object declares a [[terrain.pad]] covering its origin, the pad's
// level is used instead of the wild terrain height (the pad is merged after
// placement and defines the object's grade). Object files that only carry pad
// stubs without a footprint do not count as height fields for nested includes.
//
// follow_terrain defers Y placement to a post-pass after all terrain (including
// features from other includes) is merged and baked. Each include with
// follow_terrain = true snaps its local origin onto the terrain at its world
// (x,z); at.y is an offset above that surface. The flag inherits to nested
// [[include]] entries so a cluster can snap each child at its own position.
//
// Params are passed to the included file as Go text/template data, so an object
// can be parameterized (e.g. params = { stem_len = 2.0 }). The object reads them
// as {{.stem_len}} and can derive geometry with the add/sub/mul/div/neg helpers;
// rgb arrays use orVec3/vec3 (templates cannot write […] literals). Missing
// params fall back to the object's own `or .x <default>` defaults. Files
// with no {{ }} are passed through verbatim, so this is opt-in per object.
type includeDTO struct {
	File           string         `toml:"file"`
	At             vec3           `toml:"at"`
	RotateX        float64        `toml:"rotate_x"`
	RotateY        float64        `toml:"rotate_y"`
	RotateZ        float64        `toml:"rotate_z"`
	FollowTerrain  bool           `toml:"follow_terrain"`
	Params         map[string]any `toml:"params"`
}

type interactDTO struct {
	Hint    string  `toml:"hint"`
	OnUse   string  `toml:"on_use"`
	Range   float64 `toml:"use_range"`
	Center  vec3    `toml:"center"`
}

func (d interactDTO) build() scene.Interactable {
	return scene.Interactable{
		Hint:    d.Hint,
		Handler: d.OnUse,
		Range:   d.Range,
		Center:  d.Center.toV(),
	}
}

type playerSpawnpointDTO struct {
	ID      string   `toml:"id"`
	Pos     vec3     `toml:"pos"`
	FloorY  *float64 `toml:"floor_y"`
	Yaw     float64  `toml:"yaw"`
	Pitch   float64  `toml:"pitch"`
}

func (d playerSpawnpointDTO) build() (scene.PlayerSpawnpoint, error) {
	if d.ID == "" {
		return scene.PlayerSpawnpoint{}, fmt.Errorf("missing id")
	}
	sp := scene.PlayerSpawnpoint{
		ID:    d.ID,
		Pos:   d.Pos.toV(),
		Yaw:   d.Yaw,
		Pitch: d.Pitch,
	}
	if d.FloorY != nil {
		sp.FloorY = *d.FloorY
		sp.UseFloor = true
	}
	return sp, nil
}

type sceneDTO struct {
	Extends     string          `toml:"extends"`
	Camera      *cameraDTO      `toml:"camera"`
	Environment *environmentDTO `toml:"environment"`
	Interact    *interactDTO    `toml:"interact"`
	Include     []includeDTO    `toml:"include"`
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
	Sound       []soundDTO      `toml:"sound"`
	PlayerSpawnpoint []playerSpawnpointDTO `toml:"player_spawnpoint"`
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
	s, _, err := LoadDeps(path)
	return s, err
}

// LoadDeps is Load that also reports every file the scene depends on: the file
// itself plus everything reached through "extends" and [[include]]. Callers
// (e.g. the hot-reload watcher) can stat these to detect edits to included
// sub-scenes, not just the top-level file. Paths are absolute and deduplicated.
func LoadDeps(path string) (*scene.Scene, []string, error) {
	var deps []string
	var followPlacements []scene.TerrainFollowPlacement
	s, err := load(path, nil, map[string]bool{}, &deps, false, &followPlacements)
	if err != nil {
		return nil, deps, err
	}
	s.PrepareTerrains()
	s.ApplyTerrainFollow(followPlacements)
	return s, deps, nil
}

// recordDep appends path's absolute form to *deps if not already present.
func recordDep(deps *[]string, path string) {
	if deps == nil {
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	for _, d := range *deps {
		if d == abs {
			return
		}
	}
	*deps = append(*deps, abs)
}

// load reads, templates and decodes the scene at path. params is the template
// data supplied by a parent [[include]] (nil for the top-level file and for
// "extends" bases, which take no parameters). inheritFollowTerrain propagates
// follow_terrain from an ancestor include to nested placements.
func load(path string, params map[string]any, seen map[string]bool, deps *[]string, inheritFollowTerrain bool, followPlacements *[]scene.TerrainFollowPlacement) (*scene.Scene, error) {
	recordDep(deps, path)
	abs, err := filepath.Abs(path)
	if err == nil {
		if seen[abs] {
			return nil, fmt.Errorf("scene extends cycle at %q", path)
		}
		seen[abs] = true
		defer delete(seen, abs)
	}

	dto, err := decodeSceneFile(path, params)
	if err != nil {
		return nil, err
	}
	if dto.Extends != "" {
		basePath := dto.Extends
		if !filepath.IsAbs(basePath) {
			basePath = filepath.Join(filepath.Dir(path), basePath)
		}
		base, err := load(basePath, nil, seen, deps, false, nil)
		if err != nil {
			return nil, err
		}
		if err := dto.applyOverrides(base); err != nil {
			return nil, fmt.Errorf("apply scene overrides %q: %w", path, err)
		}
		var extendPlacements []scene.TerrainFollowPlacement
		for i, inc := range dto.Include {
			if err := mergeInclude(base, inc, filepath.Dir(path), i, seen, deps, false, &extendPlacements); err != nil {
				return nil, err
			}
		}
		base.PrepareTerrains()
		base.ApplyTerrainFollow(extendPlacements)
		return base, nil
	}
	return dto.buildWithIncludes(path, seen, deps, inheritFollowTerrain, followPlacements)
}

// decodeSceneFile reads the file at path, runs it through the object template
// engine with params (a no-op for files that contain no {{ }} actions) and
// decodes the resulting TOML into a sceneDTO.
func decodeSceneFile(path string, params map[string]any) (sceneDTO, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return sceneDTO{}, fmt.Errorf("read scene %q: %w", path, err)
	}
	rendered, err := renderObjectTemplate(path, raw, params)
	if err != nil {
		return sceneDTO{}, err
	}
	var dto sceneDTO
	if _, err := toml.Decode(string(rendered), &dto); err != nil {
		return sceneDTO{}, fmt.Errorf("load scene %q: %w", path, err)
	}
	return dto, nil
}

// renderObjectTemplate expands {{ }} template actions in an object file using
// params as the data. Files without any "{{" are returned verbatim so ordinary
// scenes never pay the templating cost (and can't trip over a stray brace).
func renderObjectTemplate(path string, raw []byte, params map[string]any) ([]byte, error) {
	if !bytes.Contains(raw, []byte("{{")) {
		return raw, nil
	}
	t, err := template.New(filepath.Base(path)).Funcs(objectTemplateFuncs).Option("missingkey=zero").Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("parse object template %q: %w", path, err)
	}
	if params == nil {
		params = map[string]any{}
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, params); err != nil {
		return nil, fmt.Errorf("render object template %q: %w", path, err)
	}
	return buf.Bytes(), nil
}

// objectTemplateFuncs are the arithmetic helpers available to parameterized
// object files. They coerce ints/floats/strings to float64 so derived geometry
// (e.g. an orb that hangs below a variable-length stem) can be computed inline.
var objectTemplateFuncs = template.FuncMap{
	"add": func(xs ...any) float64 {
		var s float64
		for _, x := range xs {
			s += toFloat(x)
		}
		return s
	},
	"sub": func(a, b any) float64 { return toFloat(a) - toFloat(b) },
	"mul": func(xs ...any) float64 {
		p := 1.0
		for _, x := range xs {
			p *= toFloat(x)
		}
		return p
	},
	"div": func(a, b any) float64 { return toFloat(a) / toFloat(b) },
	"neg": func(a any) float64 { return -toFloat(a) },
	// seq returns [0, 1, …, n-1] for use with {{range}} in parameterized objects
	// (e.g. generating staircase steps from params.steps).
	"seq": func(n any) []int {
		count := int(toFloat(n))
		if count <= 0 {
			return nil
		}
		out := make([]int, count)
		for i := range out {
			out[i] = i
		}
		return out
	},
	// vec3 formats three components as a TOML rgb array, e.g. [0.5, 0.49, 0.47].
	"vec3": func(r, g, b any) string {
		return formatVec3(toFloat(r), toFloat(g), toFloat(b))
	},
	// orVec3 uses v when it is a three-element array (from params.albedo = […]);
	// otherwise falls back to defR, defG, defB. Go templates cannot write […]
	// literals, so defaults must be passed as separate scalars.
	"orVec3": func(v, defR, defG, defB any) string {
		if xs, ok := vec3Values(v); ok {
			return formatVec3(xs[0], xs[1], xs[2])
		}
		return formatVec3(toFloat(defR), toFloat(defG), toFloat(defB))
	},
	"use_button": func() string { return "E" },
}

// toFloat coerces a template value (TOML decodes numbers as int64/float64) to a
// float64; unparseable values become 0.
func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	default:
		return 0
	}
}

func formatVec3(r, g, b float64) string {
	return fmt.Sprintf("[%g, %g, %g]", r, g, b)
}

func vec3Values(v any) ([3]float64, bool) {
	switch xs := v.(type) {
	case []any:
		if len(xs) < 3 {
			return [3]float64{}, false
		}
		return [3]float64{toFloat(xs[0]), toFloat(xs[1]), toFloat(xs[2])}, true
	case []float64:
		if len(xs) < 3 {
			return [3]float64{}, false
		}
		return [3]float64{xs[0], xs[1], xs[2]}, true
	case [3]float64:
		return xs, true
	default:
		return [3]float64{}, false
	}
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
	// Extends children may add [[terrain.pad]] tables (with a stub [[terrain]]
	// header so TOML decode succeeds). Merge pads into the base heightfield.
	var pads []scene.TerrainPad
	for _, td := range dto.Terrain {
		for _, p := range td.Pad {
			pads = append(pads, scene.TerrainPad{
				CenterX: p.Center[0], CenterZ: p.Center[1],
				HalfX: p.Half[0], HalfZ: p.Half[1],
				Level: p.Level, Margin: p.Margin,
			})
		}
	}
	if len(pads) > 0 && len(s.Terrains) == 0 {
		return fmt.Errorf("scene defines terrain.pad but base has no terrain")
	}
	addTerrainPads(s, pads)
	return nil
}

func (dto sceneDTO) build() (*scene.Scene, error) {
	s := &scene.Scene{}

	for i, d := range dto.Sphere {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("sphere[%d]: %w", i, err)
		}
		center := d.Center.toV()
		surf.Xform = d.transformDTO.buildAbout(center)
		s.Spheres = append(s.Spheres, scene.Sphere{Center: center, Radius: d.Radius, Surface: surf})
	}
	for i, d := range dto.Plane {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("plane[%d]: %w", i, err)
		}
		surf.Xform = d.transformDTO.buildAbout(vec.V{})
		s.Planes = append(s.Planes, scene.Plane{N: d.Normal.toV(), D: d.D, Surface: surf, Albedo2: d.Albedo2.toV()})
	}
	for i, d := range dto.Box {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("box[%d]: %w", i, err)
		}
		min, max, err := d.bounds()
		if err != nil {
			return nil, fmt.Errorf("box[%d]: %w", i, err)
		}
		surf.Xform = d.transformDTO.buildAbout(boxCenter(min, max))
		var holes []scene.AABB
		for j, h := range d.Hole {
			hmin, hmax, err := h.bounds()
			if err != nil {
				return nil, fmt.Errorf("box[%d].hole[%d]: %w", i, j, err)
			}
			holes = append(holes, scene.AABB{Min: hmin, Max: hmax})
		}
		s.Boxes = append(s.Boxes, scene.Box{Min: min, Max: max, Holes: holes, Surface: surf})
	}
	for i, d := range dto.Cylinder {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("cylinder[%d]: %w", i, err)
		}
		center := vec.New(d.CX, (d.YMin+d.YMax)/2, d.CZ)
		surf.Xform = d.transformDTO.buildAbout(center)
		s.Cylinders = append(s.Cylinders, scene.Cylinder{
			CX: d.CX, CZ: d.CZ, Radius: d.Radius, RadiusTop: d.RadiusTop,
			YMin: d.YMin, YMax: d.YMax, Surface: surf,
		})
	}
	for i, d := range dto.Cone {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("cone[%d]: %w", i, err)
		}
		center := vec.New(d.CX, (d.YBase+d.YTip)/2, d.CZ)
		surf.Xform = d.transformDTO.buildAbout(center)
		s.Cones = append(s.Cones, scene.Cone{CX: d.CX, CZ: d.CZ, YBase: d.YBase, YTip: d.YTip, RBase: d.RBase, Surface: surf})
	}
	for i, d := range dto.Torus {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("torus[%d]: %w", i, err)
		}
		center := d.Center.toV()
		surf.Xform = d.transformDTO.buildAbout(center)
		s.Tori = append(s.Tori, scene.Torus{Center: center, R: d.Major, Rm: d.Minor, Surface: surf})
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
	for i, d := range dto.Sound {
		a, err := d.build()
		if err != nil {
			return nil, fmt.Errorf("sound[%d]: %w", i, err)
		}
		s.Ambiences = append(s.Ambiences, a)
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
	if dto.Interact != nil {
		s.Interactables = append(s.Interactables, dto.Interact.build())
	}
	seenSpawn := map[string]bool{}
	for i, d := range dto.PlayerSpawnpoint {
		sp, err := d.build()
		if err != nil {
			return nil, fmt.Errorf("player_spawnpoint[%d]: %w", i, err)
		}
		if seenSpawn[sp.ID] {
			return nil, fmt.Errorf("player_spawnpoint[%d]: duplicate id %q", i, sp.ID)
		}
		seenSpawn[sp.ID] = true
		s.Spawnpoints = append(s.Spawnpoints, sp)
	}

	return s, nil
}

// buildWithIncludes builds the scene and merges any [[include]] composite files.
func (dto sceneDTO) buildWithIncludes(path string, seen map[string]bool, deps *[]string, inheritFollowTerrain bool, followPlacements *[]scene.TerrainFollowPlacement) (*scene.Scene, error) {
	s, err := dto.build()
	if err != nil {
		return nil, err
	}
	for i, inc := range dto.Include {
		if err := mergeInclude(s, inc, filepath.Dir(path), i, seen, deps, inheritFollowTerrain, followPlacements); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func mergeInclude(dst *scene.Scene, inc includeDTO, parentDir string, index int, seen map[string]bool, deps *[]string, inheritFollowTerrain bool, followPlacements *[]scene.TerrainFollowPlacement) error {
	incPath := inc.File
	if !filepath.IsAbs(incPath) {
		incPath = filepath.Join(parentDir, incPath)
	}
	follow := inc.FollowTerrain || inheritFollowTerrain
	var subPlacements []scene.TerrainFollowPlacement
	sub, err := load(incPath, inc.Params, seen, deps, follow, &subPlacements)
	if err != nil {
		return fmt.Errorf("include[%d] %q: %w", index, inc.File, err)
	}
	xf := instanceTransform(dst, sub, inc, follow)
	before := scene.CountPrimitives(dst)
	mergeScene(dst, sub, xf)
	if followPlacements != nil {
		if len(subPlacements) > 0 {
			scene.OffsetPlacements(subPlacements, before)
			*followPlacements = append(*followPlacements, subPlacements...)
		} else if follow {
			after := scene.CountPrimitives(dst)
			*followPlacements = append(*followPlacements, scene.PlacementFromRange(before, after, inc.At.toV().Y))
		}
	}
	return nil
}

// instanceTransform builds the world placement for an include. When follow is
// false, at.y is raised by the pad or terrain height at (at.x, at.z) when
// available. When follow is true, Y is deferred to ApplyTerrainFollow and at.y
// is kept as an offset above the sampled ground.
func instanceTransform(dst *scene.Scene, sub *scene.Scene, inc includeDTO, follow bool) *scene.Transform {
	at := inc.At.toV()
	if level, ok := sub.PadLevelAt(0, 0); ok {
		at.Y = level + at.Y
	} else if !follow {
		if h, ok := dst.TerrainHeightAt(at.X, at.Z); ok {
			at.Y = h + at.Y
		}
	}
	return scene.NewInstanceTransform(inc.RotateX, inc.RotateY, inc.RotateZ, at)
}

// mergeScene appends every primitive from sub into dst, composing each
// primitive's local transform with the instance transform xf.
func mergeScene(dst, sub *scene.Scene, xf *scene.Transform) {
	// Finite primitives keep their geometry in the sub-scene's local space and
	// carry the composed instance transform; the BVH, CPU tracer and GPU all
	// intersect in local space and map back via Xform (see bvh.addBounded and
	// trace.intersect). Baking the placement into the center/extent as well as
	// the Xform would translate the primitive twice, so we only compose Xform —
	// exactly as boxes already did.
	for i := range sub.Spheres {
		o := sub.Spheres[i]
		o.Xform = xf.Compose(o.Xform)
		dst.Spheres = append(dst.Spheres, o)
	}
	for i := range sub.Planes {
		o := sub.Planes[i]
		o.Xform = xf.Compose(o.Xform)
		if xf != nil {
			// Planes are not in the BVH; the tracer uses their world-space N/D
			// directly (ignoring Xform), so bake the placement into N and D.
			pp := planePoint(o.N, o.D)
			o.N = xf.WorldNormal(o.N)
			o.D = -o.N.Dot(xf.ToWorld(pp))
		}
		dst.Planes = append(dst.Planes, o)
	}
	for i := range sub.Boxes {
		o := sub.Boxes[i]
		o.Xform = xf.Compose(o.Xform)
		dst.Boxes = append(dst.Boxes, o)
	}
	for i := range sub.Cylinders {
		o := sub.Cylinders[i]
		o.Xform = xf.Compose(o.Xform)
		dst.Cylinders = append(dst.Cylinders, o)
	}
	for i := range sub.Cones {
		o := sub.Cones[i]
		o.Xform = xf.Compose(o.Xform)
		dst.Cones = append(dst.Cones, o)
	}
	for i := range sub.Tori {
		o := sub.Tori[i]
		o.Xform = xf.Compose(o.Xform)
		dst.Tori = append(dst.Tori, o)
	}
	for i := range sub.Lights {
		l := sub.Lights[i]
		if xf != nil {
			l.Pos = xf.ToWorld(l.Pos)
		}
		dst.Lights = append(dst.Lights, l)
	}
	for i := range sub.Campfires {
		c := sub.Campfires[i]
		if xf != nil {
			c.Center = xf.ToWorld(c.Center)
		}
		dst.Campfires = append(dst.Campfires, c)
	}
	for i := range sub.Ambiences {
		a := sub.Ambiences[i]
		if xf != nil {
			a.Pos = xf.ToWorld(a.Pos)
		}
		dst.Ambiences = append(dst.Ambiences, a)
	}
	// An included object can flatten the ground under itself by declaring
	// [[terrain.pad]] entries (with a stub [[terrain]] header so the TOML
	// decodes). It can also contribute [[terrain.feature]] peaks/valleys/ridges;
	// features are merged into the parent height field with the instance transform
	// applied to each feature's position (and yaw added to its angle).
	var pads []scene.TerrainPad
	var features []scene.TerrainFeature
	for i := range sub.Terrains {
		yaw := xf.YawRad()
		for _, p := range sub.Terrains[i].Pads {
			if xf != nil {
				c := xf.ToWorld(vec.New(p.CenterX, 0, p.CenterZ))
				p.CenterX, p.CenterZ = c.X, c.Z
				p.Angle += yaw
			}
			pads = append(pads, p)
		}
		for _, f := range sub.Terrains[i].Features {
			if xf != nil {
				w := xf.ToWorld(vec.New(f.PosX, 0, f.PosZ))
				f.PosX, f.PosZ = w.X, w.Z
				f.Angle += yaw
			}
			features = append(features, f)
		}
	}
	addTerrainPads(dst, pads)
	addTerrainFeatures(dst, features)
	for _, ia := range sub.Interactables {
		if xf != nil {
			ia.Center = xf.ToWorld(ia.Center)
		}
		dst.Interactables = append(dst.Interactables, ia)
	}
	for _, sp := range sub.Spawnpoints {
		dst.Spawnpoints = append(dst.Spawnpoints, sp.Placed(xf))
	}
}

// addTerrainPads appends pads to every terrain in dst and re-Prepares the
// affected height fields. Pads on a scene with no terrain are dropped (an
// object may be included in a scene that has no ground).
func addTerrainPads(dst *scene.Scene, pads []scene.TerrainPad) {
	if len(pads) == 0 || len(dst.Terrains) == 0 {
		return
	}
	for i := range dst.Terrains {
		dst.Terrains[i].Pads = append(dst.Terrains[i].Pads, pads...)
		dst.Terrains[i].Invalidate()
	}
}

// addTerrainFeatures appends sculpted peaks/valleys/ridges to every terrain in
// dst and rebuilds the height cache.
func addTerrainFeatures(dst *scene.Scene, features []scene.TerrainFeature) {
	if len(features) == 0 || len(dst.Terrains) == 0 {
		return
	}
	for i := range dst.Terrains {
		dst.Terrains[i].Features = append(dst.Terrains[i].Features, features...)
		dst.Terrains[i].Invalidate()
	}
}

// planePoint returns any point on the plane n·x + D = 0.
func planePoint(n vec.V, d float64) vec.V {
	if math.Abs(n.Y) > 1e-6 {
		return vec.V{Y: -d / n.Y}
	}
	if math.Abs(n.X) > 1e-6 {
		return vec.V{X: -d / n.X}
	}
	return vec.V{Z: -d / n.Z}
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
	if e.Sun != nil {
		glow := e.Sun.Glow
		if glow == 0 {
			glow = 1.0 // a configured body defaults to a normal halo
		}
		env.Sun = scene.CelestialBody{
			Color: e.Sun.Color.toV(),
			Size:  e.Sun.Size,
			Glow:  glow,
		}
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
	return ter, nil
}
