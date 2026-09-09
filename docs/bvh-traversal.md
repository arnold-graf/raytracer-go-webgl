# One AABB test per node

**Status:** shipped. Office-sunset four-view mean **10.97 -> 9.88 ms (-10.0%)**,
outdoors-night-villa **23.1 -> 21.2 ms (-8.2%)**, and byte-identical output on
every view of both, with soft shadows on and off.
**Audience:** anyone touching `bvh.wesl`, or tempted by a stackless traversal.
**Constraint:** byte parity. Every arm below was checked with
`tmp/perf/compare.py`; the shipped one moves zero pixels.

The starting hypothesis was that the six `array<u32, 32>` traversal stacks were
costing occupancy, and that the fix was to get the descent into registers. That
hypothesis is **wrong**, and the way it is wrong is the useful part: the same
work reduction is worth -10% delivered one way and +5.5% delivered another.

---

## What was actually redundant

Every node in the tree was having its AABB tested **twice**:

- `bvh_child_push` calls `slab_near` on both children to order them near-first.
- The loop then pops a child and calls `slab_hit` on it again.

For nearest-hit search the second test earns its keep: `best_t` may have been
tightened by a primitive found while the node sat on the stack, so a node that
was worth entering at push time may not be at pop time.

For **shadow any-hit it earns nothing**. The bound there is a fixed `max_t` that
never moves, so `slab_hit` at pop re-derives exactly the decision `slab_near`
made at push. Worse, the any-hit path was not even using the first test — it
pushed both children unconditionally and let the pop reject them.

The change is to move the test to the push (`t_near < bound`, a compare against
a distance already in hand) and delete it from the pop in the two any-hit
traversals. Nearest-hit keeps its second test and is otherwise untouched.

That is most of the ray population: at yaw 270 the frame issues 883,387 shadow
rays of 1,202,083 total, 73%. Node visits fall from **31.6 to 23.9 per ray** —
partly fewer tests, partly the counter no longer seeing children that used to be
pushed only to be rejected.

| yaw | before | after |
|---|---|---|
| 0 | 9.5 ms | 8.8 ms |
| 90 | 11.4 | 10.1 |
| 180 | 8.8 | 7.8 |
| 270 | 14.2 | 12.8 |
| **mean** | **10.97** | **9.88 (-10.0%)** |

With `RAYTRACER_SOFT_SHADOWS=1` the same change is 11.85 -> 10.90 ms (-8.0%),
also byte-identical.

## A blocker index bug, found on the way

`PackBVH` numbers each section's children from zero, and `instance.go` appends
the static blocker tree into the shared node buffer verbatim. So blocker child
indices are **section-relative**, and `blocker_bvh_any_hit` was loading nodes as
`bvh_nodes[blocker_off + stack[sp]]` to compensate — but then handed the raw
child indices to `bvh_child_push`, which dereferences them to order the pair.
Every blocker traversal was therefore choosing its near/far order by reading
**the main scene's nodes** instead of its own.

It could not change a shadow boolean, because that path culls nothing: the
`cull_near = false` branch pushed both children whatever the ordering said. So
with soft shadows off it is invisible, and the fix is byte-identical on all four
views.

With soft shadows **on** it is not invisible, because penumbra width is sized
from the distance to whichever blocker the any-hit happens to find first. Fixing
the rebase moves 7.4% of pixels at yaw 270 — but only 0.04% of them by more than
8 levels, and an 8x amplified diff puts all of it on the light-grid ring and the
rack panels. Small, then, but it is the difference between near-front-to-back
order and effectively arbitrary order, which is exactly the instability
[soft-shadows.md](soft-shadows.md) flags under "any-hit vs nearest blocker".

The fix is to fold `blocker_off` in at the push and index the stack absolutely,
like every other traversal here. Perf-neutral (+0.7% to +1.4%, inside noise).

---

## Tried and reverted: descent in registers

The obvious stackless move, and the one that motivated the whole exercise. Keep
the near child in a register instead of pushing it, so the descent chain — most
of a traversal — never round-trips through thread-local memory, and enter it
without the redundant re-test:

```wgsl
loop {
    if (!have) {
        if (sp == 0u) { break; }
        sp = sp - 1u;
        node = stack[sp];
        checked = false;
    }
    have = false;
    let n = bvh_nodes[node];
    if (!checked && !slab_hit(...)) { continue; }
    ...
}
```

