# Plan: WebGPU Renderer Port

## Status: COMPLETE — CPU renderer removed

The WebGPU compute renderer is now the **only** renderer. The CPU ray tracer
(`internal/trace`) and the CPU framebuffer renderer (the old `internal/render`
implementation) have been deleted. What survived from the CPU side:

- **`internal/probe`** — the ray-vs-scene distance query (acoustics) and the
  baked ambient-occlusion volume, both fed to the GPU / audio rather than used
  for rendering.
- **`internal/texture`** — procedural texture *generators* (still authored on
  the CPU and ported to `shaders/trace.wgsl`).
- **`internal/bvh`** — the acceleration structure, used by `internal/probe`.

The `-renderer` flag is gone (`main.go` always uses WebGPU and exits if no
adapter is available). The renderer now takes a `render.View` (scene + clock +
shadow/mirror/AO toggles + baked AO) instead of a `*trace.Tracer`. The GPU-vs-CPU
parity tests were removed with the CPU oracle; layout/packing tests and an
end-to-end GPU render/cache test remain.

The historical plan below is kept for context.

## Goal

Add a **WebGPU compute** renderer that produces the **same image** as the
current CPU tracer, but at much higher frame rate. The CPU renderer remains the
default and the correctness reference until GPU parity is proven.

**Non-negotiables (from product intent):**

1. **Fidelity first** — same look, not a "better" look. No new AA, no different
   BRDF, no temporal tricks in v1.
2. **Simplest GPU code** — one compute shader, one dispatch per frame, boring
   data layout. No shader framework, no wavefront path tracer, no multi-pass
   deferred pipeline.
3. **Safe rollout** — every phase is shippable behind `-renderer cpu|webgpu`;
   CPU path never regresses; bad GPU frames never crash the app.

## Target outcome

| Metric | CPU today (indoor-outdoor, 400×250) | GPU target |
| ------ | ----------------------------------- | ---------- |
| Frame time | ~10–35 ms (view-dependent) | **≤ 4 ms** (≥ 250 fps headroom) |
| Resolution | 400×250 internal | same, then 800×500 optional |
| Features | mirror, shadow, AO, textures, terrain, campfire | identical toggles |
| Visual | reference | ≤ 1 LSB avg error vs CPU on canonical views |

## CPU vs GPU split

| Stays on CPU (Go) | Moves to GPU (WGSL compute) |
| ----------------- | --------------------------- |
| TOML load / `extends` / hot reload (`sceneio`) | Primary ray generation |
| Camera + player movement + collision (`camera`, `scene` physics) | BVH traversal + all primitive intersections |
| BVH **build** (SAH, once per reload) | Shadow rays + light culling |
| Terrain height/normal/coarse grid **bake** | Terrain ray marching |
| AO volume **bake** | AO volume sampling |
| Scene → packed GPU buffers (new `prepare` step) | Procedural textures + skies |
| Hot reload orchestration (`app`) | Shading (diffuse, reflect, glass, semi-reflect) |
| **CPU renderer** (forever, as oracle) | Campfire flicker |
| Input, HUD, feature toggles | Tonemap + gamma → storage texture |

**Rule of thumb:** if it runs once per scene reload → CPU. If it runs per pixel
per frame → GPU.

## Architecture (keep it boring)

```text
TOML ──► scene.Scene ──► PreparedScene ──┬──► CPURenderer  (existing)
                                          │         ▲
                                          │    parity tests
                                          └──► GPUPrepared ──► WebGPURenderer
                                                    │
                                              WGSL megakernel
```

### New packages (proposed)

```text
internal/
  render/          # add Renderer interface; CPU impl stays here
  gpuscene/        # PreparedScene: pack scene → []byte / GPU structs
  webgpu/          # device, pipeline, buffer upload, dispatch, present
  webgpu/shaders/  # *.wgsl (one file per domain, #include via string concat)
  parity/          # headless CPU vs GPU image diff (for tests + CI)
```

