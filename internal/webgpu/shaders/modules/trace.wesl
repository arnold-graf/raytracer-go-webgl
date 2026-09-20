// trace.wesl — Megakernel: bounce loop and pixel output.
// The heart of the renderer: ray_color() walks a small stack of reflection and
// refraction segments, dispatches by material, then main() tonemaps, dithers,
// quantizes to retro color depth, and writes RGBA8 pixels.

import package::types::{AA_RESOLVE_WG, GLOSSY_MIN_CONTRIB, HIT_NO_INSTANCE, Hit, MAT_CHECKER, MAT_DIFFUSE, MAT_EMIT, MAT_GLASS, MAT_METAL, MAT_MIRROR, PRIM_BOX, PRIM_CONE, PRIM_FLAG_GLASS_THIN, PRIM_FLAG_TRANSFORMED, SEG_MIN_CONTRIB, SURFACE_EPSILON, ShadowAux, aa_dispatch, aa_hits, aa_list, hdr_pixels, params, pixels, prims, shadow_aux, waters};
import package::profile::{PROF_AA_GEOM, PROF_AA_SHADE, PROF_AA_TASKS, PROF_BVH_STEPS, PROF_DIFFUSE_REFL, PROF_GLASS_BOUNCES, PROF_HIT_INST, PROF_HIT_PRIM, PROF_HIT_TERRAIN, PROF_HIT_WATER, PROF_MAX_SEGS, PROF_MIRROR_BOUNCES, PROF_PATH_SEGS, PROF_PIXELS, PROF_PRIM_TESTS, PROF_PRI_HIT_INST, PROF_PRI_HIT_PRIM, PROF_PRI_HIT_TERRAIN, PROF_PRI_HIT_WATER, PROF_PRI_SKY, PROF_SKY, bvh_steps_acc, prim_tests_acc, prof_inc, prof_max};
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

// RAYSEG_REFL and RAYSEG_REFR mark the two primary lobes the filter can hold
// out: everything reached through the reflection, and everything reached through
// the transmission. Segments carrying one accumulate into its register instead
// of the pixel colour. The tag is inherited by every child, because a pane seen
// inside the reflection belongs to the reflection.
//
// Two tags rather than one because glass forks both at a single hit and blurs
// them by different amounts — jitter_dir gets `rough` on the reflection and
// `rough * 0.35` on the refraction — and because they are the pair an author
// wants to set independently. A diffuse glossy surface only raises RAYSEG_REFL.
const RAYSEG_REFL: u32 = 2u;
const RAYSEG_REFR: u32 = 4u;
const RAYSEG_LOBE: u32 = 6u;

// Channel switches, packed into params.refl_filter alongside the enable and
// half-res bits because 348 is the last u32 before light_grid_min's alignment.
// Bit 2 leaves the reflection inline and jittered, bit 3 the refraction.
fn refl_chan_on() -> bool {
    return (params.refl_filter & 1u) != 0u && (params.refl_filter & 4u) == 0u;
}

fn refr_chan_on() -> bool {
    return (params.refl_filter & 1u) != 0u && (params.refl_filter & 8u) == 0u;
}

// How a ray_color call should treat the primary glossy lobe.
//
// GLOSS_ALWAYS traces it, which is what the filter-off path, a lead pixel and an
// adaptive-AA tap want. GLOSS_IF_SHARP traces it only when the surface is too
// near-mirror to survive being sampled at half rate — a non-lead pixel, in other
// words, whose lobe will otherwise be filled from its block's lead. GLOSS_NEVER
// skips it outright and is currently unused: a tap has to trace its lobe because
// it cannot know until it has hit something whether it landed on the centre's
// surface, and only a tap that did not gets to keep what it traced. See
// supersample_edge.
const GLOSS_ALWAYS: u32 = 0u;
const GLOSS_IF_SHARP: u32 = 1u;
const GLOSS_NEVER: u32 = 2u;

// refl_half_ok is the pre-trace half-resolution test. The blur radius needs the
// reflected distance, which is exactly what has not been traced yet, so it is
// estimated from the part that is known: radius saturates at
// 0.5 * rough * focal_px as the reflected hit recedes, and a reflected hit at
// about the surface's own distance gives half of that. Sampling at half rate
// costs nothing the blur was not about to destroy anyway — but only above a
// couple of pixels, which is why a near-mirror surface stays full rate.
const REFL_HALF_MIN_PX: f32 = 3.0;

// True when this lobe should not be traced at this pixel: an AA tap never traces
// one, and a non-lead pixel skips it wherever the blur is wide enough to fill in.
fn lobe_skip(gloss_mode: u32, rough: f32) -> bool {
    if gloss_mode == GLOSS_NEVER {
        return true;
    }
    return gloss_mode == GLOSS_IF_SHARP && (params.refl_filter & 2u) != 0u && refl_half_ok(rough);
}

fn refl_half_ok(rough: f32) -> bool {
    let focal_px = 0.5 * f32(params.width) / max(params.aspect * params.fov_scale, 1e-6);
    return 0.25 * rough * focal_px >= REFL_HALF_MIN_PX;
}

struct TraceResult {
    color: vec3<f32>,
    // The primary glossy lobe, already weighted, and *excluded* from color when
    // params.refl_filter is set. Each *_t is the distance from the surface to
    // what that lobe hit, or -1 for no lobe.
    refl: vec3<f32>,
    refl_t: f32,
    refl_blur: f32,
    refr: vec3<f32>,
    refr_t: f32,
    refr_blur: f32,
    // The surface the two lobes left from, for the filter's same-surface gate.
    lobe_n: vec3<f32>,
    lobe_d: f32,
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
    if length(axis) < 0.001 {
        axis = cross(vec3<f32>(0.0, 0.0, 1.0), n);
    }
    return normalize(axis);
}

