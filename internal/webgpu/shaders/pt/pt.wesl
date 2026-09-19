// pt.wesl — experimental Monte Carlo path tracer.
//
// A second, independent compute kernel that reuses the megakernel's *geometry*
// (BVH traversal, analytic primitive intersection, procedural textures, sky)
// and replaces its whole transport model with unidirectional path tracing.
// Nothing in modules/ is modified; this file only imports from it.
//
// What is deliberately different from trace.wesl:
//   - No hemispheric ambient constant. Indirect light comes from actual paths.
//   - No baked AO volume. Contact darkening falls out of occluded bounce rays.
//   - No screen-space penumbra filter. Lights are sampled as spheres, so
//     penumbrae are real (and noisy).
//   - No adaptive AA. The camera ray is jittered inside the pixel per sample.
//   - No ray-segment stack. One path, one lobe per vertex, chosen by roulette.
//   - Emissive surfaces are real light sources: a path that lands on one picks
//     up its radiance. In trace.wesl they illuminate nothing.
//
// Light transport bookkeeping: next-event estimation samples the scene's
// `lights[]` only. Emissive geometry and the sky are found by BSDF sampling
// only. The two populations are disjoint, so no MIS is needed and nothing is
// double counted.

import package::types::{CampfireParams, ao_volume, campfires, Hit, HIT_NO_INSTANCE, idx_tables, Light, lights, MAT_CHECKER, MAT_DIFFUSE, MAT_EMIT, MAT_GLASS, MAT_METAL, MAT_MIRROR, params, pixels, Prim, PRIM_BOX, PRIM_CONE, PRIM_FLAG_TRANSFORMED, PRIM_SPHERE, prims, T_MISS, waters};
import package::math::{surface_grazing_weight, smoothstepf, tonemap};
import package::bvh::{nearest_hit};
import package::shade::{campfire_cull, scene_ambient, campfire_light_count, campfire_light_radius, campfire_sublight, cone_cap_normal_world, light_atten, light_span, normal_at, shadowed, spot_dir, terrain_albedo, wide_span};
import package::flame::{accumulate_flame};
import package::texture::{plane_albedo, prim_texture_bump_normal, texture_eval, texture_eval_box, texture_eval_surface};
import package::intersect::{xf_to_local_dir, xf_to_local_point, xf_to_world_normal};
import package::instance::{inst_local_point, inst_world_normal};
import package::terrain::{terrain_normal, water_normal};
import package::terrain_zone::{terrain_zone_shade_normal, terrain_zone_surface};
import package::sky::{sky};

// PTParams is the path tracer's own uniform, in its own binding slot so the
// megakernel's Params layout is untouched.
struct PTParams {
    spp: u32,            // samples to add in this dispatch
    max_depth: u32,      // hard path length cap
    frame: u32,          // dispatch index; decorrelates successive samples
    accum: u32,          // total samples accumulated, used by pt_resolve
    flags: u32,          // see PT_FLAG_*
    light_scale: f32,    // scales NEE; pi reproduces trace.wesl's direct term
    clamp_indirect: f32, // firefly clamp on contributions past depth 0
    rr_depth: u32,       // path length at which Russian roulette starts
    light_samples: u32,  // NEE samples per shading vertex
    parity: u32,         // which half of the double-buffered G-buffer is current
    denoise: u32,        // see PT_DN_*
    atrous_step: u32,    // hole size of this a-trous iteration (1, 2, 4, ...)
    atrous_in: u32,      // ping-pong slot to read
    phi_normal: f32,     // edge-stop exponent on normal agreement
    phi_depth: f32,      // edge-stop scale on depth disagreement
    phi_lum: f32,        // edge-stop scale on luminance, in standard deviations
    temporal_alpha: f32, // floor on the blend weight for the newest frame
    max_history: f32,    // frames the running mean is allowed to reach
    atrous_fade: f32,    // history length at which spatial filtering stops
    emitter_count: u32,
    prev_cam_pos: vec4<f32>,
    prev_fwd: vec4<f32>,
    prev_right: vec4<f32>,
    prev_up: vec4<f32>,
    ris_candidates: u32, // resampling candidates per shading vertex
    probe_count: u32,    // unused; kept so the params layout is unchanged
    probe_samples: u32,  // cosine-weighted rays per probe face
    _tail2: u32,
};

// Reconstruction stages, enabled independently so each can be measured alone.
const PT_DN_TEMPORAL: u32 = 1u;
const PT_DN_ATROUS: u32 = 2u;
const PT_DN_ACTIVE: u32 = 3u;

@group(0) @binding(30)
var<uniform> ptp: PTParams;

// One scratch arena for every per-pixel buffer this kernel owns. Metal allows
// 31 buffer bindings per stage and naga spends one on the runtime-array sizes
// table, so six separate bindings put the pipeline one over the limit — it
// fails at pipeline-layout creation with "Not enough memory left", which is
// not what it sounds like. The megakernel solves the same problem the same way
// with idx_tables and params.table_base.
//
// Regions, each n_pix entries of vec4<f32>:
//
//   0       progressive accumulator (raw radiance sum)
//   1..4    G-buffer, double buffered: per parity, (normal, depth) then
//           (albedo, specular flag)
//   5..6    temporal history: (illumination, history length), (moments)
//   7..8    a-trous ping-pong: (illumination, variance)
@group(0) @binding(31)
var<storage, read_write> pt_scratch: array<vec4<f32>>;

const PT_REGION_GBUF: u32 = 1u;
const PT_REGION_HIST: u32 = 5u;
const PT_REGION_PING: u32 = 7u;
// Two more regions, double buffered by parity: the ReSTIR reservoir for the
// primary hit, as (light reference, weight sum, candidate count, target).
const PT_REGION_RESV: u32 = 9u;
const PT_REGION_COUNT: u32 = 11u;

// Emissive geometry that next-event estimation can aim at, packed as one u32
// arena: `emitter_count` records of four words (primitive index, kind, two
// spare), then a primitive-index -> slot-plus-one map. Zero in the map means
// "not NEE-sampleable", which is also the answer for every instanced
// primitive. MIS consults it to decide whether a BSDF ray that landed on an
// emitter shares credit with a light sample or takes emission at full weight.
//
// Geometry and radiance are read from prims[] rather than duplicated here, so
// the two renderers cannot disagree about an emitter.
@group(0) @binding(32)
var<storage, read> pt_emit_table: array<u32>;

fn pt_emit_prim(slot: u32) -> u32 {
    return pt_emit_table[slot * 4u];
}

fn pt_emit_kind(slot: u32) -> u32 {
    return pt_emit_table[slot * 4u + 1u];
}

fn pt_emit_slot_of(prim_idx: u32) -> u32 {
    return pt_emit_table[ptp.emitter_count * 4u + prim_idx];
}

// PT_GB_SPECULAR marks a pixel whose primary hit is not diffuse. Albedo
// demodulation is meaningless there, so those pixels carry an albedo of one
// and are filtered on geometry alone.
const PT_GB_SPECULAR: f32 = 1.0;

// Albedo is divided out before filtering and multiplied back after, so the
// filter smooths *light* and never texture. The divisor is clamped and the
// clamped value is what gets stored, which keeps the round trip exact even on
// a near-black surface.
const PT_ALBEDO_FLOOR: f32 = 0.01;

const PT_FLAG_PHYSICAL: u32 = 1u;   // inverse-square falloff instead of light_atten
// PT_FLAG_AMBIENT restores the scene's hemispheric ambient constant at the
// first diffuse vertex. It is not physical and it double counts with the
// indirect light this tracer computes for real — it exists so a scene authored
// against that constant can be viewed at the brightness its author saw, and so
// the difference between "the transport changed" and "the base level went
// away" is visible separately. Applied at depth 0 only; applying it at every
// vertex, as trace.wesl does, compounds it through the whole path.
const PT_FLAG_AMBIENT: u32 = 2u;
const PT_FLAG_NO_NEE: u32 = 4u;     // brute force: no next-event estimation
const PT_FLAG_NO_SKY: u32 = 8u;     // treat the sky as black
// Diagnostic views. Each returns a first-hit quantity instead of tracing a
// path, which is how you tell "the transport is wrong" from "the geometry or
// the material lookup is wrong" without guessing.
const PT_FLAG_DBG_ALBEDO: u32 = 16u;
const PT_FLAG_DBG_NORMAL: u32 = 32u;
const PT_FLAG_DBG_DIRECT: u32 = 64u;
// Indirect only: everything the megakernel's flat ambient constant stands in
// for. Exactly the quantity an irradiance probe volume would have to store.
const PT_FLAG_DBG_INDIRECT: u32 = 4096u;
// Ablations, mirroring gpuprof's feature matrix.
const PT_FLAG_NO_SHADOW: u32 = 128u;   // skip NEE occlusion tests
const PT_FLAG_ALL_LIGHTS: u32 = 256u;  // ignore the cluster grid, draw from every light
const PT_FLAG_NO_EMITTER_NEE: u32 = 1024u; // emissive geometry by BSDF sampling only
const PT_FLAG_RESTIR_TEMPORAL: u32 = 2048u; // reuse the previous frame's reservoir
const PT_FLAG_WHITE_NOISE: u32 = 512u; // fall back to the old PCG sampler, to A/B against

const PT_PI: f32 = 3.14159265359;
const PT_INV_PI: f32 = 0.31830988618;
const PT_EPS: f32 = 1e-3;
// Radius substituted for a light that declares none. trace.wesl's penumbra
// filter uses an *angular* fallback for the same purpose; here the light is
// really sampled, so a fixed small radius keeps the noise affordable.
const PT_DEFAULT_LIGHT_RADIUS: f32 = 0.05;

