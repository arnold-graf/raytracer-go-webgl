# Where the time goes in outdoors-night-villa

**Status:** measured. The starting hypothesis — that terrain is expensive in this
scene — is not supported; the pond's reflection is. A reduced-resolution water
reflection was built and measured, then removed; see below.
**Camera:** the scene default, `pos = [0, 2, 14]`, yaw 0, pitch 0.04. `gpuprof
-mountains` resolves to the same camera here (byte-identical counters), so this
covers both.
**Baseline:** 21.0 ms, ~48 fps at 512x320, bounce depth 4, adaptive AA on.

---

## Summary

The pond covers 42% of the frame and mirrors the villa. Each of those pixels
forks a reflected ray that re-renders the valley — geometry, lighting and shadow
rays — so the frame contains most of a second render. That fork is **8.4 ms of
the 21.0 ms frame**. Terrain marching is about **2%**.

Ablation (`gpuprof -scene scenes/outdoors-night-villa.toml`):

| Config | GPU | fps |
|---|---|---|
| all on | 20.9 ms | 48 |
| shadow off | 18.6 ms | 53 |
| AO off | 20.8 ms | 48 |
| **mirror off** | **10.2 ms** | **98** |
| all off | 8.8 ms | 113 |

Scene probes, each a copy of the scene with one thing changed, interleaved
best-of-4:

| Probe | GPU | vs baseline |
|---|---|---|
| pond material `glass` -> `diffuse` (surface kept, no fork) | 12.7 ms | **-40%** |
| pond radius 7 -> 0.1 (pond gone) | 12.8 ms | -39% |
| `transmit` 0.5 -> 0.0 (reflected lobe only) | 20.3 ms | -3.8% |
| bounce depth 4 -> 1 | 17.9 ms | -14.4% |
| `grid_cell` 0.25 -> 1.0 (16x fewer baked samples) | 21.1 ms | +0.5% |
| terrain `step` 0.28 -> 1.2 (33% fewer march steps) | 20.4 ms | -2.4% |
| road `[[terrain.zone]]` removed | 20.9 ms | 0% |

The pond's *surface* is free — removing the water entirely (12.8 ms) is no
cheaper than keeping it and not forking (12.7 ms). Ripple evaluation, the water
normal and shading 42% of the screen cost about 0.1 ms. The entire 8.4 ms is
secondary rays.

Of that, refraction is minor: dropping the transmitted lobe saves 3.8%. The
**reflected** lobe is the cost, because it escapes into the open valley and hits
villa, trees, terrain and sky, each with full direct lighting and its own shadow
rays.

## Terrain is not the bottleneck here

Three independent probes agree:

- **The baked height grid is not the issue.** The terrain bakes to 1601x1601
  samples — 10.3 MB of heights plus 30.8 MB of normals — and the march does a
  bilinear fetch (4 scattered loads) per step, 5.7M times. That looks like a
  cache-thrashing story, and it is not: coarsening to 1.0 m cells shrinks the
  height buffer 16x to 0.64 MB and changes frame time by 0.0%.
- **Step count is not the issue.** Cutting 33% of march steps buys 2.4%.
- **Terrain shading is not the issue.** Removing the textured, bump-mapped cobble
  road zone changes nothing.

A fourth probe (flattening the terrain: no features, `detail = 0`) is *not*
usable and is recorded here as a trap. It came out at exactly 21.0 ms, which
looks like confirmation that relief is free, but the step count went *up*, from
5.7M to 10.1M: with the hills gone, rays graze along a near-flat field for much
longer before hitting or exiting. Primary terrain coverage dropped from 32% to
8.6% at the same time. It changes too many things at once to measure anything.

## Why the profiler said "terrain 50%"