// Slide direction in the plane of incidence (n, rd), sign pinned to ghost_axis.
fn thin_glass_ghost_slide(n: vec3<f32>, rd: vec3<f32>) -> vec3<f32> {
    let axis = thin_glass_ghost_axis(n);
    var slide = cross(cross(n, rd), n);
    if length(slide) < 0.001 {
        return axis;
    }
    slide = normalize(slide);
    if dot(slide, axis) < 0.0 {
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
// The shading inputs of a hit: material, albedo, normal and the surface
// parameters, for every kind of geometry the tracer knows.
struct Surface {
    mat: u32,
    // rough, ior, reflect, _
    surf: vec4<f32>,
    // reflect_blur, transmit_blur
    blur: vec2<f32>,
    alb: vec3<f32>,
    n: vec3<f32>,
    spec_strength: f32,
    shininess: f32,
}

// Everything needed to shade a hit, and nothing about what to do next.
//
// Factored out of ray_color so the probe pass can reach it without the segment
// stack, the glossy lobes and the reflection bookkeeping that a probe ray can
// never use. The alternative — a second dispatch written for probes — is the
// same shape as the bug that dropped campfires from an earlier probe shader:
// one more place for a surface type to go quietly missing. There is one
// implementation and both callers use it.
fn surface_at(hit: Hit, ro: vec3<f32>, rd: vec3<f32>, hp: vec3<f32>) -> Surface {
    var mat = MAT_DIFFUSE;
    var surf = vec4<f32>(0.0, 1.5, 0.0, 0.0);
    // reflect_blur, transmit_blur. Zero on both is every scene that has not
    // asked for the effect, and leaves the lobes exactly where they were.
    var blur = vec2<f32>(0.0, 0.0);
    var alb = vec3<f32>(1.0, 1.0, 1.0);
    var n = vec3<f32>(0.0, 1.0, 0.0);
    var spec_strength = 0.0;
    var shininess = 32.0;

    if hit.kind == 0u {
        let p = prims[hit.idx];
        mat = p.info.y;
        surf = p.surf;
        blur = p.surf2.xy;
        spec_strength = p.albedo.w;
        shininess = p.albedo2.w;
        if shininess <= 0.0 {
            shininess = 32.0;
        }
        // For a transformed primitive evaluate the normal and procedural
        // texture in template/local space, then rotate back to world.
        var tpl_hp = hp;
        if hit.inst_idx != HIT_NO_INSTANCE {
            tpl_hp = inst_local_point(hit.inst_idx, hp);
        }
        var tex_p = tpl_hp;
        var tex_n = n;
        var lrd = rd;
        if (p.info.w & PRIM_FLAG_TRANSFORMED) != 0u {
            lrd = xf_to_local_dir(p, rd);
        }
        var lhp = tpl_hp;
        if (p.info.w & PRIM_FLAG_TRANSFORMED) != 0u {
            lhp = xf_to_local_point(p, tpl_hp);
        }
        let cone_cap = p.info.x == PRIM_CONE && p.geo_b.y > 0.5 && abs(lhp.y - p.geo_a.w) < 0.02;
        let to_cam = params.cam_pos.xyz - hp;
        if cone_cap {
            n = cone_cap_normal_world(p, ro, hp);
            tex_n = normal_at(p, lhp, lrd);
            let grazing = surface_grazing_weight(n, to_cam);
            tex_n = prim_texture_bump_normal(p, lhp, tex_n, grazing);
            tex_p = lhp;
        } else if (p.info.w & PRIM_FLAG_TRANSFORMED) != 0u {
            tex_n = normal_at(p, lhp, lrd);
            var world_geo_n = xf_to_world_normal(p, tex_n);
            if hit.inst_idx != HIT_NO_INSTANCE {
                world_geo_n = inst_world_normal(hit.inst_idx, world_geo_n);
            }
            let grazing = surface_grazing_weight(world_geo_n, to_cam);
            tex_n = prim_texture_bump_normal(p, lhp, tex_n, grazing);
            var ln = xf_to_world_normal(p, tex_n);
            if hit.inst_idx != HIT_NO_INSTANCE {
                ln = inst_world_normal(hit.inst_idx, ln);
            }
            n = normalize(ln);
            tex_p = lhp;
        } else {
            tex_n = normal_at(p, tpl_hp, lrd);
            var world_geo_n = tex_n;
            if hit.inst_idx != HIT_NO_INSTANCE {
                world_geo_n = inst_world_normal(hit.inst_idx, tex_n);
            }
            let grazing = surface_grazing_weight(world_geo_n, to_cam);
            tex_n = prim_texture_bump_normal(p, lhp, tex_n, grazing);
            n = tex_n;
            if hit.inst_idx != HIT_NO_INSTANCE {
                n = inst_world_normal(hit.inst_idx, n);
            }
        }
        if dot(n, rd) > 0.0 {
            n = -n;
            tex_n = -tex_n;
        }
        if p.info.x == PRIM_BOX {
            alb = texture_eval_box(hit.idx, tex_p, tex_n, p.geo_a.xyz, p.geo_b.xyz, p.info.z, plane_albedo(p, hp));
        } else {
            alb = texture_eval_surface(p, tex_p, tex_n, plane_albedo(p, hp));
        }
    } else if hit.kind == 1u {
        mat = MAT_DIFFUSE;
        n = terrain_normal(hit.idx, hp);
        n = terrain_zone_shade_normal(hit.idx, hp, n);
        alb = terrain_albedo(hit.idx, hp, n);
        let zs = terrain_zone_surface(hit.idx, hp.xz);
        surf.x = zs.rough;
        surf.z = zs.reflect;
        blur.x = zs.reflect_blur;
        spec_strength = zs.specular;
        shininess = zs.shininess;
        if shininess <= 0.0 {
            shininess = 32.0;
        }
    } else {
        let wp = waters[hit.idx];
        mat = wp.info.x;
        surf = wp.surf;
        n = water_normal(hit.idx, hp);
        alb = texture_eval(wp.info.y, hp, n, wp.albedo.xyz);
    }

    if hit.kind != 0u && dot(n, rd) > 0.0 {
        n = -n;
    }

    return Surface(mat, surf, blur, alb, n, spec_strength, shininess);
}

fn ray_color(origin: vec3<f32>, dir0: vec3<f32>, gloss_mode: u32) -> TraceResult {
    var accum = vec3<f32>(0.0, 0.0, 0.0);
    // Everything reached through the primary glossy lobe, accumulated alongside
    // rather than instead: each site adds its contribution to accum as before
    // and, masked, to gloss. color is then accum - gloss. One extra multiply-add
    // per site and no branch, which is the trade this kernel rewards.
    var refl_acc = vec3<f32>(0.0, 0.0, 0.0);
    var refr_acc = vec3<f32>(0.0, 0.0, 0.0);
    var refl_t = -1.0;
    var refr_t = -1.0;
    var refl_blur = 0.0;
    var refr_blur = 0.0;
    var refl_seen = false;
    var refr_seen = false;
    var lobe_n = vec3<f32>(0.0, 0.0, 0.0);
    var lobe_d = 0.0;
    var stack: array<RaySeg, MAX_SEGS>;
    stack[0] = RaySeg(origin, dir0, vec3<f32>(1.0, 1.0, 1.0), 0u, 0u);
    var sp = 1u;
    var primary = Hit(0.0, 0xffffffffu, 0u, HIT_NO_INSTANCE);
    var got_primary = false;
    var max_sp = 1u;
    var sh_out = ShadowAux(vec3<f32>(0.0, 0.0, 0.0), 0.0, vec3<f32>(0.0, 1.0, 0.0), 0.0, vec3<f32>(0.0, 0.0, 0.0), 0.0, vec3<f32>(0.0, 0.0, 0.0), 0.0,
        vec3<f32>(0.0, 0.0, 0.0), 0.0, vec3<f32>(0.0, 0.0, 0.0), 0.0, vec3<f32>(0.0, 0.0, 0.0), 0.0,
        vec3<f32>(0.0, 0.0, 0.0), -1.0, vec3<f32>(0.0, 0.0, 0.0), 0.0,
        vec3<f32>(0.0, 0.0, 0.0), -1.0, vec3<f32>(0.0, 0.0, 0.0), 0.0,
        vec3<f32>(0.0, 0.0, 0.0), 0.0);
    // sh_out.soft_applied / soft_applied_m stay 0 here; only shadow_soften_v sets them.

    loop {
        // Sampled before the pop, so this is the stack's high-water mark.
        // Guarded: normal frames must not pay for the diagnostic.
        if params.profile_enabled != 0u {
            max_sp = max(max_sp, sp);
        }
        if sp == 0u {
            break;
        }
        sp = sp - 1u;
        let seg = stack[sp];
        let ro = seg.ro;
        let tw = seg.w;
        let depth = seg.depth;
        let seg_tag = seg.tag;
        let rmask = select(0.0, 1.0, (seg_tag & RAYSEG_REFL) != 0u);
        let tmask = select(0.0, 1.0, (seg_tag & RAYSEG_REFR) != 0u);
        let inherit = seg_tag & RAYSEG_LOBE;
        var rd = seg.rd;
        var hit: Hit;
        if depth == 0u {
            let pre = nearest_hit(ro, rd);
            let occluded = pre.idx != 0xffffffffu;
            let rd_new = heat_shimmer_dir(ro, rd, params.time, pre.t, occluded);
            if dot(rd_new, rd) < 0.99999 {
                rd = rd_new;
                hit = nearest_hit(ro, rd);
            } else {
                hit = pre;
            }
        } else {
            hit = nearest_hit(ro, rd);
        }
        prof_inc(PROF_PATH_SEGS, 1u);
        if rmask > 0.0 && !refl_seen {
            refl_seen = true;
            refl_t = hit.t;
        }
        if tmask > 0.0 && !refr_seen {
            refr_seen = true;
            refr_t = hit.t;
        }
        if !got_primary {
            primary = hit;
            got_primary = true;
        }
        if seg_tag != RAYSEG_GHOST {
            let c_flame = tw * accumulate_flame(ro, rd, hit.t, params.time);
            accum = accum + c_flame;
            refl_acc = refl_acc + c_flame * rmask;
            refr_acc = refr_acc + c_flame * tmask;
        }
        if hit.idx == 0xffffffffu {
            prof_inc(PROF_SKY, 1u);
            if depth == 0u {
                prof_inc(PROF_PRI_SKY, 1u);
            }
            let c_sky = tw * sky(rd);
            accum = accum + c_sky;
            refl_acc = refl_acc + c_sky * rmask;
            refr_acc = refr_acc + c_sky * tmask;
            continue;
        }

        if hit.kind == 0u {
            prof_inc(PROF_HIT_PRIM, 1u);
            if hit.inst_idx != HIT_NO_INSTANCE {
                prof_inc(PROF_HIT_INST, 1u);
            }
            if depth == 0u {
                prof_inc(PROF_PRI_HIT_PRIM, 1u);
                if hit.inst_idx != HIT_NO_INSTANCE {
                    prof_inc(PROF_PRI_HIT_INST, 1u);
                }
            }
        } else if hit.kind == 1u {
            prof_inc(PROF_HIT_TERRAIN, 1u);
            if depth == 0u {
                prof_inc(PROF_PRI_HIT_TERRAIN, 1u);
            }
        } else if hit.kind == 2u {
            prof_inc(PROF_HIT_WATER, 1u);
            if depth == 0u {
                prof_inc(PROF_PRI_HIT_WATER, 1u);
            }
        }

        let hp = ro + rd * hit.t;
        let sf = surface_at(hit, ro, rd, hp);
        let mat = sf.mat;
        let surf = sf.surf;
        let blur = sf.blur;
        let alb = sf.alb;
        let n = sf.n;
        let spec_strength = sf.spec_strength;
        let shininess = sf.shininess;
        let surf_eps = SURFACE_EPSILON;

        if mat == MAT_EMIT {
            let c_emit = tw * alb;
            accum = accum + c_emit;
            refl_acc = refl_acc + c_emit * rmask;
            refr_acc = refr_acc + c_emit * tmask;
            continue;
        }

        let ep = hp + n * surf_eps;
        if seg_tag == RAYSEG_GHOST {
            let c_ghost = tw * shade_ghost(hp, alb, n, ep);
            accum = accum + c_ghost;
            refl_acc = refl_acc + c_ghost * rmask;
            refr_acc = refr_acc + c_ghost * tmask;
            continue;
        }

        let rough = surf.x;
        var ior = surf.y;
        if ior == 0.0 {
            ior = 1.5;
        }
        var transmit = surf.w;
        if transmit == 0.0 {
            transmit = 0.9;
        }
        // reflective = depth < max_bounce_depth && mirror enabled.
        var max_depth = params.max_bounce_depth;
        if max_depth == 0u {
            max_depth = 2u;
        }

        let reflective = depth < max_depth && params.mirror != 0u;
        // Throughput this segment carries, and the albedo-weighted brightness a
        // child spawned here could still contribute. Child lobes below
        // SEG_MIN_CONTRIB are dropped: see the constant in types.wesl.
        let twm = tw_peak(tw);
        let twm_alb = twm * tw_peak(alb);

        if (mat == MAT_MIRROR || mat == MAT_METAL) && reflective {
            prof_inc(PROF_MIRROR_BOUNCES, 1u);
            if twm_alb * 0.96 > SEG_MIN_CONTRIB && sp < MAX_SEGS {
                stack[sp] = RaySeg(ep, jitter_dir(reflect(rd, n), hp, rough), tw * alb * 0.96, depth + 1u, inherit);
                sp = sp + 1u;
            }
            continue;
        }

        if mat == MAT_GLASS && reflective {
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
            if tir {
                w_refl = 1.0;
            }

            let w_trans = twm_alb * (1.0 - reflectance);

            if thin {
                // Single-sheet glass: enter + exit refraction in one hop (ior
                // controls bend strength). Skips a separate back-face trace hit
                // and its Fresnel lobes, so it stays cheaper than thick glass.
                if !tir && w_refl < 0.98 && w_trans > SEG_MIN_CONTRIB && sp < MAX_SEGS {
                    let cost = sqrt(k);
                    let rd_in = normalize(rd * eta + n * (eta * cosi - cost));
                    let enter_hp = hp - n * surf_eps;
                    let t_span = thin_glass_exit_t(prims[hit.idx], enter_hp, rd_in, 0.0);
                    let exit_hp = enter_hp + rd_in * t_span;
                    let exit_n = -n;
                    let eta_out = ior;
                    let cosi_out = max(0.0, dot(rd_in, exit_n));
                    let k_out = 1.0 - eta_out * eta_out * (1.0 - cosi_out * cosi_out);
                    if k_out >= 0.0 {
                        let cost_out = sqrt(k_out);
                        var rd_out = jitter_dir(normalize(rd_in * eta_out + exit_n * (eta_out * cosi_out - cost_out)), exit_hp, rough * 0.35);
                        var ttag = inherit;
                        var t_skip = false;
                        if depth == 0u && blur.y > 0.0 && refr_chan_on() {
                            refr_blur = blur.y;
                            lobe_n = n;
                            lobe_d = hit.t;
                            if lobe_skip(gloss_mode, blur.y) {
                                refr_t = -2.0;
                                t_skip = true;
                            } else {
                                ttag = RAYSEG_REFR;
                            }
                        }
                        if !t_skip {
                            stack[sp] = RaySeg(exit_hp + exit_n * surf_eps, rd_out, tw * alb * (1.0 - reflectance), depth + 1u, ttag);
                            sp = sp + 1u;
                        }
                    }
                }
            } else {
                // Thick glass: refract at this interface; the back face is a
                // separate hit with its own Fresnel/refraction lobes.
                if !tir && w_refl < 0.98 && w_trans > SEG_MIN_CONTRIB && sp < MAX_SEGS {
                    let cost = sqrt(k);
                    var rr = jitter_dir(normalize(rd * eta + n * (eta * cosi - cost)), hp, rough * 0.35);
                    var ttag = inherit;
                    var t_skip = false;
                    if depth == 0u && blur.y > 0.0 && refr_chan_on() {
                        refr_blur = blur.y;
                        lobe_n = n;
                        lobe_d = hit.t;
                        if lobe_skip(gloss_mode, blur.y) {
                            refr_t = -2.0;
                            t_skip = true;
                        } else {
                            ttag = RAYSEG_REFR;
                        }
                    }
                    if !t_skip {
                        stack[sp] = RaySeg(hp - n * surf_eps, rr, tw * alb * (1.0 - reflectance), depth + 1u, ttag);
                        sp = sp + 1u;
                    }
                }
            }

            // Reflected lobe (the world in front of the pane). Its weight is the
            // Fresnel reflectance, so even a head-on clear window keeps a ~4%
            // floor. reflMin keeps deep, faint bounces bounded.
            var refl_min = 0.02;
            if depth > 0u {
                refl_min = 0.2;
            }
            if w_refl > refl_min && twm * w_refl > SEG_MIN_CONTRIB && sp < MAX_SEGS {
                var rd_refl = jitter_dir(reflect(rd, n), hp, rough);
                var rtag = inherit;
                var r_skip = false;
                if depth == 0u && blur.x > 0.0 && refl_chan_on() {
                    refl_blur = blur.x;
                    lobe_n = n;
                    lobe_d = hit.t;
                    if lobe_skip(gloss_mode, blur.x) {
                        refl_t = -2.0;
                        r_skip = true;
                    } else {
                        rtag = RAYSEG_REFL;
                    }
                }
                if !r_skip {
                    stack[sp] = RaySeg(ep, rd_refl, tw * w_refl, depth + 1u, rtag);
                    sp = sp + 1u;
                }

                // The thin-glass ghost stays inline and unfiltered for now: it
                // is a second-pane reflection at a different angle, so it is not
                // the lobe either channel is holding out, and at 0.42 of an
                // already-Fresnel weight it is faint enough to leave sharp.

                if thin && depth == 0u && params.thin_glass_ghost != 0u && sp < MAX_SEGS {
                    let slide = thin_glass_ghost_slide(n, rd);
                    let surf_shift = thin_glass_ghost_surface_shift(cosi);
                    let ang = min(surf_shift / max(hit.t, 0.35), THIN_GLASS_GHOST_ANG_MAX);
                    let rd_pane2 = normalize(rd + slide * ang);
                    let ghost_w = w_refl * THIN_GLASS_GHOST_WEIGHT;
                    var ghost_rd = reflect(rd_pane2, n);
                    if rough > 0.001 {
                        ghost_rd = jitter_dir(ghost_rd, hp, rough * 1.5);
                    }
                    if ghost_w > refl_min && twm * ghost_w > SEG_MIN_CONTRIB {
                        stack[sp] = RaySeg(ep, ghost_rd, tw * ghost_w, max_depth, RAYSEG_GHOST);
                        sp = sp + 1u;
                    }
                }
            }
            if spec_strength > 0.0 {
                let c_spec = tw * shade_specular(hp, n, ep, tw, rd, spec_strength, shininess);
                accum = accum + c_spec;
                refl_acc = refl_acc + c_spec * rmask;
                refr_acc = refr_acc + c_spec * tmask;
            }
            continue;
        }

        // Diffuse / checker, plus mirror/metal/glass falling through here at the
        // depth cap (or when reflections are disabled): shaded as diffuse.
        var lit = shade_diffuse(hp, alb, n, ep, tw, rd, spec_strength, shininess, depth == 0u);
        if depth == 0u {
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
        if depth == 0u {
            glossy_min = SEG_MIN_CONTRIB;
        }
        if refl > 0.0 && reflective && twm * refl > glossy_min && (mat == MAT_DIFFUSE || mat == MAT_CHECKER) && sp < MAX_SEGS {
            let c_lit_r = tw * lit * (1.0 - refl);
            accum = accum + c_lit_r;
            refl_acc = refl_acc + c_lit_r * rmask;
            refr_acc = refr_acc + c_lit_r * tmask;
            if depth == 0u {
                // A glossy surface keeps only (1 - refl) of its direct lighting;
                // the rest is the reflected lobe. The record has to match, or
                // the filter adds back light this pixel never had.
                sh_out.full = sh_out.full * (1.0 - refl);
                sh_out.full_m = sh_out.full_m * (1.0 - refl);
            }
            // The filter wants a *mirror* ray: the blur is applied afterwards,
            // from the roughness and the reflected distance, so perturbing the
            // ray here would only sample that blur once and call it noise.
            var grd = jitter_dir(reflect(rd, n), hp, rough);
            var gtag = inherit;
            // The ray keeps its jitter: `rough` is world-space roughness and
            // stays exactly what it was. reflect_blur only decides whether this
            // lobe is held out and widened afterwards.
            if depth == 0u && blur.x > 0.0 && refl_chan_on() {
                refl_blur = blur.x;
                lobe_n = n;
                lobe_d = hit.t;
                if lobe_skip(gloss_mode, blur.x) {
                    // -2 marks the lobe as owed rather than absent, which is
                    // what refl_fill looks for; -1 stays "no lobe at all".
                    refl_t = -2.0;
                    continue;
                }
                gtag = RAYSEG_REFL;
            }
            prof_inc(PROF_DIFFUSE_REFL, 1u);
            stack[sp] = RaySeg(ep, grd, tw * refl, depth + 1u, gtag);
            sp = sp + 1u;
            continue;
        }
        let c_lit = tw * lit;
        accum = accum + c_lit;
        refl_acc = refl_acc + c_lit * rmask;
        refr_acc = refr_acc + c_lit * tmask;
    }
    prof_max(PROF_MAX_SEGS, max_sp);
    return TraceResult(max(accum - refl_acc - refr_acc, vec3<f32>(0.0, 0.0, 0.0)),
        refl_acc, refl_t, refl_blur, refr_acc, refr_t, refr_blur, lobe_n, lobe_d, primary, sh_out);
}

// ---------------------------------------------------------------- grain ----
//
// COLOR_QUANT_GRAIN replaces the ordered dither with Monte-Carlo-shaped grain,
// matched to the experimental path tracer's own noise rather than guessed at.
// Measured by differencing a 2spp render against a 512spp reference of the same
// frame, in linear radiance:
//
//   sigma vs brightness   sigma ~ L^0.79. Absolute grain grows with brightness,
//                         but *relative* grain falls -- sigma/L is 2.5 down in
//                         the shadows against 1.3 in the lit areas. That is the
//                         whole reason path-traced shadows read as grainier, and
//                         a flat-amplitude noise misses it completely.
//   channel correlation   R-G 0.94, G-B 0.83, R-B 0.66. Mostly common-mode with
//                         a real chromatic component, blue the least tied.
//   autocorrelation       0.002 at lag 1, and nothing above 0.003 out to lag 16.
//                         The grain is *white*: one band, no structure at any
//                         coarser scale. Spatially banded noise would look less
//                         like the path tracer, not more.
//   tails                 p50 0.42 sigma, p90 1.19, p99 4.26, p99.9 7.48,
//                         against a gaussian's 0.67 / 1.64 / 2.58 / 3.29. Most
//                         pixels nearly clean and a few far out -- that sparse
//                         speckle is what the eye reads as "path traced", and a
//                         plain gaussian looks like video noise instead.
//
// The tails are reproduced with a two-component scale mixture, which fits all
// four percentiles at once: GRAIN_HEAVY_FRAC of pixels get GRAIN_HEAVY_SCALE
// times the width. Predicted from that mixture: 0.42 / 1.14 / 4.46 / 6.98.
const COLOR_QUANT_GRAIN: u32 = 4u;

// The knob. Grain is linear in it, and it is an equivalent sample count that
// sets the scale: the measured 2spp frame sits at about 1.08, and halving the
// strength is worth 4x the samples. 0.13 is roughly a 128spp look.
const GRAIN_STRENGTH: f32 = 0.07;

// Measured exponent of sigma against brightness. 0 would be flat-amplitude
// noise, 1 would be purely multiplicative.
const GRAIN_EXP: f32 = 0.79;

// The scale mixture behind the heavy tails.
const GRAIN_HEAVY_FRAC: f32 = 0.10;
const GRAIN_HEAVY_SCALE: f32 = 5.0;

// Per-channel independent share, the rest being common to all three. Set so the
// channel correlations land near the measured ones; blue is given more of its
// own because it measured least correlated.
const GRAIN_CHROMA_RG: f32 = 0.30;
const GRAIN_CHROMA_B: f32 = 0.60;

// Grain resampled every frame, as the path tracer does. Off by default: a still
// frame that keeps crawling reads as a broken screen rather than as grain, and
// this is a *dither* -- it belongs to the pixel grid, the way the bayer pattern
// it replaces does.
const GRAIN_ANIMATE: bool = false;

// Tried and rejected: blue noise for the static pattern.
//
// The reasoning was sound and the result was not. Frozen white noise keeps its
// low-frequency energy, which no longer averages across frames and can show as
// blotching, and blue noise is what dither masks use to avoid exactly that. But
// a texture would have cost a binding (31 of 32 are in use), so the generator
// had to be analytic -- Jimenez's interleaved gradient noise -- and an analytic
// function is *periodic*, which is the one thing this filter must not be.
//
// It shows up in the measurement before the eye finds it: autocorrelation along
// a scanline went -0.655 at lag 1, correctly negative for blue noise, but then
// oscillated -- +0.577, -0.356, -0.395, +0.630 at lags 2, 3, 6, 16. That is a
// repeating pattern, not noise with a shaped spectrum. On screen it is a fine
// diagonal crosshatch: the ordered-dither look this mode exists to replace.
//
// The heavy-tail selector makes it worse than it would otherwise be. Thresholding
// a smooth deterministic function picks a periodic *subset* of pixels, so the
// sparse speckle that carries the whole character lands on a lattice.
//
// White noise it is. At this amplitude the speckle is sparse enough that
// clumping never becomes visible, which is what the worry was about.

fn grain_pcg(v: u32) -> u32 {
    let state = v * 747796405u + 2891336453u;
    let word = ((state >> ((state >> 28u) + 4u)) ^ state) * 277803737u;
    return (word >> 22u) ^ word;
}

fn grain_u01(seed: u32) -> f32 {
    // Nudged off zero so the log in the Box-Muller transform stays finite.
    return max(f32(grain_pcg(seed)) * (1.0 / 4294967296.0), 1e-7);
}

// Two independent standard normals from one seed.
fn grain_gauss2(seed: u32) -> vec2<f32> {
    let u1 = grain_u01(seed);
    let u2 = grain_u01(seed ^ 0x9e3779b9u);
    let r = sqrt(-2.0 * log(u1));
    let a = 6.2831853 * u2;
    return vec2<f32>(r * cos(a), r * sin(a));
}

// Unit-variance triangular noise, from two uniforms. Used only for the chroma
// terms: they carry a small share of the variance and sit under a common-mode
// term that is a true gaussian, so the shape of the sum is set by that one. It
// saves a second Box-Muller -- a log, a sqrt and a sincos -- per pixel.
fn grain_tri(seed: u32) -> f32 {
    return (grain_u01(seed) + grain_u01(seed ^ 0x27d4eb2fu) - 1.0) * 2.4494897;
}

// pt_grain returns the grain to add to a pixel's *linear* radiance, before the
// tonemap. That is where Monte Carlo noise actually lives, and it is why a
// firefly here gets compressed by the highlight rolloff exactly as one from the
// path tracer would.
fn pt_grain(gid: vec2<u32>, col: vec3<f32>) -> vec3<f32> {
    var frame = 0u;
    if GRAIN_ANIMATE {
        frame = u32(params.time * 60.0);
    }
    let seed = (gid.y * 1973u + gid.x * 9277u) ^ (frame * 26699u);

    let u_a = grain_u01(seed);
    let u_b = grain_u01(seed ^ 0x9e3779b9u);
    let u_h = grain_u01(seed ^ 0xc2b2ae35u);

    let bm_r = sqrt(-2.0 * log(u_a));
    let bm_a = 6.2831853 * u_b;
    // `common` is a WGSL reserved word.
    let base_n = bm_r * cos(bm_a);
    let chroma_a = bm_r * sin(bm_a);

    // One draw decides whether this pixel is in the wide component, so a firefly
    // lands on all three channels at once, as a bright path would.
    var scale = 1.0;
    if u_h < GRAIN_HEAVY_FRAC {
        scale = GRAIN_HEAVY_SCALE;
    }
    // Unit variance across the mixture, so GRAIN_STRENGTH means the same thing
    // whatever the tail settings are.
    let norm = inverseSqrt(mix(1.0, GRAIN_HEAVY_SCALE * GRAIN_HEAVY_SCALE, GRAIN_HEAVY_FRAC));

    let cw_rg = sqrt(max(1.0 - GRAIN_CHROMA_RG * GRAIN_CHROMA_RG, 0.0));
    let cw_b = sqrt(max(1.0 - GRAIN_CHROMA_B * GRAIN_CHROMA_B, 0.0));
    let chroma_b = grain_tri(seed ^ 0x165667b1u);
    let chroma_c = grain_tri(seed ^ 0xd3a2646cu);
    let unit = vec3<f32>(
        cw_rg * base_n + GRAIN_CHROMA_RG * chroma_a,
        cw_rg * base_n + GRAIN_CHROMA_RG * chroma_b,
        cw_b * base_n + GRAIN_CHROMA_B * chroma_c,
    );

    let pix_lum = dot(col, vec3<f32>(0.2126, 0.7152, 0.0722));
    let sigma = GRAIN_STRENGTH * pow(max(pix_lum, 0.0), GRAIN_EXP);
    return unit * (sigma * scale * norm);
}

// quantize_rgb reduces dithered 0..255 RGB to a retro color depth. mode 1 is
// classic 15-bit (5-5-5); mode 2 is crush — 24 levels per channel with heavy dither.
fn quantize_rgb(rgb: vec3<f32>, mode: u32) -> vec3<f32> {
    if mode == 3u || mode == COLOR_QUANT_GRAIN {
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
    if quant_mode == COLOR_QUANT_GRAIN {
        // The grain is the dither: it already breaks up the quantization banding
        // the bayer pattern exists to hide.
        return 0.0;
    }
    let bayer_idx = (gid.y & 3u) * 4u + (gid.x & 3u);
    let n = f32(bayer[bayer_idx]) / 16.0 - 0.5;
    if quant_mode == 2u {
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
    if !inside {
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
// Returns vec2(tap, kind), kind being 0 for no edge, 1 for a silhouette and 2
// for same-surface curvature. The kind is only read by the profile counters.
fn aa_axis_tap(center: f32, lo: AANeighbor, hi: AANeighbor) -> vec2<f32> {
    let d_lo = abs(center - lo.luma);
    let d_hi = abs(center - hi.luma);

    var kind = 0.0;
    var edge = (lo.surface_edge && d_lo >= AA_GEOM_MIN_LEVELS) || (hi.surface_edge && d_hi >= AA_GEOM_MIN_LEVELS);
    if edge {
        kind = 1.0;
    }
    if !edge && lo.inside && hi.inside {
        edge = abs(lo.luma + hi.luma - 2.0 * center) >= AA_SHADE_MIN_CURVE;
        if edge {
            kind = 2.0;
        }
    }
    if !edge {
        return vec2<f32>(0.5, 0.0);
    }
    if d_lo >= d_hi {
        return vec2<f32>(0.25, kind);
    }
    return vec2<f32>(0.75, kind);
}

// hits_edge is true when two packed primary hits belong to different surfaces.
fn hits_edge_packed(a: u32, b: u32) -> bool {
    if a == b {
        return false;
    }
    if a == 0xffffffffu && b == 0xffffffffu {
        return false;
    }
    return true;
}

fn pack_hit(h: Hit) -> u32 {
    if h.idx == 0xffffffffu {
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
    if sx.x == 0.5 && sy.x == 0.5 {
        return vec4<f32>(0.5, 0.5, 0.0, 0.0);
    }
    prof_inc(PROF_AA_TASKS, 1u);
    let kinds = u32(sx.y) | u32(sy.y);
    if (kinds & 1u) != 0u {
        prof_inc(PROF_AA_GEOM, 1u);
    }
    if (kinds & 2u) != 0u {
        prof_inc(PROF_AA_SHADE, 1u);
    }
    return vec4<f32>(sx.x, sy.x, 0.0, 1.0);
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
// AA_TAPS is how many extra rays a flagged pixel traces. Shipping value is 1:
// the resolve pass is throughput-bound, so cost is very close to linear in this
// (measured 2.05x / 3.05x / 5.05x / 6.97x at 2 / 3 / 5 / 7 taps). Higher values
// exist to generate a reference for tuning the single tap against.
const AA_TAPS: u32 = 1u;

// AA_TAP_REACH is how far from the pixel centre the single aimed tap sits.
//
// With one extra ray the pixel is estimated from two samples, the centre plus
// this one, and the pair is *not* symmetric about the centre — so the estimator
// is biased toward whichever side the tap was aimed at, and the reach sets how
// strong that bias is. Tuned against a 31-tap reference; see
// docs/adaptive-aa.md.
const AA_TAP_REACH: f32 = 0.35;

// AA_TAP_WEIGHT is the tap's share of the blend, the centre taking the rest.
const AA_TAP_WEIGHT: f32 = 0.30;

// van der Corput radical inverse, base 2 — the second dimension of a Hammersley
// set. Only used to place reference taps.
fn aa_radical_inverse(bits_in: u32) -> f32 {
    var bits = bits_in;
    bits = (bits << 16u) | (bits >> 16u);
    bits = ((bits & 0x55555555u) << 1u) | ((bits & 0xAAAAAAAAu) >> 1u);
    bits = ((bits & 0x33333333u) << 2u) | ((bits & 0xCCCCCCCCu) >> 2u);
    bits = ((bits & 0x0F0F0F0Fu) << 4u) | ((bits & 0xF0F0F0F0u) >> 4u);
    bits = ((bits & 0x00FF00FFu) << 8u) | ((bits & 0xFF00FF00u) >> 8u);
    return f32(bits) * 2.3283064365386963e-10;
}

// aa_tap_offset places tap i inside the pixel. One tap uses the aimed offset the
// classifier chose, because a single sample averaged with the centre has to be
// put where it will actually disagree. Above one the aim is dropped for a
// Hammersley set, which covers the pixel evenly and so converges on the box
// average this is all trying to estimate.
fn aa_tap_offset(i: u32, aimed: vec2<f32>) -> vec2<f32> {
    if AA_TAPS == 1u {
        return aimed;
    }
    return vec2<f32>((f32(i) + 0.5) / f32(AA_TAPS), aa_radical_inverse(i));
}

// True when an AA tap's glossy lobe left from the same surface as the centre's,
// so the filtered lobe refl_blur_v parked in the centre's record describes the
// tap as well.
//
// This is lobe_compatible's test with the tap's sub-pixel offset taken into
// account, which is to say without it: the tap shares the centre's pixel, so its
// view ray is the centre's to well within REFL_DEPTH_REL and the depth
// prediction collapses to the centre's own depth. The constants are the filter's
// on purpose — if the filter would have let these two lobes mix, the tap may
// take the result of mixing them.
//
// Both lobes are held out of colour only when the surface asked for a blur
// (`blur.x > 0.0` at the hit), so on a mirror both sides of the branch add zero
// and this changes nothing.
fn aa_tap_same_lobe(aux: ShadowAux, res: TraceResult) -> bool {
    if aux.lobe_depth <= 0.0 || res.lobe_d <= 0.0 {
        return false;
    }
    if dot(aux.lobe_normal, res.lobe_n) < REFL_MIN_DOT {
        return false;
    }
    return abs(res.lobe_d - aux.lobe_depth) <= aux.lobe_depth * REFL_DEPTH_REL;
}

fn supersample_edge(gx: f32, gy: f32, center: vec3<f32>, tap: vec2<f32>, idx: u32) -> vec3<f32> {
    let ro = params.cam_pos.xyz;
    let aux = shadow_aux[idx];
    var sum = vec3<f32>(0.0, 0.0, 0.0);
    for (var i = 0u; i < AA_TAPS; i = i + 1u) {
        let off = aa_tap_offset(i, tap);
        let res = ray_color(ro, pixel_ray_dir(gx, gy, off.x, off.y), GLOSS_ALWAYS);
        var e = aa_tap_correct(res, aux);
        // Whose lobes the tap adds depends on where the tap landed.
        //
        // Handing it the centre's filtered lobes unconditionally is cheaper —
        // the tap need not trace them at all — and it is what the penumbra
        // correction does with its target visibility. It is not safe in
        // general. AA fires precisely where the tap lands on different
        // geometry, a glass pixel's transmitted lobe can be most of its
        // radiance, and pushing the centre's through a tap that is looking at
        // something else put 236 isolated pixels up to 190 levels out on
        // office-sunset's glass silhouettes.
        //
        // Always taking the tap's own lobes is not safe either, and a blurred
        // surface is where that shows. The centre's lobe has been through
        // refl_blur_h/v; the tap's is a single point sample of the same lobe,
        // and on a curved surface a sub-pixel shift swings the reflected ray
        // far enough to land somewhere much brighter. Blending that in at
        // AA_TAP_WEIGHT draws the unfiltered reflection back over the filtered
        // one at 30% — sharp streaks down the metal posts of the front office's
        // bookshelf (reflect_blur 0.3), where AA fires all along the highlight.
        //
        // So the tap is asked the question the filter already asks of a
        // neighbour: same reflecting surface or not. Same surface and the
        // filtered lobe describes the tap too; different geometry and only the
        // tap's own lobe does.
        if aa_tap_same_lobe(aux, res) {
            e = e + aux.refl_color + aux.refr_color;
        } else {
            e = e + res.refl + res.refr;
        }
        sum = sum + e;
    }
    if AA_TAPS == 1u {
        return center * (1.0 - AA_TAP_WEIGHT) + sum * AA_TAP_WEIGHT;
    }
    return (center + sum) / f32(AA_TAPS + 1u);
}

fn aa_tap_correct(res: TraceResult, aux: ShadowAux) -> vec3<f32> {
    var extra = res.color;
    if params.soft_shadows != 0u {
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
        if aux.soft_applied > 0.0 {
            let lit_bound = min(res.sh.full, aux.full);
            extra = max(extra + (res.sh.frac - aux.frac) * lit_bound, vec3<f32>(0.0, 0.0, 0.0));
        }
        if aux.soft_applied_m > 0.0 {
            let lit_bound_m = min(res.sh.full_m, aux.full_m);
            extra = max(extra + (res.sh.frac_m - aux.frac_m) * lit_bound_m, vec3<f32>(0.0, 0.0, 0.0));
        }
    }
    return extra;
}

fn write_pixel(gid: vec2<u32>, col: vec3<f32>) {
    var lit = col;
    if params.color_quant == COLOR_QUANT_GRAIN {
        lit = max(col + pt_grain(gid, col), vec3<f32>(0.0, 0.0, 0.0));
    }
    let toned = tonemap(lit);
    var rgb: vec3<f32>;
    if params.color_quant == 3u {
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

// REFL_SPREAD converts a surface's `rough` into the half-angle its reflection is
// scattered through. jitter_dir perturbed the direction by up to 0.5*rough per
// component, so this keeps the filtered result the same width as the jittered
// one it replaces; it is the knob for overall reflection softness.
const REFL_SPREAD: f32 = 0.5;

// The same for the transmitted lobe. Equal to REFL_SPREAD today because the
// difference between the two is already carried in the roughness each lobe
// records — glass jitters transmission at `rough * 0.35` — so this is the knob
// for making them differ *beyond* that, which is the point of the split.
const REFR_SPREAD: f32 = 0.5;

// Cap and tap count are a matched pair, on the same rule as the penumbra comb:
// two more than twice the cap, so the comb steps at most one pixel at the widest
// radius and has nothing to alias against. Raise one and raise the other.
const REFL_MAX_PX: f32 = 16.0;
const REFL_TAPS: u32 = 33u;

const REFL_MIN_DOT: f32 = 0.9;
const REFL_MIN_DENOM: f32 = 0.02;
const REFL_DEPTH_REL: f32 = 0.02;

// The blur a lobe wants, in pixels. `spread` converts the surface's `rough` into
// the half-angle the lobe is scattered through; the reflection and the
// refraction keep their own, so a pane can later be frosted in transmission
// while still reflecting sharply.
fn lobe_radius_px(spread: f32, rough: f32, t_lobe: f32, depth: f32) -> f32 {
    if rough <= 0.0 || t_lobe <= 0.0 || depth <= 0.0 {
        return 0.0;
    }
    // Pixels per unit of lateral offset at unit forward distance.
    let focal_px = 0.5 * f32(params.width) / max(params.aspect * params.fov_scale, 1e-6);
    // The lobe's image sits behind the surface, so it is (depth + t) away from
    // the camera however near the surface itself is.
    let apparent = depth + t_lobe;
    return min(spread * rough * t_lobe * focal_px / max(apparent, 1e-3), REFL_MAX_PX);
}

fn refl_radius_px(rough: f32, t_lobe: f32, depth: f32) -> f32 {
    return lobe_radius_px(REFL_SPREAD, rough, t_lobe, depth);
}

fn refr_radius_px(rough: f32, t_lobe: f32, depth: f32) -> f32 {
    return lobe_radius_px(REFR_SPREAD, rough, t_lobe, depth);
}

fn refl_clamp_x(x: i32) -> i32 {
    return clamp(x, 0, i32(params.width) - 1);
}

fn refl_clamp_y(y: i32) -> i32 {
    return clamp(y, 0, i32(params.height) - 1);
}

// The tap has to sit on the same *reflecting* surface: extended as a plane and
// predicted along the tap's own view ray, so the tolerance stays tight at
// grazing incidence where a floor's depth changes fast between neighbours.
//
// It deliberately does *not* ask what the two taps are reflecting. That gate was
// written first, on the theory that a reflected silhouette would otherwise
// smear, and it is exactly backwards: a rough surface gathers over a cone that
// straddles the silhouette, so mixing the bar with the sky behind it is the
// blur, not an artifact of it. With the gate in, the reflection came back
// pin-sharp — every tap across the only edges in the frame was rejected, which
// is the one place the filter had anything to do.
//
// The reflected distance still sizes the radius. It just does not veto.
fn lobe_compatible(me: ShadowAux, t: ShadowAux, tx: i32, ty: i32, ndv0: f32) -> bool {
    if t.lobe_depth <= 0.0 {
        return false;
    }
    if dot(me.lobe_normal, t.lobe_normal) < REFL_MIN_DOT {
        return false;
    }
    let v_tap = pixel_ray_dir(f32(tx), f32(ty), 0.5, 0.5);
    let denom = dot(me.lobe_normal, v_tap);
    if abs(denom) < REFL_MIN_DENOM {
        return false;
    }
    let d_pred = me.lobe_depth * ndv0 / denom;
    return abs(t.lobe_depth - d_pred) <= me.lobe_depth * REFL_DEPTH_REL;
}

fn refl_ndv0(me: ShadowAux, gid: vec2<u32>) -> f32 {
    return dot(me.lobe_normal, pixel_ray_dir(f32(gid.x), f32(gid.y), 0.5, 0.5));
}

@compute @workgroup_size(8, 8, 1)
fn main(@builtin(global_invocation_id) gid: vec3<u32>) {
    if gid.x >= params.width || gid.y >= params.height {
        return;
    }

    let ro = params.cam_pos.xyz;
    let gx = f32(gid.x);
    let gy = f32(gid.y);
    let g2 = vec2<u32>(gid.xy);

    prof_inc(PROF_PIXELS, 1u);
    // One pixel of each 2x2 block leads: it traces the glossy lobe, and the
    // other three fill from it wherever the blur is wide enough to hide the
    // difference. refl_half_ok inside ray_color makes that call per surface,
    // because only it knows the roughness at the hit.
    let lead = (gid.x & 1u) == 0u && (gid.y & 1u) == 0u;
    let tr = ray_color(ro, pixel_ray_dir(gx, gy, 0.5, 0.5), select(GLOSS_IF_SHARP, GLOSS_ALWAYS, lead));

    prof_inc(PROF_BVH_STEPS, bvh_steps_acc);
    prof_inc(PROF_PRIM_TESTS, prim_tests_acc);

    let idx = pixel_index(gid.x, gid.y);
    let soft = params.soft_shadows != 0u;
    let filt = (params.refl_filter & 1u) != 0u;

    // With the filter on the glossy lobe is left out of the pixel until
    // refl_blur_v has widened it and adds it back; with it off the lobe was
    // never held out in the first place and folds straight in here.
    var col = tr.color;
    var aux = tr.sh;
    if filt {
        // Distances are passed through, not clamped: -1 is "no lobe here", -2 is
        // "lobe owed to refl_fill", and a real distance is a real distance. Only
        // a pixel with no reflecting surface under it is forced to -1.
        aux.refl_color = tr.refl;
        aux.refl_color_h = tr.refl;
        aux.refl_radius_px = refl_radius_px(tr.refl_blur, max(tr.refl_t, 0.0), tr.lobe_d);
        aux.refl_t = select(-1.0, tr.refl_t, tr.lobe_d > 0.0);
        aux.refr_color = tr.refr;
        aux.refr_color_h = tr.refr;
        aux.refr_radius_px = refr_radius_px(tr.refr_blur, max(tr.refr_t, 0.0), tr.lobe_d);
        aux.refr_t = select(-1.0, tr.refr_t, tr.lobe_d > 0.0);
        aux.lobe_normal = tr.lobe_n;
        aux.lobe_depth = tr.lobe_d;
    } else {
        col = col + tr.refl + tr.refr;
    }

    // The soften pass needs the linear colour to add light back into, so the
    // scratch is written whenever it runs even if adaptive AA is off. It carries
    // its own normal and depth for surface continuity and does not read aa_hits.
    if params.adaptive_aa != 0u || soft || filt {
        hdr_pixels[idx] = vec4<f32>(col, display_luma(col));
    }
    if params.adaptive_aa != 0u {
        aa_hits[idx] = pack_hit(tr.primary);
    }
    if soft || filt {
        shadow_aux[idx] = aux;
    }
    write_pixel(g2, col);
}

// refl_fill hands a skipped pixel the lobe its block's lead traced. Only pixels
// marked -2 are touched, so a pixel with no glossy surface under it stays empty.
//
// The lead can be on the far side of a silhouette, in which case the four
// neighbouring blocks' leads are tried before giving up. Giving up leaves the
// lobe at zero, which reads as a missing reflection on a one-pixel rim; that is
// the cost of the scheme and it is why the radius gate exists — at a wide blur
// the rim is inside the gather of every pixel around it anyway.
@compute @workgroup_size(8, 8, 1)
fn refl_fill(@builtin(global_invocation_id) gid: vec3<u32>) {
    if gid.x >= params.width || gid.y >= params.height {
        return;
    }
    let idx = pixel_index(gid.x, gid.y);
    let me = shadow_aux[idx];
    let want_refl = me.refl_t < -1.5;
    let want_refr = me.refr_t < -1.5;
    if !want_refl && !want_refr {
        return;
    }
    let ix = i32(gid.x);
    let iy = i32(gid.y);
    let ndv0 = refl_ndv0(me, gid.xy);
    let bx = ix & ~1;
    let by = iy & ~1;
    var cand = array<vec2<i32>, 5>(
        vec2<i32>(bx, by),
        vec2<i32>(bx - 2, by),
        vec2<i32>(bx + 2, by),
        vec2<i32>(bx, by - 2),
        vec2<i32>(bx, by + 2),
    );
    var got_refl = !want_refl;
    var got_refr = !want_refr;
    for (var k = 0u; k < 5u; k = k + 1u) {
        if got_refl && got_refr {
            break;
        }
        let cx = refl_clamp_x(cand[k].x);
        let cy = refl_clamp_y(cand[k].y);
        let t = shadow_aux[pixel_index(u32(cx), u32(cy))];
        if !lobe_compatible(me, t, cx, cy, ndv0) {
            continue;
        }
        if !got_refl && t.refl_t >= 0.0 {
            shadow_aux[idx].refl_color = t.refl_color;
            shadow_aux[idx].refl_color_h = t.refl_color;
            shadow_aux[idx].refl_t = t.refl_t;
            shadow_aux[idx].refl_radius_px = t.refl_radius_px;
            got_refl = true;
        }
        if !got_refr && t.refr_t >= 0.0 {
            shadow_aux[idx].refr_color = t.refr_color;
            shadow_aux[idx].refr_color_h = t.refr_color;
            shadow_aux[idx].refr_t = t.refr_t;
            shadow_aux[idx].refr_radius_px = t.refr_radius_px;
            got_refr = true;
        }
    }
    // Nothing usable nearby: leave it empty rather than borrow across a
    // silhouette, and mark it absent so the blur skips it.
    if !got_refl {
        shadow_aux[idx].refl_t = -1.0;
    }
    if !got_refr {
        shadow_aux[idx].refr_t = -1.0;
    }
}

// refl_blur_h / refl_blur_v widen the glossy lobe into the blur its roughness
// implies. Separable, for the same reason the penumbra filter is: 2N taps
// instead of N^2 for the same reach.
@compute @workgroup_size(8, 8, 1)
fn refl_blur_h(@builtin(global_invocation_id) gid: vec3<u32>) {
    if gid.x >= params.width || gid.y >= params.height {
        return;
    }
    let idx = pixel_index(gid.x, gid.y);
    let me = shadow_aux[idx];
    let do_refl = me.refl_t >= 0.0 && me.refl_radius_px >= 1.0;
    let do_refr = me.refr_t >= 0.0 && me.refr_radius_px >= 1.0;
    if !do_refl {
        shadow_aux[idx].refl_color_h = me.refl_color;
    }
    if !do_refr {
        shadow_aux[idx].refr_color_h = me.refr_color;
    }
    if !do_refl && !do_refr {
        return;
    }
    let ix = i32(gid.x);
    let iy = i32(gid.y);
    let ndv0 = refl_ndv0(me, gid.xy);
    // Both channels walk the same comb, each weighting it against its own
    // radius, so the neighbour records are read once rather than twice.
    let r_max = max(me.refl_radius_px, me.refr_radius_px);
    var acc_r = me.refl_color;
    var wsum_r = 1.0;
    var acc_t = me.refr_color;
    var wsum_t = 1.0;
    for (var i = 0u; i < REFL_TAPS; i = i + 1u) {
        let o = (f32(i) / f32(REFL_TAPS - 1u) * 2.0 - 1.0) * r_max;
        let tx = refl_clamp_x(ix + i32(round(o)));
        let t = shadow_aux[pixel_index(u32(tx), u32(iy))];
        if !lobe_compatible(me, t, tx, iy, ndv0) {
            continue;
        }
        let d = abs(o);
        if do_refl && t.refl_t >= 0.0 && d <= me.refl_radius_px {
            let u = d / me.refl_radius_px;
            let w = 1.0 - u * u;
            acc_r = acc_r + t.refl_color * w;
            wsum_r = wsum_r + w;
        }
        if do_refr && t.refr_t >= 0.0 && d <= me.refr_radius_px {
            let u = d / me.refr_radius_px;
            let w = 1.0 - u * u;
            acc_t = acc_t + t.refr_color * w;
            wsum_t = wsum_t + w;
        }
    }
    if do_refl {
        shadow_aux[idx].refl_color_h = acc_r / max(wsum_r, 1e-4);
    }
    if do_refr {
        shadow_aux[idx].refr_color_h = acc_t / max(wsum_t, 1e-4);
    }
}

@compute @workgroup_size(8, 8, 1)
fn refl_blur_v(@builtin(global_invocation_id) gid: vec3<u32>) {
    if gid.x >= params.width || gid.y >= params.height {
        return;
    }
    let idx = pixel_index(gid.x, gid.y);
    let me = shadow_aux[idx];
    let do_refl = me.refl_t >= 0.0 && me.refl_radius_px >= 1.0;
    let do_refr = me.refr_t >= 0.0 && me.refr_radius_px >= 1.0;
    var lobe_r = me.refl_color_h;
    var lobe_t = me.refr_color_h;
    if do_refl || do_refr {
        let ix = i32(gid.x);
        let iy = i32(gid.y);
        let ndv0 = refl_ndv0(me, gid.xy);
        let r_max = max(me.refl_radius_px, me.refr_radius_px);
        var acc_r = me.refl_color_h;
        var wsum_r = 1.0;
        var acc_t = me.refr_color_h;
        var wsum_t = 1.0;
        for (var i = 0u; i < REFL_TAPS; i = i + 1u) {
            let o = (f32(i) / f32(REFL_TAPS - 1u) * 2.0 - 1.0) * r_max;
            let ty = refl_clamp_y(iy + i32(round(o)));
            let t = shadow_aux[pixel_index(u32(ix), u32(ty))];
            if !lobe_compatible(me, t, ix, ty, ndv0) {
                continue;
            }
            let d = abs(o);
            if do_refl && t.refl_t >= 0.0 && d <= me.refl_radius_px {
                let u = d / me.refl_radius_px;
                let w = 1.0 - u * u;
                acc_r = acc_r + t.refl_color_h * w;
                wsum_r = wsum_r + w;
            }
            if do_refr && t.refr_t >= 0.0 && d <= me.refr_radius_px {
                let u = d / me.refr_radius_px;
                let w = 1.0 - u * u;
                acc_t = acc_t + t.refr_color_h * w;
                wsum_t = wsum_t + w;
            }
        }
        if do_refl {
            lobe_r = acc_r / max(wsum_r, 1e-4);
        }
        if do_refr {
            lobe_t = acc_t / max(wsum_t, 1e-4);
        }
    }
    // Parked back in colour so aa_resolve can hand a tap the neighbourhood's
    // conclusion instead of the tap's own unfiltered lobes. Safe to overwrite:
    // every invocation reads neighbours' *_color_h, never their *_color.
    shadow_aux[idx].refl_color = lobe_r;
    shadow_aux[idx].refr_color = lobe_t;
    let col = max(hdr_pixels[idx].xyz + lobe_r + lobe_t, vec3<f32>(0.0, 0.0, 0.0));
    hdr_pixels[idx] = vec4<f32>(col, display_luma(col));
    write_pixel(vec2<u32>(gid.xy), col);
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

// The widest radius a shadow will hand to a *lit* neighbour, as opposed to the
// widest it will gather at itself.
//
// The dilation's taper is the thing that has to fit: a tap contributes its full
// width out to `tp` and tapers to nothing at tp*(1+FEATHER), but the tap grid
// only reaches SHADOW_SOFTEN_MAX_PX. Above tp = MAX/(1+FEATHER) the taper lands
// off the end of the grid, so every tap in range sits inside the plateau and the
// borrowed radius is full width out to exactly MAX_PX and zero one pixel
// further. The feather exists to stop precisely that cliff; it had simply stopped
// applying to the widest shadows, where the drop is nearly the whole cap.
//
// What it drew: a soft-edged blob with a visible rim, 30 px around any patch
// whose width saturates the cap. The uplight on the office-sunset wall is the
// case that found it — the shade sits inside the lamp's authored 2 m emitter
// radius, so `gap` clamps to that radius, `pen` becomes the entire throw
// distance (1500-3000 px), and the compressed width pins at 29.7 across a patch
// of wall. Dilation then painted a 30 px halo of cap-width gather around the
// patch, ending in a cliff, and the gather pulled the shadow arc up into the lit
// wall inside it. That is a closed contour drawn on a flat wall by nothing in
// the scene, which is exactly the class of artifact the radius field is supposed
// not to have.
//
// Capping the *offer* rather than the taper keeps the fix in one place: a pixel
// still gathers at its own full width, and every borrowed width now has room for
// its whole taper inside the grid. Below the cap nothing changes at all, so the
// shadows that already looked right are untouched.
//
// A penumbra this wide has no edge the filter can draw in any case — 3000 px
// against a 30 px reach — so the 15 px it still hands out is already a token.
// The requirement is only that it not invent a boundary of its own.
//
// Written out rather than derived, because naga will not fold a const built from
// two other consts across the linked file. It is
// SHADOW_SOFTEN_MAX_PX / (1 + SHADOW_DILATE_FEATHER) = 30 / 2, and moves with
// either of them.
const SHADOW_DILATE_OFFER_PX: f32 = 15.0;

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
    if dot(t.normal, me.normal) < SHADOW_SOFTEN_MIN_DOT {
        return false;
    }
    let v_tap = pixel_ray_dir(f32(tx), f32(ty), 0.5, 0.5);
    let denom = dot(me.normal, v_tap);
    if abs(denom) < SHADOW_SOFTEN_MIN_DENOM {
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
    if pen <= SHADOW_SOFTEN_KNEE_PX {
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
// Field reuse is per channel: pen_px / pen_m are dead once radius_h has read
// them, so radius_v parks the 2D maxima there; r_h / r_h_m are dead once
// radius_v has read them, so soften_h parks the smoothed radii there for
// soften_v. Each pass only ever writes a field no *other* invocation of that
// same pass reads.

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
    if gid.x >= params.width || gid.y >= params.height {
        return;
    }
    let idx = pixel_index(gid.x, gid.y);
    let me = shadow_aux[idx];
    let ix = i32(gid.x);
    let iy = i32(gid.y);
    let ndv0 = shadow_ndv0(me, gid.xy);

    // A pixel with nothing blocked has an exactly zero fraction — no epsilon,
    // and nothing that depends on how bright the scene happens to be.
    // Both channels share one tap grid (offsets scale with MAX_PX, not with
    // either width), so one neighbour load serves both. A channel that is
    // already shadowed keeps its own width and skips the max; one whose
    // geometric width is sub-pixel still writes that width and does not
    // start a second loop.
    // Dilation is only for pixels that still have unoccluded light in that
    // channel to lose. A channel with full = 0 cannot apply a correction, and
    // searching for a neighbour's width would only spend bandwidth.
    let lit = tw_peak(me.frac) <= 0.0 && tw_peak(me.full) > 0.0;
    let lit_m = tw_peak(me.frac_m) <= 0.0 && tw_peak(me.full_m) > 0.0;
    var r = shadow_radius(me.pen_px);
    var r_m = shadow_radius(me.pen_m);
    for (var i = 0u; i < SHADOW_RADIUS_TAPS && (lit || lit_m); i = i + 1u) {
        let o = (f32(i) / f32(SHADOW_RADIUS_TAPS - 1u) * 2.0 - 1.0) * SHADOW_SOFTEN_MAX_PX;
        let tx = shadow_clamp_x(ix + i32(round(o)));
        let t = shadow_tap(tx, iy);
        if !shadow_same_surface(me, t, tx, iy, ndv0) {
            continue;
        }
        // Compressed *before* the reach test. pen_px is stored raw and can
        // run to hundreds of pixels when an occluder sits near the light, so
        // an untamed value reaches everywhere and wins the maximum over the
        // whole row. Compressing first lets distance actually decide.
        if lit {
            let tp = min(shadow_radius(t.pen_px), SHADOW_DILATE_OFFER_PX);
            r = max(r, tp * shadow_dilate_weight(abs(o), tp));
        }
        if lit_m {
            let tp_m = min(shadow_radius(t.pen_m), SHADOW_DILATE_OFFER_PX);
            r_m = max(r_m, tp_m * shadow_dilate_weight(abs(o), tp_m));
        }
    }
    shadow_aux[idx].r_h = min(r, SHADOW_SOFTEN_MAX_PX);
    shadow_aux[idx].r_h_m = min(r_m, SHADOW_SOFTEN_MAX_PX);
}

// radius_v completes the separable maximum down each column and parks the result
// in pen_px / pen_m, which nothing reads from here on.
@compute @workgroup_size(8, 8, 1)
fn shadow_radius_v(@builtin(global_invocation_id) gid: vec3<u32>) {
    if gid.x >= params.width || gid.y >= params.height {
        return;
    }
    let idx = pixel_index(gid.x, gid.y);
    let me = shadow_aux[idx];
    let ix = i32(gid.x);
    let iy = i32(gid.y);
    let ndv0 = shadow_ndv0(me, gid.xy);

    // Dilation is only for pixels that still have unoccluded light in that
    // channel to lose. A channel with full = 0 cannot apply a correction, and
    // searching for a neighbour's width would only spend bandwidth.
    let lit = tw_peak(me.frac) <= 0.0 && tw_peak(me.full) > 0.0;
    let lit_m = tw_peak(me.frac_m) <= 0.0 && tw_peak(me.full_m) > 0.0;
    var r = me.r_h;
    var r_m = me.r_h_m;
    for (var i = 0u; i < SHADOW_RADIUS_TAPS && (lit || lit_m); i = i + 1u) {
        let o = (f32(i) / f32(SHADOW_RADIUS_TAPS - 1u) * 2.0 - 1.0) * SHADOW_SOFTEN_MAX_PX;
        let ty = shadow_clamp_y(iy + i32(round(o)));
        let t = shadow_tap(ix, ty);
        if !shadow_same_surface(me, t, ix, ty, ndv0) {
            continue;
        }
        if lit {
            let tp = min(t.r_h, SHADOW_DILATE_OFFER_PX);
            r = max(r, tp * shadow_dilate_weight(abs(o), tp));
        }
        if lit_m {
            let tp_m = min(t.r_h_m, SHADOW_DILATE_OFFER_PX);
            r_m = max(r_m, tp_m * shadow_dilate_weight(abs(o), tp_m));
        }
    }
    shadow_aux[idx].pen_px = min(r, SHADOW_SOFTEN_MAX_PX);
    shadow_aux[idx].pen_m = min(r_m, SHADOW_SOFTEN_MAX_PX);
}

// shadow_radius_blur averages the dilated radius over a small reach. Same-surface
// only, so the field does not bleed across a silhouette and reintroduce the leak
// the plane test exists to prevent.
//
// Reach is proportional to this channel's own width, so the two channels cannot
// share tap positions the way radius_h/v can. Nine taps; cheap next to the
// 61-tap fraction gather.
fn shadow_radius_blur(me: ShadowAux, ix: i32, iy: i32, ndv0: f32, own: f32, vertical: bool, muted: bool) -> f32 {
    let reach = clamp(own * SHADOW_RADIUS_SMOOTH_FRAC, 1.0, SHADOW_RADIUS_SMOOTH_MAX_PX);
    var acc = own;
    var wsum = 1.0;
    for (var i = 0u; i < SHADOW_RADIUS_SMOOTH_TAPS; i = i + 1u) {
        let o = (f32(i) / f32(SHADOW_RADIUS_SMOOTH_TAPS - 1u) * 2.0 - 1.0) * reach;
        var tx = ix;
        var ty = iy;
        if vertical {
            ty = shadow_clamp_y(iy + i32(round(o)));
        } else {
            tx = shadow_clamp_x(ix + i32(round(o)));
        }
        let t = shadow_tap(tx, ty);
        if !shadow_same_surface(me, t, tx, ty, ndv0) {
            continue;
        }
        let u = abs(o) / reach;
        let w = 1.0 - u * u;
        var tv = t.pen_px;
        if muted {
            tv = t.pen_m;
        }
        if vertical {
            tv = t.r_h;
            if muted {
                tv = t.r_h_m;
            }
        }
        acc = acc + tv * w;
        wsum = wsum + w;
    }
    return acc / max(wsum, PENUMBRA_FILTER_EPS);
}

@compute @workgroup_size(8, 8, 1)
fn shadow_soften_h(@builtin(global_invocation_id) gid: vec3<u32>) {
    if gid.x >= params.width || gid.y >= params.height {
        return;
    }
    let idx = pixel_index(gid.x, gid.y);
    let me = shadow_aux[idx];
    let ix = i32(gid.x);
    let iy = i32(gid.y);
    let ndv0 = shadow_ndv0(me, gid.xy);

    // Smooth the dilated radius across the row, and park it in r_h for soften_v.
    // Each channel keeps its own kernel: tap offsets scale with that channel's
    // radius, so sharing a gather would undersample the narrower shadow.
    // A sub-pixel channel copies frac through and does not start the 61-tap loop.
    let r = shadow_radius_blur(me, ix, iy, ndv0, me.pen_px, false, false);
    let r_m = shadow_radius_blur(me, ix, iy, ndv0, me.pen_m, false, true);
    shadow_aux[idx].r_h = r;
    shadow_aux[idx].r_h_m = r_m;
    if r < 1.0 {
        shadow_aux[idx].frac_h = me.frac;
    } else {
        shadow_aux[idx].frac_h = shadow_soften_gather_h(me, ix, iy, ndv0, r, false);
    }
    if r_m < 1.0 {
        shadow_aux[idx].frac_h_m = me.frac_m;
    } else {
        shadow_aux[idx].frac_h_m = shadow_soften_gather_h(me, ix, iy, ndv0, r_m, true);
    }
}

fn shadow_soften_gather_h(me: ShadowAux, ix: i32, iy: i32, ndv0: f32, r: f32, muted: bool) -> vec3<f32> {
    // Every same-surface neighbour counts, lit ones included. Giving each tap a
    // reach of its own silently drops lit taps (their width is zero), and a lit
    // pixel then averages nothing but shadow, whose 1/(1+k) steps draw bands
    // parallel to every shadow.
    var acc = me.frac;
    if muted {
        acc = me.frac_m;
    }
    var wsum = 1.0;
    for (var i = 0u; i < SHADOW_SOFTEN_TAPS; i = i + 1u) {
        let o = (f32(i) / f32(SHADOW_SOFTEN_TAPS - 1u) * 2.0 - 1.0) * r;
        let tx = shadow_clamp_x(ix + i32(round(o)));
        let t = shadow_tap(tx, iy);
        if !shadow_same_surface(me, t, tx, iy, ndv0) {
            continue;
        }
        let u = abs(o) / r;
        let w = 1.0 - u * u;
        var tf = t.frac;
        if muted {
            tf = t.frac_m;
        }
        acc = acc + tf * w;
        wsum = wsum + w;
    }
    return acc / max(wsum, PENUMBRA_FILTER_EPS);
}

@compute @workgroup_size(8, 8, 1)
fn shadow_soften_v(@builtin(global_invocation_id) gid: vec3<u32>) {
    if gid.x >= params.width || gid.y >= params.height {
        return;
    }
    let idx = pixel_index(gid.x, gid.y);
    let me = shadow_aux[idx];
    // Cleared on every path that leaves the pixel alone, so supersample_edge
    // never applies a correction to a pixel this pass did not touch.
    shadow_aux[idx].soft_applied = 0.0;
    shadow_aux[idx].soft_applied_m = 0.0;
    // depth is zero only where the primary ray found no diffuse surface — sky,
    // or a mirror seen head-on. There is no shadow there to widen.
    if me.depth <= 0.0 {
        return;
    }
    let ix = i32(gid.x);
    let iy = i32(gid.y);
    let ndv0 = shadow_ndv0(me, gid.xy);
    let r = shadow_radius_blur(me, ix, iy, ndv0, me.r_h, true, false);
    let r_m = shadow_radius_blur(me, ix, iy, ndv0, me.r_h_m, true, true);
    var delta = vec3<f32>(0.0, 0.0, 0.0);
    if r >= SHADOW_SOFTEN_FADE_LO {
        delta = delta + shadow_soften_apply_v(idx, me, ix, iy, ndv0, r, false);
    }
    if r_m >= SHADOW_SOFTEN_FADE_LO {
        delta = delta + shadow_soften_apply_v(idx, me, ix, iy, ndv0, r_m, true);
    }
    if shadow_aux[idx].soft_applied <= 0.0 && shadow_aux[idx].soft_applied_m <= 0.0 {
        return;
    }
    let col = max(hdr_pixels[idx].xyz + delta, vec3<f32>(0.0, 0.0, 0.0));
    hdr_pixels[idx] = vec4<f32>(col, display_luma(col));
    write_pixel(vec2<u32>(gid.xy), col);
}

fn shadow_soften_apply_v(idx: u32, me: ShadowAux, ix: i32, iy: i32, ndv0: f32, r: f32, muted: bool) -> vec3<f32> {
    var own_frac = me.frac;
    var own_frac_h = me.frac_h;
    var own_full = me.full;
    if muted {
        own_frac = me.frac_m;
        own_frac_h = me.frac_h_m;
        own_full = me.full_m;
    }
    var acc = own_frac_h;
    var wsum = 1.0;
    for (var i = 0u; i < SHADOW_SOFTEN_TAPS; i = i + 1u) {
        let o = (f32(i) / f32(SHADOW_SOFTEN_TAPS - 1u) * 2.0 - 1.0) * r;
        let ty = shadow_clamp_y(iy + i32(round(o)));
        let t = shadow_tap(ix, ty);
        if !shadow_same_surface(me, t, ix, ty, ndv0) {
            continue;
        }
        let u = abs(o) / r;
        let w = 1.0 - u * u;
        var tf = t.frac_h;
        if muted {
            tf = t.frac_h_m;
        }
        acc = acc + tf * w;
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
    // different sub-pixel. Identical to (own_frac - frac_soft) * own_full * fade.
    let frac_target = mix(own_frac, frac_soft, fade);
    if muted {
        shadow_aux[idx].frac_m = frac_target;
        shadow_aux[idx].soft_applied_m = 1.0;
    } else {
        shadow_aux[idx].frac = frac_target;
        shadow_aux[idx].soft_applied = 1.0;
    }
    return (own_frac - frac_target) * own_full;
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

// The two packed bits per axis are a *direction* — low, centre, high — not a
// literal offset, so the reach can be tuned without touching the packing.
fn aa_task_tap(task: u32) -> vec2<f32> {
    let sxi = (task >> 24u) & 3u;
    let syi = (task >> 26u) & 3u;
    let dx = f32(sxi) - 1.0;
    let dy = f32(syi) - 1.0;
    return vec2<f32>(0.5, 0.5) + AA_TAP_REACH * vec2<f32>(dx, dy);
}

// aa_classify is AA pass 2 of 3: one thread per pixel, but it only reads the
// neighbor fingerprints and luminances and appends the pixels that need work.
// Splitting classification from supersampling is what makes the AA affordable.
// Edge pixels are scattered along silhouettes, so when a single pass did both,
// almost every 64-thread workgroup contained at least one edge pixel and the
// whole workgroup was billed for a full extra ray tree while most of its lanes
// sat idle. Compacting first means aa_resolve launches only full workgroups of
// genuine work.
// Workgroup-local compaction state. Each 8x8 tile counts its own edge pixels,
// claims one contiguous block of the task list, and fills it.
var<workgroup> aa_wg_count: atomic<u32>;
var<workgroup> aa_wg_base: u32;

@compute @workgroup_size(8, 8, 1)
fn aa_classify(@builtin(global_invocation_id) gid: vec3<u32>, @builtin(local_invocation_index) lidx: u32) {
    if lidx == 0u {
        atomicStore(&aa_wg_count, 0u);
    }
    workgroupBarrier();

    // No early return: every lane has to reach the barriers below, so an
    // out-of-range lane participates as simply "not flagged".
    var flagged = false;
    var task = 0u;
    if gid.x < params.width && gid.y < params.height {
        let idx = pixel_index(gid.x, gid.y);
        let edge = analyze_edge(vec2<u32>(gid.xy), aa_hits[idx], hdr_pixels[idx].w);
        if edge.w >= 0.5 {
            flagged = true;
            task = aa_pack_task(gid.x, gid.y, edge.xy);
        }
    }

    // One atomic per tile instead of one per edge pixel. The point is not the
    // reduced contention, it is that a tile's tasks come out *contiguous*: the
    // resolve pass then fills each of its workgroups from a few adjacent tiles
    // rather than from 64 unrelated points on the screen, and 64 neighbouring
    // camera rays are near-parallel where 64 scattered ones are not.
    var slot = 0u;
    if flagged {
        slot = atomicAdd(&aa_wg_count, 1u);
    }
    workgroupBarrier();
    if lidx == 0u {
        aa_wg_base = atomicAdd(&aa_dispatch[3], atomicLoad(&aa_wg_count));
    }
    workgroupBarrier();

    if !flagged {
        return;
    }
    let g = aa_wg_base + slot;
    if g >= arrayLength(&aa_list) {
        return;
    }
    aa_list[g] = task;
    atomicMax(&aa_dispatch[0], (g + AA_RESOLVE_WG) / AA_RESOLVE_WG);
}

// aa_resolve is AA pass 3 of 3: supersample the compacted task list. Dispatched
// indirectly from aa_dispatch, so its thread count tracks the number of edge
// pixels rather than the screen size.
@compute @workgroup_size(64, 1, 1)
fn aa_resolve(@builtin(global_invocation_id) gid: vec3<u32>) {
    if gid.x >= atomicLoad(&aa_dispatch[3]) {
        return;
    }
    let task = aa_list[gid.x];
    let px = aa_task_pixel(task);
    let idx = pixel_index(px.x, px.y);
    let col = supersample_edge(f32(px.x), f32(px.y), hdr_pixels[idx].xyz, aa_task_tap(task), idx);
    write_pixel(px, col);
}