// ---------------------------------------------------------------- rng

// Owen-scrambled Sobol, following Burley 2020 ("Practical Hash-based Owen
// Scrambling"). This replaces a white-noise PCG hash and costs about the same.
//
// Two things it buys, and the second is the one that matters here:
//
//   - Successive samples at a pixel are stratified rather than independent, so
//     an accumulating image converges faster than 1/sqrt(N) on the smooth part
//     of the integrand.
//   - The error left over is *structured* instead of white. At the sample
//     counts this renderer actually runs at, the grain is visible either way;
//     low-discrepancy grain is finer and more even, where white-noise grain
//     clumps into blotches.
//
// Per the occupancy rule in docs/bvh-traversal.md, there are no tables and no
// arrays anywhere in here: three private scalars and arithmetic.

var<private> pt_dim: u32;      // dimension consumed so far by this path
var<private> pt_index: u32;    // this pixel's shuffled position in the sequence
var<private> pt_pixhash: u32;  // per-pixel scramble seed
var<private> pt_white: u32;    // fallback white-noise state

fn pt_hash_u32(x_in: u32) -> u32 {
    var x = x_in;
    x = (x ^ (x >> 16u)) * 2246822519u;
    x = (x ^ (x >> 13u)) * 3266489917u;
    return x ^ (x >> 16u);
}

// Laine-Karras permutation: a bit-hash whose influence flows only from low
// bits to high, which is what makes the reverse-hash-reverse sandwich below a
// valid nested uniform (Owen) scramble.
fn pt_lk_hash(x_in: u32, seed: u32) -> u32 {
    var x = x_in;
    x = x + seed;
    x = x ^ (x * 0x6c50b47cu);
    x = x ^ (x * 0xb82f1e52u);
    x = x ^ (x * 0xc7afe638u);
    x = x ^ (x * 0x8d22f6e6u);
    return x;
}

fn pt_owen(x: u32, seed: u32) -> u32 {
    return reverseBits(pt_lk_hash(reverseBits(x), seed));
}

// Sobol dimension 0 is the van der Corput sequence.
fn pt_sobol0(i: u32) -> u32 {
    return reverseBits(i);
}

fn pt_seed(x: u32, y: u32, s: u32) {
    let pix = pt_hash_u32(x * 1973u + y * 9277u + 26699u);
    pt_pixhash = pix;
    // A global sample index, so accumulation keeps extending one sequence per
    // pixel instead of restarting it every dispatch. ptp.accum is the sample
    // count before this dispatch, which makes this exact even when the batch
    // size changes between dispatches.
    let gi = ptp.accum + s;
    // The index is used as-is. Shuffling it per pixel spreads it across the
    // whole 32-bit range, and the first N points of a sequence at *arbitrary*
    // indices are not a stratified set. Pixels are decorrelated by the
    // per-pixel scramble and rotation instead, which leave stratification
    // alone.
    pt_index = gi;
    pt_dim = 0u;
    pt_white = pt_hash_u32(pix ^ (gi * 60493u));
}

// Kronecker (rank-1 lattice) sequence for the dimensions past the first.
// x_i = frac(alpha * i) with an irrational alpha is low-discrepancy, costs one
// multiply and a fract, and — the property that matters — is structurally
// unrelated to a base-2 radical inverse, so it does not correlate with
// dimension 0.
//
// The alphas are 1/phi, 1/phi^2 and 1/phi^3 for the d=3 generalised golden
// ratio (the real root of x^4 = x + 1), which is Roberts' R_d construction.
// The base-3 and base-5 radical inverses these replace measured the same RMSE
// and cost 10% more, all of it in integer division.
const PT_KRONECKER_1: f32 = 0.8191725133961645;
const PT_KRONECKER_2: f32 = 0.6710436067037893;
const PT_KRONECKER_3: f32 = 0.5497004779019703;

// Cranley-Patterson rotation: shifts a sequence by a per-pixel offset, modulo
// one. Decorrelates pixels without disturbing stratification.
fn pt_rotate(x: f32, seed: u32) -> f32 {
    return fract(x + f32(pt_hash_u32(seed)) * 2.3283064365386963e-10);
}

fn pt_white_next() -> f32 {
    pt_white = pt_white * 747796405u + 2891336453u;
    let w = ((pt_white >> ((pt_white >> 28u) + 4u)) ^ pt_white) * 277803737u;
    return f32((w >> 22u) ^ w) * 2.3283064365386963e-10;
}

// PT_LDS_DIMS is how far the low-discrepancy sequence extends before the
// sampler falls back to white noise. Deeper path vertices contribute least,
// and white noise there is unbiased — which is the property that actually
// matters. A correlated "better" sequence is worse than an uncorrelated plain
// one: the first attempt here re-scrambled one base-2 sequence for every
// dimension, beat white noise at 16 spp, and then stalled at 6.1 levels while
// white noise carried on to 3.46.
const PT_LDS_DIMS: u32 = 4u;

fn pt_rand() -> f32 {
    if ((ptp.flags & PT_FLAG_WHITE_NOISE) != 0u) {
        return pt_white_next();
    }
    let d = pt_dim;
    if (d >= PT_LDS_DIMS) {
        return pt_white_next();
    }
    pt_dim = d + 1u;
    if (d == 0u) {
        return f32(pt_owen(pt_sobol0(pt_index), pt_pixhash ^ 0x85ebca6bu)) * 2.3283064365386963e-10;
    }
    var alpha = PT_KRONECKER_1;
    if (d == 2u) {
        alpha = PT_KRONECKER_2;
    } else if (d == 3u) {
        alpha = PT_KRONECKER_3;
    }
    return pt_rotate(fract(alpha * f32(pt_index)), pt_pixhash ^ (d * 0x9e3779b9u));
}

// ------------------------------------------------------------ sampling

fn pt_uniform_sphere() -> vec3<f32> {
    let z = 1.0 - 2.0 * pt_rand();
    let r = sqrt(max(0.0, 1.0 - z * z));
    let phi = 2.0 * PT_PI * pt_rand();
    return vec3<f32>(r * cos(phi), r * sin(phi), z);
}

// Branchless orthonormal basis (Duff et al.), so no divergent frame build.
fn pt_tangent(n: vec3<f32>) -> vec3<f32> {
    let s = select(-1.0, 1.0, n.z >= 0.0);
    let a = -1.0 / (s + n.z);
    let b = n.x * n.y * a;
    return vec3<f32>(1.0 + s * n.x * n.x * a, s * b, -s * n.x);
}

fn pt_bitangent(n: vec3<f32>, t: vec3<f32>) -> vec3<f32> {
    return cross(n, t);
}

fn pt_cosine_dir(n: vec3<f32>) -> vec3<f32> {
    let u1 = pt_rand();
    let u2 = pt_rand();
    let r = sqrt(u1);
    let phi = 2.0 * PT_PI * u2;
    let t = pt_tangent(n);
    let b = pt_bitangent(n, t);
    return normalize(t * (r * cos(phi)) + b * (r * sin(phi)) + n * sqrt(max(0.0, 1.0 - u1)));
}

// GGX half-vector sample (NDF sampling). Paired with pt_ggx_weight below.
fn pt_ggx_half(n: vec3<f32>, alpha: f32) -> vec3<f32> {
    let u1 = pt_rand();
    let u2 = pt_rand();
    let a2 = alpha * alpha;
    let cos_theta = sqrt(max(0.0, (1.0 - u1) / (1.0 + (a2 - 1.0) * u1)));
    let sin_theta = sqrt(max(0.0, 1.0 - cos_theta * cos_theta));
    let phi = 2.0 * PT_PI * u2;
    let t = pt_tangent(n);
    let b = pt_bitangent(n, t);
    return normalize(t * (sin_theta * cos(phi)) + b * (sin_theta * sin(phi)) + n * cos_theta);
}

// f * cos / pdf for NDF-sampled GGX with a separable Smith G, Fresnel folded in
// by the caller's tint. Reduces to G * (wo·h) / ((n·wo)(n·h)).
fn pt_ggx_weight(n: vec3<f32>, wo: vec3<f32>, wi: vec3<f32>, h: vec3<f32>, alpha: f32) -> f32 {
    let ndotwo = max(dot(n, wo), 1e-4);
    let ndotwi = max(dot(n, wi), 1e-4);
    let ndoth = max(dot(n, h), 1e-4);
    let wodoth = max(dot(wo, h), 1e-4);
    let k = alpha * 0.5;
    let g1o = ndotwo / (ndotwo * (1.0 - k) + k);
    let g1i = ndotwi / (ndotwi * (1.0 - k) + k);
    return (g1o * g1i * wodoth) / (ndotwo * ndoth);
}

fn pt_schlick(cosine: f32, f0: f32) -> f32 {
    let m = clamp(1.0 - cosine, 0.0, 1.0);
    let m2 = m * m;
    return f0 + (1.0 - f0) * m2 * m2 * m;
}

// ------------------------------------------------------------- surface

// PTSurf is everything the transport loop needs from a hit. Resolving it
// mirrors the corresponding block of ray_color in trace.wesl — same normals,
// same procedural textures, same instance/transform handling — so the two
// renderers disagree about light, not about geometry or material lookup.
struct PTSurf {
    alb: vec3<f32>,
    n: vec3<f32>,
    mat: u32,
    rough: f32,
    ior: f32,
    reflect: f32,
    transmit: f32,
    front: bool, // ray met the outside of the surface
};

