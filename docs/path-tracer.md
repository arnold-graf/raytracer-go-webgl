# An experimental path tracer

**Status:** built and working, as a separate binary. Not wired into the game and
not intended to ship as-is.
**Audience:** anyone asking what a trick-free light transport model costs and
looks like on this hardware, at this resolution, on these scenes.
**Constraint:** no existing file changes. Not the megakernel, not any
`modules/*.wesl`, not a scene. Everything here is new files, and the shipping
renderer's `trace_linked.wgsl` stamp is untouched.

```bash
# still image, with the megakernel's own frame beside it
go run ./cmd/pathtrace -scene scenes/office-sunset/index.toml -yaw-deg 270 \
    -spp 512 -ambient -compare -o tmp/pt/office.png

# one sample per pixel, reconstructed — what the interactive viewer does
go run ./cmd/pathtrace -scene scenes/office-sunset/index.toml -yaw-deg 270 \
    -spp 1 -ambient -denoise -o tmp/pt/1spp.png

# fly around it
go run ./cmd/ptlive -scene scenes/office-sunset/index.toml -yaw-deg 270
```

---

## What it is

A second compute kernel that shares the megakernel's **geometry** and replaces
its **transport**.

Reused unchanged, by import: BVH traversal, analytic primitive intersection,
CSG box holes, instancing, the terrain marcher, water, procedural textures, the
sky, the ACES tonemap, and the light cluster grid. `pt.wesl` imports those out
of `../modules/` and the linker tree-shakes the rest, so the two renderers
provably disagree about light and about nothing else.

Replaced: everything downstream of a hit.

| trace.wesl | pt.wesl |
|---|---|
| hemispheric ambient constant | paths that actually reach a light |
| baked AO volume | occluded bounce rays |
| screen-space penumbra filter (61 taps × 4 passes) | lights sampled as spheres |
| adaptive AA (classify + compact + resolve) | camera ray jittered inside the pixel |
| `array<RaySeg, 6>` fork stack | one lobe per vertex, chosen by roulette |
| `SHADOW_SKIP_LEVELS`, `GLOSSY_MIN_CONTRIB`, `SEG_MIN_CONTRIB` | Russian roulette |
| emissive = add albedo, stop | emissive geometry is a real light source |

Next-event estimation samples the scene's `lights[]` and campfire sub-lights.
Emissive geometry and the sky are found by BSDF sampling only. The two
populations are disjoint, so no MIS is needed and nothing double counts.

## Measured cost

512×320, path depth 8, Russian roulette from depth 3, one NEE sample per
vertex. Megakernel column is best-of-5 at the same camera, for scale.

| scene / view | path tracer | megakernel | ratio |
|---|---|---|---|
| office-sunset, yaw 0 | **17.5 ms/spp** | 11.1 ms/frame | 1.6× |
| office-sunset, yaw 270 | **18.2 ms/spp** | 15.0 ms/frame | 1.2× |
| outdoors-night-villa | **40.1 ms/spp** | 16.2 ms/frame | 2.5× |

So one sample per pixel of honest path tracing costs roughly what one
megakernel frame costs, and the ray budget was never the problem. The estimate
in the feasibility discussion that produced this experiment was 15 ms/spp for an
interior and 40–60 ms/spp for the villa; both hold.

**The villa is 2.3× the office per sample, and it is the terrain.** Today's
primary and shadow rays hit the heightfield along a few coherent directions; a
cosine-hemisphere bounce grazes it, and [terrain-cost.md](terrain-cost.md)
already recorded that grazing rays multiply march steps (5.7M → 10.1M when the
terrain was flattened). Analytic primitives are cheap for incoherent rays. A
raymarched heightfield is not. That is the first thing to fix if this is ever
more than an experiment.

## What it confirms about the trade

Rendered side by side (`-compare` writes `<out>-sbs.png`), against the same
camera, with `-ambient` so both are at the brightness the scene was authored
for:

