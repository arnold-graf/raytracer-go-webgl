// pathtrace_emitters.go — emissive geometry as a sampleable light population.
//
// trace.wesl treats an emissive surface as "add albedo, stop": it is a bright
// pixel that illuminates nothing. The path tracer already improves on that,
// since a BSDF ray landing on one picks up its radiance — but finding a small
// emitter by chance is the noisiest way to light a room, and office-sunset has
// 268 of them. This builds the tables that let next-event estimation aim at
// them directly.
package webgpu

import (
	"math"

	"raytracer/internal/scene"
)

// ptEmitterStride is one vec4<u32> in pt.wesl: prim index, kind, two spare.
const ptEmitterStride = 16

// ptEmitterTables is the CPU-side emitter population for one packed scene.
type ptEmitterTables struct {
	// records is the emitter list, as packed vec4<u32>.
	records []byte
	// index maps a primitive index to its emitter slot plus one; zero means
	// the primitive is not a sampleable emitter.
	index []byte
	count uint32
	// sig identifies the scene these were built from, so they are rebuilt only
	// when the packed scene actually changes.
	sig uint64
}

// buildPTEmitters collects the emissive primitives that next-event estimation
// can aim at.
//
// Instanced template primitives are deliberately excluded. They live in the
// same prims array as the static ones, starting at the lowest template
// PrimBase, but their entries hold *template-local* geometry — the world
// position depends on which placement was hit. Sampling one as though its
// stored coordinates were world coordinates would put light in the wrong
// place. They keep being found by BSDF sampling, which is correct because the
// MIS weighting below asks the emitter table whether a given hit was
// NEE-sampleable and gives full weight when it was not.
func buildPTEmitters(prims []GPUPrimitive, templates []GPUTemplateRecord) ptEmitterTables {
	staticEnd := len(prims)
	for i := range templates {
		if b := int(templates[i].PrimBase); b < staticEnd {
			staticEnd = b
		}
	}

	index := make([]uint32, len(prims))
	var records []uint32
	var count uint32
	for i := 0; i < staticEnd; i++ {
		p := &prims[i]
		if p.Meta[1] != uint32(scene.MatEmit) {
			continue
		}
		kind := p.Meta[0]
		// Only the kinds pt.wesl knows how to sample by area. Anything else
		// stays BSDF-only rather than being sampled wrongly.
		if kind != primSphere && kind != primBox {
			continue
		}
		if emitterRadiance(p) <= 0 {
			continue
		}
		index[i] = count + 1
		records = append(records, uint32(i), kind, 0, 0)
		count++
	}

	t := ptEmitterTables{count: count, sig: emitterSig(prims, staticEnd, count)}
	t.records = u32Bytes(records)
	t.index = u32Bytes(index)
	if len(t.records) == 0 {
		// A zero-length storage binding is invalid; one dead slot is simpler
		// than making the binding conditional.
		t.records = make([]byte, ptEmitterStride)
	}
	if len(t.index) == 0 {
		t.index = make([]byte, 4)
	}
	return t
}

// emitterRadiance is the emitter's peak channel. Emissive surfaces radiate
// their albedo in trace.wesl and here alike.
func emitterRadiance(p *GPUPrimitive) float32 {
	return float32(math.Max(float64(p.Albedo[0]),
		math.Max(float64(p.Albedo[1]), float64(p.Albedo[2]))))
}

func emitterSig(prims []GPUPrimitive, staticEnd int, count uint32) uint64 {
	return uint64(len(prims))<<40 | uint64(staticEnd)<<16 | uint64(count)
}
