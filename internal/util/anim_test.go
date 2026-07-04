package util

import "testing"

func TestStepToward(t *testing.T) {
	if got := StepToward(0, 10, 3); got != 3 {
		t.Fatalf("StepToward = %v, want 3", got)
	}
	if got := StepToward(8, 10, 3); got != 10 {
		t.Fatalf("StepToward near target = %v, want 10", got)
	}
	if got := StepToward(5, 0, 2); got != 3 {
		t.Fatalf("StepToward negative = %v, want 3", got)
	}
}

func TestAtTarget(t *testing.T) {
	if !AtTarget(1.0, 1.0005, 0.001) {
		t.Fatal("expected within eps")
	}
	if AtTarget(1.0, 1.01, 0.001) {
		t.Fatal("expected outside eps")
	}
}
