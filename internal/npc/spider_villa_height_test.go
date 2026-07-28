package npc

import (
	"path/filepath"
	"strings"
	"testing"

	"raytracer/internal/character"
	"raytracer/internal/sceneio"
)

func TestSpiderVillaBodyClearsTerrain(t *testing.T) {
	sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", "outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	world := FootWorld(sc)
	if err := m.Instantiate(sc, world); err != nil {
		t.Fatal(err)
	}
	a := &m.agents[0]
	for i := 0; i < 180; i++ {
		m.Update(sc, world, 1.0/60.0)
		hip := a.SpiderBody().Body.Pos
		headY := hip.Y + a.Rig.HipHeight + 0.5
		gy := world.GroundHeight(hip.X, hip.Z, headY)
		clearance := hip.Y - gy
		t.Logf("frame %d hip Y=%.2f ground=%.2f clearance=%.2f z=%.1f", i+1, hip.Y, gy, clearance, hip.Z)
		if clearance < a.Rig.HipHeight*0.85 {
			t.Fatalf("frame %d: hip clearance %.2f < %.2f (hip=%.2f ground=%.2f)", i+1, clearance, a.Rig.HipHeight*0.85, hip.Y, gy)
		}
		if i+1 == 180 {
			cylIdx := a.Body.Cylinders[0]
			for ai, bone := range a.Body.AttachmentBones {
				if a.Rig.Attachments[ai].Kind != "cylinder" || !strings.HasSuffix(bone, "_tibia") {
					if a.Rig.Attachments[ai].Kind == "cylinder" {
						cylIdx++
					}
					continue
				}
				cyl := sc.Cylinders[cylIdx]
				cylIdx++
				omin, omax := cyl.WorldBounds()
				tipX := (omin.X + omax.X) * 0.5
				tipZ := (omin.Z + omax.Z) * 0.5
				fgy := world.GroundHeight(tipX, tipZ, headY)
				if omin.Y < fgy-0.2 {
					t.Fatalf("frame %d %s tip Y=%.2f below ground %.2f", i+1, bone, omin.Y, fgy)
				}
			}
		}
		for j, f := range a.SpiderBody().Feet {
			if !f.Initialized || f.Phase == character.FootSwing {
				continue
			}
			fgy := world.GroundHeight(f.PlantWorld.X, f.PlantWorld.Z, headY)
			if f.PlantWorld.Y < fgy-0.35 {
				t.Fatalf("frame %d foot %d plant Y=%.2f below ground %.2f", i+1, j, f.PlantWorld.Y, fgy)
			}
		}
	}
}
