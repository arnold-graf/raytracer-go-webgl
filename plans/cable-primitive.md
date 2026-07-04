# Plan: Cable Primitive (PBD Rope)

## Status: PROPOSED

## Goal

Add scene-authored **cables** — power cords, hoses, Ethernet runs — that drape
realistically under gravity, respect endpoint position and tangent constraints,
route through optional **waypoints**, and settle over floors and obstacles.
Shape is solved at **load time** with position-based dynamics (PBD); the
renderer sees static geometry afterward.

## What already exists

- Analytic primitives with CPU `Intersect`, `Normal`, `WorldBounds`, and GPU
  packing (`PRIM_SPHERE` … `PRIM_LENS` in `trace.wgsl`).
- `scene.GroundHeight` / `GroundNormal` for floors, terrain, and flat box tops
  (`internal/scene/physics.go`).
- Per-primitive `Cylinder` with `open_min` / `open_max` for hollow tubes
  (`internal/scene/primitives.go`).
- `scene.Touch()` invalidation and BVH rebuild on geometry changes
  (`internal/scene/scene.go`, `internal/webgpu/cache.go`).
- `scene.DynamicBody` index ranges for runtime spawn/despawn patterns
  (`internal/scene/dynamic.go`).

Missing today: a **scene-wide CPU raycast** for obstacle contact beyond
`GroundHeight`, and any curve / swept-tube primitive.

## Design

### 1. Authoring (`[[cable]]` in TOML)

```toml
[[cable]]
start = [1.0, 0.9, 2.0]
end = [3.2, 0.05, 4.5]
start_dir = [0.0, -1.0, 0.0]   # tangent leaving the start anchor
end_dir = [0.0, 0.0, 1.0]      # tangent arriving at the end anchor
waypoints = [                    # optional routing hints (in order)
  [2.0, 0.5, 3.0],
  [2.5, 0.1, 3.8],
]
radius = 0.008
stiffness = 0.35               # 0 = limp, 1 = stiff
segments = 32                  # PBD particle count − 1
material = "rubber_black"        # diffuse surface (same as other prims)

  [cable.sim]
  iterations = 120
  gravity = [0.0, -9.8, 0.0]
  pin_waypoints = true           # default: cable passes through each waypoint
  waypoint_strength = 1.0        # when pin_waypoints = false: soft pull (0..1)
```

**Waypoints** are an ordered list of world-space points between `start` and
`end`. The full anchor chain is `start → waypoints… → end`.

- `pin_waypoints = true` (default): one particle is pinned at each waypoint
  each iteration — the cable is routed through those coordinates; sag and drape
  happen only *between* anchors.
- `pin_waypoints = false`: waypoints act as soft attractors (`waypoint_strength`)
  that pull the nearest interior particle toward each point without fixing it —
  useful for nudging a run along a wall or over a desk edge without a hard kink.

Parsed in `sceneio`; stored on `scene.Scene` as `[]Cable` (authored spec) until
baked.

### 2. PBD rope solver (`internal/scene/cable.go`)

Discretize the span into `segments` edges and `segments + 1` particles `p[0…N]`.

**Anchor chain:** `anchors = [start, waypoint₀, …, waypointₖ, end]`.

**Particle allocation:** split `segments` across the `len(anchors) − 1` spans
proportionally to each span's chord length (so a long floor run gets more
particles than a short vertical drop). Record the particle index at each anchor;
these indices are the pin targets for start, end, and each waypoint.

**Initialization:** for each span, Hermite (or cubic Bézier) blend between the
span's endpoints using `start_dir` / `end_dir` only on the first and last span;
interior span tangents are estimated from chord directions. Seed interior
particles along the resulting polyline.

**Each iteration:**

1. **Predict** — apply gravity to free particles (not pinned).
2. **Distance** — enforce rest edge length for every segment (inextensible rope).
3. **Bending** — for each interior triplet `(p[i−1], p[i], p[i+1])`, pull the
   middle particle toward the line between neighbors; blend strength by
   `stiffness` (0 = skip, 1 = full enforcement).
