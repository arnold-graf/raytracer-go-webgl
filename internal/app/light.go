package app

func handleLightToggle(ctx *UseContext) error {
	if ctx.Game.interactLights != nil {
		ctx.Game.interactLights.ToggleInteract(ctx.Interact)
	}
	return nil
}