fn pt_surface(hit: Hit, ro: vec3<f32>, rd: vec3<f32>, hp: vec3<f32>) -> PTSurf {
    var s: PTSurf;
    s.mat = MAT_DIFFUSE;
    s.alb = vec3<f32>(1.0, 1.0, 1.0);
    s.n = vec3<f32>(0.0, 1.0, 0.0);
    s.rough = 0.0;
    s.ior = 1.5;
    s.reflect = 0.0;
    s.transmit = 0.0;
    s.front = true;

    if (hit.kind == 0u) {
        let p = prims[hit.idx];
        s.mat = p.info.y;
        s.rough = p.surf.x;
        s.ior = select(p.surf.y, 1.5, p.surf.y == 0.0);
        s.reflect = p.surf.z;
        s.transmit = p.surf.w;

        var tpl_hp = hp;
        if (hit.inst_idx != HIT_NO_INSTANCE) {
            tpl_hp = inst_local_point(hit.inst_idx, hp);
        }
        var tex_p = tpl_hp;
        var tex_n = s.n;
        var lrd = rd;
        if ((p.info.w & PRIM_FLAG_TRANSFORMED) != 0u) {
            lrd = xf_to_local_dir(p, rd);
        }
        var lhp = tpl_hp;
        if ((p.info.w & PRIM_FLAG_TRANSFORMED) != 0u) {
            lhp = xf_to_local_point(p, tpl_hp);
        }
        let cone_cap = p.info.x == PRIM_CONE && p.geo_b.y > 0.5 && abs(lhp.y - p.geo_a.w) < 0.02;
        let to_cam = params.cam_pos.xyz - hp;
        var n = s.n;
        if (cone_cap) {
            n = cone_cap_normal_world(p, ro, hp);
            tex_n = normal_at(p, lhp, lrd);
            tex_n = prim_texture_bump_normal(p, lhp, tex_n, surface_grazing_weight(n, to_cam));
            tex_p = lhp;
        } else if ((p.info.w & PRIM_FLAG_TRANSFORMED) != 0u) {
            tex_n = normal_at(p, lhp, lrd);
            var world_geo_n = xf_to_world_normal(p, tex_n);
            if (hit.inst_idx != HIT_NO_INSTANCE) {
                world_geo_n = inst_world_normal(hit.inst_idx, world_geo_n);
            }
            tex_n = prim_texture_bump_normal(p, lhp, tex_n, surface_grazing_weight(world_geo_n, to_cam));
            var ln = xf_to_world_normal(p, tex_n);
            if (hit.inst_idx != HIT_NO_INSTANCE) {
                ln = inst_world_normal(hit.inst_idx, ln);
            }
            n = normalize(ln);
            tex_p = lhp;
        } else {
            tex_n = normal_at(p, tpl_hp, lrd);
            var world_geo_n = tex_n;
            if (hit.inst_idx != HIT_NO_INSTANCE) {
                world_geo_n = inst_world_normal(hit.inst_idx, tex_n);
            }
            tex_n = prim_texture_bump_normal(p, lhp, tex_n, surface_grazing_weight(world_geo_n, to_cam));
            n = tex_n;
            if (hit.inst_idx != HIT_NO_INSTANCE) {
                n = inst_world_normal(hit.inst_idx, n);
            }
        }
        s.front = dot(n, rd) < 0.0;
        if (!s.front) {
            n = -n;
            tex_n = -tex_n;
        }
        s.n = n;
        if (p.info.x == PRIM_BOX) {
            s.alb = texture_eval_box(hit.idx, tex_p, tex_n, p.geo_a.xyz, p.geo_b.xyz, p.info.z, plane_albedo(p, hp));
        } else {
            s.alb = texture_eval_surface(p, tex_p, tex_n, plane_albedo(p, hp));
        }
    } else if (hit.kind == 1u) {
        var n = terrain_normal(hit.idx, hp);
        n = terrain_zone_shade_normal(hit.idx, hp, n);
        s.alb = terrain_albedo(hit.idx, hp, n);
        let zs = terrain_zone_surface(hit.idx, hp.xz);
        s.rough = zs.rough;
        s.reflect = zs.reflect;
        s.front = dot(n, rd) < 0.0;
        if (!s.front) {
            n = -n;
        }
        s.n = n;
    } else {
        let wp = waters[hit.idx];
        s.mat = wp.info.x;
        s.rough = wp.surf.x;
        s.ior = select(wp.surf.y, 1.33, wp.surf.y == 0.0);
        s.reflect = wp.surf.z;
        s.transmit = wp.surf.w;
        var n = water_normal(hit.idx, hp);
        s.alb = texture_eval(wp.info.y, hp, n, wp.albedo.xyz);
        s.front = dot(n, rd) < 0.0;
        if (!s.front) {
            n = -n;
        }
        s.n = n;
    }
    return s;
}

// ------------------------------------------------------------- emitters

// Local space to world. The stored transform is world->local as an orthonormal
// rotation plus a translation, so the inverse rotation is the transpose that
// xf_to_world_normal already applies, and lengths and areas are preserved.
fn pt_xf_point_to_world(p: Prim, lp: vec3<f32>) -> vec3<f32> {
    if ((p.info.w & PRIM_FLAG_TRANSFORMED) == 0u) {
        return lp;
    }
    return xf_to_world_normal(p, lp) + vec3<f32>(p.xf0.w, p.xf1.w, p.xf2.w);
}

fn pt_xf_normal_to_world(p: Prim, ln: vec3<f32>) -> vec3<f32> {
    if ((p.info.w & PRIM_FLAG_TRANSFORMED) == 0u) {
        return ln;
    }
    return normalize(xf_to_world_normal(p, ln));
}

struct PTLightSample {
    wi: vec3<f32>,
    dist: f32,
    radiance: vec3<f32>,
    pdf: f32, // solid angle, for this emitter alone
    ok: bool,
};

// A sphere emitter is sampled over the cone it subtends rather than over its
// area: every sampled direction then hits it, and the pdf is constant across
// the cone, which makes the MIS pdf below a single division.
fn pt_sphere_sample(p: Prim, hp: vec3<f32>) -> PTLightSample {
    var r: PTLightSample;
    r.ok = false;
    r.pdf = 0.0;
    let c = pt_xf_point_to_world(p, p.geo_a.xyz);
    let radius = p.geo_a.w;
    let d = c - hp;
    let dist2 = dot(d, d);
    if (dist2 <= radius * radius * 1.0001) {
        return r; // shading point inside the emitter
    }
    let dist = sqrt(dist2);
    let w = d / dist;
    let cos_max = sqrt(max(0.0, 1.0 - radius * radius / dist2));
    let cos_t = 1.0 - pt_rand() * (1.0 - cos_max);
    let sin_t = sqrt(max(0.0, 1.0 - cos_t * cos_t));
    let phi = 2.0 * PT_PI * pt_rand();
    let tg = pt_tangent(w);
    let bt = pt_bitangent(w, tg);
    let wi = normalize(tg * (sin_t * cos(phi)) + bt * (sin_t * sin(phi)) + w * cos_t);

    let oc = hp - c;
    let bq = dot(oc, wi);
    let cq = dot(oc, oc) - radius * radius;
    let disc = bq * bq - cq;
    if (disc < 0.0) {
        return r;
    }
    let t_hit = -bq - sqrt(disc);
    if (t_hit <= PT_EPS) {
        return r;
    }
    r.wi = wi;
    r.dist = t_hit;
    r.radiance = p.albedo.xyz;
    r.pdf = 1.0 / (2.0 * PT_PI * (1.0 - cos_max));
    r.ok = true;
    return r;
}

fn pt_box_area(p: Prim) -> f32 {
    let e = max(p.geo_b.xyz - p.geo_a.xyz, vec3<f32>(1e-6));
    return 2.0 * (e.x * e.y + e.y * e.z + e.z * e.x);
}

// A box emitter is sampled uniformly over its surface, faces weighted by area.
//
// One caveat worth knowing: an emissive *box* is not excluded from the blocker
// set the way an emissive sphere is (PackBlockers drops MatEmit spheres but
// only MatGlass boxes), so a shadow ray toward one would be stopped by the
// emitter itself. It works because blocker_bvh_any_hit ignores anything within
// 0.05 of max_t, and the panels in these scenes are 0.01 thick. An emissive
// box thicker than 0.05 would self-shadow and go black under NEE.
fn pt_box_sample(p: Prim, hp: vec3<f32>) -> PTLightSample {
    var r: PTLightSample;
    r.ok = false;
    r.pdf = 0.0;
    let mn = p.geo_a.xyz;
    let mx = p.geo_b.xyz;
    let e = max(mx - mn, vec3<f32>(1e-6));
    let a_yz = e.y * e.z;
    let a_zx = e.z * e.x;
    let a_xy = e.x * e.y;
    let total = 2.0 * (a_yz + a_zx + a_xy);

    let u = pt_rand() * total;
    let r1 = pt_rand();
    let r2 = pt_rand();
    var lp: vec3<f32>;
    var ln: vec3<f32>;
    if (u < 2.0 * a_yz) {
        let pos = u < a_yz;
        lp = vec3<f32>(select(mn.x, mx.x, pos), mn.y + r1 * e.y, mn.z + r2 * e.z);
        ln = vec3<f32>(select(-1.0, 1.0, pos), 0.0, 0.0);
    } else if (u < 2.0 * (a_yz + a_zx)) {
        let pos = (u - 2.0 * a_yz) < a_zx;
        lp = vec3<f32>(mn.x + r1 * e.x, select(mn.y, mx.y, pos), mn.z + r2 * e.z);
        ln = vec3<f32>(0.0, select(-1.0, 1.0, pos), 0.0);
    } else {
        let pos = (u - 2.0 * (a_yz + a_zx)) < a_xy;
        lp = vec3<f32>(mn.x + r1 * e.x, mn.y + r2 * e.y, select(mn.z, mx.z, pos));
        ln = vec3<f32>(0.0, 0.0, select(-1.0, 1.0, pos));
    }

    let wp = pt_xf_point_to_world(p, lp);
    let wn = pt_xf_normal_to_world(p, ln);
    let dv = wp - hp;
    let dist2 = dot(dv, dv);
    if (dist2 <= 1e-8) {
        return r;
    }
    let dist = sqrt(dist2);
    let wi = dv / dist;
    let cos_l = dot(wn, -wi);
    if (cos_l <= 1e-4) {
        return r; // sampled a face pointing away
    }
    r.wi = wi;
    r.dist = dist;
    r.radiance = p.albedo.xyz;
    r.pdf = dist2 / (cos_l * total); // area measure -> solid angle
    r.ok = true;
    return r;
}

