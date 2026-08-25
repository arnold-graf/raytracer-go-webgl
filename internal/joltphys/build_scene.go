package joltphys

import (
	"math"

	"github.com/bbitechnologies/jolt-go/jolt"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

type ownedShape struct {
	shape *jolt.Shape
}

type ownedBody struct {
	body *jolt.BodyID
}

func (w *World) addStaticBody(shape *jolt.Shape, pos jolt.Vec3) {
	body := w.bi.CreateBody(shape, pos, jolt.MotionTypeStatic, false)
	if !body.Valid() {
		return
	}
	w.bodies = append(w.bodies, ownedBody{body: body})
}

func (w *World) addShape(shape *jolt.Shape) {
	w.shapes = append(w.shapes, ownedShape{shape: shape})
}

// maxStaticCompoundParts caps child shapes per static compound so Jolt temp
// scratch during compound build stays bounded and we stay well under body limits.
const maxStaticCompoundParts = 256

type staticCompoundBatch struct {
	parts  []jolt.CompoundPart
	shapes []*jolt.Shape
}

func staticWorldInv() *scene.Transform {
	return scene.NewRigidTransform(0, 0, 0, vec.V{}).Inverse()
}

func (w *World) appendStaticCompoundPart(batch *staticCompoundBatch, p jolt.CompoundPart, s *jolt.Shape) {
	if p.Shape == nil {
		return
	}
	batch.parts = append(batch.parts, p)
	batch.shapes = append(batch.shapes, s)
	if len(batch.parts) >= maxStaticCompoundParts {
		w.flushStaticCompound(batch)
	}
}

func (w *World) flushStaticCompound(batch *staticCompoundBatch) {
	if len(batch.parts) == 0 {
		return
	}
	compound := jolt.CreateStaticCompound(batch.parts)
	if compound == nil {
		return
	}
	w.addShape(compound)
	w.addStaticBody(compound, jolt.Vec3{})
	for _, s := range batch.shapes {
		w.shapes = append(w.shapes, ownedShape{shape: s})
	}
	batch.parts = nil
	batch.shapes = nil
}

func (w *World) buildFromScene(sc *scene.Scene) error {
	if sc == nil {
		return nil
	}
	batch := &staticCompoundBatch{}
	inv := staticWorldInv()
	for i := range sc.Boxes {
		if isDynamicBox(sc, i) {
			continue
		}
		b := &sc.Boxes[i]
		if !b.Collides() {
			continue
		}
		if len(b.Holes) > 0 {
			for _, frag := range b.SolidFragments() {
				part := scene.Box{Min: frag.Min, Max: frag.Max, Surface: b.Surface}
				p, s := boxCompoundPart(part, inv)
				w.appendStaticCompoundPart(batch, p, s)
			}
			continue
		}
		p, s := boxCompoundPart(*b, inv)
		w.appendStaticCompoundPart(batch, p, s)
	}
	for i := range sc.Cylinders {
		if sc.IsDynamicCylinder(i) {
			continue
		}
		c := &sc.Cylinders[i]
		if !c.Collides() {
			continue
		}
		p, s := cylinderCompoundPart(*c, inv)
		w.appendStaticCompoundPart(batch, p, s)
	}
	w.flushStaticCompound(batch)
	for i := range sc.Terrains {
		w.addTerrainCollider(&sc.Terrains[i])
	}
	return nil
}

// maxPhysicsTerrainAxis caps terrain collision mesh resolution. The render bake
// can be 1600+ vertices per side; feeding that verbatim into Jolt creates
// millions of triangles and multi-second reload stalls.
const maxPhysicsTerrainAxis = 256

// maxPhysicsTerrainCell is the target world spacing between physics heightfield
// samples. Island scenes clip to the landmass so this stays fine near gameplay.
const maxPhysicsTerrainCell = 2.5

// physicsTerrainRegion returns the world X/Z rectangle to cover with collision.
// Island terrains limit the mesh to the landmass plus shore margin instead of
// the full multi-km ocean footprint.
func physicsTerrainRegion(t *scene.Terrain) (x0, z0, x1, z1 float64) {
	x0, z0 = t.OriginX, t.OriginZ
	x1, z1 = t.OriginX+t.SizeX, t.OriginZ+t.SizeZ
	if isl := t.Island; isl.Radius > 0 {
		margin := isl.Margin
		if margin <= 0 {
			margin = isl.Radius * 0.5
		}
		r := isl.Radius + margin
		cx, cz := isl.CenterX, isl.CenterZ
		loX, hiX := cx-r, cx+r
		loZ, hiZ := cz-r, cz+r
		if loX > x0 {
			x0 = loX
		}
		if hiX < x1 {
			x1 = hiX
		}
		if loZ > z0 {
			z0 = loZ
		}
		if hiZ < z1 {
			z1 = hiZ
		}
	}
	return x0, z0, x1, z1
}

// physicsTerrainMesh builds a heightfield triangle mesh for Jolt. Heights come
// from Terrain.Height so hybrid LOD matches rendering.
func physicsTerrainMesh(t *scene.Terrain) (verts []jolt.Vec3, indices []int32) {
	x0, z0, x1, z1 := physicsTerrainRegion(t)
	sx := x1 - x0
	sz := z1 - z0
	if sx <= 0 || sz <= 0 {
		return nil, nil
	}
	pgnx := int(math.Ceil(sx/maxPhysicsTerrainCell)) + 1
	pgnz := int(math.Ceil(sz/maxPhysicsTerrainCell)) + 1
	if pgnx < 2 {
		pgnx = 2
	}
	if pgnz < 2 {
		pgnz = 2
	}
	if pgnx > maxPhysicsTerrainAxis {
		pgnx = maxPhysicsTerrainAxis
	}
	if pgnz > maxPhysicsTerrainAxis {
		pgnz = maxPhysicsTerrainAxis
	}
	verts = make([]jolt.Vec3, pgnx*pgnz)
	for pj := 0; pj < pgnz; pj++ {
		wz := z0
		if pgnz > 1 {
			wz += float64(pj) / float64(pgnz-1) * sz
		}
		for pi := 0; pi < pgnx; pi++ {
			wx := x0
			if pgnx > 1 {
				wx += float64(pi) / float64(pgnx-1) * sx
			}
			idx := pj*pgnx + pi
			verts[idx] = jolt.Vec3{
				X: float32(wx),
				Y: float32(t.Height(wx, wz)),
				Z: float32(wz),
			}
		}
	}
	indices = make([]int32, 0, (pgnx-1)*(pgnz-1)*6)
	for j := 0; j < pgnz-1; j++ {
		row := j * pgnx
		nrow := (j + 1) * pgnx
		for i := 0; i < pgnx-1; i++ {
			a := int32(row + i)
			b := int32(row + i + 1)
			c := int32(nrow + i)
			d := int32(nrow + i + 1)
			indices = append(indices, a, c, b, b, c, d)
		}
	}
	return verts, indices
}

func (w *World) addTerrainCollider(t *scene.Terrain) {
	if t == nil {
		return
	}
	t.Prepare()
	if !t.HasFootprint() {
		return
	}
	verts, indices := physicsTerrainMesh(t)
	if len(verts) == 0 {
		return
	}
	shape := jolt.CreateMesh(verts, indices)
	w.addShape(shape)
	w.addStaticBody(shape, jolt.Vec3{})
}

func isDynamicBox(sc *scene.Scene, i int) bool {
	for _, db := range sc.DynamicBodies {
		if i >= db.Boxes[0] && i < db.Boxes[1] {
			return true
		}
	}
	return false
}

// minColliderHalfY avoids tunneling through very thin floor slabs (often 0.01).
const minColliderHalfY float32 = 0.03

func inflateHalfY(hy float32) float32 {
	if hy < minColliderHalfY {
		return minColliderHalfY
	}
	return hy
}

func boxHalfExtents(b scene.Box) (hx, hy, hz float32, center vec.V) {
	center = vec.V{
		X: (b.Min.X + b.Max.X) * 0.5,
		Y: (b.Min.Y + b.Max.Y) * 0.5,
		Z: (b.Min.Z + b.Max.Z) * 0.5,
	}
	hx = float32((b.Max.X - b.Min.X) * 0.5)
	hy = float32((b.Max.Y - b.Min.Y) * 0.5)
	hz = float32((b.Max.Z - b.Min.Z) * 0.5)
	return hx, hy, hz, center
}

func boxCornerPoints(b scene.Box, halfY float32, localCenter vec.V) []vec.V {
	out := make([]vec.V, 0, 8)
	for _, dx := range [2]float64{0, 1} {
		for _, dy := range [2]float64{0, 1} {
			for _, dz := range [2]float64{0, 1} {
				y := localCenter.Y + (2*dy-1)*float64(halfY)
				out = append(out, vec.V{
					X: b.Min.X + dx*(b.Max.X-b.Min.X),
					Y: y,
					Z: b.Min.Z + dz*(b.Max.Z-b.Min.Z),
				})
			}
		}
	}
	return out
}

func cylinderHullPoints(c scene.Cylinder, segments int) []vec.V {
	if segments < 3 {
		segments = 3
	}
	r := c.MaxRadius()
	var out []vec.V
	for i := 0; i < segments; i++ {
		a := 2 * math.Pi * float64(i) / float64(segments)
		dx, dz := math.Cos(a)*r, math.Sin(a)*r
		out = append(out,
			vec.New(c.CX+dx, c.YMin, c.CZ+dz),
			vec.New(c.CX+dx, c.YMax, c.CZ+dz),
		)
	}
	return out
}

func toJoltVec(v vec.V) jolt.Vec3 {
	return jolt.Vec3{X: float32(v.X), Y: float32(v.Y), Z: float32(v.Z)}
}

func capsuleOffsets(cfg collisionConfig) (halfHeight, radius float32) {
	radius = float32(cfg.Radius)
	stand := float32(cfg.EyeHeight)
	halfHeight = (stand - 2*radius) * 0.5
	if halfHeight < 0.05 {
		halfHeight = 0.05
	}
	return halfHeight, radius
}

func eyeFromCharacter(pos jolt.Vec3, cfg collisionConfig) vec.V {
	halfH, r := capsuleOffsets(cfg)
	feetY := float64(pos.Y) - float64(halfH+r)
	return vec.V{
		X: float64(pos.X),
		Y: feetY + cfg.EyeHeight,
		Z: float64(pos.Z),
	}
}

func characterFromEye(eye vec.V, cfg collisionConfig) jolt.Vec3 {
	halfH, r := capsuleOffsets(cfg)
	feetY := eye.Y - cfg.EyeHeight
	return jolt.Vec3{
		X: float32(eye.X),
		Y: float32(feetY + float64(halfH+r)),
		Z: float32(eye.Z),
	}
}

func clampMag(x, z, maxMag float64) (float64, float64) {
	m := math.Hypot(x, z)
	if m > maxMag && m > 0 {
		x *= maxMag / m
		z *= maxMag / m
	}
	return x, z
}
