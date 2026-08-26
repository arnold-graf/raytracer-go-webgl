package sceneio

import "testing"

func TestTerrainZonePathExpandsToSegments(t *testing.T) {
	const sceneTOML = `
[[terrain]]
origin = [0, 0, 0]
size = [10, 10]

  [[terrain.zone]]
  path = [[0, 0], [0, 5], [5, 5]]
  width = 4
`
	s, err := Decode([]byte(sceneTOML))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Terrains[0].Zones) != 2 {
		t.Fatalf("zones = %d, want 2 path segments", len(s.Terrains[0].Zones))
	}
}
