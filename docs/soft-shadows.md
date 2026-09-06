# Contact-hardening soft shadows

**Status:** implemented behind `RAYTRACER_SOFT_SHADOWS=1`, off by default. With
the flag clear the renderer is bit-identical to before on every view tested.
**Cost:** +2.3% on outdoors-night-villa, +5.1% on the office server room,
+5.4% on the office atrium.
**Rays:** none. The shadow ray count is unchanged.

```
RAYTRACER_SOFT_SHADOWS=1 go run .
```

---

## The idea

A shadow's penumbra width is fixed by geometry: a light of radius `r`, an
occluder at distance `t` from the receiver and the light at `ldist` project a
half-shadow of `r*t/(ldist-t)` onto the receiver. **One blocker distance sizes it
exactly.** An occluder touching the surface gives `t = 0` and a hard edge; the
same occluder up near the light throws a wide soft one.

That distance was already being computed and thrown away. `blocker_bvh_any_hit`
compared a candidate's `t` against the segment length to answer a boolean; it now
returns the `t`. Nothing extra is traced to obtain it.

Knowing the width is not the same as drawing it, because a penumbra is a
*neighbourhood* property — a pixel cannot tell how far it sits from the shadow's
edge by looking only at itself. So shading writes what it knows into a per-pixel
`ShadowAux` record — `full`, what the lights would contribute unoccluded; `frac`,
the share of it something is blocking; and the penumbra width in pixels — and a
`shadow_soften` pass widens the shadow by averaging the *exact* visibilities
around each pixel.

The key property: **there is nothing stochastic anywhere.** Every value the
filter gathers was resolved by a real shadow ray. Widening is a filter, not a
reconstruction, so it needs no denoiser, no temporal history, and no extra
samples — and the radius can be as large as you like for the same price.

## Why not just trace more shadow rays

That was tried first and is recorded here because the failure is instructive.

Jittering the shadow ray over the light's disc is the textbook approach. It has
two problems that are not fixable by tuning:

- **`n` samples give only `n+1` distinct visibility levels.** A penumbra spread
  over more pixels than that renders as a few flat steps, which the Bayer dither
  then breaks into a diagonal hatch. Capping the drawn width to what the samples
  could resolve fixed the hatch and made the shadows barely soft.
- **Jittered shadow rays cost about 3.5x a coherent one.** Measured: one extra
  sample adds ~8 ms a frame on outdoors-night-villa, against a *total* current
  shadow cost of 2.3 ms for 767k rays — 3 ns/ray coherent versus ~10.7 ns/ray
  jittered. Neighbouring pixels' shadow rays toward the same light are nearly
  parallel and hit the same blocker fast; jittered ones diverge, and those that
  slip past the occluder traverse the whole BVH instead of terminating early.

Measured cost of the sampled version, with an adaptive sample count and a
dominance gate already applied:

| | villa | office |
|---|---|---|
| sampled penumbra (up to 4 extra rays per light) | +3.7% | **+58%** |
| **screen-space filter (0 extra rays)** | **+0.9%** | **+1.8%** |

The office is the interesting column: its light grid puts several shadow-casting
lights on most pixels, so the sampled version multiplied its extra rays by all of
them. The filter is per pixel, not per light, so it does not care.

The filter is also the *better-looking* of the two — smooth instead of stepped,
and soft to 14 px instead of the 5 px the samples could resolve.

## What the filter does

`shadow_soften` runs one thread per pixel, in main's own pass (consecutive
dispatches inside a compute pass see each other's storage writes), before AA
classification so edge detection sees the softened result.

It runs as **four dispatches**: `shadow_radius_h`, `shadow_radius_v`,
`shadow_soften_h`, `shadow_soften_v`. The radius passes build the gather radius as
a field; the soften passes smooth that field and gather the fraction with it. Why
the radius needs a pass of its own is below, under "The radius must be a field".

Each half is **separable**: a horizontal pass then a vertical one, 61 taps each. That is
what makes the radius affordable. A disc needs four times the taps for twice the
reach, so a single 2D pass could only pay for about 14 px — and a penumbra that
should span 40 px, blurred over 14, still reads as a hard edge with a faint
gradient, at a tap density (~0.05 taps/px²) low enough to be visibly noisy.
Separating costs 2N taps instead of N² for the same reach. Maximum is separable
over a rectangle, so the row maxima the first pass writes complete exactly.

