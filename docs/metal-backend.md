# A Metal backend, and why the intersector is the reason

**Status:** measured and **not recommended on current evidence**. The codegen
pipeline and the throughput benchmark are built and in `tools/metal-rt/`; the
register premise that motivated them is disproven.
**Audience:** whoever decides whether this renderer gets a second backend.

---

> **Measured 2026-09-21, and the headline claim below did not survive.** The
> register argument is wrong for our shader: replacing *both* traversals with
> Metal's intersector leaves `maxTotalThreadsPerThreadgroup` at 384, unmoved,
> because the peak is a plateau with more contributors than traversal. What did
> survive is a 9.2x traversal throughput measurement and a working codegen
> pipeline. See "What the measurements actually said" below before reading the
> rest as a recommendation.

## The one-line case

The megakernel is pinned at 384 of 1024 threads per threadgroup by register
pressure in the BVH traversal, and that pressure is a *plateau* — several paths
at the same height, no single one to fix
([tools/occupancy/](../tools/occupancy/README.md)). Metal's ray-tracing API
dissolves it structurally rather than by tuning: traversal happens inside
`raytracing::intersector`, and per-primitive tests are separately compiled
functions reached through an `intersection_function_table`. Neither is part of
the calling kernel's register allocation.

Measured, same probe, same machine:

| Kernel | maxThreads/TG |
|---|---|
| our `nearest_hit`, nothing else | 384 |
| our full megakernel | 384 |
| Metal bbox intersector, traversal only | **1024** |
| Metal bbox intersector + 4 segments x 8 lights with shadow rays | **704** |

Details and caveats in [tools/metal-rt/](../tools/metal-rt/README.md). The short
version of the caveats: the shading tail in that 704 is synthetic and lighter
than ours; this is software traversal on M2 and hardware on M3 and later; and
registers are a reason to expect throughput, not a measurement of it.

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

## How it would integrate

**WGSL stays the single source of truth.** The insight that shapes this: `naga`
already translates our linked WGSL to MSL, and it does *not* inline
`nearest_hit` — the function survives by name, with six call sites. So the Metal
backend does not need a ported shading language. It needs a build step and a
traversal splice.

    shaders/modules/*.wesl          single source, unchanged
       |
       +-- link.sh  -> trace_linked.wgsl -> wgpu backend   (portable, stays default)
       |
       +-- naga     -> trace.metal -> splice traversal -> Metal backend

All ten kernels come through — `main`, the two reflection-filter passes, the four
penumbra passes, `refl_fill`, `aa_classify`, `aa_resolve`. The Metal backend owns
only what WGSL cannot express: the acceleration structure, one intersection
function per primitive kind, the buffer bindings, dispatch, and the splice.

That deletes the largest item from the original plan. Porting `shade.wesl` and
the segment loop was "the largest chunk by volume"; it is now generated. The
maintenance cost drops from *two renderers to keep in step* to *one shading
codebase with two traversals*.

### The splice

MSL has no module-scope resources, so naga threads every buffer through as a
parameter and `nearest_hit`'s generated signature is enormous. Do not patch
naga's internals. Control the signature from the WGSL side instead: a `FEAT_`-
style flag makes `nearest_hit` a stub that touches no buffers, so naga emits a
short function to replace wholesale, and the accel-structure argument is added at
the six call sites.

Deterministic, but version-sensitive. Pin the naga version, keep a golden test
over the generated MSL, and run the existing byte-compare parity harness between
backends in CI from the first commit — not as an afterthought.

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

## The stages

`render.Renderer` is a one-method interface — `Render(buf, cam, v, pixSize)` —
so a Metal backend is a sibling of `internal/webgpu`, not a replacement:

    internal/render     Renderer interface, View          (unchanged)
    internal/webgpu     WGSL megakernel                   (unchanged, stays the default)
    internal/metal      MTLAccelerationStructure + MSL    (new)

Four pieces of work, in dependency order:

1. **Geometry.** Build a `primitive_acceleration_structure` over bounding boxes,
   one per existing primitive, plus an `instance_acceleration_structure` over the
   placements `internal/scene` already produces. The scene format, the authoring
   and the look are untouched. This is the piece that decides everything else, so
   build it first and benchmark traversal alone against the current 84M rays/s
   before writing a line of shading.
2. **Intersection functions.** One `[[intersection(bounding_box)]]` per primitive
   kind, ported from `intersect.wesl`. These are the functions whose registers
   stop counting against the kernel. `hit_box`'s CSG-hole walk is the fiddliest.
3. **Shading.** Generated by naga from the same WGSL — no port. The work here is
   the build step and the splice, plus the binding map that tells naga's MSL
   backend which `[[buffer(n)]]` each resource takes, which we define rather than
   reverse-engineer because we own this backend's bindings.
4. **The screen-space passes.** The penumbra filter, the reflection filter and
   adaptive AA are ordinary compute over `ShadowAux` and need no ray tracing. All
   four already sit at 1024 threads per threadgroup, so they are pure porting
   work with nothing to gain — and a reason to consider leaving them in WGSL
   behind a shared buffer rather than duplicating them.

## What it costs

One shading codebase, two traversals, and a generated artifact in the middle
that can drift. The failure mode is not "two renderers diverge" but "naga
changed and the splice missed", which a golden test catches immediately and the
byte-compare parity harness catches thoroughly. Both should be CI jobs from the
first commit.

It also forfeits the browser for that backend. Whether that matters depends on
whether anyone intends to ship this on the web; if so, WGSL stays the reference
implementation and Metal is the fast path on Apple hardware.

## What the measurements actually said

Three things were built and measured: the throughput gate, the codegen pipeline,
and the register claim.

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

So a Metal backend built on this would inherit 384, not 704, and the traversal
advantage would shrink from 9.2x toward that ~3x residual — on a component that
is a minority of frame time. That is a single-digit frame win for a second
backend, which is not a trade worth making on this evidence.

## What would change the answer

Find what else is at 384. The bisection method that works is stripping and adding
back, not ablating — single ablation cannot find the argmax of a maximum. Start
from a gutted `main` (1024) and add `ray_color`'s pieces one at a time. If the
remaining contributors turn out to be few and fixable in WGSL, the megakernel
gets faster with no second backend at all, and the intersector's 3x lands on top
of a kernel that can actually use it.

That is a day of work with the tooling that now exists, and it is the honest next
step rather than building a backend on a disproven premise.

## The honest uncertainty

Nothing here has traced a real scene. The register numbers are strong and they
point at the measured bottleneck, but the step from "2.7x the resident threads"
to "meaningfully faster frame" is exactly the step the 4-wide BVH failed to make
— it cut node visits 16% and bought 1%.

So step 1 is not a commitment to steps 2-4. Build the acceleration structure,
trace primary rays, compare rays/second. A day's work, and it either justifies
the rest or it does not.
