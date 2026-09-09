// trace.wesl — Megakernel: bounce loop and pixel output.
// The heart of the renderer: ray_color() walks a small stack of reflection and
// refraction segments, dispatches by material, then main() tonemaps, dithers,
// quantizes to retro color depth, and writes RGBA8 pixels.

import package::types::{AA_RESOLVE_WG, GLOSSY_MIN_CONTRIB, HIT_NO_INSTANCE, Hit, MAT_CHECKER, MAT_DIFFUSE, MAT_EMIT, MAT_GLASS, MAT_METAL, MAT_MIRROR, PRIM_BOX, PRIM_CONE, PRIM_FLAG_GLASS_THIN, PRIM_FLAG_TRANSFORMED, SEG_MIN_CONTRIB, SURFACE_EPSILON, ShadowAux, aa_dispatch, aa_hits, aa_list, hdr_pixels, params, pixels, prims, shadow_aux, waters};
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
import package::shade::{cone_cap_normal_world, jitter_dir, normal_at, shade_diffuse, shade_ghost, shade_specular, shadow_aux_snapshot, terrain_albedo, tw_peak};

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
    // The primary hit's dominant-light shadow record, snapshotted out of the
    // private accumulators in shade.wesl. Zero contribution means this pixel has
    // no shadow worth softening.
    sh: ShadowAux,
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
    var sh_out = ShadowAux(vec3<f32>(0.0, 0.0, 0.0), 0.0, vec3<f32>(0.0, 1.0, 0.0), 0.0, vec3<f32>(0.0, 0.0, 0.0), 0.0, vec3<f32>(0.0, 0.0, 0.0), 0.0);
    // sh_out.soft_applied stays 0 here; only shadow_soften_v sets it.

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
        if (depth == 0u) {
            // Snapshot before any deeper segment shades and resets these.
            // Weighted by tw so a shadow seen through glass is softened by the
            // same amount its light was attenuated.
            sh_out = shadow_aux_snapshot(tw, n, hit.t);
        }
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
            if (depth == 0u) {
                // A glossy surface keeps only (1 - refl) of its direct lighting;
                // the rest is the reflected lobe. The record has to match, or
                // the filter adds back light this pixel never had.
                sh_out.full = sh_out.full * (1.0 - refl);
            }
            stack[sp] = RaySeg(ep, jitter_dir(reflect(rd, n), hp, rough), tw * refl, depth + 1u, 0u);
            sp = sp + 1u;
            continue;
        }
        accum = accum + tw * lit;
    }
    prof_max(PROF_MAX_SEGS, max_sp);
    return TraceResult(accum, primary, sh_out);
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
//
// The tap traces a *hard* shadow while `center` has already been softened, so
// blending them 50/50 would put a hard edge back into exactly the pixels the
// penumbra filter smoothed. The tap has to be given the same treatment — but it
// is a different sub-pixel, and AA fires precisely where that sub-pixel lands on
// *different geometry*, which is the whole reason the pixel was flagged.
//
// So the tap is corrected with the filter's conclusion, not its arithmetic. The
// filter's conclusion is a target visibility: "a pixel here should be blocking
// this fraction of its light". That is a property of the neighbourhood, it is
// smooth by construction, and it transfers one sub-pixel over. The correction is
// then evaluated against the tap's *own* occlusion and its *own* unoccluded
// light, both of which ray_color already computed for it.
//
// Adding the centre's finished radiance delta instead — which is what this did —
// pushes the centre's surface, albedo and light onto a sample that may be on
// another step of a staircase entirely. Along stair nosings, where every shadow
// edge crosses a geometric edge, that read as the softening being undone: ragged,
// re-hardened shadow borders visible only with AA on.
fn supersample_edge(gx: f32, gy: f32, center: vec3<f32>, tap: vec2<f32>, idx: u32) -> vec3<f32> {
    let ro = params.cam_pos.xyz;
    let res = ray_color(ro, pixel_ray_dir(gx, gy, tap.x, tap.y));
    var extra = res.color;
    let aux = shadow_aux[idx];
    if (params.soft_shadows != 0u && aux.soft_applied > 0.0) {
        // Scaled by the *lesser* of the two unoccluded lights, not the tap's own.
        //
        // The target fraction is a statement about the centre's neighbourhood, so
        // the correction it licenses is bounded by the light the centre actually
        // has. Using the tap's own light unbounded is what put bright specks
        // along every high-contrast silhouette: AA fires exactly where the tap
        // lands on different geometry, and a tap that is fully blocked (frac 1)
        // beside a centre that is lit (target 0) then gets +1 x *its* full light
        // added back — un-shadowing a bright surface in a single pixel. Against
        // an open fire that is a visible white dot on the edge of the geometry.
        //
        // Measured on that frame: isolated bright pixels 63 -> 40, which is below
        // the 41 the previous correction produced, while keeping most of the
        // stair-shadow improvement this correction exists for (deviation from the
        // no-AA reference 0.57 -> 0.78, against 0.93 before).
        let lit_bound = min(res.sh.full, aux.full);
        extra = max(extra + (res.sh.frac - aux.frac) * lit_bound, vec3<f32>(0.0, 0.0, 0.0));
    }
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

    let idx = pixel_index(gid.x, gid.y);
    let soft = params.soft_shadows != 0u;
    // The soften pass needs the linear colour to add light back into, so the
    // scratch is written whenever it runs even if adaptive AA is off. It carries
    // its own normal and depth for surface continuity and does not read aa_hits.
    if (params.adaptive_aa != 0u || soft) {
        hdr_pixels[idx] = vec4<f32>(tr.color, display_luma(tr.color));
    }
    if (params.adaptive_aa != 0u) {
        aa_hits[idx] = pack_hit(tr.primary);
    }
    if (soft) {
        shadow_aux[idx] = tr.sh;
    }
    write_pixel(g2, tr.color);
}

