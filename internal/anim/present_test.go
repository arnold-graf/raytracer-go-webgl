package anim

import (
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestCloseUnlocksImmediately(t *testing.T) {
	var c Channel
	c.Open()
	if !c.Active {
		t.Fatal("expected active after open")
	}
	held := scene.NewRigidTransform(0, 0, 0, vec.New(1, 1.6, 2))
	c.Close(held)
	if c.Active {
		t.Fatal("expected inactive after close")
	}
	if !c.Closing {
		t.Fatal("expected closing after close")
	}
}

func TestCloseStartsAtFrozenPose(t *testing.T) {
	rest := scene.NewRigidTransform(0, 0, 0, vec.V{})
	held := scene.NewRigidTransform(0, 45, 0, vec.New(1, 1.6, 2))
	var c Channel
	c.Close(held)
	pose := c.closePose(rest)
	if pose.Translation() != held.Translation() {
		t.Fatalf("close start = %v want %v", pose.Translation(), held.Translation())
	}
}
