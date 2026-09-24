# Occlusion culling for the light grid (measured, doesn't pay here)

`internal/webgpu/lightvis.go`. **Off by default**; `RAYTRACER_LIGHT_CULL=1`.

The idea: `lightgrid.go` clusters lights by reach, which is a statement about
distance and nothing else. A lamp behind a wall is "in range" of a cell, so
every pixel in it casts a shadow ray to rediscover the wall. Ask once at build
time instead — can this light reach *any* sampled point of this cell? — and drop
the pair if not.

## It works, and it changes nothing

| | pairs culled | shadow rays before | after |
|---|---|---|---|
| office-sunset, server room | 474 / 2820 (17%) | 3,242,802 | 3,242,700 |
| office-sunset, front office | 474 / 2820 (17%) | 5,005,202 | 5,005,201 |
| villa hearth | 10 / 44 (23%) | 3,357,534 | 3,357,534 |

0.003%. The frames are bit-identical with it on, in all three views, so the pass
is correct — it is simply removing work nobody was doing.

## Why, and the mistake in the premise

The composition line `setLights` logs says it:

    light grid: 315 lights (74 wide: 74 outside bounds, 0 too broad), 2312 cells, 2820 pairs

**74 of 315 lights are wide** — outside the grid bounds, so every shaded point
evaluates them wherever it stands. The 2820 clustered pairs this pass operates
on average 1.2 cells per light: already as tight as clustering can make them.
The lights it culls were being rejected by distance and by `SHADOW_SKIP_LEVELS`
before they ever cast a ray.

The premise was that 75.3% of the server room's shadow rays being *blocked* meant
most were wasted. It doesn't. A blocked shadow ray there comes from a light that
is near, significant, and genuinely occluded — and the ray is what draws the
shadow. **That statistic measures how much of the scene is in shadow, not how
much work is wasted**, and separating those two is the thing to check before
building anything.

## The wide list is the real target, and the obvious fix is worse

Widening the bounds to include every light (margin = max radius rather than the
75th-percentile one) takes office-sunset from 74 wide to 3 — and makes the frame
**slower**:

| server room | wide | GPU |
|---|---|---|
| bounds from the 75th-percentile radius (shipping) | 74 | 33.9 ms |
| bounds from the max radius | 3 | 41.6 ms |

The box then spans the whole scene, the cells coarsen to match, and the occupied
ones hold far more lights than before. Fewer wide lights is not the goal;
shorter per-point lists is, and one uniform grid cannot deliver both when the
scene has dense local clusters spread over a hundred units. A two-level grid, or
per-cell visibility bits for the wide list specifically, is where this would have
to go.

## Safety, if it is ever turned on

Culling a visible light is a hard artifact; missing a cull costs only the ray we
already pay. So every ambiguity resolves toward keeping the light:

- **Glass never occludes**, whatever its shadow flag. The shader passes
  `GLASS_SHADOW_TRANSMIT` through a casting pane, so treating one as opaque here
  would delete light that does arrive. The test walks past panes instead.
- **Planes, terrain and instanced geometry are not consulted.** All three can
  block on the GPU; none is in the CPU blocker tree. That only keeps pairs.
- A pair survives if *any* of 15 sample points (corners, face centres, middle)
  sees the light.

What it cannot promise is conservatism in the formal sense — a cell whose
samples are all blocked could still have a lit sliver between them. That is why
the check is a pixel diff against an unculled render rather than an argument.
