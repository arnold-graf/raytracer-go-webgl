package webgpu

import (
	"math"
	"sort"
	"testing"

	"raytracer/internal/sceneio"
)

func TestWideDiag(t *testing.T) {
	s, _ := sceneio.Load("../../scenes/office-sunset/index.toml")
	lights := PackLights(s)
	g := buildLightGrid(lights)
	var rs []float64
	for _, li := range g.Wide {
		rs = append(rs, math.Sqrt(float64(lights[li].Falloff[0])))
	}
	sort.Float64s(rs)
	t.Logf("wide=%d radii=%.2f", len(rs), rs)
	t.Logf("grid min=%v invCell=%v dim=%v", g.Min, g.InvCell, g.Dim)
}
