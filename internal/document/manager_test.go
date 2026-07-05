package document

import (
	"testing"

	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestDismissKeepsCurrentPose(t *testing.T) {
	sc := &scene.Scene{}
	spec := scene.DocumentSpec{
		ID:    "note",
		Width: 0.21, Height: 0.297, Depth: 0.002,
		Rest: scene.NewRigidTransform(0, 0, 0, vec.New(1, 0.9, 2)),
	}
	a, err := spawnAgent(sc, spec)
	if err != nil {
		t.Fatal(err)
	}
	a.present.Open()
	a.present.OpenT = 1
	cam := camera.New()
	cam.Pos = vec.New(0, 1.6, 0)

	m := &Manager{agents: []agent{a}}
	m.Update(sc, cam, 0)
	before := sc.Boxes[a.boxIndex].Xform.Clone()
	if before == nil {
		t.Fatal("expected read pose")
	}

	m.Dismiss(sc)
	if m.Reading() {
		t.Fatal("expected movement unlock after dismiss")
	}
	if sc.Boxes[a.boxIndex].Xform == nil {
		t.Fatal("expected pose after dismiss")
	}
	if !transformNear(sc.Boxes[a.boxIndex].Xform, before) {
		t.Fatalf("dismiss snap: got %+v want %+v", sc.Boxes[a.boxIndex].Xform.Translation(), before.Translation())
	}

	m.Update(sc, cam, 0)
	if sc.Boxes[a.boxIndex].Xform == nil {
		t.Fatal("expected closing pose")
	}
	if !transformNear(sc.Boxes[a.boxIndex].Xform, before) {
		t.Fatalf("first closing frame: got %+v want %+v", sc.Boxes[a.boxIndex].Xform.Translation(), before.Translation())
	}
}

func transformNear(a, b *scene.Transform) bool {
	if a == nil || b == nil {
		return a == b
	}
	d := a.Translation().Sub(b.Translation())
	return d.LenSq() < 1e-8
}
