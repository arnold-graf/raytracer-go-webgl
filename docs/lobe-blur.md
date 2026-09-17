# reflect_blur and transmit_blur

**Status:** shipped, scene-driven. Two new primitive fields widen a lobe in
screen space after it has been traced. A scene that sets neither renders
**byte-identical** to before and pays nothing — the passes are not even
dispatched.
**Cost:** +1.0% on a scene that uses it (office-sunset with the effect forced on,
four views, interleaved). No extra rays.

```toml
[[box]]
material = "diffuse"
reflect = 0.4
rough = 0.02          # world-space roughness, unchanged: perturbs the traced ray
reflect_blur = 0.15   # and on top of it, a soft reflection

[[box]]
material = "glass"
transmit = 0.9
transmit_blur = 0.25  # frosted, while its reflections stay as hard as rough makes them
```

---

## What these are, and what they are not

`rough` perturbs the traced ray. That is what anchors a reflection in world space
and what makes a surface read as physically rough, and it is untouched — these
fields do not replace it, scale it, or interact with it. They are applied *after*
the lobe is traced, so the two combine: a surface can be world-space rough, or
screen-space soft, or both.

The screen-space blur buys something no number of traced samples per pixel could
pay for at this frame budget: a genuinely wide, smooth lobe. One jittered ray per
pixel gives one sample of that lobe, which is why a wide `rough` reads as a
dashed hash rather than a blur (see [the defect](#the-defect) below). So this is
a deliberate effect for surfaces that want it — a soft reflection on polished
stone, frosted glass — not a general replacement for roughness and not an
optimization.

**Two fields, two lobes.** Glass forks a reflection and a transmission at one
hit, and they are the pair an author most wants to set apart. They are carried as
independent channels end to end: separate tags, radiances, hit distances, radii
and spread constants. Verified on a two-pane scene where one pane sets only
`transmit_blur` and the other only `reflect_blur`:

| filtered channel | pixels changed on the transmit_blur pane | on the reflect_blur pane |
|---|---|---|
| reflection only | **0** | 9,251 |
| transmission only | 13,270 | **0** |
| both | 13,270 | 9,251 |

Exact separation, and the two sum. `tmp/glossy/panes.toml` is that scene;
`tmp/perf/lobe-blur-test.toml` is the fuller one, five floor strips separating
`rough` from `reflect_blur` plus both kinds of pane.

**Units match `rough`.** The blur is an angle, so it grows with how far away the
reflected image is — a reflection is sharp where it meets the surface and softens
with distance, the same contact-hardening structure the penumbra filter has. The
constant that sets it (`REFL_SPREAD`, `REFR_SPREAD` = 0.5) was calibrated so that
`reflect_blur = x` draws what `rough = x` converges to over many samples, which
is what makes the two interchangeable in scale.

**Scope.** `reflect_blur` works on primitives, on `[[screen]]`, and on
`[[terrain.zone]]`; `transmit_blur` on glass primitives. Water carries neither.
The thin-glass ghost stays inline and unfiltered: it is a second-pane reflection
at a different angle, so it is not the lobe either channel holds out.

A terrain zone reaches the shader through its own buffer, so it needed its own
plumbing all the way down — `scene.TerrainZone`, the zone DTO, `GPUTerrainZone`
(stride 80 -> 96, a `Surf2` lane since the four `Surf` lanes were spoken for),
`terrain_zone_surface`, and its own arm in the filter's enable test. Zone fade
weight scales `reflect` but not `reflect_blur`: the blur is a width, not an
amount, so it is taken as authored — where the weight goes to zero, `reflect`
does too and the lobe disappears with it.

**`[[screen]]` needs its own plumbing, and did not get it for free.** A screen
does not go through `surfaceDTO`. It has its own DTO, its own `scene.ScreenSpec`,
and `screen.Manager.Instantiate` builds the `scene.Surface` field by field — so
any surface property the chain does not name is silently dropped, whatever the
shader would have done with it.

`reflect_blur` hit this. So had `specular` and `shininess`, which were never
parsed on a screen at all: the shader reads them from `albedo.w` / `albedo2.w`
and always has, but nothing put them there. Both are plumbed now, DTO through
spec through Surface, and the schema advertises them.

The remaining hand-rolled Surfaces are `internal/character/attach.go` and
`rig.go`; they have the same trap waiting. When adding a surface property,
grep for `scene.Surface{` rather than assuming `surfaceDTO` is the only door.

**The offline tools cannot see screens.** `cmd/gpuprof` and `cmd/preview` never
call `screen.NewManager()`, so `[[screen]]` panels are absent from their renders
entirely. A screen surface therefore cannot be verified with `-dump`, which is
how the plumbing gap above went unnoticed: every four-view diff came back at
0.00% whether the field worked or not. Check screens with a stand-in `[[box]]`
carrying the same material numbers, or in the app.

**Weight still gates it.** The blur widens the reflected lobe; it does not
strengthen it. On the workstation monitor `reflect = 0.03`, so 3% of the pixel is
being blurred — measurable (max 82 levels on a stand-in panel against a bright
bar) but far short of what the same surface does at `reflect = 0.30` (max 175).
If a blurred reflection reads as too faint, `reflect` is the knob, not
`reflect_blur`.

---

## The defect

`jitter_dir` perturbs the reflected ray by a hash of the hit position:

```wgsl
sin(p.x * 73.1 + p.y * 17.3) * 0.5 * rough
```

One ray per pixel, so each pixel gets a *single sample* of the reflection lobe.
That is the same failure the penumbra filter was built to escape — n samples give
only n+1 levels — but it is worse here, because the hash is not noise. It is a
sinusoid in world space with a period of about 0.086 units, which at a metre or
two from the camera is roughly **seven pixels**. Neighbouring pixels therefore
sample the lobe in a correlated way and the reflection breaks into dashes.

A purpose-built scene, `tmp/glossy/glossy-test.toml` — one floor split into
strips of roughness 0 to 0.20 across the view, under bright bars at three heights
so a single reflected feature crosses every roughness level in one frame:

| | |
|---|---|
| jittered (shipping) | `tmp/glossy/band_jit.png` — a dashed hash, not a reflection |
| filtered | `tmp/glossy/band_filt2.png` — a smooth gradient |

This is very likely why every scene in `scenes/` uses `rough` between 0.005 and
0.16, with most at 0.01: above that the feature looked broken, so nobody used it.
Making roughness usable is the point of this change.

## What it does

The same trade the penumbra filter makes. A rough surface scatters the reflected
ray through a fixed *angle*, so the image it reflects is smeared by that angle
times how far away the image is — and the reflected hit distance is a quantity a
real ray already measured. So:

1. The primary glossy lobe is traced as an **unperturbed mirror ray**.
2. Its radiance is held out of the pixel colour, in the per-pixel aux record,
   along with the distance from the reflecting surface to what it hit.
3. `refl_blur_h` / `refl_blur_v` widen it separably, each pixel gathering with
   its own radius.
4. The vertical pass adds the filtered lobe back into `hdr_pixels`.

There is nothing stochastic anywhere, so no denoiser and no temporal history: it
is a filter over exact data, not a reconstruction from samples.

The lobe is separated by **tagging the ray segment**, not by tracing twice.
`RAYSEG_GLOSSY` is inherited by every child, so a glass pane seen inside the
reflection belongs to the reflection. Each accumulation site adds its
contribution to `accum` as before and, masked, to `gloss`; the returned colour is
`accum - gloss`. That is one extra multiply-add per site and no branch — the
trade this kernel rewards, on the evidence of
[bvh-traversal.md](bvh-traversal.md).

## The gate that had to come out

The design this started from named one risk up front: reflected silhouettes.
Neighbours whose reflected rays landed on different objects should not blend, or
the reflected image smears — and [terrain-cost.md](terrain-cost.md) had found
ray-guided upsampling useless on the pond because it guided on the *water
surface*, which is smooth across exactly those edges. The fix looked obvious:
gate on the **reflected hit distance**, which is what jumps there.

Built, and it is **wrong in principle**. With the gate in, the reflection came
back pin-sharp: every tap across the only edges in the frame was rejected, which
is the one place the filter had anything to do. A rough surface gathers over a
cone that straddles the silhouette — mixing the bar with the sky behind it *is*
the blur, not an artifact of it.

The two problems are not the same problem. The pond experiment was
**undersampling**: it traced one ray per 2x2 and had to invent the three it
skipped, so a reflected edge it never sampled was lost. This filter traces every
pixel and is deliberately averaging them. Guarding a blur against the thing it is
blurring is a category error, and it took a picture to see it.

What remains is a gate on the **reflecting** surface only — normals agree, plus
the plane prediction [soft-shadows.md](soft-shadows.md) uses, so the tolerance
stays tight at grazing incidence where a floor's depth changes fast between
neighbours. The reflected distance still sizes the radius. It just does not veto.

## Cost

Interleaved best-of-3, switched at runtime from a params field so both arms run
the same binary and the same shader on disk:

| yaw | off | on | |
|---|---|---|---|
| 0 | 8.8 ms | 9.0 | +2.3% |
| 90 | 10.2 | 10.3 | +1.0% |
| 180 | 7.7 | 7.8 | +1.3% |
| 270 | 13.0 | 13.0 | +0.0% |
| **mean** | **9.93** | **10.03** | **+1.0%** |

Two full-screen 33-tap separable passes for 1% of the frame, and no extra rays.

**outdoors-night-villa does not change at all** — zero pixels differ. Its pond is
`glass`, which is a different lobe, and its glossy surfaces are all `rough = 0`.
Office-sunset moves 15.6% of pixels but only 0.92% beyond 8 levels, because its
roughness is 0.005 to 0.05: the reflections there were nearly mirrors already.
**The scenes that exist barely need this. Scenes that want a rough floor now can
have one.**

## The perf argument was wrong

This was proposed partly on the grounds that an unperturbed ray is more coherent
than a jittered one, and that the coherence would pay for itself. Measured first,
which is the only reason it did not become a week of work: deleting the jitter
entirely, on office-sunset, four views interleaved, is **+0.8%** — nothing.

The reason is scene content, not the argument being unsound. At `rough = 0.01`
the perturbation is 0.005 radians; there is no coherence to recover because the
rays were already coherent. The argument would apply to a scene built around
genuinely rough reflections, which is the scene this change makes possible — so
it is worth re-measuring if one ever exists, and not before.

The follow-on that argument was really carrying — that once reflections are
blurred, tracing them at half resolution becomes acceptable — was built next. It
is behind `RAYTRACER_REFL_HALF=1` and it **does not pay**; see below.

## Half-resolution tracing: 75% of the rays, 2% of the frame

`RAYTRACER_REFL_HALF=1` traces the glossy lobe for one pixel of each 2x2 block
and fills the other three from it (`refl_fill`), wherever the blur is wide enough
to hide the difference. The pre-trace gate is on roughness, because the radius
needs the reflected distance and that is exactly what has not been traced yet:
radius saturates at `0.5 * rough * focal_px`, so `0.25 * rough * focal_px` is a
fair estimate for a reflected hit at about the surface's own distance.

**The quality side works.** On the test scene it removes 54% of the glossy rays
(89,860 -> 41,522) for 0.22% of pixels beyond 8 levels and a mean error of 0.077
levels — visually indistinguishable from the full-rate filter.

**The performance side does not.** Forcing the gate fully open on office-sunset,
so *every* glossy lobe is sampled at quarter rate, four views interleaved:

| yaw | full rate | quarter rate | |
|---|---|---|---|
| 0 | 7.6 ms | 7.6 | +0.0% |
| 90 | 10.2 | 10.0 | -2.0% |
| 180 | 7.6 | 7.6 | +0.0% |
| 270 | 12.1 | 11.5 | -5.0% |
| **mean** | **9.38** | **9.18** | **-2.1%** |

Glossy rays over the same views fall by about **75%** — 145,410 to 36,338 at yaw
0. Three quarters of the densest secondary lobe in the renderer, deleted, for two
percent.

### Why, and what shape would pay

Skipping lanes does not shorten a workgroup. An 8x8 workgroup holds 16 lead
pixels and 48 followers; the 16 still trace a full ray tree and the workgroup
runs until they finish, so the 48 idle rather than save anything. The critical
path is unchanged, and `refl_fill` adds a full-screen pass that reads up to five
96-byte neighbours, which gives some of the little that was saved back.

This is [bounce-kernel.md](bounce-kernel.md)'s criterion from the other
direction. Removing work only pays when it comes off the critical path, and in a
megakernel the critical path is the slowest lane in each workgroup.

It also explains the one reduced-resolution experiment that *did* pay.
[terrain-cost.md](terrain-cost.md)'s water reflection deferred the lobe to a task
buffer and ran a **separate pass** over the shared footprints, and its saving
"tracks removed rays almost exactly" — 13.5% of segments for 13% of frame time.
That works because the reduced dispatch genuinely has a quarter of the threads.
Skipping in place has all of them.

So the shape that pays is the deferred one: record each glossy surface's ray
origin, direction and weight, then run a quarter-resolution dispatch over that
list and composite. It is a real build — roughly 32 more bytes per pixel of ray
state, a new dispatch, a composite — and it is not obviously a win either, since
bounce-kernel.md found a *full*-resolution glossy split lost to dispatch overhead
even while being 7-28% cheaper per ray, and a quarter-resolution one is 40k
threads, which the adaptive-AA measurements put in the thin regime. But it is the
only version of this idea the evidence supports trying.

**As it stands the half-res path is kept, gated and off by default, because it is
correct and it is the scaffolding for that build — not because it earns its
keep.** At the shipped gate (`REFL_HALF_MIN_PX = 3.0`) it does not fire on any
scene in `scenes/` at all: their roughness tops out at 0.16 and most is 0.01.

### One free saving that did land

An adaptive-AA tap discards its own glossy lobe and takes the centre's filtered
one, so tracing it was always waste. Taps now pass `GLOSS_NEVER`. That is
**byte-identical** and removes about 9% of the scene's glossy rays (118,078 ->
107,353 at office yaw 270). It applies whenever the filter is on, at any
roughness.

## Why replacing `rough` was the wrong idea (how this design was arrived at)

The first build replaced the jitter outright: every glossy lobe was traced
unperturbed and blurred instead. Reported immediately — reflective surfaces
looked blurrier but no longer *rough*. Three measurements, and the third is why
the design is now additive and opt-in rather than a replacement.

**It is not half-res.** On office-sunset, `RAYTRACER_REFL_HALF=1` is byte-identical
to the new fields at every view — zero pixels differ. Nothing there
clears the roughness gate, so half-res never fires.

**The server room is not this lobe at all.** `server-room-1.toml` profiles as
`diffuse_refl 0`, `glass 321,503`. Its reflections are glass, and `rough` there
feeds `jitter_dir` on the glass reflect and refract lobes, which this filter does
not touch — they still jitter exactly as before. The filter only holds out the
primary lobe of a `diffuse`/`checker` surface with `reflect > 0`.

**Where it does apply, the physically correct blur is under a pixel.**
Classifying every glossy pixel of office-sunset by its blur radius:

| view | glossy pixels | < 1px (no blur at all) | 1-3px | >= 3px |
|---|---|---|---|---|
| yaw 0 | 142,574 (87% of frame) | 37.3% | 62.7% | **0.0%** |
| yaw 270 | 105,131 (64%) | 71.3% | 28.7% | **0.0%** |

Not one pixel in the scene wants a blur of three pixels or more, and a third to
two thirds want less than one — where the filter deliberately does nothing and
leaves a pure mirror.

So the old look was not a blur. At `rough = 0.01` the single-sample hash is a
*coherent displacement* of the reflection with a seven-pixel period, and a
sub-pixel displacement is plainly visible where a sub-pixel blur is not. What
read as surface roughness was the sampling artifact, not the phenomenon.

**The calibration is not the problem** — that was checked rather than assumed.
`jitter_dir` was given an animated phase, 24 frames were averaged to convergence,
and `REFL_SPREAD` was swept against that reference:

| `REFL_SPREAD` | 0.25 | **0.50** | 0.87 | 1.50 | 2.50 | 4.00 |
|---|---|---|---|---|---|---|
| RMS vs reference | 38.1 | **14.3** | 25.9 | 36.0 | 39.9 | 41.3 |

A clean minimum at the shipped value. The filter reproduces what the jitter
converges to; the jitter simply never got there in one sample.

**A methodology trap worth recording.** The first version of that reference
averaged the 24 dumps as 8-bit pixels and was badly wrong — it came back
desaturated and far too wide, and ranked `REFL_SPREAD` in exactly the reverse
order, "confirming" that narrower was better. `gpuprof -dump` is tonemapped and
gamma-encoded, so averaging it is not averaging radiance. The tonemap is
per-channel ACES-approx plus gamma 1/2.2, both monotonic, so a 256-entry inverse
LUT converts each frame back to linear before summing. Do that, and the ranking
inverts to the table above.

### What that settled

A screen-space blur is not roughness and cannot stand in for it. It is wider and
smoother than one jittered sample can be, which is exactly why it is worth
having — and it loses the world-space anchoring that makes `rough` read as a
material property. So `rough` keeps its job, the blur became `reflect_blur` and
`transmit_blur`, and a surface can ask for either or both.

Rule of thumb for authoring, at this FOV and a surface a few metres from what it
reflects: `radius_px ~= 95 x blur`. So 0.05 is about a five-pixel softening and
0.1 about ten.

## Two channels, because glass forks two lobes

Glass reflects and transmits at the same hit and blurs them by different amounts:
`jitter_dir` gets `rough` on the reflected lobe and `rough * 0.35` on the
refracted one. One shared channel could only ever draw both at one width, and
they are also the pair an author most wants to set apart — a frosted pane that
still reflects sharply, or a mirror-ish pane you can barely see through.

So `RAYSEG_REFL` and `RAYSEG_REFR` are separate tags, each inherited by every
child, each with its own held-out radiance, hit distance, roughness, radius and
spread constant (`REFL_SPREAD`, `REFR_SPREAD`). Each accumulation site adds its
contribution to `accum` and, masked, to both channel registers; the returned
colour is `accum - refl_acc - refr_acc`. Two extra multiply-adds per site, no
branch.

`RAYTRACER_REFL_CHANNELS=refl` filters reflections only, `=refr` refractions
only, unset does both — bits 2 and 3 of `params.refl_filter`. Nothing depends on
the choice yet; the switch exists so the two can be judged, tuned and shipped
apart when someone wants to.

Measured on the server room, against the unfiltered image: reflections alone move
12.6% of pixels, refractions alone 3.1%, and the two arms differ from each other
on 12.8% — genuinely independent, and the reflected lobe is what carries that
scene.

**The surface each lobe left from is recorded separately** (`lobe_normal`,
`lobe_depth`) rather than reusing the `normal`/`depth` the penumbra filter
writes. Those are only set by a *diffuse* primary hit and glass never takes that
path — but filling them in for glass would also enrol glass pixels in the
penumbra filter's gathers carrying `frac = 0`, which reads as "fully lit" and
would lighten every shadow beside a window.

**The thin-glass ghost stays inline and unfiltered.** It is a second-pane
reflection at a different angle, so it is not the lobe either channel holds out,
and at 0.42 of an already-Fresnel weight it is faint enough to leave sharp.

### The AA tap has to trace its own lobes, and then may throw them away

The penumbra filter hands an AA tap its *conclusion* — a target visibility — and
that transfers one sub-pixel over because it is a fraction, smooth by
construction. Doing the same with a lobe's *radiance* does not work, and glass is
where it shows: AA fires precisely where the tap lands on different geometry, and
a glass pixel's transmitted lobe can be most of its radiance.

Letting taps skip their lobes entirely and take the centre's filtered ones is
much cheaper — office-sunset goes from +1.0% to **-12.7%**, because a tap on
glass otherwise traces a full two-lobe fork. It also put **236 isolated pixels up
to 190 levels out** along glass silhouettes. Tracing its own lobes instead brings
that to **45 pixels**, and costs the whole saving.

But *always* keeping what it traced is wrong in the other direction, and a
blurred surface is where that shows. The centre's lobe has been through
`refl_blur_h`/`refl_blur_v`; the tap's is a single point sample of the same lobe,
and on a curved surface a sub-pixel shift swings the reflected ray far enough to
land somewhere much brighter. Blended in at `AA_TAP_WEIGHT` that redraws the
unfiltered reflection over the filtered one at 30%: sharp bright streaks down the
front office's bookshelf posts (`reflect_blur = 0.3`), which AA flags all the way
along their highlight.

So the tap traces its lobes and then `supersample_edge` asks it the question the
filter already asks of a neighbour — same reflecting surface or not
(`aa_tap_same_lobe`, which is `lobe_compatible` minus the depth prediction, since
a sub-pixel offset shares the centre's view ray). Same surface and the filtered
lobe describes the tap too, so the tap's own is discarded; different geometry and
only the tap's own will do.

Measured on the bookshelf-post frame (office-sunset, `-quant 3`, isolated
speckle over the posts, counting pixels ≥ 8 levels outside their neighbours'
range):

| | speckles | total excess | worst |
|---|---|---|---|
| no AA (reference) | 5 | 131 | 68 |
| tap keeps its own lobes | 21 | 425 | 63 |
| tap always takes the centre's | 5 | 77 | 28 |
| **same-surface gate** | **6** | **116** | **39** |

Across a sweep of five scenes × four yaws every frame that moved at all improved
and the rest came out bit-identical, the glass views among them: the gate falls
through to the tap's own lobes exactly where the glass argument applies. Cost is
unchanged (65.5 ms against 65.7 on the post frame, inside noise) — the tap still
traces the lobe, because it cannot know whether it needs it until it has hit
something. `GLOSS_NEVER` is still unused; it remains the one-line cheap variant
if someone wants to re-price it.

### Cost of the wider record

`ShadowAux` is 144 bytes with both channels and the lobe surface. Growing it
64 -> 96 cost about 1.8% on the soft-shadow path; 96 -> 144 costs **nothing
measurable** — office with soft shadows on and the filter off is 11.12 ms against
11.10 at stride 96 and 10.90 at 64.

## Cost of the shared record

There is no spare buffer binding. Metal allows a compute stage 31 buffers and the
megakernel uses every one; adding a 31st fails with `Resource limit exceeded`,
which is the same wall that put every index list into `idx_tables`. The
reflection fields therefore live in `ShadowAux`, which grows from 64 to 96 bytes.

That is free when soft shadows are off, because nothing writes the record at all
(hard-shadow office measures 9.93 against 9.88 before, inside noise). With soft
shadows on it measures **11.10 ms against 10.90** at the commit before this work
— about 1.8%, of which the AA tap tuning accounts for 0.3%.

If that matters, `refl_color_h` and `refl_radius_px` can alias `frac_h` and `r_h`:
the reflection passes run before the penumbra passes, so those fields are dead by
the time the shadow filter wants them, and the struct would come back to 80
bytes. It is the same field-recycling trick [soft-shadows.md](soft-shadows.md)
already uses, with the same cost — it welds the pass order in place.

## Known gaps

- **Only the primary lobe.** A glossy reflection seen *inside* another reflection
  still jitters, and still stripes. It is a second-order effect and there is no
  screen-space record to filter it with.
- **The AA tap gets the centre's filtered lobe**, on the same "hand over the
  conclusion, not the arithmetic" reasoning as the penumbra correction. Where the
  tap lands on different geometry that lobe is not really its own. Bounded and
  rare, but not right, and not yet measured against a reference the way the
  penumbra version was.
- **Coplanar surfaces share the blur.** Two strips of different roughness in the
  same plane gather from each other, because the gate can only see that they are
  coplanar. The classic screen-space failure, same as the penumbra filter's thin
  wall.
- **`REFL_SPREAD` (0.5) is a guess**, chosen so the filtered width matches the
  jittered one it replaces. It has not been tuned against a many-sample
  reference, which is what the AA tap tuning did and what this deserves.
- **Off-screen content cannot be gathered**, so the blur narrows at frame edges.

## Related

- [soft-shadows.md](soft-shadows.md) — the filter this borrows its whole shape
  from, and the plane-prediction surface test it reuses.
- [terrain-cost.md](terrain-cost.md) — the reduced-resolution pond experiment,
  and the ray-guided upsampling result this misread at first.
- [bvh-traversal.md](bvh-traversal.md) — why the tag-and-subtract split is
  branch-free arithmetic rather than a second traversal.
