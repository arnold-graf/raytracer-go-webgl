# Splitting Reflection Transport Out of the Megakernel

> **Removed from the code (2026-09-18).** The live probe field, the DDGI
> cascades, the baked volume and their tools (`cmd/probebake`, `cmd/ptlive`,
> `internal/gibake`, `shaders/modules/gi.wesl`) are gone. Nothing here ships any
> more; virtual point lights are the GI that remained — see [vpl.md](vpl.md).
> This file is kept because what it measured is why, and every reason it gives
> still applies to anything that would replace it.

**Status:** closed. Both halves built, measured and reverted — glossy first,
then glass. Neither pays, and the two results together explain why no lobe can.
**Audience:** whoever picks up the remaining reflection cost on office-sunset.
**Constraint:** perceptual parity. Reflections must contain the same objects as
the primary view, including instanced trees. Skipping geometry in a bounce is
not an acceptable trade; it was tried and reverted (see
[megakernel-optimization.md](megakernel-optimization.md)).

This document originally proposed deferring the **glossy** lobe to a separate
compiled kernel. That was built and measured; it does not pay. The measurement
pointed at glass as a larger target, so glass was built and measured too. It
does not pay either, and for the opposite reason — which is the useful part.

**The short version.** Deferring a lobe out of the megakernel pays in proportion
to how *dense* it is, because only work on the critical path is worth removing.
Compaction pays in proportion to how *sparse* it is, because only idle lanes are
worth reclaiming. Those two requirements point in opposite directions, and no
lobe in this renderer satisfies both. Glossy is dense: deferral is worth up to
13% and compaction is a loss, but keeping the Fresnel fork intact eats the gain.
Glass is sparse: compaction beats a dense dispatch handily, but the fork was
already nearly free inline, because it rides along inside workgroups that are
91% busy with something else.

The experiments are preserved at `tmp/perf/bounce-kernel-experiment.patch` (the
glossy split; the glass split is the same plumbing retargeted) if anyone wants
the scaffolding back.

---

## The measured budget

Everything below is office-sunset at 512x320, bounce depth 4, adaptive AA on.

Configurations were switched at runtime from a params field
(`RAYTRACER_BOUNCE_SPLIT`, `RAYTRACER_GLASS_SPLIT`), never by building separate
binaries — see the methodology warning below for why that distinction matters:

| yaw 270 (worst view) | ms | | yaw 0 (shipping camera) | ms |
|---|---|---|---|---|
| full frame | 11.40 | | full frame | 7.30 |
| glossy lobe, deferrable | **2.30** | | glossy lobe | **3.40** |
| glass fork, deferrable | **1.10** | | glass fork | **0.20** |
| other specular (nested lobes, ghost, induced AA) | ~2.70 | | other | ~-0.30 |
| floor: primary + diffuse + AA | **5.30** | | floor | **4.00** |

The floor is what remains with every specular lobe removed. It cross-checks
against `gpuprof`'s own ablation table, which reports `mirror off` at 5.2 ms for
the same view — two independent methods 2% apart.

The two middle rows are what a split can actually move: each was measured by
deferring that lobe and skipping its resolve pass. The "other specular" row is
the remainder and is *not* extractable as such — it is the lobes reached through
another lobe, the thin-glass ghost, and the extra adaptive-AA work that all that
luminance structure triggers.

Getting that decomposition wrong twice is the main cautionary tale of this
document. An earlier revision claimed glass cost 3.80 ms and was therefore the
larger prize; that number came from a probe which removed glass by shading it
diffuse, and so silently removed the nested and induced work along with it. The
figure that survives direct measurement is **1.10 ms**. Before the version
before that, both lobes were priced by *disabling one at a time*, which
undercounts whichever lobe is not the critical path. Only deferral prices a
lobe.

Ray counts for the same views, from `-profile`:

| yaw | pixels | glossy lobes | glass | mirror |
|---|---|---|---|---|
| 0 | 163,840 | 150,381 (92%) | 400 | 4 |
| 180 | 163,840 | 79,919 (49%) | 703 | 68 |
| 270 | 163,840 | 117,955 (72%) | 14,870 (9%) | 533 |

