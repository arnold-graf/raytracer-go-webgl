# Plan: Runtime Dynamic / Moving Objects

## Status: DONE

## Goal

Let scene objects **move at runtime** (rotating, translating, animated props)
while keeping the renderer fast. Today composite objects (e.g.
`objects/staircase.toml`) are merged into the scene at **load time** as
transformed boxes — perfect for authoring, but frozen. This plan adds a thin
runtime layer on top of the pieces already in place.

Same non-negotiables as the WebGPU port: fidelity first, simplest code, safe
rollout (a static scene must cost exactly what it does today).

## What already exists (the foundation)

- `scene.Touch()` / `scene.Generation()` — the invalidation signal.
- `webgpu.sceneCache` keys packed/uploaded GPU buffers off
  `(scene pointer, Generation())` and rebuilds only when it changes
  (`cache.go`). A static scene never re-packs; a `Touch()` forces a rebuild.
- Per-primitive rigid transforms end-to-end: `scene.Transform`,
  `GPUPrimitive.Xf0..Xf2`, the `PRIM_FLAG_TRANSFORMED` shader path, and
  `WorldBounds()` for collision. Moving an object is "change its transform".
- `NewInstanceTransform` + `mergeScene` already place rotated/translated
  composites (load-time today).

So the missing piece is **runtime mutation + re-pack**, not transforms or GPU
support.

## Design sketch

### 1. A `Mover` abstraction (CPU)

Introduce a small per-frame update step that mutates scene geometry and calls
`scene.Touch()` once per tick if anything changed:

```go
// scene/animation.go (new)
type Mover interface {
    // Update advances the animation to time t (seconds) and writes the new
    // transform/position into the scene. Returns true if it changed anything.
    Update(s *Scene, t float64) bool
}
```

The app's `Update()` loop runs all movers, and calls `s.Touch()` if any
returned true. Movers are data-driven from TOML (see §4) or code.

### 2. Track which primitives a composite owns

`mergeScene` currently appends primitives and forgets their origin. Add an
optional **instance id / index range** recorded when an include is merged, so a
mover can later rewrite just that object's primitives:

```go
type Instance struct {
    Name       string
    Base       *Transform   // the placement transform (animated)
    BoxRange   [2]int       // [start,end) into Scene.Boxes
    // ...one range per primitive kind it contributed
}
```

Store `[]Instance` on the `Scene`. A mover updates `inst.Base`, re-composes it
onto each owned primitive's local transform, and the scene is `Touch()`ed.

### 3. Re-pack flow (works today, unoptimized)

With §1+§2, a moving object already renders correctly: `Touch()` invalidates the
cache, `sceneCache.rebuild` re-packs everything (incl. a fresh `PackBVH`), and
the frame is correct. This is the **safe v1** — identical code path to a hot
reload, just triggered each tick. Cost: today's full per-frame pack (~the
pre-cache numbers) **only while something moves**; static scenes stay free.

### 4. TOML authoring (optional, after v1 works)

Extend `[[include]]` with an optional animation block so props can move without
code:

```toml
[[include]]
file = "objects/door.toml"
at = [3.0, 0.0, 1.5]
  [include.spin]          # example: rotate about Y over time
  axis = "y"
  rate_deg_per_sec = 45.0
```

`sceneio` builds the corresponding `Mover` and registers an `Instance`.

## Performance path (only if needed)

The v1 re-pack is fine for a few moving props but scales poorly if many objects
move every frame (it rebuilds the whole BVH + re-uploads all buffers). Stage the
optimizations behind measurements from `cmd/gpuprof`:

1. **Partial buffer upload** — re-pack only the dirty `Instance` ranges and
   `queue.WriteBuffer` just those byte spans (the generation signal already
   tells us *that* something changed; the `Instance` ranges tell us *what*).
2. **BVH refit instead of rebuild** — moving objects keep topology, so refit
   node AABBs bottom-up (O(nodes)) rather than a full SAH rebuild. Keep the full
   rebuild for adds/removes.
3. **Two-tier BVH (TLAS/BLAS)** — static geometry in a frozen bottom-level
   structure, moving instances as a small top-level structure rebuilt per frame.
   Only worth it for many movers; biggest change, do last.

## Fidelity / safety gates

- New test: a scene with one spinning box
- Extend `TestSceneCacheReusesStaticBuffers`: a mover that changes a transform
  must bump `Generation()` and re-pack; a no-op tick must not.
- Collision uses `WorldBounds()`, which already follows the transform, so
  walkable/solid moving geometry stays correct.

## Suggested order

§1 + §2 + §3 (correct, simple, slow-while-moving) → ship behind the existing
`-renderer` flag → measure with `gpuprof` → add §4 authoring → only then the
performance path (partial upload → BVH refit → TLAS/BLAS) as the numbers demand.

## Out of scope (v1)

- Deformable / skinned geometry (only rigid per-object transforms).
- Physics simulation (movers are scripted; collision is still
  player-vs-world).
- Continuous CSG hole animation.