fn pt_emitter_sample(slot: u32, hp: vec3<f32>) -> PTLightSample {
    let p = prims[pt_emit_prim(slot)];
    if (pt_emit_kind(slot) == PRIM_SPHERE) {
        return pt_sphere_sample(p, hp);
    }
    return pt_box_sample(p, hp);
}

// Target function for resampling: a cheap estimate of an emitter's unshadowed
// contribution. It has to be evaluable for many candidates, so it uses the
// emitter's representative point and never traces anything.
fn pt_emitter_target(slot: u32, hp: vec3<f32>, n: vec3<f32>) -> f32 {
    let p = prims[pt_emit_prim(slot)];
    var c: vec3<f32>;
    var size = 1.0;
    if (pt_emit_kind(slot) == PRIM_SPHERE) {
        c = pt_xf_point_to_world(p, p.geo_a.xyz);
        size = p.geo_a.w * p.geo_a.w;
    } else {
        c = pt_xf_point_to_world(p, 0.5 * (p.geo_a.xyz + p.geo_b.xyz));
        size = pt_box_area(p);
    }
    let d = c - hp;
    let dist2 = max(dot(d, d), 1e-4);
    let ndl = dot(n, d * inverseSqrt(dist2));
    if (ndl <= 0.0) {
        return 0.0;
    }
    let e = p.albedo.xyz;
    let lum = 0.2126 * e.x + 0.7152 * e.y + 0.0722 * e.z;
    return lum * size * ndl / dist2;
}

// Direct lighting from emissive geometry, selected by resampled importance
// sampling (ReSTIR's RIS, without reuse).
//
// This is the answer to a measured problem. Uniform selection over 268
// emitters finds a relevant one so rarely that adding next-event estimation
// to emissive geometry was worth nothing at all: 13.98 RMSE at 16 spp against
// 12.27 for BSDF sampling alone. The fix is not more shadow rays — the
// equal-time table in this document shows those never pay — but a better
// choice of which single ray to trace. RIS draws `m` candidates from the
// uniform proposal, keeps one in proportion to the unshadowed estimate above,
// and spends the one shadow ray on the survivor.
fn pt_direct_emitters(hp: vec3<f32>, n: vec3<f32>, alb: vec3<f32>) -> vec3<f32> {
    let ne = ptp.emitter_count;
    if (ne == 0u || (ptp.flags & PT_FLAG_NO_EMITTER_NEE) != 0u) {
        return vec3<f32>(0.0);
    }
    let m = max(ptp.ris_candidates, 1u);
    var w_sum = 0.0;
    var chosen = 0u;
    var chosen_target = 0.0;
    for (var k = 0u; k < m; k = k + 1u) {
        let slot = min(u32(pt_rand() * f32(ne)), ne - 1u);
        let p_hat = pt_emitter_target(slot, hp, n);
        // The proposal is uniform, so its pdf is 1/ne and the resampling
        // weight is p_hat * ne.
        let w = p_hat * f32(ne);
        w_sum = w_sum + w;
        if (w > 0.0 && pt_rand() * w_sum < w) {
            chosen = slot;
            chosen_target = p_hat;
        }
    }
    if (chosen_target <= 0.0 || w_sum <= 0.0) {
        return vec3<f32>(0.0);
    }
    // Unbiased contribution weight. At m = 1 it collapses to ne, which is
    // exactly the uniform estimator this replaces — so the candidate count is
    // a clean ablation axis.
    let w_ris = w_sum / (f32(m) * chosen_target);

    let ls = pt_emitter_sample(chosen, hp);
    if (!ls.ok || ls.pdf <= 0.0) {
        return vec3<f32>(0.0);
    }
    let ndl = dot(n, ls.wi);
    if (ndl <= 0.0) {
        return vec3<f32>(0.0);
    }
    if (shadowed(hp + n * PT_EPS, ls.wi, ls.dist)) {
        return vec3<f32>(0.0);
    }
    return alb * PT_INV_PI * ndl * ls.radiance * (w_ris / ls.pdf);
}

// ------------------------------------------------------- next-event est.

// One light, sampled as a sphere. Returns the reflected radiance already
// multiplied by the Lambert BRDF, so the caller only applies throughput.
//
// Which lights are candidates comes from the scene's existing light cluster
// grid — the same idx_tables the megakernel's light loop walks. That matters
// far more here than it does there: office-sunset packs 266 lights, most with a
// cull radius of one unit, so picking uniformly from the whole list finds a
// light that actually reaches the surface about 1% of the time. The estimator
// stays unbiased and the image is black with white sparks. Restricting the draw
// to the cell's candidates is the difference between converging and not.
fn pt_grid_bypassed() -> bool {
    return (ptp.flags & PT_FLAG_ALL_LIGHTS) != 0u;
}

fn pt_light_candidates(hp: vec3<f32>) -> u32 {
    if (pt_grid_bypassed()) {
        return params.light_count;
    }
    let wide = wide_span();
    let cell = light_span(hp);
    let n = (wide.y - wide.x) + (cell.y - cell.x);
    // A point outside the grid's bounds has no cell. Falling back to the whole
    // list keeps it lit: every light too far to matter is rejected by its own
    // cull radius anyway, so this costs variance, never correctness.
    if (n == 0u) {
        return params.light_count;
    }
    return n;
}

fn pt_pick_light(hp: vec3<f32>, pick: u32) -> u32 {
    if (pt_grid_bypassed()) {
        return pick;
    }
    let wide = wide_span();
    let cell = light_span(hp);
    let n_wide = wide.y - wide.x;
    if ((n_wide + (cell.y - cell.x)) == 0u) {
        return pick;
    }
    if (pick < n_wide) {
        return idx_tables[wide.x + pick];
    }
    return idx_tables[cell.x + (pick - n_wide)];
}

// Radiance from one sampled light, before the 1/pdf light-count factor.
fn pt_light_contrib(lt: Light, hp: vec3<f32>, n: vec3<f32>, alb: vec3<f32>) -> vec3<f32> {
    var radius = lt.shape.x;
    if (radius <= 0.0) {
        radius = PT_DEFAULT_LIGHT_RADIUS;
    }
    let lp = lt.pos.xyz + radius * pt_uniform_sphere();
    let ld = lp - hp;
    let d2 = dot(ld, ld);
    if (d2 > lt.falloff.x) {
        return vec3<f32>(0.0);
    }
    let dist = sqrt(max(d2, 1e-8));
    let wi = ld / dist;
    let ndl = dot(n, wi);
    if (ndl <= 0.0) {
        return vec3<f32>(0.0);
    }

    var atten: f32;
    if ((ptp.flags & PT_FLAG_PHYSICAL) != 0u) {
        atten = 1.0 / max(d2, 1e-4);
    } else {
        atten = light_atten(d2);
        // The authored range window, kept so a scene's lights stop where their
        // author expected them to.
        if (lt.falloff.y > 0.0) {
            let x = d2 * lt.falloff.y;
            let w = max(0.0, 1.0 - x * x);
            atten = atten * w * w;
        }
    }
    if (lt.pos.w > 0.5) {
        let cos_outer = lt.color.w;
        let axis = spot_dir(lt.falloff.z, lt.falloff.w);
        let cos_theta = dot(-wi, axis);
        if (cos_theta < cos_outer) {
            return vec3<f32>(0.0);
        }
        let edge = min(0.2, 1.0 - cos_outer);
        atten = atten * smoothstepf(cos_outer, cos_outer + edge, cos_theta);
    }
    if (atten <= 0.0) {
        return vec3<f32>(0.0);
    }
    if ((ptp.flags & PT_FLAG_NO_SHADOW) == 0u && shadowed(hp + n * PT_EPS, wi, dist)) {
        return vec3<f32>(0.0);
    }
    return alb * PT_INV_PI * ndl * lt.color.xyz * atten * ptp.light_scale;
}

// Campfires are a second light population with their own storage and their own
// animation: each fire resolves to a few sub-lights whose positions are driven
// by params.time. trace.wesl evaluates every one of them at every hit; here
// they simply join the candidate set, so a fire costs one shadow ray per vertex
// like anything else, and its cast shadows animate for the same reason.
fn pt_campfire_total() -> u32 {
    var n = 0u;
    for (var ci = 0u; ci < params.campfire_count; ci = ci + 1u) {
        n = n + campfire_light_count(campfires[ci]);
    }
    return n;
}