---

## What was built, and what it measured

Two mechanisms, which this document originally bundled as one design:

1. **Compaction.** `main` appends a `BounceTask` to a work list; an indirect
   pass runs over the compacted list. The shape that made adaptive AA
   affordable.
2. **A narrow kernel.** `bounce_resolve` as its own pipeline whose call graph is
   one `nearest_hit` plus one shade — no ray stack, no glass fork, no AA.

Both were built, plus a third variant (`dense`) that keeps the narrow kernel and
drops the compaction: one thread per pixel over `main`'s own 8x8 tiling. That
separation is what made the result readable.

Interleaved best-of-7, modes alternating within each round so clock ramp and
thermal drift land on all of them equally:

| yaw | inline | compact | dense (1 hit) | dense (chained) |
|---|---|---|---|---|
| 0 | 7.60 | 7.30 | **6.60 (-13.2%)** | 7.10 (-6.6%) |
| 90 | 10.30 | 10.10 | 9.60 (-6.8%) | 9.90 (-3.9%) |
| 180 | 7.70 | 7.70 | 7.60 (-2.6%) | 7.70 (0.0%) |
| 270 | 11.40 | 11.80 (+3.5%) | 11.40 (0.0%) | 11.80 (+3.5%) |

`compact` and `dense` are byte-identical in output; they differ only in how the
work is dispatched.

### Compaction cannot pay for glossy

**Its benefit scales with sparsity, and glossy is dense.** Adaptive AA fires on
roughly 5% of pixels, scattered along silhouettes, so a 64-lane workgroup
holding one edge pixel wasted 63 lanes — compaction reclaimed about 20x. Glossy
fires on 49-92% of pixels, so a workgroup is *already* mostly bounce lanes.
There is almost no idle-lane waste left to reclaim, and the attempt still costs
an atomic append, a header copy, an extra dispatch, and the spatial coherence of
neighbouring pixels' rays landing in the same workgroup.

This is structural, not a tuning miss. Do not retry it for a dense lobe.

**The criterion to carry forward:** compaction is worth it when the work is
sparse enough that whole workgroups would otherwise idle. The glass split later
measured the crossover directly and it is low — compaction wins at 0.2% density
and already loses to a plain dense dispatch by 8%. Above 50% it is strictly
overhead.

That criterion is real but it turned out to be the wrong question, because
sparse work is also work the megakernel hides for free. See the glass section.

### The narrow kernel does help, and it is not enough on its own

Decomposed at yaw 270 by measuring `main` with the lobe deferred but the resolve
pass not dispatched (`RAYTRACER_BOUNCE_NOPASS`):

| | yaw 270 | yaw 0 |
|---|---|---|
| glossy traced inside the megakernel | 2.70 ms | 3.60 ms |
| the same rays in the narrow kernel | 2.50 ms | 2.60 ms |
| per-ray difference | **-7%** | **-28%** |

So the narrow kernel is genuinely cheaper per ray. It just is not cheaper by
enough at yaw 270 to cover the extra dispatch, and yaw 270 is the view that
sets the floor. The win tracks how much of the *critical path* the deferred work
occupied: at yaw 0 glossy is essentially the whole frame and the split takes
13%; at yaw 270 the workgroup still waits on glass either way.

### Fidelity is what killed the cheap version

A single-hit bounce kernel cannot fork, so a bounce ray that lands on glass gets
shaded as diffuse. In the server room the chandelier's reflection in the marble
floor goes out entirely — the same class of missing-content-in-reflections
regression as the reverted instance skip, and rejected for the same reason.

Following a **chain** instead (one dominant lobe per hit, no fork,
`BOUNCE_MAX_DEPTH = 3`) brings the reflection back but dimmer, because half the
Fresnel energy is dropped: max channel delta against the inline reference falls
from 195 to 142, with 1-2.5% of pixels differing. Restoring the real two-lobe
blend requires a fork, which requires a stack, which is the thing the split
exists to avoid.

The chain costs about half the win — compare the last two columns of the table
above. **Full fidelity and the glossy split are close to mutually exclusive**,
and at that price the split is not worth shipping.

