# Live indirect light

> **Removed from the code (2026-09-18).** The live probe field, the DDGI
> cascades, the baked volume and their tools (`cmd/probebake`, `cmd/ptlive`,
> `internal/gibake`, `shaders/modules/gi.wesl`) are gone. Nothing here ships any
> more; virtual point lights are the GI that remained — see [vpl.md](vpl.md).
> This file is kept because what it measured is why, and every reason it gives
> still applies to anything that would replace it.

**Status:** off by default. With both flags unset the megakernel renders
byte-identically — verified pixel-exact on the villa, the office, Manhattan and
the mountain view.

| | |
|---|---|
| `RAYTRACER_LIVE_GI=1` | traced probes, two cascades, solved every frame |
| `RAYTRACER_GI_BAKE=<file>` | a volume solved offline by `cmd/probebake` — **the one to use** |

**Two earlier designs are gone**, and this document keeps what they measured
because the reasons are the useful part. Both filled a camera-culled grid by
injecting the radiance of whatever the frame happened to shade, then
propagating it with an all-pairs gather over cells. The gather is O(cells^2),
which capped the grid at 12^3, which is why it had to follow the camera, which
is why it popped and why nothing more than 9 m away ever received indirect
light. One cause, every symptom. Tracing replaced it: a probe casting rays is
O(probes x rays), a budget rather than a quadratic.

### Where the code is

| | |
|---|---|
| `modules/gi.wesl` | the field: octahedral storage, sampling, cascades, the sparse baked volume, probe weighting. Depends only on `types` and `math`. |
| `modules/shade.wesl` | how a probe ray shades what it hits, next to the other light loops |
| `modules/trace.wesl` | how a probe ray traces, and the update entry point |
| `internal/webgpu/gi.go` | grid sizing, the per-frame schedule, the uniform block |
| `internal/gibake` | the on-disk volume format |
| `cmd/probebake` | the baker |

All of it is inert with the flags off, which is the point of the split: nothing
in the hot path has to think about it.
**What it does:** replaces the flat ambient constant with a world grid of
irradiance that the renderer fills in as it draws, using no extra rays.
**What it does not do:** occlusion. Verified below, and it is the whole gap.

```bash
RAYTRACER_LIVE_GI=1 go run . -scene scenes/test/indirect-shadow.toml
```

---

## The idea

While shading a primary hit the megakernel already knows that surface's
outgoing radiance. Writing it into a world grid cell costs one store and no
rays at all — the light transport has already been paid for by the frame it is
drawing. A second pass turns "radiance leaving surfaces in this cell" into
"irradiance arriving at this cell", and `scene_ambient_at` reads it back in
place of the authored constant.

This is a light propagation volume whose injection source is the frame's own
shading rather than a reflective shadow map.

**The grid has no binding of its own.** The megakernel declares 30
storage/uniform buffers and naga spends one more on the runtime-array sizes
table, which is exactly Metal's limit of 31 per stage. Both GI regions live in
the tail of `ao_volume`, which was promoted to `read_write`.

## The gather must average, not sum

The shipped gather summed every occupied cell's contribution and scaled the
result by a constant. That makes the irradiance proportional to **how many
cells hold radiance**, which is a property of the grid rather than of the
scene, and it is the bug that surfaced the moment the grid started following
the camera.

Sized to the level, the grid's handful of huge cells were mostly empty air and
the sum stayed small. Anchored to the viewer, the box is packed with nearby
surfaces, every cell holds something, and the sum runs away until it pins at
`GI_CLAMP` — the frame turns white. It gets worse the further you walk,
because slots that scroll in keep acquiring radiance and nothing takes it back
out.

Dividing by the total weight makes the result the **average incident
radiance**, which is the quantity the ambient term wants anyway. It is bounded
by the brightest cell whatever the cell count, so the feedback loop's gain is
the surface albedo — below one by construction — and `GI_GAIN` became a
brightness knob instead of a stability knob that had to be re-tuned whenever
the grid's occupancy changed.

Verified by soaking: 2000 frames with the camera parked holds a median
luminance of 3.2 with zero saturated pixels and no drift at all.

## Transport: two models, and why the obvious one cannot work

The first version diffused — each cell exchanging with its 26 neighbours,
iterated across frames. **It has no working setting**, and the failure is
structural rather than a tuning miss:

- Normalised by the neighbour count it attenuates roughly 5x per hop. The test
  scene's floor sat at exactly **0.0** after 240 frames.
- Raised until light crosses the room, the same loop gain exceeds one and the
  field saturates: **252.7 of 255** by frame 300.

There is nothing in between, because a diffusion step is the wrong operator. A
wall lights a floor four metres away *in one bounce*, not by sixteen hops each
multiplied by a loss factor — and any factor that survives sixteen hops also
compounds without bound.

