package webgpu

import (
	"math"
	"unsafe"

	"raytracer/internal/gpuscene"
	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/texture"
	"raytracer/internal/vec"
)

// Primitive kinds, mirrored by PRIM_* in trace.wgsl. Sphere/box/cylinder/cone/
// torus reuse the CPU bvh.Kind* codes so the dispatch stays aligned; plane is
// 1 (planes are not in the CPU BVH but the GPU prim buffer needs a code).
const (
	primSphere   uint32 = 0
	primPlane    uint32 = 1
	primBox      uint32 = 2
	primCylinder uint32 = 3
	primCone     uint32 = 4
	primTorus    uint32 = 5
	primRing     uint32 = 6
	primLens     uint32 = 7
)

// maxPrims caps the GPU primitive storage buffer. The current scenes use far
// fewer; anything beyond this is dropped (and logged by the caller) rather than
// silently corrupting the buffer.
const maxPrims = 4096

const maxLights = 1024

const (
	maxTerrains        = 8
	maxTerrainVals     = scene.MaxTerrainGridCells
	maxTerrainFeatures = 256
	maxTerrainPads     = 64
	maxTerrainMipVals  = 1 << 21 // min/max pairs as vec2 (8 bytes each)
	maxWaters      = 64
)

const (
	// permCount is Perlin's duplicated permutation table length.
	permCount = 512
	// maxAOFloats sizes the ambient-occlusion volume buffer: aoVolMaxCells (1e6)
	// cells x 6 ambient-cube faces. Volumes larger than this are truncated.
	maxAOFloats = 6_000_000
	// maxCampfires caps the per-frame resolved campfire clusters.
	maxCampfires = 64
)

// GPUPrimitive is the std430 layout consumed by trace.wgsl. Every field is a
// 32-bit lane so the Go struct packs with no padding and can be uploaded by a
// straight reinterpret-cast. Keep this in lockstep with struct Prim in the
// shader; primStride and TestGPUPrimitiveLayout guard the size.
//
//	GeoA: sphere -> (center.xyz, radius); plane -> (n.xyz, d); box -> (min.xyz, _)
//	      cylinder -> (cx, cz, radius, ymin); cone -> (cx, cz, rbase, ybase)
//	      torus -> (center.xyz, majorR)
//	GeoB: box -> (max.xyz, _); sphere -> (cut_off,..); cylinder -> (ymax, radius_top,..);
//	      cone -> (ytip, capped_flag,..); torus -> (minorR,..); unused otherwise
//	Albedo: linear rgb in xyz
//	Albedo2: checker second color (MatChecker)
//	Params: (rough, ior, reflect, transmit)
//	Meta: (kind, material, texture, flags); bit0 = transformed, bit1 = thin glass
//	Xf0/Xf1/Xf2: world->local rotation rows (xyz) + translation t in .w, valid
//	      only when flags bit0 is set (see scene.Transform.GPUData)
//
// For a box GeoA.w/GeoB.w double as the hole range (holeStart, holeCount) into
// the shared holes buffer; see PackHoles.
type GPUPrimitive struct {
	GeoA    [4]float32
	GeoB    [4]float32
	Albedo  [4]float32
	Albedo2 [4]float32
	Params  [4]float32
	Meta    [4]uint32
	Xf0     [4]float32
	Xf1     [4]float32
	Xf2     [4]float32
}

// primStride is the byte stride of one GPUPrimitive / Prim element.
const primStride = 144

// primFlagTransformed marks a primitive whose Xf0..Xf2 hold a valid
// world->local transform (mirrored by PRIM_FLAG_TRANSFORMED in trace.wgsl).
const primFlagTransformed uint32 = 1

// primFlagGlassThin marks glass as a single sheet (one transmission event).
const primFlagGlassThin uint32 = 2

func surfaceFlags(s scene.Surface) uint32 {
	if s.Mat == scene.MatGlass && s.Thin {
		return primFlagGlassThin
	}
	return 0
}

// maxHoles caps the shared box-hole CSG buffer.
const maxHoles = 1024

// GPUHole is one axis-aligned subtracted region in a box's local space
// (std430, 32-byte stride), mirrored by struct Hole in trace.wgsl.
type GPUHole struct {
	Min [4]float32
	Max [4]float32
}

const holeStride = 32

// GPULight is the std430 layout consumed by trace.wgsl.
//
//	Point: pos.w = 0
//	Spot:  pos.w = 1, color.w = cos(half-angle), falloff.zw = yaw/pitch of Dir
//
//	Falloff: (cullR2, invR2, spotYaw, spotPitch)
type GPULight struct {
	Pos     [4]float32
	Color   [4]float32
	Falloff [4]float32
}

