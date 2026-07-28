package character

import (
	"testing"

	"raytracer/internal/vec"
)

func TestStanceSubPhase(t *testing.T) {
	roll := DefaultLocomotionParams().FootRoll
	cases := []struct {
		t    float64
		want FootPhase
	}{
		{0.05, FootHeelStrike},
		{0.14, FootHeelStrike},
		{0.5, FootMidStance},
		{0.85, FootToeOff},
		{0.95, FootToeOff},
	}
	for _, c := range cases {
		if got := stanceSubPhase(c.t, roll); got != c.want {
			t.Fatalf("stanceSubPhase(%v) = %v, want %v", c.t, got, c.want)
		}
	}
}

func TestFootRollDeg(t *testing.T) {
	roll := DefaultLocomotionParams().FootRoll
	if r := footRollDeg(FootSwing, 0, roll); r <= 0 {
		t.Fatalf("swing roll should be positive (toe up), got %v", r)
	}
	if r := footRollDeg(FootHeelStrike, 0, roll); r <= 0 {
		t.Fatalf("heel strike start should be positive, got %v", r)
	}
	if r := footRollDeg(FootHeelStrike, 0.15, roll); r > 1 {
		t.Fatalf("heel strike end should flatten, got %v", r)
	}
	if r := footRollDeg(FootMidStance, 0.5, roll); r != 0 {
		t.Fatalf("mid stance roll = %v, want 0", r)
	}
	if r := footRollDeg(FootToeOff, 1, roll); r >= 0 {
		t.Fatalf("toe off end should be negative, got %v", r)
	}
}

func TestFootPhaseTransitions(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 0, 1.2, world)

	sawSwing := false
	sawHeel := false
	sawToe := false
	for i := 0; i < 240; i++ {
		loc.Update(1.0 / 60.0, r, world)
		for _, f := range []Foot{loc.Left, loc.Right} {
			switch f.Phase {
			case FootSwing:
				sawSwing = true
				if f.SwingT < 0 || f.SwingT > 1.01 {
					t.Fatalf("swing t out of range: %v", f.SwingT)
				}
			case FootHeelStrike:
				sawHeel = true
			case FootToeOff:
				sawToe = true
			}
		}
	}
	if !sawSwing || !sawHeel || !sawToe {
		t.Fatalf("expected all phases over 240 frames: swing=%v heel=%v toe=%v", sawSwing, sawHeel, sawToe)
	}
}
