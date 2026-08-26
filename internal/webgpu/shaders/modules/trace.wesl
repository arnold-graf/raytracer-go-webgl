// trace.wesl — Megakernel: bounce loop and pixel output.
// The heart of the renderer: ray_color() walks a small stack of reflection and
// refraction segments, dispatches by material, then main() tonemaps, dithers,
// quantizes to retro color depth, and writes RGBA8 pixels.

import package::types::{HIT_NO_INSTANCE, Hit, MAT_CHECKER, MAT_DIFFUSE, MAT_EMIT, MAT_GLASS, MAT_METAL, MAT_MIRROR, PRIM_BOX, PRIM_CONE, PRIM_FLAG_GLASS_THIN, PRIM_FLAG_TRANSFORMED, SEG_MIN_CONTRIB, SURFACE_EPSILON, TEX_TILES, aa_hits, hdr_pixels, params, pixels, prims, waters};
import package::profile::{PROF_BVH_STEPS, PROF_DIFFUSE_REFL, PROF_GLASS_BOUNCES, PROF_HIT_INST, PROF_HIT_PRIM, PROF_HIT_TERRAIN, PROF_HIT_WATER, PROF_MIRROR_BOUNCES, PROF_PATH_SEGS, PROF_PIXELS, PROF_PRIM_TESTS, PROF_PRI_HIT_INST, PROF_PRI_HIT_PRIM, PROF_PRI_HIT_TERRAIN, PROF_PRI_HIT_WATER, PROF_PRI_SKY, PROF_SKY, bvh_steps_acc, prim_tests_acc, prof_inc};
import package::math::{clamp01};
import package::texture::{plane_albedo, texture_eval, texture_eval_box, texture_eval_surface};
import package::sky::{sky};
import package::intersect::{TILE_SURFACE_BIAS, displaced_normal, thin_glass_exit_t, xf_to_local_dir, xf_to_local_point, xf_to_world_normal};
import package::terrain::{terrain_normal, water_normal};
import package::terrain_zone::{terrain_zone_shade_normal, terrain_zone_surface};
import package::flame::{accumulate_flame, heat_shimmer_dir};
import package::instance::{inst_local_point, inst_world_normal};
import package::bvh::{nearest_hit};
import package::shade::{cone_cap_normal_world, jitter_dir, normal_at, shade_diffuse, shade_ghost, shade_specular, terrain_albedo, tw_peak};

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
    tag: u32,
};

const RAYSEG_GHOST: u32 = 1u;

struct TraceResult {
    color: vec3<f32>,
    primary: Hit,
};

// MAX_SEGS bounds the ray tree. Recursion is capped at depth 3 and only glass
// branches (into two children), at depths 0 and 1 (depth 2 is no longer
// "reflective" so it terminates), giving at most 1 + 2 + 4 = 7 live segments.
const MAX_SEGS: u32 = 8u;

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
    var stack: array<RaySeg, 8>;
    stack[0] = RaySeg(origin, dir0, vec3<f32>(1.0, 1.0, 1.0), 0u, 0u);
    var sp = 1u;
    var primary = Hit(0.0, 0xffffffffu, 0u, HIT_NO_INSTANCE);
    var got_primary = false;

    loop {
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
            let tiles_box = p.info.x == PRIM_BOX && p.info.z == TEX_TILES;
            if (cone_cap) {
                n = cone_cap_normal_world(p, ro, hp);
                tex_n = normal_at(p, lhp, lrd);
                tex_p = lhp;
            } else if (tiles_box) {
                surf_eps = TILE_SURFACE_BIAS;
                tex_n = displaced_normal(p, lhp);
                tex_p = lhp;
                if ((p.info.w & PRIM_FLAG_TRANSFORMED) != 0u) {
                    var ln = xf_to_world_normal(p, tex_n);
                    if (hit.inst_idx != HIT_NO_INSTANCE) {
                        ln = inst_world_normal(hit.inst_idx, ln);
                    }
                    n = normalize(ln);
                } else {
                    n = tex_n;
                    if (hit.inst_idx != HIT_NO_INSTANCE) {
                        n = inst_world_normal(hit.inst_idx, n);
                    }
                }
            } else if ((p.info.w & PRIM_FLAG_TRANSFORMED) != 0u) {
                tex_n = normal_at(p, lhp, lrd);
                var ln = xf_to_world_normal(p, tex_n);
                if (hit.inst_idx != HIT_NO_INSTANCE) {
                    ln = inst_world_normal(hit.inst_idx, ln);
                }
                n = normalize(ln);
                tex_p = lhp;
            } else {
                tex_n = normal_at(p, tpl_hp, lrd);
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
        if (refl > 0.0 && reflective && twm * refl > SEG_MIN_CONTRIB && (mat == MAT_DIFFUSE || mat == MAT_CHECKER) && sp < MAX_SEGS) {
            prof_inc(PROF_DIFFUSE_REFL, 1u);
            accum = accum + tw * lit * (1.0 - refl);
            stack[sp] = RaySeg(ep, jitter_dir(reflect(rd, n), hp, rough), tw * refl, depth + 1u, 0u);
            sp = sp + 1u;
            continue;
        }
        accum = accum + tw * lit;
    }
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
fn analyze_edge(gid: vec2<u32>, fp: u32) -> vec4<f32> {
    var dx = 0;
    var dy = 0;
    let w = params.width;
    let hgt = params.height;
    if (gid.x > 0u) {
        if (hits_edge_packed(fp, aa_hits[pixel_index(gid.x - 1u, gid.y)])) {
            dx = dx - 1;
        }
    }
    if (gid.x + 1u < w) {
        if (hits_edge_packed(fp, aa_hits[pixel_index(gid.x + 1u, gid.y)])) {
            dx = dx + 1;
        }
    }
    if (gid.y > 0u) {
        if (hits_edge_packed(fp, aa_hits[pixel_index(gid.x, gid.y - 1u)])) {
            dy = dy - 1;
        }
    }
    if (gid.y + 1u < hgt) {
        if (hits_edge_packed(fp, aa_hits[pixel_index(gid.x, gid.y + 1u)])) {
            dy = dy + 1;
        }
    }
    if (dx == 0 && dy == 0) {
        return vec4<f32>(0.5, 0.5, 0.0, 0.0);
    }
    var sx = 0.5;
    var sy = 0.5;
    if (dx < 0) {
        sx = 0.25;
    } else if (dx > 0) {
        sx = 0.75;
    }
    if (dy < 0) {
        sy = 0.25;
    } else if (dy > 0) {
        sy = 0.75;
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
        hdr_pixels[idx] = vec4<f32>(tr.color, 0.0);
        aa_hits[idx] = pack_hit(tr.primary);
        write_pixel(g2, tr.color);
        return;
    }

    write_pixel(g2, tr.color);
}

// aa_resolve is the second AA pass: compare neighbor hit fingerprints and only
// supersample pixels that sit on a geometric edge.
@compute @workgroup_size(8, 8, 1)
fn aa_resolve(@builtin(global_invocation_id) gid: vec3<u32>) {
    if (gid.x >= params.width || gid.y >= params.height) {
        return;
    }

    let g2 = vec2<u32>(gid.xy);
    let idx = pixel_index(gid.x, gid.y);
    let fp = aa_hits[idx];
    let edge = analyze_edge(g2, fp);
    if (edge.w < 0.5) {
        return;
    }
    let col = supersample_edge(f32(gid.x), f32(gid.y), hdr_pixels[idx].xyz, edge.xy);
    write_pixel(g2, col);
}
