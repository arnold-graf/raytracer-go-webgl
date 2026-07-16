// terrain.wesl — Heightfield terrain and water surfaces.
// Hybrid LOD: coarse baked grid for the mid field, full analytic height (with
// fBm) in the near band keyed on camera distance. Ray marching samples
// terrain_height, which blends the two over the configured fade band.

import package::types::{RAY_EPSILON, T_MISS, params, terrain_features, terrain_mip, terrain_pads, terrain_samples, terrains, waters};
import package::profile::{PROF_TERRAIN_STEPS, prof_inc};
import package::math::{fbm_xz, mix3, perlin_xz, smoothstepf};

fn terrain_height_baked(i: u32, x: f32, z: f32) -> f32 {
    let tr = terrains[i];
    let gnx = tr.grid.x;
    let gnz = tr.grid.y;
    let off = tr.grid.z;
    let fx0 = clamp((x - tr.bounds0.x) / tr.bounds0.z * f32(gnx - 1u), 0.0, f32(gnx - 1u));
    let fz0 = clamp((z - tr.bounds0.y) / tr.bounds0.w * f32(gnz - 1u), 0.0, f32(gnz - 1u));
    var ix = u32(floor(fx0));
    var iz = u32(floor(fz0));
    if (ix >= gnx - 1u) { ix = gnx - 2u; }
    if (iz >= gnz - 1u) { iz = gnz - 2u; }
    let tx = fx0 - f32(ix);
    let tz = fz0 - f32(iz);
    let base = off + iz * gnx + ix;
    let h00 = terrain_samples[base];
    let h10 = terrain_samples[base + 1u];
    let h01 = terrain_samples[base + gnx];
    let h11 = terrain_samples[base + gnx + 1u];
    return mix(mix(h00, h10, tx), mix(h01, h11, tx), tz);
}

fn terrain_apply_pads(i: u32, x: f32, z: f32, h: f32) -> f32 {
    let tr = terrains[i];
    let padBase = tr.offsets.y;
    let padCount = tr.offsets.w;
    var out = h;
    for (var pi = 0u; pi < padCount; pi = pi + 1u) {
        let p = terrain_pads[padBase + pi];
        var dx = x - p.center.x;
        var dz = z - p.center.y;
        if (p.params.z != 0.0) {
            let c = cos(p.params.z);
            let s = sin(p.params.z);
            let rdx = dx * c + dz * s;
            let rdz = -dx * s + dz * c;
            dx = rdx;
            dz = rdz;
        }
        var lx = abs(dx) - p.center.z;
        var lz = abs(dz) - p.center.w;
        if (lx < 0.0) { lx = 0.0; }
        if (lz < 0.0) { lz = 0.0; }
        var w = 0.0;
        if (p.params.y <= 0.0) {
            if (lx == 0.0 && lz == 0.0) {
                w = 1.0;
            }
        } else {
            w = 1.0 - smoothstepf(0.0, p.params.y, length(vec2<f32>(lx, lz)));
        }
        out = out + (p.params.x - out) * w;
    }
    return out;
}

