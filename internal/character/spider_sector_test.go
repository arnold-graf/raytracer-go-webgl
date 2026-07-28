package character

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func horizAngleDeg(a, b vec.V) float64 {
	a = vec.V{X: a.X, Z: a.Z}
	b = vec.V{X: b.X, Z: b.Z}
	if a.LenSq() < 1e-12 || b.LenSq() < 1e-12 {
		return 0
	}
	a = a.Normalize()
	b = b.Normalize()
	dot := clampScalar(a.X*b.X+a.Z*b.Z, -1, 1)
	return math.Acos(dot) * 180 / math.Pi
}

func TestClampToSectorInsideUnchanged(t *testing.T) {
	hip := vec.V{X: 0, Y: 0.5, Z: 0}
	home := vec.V{X: 1, Z: 0}
	target := vec.V{X: 0.8, Y: 0, Z: 0.2}
	out := clampToSector(target, hip, home, 45)
	if out.Sub(target).Len() > 1e-6 {
		t.Fatalf("inside sector should be unchanged: got %v want %v", out, target)
	}
}

func TestClampToSectorOutsideRotatesToBoundary(t *testing.T) {
	hip := vec.V{X: 0, Y: 0.5, Z: 0}
	home := vec.V{X: 1, Z: 0}
	target := vec.V{X: 0, Y: 0, Z: 1}
	maxDeg := 30.0
	out := clampToSector(target, hip, home, maxDeg)
	dir := out.Sub(hip)
	angle := horizAngleDeg(dir, home)
	if angle > maxDeg+0.5 {
		t.Fatalf("angle %.2f exceeds max %.2f", angle, maxDeg)
	}
	reach := vec.V{X: dir.X, Z: dir.Z}.Len()
	wantReach := vec.V{X: target.X - hip.X, Z: target.Z - hip.Z}.Len()
	if math.Abs(reach-wantReach) > 1e-4 {
		t.Fatalf("reach %.4f want %.4f", reach, wantReach)
	}
}

func TestSpiderSwingTargetsStayInLegSectors(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 0.85, world)
	legs := r.LegDefs()
	for frame := 0; frame < 240; frame++ {
		s.Update(1.0/60.0, r, world)
		bodyXf := s.Body.Transform()
		bodyHip := spiderBodyHip(bodyXf)
		stride := r.GaitForSpeed(s.Speed).Stride
		for i, leg := range legs {
			if i >= len(s.Feet) {
				break
			}
			home := legHomeDirWorld(leg, s.Heading)
			desired := s.desiredFootPos(leg, bodyXf, world, s.Body.Pos.Y+0.5, stride)
			if angle := horizAngleDeg(desired.Sub(bodyHip), home); angle > spiderLegSectorMaxDeg+1 {
				t.Fatalf("frame %d leg %s desired angle %.1f° > %.1f°", frame, leg.Prefix, angle, spiderLegSectorMaxDeg)
			}
			f := &s.Feet[i]
			if f.Phase != FootSwing {
				continue
			}
			for _, pt := range []vec.V{f.World, f.SwingTo} {
				if angle := horizAngleDeg(pt.Sub(bodyHip), home); angle > spiderLegSectorMaxDeg+2 {
					t.Fatalf("frame %d leg %s swing angle %.1f° > %.1f°", frame, leg.Prefix, angle, spiderLegSectorMaxDeg)
				}
			}
		}
	}
}