- **Real indirect light.** `scene_ambient` is two authored constants blended by
  `n.y`; office-sunset declares `ambient_sky = [0.20, 0.12, 0.10]` and every
  up-facing surface in the building gets exactly that. Replacing it with actual
  incident radiance is the largest visible change, and it is what makes the
  villa's grass pick up the campfire.
- **Contact darkening at scales the AO grid cannot hold**, and applied
  correctly. The baked volume multiplies *direct* light by an occlusion factor
  ([shade.wesl:329](../internal/webgpu/shaders/modules/shade.wesl#L329)), which
  darkens a surface in full light merely for being near a wall.
- **Emissive surfaces light the room.** 268 of them in office-sunset, all of
  which illuminate nothing today.
- **Noise**, concentrated exactly where the tricks used to be free: indirect-only
  regions, glossy floors, and anything lit by one of those 268 small emitters,
  which only BSDF sampling ever finds. 512 spp is not clean on the office
  marble.

## Three bugs worth recording

**Uniform light selection is catastrophic here, and it looks like a black
image.** office-sunset packs **266 lights**, most with a cull radius of one
world unit. Drawing one uniformly finds a light that reaches the shading point
about 1% of the time. The estimator stays unbiased — multiply by 266 and the
mean is right — so the frame comes back black with occasional white sparks, and
nothing about it says "sampling distribution". Restricting the draw to the
scene's existing cluster grid (`wide_span` + `light_span`, the same
`idx_tables` the megakernel walks) is the difference between converging and not.
This is the many-lights problem in its most literal form.

**A silent light population.** The first villa renders were missing the warm
light entirely, and the cause was that campfires are a *second* light system —
their own buffer, their own animated sub-lights — that a kernel importing only
`lights[]` never sees. Volumetric flames are a third. Anything reimplementing
transport has to enumerate all three; `params.light_count` is not the light
count.

**A long dispatch comes back as a torn frame, not as an error.** Batching 32
samples of the villa put one dispatch over 5 seconds, which trips the OS GPU
watchdog; the result is a half-rendered image with a ragged horizontal edge and
no diagnostic anywhere. `cmd/pathtrace` now sizes its batch to a wall-time
target (`-dispatch-ms`, default 250) instead of a fixed sample count, because
the per-sample cost varies 2.3× across scenes and no fixed batch straddles that.

A fourth, smaller: glass apportions energy as
`reflectance = fresnel + (1 - fresnel) × (1 - transmit)` — whatever is not
transmitted is *reflected*, not scattered diffusely. Getting that wrong turned
the villa's pond from a mirror into wet paint, and defaulting `transmit` to 1.0
instead of trace.wesl's 0.9 sent every ray down through the water into the
terrain, which is what made the dispatch long enough to trip the watchdog above.

## Reconstruction: why converging is the wrong goal

Raw, this tracer needs 100-300 spp — several seconds — for any view to look
good. That is normal and it is not what real-time path tracers do. They trace
one or two samples and *reconstruct*, and the measurements below say the same
thing here.

Three things had to be measured before building anything, because two obvious
levers turn out not to be levers at all. Both were tested at equal wall time
against a 2048 spp reference:

| change | RMSE at 64 spp | cost | verdict |
|---|---|---|---|
| baseline (1 NEE sample, RR from depth 3) | 5.98 | 18.1 ms/spp | — |
| 8 NEE samples per vertex | 3.87 | 48.8 ms/spp | **loses**: 172 raw spp in the same time reaches 3.65 |
| Russian roulette from depth 6 | 5.53 | +33% | **loses**: more samples is worth more |

So **paying extra rays for less variance does not pay here** — which is an
argument *for* ReSTIR rather than against better light sampling, since ReSTIR
buys selection quality through resampling instead of through more shadow rays.
It also says the noise is broadly distributed rather than concentrated in one
term: direct lighting alone (depth 1) already carries 3.72 of the 5.98 levels.

Broadly distributed noise is exactly the case reconstruction handles, so that
is what got built:

1. **A G-buffer** for the primary hit — normal, view depth, albedo, and a flag
   for whether the hit was diffuse.
2. **Albedo demodulation.** Illumination is filtered with the albedo divided
   out and multiplied back at resolve, so the filter smooths *light* and never
   texture. This is the single most important part; without it a wide filter
   turns marble into porridge.
3. **Temporal reprojection.** Each pixel's world position is projected with the
   previous frame's basis (three dot products — fwd/right/up are orthonormal,
   so this inverts `pixel_ray_dir` exactly) and the history is accepted only if
   depth agrees within 5% and normals within ~25°. Rejecting properly is the
   whole value of the pass; a stale sample accepted across a disocclusion
   smears far more visibly than the noise it removed.