Each pass runs two loops:

1. **Find the radius**, as a maximum of `pen_px` over the neighbours whose own
   width reaches this pixel (`|d| < min(pen, max)`). A lit pixel has no penumbra
   of its own and must borrow one from the shadow it sits beside, which is what
   lets the gradient spread *outward* across the hard edge as well as inward.
2. **Average every same-surface neighbour inside that radius**, lit ones
   included, weighted by distance alone (`1 - (d/r)²`).

Then it recombines: the pixel is blocking `frac` of its light and should be
blocking `frac_soft`, so the pass adds `(frac - frac_soft) * full`.

**A borrowed width is taken whole, gated by reach — not scaled down by
distance.** Scaling (`pen * (1 - d/max)`) looks like the gentler choice and is
the one that erodes the umbra. A lit pixel `d` away shrinks its radius to
`pen - d` and then gathers over `pen - d`, so it reaches the shadow only while
`d < pen/2`, and with a kernel weight that is already near zero when it gets
there. The shadowed side meanwhile keeps its full `pen` and reaches `pen` into
the light. The two sides exchange unequal amounts, and the shadow visibly shrinks
instead of softening — measured on the test scene as a lit side that darkened by
5 levels where the shadow side brightened by 160.

A whole width is symmetric: every pixel in the penumbra gathers with the same
radius, so what one side gives the other receives. It also needs no edge
treatment, which is what the taper was for. At the rim of the footprint the
gather only just reaches the shadow, the `1 - u²` weight there is zero, and the
correction goes to zero on its own.

**Both loops must accept lit taps.** An earlier version gave each tap a reach of
its own and skipped taps beyond it, which looks equivalent and is not: a lit
tap's penumbra width is zero, so every one of them was silently dropped and a lit
pixel averaged nothing but shadow. Its visibility then went as `1/(1+k)` in the
number of shadowed taps, and those 1, 0.5, 0.33 steps drew bands parallel to
every shadow. The per-tap form also made the blur snap between widths under
camera motion, since a tap either qualified or did not.

**Surface continuity is a normal agreement plus a plane *prediction*.** Extend
this pixel's surface as a plane and ask where the tap's own view ray would cross
it: for a plane through `P0 = cam + v0*depth0` with normal `n`, that is
`depth0 * (n·v0) / (n·v_tap)`. A tap on the same surface matches within a hair; a
tap on a silhouette does not, at any obliquity.

The first version instead *tolerated* a depth difference of
`px_world * rad * slope`, with `slope` growing as `1/(n·v)` to allow for surfaces
tilted away from the view. That allowance is unbounded in the wrong way: at
grazing incidence with the maximum radius it reaches **43% of the depth** — tens
of world units on a long floor — so the test accepted almost anything and shadows
bled straight through walls. Predicting the plane removes the dependence on
obliquity entirely, so the tolerance stays tight at any angle. It costs one ray
direction per tap, which is most of the difference between +4.1% and +8.6% on the
atrium.

**The surface test deliberately does not use the AA fingerprint.** That
fingerprint is per *primitive*, and as
[megakernel-optimization.md](megakernel-optimization.md) notes, scene geometry is
authored as many small boxes — a rack, a wall of panels — so a continuous surface
is covered in fingerprint boundaries. Gating the gather on it chopped the blur
along every seam, which read as noise, and flipped it on and off as those seams
moved under the camera. Normal and depth describe the surface itself and do not
care where one primitive ends.

**The comb is fixed, not jittered, and `TAPS` and `MAX_PX` are a matched pair.**
21 taps is two more than twice the 10 px cap, so the comb steps at most one pixel
at the widest radius: it never skips a pixel and has nothing to alias against.
Raise `MAX_PX` without raising `TAPS` and that stops being true.

An earlier version jittered the offsets per pixel to break up ridges, which was
treating the symptom. Neighbouring pixels then averaged *different tap sets*, and
every penumbra carried noise — measured at 3.8 RMS display levels against a 1.38
dither floor, worst on grazing surfaces where a shadow edge crosses the most
geometry per pixel. This is the "noisy soft edges" that survived several rounds of
other fixes. Deleting the jitter dropped it to 1.39, which is the floor exactly.

**Tap offsets round; they must not truncate.** `i32(o)` truncates toward zero, so
every offset in `(-1, 1)` collapses onto the centre pixel and triple-weights it,
asymmetrically. Rounding alone took the noise from 3.8 to 3.3 before the jitter
came out.