fn terrain_natural_analytic(i: u32, x: f32, z: f32, detail_w: f32) -> f32 {
    let tr = terrains[i];
    var h = tr.analytic.x;
    let featBase = tr.offsets.x;
    let featCount = tr.offsets.z;
    for (var fi = 0u; fi < featCount; fi = fi + 1u) {
        let f = terrain_features[featBase + fi];
        var dx = x - f.pos.x;
        var dz = z - f.pos.y;
        if (dx * dx + dz * dz > f.cull.x) {
            continue;
        }
        if (f.shape.w != 0.0) {
            let c = cos(f.shape.w);
            let s = sin(f.shape.w);
            let rdx = dx * c + dz * s;
            let rdz = -dx * s + dz * c;
            dx = rdx;
            dz = rdz;
        }
        var ex = f.shape.y;
        var ez = f.shape.z;
        var w = f.pos.w;
        if (ex == 0.0) { ex = 1.0; }
        if (ez == 0.0) { ez = 1.0; }
        if (w == 0.0) { w = 1.0; }
        var st = f.shape.x;
        if (st == 0.0) { st = 2.0; }
        let ax = dx / (w * ex);
        let az = dz / (w * ez);
        let d = length(vec2<f32>(ax, az));
        h = h + f.pos.z * exp(-pow(d, st));
    }
    if (detail_w > 0.0 && tr.analytic.y != 0.0) {
        let scale = tr.analytic.z;
        h = h + tr.analytic.y * detail_w * fbm_xz(x * scale, z * scale, 4u);
    }
    let isl = tr.island0;
    if (isl.z > 0.0) {
        let dist = length(vec2<f32>(x - isl.x, z - isl.y));
        var margin = isl.w;
        if (margin <= 0.0) {
            margin = isl.z * 0.5;
        }
        let land = 1.0 - smoothstepf(isl.z, isl.z + margin, dist);
        h = tr.island1.x + (h - tr.island1.x) * land;
    }
    return h;
}

fn terrain_height_analytic(i: u32, x: f32, z: f32, detail_w: f32) -> f32 {
    return terrain_apply_pads(i, x, z, terrain_natural_analytic(i, x, z, detail_w));
}

fn terrain_height(i: u32, x: f32, z: f32) -> f32 {
    return terrain_height_sample(i, x, z, false);
}

fn terrain_height_sample(i: u32, x: f32, z: f32, baked_only: bool) -> f32 {
    if (baked_only) {
        return terrain_height_baked(i, x, z);
    }
    let tr = terrains[i];
    if (tr.island1.z < 0.5) {
        return terrain_height_baked(i, x, z);
    }
    let cam = params.cam_pos.xz;
    let dist = length(vec2<f32>(x, z) - cam);
    let nearStart = tr.analytic.w;
    let nearEnd = tr.island1.y;
    if (dist >= nearEnd) {
        return terrain_height_baked(i, x, z);
    }
    let detailW = 1.0 - smoothstepf(nearStart, nearEnd, dist);
    let analytic = terrain_height_analytic(i, x, z, detailW);
    if (dist <= nearStart) {
        return analytic;
    }
    let t = smoothstepf(nearStart, nearEnd, dist);
    return mix(analytic, terrain_height_baked(i, x, z), t);
}

fn terrain_sample_normal(idx: u32) -> vec3<f32> {
    return vec3<f32>(terrain_samples[idx], terrain_samples[idx + 1u], terrain_samples[idx + 2u]);
}

fn terrain_normal_baked(i: u32, p: vec3<f32>) -> vec3<f32> {
    let tr = terrains[i];
    let gnx = tr.grid.x;
    let gnz = tr.grid.y;
    let off = tr.grid.w;
    let fx0 = clamp((p.x - tr.bounds0.x) / tr.bounds0.z * f32(gnx - 1u), 0.0, f32(gnx - 1u));
    let fz0 = clamp((p.z - tr.bounds0.y) / tr.bounds0.w * f32(gnz - 1u), 0.0, f32(gnz - 1u));
    var ix = u32(floor(fx0));
    var iz = u32(floor(fz0));
    if (ix >= gnx - 1u) { ix = gnx - 2u; }
    if (iz >= gnz - 1u) { iz = gnz - 2u; }
    let tx = fx0 - f32(ix);
    let tz = fz0 - f32(iz);
    let base = off + 3u * (iz * gnx + ix);
    let n00 = terrain_sample_normal(base);
    let n10 = terrain_sample_normal(base + 3u);
    let n01 = terrain_sample_normal(base + 3u * gnx);
    let n11 = terrain_sample_normal(base + 3u * gnx + 3u);
    return normalize(mix3(mix3(n00, n10, tx), mix3(n01, n11, tx), tz));
}

