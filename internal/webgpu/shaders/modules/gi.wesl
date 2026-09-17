// gi.wesl — the traced indirect-light probe field.
//
// Storage, sampling and geometry for the probe field: the octahedral maps a
// probe holds, the cascades and sparse baked volume they live in, and the
// weighting that turns eight surrounding probes into an ambient term. What it
// deliberately does not contain is lighting — how a probe ray shades what it
// hits is in shade.wesl with the other light loops, and how it traces is in
// trace.wesl. This module depends only on types and math, which is what keeps
// it out of the way of everything else.
//
// Off unless RAYTRACER_LIVE_GI or RAYTRACER_GI_BAKE is set. See
// docs/live-gi.md for what it measures and what it still gets wrong.

import package::types::{GI_C0_MAX, GI_OCT_DEPTH, GI_OCT_IRR, GI_PROBE_BASE, GI_PROBE_EMPTY, GI_PROBE_FLOATS, GI_PROBE_MOMENTS, GI_PROBE_RAYS, ao_volume, params};
import package::math::{smoothstepf};
import package::shade::{gi_probe_next};

const GI_CLAMP: f32 = 8.0;

// Probe irradiance is a cosine-weighted average of real traced radiance, so
// this is not compensating for a made-up falloff — it is the conversion from
// "average incident radiance" to the ambient term the shader multiplies by
// albedo. One means take it as measured.
const GI_PROBE_GAIN: f32 = 1.0;

// Width of the read-side fade at the box boundary, in cells.
//
// Wider wastes the grid's useful volume, and the volume is already the scarce
// thing: the box only reaches 9 m even biased forward. A cell and a half is
// the narrowest that covers the trilinear read, which already spans one.
const GI_EDGE_CELLS: f32 = 1.5;


// Set while the probe pass is tracing. Probe rays must not inject into the
// mode-1/2 field, and they need the first hit's distance for the visibility
// moments, which TraceResult has no room for.
var<private> gi_in_probe: bool = false;
var<private> gi_probe_hit_t: f32 = 0.0;
// Per-ray randomness for the probe pass's light resampling.
var<private> gi_probe_rand: u32 = 0u;
// Candidates drawn per shading point before one is kept.
const GI_PROBE_RIS: u32 = 4u;
// Segments a probe ray may follow. Diffuse surfaces end it immediately; this
// is the budget for seeing through glass and off mirrors. Two matches what
// ray_color gave a probe ray, which is the behaviour the numbers were tuned
// against.
const GI_PROBE_BOUNCES: u32 = 2u;

// What a probe ray that hits nothing records as its distance. Large enough
// that the sky never reads as an occluder, finite so the moments stay bounded.
const GI_PROBE_FAR: f32 = 1.0e4;

// How fast a probe forgets. Low, because each update is 32 stratified rays and
// therefore noisy on its own; the blend is what turns a sequence of cheap
// noisy estimates into a stable field.
// How fast a probe forgets — DDGI's hysteresis, as a blend rate.
//
// Frame-to-frame flicker scales with this almost exactly: measured on the
// villa at 0.08, 0.04 and 0.02, the field moved 0.45, 0.23 and 0.12 display
// levels a frame with the camera still. Anything above about 0.03 reads as
// soft blobs drifting over flat walls.
const GI_PROBE_HYSTERESIS: f32 = 0.03;


fn gi_on() -> bool {
    return params.gi_dim.w != 0u;
}

// Rec. 709 luminance, for comparing two probe samples by brightness without
// letting one channel speak for the colour.
fn gi_luma(c: vec3<f32>) -> f32 {
    return dot(c, vec3<f32>(0.2126, 0.7152, 0.0722));
}

