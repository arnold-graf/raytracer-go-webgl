// trace.wesl — Megakernel: bounce loop and pixel output.
// The heart of the renderer: ray_color() walks a small stack of reflection and
// refraction segments, dispatches by material, then main() tonemaps, dithers,
// quantizes to retro color depth, and writes RGBA8 pixels.

import package::types::{AA_RESOLVE_WG, GLOSSY_MIN_CONTRIB, HIT_NO_INSTANCE, Hit, MAT_CHECKER, MAT_DIFFUSE, MAT_EMIT, MAT_GLASS, MAT_METAL, MAT_MIRROR, PRIM_BOX, PRIM_CONE, PRIM_FLAG_GLASS_THIN, PRIM_FLAG_TRANSFORMED, SEG_MIN_CONTRIB, SURFACE_EPSILON, aa_dispatch, aa_hits, aa_list, hdr_pixels, params, pixels, prims, waters};
import package::profile::{PROF_BVH_STEPS, PROF_DIFFUSE_REFL, PROF_GLASS_BOUNCES, PROF_HIT_INST, PROF_HIT_PRIM, PROF_HIT_TERRAIN, PROF_HIT_WATER, PROF_MAX_SEGS, PROF_MIRROR_BOUNCES, PROF_PATH_SEGS, PROF_PIXELS, PROF_PRIM_TESTS, PROF_PRI_HIT_INST, PROF_PRI_HIT_PRIM, PROF_PRI_HIT_TERRAIN, PROF_PRI_HIT_WATER, PROF_PRI_SKY, PROF_SKY, bvh_steps_acc, prim_tests_acc, prof_inc, prof_max};
import package::math::{surface_grazing_weight, tonemap};
import package::texture::{plane_albedo, prim_texture_bump_normal, texture_eval, texture_eval_box, texture_eval_surface};
import package::sky::{sky};
import package::intersect::{thin_glass_exit_t, xf_to_local_dir, xf_to_local_point, xf_to_world_normal};
import package::terrain::{terrain_normal, water_normal};
import package::terrain_zone::{terrain_zone_shade_normal, terrain_zone_surface};
import package::flame::{accumulate_flame, heat_shimmer_dir};
import package::instance::{inst_local_point, inst_world_normal};
import package::bvh::{nearest_hit};
import package::shade::{cone_cap_normal_world, jitter_dir, normal_at, shade_diffuse, shade_ghost, shade_specular, terrain_albedo, tw_peak};

// RaySeg is one pending ray in the bounded work stack: a ray plus the
// multiplicative weight (throughput) its radiance contributes to the pixel, and
// its bounce depth. The stack lets glass blend its reflected and refracted
// lobes (a real ray tree) instead of picking one.
struct RaySeg {
    ro: vec3<f32>,
    rd: vec3<f32>,
    w: vec3<f32>,
    depth: u32,
    tag: u32,
};

const RAYSEG_GHOST: u32 = 1u;

struct TraceResult {
    color: vec3<f32>,
    primary: Hit,
};

// MAX_SEGS bounds the ray tree work stack. `stack` is a dynamically indexed
// local array, which Metal backs with per-thread scratch memory, and that
// allocation is charged to every invocation of this megakernel whether or not a
// ray ever branches. At 44 bytes per RaySeg it is the largest such array in the
// tracer, so oversizing it costs occupancy on every pixel of every scene.
//
// Counting worst cases on paper gives 1 + 2 + 4 = 7 (glass forking into two
// children at depths 0 and 1). That bound is not reachable: the Fresnel and
// SEG_MIN_CONTRIB gates prune one of the two lobes long before the tree fills
// out. TestRayStackHighWaterMark measures PROF_MAX_SEGS over every scene in
// scenes/ across a ring of 24 camera orientations each; the worst observed is 5
// (office-sunset, the facing glass panes of the skyway). 6 keeps one spare
// segment over that measurement.
//
// Raise this if the test reports a scene reaching MAX_SEGS: on overflow the
// `sp < MAX_SEGS` push guards silently drop a lobe rather than corrupt memory.
const MAX_SEGS: u32 = 6u;

