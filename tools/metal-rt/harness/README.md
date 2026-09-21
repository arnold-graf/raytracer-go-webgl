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

The Metal path renders **the same image as the WGSL backend** on both scenes at
the in-game 512x320, and is measurably faster.

| scene | wgpu | metal | pixels differing by >=8 |
|---|---|---|---|
| office-sunset atrium | 19.6-20.1 ms | 15.3-15.5 ms | 45 of 163840 (0.0%) |
| outdoors-night-villa | 16.6-16.8 ms | 13.0-13.4 ms | 61 of 163840 (0.0%) |

**1.28x and 1.27x**, interleaved over three rounds each, both sides measuring
the same window. Local-contrast ratios against the reference are 1.00 on the
frame, the villa's stairs and its grass.

### The timer

Comparing the harness's `GPUEndTime - GPUStartTime` against gpuprof's "gpu"
line would have been wrong: gpuprof measures wall clock from before encoding to
after the device goes idle, and includes the copy of the output buffer.
`mrt_run` now reports both, and encodes the same output copy, so
`submit-to-idle` is the number that lines up. On office-sunset the two differ
by about 0.35 ms, which is what the mismatch would have quietly handed this
backend.

The earlier "1.7-1.85x" was not that gap. It was mostly `aa_resolve` missing
all geometry and returning sky, which is cheap. Fixing it moved execution from
10.9 ms to 15.0 ms.

### The washed-out textures were a zeroed permutation table

Procedural textures -- marble, mottle, grain, stain -- are all `fbm()` and
`perlin()`, which index the permutation table at binding 9. In the dump that
table was **2048 bytes of zeros**, so every noise call returned a constant and
every procedural surface rendered as flat colour. Soft and washed out, exactly.

The bug was in the dump, not the shader. `perm` is uploaded once during device
setup, hundreds of lines before the bind group exists -- and the bind group is
where the dumper learns which binding a buffer belongs to. Writes that arrived
first were silently dropped. `bufdump.go` now journals pre-registration uploads
and replays them, which is the only way to tell "written before we were
looking" apart from "not a bind-group buffer at all".

Effect, both scenes, pixels differing by >=8:

| scene | before | after |
|---|---|---|
| office-sunset | 36.6% | 15.8% |
| villa | 32.2% | 7.6% |

And the villa's local-contrast ratios against the WGSL backend land at 1.04
whole frame, 1.02 on the cobblestone, 1.00 on the grass -- textures now match.

Worth noting how this was found, because two measurements pointed the wrong way
first. A local-contrast metric said Metal had *more* detail, not less, which is
true and irrelevant: it was dominated by aliased edges and blind to flat
interiors going smooth. A crop at 8x showed it immediately.

### Ruled out

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

### Threadgroup memory is bound by the host, or it is zero

naga emits threadgroup memory as an *unindexed* kernel argument, exactly as it
does buffers:

    , threadgroup metal::atomic_uint& aa_wg_count

With no `[[threadgroup(n)]]` index the host never calls
`setThreadgroupMemoryLength:atIndex:`, and every shared read returns zero. In
`aa_classify` that turns the per-tile counter into a constant 0, so
`atomicAdd(&aa_dispatch[3], atomicLoad(&aa_wg_count))` adds nothing and the task
list stays empty -- while `atomicMax(&aa_dispatch[0], ...)` still runs, leaving
the tell-tale `groups=1 tasks=0`. Anti-aliasing was silently off.

`mslbind` now assigns the indices and records the sizes in its manifest, and the
harness sets them. `aa_classify` goes from 0 tasks to 27921.

This is the same shape as the instancing tag: not a compile error, not a
validation error, and it makes the frame faster.

### Known bad: the AA resolve path

Turning AA on makes the image *worse*, not better -- 27.2% of pixels differing
becomes 36.6%, with a bright halo on every silhouette. `aa_resolve` blends new
samples against `hdr_pixels[idx]`, and in this chain the penumbra filter is
still a no-op, so that base colour is not what the WGSL backend supersamples
against. That is the next thing to chase, and until it is fixed the AA-off
comparison is the honest one.

### Still missing

- Refit. `refitAccelerationStructure:` is the analogue of `bvh_refit.go`;
  instance transforms only need the TLAS rebuilt, deforming poses need a real
  BLAS refit.
- A blocker-side `-probe`. The existing one only traces the geometry structure,
  which is why the blocker bug survived as long as it did.
