# Virtual point lights

**Status:** behind `RAYTRACER_VPL`. Off by default, and byte-identical off —
verified on the villa and office-sunset.

```bash
RAYTRACER_VPL=24 go run .
RAYTRACER_VPL=24 RAYTRACER_VPL_DUMP=tmp/villa-vpl.toml go run ./cmd/gpuprof -scene scenes/outdoors-night-villa.toml
```

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

Generation is CPU work at scene load: 174 ms for office-sunset's 267 emitters at
512 rays each, once.

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

**This is the case for the per-region budget.** Ranking within each light-grid
cell — a cut in the Lightcuts sense, which `light_span` already has the shape for
— is what would let a view's own bounces compete only against each other.

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
- **The budget is global.** Clusters are ranked by power across the whole scene,
  so on the villa — which has a mountain campfire, a lake fire and two houses —
  the first eight slots go nowhere near the room you are standing in, and
  `RAYTRACER_VPL=8` looks like no VPLs at all. A large level needs a large
  `Count`. The principled fix is a per-region budget, or a per-light-grid-cell
  cut in the Lightcuts sense, which the existing `light_span` already has the
  shape for.
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
them — pressing it repeatedly cannot grow the light list. It works whether or not
`RAYTRACER_VPL` was set at launch; without it the first press generates the
default set.

## Knobs

| Variable | Default | Effect |
|---|---|---|
| `RAYTRACER_VPL` | unset | VPL count; unset or 0 is off |
| `RAYTRACER_VPL_RAYS` | 512 | photons per emitter — placement accuracy, not brightness |
| `RAYTRACER_VPL_GAIN` | 2π | bounce brightness; see calibration above |
| `RAYTRACER_VPL_RADIUS` | 1.5 | emitter size the penumbra filter sizes from |
| `RAYTRACER_VPL_REACH` | 24 | cap on metres a VPL carries; each inherits its emitter's reach under that. 0 removes the cap |
| `RAYTRACER_VPL_CONE` | 170 | emission cone about the bounce normal; 0 is omnidirectional |
| `RAYTRACER_VPL_MIN_LEVELS` | 1.0 | drop a cluster that cannot move a black pixel this far |
| `RAYTRACER_VPL_SEED` | fixed | photon directions; generation is deterministic in it |
| `RAYTRACER_VPL_DUMP` | unset | write the set as a TOML fragment |

## Related

- [live-gi.md](live-gi.md) — the probe-field attempts and what each measured.
- [path-tracer.md](path-tracer.md) — the reference, and the RIS result that says
  choosing which light to trace beats tracing more of them.
- [ambient-zones.md](ambient-zones.md) — the authored version of the same idea.