`gpuprof -profile` prints an *estimated* time mix, not a measurement, from the
hand-weighted model in `cost_model.go`. Terrain's term is
`costTerrainStep * TerrainSteps`, and `TerrainSteps` is in the millions while
every other class is weighted by a hit count in the hundreds of thousands. With
`costTerrainStep = 1.0` the estimate is dominated by step count regardless of
what a step actually costs — measured here at roughly 0.26 ns, an order of
magnitude below the weight's implied value.

The comment on those constants claims calibration against this very scene, which
is what makes it convincing. It has been annotated in place. **Treat the mix line
as a coverage hint; use `-ablate` and scene probes for anything load-bearing.**

Recalibrating honestly needs per-event measurements for the other classes too
(prim hit, instance hit, shadow ray), which nobody has taken. Only the terrain
step weight is currently known to be wrong.

## Reduced-resolution water reflections (built, then removed)

Implemented and measured, then removed at the author's call as not worth its
keep. Recorded because the measurements stand and the trade may look different
later.

A primary water hit's *reflected* lobe was deferred to a task buffer, a trace
pass ran one ray per shared footprint, and a composite pass interpolated between
footprints. The refracted lobe stayed inline; it is 3.8% of the frame.

| Variant | GPU | fps | >8 levels | mean err |
|---|---|---|---|---|
| full resolution | 21.20 ms | 47 | — | — |
| 1x2, share vertically | 18.80 ms | 53 | 2.51% | 0.65 |
| 2x2, bilinear | 17.70 ms | 57 | 4.18% | 0.99 |
| 2x2, ray-guided bilateral | 17.30 ms | 58 | 4.08% | 0.96 |
| 2x2 + adaptive full-res refinement | 22.80 ms | 44 | 0.56% | 0.25 |

Three findings worth keeping:

- **The saving tracks removed rays almost exactly** — 2x2 removed 13.5% of path
  segments and 13% of frame time. It is a work *reduction*, which is why it paid
  at all where the bounce-kernel experiments did not.
- **Ray-guided (joint bilateral) upsampling is useless on a calm pond.** Weighting
  each neighbour by how well its traced direction matches the pixel's own changed
  nothing (4.08% vs 4.18%). At `ripple = 0.05` the pond is a near-flat mirror and
  neighbouring rays are almost parallel; the detail lost at reduced resolution is
  the *reflected scene's* own sharp edges, which no guide derived from the water
  surface can predict.
- **Sharing direction matters more than sharing amount.** The reflected content is
  dominated by tall vertical bands (window frames mirrored in the water), so 1x2
  vertical sharing cost roughly half the error of 2x2 for half the saving. 2x2
  was judged too blurry in-game.

Adaptive full-res refinement — retracing a pixel whose four neighbouring samples
disagree — recovered nearly all the quality and was *slower than full resolution*
at every threshold swept, because the refining pixels are scattered and a dense
dispatch bills the whole workgroup. That is the pre-compaction adaptive-AA
failure again.

### Other options, not pursued

- **Cap bounce depth for water-spawned rays.** Worth 3.0 ms; depth 2 and depth 4
  are identical (92,477 glass bounces either way), so the whole second tier is
  villa windows seen in the pond. **Not visually free:** an 8x diff of depth 1
  against depth 4 is confined to exactly those reflected windows, the brightest
  feature in the water (max delta 218, mean 3.5 levels, 4.4% of pixels beyond 8
  levels). A cap at 2 is a no-op.
- **Drop the transmitted lobe** (`transmit = 0`). Only 3.8%, and it removes the
  pond's depth cue.

Not worth pursuing: anything aimed at the heightfield. The three probes above
bound that work at a few percent.

---

## Related

- [ray-tracing.md](ray-tracing.md) — how terrain marching and water fit into
  `ray_color`.
- [bounce-kernel.md](bounce-kernel.md) — why relocating reflection work into a
  separate kernel does not pay, and the density/sparsity criterion behind it.
- [megakernel-optimization.md](megakernel-optimization.md) — the office-sunset
  pass, and the general warning that an ablation which changes the image
  substantially is not measuring one feature.
