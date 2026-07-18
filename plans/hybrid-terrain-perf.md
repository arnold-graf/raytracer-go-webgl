# Plan: Full-Resolution Performance for Hybrid Large Terrain

## Status: PROPOSED

## Goal

Sustain **≥ 200 fps** (≤ 5 ms GPU shade per traced frame) on large hybrid terrain
maps such as `scenes/island.toml` at the **native internal resolution**
(512×320, ~164k pixels/frame) with shadows, mirrors, AO, instanced foliage, and
campfire lighting enabled.

## Non-goals

- **Lowering render resolution or `pixSize`** — not an acceptable tradeoff for
  this plan. Upscaling from a smaller buffer is out of scope.
- Replacing the megakernel path tracer with a rasterizer.
- Hardware RT.

## Reference scene

`scenes/island.toml` — 4 km × 4 km footprint, hybrid LOD (`coarse_cell = 1.5`,
`hybrid_near = [40, 50]`), ~46 terrain features, 12 instanced pine clusters,
art-nouveau villa, moon + campfire lighting, four lagoon water bodies.

Use this scene (and two camera poses below) as the acceptance benchmark:

| Pose | Camera | What it stresses |
| ---- | ------ | ---------------- |
| **Approach** | default `[0, 2.2, 144]` | Long terrain views, open sky, mid-band marching |
| **Villa** | `[48, 2.2, -64]` near campfire/villa | Shadow rays, BVH (trees + villa), campfire sub-lights |

```bash
go run ./cmd/gpuprof -scene scenes/island.toml -profile -warmup 3 -frames 20
go run ./cmd/gpuprof -scene scenes/island.toml -profile -cam-x 48 -cam-z -64
```

In-game: HUD `[0]` shows `gpu ms`, workload rates (`paths/px`, `shadows/px`,
`terrain steps`), and `~time terr/inst/prim/water` mix.

---

## Why hybrid terrain alone is not enough

The three-ring strategy in `plans/large-maps.md` (analytic near, coarse mid,
panorama far) solves **correctness and memory at scale**. It does **not** by
itself deliver 200 fps because:

1. **Per-pixel cost is fixed by resolution**, not map size — a 4 km map shades
   the same 164k pixels as a 200 m map.
2. **Shadow rays scale with local lights × shaded surfaces**, not screen
   coverage. **Campfire** (near the villa) contributes up to **4 shadow rays**
   per lit pixel in range (core + 3 sub-lights) — each runs blocker BVH +
   terrain mip march.
3. **The near analytic band is a small screen fraction** but a large **per-sample**
   cost: `terrain_natural_analytic` loops all features + 4-octave fBm + pads;
   finite-difference normals add 4× height evals per shaded near pixel.

Recent wins (landed or in flight):

| Change | Effect |
| ------ | ------ |
| Mip pyramid + `terrain_seg_near` | Primary rays outside near band march baked-only |
| Shadow rays baked march + analytic refine only in near segments | Cuts analytic work on shadows |
| Distance-gated normals (`terrain_normal` uses baked outside `hybrid_near` end) | Removes 4× analytic height on far shading |
| Coarser shadow march step (`base * 2` on shadow rays) | Fewer terrain steps per shadow test |

These are necessary but insufficient for 200 fps. The remaining budget gap is
dominated by **shadow-ray terrain marching** (especially near local lights like
campfire) and **primary-ray near-band analytic marching**, not by missing a
finer global bake.

---

## Cost model (how to read the HUD)

From `internal/webgpu/cost_model.go` and `internal/render/render.go`:

| Counter | Meaning | Island red flag |
| ------- | ------- | --------------- |
| `terrain steps` | Heightfield march steps per shadow/terrain test | > 30 → terrain shadow pain |
| `shadows/px` | Shadow rays cast per pixel | > 2 → light budget problem |
| `paths/px` | Path segments (bounces) | > 1.3 → mirrors/glass |
| `bvh steps/ray` | BVH node visits per ray | High near villa/trees |
| `~time terr %` | Estimated shader time in terrain bucket | > 40% → prioritize Tier A–B |
| `sh occ %` | Shadow rays blocked early | Low on open terrain = expensive misses |

**Gate:** do not merge perf work without a `gpuprof -profile` before/after on
both island camera poses, plus a visual check (no shadow leaks on ridges/trees).

