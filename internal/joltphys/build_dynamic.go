package joltphys

import (
	"math"

	"github.com/bbitechnologies/jolt-go/jolt"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func (w *World) buildDynamicFromScene(sc *scene.Scene) {
	if sc == nil {
		return
	}
	for _, g := range sc.PhysicsGroups {
		if !g.Body.Attached(sc) {
			continue
		}
		mode := g.Spec.Mode
		if mode == scene.PhysicsDynamic {
			mode = scene.PhysicsCompound
		}
		switch mode {
		case scene.PhysicsCompound:
			w.spawnDynamicGroup(sc, g)
		case scene.PhysicsKinematic:
			w.spawnKinematicGroup(sc, g)
		}
	}
}

func (w *World) spawnDynamicGroup(sc *scene.Scene, g scene.PhysicsGroup) {
	origin := bodyOriginForGroup(sc, g.Body)
	parts, childShapes := compoundPartsForBody(sc, g.Body, origin)
	if len(parts) == 0 {
		return
	}
	compound := jolt.CreateStaticCompound(parts)
	w.addShape(compound)
	pos, rot := joltPoseFromTransform(origin)
	mass := estimateMassKg(sc, g.Body, g.Spec.MassKg)
	body := w.bi.CreateBodyEx(compound, pos, rot, jolt.MotionTypeDynamic, false,
		float32(mass), float32(g.Spec.Friction), float32(g.Spec.Restitution), g.Spec.Sleep)
	if body == nil {
		return
	}
	w.bodies = append(w.bodies, ownedBody{body: body})
	w.bindings.bindings = append(w.bindings.bindings, w.newBinding(g.Name, g.Body, body, origin, sc, false))
	for _, s := range childShapes {
		w.shapes = append(w.shapes, ownedShape{shape: s})
	}
	w.bi.ActivateBody(body)
}

func (w *World) spawnKinematicGroup(sc *scene.Scene, g scene.PhysicsGroup) {
	origin := bodyOriginForGroup(sc, g.Body)
	parts, childShapes := compoundPartsForBody(sc, g.Body, origin)
	if len(parts) == 0 {
		return
	}
	compound := jolt.CreateStaticCompound(parts)
	w.addShape(compound)
	pos, rot := joltPoseFromTransform(origin)
	body := w.bi.CreateBodyEx(compound, pos, rot, jolt.MotionTypeKinematic, false,
		0, float32(g.Spec.Friction), float32(g.Spec.Restitution), false)
	if body == nil {
		return
	}
	w.bodies = append(w.bodies, ownedBody{body: body})
	w.bindings.bindings = append(w.bindings.bindings, w.newBinding(g.Name, g.Body, body, origin, sc, true))
	for _, s := range childShapes {
		w.shapes = append(w.shapes, ownedShape{shape: s})
	}
	w.bi.ActivateBody(body)
}

func (w *World) newBinding(name string, db scene.DynamicBody, body *jolt.BodyID, rest *scene.Transform, sc *scene.Scene, kinematic bool) simBinding {
	restInv := rest.Inverse()
	var prims []primBinding
	for i := db.Boxes[0]; i < db.Boxes[1]; i++ {
		prims = append(prims, primBinding{
			kind:  0,
			index: i,
			rel:   restInv.Compose(copyTransform(sc.Boxes[i].Xform)),
		})
	}
	for i := db.Spheres[0]; i < db.Spheres[1]; i++ {
		prims = append(prims, primBinding{
			kind:  1,
			index: i,
			rel:   restInv.Compose(copyTransform(sc.Spheres[i].Xform)),
		})
	}
	for i := db.Cylinders[0]; i < db.Cylinders[1]; i++ {
		prims = append(prims, primBinding{
			kind:  2,
			index: i,
			rel:   restInv.Compose(copyTransform(sc.Cylinders[i].Xform)),
		})
	}
	for i := db.Lights[0]; i < db.Lights[1]; i++ {
		prims = append(prims, lightPrimBinding(restInv, sc.Lights[i], i))
	}
	return simBinding{
		body:      body,
		name:      name,
		rest:      copyTransform(rest),
		prims:     prims,
		kinematic: kinematic,
	}
}

func copyTransform(x *scene.Transform) *scene.Transform {
	if x == nil {
		return nil
	}
	cpy := *x
	return &cpy
}

func lightPrimBinding(restInv *scene.Transform, l scene.Light, index int) primBinding {
	rel := restInv.Compose(scene.Translation(l.Pos))
	var localDir vec.V
	if l.IsSpot() {
		localDir = restInv.RotateDir(l.Dir)
	}
	return primBinding{kind: 3, index: index, rel: rel, dir: localDir}
}

func bodyOriginForGroup(sc *scene.Scene, db scene.DynamicBody) *scene.Transform {
	var sum vec.V
	n := 0
	for i := db.Boxes[0]; i < db.Boxes[1]; i++ {
		mn, mx := sc.Boxes[i].WorldBounds()
		sum = sum.Add(vec.New((mn.X+mx.X)*0.5, (mn.Y+mx.Y)*0.5, (mn.Z+mx.Z)*0.5))
		n++
	}
	for i := db.Spheres[0]; i < db.Spheres[1]; i++ {
		c := sc.Spheres[i].Center
		if sc.Spheres[i].Xform != nil {
			c = sc.Spheres[i].Xform.ToWorld(c)
		}
		sum = sum.Add(c)
		n++
	}
	for i := db.Cylinders[0]; i < db.Cylinders[1]; i++ {
		c := vec.New(sc.Cylinders[i].CX, (sc.Cylinders[i].YMin+sc.Cylinders[i].YMax)*0.5, sc.Cylinders[i].CZ)
		if sc.Cylinders[i].Xform != nil {
			c = sc.Cylinders[i].Xform.ToWorld(c)
		}
		sum = sum.Add(c)
		n++
	}
	if n == 0 {
		return scene.NewRigidTransform(0, 0, 0, vec.V{})
	}
	return scene.NewRigidTransform(0, 0, 0, sum.Scale(1/float64(n)))
}

func compoundPartsForBody(sc *scene.Scene, db scene.DynamicBody, origin *scene.Transform) ([]jolt.CompoundPart, []*jolt.Shape) {
	inv := origin.Inverse()
	var parts []jolt.CompoundPart
	var shapes []*jolt.Shape
	for i := db.Boxes[0]; i < db.Boxes[1]; i++ {
		p, s := boxCompoundPart(sc.Boxes[i], inv)
		if p.Shape != nil {
			parts = append(parts, p)
			shapes = append(shapes, s)
		}
	}
	for i := db.Spheres[0]; i < db.Spheres[1]; i++ {
		p, s := sphereCompoundPart(sc.Spheres[i], inv)
		if p.Shape != nil {
			parts = append(parts, p)
			shapes = append(shapes, s)
		}
	}
	for i := db.Cylinders[0]; i < db.Cylinders[1]; i++ {
		p, s := cylinderCompoundPart(sc.Cylinders[i], inv)
		if p.Shape != nil {
			parts = append(parts, p)
			shapes = append(shapes, s)
		}
	}
	return parts, shapes
}

func boxCompoundPart(b scene.Box, bodyInv *scene.Transform) (jolt.CompoundPart, *jolt.Shape) {
	if !b.Collides() {
		return jolt.CompoundPart{}, nil
	}
	hx, hy, hz, localCenter := boxHalfExtents(b)
	collHy := inflateHalfY(hy)
	if hx <= 0 || collHy <= 0 || hz <= 0 {
		return jolt.CompoundPart{}, nil
	}
	if hy < minColliderHalfY {
		localCenter.Y = b.Min.Y + float64(collHy)
	}
	shape := jolt.CreateBox(jolt.Vec3{X: hx, Y: collHy, Z: hz})
	if shape == nil {
		return jolt.CompoundPart{}, nil
	}
	pose := scene.Translation(localCenter)
	if b.Xform != nil {
		pose = b.Xform.Compose(pose)
	}
	rel := bodyInv.Compose(pose)
	pos, rot := joltPoseFromTransform(rel)
	return jolt.CompoundPart{Shape: shape, LocalPos: pos, LocalRot: rot}, shape
}

func sphereCompoundPart(sp scene.Sphere, bodyInv *scene.Transform) (jolt.CompoundPart, *jolt.Shape) {
	if !sp.Collides() || sp.Radius <= 0 {
		return jolt.CompoundPart{}, nil
	}
	r := float32(sp.Radius)
	shape := jolt.CreateSphere(r)
	pose := scene.Translation(sp.Center)
	if sp.Xform != nil {
		pose = sp.Xform.Compose(pose)
	}
	rel := bodyInv.Compose(pose)
	pos, rot := joltPoseFromTransform(rel)
	return jolt.CompoundPart{Shape: shape, LocalPos: pos, LocalRot: rot}, shape
}

func cylinderCompoundPart(c scene.Cylinder, bodyInv *scene.Transform) (jolt.CompoundPart, *jolt.Shape) {
	if !c.Collides() {
		return jolt.CompoundPart{}, nil
	}
	h := c.YMax - c.YMin
	if h <= 0 {
		return jolt.CompoundPart{}, nil
	}
	r := float32(c.MaxRadius())
	if r <= 0 {
		return jolt.CompoundPart{}, nil
	}
	halfH := float32(h*0.5 - float64(r))
	if halfH < 0 {
		halfH = 0
	}
	shape := jolt.CreateCapsule(halfH, r)
	localCenter := vec.New(c.CX, (c.YMin+c.YMax)*0.5, c.CZ)
	pose := scene.Translation(localCenter)
	if c.Xform != nil {
		pose = c.Xform.Compose(pose)
	}
	rel := bodyInv.Compose(pose)
	pos, rot := joltPoseFromTransform(rel)
	return jolt.CompoundPart{Shape: shape, LocalPos: pos, LocalRot: rot}, shape
}

func estimateMassKg(sc *scene.Scene, db scene.DynamicBody, authored float64) float64 {
	if authored > 0 {
		return authored
	}
	vol := primitiveVolume(sc, db)
	if vol <= 0 {
		return 1.0
	}
	return vol * scene.DefaultPropDensity
}

func primitiveVolume(sc *scene.Scene, db scene.DynamicBody) float64 {
	var v float64
	for i := db.Boxes[0]; i < db.Boxes[1]; i++ {
		b := sc.Boxes[i]
		v += (b.Max.X - b.Min.X) * (b.Max.Y - b.Min.Y) * (b.Max.Z - b.Min.Z)
	}
	for i := db.Spheres[0]; i < db.Spheres[1]; i++ {
		r := sc.Spheres[i].Radius
		v += 4 / 3 * math.Pi * r * r * r
	}
	for i := db.Cylinders[0]; i < db.Cylinders[1]; i++ {
		c := sc.Cylinders[i]
		r := c.MaxRadius()
		h := c.YMax - c.YMin
		v += math.Pi * r * r * h
	}
	return v
}
