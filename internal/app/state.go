package app

import "slices"

func handleState(ctx *UseContext) error {
	if ctx.Game.state == nil || ctx.Interact == nil {
		return nil
	}
	ambiences := slices.Clone(ctx.Game.sc.Ambiences)
	if err := ctx.Game.state.HandleInteract(ctx.Game.sc, ctx.Interact); err != nil {
		return err
	}
	// A [[sound]] under a state condition (a campfire's crackle behind is_on)
	// comes and goes with it, so the audio engine has to hear about it.
	if !slices.Equal(ambiences, ctx.Game.sc.Ambiences) {
		ctx.Game.setupAmbience()
	}
	if ctx.Game.state.StructChanged() && ctx.Game.interactLights != nil {
		skip := func(i int) bool {
			return ctx.Game.state != nil && ctx.Game.state.IsStateLight(i)
		}
		ctx.Game.interactLights.Rebind(ctx.Game.sc, skip)
	}
	return nil
}
