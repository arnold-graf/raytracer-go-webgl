# Soft Shadow Experiments

**Status:** reverted (2026-09-02). Not in the renderer.  
**Audience:** anyone revisiting area-light shadows without repeating dead ends.

---

## Goal

Add **contact hardening** and **soft penumbra** for spherical emitters (street lamps, pendant orbs) using the existing megakernel and hard BVH shadow rays — “Tier A” from the id Tech / RTX discussion: low sample count, no path tracing, no separate shadow map pass.

Visual target: art-deco street lamp with `[[light]].radius = 0.5` — soft falloff at the base of the post, harder contact near the occluder.

---

## Approach tried

### Scene / CPU

- Reuse `[[light]].radius` as **emitter radius** (world units).
- Pack into `GPULight.Falloff.z` for point lights (spots keep `falloff.zw` for cone yaw/pitch).
- `SceneSoftShadows()` auto-enabled soft shadows when any point light had `radius > 0.005` and scene shadows were on.
- `params.soft_shadows` uploaded at byte offset 344 in the render-params buffer.

### Shader (`shade.wesl`)

On **primary hits only** (`depth == 0`), for each point light with `emitter_r >= SOFT_SHADOW_MIN_RADIUS`:

1. Build a tangent disk on the light sphere (4 directions on the emitter disk).
2. Cast one hard `shadowed()` ray per sample toward a point on the disk.
3. **Visibility** = unblocked fraction → scales direct diffuse + specular for that light.

Reflection / glass bounce paths kept **one hard shadow ray** per light (same as before).

Constants used in the final iteration:

| Constant | Value | Role |
|---|---|---|
| `SOFT_SHADOW_MIN_RADIUS` | 0.005 | Ignore tiny decorative radii |
| `SOFT_SHADOW_SAMPLES` | 4 (later adaptive) | Rays per soft light |
| `SOFT_SHADOW_MIN_SUBTENSE` | 0.035 | `emitter_r / distance`; below → hard shadow |

### Perf mitigations (second pass)

Still too slow for walking in the office / server room:

1. **Angular cutoff** — skip multi-sample when `emitter_r / distance < 0.035` (distant lamps → hard shadow).
2. **Adaptive samples** — 4 samples when ≤4 soft lights in scene, 2 samples when more.

These helped but **frame drops while moving** remained noticeable.

---

## Profile data (`gpuprof`, 512×320, bounce 4, adaptive AA on)

### `scenes/office-sunset/server-room-1.toml`

| Config | GPU time | Notes |
|---|---|---|
| All on | ~9.7 ms | ~134k shadow rays/frame |
| Shadows off | ~9.4 ms | Shadows alone ~0.3 ms here |
| Mirror off | ~2.6 ms | **Glass bounces dominate** (~319k segments) |

Soft shadows were not the only cost in this view, but they add work on top of an already heavy glass/mirror path tree.

### `scenes/office-sunset/index.toml` (full office)

| Config | GPU time | Shadow rays |
|---|---|---|
| All on | ~11–12 ms | ~393k |
| Shadows off | ~8.1 ms | **~3 ms shadow tax** |
| Mirror off | ~5.1 ms | |

Walking through the combined scene hits many lights per pixel; each soft light multiplied shadow BVH traversals by 2–4× on primary surfaces.

### Light mix in server room (why it hurt)

- **Ceiling grid** (`office-light-grid.toml`): many point lights, **no** `radius` → hard shadows only (cheap per light, but high count).
- **Hero lamps** (otto-wagner sphere, art-deco ring, uplights): `[[light]].radius = 0.5` → **4× shadow rays** each on every primary pixel in range.
- Workstation monitor `[[light]]` had **no** radius (hard); decorative sphere `radius = 0.1` is geometry only.

Global enable (“any radius > 0 turns soft shadows on for the whole scene”) meant there was no way to soft-shadow only the street lamp while keeping desk/orb lights hard.

---

## What looked good

- Street lamp preview (`scenes/preview/street-light.toml`): convincing penumbra and contact hardening at modest sample count.
- Specular highlights (separate feature, **kept**) still work; soft visibility scaled the full direct term.

---

## Why reverted

1. **Cost scales with lights × samples × pixels** — office interiors have many emissive props with `radius = 0.5` on `[[light]]`, not just one hero lamp.
2. **Angular cutoff + 2 samples** reduced cost but not enough for smooth locomotion.
3. **Megakernel** already spends heavily on glass/mirror bounces in the server room; soft shadows added a large primary-path multiplier on top.
4. No per-light opt-in meant authors could not mark “this lamp gets soft shadows” without affecting global behavior.

---

## Files touched during the experiment (all reverted)

| Area | Changes |
|---|---|
| `internal/webgpu/shaders/modules/shade.wesl` | `shadow_visibility_area`, `shadow_disk_basis`, `depth` on `shade_diffuse` |
| `internal/webgpu/shaders/modules/trace.wesl` | pass `depth` into shading |
| `internal/webgpu/shaders/modules/types.wesl` | `params.soft_shadows`, sample count, constants |
| `internal/webgpu/scene.go` | pack `radius` into `Falloff.z` |
| `internal/webgpu/device.go` | upload soft-shadow params |
| `internal/webgpu/soft_shadows.go` | scene detection + adaptive samples |
| `internal/webgpu/soft_shadow_test.go` | GPU / pack tests (removed) |
| `schemas/scene.schema.json` | `radius` description (back to informational) |

`scenes/preview/street-light.toml` kept as a prop preview scene (comment updated).

---

## Options if we try again

Ordered by likely payoff vs. implementation cost:

### 1. Per-light opt-in (recommended)

```toml
[[light]]
pos = [0, 3, 0]
radius = 0.5
soft = true   # explicit; default false
```

Only hero lamps pay the multi-sample cost. Decorative `radius` on geometry stays unrelated.

### 2. Hard budget per pixel

- Soft-shadow **at most one** light per shade call (brightest `att × N·L × color`).
- All other lights: hard shadow ray.

### 3. CPU light budget

- Sort lights by `radius × brightness`; only top **N** (e.g. 4) keep `Falloff.z` for soft tests; zero for the rest at pack time.

### 4. Lower sample count everywhere

- 2 fixed samples + interleaved noise across frames (TAA-style accumulation) if the player stands still; 1 sample while moving.

### 5. Separate techniques (higher cost)

- **Ray-traced contact shadows** only (short AO-style rays at contact).
- **Shadow maps** or **RT** for sun + analytic for points.
- **Wavefront** scheduling (see [reflection-optimization.md](reflection-optimization.md)) before adding more per-light work to the megakernel.

### 6. Authoring

- Remove `radius` from `[[light]]` on office props where penumbra is invisible (monitors, dim orbs).
- Keep `radius` only on street / exterior hero lights.

---

## Quick re-profile checklist

```bash
go run ./cmd/gpuprof -scene scenes/office-sunset/index.toml -profile
go run ./cmd/gpuprof -scene scenes/office-sunset/server-room-1.toml -profile
go run ./cmd/preview -scene scenes/preview/street-light.toml -view front -views 1 -o tmp/street
```

Watch **shadow rays**, **glass bounces**, and **mirror off** ablation — in the server room, mirror/glass off is often a bigger lever than shadow tuning alone.

---

## Related docs

- [reflection-optimization.md](reflection-optimization.md) — throughput-based shadow skip (B1, shipped), bounce-path costs
- [ray-tracing.md](ray-tracing.md) — bounce limits, AO bake
