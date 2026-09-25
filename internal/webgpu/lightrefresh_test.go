package webgpu

import (
	"testing"

	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// lampScene has a toggleable lamp (a dynamic light, as interactlight registers
// it), a static lamp, and a door panel that animates on its own.
func lampScene() *scene.Scene {
	s := &scene.Scene{
		Lights: []scene.Light{
			// No range: the reach follows brightness, so fades change it.
			{Pos: vec.V{X: 0, Y: 2, Z: 0}, Color: vec.V{X: 4, Y: 4, Z: 4}},
			{Pos: vec.V{X: 6, Y: 2, Z: 0}, Color: vec.V{X: 4, Y: 4, Z: 4}, Range: 8},
		},
		Boxes: []scene.Box{
			{Min: vec.V{X: 2, Y: 0, Z: 2}, Max: vec.V{X: 3, Y: 2, Z: 2.1}},
		},
	}
	s.DynamicBodies = []scene.DynamicBody{
		{Lights: [2]int{0, 1}},
		{Boxes: [2]int{0, 1}},
	}
	return s
}

func freshCache(t *testing.T, s *scene.Scene) *sceneCache {
	t.Helper()
	c := &sceneCache{}
	c.rebuild(&render.View{Scene: s})
	if !c.valid {
		t.Fatal("cache did not build")
	}
	return c
}

func TestDoorMoveLeavesLightsAlone(t *testing.T) {
	s := lampScene()
	c := freshCache(t, s)
	grid := c.lightGrid

	s.Boxes[0].Min.X += 0.5
	s.Boxes[0].Max.X += 0.5
	s.TouchTransforms()
	c.updateDynamicTransforms(s)

	if c.lightsDirty || c.gridDirty {
		t.Fatalf("door move flagged lights=%v grid=%v, want neither", c.lightsDirty, c.gridDirty)
	}
	if len(c.lightGrid.Indices) != len(grid.Indices) {
		t.Fatal("door move rebuilt the light grid")
	}
}

func TestLampFadeKeepsGrid(t *testing.T) {
	s := lampScene()
	c := freshCache(t, s)

	s.Lights[0].Color = s.Lights[0].Color.Scale(0.3)
	s.TouchTransforms()
	c.updateDynamicTransforms(s)

	if !c.lightsDirty {
		t.Fatal("dimmed lamp was not re-uploaded")
	}
	if c.lights[0].Falloff[0] >= c.gridBasis[0].Falloff[0] {
		t.Fatal("dimming did not shrink the lamp's reach; the test proves nothing")
	}
	if c.gridDirty {
		t.Fatal("dimming a lamp rebuilt the grid; the old, larger reach still covers it")
	}
	if got, want := c.lights[0].Color[0], PackLights(s)[0].Color[0]; got != want {
		t.Fatalf("lamp colour %v, want %v", got, want)
	}
}

func TestLampGrowOrMoveRebuildsGrid(t *testing.T) {
	for name, change := range map[string]func(*scene.Light){
		"brighter": func(l *scene.Light) { l.Color = l.Color.Scale(4) },
		"moved":    func(l *scene.Light) { l.Pos.X += 1 },
	} {
		t.Run(name, func(t *testing.T) {
			s := lampScene()
			c := freshCache(t, s)
			change(&s.Lights[0])
			s.TouchTransforms()
			c.updateDynamicTransforms(s)
			if !c.lightsDirty || !c.gridDirty {
				t.Fatalf("lights=%v grid=%v, want both", c.lightsDirty, c.gridDirty)
			}
			if !gridCovers(c.gridBasis, c.lights) {
				t.Fatal("rebuilt grid does not cover the new lights")
			}
		})
	}
}
