package app

func handleDocument(ctx *UseContext) error {
	if ctx.Game == nil || ctx.Game.documents == nil {
		return nil
	}
	if ctx.Game.documents.Reading() {
		ctx.Game.documents.Dismiss(ctx.Game.sc)
		return nil
	}
	opened, onUse := ctx.Game.documents.ToggleInteract(ctx.Game.sc, ctx.Interact)
	if opened && invokeHandler != nil {
		return invokeHandler(onUse, ctx)
	}
	return nil
}