---

## Hypotheses that were tested and are dead

Recorded so nobody spends the day again.

- **Per-thread scratch from the ray stack.** `array<RaySeg, MAX_SEGS>` is
  dynamically indexed, and removing exactly that kind of array from
  `box_holed_nearest` was the largest single win in the 2026-09 pass (-14%). It
  is not a factor here: `main` costs the same with the stack deleted outright.
  See the methodology warning below — the first attempt at this measurement was
  invalid and reported a bogus 1.1%.
- **Compaction losing ray coherence.** Plausible, and `dense` was built to test
  it by preserving `main`'s tiling exactly. It is a real effect (dense beats
  compact at every view) but it is second order next to the density argument.
- **Extra memory traffic through `hdr_pixels` and the task list.** About 20 MB
  per frame at 512x320, under 0.1 ms on this hardware. Not a factor.

---

## Methodology warning: do not A/B by building two binaries

`shaders.Source()` (`internal/webgpu/shaders/resolve.go`) reads
`trace_linked.wgsl` **from disk at runtime**, falling back to the `go:embed`
copy only when the file is absent or stale. Two binaries built from different
shader sources therefore both read whatever file is on disk when they run, and
an A/B between them measures the same shader twice.

This silently invalidated two measurements in this investigation before it was
caught — a `MAX_SEGS` 6-to-2 comparison and the first stackless probe, both of
which duly reported "no difference".

To compare two shader variants, either:

- switch behaviour at runtime from a `params` field (what `RAYTRACER_BOUNCE_SPLIT`
  does), which keeps one binary and one file on disk; or
- swap `modules/trace.wesl` **and** `trace_linked.wgsl` together between runs,
  since `readLinkedIfCurrent` validates a digest of the modules against the
  stamp in the linked file.

Either way, **verify the variant actually changed the image** (`-dump` plus
`tmp/perf/compare.py`) before believing a timing. A probe that renders
identically is not a probe.

Two more notes on measuring this scene at all: the noise floor is about **10%**
for an identical configuration on this machine, so any effect under that needs
interleaved rounds and a minimum-of-N estimator, not a single sample. And
`gpuprof` reports wall-clock-until-idle; there are no GPU timestamp queries in
this backend, so a pass cannot be timed in isolation — only by difference.

---

## The glass split, measured

Glass looked like the better target: incremental measurement put it at 3.80 ms
of yaw 270 against glossy's 2.30, and at ~9% of pixels it sits in the sparse
regime where compaction is supposed to pay. Both of those turned out to be
wrong, in instructive ways.

The build reused the glossy plumbing, retargeted. One detail worth keeping if
anyone revisits this: **both Fresnel children of a hit belong to the same pixel,
so one thread can own both**, sum them locally and store once. That removes the
race that WGSL's lack of f32 atomics would otherwise force you to solve, and it
means a first cut is one extra dispatch rather than the multi-wave queue this
document previously estimated at "weeks". Only depth 0 defers; glass reached
through another lobe still forks on the stack, so nested panes keep their exact
blend.

Interleaved best-of-8 (`RAYTRACER_GLASS_SPLIT`), `nopass` being the deferred
frame with the resolve dispatch skipped — a wrong image, but it prices `main`
with the fork removed:

| yaw | glass bounces | inline | compact | dense | main with glass removed |
|---|---|---|---|---|---|
| 0 | 400 (0.2%) | 7.40 | 7.50 (+1.4%) | 9.10 (+23.0%) | 7.20 |
| 90 | 13,184 (8%) | 10.10 | 11.00 (+8.9%) | 10.60 (+5.0%) | 9.70 |
| 270 | 14,870 (9%) | 11.40 | 12.40 (+8.8%) | 12.20 (+7.0%) | 10.30 |

| yaw | glass forked inline | the same work in the glass kernel |
|---|---|---|
| 0 | 0.20 ms | 0.30 ms |
| 90 | 0.40 ms | 1.30 ms |
| 270 | **1.10 ms** | **2.10 ms** |

### The 3.80 ms was an artifact