## Filter a fraction, never radiance

`ShadowAux` carries `frac`, the fraction of this point's direct light that
something is blocking, per colour channel — **not** the blocked radiance itself.
The distinction is not cosmetic. Blocked radiance is `(1 - vis) x albedo x light`,
so a bright texel in shadow is missing more light than the dark texel beside it,
and blurring that quantity and re-subtracting the pixel's own value **is an
unsharp mask on the texture**. Every shadowed region blew out: dark tones far too
dark, bright tones far too bright. Measured on the pixels the filter touches, it
raised texture-scale contrast (mean 5x5 stdev) 62-104% *above* the unfiltered
baseline:

| office view | unfiltered | blurring radiance | blurring the fraction |
|---|---|---|---|
| yaw 90 | 5.03 | 8.14 | 4.72 |
| yaw 180 | 4.80 | 7.35 | 3.96 |
| yaw 270 | 3.98 | 8.13 | 3.93 |

A ratio has no such problem, because albedo, AO and throughput are all in both
the numerator and the denominator and cancel exactly. What is left is pure
visibility, which is the only thing a neighbour can meaningfully tell this pixel.
Converting back to radiance at the end multiplies by **this** pixel's own `full`,
so the correction stays textured like the surface it lands on rather than like
its neighbours.

That is also what bounds the result. `frac` is in [0, 1] and `full` is what the
pixel would have received unoccluded, so a brightened pixel lands at most on
fully lit and a darkened one at least on fully shadowed — no halo is
representable. The design this replaced scaled the two directions by two
*unrelated* vectors, the pixel's own missing light one way and its total
deposited light the other, and that mismatch is where the halo came from.

Per channel rather than one scalar for two reasons: a lit pixel beside a shadow
cast by a warm light has to lose warm light rather than grey, and a scalar
occlusion is not a partition of unity across several lights.

## One occlusion channel, and why two failed

`ShadowAux` carries a single occlusion channel. A two-channel split by penumbra
width was built — so two shadows of different softness crossing could each be
filtered with their own radius — and reverted. It is recorded because both
failures are subtle and both looked correct when written.

**A varying split is not a partition of unity.** With `occ_a = occ*(1-u)` and
`occ_b = occ*u`, where `u` varies per pixel with the blocker distance, filtering
the two parts with *different* radii does not recombine to `occ`. On a wall whose
light is fully occluded, `occ` is a constant 1 while `u` still varies across the
surface — so the reconstruction invented a signal out of a uniform field. The
visible result was a shadow drawn on a wall by a light that wall completely
blocks, which is what made the bug identifiable at all.

**Normalising by filtered weights fixes that and kills the penumbra.** Carrying
each channel's share of the light through the same kernel and dividing does
restore the partition exactly. But a *lit* pixel has no wide-channel light, so
its weight there is zero and it is invisible to that channel. The wide channel
then averages only shadowed pixels, where occlusion and weight are the same
field, so the ratio is identically 1 and no gradient can form. Reweighting cannot
fix it either: with asymmetric radii any correction is approximate, and the
approximation reappears as a different artifact.

With one channel `frac` is filtered directly, a uniform field stays uniform, and
the only cost is that two crossing shadows of different softness share one
radius — a seam where they overlap, which is milder than either failure above.

## Sum over lights, never pick one

`ShadowAux` sums over *all* lights rather than recording a dominant one:
`full` is what every light would deliver unoccluded, and `frac` is the share of
it that something is blocking. Their ratio means exactly the same thing at every
pixel, whichever lights happen to be involved.

Picking a dominant light was the original design, and it is worth recording why
it failed, because each repair looked correct and made things worse:

- Which light "wins" **flips between neighbouring pixels** wherever two are
  comparably bright. Every downstream rule then behaves differently on either
  side of the flip. That is what produced speckle along shadow edges, and shadows
  where only part of the area was softened while the rest stayed hard.
- Tagging each record with a `light_id` and refusing to mix them removed the
  cross-light averaging but not the flipping, and **made every shadow hard**: a
  shadowed pixel rejected all the lit neighbours it needed for the "fully lit"
  end of the gradient.
