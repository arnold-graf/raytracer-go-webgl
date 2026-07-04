package app

func handleDoor(ctx *UseContext) error {
	if ctx.Game == nil || ctx.Game.doors == nil || ctx.Interact == nil {
		return nil
	}
	ctx.Game.doors.ToggleInteract(ctx.Interact, ctx.Camera.Pos)
	return nil
}
