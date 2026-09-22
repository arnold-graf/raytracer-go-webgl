# A Metal backend, and what it actually bought

**Status:** built, measured, and **renders the same image as the WGSL backend**.
1.28x on office-sunset, 1.27x on the villa, both at the in-game 512x320, timed
like for like. The harness lives in [tools/metal-rt/harness/](../tools/metal-rt/harness/README.md).
**Audience:** whoever wires this into the game.

---

## The result

| scene | wgpu | metal | pixels differing by >=8 |
|---|---|---|---|
| office-sunset atrium | 19.6-20.1 ms | 15.3-15.5 ms | 45 of 163840 (0.0%) |
| outdoors-night-villa | 16.6-16.8 ms | 13.0-13.4 ms | 61 of 163840 (0.0%) |

Interleaved, three rounds each. Local-contrast ratios against the reference are
1.00 on the frame, on the villa's stairs and on its grass. Traversal,
instancing, textures, shadows, reflections, refraction, both screen-space
filters and adaptive AA all agree with the WGSL backend.

**Both sides measure the same window.** gpuprof's `gpu` line is wall clock from
before encoding until the device is idle, and the command buffer it times
includes the copy of the output buffer. The harness reports that window
(`submit-to-idle`) as well as device execution (`gpu-exec`); only the first is
comparable. On office-sunset they differ by about 0.35 ms, which is what a
mismatched comparison would have quietly handed this backend.

### Why the earlier numbers were bigger

An isolated traversal benchmark said 9.2x. A first end-to-end run said 1.7-1.85x.
Neither survived.

The 9.2x was traversal alone, in a lean kernel, against a flattened scene -- and
traversal is a minority of the frame, which [bvh-traversal.md](bvh-traversal.md)
had already shown when a 4-wide BVH cut node visits 16% and bought 1%. The
1.7-1.85x was mostly `aa_resolve` missing all geometry and returning sky, which
is cheap. Fixing that moved execution from 10.9 ms to 15.0 ms.

27% is the number that survives contact with a matching image and a matching
timer.

## The mechanism is not registers

This document used to argue the case on register pressure, and that argument was
wrong. Replacing both traversals leaves `maxTotalThreadsPerThreadgroup` at 384,
unmoved, because the peak is a plateau with more contributors than traversal
(see [tools/occupancy/](../tools/occupancy/README.md)). The section below
records that measurement; it still stands.

What pays instead is **scratch**. `bvh.wesl` declares six
`array<u32, BVH_STACK_SIZE>` traversal stacks, 96 bytes each, and Metal's
intersector deletes all of them -- the harness confirms they are dead-code
eliminated. Sizing those stacks 32 -> 24 was worth 7% of frame time on its own
([megakernel-optimization.md](megakernel-optimization.md)); removing them
entirely is a larger version of the same lever. Register occupancy and thread
scratch are two different taxes, and only the second one has ever moved this
renderer.

## Why this and not the other options

Priced elsewhere in these docs, against the same frame:

- **Wavefront** — built, measured, reverted twice; ~8% ceiling at this scene
  scale, with a documented tripwire for when that changes
  ([bounce-kernel.md](bounce-kernel.md)).
- **4-wide BVH** — built, measured, reverted; 16% fewer node visits, 1% of frame
  ([bvh-traversal.md](bvh-traversal.md)).
- **Compressed nodes** — not built. Padding the 4-wide node from 144 to 240
  bytes cost nothing, so node traffic is not the constraint and compression has
  nothing to buy.
- **Scratch reduction** — the one thing that has repeatedly worked: 14% from an
  array in `box_holed_nearest`, 7% from the traversal stacks. It works because
  scratch limits how many rays are in flight, which is the same lever this
  proposal pulls, from the other end.

Every in-place lever is either spent or measured small. This is the first
structural one that targets what the frame is actually waiting on.

## How it integrates

