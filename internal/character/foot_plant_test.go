package character

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestFootSoleFlatOnGround(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 270, 0.2, world)
	for i := 0; i < 300; i++ {
		loc.Update(1.0/60.0, r, world)
	}
	pose := ComputeLocomotionPose(r, &loc, "idle", world)
	for _, name := range []string{"foot_l", "foot_r"} {
		xf := pose.Bones[name]
		if xf == nil {
			t.Fatalf("missing %s", name)
		}
		up := xf.RotateDir(vec.V{Z: 1})
		if math.Abs(up.Y) < 0.85 {
			t.Fatalf("%s sole up %v should be near world +Y on flat ground", name, up)
		}
	}
}

func TestStanceFootKeepsGroundHeight(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 270, 0.2, world)
	for i := 0; i < 180; i++ {
		loc.Update(1.0/60.0, r, world)
	}
	seen := false
	for i := 0; i < 180; i++ {
		loc.Update(1.0/60.0, r, world)
		for _, f := range []Foot{loc.Left, loc.Right} {
			if f.Phase == FootSwing {
				continue
			}
			seen = true
			if math.Abs(f.World.Y-f.PlantGroundY) > 1e-6 {
				t.Fatalf("stance foot Y should stay at landing height: world=%v groundY=%v", f.World.Y, f.PlantGroundY)
			}
		}
	}
	if !seen {
		t.Fatal("expected stance samples")
	}
}

func TestHipStaysNearPlantedFeet(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 270, 0.2, world)
	fwd := yawForward(loc.Heading)
	stride := r.GaitForSpeed(loc.Speed).StepStride(r.GaitForSpeed(loc.Speed).TravelSpeed(loc.Speed))
	maxLead := 0.0
	for i := 0; i < 480; i++ {
		loc.Update(1.0/60.0, r, world)
		for _, f := range []Foot{loc.Left, loc.Right} {
			if f.Phase == FootSwing {
				continue
			}
			lead := math.Abs(f.PlantWorld.Sub(loc.HipPos).Dot(fwd))
			if lead > maxLead {
				maxLead = lead
			}
		}
	}
	// Planted feet stay within one stride of the hip (fixed world plant).
	if maxLead > stride*1.05 {
		t.Fatalf("planted foot too far from hip along travel: max offset=%v stride=%v", maxLead, stride)
	}
}

type stepGround struct {
	stepX float64
	stepY float64
}

func (g stepGround) GroundHeight(x, z, headY float64) float64 {
	if x >= g.stepX {
		return g.stepY
	}
	return 0
}

func (stepGround) GroundNormal(x, z, headY float64) vec.V {
	return vec.V{Y: 1}
}

func TestHipTracksSurfaceUnderPelvis(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := stepGround{stepX: 0.3, stepY: 0.25}
	loc := NewLocomotor(r, vec.V{}, 270, 0.2, world)
	for i := 0; i < 300; i++ {
		loc.Update(1.0/60.0, r, world)
	}
	// Hip should be near the surface under its X, not stuck at spawn height.
	expected := world.GroundHeight(loc.HipPos.X, loc.HipPos.Z, loc.HipPos.Y+r.Locomotion.HeadClearance)
	got := loc.GroundY
	if got < expected-0.15 {
		t.Fatalf("hip ground lag: got=%v expected≈%v at x=%v", got, expected, loc.HipPos.X)
	}
}

func TestHipRisesWithPlantedFoot(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := stepGround{stepX: 0.3, stepY: 0.25}
	loc := NewLocomotor(r, vec.V{}, 270, 0.2, world)
	y0 := loc.HipPos.Y
	for i := 0; i < 600; i++ {
		loc.Update(1.0/60.0, r, world)
	}
	if loc.HipPos.Y <= y0+0.05 {
		t.Fatalf("hip should rise after stepping onto higher surface: y0=%v now=%v", y0, loc.HipPos.Y)
	}
}