4. **Edge-avoiding à-trous** (Dammertz et al.) with SVGF's variance-driven
   luminance edge-stop, five passes at doubling hole size — the same
   wide-reach-for-fixed-taps argument [soft-shadows.md](soft-shadows.md) makes
   for the separable penumbra filter.

### What it buys

512×320, office-sunset yaw 270, RMSE in 8-bit levels against 2048 spp:

| spp | raw | reconstructed |
|---|---|---|
| 1 | 20.66 | **9.04** |
| 4 | 16.25 | **7.18** |
| 16 | 10.49 | **6.22** |
| 64 | **5.98** | 6.17 |

**One reconstructed sample is worth about twenty raw ones**, and it costs
1.3 ms — 19.2 ms/spp becomes 20.5 ms/spp with temporal reprojection and five
à-trous passes. That is 49 fps for the office and 24 fps for the villa, at a
quality that would otherwise need roughly 400 ms of tracing.

The last row is the important caveat: **the filter has a bias floor of about 6
levels and stops helping past ~64 spp**. Above that it is strictly worse than
not filtering, so `cmd/pathtrace` leaves it off by default and the reference
images in this document are raw.

### Two defaults that made it look wrong, and why

Both showed up immediately in the interactive viewer and neither is a bug in
the usual sense — they were reasonable-looking constants that are wrong for
this use.

**Jumping black edges along silhouettes.** The G-buffer was taken from sample
zero's *jittered* camera ray. At a silhouette pixel the jitter lands on the
foreground one frame and the background the next, so the recorded normal and
depth flip, temporal reprojection alternately accepts and rejects that pixel's
history, and the edge flickers. The G-buffer is now traced from the pixel
centre on its own dedicated ray. That costs one extra primary intersection per
pixel and it is the difference between reprojection working and reprojection
strobing — the validation test is asking "is this the same surface as last
frame", and it cannot be answered with a quantity that is itself random.

**A washed-out image that never sharpened.** `max_history` was 32 and the
blend weight had a floor of 0.08, so the running mean could not exceed about
twelve effective frames no matter how long the camera sat still. Variance
therefore stayed at a twelve-frame level, the variance-driven edge-stop stayed
wide, and the à-trous kept filtering at full strength forever. The image was
permanently a blurred version of a noisy estimate rather than converging to a
sharp one.

Two changes fix it, and together they are the design worth keeping:

- The history cap is 512 and the alpha floor is zero, so a still camera is a
  true running mean.
- **Spatial filtering fades out as history builds** (`-atrous-fade`, default
  24 frames). Spatial filtering is a stand-in for samples you do not have yet;
  once you have them it should get out of the way.

The result is that a moving camera gets a heavily filtered image and a settled
one converges to the raw estimator's own grain, sharp, with no blur at all.

| frames @ 1 spp | raw | temporal only | temporal + fading à-trous |
|---|---|---|---|
| 4 | 16.25 | 16.28 | **6.04** |
| 16 | 10.49 | 10.49 | 6.08 |
| 64 | 5.98 | 5.95 | 5.95 |
| 256 | — | 3.15 | 3.15 |

Temporal-only tracks raw accumulation exactly, which is the point: with a
static camera reprojection is the identity and the pass is a pure accumulator.
It never blurs anything. That is the mode to use if you like the grain — it
just gets there sooner and survives camera motion.

