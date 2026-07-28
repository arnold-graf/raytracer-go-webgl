package character

import (
	"math"
	"path/filepath"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func repoFile(rel string) string {
	return filepath.Join("..", "..", rel)
}

func TestLoadHumanoidRig(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "humanoid" {
		t.Fatalf("name = %q, want humanoid", r.Name)
	}
	if r.Locomotion.FootOffsetLateral != 0.14 {
		t.Fatalf("foot_offset_lateral = %v, want 0.14", r.Locomotion.FootOffsetLateral)
	}
	if r.Navigation.Height != 1.7 {
		t.Fatalf("navigation.height = %v, want 1.7", r.Navigation.Height)
	}
	if len(r.Bones) < 10 {
		t.Fatalf("bones = %d, want at least 10", len(r.Bones))
	}
	if len(r.Attachments) < 8 {
		t.Fatalf("attachments = %d, want at least 8", len(r.Attachments))
	}
	if _, ok := r.Poses["idle"]; !ok {
		t.Fatal("missing idle pose")
	}
}

func TestLoadSpiderRig(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "spider" {
		t.Fatalf("name = %q, want spider", r.Name)
	}
	if len(r.Bones) != 28 {
		t.Fatalf("bones = %d, want 28", len(r.Bones))
	}
	if len(r.Attachments) != 27 {
		t.Fatalf("attachments = %d, want 27 (3 spheres + 24 leg cylinders)", len(r.Attachments))
	}
	if _, ok := r.Poses["idle"]; !ok {
		t.Fatal("missing idle pose")
	}
}

func TestSpiderInstantiate(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	sc := scene.Default()
	body, err := SpawnAttachments(r, sc)
	if err != nil {
		t.Fatal(err)
	}
	nCyl := body.Cylinders[1] - body.Cylinders[0]
	nSph := body.Spheres[1] - body.Spheres[0]
	if nCyl != 24 {
		t.Fatalf("cylinders = %d, want 24", nCyl)
	}
	if nSph != 3 {
		t.Fatalf("spheres = %d, want 3", nSph)
	}
	pose := r.ComputeFK("idle", vec.New(0, r.HipHeight, 0), 0)
	ApplyPose(r, sc, body, pose)
	if sc.Spheres[body.Spheres[0]].Xform == nil {
		t.Fatal("expected sphere transform after ApplyPose")
	}
}

func TestFKIdlePose(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 0, 0, world)
	pose := ComputeLocomotionPose(r, &loc, "idle", world)

	hips := pose.Bones["hips"].Translation()
	if math.Abs(hips.Y-r.HipHeight) > 1e-9 {
		t.Fatalf("hips Y = %v, want %v", hips.Y, r.HipHeight)
	}

	headTip := r.BoneTip(pose, "head")
	if headTip.Y <= hips.Y {
		t.Fatalf("head tip Y=%v should be above hips Y=%v", headTip.Y, hips.Y)
	}

	leftFoot := footSoleWorld(r, pose, "foot_l")
	rightFoot := footSoleWorld(r, pose, "foot_r")
	if math.Abs(leftFoot.Y-rightFoot.Y) > 0.05 {
		t.Fatalf("feet uneven: L=%v R=%v", leftFoot.Y, rightFoot.Y)
	}
	if leftFoot.Y > 0.05 {
		t.Fatalf("left foot sole Y=%v should be near ground below hips Y=%v", leftFoot.Y, hips.Y)
	}
}

func TestTwoBoneIK(t *testing.T) {
	root := vec.New(0, 1, 0)
	l1, l2 := 0.42, 0.40
	reach := l1 + l2 - 0.01
	target := root.Add(vec.V{Y: -0.7, Z: 0.2}.Normalize().Scale(reach))
	pole := vec.New(0, 1, 1)
	res := SolveTwoBone(root, target, pole, l1, l2)
	if !res.OK {
		t.Fatal("expected OK")
	}
	if res.EndError(target) > 0.05 {
		t.Fatalf("end error = %v", res.EndError(target))
	}
	d1 := res.Mid.Sub(root).Len()
	d2 := res.End.Sub(res.Mid).Len()
	if math.Abs(d1-0.42) > 1e-4 || math.Abs(d2-0.40) > 1e-4 {
		t.Fatalf("segment lengths = %v %v", d1, d2)
	}
}

func TestSpawnAndApplyPose(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	sc := scene.Default()
	body, err := SpawnAttachments(r, sc)
	if err != nil {
		t.Fatal(err)
	}
	nCyl := body.Cylinders[1] - body.Cylinders[0]
	if nCyl < 6 {
		t.Fatalf("cylinders = %d, want at least 6", nCyl)
	}
	pose := r.ComputeFK("idle", vec.New(1, r.HipHeight, 2), 45)
	ApplyPose(r, sc, body, pose)
	if sc.Cylinders[body.Cylinders[0]].Xform == nil {
		t.Fatal("expected cylinder transform after ApplyPose")
	}
}

func TestSpiderAttachmentReflect(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Attachments) == 0 || r.Attachments[0].Reflect != 0.1 {
		t.Fatalf("abdomen reflect = %v, want 0.1", r.Attachments[0].Reflect)
	}
	sc := scene.Default()
	body, err := SpawnAttachments(r, sc)
	if err != nil {
		t.Fatal(err)
	}
	sph := sc.Spheres[body.Spheres[0]]
	if sph.Reflect != 0.1 {
		t.Fatalf("spawned sphere reflect = %v, want 0.1", sph.Reflect)
	}
}
