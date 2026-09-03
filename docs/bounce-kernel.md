# Separate Compiled Bounce Kernel

**Status:** design only. Not implemented.  
**Audience:** whoever picks up the remaining reflection cost on office-sunset.  
**Constraint:** perceptual parity. Reflections must still contain the same
objects as the primary view, including instanced trees. Skipping geometry in
the bounce is not an acceptable trade; it was tried and reverted.

This is a narrower, occupancy-first version of the wavefront idea in
[reflection-optimization.md](reflection-optimization.md) (Option A). It is
shaped by the 2026-09 megakernel pass: the AA classify/resolve split already
proved that *moving work out of the fat kernel* wins, and shader specialization
already proved that *compiling unused paths out* wins. Combining those two on
glossy bounces is a remaining large lever.

---

## Why this, and why now

Ablation at yaw 270, 512×320, bounce depth 4, adaptive AA on:

| Config | GPU |
|---|---|
| all on | 11.9 ms |
| mirror off | 6.2 ms |
| shadow off | 11.8 ms |

Reflection transport is still ~5.7 ms, half the frame. Shader counters for the
same view:

| Spawn | Count / frame | Isolated disable |
|---|---|---|
| true mirror / metal | 533 | — |
| glass | 14,870 | −1.0 ms |
| glossy diffuse (`Surface.Reflect`) | 117,955 | −3.5 ms |
| path segments | 2.0 / pixel | |

Almost every pixel fires one glossy bounce. Extra bounce *depth* is free
without AA (depth 1 and depth 4 both 7.6 ms with AA off): workgroups are already
waiting on the slowest glass lane, so adding more work to other lanes does not
move the wall clock. The cost is the **first bounce's extra `nearest_hit`**,
mixed into the same megakernel as the coherent primary ray.

Glass is 9% of pixels and ~1 ms. Glossy is 72% of pixels and ~3.5 ms. True
mirrors are noise. Any design that spends its complexity on glass-forking
wavefronts is solving the smaller problem.

---

## Why not just cheapen the bounce in `ray_color`

Every attempt to do less work on bounce rays *inside* the existing megakernel
either showed, or lost the win to occupancy. Register allocation is global to
the shader module: a branch you take on 72% of pixels still charges its live
working set to the 28% that do not, and combining two “winning” branches can
be slower than either one.

Measured, then reverted or not shipped:

| Experiment | Yaw 270 | Why it died |
|---|---|---|
| Skip instance TLAS on all bounce rays | 9.2 ms (−2.7 ms) | Trees vanish from reflections *and* from the view through windows. Visually wrong. |
| Skip instances except `RAYSEG_TRANSMIT` (glass refraction still tests the TLAS) | 9.5 ms (−2.4 ms) | Through-glass trees return; trees (and any other instance) still vanish from floor sheen and from the *reflected* lobe of a window. Still disorienting. **Reverted.** |
| `shade_ghost` lighting on depth > 0 | 8.8 ms (−3.1 ms) | Reflections go ambient-only. Obvious. |
| AA taps skip glossy spawn | 10.2 ms (−1.7 ms) | Fine alone. Combined with the instance skip the megakernel got *slower* (~11.6 ms). Register union. |
| Glossy only on 2×2 leaders, no sharing | 10.5 ms (−1.4 ms) | Glass still owns the workgroup tail, so 4× fewer glossy rays barely move wall time. Looks like a grid without sharing. |
| Throughput-gated instance skip (`tw < 0.5`) | 10.0 ms (−1.9 ms) | Keeps through-glass; still drops instances from glossy sheen. Same visual class as the transmit-tag variant. |

The pattern: **work reduction inside the fat kernel is fighting the compiler,
not the GPU.** AA compaction worked because the expensive pass became a
*different dispatch* over a compacted list. Shader specialization worked
because unused paths were compiled out of the pipeline entirely. A bounce
kernel has to do both.

A second `@compute` entry point in the *same* linked WGSL module is not
enough. Metal / Tint still see `bounce_resolve` → `ray_color` → glass, terrain,
flame, campfire, AA, and the register file is the union. The bounce pipeline
has to be a **separately specialized compile**, the way `shaders.Specialize`
already strips `FEAT_*` for scenes that lack terrain.

---

## Design

### Split of responsibility

`main` (existing megakernel) traces the camera ray and any **glass**
lobe that is the actual view through a pane (refraction / thin-glass exit).
It does **not** spawn glossy diffuse or mirror/metal children. When a primary
hit would have pushed such a child, it writes a bounce task and stores
`lit * (1 - refl)` (or nothing, for a pure mirror) in `hdr_pixels`.