const lightStride = 48

type GPUTerrain struct {
	Bounds0  [4]float32 // originX, originZ, sizeX, sizeZ
	Bounds1  [4]float32 // minY, maxY, step, _
	Grid     [4]uint32  // gnx, gnz, heightOffset, normalOffset
	Material [4]uint32  // grass, rock, snow, _
	Color0   [4]float32 // grass tint
	Color1   [4]float32 // rock tint
	Color2   [4]float32 // snow tint
	Blend    [4]float32 // slopeLo, slopeHi, snowLo, snowHi
	Analytic [4]float32 // base, detail, detailScale, nearStart
	Island0  [4]float32 // centerX, centerZ, radius, margin
	Island1  [4]float32 // floor, nearEnd, hybrid (1/0), _
	Offsets  [4]uint32  // featureBase, padBase, featureCount, padCount
	Mip      [4]uint32  // mipBase (vec2 index), l0nx, l0nz, levelCount
	Coarse   [4]float32 // cwx, cwz, cInvDx, cInvDz
}

const terrainStride = 224

// GPUTerrainFeature mirrors a sculpted peak/valley for WGSL heightAnalytic.
type GPUTerrainFeature struct {
	Pos   [4]float32 // x, z, height, width
	Shape [4]float32 // steepness, extendX, extendZ, angle
	Cull  [4]float32 // cullR2 (world XZ dist² beyond which |contribution| < featureCullEps), _, _, _
}

const terrainFeatureStride = 48

// GPUTerrainPad mirrors a flattened building pad for WGSL heightAnalytic.
type GPUTerrainPad struct {
	Center [4]float32 // cx, cz, halfX, halfZ
	Params [4]float32 // level, margin, angle, _
}

const terrainPadStride = 32

type GPUWater struct {
	Geom   [4]float32 // cx, cz, radius, level
	Params [4]float32 // ripple, rippleSpeed, dirX, dirZ
	Albedo [4]float32
	Surf   [4]float32 // rough, ior, reflect, transmit
	Meta   [4]uint32  // material, texture, _, _
}

const waterStride = 80

// PackPrimitives flattens the scene's analytic primitives into the GPU
// primitive layout, including rotated/translated primitives (Xform) and boxes
// with CSG holes. Transforms ride along in Xf0..Xf2 and holes are referenced by
// (start,count) into the buffer PackHoles produces. Planes ignore Xform, just
// like the CPU tracer.
func PackPrimitives(s *scene.Scene) []GPUPrimitive {
	if s == nil {
		return nil
	}
	out := make([]GPUPrimitive, 0, len(s.Spheres)+len(s.Planes)+len(s.Boxes))

	for i := range s.Spheres {
		out = append(out, spherePrim(&s.Spheres[i]))
	}
	for i := range s.Planes {
		pl := &s.Planes[i]
		out = append(out, GPUPrimitive{
			GeoA:    [4]float32{f(pl.N.X), f(pl.N.Y), f(pl.N.Z), f(pl.D)},
			Albedo:  albedo(pl.Albedo),
			Albedo2: albedo(pl.Albedo2),
			Params:  surfaceParams(pl.Surface),
			Meta:    [4]uint32{primPlane, uint32(pl.Mat), uint32(pl.Tex), surfaceFlags(pl.Surface)},
		})
	}
	holeStart := uint32(0)
	for i := range s.Boxes {
		bx := &s.Boxes[i]
		out = append(out, boxPrim(bx, holeStart))
		holeStart += uint32(len(bx.Holes))
	}
	for i := range s.Cylinders {
		out = append(out, cylinderPrim(&s.Cylinders[i]))
	}
	for i := range s.Cones {
		out = append(out, conePrim(&s.Cones[i]))
	}
	for i := range s.Tori {
		out = append(out, torusPrim(&s.Tori[i]))
	}
	for i := range s.Rings {
		out = append(out, ringPrim(&s.Rings[i]))
	}
	for i := range s.Lenses {
		out = append(out, lensPrim(&s.Lenses[i]))
	}
	if len(out) > maxPrims {
		out = out[:maxPrims]
	}
	return out
}

