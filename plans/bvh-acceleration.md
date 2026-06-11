# Plan: BVH Acceleration Structure

## Status: IMPLEMENTED

Implemented in `internal/bvh` (median-split, flattened node array, leaves
referencing primitives by `(kind, idx)` matching the tracer's dispatch). The
tracer's `intersect`, `nearestDist`, and `shadowed` now query the BVH for finite
primitives (`Nearest`, `NearestDist`, `AnyHit`) and test planes / terrain /
water directly. The shadow path preserves the original skip rules (emissive
spheres and tori) inside `AnyHit`.

Measured at 400x300, pixSize=1, mirror+shadow+AO on:

| Scene             | before  | after   | fps        |
| ----------------- | ------- | ------- | ---------- |
| default (indoor)  | 20.7 ms | 15.2 ms | ~48 -> ~66 |
| outdoors (terrain)| 23.9 ms | 19.6 ms | ~42 -> ~51 |

`Box`/`Cone`/`Sphere`/`Cylinder` intersections dropped out of the profile's top
consumers. The outdoor frame is now dominated by terrain heightfield marching
(BVH traversal is ~14%); planes/terrain/water remain outside the BVH by design.
Output is pixel-identical to the brute-force renderer (previews unchanged, tests
pass).

## Goal

Replace the brute-force, per-ray linear scan over all primitives with a
**bounding-volume hierarchy (BVH)**, taking the renderer comfortably past 60 fps
at full 400x300 (`pixSize=1`) with all features on, and making it scale to
larger scenes.

## Motivation (from profiling)

Current state: `45.8 ms -> 22.2 ms/frame` (~21.8 -> ~45 fps) after the
math.Max/Min, sky/gamma, and AO micro-optimizations. The remaining cost is
dominated by a single root cause — **every ray tests all ~31 finite
primitives**.

CPU profile (400x300, pixSize=1, mirror+shadow+AO on):

| Function                        | flat   | cum    |
| ------------------------------- | ------ | ------ |
| `scene.(*Box).Intersect`        | 30.1%  | 30.3%  |
| `scene.(*Cone).Intersect`       | 12.9%  | 12.9%  |
| `scene.(*Sphere).Intersect`     | 7.5%   | 9.3%   |
| `trace.(*Tracer).shadowed`      | 6.6%   | 27.5%  |
| `scene.(*Cylinder).Intersect`   | 6.4%   | 6.5%   |
| `trace.(*Tracer).intersect`     | 5.5%   | 22.2%  |
| `trace.(*Tracer).nearestDist`   | 4.6%   | 33.8%  |
| `trace.(*Tracer).ambientOcclusion` | 1.7% | 40.8%  |

The three biggest *cumulative* consumers — `nearestDist` (AO probes),
`shadowed` (shadow rays), and `intersect` (primary/bounce rays) — are all the
same "loop over every object" pattern. A BVH replaces that **O(N)** scan with an
**O(log N)** traversal: ~31 tests/ray collapse to roughly ~3-6.

Because AO casts 2 rays/pixel and shadows cast up to 3 rays/pixel, these
secondary rays vastly outnumber primary rays — and all of them benefit at once.

## Approach

1. **Build once, static scene.** The scene never changes at runtime, so build
   the BVH a single time at startup. Zero per-frame build cost.

2. **Finite primitives only.** Put spheres, boxes, cylinders, cones, and tori
   into the BVH. Each needs an axis-aligned bounding box (AABB):
   - Sphere: `center ± radius`
   - Box: already an AABB
   - Cylinder: `x,z in center ± radius`, `y in [ymin, ymax]`
   - Cone: `x,z in center ± rBase`, `y in [yBase, yTip]`
   - Torus: `x,z in center ± (R+Rm)`, `y in center.y ± Rm`

3. **Planes stay separate.** Planes are infinite and cannot be bounded. Keep the
   5 planes in a small slice tested directly after (or before) the BVH
   traversal. Cheap.

4. **One structure, three query types.** A single BVH serves all of:
   - `intersect` — nearest hit + surface attributes (primary/bounce rays)
   - `nearestDist(maxT)` — nearest hit distance only, normal-free (AO)
   - `shadowed(maxT)` — any-hit early-out (shadow rays)

   Traversal differs only in the leaf test and the early-out condition.

## Implementation sketch

- New package `internal/bvh` (or `internal/accel`):
  - `type AABB struct { Min, Max vec.V }` with `Union`, `Hit(ray, tMax) bool`
    (slab test, reuse the fast comparison style already used in `Box.Intersect`).
  - A flattened node array for cache efficiency:
    `type node struct { bounds AABB; left int32; count int32; first int32 }`
    (interior nodes store child indices; leaf nodes store a primitive range).
  - Leaves reference primitives via an index list, not the typed slices, so the
    BVH is agnostic to primitive type.

- Primitive abstraction:
  - Give each primitive an `AABB()` method in `scene`.
  - To avoid interface-dispatch cost in the hot loop, consider storing leaf
    primitives as a tagged union / parallel index arrays (kind + index into the
    typed slice) so `Intersect` dispatches via a small switch — mirrors the
    existing `kind`/`idx` pattern in `trace.intersect`.

- Build:
  - Median-split or SAH (surface-area heuristic) on the longest axis. Median
    split is simpler and plenty for ~31 static objects; SAH is a later refinement.

- Wire-up in `trace`:
  - `Tracer` holds a `*bvh.BVH`. Rewrite `intersect`, `nearestDist`, and
    `shadowed` to traverse the BVH for finite primitives, then test the plane
    slice directly. Keep the existing emissive-sphere / torus skip rules where
    they currently apply (shadows skip emissive spheres and tori).

## Validation

- `go test ./internal/render/ -run TestRenderProducesImage` still passes.
- `go test ./internal/render/ -bench BenchmarkFrame` — compare ns/op before/after
  (target: <= 16.7 ms/op for 60 fps).
- Visual diff: regenerate `preview.png` (`go run ./cmd/preview -o preview.png`)
  and confirm it is unchanged vs. the brute-force renderer.

## Notes / alternatives

- A **uniform grid** is cheaper to build but behaves worse on this irregular
  layout (clustered pillars/arch vs. open floor). BVH is the better fit.
- Watch the AO/shadow epsilon and `maxT` capping — the BVH traversal must respect
  the same `aoMaxDist` (0.9) and shadow `maxT - 0.05` thresholds so results stay
  pixel-identical.
- The torus already has a bounding-sphere early-out; inside a BVH that becomes
  largely redundant but harmless.