// SHADOW_SOFTEN_TAPS is how many neighbours the penumbra filter gathers.
//
// This is a filter width, not a sample count in the sampling sense: every tap
// reads an *exact* visibility that some pixel already computed with a real ray.
// More taps make a smoother gradient, they do not reduce noise, because there is
// no noise to reduce. That is the whole reason this beats jittering the shadow
// ray, which could only ever produce TAPS+1 discrete levels and paid a BVH
// traversal for each one.
// Taps per 1D pass. Separability is what makes a wide radius affordable: this
// many per axis reaches SHADOW_SOFTEN_MAX_PX at a density a 2D disc would need
// its square to match.
const SHADOW_SOFTEN_TAPS: u32 = 61u;

// TAPS and MAX_PX are a matched pair, and matching them is what removes the need
// to jitter. One more than twice MAX_PX means the comb steps at most one pixel
// at the widest radius, so it never skips a pixel and has nothing to alias
// against. An earlier version jittered the offsets per pixel to break up ridges,
// which was treating the symptom: neighbouring pixels then averaged different
// tap sets, and every penumbra carried noise at three times the dither floor —
// worst on grazing surfaces, where a shadow edge crosses the most geometry per
// pixel. Removing the jitter dropped that to the floor exactly.
//
// If MAX_PX is ever raised, raise TAPS with it or the comb starts skipping.

// SHADOW_SOFTEN_MAX_PX bounds the gather radius so a huge penumbra cannot turn
// this into a full-screen blur. It is a *shoulder*, not a clip — see
// shadow_radius().
const SHADOW_SOFTEN_MAX_PX: f32 = 30.0;

// Below this width the geometric penumbra is drawn exactly, with no compression
// at all. This is the range contact hardening lives in: an occluder touching a
// surface gives pen_px near zero and has to stay a hard edge, and anything that
// inflates small widths would soften every contact in the scene.
const SHADOW_SOFTEN_KNEE_PX: f32 = 4.0;

// The borrow that finds each pixel's radius samples the pen_px field, which is a
// smooth geometric quantity — it does not need one tap per pixel the way the
// fraction gather does. Every other pixel over the full reach is plenty.
const SHADOW_RADIUS_TAPS: u32 = 31u;

