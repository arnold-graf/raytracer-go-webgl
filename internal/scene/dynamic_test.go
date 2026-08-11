package scene

import "testing"

func TestRemoveDynamicBodyShiftsLaterBodies(t *testing.T) {
	sc := &Scene{
		Boxes:     make([]Box, 5),
		Cylinders: make([]Cylinder, 6),
		Lights:    make([]Light, 2),
		Campfires: make([]Campfire, 2),
		DynamicBodies: []DynamicBody{
			{Name: "first", Cylinders: [2]int{2, 4}, Lights: [2]int{0, 1}, Campfires: [2]int{0, 1}},
			{Name: "second", Cylinders: [2]int{4, 6}, Lights: [2]int{1, 2}, Campfires: [2]int{1, 2}},
		},
	}
	sc.RemoveDynamicBody(sc.DynamicBodies[0])
	if len(sc.Cylinders) != 4 {
		t.Fatalf("cylinders = %d, want 4", len(sc.Cylinders))
	}
	if len(sc.Lights) != 1 {
		t.Fatalf("lights = %d, want 1", len(sc.Lights))
	}
	if len(sc.Campfires) != 1 {
		t.Fatalf("campfires = %d, want 1", len(sc.Campfires))
	}
	if len(sc.DynamicBodies) != 1 || sc.DynamicBodies[0].Name != "second" {
		t.Fatalf("bodies = %#v", sc.DynamicBodies)
	}
	got := sc.DynamicBodies[0]
	if got.Cylinders != [2]int{2, 4} {
		t.Fatalf("shifted cylinders = %v, want [2 4]", got.Cylinders)
	}
	if got.Lights != [2]int{0, 1} {
		t.Fatalf("shifted lights = %v, want [0 1]", got.Lights)
	}
	if got.Campfires != [2]int{0, 1} {
		t.Fatalf("shifted campfires = %v, want [0 1]", got.Campfires)
	}
}

func TestDynamicBodyAttached(t *testing.T) {
	sc := &Scene{Cylinders: make([]Cylinder, 3)}
	body := DynamicBody{Name: "torch", Cylinders: [2]int{0, 3}}
	if !body.Attached(sc) {
		t.Fatal("expected attached")
	}
	sc.Cylinders = sc.Cylinders[:2]
	if body.Attached(sc) {
		t.Fatal("expected detached after slice shrink")
	}
}
