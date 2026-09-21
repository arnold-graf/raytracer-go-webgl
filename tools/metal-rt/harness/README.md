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

Geometry and camera are correct -- the frame matches the reference's
composition. Lighting does not: the blocker splice returns
`float2(res.distance, 0.0)`, treating every occluder as fully opaque, and this
scene is largely glass. **No frame time from this tool is comparable until that
is fixed**; `main_` currently reports ~12.5 ms at 1024x640 while rendering a
darker image than the reference, and a wrong image is not a measurement.