`ptlive`'s `3` cycles off → temporal → temporal + spatial.

### The flag bug, and why it invalidated four measurements

Worth recording on its own, because it is the most expensive kind of mistake
this project can make: one that produces plausible images and wrong numbers.

The Go-side `ptFlag*` constants were `iota`-derived while the shader's
`PT_FLAG_*` values were written as literals. Inserting one new flag in the
middle of the Go list shifted every later bit by one, and four flags silently
swapped meanings: `-no-emitter-nee` set `WHITE_NOISE`, `-restir` set
`NO_EMITTER_NEE`, `-debug indirect` set `RESTIR_TEMPORAL`. Nothing errored.
Every render still looked like a plausible render of the scene. The emitter-NEE
ablation, the ReSTIR temporal result and the indirect-only view were all
measuring a different feature than their flag names claimed.

What surfaced it was an A/B that came back **byte-identical** when it could not
possibly have been — the repeated advice in these documents to verify that a
variant actually changed the image, arriving as a rescue rather than a
precaution.

The constants are now written out explicitly and `TestPTFlagsMatchShader`
parses `pt.wesl` and pins every one of them.

### Three more bugs, all silent

**A filter with no variance estimate does nothing, and looks like it ran.** The
luminance edge-stop divides by the standard deviation, so with no temporal
history the variance reads zero, every neighbour looks like an edge, every
weight collapses, and the noisy input passes straight through. Nothing errors.
The fix is SVGF's: a short history falls back to a spatial variance estimate
over the same window.

**Accumulating a mean where a sum was expected.** `pt_accum` holds a running
sum that resolve divides by the total sample count. Adding each dispatch's
*mean* instead made the image darker by exactly the number of dispatches —
which, with an auto-sized batch, is a different factor on every run and on
every scene. It read as "the ambient term broke".

**Demodulation must not touch the reference path.** `g_albedo` comes from one
jittered sample's position, so on a textured surface it differs from dispatch
to dispatch; dividing by one value and multiplying back by another is a
systematic whole-image error, not a rounding one. Measured at 8.6 levels RMSE.
The progressive accumulator now keeps raw radiance and only the reconstruction
chain sees demodulated illumination.

### 512x320 is the worst resolution to judge this at

