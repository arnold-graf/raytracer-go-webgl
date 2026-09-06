# Hard rings around point lights

Two separate thresholds in the point-light path draw a visible circle at a fixed
distance from every light. They look like one bug and are not — they have
different causes, different radii, and different fixes. Both were reported as
"round hard rings of light", in the villa and the server room alike, and neither
depends on scene geometry.

Both are fixed. The general lesson is the same for both: **a threshold that is
invisible on an isolated pixel is not invisible on a coherent surface.** Both of these were sized
against a per-pixel error budget, and both are drawn along a smooth iso-distance
curve, where the eye's own edge detection picks them out far below the level at
which an isolated pixel of the same error would be noticed.

## Ring 1: the attenuation clamp (fixed)

`add_point_light_raw` attenuated with

```wgsl
att = min(1.0, 1.0 / (LIGHT_ATTEN_BASE + d2 * LIGHT_ATTEN_QUADRATIC));
```

The flat top runs until the denominator reaches 1, which with `BASE = 0.5` and
`q = 0.08` is **exactly `d = 2.5` world units, for every light in every scene**.
`att` is continuous there, which is why this survived so long — but its slope
jumps from 0 to −0.4/unit, and a slope discontinuity inside a smooth gradient is
a Mach band.

Confirmed on `tmp/perf/ring.toml` (one light 2.0 above a bare floor, no
occluders, no AO, no ambient — so floor luminance is exactly `att × ndl`). The
radial profile in *linear radiance* has a curvature spike at pixel radius 56;
with the clamp removed the same radius is **29× flatter**. The predicted radius
is `sqrt(2.5² − 2.0²) = 1.500` world units, and 56 px × 0.02685 world/px =
**1.504**.

The fix rewrites `min(1, 1/u)` as `1/max(1, u)` and smooths the max, which is a
cheap hyperbola:

```wgsl
fn light_atten(d2: f32) -> f32 {
    let u = LIGHT_ATTEN_BASE + d2 * LIGHT_ATTEN_QUADRATIC;
    let e = u - 1.0;
    return 1.0 / (0.5 * (u + 1.0 + sqrt(e * e + LIGHT_ATTEN_KNEE)));
}
```

`LIGHT_ATTEN_KNEE = 0` reproduces the sharp version exactly, which is a useful
check that the rewrite is faithful. The knee costs `sqrt(KNEE)/2` at the corner
and almost nothing elsewhere:

| KNEE | curvature at the ring, vs sharp | Δ at r=20 | Δ at the ring | Δ far field |
|---|---|---|---|---|
| 0 | 100% | 0 | 0 | 0 |
| 0.0025 | 35% | −0.2% | −1.8% | −0.1% |
| **0.01** | **21%** | **−1.2%** | **−3.7%** | **−0.3%** |
| 0.04 | 5.9% | −3.9% | −7.2% | −1.3% |

Set to **0.01**. Under a 140× high-pass the ring is gone at 0.01 and still
faintly visible at 0.0025. The curvature number understates how well 0.01 works
because what the eye sees is the *sharpness* of the transition, not its
integrated magnitude.

The far field is unchanged to well under a percent, so `lightCull` in
`scene.go` — which inverts the sharp form to derive the cull radius — stays
correct and needs no matching Go constant.

**Why not just raise `LIGHT_ATTEN_BASE` to 1.0?** That makes the `min` never bind
and removes the corner for free. It also dims the near field by up to 33% (17% at
d = 5, 5.6% at d = 10), which is a global relighting of every scene. The flat top
is an artistic choice; only its corner is a bug.

Worth noting for later: the flat top means attenuation models a light of radius
2.5, while `PENUMBRA_LIGHT_RADIUS` models the same light at 0.35 for shadow
softening. The two are inconsistent by 7×. Reconciling them is a lighting
decision, not a bug fix.

## Ring 2: the shadow-skip gate (fixed by tightening it)

`SHADOW_SKIP_LEVELS` skips a light's shadow ray when the light is too dim to
change the pixel by that many display levels
([megakernel-optimization.md](megakernel-optimization.md)). Inside that radius
shadows are traced; outside, the light is added **unshadowed**. That is a step,
not a slope change, and it is drawn along a sphere around the light — an arc
across every wall in range.

Isolated by ablation on the server room: with `SHADOW_SKIP_LEVELS = 0` the arc
vanishes completely, while setting `LIGHT_CULL_EPS` to 1e-7 leaves it untouched.
So it is this gate and not the cull epsilon.

Visibility against the threshold, at 110× high-pass:

| `SHADOW_SKIP_LEVELS` | arc |
|---|---|
| 4.0 (was shipped) | strong |
| 2.0 | clearly visible |
| **1.0** | **gone** |
| 0.5, 0 | gone |

**Lowered to 1.0. It is the most expensive single change in the soft-shadow
work: 1.1 ms of 9.5 on the server room**, isolated by reverting just this
constant. That is the price of the ring, and it is a real one — the gate exists
because tracing a shadow ray for a light that cannot visibly darken the pixel is
wasted work, and 4.0 skips a lot more of them than 1.0 does.

The figures first reported here — 7.6 vs 7.7 ms, "measured free" — were wrong.
They came from an A/B that swapped `trace_linked.wgsl`, which the renderer
silently regenerates from the modules, so both arms ran the same shader. See
"Measuring a shader change" in [soft-shadows.md](soft-shadows.md).

The alternative — keeping 4.0 and making the transition gradual — has no good
form. You cannot half-trace a ray; fading the light's contribution to zero across
the boundary darkens geometry that really is lit; and dithering the threshold
spatially converts the ring into noise, which is the trade
[soft-shadows.md](soft-shadows.md) had to undo for the penumbra filter's jitter.

## Testing

`tmp/perf/ring.toml` isolates the falloff: one white light over a flat grey
floor, viewed straight down, with ambient and sun at zero and no occluders, so
the image *is* the attenuation function. Rings this faint are invisible in a
normal screenshot, so judge them with a high-pass — subtract a 13 px box blur and
amplify 90–140×. A curvature ring in a smooth gradient shows up as a clean
circle, and the eye's response to the real image tracks that far better than any
whole-frame statistic does.

Measure the profile in **linear radiance**, not display levels: see
[soft-shadows.md](soft-shadows.md) and invert ACES + gamma first, or the tonemap's
own curvature contaminates the measurement.

## Related

- [soft-shadows.md](soft-shadows.md) — the penumbra filter, and the same
  "coherent artifact beats per-pixel error budget" lesson applied to jitter.
- [megakernel-optimization.md](megakernel-optimization.md) — `SHADOW_SKIP_LEVELS`
  and the display-space reasoning behind it.