// Resolves the pick'th campfire sub-light into the same Light layout a scene
// light uses, so both populations run through pt_light_contrib unchanged.
fn pt_campfire_light(pick_in: u32) -> Light {
    var pick = pick_in;
    for (var ci = 0u; ci < params.campfire_count; ci = ci + 1u) {
        let cf = campfires[ci];
        let count = campfire_light_count(cf);
        if (pick < count) {
            let sl = campfire_sublight(cf, pick, params.time * cf.param.w);
            let cull = campfire_cull(cf);
            return Light(
                vec4<f32>(sl.pos, 0.0),
                vec4<f32>(sl.col, 0.0),
                vec4<f32>(cull.x, cull.y, 0.0, 0.0),
                vec4<f32>(campfire_light_radius(sl), 0.0, 0.0, 0.0),
            );
        }
        pick = pick - count;
    }
    return Light(vec4<f32>(0.0), vec4<f32>(0.0), vec4<f32>(0.0), vec4<f32>(0.0));
}

// How many times the per-frame candidate budget a reused reservoir may stand
// for. ReSTIR's usual cap; it bounds staleness rather than bias.
const PT_RESTIR_M_CAP: f32 = 20.0;

// Set by pt_main so pt_direct knows which pixel it is shading and whether it
// is at the primary hit. Reservoirs are screen space: only depth zero has one.
var<private> pt_pixel: u32;
var<private> pt_depth: u32;
var<private> pt_resv_written: bool;

fn pt_restir_active() -> bool {
    return (ptp.flags & PT_FLAG_RESTIR_TEMPORAL) != 0u && pt_depth == 0u && !pt_resv_written;
}

// The previous frame's reservoir for this shading point, or zero when there is
// none to reuse. Validated against the previous G-buffer the same way the
// temporal pass validates colour: accepting a reservoir across a disocclusion
// puts another surface's light on this one.
fn pt_reservoir_prev(hp: vec3<f32>) -> vec4<f32> {
    let n_pix = params.width * params.height;
    let pidx = pt_reproject(hp);
    if (pidx < 0) {
        return vec4<f32>(0.0);
    }
    let prev_g = pt_scratch[PT_REGION_GBUF * n_pix + ((1u - ptp.parity) * 2u) * n_pix + u32(pidx)];
    if (prev_g.w <= 0.0) {
        return vec4<f32>(0.0);
    }
    let cur_g = pt_scratch[PT_REGION_GBUF * n_pix + (ptp.parity * 2u) * n_pix + pt_pixel];
    if (dot(prev_g.xyz, cur_g.xyz) < 0.9) {
        return vec4<f32>(0.0);
    }
    let want = length(hp - ptp.prev_cam_pos.xyz);
    if (abs(prev_g.w - want) > 0.05 * max(want, 1.0)) {
        return vec4<f32>(0.0);
    }
    return pt_scratch[(PT_REGION_RESV + (1u - ptp.parity)) * n_pix + u32(pidx)];
}

fn pt_reservoir_store(r: vec4<f32>) {
    if (!pt_restir_active()) {
        return;
    }
    let n_pix = params.width * params.height;
    pt_scratch[(PT_REGION_RESV + ptp.parity) * n_pix + pt_pixel] = r;
    pt_resv_written = true;
}

// Unshadowed contribution estimate for one light, as a scalar. This is the
// resampling target: it must rank candidates well and must not trace anything,
// because it is evaluated for every candidate while only the survivor gets a
// shadow ray. It mirrors pt_light_contrib exactly apart from the visibility
// test and the albedo, which is common to all candidates and so cannot change
// the ranking.
fn pt_light_p_hat(lt: Light, hp: vec3<f32>, n: vec3<f32>) -> f32 {
    let ld = lt.pos.xyz - hp;
    let d2 = dot(ld, ld);
    if (d2 > lt.falloff.x) {
        return 0.0;
    }
    let dist = sqrt(max(d2, 1e-8));
    let wi = ld / dist;
    let ndl = dot(n, wi);
    if (ndl <= 0.0) {
        return 0.0;
    }
    var atten: f32;
    if ((ptp.flags & PT_FLAG_PHYSICAL) != 0u) {
        atten = 1.0 / max(d2, 1e-4);
    } else {
        atten = light_atten(d2);
        if (lt.falloff.y > 0.0) {
            let x = d2 * lt.falloff.y;
            let w = max(0.0, 1.0 - x * x);
            atten = atten * w * w;
        }
    }
    if (lt.pos.w > 0.5) {
        let cos_outer = lt.color.w;
        let cos_theta = dot(-wi, spot_dir(lt.falloff.z, lt.falloff.w));
        if (cos_theta < cos_outer) {
            return 0.0;
        }
        let edge = min(0.2, 1.0 - cos_outer);
        atten = atten * smoothstepf(cos_outer, cos_outer + edge, cos_theta);
    }
    let c = lt.color.xyz;
    return (0.2126 * c.x + 0.7152 * c.y + 0.0722 * c.z) * atten * ndl;
}

// A reservoir has to name its light in a way that survives into the next
// frame, where the candidate list is a different length in a different order.
// Grid lights use their global index; campfire sub-lights, which are built on
// the fly rather than stored, get the high bit plus their sub-index.
const PT_REF_CAMPFIRE: u32 = 0x80000000u;

fn pt_candidate_ref(hp: vec3<f32>, pick: u32, n_grid: u32) -> u32 {
    if (pick < n_grid) {
        return pt_pick_light(hp, pick);
    }
    return PT_REF_CAMPFIRE | (pick - n_grid);
}

fn pt_light_by_ref(lref: u32) -> Light {
    if ((lref & PT_REF_CAMPFIRE) != 0u) {
        return pt_campfire_light(lref & 0x7fffffffu);
    }
    return lights[lref];
}

// Projects a world point through the previous frame's camera. fwd/right/up are
// orthonormal, so this inverts pixel_ray_dir with three dot products. Returns
// -1 when the point was off screen or behind.
fn pt_reproject(wp: vec3<f32>) -> i32 {
    let d = wp - ptp.prev_cam_pos.xyz;
    let pz = dot(d, ptp.prev_fwd.xyz);
    if (pz <= 1e-4) {
        return -1;
    }
    let u = (dot(d, ptp.prev_right.xyz) / pz) / (params.aspect * params.fov_scale);
    let v = (dot(d, ptp.prev_up.xyz) / pz) / params.fov_scale;
    let px = i32(floor((u * 0.5 + 0.5) * f32(params.width)));
    let py = i32(floor((0.5 - v * 0.5) * f32(params.height)));
    if (px < 0 || py < 0 || px >= i32(params.width) || py >= i32(params.height)) {
        return -1;
    }
    return py * i32(params.width) + px;
}

// Direct lighting from the point-light population, selected by resampled
// importance sampling.
//
// This is where the variance actually is. An early ablation multi-sampled this
// population directly: eight shadow rays per vertex took RMSE from 5.98 to
// 3.87 at 64 spp, but cost 2.7x and therefore lost on equal time. That result
// says the selection is bad and the rays are expensive — which is precisely
// the gap RIS fills. It ranks `m` candidates with pt_light_p_hat, which traces
// nothing, and spends the single shadow ray on the survivor.
//
// At m = 1 this reduces exactly to the uniform estimator it replaces, so the
// candidate count is a clean ablation axis.
fn pt_direct(hp: vec3<f32>, n: vec3<f32>, alb: vec3<f32>) -> vec3<f32> {
    if ((ptp.flags & PT_FLAG_NO_NEE) != 0u) {
        return vec3<f32>(0.0);
    }
    if (params.light_count == 0u && params.campfire_count == 0u) {
        return vec3<f32>(0.0);
    }
    let n_grid = pt_light_candidates(hp);
    let n_fire = pt_campfire_total();
    let total = n_grid + n_fire;
    if (total == 0u) {
        return vec3<f32>(0.0);
    }

    let draws = max(ptp.light_samples, 1u);
    let m = max(ptp.ris_candidates, 1u);
    var sum = vec3<f32>(0.0);
    for (var d = 0u; d < draws; d = d + 1u) {
        var w_sum = 0.0;
        var chosen = 0u;
        var chosen_p = 0.0;
        for (var k = 0u; k < m; k = k + 1u) {
            let pick = min(u32(pt_rand() * f32(total)), total - 1u);
            let lref = pt_candidate_ref(hp, pick, n_grid);
            let p_hat = pt_light_p_hat(pt_light_by_ref(lref), hp, n);
            // Uniform proposal over `total` candidates, so its pdf is
            // 1/total and the resampling weight is p_hat * total.
            let w = p_hat * f32(total);
            w_sum = w_sum + w;
            if (w > 0.0 && pt_rand() * w_sum < w) {
                chosen = lref;
                chosen_p = p_hat;
            }
        }
        var m_total = f32(m);

        // Temporal reuse. The previous frame's reservoir enters as one more
        // candidate whose weight is its target here times its contribution
        // weight times how many candidates it already stands for, which is
        // what lets a handful of samples per frame behave like hundreds.
        //
        // Only the primary hit has a reservoir: this is a screen-space
        // technique and there is no pixel to reproject a deeper vertex into.
        if (pt_restir_active()) {
            let prev = pt_reservoir_prev(hp);
            if (prev.w > 0.0) {
                let prev_ref = bitcast<u32>(prev.x);
                let p_hat_prev = pt_light_p_hat(pt_light_by_ref(prev_ref), hp, n);
                // Capping the reused count bounds how stale the reservoir can
                // get; without it a still camera keeps a sample forever and
                // stops responding to anything that moves.
                let m_prev = min(prev.z, f32(m) * PT_RESTIR_M_CAP);
                let w_prev = p_hat_prev * prev.w * m_prev;
                if (w_prev > 0.0) {
                    w_sum = w_sum + w_prev;
                    m_total = m_total + m_prev;
                    if (pt_rand() * w_sum < w_prev) {
                        chosen = prev_ref;
                        chosen_p = p_hat_prev;
                    }
                }
            }
        }

        if (chosen_p <= 0.0 || w_sum <= 0.0) {
            pt_reservoir_store(vec4<f32>(0.0, 0.0, 0.0, 0.0));
            continue;
        }
        let w_ris = w_sum / (m_total * chosen_p);
        pt_reservoir_store(vec4<f32>(bitcast<f32>(chosen), w_ris, m_total, chosen_p));
        sum = sum + pt_light_contrib(pt_light_by_ref(chosen), hp, n, alb) * w_ris;
    }
    return sum / f32(draws);
}

