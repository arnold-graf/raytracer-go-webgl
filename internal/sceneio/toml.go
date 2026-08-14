// Package sceneio loads a Scene from a human-editable TOML description. The
// format mirrors the primitives in package scene: each primitive kind is an
// array of tables (e.g. [[box]]), and materials are referenced by name.
//
// The TOML format is documented by JSON Schema in schemas/scene.schema.json
// (and schemas/player.schema.json for movement config). Update those schemas
// when adding or changing tables or fields.
//
// Numbers must be written as floats (use 0.0, not 0) because vectors decode
// into fixed [3]float64 arrays.
package sceneio

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"raytracer/internal/scene"
	"raytracer/internal/sceneparam"
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

// AtVec returns the include placement vector.
func (inc includeDTO) AtVec() vec.V { return inc.At.toV() }

// surfaceDTO holds the shading fields shared by every primitive table.
type surfaceDTO struct {
	Material  string   `toml:"material"`
	Albedo    vec3     `toml:"albedo"`
	Albedo2   vec3     `toml:"albedo2"`
	Rough     float64  `toml:"rough"`
	IOR       *float64 `toml:"ior"`
	Texture   string   `toml:"texture"`
	Reflect   float64  `toml:"reflect"`
	Transmit  float64  `toml:"transmit"`
	Thin      *bool    `toml:"thin"`
	Collision *bool    `toml:"collision"`
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
	noCollision := false
	if s.Collision != nil && !*s.Collision {
		noCollision = true
	}
	thin := false
	if s.Thin != nil {
		thin = *s.Thin
	}
	return scene.Surface{
		Mat: mat, Albedo: s.Albedo.toV(), Albedo2: s.Albedo2.toV(), Rough: s.Rough, IOR: ior, Tex: tex,
		Reflect: s.Reflect, Transmit: s.Transmit, Thin: thin, NoCollision: noCollision,
	}, nil
}

// transformDTO is defined in transform_origin.go.

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
	CutOff float64 `toml:"cut_off"`
	transformDTO
	surfaceDTO
}

type planeDTO struct {
	Normal vec3    `toml:"normal"`
	D      float64 `toml:"d"`
	transformDTO
	surfaceDTO
}

// holeDTO is a rectangular opening subtracted from a box (see scene.AABB). It
// is authored as a [[box.hole]] sub-table and should pierce fully through the
// faces it cuts. Define with pos_x/pos_y/pos_z and width/height/depth.
type holeDTO struct {
	boxExtentDTO
}

type boxDTO struct {
	boxExtentDTO
	Hole []holeDTO `toml:"hole"`
	transformDTO
	surfaceDTO
	faceTextureDTO
	interactPropsDTO
}

// boxExtentDTO defines a box by minimum corner (pos_x, pos_y, pos_z) and
// positive extents (width, height, depth) along +X, +Y, +Z.
type boxExtentDTO struct {
	PosX   float64 `toml:"pos_x"`
	PosY   float64 `toml:"pos_y"`
	PosZ   float64 `toml:"pos_z"`
	Width  float64 `toml:"width"`
	Height float64 `toml:"height"`
	Depth  float64 `toml:"depth"`
}

func (d boxExtentDTO) bounds() (min, max vec.V, err error) {
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
	PosX        float64 `toml:"pos_x"`
	PosY        float64 `toml:"pos_y"`
	PosZ        float64 `toml:"pos_z"`
	Width       float64 `toml:"width"`        // uniform diameter when not tapered
	Height      float64 `toml:"height"`
	WidthBottom float64 `toml:"width_bottom"` // bottom diameter (defaults to width)
	WidthTop    float64 `toml:"width_top"`    // top diameter (defaults to width_bottom)
	OpenMin     bool    `toml:"open_min"`     // omit bottom end cap (hollow tube)
	OpenMax     bool    `toml:"open_max"`     // omit top end cap
	transformDTO
	surfaceDTO
}