- Ranking blocked lights above unblocked ones — which looks obviously right,
  since only a blocked light has a shadow — broke it from the other side. The
  filter works by a shadowed pixel and its lit neighbour *agreeing* on what to
  measure, and that rule guarantees they disagree.

A sum has no selection, so it has no discontinuity, and the filter needs no
classification beyond "same surface". It is also cheaper: the office atrium went
from +13.6% to +2.7% once the per-light bookkeeping and its branches came out.

**A campfire needs its contribution measured, not estimated.** Its core occlusion
test short-circuits all three sub-lights at once, so on a core-blocked pixel none
of them ever record and its shadow stayed hard. That path now evaluates each
sub-light with `shadow = false`, which returns the unoccluded contribution
without tracing a ray and without recording anything itself.

An earlier version guessed at it — one sub-light scaled by three — and guessed
high. The filter adds back `full × Δfrac`, so a contribution larger than the
light actually present injects radiance that was never there: campfire shadows
*brightened* their surroundings instead of darkening them. Whatever goes into
`full` has to be exactly what the pixel would have received unoccluded.

Two accounting details that are easy to miss:

- **A glossy surface keeps only `(1 - refl)` of its direct lighting**, the rest
  being the reflected lobe, so the record is scaled to match. Otherwise the
  filter adds back light the pixel never had — a systematic error on every
  reflective floor.
- **Both recombinations clamp to zero.** The delta is negative wherever the
  filter removes light, and in `supersample_edge` the tap traced a *different*
  sub-pixel that may have landed on darker geometry. That one produced isolated
  black pixels along exactly the high-contrast edges AA targets.

## Handing the correction to the AA tap

`aa_resolve` runs *after* both soften passes and overwrites the softened pixel
with `(center + tap) / 2`, where the tap is a freshly traced sub-pixel carrying a
**hard** shadow. It therefore has to be given the same treatment as the centre,
or AA puts a hard edge straight back into the pixels the filter just smoothed.

The trap is what "the same treatment" means. The first version added the centre's
finished **radiance delta** to the tap. That is wrong exactly where it is used:
AA fires when a sub-pixel lands on *different geometry* — that is the definition
of the edge it is resolving — so the correction pushed the centre's surface,
albedo and lighting onto a sample that might be on another step of a staircase
altogether. On the atrium stairs, where every shadow edge crosses a step nosing,
this read as the softening being undone: ragged, re-hardened shadow borders,
visible only with AA on.

The fix is to hand over the filter's **conclusion** rather than its arithmetic.
The conclusion is a target visibility — "a pixel here should be blocking this
fraction of its light". It is a property of the neighbourhood, it is smooth by
construction, and it therefore transfers one sub-pixel over. So `shadow_soften_v`
stores `mix(me.frac, frac_soft, fade)` in the `frac` slot instead of the delta,
and the tap evaluates the correction against its **own** occlusion and its **own**
unoccluded light, both of which `ray_color` already computed and used to discard:

```wgsl
extra = max(extra + (res.sh.frac - aux.frac) * min(res.sh.full, aux.full), vec3(0.0));
```

**Scaled by the lesser of the two unoccluded lights, not the tap's own.** The
target fraction is a statement about the *centre's* neighbourhood, so the
correction it licenses is bounded by the light the centre actually has. Scaling by
the tap's own light unbounded put bright specks along every high-contrast
silhouette: AA fires precisely where the tap lands on different geometry, and a
tap that is fully blocked (frac 1) beside a centre that is lit (target 0) then
receives `+1 x its own full light` — un-shadowing a bright surface in a single
pixel. Against an open fire that is a white dot on the edge of the geometry.

The alternative — refusing to correct a tap that fails a same-surface test —
removes the specks equally well and throws the whole fix away with them, because
different-geometry taps are the entire reason this correction exists. Measured on
the fireplace frame and the stair frame together:

| | isolated bright px | stair deviation from no-AA |
|---|---|---|
| previous correction | 41 | 0.925 |
| tap's own light | 63 | 0.571 |
| same-surface gate | 41 | 0.923 |
| **lesser of the two** | **40** | **0.784** |

`soft_applied` flags whether the vertical pass actually wrote a target, because
an untouched pixel's `frac` still looks like one, and correcting a tap the filter
never touched is its own artifact.

