// shade.wesl — Lighting, shadows, and surface normals.
// Computes shading normals per primitive, casts hard shadow rays toward lights,
// evaluates point lights and flickering campfires, samples baked ambient
// occlusion, and assembles direct diffuse lighting (shade_diffuse).

import package::types::{CampfireParams, FEAT_CAMPFIRE, FEAT_PRIM_BOX, FEAT_PRIM_CONE, FEAT_PRIM_CYLINDER, FEAT_PRIM_LENS, FEAT_PRIM_PLANE, FEAT_PRIM_RING, FEAT_PRIM_SPHERE, FEAT_PRIM_TORUS, FEAT_TERRAIN, GHOST_SHADOW_TW, LIGHT_ATTEN_BASE, LIGHT_ATTEN_KNEE, LIGHT_ATTEN_QUADRATIC, LIGHT_CULL_EPS, Light, PRIM_BOX, PRIM_CONE, PRIM_CYLINDER, PRIM_FLAG_TRANSFORMED, PRIM_LENS, PRIM_PLANE, PRIM_RING, PRIM_SPHERE, Prim, RAY_EPSILON, SHADOW_NEAREST_BLOCKER, SHADOW_SKIP_LEVELS, SHADOW_WIDTH_FLOOR, SHADOW_WIDTH_MEAN_POWER, ShadowAux, ao_volume, campfires, holes, idx_tables, lights, params, terrains};
import package::profile::{PROF_SHADOW_BLOCK, PROF_SHADOW_RAYS, prof_inc};
import package::math::{display_step_levels, mix3, perlin, smoothstepf};
import package::texture::{texture_eval};
import package::intersect::{intersect_blocker, xf_to_world_normal};
import package::terrain::{hit_terrain, terrain_shadow_skip};
import package::terrain_zone::{terrain_zone_albedo};
import package::bvh::{blocker_bvh_any_hit};

// face_axis returns (normal.xyz, dist.w): the outward axis normal of the
// [mn,mx] box face nearest to hp, plus that distance. Mirrors scene.faceAxis.






























fn face_axis(mn: vec3<f32>, mx: vec3<f32>, hp: vec3<f32>) -> vec4<f32> {
    var n = vec3<f32>(-1.0, 0.0, 0.0);
    var d = abs(hp.x - mn.x);
    let dxh = abs(hp.x - mx.x);
    if (dxh < d) { n = vec3<f32>(1.0, 0.0, 0.0); d = dxh; }
    let dyl = abs(hp.y - mn.y);
    if (dyl < d) { n = vec3<f32>(0.0, -1.0, 0.0); d = dyl; }
    let dyh = abs(hp.y - mx.y);
    if (dyh < d) { n = vec3<f32>(0.0, 1.0, 0.0); d = dyh; }
    let dzl = abs(hp.z - mn.z);
    if (dzl < d) { n = vec3<f32>(0.0, 0.0, -1.0); d = dzl; }
    let dzh = abs(hp.z - mx.z);
    if (dzh < d) { n = vec3<f32>(0.0, 0.0, 1.0); d = dzh; }
    return vec4<f32>(n, d);
}

fn box_normal(p: Prim, hp: vec3<f32>) -> vec3<f32> {
    var best = face_axis(p.geo_a.xyz, p.geo_b.xyz, hp);
    // On the inner face of a cutout the outward normal points into the opening
    // (the negative of the hole's own face normal). Mirrors scene.Box.Normal.
    let count = u32(p.geo_b.w);
    let start = u32(p.geo_a.w);
    for (var i = 0u; i < count; i = i + 1u) {
        let hole = holes[start + i];
        let fa = face_axis(hole.mn.xyz, hole.mx.xyz, hp);
        if (fa.w < best.w) {
            best = vec4<f32>(-fa.xyz, fa.w);
        }
    }
    return best.xyz;
}

fn cylinder_normal(p: Prim, hp: vec3<f32>) -> vec3<f32> {
    let ymin = p.geo_a.w;
    let ymax = p.geo_b.x;
    if (hp.y <= ymin + 1e-3) {
        return vec3<f32>(0.0, -1.0, 0.0);
    }
    if (hp.y >= ymax - 1e-3) {
        return vec3<f32>(0.0, 1.0, 0.0);
    }
    let r0 = p.geo_a.z;
    var r1 = p.geo_b.y;
    if (r1 == 0.0) {
        r1 = r0;
    }
    let h = ymax - ymin;
    let alpha = (r1 - r0) / h;
    let ry = r0 + alpha * (hp.y - ymin);
    let dx = hp.x - p.geo_a.x;
    let dz = hp.z - p.geo_a.y;
    return normalize(vec3<f32>(dx, -ry * alpha, dz));
}

fn cone_cap_normal_world(p: Prim, ro: vec3<f32>, hp: vec3<f32>) -> vec3<f32> {
    var plane_out = vec3<f32>(0.0, -1.0, 0.0);
    if ((p.info.w & PRIM_FLAG_TRANSFORMED) != 0u) {
        plane_out = normalize(xf_to_world_normal(p, plane_out));
    }
    let to_hit = hp - ro;
    if (dot(to_hit, plane_out) < 0.0) {
        return plane_out;
    }
    return -plane_out;
}

fn cone_normal(p: Prim, hp: vec3<f32>, rd: vec3<f32>) -> vec3<f32> {
    let cx = p.geo_a.x;
    let cz = p.geo_a.y;
    let rbase = p.geo_a.z;
    let ybase = p.geo_a.w;
    let ytip = p.geo_b.x;
    if (abs(hp.y - ybase) < 0.01 && p.geo_b.y > 0.5) {
        // Untransformed cones only; transformed caps use cone_cap_normal_world.
        if (rd.y > 0.0) {
            return vec3<f32>(0.0, -1.0, 0.0);
        }
        return vec3<f32>(0.0, 1.0, 0.0);
    }
    let k = rbase / (ytip - ybase);
    let lx = hp.x - cx;
    let lz = hp.z - cz;
    var lr = sqrt(lx * lx + lz * lz);
    if (lr == 0.0) { lr = 1e-9; }
    let denom = sqrt(1.0 + k * k);
    let ny = k / denom;
    let ns = 1.0 / denom;
    return vec3<f32>(lx / lr * ns, ny, lz / lr * ns);
}