### Renderer interface

```go
type Renderer interface {
    Render(cam *camera.Camera, tr *trace.Tracer, pixSize int, buf []byte)
    // GPU impl writes to internal texture; optional Readback for parity tests.
}
```

`-renderer cpu` (default) | `-renderer webgpu`. Same `app.Game` loop, same
hot reload, same toggles.

## GPU design: one compute pass ("megakernel")

**Why one pass:** simplest mental model, highest perf for this scene size.
Each workgroup item = one pixel. No intermediate G-buffers, no separate shadow
pass, no wavefront queues.

```text
for each pixel (gpu_id):
    ray = camera_ray(pixel)
  for bounce in 0..2:
    hit = intersect_all(ray)      // BVH + planes + terrain + water
    if miss: return sky(ray.dir)
    if emit: return hit.albedo
    if mirror/metal/glass: continue bounce
    color = shade(hit)            // lights, shadows, AO, semi-reflect
    return tonemap(color)
  write storage_texture[pixel]
```

This mirrors `trace.li` / `shade` almost line-for-line. Resist splitting into
multiple compute passes until profiling proves a hotspot.

### Scene data on GPU (flat, explicit)

Upload on scene reload / hot reload. No pointers, no variable-length indirection
in shaders.

| Buffer / texture | Contents | Source |
| ---------------- | -------- | ------ |
| `uniforms` | camera basis, aspect, fov, time, feature flags, env (sky id, sun, ambient) | per frame |
| `primitives` | tagged array: kind, idx, mat, albedo, rough, ior, reflect, tex, bounds… | `scene.*` slices |
| `bvh_nodes` | flattened SAH nodes (min, max, left, right, start, count) | `bvh.BVH` |
| `lights` | pos, color (brightness baked), range, cullR², invR² | `scene.Lights` |
| `campfires` | center, color, brightness, range, jitter, flicker, speed, seed | `scene.Campfires` |
| `terrain` | origin, size, base, detail, height grid, coarse min/max, pads | `scene.Terrain` |
| `ao_volume` | 3D texture, ambient-cube 6-tap data | `trace.aoVolume` |
| `output` | `rgba8unorm` storage texture | per frame |

**Packing rule:** every GPU struct has a matching Go struct in `gpuscene` with
`//go:align` or explicit padding comments. Write a unit test that
`unsafe.Sizeof` matches the WGSL layout.

### WGSL file layout (mirror Go packages)

Keep functions small and named like their Go originals. Concatenate at init:

| File | Mirrors | Notes |
| ---- | ------- | ----- |
| `math.wgsl` | `vec`, `fmin`/`fmax`/`clamp` | vec3 ops, reflect, refract |
| `camera.wgsl` | `camera.Ray` | |
| `intersect.wgsl` | `scene.(*X).Intersect` | sphere, box, cyl, cone, torus, plane |
| `bvh.wgsl` | `bvh.Nearest`, `bvh.AnyHit` | stack-based traversal |
| `terrain.wgsl` | `terrain.march`, `Height`, `Normal` | upload grids, don't re-eval analytic field |
| `noise.wgsl` | `texture/noise.go` | perm table as `const` array |
| `textures.wgsl` | `texture/*.go` | one `eval_tex(id, p, base)` switch |
| `sky.wgsl` | `trace.*Sky` | all 5 variants |
| `shade.wgsl` | `trace.shade`, `addPointLight`, campfire loop | |
| `tonemap.wgsl` | `trace.ToneMap`, gamma LUT | 4096-entry `const` array |
| `trace.wgsl` | `trace.li` | orchestrates bounce loop |
| `main.wgsl` | `render.renderRow` | `@compute` entry, one thread per pixel |

**No macros. No code generation beyond string concat.** If a Go function is
50 lines, the WGSL version should be ~50 lines with the same name.

### Constants sync (fidelity)

These **must** match between Go and WGSL. Put them in one Go file
`internal/gpuscene/constants.go` and emit a `constants.wgsl` snippet at init
(or keep a checked-in copy + a test that diffs them):

