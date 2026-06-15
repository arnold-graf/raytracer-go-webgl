package gpuscene

import (
	"fmt"
	"strings"
	"testing"
)

func TestWGSLConstantsMatchGo(t *testing.T) {
	wgsl := WGSLConstants()
	checks := map[string]string{
		"RAY_EPSILON":           fmt.Sprintf("%.9g", RayEpsilon),
		"LIGHT_CULL_EPS":        fmt.Sprintf("%.9g", LightCullEps),
		"LIGHT_ATTEN_BASE":      fmt.Sprintf("%.9g", LightAttenBase),
		"LIGHT_ATTEN_QUADRATIC": fmt.Sprintf("%.9g", LightAttenQuadratic),
		"AO_MAX_DIST":           fmt.Sprintf("%.9g", AOMaxDist),
		"GAMMA_LUT_SIZE":        fmt.Sprintf("%d", GammaLUTSize),
		"WALLPAPER_TILE_W":      fmt.Sprintf("%.9g", WallpaperTileW),
		"WALLPAPER_TILE_H":      fmt.Sprintf("%.9g", WallpaperTileH),
		"BVH_LEAF_SIZE":         fmt.Sprintf("%d", BVHLeafSize),
		"BVH_SAH_BINS":          fmt.Sprintf("%d", BVHSAHBins),
	}
	for name, value := range checks {
		if !strings.Contains(wgsl, name) {
			t.Fatalf("WGSL constants missing %s", name)
		}
		if !strings.Contains(wgsl, value) {
			t.Fatalf("WGSL constants missing value %s for %s:\n%s", value, name, wgsl)
		}
	}
}
