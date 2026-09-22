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

> **Overview (less GPU jargon):** [adaptive-aa.md](adaptive-aa.md) — what the
> three passes do, how edge detection works, and why AA is extra rays rather
> than a screen filter.

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

> Later measurement puts the *whole* AA pass at 2.4-3.8 ms, 27-30% of the frame,
> and all of it in the resolve dispatch — a tap ray costs about 4x what the same
> ray costs in `main`, mostly to divergence. See
> [aa-tap-tuning.md](aa-tap-tuning.md).

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

> **Scope changed 2026-09-19.** The gate below is sound only where nothing reads
> the per-pixel shadow record, so it no longer applies at the primary diffuse hit
> when the penumbra filter is on — a skipped light leaves no entry in
> `ShadowAux`, and the filter's whole premise is that the record means the same
> thing at neighbouring pixels. Everything else here still stands, and the gate
> still governs every bounce segment, the specular pass and the ghost pass, which
> is five rays in six. See
> [soft-shadows.md](soft-shadows.md#a-skipped-shadow-ray-is-a-skipped-record).

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

The remaining ~5.7 ms at yaw 270 is reflection transport, and cheapening it
*inside* the megakernel keeps losing to occupancy. Taking glossy out into a
separately compiled kernel was the obvious next move; it was built and measured
and it does not pay, and neither does the same treatment applied to glass.

Note also that the ~5.7 ms above is *not* all extractable, which is a trap this
section originally fell into. `mirror off` removes the lobes and everything
downstream of them, including the adaptive-AA work their luminance structure
triggers — and AA cost scales with edge count, so any ablation that flattens the
image flatters itself. Priced by deferral instead of by deletion, the two movable
lobes are 2.30 ms (glossy) and 1.10 ms (glass) at yaw 270. See
[bounce-kernel.md](bounce-kernel.md).

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

> The traversal itself was revisited later and gave up another 10%, by testing
> each node's AABB once instead of twice. Two ways of spending that saving —
> holding the descent in registers, and stacking the entry distance — both cost
> more than they returned. See [bvh-traversal.md](bvh-traversal.md).

---

Also tried and reverted, with no measurable win: shrinking `BVH_STACK_SIZE` from 32
to 20 (measured max depth 17), and stubbing all procedural textures to a flat tint.
Both landed inside run-to-run noise, and the texture stub was slightly *slower* —
register allocation in a megakernel this size responds unpredictably to local
changes, which is the recurring theme of this document.

Skipping instanced geometry on bounce rays was also tried and reverted: it was
~2 ms faster on office-sunset, but reflections missing trees (and anything else
instanced) were visually wrong. Separately compiled bounce kernels were the next
candidate — first for the glossy lobe, then for glass. Both were built, measured
and reverted, and together they close the wavefront family at this scene scale:
extraction pays in proportion to a lobe's *density* while compaction pays in
proportion to its *sparsity*, and no lobe here is both. That doc also carries the
argument against a general wavefront refactor and the fifteen-minute
resolution-scaling test that would say when it stops applying — see
[bounce-kernel.md](bounce-kernel.md).

---

## Second pass (2026-09-19): occupancy, and a record that cost more than the shadows

Measured on `scenes/office-sunset/index.toml` at the atrium view — `-cam-x 44.9
-cam-y 201.3 -cam-z 33.1 -yaw-deg 91.3 -pitch-deg -0.52` — 512×320, depth 4, AA
on. **Absolute numbers here are not comparable to the four-view mean at the top
of this document**: the scene has grown a glass wall, a skyway and a good deal of
furniture since, and this is one view rather than four. Read the deltas.

**18.4 ms → 15.4 ms is available; 18.4 → 18.0 is what preserves the image.**

### Landed: the shadow record is one hit per pixel, not six

The penumbra filter reads `ShadowAux` at the primary diffuse hit and nowhere
else. Every bounce segment, the specular pass and the ghost pass reset those
accumulators and never snapshot them. A fix for a penumbra artifact had disarmed
the display-level shadow gate for the whole frame to keep the record consistent —
but the frame traces 6.2 segments per pixel and records one of them, so five in
six of the extra shadow rays bought nothing a pixel could show.

Scoping it with a `record` argument on `shade_diffuse` took shadow rays from
1.06M to 605k and the frame from 22.4 ms to 19.7. `sh_record` also short-circuits
`shadow_aux_note`, so a discarded record costs no `pow()` either. Full reasoning
in [soft-shadows.md](soft-shadows.md#a-skipped-shadow-ray-is-a-skipped-record).

### Landed: BVH traversal stacks were sized 32 for an 18-level tree

19.7 ms → 18.4, **frame-for-frame identical output**. The deepest tree in all of
`scenes/` is 18 levels, so a DFS that pushes both children needs 19 entries. The
constant was 32 for no recorded reason. See
[bvh-traversal.md](bvh-traversal.md#stack-size-is-occupancy-not-safety-margin).

This is the same mechanism as the `box_holed_nearest` rewrite at the top of this
document: a dynamically indexed local array is thread scratch on Metal and pins
the kernel's per-thread allocation, charged to every ray in every scene. It is
worth going looking for others — but note that the knee is sharp. 32 → 24 bought
1.4 ms; 24 → 20 bought a further 0.2. Past the knee there is nothing there.

### Rejected, with numbers

Everything below was implemented and measured. None of it landed.

| Candidate | Result |
|---|---|
| Narrow the penumbra filter's tap loads — `shadow_tap` returns a 192-byte `ShadowAux` and a gather uses ~24 bytes of it, 61 taps per pixel | **Worse**: 18.6 atrium, 18.3 villa. The compiler was already batching the struct load; per-field reads broke coalescing |
| A separate, smaller stack for nested BLAS traversals (template trees are 6 levels deep and were getting 24) | Neutral — the compiler already reuses that scratch across the call boundary |
| `MAX_SEGS` 4 / 6 / 8 / 12 | 19.7 / 19.9 / 19.9 / 20.1 ms. The ray-tree stack is not a factor; high-water is 5 |
| Remove every profile counter from the hot loop | **Worse**: villa 17.0 → 18.1 ms |
| Light grid `cellsPerLight` 8 → 16 → 32 | 18.3 / 18.3 / 18.3 atrium; villa *worse* at 32. Already tuned |
| Terrain mip stack (12 parallel arrays × 12 entries) | Correctly sized: the deepest terrain in `scenes/` is 11 levels (island) |
| Fold `albedo + spec` into `LIGHT_CULL_EPS` | −2% villa, consistent — but moved **10.8% of the night villa by up to 49 levels**. On a dark surface the threshold is absolute and the pixel is dark too, so scaling it by a small albedo culls light the eye still sees |
| The same, restricted to the albedo-zero specular pass where it is provably safe | Neutral. The 0.9 ms it was chasing is the `shade_specular` *loop*, not the per-light work |
| Thin-glass ghost off | 0.1 ms |
| AA taps declining to trace a glossy lobe they will discard (`aa_tap_same_lobe` is decidable at the hit) | Correct, and neutral: too few taps land on a held-out lobe |

### Still on the table, priced

These work. They cost pixels, so they are a judgement call rather than an
optimization:

| Change | Frame | What it costs |
|---|---|---|
| glass specular at depth 0 only | 18.3 → **17.4** (−5%) | highlights on glass seen through glass |
| `AA_SHADE_MIN_CURVE` 10 → 20 | 18.4 → **16.2** (−12%) | 0.6–1.6% of pixels, *all* ≥8 levels, max 132: aliasing returns on shadow terminators |
| `AA_SHADE_MIN_CURVE` 10 → 40 | 18.4 → **15.4** (−16%) | more of the same |
| bounce depth 4 → 3 | 18.4 → **15.7** (−15%) | one less glass/reflection bounce |

Adaptive AA is still the largest single block — 4.9 ms of 18.2 for the 16.4% of
pixels it supersamples, 15.1% of them flagged by the curvature detector rather
than by silhouettes. Nothing that cuts the task count survived a pixel diff.

### Two traps this pass fell into

Both produced a confident wrong answer, and both are cheap to repeat.

**A workgroup size lives in three places.** `AA_RESOLVE_WG` appeared to take the
frame from 18.2 ms to 15.3 at 128. It had only been raised in two of the three:
the shader still declared `@workgroup_size(32)` while the host dispatched
`ceil(N/128)` groups, so three quarters of the AA tasks were never run. The frame
was faster because it was doing less. Correctly matched, 128 is neutral. Any
constant duplicated between WGSL and Go — this one, `BVHStackSize` — can fail
this way, and it fails *fast*, which is exactly what a speedup looks like.

**A finding is only as good as the build it was measured on.** Specialization was
recorded last round as 2% *slower* on office-sunset, which would have been worth
chasing. Re-measured after the BVH stack fix it is 18.1 ms specialized against
19.5 unspecialized — it helps, substantially, and the earlier reading was the
oversized stack dominating both arms. Re-measure old anomalies after any change
that moves occupancy.

The general lesson, which this document already states and which this pass
confirmed four more times: **register allocation in a megakernel this size
responds non-monotonically to local changes.** Removing dead code made it slower
twice. Single samples are worthless; so are A/B comparisons across different
builds.

---

## The peak is the BVH traversal, and nothing else (superseded)

`maxTotalThreadsPerThreadgroup` — Metal's verdict on how many threads of a
compiled kernel stay resident — is now measurable, by translating the linked
WGSL with `naga` and compiling it through a small Metal probe. See
[tools/occupancy/](../tools/occupancy/README.md) for how to run it, and for why
it cannot be read through wgpu.

On an M2 Max the megakernel reports **384 against a 1024 ceiling**; `aa_resolve`
matches it, because it calls the same `ray_color`. Every screen-space filter pass
is unconstrained at 1024.

Ablating and re-measuring localizes it, and the answer is narrow. Stripping every
`FEAT_*` flag: 384. Each primitive kind individually: 384. `BVH_STACK_SIZE` from
8 to 128: 384. `MAX_SEGS` 1 to 6: 384. Glass off, glossy lobe off, shadow record
off, `shade_diffuse` gutted to `return alb`, instancing off, `hit_prim` stubbed:
384 every time. Gut `main` so it traces nothing: **1024**. Call `nearest_hit` and
nothing else: **384**.

**The entire register peak is the bare BVH traversal loop** — the stack,
`slab_hit`, `bvh_child_push` holding two nodes' bounds at once, the `Hit` state.
Everything else in the kernel fits underneath it for free.

That reframes several things this document says:

- **Shading is not worth optimizing for occupancy.** Narrowing the carried
  `ShadowAux` from 192 bytes to 72 measured exactly neutral, and now we know why
  rather than guessing: it was never on the peak path.
- **Specialization does not pay by reducing register pressure.**
  [shader-specialization.md](shader-specialization.md) assumes it does — "code a
  scene never executes still costs occupancy on every ray". Stripping everything
  leaves the number where it was. Its 12% is real but comes from elsewhere:
  fewer instructions and branches, better scheduling, instruction cache.
- **Thread scratch is a different resource, and this probe cannot see it.**
  `BVH_STACK_SIZE` 32 → 24 was worth 7% of frame time and moves this number not
  at all. The wins recorded in this document under "occupancy" — the
  `box_holed_nearest` array, the traversal stacks — were *spill memory* wins.
  Two taxes, two instruments, and only one of them now has a number.

Superseded: see "Everything flies under the cost of BVH was wrong" below. The
measurements in this section stand -- `nearest_hit` alone really does reach 384
-- but the conclusion drawn from them does not, because removing traversal
altogether leaves the ceiling exactly where it was.

If register occupancy is ever the target, the traversal loop is the only place to
look. If frame time is the target, scratch is still the richer seam, and it is
still measured the old way.

**And the traversal is a plateau, not a hot spot.** Bisecting it further — by
stripping and adding back, because single ablation cannot find the argmax of a
maximum — puts a hand-written traversal loop at 1024, the real `nearest_hit`
stripped to its static loop at 384, and that same loop with `hit_prim` stubbed at
512. So the inlined primitive intersectors are one real contributor, but no
single prim kind is responsible, and instancing, planes, terrain and water each
hold 384 on their own. Several independent paths sit at the same pressure and
moving the number means lowering all of them together. Two restructurings were
tried and neither moved it; see [tools/occupancy/](../tools/occupancy/README.md).

## Scratch now has an instrument too, and it points at one loop

The sentence above — "scratch is still the richer seam, and it is still measured
the old way" — is no longer true. The AGX backend emits LLVM register-allocator
remarks, and a `.gputrace` capture carries them: a pass name, a function, a
source line, `NumSpills`, `NumReloads`, `TotalSpillsCost`. That is a targeting
instrument, not just a number. See [tools/spills/](../tools/spills/README.md);
`./tools/spills/run.sh` runs the whole chain and never opens Xcode.

The first reading:

```
total: 3287 spills, 9140 reloads, cost 8480

 MSL line   spills  reloads       cost   share  enclosing function
     9709      854     2199       2423   28.6%  ray_color()
    12334      454     1124       2347   27.7%  aa_resolve()
    10823      425     1115       2332   27.5%  supersample_edge()
```

The top three lines are the same code — `ray_color`'s segment loop — charged
three times, because `supersample_edge` inlines it and `aa_resolve` calls it.
**84% of the kernel's spill cost is that one loop.** This is the mechanism behind
AA costing about a quarter of the frame, which "Where the remaining time goes"
could only state as an observation.

Note where it does *not* point. The register peak is the BVH traversal; the
scratch cost is the segment loop that wraps it. Two taxes, two places, and the
optimization this document has been chasing for two passes was aimed at the
first while frame time was being set by the second.

Two hypotheses died on it immediately:

- **`ShadowAux` 192 B → 72 B.** 3287 → 3236 spills, cost 8480 → 8472. Neutral in
  spills, as it was in frame time. Third instrument, same answer.
- **`MAX_SEGS` 6 → 2.** Spill counts *byte-identical*. `array<RaySeg, MAX_SEGS>`
  is dynamically indexed, so AGX puts it in scratch by construction — it is never
  in a register, so it can never be a spill. It is scratch that the spill
  counters cannot see. (Frame time does fall 27%, but two segments drop ray-tree
  lobes and render a different image. Not a win — the third time this pass that a
  faster frame turned out to be a smaller one.)

So the spilled state is everything *else* live across the loop's calls: the
accumulators, the masks and throughput, the primary `Hit`, the lobe bookkeeping,
the reflect and refract parameters. Many small values, not one large one — the
same plateau shape the occupancy probe found, which means the same warning
applies: single ablation will read as neutral, and only strip-and-add-back can
rank them.

The obvious structural move is to stop paying for the loop three times. Having
`supersample_edge` and `aa_resolve` share one out-of-line `ray_color` rather than
inlining a second and third copy does not reduce the loop's pressure, but it
would stop multiplying it. That is untried and unpriced.

On reading the columns: the spill and reload counts are static, but `cost` is
LLVM's spill weight, scaled by the compiler's estimated block frequency — loop
depth weighted, from heuristics rather than a profile. The two disagree usefully.
`main_` has *more* spills than `aa_resolve`, 503 against 454, and one eighth the
cost, because `main_`'s sit at function scope while `aa_resolve`'s are inside the
loop. Rank by cost; read spills as the size of the resident set. Neither is a
byte count — the capture carries no scratch field — so confirm with frame time
and a pixel diff, as always.

## "Everything flies under the cost of BVH" was wrong, and now provably so

This document has said in several places that the BVH traversal sets the
register ceiling and the rest of the kernel fits underneath it for free. The
Metal backend is the experiment that settles it, because it removes traversal
from the kernel entirely rather than shrinking it. Measured on the same shader,
same machine:

| | WGSL path | Metal path |
|---|---|---|
| `main_` maxThreads/TG | 384 | **384** |
| `aa_resolve` maxThreads/TG | 384 | **384** |
| total spill cost | 8480 | **9520** |
| segment loop's share of it | 84% | **82%** |

**The ceiling does not move.** Splice out all BVH traversal: 384. Splice it out
*and* gut `shade_diffuse` to a constant: still 384. Neither traversal nor
shading holds the peak, and the statement "everything flies under BVH" had the
geometry backwards -- the other branches were never flying under it, they sit at
the same height. BVH was simply the contributor we could name, because it was
the one we had a probe for.

**Spill cost went up, not down.** Removing traversal cost 12% more spill, not
less; the intersector call has live state of its own across it. An earlier
measurement here showed -12% and was wrong: that splice replaced all of
`nearest_hit`, deleting the plane, terrain and water walks along with the
traversal. With the surgical splice `hit_terrain_mip` is back in the profile at
3.1%, which is how you can see the difference.

So the ~1.8x per-ray win is **not** a register win and **not** a spill win. It is
the six `array<u32, BVH_STACK_SIZE>` traversal stacks disappearing -- dynamically
indexed, therefore thread scratch by construction, therefore structurally
invisible to the spill counters. That blindness is proven twice over: both
`MAX_SEGS` and `BVH_STACK_SIZE` produce byte-identical spill counts while moving
frame time.

### Where that leaves the levers

1. **Anti-aliasing, and by a wider margin than before.** It is 5.3 ms of the
   WGSL frame and 4.5 ms of the Metal one -- 26% and **28%**. It got
   proportionally *worse* on Metal, because everything around it got faster. In
   the spill profile the AA path carries 54% of all spill cost, because the
   segment loop is compiled three times: once in `ray_color`, again inlined into
   `supersample_edge`, again reached from `aa_resolve`. Having those share one
   out-of-line copy would not lower the loop's pressure but would stop
   multiplying it. Still untried, and now the most valuable untried thing here.

2. **The segment loop itself**, at 82% of spill cost, unmoved by changing
   backends. It was competing with traversal for attention; on the Metal path
   nothing else is left at the top.

3. **Registers: a dead lever**, now twice proven. Stop reaching for it.

4. **The scratch that remains.** The traversal stacks are harvested on the Metal
   path. What is left -- the `RaySeg` stack, the terrain mip stack -- is equally
   invisible to `tools/spills`, so it can only be measured by frame-time A/B,
   and every such A/B needs a pixel diff, because these constants are all work
   budgets and a faster frame is usually a smaller one.

## Scratch was the lever, and a second backend measured how big

The two instruments in this document disagreed about where the frame goes, and
the disagreement turned out to be the finding. Register occupancy is pinned at
384 by a plateau and nothing lifts it. Thread scratch is invisible to that probe
and is the only thing that has ever moved frame time here: an array out of
`box_holed_nearest` was 14%, sizing the traversal stacks 32 -> 24 was 7%.

Metal's ray-tracing API settles it, because it removes scratch structurally
rather than by tuning. `bvh.wesl` declares six `array<u32, BVH_STACK_SIZE>`
stacks, 96 bytes each; an `MTLAccelerationStructure` deletes all of them, and
the generated MSL confirms they are dead-code eliminated. A backend built on it
renders **the same image** as this one and is **1.28x faster on office-sunset,
1.27x on the villa**, timed like for like at 512x320. See
[metal-backend.md](metal-backend.md).

**And the whole-frame figure understates it.** A frame carries fixed costs the
intersector does not touch -- the penumbra filter, AA classification, shading --
so the number that matters for deciding what to spend the win on is the marginal
cost of a ray. Measured by adding 466,848 shadow rays and taking the slope, over
three interleaved rounds: **about 7.3 ms per million on the WGSL path against
4.1 on Metal, so roughly 1.8x cheaper**, spread 1.6x to 2.0x. See
[metal-backend.md](metal-backend.md#what-a-ray-costs-now). At the WGSL path's
own frame budget that is room for something like 6 or 7 more shadow rays per
pixel, which no existing knob spends -- soft shadows here come from a 61-tap
screen-space filter over one record per pixel, not from ray count.

That is the largest single result in this document, and it did not come from
any of the levers this document spent two passes on. It is worth being precise
about why the earlier estimate missed:

- The register probe said the backend would inherit 384, and it does. That
  measurement was correct.
- The conclusion drawn from it -- "a single-digit frame win" -- was wrong,
  because registers were never the mechanism. The probe answered its question
  accurately and the question was not the governing one.
- An isolated traversal benchmark said 9.2x, which was also correct and also
  not predictive: traversal is a minority of the frame, exactly as the 4-wide
  BVH showed when it cut node visits 16% and bought 1%.

Three sound measurements, three wrong predictions, because each measured a
component rather than the frame. The only number that held was the one taken
end to end, against a matching image, with both sides timing the same window --
and getting *that* comparison honest was itself a correction: gpuprof measures
submit-to-idle including the output copy, while the obvious Metal number is
device execution alone, a gap worth ~0.35 ms that would have flattered the new
backend for free.

What remains true for the WGSL path: the spill profile above still says 84% of
spill cost is `ray_color`'s segment loop, charged three times. Nothing in this
section fixes that, and it is still the richest seam for anyone optimizing the
portable backend rather than replacing it.

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
