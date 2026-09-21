# spills — where the megakernel spills to thread scratch

Scratch is the resource that has repeatedly moved this renderer: taking an array
out of `box_holed_nearest` was 14%, sizing the traversal stacks 32→24 was 7%.
No Metal API exposes it. `tools/occupancy` reads
`maxTotalThreadsPerThreadgroup`, which is a *register* ceiling and is blind to
scratch — and it is pinned at 384 structurally, so it cannot rank anything.
Until now the only instrument was frame-time A/B on one constant at a time.

The AGX backend emits LLVM register-allocator remarks, and a GPU frame capture
carries them into the `.gputrace` package:

```
--- !Missed
Pass:            regalloc
Name:            SpillReloadCopies
DebugLoc:        { File: program_source, Line: 12334, Column: 1 }
Function:        agc.main
Args:
  - NumSpills:       '454'
  - TotalSpillsCost: '2.346769e+03'
  - NumReloads:      '1124'
```

They name a source line, so they are a *targeting* instrument, not just a number.

## Running it

```
./tools/spills/run.sh [outdir]      # default /tmp/spills
```

Link → `naga --metal-version 3.0` → `mslbind` (assigns real buffer indices to
naga's `[[user(fakeN)]]` placeholders) → `capture.swift` (builds every pipeline,
dispatches one threadgroup with zeroed params so all threads exit on the bounds
check) → scan the trace for remarks, aggregate per line, map each line to its
enclosing MSL function.

Needs `naga` on `$PATH` or `$NAGA`, and `swiftc`. Nothing opens Xcode.

## Baseline (office-sunset, 2026-09-21)

```
total: 3287 spills, 9140 reloads, cost 8480

 MSL line   spills  reloads       cost   share  enclosing function
     9709      854     2199       2423   28.6%  ray_color()
    12334      454     1124       2347   27.7%  aa_resolve()
    10823      425     1115       2332   27.5%  supersample_edge()
    11336      503     1158        290    3.4%  main_()
     9471      197      387        287    3.4%  shade_diffuse()
```

All three of the top lines are the same code: `ray_color`'s segment loop. The AA
path pays for it twice more — `supersample_edge` inlines it and `aa_resolve`
calls it. **84% of all spill cost is that one loop, charged three times.** That
is the mechanism behind AA costing ~25% of the frame.

## What it has ruled out

- **ShadowCore (carry 72 B instead of 192 B).** 3287→3236 spills, cost
  8480→8472: −1.6%, and frame time was neutral. The `ShadowAux` payload is not
  what spills.
- **`MAX_SEGS` 6→2.** Spill counts *byte-identical*. The `array<RaySeg, MAX_SEGS>`
  stack is dynamically indexed, so AGX places it in scratch by construction — it
  is never a register, so it can never be a *spill*. (Frame time drops 27% at
  MAX_SEGS=2 only because two segments drop ray-tree lobes and render a different
  image. Not a win.)

So the spilled state in that loop is everything *else* live across the calls:
the accumulators, the masks and throughput, the primary `Hit`, the lobe
bookkeeping, the reflect/refract parameters. Many small values, not one big one
— the same "broad plateau" the occupancy probe found, but now with a size and an
address.

## Caveat

The remarks describe the *compile*, not the run: they are static counts from the
allocator, with no dynamic weighting by how often a line executes. Treat them as
"where pressure is", then confirm with frame time and a pixel diff as usual.
