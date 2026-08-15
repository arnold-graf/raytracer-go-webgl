# Scene-Specialized Shaders (and what the glass skyway actually costs)

**Status:** **Shipped** 2026-08-15 — `internal/webgpu/shaders/specialize.go`. Skyway views ~12% faster, frames bit-identical.
**Audience:** anyone profiling the WebGPU renderer, or deciding what to optimize next
**Constraint:** strict byte-parity. Every change here is verified frame-for-frame against the unspecialized shader; anything that moved a pixel was reverted.

---

## Read this first: the benchmark was measuring the wrong thing

Until 2026-08-15, `cmd/gpuprof` built its `render.View` **without `MaxBounceDepth`**, so it fell through to the shader default of **2**. The app ships **4** (`internal/app/app.go`). It also rendered 400×250 with adaptive AA off; the app is 512×320 with AA on (`main.go`).

Same scene, same camera, before and after fixing the defaults:

| | old gpuprof defaults | app-matching defaults |
|---|---|---|
| office-sunset, inside skyway | 7.8 ms (~128 fps) | **29.3 ms (~34 fps)** |

Bounce depth drives the ray tree exponentially, so depth 2 hid roughly 4× of the cost the tool exists to find. `gpuprof` now defaults to the app's configuration, takes `-depth`, and prints whether a run matches the app:

```
GPU profile: scenes/office-sunset/index.toml  (512x320)  yaw=90°
  bounce depth 4, adaptive AA true  (matches the app)
```

> **Any GPU number in this repo recorded before that date was taken at depth 2 / 400×250 / AA off** and is not comparable to current output. That includes the tables in [reflection-optimization.md](reflection-optimization.md). Re-measure before trusting an old figure.

---

## Executive summary

Two findings, one shipped optimization.

**1. The skyway is expensive because of the ray tree, and the ray tree is the look.** With reflections off the scene renders in 3.8 ms instead of 29.2 ms — 87% of GPU time is reflection/refraction. Shadows and AO together are under 2%. Cost is close to linear in path segments, and the skyway needs **11.9 segments per pixel** versus 2.2 in the server room, because two *thick* panes × two faces each × Fresnel branching consumes the entire depth-4 budget. Holding the authored look fixed pins the segment count, which pins most of the cost.

**2. Per-segment cost is a property of the megakernel, not the scene.** Segment throughput measures **33–67M/s across every scene tested**, from 36-prim `default.toml` to 786-prim office-sunset, regardless of BVH depth or primitive count. That is what makes shader specialization pay: register allocation is global to the megakernel, so code a scene never executes still costs occupancy on every ray.

---

## What shipped

`FEAT_*` constants declared in `types.wesl`, all `true` in the checked-in shader, guarding optional paths. `shaders.Specialize` rewrites them to `false` per scene before `CreateShaderModule`; naga const-folds the guard and drops the code.

| Flag | Cleared when | Guards |
|---|---|---|
| `FEAT_TERRAIN` | no terrains packed | `hit_terrain`, `terrain_normal`, `terrain_shadow_skip`, `terrain_albedo` |
| `FEAT_WATER` | no waters packed | `hit_water`, `water_normal` |
| `FEAT_CAMPFIRE` | no campfires packed | the campfire sub-light loop in `shade_diffuse` |
| `FEAT_PRIM_*` (×8) | that primitive kind absent from prims **and** blockers | the matching arm in `hit_prim` and `normal_at` |

`Renderer.syncPipelineFeatures` rebuilds the module and both pipelines when a scene's feature set changes — once per load in practice, confirmed by instrumenting the rebuild count.

### Why `const` and not `override`

WGSL pipeline-overridable constants are the natural mechanism, but **go-webgpu v0.17.1 declares `ConstantEntry` and leaves the `Constants` field commented out** on all three pipeline descriptors, so they cannot be plumbed through without forking the binding. A source rewrite before module creation needs no binding support and gets the same dead-code elimination.

### Measured (office-sunset, 512×320, depth 4, AA on)

| camera | unspecialized | specialized |
|---|---|---|
| inside skyway, looking out | 29.3 ms (34 fps) | **25.7 ms (39 fps)** |
| inside skyway, along it | 25.2 ms (40 fps) | **22.4 ms (45 fps)** |
| front office, facing skyway | 14.2 ms | 13.8 ms |
| server room | 9.1 ms | 8.9 ms |

Terrain/water/campfire account for ~9%; primitive kinds add ~3% on top.

Verified bit-identical on `default`, `island`, `outdoors-night-villa`, `manhattan_city_block`, `indoor-outdoor`, `silicon_dreams`, `textured`, `npc-spider-test`, `outdoors-night-storm`, `office-sunset`.

### Verifying and disabling

```bash
go run ./cmd/gpuprof -scene S -dump a.rgba
RAYTRACER_NO_SHADER_SPECIALIZE=1 go run ./cmd/gpuprof -scene S -dump b.rgba
# then byte-compare a.rgba and b.rgba
```

`RAYTRACER_NO_SHADER_SPECIALIZE=1` forces the full shader. It is the first thing to try if a specialized build is ever suspect.

`TestSpecializeCoversEveryShaderFlag` fails if a `FEAT_*` exists in the WGSL with no entry in `Features` (it would silently stay enabled forever) or vice versa.

---

## The rule that matters: gate the call site, never the callee

