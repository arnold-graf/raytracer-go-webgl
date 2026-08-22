package joltphys

import (
	"math"

	"github.com/bbitechnologies/jolt-go/jolt"
)

// characterMaxStrength is the max force (N) CharacterVirtual may apply to other
// bodies during contact resolution.
const characterMaxStrength = 450

// walkPushScale scales walk intent into contact impulses on dynamic props.
const walkPushScale = 0.02

// characterMassKg is used by CharacterVirtual when resolving contacts.
const characterMassKg = 80

// applyWalkPush transfers horizontal walk intent into impulses on dynamic props
// the capsule is touching. CharacterVirtual alone often cannot overcome static
// friction on heavy furniture even with higher MaxStrength.
func (w *World) applyWalkPush(walkVel jolt.Vec3, dt float32) {
	if w == nil || w.character == nil || w.bi == nil {
		return
	}
	mag := math.Hypot(float64(walkVel.X), float64(walkVel.Z))
	if mag < 0.05 {
		return
	}
	dirX := walkVel.X / float32(mag)
	dirZ := walkVel.Z / float32(mag)
	impulseMag := characterMaxStrength * dt * walkPushScale
	if impulseMag <= 0 {
		return
	}
	impulse := jolt.Vec3{X: dirX * impulseMag, Z: dirZ * impulseMag}
	contacts := w.character.GetActiveContacts(16)
	for _, c := range contacts {
		if !c.HadCollision || c.BodyB == nil || c.IsSensorB {
			continue
		}
		w.bi.ActivateBody(c.BodyB)
		w.bi.ApplyImpulse(c.BodyB, c.Position, impulse)
	}
}
