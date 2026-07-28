package character

import (
	"fmt"
	"testing"

	"raytracer/internal/vec"
)

func TestSpiderFeetSpreadAtInit(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 0, world)
	seen := make(map[string]int)
	for i, leg := range r.LegDefs() {
		key := fmt.Sprintf("%.2f,%.2f", s.Feet[i].PlantWorld.X, s.Feet[i].PlantWorld.Z)
		seen[key]++
		t.Logf("%s plant=%v ik=%v", leg.Prefix, s.Feet[i].PlantWorld, s.Feet[i].Solve.Foot)
	}
	if len(seen) < 6 {
		t.Fatalf("feet collapsed to %d unique XZ positions: %v", len(seen), seen)
	}
}

func TestSpiderPoseTipsSpreadAtInit(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 0, world)
	pose := s.ComputePose(r)
	seen := make(map[string]int)
	for i, leg := range r.LegDefs() {
		tip := r.BoneTip(pose, leg.Distal)
		key := fmt.Sprintf("%.2f,%.2f", tip.X, tip.Z)
		seen[key]++
		t.Logf("%s tip=%v ik=%v", leg.Prefix, tip, s.Feet[i].Solve.Foot)
	}
	if len(seen) < 6 {
		t.Fatalf("pose tips collapsed to %d unique XZ: %v", len(seen), seen)
	}
}

func TestSpiderComputePoseAllLegs(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 0.85, world)
	for i := 0; i < 120; i++ {
		s.Update(1.0/60.0, r, world)
	}
	pose := s.ComputePose(r)
	for _, leg := range r.LegDefs() {
		for _, bone := range []string{leg.Proximal, leg.Mid, leg.Distal} {
			if pose.Bones[bone] == nil {
				t.Fatalf("missing pose bone %s", bone)
			}
		}
	}
}