// setXform stores a primitive's world->local transform in Xf0..Xf2 and sets the
// transformed flag, or leaves the identity (flag clear) for a nil transform.
func setXform(p *GPUPrimitive, x *scene.Transform) {
	if x == nil {
		return
	}
	r0, r1, r2, t := x.GPUData()
	p.Xf0 = [4]float32{f(r0.X), f(r0.Y), f(r0.Z), f(t.X)}
	p.Xf1 = [4]float32{f(r1.X), f(r1.Y), f(r1.Z), f(t.Y)}
	p.Xf2 = [4]float32{f(r2.X), f(r2.Y), f(r2.Z), f(t.Z)}
	p.Meta[3] |= primFlagTransformed
}

func spherePrim(sp *scene.Sphere) GPUPrimitive {
	alb, alb2 := surfaceColors(sp.Surface)
	p := GPUPrimitive{
		GeoA:    [4]float32{f(sp.Center.X), f(sp.Center.Y), f(sp.Center.Z), f(sp.Radius)},
		GeoB:    [4]float32{f(sp.CutOff), 0, 0, 0},
		Albedo:  alb,
		Albedo2: alb2,
		Params:  surfaceParams(sp.Surface),
		Meta:    [4]uint32{primSphere, uint32(sp.Mat), uint32(sp.Tex), surfaceFlags(sp.Surface)},
	}
	setXform(&p, sp.Xform)
	return p
}

func boxPrim(bx *scene.Box, holeStart uint32) GPUPrimitive {
	holeCount := uint32(len(bx.Holes))
	alb, alb2 := surfaceColors(bx.Surface)
	p := GPUPrimitive{
		GeoA:    [4]float32{f(bx.Min.X), f(bx.Min.Y), f(bx.Min.Z), f32u(holeStart)},
		GeoB:    [4]float32{f(bx.Max.X), f(bx.Max.Y), f(bx.Max.Z), f32u(holeCount)},
		Albedo:  alb,
		Albedo2: alb2,
		Params:  surfaceParams(bx.Surface),
		Meta:    [4]uint32{primBox, uint32(bx.Mat), uint32(bx.Tex), surfaceFlags(bx.Surface)},
	}
	setXform(&p, bx.Xform)
	return p
}

func cylinderPrim(c *scene.Cylinder) GPUPrimitive {
	rt := c.RadiusTop
	if rt == 0 {
		rt = c.Radius
	}
	alb, alb2 := surfaceColors(c.Surface)
	p := GPUPrimitive{
		GeoA:    [4]float32{f(c.CX), f(c.CZ), f(c.Radius), f(c.YMin)},
		GeoB:    [4]float32{f(c.YMax), f(rt), openCap(c.OpenMin), openCap(c.OpenMax)},
		Albedo:  alb,
		Albedo2: alb2,
		Params:  surfaceParams(c.Surface),
		Meta:    [4]uint32{primCylinder, uint32(c.Mat), uint32(c.Tex), surfaceFlags(c.Surface)},
	}
	setXform(&p, c.Xform)
	return p
}

func conePrim(c *scene.Cone) GPUPrimitive {
	alb, alb2 := surfaceColors(c.Surface)
	capFlag := float32(0)
	if c.Capped {
		capFlag = 1
	}
	p := GPUPrimitive{
		GeoA:    [4]float32{f(c.CX), f(c.CZ), f(c.RBase), f(c.YBase)},
		GeoB:    [4]float32{f(c.YTip), capFlag, 0, 0},
		Albedo:  alb,
		Albedo2: alb2,
		Params:  surfaceParams(c.Surface),
		Meta:    [4]uint32{primCone, uint32(c.Mat), uint32(c.Tex), surfaceFlags(c.Surface)},
	}
	setXform(&p, c.Xform)
	return p
}

func torusPrim(t *scene.Torus) GPUPrimitive {
	alb, alb2 := surfaceColors(t.Surface)
	p := GPUPrimitive{
		GeoA:    [4]float32{f(t.Center.X), f(t.Center.Y), f(t.Center.Z), f(t.R)},
		GeoB:    [4]float32{f(t.Rm), 0, 0, 0},
		Albedo:  alb,
		Albedo2: alb2,
		Params:  surfaceParams(t.Surface),
		Meta:    [4]uint32{primTorus, uint32(t.Mat), uint32(t.Tex), surfaceFlags(t.Surface)},
	}
	setXform(&p, t.Xform)
	return p
}

func ringPrim(r *scene.Ring) GPUPrimitive {
	h := r.Height
	if h <= 0 {
		h = scene.DefaultRingHeight
	}
	alb, alb2 := surfaceColors(r.Surface)
	p := GPUPrimitive{
		GeoA:    [4]float32{f(r.CX), f(r.CZ), f(r.Radius), f(r.CY)},
		GeoB:    [4]float32{f(h), 0, 0, 0},
		Albedo:  alb,
		Albedo2: alb2,
		Params:  surfaceParams(r.Surface),
		Meta:    [4]uint32{primRing, uint32(r.Mat), uint32(r.Tex), surfaceFlags(r.Surface)},
	}
	setXform(&p, r.Xform)
	return p
}