fn terrain_normal(i: u32, p: vec3<f32>) -> vec3<f32> {
    let tr = terrains[i];
    if (tr.island1.z < 0.5) {
        return terrain_normal_baked(i, p);
    }
    let e = 0.05;
    let hl = terrain_height(i, p.x - e, p.z);
    let hr = terrain_height(i, p.x + e, p.z);
    let hd = terrain_height(i, p.x, p.z - e);
    let hu = terrain_height(i, p.x, p.z + e);
    return normalize(vec3<f32>(hl - hr, 2.0 * e, hd - hu));
}

fn terrain_slab(i: u32, ro: vec3<f32>, rd: vec3<f32>) -> vec2<f32> {
    let tr = terrains[i];
    let bmin = vec3<f32>(tr.bounds0.x, tr.bounds1.x, tr.bounds0.y);
    let bmax = vec3<f32>(tr.bounds0.x + tr.bounds0.z, tr.bounds1.y, tr.bounds0.y + tr.bounds0.w);
    let inv = vec3<f32>(1.0) / rd;
    let t0 = (bmin - ro) * inv;
    let t1 = (bmax - ro) * inv;
    let lo = min(t0, t1);
    let hi = max(t0, t1);
    let enter = max(max(lo.x, lo.y), lo.z);
    let exit = min(min(hi.x, hi.y), hi.z);
    if (exit < enter || exit < RAY_EPSILON) {
        return vec2<f32>(T_MISS, T_MISS);
    }
    return vec2<f32>(max(enter, RAY_EPSILON), exit);
}

// terrain_shadow_skip reports whether a shadow ray cannot hit terrain before max_t.
fn terrain_shadow_skip(i: u32, origin: vec3<f32>, dir: vec3<f32>, max_t: f32) -> bool {
    let tr = terrains[i];
    if (dir.y >= -1e-4) {
        return true;
    }
    if (origin.y > tr.bounds1.y && dir.y >= 0.0) {
        return true;
    }
    let te = terrain_slab(i, origin, dir);
    let t_lim = min(te.y, max_t);
    if (te.x >= t_lim || te.x >= T_MISS) {
        return true;
    }
    return false;
}

fn mip_level_dims(l0nx: u32, l0nz: u32, level: u32) -> vec2<u32> {
    var nx = l0nx;
    var nz = l0nz;
    for (var l = 0u; l < level; l = l + 1u) {
        nx = max(1u, (nx + 1u) / 2u);
        nz = max(1u, (nz + 1u) / 2u);
    }
    return vec2<u32>(nx, nz);
}

fn mip_level_off(base: u32, l0nx: u32, l0nz: u32, level: u32) -> u32 {
    var off = base;
    var nx = l0nx;
    var nz = l0nz;
    for (var l = 0u; l < level; l = l + 1u) {
        off += nx * nz;
        nx = max(1u, (nx + 1u) / 2u);
        nz = max(1u, (nz + 1u) / 2u);
    }
    return off;
}

fn mip_cell_minmax(i: u32, level: u32, cell: vec2<u32>) -> vec2<f32> {
    let tr = terrains[i];
    let dims = mip_level_dims(tr.mip.y, tr.mip.z, level);
    let off = mip_level_off(tr.mip.x, tr.mip.y, tr.mip.z, level);
    return terrain_mip[off + cell.y * dims.x + cell.x];
}

fn mip_world_cell(i: u32, level: u32, x: f32, z: f32) -> vec2<u32> {
    let tr = terrains[i];
    let scale = f32(1u << level);
    let cwx = tr.coarse.x * scale;
    let cwz = tr.coarse.y * scale;
    let dims = mip_level_dims(tr.mip.y, tr.mip.z, level);
    var cx = u32(floor((x - tr.bounds0.x) / cwx));
    var cz = u32(floor((z - tr.bounds0.y) / cwz));
    let mx = dims.x - 1u;
    let mz = dims.y - 1u;
    if (cx > mx) { cx = mx; }
    if (cz > mz) { cz = mz; }
    return vec2<u32>(cx, cz);
}