**WGSL stays the single source of truth**, and this held up better than planned.
`naga` translates the linked WGSL to MSL and two small tools adjust the result;
there is no second copy of the shading, the filters, the AA, or the primitive
intersectors.

    shaders/modules/*.wesl          single source, unchanged
       |
       +-- link.sh  -> trace_linked.wgsl -> wgpu backend   (portable, stays default)
       |
       +-- mslpatch -> naga -> splice traversal -> mslbind -> metal backend

Two items the original plan budgeted for turned out to need no porting at all:

- **Shading**, because naga emits it and `rt_handle` is threaded to it
  automatically (see "The codegen pipeline works" below).
- **The primitive intersectors.** naga's output already contains `intersect()`
  and `intersect_blocker()`, the per-primitive tests a BVH leaf calls, so
  `mslpatch` emits `[[intersection(bounding_box, instancing)]]` functions that
  call them. Both backends test primitives with the same generated code, by
  construction. `hit_box`'s CSG-hole walk, called out as the fiddliest item,
  never had to be written.

### The splice

Only two functions are replaced, and the split is made **in WGSL, not in the
generated MSL**:

- `rt_nearest(ro, rd, h_in)` in `bvh.wesl` -- the static BVH plus the instance
  TLAS/BLAS, and nothing else.
- `blocker_bvh_any_hit` -- already pure BVH, because `shadow_blocker_t` composes
  planes and terrain at the call site.

`rt_nearest` exists *because of* the splice. `nearest_hit` also walks infinite
planes, terrain and water, none of which live in the BVH; an earlier version
replaced the whole function body and silently dropped all three, rendering a
cheaper wrong frame. Keeping the split in WGSL means a regex that drifts cannot
lose the tail. `tools/metal-rt/verify.sh` asserts terrain and water survive.

### Acceleration structures

Derived from the packed BVH rather than recomputed from primitives, so the two
backends cannot disagree about geometry -- see `internal/webgpu/accelpack.go`.

- `Geom[0]` is the static set, `Geom[1+t]` each template; likewise for blockers.
- `Instances[0]` is the static set at identity; `Instances[k]` is the WGSL's
  placement `k-1`, which is the numbering the generated MSL decodes.
- Leaves carry **absolute** `prims[]` indices, which ride in the structure as
  `[[primitive_data]]`, because a bounding box intersection function may not
  read `[[instance_id]]` and `primitive_id` is local to its geometry.

One numbering trap, and it is not obvious: the static blocker tree's *internal*
nodes hold section-relative child indices while its *leaves* hold absolute
primitive indices, mirroring `blocker_bvh_any_hit` pushing `blocker_off +
n.info.x` against untouched leaf slots. Template subtrees are absolute
throughout. Reading the blocker children as absolute walks back into the main
tree and fills the blocker structure with geometry leaves -- every shadow ray
testing the wrong set, no crash.

### Scene data

The Metal side packs nothing. `RT_DUMP_BUFFERS=<dir>` makes `internal/webgpu`
write all 30 bindings verbatim (`internal/webgpu/bufdump.go`) and the harness
binds those bytes at the same indices, which leaves traversal as the only
variable under test. Every upload goes through one choke point, `(*Renderer).wb`.

For the game this is the piece that changes: the real backend owns its own
`MTLBuffer`s and has to run the same packers. The dump stays useful as a parity
harness.

## What was rejected: splicing inside wgpu

The tempting shortcut is to leave everything on wgpu and have it use a Metal
intersector on Apple hardware, since wgpu compiles our WGSL to MSL anyway. It is
closed, for three reasons of increasing severity:

- `ShaderModuleDescriptor` accepts SPIR-V, WGSL and GLSL. There is no MSL
  passthrough, so wgpu cannot be handed Metal source.
- The Go binding exposes no `as_hal` or Metal interop, so the underlying
  `MTLDevice` and `MTLBuffer` are unreachable.
- `raytracing::intersector` needs an `MTLAccelerationStructure` **bound to the
  kernel**, and wgpu 0.17 has no acceleration-structure resource type at all.
  Even with MSL passthrough there would be no way to bind what the intersector
  needs.

The third is the one that cannot be worked around at any version we can reach.

## It is wired into the game

    go run . -backend auto      # metal where the hardware supports it
    go run . -backend metal
    go run . -backend webgpu

`auto` prefers Metal where `supportsRaytracing` is true and falls back to the
portable WGSL path, which is the only one that runs in a browser. Both run the
interactive loop cleanly.

    internal/render     Renderer interface, View          (unchanged)
    internal/webgpu     WGSL megakernel + the Packer      (still the default)
    internal/metal      MTLAccelerationStructure + MSL

`internal/metal` packs nothing of its own. `webgpu.Packer` produces the bytes
for every binding with no GPU device involved, and each backend uploads them
its own way. That was validated before anything was built on it: `packdump`
writes the Packer's output in the same layout `RT_DUMP_BUFFERS` produces, and 29
of 30 bindings come out byte-identical to what the wgpu upload path sends. (The
thirtieth is the AA indirect header, which the dump captures after `aa_classify`
has written to it.)

### Refit, and why it is off by default

`mtl_accel_refit` refits in place, keeping topology -- the analogue of
`bvh_refit.go`. It is **gated off**: set `RT_METAL_REFIT=1` to enable it. The
default rebuilds the structures when transforms move, which is slower and
cannot wedge the device.

That caution is earned. An earlier version refit structures that had not been
built with `MTLAccelerationStructureUsageRefit`, which is undefined. The build
succeeded, the refit was accepted, and the GPU then ran until the watchdog
killed the command buffer with
`kIOGPUCommandBufferCallbackErrorImpactingInteractivity` -- 25 seconds in a
headless reproducer, and on one run it took the machine down and cost a reboot.
The flag is set now on both descriptor kinds, and a second bug alongside it
(every refit in one encoder sharing a single scratch buffer, which the header
says is undefined once a refit starts) is fixed too. Neither has been run on
hardware since, which is exactly why the gate exists.

Rebuild-per-frame costs, at 512x320 with a moving dynamic body:

| scene | first frame | moving frame |
|---|---|---|
| office-sunset | 88 ms | 23 ms |
| villa | 89 ms | 17 ms |
| default.toml | 40 ms | 9 ms |

The Go side chooses by the frame's dirty flags: `StaticChanged` rebuilds,
`TransformsChanged` alone refits (when enabled), neither does nothing, and a
structure whose box count changed always rebuilds because a refit cannot change
how many boxes a structure holds.

What moves at runtime is *dynamic bodies*: NPC limbs are primitives the cache
repacks in spans. Instance placements are static scenery, and moving one marks
nothing dirty -- worth knowing before testing refit against trees, as I did.
`AccelStats()` reports builds and refits.

### Packing is incremental, and must stay that way

`Packer.Pack` returns only the bindings that changed. This is not an
optimization to be traded away: the AO volume alone is 24 MB, and
re-serializing the full set every frame took a moving frame on default.toml
from 5 ms to between 100 and 580 ms. `uploadFrame` has always been incremental
for the same reason.

A consequence worth remembering when testing: `gpuprof` re-renders one view, so
nothing is ever dirty, the incremental path never differs from the full one and
the refit path never runs at all. Both bugs above were invisible to it and
appeared the moment the game moved something. The reproducer in
`internal/metal`'s test moves a `DynamicBody` between frames.

### Parity

Through the same `gpuprof` command line, with and without `-metal`:

| scene | pixels differing by >=8 |
|---|---|
| office-sunset atrium | 0.00% |
| outdoors-night-villa | 0.00% |
| default.toml | 0.5%, mean level identical |

The last is an open item. It is not AA (it survives `-aa=false` on both sides)
and not leaf coverage (default.toml's five infinite planes live outside the BVH
by design, so 31 leaves for 36 prims is correct). The differing pixels are
isolated specks on small bright features, which is what a tie between two
primitives at near-equal distance looks like when two traversals order them
differently.

### Still open

- **Resize.** Buffers are sized at `New` from the initial dimensions. The game
  is fixed-resolution so nothing hits this, but a resizable target needs
  reallocation, as `internal/webgpu` does.
- **Pipelining.** The wgpu path overlaps CPU and GPU with `SetPipelined(true)`;
  the Metal path blocks on `waitUntilCompleted` every frame.
- The `default.toml` specks above.

## What it costs

One shading codebase, two traversals, and a generated artifact in the middle
that can drift. Smaller than the original estimate: shading and the primitive
intersectors are both generated, so there is no second copy of either. The failure mode is not "two renderers diverge" but "naga
changed and the splice missed", which a golden test catches immediately and the
byte-compare parity harness catches thoroughly. Both should be CI jobs from the
first commit.

It also forfeits the browser for that backend. Whether that matters depends on
whether anyone intends to ship this on the web; if so, WGSL stays the reference
implementation and Metal is the fast path on Apple hardware.

## What the earlier measurements said, and which held

Kept because the reasoning is instructive and two of the three conclusions are
still load-bearing. The third was the whole case for doing this, and it was
wrong.

### The gate passed, in a lean kernel

`tools/metal-rt/bench.swift` builds a `primitive_acceleration_structure` over
office-sunset's 1,522 primitive AABBs and traces the atrium view's 163,840
primary rays, generating them exactly as `pixel_ray_dir` does.

| | |
|---|---|
| ours, `main` gutted to `nearest_hit`, `-aa=false`, best of 3 | 2.5 ms — **65.5M rays/s** |
| Metal intersector, same rays, same geometry | 0.272 ms — **601.9M rays/s** |

9.2x. Three confounds, all favouring Metal: the benchmark's scene is flattened
while ours runs a TLAS/BLAS split; its intersection function is a slab test
against the primitive's own box rather than our full `hit_prim`; and — the big
one — the bench kernel compiles at 1024 threads per threadgroup where our
megakernel is pinned at 384. Back out the 2.7x occupancy difference and roughly
3x residual remains, which is plausibly Apple's build quality plus the flat
scene. Real, but a third of the headline.

### The codegen pipeline works

`tools/metal-rt/mslpatch` takes the linked WGSL and emits patched MSL. The
mechanism from the section above is confirmed: a dummy binding referenced inside
the traversal entry points gets threaded by naga into `nearest_hit`,
`blocker_bvh_any_hit`, `ray_color`, `supersample_edge` and the kernel entries,
with every call site passing it by name. The patch retypes that one parameter and
swaps two function bodies. **No call site is edited**, and all ten kernels —
shading, both reflection-filter passes, the four penumbra passes, `refl_fill`,
`aa_classify`, `aa_resolve` — come through as naga's output and compile.

Two things it taught us that the plan did not anticipate: naga targets metal1.0
by default, which predates the raytracing headers, so it needs
`--metal-version 3.0`; and at 3.0 it emits
`[[max_total_threads_per_threadgroup(64)]]`, which pins any occupancy reading to
the declared threadgroup size and has to be stripped before measuring.

### The register claim did not survive

This was the reason to do any of it, and it is wrong.

| Patched kernel | main_ |
|---|---|
| `nearest_hit` replaced by Metal's intersector | 384 |
| both `nearest_hit` and `blocker_bvh_any_hit` replaced | 384 |
| both replaced **and** `shade_diffuse` gutted to `return alb` | **384** |

Unmoved. The earlier probe that showed 704-1024 measured *synthetic* kernels
whose only content was an intersector and a shading-shaped tail. Our real shader
has more at the same height: the plateau documented in
[tools/occupancy/](../tools/occupancy/README.md) has contributors beyond
traversal, and removing traversal simply reveals the next one. Neither traversal
nor shading is the peak; something else in `ray_color` is, and it has not been
found.

So a Metal backend built on this inherits 384, not 704. That conclusion was
correct and the backend does inherit it.

The recommendation drawn from it -- "a single-digit frame win, not a trade worth
making" -- was wrong, because the premise named the wrong mechanism. Registers
were never what this buys. Scratch is: the six traversal stacks disappear, and
sizing those stacks alone was once worth 7% of frame time. The measured result
is 27-28%, on a frame that matches pixel for pixel.

The lesson generalises past this document. A prediction can be built on a
measurement that is itself sound -- 384 really is unmoved -- and still be wrong,
because the quantity being measured was not the one that governs the outcome.
The register probe answered its question correctly and the question was the
wrong one.

## Failure modes that are silent, and cost days

Four separate bugs this pass shared one signature: **no compile error, no
validation error, rays quietly miss, and the frame gets faster.** Metal API
validation and GPU validation were enabled throughout and reported none of
them. Anyone extending this should treat "a pass appears to do nothing" or "it
got faster" as a binding failure until proven otherwise.

1. **Intersection function tags must match the intersector.**
   `[[intersection(bounding_box)]]` is callable only through a primitive
   acceleration structure; `[[intersection(bounding_box, instancing)]]` only
   through an instance one. A mismatch is not an error -- Metal simply never
   calls the function. Symptom: every ray misses, nothing renders, `main_`
   reports 2.5 ms instead of 12.5.

2. **Intersection function tables are per pipeline.** A table is created from a
   pipeline and its function handles are valid only for that pipeline. Sharing
   one across kernels leaves the others dispatching to nothing. Symptom: AA's
   supersample rays returned sky, putting a bright halo on every silhouette.

3. **Threadgroup memory is bound by the host, or it is zero.** naga emits it as
   an unindexed argument, exactly as it does buffers, so without
   `setThreadgroupMemoryLength:atIndex:` every shared read returns 0. Symptom:
   `aa_classify`'s per-tile counter stayed 0, the task list stayed empty, and
   anti-aliasing was silently off -- `groups=1 tasks=0`, two values that
   disagree and should have been read as a contradiction sooner.

4. **`opaque = YES` on bounding box geometry skips the intersection function**,
   which is the entire mechanism for procedural primitives.

And one that was not Metal's fault at all: the buffer dump silently dropped
every upload made before the bind group existed, which zeroed the Perlin
permutation table. `fbm()` and `perlin()` then returned constants and every
procedural texture -- marble, mottle, grain, stain -- rendered as flat colour.
It looked exactly like a shading bug in another backend, hundreds of lines from
its cause.

### Metal constraints worth knowing

- **Buffer indices must be 0..30, and the megakernel is at the ceiling.**
  `aa_resolve` takes 30 arguments plus `rt_handle` at 30: exactly 31, no
  headroom. This is why the acceleration structures and tables arrive through an
  argument buffer rather than their own indices, and why it matters that an
  intersection function table has its own argument space.
- **A bounding box intersection function may not read `[[instance_id]]`**, so a
  per-instance base table is unavailable inside it. Global indices ride in the
  structure as `[[primitive_data]]`, which needs no intersector tag --
  `intersection_result` carries the member directly. The Metal headers say so
  where the online documentation does not; they are worth reading.

### How to check it still works

    ./tools/metal-rt/verify.sh        # codegen, structures, splice integrity
    go run ./tools/metal-rt/harness -dir <dir> -probe

`-probe` traces a handful of rays against both a BLAS directly and the TLAS, and
reports each. That split is the only thing that separates "our structures are
wrong" from "our tags are wrong", and it is how bug 1 above was finally found.