Measured on the stair frame: the AA image's mean distance from the no-AA
(purely softened) reference over the stair-shadow region falls from **0.92 to
0.57 display levels**, a 38% reduction, and the non-AA path is unchanged — one
pixel of 540,000 differs by one level, from `mix()` reassociating an FMA. Those
pixel numbers stand; the "cost identical" figure originally reported alongside
them does not, for the reason below.

**Why not just reorder the passes?** Running AA before the soften passes would
make softening uniform and unconditional. It does not work as-is: `aa_resolve`
writes only the display buffer, not `hdr_pixels`, so the soften pass would build
on the un-antialiased value and discard AA's work. Making AA write `hdr_pixels`
too is a viable alternative design, but it also feeds hard-shadow edges into
`analyze_edge`, which classifies partly on luminance — AA would then spend its
budget antialiasing shadow edges that the filter is about to turn into gradients
anyway.

## Tested and ruled out: any-hit vs nearest blocker

`w_pen = r*t/(ldist-t)` is monotonic in `t_block`, and `t_block` comes from an
any-hit that returns *a* blocker, not necessarily the nearest. That biases the
width wide, and — worse in principle — `bvh_child_push` orders children by AABB
*entry*, which is not order by hit distance, so which blocker is found first
could flip between adjacent pixels as the ray direction shifts, taking the
penumbra width with it. A genuinely plausible source of instability.

Measured, with `SHADOW_NEAREST_BLOCKER = true` forcing a full nearest traversal:

| soft shadows on | pixels differing | beyond 8 levels | max delta |
|---|---|---|---|
| outdoors-night-villa | 738 (0.45%) | **7** (0.004%) | 38 |
| office-sunset | **0** (0.00%) | 0 | 0 |

Essentially nothing, and it costs **+9.6%** on the villa and **+50.4%** on the
office server room, because a nearest traversal cannot exit early.

Two reasons the bias does not materialise here:

- **`BVHLeafSize` is 1** (see [megakernel-optimization.md](megakernel-optimization.md)),
  so a leaf holds a single primitive. The "whichever of the two hits first" case
  cannot occur at all.
- **Front-to-back by AABB entry is near-exact in practice.** With single-primitive
  leaves the only remaining source of disagreement is a node whose box starts
  nearer while its primitive lies farther, and the measurement bounds how often
  that changes an output pixel: seven of 163,840.

The switch is kept as a compile-time constant. It folds away at `false`, so it
costs nothing to leave in, and it lets the test be repeated if the BVH ever grows
wider leaves — at which point the intra-leaf case returns and this conclusion
should be re-measured rather than assumed.

## Light leaking through geometry

Two mechanisms, one guarded and one inherent.

**Guarded: a blocker inside the light.** `w_pen = r*t/(ldist-t)` diverges as the
blocker approaches the light. Geometrically that is true — a light barely peeking
past an occluder does cast an enormous penumbra — but in a scene it means the
light is *enclosed*: mounted against the wall it lights, or inside a fixture
housing. Blurring that shadow over the maximum radius pushes the light straight
through the geometry enclosing it. A blocker closer to the light than the light's
own radius is now treated as a hard edge. The blocked light is still recorded, so the
visibility arithmetic stays correct; only the softening is withheld.

**Inherent: coplanar geometry across a thin occluder.** The plane prediction
rejects anything off this pixel's surface, but two parts of the *same* plane
separated by a thin wall — a floor continuing on both sides of a partition — are
genuinely coplanar, and no depth or normal test can tell them apart. A gather of
up to `SHADOW_SOFTEN_MAX_PX` carries shadow across. This is the classic failure
of every screen-space shadow filter; the only lever is a smaller radius.

**If you see light where a wall should block it, check with the flag off first.**
The pass only ever adds `full * (frac - frac_soft)`, bounded by the light the
pixel's own shadow rays said was blocked, so it cannot invent light that the hard
shadow did not already compute — but it can move it up to a radius sideways.

## The bright halo around a light pool

Reported as "the brightest point of the blur is brighter than the projected light
itself". It is not overshoot — the filter cannot produce it. `frac_soft` is a
convex combination of values in [0, 1], so a pixel can never be driven brighter
than having its own blocked light fully restored. A luminance profile straight
through the worst-brightened pixel confirms a monotonic ramp:

```
x      475   487   491   495   499   503   507   511
hard  27.3  28.0  28.3  28.1  28.3  28.0  93.9  93.9
soft  27.3  28.0  30.5  37.4  46.7  59.1  71.1  81.6
```

