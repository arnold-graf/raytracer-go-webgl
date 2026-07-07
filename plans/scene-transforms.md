# Plan: Unified `transform_origin` for Placement & Rotation

## Status: PROPOSED

## Goal

One transform rule for **every** place geometry is placed or rotated: primitives,
`[[include]]` composites, `[[document]]`, doors, NPC spawns, and (later)
`[[group]]` frames. Authors think in terms of:

- **`at` / position** — where the object's **`transform_origin`** lands in the
  parent (or world, for top-level primitives).
- **`rotate_*`** — spin around that origin point.
- **`transform_origin`** — which local point is the anchor (default: **center**).

Replace today's split semantics (includes rotate about local `(0,0,0)`;
primitives rotate about per-type geometric centers; README documents a `pivot`
field that is not implemented) with a single, overridable model.

Same non-negotiables as other plans: rendering and collision stay in sync via
`Xform`; migration keeps scenes visually identical where possible.

---

## The unified formula

All placement transforms use:

```text
world(p) = R · (p_local − origin) + at
```

| Symbol | Meaning |
| ------ | ------- |
| `p_local` | Point in the object's local space (primitive coords or sub-scene coords) |
| `origin` | Resolved `transform_origin` in that same local space |
| `R` | Euler rotation, **X→Y→Z**, degrees (same as today) |
| `at` | Where `origin` lands in the parent/world frame |

**Identity check:** `world(origin) = at` for any `R`. The anchor stays on `at`;
everything else orbits it.

### Equivalent matrix form (implementation)

```go
// scene/placement.go (new)
func PlacementTransform(degX, degY, degZ float64, at, origin vec.V) *Transform {
    fwd := rotation(degX, degY, degZ)
    // world = fwd*local + (at - fwd*origin)
    return &Transform{fwd: fwd, inv: fwd.transpose(), t: at.Sub(fwd.mul(origin))}
}
```

`NewTransform(deg, pivot)` for in-file primitive articulation (lamp arms) becomes
`PlacementTransform(deg, pivot, pivot)` — rotate in place with `at = pivot`.

Top-level primitives with only `rotate_*` and no separate `at`: **`at = (0,0,0)`**
in scene space, `origin` = resolved center (current behaviour, made explicit).

---

## `transform_origin` in TOML

### Syntax

```toml
# Default when omitted — rotate about bounds/geometric center
transform_origin = "center"

# Explicit local-space anchor (corner, hinge, file origin, …)
transform_origin = [0, 0, 0]
transform_origin = [0.5, 0.9, 0.3]
```

String form: only `"center"` in v1.  
Array form: three floats `[x, y, z]` in the object's **local coordinates** (same
space as `pos_x` / `at`).

### Where it applies

| Authoring surface | Position fields | `transform_origin` | `rotate_*` |
| ----------------- | --------------- | ------------------ | ---------- |
| `[[sphere]]` … `[[lens]]` | `pos_*`, `center`, `cx`, … | Per-primitive | Per-primitive |
| `[[plane]]` | `normal`, `d` | Per-primitive (see note) | Per-primitive |
| `[[document]]` | `pos_x/y/z` | Yes | Yes |
| `[[include]]` | `at` | Yes (default center of loaded sub-scene) | Yes |
| `[[door]]` | (from spec) | Yes | Yes |
| `[[npc]]` spawn | `pos`, `yaw` | Yaw-only v1; origin for offsets | Partial |
| `[[light]]` / `[[campfire]]` | `pos` / `center` | Optional; rotation rare | If added |
| `[[group]]` (future) | `at` | Yes | Yes |

**Planes:** infinite; `"center"` is undefined unless the file also has finite
helper geometry. Require explicit `transform_origin` or use `[0,0,0]` when
rotating planes. Document in gotchas.

**Lights / campfires:** positions transform with `ToWorld` on include today; add
`transform_origin` so rotated point lights match the same rule when `rotate_*`
are added to light tables.

---

## Resolving `"center"`

### Per primitive (in a single file, before any include)

| Kind | `"center"` resolves to |
| ---- | ------------------------ |
| Box | `(min + max) / 2` |
| Cylinder | `(cx, (ymin+ymax)/2, cz)` |
| Sphere | `center` |
| Cone | `(cx, (ybase+ytip)/2, cz)` |
| Torus | `center` |
| Ring | `(cx, cy, cz)` |
| Lens | midpoint of lens AABB in local space |
| Document box | center of `[pos, pos+(w,h,d)]` |

### `[[include]]` / object files

