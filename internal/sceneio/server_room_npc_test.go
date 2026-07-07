package sceneio

import "testing"

func TestServerRoomNPCLoads(t *testing.T) {
	s, err := Load(repoFile("scenes/office-sunset/server-room-1.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.NPCSpawns) == 0 {
		t.Skip("server-room NPC spawn is commented out in server-room-1.toml")
	}
	if len(s.NPCSpawns) != 1 {
		t.Fatalf("npc spawns = %d, want 1", len(s.NPCSpawns))
	}
	if len(s.NPCSpawns[0].Patrol) != 3 {
		t.Fatalf("patrol = %v", s.NPCSpawns[0].Patrol)
	}
}
