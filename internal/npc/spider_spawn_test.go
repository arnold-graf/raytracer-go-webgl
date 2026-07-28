package npc

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func TestSpiderPreviewTibiaSpread(t *testing.T) {
	sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", "preview", "spider.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	world := FootWorld(sc)
	if err := m.Instantiate(sc, world); err != nil {
		t.Fatal(err)
	}
	if len(m.agents) != 1 {
		t.Fatalf("agents = %d", len(m.agents))
	}
	a := &m.agents[0]
	seen := make(map[string]int)
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
		tip := vec.V{X: (omin.X + omax.X) * 0.5, Z: (omin.Z + omax.Z) * 0.5}
		key := fmt.Sprintf("%.2f,%.2f", tip.X, tip.Z)
		seen[key]++
		t.Logf("%s tipXZ=%v xformNil=%v", bone, tip, cyl.Xform == nil)
	}
	if len(seen) < 6 {
		t.Fatalf("tibia tips collapsed to %d unique XZ: %v", len(seen), seen)
	}
}

func TestSpiderVillaTibiaSpreadAfterWalk(t *testing.T) {
	sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", "outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	world := FootWorld(sc)
	if err := m.Instantiate(sc, world); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 180; i++ {
		m.Update(sc, world, 1.0/60.0)
	}
	a := &m.agents[0]
	seen := make(map[string]int)
	valid := 0
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
		tip := vec.V{X: (omin.X + omax.X) * 0.5, Z: (omin.Z + omax.Z) * 0.5}
		key := fmt.Sprintf("%.1f,%.1f", tip.X, tip.Z)
		seen[key]++
		t.Logf("%s tipXZ=%v y=%.2f", bone, tip, omin.Y)
	}
	for i, f := range a.SpiderBody().Feet {
		if f.Solve.Valid {
			valid++
		}
		t.Logf("foot[%d] valid=%v plant=%v world=%v", i, f.Solve.Valid, f.PlantWorld, f.World)
	}
	if valid < 6 {
		t.Fatalf("only %d/8 legs have valid IK", valid)
	}
	if len(seen) < 4 {
		t.Fatalf("tibia tips collapsed to %d unique XZ after walk: %v", len(seen), seen)
	}
}

func TestSpiderWalkTibiaSpread(t *testing.T) {
	sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", "npc-spider-test.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	world := FootWorld(sc)
	if err := m.Instantiate(sc, world); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 180; i++ {
		m.Update(sc, world, 1.0/60.0)
	}
	a := &m.agents[0]
	t.Logf("hip=%v", a.SpiderBody().Body.Pos)
	seen := make(map[string]int)
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
		tip := vec.V{X: (omin.X + omax.X) * 0.5, Z: (omin.Z + omax.Z) * 0.5}
		key := fmt.Sprintf("%.1f,%.1f", tip.X, tip.Z)
		seen[key]++
		t.Logf("%s tip=%v", bone, tip)
	}
	if len(seen) < 6 {
		t.Fatalf("tibia tips collapsed to %d after walk: %v", len(seen), seen)
	}
}

func TestSpiderSpawnedFeetOnGround(t *testing.T) {
	sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", "npc-spider-test.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	world := FootWorld(sc)
	if err := m.Instantiate(sc, world); err != nil {
		t.Fatal(err)
	}
	if len(m.agents) != 1 {
		t.Fatalf("agents = %d", len(m.agents))
	}
	a := &m.agents[0]
	body := a.Body
	groundY := world.GroundHeight(a.Spawn.X, a.Spawn.Z, a.LocomotorState().HipPos.Y+2)
	t.Logf("hip Y=%.3f ground Y=%.3f", a.LocomotorState().HipPos.Y, groundY)

	minY := 1e9
	cylIdx := body.Cylinders[0]
	for ai, bone := range body.AttachmentBones {
		if a.Rig.Attachments[ai].Kind != "cylinder" {
			continue
		}
		cyl := sc.Cylinders[cylIdx]
		cylIdx++
		omin, _ := cyl.WorldBounds()
		if omin.Y < minY {
			minY = omin.Y
		}
		if strings.HasSuffix(bone, "_tibia") || strings.HasSuffix(bone, "_femur") {
			t.Logf("  %s cylinder minY=%.3f", bone, omin.Y)
		}
		if strings.HasSuffix(bone, "_tibia") {
			if omin.Y > groundY+0.12 {
				t.Errorf("%s minY=%.3f too far above ground %.3f", bone, omin.Y, groundY)
			}
		}
	}
	t.Logf("lowest cylinder Y=%.3f", minY)
	if minY > groundY+0.12 {
		t.Fatalf("lowest leg Y=%.3f too far above ground %.3f", minY, groundY)
	}
}