Comparing against browser path tracers like
[THREE.js-PathTracing-Renderer](https://github.com/erichlof/THREE.js-PathTracing-Renderer)
raises a fair question: why do they look converged so much sooner? Most of the
answer is workload, not algorithm — their demo scenes are a handful of
analytic shapes lit by one or two large area lights, where NEE has almost no
selection variance and every sample is cheap. office-sunset is 906 primitives,
**266 lights** and 268 small emissives, which is close to the worst case for
both. That renderer's own README puts convergence at "around 500-3,000
samples", which is the same ballpark this one reaches; its samples just arrive
far faster. It also ships a denoiser.

But part of the answer is real and measurable here, and it is about the
resolution this renderer was tuned at:

| resolution | ms/spp | ms per megapixel |
|---|---|---|
| 512x320 | 33.0 | **201** |
| 1024x640 | 89.0 | 136 |
| 1536x960 | 182.0 | **123** |

**A pixel at 1536x960 costs 39% less than a pixel at 512x320.** 164k pixels
does not fill an M2 Max; the device is idle waiting on memory that a larger
dispatch would hide. This is the condition
[bounce-kernel.md](bounce-kernel.md) predicted would change its conclusions,
arriving from the other direction.

The consequence is that supersampling beats more samples per pixel:

| render | RMSE vs 1024 spp | GPU time |
|---|---|---|
| 512x320, 16 spp | 14.01 | 0.50 s |
| 1024x640, 16 spp, box-downsampled to 512x320 | **7.88** | **1.42 s** |
| 512x320, 64 spp | 6.53 | 2.05 s |

Four times the samples per output pixel, arriving as resolution rather than as
spp, and about 12% cheaper than buying the same quality with spp. On top of
that the noise sits at a higher spatial frequency, so at a fixed display size
it reads as fine grain rather than blotches — the same effect as the earlier
observation that denoising gets *harder* at low resolution, seen from the
other side.

### Emitter NEE and ReSTIR: what paid and what did not

Four things were built here. Two are shipped on, one is shipped off, and the
order they were attempted in is the lesson.

**1. Emitter next-event estimation. Built, correct, worth nothing on its own.**

Emissive geometry was previously reachable only by BSDF sampling, which is the
noisiest way to light a room with 268 small emitters. Sampling them directly
needed area sampling for the two kinds that occur (244 spheres, 24 thin box
panels), a local-to-world transform for all 268 of them, and a rule for the
strategies not to double count.

It is unbiased: two converged estimators — BSDF-only and NEE — agree to 1.95
RMSE levels at 4096 spp, which is their own residual noise. And it is worth
about 3%:

| 64 spp | RMSE |
|---|---|
| BSDF sampling only | 5.13 |
| + emitter NEE + RIS | 4.99 |

Small, because uniform selection over 268 emitters finds a relevant one about
as rarely as uniform selection over 266 lights did — the same failure, one
population over — and because emitter light is not where this scene's
variance lives.

**2. RIS on the emitters. Also worth nothing — which was the useful result.**

Resampled importance sampling with 8 and 32 candidates measured 6.87 and 6.80
against 6.73 for uniform: flat. That is not a failure of RIS, it is evidence
that *emitter light is not where this scene's variance lives*, and the earlier
ablation table in this document had already said so — direct lighting from the
**point** population carried 3.72 of 5.98 levels.

**3. RIS on the point lights. This is the one that pays.**

Same machinery, right population. `m` candidates are ranked by an unshadowed
estimate that traces nothing, and the single shadow ray goes to the survivor.
At `m = 1` it reduces exactly to the uniform estimator, so the candidate count
is a clean ablation axis.

| office-sunset, 64 spp | ms/spp | RMSE |
|---|---|---|
| ris = 1 (uniform) | 45.0 | 6.83 |
| ris = 8 | 58.0 | 5.13 |
| **ris = 16** | **55.0** | **4.99** |
| ris = 32 | 60.0 | 4.90 |

| outdoors-night-villa, 64 spp | ms/spp | RMSE |
|---|---|---|
| ris = 1 | 69.0 | 11.46 |
| **ris = 16** | **94.0** | **5.10** |

On equal time that is **19% lower RMSE on the office and 48% on the villa** —
roughly 1.4x and 3.7x the effective sample rate. Unbiased: 1.79 levels against
the 4096 spp ground truth, indistinguishable from BSDF-only's own 1.78.

This is exactly the trade the equal-time table earlier in this document
predicted. Eight shadow rays per vertex took RMSE from 5.98 to 3.87 and lost
anyway, because it cost 2.7x. RIS buys most of that selection quality for 1.2x,
because ranking a candidate is arithmetic and only the winner gets a ray.
**The rays were never the thing to spend more of; the choice of which one to
trace was.**

**4. Temporal reservoir reuse. Built, measured, off by default.**

The other half of ReSTIR: carry the reservoir into the next frame so a handful
of candidates behaves like hundreds. It is implemented, reprojects and
validates against the previous G-buffer exactly as the colour history does,
and it gets steadily *worse* as frames accumulate:

| frames @ 1 spp | RIS only | + reservoir reuse |
|---|---|---|
| 2 | 15.41 | 15.41 |
| 8 | 10.50 | 10.54 |
| 32 | **6.59** | **8.90** |

Through the reconstruction path, where a single frame has to stand on its own,
the same: 4.85 becomes 6.59 at eight frames.

The mechanism is that reuse deliberately **correlates successive frames**, and
everything downstream here depends on frames being independent. A reservoir
carrying `M` up to twenty times the per-frame candidate budget barely moves:
the new candidates get about 5% of the weight, the pixel keeps the same light
essentially forever, and its estimate stops varying frame to frame. Progressive
accumulation and the temporal colour filter both average across frames, and
there is nothing left for them to average away.

Lowering the cap confirms the mechanism and does not rescue it — at four times
the budget instead of twenty, 32 frames go from 8.90 to 8.27, still well
behind 6.59 for no reuse.

So reuse earns its keep when the per-frame budget is one or two candidates and
nothing downstream averages across frames. This pipeline is the opposite on
both counts.

`-restir` keeps it available for anyone who wants to retry it against a much
larger light set or a much smaller candidate budget.

### What would come next

NVIDIA's [RTXPT](https://github.com/NVIDIA-RTX/RTXPT) is the useful reference
for what a production real-time path tracer actually spends its effort on, and
its feature list splits cleanly into what ports here and what does not.

**Portable, in the order the measurements above support:**

1. **Path-space layer decomposition.** RTXPT denoises "up to 3-layer path
   space decomposition" — diffuse, specular and transmission filtered
   separately. This kernel currently has a crude binary version of that: a
   specular flag in the G-buffer that skips the wide passes and blocks
   gathering across the boundary. Separating the layers properly is what
   removes the seam that guard leaves behind.
2. **A real firefly filter** rather than a fixed clamp. An outlier that
   survives into a filtered frame becomes a blob the size of the filter
   radius, so this matters more with reconstruction than without.
3. **Ray cones for texture footprint.** RTXPT uses "RayCones for texture MIP
   selection". There are no mips here, but procedural textures alias on bounce
   rays for the same reason, and a cone width is what a procedural evaluation
   would need to filter itself.

**Not portable, and worth knowing why:** Shader Execution Reordering, Opacity
Micromaps, DLSS-RR and the OptiX denoiser are all NVIDIA hardware or CUDA.
SER is the interesting one — it is a hardware answer to exactly the divergence
problem [bounce-kernel.md](bounce-kernel.md) tried to solve in software with
queues and could not make pay. There is no WebGPU equivalent and Apple GPUs do
not expose one, so that avenue stays closed here for reasons that have nothing
to do with the algorithm.

The general lesson from their feature list is the one this document keeps
arriving at independently: **a production real-time path tracer spends its
effort on importance sampling, resampling and reconstruction, and almost none
on tracing more rays.**

## Deliberately not done

- **No MIS.** Light sampling and BSDF sampling cover disjoint populations here,
  so there is nothing to combine — but that is also why emissive geometry is
  noisy. Emitter-list NEE plus MIS is the single biggest quality win available.
- **No caustics.** A unidirectional path tracer with NEE cannot find
  specular-diffuse-specular paths. Neither can trace.wesl, so this is not a
  regression, but "path tracer" does not buy them.
- **Shadow rays still use the blocker BVH**, which excludes glass and emissive
  geometry ([instance.go:194](../internal/scene/instance.go#L194)). That is a
  trick, and it is kept on purpose: a strict path tracer treats a window as an
  occluder for NEE and lights an interior only through BSDF paths that happen to
  hit the sun, which essentially never happens. Removing it makes
  office-sunset dark and noisy. It is the one place where being fully honest
  makes the picture worse.
- **The scenes are unchanged**, so every light intensity is still tuned against
  trace.wesl's non-physical `1/(0.5 + 0.08d²)` falloff. `-physical` switches to
  inverse-square and makes every scene wrong at once; relighting the library is
  the real migration cost, larger than this kernel was.

## Flags worth knowing

| flag | what it answers |
|---|---|
| `-ambient` | what was the ambient constant actually doing? |
| `-compare` | side-by-side against the megakernel, plus its frame time |
| `-checkpoints 1,8,64` | how many samples until this is clean? |
| `-debug albedo\|normal\|direct` | is it the transport, or the geometry? |
| `-no-shadow`, `-all-lights` | ablations, in gpuprof's style |
| `-light-samples N` | rays traded against light-selection noise |
| `-physical` | inverse-square falloff, i.e. how wrong are the scenes? |
| `-ris N` | light resampling candidates per vertex; 1 is uniform selection |
| `-restir` | temporal reservoir reuse (measured as a small regression) |
| `-no-emitter-nee` | emissive geometry by BSDF sampling only |
| `-denoise`, `-temporal` | reconstruction; `-atrous-passes`, `-phi-lum` tune it |
| `-drift N` | move the camera N units per frame, to exercise reprojection |

`-drift` deserves a note of its own: a still camera never exercises temporal
reprojection, because every pixel maps to itself and a completely broken
projection looks perfect. Drifting sideways a hundredth of a unit per frame is
the cheapest way to make the pass actually do its job before trusting it.

`-debug direct` is the one that found two of the three transport bugs above. A first-hit
view separates "the transport is wrong" from "the geometry or the material
lookup is wrong", and guessing between those two costs hours.

## Where the code is

| file | what |
|---|---|
| `internal/webgpu/shaders/pt/pt.wesl` | the kernel |
| `internal/webgpu/shaders/pt/link.sh` | links it against `../modules/` in a scratch dir |
| `internal/webgpu/shaders/ptsource.go` | embed + stale-relink, mirroring `resolve.go` |
| `internal/webgpu/pathtrace.go` | pipelines, bind group, accumulate/resolve |
| `cmd/pathtrace` | still renders, benchmarks, comparisons |
| `cmd/ptlive` | free-fly interactive viewer |

The link script copies `modules/*.wesl` into a temp directory rather than
dropping `pt.wesl` in beside them, because `shaders.ModulesSHA256` hashes
everything in that directory — adding a file there would invalidate the
megakernel's linked-shader stamp and force it to relink. The bind group is built
by reading the binding numbers back out of the linked WGSL, so tree-shaking a
dependency away cannot leave a hand-maintained layout list quietly wrong.

## Related

- [bounce-kernel.md](bounce-kernel.md) — the conditions under which the
  megakernel-versus-wavefront verdict flips. A path tracer satisfies #2 (more
  rays per pixel) and #3 (heavier per-hit shading) directly, so none of that
  document's conclusions should be carried across without re-deriving them.
- [terrain-cost.md](terrain-cost.md) — why the villa is the expensive one.
- [soft-shadows.md](soft-shadows.md) — the filter this replaces with sampling,
  and the 3.5× measured cost of a jittered shadow ray that makes the trade
  concrete.
- [megakernel-optimization.md](megakernel-optimization.md) — the occupancy
  lessons the kernel is written against (no dynamically indexed local arrays).

---

## Borrowing the grain back: key 5, "grain"

The path tracer's noise is the one thing about it that is cheap to keep. Key 5's
colour-mode cycle gained a fourth entry — `8bit -> 15bit -> crush -> grain` —
that swaps the ordered dither for noise shaped like this renderer's own, in
`pt_grain` in `modules/trace.wesl`.

It is matched to measurement, not to taste. Differencing a **2spp** render
against a **512spp** reference of the same frame, in linear radiance:

| property | measured | what it means |
|---|---|---|
| `sigma` vs brightness | `sigma ~ L^0.79` | absolute grain *grows* with brightness, but `sigma/L` is **2.5 in shadow against 1.3 in light** — the reason path-traced shadows read as grainier |
| channel correlation | R-G 0.94, G-B 0.83, R-B 0.66 | mostly common-mode, with a real chromatic component; blue least tied |
| autocorrelation | 0.002 at lag 1, nothing above 0.003 out to lag 16 | the grain is **white** |
| tails | p50 0.42σ, p90 1.19, p99 **4.26**, p99.9 **7.48** | against a gaussian's 0.67 / 1.64 / 2.58 / 3.29 |

Three of those drove the implementation:

- **Amplitude follows `L^0.79`**, applied to *linear* radiance before the
  tonemap. That is where Monte Carlo noise lives, and it is why a firefly here
  gets compressed by the highlight rolloff exactly as one from the path tracer
  would. Flat-amplitude noise misses the shadow behaviour entirely.
- **The tails come from a two-component scale mixture** — 10% of pixels get 5x
  the width — which fits all four percentiles at once (predicted 0.42 / 1.14 /
  4.46 / 6.98). A plain gaussian at the same variance looks like video noise;
  what reads as "path traced" is most pixels nearly clean with a few far out.
- **It is one band, not several.** The request was for a multi-band filter, and
  the autocorrelation says the path tracer has no structure at any scale beyond a
  pixel. What varies across *bands* is the amplitude against brightness, not the
  spatial frequency. Spatially banded noise would look less like the source, not
  more.

### Verification

Re-measuring our own grain against an undithered render of the same frame, with
the same tool:

| | path tracer | this filter |
|---|---|---|
| autocorrelation, lag 1-16 | ~0.002 | ~0.002 |
| channel corr R-G / G-B / R-B | 0.94 / 0.83 / 0.66 | 0.91 / 0.76 / 0.76 |
| `sigma/L`, dark to light | 2.5 -> 1.3 | 0.84 -> 0.56 (same 1.5-2x fall) |
| p90 / p99 / p99.9 in sigma | 1.19 / 4.26 / 7.48 | 1.15 / 4.43 / 9.37 |

The `p50` sits lower than the source (0.25 against 0.42) and `sigma/L` falls a
little less steeply. Both are partly real and partly an artifact of the
comparison: percentiles are normalised by a *global* sigma while the noise is
heteroscedastic by construction, so the figure depends on each image's own
brightness histogram, and the two frames do not share one. The properties that
decide the look — white spectrum, mostly-common-mode colour, amplitude rising
with brightness, heavy sparse tails — all carry across.

### Cost and blast radius

**+2.6% on the frame, and only when selected.** Modes 0-3 render
**bit-identical** to before the change, verified per mode. The chroma terms use
triangular noise rather than a second Box-Muller, which costs a log, a sqrt and a
sincos per pixel and measured statistically indistinguishable.

`GRAIN_STRENGTH` is the knob, linear, and doubles as an equivalent sample count:
the measured 2spp frame sits near 1.08, and halving the strength is worth 4x the
samples. It ships at **0.13**, roughly a 128spp look.

### It is a dither, so it does not animate

The path tracer resamples every frame and its grain crawls. Copying that was
wrong: on a still frame a crawling pattern reads as a broken screen rather than
as grain. `GRAIN_ANIMATE` ships **false**, so the pattern is a function of pixel
position alone — a dither, belonging to the grid the way the bayer pattern it
replaces does. The same frame rendered twice is byte-identical.

It still responds to the scene: amplitude follows each pixel's brightness, so a
flickering light makes the grain over it breathe. That is the `L^0.79` law doing
its job, not the pattern moving.

### Tried and rejected: blue noise

Freezing the pattern makes its spatial spectrum matter, which it does not while
animating — the eye integrates across frames. Frozen white noise keeps its
low-frequency energy, which can show as blotching, and shaped blue noise is
exactly what dither masks use to avoid that. So it was worth trying.

A blue-noise *texture* would have cost a binding, and 31 of 32 are in use, so the
generator had to be analytic: Jimenez's interleaved gradient noise. **An analytic
function is periodic, which is the one thing this filter must not be.** The
measurement caught it before the eye did — autocorrelation along a scanline went
**-0.655 at lag 1**, correctly negative for blue noise, and then *oscillated*:
+0.577, -0.356, -0.395, +0.630 at lags 2, 3, 6, 16. That is a repeating pattern,
not noise with a shaped spectrum. On screen it is a fine diagonal crosshatch —
the ordered-dither look this mode exists to replace.

The heavy-tail selector makes it worse than it would otherwise be: thresholding a
smooth deterministic function picks a *periodic subset* of pixels, so the sparse
speckle carrying the whole character lands on a lattice.

White noise it is, and the blotching worry did not materialise — at this
amplitude the speckle is sparse enough that clumping never becomes visible.
