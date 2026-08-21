# Jolt physics integration plan

**Status:** design / post-experiment  
**Goal:** One authoritative rigid-body simulation for the whole level — static
architecture, dynamic props, kinematic doors, and (later) buoyancy — with
correct rendering, collision, and performance at office-sunset scale.

Related docs: [dynamic-objects.md](dynamic-objects.md) (GPU transform path),
[npc-active-ragdoll.md](npc-active-ragdoll.md) (character physics rig).

---

## Lessons from the kickable-props experiment (do not repeat)

We prototyped a Go-side integrator in `physprops` (scene `physics = true`,
`collision = false`, manual impulses, `DynamicBodies` for GPU upload). It was
removed because:

| Problem | Root cause |
|---------|------------|
| Walk-through / no wall collision | Player on Jolt `CharacterVirtual`; props on a separate integrator with analytic `scene.Blocked` — two worlds |
| Props vanished off skyway | Ground height queried outside floor footprint snapped to y=0 |
| GPU props frozen or duplicated | Instanced scenes only partial-upload `DynamicBodies`; props not registered correctly |
| Jolt OOM (`TempAllocator` ~10 MB) | Thousands of per-primitive dynamic Jolt bodies on the moving layer |
| Edge/corner resting, endless spin | Euler angles + fake uprighting torque without real contact manifolds |
| Cylinder interpenetration | Sphere proxies for non-spherical shapes |
| Kick/pull bugs | Ad-hoc impulses not tied to player velocity or contact normals |

**Conclusion:** dynamic objects must live in **Jolt** with proper shapes,
layers, sleeping, and a single `PhysicsSystem::Update` per frame. The scene
graph remains the render/authoring source of truth; Jolt owns poses for
simulated bodies.

---

## Current state (phase 1 — shipped)

