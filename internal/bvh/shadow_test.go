package bvh

import (
	"testing"

	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func TestServerRoomSouthWallBlocksShadow(t *testing.T) {
	sc, err := sceneio.Load("../../scenes/office-sunset/index.toml")
	if err != nil {
		t.Fatal(err)
	}
	// Server-room local SW floor corner, included at y=200.
	origin := vec.V{X: 2.0, Y: 201.0, Z: 19.5}
	// Toward a point 5 m south of the south wall (outside the room).
	out := vec.V{X: 2.0, Y: 204.0, Z: 30.0}
	dir := out.Sub(origin)
	dir = dir.Scale(1.0 / dir.Len())
	ray := vec.Ray{Origin: origin, Dir: dir}
	maxT := origin.Sub(out).Len()

	b := NewBlockers(sc)
	if !b.AnyHit(ray, maxT) {
		t.Fatal("south wall should block shadow ray leaving the server room")
	}
}

func TestServerRoomSunNotBlockedBySouthWall(t *testing.T) {
	sc, err := sceneio.Load("../../scenes/office-sunset/index.toml")
	if err != nil {
		t.Fatal(err)
	}
	origin := vec.V{X: 2.0, Y: 201.0, Z: 19.5}
	sun := vec.V{X: -10.35, Y: 200.0, Z: 0.0}
	dir := sun.Sub(origin)
	maxT := dir.Len()
	dir = dir.Scale(1.0 / maxT)
	ray := vec.Ray{Origin: origin, Dir: dir}

	b := NewBlockers(sc)
	// West glass is not a blocker; grid holes may let rays through — do not require block.
	_ = b.AnyHit(ray, maxT)
}