What actually happens is the *lit* side being eroded: the pool edge drops from
93.9 to 71.1. A blur pulls a small feature's peak down while lifting its
surroundings, so the ring reads as brighter than the pool it encircles. The
brightening is real and correct; the pool dimming beside it is what makes it look
wrong.

It scales cleanly with the gather radius, and **not** with the assumed light size
— `pen` is pegged at the cap either way, so `PENUMBRA_LIGHT_RADIUS` 0.35 -> 0.12
changed pool dimming by 0.2 levels:

| `SHADOW_SOFTEN_MAX_PX` | mean pool dimming | worst |
|---|---|---|
| 18 | 18.9 levels | 47.5 |
| 10 | 11.3 | 42.0 |
| 6 | 6.4 | 36.8 |
| 4 | 3.7 | 31.1 |

Set to **10** as the compromise: half the erosion of 18, still visibly soft. This
is the knob to turn if pools look washed out (lower) or edges look hard (higher).
Note it trades directly against penumbra width — there is no setting that gives
wide penumbrae *and* leaves small light pools untouched, because a wide blur
erodes small features by construction.

`pen_px` is clamped before the reach test, and a *shadowed* pixel uses its own
width rather than borrowing a neighbour's. Both matter for a different symptom:
two occluders of the *same* light at different distances — a far wall and a near
server rack — have very different widths, and letting the wall's 15 px win inside
the rack's shadow blurs the rack's crisp edge three times too wide, which reads as
brighter.

**What finally fixed it was the distance taper coming out**, which is a different
fault from the one this section diagnoses. The taper made the correction
one-sided: the shadow side brightened at full radius while the lit side, having
shrunk its own radius to `pen - d`, barely darkened at all. The umbra therefore
*receded* rather than the edge softening, which is erosion at the shadow's expense
rather than the pool's. With a whole borrowed width the profile is two-sided —
here across the test scene's tall post, where the lit side gives up what the
shadow side receives, and no floor pixel anywhere in the frame ends up brighter
than it was:

```
x     596   600   604   608 | 612   616   620   624   628
hard  227   226   226   226 |  27    27    27    27    27
soft  226   223   217   203 | 172   130    80    44    29
       -1    -3    -9   -23 | +144  +102   +53   +17    +2
```

## Limitations

- **`PENUMBRA_LIGHT_RADIUS` (0.05 m) is only the *default*.** It applies to
  lights that do not declare a size of their own; `radius` on `[[light]]` and on
  `[[light_flickering]]` overrides it per source. It remains the knob for overall
  softness — see "Tuning softness" below.

  A caution when authoring: `radius` was long documented as "informational, not
  used by the renderer", so several scenes carry values chosen for other reasons
  — `art-deco-uplight.toml` says 0.5, ten times the default, and
  `exit-button.toml` says 2. Those are now real emitter sizes and cast very wide
  penumbrae. Penumbra width is linear in the number, so a lamp whose shadow looks
  washed out is usually a lamp whose radius is describing its shade rather than
  its bulb.
- **Screen-space, so it only knows about what is on screen.** An occluder outside
  the frustum still casts a correct hard shadow — the ray tracing is unchanged —
  but its penumbra cannot widen from off-screen neighbours.
- **`SHADOW_SOFTEN_MAX_PX` (30 px) is an artistic limit, not a physical one.**
  The true penumbra of a 0.35 m-radius light with the occluder near it runs to
  40 px and beyond, and drawing it in full dissolves small shadows entirely —
  correct for a light that size, but it reads as washed out. 30 px keeps a dark
  core under a broad gradient. Raising it softens everything and eventually
  erases small umbrae; lowering it brings back visible hard edges. This and
  `PENUMBRA_LIGHT_RADIUS` are the two knobs worth turning if the look is off.
  **Raise `SHADOW_SOFTEN_TAPS` with it** — see the comb note above.
- **A residual step at that radius.** Beyond it a pixel is left alone, so the
  outward spread ends. Small, since only a tap or two out there is shadowed.
- **One visibility channel for all lights.** Two lights casting crossing shadows
  are blurred with a single radius — the contribution-weighted average of theirs
  — so the wider penumbra is drawn slightly too tight and the narrower slightly
  too wide. Smooth and stable, but not per-light correct.