// Thin-glass double-pane ghost (key 8). Parallel panes share the same normal, so
// a second reflect() at the same surface point only superimposes (brighter, no
// shift). Real double-pane reads as the reflection field sampled from a
// neighboring point on the glass — model that by shifting the incoming view
// direction along the surface before reflecting.
const THIN_GLASS_GHOST_WEIGHT: f32 = 0.42;
const THIN_GLASS_PANE_GAP: f32 = 0.045;
const THIN_GLASS_GHOST_ANG_MAX: f32 = 0.045;

// Fixed horizontal axis on the glass (world-up × normal). Used to pin slide direction
// so the ghost offset cannot spin around the normal when rd changes up close.
fn thin_glass_ghost_axis(n: vec3<f32>) -> vec3<f32> {
    var axis = cross(vec3<f32>(0.0, 1.0, 0.0), n);
    if (length(axis) < 0.001) {
        axis = cross(vec3<f32>(0.0, 0.0, 1.0), n);
    }
    return normalize(axis);
}

// Slide direction in the plane of incidence (n, rd), sign pinned to ghost_axis.
fn thin_glass_ghost_slide(n: vec3<f32>, rd: vec3<f32>) -> vec3<f32> {
    let axis = thin_glass_ghost_axis(n);
    var slide = cross(cross(n, rd), n);
    if (length(slide) < 0.001) {
        return axis;
    }
    slide = normalize(slide);
    if (dot(slide, axis) < 0.0) {
        slide = -slide;
    }
    return slide;
}

fn thin_glass_ghost_surface_shift(cosi: f32) -> f32 {
    return THIN_GLASS_PANE_GAP * (1.0 + 4.0 * (1.0 - cosi));
}

