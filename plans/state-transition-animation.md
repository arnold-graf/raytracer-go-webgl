# Plan: State transition animation

## Status: PROPOSED

Animated motion when reactive `[state]` toggles change primitive placement or shape — without a separate `[[state.transition]]` table. Endpoints come from `# if` overrides in the same primitive block; timing and easing are per-primitive fields.

**Related:** reactive state (`internal/scenestate`, `internal/sceneparam`), doors (`internal/door`), `internal/anim.Channel`, `scene.LerpTransform` / `TouchTransforms()`.

**Out of scope for v1:** collision ghosting / sweep during motion (see [Collisions](#collisions-deferred)).

---

## Goal

Authors write state-driven geometry the way they already do for instant toggles:

```toml
[state]
open = false

[[box]]
depth = 0.1
width = 1
height = 1
pos_x = 0
pos_y = 0
pos_z = 0
transition_props = ["pos_x"]
transition_duration = 0.8
transition_easing = "ease-in-out"
animation_sound = "wall-slide"
on_use = "toggle(open)"

# if open
pos_x = 1
# endif
```

When the player toggles `open`, the wall **slides** from `pos_x = 0` to `pos_x = 1` over 0.8s instead of snapping on the next frame.

Only props listed in `transition_props` animate. Other `# if` overrides (material, brightness, structural branches) keep today’s instant refresh behaviour unless also listed and animatable.

---

## Authoring

### Conditional overrides (`# if` only)

No explicit transition table. For each primitive, the **closed/base** values are the keys outside `# if` blocks. For each state key `K`, a `# if K` / `# if not K` body in the same `[[box]]` (etc.) block defines the **open/alternate** endpoint for those keys.

Multiple state keys may each have their own `# if` sections on one primitive. A transition runs when any listed `transition_props` value changes because a tracked state key flipped.

Structural `# if` (whole extra `[[light]]`, etc.) remains **instant** — not interpolatable in v1.

### New per-primitive fields

Added to **all** geometry primitives (`[[box]]`, `[[sphere]]`, `[[cylinder]]`, …) via shared DTO / schema fragment (same pattern as `on_use` / `hint`):

| Field | Type | Default | Purpose |
| ----- | ---- | ------- | ------- |
| `transition_props` | string array | `[]` | Prop names to animate when a `# if` override changes them (e.g. `["pos_x"]`, `["pos_x", "height", "rotate_y"]`). Empty → instant snap (current behaviour). |
| `transition_duration` | number (seconds) | `0.6` | Duration of one transition on this primitive. |
| `transition_easing` | string | `"smoothstep"` | Easing curve name (see below). |
| `animation_sound` | string | `""` | Optional sound id played when a transition **starts** on this primitive. Empty → silent. |

**Easing names (v1):**

| Name | Notes |
| ---- | ----- |
| `linear` | Constant speed |
| `smoothstep` | Hermite smoothstep — matches `scene.SmoothStep` (documents/screens) |
| `ease-in` | Slow start |
| `ease-out` | Slow end |
| `ease-in-out` | Slow start and end |

Extensible later; unknown names → load error.

**Animatable props (v1):**

Placement / transform inputs only — the ones that map to `Transform` or box bounds without changing primitive count:

- `pos_x`, `pos_y`, `pos_z`
- `rotate_x`, `rotate_y`, `rotate_z`
- `transform_origin` (if both endpoints differ)
- Box sizing: `width`, `height`, `depth` (lerp `Min`/`Max` via composed transform or explicit bounds lerp — prefer **transform offset** for perf; see [Runtime animation](#runtime-animation))

Non-animatable in v1 (instant only): `material`, `albedo`, `brightness`, adding/removing tables, etc.

---

## Prerequisite: duplicate keys in source TOML

Today, authors cannot write:

```toml
[[box]]
pos_x = 0
# if open
pos_x = 1
# endif
```

because **duplicate keys in one table are rejected** (TOML tooling / strict parse path) before or during load.

### Required changes

1. **Source files:** Allow duplicate keys **within a primitive table** when later keys appear inside `# if` / `# for` comment regions (Tombi: disable or narrow duplicate-key rule for `scenes/**/*.toml`; document in `schemas/README.md`).

2. **Expander:** When `# if` body is merged into a table, apply **override semantics** (last wins for the active branch) so expanded text passed to the Go decoder never contains conflicting duplicates from base + active branch. Prefer an explicit merge step over relying on decoder last-wins.

3. **Transition metadata:** During reactive expand, record overrides per `(primitive index, state key, prop)` **before** flattening duplicates — this is the endpoint spec; do not depend on duplicate keys surviving into final TOML.

4. **Schema / loader:** `internal/sceneio/toml.go` + `schemas/scene.schema.json` — add the four new fields to `primitive_transform` or a new `transition_props` definition composed into every primitive `allOf`.

---

## Architecture

```text
  Author TOML (# if overrides + transition_*)
           │
           ▼
  sceneparam expand ──► TransitionSpec per ReactiveFragment
           │              (endpoints from # if diffs, timing from primitive fields)
           ▼
  sceneio load ──► scene + ReactiveSpec (unchanged merge path)
           │
           ▼
  scenestate.Instantiate ──► register DynamicBody spans for animatable primitives
           │
  on_use ──► store mutation ──► transitionMgr.Begin(...)  OR  instant refreshFragment
           │
  each frame ──► transitionMgr.Update(dt) ──► lerp transforms ──► TouchTransforms()
           │
  t = 1 ──► optional commit refresh (or keep pose on Xform permanently)
```

New package: **`internal/statetransition/`** (name TBD) — owns agents, easing, sound hook; **`scenestate.Manager`** decides animated vs instant per fragment refresh.

Reuse: `anim.Channel` (or thin wrapper), `scene.LerpTransform`, `scene.DynamicBody`, door-style `TouchTransforms()` path.

---

## Load time: building the transition spec

For each reactive fragment (same scope as today’s `ReactiveFragment`):

1. Parse source with **tracking expand** that records, for each primitive table:
   - stable primitive ordinal within fragment (`box[0]`, …)
   - base prop values (lines outside any `# if`)
   - overrides from each `# if <stateKey>` body (prop → value when that key is true)
   - `transition_props`, `transition_duration`, `transition_easing`, `animation_sound` from base table

2. For each `(primitive, stateKey, prop)` where:
   - `prop ∈ transition_props`, and
   - base value ≠ override value when state is true (or false for `# if not`),

   store a **TransitionEndpoint**:
   - which state key triggers it
   - false-end and true-end numeric / transform values
   - duration, easing, sound from that primitive

3. **Dual expand sanity check:** expand once with state false, once with true; confirm endpoint diff matches tracked overrides for listed props.

4. Register on `ReactiveFragment` (new field, e.g. `Transitions []TransitionBinding`).

Primitives with empty `transition_props` → no entries; state changes use existing instant `CopyFragment` / `SpliceFragment` path only.

---

## Runtime animation

### Why transform lerp, not per-frame geometry regen

Instant refresh today writes new `Min`/`Max` and calls `Touch()` (BVH regen). Doors instead lerp **`Box.Xform`** and call `TouchTransforms()` only. **v1 follows the door model** for listed placement props:

- Author **rest geometry** at the false-state pose (base keys).
- While `open` animates false → true, apply a runtime **delta transform** on the primitive’s `DynamicBody` span so the visual endpoint matches the `# if open` override.
- At `t = 1`, either:
  - **A (preferred):** commit one instant `refreshFragment` to sync buffers with expanded true state, then clear delta; or
  - **B:** leave accumulated offset on `Xform` and skip commit (door style).

Decision in implementation; plan assumes **A** for consistency with reactive truth in store, **B** as fallback if commit causes hitches.

Box dimension props (`width`, `height`, `depth`) in `transition_props`: lerp bounds in local space or scale via transform — benchmark; if bounds lerp forces `Touch()` every frame, restrict v1 docs to position/rotation only and treat size as instant until optimized.

### `TransitionManager`

```go
type Agent struct {
    FragmentIdx int
    StateKey    string
    Primitive   PrimitiveRef // kind + index in fragment span
    Channel     anim.Channel
    From, To    TransformSnapshot // props named in transition_props
    Easing      func(float64) float64
    Sound       string
}
```

**`Begin(sc, fragment, stateKey)`** (called from `scenestate` instead of immediate geometry write when only animatable props changed):

1. State already updated in store.
2. Snapshot current world pose from scene (or use cached false endpoint).
3. Compute target pose from true/false endpoint table for new state value.
4. Start channel; fire `animation_sound` via app hook (same pattern as `door.SetAnimateHook`).
5. Block duplicate toggles on same agent while `Channel.Engaged()` (or support reverse — see doors).

**`Update(sc, dt)`** (called from `Game.Update` next to `doors.Update`):

1. Advance each agent; `u := easing(channelT)`.
2. Lerp transform / bounds per `From`/`To`.
3. `sc.TouchTransforms()` if any agent moved.

**Instant path unchanged** when:

- structural splice needed (primitive count change), or
- changed props not in `transition_props`, or
- no transition spec on fragment.

### Interaction with `scenestate.Manager`

Split `refreshFragment`:

| Condition | Action |
| --------- | ------ |
| Structural change (`!SameStructureAs`) | Instant `SpliceFragment` (today) |
| Only non-animated props | Instant `CopyFragment` |
| Animated props in `transition_props` | `transitionMgr.Begin`; defer `CopyFragment` until complete |

Add `Manager.Update(sc, dt)` or delegate to `TransitionManager` from app.

---

## Sounds

- **`animation_sound`** on the primitive whose transition started.
- App registers hook: `statetransition.SetAnimateHook(func(AnimateEvent){ ... })` mirroring `internal/app/door_sound.go`.
- Event payload: sound name, fragment scope, primitive ref, state key, direction (opening/closing), duration.
- No positional audio requirements in v1 beyond optional world center of primitive.

---

## Collisions (deferred)

Do **not** block v1 on collision policy. Document known limitation: a sliding box may pop through the player or block early/late until we add door-like ghosting or sweep tests.

Future work: `GhostBox` generalization, or sweep `Blocked()` during transition agents.

---

## Implementation phases

### Phase 1 — Authoring + spec

- [ ] Duplicate-key policy: Tombi + expander override merge
- [ ] DTO + schema: `transition_props`, `transition_duration`, `transition_easing`, `animation_sound`
- [ ] Expand-time `TransitionEndpoint` capture from `# if` overrides
- [ ] Attach spec to `ReactiveFragment`; tests in `sceneparam` / `sceneio`

### Phase 2 — Runtime slide

- [ ] `internal/statetransition` + `DynamicBody` registration from `scenestate.Instantiate`
- [ ] Wire `HandleInteract` → `Begin` vs instant refresh
- [ ] `Update` in app loop; easing functions
- [ ] Preview scene: movable wall object + `scenes/preview/` visual check

### Phase 3 — Sound + polish

- [ ] `animation_sound` hook in app
- [ ] Reverse mid-flight (optional, door parity)
- [ ] Commit-at-end vs permanent Xform decision + tests

### Phase 4 — Later

- [ ] Scalar props (brightness) with `interactlight`-style lerp
- [ ] Collisions / ghosting
- [ ] Size props without full `Touch()` each frame

---

## Testing

| Layer | Cases |
| ----- | ----- |
| Expand | `# if` override diff; duplicate key merge; empty `transition_props` → no spec |
| Load | Schema accepts new fields; invalid easing rejected |
| Runtime | Wall `pos_x` 0→1 over duration; mid-toggle blocked or reversed; store true while visually mid-lerp |
| Integration | Front-office-style reactive object; toggle does not regress instant lamp/light behaviour |
| Visual | `go run ./cmd/preview` on preview scene (AGENTS.md) |

---

## Files (expected touch list)

| Area | Files |
| ---- | ----- |
| Expand / spec | `internal/sceneparam/loop_reactive.go`, `reactive.go`, new `transition.go` |
| IO | `internal/sceneio/toml.go`, `reactive.go` |
| Scene | `internal/scene/reactive.go`, `dynamic.go` |
| Runtime | `internal/statetransition/` (new), `internal/scenestate/manager.go`, `internal/app/app.go`, `internal/app/state.go` |
| Sound | `internal/app/` (new hook file or extend existing) |
| Schema | `schemas/scene.schema.json`, `tombi.toml`, `schemas/README.md` |
| Examples | `scenes/objects/movable-wall.toml`, `scenes/preview/movable-wall.toml` |

---

## Open questions

1. **Default duration** when `transition_duration` omitted but `transition_props` set — proposed `0.6s`.
2. **Multiple `# if` keys** on one primitive — one agent per state key or merge into one timeline when a single toggle flips one key only (likely one agent per `(primitive, stateKey)`).
3. **`# if not open`** — treat as endpoint when key false; spec builder must handle negated branches.
4. **Instanced includes** — transition spec lives on template fragment; instances share agents or per-instance (likely per `ReactiveFragment` span instance — same as today’s state scope).

---

## Example (full movable wall object)

```toml
[props]

[state]
open = false

[[box]]
material = "diffuse"
albedo = [0.4, 0.38, 0.35]
depth = 0.1
width = 1
height = 2
pos_x = 0
pos_y = 1
pos_z = 0
transition_props = ["pos_x"]
transition_duration = 0.8
transition_easing = "ease-in-out"
animation_sound = "wall-slide"
hint = "wall"
on_use = "toggle(open)"

# if open
pos_x = 2
# endif
```

Toggle slides the panel 2m along X; ceiling lights and desk lamp behaviour elsewhere stay on the instant reactive path.
