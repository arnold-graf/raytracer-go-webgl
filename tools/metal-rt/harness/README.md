# harness — the megakernel on Metal's acceleration structures

Runs `main_` against `MTLAccelerationStructure` instead of our BVH. It packs
nothing and ports nothing:

- **Scene bytes** come from the WGSL backend's own upload path
  (`RT_DUMP_BUFFERS`, see `internal/webgpu/bufdump.go`), bound verbatim.
- **The shader** is naga's translation of the same WGSL with only the traversal
  spliced (`../mslpatch`).
- **Primitive tests** are naga's generated `intersect()`, called from the
  generated intersection function.

So the only thing that differs from the WGSL backend is who walks the tree.

## Running it

```sh
D=tmp/$(date +%F)-metal
go run ./tools/metal-rt/accelgen scenes/office-sunset/index.toml $D/accel.bin
RT_DUMP_BUFFERS=$D/bufs go run ./cmd/gpuprof -scene scenes/office-sunset/index.toml \
    -w 1024 -h 640 -cam-x 44.9 -cam-y 201.3 -cam-z 33.1 -yaw-deg 91.3 -pitch-deg -0.52 \
    -warmup 1 -frames 1 -ablate=false -quant 3 -dump $D/ref.rgba
go run ./tools/metal-rt/mslpatch internal/webgpu/shaders/trace_linked.wgsl $D/rt.metal
go run ./tools/metal-rt/mslbind $D/rt.metal $D/bound.metal
xcrun -sdk macosx metal -std=metal3.0 -c $D/bound.metal -o $D/bound.air
xcrun -sdk macosx metallib $D/bound.air -o $D/bound.metallib
go run ./tools/metal-rt/harness -dir $D -out $D/metal.rgba
```

`-probe` traces a handful of rays and exits, `-v` prints structure sizes, and
`-maxinst N` caps placements. Those exist because of what follows.

## The instancing tag, which cost a day

An intersection function's tags must match the intersector that calls it:

```metal
[[intersection(bounding_box)]]                                   // primitive AS only
[[intersection(bounding_box, metal::raytracing::instancing)]]    // instance AS only
```

A mismatch is **not a compile error and not a validation error**. Metal simply
never calls the function. Every ray misses, nothing renders, and the frame gets
*faster* — which is the most dangerous shape a bug can have in this project.

The bisection that found it is still in the tool, because it is the only thing
that separates the two cases:

```
  selftest (1 box, 1 identity inst) BLAS=HIT  TLAS=miss   # untagged
  selftest (1 box, 1 identity inst) BLAS=miss TLAS=HIT    # tagged
```

The tag flips which one works. `-probe` reports both, so if traversal ever goes
quiet again, one run says whether the structures or the tags are at fault.

Metal's API validation and GPU validation were both enabled throughout and
reported nothing.

## Other things that are silent when wrong

- **`opaque = YES` on bounding box geometry** skips the intersection function,
  which is the entire mechanism for procedural primitives.
- **Buffer indices must be 0..30.** The megakernel is already at the ceiling, so
  the acceleration structures and tables reach the shader through an argument
  buffer rather than their own indices. An intersection function table has its
  own argument space, which is why the intersection functions' buffers are free.
- **`functionHandleWithFunction:` returning nil** is accepted by
  `setFunction:atIndex:` without complaint and yields a table that dispatches to
  nothing. The harness checks it.

## Status

All ten kernels dispatch, in the order `internal/webgpu/device.go` submits them:
`main_`, the reflection filter, the separable penumbra filter and
`aa_classify` in one encoder so each sees the last one's writes, then
`aa_resolve` indirectly from the task list `aa_classify` built. **11.0 ms at
512x320.**

That number is still not comparable, because 29.8% of pixels differ from the
WGSL backend by >=8. What the difference is *not*:

- **Not the screen-space passes.** Running them moved the gap by 0.6 points
  (30.4% -> 29.8%). Measured from the other side, turning AA off in the WGSL
  backend changes only 2.3% of pixels at this resolution.
- **Not shader specialization.** `RAYTRACER_NO_SHADER_SPECIALIZE=1` renders
  bit-identically to the specialized pipeline, so the all-features shader that
  `mslpatch` compiles is the right reference.
- **Not missing instance data.** Forcing `inst_idx` to `HIT_NO_INSTANCE` moves
  45% of pixels, so instanced shading is live and doing work.

What it looks like: the difference concentrates on the columns and the glass
rather than spreading evenly, and the Metal frame is slightly *brighter*
(mean 163.7 against 160.8). Brighter, on the repeated instanced geometry,
points at shadow rays missing occluders they should find -- the blocker TLAS
rather than the geometry one. That is the next thing to bisect, and `-probe`
already traces the geometry structure; it needs the blocker equivalent.

Reflection and refraction needed no work: both live in `ray_color`'s segment
loop, which calls `nearest_hit` and so the spliced traversal.

Shadow transmission did. Glass does not end a blocker walk in the WGSL -- it
multiplies transmission and continues -- which a flat `accept_any_intersection`
cannot express, so the accumulation lives in the intersection function in a
`ray_data` payload. Verified by forcing every blocker to transmit, which moves
16.7% of pixels. Glass-only is a no-op on this view: 54 glass blockers exist,
but no shadow ray in this frame crosses one.

### Still missing

- Refit. `refitAccelerationStructure:` is the analogue of `bvh_refit.go`;
  instance transforms only need the TLAS rebuilt, deforming poses need a real
  BLAS refit. Nothing here blocks it; the harness just builds once.