`"center"` = **centroid of the local geometry AABB** of the loaded sub-scene
(after template `params`, before the include's instance transform):

- Union local bounds of spheres, boxes, cylinders, cones, tori, rings, lenses
  (same coverage as `forEachTemplateBounds` in `internal/scene/instance.go`).
- **Exclude** infinite planes from the union (they would dominate).
- **Exclude** `[[light]]` / `[[campfire]]` from bounds (optional; they are point
  sources — include in v2 if needed).
- If no finite geometry: error at load time, or fall back to `[0,0,0]` with a
  loader warning (prefer error for includes).

Expose as:

```go
func LocalBoundsCenter(s *Scene) (vec.V, bool)
```

---

## Semantics change for `[[include]]` (why migration is needed)

### Today

```text
world(p) = R · p + at        // rotates about local (0,0,0); (0,0,0) maps to at
```

### Proposed (default `transform_origin = "center"`)

```text
world(p) = R · (p − C) + at   // C = local bounds center; C maps to at
```

`at` **changes meaning** from “where the file origin goes” to “where the object's
**center** goes”. Visual parity when switching defaults:

```text
at_new = at_old + R · C
```

When `R = I` (no rotation): `at_new = at_old + C` — even unrotated includes shift
unless they already targeted center visually.

Objects that **intentionally** use file origin `(0,0,0)` as pivot (staircase
bottom-front corner, Manhattan switchbacks) keep old behaviour with:

```toml
transform_origin = [0, 0, 0]
# at unchanged
```

---

## Composition (nested includes & primitives)

**Include merge** (unchanged structure, new transform builder):

```text
o.Xform = xf_parent.Compose(xf_child)   // child built with PlacementTransform
```

**Primitive inside included file** with its own `rotate_*`:

```text
world(p) = R_inc · ( R_prim · (p − o_prim) + (at_inc − o_inc) … )   // via Compose
```

Each level carries its own resolved `origin`. Lamp arms: primitive
`transform_origin` = hinge point (explicit `[x,y,z]`). Desk composite: include
`transform_origin = "center"`, children placed with `at` in parent local space.

### Future: `[[group]]`

```toml
[[group]]
at = [8.0, 0.2, 9.0]
rotate_y = 20
transform_origin = "center"   # default

  [[group.include]]
  file = "objects/simple-table.toml"

  [[group.include]]
  file = "objects/laptop.toml"
  at = [0.0, 0.9, 0.3]
```

One shared `PlacementTransform` for the group; children only need `at` offsets.

---

## Implementation phases

### Phase 1 — Core API

- `PlacementTransform(deg, at, origin)`
- `ResolveTransformOrigin(kind, scene, primitive, explicit)` → `vec.V`
- `LocalBoundsCenter(*Scene) (vec.V, bool)`
- Unit tests: `world(origin) == at`; parity formula `at_new = at_old + R·C`

### Phase 2 — Loader wiring

- Add `TransformOrigin` to `transformDTO` (string or `[3]float64` via custom decode)
  and `includeDTO`.
- Primitives: replace `buildAbout(center)` with `PlacementTransform(..., at=0, origin)`.
- Includes: replace `NewInstanceTransform` with `PlacementTransform` + resolved `C`.
- Documents / doors: same helper.
- Fix `SCENES.README.md` (`pivot` → `transform_origin`).

### Phase 3 — Migration script + scene audit

- `cmd/migrate-transform-origin` (see below).
- Run on `scenes/`, commit updated `at` values + `transform_origin = [0,0,0]` tags.
- `go test ./...` + spot-check `cmd/preview` on server-room, Manhattan stairs.

### Phase 4 — `[[group]]` (optional follow-up)

- TOML nesting; compose one group transform before merging children.

---

## Migration script: `cmd/migrate-transform-origin`

### Purpose

Rewrite scene TOML so that **after** switching the loader to center-default
`transform_origin`, the world placement of every `[[include]]` stays the same
(within rounding). Tag origin-pivot objects so stairs and similar do not drift.

### CLI

```bash
# Dry run (print unified diff, no writes)
go run ./cmd/migrate-transform-origin -dry-run scenes/

# Apply (rewrites files in place)
go run ./cmd/migrate-transform-origin scenes/

# Single file
go run ./cmd/migrate-transform-origin scenes/office-sunset/server-room-1.toml
```

Flags:

| Flag | Default | Purpose |
| ---- | ------- | ------- |
| `-dry-run` | false | Report only |
| `-round` | 3 | Decimal places for `at` |
| `-origin-list` | built-in | Extra glob paths forced to `[0,0,0]` |

### Algorithm (per `[[include]]` block)

1. **Parse** the parent TOML without executing the full scene merge (preserve
   comments and ordering — use a TOML-aware editor or decode→patch→encode with
   `BurntSushi/toml` meta/primitive where possible; if not, line-oriented patch
   on `[[include]]` stanzas only).

2. **Load sub-scene** with the include's `params` (same as runtime `load(incPath,
   inc.Params, …)`).

3. **Compute** `C = LocalBoundsCenter(sub)`. If `!ok`, log warning and skip.

4. **Build** `R` from `rotate_x/y/z` (0 if omitted).

5. **Origin-pivot allowlist** — if any match, **do not** change `at`; **add**
   `transform_origin = [0, 0, 0]` if missing:

   - Include `file` path ends with `objects/staircase.toml`
   - Include `file` listed in `-origin-list`
   - Include already has `transform_origin = [0, 0, 0]` (idempotent)

   Built-in list (extend as discovered):

   ```text
   **/objects/staircase.toml
   ```

6. **Center-default migration** (everything else):

   ```text
   at_new = at_old + R.Multiply(C)    // R · C as direction/point transform
   at_new = round3(at_new)            // each component, round half away or to fixed
   ```

   Rounding helper:

   ```go
   func round3(v vec.V) vec.V {
       const scale = 1000.0
       return vec.V{
           X: math.Round(v.X*scale) / scale,
           Y: math.Round(v.Y*scale) / scale,
           Z: math.Round(v.Z*scale) / scale,
       }
   }
   ```

   Do **not** emit `transform_origin = "center"` when it is the default (keeps
   files short). Optionally emit a one-line comment on first migration.

7. **Terrain follow:** if `follow_terrain = true`, only migrate the **authored**
   `at` in the file; runtime Y snapping is unchanged (still applied after
   transform build). Document that `at.y` offsets remain offsets above terrain.

8. **Verify** (dry-run or post-apply):

   - Load parent scene **before** and **after** with old vs new loader (feature
     flag during transition), or numerically: for each include, pick test points
     `{C, 0, corners of local AABB}` and assert `world_old(p) ≈ world_new(p)` within
     `1e-3`.

9. **Report** table:

   ```text
   file:line  include → objects/foo.toml  at [1,2,3] → [1.8,2,3.1]  (center C=[0.4,0,0.1])
   file:line  include → objects/staircase.toml  tagged transform_origin=[0,0,0]
   ```

### Primitives in migration

Per-primitive **default is already center** — no `at` migration. Script only:

- Adds explicit `transform_origin = [0, 0, 0]` where authors relied on origin
  pivot (grep for comments / manual list).
- Removes stale `pivot = …` if any appear in files once README is fixed.

### Objects (included files)

Object files themselves do not get `at` migrated. Optional pass: add header
comment documenting each file's natural origin vs `"center"`:

```toml
# transform_origin: use [0,0,0] on [[include]] — file origin is bottom-front corner.
```

(`objects/staircase.toml` already documents this.)

### Idempotency

- Second run on center-default includes: `C` unchanged; `at_old + R·C` stable
  after `round3` → no further edits.
- Origin-tagged includes: skip `at` math.

### Rollout order

1. Land `PlacementTransform` + `transform_origin` behind **no** default change
   (`TRANSFORM_ORIGIN_CENTER=1` env or build tag) for one PR.
2. Run migration script on `scenes/`.
3. Flip default to `"center"` in loader.
4. Remove flag.

---

## Authoring cheat sheet (after migration)

| Intent | TOML |
| ------ | ---- |
| Place object; spin around its middle | omit `transform_origin` (default `"center"`) |
| Stair flight; pivot at bottom-front | `transform_origin = [0, 0, 0]` on the `[[include]]` |
| Lamp arm hinge | `transform_origin = [0, 0.05, 0]` on the `[[cylinder]]` |
| Lay memo on rotated desk | document `pos_*` in desk file; desk include uses center default |

**`at` on include:** where the object's **center** (or overridden origin) sits in
the parent scene.

**Child props on a rotated desk:** put them in one grouped file, or nest includes
(Phase 4), so they share one `PlacementTransform`.

---

## Tests

| Test | Gate |
| ---- | ---- |
| `world(origin) == at` for random `R`, `origin`, `at` | Unit |
| Box/cylinder primitive rotate: unchanged images vs baseline `cmd/preview` | Visual |
| Include parity: `at_new = at_old + R·C` matches old loader for sample scenes | Unit + preview |
| Staircase with `transform_origin = [0,0,0]` matches old Manhattan placement | Preview |
| Migration script dry-run → zero diff on second run | Script test |
| `LocalBoundsCenter` on empty sub-scene errors | Unit |

---

## Schema & docs

- `schemas/scene.schema.json`: add `transform_origin` to primitive `allOf` and
  `include` table (`oneOf`: enum `"center"`, or `[3]number`).
- `SCENES.README.md`: replace **Per-primitive transforms** / **include** sections
  with this plan's formula and examples.
- Object file headers: document recommended `transform_origin` override for
  non-center file origins.

---

## Open questions

1. **Documents** — `DocumentRestTransform` has its own layout logic; fold fully
   into `PlacementTransform` or keep as a thin wrapper with shared `origin`?
2. **Instanced includes** (`instance = true`) — `transform_origin` on placement
   only; BLAS stays local. Same `at` migration formula on TLAS instance record.
3. **Negative scale** — not supported; if ever added, `transform_origin` must
   participate in the same formula.

---

## Related plans

- `plans/dynamic-objects.md` — movers mutate transforms; same `PlacementTransform`
  keeps GPU/CPU in sync.
- `plans/portals.md` — portal links use the same rigid frame math at runtime.
