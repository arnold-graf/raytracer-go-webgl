// Package character loads YAML rigs, solves forward/inverse kinematics, and
// spawns analytic limb primitives into a scene.
package character

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"raytracer/internal/vec"
)

// Rig is an immutable skeleton definition shared by all agents of a type.
type Rig struct {
	Name        string
	HipHeight   float64
	AnkleHeight float64
	Bones       map[string]Bone
	BoneOrder   []string
	Attachments []Attachment
	Poses       map[string]map[string]JointAngles
	Gaits       map[string]GaitParams
}

// GaitParams tunes procedural walk/run locomotion.
type GaitParams struct {
	Speed    float64 `yaml:"speed"`
	StepRate float64 `yaml:"step_rate"`
	Stride   float64 `yaml:"stride"`
	Lift     float64 `yaml:"lift"`
	Bob      float64 `yaml:"bob"`
}

// DefaultWalkGait is used when a rig omits gaits.walk.
var DefaultWalkGait = GaitParams{Speed: 1.4, StepRate: 2.0, Stride: 0.55, Lift: 0.08, Bob: 0.03}

// DefaultRunGait is used when a rig omits gaits.run.
var DefaultRunGait = GaitParams{Speed: 4.0, StepRate: 3.5, Stride: 1.0, Lift: 0.12, Bob: 0.05}

// Bone describes one joint in the skeleton tree.
type Bone struct {
	Name   string
	Parent string
	Length float64
	Offset vec.V
	IK     string // "two_bone" or empty
	Pole   vec.V
}

// JointAngles are local Euler rotations (degrees, X then Y then Z) at a joint.
type JointAngles struct {
	Pitch float64 `yaml:"pitch"`
	Yaw   float64 `yaml:"yaw"`
	Roll  float64 `yaml:"roll"`
}

// Attachment binds an analytic primitive to a bone for rendering.
type Attachment struct {
	Bone   string
	Kind   string // box, cylinder, sphere
	Size   vec.V  // box dimensions
	Radius float64
	Length float64
	Offset vec.V
	Albedo vec.V
}

type rigYAML struct {
	Name        string                            `yaml:"name"`
	HipHeight   float64                           `yaml:"hip_height"`
	AnkleHeight float64                           `yaml:"ankle_height"`
	Bones       map[string]boneYAML               `yaml:"bones"`
	Attachments []attachmentYAML                  `yaml:"attachments"`
	Poses       map[string]map[string]JointAngles `yaml:"poses"`
	Gaits       map[string]gaitYAML               `yaml:"gaits"`
}

type gaitYAML struct {
	Speed    float64 `yaml:"speed"`
	StepRate float64 `yaml:"step_rate"`
	Stride   float64 `yaml:"stride"`
	Lift     float64 `yaml:"lift"`
	Bob      float64 `yaml:"bob"`
}

type boneYAML struct {
	Parent *string    `yaml:"parent"`
	Length float64    `yaml:"length"`
	Offset [3]float64 `yaml:"offset"`
	IK     string     `yaml:"ik"`
	Pole   [3]float64 `yaml:"pole"`
}

type attachmentYAML struct {
	Bone   string     `yaml:"bone"`
	Kind   string     `yaml:"kind"`
	Size   [3]float64 `yaml:"size"`
	Radius float64    `yaml:"radius"`
	Length float64    `yaml:"length"`
	Offset [3]float64 `yaml:"offset"`
	Albedo [3]float64 `yaml:"albedo"`
}

