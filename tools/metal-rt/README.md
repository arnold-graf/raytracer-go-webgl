# Verifying what exists today

The backend is not finished -- the Swift harness that builds the structures and
dispatches is still missing, so there is no frame time yet. Everything up to
that point runs and checks itself:

    ./tools/metal-rt/verify.sh            # artifacts land in /tmp/metal-rt-verify

Seven checks, none of which need Xcode, a GPU capture, or a GPU at all beyond
rendering two frames:

1. Unit tests for the acceleration-structure extraction: leaf coverage, the
   instance numbering the generated MSL decodes, and the world->local /
   object->world transform round trip.
2. `accelgen` over office-sunset, asserting every packed primitive lands in a
   structure -- 966 of 966. A missing leaf is geometry that stops existing on
   the Metal path.
3. The upload dump is render-neutral: the same frame with and without
   `RT_DUMP_BUFFERS` must be bit-identical.
4. `mslpatch` turns the linked WGSL into MSL with the traversal spliced.
5. That MSL compiles at `-std=metal3.0` and links into a metallib carrying
   `main_`, `prim_isect` and `blocker_isect`.
6. The splice is surgical -- terrain and water are still called after it. This
   one exists because an earlier version replaced all of `nearest_hit` and
   silently dropped planes, terrain and water, which made the shader both
   faster and wrong.

To read the result rather than trust it, the spliced function is the clearest
single artifact:

    awk '/^Hit rt_nearest\(/{f=1} f{print} f&&/^}/{exit}' /tmp/metal-rt-verify/rt.metal

Everything around it in that file is naga's output from the same WGSL the wgpu
backend runs, unmodified.

---

# Would Metal's own intersector give us the registers back?

The megakernel is pinned at **384 of 1024** threads per threadgroup, and
[tools/occupancy/](../occupancy/README.md) traces that to the BVH traversal —
where it is a plateau, with several paths at the same pressure and no single one
to fix. `hit_prim`, the inlined primitive intersectors, is one of them.

Metal's ray-tracing API offers a structural answer rather than a tuning one: the
traversal runs inside `raytracing::intersector`, and per-primitive tests are
*separately compiled* functions reached through an `intersection_function_table`.
Neither is part of our kernel's register allocation.

Measured on an M2 Max with `tools/occupancy/occ`:

| Kernel | maxThreads/TG |
|---|---|
| our `nearest_hit`, nothing else | 384 |
| our full megakernel | 384 |
| `rt_traverse` — Metal triangle intersector, traversal only | 768 |
| `rt_bbox` — Metal bbox geometry + intersection function, traversal only | **1024** |
| `rt_traverse_shade` — triangles + 4 segments x 8 lights with shadow rays | 576 |
| `rt_bbox_shade` — bbox + the same shading tail | **704** |

Reproduce with:

    swiftc -O tools/occupancy/occ.swift -o /tmp/occ
    /tmp/occ tools/metal-rt/bbox.metal

`prim_isect` reports "pipeline failed" and should: it is an intersection
function, not a kernel, so there is no compute pipeline to make from it.

## Reading these honestly

**The bbox path is the one that matches our semantics.** Our primitives are
procedural — boxes with CSG holes, spheres, cylinders, cones, tori, rings,
lenses — not triangles. `primitive_acceleration_structure` over bounding boxes
with an intersection function per kind keeps them procedural and keeps the scene
format. The triangle numbers are there only as the other half of the comparison.

**The shading tails are synthetic and lighter than ours.** Four segments, eight
lights, a shadow ray each, no textures, no materials, no campfires, no glass
Fresnel, no shadow record. Real shading would pull 704 down. But our own shading
measured as *not* being the peak — gutting `shade_diffuse` to `return alb` left
384 untouched — so the headroom is real even if 704 is optimistic.

**This is software traversal.** M2 has no ray-tracing hardware; M3 and later do,
and would add hardware traversal underneath the same API. So this is a floor for
Apple silicon, not a ceiling.

**Registers are not throughput**, and the table above does not survive contact
with the real shader. Both follow-ups were run:

## Throughput: 9.2x, in a lean kernel

`bench.swift` builds a `primitive_acceleration_structure` over office-sunset's
1,522 primitive AABBs and traces the atrium view's 163,840 primary rays,
generated exactly as `pixel_ray_dir` does.

    go run ./tmp/aabb scenes/office-sunset/index.toml /tmp/scene.bin   # see docs
    swiftc -O tools/metal-rt/bench.swift -o /tmp/mtlbench
    /tmp/mtlbench /tmp/scene.bin tools/metal-rt/bench.metal 200

| | |
|---|---|
| ours, `main` gutted to `nearest_hit`, `-aa=false` | 2.5 ms — 65.5M rays/s |
| Metal intersector, same rays | 0.272 ms — **601.9M rays/s** |

Confounds, all favouring Metal: the benchmark's scene is flat where ours runs a
TLAS/BLAS split, its intersection function is a slab test rather than our full
`hit_prim`, and its kernel compiles at 1024 threads per threadgroup against our
384. Back the occupancy out and roughly 3x residual remains.

## Registers: the claim is wrong for our shader

`mslpatch/` generates the patched MSL from the linked WGSL. Measured on it:

| Patched kernel | main_ |
|---|---|
| `nearest_hit` replaced by Metal's intersector | 384 |
| both `nearest_hit` and `blocker_bvh_any_hit` replaced | 384 |
| both replaced **and** `shade_diffuse` gutted | **384** |

The 704-1024 numbers above are synthetic kernels containing nothing but an
intersector and a shading-shaped tail. Our real shader has other things at the
same height — the plateau in [tools/occupancy/](../occupancy/README.md) — so
removing traversal only reveals the next contributor. Neither traversal nor
shading is the peak, and what is has not been found.

See [docs/metal-backend.md](../../docs/metal-backend.md) for what that does to
the proposal.