Package: `internal/joltphys` via [jolt-go](https://github.com/bbitechnologies/jolt-go).

- One `PhysicsSystem` per loaded level.
- **Static** colliders from scene boxes (incl. hole CSG fragments), cylinders,
  terrain heightfields.
- **Player:** `CharacterVirtual` capsule when `jolt_physics = true` in
  `player.toml` / scene `[player.movement]`.
- `camera.World` served by Jolt raycasts + overlap; `scene.Scene` kept as
  fallback for gaps.
- CPU analytic collision remains default (`jolt_physics = false`).

Not simulated in Jolt yet: doors, NPCs, render-only dynamic poses, kickable
props, water.

---

## Target architecture

```
┌─────────────────────────────────────────────────────────────┐
│  Scene (authoring + render)                                  │
│  primitives, includes, instancing, DynamicBodies registry    │
└───────────────┬─────────────────────────────┬───────────────┘
                │ load / hot-reload            │ each frame
                ▼                              ▼
┌───────────────────────────┐    ┌────────────────────────────┐
│  joltphys.WorldBuilder     │    │  joltphys.SimStep(dt)       │
│  static mesh colliders     │    │  Update → read body poses   │
│  dynamic body spawn        │    │  → scene.Xform + TouchXform│
│  kinematic body registry   │    │  CharacterVirtual update    │
└───────────────┬───────────┘    └──────────────┬─────────────┘
                │                                  │
                ▼                                  ▼
┌─────────────────────────────────────────────────────────────┐
│  Jolt PhysicsSystem (single instance per level)                │
│  broad phase · narrow phase · constraints · sleeping         │
└─────────────────────────────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────────────────────────────┐
│  WebGPU sceneCache                                           │
│  partial transform upload for DynamicBodies (existing path)  │
└─────────────────────────────────────────────────────────────┘
```

### Principles

1. **One sim world** — player, props, doors, and (later) buoyancy probes share
   one `PhysicsSystem` and one set of collision layers.
2. **Compound over atomic** — a desk is one dynamic body with multiple child
   shapes, not four leg bodies + top unless articulation is required.
3. **Sleeping is mandatory** — resting objects must sleep; wake only on
   contact/impulse. Required for large levels.
4. **Shapes match render primitives** — box, sphere, cylinder, capped cylinder,
   convex hull / compound; no shared sphere proxy for everything.
5. **GPU follows sim** — simulated primitives are `scene.DynamicBody` entries;
   `TouchTransforms()` + `sceneCache.updateDynamicTransforms` (already used by
   NPCs).
6. **Static world stays instanced** — do not materialize every static prim into
   Jolt if the broad phase can use aggregated/static meshes; dynamic count must
   stay bounded.

---

## Scene authoring model

### Primitive coverage

| Render primitive | Jolt shape | Static | Dynamic | Notes |
|------------------|------------|--------|---------|-------|
| `box` | `BoxShape` | yes | yes | Hole CSG → compound of boxes |
| `sphere` | `SphereShape` | yes | yes | |
| `cylinder` | `CylinderShape` or convex | yes | yes | Caps for flat ends |
| `cone` | `ConvexHullShape` | yes | yes | Sample mesh or analytic hull |
| `torus`, `ring`, `lens` | convex hull / mesh | static only initially | defer | Rare as dynamic |
| `plane` | thin box or ignored | yes | no | Infinite planes → large thin box |
| Terrain | `HeightFieldShape` | yes | no | Already supported |
| `[[include]]` subtree | compound per group | yes | optional | See groups below |

### Physics groups (composite objects)

Authors need to express **how** an included object participates in simulation:

```toml
# objects/desk-setup.toml
[physics]
mode = "compound"        # "static" | "compound" | "pieces" | "kinematic"
mass = 35.0              # optional override (auto from volume × density)
sleep = true

# All primitives in this file share one rigid body unless overridden:
[[box]]
pos_x = 0; pos_y = 0.74; pos_z = 0   # desktop
width = 1.2; height = 0.04; depth = 0.6

[[box]]                  # leg — child shape of same compound
pos_x = -0.5; pos_y = 0.37; pos_z = -0.25
width = 0.08; height = 0.74; depth = 0.08

# Separate dynamic pieces on the desk (monitor, keyboard):
[[include]]
file = "objects/monitor.toml"
at = [0.1, 0.76, 0.0]
[include.physics]
mode = "dynamic"
mass = 4.0
friction = 0.6
```

**Modes:**

- `static` — Jolt static body (default for level geo).
- `compound` — one dynamic/kinematic body, many child shapes in local space
  (table, chair, crate).
- `pieces` — each marked primitive → own dynamic body (monitor, keyboard,
  loose cups). Loader spawns N bodies; optional `weld` constraints at init.
- `kinematic` — doors, elevators, scripted movers; pose driven from Go each
  frame (`MotionTypeKinematic`).

### Initial placement on surfaces

For props on furniture:

1. At load, raycast down from authored `at` to find support.
2. Spawn dynamic body slightly above contact; let first physics frame settle.
3. Optional **fixed weld** constraint to parent body with breakable impulse
   threshold (Jolt `Constraint` + listener) so a shove unseats the monitor
   but walking past does not.

### Table-tip scenario (acceptance test)

```
desk (compound dynamic, high friction legs)
 ├── monitor (dynamic, welded weakly to desktop contact point)
 └── keyboard (dynamic, welded weakly)
```

Player applies horizontal impulse to desk COM → desk tilts on leg edges →
welds break → monitor/keyboard slide/fall with real friction. No special-case
kick code; use `CharacterVirtual` contact + optional `AddImpulse` on raycast
hit body.

---

## `internal/joltphys` package layout (proposed)

```
joltphys/
  runtime.go          Init/Shutdown (existing)
  world.go            World lifecycle, CharacterVirtual, camera.World
  build_static.go     Static colliders from scene (existing build_scene)
  build_dynamic.go    Spawn compound/piece bodies from physics metadata
  body_map.go         BodyID ↔ scene primitive indices (DynamicBody ranges)
  layers.go           ObjectLayer / BroadPhaseLayer tables
  constraints.go      Welds, hinges (doors), breakable listeners
  step.go             Update, pose write-back, sleep stats
  queries.go          Raycast, cast shape, get body at hit (for interact)
  buoyancy.go         (phase 5) fluid volumes, buoyancy impl
```

### Body ↔ scene mapping

Extend `scene.DynamicBody` (or add `PhysicsBody`):

```go
type PhysicsBody struct {
    Name       string
    JoltID     uintptr // opaque BodyID
    Boxes      [2]int  // scene primitive spans (for render sync)
    Spheres    [2]int
    Cylinders  [2]int
    Compound   bool    // child shapes share one pose
    Sleepable  bool
}
```

After `PhysicsSystem.Update`, for each awake dynamic body:

1. Read position + rotation from Jolt.
2. Write `scene.RigidFromQuaternion` (or matrix) to each owned primitive
   `Xform`.
3. `scene.TouchTransforms()` once if any body moved.

Kinematic doors: `door.Manager` sets target → `BodyInterface.SetPositionAndRotation`
before step.

---

## Collision layers

| Layer | Objects | Collides with |
|-------|---------|---------------|
| `STATIC` | Level mesh, terrain | Player, dynamic, kinematic, queries |
| `DYNAMIC` | Props, furniture | Static, dynamic, player, kinematic |
| `KINEMATIC` | Doors, platforms | Static, dynamic, player |
| `PLAYER` | CharacterVirtual | Static, dynamic, kinematic |
| `TRIGGER` | Volumes, water surface | Overlap only |
| `DEBRIS` | Small props (optional) | Static, player; not each other |

Use Jolt `ObjectLayerPairFilter` so debris does not explode broad phase pairwise
cost. Player capsule should **push** dynamic bodies via contact, not a
parallel analytic resolver.

---

## Performance at large levels

### Constraints from the experiment

- Jolt global temp allocator is **~10 MB** — budget dynamic body count and
  collision pairs aggressively.
- Office-sunset has **instanced** static geometry; static Jolt mesh should mirror
  **collision-only** aggregates, not one body per decorative prim.
- Target: **&lt; 200 awake** dynamic bodies typical; thousands sleeping.

### Strategies

1. **Sleeping** — default on; `AllowSleeping = true`; tune activation thresholds.
2. **Compound shapes** — desk = 1 body, not 5.
3. **Static baking** — merge adjacent static boxes into compounds offline at
   load (per room/chunk), similar to hole CSG fragments.
4. **Chunked static** — optional spatial buckets for very large open worlds;
   only load colliders near player (future).
5. **No double simulation** — delete Go integrator path; Jolt only.
6. **GPU** — only `TouchTransforms` for awake/moved bodies; sleeping bodies
   skip upload (track `IsActive()`).
7. **Profiling** — `cmd/gpuprof` + Jolt `PhysicsSystem` stats; CI budget:
   sim step &lt; 2 ms @ office-sunset with 50 dynamic props asleep.

### Hot reload

On scene reload:

1. Destroy old `World` (all bodies/shapes).
2. Rebuild static + dynamic from new scene.
3. Rebind `body_map`; bump `scene.Generation()` for GPU static rebuild.
4. Teleport player via `SyncPlayer`.

---

## Player interaction

- **Movement:** keep `CharacterVirtual` (existing).
- **Push:** contact friction + player velocity transfer (Jolt default); tune
  capsule mass and friction.
- **Kick / use:** short raycast from camera → `BodyInterface.AddImpulse` at
  hit point with upward bias; optional cooldown.
- **Grab (future):** kinematic constraint or spring to held body.

Remove separate `handleKick` / `physprops` packages — impulses are sim queries.

---

## Doors and kinematic geometry (phase 2)

- Register door panels as **kinematic** bodies; `door.Manager` writes pose each
  frame before `Update`.
- Retire `scene.SetDoorGhost` for Jolt users once panel layers handle overlap.
- Hinge constraints optional v2; kinematic sweep is enough for v1.

---

## NPCs (phase 3 — optional overlap with ragdoll plan)

- Biped: keep kinematic capsules for blocking OR motor-driven ragdoll per
  [npc-active-ragdoll.md](npc-active-ragdoll.md) on same `PhysicsSystem`.
- Spider: ground via Jolt raycasts instead of analytic terrain.
- Simulated props must collide with NPC proxy bodies on `DYNAMIC`/`KINEMATIC`
  layers.

---

## Water and swimming (phase 5 — future)

### Authoring

```toml
[[water]]
# existing render water volumes ...

[water.physics]
buoyancy = 1.0
drag = 0.4
flow = [0.2, 0, 0]   # optional current
swim_allowed = true
```

### Simulation approach

1. **Trigger volume** — box/convex matching render water AABB on `TRIGGER`
   layer.
2. **Buoyancy** — each frame, for bodies overlapping volume: apply
   `F = ρ V g` along surface normal (Jolt `BuoyancyEffect` pattern or custom
   pre-step force at submerged COM).
3. **Drag** — velocity-dependent damping when COM inside volume.
4. **Swimming player** — replace or augment `CharacterVirtual` gravity when
   capsule overlaps water trigger: buoyancy + swim thrust from input along
   camera forward/up; disable jump, add surface stroke.
5. **Rendering** — unchanged; physics reads same `[[water]]` placement.

### Performance

- Buoyancy only for **awake** bodies intersecting water cells.
- Coarse grid over water volume; broad-phase trigger pairs already identify
  candidates.

---

## Phased rollout

| Phase | Deliverable | Success criteria |
|-------|-------------|------------------|
| **1** ✅ | Static + player `CharacterVirtual` | office-sunset walkable, no regressions |
| **2** | Kinematic doors in Jolt | doors block player; no ghost hacks |
| **3** | Dynamic compounds + pieces | desk tip test; props sleep on floor |
| **4** | Player push/kick via impulses | no second integrator; stable at rest |
| **5** | Shape expansion (cone, hull) | all common primitives or documented gaps |
| **6** | Water buoyancy + swimming | submerged box floats; player swims |
| **7** | Breakable welds / constraints | monitor slides off tilted desk |

### Phase 3 task breakdown (next implementation slice)

- [ ] Schema: `[physics]` on includes + optional per-primitive overrides
- [ ] `build_dynamic.go`: compound builder from included subtree
- [ ] `body_map.go` + write-back to `scene.DynamicBody` primitives
- [ ] Collision layers + filters
- [ ] Remove reliance on analytic prop collision for dynamics
- [ ] Test: single box spawn, sleep, push with player
- [ ] Test: compound desk + two piece props; tip desk in test harness
- [ ] Preview scene: `scenes/preview/physics-desk.toml`

---

## Open questions

1. **Convex hull from CSG** — torus/lens as dynamic: precompute hull at load or
   disallow?
2. **Instanced props** — dynamic instance template (e.g. 50 identical crates):
   shared shape, unique body IDs; cap count?
3. **Network / replay** — deterministic `PhysicsSystem` settings if needed later.
4. **jolt-go gaps** — verify compound child transforms, breakable constraints,
   buoyancy API exposure; upstream issues if missing.
5. **Mass authoring** — default density per material tag vs explicit `mass`?

---

## References

- [Jolt Physics](https://github.com/jrouwe/JoltPhysics) — architecture, sleeping,
  layers, character controller.
- [jolt-go](https://github.com/bbitechnologies/jolt-go) — CGO bindings used today.
- Experiment branch context: kickable skyway props (reverted) — informed “single
  sim world” requirement.