The shipped version is an **all-pairs gather**: every cell is a virtual area
light and each receiver integrates the rest with a 1/r² falloff. One bounce,
right geometry, nothing iterated, nothing to destabilise. It is affordable
only because the grid is deliberately tiny — `GIMaxAxis` is 12, so 1728 cells
is about 3M pairs against the 164k pixels the same frame shades.

## Verified on scenes/test/indirect-shadow.toml

That scene's floor receives no direct light at all, so everything on it is
bounce.

| floor | in shadow | lit | ratio |
|---|---|---|---|
| ambient constant | 32.0 | 32.0 | 1.00 |
| **live GI** | 76.8 | **101.7** | **0.76** |
| path tracer | 49.1 | 96.5 | **0.51** |

**Brightness and spatial falloff are right.** The flat 32.0 becomes 101.7
against the reference's 96.5, with a plausible gradient — brighter near the
reflector, dimmer away from it. That is real bounce light, updated every
frame, for no rays.

**The shadow is absent, and worse than absent.** Rendering the same scene with
the pillar deleted isolates what the pillar actually does:

| shadow patch | with pillar | without | pillar's effect |
|---|---|---|---|
| live GI | 96.2 | 84.9 | **+13% brighter** |
| path tracer | 49.1 | 66.1 | **26% darker** |

The pillar *brightens* the region it should darken, because the grid has no
visibility term and the pillar is itself a surface injecting radiance. It adds
a light where there should be an occluder. This is the same inversion the
ambient zones showed, in a more literal form.

## Cost

3.4 ms to 6.1 ms on the test scene. That scene is trivial, so the 2.7 ms is
almost entirely the all-pairs gather, which is independent of scene
complexity: it is 1728² pairs whatever is being rendered. On a real scene it
would be a much smaller fraction, and it is the obvious thing to bound with a
distance cutoff if it ever matters.

## Culling: the grid follows the camera

The grid's fidelity was never the problem — its *reach* was. Sized to the
scene, `GIMaxAxis` cells had to span the whole level:

| scene-sized cell | with tight AO bounds | without |
|---|---|---|
| outdoors-night-villa | 18.07 m | 183.34 m |
| office-sunset | 12.67 m | 36.09 m |

An 18 m cell is larger than the rooms it is meant to light, so the grid was
useful only on scenes about the size of the test room.

The fix is to stop covering the level. The grid is now a fixed box centred on
the camera — `GIMaxAxis` cells of `GICellMeters` — giving **1.5 m cells
everywhere, 8x to 122x finer**, for exactly the same cell count and cost.
Outside the box, shading falls back to the ambient zones and then the authored
constants, as it already did outside the old grid.

**Culling buys resolution, not capacity.** The gather is all-pairs over cells,
O(cells²), so raising `GIMaxAxis` remains the thing that is unaffordable —
1728 cells is about 3M pairs, and doubling the axis is 64x that. Shrinking the
volume each cell covers costs nothing at all.

A second thing falls out: the grid no longer needs a baked AO volume to derive
bounds from, so `RAYTRACER_LIVE_GI` no longer depends on AO being present.

### Toroidal indexing is what makes it movable

A storage slot is keyed on the **absolute world cell**, not on the cell's
position inside the box, and the box origin is snapped to the cell lattice.

Both halves are necessary. Without snapping the box slides continuously and
every cell's accumulated radiance smears into its neighbour. With snapping but
plain local indexing, advancing the origin by one cell shifts every cell's slot
by one, so the whole field is off by a cell until it decays and refills — and
at metre-scale cells a walking camera crosses one about once a second. Keyed
absolutely, a cell that stays in range keeps its slot and its history, and only
the slab that wraps off the far side is stale; the injection blend refills that
within a few frames.

### Measured

Villa fireplace, the shaded wall beside an open fire — red-to-blue ratio, which
is what "is there bounce light" looks like numerically:

(Measured with the box centred on the camera; the forward bias below lifts
these to 3.51 and 4.33.)

| wall patch | ambient constant | culled grid | path traced |
|---|---|---|---|
| right of chimney | 1.14 | **3.19** | 3.50 |
| left of chimney | 1.07 | **4.02** | 5.30 |

**91% and 76% of the reference's tint, with no authoring and updating every
frame.** That matches what the baked ambient zones reached on the same view
(3.24), which took hand-drawn boxes and an offline bake.

With the gather averaged and `GI_GAIN` re-tuned to 1.2 against the path
tracer, the same patches read 3.58 and 4.26. Absolute brightness is still low —
69 red against the reference's 92 — so the hue is right and the magnitude is
not, which is the same gap the zones showed.

