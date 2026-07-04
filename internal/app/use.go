package app

import (
	"raytracer/internal/camera"
	"raytracer/internal/render"
	"raytracer/internal/scene"
)

// UseContext is passed to interact handlers when the player activates an object.
type UseContext struct {
	Game     *Game
	Camera   *camera.Camera
	Renderer render.Renderer
	Interact *scene.Interactable
}

// UseHandler runs when the player activates an interactable.
type UseHandler func(*UseContext) error

// UseHandlers maps TOML on_use ids to handler implementations.
var UseHandlers = map[string]UseHandler{
	"exit_portal": handleExitPortal,
	"door":        handleDoor,
}

// ApplyCapturesToScene marks the scene dirty so the GPU re-uploads capture textures.
func ApplyCapturesToScene(sc *scene.Scene) {
	if sc != nil {
		sc.Touch()
	}
}