// ---------------------------------------------------------------------------
// Probe field (mode 3)
//
// The mode-1/2 grid injected radiance from whatever the frame happened to
// shade, then propagated it with an all-pairs gather over cells. The gather is
// O(cells^2) — 1728 cells is 3M pairs — which is what capped the grid at 12^3,
// which is why it had to follow the camera, which is why it popped and why
// nothing more than 9 m away ever received indirect light.
//
// Probes replace the gather with tracing. Each probe casts GI_PROBE_RAYS rays
// and shades what they hit with the megakernel's own lighting, so the update
// is O(probes * rays) — linear — and the grid can cover the whole level for
// the cost the 12^3 box used to take. Nothing is culled, so nothing pops, and
// a probe sees a lamp across the room whether or not it is on screen.
// ---------------------------------------------------------------------------

fn gi_probe_on() -> bool {
    return params.gi_dim.w == 3u;
}

// Storage slot of a probe. Plain linear indexing: the grid is anchored to the
// scene, not the viewer, so no slot ever has to change which cell it means.
// ---------------------------------------------------------------------------
// Octahedral mapping
//
// The sphere onto a square, with no seams that matter and near-uniform area —
// which is why DDGI stores probe data this way rather than in a cube or a
// handful of lobes. Directions map to [-1,1]^2; the outer half of the square
// is the lower hemisphere, folded.
// ---------------------------------------------------------------------------

fn oct_encode(d: vec3<f32>) -> vec2<f32> {
    let n = d / (abs(d.x) + abs(d.y) + abs(d.z));
    var uv = n.xy;
    if (n.z < 0.0) {
        // Fold the lower hemisphere out into the corners.
        uv = (vec2<f32>(1.0) - abs(n.yx)) * vec2<f32>(
            select(-1.0, 1.0, n.x >= 0.0),
            select(-1.0, 1.0, n.y >= 0.0));
    }
    return uv;
}

fn oct_decode(uv: vec2<f32>) -> vec3<f32> {
    var d = vec3<f32>(uv, 1.0 - abs(uv.x) - abs(uv.y));
    if (d.z < 0.0) {
        let s = vec2<f32>(
            select(-1.0, 1.0, d.x >= 0.0),
            select(-1.0, 1.0, d.y >= 0.0));
        let xy = (vec2<f32>(1.0) - abs(d.yx)) * s;
        d = vec3<f32>(xy, d.z);
    }
    return normalize(d);
}

// The direction a texel centre stands for.
fn oct_texel_dir(t: vec2<u32>, n: u32) -> vec3<f32> {
    let uv = (vec2<f32>(t) + vec2<f32>(0.5)) / f32(n) * 2.0 - vec2<f32>(1.0);
    return oct_decode(uv);
}

// Fold a texel coordinate that has run off an edge back onto the map.
//
// The octahedron's border is not a clamp and not a wrap: stepping off one edge
// continues onto the same edge mirrored, because the two are the same great
// circle folded flat. Getting this wrong puts a seam through every probe.
fn oct_wrap(c: vec2<i32>, n: i32) -> vec2<i32> {
    var t = c;
    if (t.x < 0) {
        t = vec2<i32>(-1 - t.x, n - 1 - t.y);
    } else if (t.x >= n) {
        t = vec2<i32>(2 * n - 1 - t.x, n - 1 - t.y);
    }
    if (t.y < 0) {
        t = vec2<i32>(n - 1 - t.x, -1 - t.y);
    } else if (t.y >= n) {
        t = vec2<i32>(n - 1 - t.x, 2 * n - 1 - t.y);
    }
    return clamp(t, vec2<i32>(0), vec2<i32>(n - 1));
}

fn gi_irr_texel(base: u32, t: vec2<i32>) -> vec3<f32> {
    let c = oct_wrap(t, i32(GI_OCT_IRR));
    let o = base + (u32(c.y) * GI_OCT_IRR + u32(c.x)) * 3u;
    return vec3<f32>(ao_volume[o], ao_volume[o + 1u], ao_volume[o + 2u]);
}

