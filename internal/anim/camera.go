package anim

import (
	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// PresentToCamera builds a transform that holds an object in front of the camera,
// facing the viewer. distance is meters along the view axis; up defaults to camera up.
func PresentToCamera(cam *camera.Camera, distance float64) *scene.Transform {
	fwd, _, up := cam.Basis()
	center := cam.Pos.Add(fwd.Scale(distance))
	toCam := cam.Pos.Sub(center).Normalize()
	return scene.NewTransformYZ(center, up, toCam)
}

// PresentToCameraAt is like PresentToCamera but uses an explicit up vector.
func PresentToCameraAt(cam *camera.Camera, distance float64, up vec.V) *scene.Transform {
	fwd, _, camUp := cam.Basis()
	if up == (vec.V{}) {
		up = camUp
	}
	center := cam.Pos.Add(fwd.Scale(distance))
	toCam := cam.Pos.Sub(center).Normalize()
	return scene.NewTransformYZ(center, up, toCam)
}