fn torus_normal(p: Prim, hp: vec3<f32>) -> vec3<f32> {
    let center = p.geo_a.xyz;
    let bigR = p.geo_a.w;
    let e = hp - center;
    var l = sqrt(e.x * e.x + e.z * e.z);
    if (l == 0.0) { l = 1e-9; }
    let c = vec3<f32>(e.x / l * bigR, 0.0, e.z / l * bigR);
    return normalize(e - c);
}

fn ring_normal(p: Prim, hp: vec3<f32>) -> vec3<f32> {
    let cx = p.geo_a.x;
    let cz = p.geo_a.y;
    let cy = p.geo_a.w;
    var height = p.geo_b.x;
    if (height <= 0.0) {
        height = 0.03;
    }
    let band_half = height * 0.5;
    if (hp.y <= cy - band_half + 1e-4) {
        return vec3<f32>(0.0, -1.0, 0.0);
    }
    if (hp.y >= cy + band_half - 1e-4) {
        return vec3<f32>(0.0, 1.0, 0.0);
    }
    let lx = hp.x - cx;
    let lz = hp.z - cz;
    var l = sqrt(lx * lx + lz * lz);
    if (l == 0.0) { l = 1e-9; }
    return vec3<f32>(lx / l, 0.0, lz / l);
}

fn lens_normal(p: Prim, hp: vec3<f32>) -> vec3<f32> {
    let cx = p.geo_a.x;
    let cy = p.geo_a.y;
    let cz = p.geo_a.z;
    let r_front = p.geo_b.x;
    let r_back = p.geo_b.y;
    var thickness = p.geo_b.z;
    if (thickness <= 0.0) {
        thickness = 0.004;
    }
    let half_t = thickness * 0.5;
    let y_front = cy - half_t + r_front;
    let y_back = cy + half_t - r_back;
    if (hp.y < cy) {
        var n = vec3<f32>(hp.x - cx, hp.y - y_front, hp.z - cz);
        let l = length(n);
        if (l > 1e-12) {
            return n / l;
        }
        return vec3<f32>(0.0, -1.0, 0.0);
    }
    var n = vec3<f32>(hp.x - cx, hp.y - y_back, hp.z - cz);
    let l = length(n);
    if (l > 1e-12) {
        return n / l;
    }
    return vec3<f32>(0.0, 1.0, 0.0);
}

fn normal_at(p: Prim, hp: vec3<f32>, rd: vec3<f32>) -> vec3<f32> {
    let kind = p.info.x;
    if (FEAT_PRIM_SPHERE && kind == PRIM_SPHERE) {
        return normalize(hp - p.geo_a.xyz);
    }
    if (FEAT_PRIM_PLANE && kind == PRIM_PLANE) {
        return p.geo_a.xyz;
    }
    if (FEAT_PRIM_BOX && kind == PRIM_BOX) {
        return box_normal(p, hp);
    }
    if (FEAT_PRIM_CYLINDER && kind == PRIM_CYLINDER) {
        return cylinder_normal(p, hp);
    }
    if (FEAT_PRIM_CONE && kind == PRIM_CONE) {
        return cone_normal(p, hp, rd);
    }
    if (FEAT_PRIM_RING && kind == PRIM_RING) {
        return ring_normal(p, hp);
    }
    if (FEAT_PRIM_LENS && kind == PRIM_LENS) {
        return lens_normal(p, hp);
    }
    if (!FEAT_PRIM_TORUS) {
        return vec3<f32>(0.0, 1.0, 0.0);
    }
    return torus_normal(p, hp);
}

// PENUMBRA_LIGHT_RADIUS is the size an *undeclared* light is assumed to have, at
// PENUMBRA_LIGHT_REF_DIST away. A true point light casts a geometrically hard
// shadow at any distance, which is what makes CG shadows read as CG.
//
// It is an angle, not a length. A fixed length is wrong in a way that only shows
// up at range: 5 cm is a fair guess for a lamp three metres off, and the same
// 5 cm on a moon 230 units away is a point source, so every shadow it casts is
// razor hard. That is what made a night scene read as harsh — the sun that had
// been carrying the frame was gone, and what remained were distant lights all
// modelled as pinpricks. The scene even drew a moon sphere of radius 30 beside a
// light modelled at 0.05, a 600x mismatch.
//
// Resolving it as `RADIUS * ldist / REF_DIST` makes the assumption "this light
// subtends about this angle", which is what an unspecified source really is.
// Penumbra width then works out at roughly `angle * blocker_distance`,
// independent of how far the light is — the physically right behaviour, and the
// reason a real overcast sky softens everything while a real bare bulb does not.
//
// A scene that declares `radius` on the light overrides this entirely, in world
// units; see packLight in scene.go.
//
// ---- This is the knob for overall shadow softness. ----
//
// Penumbra width is linear in it (`pen = radius * t_block / gap`), so doubling
// it doubles every soft edge in every scene. Edit here and restart — the shader
// relinks from these modules at startup, so no Go rebuild is needed.
//
// Lowering it does two things at once, which is why it is the right knob rather
// than SHADOW_SOFTEN_MAX_PX: shadows get tighter *and* the contact-hardening
// gradient gets more legible, because more of the width range lands below
// shadow_radius()'s knee where it is reproduced faithfully instead of being
// compressed. Measured on one office frame, over the wall a cone lamp casts
// onto — "spread" is the ratio of gather radius between the near and far ends
// of that one shadow, so 1.00x means the whole shadow is blurred identically:
//
//   radius   median blur   spread
//   0.35        27 px       1.10x   (whole wall in deep penumbra)
//   0.15        23          1.22x
//   0.05        16          1.52x   <- current
//   0.03        12          1.72x
//   0.02         9          1.90x   (about as tight as the old clamped look)
//
// Per-light override: the transport exists (Light.shape.x, 0 = use this value),
// but nothing writes it yet. See packLight in scene.go for why scene.Light.Radius
// is not it.
const PENUMBRA_LIGHT_RADIUS: f32 = 0.05;