fn gi_depth_texel(base: u32, t: vec2<i32>) -> vec2<f32> {
    let c = oct_wrap(t, i32(GI_OCT_DEPTH));
    let o = base + GI_PROBE_MOMENTS + (u32(c.y) * GI_OCT_DEPTH + u32(c.x)) * 2u;
    return vec2<f32>(ao_volume[o], ao_volume[o + 1u]);
}

// Bilinear octahedral lookup. Written out rather than using a texture because
// the probe field lives in the tail of ao_volume: the megakernel is at Metal's
// 31-buffer limit and has no binding to spare for a real texture array.
fn gi_sample_irr(base: u32, dir: vec3<f32>) -> vec3<f32> {
    let n = f32(GI_OCT_IRR);
    let uv = (oct_encode(dir) * 0.5 + vec2<f32>(0.5)) * n - vec2<f32>(0.5);
    let i0 = vec2<i32>(floor(uv));
    let f = uv - floor(uv);
    let a = mix(gi_irr_texel(base, i0), gi_irr_texel(base, i0 + vec2<i32>(1, 0)), f.x);
    let b = mix(gi_irr_texel(base, i0 + vec2<i32>(0, 1)), gi_irr_texel(base, i0 + vec2<i32>(1, 1)), f.x);
    return mix(a, b, f.y);
}

fn gi_sample_depth(base: u32, dir: vec3<f32>) -> vec2<f32> {
    let n = f32(GI_OCT_DEPTH);
    let uv = (oct_encode(dir) * 0.5 + vec2<f32>(0.5)) * n - vec2<f32>(0.5);
    let i0 = vec2<i32>(floor(uv));
    let f = uv - floor(uv);
    let a = mix(gi_depth_texel(base, i0), gi_depth_texel(base, i0 + vec2<i32>(1, 0)), f.x);
    let b = mix(gi_depth_texel(base, i0 + vec2<i32>(0, 1)), gi_depth_texel(base, i0 + vec2<i32>(1, 1)), f.x);
    return mix(a, b, f.y);
}

// ---------------------------------------------------------------------------
// Baked volume (mode 3 with a bake loaded, and what the baker itself writes)
//
// One static grid over the whole scene, at whatever spacing the bake chose.
// Nothing follows the camera and nothing is culled, so there is no boundary to
// cross and nothing to pop — the failure the live cascades never stopped
// having. It is affordable only because it is sparse: an index grid of one
// u32 per cell points into probe records that exist only where there is
// geometry to light. On the office that is a tenth of the cells; on the villa,
// whose bounds are mostly terrain, a two-hundredth.
// ---------------------------------------------------------------------------

fn gi_bake_on() -> bool {
    return params.gi_bake.z != 0u;
}

fn gi_bake_cascade() -> GICascade {
    return GICascade(params.gi_bake_min.xyz, params.gi_bake_min.w, params.gi_bake_dim.xyz,
        GI_PROBE_BASE, false, true);
}

// Storage slot of a cell, or GI_PROBE_EMPTY where the bake kept no probe.
fn gi_bake_slot(cell: vec3<u32>) -> u32 {
    let dim = params.gi_bake_dim.xyz;
    if (any(cell >= dim)) {
        return GI_PROBE_EMPTY;
    }
    let li = (cell.z * dim.y + cell.y) * dim.x + cell.x;
    return bitcast<u32>(ao_volume[params.gi_bake.x + li]);
}

// The cell a slot stands for, for the update pass walking active probes.
fn gi_bake_cell_of(slot: u32) -> vec3<u32> {
    let packed = bitcast<u32>(ao_volume[params.gi_bake.y + slot]);
    return vec3<u32>(packed & 0x3ffu, (packed >> 10u) & 0x3ffu, (packed >> 20u) & 0x3ffu);
}

