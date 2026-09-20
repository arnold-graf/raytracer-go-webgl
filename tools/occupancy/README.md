# Measuring the megakernel's register pressure

`maxTotalThreadsPerThreadgroup` is the Metal compiler's verdict on how many
threads of a compiled kernel the hardware can keep resident. It falls as the
function's register use rises, so it is the closest thing to a register count
reachable without Xcode's GPU debugger — and unlike a frame capture it is a
number a script can diff.

**It is not reachable through wgpu.** wgpu validates a static `@workgroup_size`
against the *device* limit and creates the pipeline regardless of register use;
a bisection over the declared size returns 1024 for every entry point, including
a synthetic kernel built to be register-hungry. The property lives on
`MTLComputePipelineState` and needs Metal directly, which is what `occ.swift` is
for.

## Running it

    cargo install naga-cli                    # once; brew install rust first
    swiftc -O tools/occupancy/occ.swift -o /tmp/occ
    naga internal/webgpu/shaders/trace_linked.wgsl /tmp/trace.metal
    /tmp/occ /tmp/trace.metal

Output as of 2026-09-20, on an M2 Max:

    entry               maxThreads/TG  simdWidth  tgMem
    aa_classify         1024           32         0
    aa_resolve          384            32         0
    main_               384            32         0
    refl_blur_h         1024           32         0
    refl_blur_v         1024           32         0
    refl_fill           1024           32         0
    shadow_radius_h     1024           32         0
    shadow_radius_v     1024           32         0
    shadow_soften_h     1024           32         0
    shadow_soften_v     1024           32         0

The megakernel sits at 37.5% of the ceiling; `aa_resolve` matches it because it
calls the same `ray_color`. Every screen-space filter pass is unconstrained.

## Localizing the peak

The number is one scalar per compiled function, set by the *maximum* live-range
pressure over all paths. It says how high the peak is, never where. To find
where, ablate and re-measure — stub something out, relink, re-run:

    sh internal/webgpu/shaders/link.sh
    naga internal/webgpu/shaders/trace_linked.wgsl /tmp/trace.metal && /tmp/occ /tmp/trace.metal

## What that found

| Ablation | main_ |
|---|---|
| baseline | 384 |
| every `FEAT_*` stripped (`Features{}`) | 384 |
| each prim kind stripped individually | 384 |
| `BVH_STACK_SIZE` 8 / 16 / 24 / 32 / 64 / 128 | 384 |
| `MAX_SEGS` 1 / 2 / 6 | 384 |
| glass branch off | 384 |
| glossy lobe off | 384 |
| `shade_diffuse` gutted to `return alb` | 384 |
| shadow record off | 384 |
| instancing (TLAS/BLAS descent) off | 384 |
| `hit_prim` stubbed to `T_MISS` | 384 |
| **`main` gutted, no tracing at all** | **1024** |
| **`nearest_hit` alone, nothing else** | **384** |

**The whole peak is the bare BVH traversal loop in `nearest_hit`** — the stack,
`slab_hit`, `bvh_child_push` holding two nodes' bounds at once, and the `Hit`
state. Shading, glass, the glossy lobes, the shadow record, the primitive
intersectors and every optional feature all fit underneath it at no cost.

Three consequences worth carrying around:

- **Making shading leaner cannot buy occupancy.** Narrowing the carried
  `ShadowAux` from 192 to 72 bytes measured exactly neutral, and this is why —
  it was never the peak. The probe predicts that in thirty seconds.
- **`FEAT_*` specialization does not work by reducing register pressure**, which
  is what `shader-specialization.md` assumed. Stripping everything leaves the
  number unmoved. Its measured 12% comes from somewhere else — fewer
  instructions and branches, better scheduling, instruction cache.
- **Thread scratch is a separate resource and this probe is blind to it.**
  `BVH_STACK_SIZE` 32 → 24 was worth 7% of frame time and does not move this
  number at all; the stack is spill memory, not registers. Two distinct taxes,
  two distinct instruments.

So the lever for occupancy is the traversal loop, and nothing else in the kernel
is worth looking at for it.
