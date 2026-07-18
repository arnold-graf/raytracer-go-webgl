// trace.wesl — Megakernel: bounce loop and pixel output.
// The heart of the renderer: ray_color() walks a small stack of reflection and
// refraction segments, dispatches by material, then main() tonemaps, dithers,
// quantizes to retro color depth, and writes RGBA8 pixels.

import package::types::{HIT_NO_INSTANCE, MAT_CHECKER, MAT_DIFFUSE, MAT_EMIT, MAT_GLASS, MAT_METAL, MAT_MIRROR, PRIM_BOX, PRIM_FLAG_TRANSFORMED, SURFACE_EPSILON, params, pixels, prims, waters};
import package::profile::{PROF_BVH_STEPS, PROF_DIFFUSE_REFL, PROF_GLASS_BOUNCES, PROF_HIT_INST, PROF_HIT_PRIM, PROF_HIT_TERRAIN, PROF_HIT_WATER, PROF_MIRROR_BOUNCES, PROF_PATH_SEGS, PROF_PIXELS, PROF_PRIM_TESTS, PROF_PRI_HIT_INST, PROF_PRI_HIT_PRIM, PROF_PRI_HIT_TERRAIN, PROF_PRI_HIT_WATER, PROF_PRI_SKY, PROF_SKY, bvh_steps_acc, prim_tests_acc, prof_inc};
import package::math::{clamp01};
import package::texture::{plane_albedo, texture_eval, texture_eval_box};
import package::sky::{sky};
import package::intersect::{xf_to_local_point, xf_to_world_normal};
import package::terrain::{terrain_normal, water_normal};
import package::instance::{inst_local_point, inst_world_normal};
import package::bvh::{nearest_hit};
import package::shade::{jitter_dir, normal_at, shade_diffuse, terrain_albedo};

fn tonemap_channel(x: f32) -> f32 {
    return clamp01((x * (2.51 * x + 0.03)) / (x * (2.43 * x + 0.59) + 0.14));
}

fn gamma_encode(x: f32) -> f32 {
    if (x <= 0.0) {
        return 0.0;
    }
    if (x >= 1.0) {
        return 1.0;
    }
    return pow(x, 1.0 / 2.2);
}

fn tonemap(c: vec3<f32>) -> vec3<f32> {
    return vec3<f32>(
        gamma_encode(tonemap_channel(c.x)),
        gamma_encode(tonemap_channel(c.y)),
        gamma_encode(tonemap_channel(c.z)),
    );
}

// RaySeg is one pending ray in the bounded work stack: a ray plus the
// multiplicative weight (throughput) its radiance contributes to the pixel, and
// its bounce depth. The stack lets glass blend its reflected and refracted
// lobes (a real ray tree) instead of picking one.
struct RaySeg {
    ro: vec3<f32>,
    rd: vec3<f32>,
    w: vec3<f32>,
    depth: u32,
};

// MAX_SEGS bounds the ray tree. Recursion is capped at depth 3 and only glass
// branches (into two children), at depths 0 and 1 (depth 2 is no longer
// "reflective" so it terminates), giving at most 1 + 2 + 4 = 7 live segments.
const MAX_SEGS: u32 = 16u;

