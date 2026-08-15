package scenestate

import (
	"fmt"

	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/sceneparam"
)

// Manager rebuilds reactive object fragments when state changes.
type Manager struct {
	reactive      *scene.ReactiveSpec
	store         *Store
	keyFragments  map[string][]int
	actions       map[int]Action
	refreshCount  int
	structChanged bool
}

// NewManager builds a manager from a loaded scene's reactive spec.
// spec must remain the scene's ReactiveSpec pointer for the manager's lifetime.
func NewManager(spec *scene.ReactiveSpec) (*Manager, error) {
	if spec == nil || len(spec.Fragments) == 0 {
		return &Manager{keyFragments: map[string][]int{}}, nil
	}
	initial := map[string]sceneparam.StateValue{}
	for _, frag := range spec.Fragments {
		for local, v := range frag.State {
			initial[scopedKey(frag.ScopeID, local)] = v
		}
	}
	store, err := newStore(initial)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		reactive:     spec,
		store:        store,
		keyFragments: make(map[string][]int),
		actions:      make(map[int]Action),
	}
	for i, frag := range spec.Fragments {
		for local := range frag.State {
			key := scopedKey(frag.ScopeID, local)
			m.keyFragments[key] = append(m.keyFragments[key], i)
		}
		for _, b := range frag.BoundStateProps {
			if frag.ParentScopeID == "" {
				continue
			}
			key := scopedKey(frag.ParentScopeID, b.StateKey)
			m.keyFragments[key] = append(m.keyFragments[key], i)
		}
	}
	return m, nil
}

// Instantiate registers dynamic bodies and state interact handlers for reactive fragments.
func (m *Manager) Instantiate(sc *scene.Scene) error {
	if m == nil || sc == nil || m.reactive == nil || len(m.reactive.Fragments) == 0 {
		return nil
	}
	for i := range m.reactive.Fragments {
		if err := m.syncFragmentInteractables(sc, i); err != nil {
			return err
		}
		m.registerDynamicBodies(sc, m.reactive.Fragments[i].Span)
	}
	return m.syncDetachedStateInteractables(sc)
}

// HandleInteract runs the state action for ia and refreshes dependent fragments.
func (m *Manager) HandleInteract(sc *scene.Scene, ia *scene.Interactable) error {
	if m == nil || sc == nil || ia == nil {
		return nil
	}
	action, ok := m.actions[ia.Index()]
	if !ok {
		return fmt.Errorf("no state action for interactable")
	}
	if err := action.run(m.store); err != nil {
		return err
	}
	return m.applyForKeys(sc, m.store.lastChangedKeys())
}