// The distance at which PENUMBRA_LIGHT_RADIUS is taken literally. Together they
// define the assumed half-angle: 0.05 / 3.0 is about one degree, close to what
// the sun and moon actually subtend. Lights nearer than this get a smaller
// source and sharper shadows, further ones a larger source and softer shadows.
const PENUMBRA_LIGHT_REF_DIST: f32 = 3.0;

const PENUMBRA_EPS: f32 = 1e-3;

// Per-pixel shadow accumulators, filled in while shading and consumed by the
// shadow_soften pass. Nothing here traces anything.
//
// These sum over *all* lights rather than picking a dominant one. Picking was
// the original design and it is subtly wrong: which light wins can flip between
// neighbouring pixels wherever two are comparably bright, and every downstream
// rule then behaves differently on either side of the flip. That showed up as
// speckle along shadow edges and as shadows where only part of the area got
// softened.
//
// A sum has no such discontinuity. `sh_deficit` is the light every blocked
// source is withholding, which means exactly the same thing at every pixel
// whichever lights happen to be involved.
var<private> sh_deficit: vec3<f32> = vec3<f32>(0.0, 0.0, 0.0);
// Every light's contribution, blocked or not. sh_deficit / sh_full is the
// blocked fraction, and dividing is what removes the surface from the quantity
// the filter carries: both numerator and denominator share this point's albedo,
// its AO and its throughput, so all three cancel and what is left is pure
// visibility. What the two sums do *not* share cancels usefully too — a light
// contributes to the fraction in proportion to how much light it is.
var<private> sh_full: vec3<f32> = vec3<f32>(0.0, 0.0, 0.0);
var<private> sh_pen_sum: f32 = 0.0;
var<private> sh_pen_w: f32 = 0.0;
// Ambient occlusion is applied to `lit` *after* the light loop, so the deficit
// above is the direct light before it. The snapshot scales by this so the
// recorded value matches the light the frame actually deposited; without it the
// filter restores un-attenuated light, too bright by 1/ao exactly where AO
// darkens, which is corners and creases — where shadow fringes are.
var<private> sh_ao: f32 = 1.0;

// Linear Rec.709 luminance, for weighting penumbra widths by how much light each
// blocked source actually contributes.
fn shadow_luma(c: vec3<f32>) -> f32 {
    return 0.2126 * c.x + 0.7152 * c.y + 0.0722 * c.z;
}

// A campfire's sub-lights inherit the fire's emitter radius, falling back to the
// global default when the scene did not give one.
// The factor that turns a sub-light's flickering amplitude back into the fire's
// steady output, for the penumbra-width vote only. cf.param.x is the authored
// brightness the flicker oscillates around.
fn campfire_steady_scale(cf: CampfireParams, sl: CampfireSample) -> f32 {
    return cf.param.x / max(sl.intensity, PENUMBRA_EPS);
}

// 0 means the scene declared no size, which shadow_aux_note resolves from
// distance rather than substituting a fixed one.
fn campfire_light_radius(cf: CampfireParams) -> f32 {
    return cf.phase.w;
}

fn shadow_aux_reset() {
    sh_deficit = vec3<f32>(0.0, 0.0, 0.0);
    sh_full = vec3<f32>(0.0, 0.0, 0.0);
    sh_pen_sum = 0.0;
    sh_pen_w = 0.0;
    sh_ao = 1.0;
}

