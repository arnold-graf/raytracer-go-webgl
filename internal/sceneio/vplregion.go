package sceneio

import (
	"path/filepath"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// vplDTO is the `[vpl]` table: one per scene file, declaring that this object's
// own volume deserves its own bounce-light budget.
//
//	[vpl]
//	lights = 24
//	gain = 6
//
// Put on an object file it travels with the object, so two [[include]]s of the
// same villa produce two regions, each with its own budget — which is what you
// want, because each is a separate place a player can stand.
type vplDTO struct {
	// Lights is the region's budget. 0 disables the region.
	Lights int `toml:"lights"`

	// Gain and Reach override the generator's defaults inside the region. Both
	// are absolute, matching RAYTRACER_VPL_GAIN / _REACH rather than scaling
	// them, so a number read off the calibration table means the same thing
	// here as it does on the command line.
	Gain  *float64 `toml:"gain"`
	Reach *float64 `toml:"reach"`

	// ShadowLevels is how many display levels a bounce light here must be worth
	// before it pays for a shadow ray. Raising it is the cheapest way to make a
	// room full of bounce lights affordable; a large value turns their shadows
	// off outright. Omitted inherits the generator default.
	ShadowLevels *float64 `toml:"shadow_levels"`

	// Min and Max scope the region by hand, in the file's own local
	// coordinates. Omitted, the region is the AABB of the file's geometry,
	// which is the ergonomic case: `[vpl] lights = 24` on a building means
	// "this building". Author them to aim tighter — a hall rather than a whole
	// villa — so exterior bounces stop competing for interior slots.
	Min *vec3 `toml:"min"`
	Max *vec3 `toml:"max"`

	// Expand grows the box by this many metres on every side. Bounce that
	// belongs to a room routinely lands just outside its shell — on a porch, a
	// step, or the far side of a doorway — and a box drawn exactly on the
	// geometry drops it into the global pool.
	Expand float64 `toml:"expand"`
}

// buildVPLRegion turns a [vpl] table into a region in the file's local space.
// Bounds default to the AABB of everything the file built, which is only
// meaningful once its own [[include]]s have been merged — so this is called at
// the end of the file's load, not during decode.
func buildVPLRegion(d *vplDTO, s *scene.Scene, path string) (scene.VPLRegion, bool) {
	if d == nil || s == nil || d.Lights <= 0 {
		return scene.VPLRegion{}, false
	}
	var mn, mx vec.V
	switch {
	case d.Min != nil && d.Max != nil:
		mn, mx = d.Min.toV(), d.Max.toV()
	default:
		lo, hi, ok := scene.TemplateWorldBounds(s, nil)
		if !ok {
			// A file with no finite geometry has no volume to scope, and a
			// region with no volume would silently capture nothing.
			return scene.VPLRegion{}, false
		}
		mn, mx = lo, hi
	}
	if e := d.Expand; e != 0 {
		g := vec.New(e, e, e)
		mn, mx = mn.Sub(g), mx.Add(g)
	}
	r := scene.VPLRegion{
		Min:    mn,
		Max:    mx,
		Lights: d.Lights,
		Label:  filepath.Base(path),
	}
	if d.Gain != nil {
		r.Gain = *d.Gain
	}
	if d.Reach != nil {
		r.Reach = *d.Reach
	}
	if d.ShadowLevels != nil {
		r.ShadowLevels = *d.ShadowLevels
	}
	if !r.Valid() {
		return scene.VPLRegion{}, false
	}
	return r, true
}

// appendVPLRegion records the file's [vpl] table on the scene it just built.
func appendVPLRegion(s *scene.Scene, d *vplDTO, path string) {
	if r, ok := buildVPLRegion(d, s, path); ok {
		s.VPLRegions = append(s.VPLRegions, r)
	}
}