- `eps`, `aoMaxDist`, `aoVol*` bake params
- `lightCullEps`, attenuation `0.5 + 0.08*d²`
- `gammaLUTSize`, tonemap coefficients
- `wpTileW`, `wpTileH`, wallpaper palettes
- terrain `step`, grid cell sizes
- BVH `leafSize`, `sahBins`

Add `TestConstantsMatchWGSL` that parses the emitted snippet.

## Window / presentation

**Do not** try to share Ebiten's GL context with WebGPU. Two clean options:

1. **Recommended:** WebGPU renderer owns its own window via `glfw` + wgpu
   surface. Reuse `app.Game` logic for input/camera/reload; only swap the draw
   backend. Slightly more wiring, zero fighting Ebiten internals.

2. **Fallback:** WebGPU renders offscreen; readback to CPU `[]byte`; Ebiten
   `WritePixels` as today. **Only for early parity tests** — readback kills the
   perf win and must not ship as the real path.

## Phased rollout (each phase = safe checkpoint)

Every phase ends with: `go test ./...` green, CPU unchanged, optional GPU
parity snapshot. Merge when the phase passes its gate.

### Phase 0 — Skeleton

- [x] Add `github.com/rajveermalviya/go-webgpu/wgpu` (or `cogentcore/webgpu`)
- [x] `internal/webgpu`: open device, compile empty compute shader, write gradient
- [x] `-renderer webgpu` flag; app still defaults to CPU
- [ ] **Gate:** window opens, gradient visible, CPU path unaffected

### Phase 1 — Camera + sky

- [x] Port `camera.Ray`, `clearSky` (only)
- [x] Uniform buffer: camera basis, aspect, fov
- [ ] **Gate:** GPU sky matches CPU `clearSky` on 4 fixed rays (unit test passes);
  visual match on default scene (no geometry)

### Phase 2 — Primitives + diffuse

- [x] `webgpu.PackPrimitives`: spheres, boxes, planes → std430 storage buffer
  (`GPUPrimitive`, 64-byte stride, layout-guarded by `TestGPUPrimitiveLayout`).
  Transforms / box holes / plane checker deferred to later phases.
- [x] Port intersections (sphere, plane, box slab) + `shade` ambient-only
  (flat 0.04), emissive passthrough, sky on miss
- [x] Port tonemap/gamma + ordered dither (exact `clampByte(col*255+bdt)` match)
- [x] **Gate:** CPU↔GPU parity test passes with mirror/shadow/AO **off**.
  Uses a controlled diffuse scene (`diffuseScene`) instead of `default.toml`
  because default's lights/emit/checker need Phase 3+; mean err 0.03 LSB,
  outliers (edges) 0.03% of pixels. Re-point the gate at `default.toml` once
  lights + checker land.

### Phase 3 — Lights + shadows

- [x] Upload static point lights with CPU-matched precomputed cull/range data
  (`GPULight`, 48-byte stride, layout-guarded by `TestGPULightLayout`)
- [x] Upload shadow-casting primitive array (sphere/plane/box blockers,
  excluding emissive/glass where the CPU blocker BVH does)
- [x] Port `addPointLight` attenuation, range window, contribution culling, hit
  normals and hard `shadowed` rays. Shadow traversal now uses the GPU blocker
  BVH for finite primitives; infinite planes remain a separate loop, matching
  the CPU tracer split.
- [x] **Gate:** CPU↔GPU parity with `Shadow` on passes on controlled lit diffuse
  scene (`TestPointLightShadowParityMatchesCPU`): mean err 0.05 LSB, outliers
  0.03% of pixels (shadow/silhouette edges).
- [ ] Re-point gate to `indoor-outdoor` interior view once Phase 4+ cover BVH,
  textures, transformed/holed boxes, cylinders/cones, terrain, campfires and
  hemispheric/sun lighting.

### Phase 4 — BVH on GPU

