package scenestate

import (
	"fmt"

	"raytracer/internal/sceneparam"
)

// Store holds scoped reactive state and records which keys changed last.
type Store struct {
	env         *sceneparam.Env
	subscribers map[string][]int
	lastKeys    []string
}

func newStore(initial map[string]sceneparam.StateValue) (*Store, error) {
	env, err := sceneparam.BuildEnv(initial)
	if err != nil {
		return nil, err
	}
	return &Store{
		env:         env,
		subscribers: make(map[string][]int),
	}, nil
}

// Subscribe registers a binding index to re-run when key changes.
func (s *Store) Subscribe(key string, bindingIdx int) {
	if s == nil {
		return
	}
	s.subscribers[key] = append(s.subscribers[key], bindingIdx)
}

// Toggle flips a boolean state variable.
func (s *Store) Toggle(scopedKey string) error {
	if s == nil {
		return nil
	}
	v, ok := s.env.LookupState(scopedKey)
	if !ok {
		return fmt.Errorf("unknown state %q", scopedKey)
	}
	if v.Kind != "bool" {
		return fmt.Errorf("toggle requires bool state %q", scopedKey)
	}
	v.Boolean = !v.Boolean
	s.set(scopedKey, v)
	return nil
}

// SetValue updates a state variable and records the changed key.
func (s *Store) SetValue(scopedKey string, v sceneparam.StateValue) {
	if s == nil {
		return
	}
	s.set(scopedKey, v)
}

func (s *Store) set(scopedKey string, v sceneparam.StateValue) {
	val, err := sceneparam.ImportStateValue(v)
	if err != nil {
		return
	}
	s.env.Set(scopedKey, val)
	s.lastKeys = []string{scopedKey}
}

func (s *Store) lastChangedKeys() []string {
	if s == nil {
		return nil
	}
	return s.lastKeys
}

// Env returns the evaluation environment for bindings.
func (s *Store) Env() *sceneparam.Env {
	if s == nil {
		return sceneparam.NewEnv()
	}
	return s.env
}

// SubscriberIndices returns binding indices for key (for tests).
func (s *Store) SubscriberIndices(key string) []int {
	if s == nil {
		return nil
	}
	out := make([]int, len(s.subscribers[key]))
	copy(out, s.subscribers[key])
	return out
}

// Lookup reads a scoped state value.
func (s *Store) Lookup(scopedKey string) (sceneparam.StateValue, bool) {
	if s == nil {
		return sceneparam.StateValue{}, false
	}
	return s.env.LookupState(scopedKey)
}