// LoadRig reads a YAML rig definition from path.
func LoadRig(path string) (*Rig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw rigYAML
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse rig %q: %w", path, err)
	}
	if raw.Name == "" {
		return nil, fmt.Errorf("rig %q: missing name", path)
	}
	if len(raw.Bones) == 0 {
		return nil, fmt.Errorf("rig %q: no bones", path)
	}
	hip := raw.HipHeight
	if hip <= 0 {
		hip = 0.95
	}
	ankle := raw.AnkleHeight
	if ankle <= 0 {
		ankle = 0.06
	}

	r := &Rig{
		Name:        raw.Name,
		HipHeight:   hip,
		AnkleHeight: ankle,
		Bones:     make(map[string]Bone, len(raw.Bones)),
		Poses:     raw.Poses,
	}
	if r.Poses == nil {
		r.Poses = map[string]map[string]JointAngles{}
	}
	r.Gaits = loadGaits(raw.Gaits)

	for name, b := range raw.Bones {
		parent := ""
		if b.Parent != nil {
			parent = *b.Parent
		}
		r.Bones[name] = Bone{
			Name:   name,
			Parent: parent,
			Length: b.Length,
			Offset: vec.New(b.Offset[0], b.Offset[1], b.Offset[2]),
			IK:     b.IK,
			Pole:   vec.New(b.Pole[0], b.Pole[1], b.Pole[2]),
		}
	}

	if err := validateBones(r.Bones); err != nil {
		return nil, fmt.Errorf("rig %q: %w", path, err)
	}
	r.BoneOrder = topoOrder(r.Bones)

	for _, a := range raw.Attachments {
		if _, ok := r.Bones[a.Bone]; !ok {
			return nil, fmt.Errorf("rig %q: attachment references unknown bone %q", path, a.Bone)
		}
		alb := vec.New(0.7, 0.7, 0.7)
		if a.Albedo != [3]float64{} {
			alb = vec.New(a.Albedo[0], a.Albedo[1], a.Albedo[2])
		}
		r.Attachments = append(r.Attachments, Attachment{
			Bone:   a.Bone,
			Kind:   a.Kind,
			Size:   vec.New(a.Size[0], a.Size[1], a.Size[2]),
			Radius: a.Radius,
			Length: a.Length,
			Offset: vec.New(a.Offset[0], a.Offset[1], a.Offset[2]),
			Albedo: alb,
		})
	}
	return r, nil
}

func validateBones(bones map[string]Bone) error {
	if _, ok := bones["hips"]; !ok {
		return fmt.Errorf("missing root bone %q", "hips")
	}
	for name, b := range bones {
		if b.Parent == "" {
			if name != "hips" {
				return fmt.Errorf("bone %q has no parent but is not hips", name)
			}
			continue
		}
		if _, ok := bones[b.Parent]; !ok {
			return fmt.Errorf("bone %q: unknown parent %q", name, b.Parent)
		}
	}
	return nil
}

func topoOrder(bones map[string]Bone) []string {
	children := map[string][]string{}
	var roots []string
	for name, b := range bones {
		if b.Parent == "" {
			roots = append(roots, name)
			continue
		}
		children[b.Parent] = append(children[b.Parent], name)
	}
	var order []string
	var walk func(string)
	walk = func(n string) {
		order = append(order, n)
		for _, c := range children[n] {
			walk(c)
		}
	}
	for _, root := range roots {
		walk(root)
	}
	return order
}

func loadGaits(raw map[string]gaitYAML) map[string]GaitParams {
	out := map[string]GaitParams{
		"walk": DefaultWalkGait,
		"run":  DefaultRunGait,
	}
	for name, g := range raw {
		p := GaitParams{
			Speed:    g.Speed,
			StepRate: g.StepRate,
			Stride:   g.Stride,
			Lift:     g.Lift,
			Bob:      g.Bob,
		}
		if p.Speed == 0 {
			if d, ok := out[name]; ok {
				p.Speed = d.Speed
			}
		}
		if p.StepRate == 0 {
			if d, ok := out[name]; ok {
				p.StepRate = d.StepRate
			} else {
				p.StepRate = 2.0
			}
		}
		if p.Stride == 0 {
			if d, ok := out[name]; ok {
				p.Stride = d.Stride
			}
		}
		if p.Lift == 0 {
			if d, ok := out[name]; ok {
				p.Lift = d.Lift
			}
		}
		if p.Bob == 0 {
			if d, ok := out[name]; ok {
				p.Bob = d.Bob
			}
		}
		out[name] = p
	}
	return out
}

// GaitForSpeed picks walk or run parameters from agent speed.
func (r *Rig) GaitForSpeed(speed float64) GaitParams {
	if speed >= 3.5 {
		if g, ok := r.Gaits["run"]; ok {
			return g
		}
		return DefaultRunGait
	}
	if g, ok := r.Gaits["walk"]; ok {
		return g
	}
	return DefaultWalkGait
}

// PoseAngles returns joint angles for poseName, falling back to zero angles.
func (r *Rig) PoseAngles(poseName, bone string) JointAngles {
	if r.Poses == nil {
		return JointAngles{}
	}
	if pose, ok := r.Poses[poseName]; ok {
		if a, ok := pose[bone]; ok {
			return a
		}
	}
	return JointAngles{}
}

// JointLocal returns where a child bone attaches in its parent's local space.
func (r *Rig) JointLocal(childName string) vec.V {
	b := r.Bones[childName]
	if b.Offset != (vec.V{}) {
		return b.Offset
	}
	if b.Parent == "" {
		return vec.V{}
	}
	parent := r.Bones[b.Parent]
	return vec.V{Y: parent.Length}
}
