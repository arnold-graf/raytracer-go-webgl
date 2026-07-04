package app

import (
	"fmt"

	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func applyPlayerAtPoint(cam *camera.Camera, p scene.Point) {
	if p.UseFloor {
		cam.SetPose(camera.Pose{
			Pos:   vec.New(p.Pos.X, 0, p.Pos.Z),
			Yaw:   p.Yaw,
			Pitch: p.Pitch,
		})
		cam.PlaceOnFloor(p.FloorY)
		return
	}
	cam.SetPose(camera.Pose{Pos: p.Pos, Yaw: p.Yaw, Pitch: p.Pitch})
	cam.Land()
}

func spawnPlayerAt(cam *camera.Camera, sc *scene.Scene, id string) error {
	p, ok := sc.PointByID(id)
	if !ok {
		return fmt.Errorf("point %q not found", id)
	}
	applyPlayerAtPoint(cam, p)
	return nil
}