// ------------------------------------------------------------ transport


fn pt_env(rd: vec3<f32>) -> vec3<f32> {
    if ((ptp.flags & PT_FLAG_NO_SKY) != 0u) {
        return vec3<f32>(0.0);
    }
    return sky(rd);
}

// Drops the primary vertex's own direct lighting and emission, leaving what
// reaches the camera only after at least one bounce.
// True while the path has only ever scattered diffusely. A probe volume
// stores diffuse irradiance; light that arrived through a mirror or a pane of
// glass is not something it could ever represent, and the megakernel already
// carries that with its own ray tree.
var<private> pt_path_diffuse: bool;

fn pt_indirect_only() -> bool {
    return (ptp.flags & PT_FLAG_DBG_INDIRECT) != 0u;
}

fn pt_clamped(c: vec3<f32>, depth: u32) -> vec3<f32> {
    if (pt_indirect_only() && (depth == 0u || !pt_path_diffuse)) {
        return vec3<f32>(0.0);
    }
    if (depth == 0u || ptp.clamp_indirect <= 0.0) {
        return c;
    }
    let peak = max(c.x, max(c.y, c.z));
    if (peak <= ptp.clamp_indirect) {
        return c;
    }
    return c * (ptp.clamp_indirect / peak);
}

// How far the first segment of the last pt_trace got. The probe pass needs it
// for the visibility moments and nothing else wants it, so it rides out here
// rather than widening pt_trace's return.
var<private> pt_first_hit_t: f32 = 0.0;

fn pt_trace(origin: vec3<f32>, dir: vec3<f32>) -> vec3<f32> {
    var radiance = vec3<f32>(0.0);
    var beta = vec3<f32>(1.0);
    var ro = origin;
    var rd = dir;
    // Whether the ray now being traced came from a lobe next-event estimation
    // already covered. False for the camera ray and for every specular or
    // glossy continuation, so emission found along those keeps full weight.
    var prev_lobe_was_nee_diffuse = false;
    pt_path_diffuse = true;

    for (var depth = 0u; depth < ptp.max_depth; depth = depth + 1u) {
        pt_depth = depth;
        let hit = nearest_hit(ro, rd);
        if (depth == 0u) {
            pt_first_hit_t = hit.t;
        }
        // Volumetric flame emission along the segment. Not a surface, so it is
        // gathered by marching rather than by hitting anything, exactly as in
        // trace.wesl; a path tracer has nothing better to offer here.
        if (params.campfire_count > 0u) {
            radiance = radiance + pt_clamped(beta * accumulate_flame(ro, rd, hit.t, params.time), depth);
        }
        if (hit.idx == 0xffffffffu) {
            radiance = radiance + pt_clamped(beta * pt_env(rd), depth);
            break;
        }
        let hp = ro + rd * hit.t;
        let s = pt_surface(hit, ro, rd, hp);

        if ((ptp.flags & PT_FLAG_DBG_ALBEDO) != 0u) {
            return s.alb;
        }
        if ((ptp.flags & PT_FLAG_DBG_NORMAL) != 0u) {
            return s.n * 0.5 + vec3<f32>(0.5);
        }
        if ((ptp.flags & PT_FLAG_DBG_DIRECT) != 0u) {
            return pt_direct(hp, s.n, s.alb);
        }

        // Emissive geometry is a real light here. With emitter NEE on, this
        // direction may also have been reachable by a light sample from the
        // previous vertex, so the two strategies split the credit by the
        // balance heuristic. An emitter the light sampler cannot reach —
        // instanced, or a kind pt_emitter_sample does not handle — returns a
        // pdf of zero and keeps full weight, which is what makes it safe to
        // sample only part of the emitter population.
        if (s.mat == MAT_EMIT) {
            // The two strategies are kept disjoint rather than MIS-weighted.
            // Next-event estimation owns the diffuse share of emitter light,
            // so a diffuse continuation landing on an emitter NEE can reach
            // would be estimating the same integral twice and contributes
            // nothing. Everything NEE cannot reach keeps full weight: every
            // specular and glossy lobe, every instanced emitter, and every
            // primitive kind pt_emitter_sample does not handle.
            //
            // Disjoint rather than MIS because resampling below makes the
            // effective selection pdf a random variable the BSDF side cannot
            // reconstruct — the balance heuristic would need the p_hat
            // function summed over all 268 emitters at every hit.
            var lit = true;
            if (prev_lobe_was_nee_diffuse && ptp.emitter_count > 0u
                && hit.kind == 0u && hit.inst_idx == HIT_NO_INSTANCE
                && pt_emit_slot_of(hit.idx) != 0u) {
                lit = false;
            }
            if (lit) {
                radiance = radiance + pt_clamped(beta * s.alb, depth);
            }
            break;
        }

        let wo = -rd;

        if (s.mat == MAT_GLASS) {
            // Fresnel split, resolved stochastically instead of by forking the
            // ray stack. The apportionment matches trace.wesl exactly:
            // whatever is not transmitted is *reflected*, not scattered
            // diffusely — which is what keeps a pond at transmit = 0.5 reading
            // as a mirror rather than as wet paint.
            var eta_i = 1.0;
            var eta_t = s.ior;
            if (!s.front) {
                eta_i = s.ior;
                eta_t = 1.0;
            }
            let eta = eta_i / eta_t;
            let cosi = clamp(dot(s.n, wo), 0.0, 1.0);
            let k = 1.0 - eta * eta * (1.0 - cosi * cosi);
            let f0 = ((eta_i - eta_t) / (eta_i + eta_t)) * ((eta_i - eta_t) / (eta_i + eta_t));
            let fres = pt_schlick(cosi, f0);
            var transmit = s.transmit;
            if (transmit <= 0.0) {
                transmit = 0.9; // trace.wesl's default for glass that omits it
            }
            var reflectance = fres + (1.0 - fres) * (1.0 - transmit);
            if (k < 0.0) {
                reflectance = 1.0; // total internal reflection
            }
            if (pt_rand() < reflectance) {
                var wi = reflect(rd, s.n);
                if (s.rough > 0.0) {
                    let alpha = s.rough * s.rough;
                    let h = pt_ggx_half(s.n, alpha);
                    let wr = reflect(rd, h);
                    if (dot(wr, s.n) > 0.0) {
                        beta = beta * pt_ggx_weight(s.n, wo, wr, h, alpha);
                        wi = wr;
                    }
                }
                ro = hp + s.n * PT_EPS;
                rd = normalize(wi);
            } else {
                let wi = refract(rd, s.n, eta);
                ro = hp - s.n * PT_EPS;
                rd = normalize(wi);
                beta = beta * s.alb;
                prev_lobe_was_nee_diffuse = false;
                pt_path_diffuse = false;
            }
        } else if (s.mat == MAT_MIRROR || s.mat == MAT_METAL) {
            var wi = reflect(rd, s.n);
            var tint = s.alb;
            if (s.mat == MAT_MIRROR) {
                tint = s.alb * 0.96; // matches trace.wesl's mirror loss
            }
            if (s.rough > 0.0) {
                let alpha = s.rough * s.rough;
                let h = pt_ggx_half(s.n, alpha);
                let wr = reflect(rd, h);
                if (dot(wr, s.n) > 0.0) {
                    tint = tint * pt_ggx_weight(s.n, wo, wr, h, alpha);
                    wi = wr;
                }
            }
            beta = beta * tint;
            ro = hp + s.n * PT_EPS;
            rd = normalize(wi);
            prev_lobe_was_nee_diffuse = false;
            pt_path_diffuse = false;
        } else {
            // Diffuse / checker, optionally with an authored glossy lobe.
            let p_spec = clamp(s.reflect, 0.0, 0.9);
            // Direct light is gathered for the diffuse share at every vertex,
            // independent of which lobe carries the path onward. Decoupling the
            // two is what keeps the estimator low-variance on a lit wall.
            let diffuse_weight = 1.0 - p_spec;
            if (diffuse_weight > 0.0) {
                var d = pt_direct(hp, s.n, s.alb) * diffuse_weight;
                // Point lights are not geometry, so BSDF sampling can never
                // find them and they need no MIS. Emitters are geometry and do.
                d = d + pt_direct_emitters(hp, s.n, s.alb) * diffuse_weight;
                if (depth == 0u && (ptp.flags & PT_FLAG_AMBIENT) != 0u) {
                    d = d + s.alb * scene_ambient(s.n) * diffuse_weight;
                }
                radiance = radiance + pt_clamped(beta * d, depth);
            }
            if (pt_rand() < p_spec) {
                let alpha = max(s.rough * s.rough, 1e-3);
                let h = pt_ggx_half(s.n, alpha);
                let wr = reflect(rd, h);
                if (dot(wr, s.n) <= 0.0) {
                    break;
                }
                beta = beta * pt_ggx_weight(s.n, wo, wr, h, alpha);
                ro = hp + s.n * PT_EPS;
                rd = normalize(wr);
                prev_lobe_was_nee_diffuse = false;
                pt_path_diffuse = false;
            } else {
                beta = beta * s.alb;
                ro = hp + s.n * PT_EPS;
                rd = pt_cosine_dir(s.n);
                prev_lobe_was_nee_diffuse = true;
            }
        }
        // Russian roulette. Unbiased, and it replaces trace.wesl's whole family
        // of display-space contribution gates (SEG_MIN_CONTRIB, GLOSSY_MIN_CONTRIB).
        if (depth >= ptp.rr_depth) {
            let p = clamp(max(beta.x, max(beta.y, beta.z)), 0.05, 1.0);
            if (pt_rand() > p) {
                break;
            }
            beta = beta / p;
        }
        if (max(beta.x, max(beta.y, beta.z)) <= 0.0) {
            break;
        }
    }
    return radiance;
}

