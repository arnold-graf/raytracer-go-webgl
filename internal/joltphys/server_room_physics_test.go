package joltphys

import (
	"strings"
	"testing"

	"raytracer/internal/sceneio"
)

func TestServerRoomChairPhysics(t *testing.T) {
	sc, err := sceneio.Load(repoPath("scenes/office-sunset/server-room-1.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var chairGroups int
	for _, g := range sc.PhysicsGroups {
		if strings.Contains(g.Name, "chair") {
			chairGroups++
		}
	}
	if chairGroups < 1 {
		t.Fatalf("chair physics groups = %d, want at least 1; groups = %v", chairGroups, sc.PhysicsGroups)
	}
	for _, g := range sc.PhysicsGroups {
		if strings.Contains(g.Name, "simple-table") || strings.Contains(g.Name, "laptop") {
			t.Fatalf("unexpected physics on non-chair prop: %s", g.Name)
		}
	}
}