// shadow_aux_note records the light one blocked source is withholding from this
// point, and how wide its penumbra should be.
//
// The width is pure geometry: a light of radius r, an occluder at t_block from
// the receiver and the light at ldist project a half-shadow of r*t/(ldist-t)
// onto the receiver. One blocker distance sizes it exactly, which is what makes
// contact hardening free — an occluder touching the surface gives t=0 and a hard
// edge, and the shadow softens with height on its own.
//
// Several blocked lights are averaged by contribution, so the brightest shadow
// dominates the width without the result jumping when they trade places.
fn shadow_aux_note(hp: vec3<f32>, total: vec3<f32>, ldist: f32, t_block: f32, declared_r: f32, weight_scale: f32) {
    sh_full = sh_full + total;
    if (t_block < 0.0) {
        return;
    }
    // A declared radius is a world size and is used as given. An undeclared one
    // becomes an angle: the same assumed angular size at any range.
    var light_r = declared_r;
    if (light_r <= 0.0) {
        light_r = PENUMBRA_LIGHT_RADIUS * ldist / PENUMBRA_LIGHT_REF_DIST;
    }
    sh_deficit = sh_deficit + total;
    // A blocker closer to the light than the light's own radius is *inside* the
    // source, and r*t/(ldist-t) diverges there. Clamping the gap to the source's
    // own radius bounds the width at r*t/r = t: a penumbra can be as wide as the
    // throw distance and no wider, which is the correct limit for an occluder
    // sitting in the emitter.
    //
    // This used to `return` instead, withholding the penumbra and drawing a hard
    // edge, out of a fear that blurring such a shadow would leak light through
    // whatever encloses the lamp. That fear was misplaced twice over. The leaking
    // it was guarding against turned out to be the *old* depth tolerance, which
    // reached 43% of the depth at grazing angles and has since been replaced by
    // an exact plane prediction; and the width no longer runs away regardless,
    // because shadow_radius() compresses rather than clips.
    //
    // What the guard did do was fire on every campfire in the game. A fire's logs
    // lie across the flame, so the blocker really is inside the emitter, gap is
    // ~0, and every log drew a pitch-black razor-edged shadow — the one case
    // where the answer should obviously have been "very soft indeed".
    let gap = max(ldist - t_block, light_r);
    // World size of one pixel at this depth, so the filter works in the units it
    // will gather in.
    let px = 2.0 * params.fov_scale * length(hp - params.cam_pos.xyz) / f32(params.height);
    let pen = (light_r * t_block / gap) / max(px, PENUMBRA_EPS);
    // Weighted by how much light this source represents, so the brightest shadow
    // sets the width without the result jumping when two lights trade places.
    //
    // `weight_scale` divides out any animated amplitude. Penumbra width is pure
    // geometry — r * t / gap — and does not depend on brightness at all; the
    // luminance here only arbitrates *which* blocked light's width wins. Letting
    // an instantaneous flicker do that arbitration makes the blur radius pulse
    // on surfaces the flickering light never reaches: a campfire sealed behind a
    // closed door is fully blocked, so it is still recorded (correctly — its
    // light really is missing), and its vote on the width then throbbed at the
    // flame's rate. Measured on the server-room frame, that alone moved the
    // radius on 26.6% of filtered pixels, by up to 5.7 px.
    let weight = shadow_luma(total) * weight_scale;
    // Combined as a contribution-weighted *power* mean with a negative exponent,
    // not an arithmetic one.
    //
    // Several lights blocked at one point have to agree on a single width, and
    // the average chosen decides what the surface looks like. An arithmetic mean
    // is the wrong one: superimpose a hard step on a soft ramp and the result
    // reads hard — the sharpest component dominates what the eye sees — but the
    // arithmetic mean smears it. A contact shadow at 0.5 px crossing a broad one
    // at 60 averages to 30, and the contact edge is gone. That is the "contact
    // shadows are uniformly soft" report, and it is why every attempt to fix it
    // by splitting the deficit into per-width bands was solving the wrong
    // problem: the goal was only ever to avoid taking this average.
    //
    // A negative exponent lets the tightest blocked light pull the result toward
    // itself, in proportion to how much light it actually blocks, so a negligible
    // sliver cannot harden everything. SHADOW_WIDTH_MEAN_POWER = -1 cancels back
    // to the arithmetic mean exactly, 1 is the harmonic mean, and large values
    // approach the minimum.
    //
    // It is also structurally incapable of inventing edges, unlike every banded
    // scheme: there is one signal filtered at one radius, so there is no
    // partition whose parts can fail to recombine.
    sh_pen_sum = sh_pen_sum + weight / pow(max(pen, SHADOW_WIDTH_FLOOR), SHADOW_WIDTH_MEAN_POWER);
    sh_pen_w = sh_pen_w + weight;
}

fn shadow_aux_snapshot(tw: vec3<f32>, n: vec3<f32>, depth: f32) -> ShadowAux {
    var pen = 0.0;
    if (sh_pen_w > 0.0 && sh_pen_sum > 0.0) {
        pen = pow(sh_pen_w / sh_pen_sum, 1.0 / SHADOW_WIDTH_MEAN_POWER);
    }
    // The fraction needs no scaling at all — tw and AO are in both sums.
    let frac = sh_deficit / max(sh_full, vec3<f32>(PENUMBRA_EPS, PENUMBRA_EPS, PENUMBRA_EPS));
    return ShadowAux(frac, pen, n, depth, frac, 0.0, tw * sh_full * sh_ao, 0.0);
}

fn shadow_blocker_t(origin: vec3<f32>, dir: vec3<f32>, max_t: f32) -> f32 {
    prof_inc(PROF_SHADOW_RAYS, 1u);
    var best = -1.0;
    let tb = blocker_bvh_any_hit(origin, dir, max_t);
    if (tb >= 0.0) {
        if (!SHADOW_NEAREST_BLOCKER) {
            prof_inc(PROF_SHADOW_BLOCK, 1u);
            return tb;
        }
        best = tb;
    }
    let limit = max_t - 0.05;
    for (var i = 0u; i < params.blocker_plane_count; i = i + 1u) {
        let bi = idx_tables[params.table_base.x + i];
        let t = intersect_blocker(bi, origin, dir);
        if (t > RAY_EPSILON && t < limit) {
            if (!SHADOW_NEAREST_BLOCKER) {
                prof_inc(PROF_SHADOW_BLOCK, 1u);
                return t;
            }
            if (best < 0.0 || t < best) {
                best = t;
            }
        }
    }
    for (var i = 0u; i < params.terrain_count; i = i + 1u) {
        if (terrain_shadow_skip(i, origin, dir, max_t)) {
            continue;
        }
        let t = hit_terrain(i, origin, dir, max_t, true, true);
        if (t > RAY_EPSILON && t < limit) {
            if (!SHADOW_NEAREST_BLOCKER) {
                prof_inc(PROF_SHADOW_BLOCK, 1u);
                return t;
            }
            if (best < 0.0 || t < best) {
                best = t;
            }
        }
    }
    if (best >= 0.0) {
        prof_inc(PROF_SHADOW_BLOCK, 1u);
    }
    return best;
}

fn shadowed(origin: vec3<f32>, dir: vec3<f32>, max_t: f32) -> bool {
    return shadow_blocker_t(origin, dir, max_t) >= 0.0;
}

fn tw_peak(tw: vec3<f32>) -> f32 {
    return max(tw.x, max(tw.y, tw.z));
}

fn spot_dir(yaw: f32, pitch: f32) -> vec3<f32> {
    let cp = cos(pitch);
    return vec3<f32>(sin(yaw) * cp, sin(pitch), -cos(yaw) * cp);
}

