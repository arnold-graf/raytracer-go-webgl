package character

import (
	"fmt"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// SpawnedBody records which scene primitive indices belong to one character.
type SpawnedBody struct {
	Boxes     [2]int
	Cylinders [2]int
	Spheres   [2]int
	// attachmentBone[i] is the bone name for attachment i (same order as rig).
	AttachmentBones []string
}

// SpawnAttachments appends rig attachment primitives to sc and returns index
// ranges. Primitives are authored in bone-local space; call ApplyPose to set
// world transforms.
func SpawnAttachments(rig *Rig, sc *scene.Scene) (SpawnedBody, error) {
	var body SpawnedBody
	body.Boxes[0] = len(sc.Boxes)
	body.Cylinders[0] = len(sc.Cylinders)
	body.Spheres[0] = len(sc.Spheres)

	for _, att := range rig.Attachments {
		surf := scene.Surface{
			Mat:    scene.MatDiffuse,
			Albedo: att.Albedo,
			IOR:    1.5,
		}
		switch att.Kind {
		case "box":
			half := att.Size.Scale(0.5)
			min := att.Offset.Sub(half)
			max := att.Offset.Add(half)
			sc.Boxes = append(sc.Boxes, scene.Box{Min: min, Max: max, Surface: surf})
		case "cylinder":
			length := att.Length
			if length <= 0 {
				length = rig.Bones[att.Bone].Length
			}
			radius := att.Radius
			if radius <= 0 {
				radius = 0.05
			}
			y0 := att.Offset.Y
			sc.Cylinders = append(sc.Cylinders, scene.Cylinder{
				CX: att.Offset.X, CZ: att.Offset.Z,
				Radius: radius, YMin: y0, YMax: y0 + length,
				Surface: surf,
			})
		case "sphere":
			radius := att.Radius
			if radius <= 0 {
				radius = 0.1
			}
			sc.Spheres = append(sc.Spheres, scene.Sphere{
				Center: att.Offset, Radius: radius, Surface: surf,
			})
		default:
			return body, fmt.Errorf("unknown attachment kind %q", att.Kind)
		}
		body.AttachmentBones = append(body.AttachmentBones, att.Bone)
	}

	body.Boxes[1] = len(sc.Boxes)
	body.Cylinders[1] = len(sc.Cylinders)
	body.Spheres[1] = len(sc.Spheres)
	return body, nil
}

// ApplyPose writes world transforms for all attachment primitives in body.
func ApplyPose(rig *Rig, sc *scene.Scene, body SpawnedBody, pose SkeletonPose) {
	boxIdx := body.Boxes[0]
	cylIdx := body.Cylinders[0]
	sphIdx := body.Spheres[0]

	for i, boneName := range body.AttachmentBones {
		att := rig.Attachments[i]
		xf := pose.Bones[boneName]
		if xf == nil {
			continue
		}
		// Attachment geometry is already offset in local bone space; only bone
		// rotation + translation is needed at the primitive root.
		world := xf
		switch att.Kind {
		case "box":
			if boxIdx < body.Boxes[1] {
				sc.Boxes[boxIdx].Xform = world
				boxIdx++
			}
		case "cylinder":
			if cylIdx < body.Cylinders[1] {
				sc.Cylinders[cylIdx].Xform = world
				cylIdx++
			}
		case "sphere":
			if sphIdx < body.Spheres[1] {
				sc.Spheres[sphIdx].Xform = world
				sphIdx++
			}
		}
	}
}

// HipPositionFromGround returns hips world position given feet XZ and ground Y.
func HipPositionFromGround(x, groundY, z, hipHeight float64) vec.V {
	return vec.V{X: x, Y: groundY + hipHeight, Z: z}
}