// After the max, the radius field is blurred over a reach proportional to the
// radius being smoothed, capped here. Proportional is load-bearing: a fixed reach
// wider than the plateau it is smoothing averages that plateau with the zeros
// around it and shrinks it. At a fixed 9 px every small penumbra in the contact
// test fell under the 1 px cutoff and the scene came back with hard shadows.
// Smoothing each feature at its own scale leaves small radii intact and still
// spreads the large steps that cause seams.
//
// The max is a
// dilation: it produces a plateau at the full width across a shadow's footprint
// and drops to the local value in one pixel at the rim. That cliff is invisible
// on the shadow that *produced* it, because the gather's parabola weight is
// already zero there — but any *other* shadow crossing the rim gets blurred at
// two different widths on either side of an invisible line, which is exactly
// the seam that shows up wherever a soft and a hard shadow share a surface.
//
// Blurring after the max is what fixes it without the erosion a distance taper
// causes: a blur leaves the interior of a plateau untouched, so pixels beside a
// shadow keep the full radius and the exchange stays symmetric. Only the rim
// becomes a gradient.
// How far past its own width a shadow keeps handing its radius outward, as a
// fraction of that width, tapering linearly to nothing.
//
// The dilation's reach test used to be a step: a tap within `tp` of this pixel
// contributed its *entire* width, and one pixel further contributed none. The
// first tap to satisfy it therefore moved the radius the whole way at once. On
// the atrium ceiling the raw penumbra field ramps 0.3 -> 11 px over six pixels,
// which is a perfectly smooth boundary; the dilation turned that into a plateau
// extending sixteen pixels further out and ending in a *four*-pixel cliff, at a
// position set by nothing in the scene -- just where distance-to-the-wide-region
// first fell below that region's own width. That cliff is an iso-line of the
// penumbra field, so it draws a clean curve across the ceiling, and a coherent
// curve is visible far below the contrast at which a scattered pixel would be.
//
// Tapering past `tp` only *adds* radius where there was none, so the plateau
// inside `tp` -- which is what keeps the light exchange symmetric and the umbra
// from eroding -- is untouched.
const SHADOW_DILATE_FEATHER: f32 = 1.0;

const SHADOW_RADIUS_SMOOTH_FRAC: f32 = 0.5;
const SHADOW_RADIUS_SMOOTH_MAX_PX: f32 = 9.0;
const SHADOW_RADIUS_SMOOTH_TAPS: u32 = 9u;

// The blur fades in across this range of radii instead of switching on, so a
// penumbra narrowing toward sub-pixel does not pop as the camera moves.
const SHADOW_SOFTEN_FADE_LO: f32 = 1.0;
const SHADOW_SOFTEN_FADE_HI: f32 = 2.5;

const PENUMBRA_FILTER_EPS: f32 = 1e-4;

// Surface-continuity thresholds for the gather. MIN_DOT rejects a corner,
// DEPTH_REL is the slack added to the plane-consistent depth allowance, and
// MIN_NDV caps how much that allowance can grow at grazing angles before it
// would start accepting anything.
const SHADOW_SOFTEN_MIN_DOT: f32 = 0.85;
const SHADOW_SOFTEN_DEPTH_REL: f32 = 0.01;
// Below this the tap's view ray is nearly parallel to the surface and the plane
// prediction is unusable, so the tap is rejected rather than guessed at.
const SHADOW_SOFTEN_MIN_DENOM: f32 = 0.02;