// A cascade: where it is, how fine, and where its probes live.
//
// Two of them. One uniform grid cannot be both level-wide and fine enough to
// resolve room-scale bounce: measured on the villa, 4.6 m probes put the
// fireplace wall's red-to-blue at 1.84 against the path tracer's 3.50, and
// halving the spacing to 2.3 m took it to 4.93 — but at eight times the probe
// count, which needs 25 seconds to converge and does not fit the budget.
//
// So the fine cascade is small and follows the viewer, and the coarse one
// spans the level and never moves. What made this unworkable before is that
// the fallback outside the moving grid was an authored constant, so crossing
// the boundary was a step. Here the fallback is the same kind of field
// produced the same way, so the two agree and the crossing is a blend.
struct GICascade {
    mn: vec3<f32>,
    inv: f32,
    dim: vec3<u32>,
    base: u32,
    // The fine cascade moves, so its slots are keyed on the absolute world
    // cell and a probe that stays in range keeps its history.
    toroidal: bool,
    // The baked volume is sparse: its slots come from an index grid, and a
    // cell with no probe has to be skipped rather than read.
    sparse: bool,
}

fn gi_cascade_fine() -> GICascade {
    // Its storage base is not fixed: with a bake loaded the fine cascade lives
    // after the baked probes, because both would otherwise start at
    // GI_PROBE_BASE and the live cascade would write straight over the bake it
    // is supposed to be refining.
    return GICascade(params.gi_min.xyz, params.gi_min.w, params.gi_dim.xyz,
        params.gi_probe2.w, true, false);
}

fn gi_cascade_coarse() -> GICascade {
    return GICascade(params.gi2_min.xyz, params.gi2_min.w, params.gi2_dim.xyz,
        GI_PROBE_BASE + GI_C0_MAX * GI_PROBE_FLOATS, false, false);
}

fn gi_cas_coord(c: GICascade, p: vec3<f32>) -> vec3<f32> {
    return (p - c.mn) * c.inv;
}

fn gi_cas_pos(c: GICascade, cell: vec3<u32>) -> vec3<f32> {
    return c.mn + (vec3<f32>(cell) + vec3<f32>(0.5)) / c.inv;
}

// Local cell to storage slot, toroidal for the cascade that moves.
fn gi_cas_slot(c: GICascade, cell: vec3<u32>) -> u32 {
    if (c.sparse) {
        return gi_bake_slot(cell);
    }
    var s = cell;
    if (c.toroidal) {
        let dimf = vec3<f32>(c.dim);
        let b = floor(c.mn * c.inv);
        s = (cell + vec3<u32>(b - floor(b / dimf) * dimf)) % c.dim;
    }
    return (s.z * c.dim.y + s.y) * c.dim.x + s.x;
}

// Storage slot to local cell: the inverse, for the update pass.
fn gi_cas_local(c: GICascade, slot: u32) -> vec3<u32> {
    let s = vec3<u32>(slot % c.dim.x, (slot / c.dim.x) % c.dim.y, slot / (c.dim.x * c.dim.y));
    if (!c.toroidal) {
        return s;
    }
    let dimf = vec3<f32>(c.dim);
    let b = floor(c.mn * c.inv);
    return (s + c.dim - vec3<u32>(b - floor(b / dimf) * dimf)) % c.dim;
}

// How far off the surface a lookup is nudged, in metres. Absolute rather than
// a fraction of probe spacing, so a coarse cascade does not get a bias wider
// than the rooms it is sampling.
// How far a lookup is nudged off the surface before anything is sampled, as a
// fraction of the cascade's cell size.
const GI_NORMAL_BIAS_CELLS: f32 = 0.25;
const GI_VIEW_BIAS_CELLS: f32 = 0.1;

// Floor under the Chebyshev variance: an absolute one in metres, and one
// proportional to the recorded distance for the angular spread a texel cannot
// resolve.
// The depth map's own blend rate, far slower than the irradiance map's.
//
// Depth is geometry, not lighting: for a probe that is not moving it is the
// same every frame, so it can be averaged over hundreds of them. That is what
// makes a 16x16 map affordable at 32 rays — a texel is reached about once
// every eight frames, and a rate this low turns that trickle into a smooth
// estimate instead of the noise that showed up as flower-shaped patches.
const GI_DEPTH_HYSTERESIS: f32 = 0.006;