func lensPrim(l *scene.Lens) GPUPrimitive {
	th := l.Thickness
	if th <= 0 {
		th = scene.DefaultLensThickness
	}
	alb, alb2 := surfaceColors(l.Surface)
	p := GPUPrimitive{
		GeoA:    [4]float32{f(l.CX), f(l.CY), f(l.CZ), f(l.Aperture)},
		GeoB:    [4]float32{f(l.RFront), f(l.RBack), f(th), 0},
		Albedo:  alb,
		Albedo2: alb2,
		Params:  surfaceParams(l.Surface),
		Meta:    [4]uint32{primLens, uint32(l.Mat), uint32(l.Tex), surfaceFlags(l.Surface)},
	}
	setXform(&p, l.Xform)
	return p
}

// PackHoles flattens every box's CSG holes into one buffer in box order, so the
// (holeStart, holeCount) ranges PackPrimitives and PackBlockers bake into each
// box prim address the same slice. Holes are stored in the box's local space.
func PackHoles(s *scene.Scene) []GPUHole {
	if s == nil {
		return nil
	}
	var out []GPUHole
	for i := range s.Boxes {
		for _, h := range s.Boxes[i].Holes {
			out = append(out, GPUHole{
				Min: [4]float32{f(h.Min.X), f(h.Min.Y), f(h.Min.Z), 0},
				Max: [4]float32{f(h.Max.X), f(h.Max.Y), f(h.Max.Z), 0},
			})
			if len(out) >= maxHoles {
				return out
			}
		}
	}
	return out
}

// PackBlockers flattens the primitives that should cast shadows. It mirrors the
// CPU blocker BVH's material filtering for the primitives currently ported to
// WGSL: emissive/glass spheres do not cast shadows, glass boxes do not cast
// shadows, and planes are tested separately by the CPU shadow path so they are
// included here as blocker primitives.
func PackBlockers(s *scene.Scene) []GPUPrimitive {
	if s == nil {
		return nil
	}
	out := make([]GPUPrimitive, 0, len(s.Spheres)+len(s.Planes)+len(s.Boxes))
	for i := range s.Spheres {
		sp := &s.Spheres[i]
		if sp.Mat == scene.MatEmit || sp.Mat == scene.MatGlass {
			continue
		}
		out = append(out, spherePrim(sp))
	}
	for i := range s.Planes {
		pl := &s.Planes[i]
		out = append(out, GPUPrimitive{
			GeoA:    [4]float32{f(pl.N.X), f(pl.N.Y), f(pl.N.Z), f(pl.D)},
			Albedo:  albedo(pl.Albedo),
			Albedo2: albedo(pl.Albedo2),
			Params:  surfaceParams(pl.Surface),
			Meta:    [4]uint32{primPlane, uint32(pl.Mat), uint32(pl.Tex), surfaceFlags(pl.Surface)},
		})
	}
	// holeStart accumulates over every box (even skipped glass ones) so the
	// ranges baked here index the same PackHoles buffer as PackPrimitives.
	holeStart := uint32(0)
	for i := range s.Boxes {
		bx := &s.Boxes[i]
		if bx.Mat != scene.MatGlass {
			out = append(out, boxPrim(bx, holeStart))
		}
		holeStart += uint32(len(bx.Holes))
	}
	for i := range s.Cylinders {
		cy := &s.Cylinders[i]
		if cy.Mat == scene.MatGlass {
			continue
		}
		out = append(out, cylinderPrim(cy))
	}
	for i := range s.Cones {
		co := &s.Cones[i]
		if co.Mat == scene.MatGlass {
			continue
		}
		out = append(out, conePrim(co))
	}
	for i := range s.Rings {
		rg := &s.Rings[i]
		if rg.Mat == scene.MatGlass {
			continue
		}
		out = append(out, ringPrim(rg))
	}
	for i := range s.Lenses {
		ln := &s.Lenses[i]
		if ln.Mat == scene.MatGlass {
			continue
		}
		out = append(out, lensPrim(ln))
	}
	// Tori are intentionally excluded: the CPU shadow path skips tori entirely.
	if len(out) > maxPrims {
		out = out[:maxPrims]
	}
	return out
}

