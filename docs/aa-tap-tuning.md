# Where adaptive AA's time goes, and the two samples it was blending wrong

**Status:** shipped. AA is **unchanged in cost** (+0.3% over 7 interleaved
rounds, inside the per-view spread) and measurably closer to ground truth: RMS
error against a 31-tap reference falls **16.3%** on office-sunset's four views
and **27%** on outdoors-night-villa, with errors beyond 8 display levels down
31% and 30%.
**Audience:** anyone who thinks AA is too expensive, or is about to reorder the
pass chain to fix it.

The investigation started from the belief that AA's cost was structural — the
tonemap written up to three times per pixel, the full-screen classify, the pass
boundary the indirect dispatch forces. **None of that is measurable.** All of
AA's cost is the extra rays, and the interesting result is that those rays cost
about four times what the same rays cost in `main`.

---

## The decomposition

Office-sunset, 512x320, bounce depth 4. The middle row was produced by setting
both classifier thresholds to 1e9, so `main` still writes its AA scratch and
`aa_classify` still runs over every pixel, but no task is ever appended:

| | yaw 0 | yaw 270 |
|---|---|---|
| AA off | 6.5 ms | 8.8 ms |
| + `main`'s scratch writes + `aa_classify` over the full screen | 6.5 | 9.0 |
| + `aa_resolve` | 8.9 | 12.6 |

So the classify pass and the redundant writes together are **0.0 to 0.2 ms**,
and adaptive AA is **2.4 to 3.8 ms — 27% to 30% of the frame** — every bit of it
the resolve dispatch.

That closes the pass-ordering question before any code is written. Making every
pass write HDR and tonemapping once at the end is a reasonable tidying of the
buffer contract, and it would let `supersample_edge` drop the correction it
carries for the penumbra filter, but **it cannot move the frame time**, because
the thing it removes costs nothing.

## Tap rays cost 4x what the same rays cost in `main`

New counters (`PROF_AA_TASKS`, `PROF_AA_GEOM`, `PROF_AA_SHADE`) report what the
classifier actually selects:

| yaw | flagged | silhouette | curvature | resolve cost | per tap |
|---|---|---|---|---|---|
| 0 | 4,201 (2.6%) | 1,499 | 2,708 | 2.4 ms | **571 ns** |
| 270 | 15,973 (9.7%) | 11,165 | 5,668 | 3.8 ms | **238 ns** |

Against `main`, which traces 163,840 rays in 6.5 / 8.8 ms — **40 to 54 ns** per
ray, each with its own bounces and shadow rays.

The obvious explanation is that edge pixels are harder pixels, and the counters
say that is a small part of it. Differencing a profiled frame against `-aa=false`
attributes to each tap 2.11 path segments and 6.09 shadow rays, against 1.74 and
4.80 for an average `main` pixel — **1.25x the work**. The other **~3.5x** is
not work. It is 64 unrelated points on the screen sharing a workgroup: their
rays diverge, and the workgroup runs at the pace of its deepest lane.

This is the effect [bounce-kernel.md](bounce-kernel.md) predicted when it warned
that compaction "costs the spatial coherence of neighbouring pixels' rays landing
in the same workgroup". There it was worth 4-8%. Here it is worth 350%, because
AA edges are far more scattered than a glossy surface is.

Note also that the per-tap cost is *worse* where there are fewer taps. That looks
like under-saturation — 4,201 tasks is 66 workgroups against ~30 GPU cores — and
it is tempting, because it would mean extra samples were nearly free. **They are
not.** Sweeping `AA_TAPS`:

| taps | yaw 0 | yaw 270 | ideal |
|---|---|---|---|
| 2 | 1.80x | 2.05x | 2.00x |
| 3 | 2.55x | 3.05x | 3.00x |
| 5 | 4.15x | 5.05x | 5.00x |
| 7 | 5.90x | 6.97x | 7.00x |

Linear at yaw 270 and only slightly sublinear at yaw 0. The pass is
throughput-bound. **Quality cannot come from more rays here** — 3 taps costs
11.6 ms of AA at yaw 270 against a whole-frame budget of 12.6.

## The two samples were weighted wrong

Which leaves getting more out of the one tap that is already paid for.

`aa_resolve` estimates the pixel's box average from two samples: the centre,
which `main` traced, and one tap aimed at the edge. It averaged them 50/50. But
**the pair is not symmetric about the pixel centre** — the centre sits at 0.5 and
the tap at 0.5 ± reach — so equal weights put the estimator's centroid at
`0.5 ± reach × weight`, biased toward whichever side the classifier aimed at.
And it aims at the *brighter* neighbour, so the bias has a direction.