// Furthest a probe ray's distance is recorded as, in cells. Comfortably past
// the one-cell reach of the trilinear read, so a point in the open is never
// mistaken for an occluded one.
const GI_DEPTH_MAX_CELLS: f32 = 4.0;

const GI_DEPTH_MIN_SIGMA: f32 = 0.02;
// Small. A large relative floor is indistinguishable from having no occlusion
// test: at 0.2 a wall 25% nearer than the shaded point still passed half its
// light through.
const GI_DEPTH_REL_SIGMA: f32 = 0.02;
// How far past the recorded depth a point may sit before it is treated as
// occluded, as a fraction of that depth.
const GI_DEPTH_SLACK: f32 = 0.15;

const GI_NORMAL_BIAS: f32 = GI_NORMAL_BIAS_CELLS;
const GI_VIEW_BIAS: f32 = GI_VIEW_BIAS_CELLS;

// DDGI's crush threshold: weights below it are cubed down rather than kept,
// so a probe that only just passes the tests does not contribute a faint wash.
const GI_CRUSH: f32 = 0.2;

// Below this a probe's contribution is not worth the memory traffic to fetch.
// Small enough to be invisible — the weights sum to one, so this is a
// thousandth of the answer at most — and it removes most of the eight probe
// reads for points that sit near one.
const GI_WEIGHT_EPS: f32 = 1e-3;

