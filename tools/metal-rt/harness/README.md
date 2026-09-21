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

All ten kernels dispatch, in the order `internal/webgpu/device.go` submits them.
Two scenes, both at the in-game 512x320:

| scene | wgpu | metal |
|---|---|---|
| office-sunset atrium | 20.0 ms | 10.8 ms |
| outdoors-night-villa | 16.8 ms | 9.8 ms |

**Neither number is a result yet**, for two reasons that both have to go away
first. The images still differ (27.2% and 32.2% of pixels by >=8), and the two
timers measure different things: gpuprof reports wall-clock until the device is
idle, including readback, while the harness reports `GPUEndTime - GPUStartTime`.
Closing both is what turns this into a measurement.

Textures are correct. The villa renders its cobblestone base, tree bark, wood
grain and grass identically to the WGSL backend, which is the clearest evidence
so far that material and texture lookups are getting the right primitive index.

What still differs, by scene: office-sunset loses the surface detail on the
nearest column; the villa's water reflection is flat where the WGSL backend's
is wavy. Both are surface-normal-shaped rather than geometry-shaped.

### Ruled out

- **The screen-space passes.** Adding all nine moved office-sunset by 0.6
  points. Turning AA off in the WGSL backend changes 2.3% of pixels.
- **Shader specialization.** `RAYTRACER_NO_SHADER_SPECIALIZE=1` renders
  bit-identically, so the all-features shader `mslpatch` compiles is right.
- **Missing instance data.** Forcing `inst_idx` to `HIT_NO_INSTANCE` moves 45%
  of pixels, so instanced shading is live.

### Fixed along the way

The blocker structures were built from the *geometry* tree. `PackBVH` numbers a
section's children from zero and `instance.go` appends the blocker tree
verbatim, so its internal nodes hold section-relative child indices while its
leaves hold absolute primitive ones -- which `blocker_bvh_any_hit` handles by
pushing `blocker_off + n.info.x` against untouched leaf slots. Reading them as
absolute walks straight back into the main tree.

It never crashed. office-sunset's static geometry indices happen to fall below
its blocker count, so the range check passed and the structure was quietly full
of the wrong primitives -- every shadow ray testing the wrong set. The villa
exposed it because its indices do not line up that way. This is also where the
"12 benign duplicates" noted earlier came from; they were never benign, and the
counts are now exact on both scenes.

### Still missing

- Refit. `refitAccelerationStructure:` is the analogue of `bvh_refit.go`;
  instance transforms only need the TLAS rebuilt, deforming poses need a real
  BLAS refit.
- A blocker-side `-probe`. The existing one only traces the geometry structure,
  which is why the blocker bug survived as long as it did.