// PackLights computes the per-light cull distance and falloff for static point
// and spot lights (the same model the shader's add_point_light uses). Campfire
// parameters are resolved in the shader from constant data (PackCampfireParams).
func PackLights(s *scene.Scene) []GPULight {
	if s == nil {
		return nil
	}
	out := make([]GPULight, 0, len(s.Lights))
	for i := range s.Lights {
		out = append(out, packLight(&s.Lights[i]))
	}
	if len(out) > maxLights {
		out = out[:maxLights]
	}
	return out
}

func packLight(l *scene.Light) GPULight {
	cullR2, invR2 := lightCull(l.Color, l.Range)
	gl := GPULight{
		Pos:     [4]float32{f(l.Pos.X), f(l.Pos.Y), f(l.Pos.Z), 0},
		Color:   albedo(l.Color),
		Falloff: [4]float32{f(cullR2), f(invR2), 0, 0},
	}
	if l.IsSpot() {
		d := l.Dir.Normalize()
		half := l.ConeDeg * 0.5 * math.Pi / 180
		gl.Pos[3] = 1
		gl.Color[3] = float32(math.Cos(half))
		gl.Falloff[2] = float32(math.Atan2(d.X, -d.Z))
		pitch := d.Y
		if pitch > 1 {
			pitch = 1
		} else if pitch < -1 {
			pitch = -1
		}
		gl.Falloff[3] = float32(math.Asin(pitch))
	}
	return gl
}

func PackTerrains(s *scene.Scene) ([]GPUTerrain, []float32, []GPUTerrainFeature, []GPUTerrainPad, []float32) {
	if s == nil {
		return nil, nil, nil, nil, nil
	}
	terrains := make([]GPUTerrain, 0, len(s.Terrains))
	samples := make([]float32, 0)
	mips := make([]float32, 0)
	features := make([]GPUTerrainFeature, 0)
	pads := make([]GPUTerrainPad, 0)
	for i := range s.Terrains {
		t := &s.Terrains[i]
		snap := t.CacheSnapshot()
		// Heights and normals live in separate regions (float offsets in
		// Grid[2]/Grid[3]) so the march loop's bilinear height fetch touches
		// 4 bytes per tap instead of a full vec4.
		hOff := len(samples)
		for _, h := range snap.Height {
			samples = append(samples, f(h))
		}
		nOff := len(samples)
		for _, n := range snap.Normal {
			samples = append(samples, f(n.X), f(n.Y), f(n.Z))
		}
		mipLevels, cwx, cwz, cInvDx, cInvDz := t.MipSnapshot()
		mipBase := uint32(len(mips) / 2)
		var l0nx, l0nz, mipCount uint32
		for _, lvl := range mipLevels {
			mips = append(mips, lvl.MinMax...)
			if l0nx == 0 {
				l0nx, l0nz = uint32(lvl.NX), uint32(lvl.NZ)
			}
			mipCount++
		}
		featBase := uint32(len(features))
		for _, feat := range t.Features {
			ex, ez := feat.ExtendX, feat.ExtendZ
			if ex == 0 {
				ex = 1
			}
			if ez == 0 {
				ez = 1
			}
			w := feat.Width
			if w == 0 {
				w = 1
			}
			st := feat.Steepness
			if st == 0 {
				st = 2
			}
			// Conservative cull radius: the feature adds height·exp(-d^st)
			// with d ≥ worldDist/(w·max(ex,ez)), so beyond dCut the
			// contribution is under featureCullEps and the GPU skips it.
			const featureCullEps = 1e-5
			cullR2 := 0.0
			if ah := math.Abs(feat.Height); ah > featureCullEps {
				dCut := math.Pow(math.Log(ah/featureCullEps), 1/st)
				r := dCut * w * math.Max(ex, ez)
				cullR2 = r * r
			}
			features = append(features, GPUTerrainFeature{
				Pos:   [4]float32{f(feat.PosX), f(feat.PosZ), f(feat.Height), f(w)},
				Shape: [4]float32{f(st), f(ex), f(ez), f(feat.Angle)},
				Cull:  [4]float32{f(cullR2), 0, 0, 0},
			})
		}
		padBase := uint32(len(pads))
		for _, p := range t.Pads {
			pads = append(pads, GPUTerrainPad{
				Center: [4]float32{f(p.CenterX), f(p.CenterZ), f(p.HalfX), f(p.HalfZ)},
				Params: [4]float32{f(p.Level), f(p.Margin), f(p.Angle), 0},
			})
		}
		nearStart, nearEnd := t.HybridNearDistances()
		isl := t.Island
		hybrid := float32(0)
		if t.HybridLOD() {
			hybrid = 1
		}
		terrains = append(terrains, GPUTerrain{
			Bounds0:  [4]float32{f(t.OriginX), f(t.OriginZ), f(t.SizeX), f(t.SizeZ)},
			Bounds1:  [4]float32{f(t.MinY), f(t.MaxY), f(t.Step), 0},
			Grid:     [4]uint32{uint32(snap.GNX), uint32(snap.GNZ), uint32(hOff), uint32(nOff)},
			Material: [4]uint32{uint32(t.Grass), uint32(t.Rock), uint32(t.Snow), 0},
			Color0:   albedo(t.GrassCol),
			Color1:   albedo(t.RockCol),
			Color2:   albedo(t.SnowCol),
			Blend:    [4]float32{f(t.SlopeLo), f(t.SlopeHi), f(t.SnowLo), f(t.SnowHi)},
			Analytic: [4]float32{f(t.Base), f(t.Detail), f(t.DetailScale), f(nearStart)},
			Island0:  [4]float32{f(isl.CenterX), f(isl.CenterZ), f(isl.Radius), f(isl.Margin)},
			Island1:  [4]float32{f(isl.Floor), f(nearEnd), hybrid, 0},
			Offsets:  [4]uint32{featBase, padBase, uint32(len(t.Features)), uint32(len(t.Pads))},
			Mip:      [4]uint32{mipBase, l0nx, l0nz, mipCount},
			Coarse:   [4]float32{f(cwx), f(cwz), f(cInvDx), f(cInvDz)},
		})
		if len(terrains) >= maxTerrains || len(samples)/4 >= maxTerrainVals ||
			len(features) > maxTerrainFeatures || len(pads) > maxTerrainPads ||
			len(mips)/2 > maxTerrainMipVals {
			break
		}
	}
	return terrains, samples, features, pads, mips
}

