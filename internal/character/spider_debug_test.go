package character

import (
	"testing"

	"raytracer/internal/vec"
)

func TestSpiderDebugSimStability(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 1.0, world)
	for i := 0; i < 180; i++ {
		s.Update(1.0/60.0, r, world)
		if i == 29 || i == 89 || i == 179 {
			t.Logf("f=%d pos=%v vel=%.2f yaw=%.1f pitch=%.1f roll=%.1f ang=%v",
				i+1, s.Body.Pos, s.Body.Vel.Len(), s.Body.Yaw, s.Body.Pitch, s.Body.Roll, s.Body.AngVel)
		}
	}
	if s.Body.Vel.Len() > 4 {
		t.Fatalf("velocity exploded: %.2f", s.Body.Vel.Len())
	}
	if abs(s.Body.Pos.Y-s.RestHeight) > 0.35 {
		t.Fatalf("height drifted: body Y=%.2f", s.Body.Pos.Y)
	}
	if abs(s.Body.Pos.Z) > 4 {
		t.Fatalf("position ran away: z=%.2f", s.Body.Pos.Z)
	}
	if s.Body.Vel.Len() > s.Speed*1.2+0.1 {
		t.Fatalf("velocity %.2f exceeds gait speed %.2f", s.Body.Vel.Len(), s.Speed)
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