// Same surface? Normal agreement rejects corners; a plane test rejects
// silhouettes. Neither cares where one primitive ends and the next begins, which
// is the point: the AA fingerprint is per primitive, and a wall of panels is
// covered in primitive boundaries that are not surface boundaries.
//
// The depth test predicts, rather than tolerates. Extend this pixel's surface as
// a plane and ask where the *tap's* view ray would cross it: for a plane through
// P0 = cam + v0*depth0 with normal n, that is depth0 * (n·v0) / (n·v_tap). A tap
// on the same surface matches within a hair; one on a silhouette does not.
//
// The previous form allowed a slop of px_world * rad * slope, with slope growing
// as 1/(n·v) at grazing angles. On a long obliquely-viewed floor that reached
// ~43% of the depth — tens of world units — so it accepted almost anything and
// shadows bled straight through walls. Predicting the plane removes the
// dependence on obliquity entirely, so the tolerance can stay tight at any angle.
fn shadow_same_surface(me: ShadowAux, t: ShadowAux, tx: i32, ty: i32, ndv0: f32) -> bool {
    if (dot(t.normal, me.normal) < SHADOW_SOFTEN_MIN_DOT) {
        return false;
    }
    let v_tap = pixel_ray_dir(f32(tx), f32(ty), 0.5, 0.5);
    let denom = dot(me.normal, v_tap);
    if (abs(denom) < SHADOW_SOFTEN_MIN_DENOM) {
        return false;
    }
    let t_pred = me.depth * ndv0 / denom;
    return abs(t.depth - t_pred) <= me.depth * SHADOW_SOFTEN_DEPTH_REL;
}

// Clamped tap coordinate, so the plane prediction is evaluated at the pixel the
// gather actually reads.
fn shadow_clamp_x(x: i32) -> i32 {
    return clamp(x, 0, i32(params.width) - 1);
}

fn shadow_clamp_y(y: i32) -> i32 {
    return clamp(y, 0, i32(params.height) - 1);
}

// The geometric penumbra width, compressed into the width the filter can afford
// to gather.
//
// `min(pen, MAX)` was the obvious way to bound this and it destroys the effect it
// is bounding. Real penumbrae far exceed the cap: measured across one office
// frame, the median blocked pixel wants 164 px and 85% want more than the 10 px
// cap that used to be here, so nearly every shadow in the frame clamped to the
// *same* radius. Contact hardening was still computed correctly and then thrown
// away — a cone lamp whose shadow genuinely runs from ~20 px of penumbra near the
// shade to ~250 px across the room was drawn with one uniform blur end to end.
//
// This is the same problem as tonemapping radiance, and it takes the same answer:
// identity in the range you care about, then a shoulder that compresses instead
// of clipping. The hyperbola has slope 1 at the knee, so the join is smooth, and
// approaches MAX without ever reaching it, so two very wide penumbrae still order
// correctly instead of collapsing onto one value.
//
// Widening MAX is nearly free — the filter is memory-bound, and 10 -> 30 px with
// the tap count tripled to match measured identically on the server room (8.8 ms)
// and the villa (23.3) — so the shoulder only has to absorb the tail.
// Full width out to `tp`, then a linear taper to nothing at tp*(1+FEATHER).
// Constant inside the plateau, so this changes only the rim.
fn shadow_dilate_weight(d: f32, tp: f32) -> f32 {
    let feather = max(tp * SHADOW_DILATE_FEATHER, 1e-3);
    return clamp((tp + feather - d) / feather, 0.0, 1.0);
}

fn shadow_radius(pen: f32) -> f32 {
    if (pen <= SHADOW_SOFTEN_KNEE_PX) {
        return pen;
    }
    let e = pen - SHADOW_SOFTEN_KNEE_PX;
    let s = SHADOW_SOFTEN_MAX_PX - SHADOW_SOFTEN_KNEE_PX;
    return SHADOW_SOFTEN_KNEE_PX + s * e / (e + s);
}

fn shadow_tap(x: i32, y: i32) -> ShadowAux {
    let cx = u32(clamp(x, 0, i32(params.width) - 1));
    let cy = u32(clamp(y, 0, i32(params.height) - 1));
    return shadow_aux[pixel_index(cx, cy)];
}

// n·v for this pixel's own view ray, the numerator of the plane prediction.
fn shadow_ndv0(me: ShadowAux, gid: vec2<u32>) -> f32 {
    return dot(me.normal, pixel_ray_dir(f32(gid.x), f32(gid.y), 0.5, 0.5));
}

