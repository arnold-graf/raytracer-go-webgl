package shaders

import (
	"fmt"
	"regexp"
	"strings"
)

// Features selects which optional tracer paths a compiled shader keeps. A false
// field means the scene contains none of that feature, so the corresponding
// FEAT_* constant in the WGSL is rewritten to false and the compiler drops the
// guarded code.
//
// This is a performance-only transform: every guarded path is already skipped at
// runtime by a `count == 0` loop bound or an unreachable dispatch arm, so
// removing it cannot change any pixel. What it does change is register
// allocation, which is global to the megakernel — terrain marching in
// particular holds a large live working set that costs occupancy on every ray in
// every scene, including the ones with no terrain at all.
//
// Every flag here was verified frame-for-frame against the unspecialized shader
// across scenes/, by dumping both and comparing:
//
//	go run ./cmd/gpuprof -scene X -dump a.rgba
//	RAYTRACER_NO_SHADER_SPECIALIZE=1 go run ./cmd/gpuprof -scene X -dump b.rgba
//
// That env var is the escape hatch if a specialized build is ever suspect.
// Volumetric flame was dropped from the set despite being equally unreachable;
// see the note in types.wesl.
type Features struct {
	Terrain  bool
	Water    bool
	Campfire bool
	// Prim is indexed by primitive kind (types.wesl PRIM_*): true when the scene
	// packs at least one of that kind, in prims or blockers.
	Prim [PrimKindCount]bool
}

// PrimKindCount is the number of primitive kinds, matching PRIM_SPHERE..PRIM_LENS.
const PrimKindCount = 8

// primFlagNames maps a primitive kind to its shader constant, indexed by kind.
var primFlagNames = [PrimKindCount]string{
	"FEAT_PRIM_SPHERE", "FEAT_PRIM_PLANE", "FEAT_PRIM_BOX", "FEAT_PRIM_CYLINDER",
	"FEAT_PRIM_CONE", "FEAT_PRIM_TORUS", "FEAT_PRIM_RING", "FEAT_PRIM_LENS",
}

// AllFeatures matches the checked-in shader: nothing stripped.
func AllFeatures() Features {
	f := Features{Terrain: true, Water: true, Campfire: true}
	for i := range f.Prim {
		f.Prim[i] = true
	}
	return f
}

// Key is a stable identifier for caching one compiled module per feature set.
func (f Features) Key() string {
	b := func(on bool, c byte) byte {
		if on {
			return c
		}
		return '-'
	}
	key := []byte{b(f.Terrain, 't'), b(f.Water, 'w'), b(f.Campfire, 'c'), '/'}
	for i, on := range f.Prim {
		key = append(key, b(on, "splbcxrn"[i]))
	}
	return string(key)
}

// featConst matches one flag declaration in the linked WGSL. WESL linking keeps
// module-level constant names unmangled, so the linked source still contains the
// literal declarations written in types.wesl.
var featConst = regexp.MustCompile(`(?m)^const (FEAT_[A-Z_]+): bool = (?:true|false);$`)

// Specialize rewrites the FEAT_* declarations in src to match f. Unknown flags
// are left alone so a newly added flag defaults to enabled rather than silently
// switching off.
func Specialize(src string, f Features) string {
	want := map[string]bool{
		"FEAT_TERRAIN":  f.Terrain,
		"FEAT_WATER":    f.Water,
		"FEAT_CAMPFIRE": f.Campfire,
	}
	for kind, name := range primFlagNames {
		want[name] = f.Prim[kind]
	}
	return featConst.ReplaceAllStringFunc(src, func(line string) string {
		name := featConst.FindStringSubmatch(line)[1]
		on, known := want[name]
		if !known {
			return line
		}
		return fmt.Sprintf("const %s: bool = %t;", name, on)
	})
}

// FlagNames lists the FEAT_* declarations present in src, so a test can assert
// the Go-side map has not drifted from the shader.
func FlagNames(src string) []string {
	var out []string
	for _, m := range featConst.FindAllStringSubmatch(src, -1) {
		out = append(out, m[1])
	}
	return out
}

// Describe renders a short human-readable summary for logs and profiling output.
func (f Features) Describe() string {
	var off []string
	if !f.Terrain {
		off = append(off, "terrain")
	}
	if !f.Water {
		off = append(off, "water")
	}
	if !f.Campfire {
		off = append(off, "campfire")
	}
	kinds := 0
	for _, on := range f.Prim {
		if !on {
			kinds++
		}
	}
	if kinds > 0 {
		off = append(off, fmt.Sprintf("%d prim kinds", kinds))
	}
	if len(off) == 0 {
		return "full"
	}
	return "without " + strings.Join(off, "/")
}
