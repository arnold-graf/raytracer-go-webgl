# Reactive state — remaining refactors

Tracked follow-ups after unifying interact bindings (`ApplyInteractBindings`) and
making `scenestate.Manager` mutate `sc.Reactive.Fragments` directly (no duplicate
fragment slice).

## Done

- [x] **ApplyInteractBindings** — single path for `MergeInteractables`, splice, and refresh
- [x] **Manager uses `sc.Reactive.Fragments`** — span updates go through one registry

## 3. `finalizeFragment` in manager

`refreshFragment` still duplicates its tail in the copy vs splice branches:

- `syncFragmentInteractables`
- `syncDetachedStateInteractables`
- `registerDynamicBodies`
- touch (`Touch` / `TouchTransforms`)

Extract `finalizeFragment(sc, fi, span, touchMode)` and call from both paths. On the
copy path, consider whether both `RefreshFragmentInteractables` and
`syncFragmentInteractables` are always needed (pick maps vs action re-parse).

## 4. Scope resolution at build time

`syncDetachedStateInteractables` + `scopeForStateAction` remain as fallbacks for
interactables outside a reactive span (e.g. light switch in an included prop).

When expanding `on_use = 'toggle(foo)'`, stamp `StateScopeID` on the interactable
(or record `{iaIdx → scope}` in `ReactiveSpec`) so registration is O(1) and duplicate
local key names cannot collide silently.

## 5. Invariant helper

Add `AssertInteractConsistent(sc)` (test-only or debug):

- Every `Handler == "state"` interactable has a pick-map entry
- Every pick-map entry points to a valid primitive index inside the owning span

Run at the end of `refreshFragment` in tests.

## 6. `-1` sentinels on `Interactable` geometry fields

Initialize `BoxIndex` / `SphereIndex` / `LightIndex` to `-1` in
`RegisterInteractable` and `InteractFromOnUse`. Prevents zero-value indices from
being mistaken for valid bindings if a bad registration path is reintroduced.

## 7. Primitive-kind loop for spans

`ReactiveSpan`, `OffsetReactiveSpan`, `ShiftAll`, `CopyFragment`, and
`clearInteractMaps` manually enumerate ten primitive types. A small `PrimitiveKind`
enum + `Range(k)` / `ShiftAllKinds` would keep span arithmetic in sync when adding
primitive types.

## 8. `Scene.RefreshReactiveFragment`

Push copy/splice + span shift into `scene` as one operation:

```go
func (dst *Scene) RefreshReactiveFragment(spec *ReactiveSpec, fi int, local *Scene) (FragmentRefreshResult, error)
```

Manager keeps store + dependency graph; scene owns index surgery. Makes fragment
invariants testable without `scenestate` or `sceneio`.

## 9. Legacy reactive metadata

`sceneparam.ReactiveMeta` and `scene.ReactiveFragment` overlap; `Bindings` /
`Actions` (brightness-driven lights) overlap with structural `# if`. Consolidate or
deprecate after migrating `state-lamp.toml` and similar scenes to `[state]` + `# if`.

## 10. Dynamic light body registration

State lights use `state_light_%d` (`scenestate.registerDynamicBodies`); interactive
uplights use `interactive_light_%d` (`interactlight.Instantiate`). Converge on one
helper before state-transition animation work.

## 11. Document instanced vs flat refresh

`ReactiveFragment.Instanced` splits BLAS template updates from `SpliceFragment` on
flat geometry. Add a short comment block on `ReactiveFragment` (or a doc in this
plan) so new reactive features remember both paths.

## 12. Minor cleanup

- Deduplicate `scopedKey` (`scenestate`, `Action.scopedKey`, `sceneparam.ScopedKey`)
- Remove `EvalCount` / `ResetEvalCount` aliases once tests are migrated
- `replaceFragmentInteractables` merge (splice vs refresh) — low priority now that
  both call `applyFragmentInteractBindings`

## Suggested order

1. `finalizeFragment` + `AssertInteractConsistent` in tests
2. `StateScopeID` at expansion
3. `-1` sentinels
4. Everything else as polish or when touching related code