// Shared core for one point light (static lights + campfire sub-lights).
// Tuning knobs at the top of this file: LIGHT_CULL_EPS, SHADOW_SKIP_LEVELS,
// LIGHT_ATTEN_BASE, LIGHT_ATTEN_QUADRATIC. tw is the ray segment throughput.
// spec/shine add Blinn–Phong highlights when spec > 0 (diffuse/checker/glass).
// Point-light distance attenuation: inverse-square-ish falloff with a flat top
// near the light, joined smoothly.
//
// The intended shape is `min(1, 1/(BASE + q*d²))`. Written that way it has a
// *corner*: the flat top runs out to `BASE + q*d² = 1`, which with BASE = 0.5 and
// q = 0.08 is exactly d = 2.5, and the slope jumps from 0 to -0.4/unit there.
// The value is continuous, so it is easy to assume this is harmless — but the
// eye differentiates, and a slope discontinuity in an otherwise smooth gradient
// is a Mach band. The result was a hard round ring at a fixed 2.5-unit radius
// around *every* point light in every scene, independent of geometry.
//
// So express it as `1/max(1, u)` and smooth the max instead. This is the
// standard hyperbolic soft-max: it equals the sharp one when KNEE = 0, and KNEE
// buys a rounded join at the cost of dipping `sqrt(KNEE)/2` below it at the
// corner. That cost is strictly local — measured 3.7% at the ring radius, 1.2%
// one unit inside it, 0.3% in the far field — which is why the knee can be wide
// enough to hide the ring without re-lighting any scene.
//
// The far field is unchanged to well under a percent, so the CPU-side cull radius
// in `lightCull` (which inverts the sharp form) stays correct and needs no
// matching constant.
fn light_atten(d2: f32) -> f32 {
    let u = LIGHT_ATTEN_BASE + d2 * LIGHT_ATTEN_QUADRATIC;
    let e = u - 1.0;
    return 1.0 / (0.5 * (u + 1.0 + sqrt(e * e + LIGHT_ATTEN_KNEE)));
}

fn add_point_light_raw(lit: vec3<f32>, hp: vec3<f32>, albedo: vec3<f32>, n: vec3<f32>, ep: vec3<f32>, pos: vec3<f32>, color: vec3<f32>, cull_r2: f32, inv_r2: f32, tw: vec3<f32>, spot_axis: vec3<f32>, cos_outer: f32, rd: vec3<f32>, spec: f32, shine: f32, shadow: bool, light_r: f32, pen_weight_scale: f32) -> vec3<f32> {
    let twm = tw_peak(tw);
    let ld = pos - hp;
    let d2 = dot(ld, ld);
    if (d2 > cull_r2) {
        return lit;
    }
    var ldist = sqrt(d2);
    if (ldist == 0.0) {
        ldist = 1.0;
    }
    let ln = ld / ldist;
    let ndl = dot(n, ln);
    if (ndl < 0.001) {
        return lit;
    }
    var att = light_atten(d2);
    if (inv_r2 > 0.0) {
        let x = d2 * inv_r2;
        var w = 1.0 - x * x;
        if (w < 0.0) {
            w = 0.0;
        }
        att = att * w * w;
    }
    if (cos_outer > 0.0) {
        let cos_theta = dot(-ln, spot_axis);
        if (cos_theta < cos_outer) {
            return lit;
        }
        let edge = min(0.2, 1.0 - cos_outer);
        att = att * smoothstepf(cos_outer, cos_outer + edge, cos_theta);
    }
    let color_peak = max(color.x, max(color.y, color.z));
    if (twm * att * ndl * color_peak < LIGHT_CULL_EPS) {
        return lit;
    }
    let unshadowed = color * (att * ndl) * albedo;
    var spec_contrib = vec3<f32>(0.0);
    if (spec > 0.0 && shine > 0.0) {
        let v = normalize(-rd);
        let h = normalize(ln + v);
        let ndh = max(dot(n, h), 0.0);
        spec_contrib = color * (att * ndl) * spec * pow(ndh, shine);
    }
    let total = unshadowed + spec_contrib;
    let peak = max(total.x, max(total.y, total.z));
    // Is this light's shadow worth a BVH traversal? The question is whether the
    // shadow would be *visible*, so it is asked in display space, against the
    // radiance already accumulated at this hit. A dim light landing on an
    // already-lit surface cannot darken it perceptibly no matter what occludes
    // it, while the same light against near-black is the only thing there.
    let step_levels = display_step_levels(twm * tw_peak(lit), twm * peak);
    if (!shadow || step_levels < SHADOW_SKIP_LEVELS) {
        return lit + total;
    }
    if (params.shadows == 0u) {
        return lit + total;
    }
    let t_block = shadow_blocker_t(ep, ln, ldist);
    if (t_block < 0.0) {
        // Recorded even though nothing is occluded: the filter widens a penumbra
        // outward as well as inward, so a lit pixel beside a shadow has to know
        // which light it would be losing.
        shadow_aux_note(hp, total, ldist, -1.0, light_r, pen_weight_scale);
        return lit + total;
    }
    shadow_aux_note(hp, total, ldist, t_block, light_r, pen_weight_scale);
    return lit;
}

fn add_point_light(lit: vec3<f32>, hp: vec3<f32>, albedo: vec3<f32>, n: vec3<f32>, ep: vec3<f32>, light: Light, tw: vec3<f32>, rd: vec3<f32>, spec: f32, shine: f32, shadow: bool) -> vec3<f32> {
    var axis = vec3<f32>(0.0, 1.0, 0.0);
    var cos_outer = 0.0;
    if (light.pos.w > 0.5) {
        axis = spot_dir(light.falloff.z, light.falloff.w);
        cos_outer = light.color.w;
    }
    // A light that declares no radius falls back to the global default, so
    // scenes that never mention one behave exactly as before.
    // 0 stays 0: an undeclared size is resolved from distance in shadow_aux_note,
    // where ldist is in scope.
    let lr = light.shape.x;
    return add_point_light_raw(lit, hp, albedo, n, ep, light.pos.xyz, light.color.xyz, light.falloff.x, light.falloff.y, tw, axis, cos_outer, rd, spec, shine, shadow, lr, 1.0);
}

