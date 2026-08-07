package app

import "raytracer/internal/door"

func (g *Game) wireDoorSounds() {
	if g == nil || g.doors == nil {
		return
	}
	g.doors.SetAnimateHook(g.onDoorAnimate)
}

func (g *Game) onDoorAnimate(ev door.AnimateEvent) {
	if g.snd == nil || ev.Kind != "sliding" {
		return
	}
	_, right, _ := g.cam.Basis()
	g.snd.PlaySlideDoor(ev.Opening, ev.TravelTime, ev.Center, g.cam.Pos, right, 0.55)
}
