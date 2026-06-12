# Plan: WebGPU Renderer Port

## Status: NOT STARTED

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

```
TOML ──► scene.Scene ──► PreparedScene ──┬──► CPURenderer  (existing)
                                          │         ▲
                                          │    parity tests
                                          └──► GPUPrepared ──► WebGPURenderer
                                                    │
                                              WGSL megakernel
```

### New packages (proposed)

```
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

```
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

### Phase 0 — Skeleton (≈ 1 day)

- [ ] Add `github.com/rajveermalviya/go-webgpu/wgpu` (or `cogentcore/webgpu`)
- [ ] `internal/webgpu`: open device, compile empty compute shader, write gradient
- [ ] `-renderer webgpu` flag; app still defaults to CPU
- [ ] **Gate:** window opens, gradient visible, CPU path unaffected

### Phase 1 — Camera + sky (≈ 1 day)

- [ ] Port `camera.Ray`, `clearSky` (only)
- [ ] Uniform buffer: camera basis, aspect, fov
- [ ] **Gate:** GPU sky matches CPU `clearSky` on 4 fixed rays (unit test);
  visual match on default scene (no geometry)

### Phase 2 — Primitives + diffuse (≈ 2–3 days)

- [ ] `gpuscene.Pack`: spheres, boxes, planes → storage buffer
- [ ] Port intersections + `shade` with ambient only (no lights yet)
- [ ] Port tonemap/gamma
- [ ] **Gate:** `parity.Compare(cpu, gpu, default.toml, tol=1/255)` passes on
  3 fixed cameras with mirror/shadow/AO **off**

### Phase 3 — Lights + shadows (≈ 2 days)

- [ ] Upload lights with precomputed cull data
- [ ] Port `addPointLight`, `shadowed`, blocker BVH `AnyHit`
- [ ] **Gate:** parity with shadow on; indoor-outdoor interior view

### Phase 4 — BVH on GPU (≈ 1–2 days)

- [ ] Upload CPU-built SAH BVH node array
- [ ] Port `bvh.Nearest` / `AnyHit` traversal
- [ ] Remove brute-force primitive loop
- [ ] **Gate:** parity on textured.toml + indoor-outdoor; profile shows BVH working

### Phase 5 — Procedural textures (≈ 2–3 days)

- [ ] Port `noise.go` (perm table as const)
- [ ] Port all textures: wood, brick, stone, cement, marble, grass, dirt, snow,
  wallpaper (3 variants)
- [ ] **Gate:** parity on textured.toml + indoor-outdoor wallpaper walls

### Phase 6 — Reflections + semi-reflect (≈ 1–2 days)

- [ ] Port bounce loop: mirror, metal, glass, `reflect` blend
- [ ] **Gate:** parity on default.toml (reflective floor) with mirror on

### Phase 7 — Terrain + water + pads (≈ 2–3 days)

- [ ] Upload terrain height/normal/coarse grids + pad params
- [ ] Port `terrain.march`, `IntersectWithin`, water pool
- [ ] **Gate:** parity on outdoors.toml + indoor-outdoor.toml (pad under room)

### Phase 8 — AO volume + campfires (≈ 1–2 days)

- [ ] Upload AO 3D texture (CPU still bakes; GPU samples)
- [ ] Port campfire `LightAt` flicker + shadow gate
- [ ] **Gate:** full parity on indoor-outdoor.toml, all toggles on

### Phase 9 — Hot reload + polish (≈ 1 day)

- [ ] GPU buffer rebuild on scene reload (reuse existing `app.checkReload`)
- [ ] Invalidate AO bake + BVH on reload
- [ ] HUD shows `webgpu` backend + reload status
- [ ] **Gate:** edit TOML, save, GPU scene updates live; no crash on bad TOML

### Phase 10 — Default to GPU (when ready)

- [ ] Flip default `-renderer` to `webgpu` on supported platforms
- [ ] Keep CPU as `-renderer cpu` fallback
- [ ] Document in README

## Parity harness (build this in Phase 2, use forever)

```
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

1. `go get github.com/rajveermalviya/go-webgpu/wgpu@latest`
2. Create `internal/webgpu/device.go` — init instance/adapter/device
3. Create `internal/webgpu/shaders/gradient.wgsl` — sanity compute shader
4. Wire `-renderer webgpu` in `main.go` (no-op fallback if device fails)
5. Confirm gradient in window; `go test ./...` still green

Do not port intersections until sky parity passes.