### Combining with the prebaked ambient: `RAYTRACER_LIVE_GI=2`

The two are already stacked — the grid answers inside its box, the ambient
zones and then the scene constants answer outside it — but mode 1 stacks them
with a hard switch, so the box boundary is a brightness step that travels with
the viewer. Mode 2 makes the handover a ramp instead: `gi_coverage` falls from
one to zero across the outermost `GI_EDGE_CELLS` of the box, and
`scene_ambient_at` mixes the grid into the baked answer by it.

Two things are deliberately *not* in it.

**It does not defend the bake's brightness.** The grid is free to come out
darker than the authored ambient, and routinely does: one gathered bounce is
usually less than a level picked by eye. An earlier version enforced "the grid
may add light, never remove it", on the reasoning that an 18 m box always
under-samples the far field the bake accounted for. It was dropped. No test on
the stored value separates the cases: the fireplace wall — the case the
feature exists for — gathers light that is *redder* than the bake and no
brighter, so any brightness test suppresses exactly what it is meant to keep.

**It does not touch `gi_propagate`.** Nothing reads the field that pass writes
except `scene_ambient_at`, so mixing the bake into storage buys nothing, and it
would round the bake's hemispherical gradient (`scene_ambient` is a
sky/ground lerp in `n.y`) through a six-face cube that cannot represent it —
measured as up to 9 display levels of unintended darkening on 2.4% of the
office frame. The whole combination is four lines at the read site.

**Honest result: it measures the same as mode 1.** On the villa fireplace and
on office-sunset, mode 2's patches match mode 1's to within the run-to-run
noise, and the sharpness of the grid's own footprint is identical to three
decimal places. Sweeping the camera 1.6 m in 0.1 m steps — crossing a lattice
line, so the box snaps a whole cell — shows no jump in either mode above the
parallax of the step itself. The step mode 2 removes is real in the code; it is
not visible in anything measured here. It is the right structure and it is
cheap (+0.3 ms of 17.7), but it is not yet a demonstrated improvement.

### The box is biased forward, not centred

Centred on the camera, half the grid sits behind the viewer and the box reaches
only its half-extent — 9 m — into the scene. On the fireplace view that put the
wall being measured at the far face, inside the boundary fade, which is what
made mode 2 look like it was doing nothing.

`giForwardBias` pushes the box 0.35 of its half-extent along the view
direction. It applies to both modes:

| wall patch | centred | biased | path traced |
|---|---|---|---|
| right of chimney | 3.38 | **3.51** | 3.50 |
| left of chimney | 4.11 | **4.33** | 5.30 |

Not one: at a full bias the box starts at the near plane and light bouncing off
the floor just under the camera — the strongest indirect term in most interiors
— falls out of the grid entirely. The measurement saturates by 0.25, so 0.35
has margin without giving up the ground behind.

### Known: scrolled-out cells go stale

Walking makes this measurable. `tmp/giwalk` converges at a pose, walks sideways
and back, and renders one frame at the original pose: the fireplace patch
returns at RGB 36 against the 69 it holds when parked, and the same 36 whether
the walk was 20 m or 150 m, so it is a transient and not an accumulation. Mode
2 does not help — the affected cells are deep inside the box, where the
coverage ramp is one.

A slot that wraps off the far side of the box keeps the radiance of the cell it
used to represent. If the cell it now represents is empty air, nothing
re-injects it and the old value stays — a phantom emitter somewhere the viewer
has already walked past.

Averaging makes this bounded rather than explosive, so it is no longer a
blow-out, but it is still wrong light. The fix is to zero a slot when its world
cell changes, which needs the previous frame's origin in `Params` and a small
pass ahead of `main`; `Params` is currently packed to its last byte, so it
needs a slot first.

| GPU | off | mode 1 | mode 2 |
|---|---|---|---|
| villa fireplace | 13.5 ms | 17.4 ms | 17.7 ms |
| office-sunset | 12.0 ms | 15.5 ms | 15.6 ms |

Median of three interleaved rounds. Layering the bake underneath costs 0.3 ms:
it is one extra evaluation of the zone list per shaded vertex, and the zone
list is short.

### Removed: the per-primitive surface cache

Built, measured, removed. Keying storage on geometry instead of space is the
right instinct for large levels, and it did eventually produce the first
occlusion shadow in this project — but only after four separate faults were
fixed, and it never approached the grid's fidelity. Subdividing faces into
texels turned it into a lightmap with a lightmap's problems: the sample grid
became the most visible thing in the frame, and the cached radiance smeared a
sharp light pool across a whole wall, which is precisely the detail the
approach existed to preserve. The grid, at 1.5 m cells, reproduces the same
scene better and more cheaply.

## Probes: `RAYTRACER_LIVE_GI=3`