// specs resolves the engine cylinder from box-style placement fields. pos_* is
// the minimum corner of the footprint square (side = max(bottom, top) diameter);
// the cylinder axis runs through the center of that square.
func (d cylinderDTO) specs() (cx, cz, ymin, ymax, radius, radiusTop float64, err error) {
	bottomD := d.WidthBottom
	if bottomD == 0 {
		bottomD = d.Width
	}
	if bottomD <= 0 {
		return 0, 0, 0, 0, 0, 0, fmt.Errorf("width or width_bottom must be positive")
	}
	if d.Height <= 0 {
		return 0, 0, 0, 0, 0, 0, fmt.Errorf("height must be positive")
	}
	topD := d.WidthTop
	if topD == 0 {
		topD = bottomD
	}
	foot := bottomD
	if topD > foot {
		foot = topD
	}
	cx = d.PosX + foot/2
	cz = d.PosZ + foot/2
	ymin = d.PosY
	ymax = d.PosY + d.Height
	radius = bottomD / 2
	radiusTop = topD / 2
	if math.Abs(topD-bottomD) < 1e-12 {
		radiusTop = 0 // uniform: engine treats 0 as same as Radius
	}
	return cx, cz, ymin, ymax, radius, radiusTop, nil
}

type coneDTO struct {
	PosX   float64 `toml:"pos_x"`
	PosY   float64 `toml:"pos_y"`
	PosZ   float64 `toml:"pos_z"`
	Width  float64 `toml:"width"`
	Height float64 `toml:"height"`
	Capped *bool   `toml:"capped"`
	transformDTO
	surfaceDTO
}

// specs resolves the engine cone from box-style placement fields. pos_* is the
// minimum corner of the base footprint square (side = width); the cone axis runs
// through the center of that square. width is the base diameter; height spans
// base to tip.
func (d coneDTO) specs() (cx, cz, ybase, ytip, rbase float64, err error) {
	if d.Width <= 0 {
		return 0, 0, 0, 0, 0, fmt.Errorf("width must be positive")
	}
	if d.Height <= 0 {
		return 0, 0, 0, 0, 0, fmt.Errorf("height must be positive")
	}
	cx = d.PosX + d.Width/2
	cz = d.PosZ + d.Width/2
	ybase = d.PosY
	ytip = d.PosY + d.Height
	rbase = d.Width / 2
	return cx, cz, ybase, ytip, rbase, nil
}

func coneCappedFromDTO(d *bool) bool {
	if d == nil {
		return true
	}
	return *d
}

type torusDTO struct {
	Center vec3    `toml:"center"`
	Major  float64 `toml:"major"`
	Minor  float64 `toml:"minor"`
	transformDTO
	surfaceDTO
}

type ringDTO struct {
	CX     float64 `toml:"cx"`
	CZ     float64 `toml:"cz"`
	CY     float64 `toml:"cy"`
	Radius float64 `toml:"radius"`
	Height float64 `toml:"height"`
	transformDTO
	surfaceDTO
}

type lensDTO struct {
	CX        float64 `toml:"cx"`
	CY        float64 `toml:"cy"`
	CZ        float64 `toml:"cz"`
	Aperture  float64 `toml:"aperture"`
	RFront    float64 `toml:"r_front"`
	RBack     float64 `toml:"r_back"`
	Thickness float64 `toml:"thickness"`
	transformDTO
	surfaceDTO
}

type lightDTO struct {
	Pos    vec3    `toml:"pos"`
	Color  vec3    `toml:"color"`
	Radius float64 `toml:"radius"`
	Range  float64 `toml:"range"`
	Dir    vec3    `toml:"dir"`
	// ConeAngle is the full outer cone angle in degrees for spot lights.
	ConeAngle float64 `toml:"cone_angle"`
	// Brightness scales the light's intensity independently of its color/range
	// (1 = as authored), mirroring the campfire's brightness knob. It is folded
	// into the color at load time, so culling and shading honor it for free.
	Brightness  float64 `toml:"brightness"`
	Interactive bool    `toml:"interactive"`
	Hint        string  `toml:"hint"`
}

// build resolves a light, applying the brightness multiplier (default 1) to the
// color so the rest of the engine only sees a single effective intensity.
func (d lightDTO) build() scene.Light {
	b := d.Brightness
	if b == 0 {
		b = 1
	}
	dir := d.Dir.toV()
	if dir.LenSq() > 0 {
		dir = dir.Normalize()
	}
	return scene.Light{
		Pos: d.Pos.toV(), Color: d.Color.toV().Scale(b), Radius: d.Radius, Range: d.Range,
		Dir: dir, ConeDeg: d.ConeAngle,
		Interactive: d.Interactive,
		Hint:        lightHint(d.Interactive, d.Hint),
	}
}