Deferring the primary glass fork removes **1.10 ms** from `main` at yaw 270, not
3.80. The earlier figure came from a probe that removed glass by shading it as
diffuse, which also removed the nested glossy lobes *behind* glass, the
thin-glass ghost, and all the adaptive-AA work that structure induces — it moved
58.9% of pixels. Attributing that whole delta to "glass" was wrong.

The lesson generalises past this document: **an ablation that changes the image
substantially is not measuring one feature.** Adaptive AA cost scales with edge
count, so anything that flattens the frame makes AA cheaper and inflates the
apparent cost of whatever was removed. Price a lobe by deferring it, not by
deleting what it produces.

### Sparsity makes inline *good*

The glass kernel costs roughly **twice** what the inline fork costs, at every
view. That is the reverse of the glossy narrow kernel, which was 7-28% cheaper
per ray, and the reason is the density that made glass look attractive.

At 9% of pixels a glass fork rides along inside workgroups where the other 91%
of lanes are doing diffuse work. It is latency-hidden for free — the same effect
already recorded in [megakernel-optimization.md](megakernel-optimization.md) as
"extra bounce depth is free without AA, because workgroups are already waiting
on the slowest lane". Pull that fork into its own dispatch and it stops
overlapping with anything: 14,870 tasks at 64 per workgroup is ~233 workgroups,
far too few to saturate 30 GPU cores, and each task is two three-segment chains,
so it pays full memory latency with no other work to hide behind.

So sparsity is not an argument for extracting a lobe. It is an argument for
leaving it where it is.

### What the compaction criterion is actually worth

Compact versus dense does behave as predicted, which is worth recording because
it is the one prediction that held:

| glass density | compact | dense | winner |
|---|---|---|---|
| 0.2% (yaw 0) | 7.50 | 9.10 | compaction, by 21% |
| 8% (yaw 90) | 11.00 | 10.60 | dense, by 4% |
| 9% (yaw 270) | 12.40 | 12.20 | dense, marginally |

The crossover is low — somewhere in the low single digits of percent. Compaction
is worth it only for genuinely rare work.
Adaptive AA at ~5% of pixels sits right at that boundary and wins there because
its alternative is catastrophic (a whole workgroup billed for one lane's ray
tree), not because compaction is efficient in absolute terms.

### Fidelity

Better than the glossy split, since only nested glass loses its fork:

| yaw | pixels differing | >8 levels | max delta | mean abs err |
|---|---|---|---|---|
| 0 | 0.00% | 0.00% | 0 | 0.000 |
| 90 | 1.83% | 1.57% | 139 | 0.832 |
| 180 | 0.07% | 0.06% | 47 | 0.020 |
| 270 | 0.81% | 0.34% | 142 | 0.072 |

Not that it matters at +8.8%. Recorded so nobody assumes fidelity was the
blocker this time; the cost was.

---

## Where the remaining money is

Not in reflection transport. At yaw 270 the frame is 11.40 ms and the floor —
primary rays, diffuse shading and adaptive AA, with every specular lobe gone —
is 5.30 ms. Of the 6.10 ms in between, the two lobes that could plausibly be
extracted account for 2.30 (glossy) and 1.10 (glass), and both cost more to run
outside the megakernel than in it once fidelity is held constant.

That closes the wavefront family for this renderer at this scene scale. Option A
in [reflection-optimization.md](reflection-optimization.md) should stay closed
unless something changes the arithmetic:

- **Much heavier per-hit shading** (many more lights per cluster, triangle
  meshes with real materials) would raise the cost of a bounce ray relative to
  the dispatch overhead a split pays.
- **Much higher resolution** would make the sparse dispatches large enough to
  saturate the device, which is the specific thing that killed the glass pass.
- **A lobe that is both dense and non-forking** would satisfy both requirements
  at once. Glossy is exactly that shape, and it *did* win 13% at yaw 0 — it lost
  only because a single-hit bounce cannot reproduce glass seen inside a
  reflection. A cheaper way to keep glass fidelity in a deferred glossy chain is
  the one live thread left here.

The 5.30 ms floor is the more interesting number now: it is 46% of the frame and
none of it is reflection. Adaptive AA is the largest identified item inside it.