// ray_color evaluates the radiance along the camera ray by walking a small work
// stack: sky on a miss, emissive passthrough, single-bounce mirror/metal, the
// two-lobe Fresnel glass blend, diffuse direct lighting, and semi-reflective
// diffuse/checker. Reflections are gated by params.mirror.
fn ray_color(origin: vec3<f32>, dir0: vec3<f32>) -> vec3<f32> {
    var accum = vec3<f32>(0.0, 0.0, 0.0);
    var stack: array<RaySeg, 16>;
    stack[0] = RaySeg(origin, dir0, vec3<f32>(1.0, 1.0, 1.0), 0u);
    var sp = 1u;

    loop {
        if (sp == 0u) {
            break;
        }
        sp = sp - 1u;
        let seg = stack[sp];
        let ro = seg.ro;
        let rd = seg.rd;
        let tw = seg.w;
        let depth = seg.depth;
        prof_inc(PROF_PATH_SEGS, 1u);

        let hit = nearest_hit(ro, rd);
        if (hit.idx == 0xffffffffu) {
            prof_inc(PROF_SKY, 1u);
            if (depth == 0u) {
                prof_inc(PROF_PRI_SKY, 1u);
            }
            accum = accum + tw * sky(rd);
            continue;
        }

        if (hit.kind == 0u) {
            prof_inc(PROF_HIT_PRIM, 1u);
            if (hit.inst_idx != HIT_NO_INSTANCE) {
                prof_inc(PROF_HIT_INST, 1u);
            }
            if (depth == 0u) {
                prof_inc(PROF_PRI_HIT_PRIM, 1u);
                if (hit.inst_idx != HIT_NO_INSTANCE) {
                    prof_inc(PROF_PRI_HIT_INST, 1u);
                }
            }
        } else if (hit.kind == 1u) {
            prof_inc(PROF_HIT_TERRAIN, 1u);
            if (depth == 0u) {
                prof_inc(PROF_PRI_HIT_TERRAIN, 1u);
            }
        } else {
            prof_inc(PROF_HIT_WATER, 1u);
            if (depth == 0u) {
                prof_inc(PROF_PRI_HIT_WATER, 1u);
            }
        }

        let hp = ro + rd * hit.t;
        var mat = MAT_DIFFUSE;
        var surf = vec4<f32>(0.0, 1.5, 0.0, 0.0);
        var alb = vec3<f32>(1.0, 1.0, 1.0);
        var n = vec3<f32>(0.0, 1.0, 0.0);

        if (hit.kind == 0u) {
            let p = prims[hit.idx];
            mat = p.info.y;
            surf = p.surf;
            // For a transformed primitive evaluate the normal and procedural
            // texture in template/local space, then rotate back to world.
            var tpl_hp = hp;
            if (hit.inst_idx != HIT_NO_INSTANCE) {
                tpl_hp = inst_local_point(hit.inst_idx, hp);
            }
            var tex_p = tpl_hp;
            var tex_n = n;
            if ((p.info.w & PRIM_FLAG_TRANSFORMED) != 0u) {
                let lhp = xf_to_local_point(p, tpl_hp);
                tex_n = normal_at(p, lhp);
                var ln = xf_to_world_normal(p, tex_n);
                if (hit.inst_idx != HIT_NO_INSTANCE) {
                    ln = inst_world_normal(hit.inst_idx, ln);
                }
                n = normalize(ln);
                tex_p = lhp;
            } else {
                tex_n = normal_at(p, tpl_hp);
                n = tex_n;
                if (hit.inst_idx != HIT_NO_INSTANCE) {
                    n = inst_world_normal(hit.inst_idx, n);
                }
            }
            if (dot(n, rd) > 0.0) {
                n = -n;
                tex_n = -tex_n;
            }
            if (p.info.x == PRIM_BOX) {
                alb = texture_eval_box(hit.idx, tex_p, tex_n, p.geo_a.xyz, p.geo_b.xyz, p.info.z, p.albedo.xyz);
            } else {
                alb = texture_eval(p.info.z, tex_p, tex_n, plane_albedo(p, hp));
            }
        } else if (hit.kind == 1u) {
            mat = MAT_DIFFUSE;
            n = terrain_normal(hit.idx, hp);
            alb = terrain_albedo(hit.idx, hp, n);
        } else {
            let wp = waters[hit.idx];
            mat = wp.info.x;
            surf = wp.surf;
            n = water_normal(hit.idx, hp);
            alb = texture_eval(wp.info.y, hp, n, wp.albedo.xyz);
        }

        if (hit.kind != 0u && dot(n, rd) > 0.0) {
            n = -n;
        }

        if (mat == MAT_EMIT) {
            accum = accum + tw * alb;
            continue;
        }

        let rough = surf.x;
        var ior = surf.y;
        if (ior == 0.0) {
            ior = 1.5;
        }
        var transmit = surf.w;
        if (transmit == 0.0) {
            transmit = 0.9;
        }
        let ep = hp + n * SURFACE_EPSILON;
        // reflective = depth < max_bounce_depth && mirror enabled.
        var max_depth = params.max_bounce_depth;
        if (max_depth == 0u) {
            max_depth = 2u;
        }
        let reflective = depth < max_depth && params.mirror != 0u;

        if ((mat == MAT_MIRROR || mat == MAT_METAL) && reflective) {
            prof_inc(PROF_MIRROR_BOUNCES, 1u);
            if (sp < MAX_SEGS) {
                stack[sp] = RaySeg(ep, jitter_dir(reflect(rd, n), hp, rough), tw * alb * 0.96, depth + 1u);
                sp = sp + 1u;
            }
            continue;
        }

        if (mat == MAT_GLASS && reflective) {
            prof_inc(PROF_GLASS_BOUNCES, 1u);
            let cosi = max(0.0, -dot(rd, n));
            var r0 = (1.0 - ior) / (1.0 + ior);
            r0 = r0 * r0;
            let fres = r0 + (1.0 - r0) * pow(1.0 - cosi, 5.0);
            let reflectance = fres + (1.0 - fres) * (1.0 - transmit);
            let eta = 1.0 / ior;
            let k = 1.0 - eta * eta * (1.0 - cosi * cosi);
            let tir = k < 0.0;
            var w_refl = reflectance;
            if (tir) {
                w_refl = 1.0;
            }

            // Transmitted (see-through) lobe, tinted by albedo and weighted by
            // (1 - reflectance). Skipped under TIR and near grazing angles where
            // it is almost fully reflected anyway.
            if (!tir && w_refl < 0.98 && sp < MAX_SEGS) {
                let cost = sqrt(k);
                let rr = jitter_dir(normalize(rd * eta + n * (eta * cosi - cost)), hp, rough * 0.35);
                stack[sp] = RaySeg(hp - n * SURFACE_EPSILON, rr, tw * alb * (1.0 - reflectance), depth + 1u);
                sp = sp + 1u;
            }

            // Reflected lobe (the world in front of the pane). Its weight is the
            // Fresnel reflectance, so even a head-on clear window keeps a ~4%
            // floor. reflMin keeps deep, faint bounces bounded.
            var refl_min = 0.02;
            if (depth > 0u) {
                refl_min = 0.2;
            }
            if (w_refl > refl_min && sp < MAX_SEGS) {
                stack[sp] = RaySeg(ep, jitter_dir(reflect(rd, n), hp, rough), tw * w_refl, depth + 1u);
                sp = sp + 1u;
            }
            continue;
        }

        // Diffuse / checker, plus mirror/metal/glass falling through here at the
        // depth cap (or when reflections are disabled): shaded as diffuse.
        let lit = shade_diffuse(hp, alb, n, ep, tw);
        let refl = surf.z;
        if (refl > 0.0 && reflective && (mat == MAT_DIFFUSE || mat == MAT_CHECKER) && sp < MAX_SEGS) {
            prof_inc(PROF_DIFFUSE_REFL, 1u);
            accum = accum + tw * lit * (1.0 - refl);
            stack[sp] = RaySeg(ep, jitter_dir(reflect(rd, n), hp, rough), tw * refl, depth + 1u);
            sp = sp + 1u;
            continue;
        }
        accum = accum + tw * lit;
    }
    return accum;
}