// ao_sample reproduces aoVolume.sample: nudge off the surface by bias along n,
// then trilinearly interpolate the ambient cube and blend its three relevant
// faces by the surface normal.
fn ao_cell_frac(fc: f32, ncell: u32) -> vec2<f32> {
    if (fc <= 0.0) {
        return vec2<f32>(0.0, 0.0);
    }
    let last = f32(ncell - 1u);
    if (fc >= last) {
        return vec2<f32>(last, 0.0);
    }
    let i = floor(fc);
    return vec2<f32>(i, fc - i);
}

fn ao_face(comp: f32, pos_face: u32) -> vec2<f32> {
    if (comp >= 0.0) {
        return vec2<f32>(f32(pos_face), comp * comp);
    }
    return vec2<f32>(f32(pos_face + 1u), comp * comp);
}

fn ao_corner(ix: u32, iy: u32, iz: u32, fx: u32, fy: u32, fz: u32, wx: f32, wy: f32, wz: f32) -> f32 {
    let base = (((iz * params.ao_ny) + iy) * params.ao_nx + ix) * 6u;
    return wx * ao_volume[base + fx] + wy * ao_volume[base + fy] + wz * ao_volume[base + fz];
}

fn ao_sample(p_in: vec3<f32>, n: vec3<f32>) -> f32 {
    let p = p_in + n * params.ao_bias;
    let fx = (p.x - params.ao_min.x) * params.ao_inv - 0.5;
    let fy = (p.y - params.ao_min.y) * params.ao_inv - 0.5;
    let fz = (p.z - params.ao_min.z) * params.ao_inv - 0.5;

    let cx = ao_cell_frac(fx, params.ao_nx);
    let cy = ao_cell_frac(fy, params.ao_ny);
    let cz = ao_cell_frac(fz, params.ao_nz);
    let ix0 = u32(cx.x);
    let iy0 = u32(cy.x);
    let iz0 = u32(cz.x);
    let ix1 = min(ix0 + 1u, params.ao_nx - 1u);
    let iy1 = min(iy0 + 1u, params.ao_ny - 1u);
    let iz1 = min(iz0 + 1u, params.ao_nz - 1u);
    let tx = cx.y;
    let ty = cy.y;
    let tz = cz.y;

    let fxw = ao_face(n.x, 0u);
    let fyw = ao_face(n.y, 2u);
    let fzw = ao_face(n.z, 4u);
    let fxi = u32(fxw.x);
    let fyi = u32(fyw.x);
    let fzi = u32(fzw.x);
    let wx = fxw.y;
    let wy = fyw.y;
    let wz = fzw.y;

    let c000 = ao_corner(ix0, iy0, iz0, fxi, fyi, fzi, wx, wy, wz);
    let c100 = ao_corner(ix1, iy0, iz0, fxi, fyi, fzi, wx, wy, wz);
    let c010 = ao_corner(ix0, iy1, iz0, fxi, fyi, fzi, wx, wy, wz);
    let c110 = ao_corner(ix1, iy1, iz0, fxi, fyi, fzi, wx, wy, wz);
    let c001 = ao_corner(ix0, iy0, iz1, fxi, fyi, fzi, wx, wy, wz);
    let c101 = ao_corner(ix1, iy0, iz1, fxi, fyi, fzi, wx, wy, wz);
    let c011 = ao_corner(ix0, iy1, iz1, fxi, fyi, fzi, wx, wy, wz);
    let c111 = ao_corner(ix1, iy1, iz1, fxi, fyi, fzi, wx, wy, wz);

    let c00 = c000 + (c100 - c000) * tx;
    let c10 = c010 + (c110 - c010) * tx;
    let c01 = c001 + (c101 - c001) * tx;
    let c11 = c011 + (c111 - c011) * tx;
    let c0 = c00 + (c10 - c00) * ty;
    let c1 = c01 + (c11 - c01) * ty;
    return c0 + (c1 - c0) * tz;
}

struct CampfireSample {
    pos: vec3<f32>,
    col: vec3<f32>,
    // The instantaneous intensity this sample was built with. Only the penumbra
    // width arbitration uses it, to divide the flicker back out; see
    // shadow_aux_note.
    intensity: f32,
};

fn campfire_cull(cf: CampfireParams) -> vec2<f32> {
    let range = cf.core.w;
    if (range > 0.0) {
        let r2 = range * range;
        return vec2<f32>(r2, 1.0 / r2);
    }
    let peak = max(cf.color.x, max(cf.color.y, cf.color.z)) * cf.param.x * (1.0 + cf.param.z);
    if (peak > LIGHT_CULL_EPS * LIGHT_ATTEN_BASE) {
        let r2 = (peak / LIGHT_CULL_EPS - LIGHT_ATTEN_BASE) / LIGHT_ATTEN_QUADRATIC;
        return vec2<f32>(r2, 0.0);
    }
    return vec2<f32>(0.0, 0.0);
}

fn campfire_sublight(cf: CampfireParams, j: u32, ts: f32) -> CampfireSample {
    var fs = cf.phase.z;
    if (fs <= 0.0) {
        fs = 1.0;
    }
    var base: vec3<f32>;
    var tint: vec3<f32>;
    if (j == 0u) {
        base = vec3<f32>(0.22, 0.06, 0.14);
        tint = vec3<f32>(1.00, 0.60, 0.28);
    } else if (j == 1u) {
        base = vec3<f32>(-0.24, 0.26, -0.12);
        tint = vec3<f32>(1.00, 0.80, 0.46);
    } else {
        base = vec3<f32>(0.03, 0.52, 0.16);
        tint = vec3<f32>(1.00, 0.92, 0.66);
    }
    base = base * fs;
    let ph = cf.phase.x + f32(j) * 1.7;
    let fl = 0.6 * sin(ts * 7.0 + ph) + 0.3 * sin(ts * 13.0 + ph * 2.1) + 0.1 * sin(ts * 23.0 + ph * 3.7);
    let intensity = cf.param.x * (1.0 + cf.param.z * fl);
    let pos = cf.core.xyz + base + vec3<f32>(
        cf.param.y * (0.7 * sin(ts * 9.0 + ph * 1.3) + 0.3 * sin(ts * 17.0 + ph * 2.7)),
        cf.param.y * (0.4 + 0.4 * sin(ts * 15.0 + ph)),
        cf.param.y * (0.7 * sin(ts * 11.0 + ph * 1.9) + 0.3 * sin(ts * 19.0 + ph * 0.7)),
    );
    let used = max(intensity, 0.15 * cf.param.x);
    let col = cf.color.xyz * used * tint;
    return CampfireSample(pos, col, used);
}