## Does this argue against a general wavefront refactor?

Mostly yes, at this scene scale — and the reasoning is worth keeping because it
is about the workload, not about these two experiments.

A full wavefront adds two mechanisms neither experiment isolated: sorting rays by
material across *all* depths at once, and an extend kernel that does only BVH
traversal with no shading registers live. But both experiments priced the thing
all three designs share — moving rays out of the megakernel into a queue — and
that pricing is what decides it.

**The binding constraint is absolute queue size, not divergence.** The glass pass
failed because 14,870 tasks is 233 workgroups, under 4 threads per lane across an
M2 Max's ~3,840 ALUs, far too few to hide memory latency. The glossy queue at
118k gave ~31 per lane and the *same kernel structure* came out 7-28% cheaper per
ray. Material sorting makes this worse rather than better: this scene issues
about 164k primary and 133k secondary rays per frame at 512x320, so per-material
queues shard an already-marginal total into several non-saturating ones.

Worth knowing where the wavefront literature comes from: it was developed for
Fermi/Kepler-era GPUs with small register files, punishing divergence, and
millions of rays in flight. Apple GPUs have large register files and handle
divergence far better, so the trade a wavefront makes — spend memory bandwidth to
save register pressure — is much less favourable here. Every occupancy win
recorded in [megakernel-optimization.md](megakernel-optimization.md) lands in
single digits to ~15%; none has ever been a 2x lever.

**Upper bound on the whole idea.** Extractable secondary transport at yaw 270 is
3.40 ms (2.30 glossy + 1.10 glass) of an 11.40 ms frame. Applying the best
per-ray improvement ever measured, 28%, gives 0.95 ms — about 8% — before
dispatch overhead and before queue fragmentation claws some back. The 5.30 ms
floor is untouched either way: a wavefront reorganizes primary traversal, diffuse
shading and AA, it does not remove them.

### The conditions that would change the answer

In rough order of how directly the measurements support them:

1. **Higher resolution.** The clearest one, because it fixes exactly what killed
   the glass pass. At 1080p there are 12.6x the pixels and the glass queue goes
   from 15k to ~190k, which saturates. Every per-queue argument above flips.
2. **More rays per pixel** — multiple samples, deeper paths, multi-tap soft
   shadows. Same mechanism.
   The Metal backend makes this cheaper rather than structurally different: a
   ray costs roughly half what it does on the WGSL path
   ([metal-backend.md](metal-backend.md#what-a-ray-costs-now)), so the point at
   which more rays per pixel is affordable arrives sooner. The queueing argument
   above is unchanged by it.
3. **Much heavier per-hit shading** (many more lights per cluster, area lights,
   real material models). This is the one case where the memory-versus-registers
   trade genuinely reverses, because shading's register footprint is what the
   extend/shade separation exists to get off the traversal threads.
4. **Material diversity with divergent cost.** Today diffuse is ~91% of hits and
   cheap. Sorting pays only when expensive materials are common enough to fill a
   queue but rare enough to be poisoning workgroups.
5. **Triangle meshes with textures**, which raise traversal and shading pressure
   together.

### The cheap test for when that threshold is crossed

Re-run the glossy split at 1920x1080 (`gpuprof -w 1920 -h 1080`; the patch is in
`tmp/perf/`). It won 13% at yaw 0 and roughly nothing at yaw 270 at 512x320. If
that win *grows* with resolution, the wavefront's economics are shifting and the
extend/shade separation is worth prototyping. If it stays flat, the question is
settled for any resolution this renderer would ship at.

Do this before writing any queue code. It is fifteen minutes against weeks.

---

## Related

- [megakernel-optimization.md](megakernel-optimization.md) — AA compaction,
  occupancy lessons, why leaf widening failed, the reverted instance skip.
- [reflection-optimization.md](reflection-optimization.md) — original Option A
  (full wavefront) vs Option B (cheaper bounce shadows; B1 shipped).
- [shader-specialization.md](shader-specialization.md) — `FEAT_*` compile-out.
- [adaptive-aa.md](adaptive-aa.md) — the classify/resolve split this borrowed
  from, and the sparsity that made it work.