// shadow_soften_h and shadow_soften_v widen each pixel's hard shadow into a
// penumbra of the width its blocker distance implies, by averaging the exact
// occlusions around it.
//
// Split into two 1D passes because the radius has to be large. A disc needs four
// times the taps for twice the radius, so a single 2D pass could only afford
// about 14 px — and a penumbra that should span 40 px, blurred over 14, still
// reads as a hard edge with a faint gradient, at a tap density low enough to be
// visibly noisy. Separating costs 2N taps instead of N² for the same reach.
// The filter runs as four dispatches, not two, because the gather radius has to
// be a *smooth* field and has to exist before any fraction is blurred.
//
// radius_h and radius_v establish it: a separable maximum of each pixel's own
// penumbra width over the neighbours that can reach it. soften_h and soften_v
// then smooth that field and gather the fraction with it. Splitting them also
// fixes a smaller inconsistency in the old two-pass form, where the row gather
// used a row-only radius while the column gather used the full 2D one.
//
// Field reuse keeps ShadowAux at 64 bytes: pen_px is dead once radius_h has read
// it, so radius_v parks the 2D maximum there; r_h is dead once radius_v has read
// it, so soften_h parks the smoothed radius there for soften_v. Each pass only
// ever writes a field no *other* invocation of that same pass reads.

// radius_h: row maximum of the penumbra width, over the neighbours that can
// actually reach this pixel.
//
// A *shadowed* pixel already knows its own blocker, so it uses its own width and
// borrows nothing. Only a lit pixel has no penumbra of its own and must take one
// from the shadow beside it, so the gradient can spread outward across the hard
// edge as well as inward.
//
// Letting shadowed pixels borrow too was the bug behind overlapping shadows
// washing out. Two occluders of the *same* light at different distances — a far
// wall and a near server rack — give very different widths, and letting the
// wall's 15 px win inside the rack's shadow blurred the rack's crisp edge three
// times too wide, which read as brighter.
//
// A borrowed width is taken whole, gated by whether the source is within its own
// reach, rather than scaled down by distance. Scaling looks like the gentler
// choice and is the one that erodes the umbra: a lit pixel d away that shrinks
// its radius to p-d then gathers over p-d, so it reaches the shadow only while
// d < p/2. The shadowed side meanwhile keeps its full p and reaches p into the
// light; the two sides exchange unequal amounts and the shadow visibly shrinks.
@compute @workgroup_size(8, 8, 1)
fn shadow_radius_h(@builtin(global_invocation_id) gid: vec3<u32>) {
    if (gid.x >= params.width || gid.y >= params.height) {
        return;
    }
    let idx = pixel_index(gid.x, gid.y);
    let me = shadow_aux[idx];
    let ix = i32(gid.x);
    let iy = i32(gid.y);
    let ndv0 = shadow_ndv0(me, gid.xy);

    // A pixel with nothing blocked has an exactly zero fraction — no epsilon,
    // and nothing that depends on how bright the scene happens to be.
    let lit = tw_peak(me.frac) <= 0.0;
    var r = shadow_radius(me.pen_px);
    for (var i = 0u; i < SHADOW_RADIUS_TAPS && lit; i = i + 1u) {
        let o = (f32(i) / f32(SHADOW_RADIUS_TAPS - 1u) * 2.0 - 1.0) * SHADOW_SOFTEN_MAX_PX;
        let tx = shadow_clamp_x(ix + i32(round(o)));
        let t = shadow_tap(tx, iy);
        if (shadow_same_surface(me, t, tx, iy, ndv0)) {
            // Compressed *before* the reach test. pen_px is stored raw and can
            // run to hundreds of pixels when an occluder sits near the light, so
            // an untamed value reaches everywhere and wins the maximum over the
            // whole row. Compressing first lets distance actually decide.
            let tp = shadow_radius(t.pen_px);
            r = max(r, tp * shadow_dilate_weight(abs(o), tp));
        }
    }
    shadow_aux[idx].r_h = min(r, SHADOW_SOFTEN_MAX_PX);
}

