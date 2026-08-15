package scene

import (
	"path/filepath"
	"strconv"

	"raytracer/internal/sceneparam"
)

// ReactiveSpec collects reactive fragments merged while loading a scene.
type ReactiveSpec struct {
	Fragments []ReactiveFragment
}

// ReactiveFragment is one parameterized object's runtime state and bindings.
type ReactiveFragment struct {
	ScopeID        string
	SourcePath     string
	IncludeProps   map[string]any
	State          map[string]sceneparam.StateValue
	Props          map[string]sceneparam.StateValue
	StructuralDeps []string
	Span           ReactiveSpan
	Transform      *Transform

	// ParentScopeID and BoundStateProps mark an [[include]] whose props mirror parent [state].
	ParentScopeID   string
	BoundStateProps []BoundStateProp

	// Legacy metadata from first expansion.
	Bindings []sceneparam.LightBrightnessBinding
	Actions  []sceneparam.LightAction

	// Instanced marks fragments whose GPU geometry comes from InstancingCatalog templates.
	Instanced bool
}

// BoundStateProp wires an include prop to a parent-scope state variable.
type BoundStateProp struct {
	Prop     string
	StateKey string
}

// ScopeIDForPath returns the reactive state scope basename for a source file path.
func ScopeIDForPath(path string) string {
	return filepath.Base(path)
}

// IncludeScopePrefix builds the scope prefix applied when merging an [[include]].
func IncludeScopePrefix(parentPath string, includeIndex int) string {
	return ScopeIDForPath(parentPath) + "#" + strconv.Itoa(includeIndex) + "/"
}

// InstanceScopeID returns a unique reactive scope for one instanced placement.
func InstanceScopeID(sourcePath string, placementIndex int) string {
	return ScopeIDForPath(sourcePath) + "@p" + strconv.Itoa(placementIndex)
}

// PrefixFragmentScopes prepends prefix to a fragment's ScopeID and ParentScopeID.
func PrefixFragmentScopes(frag *ReactiveFragment, prefix string) {
	if frag == nil || prefix == "" {
		return
	}
	if frag.ScopeID == "" && frag.SourcePath != "" && frag.ParentScopeID == "" {
		frag.ScopeID = ScopeIDForPath(frag.SourcePath)
	}
	if frag.ScopeID != "" {
		frag.ScopeID = prefix + frag.ScopeID
	}
	if frag.ParentScopeID != "" {
		frag.ParentScopeID = prefix + frag.ParentScopeID
	}
}

// MergeFragment appends a loaded include fragment with primitive offsets applied.
func (s *ReactiveSpec) MergeFragment(frag ReactiveFragment, lightOffset int) {
	if s == nil || (len(frag.State) == 0 && len(frag.Bindings) == 0 && len(frag.Actions) == 0 &&
		len(frag.BoundStateProps) == 0 && frag.SourcePath == "") {
		return
	}
	for i := range frag.Bindings {
		frag.Bindings[i].LightIndex += lightOffset
	}
	for i := range frag.Actions {
		frag.Actions[i].LightIndex += lightOffset
	}
	s.Fragments = append(s.Fragments, frag)
}

// ShiftFragmentsAfter updates spans for fragments following a structural edit at editIdx.
func (s *ReactiveSpec) ShiftFragmentsAfter(editIdx int, delta PrimitiveCounts, iaDelta int) {
	if s == nil || editIdx < 0 {
		return
	}
	for i := editIdx + 1; i < len(s.Fragments); i++ {
		s.Fragments[i].Span.ShiftAll(delta, iaDelta)
	}
}