// Direct diffuse lighting: ambient + point lights + campfires + baked AO.
// Campfire core occlusion also uses SHADOW_SKIP_LEVELS (see loop below).
fn scene_ambient(n: vec3<f32>) -> vec3<f32> {
    let t = clamp(n.y * 0.5 + 0.5, 0.0, 1.0);
    return mix(params.ambient_ground.xyz, params.ambient_sky.xyz, t);
}

// light_span returns the [start, end) range of light_grid_idx listing every
// light whose influence sphere can reach hp. A point outside the grid is beyond
// every light's cull radius, so its span is empty.
//
// The grid is conservative, so this is not an approximation: add_point_light_raw
// already returns unchanged for a light past its cull radius, and any light this
// skips would have taken that early return.
fn light_span(hp: vec3<f32>) -> vec2<u32> {
    let dim = params.light_grid_dim.xyz;
    let c = floor((hp - params.light_grid_min.xyz) * params.light_grid_inv_cell.xyz);
    if (any(c < vec3<f32>(0.0)) || any(c >= vec3<f32>(dim))) {
        return vec2<u32>(0u, 0u);
    }
    let cells = params.table_base.y + params.table_base.z;
    let ci = (u32(c.z) * dim.y + u32(c.y)) * dim.x + u32(c.x);
    let first = params.table_base.y + params.light_grid_dim.w;
    return vec2<u32>(first + idx_tables[cells + ci], first + idx_tables[cells + ci + 1u]);
}

// wide_span is the prefix of scene-spanning lights that every lit point has to
// evaluate, because clustering them would put them in most cells anyway.
fn wide_span() -> vec2<u32> {
    return vec2<u32>(params.table_base.y, params.table_base.y + params.table_base.z);
}

fn shade_ghost(hp: vec3<f32>, alb: vec3<f32>, n: vec3<f32>, ep: vec3<f32>) -> vec3<f32> {
    // Faint second-pane lobe: ambient + direct light, no shadow/AO/campfire rays.
    let tw_skip = vec3<f32>(GHOST_SHADOW_TW);
    var lit = alb * scene_ambient(n);
    let rd_skip = vec3<f32>(0.0, 0.0, -1.0);
    let wide = wide_span();
    for (var i = wide.x; i < wide.y; i = i + 1u) {
        lit = add_point_light(lit, hp, alb, n, ep, lights[idx_tables[i]], tw_skip, rd_skip, 0.0, 0.0, false);
    }
    let span = light_span(hp);
    for (var i = span.x; i < span.y; i = i + 1u) {
        lit = add_point_light(lit, hp, alb, n, ep, lights[idx_tables[i]], tw_skip, rd_skip, 0.0, 0.0, false);
    }
    return lit;
}

fn shade_diffuse(hp: vec3<f32>, alb: vec3<f32>, n: vec3<f32>, ep: vec3<f32>, tw: vec3<f32>, rd: vec3<f32>, spec: f32, shine: f32) -> vec3<f32> {
    shadow_aux_reset();
    var lit = alb * scene_ambient(n);
    let wide = wide_span();
    for (var i = wide.x; i < wide.y; i = i + 1u) {
        lit = add_point_light(lit, hp, alb, n, ep, lights[idx_tables[i]], tw, rd, spec, shine, true);
    }
    let span = light_span(hp);
    for (var i = span.x; i < span.y; i = i + 1u) {
        lit = add_point_light(lit, hp, alb, n, ep, lights[idx_tables[i]], tw, rd, spec, shine, true);
    }
    // FEAT_CAMPFIRE folds to false in scenes with no campfire, so the compiler
    // drops this whole loop — including the sub-light and flame-cull helpers it
    // would otherwise keep live.
    for (var ci = 0u; FEAT_CAMPFIRE && ci < params.campfire_count; ci = ci + 1u) {
        let cf = campfires[ci];
        let core = cf.core.xyz;
        let cl = core - hp;
        let cd2 = dot(cl, cl);
        let cull = campfire_cull(cf);
        if (cd2 > cull.x) {
            continue;
        }
        if (params.shadows != 0u) {
            var cdist = sqrt(cd2);
            if (cdist == 0.0) {
                cdist = 1.0;
            }
            let cdir = cl / cdist;
            let ndl = dot(n, cdir);
            if (ndl >= 0.001) {
                let att = light_atten(cd2);
                let peak = att * ndl * cf.param.x * max(alb.x, max(alb.y, alb.z));
                if (display_step_levels(tw_peak(tw) * tw_peak(lit), tw_peak(tw) * peak) >= SHADOW_SKIP_LEVELS) {
                    let ct = shadow_blocker_t(ep, cdir, cdist);
                    if (ct >= 0.0) {
                        // The whole campfire is occluded here, so its sub-lights
                        // are skipped and never record anything — which is why
                        // campfire shadows stayed hard without this.
                        //
                        // The contribution is *measured*, not estimated: each
                        // sub-light is evaluated with shadow = false, which
                        // returns its unoccluded contribution without tracing a
                        // ray and without recording anything itself. An earlier
                        // version guessed at it (one sub-light scaled by three)
                        // and guessed high, so the filter added back light the
                        // pixel never had — campfire shadows brightened their
                        // surroundings instead of darkening them.
                        let cts = params.time * cf.param.w;
                        var probe = vec3<f32>(0.0, 0.0, 0.0);
                        var inst = 0.0;
                        for (var pj = 0u; pj < 3u; pj = pj + 1u) {
                            let psl = campfire_sublight(cf, pj, cts);
                            inst = inst + psl.intensity;
                            probe = add_point_light_raw(probe, hp, alb, n, ep, psl.pos, psl.col, cull.x, cull.y, tw, vec3<f32>(0.0, 1.0, 0.0), 0.0, rd, spec, shine, false, campfire_light_radius(cf), 1.0);
                        }
                        shadow_aux_note(hp, probe, cdist, ct, campfire_light_radius(cf),
                            3.0 * cf.param.x / max(inst, PENUMBRA_EPS));
                        continue;
                    }
                }
            }
        }
        // Each sub-light shadows itself. The sub-light positions are driven by
        // params.time, so these are the rays that animate a campfire's cast
        // shadows; the static core test above only decides whether the whole
        // campfire is occluded and cannot stand in for them.
        let ts = params.time * cf.param.w;
        for (var j = 0u; j < 3u; j = j + 1u) {
            let sl = campfire_sublight(cf, j, ts);
            lit = add_point_light_raw(lit, hp, alb, n, ep, sl.pos, sl.col, cull.x, cull.y, tw, vec3<f32>(0.0, 1.0, 0.0), 0.0, rd, spec, shine, true, campfire_light_radius(cf), campfire_steady_scale(cf, sl));
        }
    }
    if (params.ao_enabled != 0u) {
        sh_ao = ao_sample(ep, n);
        lit = lit * sh_ao;
    }
    return lit;
}