func PackWaters(s *scene.Scene) []GPUWater {
	if s == nil {
		return nil
	}
	out := make([]GPUWater, 0, len(s.Waters))
	for i := range s.Waters {
		w := &s.Waters[i]
		out = append(out, GPUWater{
			Geom:   [4]float32{f(w.CX), f(w.CZ), f(w.Radius), f(w.Level)},
			Params: [4]float32{f(w.Ripple), f(w.RippleSpeed), f(w.RippleDirX), f(w.RippleDirZ)},
			Albedo: albedo(w.Albedo),
			Surf:   surfaceParams(w.Surface),
			Meta:   [4]uint32{uint32(w.Mat), uint32(w.Tex), boolU32(w.MaskShoreline), 0},
		})
		if len(out) >= maxWaters {
			break
		}
	}
	return out
}

// CampfireParams holds a campfire's constant parameters. The campfire shader
// resolves sub-light positions and intensities from these each frame, so this
// struct is uploaded once as part of the static scene cache. Mirrors struct
// CampfireParams in trace.wgsl (std430, 64-byte stride).
type CampfireParams struct {
	Core  [4]float32 // cx, cy, cz, range
	Color [4]float32 // r, g, b, 0
	Param [4]float32 // brightness, jitter, flicker, speed
	Phase [4]float32 // seed phase, 0, 0, 0
}

const campfireStride = 64

// PackPerm returns Perlin's 512-entry permutation table for the GPU noise.
func PackPerm() []uint32 {
	tbl := texture.PermTable()
	out := make([]uint32, permCount)
	for i := range out {
		out[i] = uint32(tbl[i])
	}
	return out
}

// PackCampfireParams packs the scene's campfire parameters for the GPU. This
// is called once as part of the static scene cache; per-frame flicker is
// resolved in the shader from these parameters.
func PackCampfireParams(s *scene.Scene) []CampfireParams {
	if s == nil {
		return nil
	}
	out := make([]CampfireParams, 0, len(s.Campfires))
	for i := range s.Campfires {
		fr := &s.Campfires[i]
		bright := fr.Brightness
		if bright == 0 {
			bright = 1
		}
		speed := fr.Speed
		if speed == 0 {
			speed = 1
		}
		p := CampfireParams{
			Core:  [4]float32{f(fr.Center.X), f(fr.Center.Y), f(fr.Center.Z), f(fr.Range)},
			Color: albedo(fr.Color),
			Param: [4]float32{f(bright), f(fr.Jitter), f(fr.Flicker), f(speed)},
			Phase: [4]float32{f(fr.Seed), 0, 0, 0},
		}
		out = append(out, p)
		if len(out) >= maxCampfires {
			break
		}
	}
	return out
}