4. **Anchor pins** — fix particles at `start`, `end`, and (when
   `pin_waypoints`) each waypoint index.
5. **Waypoint attractors** — when `pin_waypoints = false`, for each waypoint
   pull the nearest free particle toward it by `waypoint_strength`.
6. **Tangent pins** — on the first span, optionally constrain the particle after
   `start` along `start_dir`; on the last span, constrain the particle before
   `end` along `end_dir`.
7. **Collision** — project particles onto walkable surfaces and out of solids
   (see §3).

Run `iterations` times at load; output a polyline `[]vec.V`.

### 3. Scene collision

**v1 — floor draping**

For each free particle, set `y = max(y, GroundHeight(x, z, y) + radius)`.

**v2 — obstacles**

Add `scene.Raycast(r, maxT) (hit vec.V, normal vec.V, ok bool)` over static
primitives (boxes, cylinders, cones, spheres, terrain). Use downward rays from
each particle plus short sphere-sweep checks along segments to catch desk lips
and vertical faces.

Skip dynamic-body geometry (same policy as `GroundHeightStatic`).

### 4. Bake to render geometry

**v1 — cylinder chain (no new GPU primitive)**

For each polyline edge `(p[i], p[i+1])`, append a `Cylinder` aligned to the
segment (local Y along the edge, `YMin`/`YMax` spanning the chord, `Radius =
radius`, `open_min` + `open_max` so caps are open between segments). One
diffuse `Surface` per cable.

Record owned `[start, end)` ranges on a `CableBake` entry (or reuse
`DynamicBody` if cables can move at runtime later).

**v2 — `PRIM_CABLE` poly-capsule (optional)**

Single primitive holding `Points[]`, `Radius`, `Surface`. CPU `Intersect` loops
segments (capsule tests). GPU `hit_cable` in `trace.wgsl`; pack control points
into the primitive buffer. Fewer BVH leaves for long cables. Defer until v1
draping is validated.

### 5. Load-time integration (`sceneio`)

After all static geometry is merged:

1. For each `[[cable]]`, run the PBD solver against the assembled `Scene`.
2. Append baked cylinders (or `Cable` primitives).
3. Do not retain PBD state unless endpoints are dynamic.

`scene.Touch()` once after baking (normal load path).

### 6. Dynamic endpoints (optional, later)

If start/end move at runtime (dragged plug), store the authored spec + solver
params, rebake cylinders each change, trim/re-append owned ranges, `Touch()`.
Same pattern as spyglass spawn/despawn.

## Fidelity / safety gates

- Unit tests: distance + bending constraints on a flat plane (known sag).
- Unit tests: pinned endpoints and tangent directions preserved after solve.
- Unit tests: cable with two waypoints passes within ε of each pinned point;
  soft-attractor mode pulls nearest particle toward waypoints without exact pin.
- Integration test: short cable between two box tops in a minimal TOML scene;
  baked cylinder count matches `segments`, centers lie above `GroundHeight`.
- Visual: server-room or office scene with a desk-to-floor power cord routed
  through a wall hook waypoint.

## Suggested order

1. `Cable` spec type + PBD solver + unit tests (flat ground only).
2. `scene.Raycast` + obstacle collision in the solver.
3. `[[cable]]` TOML parsing + cylinder bake at load.
4. Schema + example scene object (`scenes/objects/power-cord.toml` or inline in
   a test scene).
5. (Optional) `PRIM_CABLE` poly-capsule if cylinder count or BVH size is a
   problem.

## Out of scope (v1)

- Real-time per-frame simulation.
- Self-collision and cable–cable interaction.
- Cosserat / implicit rod solvers.
- Analytic swept-spline ray intersection in WGSL.
- Cable affecting player physics (walkable / blocking).
