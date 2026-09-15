# Per-region ambient cubes

**Status:** shipped behind `RAYTRACER_AMBIENT_ZONES=1`, **off by default**. With
the flag off the megakernel renders byte-identically to before the feature
existed, verified by A/B against the pristine shader.
**Audience:** anyone trying to get indirect light into the real-time renderer.
**What it replaces:** `scene_ambient`, two authored constants blended by the
surface normal's Y.

```bash
# bake zone colours from the path tracer
go run ./cmd/ambientbake -scene scenes/office-sunset/index.toml -grid 3,2,3 > zones.toml

# render with them
RAYTRACER_AMBIENT_ZONES=1 go run .
```

---

## The problem it addresses

The shipping renderer's entire indirect-light model is

```wgsl
fn scene_ambient(n: vec3<f32>) -> vec3<f32> {
    let t = clamp(n.y * 0.5 + 0.5, 0.0, 1.0);
    return mix(params.ambient_ground.xyz, params.ambient_sky.xyz, t);
}
```

Two colours, blended by which way the surface points. office-sunset declares
`ambient_sky = [0.20, 0.12, 0.10]`, so every up-facing surface in the building
receives exactly that — a desk under a lit atrium and a shelf in a windowless
server room alike. It is the single largest departure from what the scene
would actually look like, and the path tracer makes the gap measurable: full
transport differs from direct-only by 13.67 RMSE levels on the yaw-270 view,
and the diffuse indirect field alone carries a mean display level of 3.4
against 13.4 for the whole image.

## Why zones rather than a probe grid

The obvious answer is an irradiance volume, and the structure for one is
already in the renderer: `ao_volume` is a **six-face ambient cube per cell**
with trilinear interpolation and normal-weighted face blending
(`ao_face`, `ao_corner`), sampled for essentially free. Storing RGB irradiance
instead of scalar occlusion is close to a drop-in.

Two measurements say not yet:

- **Resolution.** `aoVolMaxCells` caps the grid at a million cells, which
  against these scene bounds lands at **3.87 world units per cell in the
  office and 17.24 in the villa**. A 3.87 m cell spans rooms. Going to 1 m
  dense would be ~45M cells, about 3.2 GB.
- **Leaking**, which follows directly. A cell straddling a wall averages a lit
  room and a dark one and bleeds each into the other. Fixing that properly is
  DDGI's per-probe visibility term, which is the real work.

A zone sidesteps both by making the boundary an authored region instead of a
lattice: an explicit box cannot leak into the room next door, because the
rooms are what the boxes are. It is coarser than a probe grid inside a single
space, and that is the trade — indirect light is smooth and low amplitude, so
a room-sized constant is a far better approximation of it than a
building-sized one.

## How it works

`[[ambient_zone]]` declares a box and six RGB values, ordered
`+X, -X, +Y, -Y, +Z, -Z` — the same basis `ao_sample` already uses. A surface
normal weights the three faces it points toward by the square of its
components, which sum to one.

```toml
[[ambient_zone]]
min = [-8.0, 0.0, -6.0]
max = [ 8.0, 4.0,  6.0]
px = [0.21, 0.13, 0.09]
nx = [0.18, 0.11, 0.08]
py = [0.26, 0.15, 0.20]
ny = [1.03, 0.39, 0.16]
pz = [0.20, 0.12, 0.09]
nz = [0.24, 0.14, 0.10]
```

`scene_ambient_at(p, n)` evaluates **every** zone and blends them. A zone's
influence is its box dilated by half of `AMBIENT_ZONE_BLEND` on each side,
ramping linearly from one at the inner edge to zero at the outer, so two zones
sharing a face each contribute exactly a half there and the pair forms a
partition of unity across the seam. Where the influences no longer sum to one
— the outer edge of the zoned region — the remainder fades back to the
scene-wide constants rather than ending on a step.

Taking the first containing zone instead, which is what this did first, makes
every boundary a hard step in the ambient term. On the villa fireplace view
that showed as an **86-level jump across two rows** of the upper wall
(50.5 + 35.4 in the red channel) where a zone plane crossed it. With the blend
the largest step on that wall is 4.7, and the only larger one left, 14.8, is a
geometry edge present with zones off as well.

**The table rides inside `idx_tables`, not its own binding.** The megakernel
declares 30 storage/uniform buffers and naga spends one more on the
runtime-array sizes table, which is exactly Metal's limit of 31 per stage — a
31st binding fails at pipeline creation with "Not enough memory left", which
is not what it sounds like. `idx_tables` is already the shared arena for
variable-length tables addressed by `params.table_base`, so this is one more
region in it, at `table_base.w`, with floats bitcast into the u32 array. The
first word is the zone count, and zero is the off switch.

