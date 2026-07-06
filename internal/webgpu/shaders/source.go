// Package shaders concatenates the trace compute shader from logical WGSL modules.
package shaders

import (
	_ "embed"
	"strings"
)

//go:embed modules/types.wgsl
var modTypes string

//go:embed modules/profile.wgsl
var modProfile string

//go:embed modules/math.wgsl
var modMath string

//go:embed modules/texture.wgsl
var modTexture string

//go:embed modules/sky.wgsl
var modSky string

//go:embed modules/intersect.wgsl
var modIntersect string

//go:embed modules/terrain.wgsl
var modTerrain string

//go:embed modules/instance.wgsl
var modInstance string

//go:embed modules/bvh.wgsl
var modBVH string

//go:embed modules/shade.wgsl
var modShade string

//go:embed modules/trace.wgsl
var modTrace string

// Source returns the full trace compute shader WGSL, assembled in dependency order.
func Source() string {
	return strings.Join([]string{
		modTypes,
		modProfile,
		modMath,
		modTexture,
		modSky,
		modIntersect,
		modTerrain,
		modInstance,
		modBVH,
		modShade,
		modTrace,
	}, "\n")
}