// terrain_seg_near reports whether any point of the ray segment [t0,t1] passes
// within nearEnd of the camera in XZ — i.e. whether hybrid heights can differ
// from the baked grid anywhere on the segment.
fn terrain_seg_near(ro: vec3<f32>, rd: vec3<f32>, t0: f32, t1: f32, near_end: f32) -> bool {
    let a = ro.xz + rd.xz * t0;
    let ab = rd.xz * (t1 - t0);
    let toc = params.cam_pos.xz - a;
    let tt = clamp(dot(toc, ab) / max(dot(ab, ab), 1e-12), 0.0, 1.0);
    let d = a + ab * tt - params.cam_pos.xz;
    return dot(d, d) < near_end * near_end;
}

// Height used while marching toward an intersection. Primary rays use the full
// hybrid height within the near band; shadow rays march baked then refine.
fn terrain_height_march(i: u32, x: f32, z: f32, hybrid: bool) -> f32 {
    if (hybrid) {
        return terrain_height(i, x, z);
    }
    return terrain_height_baked(i, x, z);
}

fn hit_terrain_fine(i: u32, ro: vec3<f32>, rd: vec3<f32>, t_enter: f32, t_exit: f32, refine: bool, shadow_ray: bool) -> f32 {
    let tr = terrains[i];
    let base = tr.bounds1.z;
    // Hoisted per-segment: only segments passing through the near band ever
    // see hybrid heights, so far segments march (and refine) baked-only.
    var seg_hybrid = false;
    if (tr.island1.z >= 0.5) {
        seg_hybrid = terrain_seg_near(ro, rd, t_enter, t_exit, tr.island1.y);
    }
    let march_hybrid = seg_hybrid && !shadow_ray;
    var tc = t_enter;
    var p = ro + rd * tc;
    var fc = p.y - terrain_height_march(i, p.x, p.z, march_hybrid);
    for (var iter = 0u; iter < 256u; iter = iter + 1u) {
        prof_inc(PROF_TERRAIN_STEPS, 1u);
        if (tc >= t_exit) {
            break;
        }
        var step = base;
        if (fc > 0.0) {
            step = max(step, fc * 0.7);
            step = min(step, base * 20.0);
        }
        var tn = min(tc + step, t_exit);
        let pn = ro + rd * tn;
        let f_next = pn.y - terrain_height_march(i, pn.x, pn.z, march_hybrid);
        if (f_next <= 0.0 && fc > 0.0) {
            if (!refine) {
                return tn;
            }
            var lo = tc;
            var hi = tn;
            for (var j = 0u; j < 10u; j = j + 1u) {
                let m = (lo + hi) * 0.5;
                let pm = ro + rd * m;
                if (pm.y - terrain_height_march(i, pm.x, pm.z, seg_hybrid) <= 0.0) {
                    hi = m;
                } else {
                    lo = m;
                }
            }
            let th = (lo + hi) * 0.5;
            let ph = ro + rd * th;
            if (ph.y - terrain_height_march(i, ph.x, ph.z, seg_hybrid) > 0.02) {
                tc = th;
                fc = ph.y - terrain_height_march(i, ph.x, ph.z, march_hybrid);
            } else {
                return th;
            }
        } else {
            tc = tn;
            p = pn;
            fc = f_next;
        }
    }
    return T_MISS;
}