It works, it is byte-identical, and it cuts node visits to the same 23.9 per ray
as the shipped change. It is **+5.5%**, consistently, on all four views.

The reason is the loop shape. The original body is *branch-uniform*: every lane
pops, every lane tests, every lane processes. The rewrite splits each iteration
into a pop path and a descend path, and lanes in a 64-thread workgroup are at
different depths, so the two paths serialize. Three extra live values per
traversal (`node`, `have`, `checked`) do not help either, in a megakernel whose
register allocation is global.

**Fifteen percent** separates the two arms — the same deleted work, once
delivered by also adding a branch, once by adding nothing.

## Tried and reverted: the entry distance on the stack

The nearest-hit path still tests twice, and there is a way to fix that without
touching the loop shape: push each child's `slab_near` result alongside it, and
make the pop-time test `stack_t[sp] >= bound` — a scalar compare instead of a
second slab test. The loop stays uniform; only the test gets cheaper.

Byte-identical, and a **wash**: -9.8% against the shipped -10.2% in the same
interleaved run. The second `array<f32, 32>` costs precisely what the deleted
slab tests save.

That is the same lesson from the other side. Deleting a test paid. Deleting the
same test while adding 128 bytes of per-thread scratch paid nothing.

---

## The rule this leaves behind

**Arithmetic is not the scarce resource in this kernel. Control flow and
per-thread scratch are.** A change that only *removes* something wins. A change
that removes the same thing while adding a flag, a branch, or an array does not,
and the penalty is large enough to invert the sign.

That is consistent with everything in
[megakernel-optimization.md](megakernel-optimization.md) — the -14% from taking
one `array<f32, 8>` out of `box_holed_nearest`, the leaf-width result, the
texture stub that came out slower — and it is the reason
[bounce-kernel.md](bounce-kernel.md)'s queues lost. Deferring a lobe moves work
and adds a dispatch. This removed work and added nothing.

### What is left here

- **Branch factor, not leaf width.** The leaf-width table in
  megakernel-optimization.md answers a different question; the tree is still
  binary and ~18 deep, so traversal is a long dependent-load chain. A 4-wide
  node halves the depth. Untested, and the one BVH lever nobody has pulled.
- **A genuinely stackless traversal** (skip pointers, or Hapala parent
  pointers). Both give up near-first ordering or pay to recompute it, and the
  result above says the loop that stays uniform is the one that wins — so
  neither looks promising, but neither has been measured.

## Measuring this

`tmp/bvhwork/ab.sh` runs the arms interleaved within each round, which matters:
the arms here differ by 10% and single-sample spread on this machine was 0.6 ms.
Note also that the noise floor is not always the ~10% the other docs quote — two
identical runs came in at 10.93 and 10.95 with the machine cool and the game
closed, so a careful measurement can resolve a good deal more than 10%.

The usual trap applies and is worth repeating: **swap `modules/bvh.wesl` and
relink**, never build two binaries. `ab.sh` copies the module and runs `link.sh`
between arms for exactly this reason. Verify the arm changed the image, or the
timing means nothing.

## Related

- [megakernel-optimization.md](megakernel-optimization.md) — occupancy lessons,
  BVH leaf width, `SHADOW_SKIP_LEVELS`.
- [bounce-kernel.md](bounce-kernel.md) — why moving rays out of the megakernel
  loses, and the density/sparsity criterion.
- [soft-shadows.md](soft-shadows.md) — what the blocker distance is used for,
  and the any-hit ordering question this bug was quietly sitting inside.

## Child ordering is the shadow answer

Any-hit returns whatever it reaches first, so for shadow rays the child ordering
*is* the result — not just the speed. Ordering the blocker tree's children
correctly (they were being dereferenced with section-relative indices, so each
pair was sorted by two unrelated nodes' boxes) changed which blocker came back
and therefore every penumbra width in the scene.

The blocker traversal now orders **far-first** on purpose, so the first hit is
the widest occluder rather than the nearest. See
[which blocker sets the width](soft-shadows.md#which-blocker-sets-the-width-when-several-block-the-same-light)
for why widest is the right pick when several occluders share a light, and
[nearest-hit search](#) for why the nearest traversals keep their near-first order.
