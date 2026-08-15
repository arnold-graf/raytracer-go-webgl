package sceneio

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"

	"raytracer/internal/scene"
	"raytracer/internal/sceneparam"
)

func reactiveFragmentFromMeta(meta *sceneparam.ReactiveMeta) scene.ReactiveFragment {
	if meta == nil {
		return scene.ReactiveFragment{}
	}
	return scene.ReactiveFragment{
		ScopeID:        meta.ScopeID,
		SourcePath:     meta.SourcePath,
		State:          sceneparam.ExportState(meta.State),
		Props:          sceneparam.ExportState(meta.Props),
		StructuralDeps: append([]string(nil), meta.StructuralDeps...),
		Bindings:       meta.Bindings,
		Actions:        meta.Actions,
	}
}

func attachReactive(s *scene.Scene, meta *sceneparam.ReactiveMeta, path string, before scene.PrimitiveCounts, iaBefore int) {
	if s == nil || meta == nil {
		return
	}
	if s.Reactive == nil {
		s.Reactive = &scene.ReactiveSpec{}
	}
	frag := reactiveFragmentFromMeta(meta)
	frag.SourcePath = path
	after := scene.CountPrimitives(s)
	frag.Span = scene.SpanFromMerge(before, after, iaBefore, len(s.Interactables))
	s.Reactive.MergeFragment(frag, 0)
}

func mergeBoundInclude(dst *scene.Scene, parentPath, incPath string, includeIndex int, props map[string]any, before, after scene.PrimitiveCounts, iaBefore, iaAfter int, xf *scene.Transform) error {
	if dst == nil || dst.Reactive == nil {
		return nil
	}
	deps, err := sceneparam.IncludeStatePropDeps(parentPath, includeIndex)
	if err != nil {
		return err
	}
	if len(deps) == 0 {
		return nil
	}
	bound := make([]scene.BoundStateProp, len(deps))
	for i, d := range deps {
		bound[i] = scene.BoundStateProp{Prop: d.Prop, StateKey: d.StateKey}
	}
	propsCopy := map[string]any{}
	for k, v := range props {
		propsCopy[k] = v
	}
	frag := scene.ReactiveFragment{
		SourcePath:      incPath,
		IncludeProps:    propsCopy,
		ParentScopeID:   scopeIDForPath(parentPath),
		BoundStateProps: bound,
		Span:            scene.SpanFromMerge(before, after, iaBefore, iaAfter),
		Transform:       xf,
	}
	dst.Reactive.MergeFragment(frag, before.Lights)
	return nil
}

func mergeReactive(dst *scene.Scene, sub *scene.Scene, parentPath string, includeIndex int, incPath string, props map[string]any, before, after scene.PrimitiveCounts, iaBefore, iaAfter int, xf *scene.Transform) {
	if dst == nil || sub == nil || sub.Reactive == nil {
		return
	}
	if dst.Reactive == nil {
		dst.Reactive = &scene.ReactiveSpec{}
	}
	_ = after
	_ = iaAfter
	prefix := scene.IncludeScopePrefix(parentPath, includeIndex)
	for _, frag := range sub.Reactive.Fragments {
		if frag.SourcePath == "" {
			frag.SourcePath = incPath
		}
		if len(frag.IncludeProps) == 0 && len(props) > 0 {
			frag.IncludeProps = props
		}
		scene.PrefixFragmentScopes(&frag, prefix)
		frag.Span = scene.OffsetReactiveSpan(frag.Span, before, iaBefore)
		if xf != nil {
			composed := xf
			if frag.Transform != nil {
				composed = xf.Compose(frag.Transform)
			}
			frag.Transform = composed
		}
		dst.Reactive.MergeFragment(frag, before.Lights)
	}
}

// BuildReactiveObject re-expands a parameterized object file with runtime state overrides.
func BuildReactiveObject(path string, props map[string]any, stateOverride map[string]sceneparam.StateValue) (*scene.Scene, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read object %q: %w", path, err)
	}
	rendered, _, meta, err := sceneparam.ExpandWithReactive(path, raw, props, scopeIDForPath(path), stateOverride)
	if err != nil {
		return nil, err
	}
	var dto sceneDTO
	if _, err := toml.Decode(string(rendered), &dto); err != nil {
		return nil, fmt.Errorf("decode object %q: %w", path, err)
	}
	s, err := dto.build()
	if err != nil {
		return nil, err
	}
	if meta != nil {
		attachReactive(s, meta, path, scene.PrimitiveCounts{}, 0)
	}
	return s, nil
}

func scopeIDForPath(path string) string {
	return scene.ScopeIDForPath(path)
}
