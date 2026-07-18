# Plan: Large Maps (2×2 km, hundreds of buildings, long view distance)

## Status: PROPOSED

**Per-frame GPU performance** (200 fps at full 512×320 resolution on hybrid maps
like `scenes/island.toml`, without lowering `pixSize`) is tracked separately in
[`hybrid-terrain-perf.md`](hybrid-terrain-perf.md). This file owns world
representation, memory, and LOD rings; that file owns shadow/light budgets and
near-field bake streaming.

## Comment from the Human

For the terrain we have a number of options.

1. Have a coarse version of the grid and the fine grid we currently have. As the
   player moves, we stream the grid into memory.
2. Make the whole thing analytical, i.e. we port heightAnalytic to WGSL. For a 20m radius around the player, we calculate the fBm for detail. Anywhere beyond that we drop the fbm.
3. A combination: We use have a coarse, tiled version of the grid in the
   distance, but full analytic precision 20m around the player.

## Terrain strategy (recommended)

The three options above are not mutually exclusive with the rest of this plan.
They answer a narrower question: **how do we represent ground height in the near
and mid field?** Step 3 (panorama) still owns everything beyond ~200–400 m.

### Three rings

Think of the world in concentric bands from the camera:

| Band | Distance | Height representation |
| ---- | -------- | --------------------- |
| **Near** | 0 – ~40 m | Full detail: analytic `heightAnalytic` **or** streamed fine bake (see below) |
| **Mid** | ~40 m – ~400 m | Coarse global heightfield (base + features + pads, **no fBm**) + mip pyramid for fast marching |
| **Far** | ~400 m+ | Panorama lookup (Step 3) — no terrain march at all |

**2026-07-18 learnings (island, `gpuprof` + shader work):**

- Hybrid LOD + mip pyramid + `terrain_seg_near` land correctly; the remaining
  cost on large maps is **shadow-ray terrain marching** and **per-light shadow
  multiplicity**, not mid-band bake resolution.
- **Distance-gated normals** (baked outside `hybrid_near` end) are high ROI;
  analytic finite-diff normals everywhere in hybrid mode were a major waste.
- **Analytic near field for marching** is still expensive at full resolution;
  for 200 fps targets, prefer **streamed near tile (heights + normals)** over
  live WGSL `heightAnalytic` per march step — see `hybrid-terrain-perf.md` Tier B1.
- Normals-only streaming is **not** worth the complexity alone; bake heights too.

