package app

import (
	"fmt"

	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func applySpawnpoint(cam *camera.Camera, sp scene.PlayerSpawnpoint) {
	if sp.UseFloor {
		cam.SetPose(camera.Pose{
			Pos:   vec.New(sp.Pos.X, 0, sp.Pos.Z),
			Yaw:   sp.Yaw,
			Pitch: sp.Pitch,
		})
		cam.PlaceOnFloor(sp.FloorY)
		return
	}
	cam.SetPose(camera.Pose{Pos: sp.Pos, Yaw: sp.Yaw, Pitch: sp.Pitch})
	cam.Land()
}

func spawnPlayerAt(cam *camera.Camera, sc *scene.Scene, id string) error {
	sp, ok := sc.Spawnpoint(id)
	if !ok {
		return fmt.Errorf("player_spawnpoint %q not found", id)
	}
	applySpawnpoint(cam, sp)
	return nil
}
