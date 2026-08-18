// Package joltphys integrates [Jolt Physics] via github.com/bbitechnologies/jolt-go.
//
// Migration map (existing → Jolt):
//
//   - Player walk/collision (camera.World Blocked/GroundHeight) → CharacterVirtual + static colliders
//   - Static scene geometry (boxes, cylinders, terrain) → static Jolt bodies at scene load
//   - Doors (animated boxes + ghost callback) → kinematic bodies + collision layers (future)
//   - NPC bipeds (kinematic + foot IK) → keep procedural; optional capsule proxies later
//   - Spider NPCs (internal/physics hover integrator) → keep custom; raycasts can move to Jolt
//   - Dynamic props / rigid debris → MotionTypeDynamic bodies (future)
//
// While jolt_physics is enabled in player config, the player uses CharacterVirtual;
// scene queries remain for footsteps, NPC nav, and rendering.
//
// [Jolt Physics]: https://github.com/jrouwe/JoltPhysics
package joltphys
