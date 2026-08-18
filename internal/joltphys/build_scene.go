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
	w.bodies = append(w.bodies, ownedBody{body: body})
}

func (w *World) addShape(shape *jolt.Shape) {
	w.shapes = append(w.shapes, ownedShape{shape: shape})
}

func (w *World) buildFromScene(sc *scene.Scene) error {
	if sc == nil {
		return nil
	}
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
				w.addBoxCollider(part)
			}
			continue
		}
		w.addBoxCollider(*b)
	}
	for i := range sc.Cylinders {
		if sc.IsDynamicCylinder(i) {
			continue
		}
		c := &sc.Cylinders[i]
		if !c.Collides() {
			continue
		}
		w.addCylinderCollider(*c)
	}
	for i := range sc.Terrains {
		w.addTerrainCollider(&sc.Terrains[i])
	}
	return nil
}

func (w *World) addBoxCollider(b scene.Box) {
	hx, hy, hz, localCenter := boxHalfExtents(b)
	collHy := inflateHalfY(hy)
	if hx <= 0 || collHy <= 0 || hz <= 0 {
		return
	}

	if b.Xform == nil {
		center := localCenter
		if hy < minColliderHalfY {
			// Keep the bottom of thin floors (e.g. 0.01 slabs) at Min.Y.
			center.Y = b.Min.Y + float64(collHy)
		}
		shape := jolt.CreateBox(jolt.Vec3{X: hx, Y: collHy, Z: hz})
		w.addShape(shape)
		w.addStaticBody(shape, toJoltVec(center))
		return
	}

	worldCenter := b.Xform.ToWorld(localCenter)
	pts := boxCornerPoints(b, collHy, localCenter)
	relative := make([]jolt.Vec3, len(pts))
	for i, p := range pts {
		wp := b.Xform.ToWorld(p)
		relative[i] = jolt.Vec3{
			X: float32(wp.X - worldCenter.X),
			Y: float32(wp.Y - worldCenter.Y),
			Z: float32(wp.Z - worldCenter.Z),
		}
	}
	shape := jolt.CreateConvexHull(relative)
	w.addShape(shape)
	w.addStaticBody(shape, toJoltVec(worldCenter))
}

func (w *World) addCylinderCollider(c scene.Cylinder) {
	h := c.YMax - c.YMin
	if h <= 0 {
		return
	}
	r := float32(c.MaxRadius())
	if r <= 0 {
		return
	}
	halfH := float32(h*0.5 - float64(r))
	if halfH < 0 {
		halfH = 0
	}
	localCenter := vec.New(c.CX, (c.YMin+c.YMax)*0.5, c.CZ)
	if c.Xform == nil {
		shape := jolt.CreateCapsule(halfH, r)
		w.addShape(shape)
		w.addStaticBody(shape, jolt.Vec3{
			X: float32(c.CX),
			Y: float32((c.YMin + c.YMax) * 0.5),
			Z: float32(c.CZ),
		})
		return
	}
	worldCenter := c.Xform.ToWorld(localCenter)
	pts := cylinderHullPoints(c, 8)
	relative := make([]jolt.Vec3, len(pts))
	for i, p := range pts {
		wp := c.Xform.ToWorld(p)
		relative[i] = jolt.Vec3{
			X: float32(wp.X - worldCenter.X),
			Y: float32(wp.Y - worldCenter.Y),
			Z: float32(wp.Z - worldCenter.Z),
		}
	}
	shape := jolt.CreateConvexHull(relative)
	w.addShape(shape)
	w.addStaticBody(shape, toJoltVec(worldCenter))
}

func (w *World) addTerrainCollider(t *scene.Terrain) {
	if t == nil {
		return
	}
	t.Prepare()
	gnx, gnz := t.GridDimensions()
	if gnx < 2 || gnz < 2 {
		return
	}
	snap := t.CacheSnapshot()
	if len(snap.Height) == 0 {
		return
	}
	ox, oz := t.OriginX, t.OriginZ
	sx, sz := t.SizeX, t.SizeZ
	gnx, gnz = snap.GNX, snap.GNZ
	dx := sx / float64(gnx-1)
	dz := sz / float64(gnz-1)
	verts := make([]jolt.Vec3, gnx*gnz)
	for j := 0; j < gnz; j++ {
		for i := 0; i < gnx; i++ {
			idx := j*gnx + i
			verts[idx] = jolt.Vec3{
				X: float32(ox + float64(i)*dx),
				Y: float32(snap.Height[idx]),
				Z: float32(oz + float64(j)*dz),
			}
		}
	}
	var indices []int32
	for j := 0; j < gnz-1; j++ {
		for i := 0; i < gnx-1; i++ {
			a := int32(j*gnx + i)
			b := int32(j*gnx + i + 1)
			c := int32((j+1)*gnx + i)
			d := int32((j+1)*gnx + i + 1)
			indices = append(indices, a, c, b, b, c, d)
		}
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