// shade_specular adds Blinn–Phong point-light highlights only (no ambient/diffuse).
fn shade_specular(hp: vec3<f32>, n: vec3<f32>, ep: vec3<f32>, tw: vec3<f32>, rd: vec3<f32>, spec: f32, shine: f32) -> vec3<f32> {
    if (spec <= 0.0 || shine <= 0.0) {
        return vec3<f32>(0.0);
    }
    let no_diffuse = vec3<f32>(0.0);
    var lit = vec3<f32>(0.0);
    let wide = wide_span();
    for (var i = wide.x; i < wide.y; i = i + 1u) {
        lit = add_point_light(lit, hp, no_diffuse, n, ep, lights[idx_tables[i]], tw, rd, spec, shine, true);
    }
    let span = light_span(hp);
    for (var i = span.x; i < span.y; i = i + 1u) {
        lit = add_point_light(lit, hp, no_diffuse, n, ep, lights[idx_tables[i]], tw, rd, spec, shine, true);
    }
    for (var ci = 0u; FEAT_CAMPFIRE && ci < params.campfire_count; ci = ci + 1u) {
        let cf = campfires[ci];
        let core = cf.core.xyz;
        let cl = core - hp;
        let cd2 = dot(cl, cl);
        let cull = campfire_cull(cf);
        if (cd2 > cull.x) {
            continue;
        }
        if (params.shadows != 0u) {
            var cdist = sqrt(cd2);
            if (cdist == 0.0) {
                cdist = 1.0;
            }
            let cdir = cl / cdist;
            let ndl = dot(n, cdir);
            if (ndl >= 0.001) {
                let att = light_atten(cd2);
                let peak = att * ndl * cf.param.x;
                if (display_step_levels(tw_peak(tw) * tw_peak(lit), tw_peak(tw) * peak) >= SHADOW_SKIP_LEVELS) {
                    if (shadowed(ep, cdir, cdist)) {
                        continue;
                    }
                }
            }
        }
        // Each sub-light shadows itself. The sub-light positions are driven by
        // params.time, so these are the rays that animate a campfire's cast
        // shadows; the static core test above only decides whether the whole
        // campfire is occluded and cannot stand in for them.
        let ts = params.time * cf.param.w;
        for (var j = 0u; j < 3u; j = j + 1u) {
            let sl = campfire_sublight(cf, j, ts);
            lit = add_point_light_raw(lit, hp, no_diffuse, n, ep, sl.pos, sl.col, cull.x, cull.y, tw, vec3<f32>(0.0, 1.0, 0.0), 0.0, rd, spec, shine, true, campfire_light_radius(cf), campfire_steady_scale(cf, sl));
        }
    }
    return lit;
}

fn jitter_dir(d: vec3<f32>, p: vec3<f32>, rough: f32) -> vec3<f32> {
    if (rough <= 0.0) {
        return d;
    }
    return normalize(d + vec3<f32>(
        sin(p.x * 73.1 + p.y * 17.3) * 0.5 * rough,
        sin(p.y * 91.7 + p.z * 37.1) * 0.5 * rough,
        sin(p.z * 53.3 + p.x * 61.7) * 0.5 * rough,
    ));
}

fn terrain_albedo(i: u32, p: vec3<f32>, n: vec3<f32>) -> vec3<f32> {
    if (!FEAT_TERRAIN) { return vec3<f32>(0.5, 0.5, 0.5); }
    let tr = terrains[i];
    let slope = 1.0 - n.y;
    let j = 0.08 * perlin(p.x * 0.7, 0.0, p.z * 0.7);
    let rock_w = smoothstepf(tr.blend.x, tr.blend.y, slope + j);
    let snow_w = smoothstepf(tr.blend.z, tr.blend.w, p.y + 2.0 * j);
    var c = texture_eval(tr.material.x, p, n, tr.color0.xyz);
    if (rock_w > 0.001) {
        c = mix3(c, texture_eval(tr.material.y, p, n, tr.color1.xyz), rock_w);
    }
    if (snow_w > 0.001) {
        c = mix3(c, texture_eval(tr.material.z, p, n, tr.color2.xyz), snow_w);
    }
    return terrain_zone_albedo(i, p, n, c);
}