// radius_v completes the separable maximum down each column and parks the result
// in pen_px, which nothing reads from here on.
@compute @workgroup_size(8, 8, 1)
fn shadow_radius_v(@builtin(global_invocation_id) gid: vec3<u32>) {
    if (gid.x >= params.width || gid.y >= params.height) {
        return;
    }
    let idx = pixel_index(gid.x, gid.y);
    let me = shadow_aux[idx];
    let ix = i32(gid.x);
    let iy = i32(gid.y);
    let ndv0 = shadow_ndv0(me, gid.xy);

    let lit = tw_peak(me.frac) <= 0.0;
    var r = me.r_h;
    for (var i = 0u; i < SHADOW_RADIUS_TAPS && lit; i = i + 1u) {
        let o = (f32(i) / f32(SHADOW_RADIUS_TAPS - 1u) * 2.0 - 1.0) * SHADOW_SOFTEN_MAX_PX;
        let ty = shadow_clamp_y(iy + i32(round(o)));
        let t = shadow_tap(ix, ty);
        if (shadow_same_surface(me, t, ix, ty, ndv0)) {
            let tp = t.r_h;
            r = max(r, tp * shadow_dilate_weight(abs(o), tp));
        }
    }
    shadow_aux[idx].pen_px = min(r, SHADOW_SOFTEN_MAX_PX);
}

// shadow_radius_blur averages the dilated radius over a small reach. Same-surface
// only, so the field does not bleed across a silhouette and reintroduce the leak
// the plane test exists to prevent.
fn shadow_radius_blur(me: ShadowAux, ix: i32, iy: i32, ndv0: f32, own: f32, vertical: bool) -> f32 {
    let reach = clamp(own * SHADOW_RADIUS_SMOOTH_FRAC, 1.0, SHADOW_RADIUS_SMOOTH_MAX_PX);
    var acc = own;
    var wsum = 1.0;
    for (var i = 0u; i < SHADOW_RADIUS_SMOOTH_TAPS; i = i + 1u) {
        let o = (f32(i) / f32(SHADOW_RADIUS_SMOOTH_TAPS - 1u) * 2.0 - 1.0) * reach;
        var tx = ix;
        var ty = iy;
        if (vertical) {
            ty = shadow_clamp_y(iy + i32(round(o)));
        } else {
            tx = shadow_clamp_x(ix + i32(round(o)));
        }
        let t = shadow_tap(tx, ty);
        if (!shadow_same_surface(me, t, tx, ty, ndv0)) {
            continue;
        }
        let u = abs(o) / reach;
        let w = 1.0 - u * u;
        var tv = t.pen_px;
        if (vertical) {
            tv = t.r_h;
        }
        acc = acc + tv * w;
        wsum = wsum + w;
    }
    return acc / max(wsum, PENUMBRA_FILTER_EPS);
}

@compute @workgroup_size(8, 8, 1)
fn shadow_soften_h(@builtin(global_invocation_id) gid: vec3<u32>) {
    if (gid.x >= params.width || gid.y >= params.height) {
        return;
    }
    let idx = pixel_index(gid.x, gid.y);
    let me = shadow_aux[idx];
    let ix = i32(gid.x);
    let iy = i32(gid.y);
    let ndv0 = shadow_ndv0(me, gid.xy);

    // Smooth the dilated radius across the row, and park it in r_h for soften_v.
    let r = shadow_radius_blur(me, ix, iy, ndv0, me.pen_px, false);
    shadow_aux[idx].r_h = r;
    if (r < 1.0) {
        shadow_aux[idx].frac_h = me.frac;
        return;
    }

    // Every same-surface neighbour counts, lit ones included. Giving each tap a
    // reach of its own silently drops lit taps (their width is zero), and a lit
    // pixel then averages nothing but shadow, whose 1/(1+k) steps draw bands
    // parallel to every shadow.
    var acc = me.frac;
    var wsum = 1.0;
    for (var i = 0u; i < SHADOW_SOFTEN_TAPS; i = i + 1u) {
        let o = (f32(i) / f32(SHADOW_SOFTEN_TAPS - 1u) * 2.0 - 1.0) * r;
        let tx = shadow_clamp_x(ix + i32(round(o)));
        let t = shadow_tap(tx, iy);
        if (!shadow_same_surface(me, t, tx, iy, ndv0)) {
            continue;
        }
        let u = abs(o) / r;
        let w = 1.0 - u * u;
        acc = acc + t.frac * w;
        wsum = wsum + w;
    }
    shadow_aux[idx].frac_h = acc / max(wsum, PENUMBRA_FILTER_EPS);
}

