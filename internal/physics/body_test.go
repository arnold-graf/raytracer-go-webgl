package physics

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestHoverForceSettles(t *testing.T) {
	body := NewBody(vec.V{Y: 2}, 0, 10)
	plane := vec.V{Y: 0}
	normal := vec.V{Y: 1}
	rest := 1.0

	for i := 0; i < 400; i++ {
		force := HoverForce(&body, plane, normal, rest, 400, 40)
		body.ApplyForce(force, 1.0/60.0)
		body.Integrate(1.0/60.0, 2, 4)
	}
	if math.Abs(body.Pos.Y-rest) > 0.08 {
		t.Fatalf("body Y = %.3f, want ~%.3f", body.Pos.Y, rest)
	}
}

func TestFitPlaneLevel(t *testing.T) {
	pts := []vec.V{
		{X: -1, Y: 0, Z: -1},
		{X: 1, Y: 0, Z: -1},
		{X: 0, Y: 0, Z: 1},
	}
	n, c := FitPlane(pts)
	if math.Abs(n.Y-1) > 0.01 {
		t.Fatalf("normal Y = %.3f, want ~1", n.Y)
	}
	if math.Abs(c.Y) > 0.01 {
		t.Fatalf("centroid Y = %.3f, want ~0", c.Y)
	}
}
