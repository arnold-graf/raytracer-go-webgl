package character

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestMultipedStepManagerWalks(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 1.0, world)
	z0 := s.Body.Pos.Z
	for i := 0; i < 240; i++ {
		s.Update(1.0/60.0, r, world)
		if math.IsNaN(s.Body.Pos.X) || math.IsNaN(s.Body.Pos.Z) {
			t.Fatalf("frame %d: NaN body pos", i)
		}
		for j := range s.Feet {
			if math.IsNaN(s.Feet[j].World.X) {
				t.Fatalf("frame %d foot %d NaN", i, j)
			}
		}
	}
	if z0-s.Body.Pos.Z < 1.0 {
		t.Fatalf("step manager: moved only %.2fm", z0-s.Body.Pos.Z)
	}
}

func TestSpiderStepManagerHipPlantDistanceBounded(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 1.0, world)
	for i := 0; i < 180; i++ {
		s.Update(1.0/60.0, r, world)
	}
	for i := 0; i < 120; i++ {
		s.Update(1.0/60.0, r, world)
		if d := s.maxHipPlantHoriz(r); d > spiderMaxHipPlantHoriz+0.02 {
			t.Fatalf("frame %d: hip-plant %.3f > %.3f", i+180, d, spiderMaxHipPlantHoriz)
		}
	}
}

func TestSpiderStepmgrPlantSpread(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 0.85, world)
	for i := 0; i < 180; i++ {
		s.Update(1.0/60.0, r, world)
	}
	body := s.Body.Pos
	var spread float64
	for i := range s.Feet {
		p := s.Feet[i].PlantWorld
		d := horizDist(vec.V{X: p.X, Z: p.Z}, vec.V{X: body.X, Z: body.Z})
		if d > spread {
			spread = d
		}
	}
	if spread < 0.55 {
		t.Fatalf("stepmgr max plant spread %.2f < 0.55", spread)
	}
}

func TestSpiderStanceFootRows(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 1.0, world)
	fwd := yawForward(0)
	z0 := s.Body.Pos.Z

	type plantTrack struct {
		pos    vec.V
		frames int
	}
	plants := make(map[int]plantTrack)
	rowed := false

	for frame := 0; frame < 150; frame++ {
		s.Update(1.0/60.0, r, world)
		bodyXf := s.rootTransform()
		for i, leg := range r.LegDefs() {
			if i >= len(s.Feet) {
				break
			}
			f := &s.Feet[i]
			if !f.Initialized || f.Phase == FootSwing {
				continue
			}
			if d := f.World.Sub(f.PlantWorld).Len(); d > 0.002 {
				t.Fatalf("frame %d %s: stance world != plant (d=%.4f)", frame, leg.Prefix, d)
			}
			prev, ok := plants[i]
			if ok && prev.pos.Sub(f.PlantWorld).Len() < 0.002 {
				prev.frames++
			} else {
				prev = plantTrack{pos: f.PlantWorld, frames: 1}
			}
			plants[i] = prev
			if prev.frames >= 4 {
				hip := bodyXf.ToWorld(r.JointLocal(leg.Proximal))
				ahead := (hip.X-f.PlantWorld.X)*fwd.X + (hip.Z-f.PlantWorld.Z)*fwd.Z
				if ahead > 0.04 {
					rowed = true
				}
			}
		}
	}
	if z0-s.Body.Pos.Z < 0.8 {
		t.Fatalf("body moved only %.2fm", z0-s.Body.Pos.Z)
	}
	if !rowed {
		t.Fatal("no stance leg showed hip ahead of planted foot (rowing)")
	}
}

func TestSpiderTetrapodStepModeWalks(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	r.Locomotion.MultipedStepMode = "tetrapod"
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 1.0, world)
	z0 := s.Body.Pos.Z
	for i := 0; i < 240; i++ {
		s.Update(1.0/60.0, r, world)
		if d := s.maxHipPlantHoriz(r); d > spiderMaxHipPlantHoriz+0.02 {
			t.Fatalf("frame %d: hip-plant %.3f", i, d)
		}
	}
	if z0-s.Body.Pos.Z < 1.0 {
		t.Fatalf("tetrapod: moved only %.2fm", z0-s.Body.Pos.Z)
	}
}