@compute @workgroup_size(8, 8, 1)
fn shadow_soften_v(@builtin(global_invocation_id) gid: vec3<u32>) {
    if (gid.x >= params.width || gid.y >= params.height) {
        return;
    }
    let idx = pixel_index(gid.x, gid.y);
    let me = shadow_aux[idx];
    // Cleared on every path that leaves the pixel alone, so supersample_edge
    // never applies a correction to a pixel this pass did not touch.
    shadow_aux[idx].soft_applied = 0.0;
    // depth is zero only where the primary ray found no diffuse surface — sky,
    // or a mirror seen head-on. There is no shadow there to widen.
    if (me.depth <= 0.0) {
        return;
    }
    let ix = i32(gid.x);
    let iy = i32(gid.y);
    let ndv0 = shadow_ndv0(me, gid.xy);

    let r = shadow_radius_blur(me, ix, iy, ndv0, me.r_h, true);
    if (r < SHADOW_SOFTEN_FADE_LO) {
        return;
    }

    var acc = me.frac_h;
    var wsum = 1.0;
    for (var i = 0u; i < SHADOW_SOFTEN_TAPS; i = i + 1u) {
        let o = (f32(i) / f32(SHADOW_SOFTEN_TAPS - 1u) * 2.0 - 1.0) * r;
        let ty = shadow_clamp_y(iy + i32(round(o)));
        let t = shadow_tap(ix, ty);
        if (!shadow_same_surface(me, t, ix, ty, ndv0)) {
            continue;
        }
        let u = abs(o) / r;
        let w = 1.0 - u * u;
        acc = acc + t.frac_h * w;
        wsum = wsum + w;
    }
    let frac_soft = acc / max(wsum, PENUMBRA_FILTER_EPS);

    // Faded in rather than switched on. A sub-pixel penumbra is a hard edge and
    // must stay one, but crossing that threshold abruptly makes the blur pop in
    // as the camera approaches a shadow.
    let fade = smoothstep(SHADOW_SOFTEN_FADE_LO, SHADOW_SOFTEN_FADE_HI, r);

    // The whole correction, both directions at once: this pixel should be
    // blocking the neighbourhood-average fraction of its light rather than its
    // own hard-edged one. Shadowed pixels block more than the average and get
    // some light back; lit pixels block none and give some up.
    //
    // Both sides are the same quantity, so this cannot manufacture light. The
    // fraction is bounded by 1 and multiplied by the light the pixel would have
    // had unoccluded, so a brightened pixel lands at most on fully lit and a
    // darkened one at least on fully shadowed.
    //
    // Multiplying by *this* pixel's full light is equally load-bearing. It keeps
    // the correction textured like the surface it lands on, and it is the
    // difference between softening a shadow and running an unsharp mask over the
    // albedo underneath it.
    //
    // Expressed as a target fraction so supersample_edge can reproduce it for a
    // different sub-pixel. Identical to (me.frac - frac_soft) * me.full * fade.
    let frac_target = mix(me.frac, frac_soft, fade);
    let delta = (me.frac - frac_target) * me.full;
    let col = max(hdr_pixels[idx].xyz + delta, vec3<f32>(0.0, 0.0, 0.0));
    shadow_aux[idx].frac = frac_target;
    shadow_aux[idx].soft_applied = 1.0;
    hdr_pixels[idx] = vec4<f32>(col, display_luma(col));
    write_pixel(vec2<u32>(gid.xy), col);
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
    let col = supersample_edge(f32(px.x), f32(px.y), hdr_pixels[idx].xyz, aa_task_tap(task), idx);
    write_pixel(px, col);
}
