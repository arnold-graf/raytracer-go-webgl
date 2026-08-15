package app

func handleState(ctx *UseContext) error {
	if ctx.Game.state == nil || ctx.Interact == nil {
		return nil
	}
	if err := ctx.Game.state.HandleInteract(ctx.Game.sc, ctx.Interact); err != nil {
		return err
	}
	if ctx.Game.state.StructChanged() && ctx.Game.interactLights != nil {
		skip := func(i int) bool {
			return ctx.Game.state != nil && ctx.Game.state.IsStateLight(i)
		}
		ctx.Game.interactLights.Rebind(ctx.Game.sc, skip)
	}
	return nil
}