Modes 1 and 2 have two visible faults — the culled grid pops as it scrolls, and
nothing beyond the box receives indirect light at all. Both come from the same
line of the design.

**The all-pairs gather is the root cause.** `gi_propagate` is O(cells^2): 1728
cells is 3M pairs, and that is what caps the grid at 12^3. A level-sized grid
at 2 m is ~12,000 cells, 151M pairs, about 50x the work. So the box had to
shrink to 18 m — and *that* is the pop. And because injection only comes from
visible primary hits inside that box, a lamp 30 m away contributes nothing.
One cause, both symptoms.

**Probes replace the gather with tracing.** Each probe casts `GI_PROBE_RAYS`
rays and shades what they hit, so the update is O(probes * rays) — linear, a
budget rather than a quadratic. A probe sees a lamp across the room whether or
not it is on screen, so there is no screen-space dependency left anywhere in
the field.

Probe rays are traced with the megakernel's own `ray_color`, not the path
tracer's transport, so the indirect term agrees with the direct one instead of
drifting from it. Their first hit is shaded with `scene_ambient_at`, which
reads the probe field — so further bounces come from the field itself, which is
DDGI's multi-bounce trick and measures as +18% on the fireplace wall.

### Two cascades, because one grid cannot be both

A single uniform grid is either level-wide or fine enough to resolve room-scale
bounce, not both. Measured on the villa fireplace, red-to-blue against the path
tracer's 3.50 and 5.30:

| grid | right of chimney | left of chimney |
|---|---|---|
| one grid, 4.6 m | 1.84 | 2.12 |
| one grid, 2.3 m | 2.35 | 4.93 |
| **two cascades** | **2.81** | **5.25** |

The 2.3 m row is 8x the probes: 25 seconds to converge at the frame budget, and
it does not fit storage. So the fine cascade is small and follows the viewer
(16x12x16 at 2 m), and the coarse one spans the level and never moves.

**What makes this work where the same shape failed in mode 2** is what the
fallback is. Mode 2 fell back to an authored constant, so crossing the fine
grid's boundary was a step between two unrelated quantities. Here it falls back
to the same kind of field produced the same way, so the two agree and the
crossing is a blend.

### Distance to the path tracer

Fireplace patches, RMS over RGB in display levels, both patches:

| | RMS |
|---|---|
| authored zones only | 35.7 |
| mode 1, culled grid | 38.0 |
| **mode 3, cascaded probes** | **13.7** |

Mode 1 is *worse* than the bake overall — it wins on tint and loses more on
magnitude. Mode 3 is 2.8x closer than either. The left patch's red-to-blue
lands at 5.25 against the reference's 5.30.

### Both original faults, measured

**Pop.** One frame at a pose after walking 60 m away and back, against the same
pose parked:

| | right patch | left patch |
|---|---|---|
| mode 1 | −48% | −28% |
| mode 3 | −13% | −11% |

The coarse cascade never moves, so it catches the fine one while it refills.
Not eliminated — the fine cascade still scrolls — but a quarter of the size.

**Distance.** On the mountain view, with the villa 100+ m away, mode 3
contributes 1.9x what mode 1 does (mean |delta| 0.0047 against 0.0025), and its
footprint is far less of a cliff (sharpest gradient 37.6x the mean, against
61.7x).

### Aligning with the DDGI paper

The first probe build used six face directions with cosine-lobe moments. That
leaks, because a face's "mean distance in +X" averages the whole +X hemisphere:
a probe beside a wall records a mean of half the room and happily lights points
behind it. Six directions is not a visibility test.

It now stores what DDGI stores: an **8x8 octahedral irradiance map** and an
**8x8 octahedral depth map** of (mean, mean squared) distance, read with DDGI's
own weighting — wrap shading `(dot*0.5+0.5)^2 + 0.2`, a normal/view bias on the
query point, Chebyshev against the depth map, and the low-weight crush.

**Five bugs came out of that, each worth recording:**

1. **The crush must come before the trilinear weight, not after.** Folding
   trilinear in first puts every corner probe under the 0.2 threshold on its
   own — a corner is about an eighth — so all eight got cubed into the 1e-8
   range, the sum fell under the guard, and the whole cascade silently gave way
   to the coarse one. It read as a dim grey wash, because the coarse probes are
   mostly outdoors and what came back was sky.

2. **The bias is metres, not a fraction of probe spacing.** At `0.75 * cell` it
   is 1.5 m on the fine cascade, which does not nudge the query point off the
   wall so much as move it into the middle of the room — far enough to cross a
   thin wall and sample the visibility of somewhere else entirely.

3. **An unwritten depth texel reads as mean zero**, which the Chebyshev test
   treats as an occluder pressed against the probe, rejecting everything. A
   texel no ray has reached yet has to get the benefit of the doubt.