## Baking

`cmd/ambientbake` uses the path tracer as the reference. For each zone it
places a probe at the centre and, for each of the six axis faces, averages
cosine-weighted radiance over the hemisphere.

That average is exactly the quantity `shade.wesl` wants. The megakernel's
ambient term is `alb * scene_ambient(n)` with no 1/pi, so the value it needs is
irradiance over pi, and the mean of cosine-sampled radiance is that directly —
a baked cube drops in with no rescaling.

Nothing is excluded from the probe trace, and that is deliberate: a probe ray
can only ever hit geometry, the sky or an emissive surface, never a point
light, which has no geometry to hit. So what it gathers is precisely the light
the megakernel's own light loop does not already compute. No double counting,
no special case.

`-grid NX,NY,NZ` partitions the scene bounds instead of using authored zones.
It is for trying the feature out, not for shipping: a coarse partition puts
probe centres inside walls and inside the building mass, which is exactly the
failure mode zones exist to avoid. Real zones are drawn per room.

## What it actually recovers

Measured on the villa fireplace, which is the case the feature exists for: an
open fire against a wall the direct lighting leaves in shadow. Mean red-to-blue
ratio over two shaded wall patches, against a 512 spp path-traced reference of
the same camera:

| shaded wall patch | zones off | 1 zone | 4 zones, hard | 4 zones, blended | path traced |
|---|---|---|---|---|---|
| right of chimney | 1.14 | 1.27 | 1.55 | **1.70** | **3.50** |
| left of chimney | 1.10 | 1.24 | 1.58 | **1.76** | **5.30** |

Three things to read out of that.

**The sign is right and the wall stops being grey.** With zones off the shaded
wall beside a lit fire is very slightly warm, 1.14, which is to say neutral —
it is receiving `ambient_sky` and nothing else. The reference says it should be
strongly red.

**Granularity is most of the remaining gap.** One cube for the whole room
recovers little, because it averages the bounce over ten world units; four
slabs across the same room capture the falloff, and the baked `nx` face — the
one pointing at the fire — runs 0.66, 0.22, 0.23, 0.17 in red from the hearth
outward. More, smaller zones keep helping.

**But the thing that dominated all of it was coverage, not granularity or
blending.** The first zones for that room were drawn 4.2 to 7.5 in Y, and the
villa's stairwell is two storeys: everything above 7.5 sat outside every zone
and fell back to the flat constant. The vertical profile up the wall showed it
exactly — identical to zones-off above y=55, then ramping up below:

| screen row | 15 | 35 | 55 | 75 | 95 | 115 |
|---|---|---|---|---|---|---|
| zones off | 36.4 | 48.3 | 48.0 | 47.8 | 47.5 | 55.1 |
| truncated zones | 36.4 | 48.3 | 48.0 | 91.8 | 123.7 | 129.7 |
| full-height zones | 83.3 | 72.3 | 72.7 | 77.8 | 81.9 | 85.3 |
| path traced | 87.2 | 84.5 | 96.9 | 107.7 | 119.1 | 122.8 |

That step at the top of the coverage is what reads as a hard edge, and no
amount of blending between zones fixes it, because the discontinuity is
between the zoned region and the fallback rather than between two zones.
Re-baking the same room over its true height, 100 zones at 5x5x4:

| shaded wall patch | R/B | red |
|---|---|---|
| zones off | 1.14 | 46.6 |
| truncated, blended | 1.70 | 64.9 |
| **full height** | **3.24** | **76.3** |
| path traced | 3.50 | 92.5 |

