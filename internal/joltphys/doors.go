package joltphys

import (
	"strings"

	"github.com/bbitechnologies/jolt-go/jolt"

	"raytracer/internal/door"
	"raytracer/internal/scene"
)

func (w *World) buildDoorBodies(sc *scene.Scene) {
	if sc == nil {
		return
	}
	for _, db := range sc.DynamicBodies {
		if !strings.HasPrefix(db.Name, "door_") || !db.Attached(sc) {
			continue
		}
		origin := bodyOriginForGroup(sc, db)
		parts, childShapes := compoundPartsForBody(sc, db, origin)
		if len(parts) == 0 {
			continue
		}
		compound := jolt.CreateStaticCompound(parts)
		w.addShape(compound)
		pos, rot := joltPoseFromTransform(origin)
		body := w.bi.CreateBodyEx(compound, pos, rot, jolt.MotionTypeKinematic, false,
			0, 0.5, 0, false)
		if body == nil {
			continue
		}
		w.bodies = append(w.bodies, ownedBody{body: body})
		for _, s := range childShapes {
			w.shapes = append(w.shapes, ownedShape{shape: s})
		}
		b := w.newBinding(db.Name, db, body, origin, sc, true)
		b.isDoor = true
		w.bindings.bindings = append(w.bindings.bindings, b)
		w.bi.ActivateBody(body)
	}
}

// SyncKinematicDoors updates door panel bodies from scene transforms and ghost state.
func (w *World) SyncKinematicDoors(sc *scene.Scene, doors *door.Manager) {
	if w == nil || sc == nil || doors == nil {
		return
	}
	for i := range w.bindings.bindings {
		b := &w.bindings.bindings[i]
		if !b.isDoor || b.body == nil {
			continue
		}
		origin := bodyOriginForGroup(sc, bodyFromBinding(sc, b))
		pos, rot := joltPoseFromTransform(origin)
		w.bi.SetPositionAndRotation(b.body, pos, rot, false)
		solid := doors.PanelBlocks(strings.TrimPrefix(b.name, "door_"))
		w.bi.SetSensor(b.body, !solid)
	}
}

func bodyFromBinding(sc *scene.Scene, b *simBinding) scene.DynamicBody {
	for _, db := range sc.DynamicBodies {
		if db.Name == b.name {
			return db
		}
	}
	return scene.DynamicBody{Name: b.name}
}