4. **DDGI's backface rejection does not transfer to analytic primitives.** A
   room here is a hollow box, so every ray that hits an interior wall is a
   backface hit by the geometric test. Applying it zeroed the radiance of
   essentially every indoor probe ray.

5. **A second lighting path is a second place for a light type to go missing.**
   A first cheap probe shader — wide lights exactly, one pick from the cluster
   — saved 1.8 ms of 8.9 and silently omitted campfires, which is the villa
   fireplace: the one light whose bounce this feature exists to show. It read
   as a 2.3x darkening with the tint gone. See `gi_probe_shade` below for the
   version that keeps every light type.

### The probe shading path

Probe rays are the whole cost of this feature, so they get their own shading
function — but one that keeps everything able to change the answer and replaces
only the per-light shadow loop:

- **wide lights exactly**, because there are few and they reach everywhere;
- **campfires exactly**, because the first attempt dropped them along with the
  villa fireplace's entire bounce;
- **the clustered lights by resampled importance** — four candidates drawn,
  weighted by what each would contribute unshadowed, one kept, one shadow ray.

The importance step is what makes a single sample survivable. Uniform selection
scaled by the cluster's population is unbiased but so noisy that the hysteresis
cannot average it out inside a clamp; the path tracer found the same thing
scene-wide. At one draw the estimator collapses back to that uniform case,
which is a useful check on the weight.

Measured against shading probe rays through `shade_diffuse` in full:

| | villa | office |
|---|---|---|
| `shade_diffuse` | +11.4 ms | +9.6 ms |
| **RIS probe shader** | **+9.2 ms** | **+7.3 ms** |
| RIS + `gi_probe_trace` | **+8.1 ms** | **+3.9 ms** |

and it costs nothing in accuracy — the fireplace patches move by 1-2% (left
patch 128.0 against 128.8, reference 127.8) — while settling slightly improves,
0.166 to 0.145 display levels a frame, because one importance-sampled light is
a lower-variance estimate than the sum of many shadowed ones is per update.

### The probe trace

Probe rays no longer go through `ray_color`. It carries a segment stack of
`MAX_SEGS` ray records in registers, glossy and refractive lobe bookkeeping,
the reflection filter's aux outputs, heat shimmer, ghost segments and the
profile counters — all register pressure a probe ray pays for and never reads.

The way *not* to fix that is a second traversal path written for probes: that
is the same shape as the campfire bug above, one more place for a surface type
to go quietly missing. Instead `surface_at()` — material, albedo, normal and
surface parameters for every kind of geometry — is factored out of `ray_color`,
and `gi_probe_trace` calls the same function. The refactor is verified by the
off-path renders staying byte-identical across five scenes.

| | villa | office |
|---|---|---|
| probe rays via `ray_color` | +9.2 ms | +7.3 ms |
| **via `gi_probe_trace`** | **+8.1 ms** | **+3.9 ms** |

The office nearly halves. The villa moves less because what dominates there is
`accumulate_flame` marching the campfire's volume, which both paths do.

**Specular surfaces have to be followed, not shaded.** The first lean version
terminated on every hit, which shades a window as a lit wall and a mirror as a
grey panel. Both *add* light — the wrong direction for a field that is already
leaking — and on office-sunset, which is largely glazed, it raised the excess
against the path tracer from 0.0012 to 0.0015. `gi_probe_trace` now carries two
segments: mirrors reflect, glass passes straight through (not refracted — which
part of the garden a window sees is detail no 8x8 irradiance map can hold), and
only a diffuse hit ends the ray. That restored the excess to 0.0012 and left
fidelity level with `ray_color` — villa RMS 11.6 against 11.8, office RMSE
0.0112 against 0.0111.

**A dead clamp came out of this.** Probe rays were supposed to be one bounce,
set by `max_depth = 0u` inside `ray_color` — immediately overridden by the
`if max_depth == 0u { max_depth = 2u; }` two lines below it. Probe rays had been
running two bounces the whole time, which is why removing the clamp changed
nothing measurable when it was tested. `GI_PROBE_BOUNCES` is now explicit, and
2 because that is the behaviour every number here was tuned against.

### Why the paper's 1-2 ms is not reachable here

DDGI reports 1-2 ms on an RTX 2080 Ti. That machine has ray-tracing cores; this
one traverses a BVH in a compute shader. Measured here, a single-bounce ray
costs **56 ns** — the traversal-only probe pass is 3.7 ms for 65,536 rays,
before any shading. An RT core is one to two orders faster at the same work, so
the paper's *ray budget* — every probe, 64-256 rays each, every frame, several
hundred thousand rays — costs tens of milliseconds here.

