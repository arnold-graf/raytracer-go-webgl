package sceneio

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestOfficeChairLegsReachWheels(t *testing.T) {
	path := repoFile("scenes/objects/office-chair.toml")
	parent := filepath.Join(t.TempDir(), "scene.toml")
	if err := os.WriteFile(parent, []byte(
		"[[include]]\nfile = "+strconv.Quote(path)+"\nat = [0.0, 0.0, 0.0]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(parent)
	if err != nil {
		t.Fatal(err)
	}

	legs := s.Boxes[3:8]
	wheels := s.Cylinders[2:7]

	for i, leg := range legs {
		tip := boxFarEndWorld(&leg, vec.New(0, 0.05, 0))
		w := wheels[i]
		wmin, wmax := w.WorldBounds()
		center := wmin.Add(wmax).Scale(0.5)

		dx := tip.X - center.X
		dz := tip.Z - center.Z
		dist := math.Hypot(dx, dz)
		if dist > 0.06 {
			t.Fatalf("leg %d: tip %v wheel center %v dist %.3f", i, tip, center, dist)
		}
	}
}

// boxFarEndWorld returns the box corner farthest from hub in XZ (leg tip).
func boxFarEndWorld(b *scene.Box, hub vec.V) vec.V {
	corners := []vec.V{
		b.Min, b.Max,
		{X: b.Min.X, Y: b.Min.Y, Z: b.Max.Z},
		{X: b.Min.X, Y: b.Max.Y, Z: b.Min.Z},
		{X: b.Min.X, Y: b.Max.Y, Z: b.Max.Z},
		{X: b.Max.X, Y: b.Min.Y, Z: b.Min.Z},
		{X: b.Max.X, Y: b.Min.Y, Z: b.Max.Z},
		{X: b.Max.X, Y: b.Max.Y, Z: b.Min.Z},
	}
	best := corners[0]
	bestD := 0.0
	for _, c := range corners {
		w := c
		if b.Xform != nil {
			w = b.Xform.ToWorld(c)
		}
		d := math.Hypot(w.X-hub.X, w.Z-hub.Z)
		if d > bestD {
			bestD = d
			best = w
		}
	}
	return best
}