- [x] Upload CPU-built SAH BVH node/index arrays for currently ported finite
  primitives (spheres/boxes). Planes stay outside the BVH because they are
  infinite, matching the CPU tracer.
- [x] Port `bvh.Nearest` / `AnyHit` style traversal to WGSL for primary hits and
  shadow blockers (`BVHNode` + primitive-index leaf refs, 64-entry stack)
- [x] Remove brute-force finite-primitive loops from WGSL. Only plane loops
  remain until non-boundable/infinite primitives are handled separately.
- [x] **Gate:** existing Phase 2/3 CPU↔GPU parity still passes after traversal
  swap (`TestPrimitiveParityMatchesCPU`, `TestPointLightShadowParityMatchesCPU`)
  with unchanged error bounds.
- [x] Cylinder, cone and torus added to the GPU prim buffer + BVH bounds, so all
  finite analytic kinds now traverse the BVH.
- [x] Arbitrary primitive transforms (`Xform`): each prim carries its
  world→local rotation rows + translation (`Xf0..Xf2`); the BVH bounds enclose
  the transformed corners and `hit_prim` maps the ray into local space, with the
  normal rotated back. Gated by `TestTransformParityMatchesCPU` (mean 0.01 LSB).
- [x] Box-hole CSG: each box carries a (start,count) range into a shared holes
  buffer; `hit_box` runs the same segment-subtraction difference as the CPU and
  the normal picks the nearest (negated) hole face. Composes with transforms.
  Gated by `TestBoxHoleParityMatchesCPU` (mean 0.01 LSB).

### Phase 5 — Procedural textures

- [x] Port exact `noise.go` Perlin permutation table (uploaded as a storage
  buffer) plus `perlin`/`fbm`/`turbulence`/`cellRand`, then faithful WGSL ports
  of every texture: wood, brick, stone, cement, marble, grass, dirt, snow,
  wallpaper (3 variants). Terrain albedo jitter and water ripple normals now use
  the same exact Perlin.
- [x] Brick parity: the per-brick cell hash was the one texture that diverged
  (the old `frac(sin(x)*43758)` is chaotic and f32/f64 disagree on large
  arguments). Reworked `texture.cellRand` into an integer bit-mix hash that the
  WGSL `cell_rand` reproduces bit-for-bit, so brick now matches.