fn mip_init_dda(
    i: u32,
    level: u32,
    ro: vec3<f32>,
    rd: vec3<f32>,
    tc: f32,
    cx: ptr<function, i32>,
    cz: ptr<function, i32>,
    t_next_x: ptr<function, f32>,
    t_delta_x: ptr<function, f32>,
    step_x: ptr<function, i32>,
    t_next_z: ptr<function, f32>,
    t_delta_z: ptr<function, f32>,
    step_z: ptr<function, i32>,
) {
    let tr = terrains[i];
    let scale = f32(1u << level);
    let cwx = tr.coarse.x * scale;
    let cwz = tr.coarse.y * scale;
    let cell = mip_world_cell(i, level, ro.x + rd.x * tc, ro.z + rd.z * tc);
    *cx = i32(cell.x);
    *cz = i32(cell.y);

    *t_next_x = 1e30;
    *t_delta_x = 1e30;
    *step_x = 0;
    if (rd.x > 1e-9) {
        *step_x = 1;
        *t_next_x = (tr.bounds0.x + f32(*cx + 1) * cwx - ro.x) / rd.x;
        *t_delta_x = cwx / rd.x;
    } else if (rd.x < -1e-9) {
        *step_x = -1;
        *t_next_x = (tr.bounds0.x + f32(*cx) * cwx - ro.x) / rd.x;
        *t_delta_x = -cwx / rd.x;
    }

    *t_next_z = 1e30;
    *t_delta_z = 1e30;
    *step_z = 0;
    if (rd.z > 1e-9) {
        *step_z = 1;
        *t_next_z = (tr.bounds0.y + f32(*cz + 1) * cwz - ro.z) / rd.z;
        *t_delta_z = cwz / rd.z;
    } else if (rd.z < -1e-9) {
        *step_z = -1;
        *t_next_z = (tr.bounds0.y + f32(*cz) * cwz - ro.z) / rd.z;
        *t_delta_z = -cwz / rd.z;
    }
}