What transfers is the structure, not the number. The budget is rays per frame,
and two scheduling mistakes cost more than the rays did:

> The budget is also backend-dependent now. On the Metal path a ray costs about
> half what it does on the WGSL path
> ([metal-backend.md](metal-backend.md#what-a-ray-costs-now)), which is closer
> to the paper's assumptions than this section had to assume -- though still far
> from an RT core's one-to-two orders.

- **One thread per probe leaves the GPU idle.** 2048 probes is 32 workgroups of
  long serial threads; it measured the same at 256 probes as at 2048, which is
  the signature of a latency-bound pass. A lane per ray took it from +17 ms to
  +5 ms.
- **The workgroup must be a whole SIMD group.** Apple's is 32 lanes, and a
  16-wide workgroup wastes half of one — 1.7x on the whole pass.

### Settling

With the camera still and nothing animating, a converged field should render an
identical frame every time. `tmp/giwalk -seq` measures what is left, in display
levels per frame:

| | mean | worst |
|---|---|---|
| GI off | 0.000 | 0.000 |
| mode 1 | 0.056 | 0.083 |
| mode 3, first build | 0.481 | 1.589 |
| **mode 3, now** | **0.166** | **0.594** |

Three things got it there, and one did not:

- **The ray-set rotation has to be a real rotation.** Offsetting the Fibonacci
  spiral's azimuth by the frame index is a rotation about Z and nothing else,
  so the +-Z faces resampled almost the same directions every frame while the
  others swung. The error was periodic rather than random, which is what soft
  ovals fading in and out on a cycle actually is. It is a uniform random
  quaternion now.
- **The update cycle has to be short.** Refreshing an eighth of the coarse
  cascade per frame means its probes all step together on an eight-frame beat.
  Both cascades are halved per frame now, so the cycle is two frames.
- **Hysteresis scales the flicker almost exactly.** Measured at 0.08, 0.04 and
  0.02: 0.45, 0.23 and 0.12 levels a frame. It is 0.03.
- **An adaptive blend rate did not work.** Converging faster when the answer
  moves a lot is what RTXGI does; at 32 rays a texel's own sampling noise is
  tens of percent, so any threshold low enough to catch a real change fires
  constantly and the field shimmered four times worse (0.73 against 0.17).
  Separating the two needs a signal that is not the sample itself — that the
  probe has scrolled, or that a light has moved.

### The depth lobe can only be as sharp as the ray budget feeds

The server-room walls showed dark diamond-shaped patches in a regular grid —
one octahedral texel's footprint drawn in world space, repeating at probe
spacing. Disabling the Chebyshev test alone removed them, which located it in
the depth map rather than the irradiance map.

The cause is undersampling, not the test. DDGI's depth lobe is `pow(dot, 50)`,
which assumes the paper's 64-256 rays a probe. At 32 rays into 64 texels an
exponent that steep leaves most texels with **less than one contributing ray
per frame**, so the map is a patchwork of stale single samples. And a single
sample has `E[t^2] == E[t]^2` exactly — zero variance — so Chebyshev degenerates
from a soft bound into a hard binary reject, and cubing it makes the rejection
total. A black cone per probe, per direction that happened to be sampled badly.

Dropping the lobe to `a^4` gives every texel several rays a frame. The patches
are gone, and the leak metric moved the *right* way — excess against the office
reference 0.0012 to 0.0011 — because a well-sampled blunt estimate beats a
sharp one made of noise. Settling improved too, 0.146 to 0.130 levels a frame.

A minimum-variance floor went in alongside it, the term variance shadow maps
carry for the same degeneracy. Honest accounting: on its own it did **not** fix
the patches, because the problem was that the estimate had no samples rather
than that it had no spread. It stays as a guard on the degenerate case.

**The general lesson, and the one to apply to the remaining knobs:** every DDGI
default assumes a hardware ray budget. Ported at face value onto a software BVH
they do not merely cost more, they break — a sharp lobe over too few rays is
worse than no sharpness at all.

### Fixing the leak: four causes, none of them the formula

The server room leaked badly — a bright light sits directly behind its east and
south walls and the walls glowed. Measured as *what the GI term adds*, against
the path tracer's own indirect-only render on the same view, it was **8.56x**
the reference. The scene's authored ambient, for scale, is 2.55x.

The first useful signal was that **switching the Chebyshev test off made the
image very slightly better**. A visibility test that costs nothing to remove is
not doing anything, and four separate things were stopping it working. None was
the bound itself.

**1. Ray distance was recorded uncapped.** A probe ray that leaves through a
window hits nothing and returns `GI_PROBE_FAR` — ten kilometres. Blended into a
depth texel at a few percent a frame, one of those drags that texel's mean into
the hundreds of metres, and a mean of hundreds means *nothing is ever in the
way*. Rendering the recorded depth showed it saturated across every wall: the
probes reported metres of clear space through solid geometry. Distances are now
clamped to four cells, which is DDGI's probe max ray distance and is all the
map needs — it only has to answer whether something sits between a probe and a
point one cell away.

**2. The depth map was too coarse to resolve a wall.** At 8x8 an octahedral
texel spans about 45 degrees and the bilinear read blends four of them, so the
lookup averaged over roughly 90 degrees. No update lobe can sharpen past the
resolution of the map it writes into. Depth is 16x16 now; irradiance stays 8x8,
because irradiance is smooth and occlusion is not.

**3. The blend fell back to the coarse cascade exactly when it should not.**
`mix(coarse, fine, cov * fine.w)` weights by whether the fine cascade answered
— and the one case where every fine probe is rejected is a point that is
*correctly* occluded. Falling back there hands it the answer from probes ten
metres apart, which cannot resolve a room, let alone a wall. The better the
fine cascade got at rejecting, the more light this let through. It blends on
coverage alone now, so a fully rejected point returns zero and keeps it.

**4. The bias was absolute where it needed to scale with probe spacing.** This
was the big one. On a wall, `dist` and the recorded depth are the same number,
so the test sits permanently on its own boundary and any texel-to-texel
variation flips it on and off — which is what drew first the diamonds and then
the rosettes. The bias exists to push the query point clear of the wall it
belongs to, and how far is "clear" is set by how far apart the probes are. A
fixed 0.15 m is a tenth of the margin on a 2 m grid. At a quarter of a cell the
artifacts vanish and the leak collapses.

| | GI added | vs path-traced indirect |
|---|---|---|
| before | 0.05253 | 8.56x |
| + capped ray distance | 0.04374 | 7.12x |
| + 16x16 depth map | 0.03430 | 5.59x |
| + cell-scaled bias | **0.01164** | **1.90x** |
| (authored ambient, for scale) | 0.01567 | 2.55x |

Excess light in the reference's darkest quarter — the number leaking actually
lives in — went from **0.0488 to 0.0106**, and the villa fireplace improved at
the same time rather than paying for it: RMS against the reference 11.6 to
**10.1**, with the green channel landing on 52.5 against 52.3.

**Also corrected here:** the earlier fix for the diamond artifacts widened the
depth lobe from `a^16` to `a^4`. That diagnosis was wrong. The diamonds were
the self-occlusion boundary, not undersampling, and widening the lobe made a
probe beside a wall average that wall together with the open room grazing past
it — which is what destroyed the occlusion in the first place. The lobe is
sharp again, the boundary is handled by the bias and a few percent of slack on
the recorded depth, and the depth map gets its own much slower blend rate,
which it can afford because depth is geometry and does not change between
frames.

### Where it actually stands

Honest, and mixed.

Villa fireplace against a path-traced reference, RMS over RGB in display levels
across both patches:

| | RMS |
|---|---|
| authored zones only | 35.7 |
| mode 1, culled grid | 38.0 |
| mode 3, six-face probes | 13.7 |
| **mode 3, octahedral probes** | **11.7** |

The left patch's red lands at 128.8 against the reference's 127.8.

But on **office-sunset, against a 256 spp path-traced reference, GI is not yet
an improvement over no GI at all**:

| | RMSE | excess light |
|---|---|---|
| GI off | 0.0084 | 0.0009 |
| mode 1 | 0.0115 | 0.0003 |
| mode 3 | 0.0111 | 0.0011 |

(That view is close to flat for every mode. The server-room view above is the
one that separates them, and is where the leak work was done.)

That scene's authored ambient was tuned by eye and is already close to the
reference; a physical estimate sampled ten times under budget does not beat it,
and mode 3 still carries the most excess light — which is leaking, just less of
it than six faces leaked. The octahedral maps are the right mechanism, and the
leak reduction is **not yet demonstrated on the scene where it was reported**.

Cost is now **+8.8 ms on the villa and +4.5 ms on the office**, against mode 1's
+4 ms — the office reached parity with the far cruder mode 1, the villa has not,
because the campfire's volumetric march dominates it.

And lowering hysteresis to stop the shimmer traded away responsiveness. One
frame back at a pose after walking 60 m out and returning:

| | right patch | left patch |
|---|---|---|
| six-face, alpha 0.08 | −13% | −11% |
| octahedral, alpha 0.03 | −29% | −41% |

Stability and pop are the same knob pulled in opposite directions, and nothing
here has separated them.

### Known limits

- **Ray budget.** Everything above is downstream of 56 ns a ray. At 65,536 rays
  a frame the field is roughly ten times under-sampled against what DDGI
  assumes, and the residual shimmer is that, not a bug.
- **Probes inside geometry are not classified.** DDGI counts backface hits;
  that test does not work on hollow-box rooms (above), so a buried probe just
  reports what it sees.
- **The fine cascade still scrolls.** Toroidal indexing keeps a probe's history
  while it stays in range, and the coarse cascade covers what wraps.
- **Bounds come from `Probe.LitBounds`.** Without a bake there is nothing to
  size the coarse cascade from, and mode 3 falls back to mode 1. The filter
  matters: the villa's raw bounds are 826 x 2202 x 895 m, which at any
  affordable probe count puts probes tens of metres apart and they see nothing
  but sky. Filtered they are 163 x 93 x 217 m.

## Baked probe volumes: `cmd/probebake`

Everything the live field struggles with is downstream of one number: a
software BVH traverses a ray in about 56 ns, so a frame can afford tens of
thousands of them. Offline that budget does not exist.

**The baker is the renderer's own probe pass, driven offline.** Not a second
implementation: the same dispatch, the same `gi_probe_trace`, the same
`shade_diffuse`. It inherits every fix above rather than reproducing them,
which matters — a parallel shading path in this feature has already dropped
campfires once.

```bash
go run ./cmd/probebake -scene scenes/office-sunset/index.toml -spacing 1.0 -rays 512
RAYTRACER_GI_BAKE=scenes/office-sunset/index.gi RAYTRACER_LIVE_GI=3 go run .
```

`-rays` is spent as iterations of the shader's fixed 32, each with a fresh
rotation of the ray set, and the blend rate is set to 1/k on the k'th pass so
it is an exact running mean — N iterations average N x 32 rays rather than
trailing them. `-spacing` and `-radius` size the volume.

### Nothing is culled

One static grid over the whole scene. Nothing follows the camera, so there is
no boundary to cross and no history to lose:

| | live cascades | baked |
|---|---|---|
| frame-to-frame change, camera still | 0.146 levels | **0.000** |
| frame after walking 60 m out and back | −29% | **byte-identical** |

It is affordable only because it is sparse. A grid that covers a level at a
useful spacing is far too many cells to store densely — the villa at one metre
is 3.3M, which is 9 GB of probe records, and almost all of it is empty air. A
dense index grid of one u32 per cell points into probe records kept only near
geometry.

### Measured, on the server-room view

| | gpu | leak in dark | mid error | RMSE |
|---|---|---|---|---|
| GI off | 9.5 ms | 0.00709 | 0.01254 | 0.03370 |
| live mode 3 | 13.3 ms | 0.01059 | 0.01573 | 0.04112 |
| **baked, 2 m / 256 rays** | **10.8 ms** | **0.00106** | **0.01068** | **0.03356** |

The first configuration in this document that beats *no GI at all* against the
path tracer, on every measure, and it costs +1.3 ms against the live field's
+3.8 — because what a bake removes is the update, which is where almost all of
that cost was.

| bake | probes | file | leak in dark | mid error |
|---|---|---|---|---|
| 3.0 m / 64 rays | 21k | 58 MB | 0.00027 | 0.01101 |
| 2.0 m / 256 rays | 46k | 130 MB | 0.00106 | 0.01068 |
| 1.25 m / 512 rays | 129k | 366 MB | 0.00111 | 0.01052 |

Files are large because the depth map is 16x16x2 floats and dominates the
record. It is pure geometry, so it need not be stored at all — deriving it at
load by running the pass depth-only would cut these by roughly five times, and
is the obvious next step.

### Two bugs worth keeping

**A bake that reads back zeros looks like a success.** `webgpu.New` loads the
placeholder file into a `Volume` of its own, and that is the one the readback
fills; the baker's copy still held its zeros, and writing it produced a file
that loaded cleanly and rendered *better than anything before it* — dark walls,
no leaking, no artifacts, an excellent leak score. An empty field simply
switches the ambient off. The baker now refuses to write a field that is all
zero, because nothing else about it looked wrong.

**More than 65,535 workgroups aborts the process.** One dispatch dimension caps
there, and a bake wants a workgroup per probe. Going over it did not return an
error; it killed the process with a bare Metal encoder assertion, which reads
like a driver bug rather than a limit. The pass is dispatched as a 2D grid now.

## What would fix the shadow

Visibility, which is the conclusion every approach in this project has reached
from a different direction. The cheapest version here: store, per cell pair
direction, how far the grid can see — the same depth-moment trick DDGI uses —
and weight the gather by it. The grid is tiny, so there is room.

## Related

- [ambient-zones.md](ambient-zones.md) — the authored, baked version of the
  same idea, and the same missing visibility term.
- [path-tracer.md](path-tracer.md) — the reference this is measured against.
