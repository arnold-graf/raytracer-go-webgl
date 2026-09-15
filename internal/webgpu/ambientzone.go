// ambientzone.go — per-region ambient cubes for the megakernel.
//
// These ride inside idx_tables rather than getting a binding of their own.
// The megakernel already declares 30 storage/uniform buffers and naga spends
// one more on the runtime-array sizes table, which is exactly Metal's limit of
// 31 per stage; a 31st binding fails at pipeline creation. idx_tables is
// already the shared arena for variable-length tables addressed by
// params.table_base, so this is one more region in it. The values are floats
// bitcast into the u32 array.
package webgpu

import (
	"math"

	"raytracer/internal/scene"
)

// ambientZoneWords is the packed size of one zone: min.xyz, max.xyz, then six
// RGB faces.
const ambientZoneWords = 3 + 3 + 6*3

// maxAmbientZones bounds the region so it cannot run past idx_tables.
const maxAmbientZones = 256

// idxTablesAmbientBase is where the ambient-zone region starts. The first word
// is the zone count, so a zero there means "no zones" and the shader takes the
// scene-wide constants exactly as before.
const idxTablesAmbientBase = idxTablesLightGridBase + lightGridBufWords

// packAmbientZones lays out the zone table: one count word followed by
// ambientZoneWords per zone.
func packAmbientZones(zones []scene.AmbientZone) []uint32 {
	if len(zones) > maxAmbientZones {
		zones = zones[:maxAmbientZones]
	}
	out := make([]uint32, 1, 1+len(zones)*ambientZoneWords)
	out[0] = uint32(len(zones))
	for i := range zones {
		z := &zones[i]
		out = append(out,
			f32bits(z.Min.X), f32bits(z.Min.Y), f32bits(z.Min.Z),
			f32bits(z.Max.X), f32bits(z.Max.Y), f32bits(z.Max.Z))
		for f := 0; f < 6; f++ {
			c := z.Faces[f]
			out = append(out, f32bits(c.X), f32bits(c.Y), f32bits(c.Z))
		}
	}
	return out
}

func f32bits(v float64) uint32 { return math.Float32bits(float32(v)) }