// AOVolume is the GPU-side handle for a baked ambient-occlusion volume: scalar
// grid params live in the uniform Params; Data is the nx*ny*nz*6 ambient cube.
type AOVolume struct {
	Min             vec.V
	Inv, Cell, Bias float64
	NX, NY, NZ      int
	Data            []float32
}

// PackAOVolume returns the view's baked volume snapshot for upload, or ok=false
// when the scene has no occluding geometry. The AO runtime toggle (View.AO) is
// applied per frame in Render, not here, so toggling it costs no re-pack.
func PackAOVolume(v *render.View) (AOVolume, bool) {
	if v == nil || !v.AOok {
		return AOVolume{}, false
	}
	data := v.AOData.Data
	if len(data) > maxAOFloats {
		data = data[:maxAOFloats]
	}
	return AOVolume{
		Min: v.AOData.Min, Inv: v.AOData.Inv, Cell: v.AOData.Cell, Bias: v.AOData.Bias,
		NX: v.AOData.NX, NY: v.AOData.NY, NZ: v.AOData.NZ, Data: data,
	}, true
}

func campfireBytes(fires []CampfireParams) []byte {
	if len(fires) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&fires[0])), len(fires)*campfireStride)
}

func u32Bytes(v []uint32) []byte {
	if len(v) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&v[0])), len(v)*4)
}

func lightCull(color vec.V, rng float64) (cullR2, invR2 float64) {
	cmax := max(color.X, max(color.Y, color.Z))
	autoR2 := 0.0
	if cmax > gpuscene.LightCullEps*gpuscene.LightAttenBase {
		autoR2 = (cmax/gpuscene.LightCullEps - gpuscene.LightAttenBase) / gpuscene.LightAttenQuadratic
		if autoR2 < 0 {
			autoR2 = 0
		}
	}
	if rng > 0 {
		r2 := rng * rng
		return r2, 1 / r2
	}
	return autoR2, 0
}

// primBytes reinterprets the packed primitives as the raw little-endian buffer
// uploaded to the GPU. GPUPrimitive is all 32-bit lanes with no padding, so the
// in-memory layout already matches std430.
func primBytes(prims []GPUPrimitive) []byte {
	if len(prims) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&prims[0])), len(prims)*primStride)
}

func lightBytes(lights []GPULight) []byte {
	if len(lights) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&lights[0])), len(lights)*lightStride)
}

func waterBytes(waters []GPUWater) []byte {
	if len(waters) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&waters[0])), len(waters)*waterStride)
}

func terrainBytes(terrains []GPUTerrain) []byte {
	if len(terrains) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&terrains[0])), len(terrains)*terrainStride)
}

func terrainFeatureBytes(features []GPUTerrainFeature) []byte {
	if len(features) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&features[0])), len(features)*terrainFeatureStride)
}

func terrainPadBytes(pads []GPUTerrainPad) []byte {
	if len(pads) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&pads[0])), len(pads)*terrainPadStride)
}

func floatBytes(values []float32) []byte {
	if len(values) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&values[0])), len(values)*4)
}

func surfaceParams(s scene.Surface) [4]float32 {
	return [4]float32{f(s.Rough), f(s.IOR), f(s.Reflect), f(s.Transmit)}
}

func albedo(v vec.V) [4]float32 { return [4]float32{f(v.X), f(v.Y), f(v.Z), 0} }

func surfaceColors(s scene.Surface) (alb, alb2 [4]float32) {
	return albedo(s.Albedo), albedo(s.Albedo2)
}

func f(x float64) float32 { return float32(x) }

func boolU32(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}

func openCap(open bool) float32 {
	if open {
		return 1
	}
	return 0
}

// f32u stores a small unsigned count in an f32 lane; exact for v < 2^24.
func f32u(v uint32) float32 { return float32(v) }

// BoxFacesPerPrim is the number of per-face texture slots per GPU primitive.
const BoxFacesPerPrim = 6

// boxFacesPerPrim is an alias for internal use.
const boxFacesPerPrim = BoxFacesPerPrim

func appendFaceSlots(out []uint32, bx *scene.Box) []uint32 {
	slots := make([]uint32, boxFacesPerPrim)
	for f := range bx.FaceTex {
		slots[f] = uint32(bx.FaceTex[f])
	}
	return append(out, slots...)
}

func appendZeroFaceSlots(out []uint32) []uint32 {
	return append(out, make([]uint32, boxFacesPerPrim)...)
}

