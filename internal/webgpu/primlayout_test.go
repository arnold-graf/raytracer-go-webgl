package webgpu

import (
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestDynamicGPUIndices(t *testing.T) {
	sc := &scene.Scene{
		Spheres: []scene.Sphere{{Center: vec.V{}, Radius: 0.1}},
		Boxes: []scene.Box{
			{Min: vec.V{}, Max: vec.V{X: 1}, Surface: scene.Surface{Mat: scene.MatDiffuse}},
			{Min: vec.V{X: 2}, Max: vec.V{X: 3}, Surface: scene.Surface{Mat: scene.MatDiffuse}},
		},
		Cylinders: []scene.Cylinder{
			{CX: 0, CZ: 0, Radius: 0.1, YMin: 0, YMax: 1, Surface: scene.Surface{Mat: scene.MatDiffuse}},
			{CX: 1, CZ: 0, Radius: 0.1, YMin: 0, YMax: 1, Surface: scene.Surface{Mat: scene.MatDiffuse}},
		},
		DynamicBodies: []scene.DynamicBody{{
			Boxes:     [2]int{1, 2},
			Cylinders: [2]int{0, 2},
		}},
	}
	l := computePrimLayout(sc)
	got := dynamicGPUIndices(sc, l)
	boxBase := l.nSphere + l.nPlane
	cylBase := boxBase + l.nBox
	want := []int{boxBase + 1, cylBase, cylBase + 1}
	if len(got) != len(want) {
		t.Fatalf("indices = %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index[%d] = %d want %d (all=%v)", i, got[i], want[i], got)
		}
	}
}

func TestCoalesceIndices(t *testing.T) {
	spans := coalesceIndices([]int{3, 1, 2, 5, 7, 8})
	if len(spans) != 3 {
		t.Fatalf("spans = %v", spans)
	}
	if spans[0] != [2]int{1, 4} || spans[1] != [2]int{5, 6} || spans[2] != [2]int{7, 9} {
		t.Fatalf("spans = %v", spans)
	}
}

func TestRepackGPUPrimUpdatesTransform(t *testing.T) {
	sc := &scene.Scene{
		Boxes: []scene.Box{{
			Min:     vec.V{},
			Max:     vec.V{X: 1, Y: 1, Z: 1},
			Surface: scene.Surface{Mat: scene.MatDiffuse, Xform: scene.NewRigidTransform(0, 0, 0, vec.V{})},
		}},
	}
	l := computePrimLayout(sc)
	prims := PackPrimitives(sc)
	sc.Boxes[0].Xform = scene.NewRigidTransform(0, 45, 0, vec.V{X: 1})
	repackGPUPrim(sc, l, l.nSphere+l.nPlane, &prims[0])
	if prims[0].Meta[3]&primFlagTransformed == 0 {
		t.Fatal("expected transformed flag after repack")
	}
	if prims[0].Xf0[3] == 0 {
		t.Fatal("expected non-zero translation in repacked xform")
	}
}