**93% of the reference's tint**, and smooth: the largest row-to-row step on
that wall is 6.3 levels against the path tracer's own 5.7, both at the same
geometry edge. What is left is a brightness gap, not a colour or continuity
one — see [the AO volume](#the-ao-volume-is-not-the-culprit-and-is-nearly-inert)
for where it does *not* come from.

**Zones must cover every surface that can be seen from inside them.** Drawing
them to the floor plan is not enough; they are volumes, and a room is as tall
as its tallest visible wall.

## The AO volume is not the culprit, and is nearly inert

The obvious suspect for the remaining brightness gap was the baked AO volume.
It multiplies the ambient term, and the megakernel applies it *after* the light
loop — so it scales direct light too
([shade.wesl:329](../internal/webgpu/shaders/modules/shade.wesl#L329)), which
darkens a surface in full light merely for being near a wall. Direct light
already has real shadow rays; occluding it again is double counting.

`RAYTRACER_AO_INDIRECT_ONLY=1` restricts the volume to the ambient term.
`ao_enabled` became a bitfield to carry it (bit 0 on, bit 1 ambient-only) so
the Params layout did not move, and the soft-shadow snapshot stops scaling by
AO at the same time — otherwise the penumbra filter restores more direct light
than the frame deposited.

**It is correct, and it changes almost nothing**, because the volume is barely
doing anything to begin with:

| | office-sunset | outdoors-night-villa |
|---|---|---|
| pixels changed | 3.3% | ~0 |
| on those pixels | median 8 levels, max 33 | — |
| wall patch red | — | 76.3 -> 76.7 |

An 8x amplified difference of the office frame is essentially black. The reason
is resolution: `aoVolMaxAxis` caps the grid at 128 cells per axis, which
against these scene bounds gives **3.87 world units per cell in the office and
17.24 in the villa**. At that scale the volume cannot represent a crevice, so
`ao_sample` returns ~1.0 almost everywhere and there is nothing to
misattribute.

So the earlier guess in this document was wrong. Decomposing the same wall
patch with the path tracer says where the gap really is:

| | red | green | blue |
|---|---|---|---|
| PT direct only | 42.3 | 28.1 | 17.0 |
| PT indirect only | **68.1** | 33.7 | 15.5 |
| PT full | 92.5 | 52.3 | 26.4 |
| megakernel, no zones | 46.6 | 46.2 | 40.9 |
| megakernel, zones + AO fix | 76.7 | 45.2 | 23.7 |

Two things fall out. The megakernel's *direct* lighting of that wall is about
right — 46.6 red against the path tracer's 42.3 — so the campfire model is not
the problem. And on a shaded wall beside a fire, **indirect light is larger
than direct** (68.1 against 42.3), which is why the flat constant is so
visible there. The zones deliver roughly 60% of it.

The remaining 40% is the approximation itself: a six-face cube is a very
low-order directional basis, and each zone still averages the field over a
couple of world units. More and smaller zones keep helping; the basis is the
floor.

The AO fix should stay on once scenes carry zones — it is physically right and
free — but it is not a lever on brightness. Making the AO volume *matter* is a
separate project, and it runs into the same wall as the irradiance volume: at
useful resolution a dense grid is far too large, so it needs sparsity.

## A test scene for this

`scenes/test/indirect-shadow.toml` isolates the property every scheme here is
trying to approximate. One narrow spot lights a white wall and nothing else;
the cone never touches the floor, so the floor is lit purely by bounce, and a
pillar stands in the bounce path. Direct-only renders black, which is the
proof the setup works.

| floor behind the pillar vs clear floor | ratio |
|---|---|
| path tracer | 49.1 / 96.5 = **0.51** |
| megakernel, beyond 0.9 m | 32.0 / 32.0 = **1.00** |

The megakernel is not flat everywhere: the AO volume darkens a contact band at
the pillar base (32.0 to about 17 in the render, and the volume reads 0.776 a
decimetre from the face). But `AOMaxDist` is 0.9, so it is exactly 1.000 from
there outward.

Which names the gap precisely. The megakernel models a **contact** shadow a
decimetre wide; the reference has a **cast** shadow halving the light across
metres. Same physical effect, two scales, one of them modelled. Any irradiance
scheme without a visibility term will raise that 1.00 floor's brightness and
leave the ratio at 1.00; one with visibility will bend it toward 0.51.

## Cost

100 zones is +1.4 ms on that view (12.2 -> 13.6 ms). The blend has to test
every zone rather than stopping at the first hit, so this is a linear scan per
shaded vertex. A handful of zones is free; a hundred is a tenth of the frame,
and anything beyond that wants the zones in a grid or a BVH.

## Expect to re-light

The baked values come out several times larger than the authored constants —
around 0.5 to 1.2 against `ambient_sky = [0.20, 0.12, 0.10]`. That is real
indirect light against a conservative hand-tuned stand-in, and it means
enabling zones on a scene tuned without them makes it brighter. This is the
same migration cost the path tracer document describes: scene lighting is
authored against the approximation, so improving the approximation invalidates
the authoring.

## Related

- [path-tracer.md](path-tracer.md) — the reference renderer this bakes from,
  and the measurements motivating the feature.
- [soft-shadows.md](soft-shadows.md) — the other place a screen-space stand-in
  beats the physically-derived alternative at this frame budget.
