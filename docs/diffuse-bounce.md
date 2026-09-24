# One traced bounce of indirect light (key 8)

An experiment, off by default. When on, every **primary** diffuse hit casts
cosine-weighted rays into the hemisphere, shades whatever they land on with the
same direct-lighting loop the primary hit used, and adds that back as indirect
light. Path depth 2 and no further: the second hit does not bounce again.

    key 8                       toggle (HUD shows `bounce[8]:off` / `bounce[8]:2r+dn`)
    RAYTRACER_BOUNCE_RAYS=N     rays per hit, 1..16, default 2
    RAYTRACER_BOUNCE_AMBIENT=f  share of the ambient constant kept, 0..1, default 0
    RAYTRACER_BOUNCE_DENOISE=0  turn the reconstruction off and see the raw estimate
    RAYTRACER_BOUNCE_ATROUS=N   wavelet passes, 0..4, default 3
    RAYTRACER_BOUNCE_SHADOW_RR=f  bounce shadow-ray roulette, 0 = off, default 24
    RAYTRACER_BOUNCE_HALF=0     gather on every pixel instead of half
    RAYTRACER_BOUNCE_BLUE=0     white-noise sampler instead of low-discrepancy
    RAYTRACER_BOUNCE_TILE=N     coherent sampling tile edge, 1..16; 1 turns it off

    go run ./cmd/gpuprof -scene scenes/office-sunset/index.toml \
      -cam-x 44.9 -cam-y 201.3 -cam-z 33.1 -yaw-deg 270 -pitch-deg -5 \
      -w 1024 -h 640 -bounce 2 -dump tmp/on.rgba
    # ablations: -bounce-temporal=false  -bounce-atrous 0

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

## Measured cost (M2 Max, office-sunset colonnade, 1024×640, WebGPU)

Interleaved, 3 rounds. The reconstruction is on the right-hand column:

| | GPU |
|---|---|
| bounce off | 63.0 ms |
| 2 rays, raw | 109.9 ms |
| 2 rays + temporal | 104.4 ms |
| 2 rays + temporal + 3 à-trous | **99.4 ms** |

**The denoiser makes the frame faster.** Five extra dispatches, 10.5 ms saved.

The reason is adaptive AA. Its shading-edge detector fires on luminance
curvature, and Monte Carlo noise is nothing but luminance curvature, so the raw
bounce flagged **49.2% of pixels** for supersampling — 318,955 of them by the
shading detector — and every flagged pixel re-traces a whole path. Denoised,
that falls to **11.4%**, and path segments drop from 7.9 to 6.2 per pixel. The
filter costs less than the AA work it stops provoking.

This is worth stating plainly because it inverts the usual intuition: on this
renderer a denoiser is not a tax on the indirect term, it is what keeps the
indirect term from taxing everything downstream of it.

With the bounce *off* the frame is unchanged: 63.1 ms against HEAD's 63.6 ms
interleaved, and bit-identical output — the larger `ShadowAux` (192 → 272 bytes,
285 MB at maxDim 1024) costs nothing measurable here.

On the Metal backend the same view is 51.8 ms off and 74.0 ms with the bounce
and the full reconstruction. `internal/metal` keeps its own copy of the dispatch
chain (`kernels.go`), so the six passes and their gating are listed there too;
adding them took the chain past `MAX_KERNELS`, which is now 20. Naga mangles an
entry point whose name ends in a digit, so the chain names the à-trous levels
`bounce_atrous_0_`…`_3_` — the same reason `main` appears there as `main_`.