fn pt_camera_ray(gx: f32, gy: f32) -> vec3<f32> {
    let u = (gx / f32(params.width)) * 2.0 - 1.0;
    let v = 1.0 - (gy / f32(params.height)) * 2.0;
    let dir = params.fwd.xyz
        + params.right.xyz * (u * params.aspect * params.fov_scale)
        + params.up.xyz * (v * params.fov_scale);
    return normalize(dir);
}

fn pt_pix_count() -> u32 {
    return params.width * params.height;
}

fn pt_luma(c: vec3<f32>) -> f32 {
    return 0.2126 * c.x + 0.7152 * c.y + 0.0722 * c.z;
}

@compute @workgroup_size(8, 8, 1)
fn pt_main(@builtin(global_invocation_id) gid: vec3<u32>) {
    if (gid.x >= params.width || gid.y >= params.height) {
        return;
    }
    let n_pix = pt_pix_count();
    let idx = gid.y * params.width + gid.x;

    // The G-buffer is traced from the pixel *centre*, on its own ray, and this
    // matters more than it looks. Taking it from a jittered sample instead
    // makes a silhouette pixel report the foreground on one frame and the
    // background on the next; temporal reprojection then alternately accepts
    // and rejects its history, and the edge flickers. One extra primary
    // intersection per pixel buys a G-buffer that is stable frame to frame,
    // which is what the reprojection test is actually asking about.
    let cdir = pt_camera_ray(f32(gid.x) + 0.5, f32(gid.y) + 0.5);
    let chit = nearest_hit(params.cam_pos.xyz, cdir);
    var g_n = -cdir;
    var g_alb = vec3<f32>(1.0);
    var g_depth = -1.0;
    var g_spec = PT_GB_SPECULAR;
    if (chit.idx != 0xffffffffu) {
        let chp = params.cam_pos.xyz + cdir * chit.t;
        let cs = pt_surface(chit, params.cam_pos.xyz, cdir, chp);
        g_n = cs.n;
        g_depth = chit.t;
        if ((cs.mat == MAT_DIFFUSE || cs.mat == MAT_CHECKER) && cs.reflect < 0.5) {
            g_alb = max(cs.alb, vec3<f32>(PT_ALBEDO_FLOOR));
            g_spec = 0.0;
        }
    }

    var total = vec3<f32>(0.0);
    pt_pixel = idx;
    pt_resv_written = false;
    for (var s = 0u; s < ptp.spp; s = s + 1u) {
        pt_seed(gid.x, gid.y, s);
        // Jittering the camera ray inside the pixel is the entire antialiasing
        // story: no edge classification, no compaction, no tap budget.
        let gx = f32(gid.x) + pt_rand();
        let gy = f32(gid.y) + pt_rand();
        total = total + pt_trace(params.cam_pos.xyz, pt_camera_ray(gx, gy));
    }
    let gb = (ptp.parity * 2u) * n_pix + idx;
    pt_scratch[PT_REGION_GBUF * n_pix + gb] = vec4<f32>(g_n, g_depth);
    pt_scratch[PT_REGION_GBUF * n_pix + gb + n_pix] = vec4<f32>(g_alb, g_spec);

    // The progressive accumulator keeps raw radiance. Demodulation is only for
    // the filter, and it must not touch the reference path: g_alb comes from
    // one sample's jittered position, so on a textured surface it differs from
    // dispatch to dispatch, and dividing by one value while multiplying back
    // by another is a systematic error over the whole image rather than a
    // rounding one. Measured at 8.6 levels RMSE before this was split.
    pt_scratch[idx] = pt_scratch[idx] + vec4<f32>(total, 0.0);

    // Per-frame illumination estimate for the reconstruction chain, where the
    // divide and the multiply are the same frame's albedo by construction.
    let mean = total / f32(max(ptp.spp, 1u));
    pt_scratch[PT_REGION_PING * n_pix + idx] = vec4<f32>(mean / g_alb, 0.0);
}

// Reprojects the pixel into the previous frame and blends. The rejection test
// is the whole value of the pass: accepting a stale sample across a
// disocclusion is what produces smearing, and it is far more visible than the
// noise the blend removes.
@compute @workgroup_size(8, 8, 1)
fn pt_temporal(@builtin(global_invocation_id) gid: vec3<u32>) {
    if (gid.x >= params.width || gid.y >= params.height) {
        return;
    }
    let n_pix = pt_pix_count();
    let idx = gid.y * params.width + gid.x;
    let gb = (ptp.parity * 2u) * n_pix + idx;
    let cur = pt_scratch[PT_REGION_PING * n_pix + idx].xyz;
    let g0 = pt_scratch[PT_REGION_GBUF * n_pix + gb];
    let n_cur = g0.xyz;
    let z_cur = g0.w;

    var hist_col = cur;
    var hist_len = 0.0;
    var m1 = pt_luma(cur);
    var m2 = m1 * m1;

    if (z_cur > 0.0) {
        // World position of this pixel's primary hit, projected with the
        // previous frame's basis. fwd/right/up are orthonormal, so the inverse
        // of pixel_ray_dir is three dot products.
        let dir = pt_camera_ray(f32(gid.x) + 0.5, f32(gid.y) + 0.5);
        let wp = params.cam_pos.xyz + dir * z_cur;
        let d = wp - ptp.prev_cam_pos.xyz;
        let pz = dot(d, ptp.prev_fwd.xyz);
        if (pz > 1e-4) {
            let a = dot(d, ptp.prev_right.xyz) / pz;
            let b = dot(d, ptp.prev_up.xyz) / pz;
            let u = a / (params.aspect * params.fov_scale);
            let v = b / params.fov_scale;
            let px = (u * 0.5 + 0.5) * f32(params.width);
            let py = (0.5 - v * 0.5) * f32(params.height);
            let ix = i32(floor(px));
            let iy = i32(floor(py));
            if (ix >= 0 && iy >= 0 && ix < i32(params.width) && iy < i32(params.height)) {
                let pidx = u32(iy) * params.width + u32(ix);
                let pgb = ((1u - ptp.parity) * 2u) * n_pix + pidx;
                let pg0 = pt_scratch[PT_REGION_GBUF * n_pix + pgb];
                let z_prev = pg0.w;
                // Distance along the previous view ray to the same world point.
                let z_want = length(d);
                let z_prev_world = z_prev;
                let ok_depth = z_prev > 0.0 && abs(z_prev_world - z_want) <= 0.05 * max(z_want, 1.0);
                let ok_normal = dot(pg0.xyz, n_cur) > 0.9;
                if (ok_depth && ok_normal) {
                    let h = pt_scratch[PT_REGION_HIST * n_pix + pidx];
                    let mom = pt_scratch[PT_REGION_HIST * n_pix + n_pix + pidx];
                    hist_len = min(h.w, max(ptp.max_history, 1.0));
                    let alpha = max(ptp.temporal_alpha, 1.0 / (hist_len + 1.0));
                    hist_col = mix(h.xyz, cur, alpha);
                    let l = pt_luma(cur);
                    m1 = mix(mom.x, l, alpha);
                    m2 = mix(mom.y, l * l, alpha);
                }
            }
        }
    }

    let new_len = hist_len + 1.0;
    pt_scratch[PT_REGION_HIST * n_pix + idx] = vec4<f32>(hist_col, new_len);
    pt_scratch[PT_REGION_HIST * n_pix + n_pix + idx] = vec4<f32>(m1, m2, 0.0, 0.0);

    // Variance drives the luminance edge-stop below. Short histories cannot
    // estimate it, so they are given a large value and filtered hard — which
    // is exactly the right behaviour just after a disocclusion.
    var variance = max(m2 - m1 * m1, 0.0);
    if (new_len < 4.0) {
        variance = max(variance, 1.0);
    }
    pt_scratch[PT_REGION_PING * n_pix + idx] = vec4<f32>(hist_col, variance);
}