// Irradiance at p facing n from one cascade, weighted as DDGI weights it.
// w is zero when p is outside the cascade.
fn gi_cascade_sample(c: GICascade, p_in: vec3<f32>, n: vec3<f32>, v: vec3<f32>) -> vec4<f32> {
    // Push the query point off the surface before looking anything up.
    //
    // Without this a surface samples probes along its own plane, where the
    // depth map says "zero distance" and the Chebyshev test rejects
    // everything. Mostly along the normal, partly toward the viewer.
    //
    // Absolute metres, not a fraction of the probe spacing. Scaled by spacing
    // it is 1.5 m on the fine cascade, which does not nudge the query point
    // off the wall so much as move it into the middle of the room — far
    // enough to cross a thin wall and sample the visibility of somewhere
    // else entirely. That alone emptied the field.
    // Scaled by the cascade's cell size, not absolute.
    //
    // The bias exists so a surface is not tested against the very wall it is
    // part of: on a wall, dist and the recorded depth are the same number, so
    // the test sits permanently on its own boundary and any texel-to-texel
    // variation in the depth map flips it on and off. That is what draws the
    // rosettes — the depth map's 16x16 grid printed on the wall.
    //
    // How far is "far enough" is set by how far apart the probes are, because
    // that is what sets both the angle subtended by a texel and the distance
    // to the probe being tested. An absolute 0.15 m is a tenth of the margin
    // on a 2 m grid and most of it on a 0.3 m one.
    let cell = 1.0 / c.inv;
    let p = p_in + (n * GI_NORMAL_BIAS + v * GI_VIEW_BIAS) * cell;

    let dim = vec3<i32>(c.dim);
    let f = gi_cas_coord(c, p) - vec3<f32>(0.5);
    let i0 = vec3<i32>(floor(f));
    let t = clamp(f - floor(f), vec3<f32>(0.0), vec3<f32>(1.0));
    var acc = vec3<f32>(0.0);
    var wsum = 0.0;
    for (var k = 0u; k < 8u; k = k + 1u) {
        let o = vec3<i32>(i32(k & 1u), i32((k >> 1u) & 1u), i32((k >> 2u) & 1u));
        let ci = i0 + o;
        if (any(ci < vec3<i32>(0)) || any(ci >= dim)) {
            continue;
        }
        let wv = mix(vec3<f32>(1.0) - t, t, vec3<f32>(o));
        let tri = wv.x * wv.y * wv.z;
        // Drop corners that cannot matter before touching memory.
        //
        // The eight trilinear weights sum to one, so a point anywhere near a
        // probe leaves most of them close to zero — and each one costs twelve
        // buffer loads for the irradiance map and eight for the depth map,
        // scattered across a region far too large to stay in cache. This is
        // the read's whole cost, and the read is all a baked volume costs at
        // runtime.
        if (tri < GI_WEIGHT_EPS) {
            continue;
        }
        let cu = vec3<u32>(ci);
        let slot = gi_cas_slot(c, cu);
        if (slot == GI_PROBE_EMPTY) {
            continue;
        }
        let base = c.base + slot * GI_PROBE_FLOATS;
        let ppos = gi_cas_pos(c, cu);
        let to_probe = ppos - p;
        let dist = length(to_probe);
        let dir = select(to_probe / max(dist, 1e-6), n, dist < 1e-6);

        // Wrap shading rather than a clamped cosine: squared and lifted, so a
        // probe slightly behind the surface fades out instead of vanishing,
        // which keeps the interpolation smooth as the viewer moves.
        var w = pow(max(dot(dir, n), 0.0) * 0.5 + 0.5, 2.0) + 0.2;

        // Chebyshev's inequality as a soft shadow test, against the depth the
        // probe actually recorded in this direction. This is the piece that
        // stops a probe on the far side of a wall from lighting this surface,
        // and the reason it needs a real octahedral map: six cosine lobes
        // blur the occluder away and the test passes when it should not.
        var m = gi_sample_depth(base, -dir);
        // Slack on the recorded depth, for the surface being shaded when it is
        // itself the thing the probe recorded.
        //
        // A point on a wall sits at almost exactly the distance the probe
        // stored for that wall, and "almost" is the problem: the path from the
        // probe is longer at an angle than head on, the texel is 45 degrees
        // wide, and the bias has already moved the point. So dist creeps past
        // mean on a surface that is in plain view, and on a flat wall — where
        // every ray really does return the same distance, so the variance
        // really is zero — Chebyshev hard-rejects and the cube makes it black.
        //
        // That is what drew the diamond patches on the server room walls: not
        // undersampling, but the self-occlusion boundary. A few percent of
        // slack absorbs it and costs nothing real, because an occluder worth
        // rejecting is a whole wall thickness further on, not 8% of a metre.
        m.x = m.x * (1.0 + GI_DEPTH_SLACK);
        // A texel no ray has reached yet reads as mean zero, which the test
        // below would treat as an occluder pressed against the probe and
        // reject everything. Until a probe has recorded something in this
        // direction it gets the benefit of the doubt; the alternative is a
        // field that stays black wherever the ray set has not yet swept.
        if (m.x > 0.0 && dist > m.x) {
            let sigma = max(GI_DEPTH_MIN_SIGMA, m.x * GI_DEPTH_REL_SIGMA);
            let variance = max(m.y - m.x * m.x, sigma * sigma);
            let d = dist - m.x;
            let cheb = variance / (variance + d * d);
            w = w * clamp(cheb * cheb * cheb, 0.0, 1.0);
        }
        w = max(w, 1e-6);
        // Crush *before* the trilinear weight, not after.
        //
        // Folding trilinear in first puts every corner probe under the
        // threshold on its own — a corner contributes about an eighth — so
        // the cube crushed all eight into the 1e-8 range, the sum fell under
        // the guard below, and the whole cascade silently gave way to the
        // coarse one. That read as a dim grey wash: the coarse cascade's
        // probes are mostly outdoors, so what came back was sky.
        if (w < GI_CRUSH) {
            w = w * w * w / (GI_CRUSH * GI_CRUSH);
        }
        w = w * tri;
        if (w < GI_WEIGHT_EPS) {
            // Crushed or occluded to nothing: the irradiance fetch behind
            // this would contribute less than the sum's own rounding.
            continue;
        }
        acc = acc + gi_sample_irr(base, n) * w;
        wsum = wsum + w;
    }
    if (wsum <= 0.0) {
        return vec4<f32>(0.0);
    }
    return vec4<f32>(acc / wsum, 1.0);
}