---

## Tier A — Shadow and light budget (highest ROI)

Target: **2–4×** reduction in shadow-ray work. These changes preserve full
resolution and near-field visual quality.

### A1. Campfire shadow consolidation

**Problem:** Each campfire runs 1 core + 3 sub-light shadow tests per shaded
pixel in range (`shade.wesl` `shade_diffuse` loop).

**Plan:**

- **v1:** One merged shadow ray from a jittered representative point (core +
  sub-light centroid) with softened attenuation — revisit the reverted experiment
  with a wider kernel so quality is acceptable.
- **v2:** Campfire shadows only within `N` m of the fire (e.g. 20 m); beyond
  that, unshadowed flicker only.

**Acceptance:** Near-villa pose `shadows/px` drops ~3× for campfire-contributing
pixels; no obvious fire-light popping at range boundary.

### A2. Shadow-ray terrain fast path

**Problem:** `shadowed()` always calls `hit_terrain(..., refine=true, shadow_ray=true)`.
Even baked marching accumulates steps on long outdoor rays.

**Plan (incremental, each gated separately):**

1. **`refine=false` for all shadow terrain hits** — bisection is for primary
   precision; shadows only need hit/miss.
2. **Increase shadow march step** beyond current `2×` base (try `3–4×` with leak
   tests on ridge lines and tree shadows).
3. **`max_terrain_shadow_dist`** uniform — skip terrain component of `shadowed()`
   when `max_t` exceeds threshold (e.g. 80–120 m); rely on ambient + slope term.
4. **Stronger `terrain_shadow_skip`** — reject rays that cannot intersect ground
   slab before `max_t` using coarse mip bounds (extend current skip logic).

**Acceptance:** `terrain steps` counter drops ≥ 30% on approach pose; no new
shadow leaks on villa roofline or pine clusters (preview orbit + walk-through).

### A3. Per-shade shadow ray cap

**Plan:** In `shade_diffuse`, evaluate shadows for at most **K** contributions
per pixel (K = 2 for v1), prioritized by `att × ndl × throughput`. Skip the rest
unshadowed or with a cheap ambient term.

**Acceptance:** Worst-case `shadows/px` bounded; visible only when many lights
overlap (rare on island).

---

## Tier B — Terrain near/mid field (medium ROI)

Target: **1.3–2×** on ground-level views. Complements `plans/large-maps.md` Step 4.

### B1. Streamed near-field bake (heights + normals)

**Problem:** Primary-ray marching in the near band still calls
`terrain_height_analytic` per step. Normals in the band still use 4× finite diff
on blended height.

**Plan:**

- CPU: maintain a **camera-centered tile** (e.g. 80×80 m at 0.25–0.5 m cells)
  baked with full `heightAnalytic` + precomputed normals.
- GPU: sample near tile when `dist < nearEnd`; cross-fade to global coarse bake
  over the existing `hybrid_near` fade band.
- Double-buffer tile; rebake when camera moves > ½ tile (async on worker goroutine;
  upload on scene dirty path).

**Memory:** ~100k–400k samples ≈ 0.4–1.6 MB — negligible.

**Acceptance:** `PROF_TERRAIN_STEPS` on primary rays drops near camera; no seams
at tile boundary (visual gate); CPU rebake hitch < 16 ms amortized.

**Note:** Streaming **normals only** is a small win (~5–15% terrain bucket);
must include **heights** to affect marching cost. See analysis in chat 2026-07-18.

### B2. Tighten hybrid band (scene + shader)

- Reduce `hybrid_near` end from 50 m → 30–35 m on island after B1 ships.
- Ensure `terrain_seg_near` + baked normals + baked far march stay consistent at
  the new boundary.

### B3. Terrain far-clip (from large-maps Step 4)

- Hard cap primary terrain trace at `panorama_dist` (200–400 m on island).
- Pair with Step 3 panorama when ready; until then, sky + coarse last mip level
  is acceptable for rays beyond clip.

---

## Tier C — Geometry and instances (medium ROI, scene-dependent)

Target: **1.3–2×** on villa pose where `~time inst %` and `~time prim %` rise.

### C1. Instance LOD (Step 2 from large-maps)

- Pine clusters beyond 60–80 m → proxy box or card impostor (no per-needle BVH).
- **Shadow cast off** for tree instances beyond `N` m (e.g. 50 m) — terrain and
  villa still cast.

