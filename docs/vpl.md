# Virtual point lights

**Status:** two ways in, and they are independent.

- **Authored, always on.** A scene file or object file with a `[vpl]` table gets
  those lights every launch, flag or no flag. An authored budget is scene data,
  like the walls — see [Per-region budgets](#per-region-budgets-vpl-on-an-object).
- **Global, behind `RAYTRACER_VPL`.** The flag sets the scene-wide budget for
  bounce landing *outside* every region. Unset, that pool is empty.

So a scene with no `[vpl]` anywhere and no flag is untouched and byte-identical,
which is the guarantee that was verified on the villa and office-sunset.
`RAYTRACER_VPL_REGIONS=0` additionally ignores every region, and that pair —
no flag, regions off — is the complete off switch for a scene that has opted in.

```bash
go run .                            # just what the scene's objects authored
RAYTRACER_VPL=24 go run .           # those, plus 24 scene-wide
RAYTRACER_VPL_REGIONS=0 go run .    # nothing at all
RAYTRACER_VPL=24 RAYTRACER_VPL_DUMP=tmp/villa-vpl.toml go run ./cmd/gpuprof -scene scenes/outdoors-night-villa.toml
```

**This is the only indirect-light feature in the renderer.** The probe fields —
live DDGI cascades, the baked volume, the path-traced near field — and the
authored ambient zones were all removed on 2026-09-18; their write-ups are kept
in [live-gi.md](live-gi.md), [bounce-kernel.md](bounce-kernel.md) and
[ambient-zones.md](ambient-zones.md) as the record of what each measured.

**What it does:** shoots photons from the scene's own emitters and leaves a point
light wherever one lands on a diffuse surface, so the first bounce is carried by
the light path the renderer already has.
**What it does not do:** ground bounce off terrain, or any bounce past the first.

---

## Why lights and not another probe field

[live-gi.md](live-gi.md) records four attempts at a field — an injected LPV, a
culled grid, cascaded DDGI probes, and a baked volume — and every artifact in it
is a resolution artifact. Leaking, the diamond patches, the rosettes, the pop,
the shimmer: each traced back to the same sentence, *every DDGI default assumes a
hardware ray budget*. At 56 ns a ray the field is about ten times under-sampled
against what the paper assumes, and a sharp lobe over too few rays is worse than
no sharpness at all.

**A VPL has no resolution to run out of.** It is a light, and the light path is
already built: clustered by `internal/webgpu/lightgrid.go`, bounded by a cull
radius, penumbra-filtered, and gated on whether its shadow ray could change the
image at all (`SHADOW_SKIP_LEVELS`). This adds scene data, not a subsystem.

Three things fall out of that choice:

- **No boundary and no history.** Nothing follows the camera, so there is
  nothing to scroll, reproject, or go stale. Frames are identical when parked,
  where the live cascades settle at 0.146 display levels a frame.
- **It is a few hundred bytes**, against the baked volume's 130 MB.
- **The output is authorable.** `RAYTRACER_VPL_DUMP` writes the set as ordinary
  `[[light]]` entries. An artist can move one, retint it, or delete it — which is
  what the villa's hand-placed red fills were doing by hand already.

## How it works

1. **Emitters.** Every `[[light]]`, plus every campfire sub-light resolved
   through `scene.Campfire.LightAt` — the same function the renderer uses, not a
   second copy of the flicker maths. A parallel shading path in this area has
   already dropped campfires once, and the villa hearth is a campfire.
2. **Photons.** `Rays` directions per emitter, uniform on the sphere or inside
   the cone for a spot, traced against the scene BVH plus the plane list.
3. **Landing.** Only a diffuse or checker hit leaves a VPL. A mirror sends the
   photon elsewhere and an emitter is already a light; standing a VPL on either
   adds light that is misplaced or double counted.
4. **Clustering.** One merge pass at `Reach/16`, strongest-first, with a normal
   test so two points on opposite faces of one wall never merge into a light
   inside it; then the strongest `Count` are kept and the rest folded into the
   nearest survivor within one reach. Merging **sums** colour and moves to the
   power-weighted centroid, so the total does not depend on how many photons
   happened to land.
5. **Emission.** Each VPL gets a wide cone (170°) about its bounce normal, so it
   lights the hemisphere it came from and not through its own surface, and a
   reach inherited from the emitter whose bounce it is, capped at `Reach`.
   Bounding reach is much cheaper than asking for visibility, which is the
   conclusion every other approach here reached from a different direction.

**The photon's weight is its emitter's colour times the surface albedo, and
nothing else.** No distance falloff and no incidence cosine: a photon carries
constant flux however far it travels, and a patch that is far away or edge-on
already catches proportionally fewer photons because it subtends a smaller solid
angle at the emitter. Applying either a second time is a double count — see
`attenNorm`, which keeps only the *ratio* between the renderer's bounded falloff
and true inverse square.

Six things about that were wrong in the first versions, and each was found by
measurement rather than by reading the code:

- **The merge radius must not come from the candidate cloud's extent.** On
  office-sunset the sun scatters photons across the whole city, so the cloud is
  hundreds of metres wide and a server room's distinct bounces merged into one
  light before the search took a step.
- **There must be no radius search.** Growing the radius until the set fits
  returns the first radius that does, which overshoots: one step took
  office-sunset from 40-odd clusters to 14, leaving ten slots unused.
- **Reach must be per-VPL.** The server racks carry about 250 LEDs of range 0.8,
  so their light lands within a metre of each rack; a flat 12 m reach re-emitted
  it over fifteen times that area and put blue at **5x** the path tracer's while
  red and green were inside 20%.
- **The falloff was applied twice**, and this was the big one. Scaling each
  photon by `light_atten` on top of the solid-angle dilution makes deposited
  energy fall as 1/d⁴ instead of 1/d². office-sunset's skyway floor sits 47 m
  from the only light that reaches it and came out **570x** too dark — which is
  why that bridge produced no ceiling bounce at all, and most of why the scene
  measured nine times short on the sun-lit view.
- **Photons must pass through glass and off mirrors.** Stopping at the first
  non-diffuse hit drops light that has not landed yet, and the skyway is a glass
  tube: every photon aimed at its floor died on the west wall.
- **Aim at the scene, not the sphere.** A sun 226 m away puts 0.19 of its 512
  photons on a bridge floor. `aim.go` fires into the cone subtending the scene
  and scales by the solid-angle fraction — the same estimator, far less
  variance. (It engages rarely on office-sunset, whose 313 m scene sphere
  contains almost every emitter, so it is insurance rather than the fix.)

## Calibration: `Gain` is measured, not guessed

The renderer's falloff is not inverse-square — `light_atten` is a bounded curve
with a knee, chosen to kill banding — so there is no exact constant converting
emitted flux into a VPL intensity. `Gain` absorbs it, and it is calibrated
against `cmd/pathtrace -debug indirect` on the villa hearth room:

| mean linear RGB over the frame | R | G | B |
|---|---|---|---|
| path-traced indirect, 96 spp | 0.02473 | 0.00822 | 0.00230 |
| VPL contribution at `Gain = 1` | 0.00400 | 0.00117 | 0.00017 |
| **VPL contribution at `Gain = 2π`** | **0.02513** | **0.00756** | **0.00113** |

Matching the reference's mean wanted 6.61; 2π is 6.28, and the shipped default.
(Those figures are from the villa at `Reach = 12`; with the shipped `Reach = 24`
and the corrected clustering the same view wants **1.03x**.)
**The hue was already right at gain 1** — R:G of 1:0.29 against the reference's
1:0.33 — which is the part that says the transport is being modelled and not
just the brightness. Blue lands low (1:0.043 against 1:0.093) because the bluest
bounce in that room comes off terrain, which the photon tracer cannot see.

Recalibrate with that view if the falloff constants ever move.

## Per-region budgets: `[vpl]` on an object

The budget used to be global, and a global power ranking is not a ranking of
usefulness. On the night villa at `Count = 24`, measured by where each VPL
landed:

| where the slots went | at `Count = 24` |
|---|---|
| villa A (the one you spawn in front of) | 7 |
| villa B, 48 m away | 4 |
| **a campfire on the mountain, 160 m away and 80 m up** | **11** |
| the Vienna apartment | **0 at every count tried** |

Eleven of twenty-four slots went to a fire nobody is looking at, and the
apartment never won a slot at any `Count`, because it was never competing with
its own bounces — it was competing with a mountain.

A `[vpl]` table fixes that by giving a volume its own budget:

```toml
# scenes/objects/art-nouveau-villa.toml
[vpl]
lights = 24
expand = 2.0
```

Put on an **object** file it travels with the object, so each `[[include]]` of it
becomes its own region with its own budget — two villas get twenty-four lights
each, which is right, because each is a place a player can stand.

| key | default | effect |
|---|---|---|
| `lights` | — | the region's budget, ranked within the region. 0 disables it |
| `gain` | inherit | bounce brightness here; absolute, like `RAYTRACER_VPL_GAIN` |
| `reach` | inherit | metre cap here; cuts the bounce off, so prefer `shadow_levels` for cost |
| `shadow_levels` | inherit | display levels a bounce here must be worth to cast a shadow. **The cost knob** |
| `min` / `max` | object AABB | scope by hand, in the file's *local* coordinates |
| `expand` | 0 | grow the box, for bounce landing on a porch or a step |

### It selects on where the photon landed, not on where the emitter is

This is the property that makes a region safe, and it is worth being explicit
about because the opposite design is the obvious one and it is wrong. A region
never removes an emitter from the trace. Every light and every campfire is shot
exactly as before; the region only decides which pool a *landed* photon competes
in. So a campfire standing in the garden still lights the hall it shines into,
because the bounce it leaves on the hall floor is inside the hall's box even
though the fire is not.

Measured on the villa, whose garden campfire at `(-9, 0, 5)` sits outside the
region box (`z` stops at 4.0): removing that fire drops the hall's brightest
bounce from **0.334 to 0.259**, so roughly a fifth of the entrance-hall bounce is
coming from an emitter the region does not contain, and the region keeps it.

A region can move a slot between pools. It cannot delete light.

Two consequences worth knowing:

- **The box is a box, so a building's outside faces are in it too.** Bounce on
  the exterior walls competes for the same budget as bounce in the rooms. Use
  `min`/`max` to aim at one hall if that matters.
- **Overlap resolves smallest-first.** A box around one room beats the box around
  the building containing it, so include order never decides the outcome.

### The two budgets are independent

A region's `lights` is generated whether or not `RAYTRACER_VPL` is set. The flag
only sizes the **leftover pool**: candidates landing in no region at all.

| launch | regions | leftover pool |
|---|---|---|
| no flag | authored count each | **none placed** |
| `RAYTRACER_VPL=24` | authored count each | 24, ranked scene-wide |
| `RAYTRACER_VPL_REGIONS=0` | ignored | none |
| `RAYTRACER_VPL_REGIONS=0 RAYTRACER_VPL=24` | ignored | 24 — the old global-only behaviour, kept as the A/B |

Unflagged is the interesting default: you get exactly the bounce the scene's
objects asked for and nothing scattered across the rest of the level. The
leftover clustering is skipped entirely in that case rather than run and
discarded, so there is no cost to the pool you did not ask for.

## Cost

At the app's 512x320, best of five **interleaved** rounds of 25 frames —
configurations rotate inside each round, so thermal drift lands on all of them
equally rather than on whichever ran last (`tmp/vpl/cost.sh`):

| VPLs | villa hearth | office server room |
|---|---|---|
| off | 9.0 ms | 11.2 ms |
| 16 | 12.1 ms | 11.6 ms |
| **24** | **11.9 ms** | **12.3 ms** |
| 48 | 13.5 ms | 12.2 ms |

Shadow rays on the villa view: 668,501 off against 1,117,401 at 24 VPLs.

**It is strongly sublinear, and that is the whole reason this is affordable.**
Twenty-four VPLs add 2.7 shadow rays per pixel rather than 24, and on the office
48 cost no more than 24. Two mechanisms already in the renderer do it: the cull
radius keeps a VPL out of every light-grid cell its reach does not touch, and
`SHADOW_SKIP_LEVELS` refuses a traversal for any light that could not move the
pixel a full display level — which is precisely what dim broad bounce light is.

Cost scales with `Reach`, not really with `Count`: at `Reach = 12` the villa's 24
VPLs cost +1.5 ms against the +2.9 ms they cost at 24.

### What a region buys, measured

Best of interleaved rounds of 25 frames at 512x320 on the night villa's default
facade view (`tmp/vplcost.sh`), against the number of VPLs that actually landed
in villa A — the room you are standing in front of:

| configuration | total VPLs | in villa A | GPU | VPL overhead |
|---|---|---|---|---|
| off | 0 | 0 | 16.7 ms | — |
| `RAYTRACER_VPL=24`, no regions | 24 | 7 | 22.6 ms | +5.9 ms |
| `RAYTRACER_VPL=80`, no regions | 80 | 32 | 29.2 ms | +12.5 ms |
| **`RAYTRACER_VPL=4` + `[vpl] lights = 24`** | **52** | **24** | **23.3 ms** | **+6.6 ms** |
| `RAYTRACER_VPL=24` + `[vpl] lights = 24` | 72 | 29 | 28.8 ms | +12.1 ms |

Read the third and fourth rows together. Getting two dozen bounce lights into
the villa used to mean a global count of about 80 and **+12.5 ms**; with a region
it takes **+6.6 ms** — a **47% cut in the VPL overhead for the same local
result**. The rest of that 80 was paying to light a mountain.

Against the cheapest old configuration the trade is even plainer: for 0.7 ms more
than `RAYTRACER_VPL=24`, the villa goes from 7 local lights to 24.

Note the 52 includes villa B's own 24, because the `[vpl]` table is on the object
and the scene places it twice. That is the intended behaviour and most of it is
not free but is cheap: villa B is 48 m away, so the cull radius keeps it out of
the light-grid cells near the camera and `SHADOW_SKIP_LEVELS` drops what is
left.

Generation is CPU work at scene load: 174 ms for office-sunset's 267 emitters at
512 rays each, once.

## What a VPL actually costs, and the one lever that moved it

The cost table above is an exterior view. Standing **inside** a lit room is the
hard case, and it is much worse, because there the cull radius does nothing: a
room's bounce lights all reach every pixel in that room. Villa hearth room,
1024x640, 48 VPLs per region:

| | GPU | shadow rays |
|---|---|---|
| VPLs off | 53.1 ms | 4.32 M |
| 48 per room, exact shadows | **129.9 ms** | **15.95 M** |

The frame nearly triples and shadow rays nearly quadruple. **That is the whole
cost — it is shadow rays, not shading maths.** Two things were tried against it
before the one that worked, and both failed for the same reason.

### The light grid cannot help here, and it was not even trying

Profiling turned up something worth writing down: with 24 VPLs per room,
**55 of 61 lights were in the grid's *wide* list**, which every pixel evaluates in
full. Two separate mechanisms put them there:

- Grid bounds are built from light positions plus a *typical* radius (the 0.75
  quantile), but `cellsOf` rejects a light whose own sphere escapes those bounds.
  Any light reaching further than typical is wide by construction, and VPL
  reaches cluster tightly around the cap, so most of them qualify.
- `lightGridMaxCellShare` then caps a light at 1/16 of the cells. A 24 m reach
  over the villa's cells blows past that, and **finer cells make it worse**, which
  is why raising `lightGridCellsPerLight` from 8 to 512 changed nothing.

Relaxing both got the wide list from 105 down to 27 — and the frame did not move
at all (28.5 ms to 28.3 ms, shadow rays 2 848 811 to 2 848 412). That is the
real lesson: **inside a room those lights genuinely all reach the pixel**, so
clustering has nothing to remove. No amount of spatial culling shortens a list
that is honestly that long. The grid is still worth fixing for scenes where the
lights are spread out; it is not the answer to this.

### RIS was fast and unusable

If the list cannot be shortened, sample it. `gi_probe_shade` already collapses a
whole cluster to one shadow ray by resampled importance sampling, and the path
tracer reached the same conclusion independently — choosing which light to trace
beats tracing more of them. Lifting that estimator into `shade_diffuse`, over
both the wide list and the cell span, works exactly as advertised on cost:
**129.9 ms falls to 60.0 ms at four draws**, with shadow rays down from 16.0 M to
3.7 M, and the mean stays within 2.5% — it is an unbiased estimator and it
measures unbiased.

It is also unusable. Four draws over a population of fifty puts blocky
salt-and-pepper across every surface: RMSE 0.47 against a mean linear radiance of
0.25, and high-frequency energy **three times the image's own detail**. Probe rays
survive this because temporal hysteresis averages it; a primary pixel has nothing
to average it with. Making it usable needs a denoiser or temporal accumulation,
which is a much larger project and brings back exactly the shimmer and lag
[live-gi.md](live-gi.md) spent four attempts removing. Recorded here so the next
person does not spend the afternoon rediscovering that it is fast.

### Skipping the shadow instead of the light

What worked is asking a cheaper question: not *which* light to trace, but
*whether this light's shadow is worth a traversal at all*.

`SHADOW_SKIP_LEVELS` already gates every light on whether its shadow could move
the pixel a full display level, judged against the radiance already accumulated
there. `ShadowLevels` makes that threshold per-light (`Light.Shape.z`), and VPLs
raise it. A bounce light is a point standing in for a lit patch, so its shadow is
the least defensible detail it carries, and the first thing to give up when it is
one of fifty each adding a sliver.

| `RAYTRACER_VPL_SHADOW_LEVELS` | GPU | shadow rays | mean linear vs exact |
|---|---|---|---|
| 0 (exact, every VPL shadows) | 129.9 ms | 15.95 M | — |
| 4 | 104.3 ms | 11.18 M | +3.0% |
| **16 (shipped default)** | **72.3 ms** | **5.65 M** | **+6.5%** |
| very large (no VPL shadows) | 59.1 ms | 4.32 M | +9.2% |

**1.8x on the interior view, and the artifact is the opposite of RIS's.** Where
resampling *added* high-frequency energy, this removes shadow detail: measured
added high-frequency content is 0.014 against the image's own 0.018, so the room
goes slightly brighter and slightly flatter under the stairs and nothing else
changes. The bias is light that should have been blocked and is not, which is the
honest description of what was traded.

16 keeps the mechanism adaptive rather than turning into an off switch — a bounce
bright enough to dominate a dark corner still casts. Set it very large for the
last 13 ms if a scene does not need that.

For comparison, lowering `reach` — the other knob that is often suggested —
does far worse per millisecond saved: `reach = 6` gets to 96.2 ms but takes
**28.8% of the room's light with it**, because it is cutting the bounce off
rather than approximating its shadow.

## Measured against the reference

**Villa hearth room.** Compared with the authored fill lights this replaces:

- **No fill** leaves the walls cold blue-grey; the room has no bounce at all.
- **The hand-placed red fill** washes the whole room evenly, including the far
  wall that the fire cannot see. It is one light standing in for a field, so it
  cannot fall off.
- **Generated VPLs** put warm light on the door and door frame and let the far
  wall stay dark, which is the distribution the path tracer shows.

**Office-sunset, and here the budget is the story.** Four views from the same
standing position in the server room, each against its own path-traced
indirect-only render, showing the gain that would match its mean:

| view | what it faces | Count = 24 | Count = 256 |
|---|---|---|---|
| yaw 0 | dark corner | 6593x | **1.42x** |
| yaw 120 | the sunlit window wall | 6.28x | 0.17x |
| yaw 180 | racks, side wall | 1.93x | 0.06x |
| yaw 240 | racks, far windows | 215x | **1.52x** |

Read that as a ranking problem, not a physics one. Correcting the falloff made
far-field bounces 100x stronger, so they now dominate a *global* power ranking:
at 24 the whole budget goes to the bright far field and the local views get
essentially nothing, and at 256 the local views are served but the rack views run
16x hot. There is no `Count` that suits all four, because the set is chosen by
scene-wide power and what a view needs is local.

**This was the case for the per-region budget**, and it is what `[vpl]` now
does — see [Per-region budgets](#per-region-budgets-vpl-on-an-object). Declaring
the server room a region lets its own bounces compete only against each other,
which is exactly what the table above says no global `Count` can do. The fully
automatic version, ranking within each light-grid cell, is still unwritten.

The skyway is the clearest single demonstration. At `RAYTRACER_VPL=256` the
floor's bounce reaches the ceiling and the tube glows, where at 24 and at 48 it
does not, and where before the falloff fix no `Count` at all produced it.

**On office-sunset the VPLs also read as too bright**, which is not a
contradiction with the table. `ambient_sky` there was tuned by eye and is already
standing in for the bounce — live-gi.md found the same thing from the probe side
— so VPLs layered on top double-count. Dropping `ambient_sky` 5x first and
letting the VPLs supply it lands closer to the reference than either. The villa
works cleanly because its author had already commented `ambient_ground` out,
which is the honest prerequisite: **turn the authored ambient down before turning
VPLs on, or you are paying for the same light twice.**

## Known limits

- **No terrain bounce.** The photon tracer uses the BVH plus planes, matching
  `internal/probe`; terrain is in neither. Outdoors the ground is usually the
  largest bouncing surface, so an exterior scene gets much less than it should.
  Marching terrain per photon is the fix and it is offline work, so cost is not
  the objection — it just is not written.
- **No instanced geometry**, for the same reason: `bvh.New` does not carry the
  instance TLAS, so the villa's trees neither bounce nor occlude photons.
- **The global budget is still ranked globally.** Anything outside every `[vpl]`
  region competes scene-wide, so on a large level the leftover pool behaves as it
  always did: `RAYTRACER_VPL=8` with no regions declared still looks like no VPLs
  at all. Regions fix this where an author asks for it; they do not fix it
  automatically. The unattended version is still a per-light-grid-cell cut in the
  Lightcuts sense, which the existing `light_span` already has the shape for.
- **A broad lit surface is the case this cannot do.** Measured above: nine times
  short on the office view facing a sunlit wall. Raising `Count` does not fix it,
  because the shortfall is not resolution — it is that the total carried by the
  set is the total the photons deposited, and representing an evenly lit wall as
  points puts that total in a few places instead of across it. Wants area lights,
  or many more VPLs than the shadow-ray budget allows.
- **It double-counts an authored ambient.** A scene whose `ambient_sky` /
  `ambient_ground` was tuned by eye already has a stand-in for this light. Turn
  it down before turning VPLs on.
- **A VPL is a point, and a bounce is not.** On a large flat wall a single
  cluster reads as a soft hot spot rather than an even wash. `Radius` and a
  higher `Count` both soften it; neither removes it.
- **One bounce.** Light that reaches a room only via two walls is missing.

## Toggling it at runtime

Key **9** in the app attaches and detaches the set on a live scene, and the HUD
status line carries `vpl[9]`. The set is generated once and kept, so the two
states are the same lights and differ only in whether the scene is carrying
them — pressing it repeatedly cannot grow the light list.

The key toggles **what the scene asked for, and invents nothing.** On a scene
that declared `[vpl]` regions the set already exists at load, so the first press
detaches it — the quickest A/B for whether a region is earning its cost. Launch
with `RAYTRACER_VPL=24` and the key toggles those twenty-four as well.

On a scene with no `[vpl]` anywhere and no flag, **the key does nothing**. It used
to fall back to the default count and generate a scene-wide set on the spot, so
that it would demonstrate the bounce on a scene that had never opted in. That
made it the one path which could place lights nobody asked for, contradicting the
unflagged launch rule directly: on office-sunset, which authors no `[vpl]` at all,
a keypress conjured twenty-four globally-ranked VPLs. Pass `RAYTRACER_VPL` at
launch to get that set back.

## Knobs

| Variable | Default | Effect |
|---|---|---|
| `RAYTRACER_VPL` | unset | scene-wide budget for bounce landing outside every `[vpl]` region; unset or 0 places none. Does **not** gate the regions themselves |
| `RAYTRACER_VPL_RAYS` | 512 | photons per emitter — placement accuracy, not brightness |
| `RAYTRACER_VPL_GAIN` | 2π | bounce brightness; see calibration above |
| `RAYTRACER_VPL_RADIUS` | 1.5 | emitter size the penumbra filter sizes from |
| `RAYTRACER_VPL_REACH` | 24 | cap on metres a VPL carries; each inherits its emitter's reach under that. 0 removes the cap |
| `RAYTRACER_VPL_CONE` | 170 | emission cone about the bounce normal; 0 is omnidirectional |
| `RAYTRACER_VPL_MIN_LEVELS` | 1.0 | drop a cluster that cannot move a black pixel this far |
| `RAYTRACER_VPL_SHADOW_LEVELS` | 16 | display levels a VPL must be worth before its shadow is traced. **The cost knob.** 0 restores exact per-VPL shadows; a large value turns them off |
| `RAYTRACER_VPL_SEED` | fixed | photon directions; generation is deterministic in it |
| `RAYTRACER_VPL_DUMP` | unset | write the set as a TOML fragment |
| `RAYTRACER_VPL_REGIONS` | on | `0` ignores every `[vpl]` region and ranks the level globally, as before regions existed. With `RAYTRACER_VPL` unset it is the complete off switch |

## Related

- [live-gi.md](live-gi.md) — the probe-field attempts and what each measured.
- [path-tracer.md](path-tracer.md) — the reference, and the RIS result that says
  choosing which light to trace beats tracing more of them.
- [ambient-zones.md](ambient-zones.md) — the authored version of the same idea,
  also removed from the code.