// PackBoxFaceTextures returns per-face texture ids for PackPrimitives order.
func PackBoxFaceTextures(s *scene.Scene) []uint32 {
	return packFaceTextures(s, false, nil)
}

// PackBoxFaceTexturesOmitDynamic matches packPrimitivesOmitDynamic ordering.
func PackBoxFaceTexturesOmitDynamic(s, skipFrom *scene.Scene) []uint32 {
	return packFaceTextures(s, true, skipFrom)
}

func packFaceTextures(s *scene.Scene, omitDynamic bool, skipFrom *scene.Scene) []uint32 {
	if s == nil {
		return nil
	}
	var sph, box, cyl, lens map[int]struct{}
	if omitDynamic {
		sph, box, cyl, lens = dynamicIndexSets(skipFrom)
	}
	var out []uint32
	for i := range s.Spheres {
		if omitDynamic {
			if _, skip := sph[i]; skip {
				continue
			}
		}
		out = appendZeroFaceSlots(out)
	}
	for range s.Planes {
		out = appendZeroFaceSlots(out)
	}
	holeStart := uint32(0)
	for i := range s.Boxes {
		if omitDynamic {
			if _, skip := box[i]; skip {
				holeStart += uint32(len(s.Boxes[i].Holes))
				continue
			}
		}
		out = appendFaceSlots(out, &s.Boxes[i])
		holeStart += uint32(len(s.Boxes[i].Holes))
	}
	for i := range s.Cylinders {
		if omitDynamic {
			if _, skip := cyl[i]; skip {
				continue
			}
		}
		out = appendZeroFaceSlots(out)
	}
	for range s.Cones {
		out = appendZeroFaceSlots(out)
	}
	for range s.Tori {
		out = appendZeroFaceSlots(out)
	}
	for range s.Rings {
		out = appendZeroFaceSlots(out)
	}
	for i := range s.Lenses {
		if omitDynamic {
			if _, skip := lens[i]; skip {
				continue
			}
		}
		out = appendZeroFaceSlots(out)
	}
	return out
}

func appendDynamicBodyFaceTextures(s *scene.Scene, faces []uint32) []uint32 {
	if s == nil {
		return faces
	}
	sph, box, cyl, lens := dynamicIndexSets(s)
	for i := range s.Spheres {
		if _, ok := sph[i]; !ok {
			continue
		}
		faces = appendZeroFaceSlots(faces)
	}
	for i := range s.Boxes {
		if _, ok := box[i]; !ok {
			continue
		}
		faces = appendFaceSlots(faces, &s.Boxes[i])
	}
	for i := range s.Cylinders {
		if _, ok := cyl[i]; !ok {
			continue
		}
		faces = appendZeroFaceSlots(faces)
	}
	for i := range s.Lenses {
		if _, ok := lens[i]; !ok {
			continue
		}
		faces = appendZeroFaceSlots(faces)
	}
	return faces
}

// PackPrimitivesOmitDynamic is exported for tests.
func PackPrimitivesOmitDynamic(s, skipFrom *scene.Scene) []GPUPrimitive {
	return packPrimitivesOmitDynamic(s, skipFrom)
}

func PackSceneFaceTextures(s *scene.Scene, prims []GPUPrimitive) []uint32 {
	return packSceneFaceTextures(s, prims)
}

func packSceneFaceTextures(s *scene.Scene, prims []GPUPrimitive) []uint32 {
	if s == nil {
		return nil
	}
	var faces []uint32
	if s.HasInstancing() {
		static, ok := s.StaticPrimitiveCounts()
		if ok {
			staticScene := sliceStaticScene(s, static)
			faces = PackBoxFaceTexturesOmitDynamic(staticScene, s)
			faces = appendDynamicBodyFaceTextures(s, faces)
			if cat := s.Instancing(); cat != nil {
				for _, t := range cat.Templates {
					if t.Scene != nil {
						faces = append(faces, PackBoxFaceTextures(t.Scene)...)
					}
				}
			}
		}
	} else if len(s.DynamicBodies) > 0 {
		faces = PackBoxFaceTexturesOmitDynamic(s, s)
		faces = appendDynamicBodyFaceTextures(s, faces)
	} else {
		faces = PackBoxFaceTextures(s)
	}
	want := len(prims) * BoxFacesPerPrim
	if len(faces) < want {
		pad := make([]uint32, want-len(faces))
		faces = append(faces, pad...)
	} else if len(faces) > want {
		faces = faces[:want]
	}
	return faces
}

func holeBytes(holes []GPUHole) []byte {
	if len(holes) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&holes[0])), len(holes)*holeStride)
}