// How much the fine cascade applies at p: one inside, ramping to zero over its
// outermost cells. The coarse cascade takes over across that band.
fn gi_fine_coverage(c: GICascade, p: vec3<f32>) -> f32 {
    let f = gi_cas_coord(c, p);
    let lim = vec3<f32>(c.dim) - vec3<f32>(1.0);
    let inside = min(f, lim - f);
    let t = clamp(inside / GI_EDGE_CELLS, vec3<f32>(0.0), vec3<f32>(1.0));
    let w = vec3<f32>(
        smoothstepf(0.0, 1.0, t.x),
        smoothstepf(0.0, 1.0, t.y),
        smoothstepf(0.0, 1.0, t.z));
    return w.x * w.y * w.z;
}

fn gi_probe_irradiance(p: vec3<f32>, n: vec3<f32>) -> vec3<f32> {
    // Toward the viewer, for the bias offset. Probe rays have no viewer, so
    // they fall back to the normal.
    let v = select(normalize(params.cam_pos.xyz - p), n, gi_in_probe);
    if (gi_bake_on()) {
        let baked = gi_cascade_sample(gi_bake_cascade(), p, n, v).xyz;
        if (params.gi_probe2.z == 0u) {
            // One volume, no cascades, no coverage ramp: it spans the scene.
            return baked;
        }
        // A live near field over the baked far one.
        //
        // This is the composition the cascades were reaching for and could not
        // reach: the fine cascade resolves the room the viewer is in and
        // follows the light, and what it hands over to is not a coarse live
        // grid — ten metres between probes cannot resolve a room, and filling
        // one indoors leaks more than it adds — but a volume solved offline at
        // the same spacing everywhere.
        let fc = gi_cascade_fine();
        let cov = gi_fine_coverage(fc, p);
        if (cov <= 0.0) {
            return baked;
        }
        return mix(baked, gi_cascade_sample(fc, p, n, v).xyz, cov);
    }
    let coarse = gi_cascade_sample(gi_cascade_coarse(), p, n, v);
    let fc = gi_cascade_fine();
    let cov = gi_fine_coverage(fc, p);
    if (cov <= 0.0) {
        return coarse.xyz;
    }
    let fine = gi_cascade_sample(fc, p, n, v);
    // Blend on coverage alone, never on whether the fine cascade answered.
    //
    // Weighting by fine.w looks like prudence and is precisely backwards: the
    // one case where every fine probe is rejected is a point that is properly
    // occluded, and falling back to the coarse cascade there hands it the
    // answer from probes ten metres apart that cannot resolve a room, let
    // alone a wall. The better the fine cascade got at rejecting, the more
    // light this let through — occlusion working correctly turned into a leak.
    //
    // With coverage alone, a fully rejected point returns zero from the fine
    // cascade and that zero is what it gets.
    return mix(coarse.xyz, fine.xyz, cov);
}



// ---------------------------------------------------------------------------
// Probe update (mode 3)
// ---------------------------------------------------------------------------