func lightHint(interactive bool, hint string) string {
	if hint != "" {
		return hint
	}
	if interactive {
		return "lamp"
	}
	return ""
}

type lightFlickeringDTO struct {
	Center     vec3    `toml:"center"`
	Color      vec3    `toml:"color"`
	Brightness float64 `toml:"brightness"`
	Range      float64 `toml:"range"`
	Jitter     float64 `toml:"jitter"`
	Flicker    float64 `toml:"flicker"`
	Speed      float64 `toml:"speed"`
	Seed       float64 `toml:"seed"`
	Lights     int     `toml:"lights"`
	Flame      *bool   `toml:"flame"`
	FlameEmber *vec3   `toml:"flame_ember"`
	FlameMid   *vec3   `toml:"flame_mid"`
	FlameTip   *vec3   `toml:"flame_tip"`
	FlameAsh   *vec3   `toml:"flame_ash"`
	FlameScale float64 `toml:"flame_scale"`
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

// build resolves a light_flickering source, filling in sensible defaults for
// any omitted flicker parameters so a bare [[light_flickering]] with just a
// center already looks like a fire.
func (d lightFlickeringDTO) build() scene.Campfire {
	c := scene.Campfire{
		Center:     d.Center.toV(),
		Color:      d.Color.toV(),
		Brightness: d.Brightness,
		Range:      d.Range,
		Jitter:     d.Jitter,
		Flicker:    d.Flicker,
		Speed:      d.Speed,
		Seed:       d.Seed,
		Lights:     d.Lights,
	}
	if d.Flame != nil {
		c.Flame = *d.Flame
	}
	if d.FlameEmber != nil {
		c.FlameEmber = d.FlameEmber.toV()
	}
	if d.FlameMid != nil {
		c.FlameMid = d.FlameMid.toV()
	}
	if d.FlameTip != nil {
		c.FlameTip = d.FlameTip.toV()
	}
	if d.FlameAsh != nil {
		c.FlameAsh = d.FlameAsh.toV()
	}
	c.FlameScale = d.FlameScale
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
	if c.Lights == 0 {
		c.Lights = scene.CampfireLights
	}
	return c
}

type waterDTO struct {
	Pos         [2]float64 `toml:"pos"`
	Radius      float64    `toml:"radius"`
	Level       float64    `toml:"level"`
	Mask        *bool      `toml:"mask"`
	Ripple      float64    `toml:"ripple"`
	RippleSpeed float64    `toml:"ripple_animation_speed"`
	RippleDir   [2]float64 `toml:"ripple_direction"`
	surfaceDTO
}

type terrainIslandDTO struct {
	Center [2]float64 `toml:"center"`
	Radius float64    `toml:"radius"`
	Margin float64    `toml:"margin"`
	Floor  float64    `toml:"floor"`
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
	CoarseCell  float64    `toml:"coarse_cell"`
	HybridNear  [2]float64 `toml:"hybrid_near"` // [start, end] camera distance band (m)

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

	Island  *terrainIslandDTO   `toml:"island"`
	Feature []terrainFeatureDTO `toml:"feature"`
	Pad     []terrainPadDTO     `toml:"pad"`
}

// terrainPadDTO flattens a building site into the terrain. center/half are the
// inner flat rectangle (X/Z); level is the flattened height; margin is the
// width of the smooth blend ring around it.
type terrainPadDTO struct {
	Center   [2]float64 `toml:"center"`
	Half     [2]float64 `toml:"half"`
	Level    float64    `toml:"level"`
	Margin   float64    `toml:"margin"`
	Absolute bool       `toml:"absolute"`
}

func (p terrainPadDTO) buildPad() scene.TerrainPad {
	return scene.TerrainPad{
		CenterX: p.Center[0], CenterZ: p.Center[1],
		HalfX: p.Half[0], HalfZ: p.Half[1],
		Level: p.Level, Margin: p.Margin, Absolute: p.Absolute,
	}
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
// transform (rotate about transform_origin, then translate so origin lands at at).
//
// When the parent scene has a terrain height field, at.y is an offset above the
// ground at (at.x, at.z) — 0 places the object's origin on the ground. If the
// included object declares a [[terrain.pad]] covering its origin, the pad's
// grade is used instead of the wild terrain height (relative pads add their
// level offset to natural terrain at the anchor). The pad is merged after
// placement and defines the object's grade. Object files that only carry pad
// stubs without a footprint do not count as height fields for nested includes.
//
// follow_terrain defers Y placement to a post-pass after all terrain (including
// features from other includes) is merged and baked. Only the [[include]] line
// that sets follow_terrain = true is snapped; nested includes without the flag
// move rigidly with the parent assembly. For scattered props (e.g. a tree row),
// set follow_terrain on each child include, not on the layout file.
//
// Props listed on the [[include]] are merged into the child's [props] table
// (see internal/sceneparam). Parent [props] are not inherited: only keys in the
// include's props table are passed. Expressions in those props (e.g.
// width = 'width') are evaluated in the parent env during expansion, so values
// can still be derived from parent props without cascading the whole map.
// Door panel_file loads still receive the parent's resolved [props] (there is
// no per-panel props table). Object files use valid TOML with [props], [const],
// single-quoted expressions, and comment directives (# for, # if, # let).
// Files without [props]/[const] are passed through verbatim.
type includeDTO struct {
	File            string              `toml:"file"`
	At              vec3                `toml:"at"`
	RotateX         float64             `toml:"rotate_x"`
	RotateY         float64             `toml:"rotate_y"`
	RotateZ         float64             `toml:"rotate_z"`
	TransformOrigin *transformOriginDTO `toml:"transform_origin"`
	FollowTerrain   bool                `toml:"follow_terrain"`
	Instance        bool                `toml:"instance"`
	Props           map[string]any      `toml:"props"`
}


type pointDTO struct {
	ID      string   `toml:"id"`
	Pos     vec3     `toml:"pos"`
	FloorY  *float64 `toml:"floor_y"`
	Yaw     float64  `toml:"yaw"`
	Pitch   float64  `toml:"pitch"`
}

func (d pointDTO) build() (scene.Point, error) {
	if d.ID == "" {
		return scene.Point{}, fmt.Errorf("missing id")
	}
	p := scene.Point{
		ID:    d.ID,
		Pos:   d.Pos.toV(),
		Yaw:   d.Yaw,
		Pitch: d.Pitch,
	}
	if d.FloorY != nil {
		p.FloorY = *d.FloorY
		p.UseFloor = true
	}
	return p, nil
}

type sceneDTO struct {
	Extends     string          `toml:"extends"`
	Camera      *cameraDTO      `toml:"camera"`
	Player      *playerSceneDTO `toml:"player"`
	Environment *environmentDTO `toml:"environment"`
	Include     []includeDTO    `toml:"include"`
	Sphere      []sphereDTO     `toml:"sphere"`
	Plane       []planeDTO      `toml:"plane"`
	Box         []boxDTO        `toml:"box"`
	Cylinder    []cylinderDTO   `toml:"cylinder"`
	Cone        []coneDTO       `toml:"cone"`
	Torus       []torusDTO      `toml:"torus"`
	Ring        []ringDTO       `toml:"ring"`
	Lens        []lensDTO       `toml:"lens"`
	Terrain     []terrainDTO    `toml:"terrain"`
	Water       []waterDTO      `toml:"water"`
	Light       []lightDTO      `toml:"light"`
	LightFlickering []lightFlickeringDTO `toml:"light_flickering"`
	Sound       []soundDTO      `toml:"sound"`
	Point            []pointDTO            `toml:"point"`
	NPC              []npcDTO              `toml:"npc"`
	Door             []doorDTO             `toml:"door"`
	Document         []documentDTO         `toml:"document"`
	Screen           []screenDTO           `toml:"screen"`
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
	s, err := load(path, nil, map[string]bool{}, &deps, &followPlacements)
	if err != nil {
		return nil, deps, err
	}
	if err := finalizeDocuments(s); err != nil {
		return nil, deps, err
	}
	s.PrepareTerrains()
	s.ApplyTerrainFollow(followPlacements)
	s.ApplyInstanceTerrainFollow()
	s.FinalizeInstancing()
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

// load reads, expands parameterized objects when needed, and decodes the scene
// at path. props from a parent [[include]] are merged into the object's [props] table.
// data supplied by a parent [[include]] (nil for the top-level file and for
// "extends" bases, which take no parameters).
func load(path string, params map[string]any, seen map[string]bool, deps *[]string, followPlacements *[]scene.TerrainFollowPlacement) (*scene.Scene, error) {
	recordDep(deps, path)
	abs, err := filepath.Abs(path)
	if err == nil {
		if seen[abs] {
			return nil, fmt.Errorf("scene extends cycle at %q", path)
		}
		seen[abs] = true
		defer delete(seen, abs)
	}

	dto, resolved, err := decodeSceneFile(path, params)
	if err != nil {
		return nil, err
	}
	if dto.Extends != "" {
		basePath := dto.Extends
		if !filepath.IsAbs(basePath) {
			basePath = filepath.Join(filepath.Dir(path), basePath)
		}
		base, err := load(basePath, nil, seen, deps, nil)
		if err != nil {
			return nil, err
		}
		if err := dto.applyOverrides(base); err != nil {
			return nil, fmt.Errorf("apply scene overrides %q: %w", path, err)
		}
		var extendPlacements []scene.TerrainFollowPlacement
		for i, inc := range dto.Include {
			if err := mergeInclude(base, inc, filepath.Dir(path), i, seen, deps, &extendPlacements); err != nil {
				return nil, err
			}
		}
		if err := resolveDoors(base, dto.Door, filepath.Dir(path), params, seen, deps); err != nil {
			return nil, err
		}
		base.PrepareTerrains()
		base.ApplyTerrainFollow(extendPlacements)
		base.ApplyInstanceTerrainFollow()
		base.FinalizeInstancing()
		return base, nil
	}
	return dto.buildWithIncludes(path, resolved, seen, deps, followPlacements)
}

// decodeSceneFile reads the file at path, expands parameterized object syntax
// when present, and decodes the resulting TOML into a sceneDTO. resolved holds
// merged [props] values (used for door panel loads; [[include]] children only
// receive their own explicit props).
func decodeSceneFile(path string, params map[string]any) (sceneDTO, map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return sceneDTO{}, nil, fmt.Errorf("read scene %q: %w", path, err)
	}
	rendered, resolved, err := sceneparam.ExpandWithResolved(path, raw, params)
	if err != nil {
		return sceneDTO{}, nil, err
	}
	var dto sceneDTO
	if _, err := toml.Decode(string(rendered), &dto); err != nil {
		return sceneDTO{}, nil, fmt.Errorf("load scene %q: %w", path, err)
	}
	return dto, resolved, nil
}

// Decode decodes a TOML scene from an in-memory byte slice (used for the
// embedded default scene).
func Decode(data []byte) (*scene.Scene, error) {
	var dto sceneDTO
	if err := toml.Unmarshal(data, &dto); err != nil {
		return nil, fmt.Errorf("decode scene: %w", err)
	}
	s, err := dto.build()
	if err != nil {
		return nil, err
	}
	if err := resolveDocuments(s, dto.Document, "."); err != nil {
		return nil, err
	}
	if err := resolveScreens(s, dto.Screen, "."); err != nil {
		return nil, err
	}
	if err := finalizeDocuments(s); err != nil {
		return nil, err
	}
	return s, nil
}

func (dto sceneDTO) applyOverrides(s *scene.Scene) error {
	if dto.Camera != nil {
		s.Start = scene.CameraStart{Set: true, Pos: dto.Camera.Pos.toV(), Yaw: dto.Camera.Yaw, Pitch: dto.Camera.Pitch}
	}
	if dto.Player != nil {
		s.PlayerMovement = dto.Player.Movement.toScene()
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
	if dto.LightFlickering != nil {
		s.Campfires = s.Campfires[:0]
		for _, d := range dto.LightFlickering {
			s.Campfires = append(s.Campfires, d.build())
		}
	}
	// Extends children may add [[terrain.pad]] tables (with a stub [[terrain]]
	// header so TOML decode succeeds). Merge pads into the base heightfield.
	var pads []scene.TerrainPad
	for _, td := range dto.Terrain {
		for _, p := range td.Pad {
			pads = append(pads, p.buildPad())
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
		surf.Xform = d.transformDTO.buildPlacement(center)
		s.Spheres = append(s.Spheres, scene.Sphere{Center: center, Radius: d.Radius, CutOff: d.CutOff, Surface: surf})
	}
	for i, d := range dto.Plane {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("plane[%d]: %w", i, err)
		}
		surf.Xform = d.transformDTO.buildPlacement(vec.V{})
		s.Planes = append(s.Planes, scene.Plane{N: d.Normal.toV(), D: d.D, Surface: surf})
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
		surf.Xform = d.transformDTO.buildPlacement(boxCenter(min, max))
		faceTex, err := d.faceTextureDTO.resolve()
		if err != nil {
			return nil, fmt.Errorf("box[%d]: %w", i, err)
		}
		var holes []scene.AABB
		for j, h := range d.Hole {
			hmin, hmax, err := h.bounds()
			if err != nil {
				return nil, fmt.Errorf("box[%d].hole[%d]: %w", i, j, err)
			}
			holes = append(holes, scene.AABB{Min: hmin, Max: hmax})
		}
		s.Boxes = append(s.Boxes, scene.Box{Min: min, Max: max, Holes: holes, Surface: surf, FaceTex: faceTex})
		if d.OnUse != "" {
			iaIdx := s.RegisterInteractable(d.interactPropsDTO.build())
			s.SetBoxInteract(len(s.Boxes)-1, iaIdx)
		}
	}
	for i, d := range dto.Cylinder {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("cylinder[%d]: %w", i, err)
		}
		cx, cz, ymin, ymax, radius, radiusTop, err := d.specs()
		if err != nil {
			return nil, fmt.Errorf("cylinder[%d]: %w", i, err)
		}
		center := vec.New(cx, (ymin+ymax)/2, cz)
		surf.Xform = d.transformDTO.buildPlacement(center)
		s.Cylinders = append(s.Cylinders, scene.Cylinder{
			CX: cx, CZ: cz, Radius: radius, RadiusTop: radiusTop,
			YMin: ymin, YMax: ymax, OpenMin: d.OpenMin, OpenMax: d.OpenMax,
			Surface: surf,
		})
	}
	for i, d := range dto.Cone {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("cone[%d]: %w", i, err)
		}
		cx, cz, ybase, ytip, rbase, err := d.specs()
		if err != nil {
			return nil, fmt.Errorf("cone[%d]: %w", i, err)
		}
		center := vec.New(cx, (ybase+ytip)/2, cz)
		surf.Xform = d.transformDTO.buildPlacement(center)
		s.Cones = append(s.Cones, scene.Cone{
			CX: cx, CZ: cz, YBase: ybase, YTip: ytip, RBase: rbase,
			Capped: coneCappedFromDTO(d.Capped), Surface: surf,
		})
	}
	for i, d := range dto.Torus {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("torus[%d]: %w", i, err)
		}
		center := d.Center.toV()
		surf.Xform = d.transformDTO.buildPlacement(center)
		s.Tori = append(s.Tori, scene.Torus{Center: center, R: d.Major, Rm: d.Minor, Surface: surf})
	}
	for i, d := range dto.Ring {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("ring[%d]: %w", i, err)
		}
		if d.Radius <= 0 {
			return nil, fmt.Errorf("ring[%d]: radius must be > 0", i)
		}
		center := vec.New(d.CX, d.CY, d.CZ)
		surf.Xform = d.transformDTO.buildPlacement(center)
		s.Rings = append(s.Rings, scene.Ring{CX: d.CX, CZ: d.CZ, CY: d.CY, Radius: d.Radius, Height: d.Height, Surface: surf})
	}
	for i, d := range dto.Lens {
		surf, err := d.toSurface()
		if err != nil {
			return nil, fmt.Errorf("lens[%d]: %w", i, err)
		}
		if d.Aperture <= 0 || d.RFront <= 0 || d.RBack <= 0 {
			return nil, fmt.Errorf("lens[%d]: aperture, r_front, and r_back must be > 0", i)
		}
		center := vec.New(d.CX, d.CY, d.CZ)
		surf.Xform = d.transformDTO.buildPlacement(center)
		s.Lenses = append(s.Lenses, scene.Lens{
			CX: d.CX, CY: d.CY, CZ: d.CZ,
			Aperture: d.Aperture, RFront: d.RFront, RBack: d.RBack, Thickness: d.Thickness,
			Surface: surf,
		})
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
		mask := d.Radius <= 0
		if d.Mask != nil {
			mask = *d.Mask
		}
		s.Waters = append(s.Waters, scene.WaterPool{
			CX: d.Pos[0], CZ: d.Pos[1], Radius: d.Radius, Level: d.Level, MaskShoreline: mask,
			Ripple: d.Ripple, RippleSpeed: d.RippleSpeed, RippleDirX: dirX, RippleDirZ: dirZ, Surface: surf,
		})
	}
	for _, d := range dto.Light {
		s.Lights = append(s.Lights, d.build())
	}
	for _, d := range dto.LightFlickering {
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
	if dto.Player != nil {
		s.PlayerMovement = dto.Player.Movement.toScene()
	}
	if e := dto.Environment; e != nil {
		env, err := e.build()
		if err != nil {
			return nil, err
		}
		s.Env = env
	}
	seenPoint := map[string]bool{}
	for i, d := range dto.Point {
		p, err := d.build()
		if err != nil {
			return nil, fmt.Errorf("point[%d]: %w", i, err)
		}
		if seenPoint[p.ID] {
			return nil, fmt.Errorf("point[%d]: duplicate id %q", i, p.ID)
		}
		seenPoint[p.ID] = true
		s.Points = append(s.Points, p)
	}
	for i, d := range dto.NPC {
		sp, err := d.build()
		if err != nil {
			return nil, fmt.Errorf("npc[%d]: %w", i, err)
		}
		s.NPCSpawns = append(s.NPCSpawns, sp)
		if s.NPCSpawns[len(s.NPCSpawns)-1].Rig == "" {
			return nil, fmt.Errorf("npc[%d]: missing rig", i)
		}
	}

	return s, nil
}

