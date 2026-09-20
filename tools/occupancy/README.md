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

## The peak is a plateau, not a hotspot

Going one level further found something that changes how to read the table above.
**Single ablation is the wrong instrument for a maximum.** Stub one contributor
and another sitting at the same height takes over, so the number does not move
and the thing you removed looks innocent. Every 384 above is subject to that.

Stripping instead, and adding back, separates them:

| Variant | main_ |
|---|---|
| hand-written stack walk, 24-entry array, no node loads | 1024 |
| hand-written traversal: node loads + `slab_hit` + child pushes | 1024 |
| real `nearest_hit`, instancing/planes/terrain/water removed | 384 |
| the same, with `hit_prim` stubbed to `T_MISS` | **512** |
| the same, with any *single* prim kind stripped | 384 |

Three things fall out. The traversal *skeleton* is not the cost — a hand-written
one with the same stack, loads and slab tests compiles at 1024. `hit_prim`, the
inlined primitive intersectors, is a genuine contributor: removing all of them
lifts 384 to 512. But no single primitive kind is responsible, and neither is
instancing, planes, terrain or water on its own — each is enough to hold 384 by
itself.

**There is no single thing to fix.** Several independent paths sit at the same
pressure, so moving the number means lowering several at once. That is a much
worse prospect than one hot spot, and it is worth knowing before anyone spends a
week on it.

Restructuring attempts that did not move it, both bit-identical:

- Vectorizing `slab_hit` / `slab_near` / `slab_range` — six scalar temporaries
  and three compare-and-swap branches become two `vec3`s and no branches. Kept
  anyway: consistently ~0.7% faster across interleaved pairs and about fifty
  fewer lines. Occupancy unchanged.
- Scoping the node loads in `bvh_child_push` so one child's bounds dies before
  the other is read, instead of holding 24 words at once. Exactly neutral; the
  compiler was already doing it. Reverted.