// Spherical Fibonacci: GI_PROBE_RAYS directions spread evenly over the sphere,
// with no clustering at the poles that a latitude/longitude split would give.
//
// Rotated every frame. A fixed set would sample the same 32 directions
// forever, so anything falling between them is invisible to the probe no
// matter how long it runs; rotating turns the fixed set into a stratified
// estimator whose error the hysteresis blend averages away.
// A uniform random rotation for the frame, by Shoemake's method.
//
// The first version offset the Fibonacci spiral's azimuth by the frame seed,
// which is a rotation about Z and nothing else. The six-face projection then
// saw a set that swept steadily around one axis: the +-Z faces resampled
// almost the same directions every frame while +-X and +-Y swung, so the error
// was periodic instead of random and the hysteresis had nothing to average
// away. On a wall that reads as soft ovals fading in and out on a cycle, which
// is exactly what it looked like.
// The key a fine-cascade slot currently holds, and the key a cell should have.
//
// Packed the same way the baked volume packs cells, and 0xffffffff means "no
// probe has been traced into this slot yet".
fn gi_fine_key_of(cell: vec3<u32>) -> u32 {
    return (cell.x & 0x3ffu) | ((cell.y & 0x3ffu) << 10u) | ((cell.z & 0x3ffu) << 20u);
}

fn gi_fine_key_load(slot: u32) -> u32 {
    return bitcast<u32>(ao_volume[params.gi_probe3.x + slot]);
}

fn gi_fine_key_store(slot: u32, key: u32) {
    ao_volume[params.gi_probe3.x + slot] = bitcast<f32>(key);
}

// How much of this update a probe should take.
//
// Normally the blend rate. But a slot whose world cell has changed is holding
// the last cell's light, and blending up from that is the pop you see walking:
// measured, a 40 m round trip left the frame 77% too bright at a blend of
// 0.05, and worse at lower rates because the wrong value decays more slowly.
// Such a slot takes the new estimate whole.
//
// The signal is the cell, not the sample. An earlier attempt keyed this on how
// much the value had moved, which cannot work: at 32 rays the sampling noise
// is larger than most real changes, so it fired constantly and the field
// shimmered four times worse.
fn gi_fine_rate(slot: u32, cell: vec3<u32>, rate: f32) -> f32 {
    let key = gi_fine_key_of(cell);
    if (gi_fine_key_load(slot) == key) {
        return rate;
    }
    gi_fine_key_store(slot, key);
    return 1.0;
}

fn gi_probe_rot(seed: u32) -> mat3x3<f32> {
    let h1 = gi_probe_next(seed * 747796405u + 2891336453u);
    let h2 = gi_probe_next(h1);
    let h3 = gi_probe_next(h2);
    let u1 = f32(h1) * 2.3283064e-10;
    let u2 = f32(h2) * 2.3283064e-10;
    let u3 = f32(h3) * 2.3283064e-10;
    let s1 = sqrt(1.0 - u1);
    let s2 = sqrt(u1);
    let q = vec4<f32>(s1 * sin(6.28318531 * u2), s1 * cos(6.28318531 * u2),
                      s2 * sin(6.28318531 * u3), s2 * cos(6.28318531 * u3));
    let x = q.x; let y = q.y; let z = q.z; let w = q.w;
    return mat3x3<f32>(
        vec3<f32>(1.0 - 2.0 * (y * y + z * z), 2.0 * (x * y + z * w), 2.0 * (x * z - y * w)),
        vec3<f32>(2.0 * (x * y - z * w), 1.0 - 2.0 * (x * x + z * z), 2.0 * (y * z + x * w)),
        vec3<f32>(2.0 * (x * z + y * w), 2.0 * (y * z - x * w), 1.0 - 2.0 * (x * x + y * y)));
}

// The fixed spherical Fibonacci set, before rotation: GI_PROBE_RAYS directions
// spread evenly over the sphere with no clustering at the poles.
fn gi_probe_dir_fixed(i: u32) -> vec3<f32> {
    let n = f32(GI_PROBE_RAYS);
    let fi = f32(i) + 0.5;
    let phi = fi * 2.39996323;
    let z = 1.0 - 2.0 * fi / n;
    let r = sqrt(max(1.0 - z * z, 0.0));
    return vec3<f32>(r * cos(phi), r * sin(phi), z);
}