Earlier ray-count sweep, before the reconstruction existed (bounce off 56.1 ms
in that session's thermal state):

    1 ray         92.2 ms   +64%
    2 rays       107.8 ms   +92%
    4 rays       133.3 ms  +138%

**The first ray costs 2.3× what the second does** — 36 ms for ray one, 16 ms for
ray two, ~6 ms each after that. The fixed part is divergence and cold caches at
the first hemisphere ray; once paid, more samples are comparatively cheap. So if
the bounce is on at all, 1 ray is the worst value on this curve.

Each ray is a BVH traversal **plus a direct-lighting evaluation** at whatever it
lands on. Which of the two dominates is scene-dependent by a factor of six — see
*Shadow rays from bounce hits* below.

## Noise, and the reconstruction that answers it

One sample per pixel is an unbiased estimate with enormous variance. Raw, RMS
deviation from a 16-ray reference falls ×0.73 per doubling of the ray count
(1 ray 0.228, 2 rays 0.157, 4 rays 0.115, 8 rays 0.085) — textbook 1/√N, and
far too slow to buy watchability with rays alone.

So three passes reconstruct it instead, in the same compute pass as `main()`,
between the reflection filter and the penumbra one:

    bounce_temporal     reproject last frame's estimate through the camera's
                        motion and blend into a running mean
    bounce_atrous_0..3  edge-avoiding wavelet passes, each doubling its hole size
    bounce_resolve      add back into hdr_pixels, roll the history forward

RMS against a 16-ray, 120-frame reference at 2 rays/hit, 512×320 (colonnade
view; the scene's own lighting has been retuned since, so re-measure rather than
compare these against older runs):

| | RMS | rel |
|---|---|---|
| raw | 0.155 | 48.8% |
| temporal only | 0.044 | 13.8% |
| à-trous only | 0.034 | 10.6% |
| both | 0.012 | **3.8%** |

**13× better than raw.** The à-trous-only row is also what a *freshly disoccluded*
pixel gets, since it has no history to reproject — which is the whole reason
both stages exist rather than just the cheaper one.

### The sampler and the denoiser want opposite things

The first build of this filter ran in full and changed nothing. Two causes, both
worth knowing:

1. **The sampler was seeded from the hit point alone**, so every frame drew the
   *same* directions. A running mean over identical samples converges to the one
   noisy estimate it started with. Averaging needs independent samples, so the
   frame counter is now mixed into the seed — but only when temporal
   accumulation is on, because without a denoiser that same stability is exactly
   what keeps the grain from boiling. The two halves of the feature want
   opposite things from the sampler.
2. **`main()` writes the whole `ShadowAux` record every frame**, and `tr.sh`
   comes out of `shadow_aux_snapshot` with every bounce slot zeroed — so the
   history was erased at the top of each frame before the temporal pass could
   read it. `main()` now carries the four persistent fields over explicitly.

### Deliberately not SVGF

There are no stored luminance moments. SVGF keeps a running mean and
mean-of-squares per pixel to estimate variance and drive the luminance
edge-stop; that is two more floats in a record with no binding to spare, and the
signal is largely the one the history length already carries — a pixel with four
frames behind it is noisy, a pixel with thirty is not. So the history length
drives the edge-stop directly (`phi_lum = 4/√(len+1)`). The cost is a filter
that cannot tell a genuinely high-variance region from a merely young one.

Reprojection uses a read/write field split rather than frame-parity ping-pong:
`bounce_temporal` reads `bounce_hist`/`bounce_prev_*` at the reprojected pixel
and writes `bounce`/`bounce_hist_w` at its own, so the two sets never alias
inside one dispatch. `bounce_resolve` copies `_w → hist` at end of frame.

History is dropped whenever it cannot be trusted: the first frame, a resize, the
toggle coming back on, or a reprojected pixel whose normal or depth disagrees.
Portal capture renders a different camera into the same buffers and so is
excluded from the filter entirely.

Tuning lives in `render.DefaultBounceFilter` — alpha 0.05, max history 32,
phi_normal 64, phi_depth 0.05 — each with its reasoning at the definition.

## Antialiasing

Adaptive AA re-enters `ray_color` for each flagged pixel, and `bounce_allowed`
is off there: a tap must not gather a hemisphere of its own. That would be a
second full gather on the most expensive pixels in the frame, returning one
unfiltered sample to blend at 30% over a filtered one.

So the tap has to be *shaded* like the centre without *sampling* like it, and
that turned out to be the whole difficulty. Two bugs, each of which drew a
visible bright outline around silhouettes:

1. **The ambient swap rode on the ray count.** Replacing the ambient constant
   with a traced estimate is a shading decision; gathering a hemisphere is a
   sampling one. They were one branch keyed on `bounce_rays_wanted()`, so a tap
   — which returns zero there — skipped both. The tap kept an ambient term the
   centre had removed, was handed the centre's indirect on top, and came out
   brighter by exactly that constant.
2. **Off-surface taps kept the constant.** The first fix let a tap that landed
   on different geometry keep its ambient as a stand-in, on the theory that the
   centre's indirect describes the wrong surface. But every other pixel in the
   frame has had that constant replaced, so such a tap is shaded by different
   rules than everything it is averaged into — and in a dim room the constant is
   the *larger* of the two, so the halo came straight back.

The answer is that an off-surface tap takes the **aimed-at neighbour's**
estimate. `analyze_edge` already picked the tap direction by finding which
neighbour disagrees, so that neighbour is almost certainly looking at the
geometry the tap hit, and its pixel holds a filtered estimate for exactly that
geometry. One storage read.

Measured on the server-room view (`-cam-x 13.1 -cam-y 201.5 -cam-z 14.8
-yaw-deg -158.5 -pitch-deg 1.89`), comparing AA on against AA off — for
unbiased antialiasing the brighter/darker split should be even:

| | pixels changed | brighter | mean Δ |
|---|---|---|---|
| bounce off (control) | 1729 | 50.0% | +0.47 |
| bug 1 | 3163 | 93.2% | +5.88 |
| bug 2 | 1078 | 78.7% | +5.11 |
| fixed | 986 | **50.7%** | +1.23 |

The control is the point of that table: AA on a frame *without* the bounce
splits 50/50, so any departure from that is the indirect term being shaded
inconsistently between a pixel and its own tap, not antialiasing doing its job.
`tmp/halo_stat.py` is the measurement.

## Shadow rays from bounce hits

Each ray is a BVH traversal plus a direct-lighting evaluation at whatever it
lands on, and how much of the cost is which depends entirely on the scene:

| bounce rays provoke… | office atrium | villa hearth |
|---|---|---|
| shadow rays per bounce ray | 0.54 | **3.09** |
| bounce rays per pixel | 0.93 | **2.00** |

The villa is the hard case on both counts. Every pixel has a diffuse primary hit
(sealed interior, no glass or sky), and the room is dark enough that
`SHADOW_SKIP_LEVELS`' display-space gate never fires — so every one of the
hearth fire's self-shadowing sub-lights counts as significant and gets a ray.

The obvious saving is to raise that threshold for bounce hits, and it is a trap
the renderer has already sprung once. The note on `SHADOW_SKIP_LEVELS` records
it: a threshold on a smoothly varying quantity does not fire per pixel, it fires
on an iso-surface around each light, *"drawing a hard arc across every wall in
range where shadows abruptly stop being traced"* — reported at the time as round
hard rings of light.

So the decision is **randomised** instead. Each light's shadow ray is kept with
probability `p` proportional to `step_levels`, the display-space measure the
existing gate already computes, and a kept light is scaled by `1/p`:

    E[kept] = p · (total · V / p) + (1 − p) · 0 = total · V

Unbiased by construction, and non-negative — rouletting only the shadow
*deficit* would have lower variance but goes negative on a fully blocked light,
and clamping that to zero puts the bias straight back.
`BOUNCE_SHADOW_RR_MIN_P` (0.25) floors `p`, bounding amplification at 4×.

The iso-surface becomes white noise, which is what the à-trous pass is there to
remove. Measured with `tmp/band_stat.py`, which asks how often a changed pixel
has a changed neighbour against what independent scatter predicts at the same
density:

| | changed | clustering ratio |
|---|---|---|
| roulette (rr 0 → 24) | 1.46% | **1.22** |
| AA on vs off (a genuinely edge-structured change) | 4.54% | 4.87 |

1.22 is scatter. The AA row is the calibration: the metric detects structure
when it is there.

It is also free in quality terms. Against a 16-ray reference **with no temporal
history** — the honest case, since a settled camera hides this entirely — RMS
runs 0.00894 / 0.00890 / 0.00892 / 0.00913 at rr 0 / 12 / 24 / 48. And per
pixel the deviation splits 49.3% brighter / 50.6% darker, mean −0.04 levels,
extremes ±11: no bias, no fireflies.

Cost, interleaved, at 2 rays/hit, 1024×640:

| | villa hearth | office atrium |
|---|---|---|
| rr 0 (trace every ray) | 233.0 ms | 86.7 ms |
| rr 12 | 207.7 ms (−10.8%) | 84.6 ms (−2.5%) |
| **rr 24 (default)** | **197.9 ms (−15.1%)** | 84.3 ms (−2.8%) |
| rr 48 | 193.2 ms (−17.1%) | 83.4 ms (−3.9%) |

The win is specific to dim interiors with many small lights — which is exactly
where the bounce was most expensive. `RAYTRACER_BOUNCE_SHADOW_RR=0` turns it
off; `-bounce-shadow-rr` does the same in gpuprof.

## Half rate, and where the saving actually was

The hemisphere is gathered on half the pixels each frame, in a checkerboard that
inverts every frame. A skipped pixel keeps its reprojected history — it loses a
frame of latency, not an estimate — and a skipped pixel that has *no* history
(freshly disoccluded) is filled from the four orthogonal neighbours that did
gather and sit on the same surface.

The checkerboard inverts rather than staying put because a fixed half would
leave the same pixels permanently dependent on their neighbours, which is a
static spatial bias — the thing most likely to read as a pattern.

**The granularity is what matters, and not for the reason you would guess.**
A per-pixel checkerboard halves the ray count exactly (1,310,720 → 655,360) and
saves **13%**. The same checkerboard decided per 8×8 workgroup saves **37%**,
with the identical ray count. The difference is SIMD divergence: traced and
skipped pixels share a 32-lane group, so the group still walks the gather and
the skipped lanes are merely masked off. Only when a whole group skips does the
work actually disappear.

| villa hearth, 2 rays, 1024×640 | GPU | RMS (settled) |
|---|---|---|
| full rate | 202.8 ms | 0.00862 |
| half, per pixel | 176.6 ms (−12.9%) | 0.00865 |
| **half, per 8×8 workgroup** | **128.8 ms (−36.5%)** | 0.00874 |

The coarser pattern does not show. `tmp/tile_stat.py` averages the difference
over each 8×8 tile and projects it onto the tile checkerboard — a visible tile
artifact means updated tiles sit systematically above or below skipped ones:

| | tile-structured share | checkerboard alignment |
|---|---|---|
| half per workgroup, settled | 0.8% | +0.0009 levels |
| half per workgroup, 4 frames | 2.8% | −0.0095 levels |
| half per pixel (control, no tiles) | 1.2% | +0.0012 levels |

Hundredths of a display level, and *less* structured than the per-pixel control.
The temporal filter carries a skipped tile forward, so there is nothing
systematic for the pattern to lock onto.

## Blue noise

The direction sampler is a low-discrepancy sequence rather than a hash per
sample: R2 (Roberts' generalised golden ratio) over (frame, sample), Cranley-
Patterson rotated by a per-pixel interleaved-gradient offset. No texture, which
matters because a blue-noise mask would need a buffer binding and there is none
spare.

It is worth a few percent, and only where samples are scarce — which is the
case that matters, since a settled camera converges regardless:

| villa hearth, 2 rays | white | blue |
|---|---|---|
| no temporal history | 0.00873 | **0.00836** (−4.2%) |
| 4 frames of history | 0.00852 | 0.00850 |
| 4 frames, half rate | 0.00868 | **0.00808** (−6.9%) |
| settled (40 frames) | 0.00865 | 0.00871 |

It costs about 1.5% of frame time. Notably `blue + half rate` (0.00808) beats
`white + full rate` (0.00852) while tracing half the rays.

**The first version made things worse**, and the reason is worth recording. The
two per-pixel offsets were `ign(p)` and `ign(p + d)`. IGN is
`fract(c · fract(dot(p, k)))`, so translating `p` only shifts the inner dot
product by a constant: the two are correlated at +0.29 and their joint
distribution covers **12.5% of the unit square**, collapsed onto diagonals. A
nominally 2D rotation was effectively 1D, and the sampler was worse than the
white noise it replaced. Swapping the pixel's components instead —
`ign(p.yx + d)` — is a genuinely different linear form: correlation +0.0001,
full occupancy. Cheap to check, and there is a script for it in the history of
this file.

## Ray coherence: the largest single win, and it is not a wavefront

Incoherence was the biggest inefficiency in the renderer, and two probes sized
it before anything was built. Villa hearth, 2 rays, bounce cost isolated
against `-bounce 0`:

**How much is per-invocation overhead?** Same rays, half the invocations
(2 rays at half rate against 1 ray at full rate): 93.0 ms vs 84.1 ms. About
14 ns per invocation, 10-15% of the bounce. That is all a wavefront recovers by
batching alone.

**How much is incoherence?** Stripping the per-pixel rotation from the sampler
so neighbouring pixels trace near-parallel rays -- wrong for the image, but it
measures the ceiling: **157.2 ms against 69.2 ms. 2.27x.** 88 ms of a 201 ms
frame was threads in a SIMD group walking different parts of the tree and
touching different geometry at the far end.

The fix turned out to be two lines rather than a wavefront. Rotate the
low-discrepancy sampler **per tile** instead of per pixel
(`BOUNCE_FLAG_COHERENT`): rays run near-parallel inside a tile, where the
a-trous is about to average over them anyway, while the frame counter keeps
successive frames independent so the temporal mean still converges. Spatially
correlated, temporally decorrelated -- the opposite of the usual arrangement.

At an 8-pixel tile it lands on **68.8 ms against the 69.2 ms ceiling**: the
whole of the coherence loss, recovered. 4 is the shipping default, paired with
`BOUNCE_SPREAD` below -- on its own the cloudiness is
easy to see in motion -- the sweep below was taken at 4 frames of history, which
understates how often tiles are fresh when the camera is actually moving. It is
`RAYTRACER_BOUNCE_TILE` (or `-bounce-tile`) and it is a runtime uniform rather
than a shader constant precisely so it can be swept without regenerating the
Metal library; 1 turns it off.

Villa hearth, one binary, whole frame:

| tile | WebGPU | Metal |
|---|---|---|
| 1 (off) | 199.7 ms | 171.0 ms |
| 2 | 179.3 ms | 150.9 ms |
| 4 | 142.7 ms | 120.1 ms |
| 8 | 113.2 ms | 97.0 ms |

### The cloud is a sample-count problem, and `BOUNCE_SPREAD` is the fix

With one rotation per tile, all sixteen pixels of a 4x4 draw the same direction
and return the same estimate. The a-trous averaging over that tile therefore has
sixteen copies of one sample rather than sixteen samples: the tile's effective
sample count is **one**. That is the cloud, and it is a count problem rather
than a sampling one.

`RAYTRACER_BOUNCE_SPREAD` (0..1) keeps a fraction of each pixel's own rotation
on top of the tile's, so the tile's rays span a small cone instead of a single
direction -- near enough to walk the same part of the tree, far enough apart
that averaging them means something. 0 is the old behaviour; 1 is the per-pixel
sampler with no coherence at all.

At **tile 4, spread 0.15** the cloud is gone: visually indistinguishable from
per-pixel on a dark wall at four frames of history, where spread 0 shows obvious
blotches. Tile-mean spread falls 0.857 -> 0.377.

**Unverified:** every timing for this section was taken on a laptop that was low
on battery, and the cross-view run disagreed with the earlier single-view one by
about a factor of two on the same configuration. Re-measure on mains power
before trusting any of it. What is *not* in doubt is the shape: the win and the
cloud are the same phenomenon, and buying the cloud back costs most of the win.

### The trade is one-dimensional, and three ways of dodging it fail

Worth recording because each looked like it should escape the trade:

- **Spreading only one sample dimension.** Azimuth (u2) carries nearly all of
  both the decorrelation and the cost; elevation (u1) is cheaper and decorrelates
  proportionally less. Marginally more efficient, same curve.
- **A smaller tile instead of spread.** Tile 2 at spread 0 lands within noise of
  tile 4 at spread 0.15 on both cost and tile-mean spread. The two knobs move
  along one curve.
- **Jittering the tile grid per frame**, so a pixel shares with a different set
  of neighbours each frame. Worse on *both* axes: the win needs the tile aligned
  to the dispatch, so an offset grid makes a workgroup span several tiles and
  brings the divergence back, while neighbouring pixels still share most of
  their tiles across a 32-frame window and their histories stay just as
  correlated.

The conclusion the three share: rays cannot be coherent enough to share a
traversal *and* decorrelated enough for their estimates to be independent. Every
parameterisation lands on one curve, and the only real escape is more samples.

### Editing a shader does not rebuild the Metal library

Worth knowing before tuning anything here. The WebGPU path relinks itself --
`shaders/resolve.go` reruns `link.sh` whenever a module is newer than
`trace_linked.wgsl` -- so a `.wesl` edit is live on the next `go run .`. The
Metal library is `//go:embed`ed, so the same edit does **nothing** until
`sh internal/metal/gen.sh` has run *and* the binary is rebuilt. `-backend auto`
prefers Metal, so the default experience of editing a shader is that it silently
has no effect.

`gen.sh` now stamps the module digest into `trace.sha256` and `metal.New`
compares it against the live sources, so that mismatch is a startup warning
instead of an afternoon.

Measured across every view, 2 rays, 1024x640:

| | per-pixel | per-tile (4) | |
|---|---|---|---|
| villa hearth | 125.3 ms | 97.2 ms | **-22.4%** |
| server room | 72.8 ms | 57.5 ms | **-21.1%** |
| front office | 89.2 ms | 75.2 ms | -15.7% |
| skyway | 53.9 ms | 51.5 ms | -4.3% |

The skyway gains least for the same reason it always does: only 0.53 bounce
rays per pixel survive there, so there is little incoherent work to make
coherent.

**This settles the wavefront question.** A wavefront exists to buy two things:
amortised per-invocation cost, bounded here at 10-15%, and ray coherence, which
this captures in full for two lines and no new buffers. Sorting rays would be
attacking a problem that is already solved.

## Motion: disocclusion, and what does not fix it

A fixed camera hides every temporal artifact, so `cmd/gpuprof` can walk one:
`-cam-vel x,y,z` and `-cam-yaw-vel deg`, with the authored `-cam-*` pose as the
**end** of the path so a still render at the same flags is directly comparable.

Measure the difference in **linear radiance**, averaged into 16x16 blocks first
(`tmp/ghost_stat.py`). Both halves of that matter. Display space lies, because
ACES is nonlinear and a noisier frame reads brighter at the same radiance --
that alone made an 8-ray render look biased against a 2-ray reference. And
per-pixel RMS drowns the artifact: what shows up is a broad, smooth bias across
a large surface at well under a level per pixel, which no threshold-and-cluster
test detects.

### The asymmetry

| front office, low-frequency error | backward | forward |
|---|---|---|
| nearest history fetch | 1.009 | 0.578 |
| **bilinear** | **0.894** | **0.407** |
| + velocity-shortened history | 0.890 | **0.365** |
| + adaptive sampling | **0.789** | 0.360 |

Backing away is more than twice as bad as walking forwards, and the counters say
why: it disoccludes **12.1% of pixels per frame** against **0.2%**. Pulling back
widens the view, so a border band around the whole frame was outside the
previous frustum. Those pixels have no history to average, the a-trous filters
hardest exactly where history is shortest (`phi_lum = 4/sqrt(len+1)`), and the
result is the coarse streaking on the floor -- the largest surface, seen at a
grazing angle, with the most newly revealed area.

### Adaptive sampling (on)

`BOUNCE_FLAG_ADAPT` spends 4x the rays on a point that was outside the previous
frustum. The test is arithmetic only -- reproject and check the bounds -- because
a dependent storage read in the megakernel's hot path would cost more than the
rays it saves. Disocclusion *behind* a silhouette is missed; the border band,
which is the whole of the asymmetry, is caught exactly.

It is a straight quality/cost dial, not a free win: **-11% error for +19% frame
time** while moving (124.2 -> 147.3 ms, bounce rays 997k -> 1.48M), and nothing
at all when the camera is still. `RAYTRACER_BOUNCE_ADAPT=0` turns it off.

### Three things that did not work

Recorded because each looked obviously right beforehand, and the reasons
generalise:

- **Neighbourhood clamping.** The standard anti-ghosting tool assumes the
  current neighbourhood is a better picture of "what is here now" than the
  history. At 2 spp it is not: the bias is ~0.4 levels and the 3x3 it would be
  measured against has a standard deviation of several. Moving error was
  unchanged at every width while the still floor degraded up to 9x. See
  `render.DefaultBounceClamp`.
- **Minification discount.** Backing away magnifies the previous frame, so a 2x2
  fetch undersamples it -- true, and irrelevant. 0.889 without, 0.894 with.
  There is no history in that band to undersample.
- **Spatial reuse on disocclusion.** Seed a disoccluded pixel from a converged
  neighbour on the same surface. It finds one for **0.8%** of the pixels that
  need it backing away (610 of 79,242), because the band is deeper than any sane
  search and every neighbour is in it. Walking forwards it fires on 96% of a
  much smaller set and makes that direction *worse* (0.365 -> 1.698): same
  surface is not the same lighting, and a plane test cannot see the difference
  where the a-trous at least has a luminance edge-stop. See
  `BOUNCE_FLAG_REUSE`.

- **Clamp-to-edge history.** A point that reprojects off-screen takes the
  nearest edge pixel's history instead of being rejected, accepted only where a
  plane test says the surface continues past the frame edge. The floor does
  continue, so the test fires — and the result is monotonically worse the more
  of it you take:

        credited history   backward error
        0 (a no-op)              0.891
        1                        1.071
        3                        1.737
        feature off              0.890

  Removed rather than left switched off: there is no setting where it pays, and
  at the only harmless one it does nothing.

The pattern in all four: they treat the problem as *bad* history. It is
**absent** history.

That is worth stating as a rule, because it cost four attempts to learn. **A
spatially borrowed estimate of indirect light is worse than no estimate**, even
from a neighbour demonstrably on the same surface, even credited with almost no
confidence. Same surface is not the same lighting, the a-trous has a luminance
edge-stop where none of these do, and the error a wrong value plants persists
for as many frames as the history window is long.

Which is the argument for a guard band. Every fix above tries to *invent* a
value for points that were off-screen; the band is the only one that computes
the real one. And it can be very cheap: those pixels are never displayed, so
they need no primary direct lighting (57% of the server-room frame), no
reflection tree (65% of the skyway's), no AA, no a-trous and no resolve -- only
a primary hit, a bounce gather and a temporal blend. It can run at a lower
sample rate, since absent history is the problem and not inaccurate history, and
it can be sized from camera velocity: zero standing still, zero walking
forwards, sized only when backing away. The obstacle is memory rather than time
-- `shadow_aux` is already 285 MB at maxDim 1024 and a 12% margin adds ~71 MB of
which a band pixel needs 48 bytes of the 272.

## Limits

- **The AA taps do not trace their own bounce**; they are handed a neighbouring
  pixel's reconstructed estimate. See *Antialiasing* below for why, and for the
  two ways getting this wrong draws a halo.
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