fn pt_atrous_h(i: i32) -> f32 {
    if (i == 0) {
        return 0.375;
    }
    if (i == 1 || i == -1) {
        return 0.25;
    }
    return 0.0625;
}

// Edge-avoiding a-trous wavelet pass (Dammertz et al., with SVGF's variance
// driven luminance term). Run several times with a doubling hole size, it
// reaches a wide radius for a fixed 5x5 tap count per pass — the same
// separable-cost argument the penumbra filter makes in soft-shadows.md.
@compute @workgroup_size(8, 8, 1)
fn pt_atrous(@builtin(global_invocation_id) gid: vec3<u32>) {
    if (gid.x >= params.width || gid.y >= params.height) {
        return;
    }
    let n_pix = pt_pix_count();
    let idx = gid.y * params.width + gid.x;
    let in_base = ptp.atrous_in * n_pix;
    let out_base = (1u - ptp.atrous_in) * n_pix;
    let gb = (ptp.parity * 2u) * n_pix + idx;

    let g0 = pt_scratch[PT_REGION_GBUF * n_pix + gb];
    let n_c = g0.xyz;
    let z_c = g0.w;
    let c = pt_scratch[PT_REGION_PING * n_pix + in_base + idx];
    if (z_c <= 0.0) {
        pt_scratch[PT_REGION_PING * n_pix + out_base + idx] = c;
        return;
    }
    // A pixel whose primary hit is specular or emissive was never demodulated,
    // so the filter would be smoothing radiance rather than illumination —
    // that blurs a mirror image and smears an emitter's own edge. Those pixels
    // get the narrow passes only, which still removes per-pixel noise without
    // reaching across the reflection.
    let spec_c = pt_scratch[PT_REGION_GBUF * n_pix + (ptp.parity * 2u + 1u) * n_pix + idx].w;
    if (spec_c > 0.5 && ptp.atrous_step > 2u) {
        pt_scratch[PT_REGION_PING * n_pix + out_base + idx] = c;
        return;
    }
    let l_c = pt_luma(c.xyz);

    // The luminance edge-stop needs a variance estimate, and where it comes
    // from decides whether this filter does anything at all. Temporal moments
    // are the good estimate, but they need a few frames of history; with none
    // (a disocclusion, or temporal reprojection switched off entirely) the
    // variance reads as zero, every neighbour looks like an edge, and the
    // filter silently passes the noisy input straight through. So a short
    // history falls back to a spatial estimate over the same window.
    var var_sum = 0.0;
    var l_sum = 0.0;
    var l2_sum = 0.0;
    var var_w = 0.0;
    for (var dy = -2; dy <= 2; dy = dy + 1) {
        for (var dx = -2; dx <= 2; dx = dx + 1) {
            let sx = i32(gid.x) + dx;
            let sy = i32(gid.y) + dy;
            if (sx < 0 || sy < 0 || sx >= i32(params.width) || sy >= i32(params.height)) {
                continue;
            }
            let sp = pt_scratch[PT_REGION_PING * n_pix + in_base + u32(sy) * params.width + u32(sx)];
            let sl = pt_luma(sp.xyz);
            var_sum = var_sum + sp.w;
            l_sum = l_sum + sl;
            l2_sum = l2_sum + sl * sl;
            var_w = var_w + 1.0;
        }
    }
    let inv_n = 1.0 / max(var_w, 1.0);
    let mean_l = l_sum * inv_n;
    var est_var = var_sum * inv_n;
    if (pt_scratch[PT_REGION_HIST * n_pix + idx].w < 4.0) {
        est_var = max(l2_sum * inv_n - mean_l * mean_l, 0.0);
    }
    let sigma_l = sqrt(max(est_var, 0.0));

    let step = i32(ptp.atrous_step);
    var sum = c.xyz * pt_atrous_h(0) * pt_atrous_h(0);
    var sum_var = c.w * pt_atrous_h(0) * pt_atrous_h(0) * pt_atrous_h(0) * pt_atrous_h(0);
    var wsum = pt_atrous_h(0) * pt_atrous_h(0);
    for (var dy = -2; dy <= 2; dy = dy + 1) {
        for (var dx = -2; dx <= 2; dx = dx + 1) {
            if (dx == 0 && dy == 0) {
                continue;
            }
            let sx = i32(gid.x) + dx * step;
            let sy = i32(gid.y) + dy * step;
            if (sx < 0 || sy < 0 || sx >= i32(params.width) || sy >= i32(params.height)) {
                continue;
            }
            let sidx = u32(sy) * params.width + u32(sx);
            let sg = pt_scratch[PT_REGION_GBUF * n_pix + (ptp.parity * 2u) * n_pix + sidx];
            if (sg.w <= 0.0) {
                continue;
            }
            if (pt_scratch[PT_REGION_GBUF * n_pix + (ptp.parity * 2u + 1u) * n_pix + sidx].w != spec_c) {
                continue;
            }
            let s = pt_scratch[PT_REGION_PING * n_pix + in_base + sidx];

            let w_n = pow(max(dot(n_c, sg.xyz), 0.0), ptp.phi_normal);
            let w_z = exp(-abs(z_c - sg.w) / (ptp.phi_depth * max(z_c, 1.0) + 1e-4));
            let w_l = exp(-abs(l_c - pt_luma(s.xyz)) / (ptp.phi_lum * sigma_l + 1e-3));
            let h = pt_atrous_h(dx) * pt_atrous_h(dy);
            let w = h * w_n * w_z * w_l;
            sum = sum + s.xyz * w;
            sum_var = sum_var + s.w * w * w;
            wsum = wsum + w;
        }
    }
    let inv = 1.0 / max(wsum, 1e-6);

    // Fade the filter out as the running mean builds up. A settled camera
    // should end at the sharp, grainy image the raw estimator converges to,
    // not at a blurred version of it — spatial filtering is a stand-in for
    // samples you do not have yet, so once you have them it should get out of
    // the way. Only a short history (motion, disocclusion) is filtered hard.
    var strength = 1.0;
    if (ptp.atrous_fade > 0.0) {
        strength = clamp(1.0 - pt_scratch[PT_REGION_HIST * n_pix + idx].w / ptp.atrous_fade, 0.0, 1.0);
    }
    let filtered = sum * inv;
    pt_scratch[PT_REGION_PING * n_pix + out_base + idx] = vec4<f32>(mix(c.xyz, filtered, strength), sum_var * inv * inv);
}

@compute @workgroup_size(8, 8, 1)
fn pt_clear(@builtin(global_invocation_id) gid: vec3<u32>) {
    if (gid.x >= params.width || gid.y >= params.height) {
        return;
    }
    let n_pix = pt_pix_count();
    let idx = gid.y * params.width + gid.x;
    pt_scratch[idx] = vec4<f32>(0.0);
    pt_scratch[PT_REGION_HIST * n_pix + idx] = vec4<f32>(0.0);
    pt_scratch[PT_REGION_HIST * n_pix + n_pix + idx] = vec4<f32>(0.0);
    pt_scratch[PT_REGION_PING * n_pix + idx] = vec4<f32>(0.0);
    pt_scratch[PT_REGION_PING * n_pix + n_pix + idx] = vec4<f32>(0.0);
    pt_scratch[PT_REGION_GBUF * n_pix + idx] = vec4<f32>(0.0);
    pt_scratch[PT_REGION_GBUF * n_pix + n_pix + idx] = vec4<f32>(0.0);
    pt_scratch[PT_REGION_GBUF * n_pix + 2u * n_pix + idx] = vec4<f32>(0.0);
    pt_scratch[PT_REGION_GBUF * n_pix + 3u * n_pix + idx] = vec4<f32>(0.0);
    pt_scratch[(PT_REGION_RESV + 0u) * n_pix + idx] = vec4<f32>(0.0);
    pt_scratch[(PT_REGION_RESV + 1u) * n_pix + idx] = vec4<f32>(0.0);
}

@compute @workgroup_size(8, 8, 1)
fn pt_resolve(@builtin(global_invocation_id) gid: vec3<u32>) {
    if (gid.x >= params.width || gid.y >= params.height) {
        return;
    }
    let n_pix = pt_pix_count();
    let idx = gid.y * params.width + gid.x;
    let alb = pt_scratch[PT_REGION_GBUF * n_pix + (ptp.parity * 2u + 1u) * n_pix + idx].xyz;

    var c: vec3<f32>;
    if ((ptp.denoise & PT_DN_ACTIVE) != 0u) {
        c = pt_scratch[PT_REGION_PING * n_pix + ptp.atrous_in * n_pix + idx].xyz * alb;
    } else {
        c = pt_scratch[idx].xyz / f32(max(ptp.accum, 1u));
    }

    let toned = tonemap(c);
    let rgb = clamp(toned * 255.0, vec3<f32>(0.0), vec3<f32>(255.0));
    let r = u32(floor(rgb.x));
    let g = u32(floor(rgb.y));
    let b = u32(floor(rgb.z));
    pixels[idx] = r | (g << 8u) | (b << 16u) | (255u << 24u);
}


// ---------------------------------------------------------------------------
// Probe update, path traced
//
// The same probe field the megakernel fills, filled here instead by real
// multi-bounce transport. It replaces one thing only — how a probe ray is
// traced — and shares everything else with gi.wesl: the octahedral layout, the
// cascades, the sparse baked volume, the rotating ray set. There is one
// storage format and one set of sampling rules, which is what keeps the two
// transports comparable rather than merely coexisting.
//
// ---------------------------------------------------------------------------

