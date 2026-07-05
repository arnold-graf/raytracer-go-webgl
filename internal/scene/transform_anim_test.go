package scene

import (
	"testing"

	"raytracer/internal/vec"
)

func TestLerpTransformEndpoints(t *testing.T) {
	a := NewRigidTransform(0, 0, 0, vec.New(0, 0, 0))
	b := NewRigidTransform(0, 90, 0, vec.New(1, 2, 3))

	at0 := LerpTransform(a, b, 0)
	if at0.Translation() != a.Translation() {
		t.Fatalf("t=0 pos = %v want %v", at0.Translation(), a.Translation())
	}
	at1 := LerpTransform(a, b, 1)
	if at1.Translation() != b.Translation() {
		t.Fatalf("t=1 pos = %v want %v", at1.Translation(), b.Translation())
	}
}

func TestSmoothStepEndpoints(t *testing.T) {
	if SmoothStep(0) != 0 || SmoothStep(1) != 1 {
		t.Fatalf("smoothstep endpoints = %v, %v", SmoothStep(0), SmoothStep(1))
	}
}
