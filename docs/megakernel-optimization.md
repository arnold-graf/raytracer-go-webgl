# Megakernel Optimization Pass (2026-09)

**Status:** shipped. Four-view mean on `scenes/office-sunset/index.toml` went from
**13.88 ms (72 fps) to 10.57 ms (94.6 fps)**, and adaptive AA now also covers
shadow edges.
**Audience:** anyone tuning the tracer's hot path, or wondering why one of these
constants is set where it is.
**Constraint:** perceptual parity. Byte parity was not required, but every step
below was checked against a recorded reference with `tmp/perf/suite.sh` and, where
the pixel diff was non-trivial, inspected as a magnified crop.

> Supersedes the `SHADOW_SKIP_EPS` section of
> [reflection-optimization.md](reflection-optimization.md). That constant no
> longer exists; see [Shadow gating in display space](#shadow-gating-in-display-space).

---

## Measurement setup

All numbers are from `tmp/perf/suite.sh`, at 512×320, bounce depth 4, adaptive AA
on — matching the app.

Two things about the harness matter for reading any of this:

- **Four yaws, not one.** Scene cost varies about 2× with view direction, and the
  shipping camera at yaw 0 is the *cheapest* one. A change tuned on the default
  view overfits badly.
- **Best of three runs per view.** Run-to-run spread from GPU clock and thermal
  drift measured at roughly ±10%, which is larger than several of the individual
  wins below. A single sample cannot distinguish a real change from noise.

`cmd/gpuprof` gained a `-time` flag in this pass. Without it the animation clock
is pinned at 0, so campfire sub-lights, flames, and water ripples cannot be
profiled or A/B-tested at all — which is how the campfire regression below went
unnoticed.

Per-view final state:

| Yaw | Before | After |
|---|---|---|
| 0 | — | 8.4 ms (119 fps) |
| 90 | — | 11.4 ms (88 fps) |
| 180 | — | 8.3 ms (121 fps) |
| 270 | — | 12.6 ms (79 fps) |
| **mean** | **13.88 ms (72 fps)** | **10.2 ms (~98 fps)** |

---

## Scalar rewrite of `box_holed_nearest`

**−14%, pixel-identical.** The largest single win in the pass, and it had nothing
to do with the algorithm.

`box_holed_nearest` tracked candidate spans in `array<f32, 8>` locals indexed by a
loop variable. A dynamically-indexed array cannot live in registers, so on Apple
GPUs it spills to thread-local memory — and because the megakernel's register
allocation is global, *every pixel of every scene* paid the occupancy cost, including
the office scene's three holes and scenes with none at all.

Rewriting the span logic as plain scalars removed the arrays. Verified byte-identical
across all four views.

The general lesson, which applies to the rest of this file: in a megakernel, a
local array in a rarely-taken branch is not free. It is a tax on the whole shader.

## Ray segment stack sizing

`MAX_SEGS` went from 8 to 6. `RaySeg` is 44 bytes, so this is the largest
per-thread array in the tracer.

The paper worst case is 1 + 2 + 4 = 7 (glass forking two children at depths 0 and
1), but that bound is unreachable because the Fresnel and `SEG_MIN_CONTRIB` gates
prune a lobe long before the tree fills out. A `PROF_MAX_SEGS` high-water-mark
counter measured every scene in `scenes/` across 24 camera orientations each; the
worst observed was 5, in office-sunset's facing skyway panes. 6 keeps one spare.
`TestRayStackHighWaterMark` guards this.

## Depth-aware glossy reflection cull

`GLOSSY_MIN_CONTRIB` (0.05) prunes faint *glossy diffuse* lobes, which are far
coarser than the mirror and glass lobes governed by `SEG_MIN_CONTRIB` (1/1024).
The office towers use `reflect` 0.15–0.4, so a 0.15 surface reflecting a 0.15
surface still cleared 1/1024 at four bounces — four BVH traversals plus four full
direct-lighting evaluations to move a tonemapped pixel by well under one 8-bit
level.

**The first version of this cull applied at every depth and was a bug.** It
silently erased every `reflect < 0.05` surface in the scene, which is a
scene-authoring trap: the author sets a value and gets nothing back. It is why the
server-room floor briefly needed `reflect = 0.06` to show up at all.

The shipped version is depth-aware: primary hits (`depth == 0`) use the loose
`SEG_MIN_CONTRIB`, so authored sheen always renders however faint, and only
reflections seen *inside another reflection* get the coarse threshold. The error
from dropping a glossy lobe is bounded by `refl × |reflected − local|` — the
surface already contributed `lit × (1 - refl)` — not by the reflected radiance,
which is what makes the coarse threshold safe on bounce paths.

---

## Adaptive AA: classify, then resolve indirectly

The AA pass was the single largest line item, ~5 ms of a 14 ms frame. Almost all
of it was waste, and not from the taps themselves.

Edge pixels are scattered along silhouettes. When one pass did both edge detection
and supersampling, nearly every 64-thread workgroup contained *at least one* edge
pixel, so the whole workgroup was billed for a full extra ray tree while most of
its lanes sat idle.

The pass is now split in three:

1. `main` — one ray per pixel, stashes color, luminance, and a packed primary-hit
   fingerprint.
2. `aa_classify` — one thread per pixel, but only reads neighbor fingerprints and
   luminances. Appends the pixels needing work to `aa_list` and grows an indirect
   dispatch header.
3. `aa_resolve` — 1D over the compacted list, dispatched indirectly, so thread
   count tracks edge count rather than screen area. Every workgroup is full of
   real work.

A task packs into one `u32`: 12 bits of x, 12 of y, and 2 bits per axis for the
sub-pixel tap. The tap is always exactly one of `{0.25, 0.5, 0.75}`, so the
packing is lossless and the split is behavior-preserving.

Two WebGPU details worth knowing if you touch this:

- A buffer cannot be a writable storage binding *and* the indirect source in the
  same pass. The header is written in the classify pass, copied to a dedicated
  indirect-only buffer, and consumed by a second pass.
- Consecutive dispatches within one pass have an implicit barrier, so
  `aa_classify` reliably sees `main`'s writes.

## AA now covers shadow edges

Previously AA fired only when neighbors hit *different primitives*. A shadow does
not change the primary hit, so shadow boundaries were never antialiased — they
stayed visibly stepped.

`aa_axis_tap` now also fires on the second difference of luminance,
`|lo + hi - 2·center|`. Curvature rather than a plain neighbor gap is the right
test: a plain gap fires everywhere along a point light's smooth falloff, where a
supersample only re-averages a value the gradient already got right. Curvature is
near zero along any linear ramp and jumps to the full step height where luminance
*bends*, which is exactly where a hard shadow produces a staircase.

`AA_SHADE_MIN_CURVE` is 10 levels, just above the 8.2-level step of the 15-bit
output, so a staircase that is merely a quantization artifact — where the extra
ray would land in the same bucket — does not qualify.

| `AA_SHADE_MIN_CURVE` | Four-view mean | Note |
|---|---|---|
| off (silhouettes only) | 10.07 ms | shadow edges stay stepped |
| 20.0 | 10.62 ms | hardest shadow edges only |
| **10.0** | **10.85 ms** | shipped |
| 5.0 | 11.28 ms | below one quantization step; pays for dither noise |

Cost is ~0.8 ms. This is affordable *because* of the compaction above: AA cost now
scales with actual edge count instead of per-workgroup waste, so widening the
trigger is much cheaper than it would have been before.

Verified on a magnified crop of the office desk lamp — the stem and base rim
smoothed noticeably, along with the shadow boundary on the desk.

## Rejected: cheaper AA tap rays

**Tried and reverted.** A tap only lands on a flagged edge pixel and is averaged
50/50 with a full-quality center sample, so shading it more cheaply looks like free
money. It saved 0.8 ms of mean.

It is not visually free. Capping tap bounce depth made a tap whose refracted ray
hit the cap fall through to the diffuse path, reporting the glass surface instead
of the scene behind it. The tap then disagreed with the center sample about the
*subject* of the pixel, and the 50/50 blend drew a bright fringe along every
antialiased edge seen through glass — spurious edge detection, exactly where AA is
supposed to be cleaning up.

Restricting the cap to glossy lobes only, leaving mirror and glass chains at full
depth, reduced but did not remove it: a diff still showed lines up to 62 levels
along glass column silhouettes.

The failure is structural, not a mistuning. **Any tap shaded differently from the
center sample disagrees with it precisely on edges**, and a 50/50 blend turns that
disagreement into a visible line. Don't retry this by tweaking thresholds.

---

## Shadow gating in display space

Shadow rays are the largest ray population in an interior view: 499k of 819k total
rays at yaw 270, 87% of them blocked.

The old gate skipped a shadow ray when `tw_peak(tw) × unshadowed_peak` fell below
`SHADOW_SKIP_EPS`, a fixed threshold on **linear radiance**. That is the wrong
space for the decision, in both directions:

- The tonemap compresses highlights, so a contribution that clears a fixed linear
  bar can still be worth a hundredth of a level on a bright surface — a shadow ray
  traced for nothing.
- Gamma strongly amplifies near-black, so against a dark interior the same bar sits
  well *above* one level, and the gate was quietly erasing shadows that were
  genuinely visible.

`SHADOW_SKIP_LEVELS` replaces it, and asks the question the viewer actually cares
about: how far does this light move the output pixel, in 8-bit levels, on top of
the radiance already accumulated at this hit? `display_step_levels` in `math.wesl`
answers it by running the tonemap twice. Two extra tonemap evaluations are trivial
against a BVH traversal.

The tonemap moved from `trace.wesl` to `math.wesl` so the shading code can reach it
without an import cycle.

| `SHADOW_SKIP_LEVELS` | Four-view mean | Note |
|---|---|---|
| 0.5 | 13.10 ms | ~2× the shadow rays of the linear gate; almost nothing qualifies as skippable |
| **4.0** | **10.70 ms** | shipped; half a quantization step |
| 8.0 | 9.95 ms | visibly thins contact shadows under desks |

4.0 is half the 8.2-level step of the 15-bit output, so a skipped shadow stays
inside one quantized bucket. Note that this change is roughly perf-neutral against
the old linear gate — its value is that the shadows it keeps and drops are now the
*right* ones. The 0.5 row is the interesting one: it shows how much visible
shadowing the old fixed linear threshold had been discarding.

Contributions below the bar are still added, so per-light error is bounded by the
threshold and only accumulates across lights that are each individually invisible.

### Campfire regression, and the trap behind it

This change introduced a bug worth recording. The ghost (second glass pane) pass
disabled its shadow rays by passing a deliberately tiny throughput, relying on the
old linear gate to fold every light to "not worth a ray". A display-space gate
correctly rejects that trick — near black, a tiny linear value is very visible — so
the ghost pass started tracing shadow rays.

The fix replaced the magic-constant trick with an explicit `shadow: bool` parameter
on `add_point_light_raw`. In doing so, campfire sub-lights were also passed
`false`, on the mistaken reasoning that the campfire *core* occlusion test above the
loop already stood in for them.

It does not. The core test uses the static `cf.core` position and only decides
whether the whole campfire is occluded. The three sub-lights orbit the core as a
function of `params.time`, and **their individual shadow rays are what animate a
campfire's cast shadows.** With them disabled, campfire lighting still flickered —
sub-light motion changes `N·L` and attenuation either way — but the cast shadows
froze. That partial symptom is what made it easy to miss.

Confirmed with a two-pillar test scene rendered at two clock values: the
fixed-vs-broken difference at a *fixed* time is confined exactly to the two pillar
shadow wedges. Comparing two times is not a valid test on its own, because the
lighting animates in both versions.

---

## Where the remaining time goes

Ablation at yaw 270, the worst view:

| Config | GPU | Note |
|---|---|---|
| all on | 11.8 ms | |
| shadow off | 11.4 ms | shadow rays are numerous but cheap |
| AO off | 11.7 ms | baked volume, essentially free |
| mirror off | 6.5 ms | **reflection transport is the whole remaining cost** |
| all off | 5.2 ms | |

Reflections are the lever. The tracer is traversal-bound, not
intersection-bound: 28.1 BVH node steps per ray for only 2.8 primitive tests, i.e.
about 10 node visits per primitive tested.

Tree *quality* is not the problem, and this has now been checked twice. A
`bvh_analysis_test.go` diagnostic reports an SAH cost of 7.11 with few oversized
primitive AABBs, and an earlier pass measured a balanced tree as 23% *slower*.

That ratio suggested widening leaves. It was tried, and it is wrong — see below.

---

## BVH leaf width: narrower, not wider

The 10:1 ratio of node visits to primitive tests reads like a traversal problem,
so the plan was to widen leaves and trade steps for primitive tests. Leaf width
was raised to 4 by using the two words that padding wastes in the 48-byte node
(an AABB needs only three floats of each `vec4`), which keeps the indices inside
the node — a separate primitive-index array would have added a dependent memory
load to every leaf visit.

Measured at yaw 270:

| `BVHLeafSize` | Nodes | Steps/ray | Prim tests/ray | Best of 5 |
|---|---|---|---|---|
| 1 | 4321 | 30.1 | 1.0 | **12.60 ms** |
| **2** (was shipped) | 2913 | 27.9 | 2.8 | 12.90 ms |
| 3 | 2355 | 26.8 | 4.6 | — |
| 4 | 2039 | 25.7 | 5.1 | 13.00 ms |

Widening loses. Dropping 30% of the nodes removed only 8% of the node visits
while nearly doubling primitive tests. The node visits are not leaf visits — they
are interior slab tests against overlapping boxes, so merging leaves barely
touches them, and every merge forces tests on primitives a slab test would have
rejected for free.

Read the other direction, the same table says the profitable trade is the
opposite one. At leaf size 1 primitive tests fall from 2.8 to 1.0 per ray for 8%
more node visits, worth 2-5% depending on view and **pixel-identical on all four
views** (0.00% differing pixels), since leaf width cannot change which primitive
is nearest. That is what ships. It is unconventional — most BVH guidance says 2
to 4 — because the usual advice assumes cheap triangles, whereas here a
primitive can be a holed box or a torus and a node visit is only a slab test.

Two other findings from this pass:

- **`BVHSAHTraverseCost` is inert.** Both SAH builders add it to every split
  candidate before taking the minimum, so it shifts all candidates equally and
  cannot change which split wins; sweeping it from 1.0 to 0.25 produced
  byte-identical trees. Neither builder ever compares a split against making a
  leaf, so `BVHLeafSize` is the only knob that exists. Tuning the cost would
  require adding that comparison first.
- **Stack headroom is fine.** Leaf size 1 deepens the worst tree in `scenes/`
  from 17 to 18 levels against a stack of 32, so this does not reopen the
  `BVH_STACK_SIZE` question.

---

Also tried and reverted, with no measurable win: shrinking `BVH_STACK_SIZE` from 32
to 20 (measured max depth 17), and stubbing all procedural textures to a flat tint.
Both landed inside run-to-run noise, and the texture stub was slightly *slower* —
register allocation in a megakernel this size responds unpredictably to local
changes, which is the recurring theme of this document.

---

## Harness

Throwaway tooling in `tmp/perf/`, useful if you pick this up:

| Script | Purpose |
|---|---|
| `suite.sh` | four-view benchmark, best-of-N, diffs against recorded references |
| `compare.py` | pixel diff: count differing, count >8 levels, max delta, mean abs error |
| `diffpng.py` | 8× amplified absolute-difference image; makes sub-level changes legible |
| `crop.py` | crop + nearest-neighbor magnify, for judging staircase artifacts |
| `topng.py` | raw RGBA dump to PNG |

The amplified diff is the important one. A 39% "pixels differing" figure sounds
alarming and is usually one-level dither shuffle; the 8× diff image shows
immediately whether a change is scattered noise or a structured artifact along an
edge.
