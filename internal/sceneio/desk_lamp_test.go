package sceneio_test

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func TestDeskLampHingeAlignment(t *testing.T) {
	path := filepath.Join("..", "..", "scenes", "objects", "desk-anglepoise-lamp.toml")
	sc, err := sceneio.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cylEnds := func(c scene.Cylinder) (vec.V, vec.V) {
		lo := vec.New(c.CX, c.YMin, c.CZ)
		hi := vec.New(c.CX, c.YMax, c.CZ)
		if c.Xform != nil {
			lo = c.Xform.ToWorld(lo)
			hi = c.Xform.ToWorld(hi)
		}
		return lo, hi
	}
	near := func(a, b vec.V, eps float64) bool {
		return math.Abs(a.X-b.X) <= eps && math.Abs(a.Y-b.Y) <= eps && math.Abs(a.Z-b.Z) <= eps
	}
	p0 := vec.New(-0.014, 0.07, 0)
	p1 := vec.New(-0.014, 0.30, -0.14)
	p1c := vec.New(0, 0.30, -0.14)
	p2 := vec.New(0, 0.46, 0.22)
	const eps = 0.012

	lo, hi := cylEnds(sc.Cylinders[1])
	if !near(lo, p0, eps) || !near(hi, p1, eps) {
		t.Fatalf("lower arm: got %v→%v want %v→%v", lo, hi, p0, p1)
	}
	lo, hi = cylEnds(sc.Cylinders[3])
	if !near(lo, p1c, eps) || !near(hi, p2, eps) {
		t.Fatalf("upper arm: got %v→%v want %v→%v", lo, hi, p1, p2)
	}
}

func TestDeskLampAlbedoParam(t *testing.T) {
	path := filepath.Join("..", "..", "scenes", "objects", "desk-anglepoise-lamp.toml")
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	parent := filepath.Join(dir, "scene.toml")
	if err := os.WriteFile(parent, []byte(
		"[[include]]\nfile = "+strconv.Quote(abs)+"\nat = [0.0, 0.9, 0.0]\n"+
			"props = { albedo = [0.2, 0.3, 0.4] }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sc, err := sceneio.Load(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Boxes) < 1 {
		t.Fatal("expected lamp boxes")
	}
	alb := sc.Boxes[0].Albedo
	if math.Abs(alb.X-0.2) > 1e-9 || math.Abs(alb.Y-0.3) > 1e-9 || math.Abs(alb.Z-0.4) > 1e-9 {
		t.Fatalf("base albedo = %v, want [0.2, 0.3, 0.4]", alb)
	}
}

func TestLoadDeskAnglepoiseLamp(t *testing.T) {
	path := filepath.Join("..", "..", "scenes", "objects", "desk-anglepoise-lamp.toml")
	sc, err := sceneio.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Cylinders) < 4 || len(sc.Cones) != 1 {
		t.Fatalf("cylinders=%d cones=%d", len(sc.Cylinders), len(sc.Cones))
	}
	if len(sc.Lights) != 1 {
		t.Fatalf("lights=%d", len(sc.Lights))
	}
}

func TestLoadServerRoomDesk(t *testing.T) {
	path := filepath.Join("..", "..", "scenes", "office-sunset", "objects", "server-room-desk.toml")
	sc, err := sceneio.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Lights) < 1 {
		t.Fatal("desk lamp light missing")
	}
}