`bounce_resolve` is a second compute pipeline, compiled from a *restricted*
entry point whose call graph is:

- `nearest_hit` (static BVH + instances + planes; terrain/water only if the
  scene actually has them, via existing `FEAT_*`)
- one `shade_diffuse` (same lights, same shadows, same instances)
- sky on miss
- **no** glass fork, **no** nested glossy spawn, **no** AA, **no** flame
  march, **no** heat shimmer

One bounce, done. Nested glossy is already killed by `GLOSSY_MIN_CONTRIB` for
almost every pixel; dropping it in this kernel is the same cull, just
structural. If a glossy ray hits glass, shade it as the depth-cap fallback
already does (diffuse), or optionally push a single un-forked continuation —
that is a v2 question and must not reintroduce glass into the register file
of v1.

Mirror/metal primary hits are rare (533/frame) and can go through the same
task list: the bounce kernel traces one reflection ray and shades whatever it
hits.

### Why instances stay

The instance skip was the largest cheap win and the wrong one. Floor sheen
and window reflections that omit trees (or NPCs, or any other instanced
prop) read as a hole in the world. The bounce kernel traces the **same**
`nearest_hit` as today, including the TLAS. The speedup is occupancy and
coherence, not missing geometry.

### Task list

Mirror the AA plumbing (`aa_list` / `aa_dispatch` / `aa_indirect`):

```
struct BounceTask {
    ro: vec3<f32>,     // offset origin (already nudged off the surface)
    pixel: u32,        // linear index into hdr_pixels / pixels
    rd: vec3<f32>,     // reflection direction
    _pad0: u32,
    tw: vec3<f32>,     // lobe throughput (primary tw * refl, or alb*0.96 for mirror)
    _pad1: u32,
};
```

48 bytes. Worst case one task per pixel (plus a handful of mirrors) →
`maxDim * maxDim * 48` bytes, same capacity argument as `aa_list`. Append
with `atomicAdd` on a dispatch header `[workgroups_x, 1, 1, task_count]`,
copy to an indirect buffer, dispatch. WebGPU still forbids using one buffer
as writable storage and as the indirect source in the same pass, so the
copy stays.

One pixel produces at most one glossy task from the primary hit. Glass
reflect+refract stays in `main`, so we never have two bounce threads writing
the same pixel. `hdr_pixels[pixel] += tw * incoming` is then a plain store,
not an atomic.

### Frame order

Today:

```
pass 1:  main + aa_classify
copy     aa_dispatch → aa_indirect
pass 2:  aa_resolve
```

Proposed:

```
pass 1:  main            // primary + glass; append BounceTask; write hdr without sheen
copy     bounce_dispatch → bounce_indirect
pass 2:  bounce_resolve  // add sheen into hdr; rewrite pixels
pass 3:  aa_classify     // luma now includes sheen, so shadow/glass edges classify correctly
copy     aa_dispatch → aa_indirect
pass 4:  aa_resolve
```

`aa_classify` must run *after* bounce, otherwise edge detection sees a
sheen-less image and AA will fight the composite. `aa_resolve` currently
calls full `ray_color` (primary + bounces). Leave that as-is for v1: AA is
already compacted to edges, and capping AA-tap glossy was previously visible
on glass. A later pass can point AA taps at the bounce kernel too; do not
couple that to v1.

### Compilation

`buildPipelines` already compiles one WGSL module three times, once per entry
point (`main`, `aa_classify`, `aa_resolve`), after `shaders.Specialize`
rewrites `FEAT_*`. Add a fourth entry point, `bounce_resolve`, and specialize
it further:

- Force `FEAT_FLAME` / heat-shimmer off: bounce rays do not origin-shift.
- Keep `FEAT_CAMPFIRE` / lights / instances / shadows: sheen has to light
  correctly, including trees.
- Glass code must be unreachable from `bounce_resolve`. The practical way is
  a separate WESL module (`bounce.wesl`) that calls `nearest_hit` and
  `shade_diffuse` and does **not** import `ray_color`. If the linker still
  pulls glass in through `shade.wesl`, split shade the way terrain is already
  gated with `FEAT_*`.

Verify with the compiler, not by hoping: if `bounce_resolve`'s pipeline
report (or a one-off Tint dump) still contains `box_holed_nearest` / Fresnel
forks / `aa_classify`, the split failed and occupancy will not move.

Same bind group as today. New bindings for `bounce_list` and
`bounce_dispatch` (the AA pair is 28/29; 30/31 are free as of this writing —
confirm against `types.wesl` at implementation time, especially after the
`idx_tables` packing).

