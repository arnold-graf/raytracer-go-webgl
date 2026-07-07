# Plan: Same-Session Portals (Non-Euclidean Worlds + Lazy Loading)

## Status: PROPOSED

## Goal

Add **true portals**: see through a framed opening into another space, walk
through it, and emerge on the other side — all in one uninterrupted play
session. Portals may link:

- **Different cells** (separate TOML files, lazy-loaded as needed), or
- **The same cell** (two openings in one level, creating non-Euclidean topology
  from the player's point of view).

This is **in addition to** the existing exit-button / cube-lab system
(`internal/app/portal.go`, `texture.Capture_*`). Those stay: fade, five-view
capture, scene swap, and static textures on the Cube interior. The new system
uses **ray continuation** for in-world links where parallax and walk-through
matter. The two features solve different problems and share only low-level
pieces (planes, holes, instancing).

Same non-negotiables as other plans: fidelity first, simplest code that fits the
megakernel, safe rollout (scenes without `[[portal]]` must behave exactly as
today — including `exit_portal` and capture walls unchanged).

## What already exists

| Piece | Location | Role today |
| ----- | -------- | ---------- |
| Scene transition portal | `internal/app/portal.go` | Fade → capture 5 views → swap TOML → spawn at `[[point]]` |
| Capture textures | `internal/texture/capture.go`, `texture.wesl` | Static screenshots on cube interior walls |
| TLAS / BLAS instancing | `internal/webgpu/instance.go`, `bvh.wesl` | Shared template trees + placement transforms |
| Box holes (doorways) | `scene.Hole`, `holes` GPU buffer | CSG openings in walls; collision respects them |
| Plane index lists | `plane_idx` in shader | Infinite planes outside the BVH |
| Scene includes | `[[include]]` in TOML | Reusable object files — natural cell authoring format |

### Two portal systems (both kept)

| | **Exit button + Cube** (existing) | **Live `[[portal]]`** (this plan) |
| --- | --- | --- |
| Purpose | Narrative scene transition; “step into the Cube, emerge elsewhere” | In-world connectivity; non-Euclidean layout; optional lazy-loaded cells |
| View | Five static captures baked onto Cube walls | Ray continuation through the opening (live parallax) |
| World | Full scene swap (`LoadDeps` + spawn at `[[point]]`) | Same session; multiple cells active on GPU |
| Trigger | `exit_portal` use action + fade | Walk / look through linked frame |
| Code | `portal.go`, `portal_capture.go`, `Capture_*` textures | `WorldSession`, portal hop in shader |

No migration required. Authors pick per beat: Cube transition for set-piece
teleports; `[[portal]]` for spatial puzzles and connected levels.

---

## Mental model: cells + links

The world is a **graph of cells**, not one Euclidean map.

- **Cell** — a loadable chunk of geometry, usually one TOML file (or a named
  region inside a file). Has its own local coordinate system and a pre-built
  BLAS.
- **Portal** — a **link** between two oriented rectangles (portal frames) on two
  cells. Carries the rigid transform from side A's frame to side B's frame.
- **Active set** — cells currently merged into the GPU session (see lazy loading).

From the player's perspective, walking through a portal remaps position and
orientation; the Euclidean distance between cell origins is irrelevant. Two
portals in the same cell linked to each other produce a classic infinite
corridor.

```text
  Cell A                    Cell B
 ┌────────┐                ┌────────┐
 │  [P1]──┼─── link ───────┼──[P2]  │
 │        │                │        │
 └────────┘                └────────┘

  Same cell (non-Euclidean):
 ┌──────────────────┐
 │  [P1]══════[P2]  │   walk through P1 → emerge at P2 facing back toward P1
 └──────────────────┘
```

---

## Design decisions (locked)

| Decision | Choice | Rationale |
| -------- | ------ | --------- |
| GPU structure | **Cell-local BLAS + portal hop** | Reuses instancing; lazy load = add/remove a template + TLAS slot, not rebuild one giant BVH |
| Portal depth limit | **3** | Primary view + portal-in-portal effects (mirrors, nested frames, “hall of portals”) without unbounded cost |
| Sidedness | **Two-sided** | Both faces of a portal frame are walkable and see-through; each side uses the same link (A↔B) |
| Lighting across cells | **v1: none** | Each cell is self-lit; no light transport through portals in the first ship |
| AO across cells | **v1: per-cell bake** | No probe bleeding through links; good enough at our resolution |
| Max active cells | **4–8** (tunable) | Memory budget; LRU eviction beyond cap |

---

## Architecture

### Runtime: `WorldSession`

New layer above `scene.Scene` (lives in `internal/world` or `internal/scene`):

```text
WorldSession
  cells     map[CellID] → *LoadedCell   // CPU scene + packed BLAS template id
  links     []PortalLink                // parsed from [[portal]] in TOML
  active    set[CellID]                 // currently on GPU
  player    CellID                      // which cell the player is inside
  merged    GPUMergedWorld              // what the renderer uploads each frame
```

`LoadedCell` holds:

- `*scene.Scene` (or a cell-local slice of one)
- GPU template index (BLAS roots, prim offsets — same as instancing today)
- Optional per-cell AO volume handle
- Load generation / last-used time (for LRU)

The game's `render.View` points at `WorldSession` instead of a single `Scene`
once portals are enabled. Static scenes without portals can keep the current
one-scene path indefinitely.

### GPU: cell as BLAS template, world as TLAS + portal table

Extend the existing instancing path:

1. Each **loaded cell** is one **BLAS template** (built once at load).
2. **Active cells** are TLAS placements at their authored world offsets (often
   identity for cell-local authoring — the portal link provides the real
   connectivity).
3. A separate **`portals` storage buffer** lists link records the shader reads on
   portal hits.

Portal link record (conceptual WGSL layout):

```wgsl
struct PortalLink {
    // Side A: which cell + which portal prim index + frame (origin, normal, tangent, bitangent)
    cell_a: u32,
    prim_a: u32,
    frame_a: mat4x4,   // portal A local → world (or packed vec4×3)

    // Side B: same
    cell_b: u32,
    prim_b: u32,
    frame_b: mat4x4,

    // Precomputed: world-space A → world-space B for rays emerging from A
    a_to_b: mat4x4,
    b_to_a: mat4x4,
};
```

Two-sided: hitting prim from **either** normal direction uses the same link;
pick `a_to_b` or `b_to_a` based on which side of the plane the ray entered.

### Shader: portal hop in the trace loop

Add `PRIM_PORTAL` (or a flag on plane/box face prims) and extend `Hit`:

```wgsl
struct Hit {
    t: f32,
    idx: u32,
    kind: u32,
    inst_idx: u32,
    portal_link: u32,  // 0xffffffff when not a portal
};
```

In `ray_color` (and shadow rays — portals are **opaque blockers** for shadow
unless we explicitly carve them out later):

1. `nearest_hit` as today (TLAS → BLAS, or static BVH).
2. If hit is a portal and `portal_depth < 3`:
   - Nudge origin off the surface (`SURFACE_EPSILON`).
   - Transform `(ro, rd)` with `a_to_b` or `b_to_a` (position + direction;
     direction uses the linear 3×3 part only).
   - Increment `portal_depth`, continue trace from the linked cell's space.
3. If `portal_depth == 3`, treat as emissive black / fog / last solid color —
   artist-tunable fallback to avoid silent failures.

**Rectangle clipping:** portal prims are finite (box face with hole, or bounded
plane quad). Rays that hit the frame but outside the opening hit solid border;
only the opening triggers a hop.

**Reflections:** mirror segments pass through portals like primary rays, sharing
the same depth counter (so a mirror facing a portal-in-portal stack respects the
limit of 3).

### CPU: walking through

Each frame, after movement integration:

1. Test player capsule against each **active** portal opening (same rectangle
   as the shader).
2. On crossing (signed distance flips past the plane):
   - `pose' = linkTransform * pose` (position + yaw; pitch if desired).
   - Update `WorldSession.player` to the destination cell.
   - Fire a one-shot “portal crossed” event (audio, optional reverb cell id).

Collision (`scene.Blocked`) uses the **merged** active geometry. Cells not in
the active set are unreachable without crossing a link, so their collision
volumes are not needed until loaded.

---

## Lazy loading and unloading

### Load triggers

| Event | Action |
| ----- | ------ |
| Session start | Load **spawn cell** |
| Player within `prefetch_distance` of a portal | Load linked cell (async) |
| Portal opening visible (frustum ∩ rectangle) | Ensure linked cell loaded |
| Linked cell referenced by a loaded cell's portal table | Prefetch (graph walk) |

`prefetch_distance` ≈ 10–20 m; tunable per portal in TOML.

### Unload rules

Unload a cell when **all** are true:

- Not the player's current cell
- No portal path from player to this cell within **H hops** (default H = 2)
- Not visible through any portal in the active set this frame
- Eviction candidate if over `max_active_cells` (LRU by last use)

Unload = remove TLAS placement + drop CPU `LoadedCell` + release GPU template
if refcount hits zero (two portals can share a cell).

### Async load

Cell load (TOML parse → `Prepare` → pack BLAS → optional AO bake) may take tens
of ms. While loading:

- Portal shows a neutral “shimmer” or the sealed back-face color (v1)
- Player cannot cross until load completes (invisible collision plane)
- Prefetch makes this rare in practice

---

## TOML authoring

### Cell identity

Cells are referenced by path (relative to scene root) or by `id` on a top-level
scene file:

```toml
[cell]
id = "office_wing_a"
file = "office-sunset/wing-a.toml"
```

A single TOML can host multiple cells via named groups later; v1 is **one file =
one cell**.

### Portal definition

```toml
[[portal]]
id = "lab_to_server_room"
# Side A — this file
a.cell = "self"                    # or "office_wing_a"
a.prim = "portal_out"              # names a [[plane]] or box face tag

# Side B — another cell file
b.cell = "office-sunset/server-room-1.toml"
b.prim = "portal_in"

# Optional overrides (default: derive from prim geometry)
# flip = false
# prefetch_distance = 15.0
```

Primitives gain an optional `portal_id` or are referenced by `[[point]]`-style
string ids on planes / box faces:

```toml
[[plane]]
id = "portal_out"
material = "diffuse"
# ... geometry ...
portal = true   # marks as portal surface (opening defined by paired hole box)
```

For box walls, reuse `[[hole]]` to cut the opening; the portal frame is the
hole rectangle.

### Same-cell link (non-Euclidean)

```toml
[[portal]]
id = "infinity_corridor"
a.prim = "portal_a"
b.prim = "portal_b"
# both sides omit b.cell / default to "self"
```

### Spawn points after cross

```toml
[[point]]
id = "server_room_portal_spawn"
pos = [2.0, 0.0, -1.0]
floor_y = 0.0
yaw = 3.14159
```

Optional on portal: `b.spawn = "server_room_portal_spawn"` for slight nudge off
the exit plane (avoids immediate re-cross).

---

## Relationship to `plans/large-maps.md`

Large maps and portals solve different problems; they compose well:

| Concern | Large maps plan | Portals plan |
| ------- | --------------- | ------------ |
| Distant geometry | Panorama, LOD impostors | Not loaded until linked |
| Memory | Streaming buildings | Cell LRU cap |
| Traversal | Shallow TLAS | **Same** — cell = BLAS template |
| Long view rays | Terrain march length | Only active cells in TLAS |

A 2 km outdoor cell can stay unloaded until the player enters a bunker portal;
inside, a small non-Euclidean cell graph can use depth-3 recursion without
loading the whole map.

---

## Implementation phases

### Phase 1 — Data model + walk-through (no shader hop)

- Parse `[[portal]]` in `sceneio`
- Build `PortalLink` table (frames, `a_to_b` / `b_to_a` matrices)
- CPU crossing test + pose remap
- **Gate:** infinite corridor test scene (two portals, same TOML); player walks
  through repeatedly without rendering the far side correctly yet

### Phase 2 — Shader portal hop (single cell)

- `PRIM_PORTAL` / link buffer
- Ray continuation in `ray_color`, `portal_depth` ≤ 3
- Two-sided entry
- **Gate:** look through portal A, see room through B; parallax when strafing;
  `gpuprof` frame time within budget for 1-hop

### Phase 3 — `WorldSession` + multi-cell BLAS

- Cell = BLAS template; active set = TLAS placements
- Merge upload path in `webgpu/cache.go`
- Cross-cell links
- **Gate:** two TOML cells, portal between them, both visible without scene
  swap

### Phase 4 — Lazy load / unload

- Prefetch, async load, LRU eviction
- Loading shimmer / block-until-ready
- **Gate:** three-cell chain; middle cell unloads when player is two hops away

### Phase 5 — Polish

- Portal-in-portal-in-portal (depth 3) test scene
- Reflections through portals
- Sealed backfaces (no seeing the “rear” of the destination cell)
- Document author guidelines: when to use `[[portal]]` vs exit button / captures

---

## Performance notes

- **Off-screen loaded cells:** no extra primary rays unless a portal or mirror
  points at them. Unloaded cells: zero GPU cost.
- **Portal depth 3:** worst case multiplies trace work for pixels looking through
  nested portals — acceptable at ~100k pixels if each hop is a BVH traverse +
  shade, not a full scene scan.
- **Shadow rays:** v1 does not traverse portals (treat portal opening as opaque
  to shadow rays from the far side). May cause minor lighting inconsistencies at
  thresholds; revisit if visible.
- **Profiling:** extend `PROF_*` with `PROF_PORTAL_HOPS` for HUD / `gpuprof`.

---

## Test scenes (proposed)

| Scene | Exercises |
| ----- | --------- |
| `scenes/tests/portal-corridor.toml` | Same-cell A↔B, walk + look |
| `scenes/tests/portal-recursive.toml` | Three aligned portals, depth 3 |
| `scenes/tests/portal-two-cells/` | Two files, lazy load second |
| `scenes/tests/portal-mirror.toml` | Mirror reflecting a portal view |

---

## Open questions (non-blocking for Phase 1)

1. **Terrain in cells** — does each cell own its terrain volume, or is terrain
   global with cells as interior only? (Recommend: per-cell terrain for v1.)
2. **NPCs across cells** — do agents migrate with the player cell, or stay in
   their authored cell until loaded?
3. **Hot reload** — reloading a cell TOML while active should invalidate that
   cell's BLAS only, not the whole session.
4. **Audio** — `internal/probe` reverb zones per cell id (natural fit with lazy
   load).

---

## Coexistence with exit button and Cube lab

The **exit button** (`scenes/objects/exit-button.toml`, `exit_portal` use
handler) and **Cube interior** (`scenes/office-sunset/objects/cube.toml`,
`capture_forward` … `capture_down` textures) are first-class features and stay
as-is. Do not remove or reroute them to `[[portal]]`.

- **Exit button** — intentional hard cut: capture Manhattan (or wherever), fade,
  load `office-sunset/index.toml`, spawn at `cube_lab_1`. Good for story beats
  and loading heavy scenes without keeping both in memory.
- **Cube walls** — display those captures as a diegetic “outside world on screens”
  inside a small room. One-way, static, no walk-through.
- **`[[portal]]`** — separate TOML and code path for continuous space and
  same-session multi-cell play.

Optional future overlap: a level could place a live `[[portal]]` next to a Cube
door for contrast, but the exit-button pipeline does not depend on this plan.