- **The fraction is per colour, not per light.** Two lights of the same colour
  casting crossing shadows are indistinguishable in the record, so they share a
  radius. Differently coloured ones separate for free, which is a happy accident
  of the representation rather than a design.

## Tuning softness

**`PENUMBRA_LIGHT_RADIUS` in `shade.wesl` is the knob**, and the shader relinks
from the modules at startup, so editing it needs no Go rebuild. Penumbra width is
linear in it.

It is the right knob rather than `SHADOW_SOFTEN_MAX_PX` because lowering it does
two things at once: shadows get tighter, *and* the contact-hardening gradient
gets more legible, because more of the width range lands below `shadow_radius()`'s
knee where it is reproduced faithfully instead of compressed. Measured on one
office frame, over the wall a cone lamp casts onto — "spread" is the ratio of
gather radius between the near and far ends of that single shadow, so 1.00x means
the whole shadow is blurred identically:

| radius | median blur | spread |
|---|---|---|
| 0.35 | 27 px | 1.10x |
| 0.15 | 23 | 1.22x |
| **0.05** | **16** | **1.52x** |
| 0.03 | 12 | 1.72x |
| 0.02 | 9 | 1.90x |

## Width is compressed, never clipped

`min(pen, MAX)` is the obvious way to bound the gather radius and it destroys the
effect it is bounding. Real penumbrae far exceed any affordable cap: on that same
office frame the median blocked pixel wanted **164 px** of penumbra and **85%
wanted more than the 10 px cap** that used to be here, so nearly every shadow in
the frame clamped to the same radius. Contact hardening was computed correctly
and then thrown away — a cone lamp whose shadow genuinely runs from ~20 px of
penumbra near the shade to ~250 px across the room was drawn with one uniform
blur, end to end, and reported as "uniformly blurred".

`shadow_radius()` is the same problem as tonemapping radiance and takes the same
answer: identity below a knee, then a shoulder that compresses instead of
clipping. The hyperbola has slope 1 at the knee so the join is smooth, and
approaches `MAX` without reaching it, so two very wide penumbrae still order
correctly instead of collapsing onto one value. Below `SHADOW_SOFTEN_KNEE_PX`
(4 px) the width is exact, which is the range contact hardening lives in — an
occluder touching a surface must stay a hard edge.

Widening the cap to make room is not free, though it is cheap for what it buys:
10 -> 30 px with the tap count tripled to match (21 -> 61) costs **0.7 ms of 9.5**
on the server room, isolated by reverting just those two constants.

An earlier revision of this document claimed it measured *identically*. That was
wrong, and the way it was wrong is worth recording — see "Measuring a shader
change" below.

## The radius must be a field, and a smooth one

Each pixel gathers with a single radius, taken as a **maximum** over the
neighbours whose own penumbra reaches it. A maximum is a dilation: it produces a
plateau at the full width across a shadow's footprint, then drops to the local
value in a single pixel at the rim.

That cliff is invisible on the shadow that *produced* it — the gather's parabola
weight is already zero out there, which is why the reach gate looked safe. It is
very visible on any **other** shadow crossing the same rim. Wherever a soft and a
hard shadow share a surface, the wide one imposes its radius on the narrow one
everywhere it reaches, and at the rim the narrow shadow snaps back to being
blurred at its own width. The result is a seam along an invisible line, reported
as "the edge of the blur filter between them".

Instrumenting the radius on the reported frame shows it directly — adjacent
samples 20 px apart, on one flat wall:

```
y234:  5.9  7.5 13.6 13.6 13.6  5.9  5.7  5.7  5.7  5.2 ...
y252:  7.1  7.1 12.7 12.7 12.7 12.7 12.7  5.7  5.7  5.4 ...
```

The fix is to **blur the radius after the maximum**. A blur leaves the interior of
a plateau untouched, so pixels beside a shadow keep the full width and the
exchange between the two sides stays symmetric — this is what a distance taper
cannot do, and why tapering erodes umbrae. Only the rim becomes a gradient.

That requires the radius to exist everywhere *before* any fraction is blurred,
which is why the filter is four passes rather than two. It also removes a smaller
inconsistency in the old form, where the row gather used a row-only radius while
the column gather used the full 2D one. `ShadowAux` stays at 64 bytes by reusing
fields as they die: `pen_px` is spent once `radius_h` has read it, so `radius_v`
parks the 2D maximum there; `r_h` is spent once `radius_v` has read it, so
`soften_h` parks the smoothed radius there for `soften_v`. No pass writes a field
another invocation of that same pass reads.