func (m *Manager) applyForKeys(sc *scene.Scene, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	seen := map[int]struct{}{}
	for _, key := range keys {
		for _, fi := range m.keyFragments[key] {
			if _, ok := seen[fi]; ok {
				continue
			}
			seen[fi] = struct{}{}
			if err := m.refreshFragment(sc, fi); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) refreshFragment(sc *scene.Scene, fi int) error {
	spec := m.reactiveFor(sc)
	if spec == nil || fi < 0 || fi >= len(spec.Fragments) {
		return nil
	}
	frag := &spec.Fragments[fi]
	if frag.SourcePath == "" {
		return fmt.Errorf("reactive fragment %q missing source path", frag.ScopeID)
	}
	m.refreshCount++

	var local *scene.Scene
	if len(frag.BoundStateProps) > 0 {
		props := cloneIncludeProps(frag.IncludeProps)
		for _, b := range frag.BoundStateProps {
			if frag.ParentScopeID == "" {
				continue
			}
			v, ok := m.store.Lookup(scopedKey(frag.ParentScopeID, b.StateKey))
			if !ok {
				continue
			}
			props[b.Prop] = stateValueToAny(v)
		}
		var err error
		local, err = sceneio.BuildReactiveObject(frag.SourcePath, props, nil)
		if err != nil {
			return err
		}
	} else {
		var err error
		local, err = sceneio.BuildReactiveObject(frag.SourcePath, frag.IncludeProps, m.stateOverride(frag.ScopeID))
		if err != nil {
			return err
		}
	}
	if frag.Instanced {
		sc.UpdateInstanceTemplate(frag.SourcePath, frag.IncludeProps, local)
	}
	if frag.Transform != nil {
		scene.ComposeFragment(local, frag.Transform)
	}

	if frag.Span.SameStructureAs(local) {
		needGen, needXform := scene.FragmentTouchLevel(sc, frag.Span, local)
		scene.CopyFragment(sc, frag.Span, local)
		scene.RefreshFragmentInteractables(sc, frag.Span, local, frag.Transform)
		if err := m.syncFragmentInteractables(sc, fi); err != nil {
			return err
		}
		if err := m.syncDetachedStateInteractables(sc); err != nil {
			return err
		}
		m.registerDynamicBodies(sc, frag.Span)
		switch {
		case needGen:
			sc.Touch()
		case needXform:
			sc.TouchTransforms()
		}
		return nil
	}

	m.structChanged = true
	delta, iaDelta := scene.SpliceFragment(sc, &frag.Span, local)
	spec.ShiftFragmentsAfter(fi, delta, iaDelta)
	if err := m.syncFragmentInteractables(sc, fi); err != nil {
		return err
	}
	if err := m.syncDetachedStateInteractables(sc); err != nil {
		return err
	}
	m.registerDynamicBodies(sc, frag.Span)
	sc.Touch()
	return nil
}

func (m *Manager) reactiveFor(sc *scene.Scene) *scene.ReactiveSpec {
	if sc != nil && sc.Reactive != nil {
		return sc.Reactive
	}
	return m.reactive
}

func (m *Manager) stateOverride(scopeID string) map[string]sceneparam.StateValue {
	out := map[string]sceneparam.StateValue{}
	spec := m.reactive
	if spec == nil {
		return out
	}
	for _, frag := range spec.Fragments {
		if frag.ScopeID != scopeID {
			continue
		}
		for local := range frag.State {
			if v, ok := m.store.Lookup(scopedKey(scopeID, local)); ok {
				out[local] = v
			}
		}
	}
	return out
}

func (m *Manager) syncFragmentInteractables(sc *scene.Scene, fi int) error {
	spec := m.reactiveFor(sc)
	if spec == nil || fi < 0 || fi >= len(spec.Fragments) {
		return nil
	}
	frag := spec.Fragments[fi]
	scopeID := frag.ScopeID
	if len(frag.BoundStateProps) > 0 && frag.ParentScopeID != "" {
		scopeID = frag.ParentScopeID
	} else if scopeID == "" && frag.ParentScopeID != "" {
		scopeID = frag.ParentScopeID
	}
	span := frag.Span
	for iaIdx := span.Interactables[0]; iaIdx < span.Interactables[1]; iaIdx++ {
		if iaIdx >= len(sc.Interactables) {
			continue
		}
		ia := &sc.Interactables[iaIdx]
		if ia.Handler != "state" || ia.StateAction == "" {
			continue
		}
		action, err := ParseAction(scopeID, ia.StateAction)
		if err != nil {
			return err
		}
		m.actions[iaIdx] = action
	}
	return nil
}

// syncDetachedStateInteractables registers state actions on interactables outside
// reactive fragment spans (e.g. an included prop object toggling parent [state]).
func (m *Manager) syncDetachedStateInteractables(sc *scene.Scene) error {
	for iaIdx := range sc.Interactables {
		if _, ok := m.actions[iaIdx]; ok {
			continue
		}
		ia := &sc.Interactables[iaIdx]
		if ia.Handler != "state" || ia.StateAction == "" {
			continue
		}
		scopeID, err := m.scopeForStateAction(ia.StateAction)
		if err != nil {
			return err
		}
		action, err := ParseAction(scopeID, ia.StateAction)
		if err != nil {
			return err
		}
		m.actions[iaIdx] = action
	}
	return nil
}

func (m *Manager) scopeForStateAction(stateAction string) (string, error) {
	parsed, err := ParseAction("", stateAction)
	if err != nil {
		return "", err
	}
	spec := m.reactive
	if spec == nil {
		return "", fmt.Errorf("no reactive scope owns state key %q from action %q", parsed.Key, stateAction)
	}
	for _, frag := range spec.Fragments {
		if _, ok := frag.State[parsed.Key]; ok {
			return frag.ScopeID, nil
		}
	}
	return "", fmt.Errorf("no reactive scope owns state key %q from action %q", parsed.Key, stateAction)
}

func (m *Manager) registerDynamicBodies(sc *scene.Scene, span scene.ReactiveSpan) {
	for i := span.Lights[0]; i < span.Lights[1]; i++ {
		name := fmt.Sprintf("state_light_%d", i)
		found := false
		for _, b := range sc.DynamicBodies {
			if b.Name == name {
				found = true
				break
			}
		}
		if found {
			continue
		}
		sc.DynamicBodies = append(sc.DynamicBodies, scene.DynamicBody{
			Name:   name,
			Lights: [2]int{i, i + 1},
		})
	}
}

// RefreshCount returns how many fragment rebuilds ran (for tests).
func (m *Manager) RefreshCount() int {
	if m == nil {
		return 0
	}
	return m.refreshCount
}

// ResetRefreshCount clears the refresh counter (for tests).
func (m *Manager) ResetRefreshCount() {
	if m == nil {
		return
	}
	m.refreshCount = 0
	m.structChanged = false
}

// StructChanged reports whether the last refresh pass changed primitive counts.
func (m *Manager) StructChanged() bool {
	if m == nil {
		return false
	}
	return m.structChanged
}

// EvalCount is an alias for RefreshCount for older tests.
func (m *Manager) EvalCount() int { return m.RefreshCount() }

// ResetEvalCount is an alias for ResetRefreshCount for older tests.
func (m *Manager) ResetEvalCount() { m.ResetRefreshCount() }

// HasStateLights reports whether any reactive fragments were loaded.
func (m *Manager) HasStateLights() bool {
	return m != nil && m.reactive != nil && len(m.reactive.Fragments) > 0
}

// IsStateLight reports whether lightIdx belongs to a reactive fragment span.
func (m *Manager) IsStateLight(lightIdx int) bool {
	if m == nil || m.reactive == nil {
		return false
	}
	for _, frag := range m.reactive.Fragments {
		sp := frag.Span
		if lightIdx >= sp.Lights[0] && lightIdx < sp.Lights[1] {
			return true
		}
	}
	return false
}

func scopedKey(scopeID, local string) string {
	if scopeID == "" {
		return local
	}
	return scopeID + "." + local
}

func cloneIncludeProps(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func stateValueToAny(v sceneparam.StateValue) any {
	switch v.Kind {
	case "bool":
		return v.Boolean
	case "string":
		return v.String
	case "vec3":
		return v.Vec3
	case "number", "":
		return v.Number
	default:
		return v.Number
	}
}