- [x] **Gate:** `TestTextureParityMatchesCPU` (marble/stone/wood/wallpaper/
  cement/grass/**brick**): mean err 0.10 LSB, no outliers.

### Phase 6 — Reflections + semi-reflect

- [x] Port bounded bounce loop: mirror, metal, rough jitter, diffuse `reflect`
  blend, and thin-pane glass Fresnel/refraction path.
- [x] Glass now blends **both** lobes like the CPU (refraction tinted by albedo
  *and* the Fresnel reflection of the world in front), instead of selecting a
  single lobe. `ray_color` is a bounded work-stack ray-tree evaluator (max 16
  live segments) that transcribes `trace.li`, gated by `params.mirror` to match
  `tr.Opts.Mirror`. The depth cap now falls through to diffuse shading exactly
  like the CPU, which also tightened mirror/reflect parity.
- [x] Analytic checker plane material (`Plane.AlbedoAt`) ported with a per-prim
  second albedo; gated by `TestCheckerParityMatchesCPU` (mean 0.16 LSB).
- [x] **Gate:** `TestReflectionParityMatchesCPU` (mean 0.02 LSB, no outliers)
  and `TestGlassParityMatchesCPU` (mean 0.01 LSB, no outliers).

### Phase 7 — Terrain + water + pads

- [x] Upload CPU-baked terrain height/normal grids as compact samples
  (`normal.xyz,height`) plus terrain material/pad-baked descriptors. Pads stay in
  Go and are already baked into the uploaded height grid.
- [x] Port terrain slab + adaptive fine march + bisection refinement,
  `IntersectWithin`-style max distance cap, terrain normals/albedo blend, terrain
  shadow occlusion, and water pool disk/ripple normals
- [x] **Gate:** focused flat terrain + water parity
  (`TestTerrainWaterParityMatchesCPU`): mean err 0.04 LSB, no outliers
- [ ] Coarse-DDA terrain skipping is a perf-only follow-up (parity already met).
  **Attempted on GPU (2025-06-17):** uploaded CPU `cmin/cmax` bands + WGSL
  `hit_terrain_coarse` DDA (binding 14, terrain stride 128→160). No reliable
  win in `gpuprof` or in-game; possibly slower (extra indirection on every
  terrain march, coarse grid too shallow for this scene's ray lengths). **Reverted.**
  CPU already skips empty coarse cells; a multi-level mip pyramid (see
  `plans/large-maps.md` Step 4) is the better next terrain lever if needed.

### Phase 8 — AO volume + campfires

- [x] Upload the CPU-baked ambient-occlusion volume (ambient cube, six faces per
  cell) as a storage buffer; GPU samples it with the exact `aoVolume.sample`
  trilinear + face-blend logic. Device now requests the adapter's full limits so
  the 11 storage buffers (prims, lights, blockers, bvh, terrain, terrain samples,
  water, perm, AO, campfires, output) fit past the default 8-buffer cap.
- [x] Port campfire `LightAt` flicker + the shared core shadow early-out + the
  per-sub-light shadow rays (the "dancing shadows"). Sub-lights are resolved on
  the CPU each frame at `tr.Time` and packed with the cluster cull radius.
- [x] **Gate:** `TestAOVolumeParityMatchesCPU` (mean 0.05 LSB) and
  `TestCampfireParityMatchesCPU` (mean 0.03 LSB).

### Geometry feature coverage — COMPLETE

Every analytic geometry feature the CPU renderer supports now has a GPU port
with a focused parity gate: spheres, planes, boxes, cylinders, cones, tori,
checker planes, all procedural textures (incl. brick), arbitrary transforms and
box-hole CSG. The remaining open items are whole-scene integration gates
(pointing the harness at `indoor-outdoor.toml` etc.) and the standalone
`cmd/parity` tool, which are wired up alongside Phase 9 hot reload.

### Phase 9 — Hot reload + polish

- [x] GPU buffer rebuild on scene reload (reuses existing `app.checkReload`).
  `reloadScene` swaps in a fresh `trace.Tracer` (new `*scene.Scene`); the
  WebGPU `sceneCache` is keyed on (scene pointer, `Generation()`), so the next
  `Render` sees a stale cache and re-packs/re-uploads every static buffer. No
  webgpu-specific reload plumbing was needed — the cache contract from Phase 8.5
  already covers it.
- [x] Invalidate AO bake + BVH on reload. `reloadScene` calls `tr.Prepare()`
  (re-bakes the AO volume) and `cache.rebuild` re-runs `PackBVH`/`PackAOVolume`
  from the new scene, so both follow the geometry edit live.
- [x] HUD shows the active backend (`cpu`/`webgpu`, via the optional
  `render.BackendNamer`) alongside fps + the transient reload status toast.
  Pressing `0` hides the dev overlay for a clean view while keeping the reload
  toast so edits are still confirmed.
- [x] **Gate:** edit a watched TOML (top-level or an `[[include]]` like
  `objects/building.toml`), save, and the GPU scene updates live; a parse error
  mid-save keeps the current scene and is retried next poll (no crash), since
  `reloadScene` only swaps on success.

### Phase 10 — Default to GPU (when ready)

- [ ] Flip default `-renderer` to `webgpu` on supported platforms
- [ ] Keep CPU as `-renderer cpu` fallback
- [ ] Document in README

## Parity harness (build this in Phase 2, use forever)

> Status: an in-package parity check exists (`compareFrames` in
> `internal/webgpu/device_test.go`) reporting mean/max error and outlier
> fraction, gating Phase 2. A standalone `cmd/parity` with the flags below and
> the 5 canonical testdata views is still to be built (folded into Phase 3+).

```text
cmd/parity/
  -scene scenes/indoor-outdoor.toml
  -camera 16,2.4,1.8,0,-0.12
  -w 400 -h 250
  -tol 0.004        # ~1/255 per channel
  -o diff.png       # optional heatmap
```

- Renders same view on CPU and GPU
- Reports: mean error, max error, % pixels over tolerance
- Exit code 1 on failure → CI gate
- **Canonical views** (check in as testdata):
  1. `default.toml` — center, mirror ball
  2. `textured.toml` — all procedural textures
  3. `indoor-outdoor.toml` — interior wallpaper + campfire + terrain
  4. `outdoors.toml` — terrain + water + sky
  5. `outdoors-night-stars.toml` — stars + campfire shadows

Run parity on every phase gate. Never merge a phase that widens the error budget.

## Performance tactics (simple, high impact)

Do these **after** parity, not before — premature GPU optimization breaks fidelity
debugging.

For **large hybrid terrain maps** at full 512×320 (no resolution scaling), see
[`plans/hybrid-terrain-perf.md`](hybrid-terrain-perf.md) — shadow/light budget,
streamed near tile, and instance LOD tiers targeting 200 fps on `island.toml`.

1. **No CPU readback** in the render loop (storage texture → surface present).
2. **Bind groups stable** — only `uniforms` and `output` change per frame; scene
   buffers only on reload.
3. **SOA not needed yet** — one interleaved primitive buffer is fine for <500
   objects; revisit only if profiling shows divergence.
4. **WGSL `f32` everywhere** — matches Go `float64` closely enough; document
   any intentional `f32` in constants test.
5. **Workgroup size 8×8** — tune once on M2 Max; don't over-tune per platform
   in v1.
6. **Skip texture cache on GPU** — the CPU cache exists because procedural eval
   is expensive on CPU; on GPU, re-evaluating Perlin is cheap. Simpler shader.

## What we explicitly do NOT do in v1

- Hardware RT (no benefit for analytic primitives; M2 Max has none anyway)
- Wavefront / path-reuse / ReSTIR
- Temporal accumulation / TAA
- Separate shadow or AO compute passes
- Triangle mesh support
- Shader code generation from Go (beyond constants snippet)
- Replacing the CPU renderer
- Ebiten+WebGPU context sharing

## Risk register

| Risk | Mitigation |
| ---- | ---------- |
| WGSL/Go math drift | parity harness + constants test; tolerate ≤1 LSB |
| wgpu-native build pain on macOS | pin wgpu version; document `brew install` deps |
| Hot reload hitch (AO bake) | same as CPU today; show HUD "reloading…"; async bake later |
| Shader debugging is hard | debug views (normal/albedo/depth/material id); keep CPU oracle |
| Two renderers diverge | CPU is reference only; no new features land GPU-only |
| Scope creep | phase gates; v1 = parity, not improvement |

## Definition of done

- [ ] `-renderer webgpu` renders indoor-outdoor at ≥60 fps, 400×250, all features on
- [ ] Parity harness passes all 5 canonical views at ≤1 LSB mean error
- [ ] Hot reload works for scene + player TOML
- [ ] CPU renderer unchanged and still default until Phase 10
- [ ] `plans/webgpu-port.md` status updated to IMPLEMENTED with benchmark table

## First session checklist (start here)

When picking this up, do **only Phase 0 + start Phase 1**:

1. [x] `go get github.com/rajveermalviya/go-webgpu/wgpu@latest`
2. [x] Create `internal/webgpu/device.go` — init instance/adapter/device
3. [x] Create `internal/webgpu/shaders/gradient.wgsl` — sanity compute shader
4. [x] Wire `-renderer webgpu` in `main.go` (no-op fallback if device fails)
5. [ ] Confirm gradient in window; `go test ./...` still green

Do not port intersections until sky parity passes.
