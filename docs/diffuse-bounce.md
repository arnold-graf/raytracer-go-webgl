# One traced bounce of indirect light (key 8)

An experiment, off by default. When on, every **primary** diffuse hit casts
cosine-weighted rays into the hemisphere, shades whatever they land on with the
same direct-lighting loop the primary hit used, and adds that back as indirect
light. Path depth 2 and no further: the second hit does not bounce again.

    key 8                       toggle (HUD shows `bounce[8]:off` / `bounce[8]:2r`)
    RAYTRACER_BOUNCE_RAYS=N     rays per hit, 1..16, default 2
    RAYTRACER_BOUNCE_AMBIENT=f  share of the ambient constant kept, 0..1, default 0

    go run ./cmd/gpuprof -scene scenes/office-sunset/index.toml \
      -cam-x 44.9 -cam-y 201.3 -cam-z 33.1 -yaw-deg 270 -pitch-deg -5 \
      -w 1024 -h 640 -bounce 8 -dump tmp/on.rgba

Key 8 used to toggle the thin-glass ghost. The ghost is now always on: it is a
look, and this is the question of whether the renderer can afford GI at all.

## Why this is the one thing the megakernel did not already do

Every other recursion in `ray_color` is deterministic. A mirror has exactly one
outgoing direction, glass has two, and the ray *tree* on the segment stack
resolves them exactly. A diffuse surface reflects into the whole hemisphere and
no single direction stands for it, so the integral has to be **sampled**. That
is the line between this renderer and `cmd/pathtrace` — not "bounces versus no
bounces", but "an integral versus a few discrete directions".

## What a ray is worth

The diffuse term of the rendering equation is

    ∫ (albedo/π) · L_in(ω) · cos θ dω

and a cosine-weighted sample has pdf `cos θ/π`. The estimator is `f·L·cos/pdf`,
so the cosine and both π cancel and **the entire weight a bounce ray carries is
the albedo**. There is no gain constant here and nothing to calibrate: if the
bounce reads too bright or too dim against `cmd/pathtrace`, the bug is
elsewhere. This is the same cancellation `pt_linked.wgsl` relies on when it
writes `beta = beta * s.alb` beside `rd = pt_cosine_dir(s.n)`.

Contrast `internal/vpl`, which needs a measured `Gain` precisely because it
converts emitted flux into point lights under a falloff curve that is not
inverse-square.

## Ambient is taken back out

`scene_ambient_at` is the constant that has been standing in for indirect light
all along. Keeping it *and* the traced bounce counts the same quantity twice,
and the toggle then reads as "GI made the scene brighter" rather than "GI moved
the light around". So the primary hit's ambient contribution — recorded after AO
in `sh_ambient` — is subtracted before the bounce is added.
`RAYTRACER_BOUNCE_AMBIENT` puts a share of it back for scenes tuned hard enough
against the constant that removing it leaves them black.

On the atrium view the swap is still net brighter: mean display level 160.6 →
171.5. The constant was under-representing the real bounce there.

## Measured cost (M2 Max, office-sunset atrium, 1024×640, WebGPU)

Interleaved, 3 rounds, 8 frames each:

    bounce off    56.1 ms
    1 ray         92.2 ms   +64%
    2 rays       107.8 ms   +92%
    4 rays       133.3 ms  +138%

**The first ray costs 2.3× what the second does** — 36 ms for ray one, 16 ms for
ray two, ~6 ms each after that. The fixed part is divergence and cold caches at
the first hemisphere ray; once paid, more samples are comparatively cheap. So if
the bounce is on at all, 1 ray is the worst value on this curve.

Each ray is a BVH traversal **plus a full direct-lighting evaluation** — every
light, every shadow ray — at whatever it lands on. `PROF_BOUNCE_RAYS` and the
shadow-ray counter move together for that reason. What makes it affordable at
all is `SHADOW_SKIP_LEVELS`: each ray is handed `tw · albedo / rays` as its
throughput, so the display-space gate can tell how little a bounce-lit shadow is
worth and skip the ray.

### Cost with the bounce off

2.5% (52.95 → 54.25 ms, interleaved, 4 rounds). Occupancy is unchanged —
`main_` stays at 384 threads/TG on both (`tools/occupancy`) — so this is
instruction-cache and branch cost, not a register cliff. The frame is
bit-identical to the pre-change renderer when off.

## Noise, and why it is the point

One sample per pixel is an unbiased estimate with enormous variance, and this
path has **no denoiser** — the temporal + à-trous reconstruction in
`pathtrace.go` serves the offline tracer only. RMS deviation from a 16-ray
reference, in linear radiance:

    1 ray   0.228      2 rays  0.157      4 rays  0.115      8 rays  0.085

That is ×0.73 per doubling, against the theoretical 1/√2 = 0.707. Textbook Monte
Carlo, which is exactly why a real-time path tracer is a denoiser with a tracer
attached rather than the other way round.

The sampler is seeded from the **quantised hit point**, not the pixel. The grain
then sticks to the surface and stays put as the camera moves instead of boiling,
and the adaptive-AA taps — which re-enter `ray_color` at sub-pixel offsets —
draw different directions from the centre ray instead of averaging to nothing.
The cost is that the grain has a world size (`BOUNCE_SEED_SCALE`, 1024/m): back
away and it drops below a pixel, walk up to a wall and it grows into visible
blotches.

## Limits

- **Primary hits only.** A bounce at every depth would multiply with the
  reflection tree, and `MAX_SEGS` is measured rather than spare. The visible
  consequence: anything seen *through* glass is at depth ≥ 1 and gets no bounce,
  which is most of the atrium view's background.
- **One bounce.** The second hit gathers direct light and stops, so a white room
  reads darker than the `1/(1-albedo)` series it should sum to.
- **Diffuse and checker materials only.**
- No importance sampling toward bright regions, no MIS, no reuse between pixels.
  Every one of those is what `internal/vpl` buys instead by precomputing the
  same first bounce as a few dozen point lights, at a cost that does not scale
  with pixel count. See `docs/vpl.md`.