fn hit_terrain_mip(i: u32, ro: vec3<f32>, rd: vec3<f32>, t_enter: f32, t_exit: f32, refine: bool, shadow_ray: bool) -> f32 {
    let tr = terrains[i];
    var stk_level: array<u32, 12>;
    var stk_t_exit: array<f32, 12>;
    var stk_tc: array<f32, 12>;
    var stk_cx: array<i32, 12>;
    var stk_cz: array<i32, 12>;
    var stk_t_next_x: array<f32, 12>;
    var stk_t_delta_x: array<f32, 12>;
    var stk_step_x: array<i32, 12>;
    var stk_t_next_z: array<f32, 12>;
    var stk_t_delta_z: array<f32, 12>;
    var stk_step_z: array<i32, 12>;
    var stk_resume: array<f32, 12>;
    var sp = 0u;

    var level = tr.mip.w - 1u;
    var seg_exit = t_exit;
    var tc = t_enter;
    var cx: i32 = 0;
    var cz: i32 = 0;
    var t_next_x: f32 = 1e30;
    var t_delta_x: f32 = 1e30;
    var step_x: i32 = 0;
    var t_next_z: f32 = 1e30;
    var t_delta_z: f32 = 1e30;
    var step_z: i32 = 0;
    var need_init = true;

    for (var guard = 0u; guard < 8192u; guard = guard + 1u) {
        if (level == 0u) {
            if (tc + 1e-4 >= seg_exit) {
                if (sp == 0u) {
                    return T_MISS;
                }
                sp = sp - 1u;
                level = stk_level[sp];
                seg_exit = stk_t_exit[sp];
                tc = stk_resume[sp];
                cx = stk_cx[sp];
                cz = stk_cz[sp];
                t_next_x = stk_t_next_x[sp];
                t_delta_x = stk_t_delta_x[sp];
                step_x = stk_step_x[sp];
                t_next_z = stk_t_next_z[sp];
                t_delta_z = stk_t_delta_z[sp];
                step_z = stk_step_z[sp];
                let cross_x = t_next_x <= t_next_z;
                let cross_z = t_next_z <= t_next_x;
                if (cross_x) {
                    cx += step_x;
                    t_next_x += t_delta_x;
                }
                if (cross_z) {
                    cz += step_z;
                    t_next_z += t_delta_z;
                }
                need_init = false;
                continue;
            }
            let hit = hit_terrain_fine(i, ro, rd, tc, seg_exit, refine, shadow_ray);
            if (hit < T_MISS) {
                return hit;
            }
            if (sp == 0u) {
                return T_MISS;
            }
            sp = sp - 1u;
            level = stk_level[sp];
            seg_exit = stk_t_exit[sp];
            tc = stk_resume[sp];
            cx = stk_cx[sp];
            cz = stk_cz[sp];
            t_next_x = stk_t_next_x[sp];
            t_delta_x = stk_t_delta_x[sp];
            step_x = stk_step_x[sp];
            t_next_z = stk_t_next_z[sp];
            t_delta_z = stk_t_delta_z[sp];
            step_z = stk_step_z[sp];
            let cross_x = t_next_x <= t_next_z;
            let cross_z = t_next_z <= t_next_x;
            if (cross_x) {
                cx += step_x;
                t_next_x += t_delta_x;
            }
            if (cross_z) {
                cz += step_z;
                t_next_z += t_delta_z;
            }
            need_init = false;
            continue;
        }

        let dims = mip_level_dims(tr.mip.y, tr.mip.z, level);
        if (need_init) {
            mip_init_dda(i, level, ro, rd, tc, &cx, &cz, &t_next_x, &t_delta_x, &step_x, &t_next_z, &t_delta_z, &step_z);
            need_init = false;
        }

        if (tc >= seg_exit) {
            if (sp == 0u) {
                return T_MISS;
            }
            sp = sp - 1u;
            level = stk_level[sp];
            seg_exit = stk_t_exit[sp];
            tc = stk_resume[sp];
            cx = stk_cx[sp];
            cz = stk_cz[sp];
            t_next_x = stk_t_next_x[sp];
            t_delta_x = stk_t_delta_x[sp];
            step_x = stk_step_x[sp];
            t_next_z = stk_t_next_z[sp];
            t_delta_z = stk_t_delta_z[sp];
            step_z = stk_step_z[sp];
            need_init = true;
            continue;
        }
        if (cx < 0 || cz < 0 || cx >= i32(dims.x) || cz >= i32(dims.y)) {
            if (sp == 0u) {
                return T_MISS;
            }
            sp = sp - 1u;
            level = stk_level[sp];
            seg_exit = stk_t_exit[sp];
            tc = stk_resume[sp];
            cx = stk_cx[sp];
            cz = stk_cz[sp];
            t_next_x = stk_t_next_x[sp];
            t_delta_x = stk_t_delta_x[sp];
            step_x = stk_step_x[sp];
            t_next_z = stk_t_next_z[sp];
            t_delta_z = stk_t_delta_z[sp];
            step_z = stk_step_z[sp];
            need_init = true;
            continue;
        }

        var tn = t_next_x;
        if (t_next_z < tn) {
            tn = t_next_z;
        }
        if (tn > seg_exit) {
            tn = seg_exit;
        }
        if (tn < tc) {
            tn = tc;
        }

        let mm = mip_cell_minmax(i, level, vec2<u32>(u32(cx), u32(cz)));
        let y0 = ro.y + rd.y * tc;
        let y1 = ro.y + rd.y * tn;
        let seg_min = min(y0, y1);
        let seg_max = max(y0, y1);
        // Hybrid near-band heights can exceed the baked mip bounds by the fBm
        // amplitude; only borderline cells whose segment passes near the
        // camera need that padding (far cells march baked, bounded by mm).
        var overlap = seg_max >= mm.x && seg_min <= mm.y;
        if (!overlap && tr.island1.z >= 0.5) {
            let mm_pad = tr.analytic.y * 0.5;
            if (seg_max >= mm.x - mm_pad && seg_min <= mm.y + mm_pad
                && terrain_seg_near(ro, rd, tc, tn, tr.island1.y)) {
                overlap = true;
            }
        }
        if (overlap && tn > tc + 1e-4) {
            if (sp >= 12u) {
                return T_MISS;
            }
            stk_level[sp] = level;
            stk_t_exit[sp] = seg_exit;
            stk_tc[sp] = tc;
            stk_cx[sp] = cx;
            stk_cz[sp] = cz;
            stk_t_next_x[sp] = t_next_x;
            stk_t_delta_x[sp] = t_delta_x;
            stk_step_x[sp] = step_x;
            stk_t_next_z[sp] = t_next_z;
            stk_t_delta_z[sp] = t_delta_z;
            stk_step_z[sp] = step_z;
            stk_resume[sp] = tn + 1e-5;
            sp = sp + 1u;
            level = level - 1u;
            seg_exit = tn;
            need_init = true;
            continue;
        }

        if (tn >= seg_exit) {
            if (sp == 0u) {
                return T_MISS;
            }
            sp = sp - 1u;
            level = stk_level[sp];
            seg_exit = stk_t_exit[sp];
            tc = stk_resume[sp];
            cx = stk_cx[sp];
            cz = stk_cz[sp];
            t_next_x = stk_t_next_x[sp];
            t_delta_x = stk_t_delta_x[sp];
            step_x = stk_step_x[sp];
            t_next_z = stk_t_next_z[sp];
            t_delta_z = stk_t_delta_z[sp];
            step_z = stk_step_z[sp];
            need_init = true;
            continue;
        }

        let cross_x = t_next_x <= t_next_z;
        let cross_z = t_next_z <= t_next_x;
        if (cross_x) {
            cx += step_x;
            t_next_x += t_delta_x;
        }
        if (cross_z) {
            cz += step_z;
            t_next_z += t_delta_z;
        }
        tc = tn + 1e-5;
    }
    return T_MISS;
}