func (dto sceneDTO) buildWithIncludes(path string, parentResolved map[string]any, seen map[string]bool, deps *[]string, followPlacements *[]scene.TerrainFollowPlacement) (*scene.Scene, error) {
	s, err := dto.build()
	if err != nil {
		return nil, err
	}
	if err := resolveDoors(s, dto.Door, filepath.Dir(path), parentResolved, seen, deps); err != nil {
		return nil, err
	}
	if err := resolveDocuments(s, dto.Document, filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := resolveScreens(s, dto.Screen, filepath.Dir(path)); err != nil {
		return nil, err
	}
	for i, inc := range dto.Include {
		if err := mergeInclude(s, inc, filepath.Dir(path), i, seen, deps, followPlacements); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func mergeInclude(dst *scene.Scene, inc includeDTO, parentDir string, index int, seen map[string]bool, deps *[]string, followPlacements *[]scene.TerrainFollowPlacement) error {
	incPath := inc.File
	if !filepath.IsAbs(incPath) {
		incPath = filepath.Join(parentDir, incPath)
	}
	follow := inc.FollowTerrain

	if inc.Instance {
		if err := registerLeafInstance(dst, inc, parentDir, seen, deps); err != nil {
			return fmt.Errorf("include[%d] %q: %w", index, inc.File, err)
		}
		return nil
	}

	var subFollow []scene.TerrainFollowPlacement
	fp := &subFollow
	if follow {
		fp = nil
	}
	sub, err := load(incPath, inc.Props, seen, deps, fp)
	if err != nil {
		return fmt.Errorf("include[%d] %q: %w", index, inc.File, err)
	}
	xf, err := instanceTransform(dst, sub, inc, follow)
	if err != nil {
		return fmt.Errorf("include[%d] %q: %w", index, inc.File, err)
	}
	before := scene.CountPrimitives(dst)
	mergeScene(dst, sub, xf)
	mergeInstancingCatalog(dst, sub, xf)
	if followPlacements == nil {
		return nil
	}
	if follow {
		after := scene.CountPrimitives(dst)
		p := scene.PlacementFromRange(before, after, inc.At.toV().Y)
		p.Anchor = xf.PlacementAnchor()
		*followPlacements = append(*followPlacements, p)
		return nil
	}
	if len(subFollow) > 0 {
		for i := range subFollow {
			subFollow[i].Anchor = xf.ToWorld(subFollow[i].Anchor)
		}
		scene.OffsetPlacements(subFollow, before)
		*followPlacements = append(*followPlacements, subFollow...)
	}
	return nil
}

// includePlacementAt resolves the world-space include anchor (at). at.x/at.z place
// transform_origin; at.y is adjusted so local (0,0,0) — the object's grade —
// sits on a pad or terrain. Sampling uses the grade footprint in XZ, not the
// pivot anchor, so center-pivot objects still flatten under the building.
func includePlacementAt(dst, sub *scene.Scene, inc includeDTO, follow bool) (vec.V, error) {
	at := inc.At.toV()
	if sub == nil {
		if !follow {
			if h, ok := dst.TerrainHeightAt(at.X, at.Z); ok {
				at.Y = h + at.Y
			}
		}
		return at, nil
	}
	origin, err := inc.resolvedOrigin(sub)
	if err != nil {
		return vec.V{}, err
	}
	probe := scene.PlacementTransform(inc.RotateX, inc.RotateY, inc.RotateZ, at, origin)
	grade := probe.ToWorld(vec.V{})
	if g, ok := sub.PadGradeAt(0, 0, dst, grade.X, grade.Z); ok {
		at.Y = g + at.Y
	} else if g, ok := dst.TerrainPadGradeAt(grade.X, grade.Z); ok {
		at.Y = g + at.Y
	} else if !follow {
		if h, ok := dst.TerrainHeightAt(grade.X, grade.Z); ok {
			at.Y = h + at.Y
		}
	}
	return at, nil
}

func instanceTransformForInclude(dst *scene.Scene, inc includeDTO, follow bool, sub *scene.Scene) (*scene.Transform, error) {
	at, err := includePlacementAt(dst, sub, inc, follow)
	if err != nil {
		return nil, err
	}
	return buildIncludeTransform(inc, at, sub)
}

// instanceTransform builds the world placement for an include. When follow is
// false, at.y is raised by the pad or terrain height at (at.x, at.z) when
// available. When follow is true, Y is deferred to ApplyTerrainFollow and at.y
// is kept as an offset above the sampled ground.
func instanceTransform(dst *scene.Scene, sub *scene.Scene, inc includeDTO, follow bool) (*scene.Transform, error) {
	at, err := includePlacementAt(dst, sub, inc, follow)
	if err != nil {
		return nil, err
	}
	return buildIncludeTransform(inc, at, sub)
}

// mergeScene appends every primitive from sub into dst, composing each
// primitive's local transform with the instance transform xf.
func mergeScene(dst, sub *scene.Scene, xf *scene.Transform) {
	boxOffset := len(dst.Boxes)
	sphereOffset := len(dst.Spheres)
	cylinderOffset := len(dst.Cylinders)
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
	for i := range sub.Rings {
		o := sub.Rings[i]
		o.Xform = xf.Compose(o.Xform)
		dst.Rings = append(dst.Rings, o)
	}
	for i := range sub.Lenses {
		o := sub.Lenses[i]
		o.Xform = xf.Compose(o.Xform)
		dst.Lenses = append(dst.Lenses, o)
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
	dst.MergeInteractables(sub, boxOffset)
	for _, p := range sub.Points {
		dst.Points = append(dst.Points, p.Placed(xf))
	}
	for _, sp := range sub.NPCSpawns {
		dst.NPCSpawns = append(dst.NPCSpawns, sp.Placed(xf))
	}
	mergeDoorSpecs(dst, sub, xf, boxOffset, sphereOffset, cylinderOffset)
	mergeDocumentSpecs(dst, sub, xf)
	mergeScreenSpecs(dst, sub, xf)
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
		Base: d.Base, Detail: d.Detail, DetailScale: d.DetailScale, Step: d.Step,
		GridCell: d.GridCell, CoarseCell: d.CoarseCell,
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
		ter.Pads = append(ter.Pads, p.buildPad())
	}
	if d.Island != nil {
		ter.Island = scene.TerrainIsland{
			CenterX: d.Island.Center[0], CenterZ: d.Island.Center[1],
			Radius: d.Island.Radius, Margin: d.Island.Margin, Floor: d.Island.Floor,
		}
	}
	if len(d.HybridNear) > 0 {
		ter.HybridNearStart = d.HybridNear[0]
	}
	if len(d.HybridNear) > 1 {
		ter.HybridNearEnd = d.HybridNear[1]
	}
	return ter, nil
}