### `ray_color` change

When a depth-0 diffuse/checker hit would spawn glossy:

```
accum += tw * lit * (1 - refl)
// instead of stack.push(reflect):
stash BounceTask { ep, reflect(rd,n), tw * refl, pixel }
```

When a depth-0 mirror/metal hit would spawn:

```
stash BounceTask { ep, reflect(rd,n), tw * alb * 0.96, pixel }
// accum stays 0 for that segment
```

Glass is unchanged. Nested glossy from bounce hits is dropped in v1
(`GLOSSY_MIN_CONTRIB` already removes almost all of it).

`main` needs the pixel index to fill `BounceTask.pixel`. It already has
`gid`.

### Expected gain, not a promise

Back-of-envelope from the measurements, not a commitment:

- `main` without glossy should look like the “no-glossy” experiment: **8.4 ms**
  at yaw 270, still including glass and AA.
- After the split, `main` no longer waits on glossy lanes, so it should sit
  closer to **mirrors-off plus glass**, i.e. somewhere in the 6.2–8.4 ms
  band, plus a cheap append.
- `bounce_resolve` runs ~118k full `nearest_hit` + `shade_diffuse` in a
  kernel whose register file is only that. 72% of the screen, but every lane
  in a bounce workgroup is a bounce lane (the AA lesson). If occupancy is
  even modestly better than the megakernel, this pass should be well under
  the 3.5 ms glossy currently costs *inside* the fat shader.

A result that does not beat ~9.5 ms mean, or that reintroduces missing
geometry, is a failed experiment — revert, same as the instance skip.

---

## Visual acceptance

v1 is allowed to differ from today in exactly these ways:

- Nested glossy-inside-glossy (already below `GLOSSY_MIN_CONTRIB` for weak
  reflectors) may disappear entirely.
- A glossy ray that hits glass is shaded as the depth-cap diffuse fallback
  instead of forking. Check the skyway panes looking into other panes.
- AA taps still trace the old full `ray_color`, so glass silhouettes stay
  as they are. Floor sheen AA may be slightly inconsistent with the
  center sample until AA is wired to the bounce kernel.

v1 is **not** allowed to:

- Drop instances, holes, or static prims from a bounce hit.
- Replace bounce lighting with ambient / unshadowed / `shade_ghost`.
- Skip shadows on bounce. (They are already cheap; `SHADOW_SKIP_LEVELS`
  handles the invisible ones.)

Gate with `tmp/perf/suite.sh` against a freshly recorded reference from the
pre-bounce-kernel tree. The 8× amplified diff must not light up tree
canopies in windows or on floors. Mean abs error and `>8 levels` are the
numbers that matter; raw “pixels differing” is dither.

---

## Implementation sketch

1. `BounceTask` + buffers + bindings, cloned from the AA list/dispatch/indirect
   trio. Reset the header each frame to `[0, 1, 1, 0]`.
2. Extract `bounce.wesl`: `bounce_trace(ro, rd) -> vec3` = nearest_hit +
   shade one hit + sky. No stack. Confirm glass/AA stay out of its graph.
3. `ray_color` defers depth-0 glossy/mirror to a `TraceResult` stash
   (origin, dir, tw) instead of pushing. `main` appends the task.
4. `submitTrace` grows to the four-pass order above. `bounce_resolve` adds
   into `hdr_pixels` and calls the existing `write_pixel`.
5. `buildPipelines` creates `bouncePipeline` from the specialized module,
   entry `bounce_resolve`.
6. Measure yaw 0/90/180/270 with `suite.sh`. If occupancy did not move,
   dump the bounce pipeline’s compiled size / bind the HUD counters to a
   bounce-only profile pass before adding more smarts.
7. Only then consider: AA taps using the bounce kernel, a second bounce
   wave for the rare nested case, or scene-specialized bounce (`FEAT_*`
   already handles “no campfire”).

Do not start with a general streaming wavefront. Glass forking, multiple
waves, and per-depth compaction are how Option A became “weeks.” This
design is one extra pass, one task per glossy pixel, same hit/shade as
today.

---

## Related

- [megakernel-optimization.md](megakernel-optimization.md) — AA compaction,
  occupancy lessons, why leaf widening failed, the reverted instance skip.
- [reflection-optimization.md](reflection-optimization.md) — original Option A
  (full wavefront) vs Option B (cheaper bounce shadows; B1 shipped).
- [shader-specialization.md](shader-specialization.md) — `FEAT_*` compile-out,
  the mechanism bounce_resolve has to use.
