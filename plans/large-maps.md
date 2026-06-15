# Plan: Large Maps (2×2 km, hundreds of buildings, long view distance)

## Status: PROPOSED

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

## Step 4 — Terrain at scale (mip pyramid + tiling + far-clip)

### Executive summary

Terrain is drawn by "ray marching": stepping a ray forward until it dips below
the ground height. Over kilometers that's a lot of steps. The fix is a **min/max
height pyramid** (a "mipmap": the same heightfield stored at progressively
coarser resolutions, each cell remembering the lowest and highest ground beneath
it). Far from any hill, a ray consults a coarse level and leaps across huge empty
regions in a few big steps, only refining to fine steps near an actual hit. We
already do a one-level version of this with the coarse `cmin/cmax` grid; this
generalizes it to many levels so kilometer views stay cheap.

Two supporting moves: **tile** the dense height/normal caches so we keep fine
data only near the player (a full-resolution 2 km cache is hundreds of MB), and
add a **hard far-clip with atmospheric fog** so rays stop at a fixed distance and
fade to haze. The fog bounds ray length *and* hides the moment where LOD/impostor
swaps happen — and a hazy distance reads as an epic vista anyway.

### Notes

- "Maximum-mipmap heightfield tracing" is the standard name for the pyramid trick.
- Beyond the fine-tile ring, the Step 3 panorama covers the terrain, so tiling
  and the far field reinforce each other.
- Fog + far-clip is also the cheapest immediate win and can land before the
  pyramid.

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
- **Terrain min/max mip pyramid + tiled height/normal caches** (extends the
  existing `cmin/cmax`) (Step 4).
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
3. **Terrain far-clip + fog**, then **mip pyramid + tiling** (Step 4).
4. **Far-field panorama** (Step 3).
5. **Localize the AO volume** (Step 5).
6. **Tile streaming + frustum cull** (Step 6) — last, once content exceeds memory.

Steps 1–4 mostly *reduce* per-frame work, so each can be validated with the
existing parity-gate discipline. A far LOD/impostor ring widens the error budget
at distance *by design*: gate the near field strictly and accept measured
far-field divergence.

## Risks / notes

| Risk | Mitigation |
| ---- | ---------- |
| LOD/impostor pops as you move | hysteresis on switch distance; fog hides the seam |
| Panorama parallax wrong while climbing | layered shells; re-bake on movement threshold |
| AO cell-size cliff at 2 km | player-local window or per-template AO (Step 5) |
| GPU storage-buffer / memory limits | tile streaming bounds resident set (Step 6) |
| Two renderers diverge | CPU stays the reference; gate near field at ≤1 LSB |
