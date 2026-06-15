# Plan: GPU Glass / Reflection Cost Optimization

## Status: PROPOSED

## Goal

Cut the GPU shading cost of glass-heavy views **without changing the look**. The
`manhattan_city_block` scene is dominated by glass: at the spawn camera, turning
reflections off roughly halves GPU shade time, so the two-lobe Fresnel glass +
mirror path is the single biggest lever left after the scene-buffer cache.

Same fidelity rule as the WebGPU port: **≤ ~1 LSB mean error vs the CPU oracle**
on the existing parity views. No new BRDF, no temporal tricks.

## Evidence (cmd/gpuprof, 400×250, this machine)

Manhattan @ scene camera, **after** the scene-buffer cache landed:

| config       | gpu shade | total | fps |
| ------------ | --------- | ----- | --- |
| all on       | ~5.9 ms   | 6.0   | 168 |
| **mirror off** | **~3.1 ms** | 3.2 | 312 |
| shadow off   | ~5.8 ms   | 5.9   | 171 |
| AO off       | ~6.0 ms   | 6.0   | 166 |

→ Reflection/refraction is ~2.8 ms (~half) of the frame. Shadow and AO are
noise here. Indoor-outdoor (little glass in view) is already ~3.9 ms and barely
moves with mirror off, confirming glass is the Manhattan-specific cost.

Re-measure any change with:

```
go run ./cmd/gpuprof -scene scenes/manhattan_city_block.toml
```

## Where the cost is

`internal/webgpu/shaders/trace.wgsl`, `ray_color` work-stack (around the
`MAT_GLASS` branch). Each glass hit can push **two** child rays (refracted +
reflected); with two glass-eligible depths (0 and 1) a single primary ray can
fan out to a 7-segment tree, and every segment re-runs full `nearest_hit` BVH
traversal. Three glass-clad towers means many primary rays start that fan-out.

Contributing factors:

- **Two lobes per glass hit**, each a full scene traversal.
- **Overlapping panes**: a ray through a window often hits the inner pane, then
  another pane behind it — glass behind glass multiplies the tree.
- **Roughness jitter** (`jitter_dir`) adds divergence between neighboring lanes,
  hurting GPU occupancy even though sample count is 1.

## Options (cheapest / safest first)

### 1. Weight-based early-out for the reflected lobe — LOW risk

The transmitted lobe already skips when `w_refl >= 0.98`. The reflected lobe
only skips below `refl_min` (0.02 at depth 0). Raise the deep-bounce floor and
drop lobes whose **accumulated throughput** `tw * weight` is below a small
epsilon (e.g. 1/256, an LSB). A child that can't change the pixel by an LSB is
invisible — so this is fidelity-preserving by construction. Expected: trims the
faint tail of the tree. Gate with the glass parity test.

### 2. Skip the reflected lobe at the depth cap — LOW risk

Confirm depth-2 glass already terminates as diffuse (it should: `reflective =
depth < 2`). If any reflected lobe is still pushed at the last eligible depth
with near-zero contribution, cut it. Mostly a verification + tightening pass.

### 3. Throughput-ordered / luminance cutoff on the stack — MEDIUM risk

Track a running max-remaining-throughput; once `accum` is effectively final and
all remaining segments are below an LSB, break early. Keeps the brightest paths,
drops the rest. Needs care so it stays deterministic vs the CPU.

### 4. "Glass behind glass" cap — MEDIUM risk, MEDIUM payoff

Add a small per-ray glass-bounce counter; after N glass interfaces, shade the
next glass hit as a cheap tint instead of spawning lobes. N tuned so the parity
delta stays ≤ 1 LSB on the Manhattan interior/exterior views. Directly attacks
the overlapping-pane multiplication.

### 5. Reduce roughness jitter divergence — MEDIUM risk

For near-smooth glass (`rough` below a threshold) skip `jitter_dir` entirely
(it's already a tiny perturbation). Improves lane coherence; verify it doesn't
shift the rough-glass look.

## Explicitly out of scope (v1)

- Wavefront / multi-pass path tracer (violates "one dispatch" rule).
- Screen-space reflection or any temporal reuse (changes the look).
- Importance sampling / new BRDF.
- Separate "glass pre-pass" buffers.

## Verification gates

For every change:

1. `go test ./internal/webgpu/` — especially `TestGlassParityMatchesCPU`,
   `TestReflectionParityMatchesCPU`, `TestSkyVariantsMatchCPU`.
2. `cmd/gpuprof` before/after on `manhattan_city_block.toml` (glass-heavy) and
   `indoor-outdoor.toml` (regression check — must not get slower or diverge).
3. Eyeball the Manhattan interior + exterior and a window seen edge-on.

## Suggested order

1 → 2 (quick, safe, measure) → reassess. If glass is still the bottleneck,
do 4 (biggest structural win), then 5. Keep 3 only if 1/4 are insufficient.

## Notes

- The scene-buffer cache already removed ~5.4 ms of per-frame CPU pack/upload;
  pack/upload now read ~0 ms in `gpuprof`, so all remaining wins are in-shader.
- These are all single-shader, single-dispatch tweaks — consistent with the
  WebGPU port's "simplest GPU code" non-negotiable.
