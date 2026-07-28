package character

import (
	"math"
	"sort"

	"raytracer/internal/vec"
)

// LegKind distinguishes biped (thigh/shin/foot) from tripod (coxa/femur/tibia).
type LegKind int

const (
	LegKindBiped LegKind = iota
	LegKindTripod
)

// LegDef describes one locomotion leg on a rig.
type LegDef struct {
	Prefix      string
	Kind        LegKind
	Proximal    string // thigh or coxa
	Mid         string // shin or femur
	Distal      string // foot or tibia (IK end effector)
	PhaseOffset float64
	SideSign    float64 // biped: +1 left, -1 right
	RestOffset  vec.V   // hip-local socket offset for foot placement
}

type legYAML struct {
	Prefix string  `yaml:"prefix"`
	Phase  float64 `yaml:"phase"`
}

// LegDefs returns locomotion legs for this rig, auto-detecting when unset.
func (r *Rig) LegDefs() []LegDef {
	if len(r.Legs) > 0 {
		return r.Legs
	}
	if _, ok := r.Bones["thigh_l"]; ok {
		return defaultBipedLegs(r)
	}
	if legs := detectTripodLegs(r); len(legs) > 0 {
		return legs
	}
	return nil
}

func (r *Rig) legBoneSet() map[string]bool {
	skip := make(map[string]bool)
	for _, leg := range r.LegDefs() {
		skip[leg.Proximal] = true
		skip[leg.Mid] = true
		skip[leg.Distal] = true
	}
	return skip
}

func (r *Rig) isMultiped() bool {
	return len(r.LegDefs()) > 2
}

func defaultBipedLegs(r *Rig) []LegDef {
	return []LegDef{
		{
			Prefix: "l", Kind: LegKindBiped,
			Proximal: "thigh_l", Mid: "shin_l", Distal: "foot_l",
			PhaseOffset: 0, SideSign: 1,
			RestOffset: r.JointLocal("thigh_l"),
		},
		{
			Prefix: "r", Kind: LegKindBiped,
			Proximal: "thigh_r", Mid: "shin_r", Distal: "foot_r",
			PhaseOffset: 0.5, SideSign: -1,
			RestOffset: r.JointLocal("thigh_r"),
		},
	}
}

func detectTripodLegs(r *Rig) []LegDef {
	type cand struct {
		prefix string
		off    vec.V
		angle  float64
	}
	var cands []cand
	for name, b := range r.Bones {
		if b.Parent != "hips" || len(name) < 6 || name[len(name)-5:] != "_coxa" {
			continue
		}
		prefix := name[:len(name)-5]
		off := b.Offset
		if off == (vec.V{}) {
			off = r.JointLocal(name)
		}
		cands = append(cands, cand{
			prefix: prefix,
			off:    off,
			angle:  math.Atan2(off.X, -off.Z),
		})
	}
	if len(cands) == 0 {
		return nil
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].angle < cands[j].angle })
	n := float64(len(cands))
	legs := make([]LegDef, len(cands))
	for i, c := range cands {
		legs[i] = LegDef{
			Prefix:      c.prefix,
			Kind:        LegKindTripod,
			Proximal:    c.prefix + "_coxa",
			Mid:         c.prefix + "_femur",
			Distal:      c.prefix + "_tibia",
			PhaseOffset: float64(i) / n,
			SideSign:    legSideSignFromOffset(c.off),
			RestOffset:  c.off,
		}
	}
	return legs
}

func loadLegDefs(raw []legYAML, bones map[string]Bone) []LegDef {
	if len(raw) == 0 {
		return nil
	}
	legs := make([]LegDef, 0, len(raw))
	for _, item := range raw {
		prefix := item.Prefix
		coxa := prefix + "_coxa"
		if _, ok := bones[coxa]; !ok {
			continue
		}
		off := bones[coxa].Offset
		legs = append(legs, LegDef{
			Prefix:      prefix,
			Kind:        LegKindTripod,
			Proximal:    coxa,
			Mid:         prefix + "_femur",
			Distal:      prefix + "_tibia",
			PhaseOffset: item.Phase,
			SideSign:    legSideSignFromOffset(off),
			RestOffset:  off,
		})
	}
	return legs
}

// hipWorldOffset maps a hip-local offset into world space at heading.
func hipWorldOffset(hip vec.V, hipY, heading float64, local vec.V) vec.V {
	fwd, right := yawForward(heading), yawRight(heading)
	return vec.V{
		X: hip.X + fwd.X*(-local.Z) + right.X*local.X,
		Y: hipY,
		Z: hip.Z + fwd.Z*(-local.Z) + right.Z*local.X,
	}
}

func legRadialDir(hip, restWorld vec.V, heading float64) vec.V {
	radial := restWorld.Sub(vec.V{X: hip.X, Y: hip.Y, Z: hip.Z})
	radial.Y = 0
	if radial.LenSq() < 1e-9 {
		return yawRight(heading)
	}
	return radial.Normalize()
}

// legSideSignFromOffset returns +1 for left (negative local X) and -1 for right.
func legSideSignFromOffset(local vec.V) float64 {
	if local.X < 0 {
		return 1
	}
	if local.X > 0 {
		return -1
	}
	return 1
}

// legMinLateral returns the minimum lateral distance from hip for this leg's foot.
func legMinLateral(loc LocomotionParams, leg LegDef) float64 {
	min := loc.FootLateralMin
	attach := math.Abs(leg.RestOffset.X)
	if attach > min {
		min = attach * 0.85
	}
	return min
}