**The smoothing reach is proportional to the radius being smoothed.** A fixed
reach wider than the plateau averages it with the zeros around it and shrinks it:
at a fixed 9 px, every small penumbra in the contact test fell under the 1 px
cutoff and the scene came back with hard shadows. Scaling the reach to the local
radius leaves small features intact and still spreads the large steps.

Measured on the reported frame, over the seam region: the worst second difference
falls from **13.4 to 5.6** display levels, against 29.0 for the unfiltered
reference. The contact-hardening scene moves by 1.7% of pixels; a fixed reach
moved 3.2% and looked worse. Cost is **+0.5 ms of 9.0** on the server room, and
nothing measurable on the villa.

**What this does not fix.** Two shadows of different softness still share one
radius wherever their footprints overlap — the seam is now a gradient rather than
a step, not a per-source result. Getting that right needs per-source filtering,
which "One occlusion channel, and why two failed" above records as tried and
reverted for an unrelated reason.

## Tried and reverted: a per-band radius

The single shared gather radius is the acknowledged limitation behind several
artifacts, so it was worth building the obvious fix properly: split blocked
lights into bands by penumbra width, give each band its own radius, filter them
independently and sum the corrections.

It was built and it works. The split is per *light*, so the bands partition the
deficit exactly at every pixel — a real partition of unity, which is what the
earlier two-channel attempt lacked. Membership is a smooth function of width, so
a light drifting across a boundary is shared rather than jumping. One gather
serves every band, with taps placed at the widest band's radius and each band
weighting them against its own, so the tap count does not multiply.

**It is still worse, and the reason generalises.** Each band gets its own radius
field, and each radius field has its own footprint boundary where the dilation
runs out. Those boundaries do not coincide. One shared radius draws one seam; N
bands draw N seams. Measured against the single-band build on the same frames:

| | single band | 3 bands |
|---|---|---|
| seam, worst 2nd difference | 5.4 | **12.1** |
| fireplace isolated bright px | 41 | **53** |

The seam metric lands back where it was before any of the seam work. Two bands
were tried first and sat between the two, at 274% against 285% on the hardening
case that motivated it — no separation of the widths that actually collided.

So the shared radius is a real limitation, but per-band radii is not the fix for
it: the artifact it produces scales with the number of bands. A fix would have to
avoid introducing new radius discontinuities, not just reduce how much each one
matters.

## Measuring a shader change

**A/B by swapping `modules/*.wesl` and relinking. Swapping `trace_linked.wgsl`
does nothing.** `readLinkedIfCurrent` hashes the modules and compares that to a
stamp inside the linked file; on any mismatch it re-runs `link.sh` and uses the
regenerated output. Timestamps are not consulted, so `touch` does not help, and
neither does building two binaries — the shader is read from disk at run time.

This invalidated several timings in an earlier revision of this document. The
failure is silent and it always fails the same way: both arms of the A/B run
whatever the modules currently say, so the answer comes back "no difference",
which is exactly the result that gets believed and shipped. Grepping the linked
file is not a check — it passes right up until the renderer overwrites it. The
only reliable check is that the two arms produce *different pixels*; if a change
you know is visible reports zero pixels differing, the swap did not take.

Second, **close the game before measuring**. A running instance contends for the
GPU and inflates times by 30-80% unpredictably; one sweep here reported a 52%
regression that dropped to 3% once the window was closed. Interleave the arms and
take best-of-N regardless.

## Testing

`tmp/perf/penumbra-test.toml` is a purpose-built scene: a bar suspended just
under the light (the widest penumbra geometry, since the width grows as the
occluder approaches the light) plus posts standing on the floor (the contact end,
which must stay sharp where they meet the ground and soften with height). Both
ends of the range are visible in one frame, which is what made each failure above
diagnosable — the office and villa scenes were too dark and too cluttered to
judge a shadow edge in.

## Related

- [megakernel-optimization.md](megakernel-optimization.md) — `SHADOW_SKIP_LEVELS`
  and the display-space reasoning this reuses, plus the adaptive-AA
  classify/resolve split whose shape the soften pass follows.
- [bounce-kernel.md](bounce-kernel.md) — why *relocating* rays into a separate
  kernel does not pay, and why this one does: it removes work rather than moving
  it.