Primitive-kind stripping was **implemented, measured as a large regression, removed, and then made to work** by changing only where the guard sits. The two shapes:

```wgsl
// SLOW — 26.5 ms -> 48.2 ms. Guard inside the callee.
fn hit_box(...) -> f32 { if (!FEAT_PRIM_BOX) { return T_MISS; } ... }

// FAST — 26.2 ms -> 25.4 ms. Guard at the dispatch site.
if (FEAT_PRIM_BOX && kind == PRIM_BOX) { return hit_box(p, lro, lrd); }
```

Both produce identical frames. The first leaves every call edge intact and merely makes the callees trivial, which frees inliner budget; the likely consequence is `hit_box` — which carries two dynamically indexed 8-element arrays for hole CSG — getting inlined into `hit_prim`, a function called from every BVH leaf loop in five traversal routines. The second leaves the kept arms byte-identical and simply orphans the stripped functions.

**The gap between the two shapes (23 ms) is far larger than the optimization itself (0.8 ms).** If a kind or feature ever needs gating somewhere new, gate where it is called.

---

## Deliberately not stripped: volumetric flame

`accumulate_flame` and `heat_shimmer_dir` both loop over `campfire_count` and return neutral values at zero, so stripping them is as unreachable on paper as anything above. In practice it perturbed **8 pixels of `scenes/default.toml`** — five by 1 level, one by 9, one by 18 (mean absolute delta 0.00018/255).

No logic reaches that code with no campfires, so the cause is the compiler reallocating registers and flipping a hit decision at a silhouette. It was worth 0.4 ms of 26.5. The bit-identical guarantee is worth more.

This is the honest caveat on the whole technique: stripping unreachable code is semantically free but **not guaranteed to be numerically free**, because it perturbs codegen everywhere. Hence the cross-scene byte check on every flag.

---

## Tried and rejected

All at app settings, camera inside the skyway looking out, baseline 29.2 ms.

| Change | Speed | Image |
|---|---|---|
| **terrain/water/campfire strip** | **−9%** | **bit-identical — shipped** |
| **prim kinds, call-site gated** | **−3%** | **bit-identical — shipped** |
| prim kinds, guarded in callee | **+84% (48.2 ms)** | bit-identical |
| glass-interior seeded traversal | 0% | bit-identical |
| BVH traversal stack 32 → 18 | 0% | identical |
| accumulation-relative cutoff | 0% | — |
| balanced (median-split) BVH | **+23% slower** | 2,369 px differ |
| flame strip | −1.5% | 8 px differ |
| procedural textures off | −3% | differs |
| adaptive AA off | −8% | differs |
| `SEG_MIN_CONTRIB` 1/1024 → 1/64 | −11% | 20% of px, Δ10 |
| `max_bounce_depth` 4 → 3 | −36% (18.6 ms) | 81% of px, Δ105 |

Notes on the interesting failures:

- **Glass-interior seeded traversal.** Seeding `nearest_hit` with the pane's own exit hit, so a ray inside 0.1 units of glass starts with a tight `best_t`. Exact by construction and it works — steps/ray fell 17.5 → 16.6 — but the clock did not move. Traversal is not the bottleneck.
- **Balanced BVH.** Forcing median splits gave avg depth 9.9 / max 10 versus SAH's 12.5 / max 17, and ran **23% slower** (36.8 steps/ray vs 17.5). SAH is doing its job; tree balance is the wrong metric.
- **Accumulation-relative cutoff.** Dropping child lobes faint relative to light already gathered. Inert: the walk is depth-first, so glass children spawn *before* any radiance accumulates and `accum` is still ~0 at the decision point.

---

## Where the remaining headroom is

Not in the BVH. Not in shading. Segment throughput is scene-independent at 33–67M/s, which on a 30-core M2 Max is roughly an order of magnitude below what the hardware should do — the cost is the megakernel itself, and specialization only trims its edges.

Two directions, in order of cost:

1. **Measure occupancy directly.** Capture a frame in Xcode's Metal debugger and read register count and occupancy for this pipeline. This is a measurement, not a rewrite, and it would confirm or kill the register-pressure theory before anyone commits to (2). It is the one diagnostic unavailable through wgpu, and the reason the work above had to infer register pressure indirectly.
2. **Split the megakernel** — wavefront/streaming, as laid out in [reflection-optimization.md](reflection-optimization.md) Option A. Image-identical in principle, weeks of work, and it costs the browser and non-Apple targets if done in Metal rather than WGSL.

Also worth knowing: swapping the BVH build changed 2,369 pixels (max Δ90). Some surfaces in office-sunset are coincident enough that traversal order decides the winner — pre-existing z-fighting, and a hazard for anyone touching the accelerator.

---

## Adding a new flag

1. Declare `const FEAT_X: bool = true;` in `types.wesl` — **true**, so the checked-in linked WGSL stays the full build (`TestAllFeaturesMatchesCheckedInShader` enforces this).
2. Guard **call sites**, not function bodies. See the rule above.
3. Add the field to `shaders.Features`, the entry to `Specialize`'s map, and the condition to `featuresFor` in `device.go`. Clear the flag only when the feature is genuinely absent, so the guarded code was already unreachable.
4. Remember blockers: a primitive kind or feature used only for shadow casting still needs its arm.
5. Byte-compare against `RAYTRACER_NO_SHADER_SPECIALIZE=1` across `scenes/`. If any pixel moves, it does not ship.