### C2. Villa blocker simplification

- Audit `art-nouveau-villa.toml` blocker mesh — merge coplanar shadow blockers,
  drop interior-only blockers from TLAS shadow set.

### C3. Water / terrain overlap

- Lagoon `terrain_height()` checks in `hit_water` — ensure early-out when water
  cell is far from camera and terrain is below water level (minor).

---

## Tier D — Renderer architecture (required if Tiers A–C plateau below 200 fps)

Full resolution, full features, 200 fps likely needs **decoupling lighting from
the megakernel**. Ordered by complexity:

| Item | Idea | Est. gain |
| ---- | ---- | --------- |
| **D1. Half-res shadow mask pass** | Trace shadows at 256×160, bilateral upsample | 2–3× on shadow cost |
| **D2. Temporal shadow reuse** | Reuse shadow results 1–2 frames; validate on camera motion | ~2× on shadows |
| **D3. Tiled light culling** | Per-tile light list; skip dead lights before `shade_diffuse` | Scales with light count |
| **D4. Panorama far field** | `plans/large-maps.md` Step 3 — after far-clip + hybrid stable | Kills km marches |
| **D5. ReSTIR / reservoir DI** | Many lights, few shadow rays | Future; large shader refactor |

Explicitly **not** in v1 per `webgpu-port.md`: temporal accumulation/TAA as a
quality feature — but **temporal shadow reuse** (D2) is a perf-only mask cache,
not accumulation.

---

## Quality presets (without resolution scaling)

Ship three **content/shader** presets, not resolution tiers:

| Preset | Shadows | Near field | Instances | Target |
| ------ | ------- | ---------- | --------- | ------ |
| **Quality** | Full | Analytic or streamed 0.25 m | Full | 60–90 fps |
| **Balanced** | Merged campfire shadows | Streamed 0.5 m | LOD at 80 m | 120–160 fps |
| **Performance** | A2 + A3 + capped campfire range | Coarse only (hybrid off) | Impostors at 60 m | 180–200+ fps |

TOML/scene flags or runtime toggle — same 512×320 buffer for all.

---

## Suggested implementation order

1. **Measure baseline** — `gpuprof -profile` both island poses; record counters.
2. **A2** shadow terrain fast path (low risk, incremental).
3. **A1** campfire shadow merge / range limit.
4. **B1** streamed near tile (if primary `terrain steps` still high after A).
5. **C1** instance LOD + shadow cast distance.
6. **B3** terrain far-clip.
7. Reassess; if still < 200 fps, **D1** half-res shadows.

Cross-cutting: update `cost_model.go` weights after each tier; extend `gpuprof`
ablation matrix with island-specific configs (campfire shadow rays, terrain march).

---

## Risks

| Risk | Mitigation |
| ---- | ---------- |
| Shadow leaks after coarser march | Ridge/tree test suite; `gpuprof` + preview orbits |
| Near-tile seams | Cross-fade band; rebake margin overlap |
| Campfire merge looks wrong | Soft kernel; keep 4 lights for shading, 1 for shadow |
| 200 fps unreachable at full features | Presets + honest HUD; revisit D-tier |
| CPU rebake hitches (B1) | Async bake; double-buffer; movement threshold |

---

## Relationship to other plans

| Plan | Relationship |
| ---- | ------------ |
| `large-maps.md` | Owns three-ring terrain strategy, panorama, streaming tiles, AO localization — this plan owns **per-frame GPU cost** within the near/mid bands |
| `webgpu-port.md` | Parity gates still apply; perf tactics that skip work must be gated |
| `bvh-acceleration.md` | Instance LOD reduces BVH pressure cited for outdoor scenes |
| `glass-gpu.optimization.md` | Mirror/glass bounce cost is secondary on island; watch `paths/px` |

---

## Definition of done (v1)

- [ ] `gpuprof` island approach pose: **≥ 200 fps** mean over 20 frames at 512×320,
  all features on, **Performance** preset.
- [ ] Same measurement, **Balanced** preset: **≥ 120 fps** with no resolution scaling.
- [ ] Visual gate: preview orbit + walk villa → campfire → lagoon; no terrain holes,
  no obvious shadow leaks on ridges.
- [ ] Document final counter values and preset TOML flags in this file.
