package webgpu

import (
	"math"

	"raytracer/internal/gpuscene"
	"raytracer/internal/vec"
)

// campfireBase and campfireTint mirror scene/campfire.go and trace.wgsl. Keep
// in sync when either side changes.
var (
	campfireBase = [3]vec.V{
		{X: 0.22, Y: 0.06, Z: 0.14},
		{X: -0.24, Y: 0.26, Z: -0.12},
		{X: 0.03, Y: 0.52, Z: 0.16},
	}
	campfireTint = [3]vec.V{
		{X: 1.00, Y: 0.60, Z: 0.28},
		{X: 1.00, Y: 0.80, Z: 0.46},
		{X: 1.00, Y: 0.92, Z: 0.66},
	}
)

// campfireCull returns squared cull distance and inverse-square falloff radius
// for a packed campfire. Mirrors campfire_cull() in trace.wgsl and the old
// fireCull helper.
func campfireCull(cf CampfireParams) (cullR2, invR2 float64) {
	rangeVal := float64(cf.Core[3])
	if rangeVal > 0 {
		r2 := rangeVal * rangeVal
		return r2, 1 / r2
	}
	peak := float64(cf.Color[0])
	if float64(cf.Color[1]) > peak {
		peak = float64(cf.Color[1])
	}
	if float64(cf.Color[2]) > peak {
		peak = float64(cf.Color[2])
	}
	peak *= float64(cf.Param[0]) * (1 + float64(cf.Param[2]))
	if peak > gpuscene.LightCullEps*gpuscene.LightAttenBase {
		autoR2 := (peak/gpuscene.LightCullEps - gpuscene.LightAttenBase) / gpuscene.LightAttenQuadratic
		if autoR2 < 0 {
			autoR2 = 0
		}
		return autoR2, 0
	}
	return 0, 0
}

// resolveCampfireSublight evaluates one flickering sub-light from packed GPU
// parameters at animation time t. Mirrors campfire_sublight() in trace.wgsl;
// used for CPU/GPU parity tests.
func resolveCampfireSublight(cf CampfireParams, j int, t float64) (pos, color vec.V) {
	core := vec.V{X: float64(cf.Core[0]), Y: float64(cf.Core[1]), Z: float64(cf.Core[2])}
	baseColor := vec.V{X: float64(cf.Color[0]), Y: float64(cf.Color[1]), Z: float64(cf.Color[2])}
	bright := float64(cf.Param[0])
	jitter := float64(cf.Param[1])
	flicker := float64(cf.Param[2])
	speed := float64(cf.Param[3])
	if speed == 0 {
		speed = 1
	}
	ts := t * speed
	ph := float64(cf.Phase[0]) + float64(j)*1.7

	fl := 0.6*math.Sin(ts*7.0+ph) + 0.3*math.Sin(ts*13.0+ph*2.1) + 0.1*math.Sin(ts*23.0+ph*3.7)
	intensity := bright * (1 + flicker*fl)
	if intensity < 0.15*bright {
		intensity = 0.15 * bright
	}

	jx := jitter * (0.7*math.Sin(ts*9.0+ph*1.3) + 0.3*math.Sin(ts*17.0+ph*2.7))
	jz := jitter * (0.7*math.Sin(ts*11.0+ph*1.9) + 0.3*math.Sin(ts*19.0+ph*0.7))
	jy := jitter * (0.4 + 0.4*math.Sin(ts*15.0+ph))

	b := campfireBase[j]
	tint := campfireTint[j]
	pos = vec.V{
		X: core.X + b.X + jx,
		Y: core.Y + b.Y + jy,
		Z: core.Z + b.Z + jz,
	}
	color = vec.V{
		X: baseColor.X * intensity * tint.X,
		Y: baseColor.Y * intensity * tint.Y,
		Z: baseColor.Z * intensity * tint.Z,
	}
	return pos, color
}