// ray_color evaluates the radiance along the camera ray by walking a small work
// stack: sky on a miss, emissive passthrough, single-bounce mirror/metal, the
// two-lobe Fresnel glass blend, diffuse direct lighting, and semi-reflective
// diffuse/checker. Reflections are gated by params.mirror.
fn ray_color(origin: vec3<f32>, dir0: vec3<f32>) -> TraceResult {
    var accum = vec3<f32>(0.0, 0.0, 0.0);
    var stack: array<RaySeg, MAX_SEGS>;
    stack[0] = RaySeg(origin, dir0, vec3<f32>(1.0, 1.0, 1.0), 0u, 0u);
    var sp = 1u;
    var primary = Hit(0.0, 0xffffffffu, 0u, HIT_NO_INSTANCE);
    var got_primary = false;
    var max_sp = 1u;

    loop {
        // Sampled before the pop, so this is the stack's high-water mark.
        // Guarded: normal frames must not pay for the diagnostic.
        if (params.profile_enabled != 0u) {
            max_sp = max(max_sp, sp);
        }
        if (sp == 0u) {
            break;
        }
        sp = sp - 1u;
        let seg = stack[sp];
        let ro = seg.ro;
        let tw = seg.w;
        let depth = seg.depth;
        let seg_tag = seg.tag;
        var rd = seg.rd;
        var hit: Hit;
        if (depth == 0u) {
            let pre = nearest_hit(ro, rd);
            let occluded = pre.idx != 0xffffffffu;
            let rd_new = heat_shimmer_dir(ro, rd, params.time, pre.t, occluded);
            if (dot(rd_new, rd) < 0.99999) {
                rd = rd_new;
                hit = nearest_hit(ro, rd);
            } else {
                hit = pre;
            }
        } else {
            hit = nearest_hit(ro, rd);
        }
        prof_inc(PROF_PATH_SEGS, 1u);
        if (!got_primary) {
            primary = hit;
            got_primary = true;
        }
        if (seg_tag != RAYSEG_GHOST) {
            accum = accum + tw * accumulate_flame(ro, rd, hit.t, params.time);
        }
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
        } else if (hit.kind == 2u) {
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
        var spec_strength = 0.0;
        var shininess = 32.0;
        var surf_eps = SURFACE_EPSILON;

        if (hit.kind == 0u) {
            let p = prims[hit.idx];
            mat = p.info.y;
            surf = p.surf;
            spec_strength = p.albedo.w;
            shininess = p.albedo2.w;
            if (shininess <= 0.0) {
                shininess = 32.0;
            }
            // For a transformed primitive evaluate the normal and procedural
            // texture in template/local space, then rotate back to world.
            var tpl_hp = hp;
            if (hit.inst_idx != HIT_NO_INSTANCE) {
                tpl_hp = inst_local_point(hit.inst_idx, hp);
            }
            var tex_p = tpl_hp;
            var tex_n = n;
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
            if (cone_cap) {
                n = cone_cap_normal_world(p, ro, hp);
                tex_n = normal_at(p, lhp, lrd);
                let grazing = surface_grazing_weight(n, to_cam);
                tex_n = prim_texture_bump_normal(p, lhp, tex_n, grazing);
                tex_p = lhp;
            } else if ((p.info.w & PRIM_FLAG_TRANSFORMED) != 0u) {
                tex_n = normal_at(p, lhp, lrd);
                var world_geo_n = xf_to_world_normal(p, tex_n);
                if (hit.inst_idx != HIT_NO_INSTANCE) {
                    world_geo_n = inst_world_normal(hit.inst_idx, world_geo_n);
                }
                let grazing = surface_grazing_weight(world_geo_n, to_cam);
                tex_n = prim_texture_bump_normal(p, lhp, tex_n, grazing);
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
                let grazing = surface_grazing_weight(world_geo_n, to_cam);
                tex_n = prim_texture_bump_normal(p, lhp, tex_n, grazing);
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
                alb = texture_eval_box(hit.idx, tex_p, tex_n, p.geo_a.xyz, p.geo_b.xyz, p.info.z, plane_albedo(p, hp));
            } else {
                alb = texture_eval_surface(p, tex_p, tex_n, plane_albedo(p, hp));
            }
        } else if (hit.kind == 1u) {
            mat = MAT_DIFFUSE;
            n = terrain_normal(hit.idx, hp);
            n = terrain_zone_shade_normal(hit.idx, hp, n);
            alb = terrain_albedo(hit.idx, hp, n);
            let zs = terrain_zone_surface(hit.idx, hp.xz);
            surf.x = zs.x;
            surf.z = zs.y;
            spec_strength = zs.z;
            shininess = zs.w;
            if (shininess <= 0.0) {
                shininess = 32.0;
            }
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

        let ep = hp + n * surf_eps;
        if (seg_tag == RAYSEG_GHOST) {
            accum = accum + tw * shade_ghost(hp, alb, n, ep);
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
        // reflective = depth < max_bounce_depth && mirror enabled.
        var max_depth = params.max_bounce_depth;
        if (max_depth == 0u) {
            max_depth = 2u;
        }

        let reflective = depth < max_depth && params.mirror != 0u;
        // Throughput this segment carries, and the albedo-weighted brightness a
        // child spawned here could still contribute. Child lobes below
        // SEG_MIN_CONTRIB are dropped: see the constant in types.wesl.
        let twm = tw_peak(tw);
        let twm_alb = twm * tw_peak(alb);

        if ((mat == MAT_MIRROR || mat == MAT_METAL) && reflective) {
            prof_inc(PROF_MIRROR_BOUNCES, 1u);
            if (twm_alb * 0.96 > SEG_MIN_CONTRIB && sp < MAX_SEGS) {
                stack[sp] = RaySeg(ep, jitter_dir(reflect(rd, n), hp, rough), tw * alb * 0.96, depth + 1u, 0u);
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
            let thin = hit.kind == 0u && (prims[hit.idx].info.w & PRIM_FLAG_GLASS_THIN) != 0u;
            let eta = 1.0 / ior;
            let k = 1.0 - eta * eta * (1.0 - cosi * cosi);
            let tir = k < 0.0;
            var w_refl = reflectance;
            if (tir) {
                w_refl = 1.0;
            }

            let w_trans = twm_alb * (1.0 - reflectance);

            if (thin) {
                // Single-sheet glass: enter + exit refraction in one hop (ior
                // controls bend strength). Skips a separate back-face trace hit
                // and its Fresnel lobes, so it stays cheaper than thick glass.
                if (!tir && w_refl < 0.98 && w_trans > SEG_MIN_CONTRIB && sp < MAX_SEGS) {
                    let cost = sqrt(k);
                    let rd_in = normalize(rd * eta + n * (eta * cosi - cost));
                    let enter_hp = hp - n * surf_eps;
                    let t_span = thin_glass_exit_t(prims[hit.idx], enter_hp, rd_in, 0.0);
                    let exit_hp = enter_hp + rd_in * t_span;
                    let exit_n = -n;
                    let eta_out = ior;
                    let cosi_out = max(0.0, dot(rd_in, exit_n));
                    let k_out = 1.0 - eta_out * eta_out * (1.0 - cosi_out * cosi_out);
                    if (k_out >= 0.0) {
                        let cost_out = sqrt(k_out);
                        let rd_out = jitter_dir(normalize(rd_in * eta_out + exit_n * (eta_out * cosi_out - cost_out)), exit_hp, rough * 0.35);
                        stack[sp] = RaySeg(exit_hp + exit_n * surf_eps, rd_out, tw * alb * (1.0 - reflectance), depth + 1u, 0u);
                        sp = sp + 1u;
                    }
                }
            } else {
                // Thick glass: refract at this interface; the back face is a
                // separate hit with its own Fresnel/refraction lobes.
                if (!tir && w_refl < 0.98 && w_trans > SEG_MIN_CONTRIB && sp < MAX_SEGS) {
                    let cost = sqrt(k);
                    let rr = jitter_dir(normalize(rd * eta + n * (eta * cosi - cost)), hp, rough * 0.35);
                    stack[sp] = RaySeg(hp - n * surf_eps, rr, tw * alb * (1.0 - reflectance), depth + 1u, 0u);
                    sp = sp + 1u;
                }
            }

            // Reflected lobe (the world in front of the pane). Its weight is the
            // Fresnel reflectance, so even a head-on clear window keeps a ~4%
            // floor. reflMin keeps deep, faint bounces bounded.
            var refl_min = 0.02;
            if (depth > 0u) {
                refl_min = 0.2;
            }
            if (w_refl > refl_min && twm * w_refl > SEG_MIN_CONTRIB && sp < MAX_SEGS) {
                let rd_refl = jitter_dir(reflect(rd, n), hp, rough);
                stack[sp] = RaySeg(ep, rd_refl, tw * w_refl, depth + 1u, 0u);
                sp = sp + 1u;

                if (thin && depth == 0u && params.thin_glass_ghost != 0u && sp < MAX_SEGS) {
                    let slide = thin_glass_ghost_slide(n, rd);
                    let surf_shift = thin_glass_ghost_surface_shift(cosi);
                    let ang = min(surf_shift / max(hit.t, 0.35), THIN_GLASS_GHOST_ANG_MAX);
                    let rd_pane2 = normalize(rd + slide * ang);
                    let ghost_w = w_refl * THIN_GLASS_GHOST_WEIGHT;
                    var ghost_rd = reflect(rd_pane2, n);
                    if (rough > 0.001) {
                        ghost_rd = jitter_dir(ghost_rd, hp, rough * 1.5);
                    }
                    if (ghost_w > refl_min && twm * ghost_w > SEG_MIN_CONTRIB) {
                        stack[sp] = RaySeg(ep, ghost_rd, tw * ghost_w, max_depth, RAYSEG_GHOST);
                        sp = sp + 1u;
                    }
                }
            }
            if (spec_strength > 0.0) {
                accum = accum + tw * shade_specular(hp, n, ep, tw, rd, spec_strength, shininess);
            }
            continue;
        }

        // Diffuse / checker, plus mirror/metal/glass falling through here at the
        // depth cap (or when reflections are disabled): shaded as diffuse.
        let lit = shade_diffuse(hp, alb, n, ep, tw, rd, spec_strength, shininess);
        let refl = surf.z;
        // A glossy lobe on a *primary* hit is the sheen the scene author asked
        // for, however faint, so it only has to clear the generic
        // SEG_MIN_CONTRIB floor. Only reflections seen inside another
        // reflection are pruned by GLOSSY_MIN_CONTRIB.
        var glossy_min = GLOSSY_MIN_CONTRIB;
        if (depth == 0u) {
            glossy_min = SEG_MIN_CONTRIB;
        }
        if (refl > 0.0 && reflective && twm * refl > glossy_min && (mat == MAT_DIFFUSE || mat == MAT_CHECKER) && sp < MAX_SEGS) {
            prof_inc(PROF_DIFFUSE_REFL, 1u);
            accum = accum + tw * lit * (1.0 - refl);
            stack[sp] = RaySeg(ep, jitter_dir(reflect(rd, n), hp, rough), tw * refl, depth + 1u, 0u);
            sp = sp + 1u;
            continue;
        }
        accum = accum + tw * lit;
    }
    prof_max(PROF_MAX_SEGS, max_sp);
    return TraceResult(accum, primary);
}

// quantize_rgb reduces dithered 0..255 RGB to a retro color depth. mode 1 is
// classic 15-bit (5-5-5); mode 2 is crush — 24 levels per channel with heavy dither.
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
    let q = floor(rgb * 23.0 / 255.0 + 0.5);
    return q * (255.0 / 23.0);
}

// bayer_offset returns a per-pixel dither nudge in 0..255 space before quantize.
fn bayer_offset(gid: vec2<u32>, quant_mode: u32) -> f32 {
    var bayer = array<u32, 16>(
        0u, 8u, 2u, 10u,
        12u, 4u, 14u, 6u,
        3u, 11u, 1u, 9u,
        15u, 7u, 13u, 5u,
    );
    let bayer_idx = (gid.y & 3u) * 4u + (gid.x & 3u);
    let n = f32(bayer[bayer_idx]) / 16.0 - 0.5;
    if (quant_mode == 2u) {
        return n * (255.0 / 23.0) * 1.5;
    }
    return n * 6.0;
}

// pixel_ray_dir builds a normalized camera ray through fractional pixel coords
// (gx, gy) plus a sub-pixel offset (sx, sy) in 0..1.
fn pixel_ray_dir(gx: f32, gy: f32, sx: f32, sy: f32) -> vec3<f32> {
    let u = (gx + sx) / f32(params.width) * 2.0 - 1.0;
    let v = 1.0 - (gy + sy) / f32(params.height) * 2.0;
    return normalize(
        params.fwd.xyz +
        params.right.xyz * (u * params.aspect * params.fov_scale) +
        params.up.xyz * (v * params.fov_scale)
    );
}

fn pixel_index(x: u32, y: u32) -> u32 {
    return y * params.width + x;
}

// AA_GEOM_MIN_LEVELS is the display-space luminance gap, in 8-bit levels, that
// two neighboring pixels on *different surfaces* must span before their shared
// boundary is worth a supersample.
//
// The pass-1 fingerprint test (hits_edge_packed) answers "do these pixels sit on
// different primitives", which is a weaker signal than "is there a visible jaggy
// here". Scene geometry is authored as many small boxes — a server rack, a wall
// of panels, a skyscraper facade — so a continuous, uniformly lit surface is
// covered in fingerprint boundaries with nothing to antialias. The gate is safe
// because the output is quantized: at 15-bit color depth (color_quant == 1) each
// channel keeps 32 levels, ~8.2 display levels apart, so neighbors closer than
// that largely land in the same bucket. Kept well below one quantization step so
// dithered near-threshold edges still resolve.
const AA_GEOM_MIN_LEVELS: f32 = 3.0;

// AA_SHADE_MIN_CURVE is the same idea for edges *within* one surface: shadow
// terminators, contact shadows, and texture boundaries, none of which change the
// primary hit and so were invisible to the fingerprint test.
//
// The test is the second difference of luminance across the pixel,
// |lo + hi - 2*center|, rather than a plain neighbor gap. A plain gap fires
// everywhere on the smooth falloff of a point light, where a supersample only
// re-averages a value the gradient already got right — a large ray cost for no
// visible change. Curvature is near zero along any linear ramp and jumps to the
// full step height where luminance *bends*, which is exactly where a hard shadow
// boundary produces a staircase.
//
// Sits just above one 15-bit quantization step (~8.2 levels), so a staircase
// that is merely a banding artifact — where the extra ray would land in the same
// output bucket — does not qualify. Measured on the office-sunset views, cost
// over the silhouette-only detector is +0.8ms at 10.0 and +1.2ms at 5.0, the
// latter mostly spent chasing dither noise.
//
//	 5.0 → below one quantization step; pays for dither noise
//	10.0 → current default; hard shadow and contact-shadow edges
//	48.0 → only the highest-contrast shadow boundaries
const AA_SHADE_MIN_CURVE: f32 = 10.0;

// display_luma is the Rec.709 luminance of the tonemapped color in 0..255, the
// space the eye judges an edge in. Pass 1 stashes it in hdr_pixels[].w so the
// resolve pass compares a scalar per neighbor instead of retonemapping four.
fn display_luma(c: vec3<f32>) -> f32 {
    let t = tonemap(c);
    return (0.2126 * t.x + 0.7152 * t.y + 0.0722 * t.z) * 255.0;
}

// AANeighbor is one neighboring pixel's contribution to the edge decision.
struct AANeighbor {
    // luma is the neighbor's display luminance, or the center's when off-screen
    // so it reads as a flat continuation and never invents an edge.
    luma: f32,
    // surface_edge is true when the neighbor's primary hit is a different
    // surface, i.e. the boundary is a silhouette rather than shading.
    surface_edge: bool,
    inside: bool,
}

fn aa_neighbor(fp: u32, center_luma: f32, x: u32, y: u32, inside: bool) -> AANeighbor {
    if (!inside) {
        return AANeighbor(center_luma, false, false);
    }
    let idx = pixel_index(x, y);
    return AANeighbor(hdr_pixels[idx].w, hits_edge_packed(fp, aa_hits[idx]), true);
}

// aa_axis_tap picks the sub-pixel tap along one axis: 0.25 toward the low
// neighbor, 0.75 toward the high one, or 0.5 for "no edge on this axis".
//
// The tap always aims at the stronger of the two sides. Aiming matters because
// the resolve pass averages the center sample with this single extra ray, so a
// tap at 0.5 would just re-trace the center and change nothing.
fn aa_axis_tap(center: f32, lo: AANeighbor, hi: AANeighbor) -> f32 {
    let d_lo = abs(center - lo.luma);
    let d_hi = abs(center - hi.luma);

    var edge = (lo.surface_edge && d_lo >= AA_GEOM_MIN_LEVELS) || (hi.surface_edge && d_hi >= AA_GEOM_MIN_LEVELS);
    if (!edge && lo.inside && hi.inside) {
        edge = abs(lo.luma + hi.luma - 2.0 * center) >= AA_SHADE_MIN_CURVE;
    }
    if (!edge) {
        return 0.5;
    }
    if (d_lo >= d_hi) {
        return 0.25;
    }
    return 0.75;
}

// hits_edge is true when two packed primary hits belong to different surfaces.
fn hits_edge_packed(a: u32, b: u32) -> bool {
    if (a == b) {
        return false;
    }
    if (a == 0xffffffffu && b == 0xffffffffu) {
        return false;
    }
    return true;
}

fn pack_hit(h: Hit) -> u32 {
    if (h.idx == 0xffffffffu) {
        return 0xffffffffu;
    }
    return h.kind | (h.idx << 4u) | (h.inst_idx << 20u);
}

// analyze_edge finds which neighbors differ and picks a sub-pixel tap toward them
// (corner when two axes disagree, edge midpoint when only one does).
// Returns vec4(tap.x, tap.y, _, active) with active = 1 when supersampling is needed.
fn analyze_edge(gid: vec2<u32>, fp: u32, luma: f32) -> vec4<f32> {
    let left = aa_neighbor(fp, luma, max(gid.x, 1u) - 1u, gid.y, gid.x > 0u);
    let right = aa_neighbor(fp, luma, gid.x + 1u, gid.y, gid.x + 1u < params.width);
    let down = aa_neighbor(fp, luma, gid.x, max(gid.y, 1u) - 1u, gid.y > 0u);
    let up = aa_neighbor(fp, luma, gid.x, gid.y + 1u, gid.y + 1u < params.height);

    let sx = aa_axis_tap(luma, left, right);
    let sy = aa_axis_tap(luma, down, up);
    if (sx == 0.5 && sy == 0.5) {
        return vec4<f32>(0.5, 0.5, 0.0, 0.0);
    }
    return vec4<f32>(sx, sy, 0.0, 1.0);
}

// supersample_edge blends the pass-1 center sample with one tap toward the edge.
fn supersample_edge(gx: f32, gy: f32, center: vec3<f32>, tap: vec2<f32>) -> vec3<f32> {
    let ro = params.cam_pos.xyz;
    let extra = ray_color(ro, pixel_ray_dir(gx, gy, tap.x, tap.y)).color;
    return (center + extra) * 0.5;
}

fn write_pixel(gid: vec2<u32>, col: vec3<f32>) {
    let toned = tonemap(col);
    var rgb: vec3<f32>;
    if (params.color_quant == 3u) {
        rgb = clamp(toned * 255.0, vec3(0.0), vec3(255.0));
    } else {
        let bdt = bayer_offset(gid, params.color_quant);
        rgb = clamp(toned * 255.0 + vec3(bdt), vec3(0.0), vec3(255.0));
    }
    rgb = quantize_rgb(rgb, params.color_quant);
    let r = u32(floor(rgb.x));
    let g = u32(floor(rgb.y));
    let b = u32(floor(rgb.z));
    pixels[pixel_index(gid.x, gid.y)] = r | (g << 8u) | (b << 16u) | (255u << 24u);
}

@compute @workgroup_size(8, 8, 1)
fn main(@builtin(global_invocation_id) gid: vec3<u32>) {
    if (gid.x >= params.width || gid.y >= params.height) {
        return;
    }

    let ro = params.cam_pos.xyz;
    let gx = f32(gid.x);
    let gy = f32(gid.y);
    let g2 = vec2<u32>(gid.xy);

    prof_inc(PROF_PIXELS, 1u);
    let tr = ray_color(ro, pixel_ray_dir(gx, gy, 0.5, 0.5));

    prof_inc(PROF_BVH_STEPS, bvh_steps_acc);
    prof_inc(PROF_PRIM_TESTS, prim_tests_acc);

    if (params.adaptive_aa != 0u) {
        let idx = pixel_index(gid.x, gid.y);
        hdr_pixels[idx] = vec4<f32>(tr.color, display_luma(tr.color));
        aa_hits[idx] = pack_hit(tr.primary);
        write_pixel(g2, tr.color);
        return;
    }

    write_pixel(g2, tr.color);
}

// aa_pack_task encodes a supersample task: pixel coordinate plus which of the
// three sub-pixel taps (0.25 / 0.5 / 0.75) to use on each axis.
fn aa_pack_task(x: u32, y: u32, tap: vec2<f32>) -> u32 {
    let sxi = u32(tap.x * 4.0) - 1u; // 0.25|0.5|0.75 -> 0|1|2
    let syi = u32(tap.y * 4.0) - 1u;
    return (x & 0xfffu) | ((y & 0xfffu) << 12u) | (sxi << 24u) | (syi << 26u);
}

fn aa_task_pixel(task: u32) -> vec2<u32> {
    return vec2<u32>(task & 0xfffu, (task >> 12u) & 0xfffu);
}

fn aa_task_tap(task: u32) -> vec2<f32> {
    let sxi = (task >> 24u) & 3u;
    let syi = (task >> 26u) & 3u;
    return vec2<f32>(f32(sxi + 1u) * 0.25, f32(syi + 1u) * 0.25);
}

// aa_classify is AA pass 2 of 3: one thread per pixel, but it only reads the
// neighbor fingerprints and luminances and appends the pixels that need work.
// Splitting classification from supersampling is what makes the AA affordable.
// Edge pixels are scattered along silhouettes, so when a single pass did both,
// almost every 64-thread workgroup contained at least one edge pixel and the
// whole workgroup was billed for a full extra ray tree while most of its lanes
// sat idle. Compacting first means aa_resolve launches only full workgroups of
// genuine work.
@compute @workgroup_size(8, 8, 1)
fn aa_classify(@builtin(global_invocation_id) gid: vec3<u32>) {
    if (gid.x >= params.width || gid.y >= params.height) {
        return;
    }
    let idx = pixel_index(gid.x, gid.y);
    let edge = analyze_edge(vec2<u32>(gid.xy), aa_hits[idx], hdr_pixels[idx].w);
    if (edge.w < 0.5) {
        return;
    }
    let slot = atomicAdd(&aa_dispatch[3], 1u);
    if (slot >= arrayLength(&aa_list)) {
        return;
    }
    aa_list[slot] = aa_pack_task(gid.x, gid.y, edge.xy);
    // Grow the indirect workgroup count to cover this slot.
    atomicMax(&aa_dispatch[0], (slot + AA_RESOLVE_WG) / AA_RESOLVE_WG);
}

// aa_resolve is AA pass 3 of 3: supersample the compacted task list. Dispatched
// indirectly from aa_dispatch, so its thread count tracks the number of edge
// pixels rather than the screen size.
@compute @workgroup_size(64, 1, 1)
fn aa_resolve(@builtin(global_invocation_id) gid: vec3<u32>) {
    if (gid.x >= atomicLoad(&aa_dispatch[3])) {
        return;
    }
    let task = aa_list[gid.x];
    let px = aa_task_pixel(task);
    let idx = pixel_index(px.x, px.y);
    let col = supersample_edge(f32(px.x), f32(px.y), hdr_pixels[idx].xyz, aa_task_tap(task));
    write_pixel(px, col);
}
