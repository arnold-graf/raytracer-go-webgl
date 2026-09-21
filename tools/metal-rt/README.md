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

**Registers are not throughput.** 1.5-2.7x the resident threads is a reason to
expect a win, not a measurement of one. Nothing here traces a real scene. The
next experiment, if this is pursued, is throughput: build a
`primitive_acceleration_structure` over the office-sunset prims and compare
rays/second against the 84M/s the megakernel currently achieves.
