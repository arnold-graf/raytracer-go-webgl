# Jolt physics integration

[Jolt-go](https://github.com/bbitechnologies/jolt-go) wraps [Jolt Physics](https://github.com/jrouwe/JoltPhysics) with prebuilt CGO binaries (macOS ARM64, Linux x86-64/ARM64).

## Current state (phase 1)

- `internal/joltphys` builds a physics world from static scene geometry (boxes, cylinders, terrain).
- Player movement can opt in with `jolt_physics = true` in `player.toml` or per-scene `[player.movement]`.
- When enabled, `camera.World` is served by Jolt (`CharacterVirtual` + raycasts); the analytic `scene.Scene` queries remain as fallback for missing colliders.
- Boxes with `[[box.hole]]` openings are decomposed into solid sub-boxes (same CSG as the CPU tracer).
- Legacy CPU collision remains the default (`jolt_physics = false`).

## What stays on the old path (for now)

| System | Package | Reason |
|--------|---------|--------|
| Spider NPC hover/orient | `internal/physics` | Custom integrator tuned for arachnid locomotion |
| Biped NPC gait + foot IK | `internal/npc` | Kinematic, not rigid bodies |
| Doors | `internal/door` | Animated boxes + ghost volumes; needs kinematic bodies + layers |
| Dynamic render props | scene `dynamic` flag | Pose-driven, not simulated |
| Footstep / probe audio | `internal/probe` | Uses scene raycasts independent of player collider |

## Phased migration

### Phase 2 — doors and moving geometry

- Register door panels as kinematic Jolt bodies updated each frame from `door.Manager`.
- Collision layers: player vs static vs kinematic vs triggers.
- Retire door-specific `scene` ghost callbacks once layers cover interact volumes.

### Phase 3 — dynamic props

- Scene objects marked `dynamic` spawn `MotionTypeDynamic` bodies.
- Sync mesh transforms from Jolt after `PhysicsSystem.Update`.

### Phase 4 — NPC proxies (optional)

- Capsule `CharacterVirtual` or kinematic capsules for biped blocking.
- Spider ground height via Jolt raycasts instead of terrain sampling.

## Tuning notes

- Game tick uses ~1/60 s; gravity/jump values in `player.toml` are per-tick, not m/s².
- `World.UpdatePlayer` scales gravity for Jolt's integration (`gravity / dt` in extended update).
- Rebuild the Jolt world on scene hot-reload and when toggling `jolt_physics`.

## Try it

```bash
# player.toml
jolt_physics = true

go run . -scene scenes/office-sunset/index.toml
```

Fallback: if Jolt fails to init or world build errors, stderr logs a warning and CPU collision is used.
