package joltphys

import (
	"github.com/bbitechnologies/jolt-go/jolt"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

type primBinding struct {
	kind  uint8 // 0 box, 1 sphere, 2 cylinder, 3 light
	index int
	rel   *scene.Transform // primitive pose relative to body rest pose
	dir   vec.V            // body-local emission axis for spot lights (kind 3)
}

type simBinding struct {
	body     *jolt.BodyID
	name     string
	rest     *scene.Transform
	prims    []primBinding
	isDoor   bool
	kinematic bool
}

type bodyMap struct {
	bindings []simBinding
}

func (m *bodyMap) find(name string) *simBinding {
	for i := range m.bindings {
		if m.bindings[i].name == name {
			return &m.bindings[i]
		}
	}
	return nil
}
