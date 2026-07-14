package app

func handleScreen(ctx *UseContext) error {
	if ctx.Game == nil || ctx.Game.screens == nil {
		return nil
	}
	if ctx.Game.screens.Viewing() {
		ctx.Game.screens.Dismiss(ctx.Game.sc, ctx.Game.cam)
		return nil
	}
	aspect := float64(ctx.Game.rw) / float64(ctx.Game.rh)
	opened, onUse := ctx.Game.screens.ToggleInteract(ctx.Game.sc, ctx.Game.cam, aspect, ctx.Interact)
	if opened && invokeHandler != nil {
		return invokeHandler(onUse, ctx)
	}
	return nil
}