Tuned against a 31-tap Hammersley reference, measured over only the pixels AA
actually touches, RMS across all four office views:

| reach \ weight | 0.30 | 0.35 | 0.40 | 0.45 | 0.50 |
|---|---|---|---|---|---|
| 0.25 | 7.90 | 7.72 | 7.77 | 8.05 | **8.51** |
| 0.30 | 7.42 | 7.37 | 7.61 | 8.09 | 8.77 |
| 0.35 | **7.13** | 7.26 | 7.70 | 8.40 | 9.29 |
| 0.40 | 7.26 | 7.58 | 8.20 | 9.07 | 10.09 |

Bold: what shipped before (0.25 / 0.50) and what ships now (0.35 / 0.30).

The good cells lie along `reach × weight ≈ 0.10 to 0.12`, which is exactly the
centroid displacement above — the sweep is finding the bias and cancelling it.
Within that constraint a *larger* reach is better, because a tap further out is
likelier to actually land on the other surface and so carries more information,
until it starts leaving the pixel: 0.40 is already worse than 0.35.

It is not merely a weaker correction. Every error statistic improves, on both
scenes and in the worst local window as well as globally:

| | office 4 views, RMS | >8 levels | villa RMS | villa >8 |
|---|---|---|---|---|
| 0.25 / 0.50 | 8.51 | 7,626 | 13.66 | 4,765 |
| **0.35 / 0.30** | **7.13** | **5,284** | **9.99** | **3,327** |

Two things worth keeping about the method. The metric is restricted to pixels the
reference actually changed, because 90% of the frame is identical in every arm
and would otherwise dilute the comparison into noise. And it was swept on four
views and confirmed on a second scene: the villa gains almost twice as much as
the office, so a single-view tune would have understated it badly.

**A magnified crop is still worth looking at, and will mislead you if it is all
you look at.** On the office chandelier the new build reads as dimmer and the
parapet band as brighter, which looks like a regression. Measured against the
reference over that same 64x64 window, it is not: RMS 16.89 -> 15.87, errors
beyond 8 levels 542 -> 468, and the bias against the reference halves from -1.61
to -0.83. The old build was the one further from the truth; it was just further
in the direction that reads as contrast.

## Shipped alongside: tile-aggregated compaction

`aa_classify` used to do one global `atomicAdd` per edge pixel, so a tile's
tasks were scattered through the list by whatever order workgroups happened to
run in. It now counts its edge pixels in workgroup memory, claims one contiguous
block with a single atomic, and fills it — so a tile's tasks come out adjacent,
and `aa_resolve` draws each workgroup from a few neighbouring tiles instead of
64 unrelated points.

Byte-identical, and **-1.0%** interleaved. Worth having, but note how small it is
against the 3.5x above: contiguous *blocks* are not contiguous *pixels*, and the
divergence that costs the 3.5x is between rays a few tiles apart, not within one.

Closing that gap properly means sorting tasks by expected cost, which is the
wavefront argument [bounce-kernel.md](bounce-kernel.md) closed for this renderer
at this scene scale — and AA at 4k-16k tasks is far below even the queue sizes
that failed there.

## What is left

- **The classifier, not the resolver.** Cost is now proportional to task count
  and nothing else, so the lever is flagging fewer pixels. Curvature alone
  selects 1.7-3.5% of the frame; whether all of those pixels are visibly
  improved by a tap has never been checked against a reference, and now can be.
- **Coverage from the data already in hand.** The neighbour luminances and
  primary-hit fingerprints describe the edge running through the pixel. An
  analytic coverage estimate would give a per-pixel blend weight instead of the
  single tuned constant above, at no extra rays. The constant is the zeroth-order
  version of exactly that.
- **Pass ordering** remains a defensible tidy-up — one tonemap per pixel, AA
  writing HDR, `supersample_edge` losing its correction — on code-health grounds
  only. It is worth 0.0 to 0.2 ms and it disturbs a soft-shadow interaction that
  took a long time to get right, so it should be done for its own reasons or not
  at all.

## Related

- [adaptive-aa.md](adaptive-aa.md) — what the three passes do.
- [megakernel-optimization.md](megakernel-optimization.md) — the classify/resolve
  split, and the rejected cheaper tap rays (still rejected; this changes the
  tap's *placement and weight*, not its shading).
- [bounce-kernel.md](bounce-kernel.md) — density, sparsity, and why moving rays
  into their own dispatch costs coherence.
- [soft-shadows.md](soft-shadows.md) — the correction `supersample_edge` carries.
