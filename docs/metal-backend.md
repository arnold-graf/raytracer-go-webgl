# A Metal backend, and why the intersector is the reason

**Status:** proposal, with one measurement behind it and no code.
**Audience:** whoever decides whether this renderer gets a second backend.

---

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
3. **Shading.** Port `shade.wesl` and the segment loop. Largest chunk by volume,
   least interesting: it is a transliteration, and shading was measured not to be
   the register peak in the first place.
4. **The screen-space passes.** The penumbra filter, the reflection filter and
   adaptive AA are ordinary compute over `ShadowAux` and need no ray tracing. All
   four already sit at 1024 threads per threadgroup, so they are pure porting
   work with nothing to gain — and a reason to consider leaving them in WGSL
   behind a shared buffer rather than duplicating them.

## What it costs

Two renderers to keep in step, and the WGSL path stops being the only truth —
every visual regression becomes "on which backend?". The parity harness that
already exists for shader changes (dump both, byte-compare) is the mitigation,
and it should be a CI job from the first commit rather than an afterthought.

It also forfeits the browser for that backend. Whether that matters depends on
whether anyone intends to ship this on the web; if so, WGSL stays the reference
implementation and Metal is the fast path on Apple hardware.

## The honest uncertainty

Nothing here has traced a real scene. The register numbers are strong and they
point at the measured bottleneck, but the step from "2.7x the resident threads"
to "meaningfully faster frame" is exactly the step the 4-wide BVH failed to make
— it cut node visits 16% and bought 1%.

So step 1 is not a commitment to steps 2-4. Build the acceleration structure,
trace primary rays, compare rays/second. A day's work, and it either justifies
the rest or it does not.
