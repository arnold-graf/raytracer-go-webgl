package sceneio

import (
	"math"
	"testing"
)

func floatPtr(v float64) *float64 { return &v }

func TestCylinderBoxPlacement(t *testing.T) {
	d := cylinderDTO{
		placementDTO: placementDTO{PosX: floatPtr(-0.28), PosY: floatPtr(0), PosZ: floatPtr(-0.28)},
		Width:        0.56, Height: 4,
	}
	cx, cz, ymin, ymax, r, rt, err := d.specs()
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(cx-0) > 1e-9 || math.Abs(cz-0) > 1e-9 {
		t.Fatalf("center = (%v,%v), want (0,0)", cx, cz)
	}
	if ymin != 0 || ymax != 4 || math.Abs(r-0.28) > 1e-9 || rt != 0 {
		t.Fatalf("y=[%v,%v] r=%v rt=%v (uniform uses rt=0)", ymin, ymax, r, rt)
	}
}

func TestCylinderTaperedPlacement(t *testing.T) {
	d := cylinderDTO{
		placementDTO: placementDTO{PosX: floatPtr(-0.65), PosY: floatPtr(0.4), PosZ: floatPtr(-0.65)},
		WidthBottom: 1.3, WidthTop: 0.4, Height: 7.2,
	}
	cx, cz, ymin, ymax, r, rt, err := d.specs()
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(cx) > 1e-9 || math.Abs(cz) > 1e-9 || ymin != 0.4 || math.Abs(ymax-7.6) > 1e-9 {
		t.Fatalf("center=(%v,%v) y=[%v,%v]", cx, cz, ymin, ymax)
	}
	if math.Abs(r-0.65) > 1e-9 || math.Abs(rt-0.2) > 1e-9 {
		t.Fatalf("r=%v rt=%v", r, rt)
	}
}

func TestCylinderLoadFromTOML(t *testing.T) {
	s, err := Decode([]byte(`
[[cylinder]]
pos_x = -0.1
pos_y = 0.0
pos_z = -0.1
width = 0.2
height = 1.0
material = "diffuse"
albedo = [1.0, 1.0, 1.0]
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Cylinders) != 1 {
		t.Fatalf("cylinders = %d", len(s.Cylinders))
	}
	c := s.Cylinders[0]
	if math.Abs(c.CX) > 1e-9 || math.Abs(c.YMin) > 1e-9 || math.Abs(c.Radius-0.1) > 1e-9 {
		t.Fatalf("cylinder = %+v", c)
	}
}
