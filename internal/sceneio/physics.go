package sceneio

import (
	"fmt"
	"path/filepath"
	"strings"

	"raytracer/internal/scene"
)

type physicsDTO struct {
	Mode        string   `toml:"mode"`
	Mass        float64  `toml:"mass"`
	Friction    *float64 `toml:"friction"`
	Restitution *float64 `toml:"restitution"`
	Sleep       *bool    `toml:"sleep"`
}

func (p physicsDTO) build() (scene.PhysicsSpec, error) {
	if p.Mode == "" {
		return scene.PhysicsSpec{}, nil
	}
	mode := scene.PhysicsMode(strings.ToLower(strings.TrimSpace(p.Mode)))
	switch mode {
	case scene.PhysicsStatic, scene.PhysicsCompound, scene.PhysicsPieces,
		scene.PhysicsKinematic, scene.PhysicsDynamic:
	default:
		return scene.PhysicsSpec{}, fmt.Errorf("unknown physics mode %q", p.Mode)
	}
	spec := scene.PhysicsSpec{
		Mode:        mode,
		MassKg:      p.Mass,
		Friction:    0.5,
		Restitution: 0.0,
		Sleep:       true,
	}
	if p.Friction != nil {
		spec.Friction = *p.Friction
	}
	if p.Restitution != nil {
		spec.Restitution = *p.Restitution
	}
	if p.Sleep != nil {
		spec.Sleep = *p.Sleep
	}
	return spec, nil
}

func mergePhysicsDTO(file, include *physicsDTO) *physicsDTO {
	if include != nil {
		return include
	}
	return file
}

func physicsFromDTO(d *physicsDTO) (scene.PhysicsSpec, error) {
	if d == nil {
		return scene.PhysicsSpec{Mode: scene.PhysicsStatic}, nil
	}
	spec, err := d.build()
	if err != nil {
		return scene.PhysicsSpec{}, err
	}
	if spec.Mode == "" {
		return scene.PhysicsSpec{Mode: scene.PhysicsStatic}, nil
	}
	return spec, nil
}

func registerPhysicsGroup(dst *scene.Scene, name string, spec scene.PhysicsSpec, before, after scene.PrimitiveCounts) {
	if dst == nil || spec.Mode == scene.PhysicsStatic || spec.Mode == "" {
		return
	}
	switch spec.Mode {
	case scene.PhysicsPieces:
		registerPieceGroups(dst, name, spec, before, after)
	default:
		body := scene.DynamicBody{
			Name:      name,
			Boxes:     [2]int{before.Boxes, after.Boxes},
			Cylinders: [2]int{before.Cylinders, after.Cylinders},
			Spheres:   [2]int{before.Spheres, after.Spheres},
			Lights:    [2]int{before.Lights, after.Lights},
		}
		dst.DynamicBodies = append(dst.DynamicBodies, body)
		dst.PhysicsGroups = append(dst.PhysicsGroups, scene.PhysicsGroup{
			Name: name,
			Spec: spec,
			Body: body,
		})
	}
}

func registerPieceGroups(dst *scene.Scene, prefix string, spec scene.PhysicsSpec, before, after scene.PrimitiveCounts) {
	for i := before.Boxes; i < after.Boxes; i++ {
		name := fmt.Sprintf("%s_box_%d", prefix, i-before.Boxes)
		body := scene.DynamicBody{Name: name, Boxes: [2]int{i, i + 1}}
		dst.DynamicBodies = append(dst.DynamicBodies, body)
		dst.PhysicsGroups = append(dst.PhysicsGroups, scene.PhysicsGroup{
			Name: name,
			Spec: spec,
			Body: body,
		})
	}
	for i := before.Spheres; i < after.Spheres; i++ {
		name := fmt.Sprintf("%s_sphere_%d", prefix, i-before.Spheres)
		body := scene.DynamicBody{Name: name, Spheres: [2]int{i, i + 1}}
		dst.DynamicBodies = append(dst.DynamicBodies, body)
		dst.PhysicsGroups = append(dst.PhysicsGroups, scene.PhysicsGroup{
			Name: name,
			Spec: spec,
			Body: body,
		})
	}
	for i := before.Cylinders; i < after.Cylinders; i++ {
		name := fmt.Sprintf("%s_cylinder_%d", prefix, i-before.Cylinders)
		body := scene.DynamicBody{Name: name, Cylinders: [2]int{i, i + 1}}
		dst.DynamicBodies = append(dst.DynamicBodies, body)
		dst.PhysicsGroups = append(dst.PhysicsGroups, scene.PhysicsGroup{
			Name: name,
			Spec: spec,
			Body: body,
		})
	}
}

// mergeScenePhysics copies physics metadata from a merged sub-scene into dst,
// shifting primitive indices by the pre-merge counts on dst.
func mergeScenePhysics(dst, sub *scene.Scene, before scene.PrimitiveCounts) {
	if sub == nil || len(sub.PhysicsGroups) == 0 {
		return
	}
	boxOff := before.Boxes
	sphereOff := before.Spheres
	cylinderOff := before.Cylinders
	lightOff := before.Lights
	for _, g := range sub.PhysicsGroups {
		body := shiftDynamicBody(g.Body, boxOff, sphereOff, cylinderOff, lightOff)
		dst.DynamicBodies = append(dst.DynamicBodies, body)
		dst.PhysicsGroups = append(dst.PhysicsGroups, scene.PhysicsGroup{
			Name: g.Name,
			Spec: g.Spec,
			Body: body,
		})
	}
}

func shiftDynamicBody(db scene.DynamicBody, boxOff, sphereOff, cylinderOff, lightOff int) scene.DynamicBody {
	out := db
	if out.Boxes[1] > out.Boxes[0] {
		out.Boxes[0] += boxOff
		out.Boxes[1] += boxOff
	}
	if out.Spheres[1] > out.Spheres[0] {
		out.Spheres[0] += sphereOff
		out.Spheres[1] += sphereOff
	}
	if out.Cylinders[1] > out.Cylinders[0] {
		out.Cylinders[0] += cylinderOff
		out.Cylinders[1] += cylinderOff
	}
	if out.Lights[1] > out.Lights[0] {
		out.Lights[0] += lightOff
		out.Lights[1] += lightOff
	}
	return out
}

func mergeIncludePhysics(dst *scene.Scene, sub *scene.Scene, inc includeDTO, incPath string, index int, before, after scene.PrimitiveCounts) error {
	dto := mergePhysicsDTO(subFilePhysics(sub), inc.Physics)
	spec, err := physicsFromDTO(dto)
	if err != nil {
		return err
	}
	if spec.Mode == scene.PhysicsStatic {
		return nil
	}
	base := strings.TrimSuffix(filepath.Base(incPath), filepath.Ext(incPath))
	name := fmt.Sprintf("include_%d_%s", index, base)
	registerPhysicsGroup(dst, name, spec, before, after)
	return nil
}

func subFilePhysics(sub *scene.Scene) *physicsDTO {
	if sub == nil || sub.FilePhysics.Mode == "" {
		return nil
	}
	d := &physicsDTO{
		Mode: string(sub.FilePhysics.Mode),
		Mass: sub.FilePhysics.MassKg,
	}
	if sub.FilePhysics.Friction > 0 {
		f := sub.FilePhysics.Friction
		d.Friction = &f
	}
	if sub.FilePhysics.Restitution > 0 {
		r := sub.FilePhysics.Restitution
		d.Restitution = &r
	}
	sleep := sub.FilePhysics.Sleep
	d.Sleep = &sleep
	return d
}
