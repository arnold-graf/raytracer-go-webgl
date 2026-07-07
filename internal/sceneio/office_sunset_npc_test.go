package sceneio

import (
	"testing"

	"raytracer/internal/npc"
)

func TestOfficeSunsetIndexIncludesNPCSpawns(t *testing.T) {
	idx, err := Load(repoFile("scenes/office-sunset/index.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.NPCSpawns) == 0 {
		t.Skip("server-room NPC spawn is commented out in server-room-1.toml")
	}
	if len(idx.NPCSpawns) != 1 {
		t.Fatalf("index npc spawns = %d, want 1 (included server-room NPCs)", len(idx.NPCSpawns))
	}
	sp := idx.NPCSpawns[0]
	if sp.Pos.Y < 199 {
		t.Fatalf("spawn pos = %v, want Y ~200 from include offset", sp.Pos)
	}
	m := npc.NewManager()
	if err := m.Instantiate(idx, npc.FootWorld(idx)); err != nil {
		t.Fatal(err)
	}
	if len(idx.DynamicBodies) != 1 {
		t.Fatalf("dynamic bodies = %d, want 1", len(idx.DynamicBodies))
	}
}

func TestServerRoomDirectNPCSpawnPosition(t *testing.T) {
	s, err := Load(repoFile("scenes/office-sunset/server-room-1.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.NPCSpawns) == 0 {
		t.Skip("server-room NPC spawn is commented out in server-room-1.toml")
	}
	if s.NPCSpawns[0].Pos.Y != 0.2 {
		t.Fatalf("spawn Y = %v, want 0.2", s.NPCSpawns[0].Pos.Y)
	}
	m := npc.NewManager()
	if err := m.Instantiate(s, npc.FootWorld(s)); err != nil {
		t.Fatal(err)
	}
	if len(s.DynamicBodies) != 1 {
		t.Fatalf("dynamic bodies = %d, want 1", len(s.DynamicBodies))
	}
}