The near band uses **camera/sample distance**, not player position. Ground 80 m
ahead of you is outside a "20 m around the player" bubble but still needs
appropriate detail when you look down a slope; measuring from the camera (or
from the ray's `(x,z)` sample point) avoids muddy terrain stretching out in
front of you.

### Option comparison

| | Option 1: streamed fine grid | Option 2: full analytic | Option 3: hybrid (**recommended**) |
| --- | --- | --- | --- |
| **Near field** | Baked 0.25 m tiles, streamed with player | WGSL `heightAnalytic`; fBm gated by distance | WGSL analytic (full detail) in near band |
| **Mid field** | Coarse grid / mip pyramid | Same analytic, fBm off | Global coarse grid (2–4 m cells), mip pyramid |
| **Memory** | Fine tiles resident near player | O(1) for near detail | One global coarse bake (~750k cells for 3 km @ 4 m) |
| **Risk** | Tile streaming, upload, seams | Dual CPU/GPU impl; long marches still need coarse pyramid | LOD seam at near/mid boundary; three height paths in shader |
| **Fits current code** | Best — extends `terrain.go` as-is | Requires WGSL port + parity tests | CPU already has `heightAnalytic`; GPU adds near path |

### Recommendation: Option 3 (hybrid)

1. **Near (~0–40 m from camera):** two viable paths:
   - **Quality (original):** evaluate `heightAnalytic` in WGSL with full fBm;
     normals via finite differences only inside the near band (baked outside).
   - **Performance (recommended for island-scale):** CPU-stream a camera-centered
     fine tile (0.25–0.5 m) with precomputed heights + normals; GPU cross-fades
     to the coarse global bake at the band edge. Avoids per-step feature loops
     and fBm on primary and shadow refine paths. See `hybrid-terrain-perf.md`.
2. **Mid (~40 m – panorama cutoff):** one **global coarse heightfield** baked
   at load (base + features + pads, no fBm). Upload once; fits comfortably under
   the existing `maxTerrainVals` cap. Mip min/max pyramid accelerates ray
   marching (extends today's `cmin/cmax` to multiple levels).
3. **Far (panorama cutoff+):** Step 3 — single texture lookup, no terrain march.

**fBm transition:** don't hard-switch at a radius. Fade fBm contribution to zero
over ~20 m (e.g. multiply by `1 - smoothstep(40, 60, dist)`). Coarse grid
deliberately omits fBm (~0.35 m amplitude in typical scenes), so near and mid
 heights stay consistent at the boundary.

**CPU parity:** walking/collision keeps using `heightAnalytic` on the CPU (already
implemented). Only the GPU near band needs the WGSL port; gate with tests against
`Terrain.Height` on a grid of sample points.

**Fallback:** if the analytic WGSL port slips, Option 1 (streamed fine tiles) is
the conservative path — boring, but proven against the current renderer.

### What this changes in Steps 4 and 6

- **Step 4** no longer assumes fine-grid **streaming** as the default. The mip
  pyramid + far-clip remain; fine tiles become the fallback, not the plan.
- **Step 6** streaming applies primarily to **buildings** and optional terrain
  tiles if we ship Option 1 instead. Under Option 3, terrain streaming is
  unnecessary — the coarse global bake is loaded once.

## Why this is tractable for *our* renderer

We are a primary-ray analytic raytracer at low resolution (~400×250 → ~100k
primary rays/frame, plus a few shadow/bounce rays each). Two consequences drive
this entire plan:

- **Ray count is fixed by resolution, not world size.** Unlike a rasterizer, we
  never "submit" the 200 buildings. A ray only pays for the BVH nodes it
  actually descends. A 2 km map can cost the same per pixel as one room — *if*
  the acceleration structure and the baked volumes scale.
- **The real enemies of a big map are therefore:**
  1. **BVH depth/quality** — traversal is `O(log N)`, but a sprawling flat tree
     means deep descents and fat nodes.
  2. **Ray *length*** — long views make terrain marching and shadow rays travel
     far. This scales with *distance*, not object count.
  3. **Baked-volume memory** — the AO volume (`internal/probe/ao.go`) and
     terrain caches (`internal/scene/terrain.go`) are *dense grids*. These are
     what literally explode at 2 km, not the geometry.

The guiding lever: **detail is unimportant past ~200 m** (low resolution + we
want long vistas for hill-climbing, not far-field fidelity). Every step below
spends that budget.

## Current foundations we build on

- Flat SAH BVH over all finite primitives (`internal/bvh`, `internal/webgpu/bvh.go`).
- `Xform` (arbitrary primitive transforms) is the next pending geometry TODO in
  `plans/webgpu-port.md` — it is the unlock for Step 1.
- Terrain already keeps a dense height/normal cache **and** a coarse per-cell
  `cmin/cmax` max-height grid for empty-space skipping (`internal/scene/terrain.go`).
- AO is a single dense global grid, capped at `aoVolMaxCells = 1_000_000` /
  `aoVolMaxAxis = 128`, baked up front in `Tracer.Prepare()`.

---

## Step 1 — Two-level BVH + instancing (the foundation)

### Executive summary

Think of it like a code module you `import` many times instead of copy-pasting.
Today every primitive of every building lives in one giant tree. Instead, build
each *unique building design* into its own small tree once (a "BLAS" — bottom
level), then the world holds a second tree (a "TLAS" — top level) whose leaves
are just *placements*: "template #7, rotated 90°, at (x,z)." When a ray reaches
a placement, we transform the ray into the building's local coordinate space and
test it against that shared small tree.

This is the same world→local ray-transform math you'd write for `Xform` anyway —
instancing is simply "a transform whose payload is a sub-tree instead of one
shape." 200 buildings made of ~20 designs go from "200 × 100 primitives in one
tree" to "20 small trees + 200 cheap placement records." Build time and memory
both collapse, and traversal stays shallow.

### Notes

- Design the `Xform` ray-transform path so the transformed payload can be a
  *sub-BVH*, not only a single primitive. That one decision yields instancing.
- TLAS leaf = `{templateID, transform (or its inverse), worldAABB}`. Ray enters,
  we apply the inverse transform, traverse the BLAS in local space, then
  transform the hit normal back.
- Gate: parity on a scene with a handful of instanced templates vs. the same
  scene authored as explicit primitives.
- **Villa perf (2025-06-17):** ~750 of ~932 primitives are duplicated pine-tree
  geometry (75 clusters × ~10 prims). BVH traversal dominates the mountain-facing
  view; glass/terrain micro-opts did not help. **Instancing is the highest-value
  next step for this scene** — one BLAS per `pine-tree-cluster.toml` (and other
  includes), TLAS placements for each `[[include]]`, shallow tree + smaller GPU
  primitive buffer. `Xform` is already landed on GPU; extend it to carry a
  template BVH id.

---

## Step 2 — Distance LOD / impostors

### Executive summary

A building 500 m away covers a few pixels. Tracing its 100 real primitives to
shade 3 pixels is pure waste. So past a distance threshold we swap the full
building for a **proxy**: one (or a few) flat-shaded box(es) with the building's
average color. "LOD" = level of detail; an "impostor" is a cheap stand-in that
looks right from far away. Because our resolution is low, the silhouette and
color are all the eye gets at distance, so the box is indistinguishable from the
real thing.

The win compounds with Step 1: a far placement becomes a single leaf box instead
of a 100-primitive sub-tree, so the *effective* tree a ray walks shrinks
dramatically the farther it looks.

### Notes

- Selection by distance from ray origin at the TLAS level — essentially free.
- Frontload: precompute each template's proxy box(es) + average albedo at load.
- A small hysteresis band on the switch distance avoids flicker as the player
  moves across the threshold.

---

## Step 3 — Bake the far field into a panorama (the highest-value frontload)

### Executive summary

Far-away scenery barely changes as you take a few steps — the mountains don't
shift. So we **render everything beyond ~200–400 m once into a low-resolution
360° image wrapped around the player** (a panorama / "environment map"), and rays
that make it past the near field just *look up a pixel* in that image instead of
marching kilometers of terrain and traversing hundreds of far buildings.

We only re-bake the panorama when the player has moved far enough that the
parallax (the subtle shift of near-vs-far objects) would actually be visible —
say every N meters — and we can spread that re-bake over several frames. Between
re-bakes, the entire distant world costs one texture sample per ray. This is the
single biggest answer to "long view distance over a huge map": it turns an
`O(distance × geometry)` march into `O(1)`.

### Notes

- A *layered* panorama (2–3 depth shells) preserves some hill parallax as you
  climb — worthwhile given the hill-climbing goal.
- Re-bake trigger: player movement threshold (and/or a time budget), amortized
  across frames to avoid a hitch.
- Pairs with Step 5's fog: the panorama *is* the far field, so the near field can
  fade into it seamlessly.

---

## Step 4 — Terrain at scale (hybrid height + mip pyramid + far-clip)

### Executive summary

Terrain is drawn by "ray marching": stepping a ray forward until it dips below
the ground height. Over kilometers that's a lot of steps. See **Terrain strategy**
above for the recommended three-ring approach (analytic near, coarse grid mid,
panorama far).

Within the mid band, the fix is a **min/max height pyramid** (a "mipmap": the
same heightfield stored at progressively coarser resolutions, each cell
remembering the lowest and highest ground beneath it). Far from any hill, a ray
consults a coarse level and leaps across huge empty regions in a few big steps,
only refining to fine steps near an actual hit. We already do a one-level version
of this with the coarse `cmin/cmax` grid; this generalizes it to many levels so
kilometer views in the mid band stay cheap.

Add a **hard far-clip** at the panorama hand-off (~400 m) so rays stop marching
terrain entirely once Step 3 takes over. Optional **fog** softens the transition
but is not required for performance (see terrain strategy / risks table).

### Notes

- "Maximum-mipmap heightfield tracing" is the standard name for the pyramid trick.
- The global coarse bake (no fBm) covers the mid band; Step 3 panorama covers
  everything beyond the far-clip — they meet at the horizon.
- **GPU coarse-DDA (single-level `cmin/cmax` upload) was tried and reverted
  (2025-06-17):** no measurable win on `outdoors-night-villa`; extra shader
  branches and a 16th storage buffer likely hurt more than the skip saved.
  Prefer a **multi-level mip pyramid** before revisiting coarse skip on GPU.
- **Streamed fine tiles** (Option 1) remain the fallback if the WGSL analytic
  near path is deferred.
- Far-clip is the cheapest immediate win and can land before the pyramid.
- **Indoor shadow terrain march (2025-06-17, HUD profiling):** `~time terr` stays
  ~25–28% even when staring at the villa floor — not screen coverage, but shadow
  rays. `shadowed()` in `trace.wgsl` always calls `hit_terrain` for every shadow
  ray that survives the blocker BVH, so floor pixels pay heightfield marching
  toward exterior lights/campfire (through openings) and for rays traversing the
  terrain AABB from inside the scene box. Toggling shadows `[2]` only drops the
  terrain time mix modestly (observed ~28% → ~21% indoors); the rest is primary/
  bounce terrain hits and shading attribution in the cost model.
  **Potential optimization:** cheap reject before `hit_terrain` in the shadow
  path — e.g. skip when origin is above local/coarse max height and the light ray
  points away from the ground; skip when the ray misses a tightened ground slab;
  optional max distance / mip-pyramid leap for shadow-only marches (same pyramid
  as mid-band primary). Goal: zero terrain steps for clearly indoor upward rays;
  keep parity on outdoor shadow silhouettes. Revisit alongside Step 4 mip pyramid
  (shadow rays benefit from the same coarse skip as primary rays).

---

## Step 5 — Localize the ambient-occlusion volume (this one *will* break otherwise)

### Executive summary

Ambient occlusion (AO) is the soft contact shadow in crevices and under eaves.
We precompute it into a 3D grid covering the scene (`aovolume.go`). That grid is
**dense and global**, capped at ~1M cells. Stretch it over 2 km and the cells are
forced to ~15 m each — AO becomes meaningless, and we'd waste time baking the
whole empty world. So the global single-volume assumption has to go.

Two ways to fix it, best paired with instancing:

- **Player-local window:** bake a small high-quality volume (the nice 0.45 m
  cells) only around the player, ~60–100 m, and re-bake the window as they move.
  Far AO is invisible at our resolution.
- **Per-template AO, instanced:** bake AO once in each *building's local space*
  and sample it through the instance transform (Step 1). Crisp contact shadows
  everywhere, near-zero marginal memory.

Either replaces "one immutable `tr.aoVol` shared across all goroutines" with
something tiled or instanced.

### Notes

- Player-local is simpler to ship; per-template is the better long-term fit and
  composes with Steps 1–2.
- Can be deferred until content actually approaches the cell-size cliff, but it is
  a hard blocker for the full 2 km target.

---

## Step 6 — Streaming + frustum culling around the player

### Executive summary

At 2 km you don't keep the whole world resident in memory. Carve the world into
a grid of tiles and only "stream in" (load + upload to the GPU) the buildings,
terrain tiles, and the local AO window for tiles near the player; let the
top-level tree hold just the active tiles. On top of that, a **frustum cull**
drops tiles outside the camera's view cone before we even build the per-frame
tree (the "frustum" is the pyramid of space the camera can see).

This keeps both memory and GPU buffer uploads bounded — which matters because the
device already had to request the adapter's full storage-buffer limits to fit our
current 11 buffers. Do this last: it's the bookkeeping layer that only pays off
once content genuinely exceeds what fits comfortably in memory.

### Notes

- Tile grid with per-tile world AABBs feeds both streaming and the cull test.
- Stream with hysteresis (load a ring slightly larger than the view radius) to
  avoid pop-in at the edges.

---

## What to frontload (load-time / offline)

- **Per-template BLAS** (build once, instance many) + **proxy box + average
  color** for distance LOD (Steps 1–2).
- **Per-template local AO volume** to replace the global one (Step 5).
- **Terrain:** global coarse height/normal bake + min/max mip pyramid; WGSL
  `heightAnalytic` for the near band (Option 3). Streamed fine tiles only if
  we fall back to Option 1 (Step 4).
- **Top-level world tile grid** with per-tile bounds for streaming + frustum cull
  (Step 6).
- **Initial far-field panorama** seed, then refreshed at runtime on a movement
  threshold (Step 3).
- Keep what already works: `Tracer.Prepare()` (AO bake up front), terrain
  height/normal baking, procedural textures evaluated on the fly (cheap on GPU,
  no atlas needed).

## Suggested ordering

1. **Land `Xform` transforms** (already the next geometry TODO) — but build the
   ray-transform path so a transformed payload can be a *sub-BVH*. This single
   decision buys instancing.
2. **TLAS/BLAS + instancing** (Step 1) → **distance-LOD proxies** (Step 2).
3. **Terrain far-clip**, then **hybrid height (Option 3) + mip pyramid** (Step 4).
4. **Shadow/light budget + near-tile streaming** (`hybrid-terrain-perf.md` Tiers
   A–B) — parallel with Step 4 if island is the acceptance scene.
5. **Far-field panorama** (Step 3).
6. **Localize the AO volume** (Step 5).
7. **Tile streaming + frustum cull** (Step 6) — last, once content exceeds memory.

Steps 1–4 mostly *reduce* per-frame work, so each can be validated with the
existing parity-gate discipline. A far LOD/impostor ring widens the error budget
at distance *by design*: gate the near field strictly and accept measured
far-field divergence.

## Risks / notes

| Risk | Mitigation |
| ---- | ---------- |
| LOD/impostor pops as you move | hysteresis on switch distance; fog hides the seam |
| Terrain near/mid seam (Option 3) | fade fBm over ~20 m; coarse bake omits fBm by design |
| Analytic CPU/GPU mismatch | parity tests on height grid; CPU keeps `heightAnalytic` for physics |
| Panorama parallax wrong while climbing | layered shells; re-bake on movement threshold |
| AO cell-size cliff at 2 km | player-local window or per-template AO (Step 5) |
| GPU storage-buffer / memory limits | tile streaming bounds resident set (Step 6) |
| Two renderers diverge | CPU stays the reference; gate near field at ≤1 LSB |

## Learnings from an MVP

We built a quick WebGPU panorama prototype on `outdoors-night-villa.toml` to
try Step 3 before investing in the rest of the large-map stack. The experiment
is reverted in code, but the results are worth keeping.

### What we built

- A 1024×512 equirect HDR bake from the player’s eye, refreshed on horizontal
  movement (~40 m).
- A hard terrain/water trace cap at `panorama_dist` (100–200 m in tests); on
  miss, sample the bake instead of marching further.
- TOML knob: `environment.panorama_dist`.

### What looked bad in practice

- **Cutoff sphere is visible.** At 100 m the world reads as a low-res cylinder:
  sharp 3D ground in front, then a dark horizontal band, then procedural sky.
  The seam jumps when the bake recenters on movement.
- **Wrong content in the bake.** Early versions stored sky and near-field
  geometry in the pano; the villa at ~90 m became a few orange pixels smeared
  across the horizon instead of the real building.
- **Sky must stay live.** Baking sky into equirect and sampling it back replaced
  crisp stars with blocky texels and bled horizon pixels into upward rays.
- **Buildings cannot share the terrain cap.** Capping BVH/primitive traces made
  mid-field architecture disappear into the pano; only terrain/water should
  honor the far clip.
- **This map is too small for the test.** Mountains sit ~30–40 m from the start
  on a 200×200 m heightfield; at 100–200 m cutoff the pano often did nothing
  useful or fought the full trace. The plan’s 200–400 m target assumes a much
  larger play space.

### What worked / validated the direction

- **Bake-on-move is feasible.** One GPU dispatch (~512k pixels) on a movement
  threshold is acceptable for a loading hitch; amortizing across frames is the
  next polish.
- **Alpha-masked far-only bake is the right model.** Store radiance only for
  hits beyond the cutoff; keep procedural sky for open directions; trace
  buildings to full distance.
- **Panorama is a far-field tool, not a replacement renderer.** It only makes
  sense once near/mid field (analytic terrain, instanced props, LOD) already
  owns everything within ~200 m. On a villa-sized map it cannot substitute for
  walking up to the house.

### Revised takeaways for Step 3

1. Implement panorama **after** terrain far-clip and hybrid near/mid terrain
   (Steps 4 + Option 3), not before.
2. Use **200–400 m** cutoff on large maps; do not use aggressive distances to
   “see it work” on small scenes.
3. **Never bake sky**; optional layered shells later for parallax on hills.
4. **Re-bake on horizontal movement only**, with hysteresis and optional
   cross-fade to hide pops.
5. Gate acceptance on **large outdoor scenes** where the pano actually replaces
   kilometers of terrain march, not on `outdoors-night-villa`.