fn hit_terrain(i: u32, ro: vec3<f32>, rd: vec3<f32>, max_t: f32, refine: bool, shadow_ray: bool) -> f32 {
    let te = terrain_slab(i, ro, rd);
    var t_exit = min(te.y, max_t);
    if (te.x >= t_exit || te.x >= T_MISS) {
        return T_MISS;
    }
    let tr = terrains[i];
    if (tr.mip.w > 0u) {
        return hit_terrain_mip(i, ro, rd, te.x, t_exit, refine, shadow_ray);
    }
    return hit_terrain_fine(i, ro, rd, te.x, t_exit, refine, shadow_ray);
}

fn hit_water(i: u32, ro: vec3<f32>, rd: vec3<f32>) -> f32 {
    let w = waters[i];
    if (abs(rd.y) < 1e-6) {
        return T_MISS;
    }
    let t = (w.geom.w - ro.y) / rd.y;
    if (t < RAY_EPSILON) {
        return T_MISS;
    }
    let p = ro + rd * t;
    if (w.geom.z > 0.0) {
        let d = p.xz - w.geom.xy;
        if (dot(d, d) > w.geom.z * w.geom.z) {
            return T_MISS;
        }
    }
    if (w.info.z != 0u) {
        var th = -1e10;
        for (var ti = 0u; ti < params.terrain_count; ti = ti + 1u) {
            th = max(th, terrain_height(ti, p.x, p.z));
        }
        if (th >= w.geom.w) {
            return T_MISS;
        }
    }
    return t;
}

fn water_normal(i: u32, p: vec3<f32>) -> vec3<f32> {
    let w = waters[i];
    if (w.params.x <= 0.0) {
        return vec3<f32>(0.0, 1.0, 0.0);
    }
    let phase = params.time * w.params.y;
    let dx = phase * w.params.z;
    let dz = phase * w.params.w;
    return normalize(vec3<f32>(
        w.params.x * perlin_xz(p.x * 2.5 + dx, p.z * 2.5 + dz),
        1.0,
        w.params.x * perlin_xz(p.x * 2.5 + dx + 7.0, p.z * 2.5 + dz + 3.0),
    ));
}