// quantize_rgb reduces dithered 0..255 RGB to a retro color depth. mode 1 is
// classic 15-bit (5-5-5, 32768 colors); mode 2 is the PC 256-color cube (3-3-2).
fn quantize_rgb(rgb: vec3<f32>, mode: u32) -> vec3<f32> {
    if mode == 3u {
        return rgb;
    }
    if mode == 0u {
        return rgb;
    }
    if mode == 1u {
        let q = floor(rgb * 31.0 / 255.0 + 0.5);
        return q * (255.0 / 31.0);
    }
    let rq = floor(rgb.x * 7.0 / 255.0 + 0.5);
    let gq = floor(rgb.y * 7.0 / 255.0 + 0.5);
    let bq = floor(rgb.z * 3.0 / 255.0 + 0.5);
    return vec3(rq * (255.0 / 7.0), gq * (255.0 / 7.0), bq * (255.0 / 3.0));
}

@compute @workgroup_size(8, 8, 1)
fn main(@builtin(global_invocation_id) gid: vec3<u32>) {
    if (gid.x >= params.width || gid.y >= params.height) {
        return;
    }

    let u = (f32(gid.x) + 0.5) / f32(params.width) * 2.0 - 1.0;
    let v = 1.0 - (f32(gid.y) + 0.5) / f32(params.height) * 2.0;
    let dir = normalize(
        params.fwd.xyz +
        params.right.xyz * (u * params.aspect * params.fov_scale) +
        params.up.xyz * (v * params.fov_scale)
    );
    let ro = params.cam_pos.xyz;

    prof_inc(PROF_PIXELS, 1u);
    var col = ray_color(ro, dir);
    col = tonemap(col);

    // Flush per-invocation BVH traversal-quality accumulators (one atomic pair
    // per pixel instead of one per node visit).
    prof_inc(PROF_BVH_STEPS, bvh_steps_acc);
    prof_inc(PROF_PRIM_TESTS, prim_tests_acc);

    var bayer = array<u32, 16>(
        0u, 8u, 2u, 10u,
        12u, 4u, 14u, 6u,
        3u, 11u, 1u, 9u,
        15u, 7u, 13u, 5u,
    );
    let bayer_idx = (gid.y & 3u) * 4u + (gid.x & 3u);
    var rgb: vec3<f32>;
    if (params.color_quant == 3u) {
        rgb = clamp(col * 255.0, vec3(0.0), vec3(255.0));
    } else {
        let bdt = (f32(bayer[bayer_idx]) / 16.0 - 0.5) * 6.0;
        rgb = clamp(col * 255.0 + vec3(bdt), vec3(0.0), vec3(255.0));
    }
    rgb = quantize_rgb(rgb, params.color_quant);
    let r = u32(floor(rgb.x));
    let g = u32(floor(rgb.y));
    let b = u32(floor(rgb.z));
    let a = 255u;

    // Host reads little-endian bytes as RGBA.
    pixels[gid.y * params.width + gid.x] = r | (g << 8u) | (b << 16u) | (a << 24u);
}








