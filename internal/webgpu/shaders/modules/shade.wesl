// shade.wesl — Lighting, shadows, and surface normals.
// Computes shading normals per primitive, casts hard shadow rays toward lights,
// evaluates point lights and flickering campfires, samples baked ambient
// occlusion, and assembles direct diffuse lighting (shade_diffuse).

import package::types::{CampfireParams, LIGHT_ATTEN_BASE, LIGHT_ATTEN_QUADRATIC, LIGHT_CULL_EPS, Light, PRIM_BOX, PRIM_CONE, PRIM_CYLINDER, PRIM_FLAG_TRANSFORMED, PRIM_LENS, PRIM_PLANE, PRIM_RING, PRIM_SPHERE, Prim, RAY_EPSILON, SHADOW_SKIP_EPS, ao_volume, blocker_plane_idx, campfires, holes, lights, params, terrains};
import package::profile::{PROF_SHADOW_BLOCK, PROF_SHADOW_RAYS, prof_inc};
import package::math::{mix3, perlin, smoothstepf};
import package::texture::{texture_eval};
import package::intersect::{intersect_blocker, xf_to_world_normal};
import package::terrain::{hit_terrain, terrain_shadow_skip};
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
    if (kind == PRIM_SPHERE) {
        return normalize(hp - p.geo_a.xyz);
    }
    if (kind == PRIM_PLANE) {
        return p.geo_a.xyz;
    }
    if (kind == PRIM_BOX) {
        return box_normal(p, hp);
    }
    if (kind == PRIM_CYLINDER) {
        return cylinder_normal(p, hp);
    }
    if (kind == PRIM_CONE) {
        return cone_normal(p, hp, rd);
    }
    if (kind == PRIM_RING) {
        return ring_normal(p, hp);
    }
    if (kind == PRIM_LENS) {
        return lens_normal(p, hp);
    }
    return torus_normal(p, hp);
}

fn shadowed(origin: vec3<f32>, dir: vec3<f32>, max_t: f32) -> bool {
    prof_inc(PROF_SHADOW_RAYS, 1u);
    if (blocker_bvh_any_hit(origin, dir, max_t)) {
        prof_inc(PROF_SHADOW_BLOCK, 1u);
        return true;
    }
    let limit = max_t - 0.05;
    for (var i = 0u; i < params.blocker_plane_count; i = i + 1u) {
        let bi = blocker_plane_idx[i];
        let t = intersect_blocker(bi, origin, dir);
        if (t > RAY_EPSILON && t < limit) {
            prof_inc(PROF_SHADOW_BLOCK, 1u);
            return true;
        }
    }
    for (var i = 0u; i < params.terrain_count; i = i + 1u) {
        if (terrain_shadow_skip(i, origin, dir, max_t)) {
            continue;
        }
        let t = hit_terrain(i, origin, dir, max_t, true, true);
        if (t > RAY_EPSILON && t < limit) {
            prof_inc(PROF_SHADOW_BLOCK, 1u);
            return true;
        }
    }
    return false;
}

fn tw_peak(tw: vec3<f32>) -> f32 {
    return max(tw.x, max(tw.y, tw.z));
}

fn spot_dir(yaw: f32, pitch: f32) -> vec3<f32> {
    let cp = cos(pitch);
    return vec3<f32>(sin(yaw) * cp, sin(pitch), -cos(yaw) * cp);
}

// Shared core for one point light (static lights + campfire sub-lights).
// Tuning knobs at the top of this file: LIGHT_CULL_EPS, SHADOW_SKIP_EPS,
// LIGHT_ATTEN_BASE, LIGHT_ATTEN_QUADRATIC. tw is the ray segment throughput.
fn add_point_light_raw(lit: vec3<f32>, hp: vec3<f32>, albedo: vec3<f32>, n: vec3<f32>, ep: vec3<f32>, pos: vec3<f32>, color: vec3<f32>, cull_r2: f32, inv_r2: f32, tw: vec3<f32>, spot_axis: vec3<f32>, cos_outer: f32) -> vec3<f32> {
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
    var att = min(1.0, 1.0 / (LIGHT_ATTEN_BASE + d2 * LIGHT_ATTEN_QUADRATIC));
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
    let peak = max(unshadowed.x, max(unshadowed.y, unshadowed.z));
    if (twm * peak < SHADOW_SKIP_EPS) {
        return lit + unshadowed;
    }
    if (params.shadows != 0u && shadowed(ep, ln, ldist)) {
        return lit;
    }
    return lit + unshadowed;
}

fn add_point_light(lit: vec3<f32>, hp: vec3<f32>, albedo: vec3<f32>, n: vec3<f32>, ep: vec3<f32>, light: Light, tw: vec3<f32>) -> vec3<f32> {
    var axis = vec3<f32>(0.0, 1.0, 0.0);
    var cos_outer = 0.0;
    if (light.pos.w > 0.5) {
        axis = spot_dir(light.falloff.z, light.falloff.w);
        cos_outer = light.color.w;
    }
    return add_point_light_raw(lit, hp, albedo, n, ep, light.pos.xyz, light.color.xyz, light.falloff.x, light.falloff.y, tw, axis, cos_outer);
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
    let ph = cf.phase.x + f32(j) * 1.7;
    let fl = 0.6 * sin(ts * 7.0 + ph) + 0.3 * sin(ts * 13.0 + ph * 2.1) + 0.1 * sin(ts * 23.0 + ph * 3.7);
    let intensity = cf.param.x * (1.0 + cf.param.z * fl);
    let pos = cf.core.xyz + base + vec3<f32>(
        cf.param.y * (0.7 * sin(ts * 9.0 + ph * 1.3) + 0.3 * sin(ts * 17.0 + ph * 2.7)),
        cf.param.y * (0.4 + 0.4 * sin(ts * 15.0 + ph)),
        cf.param.y * (0.7 * sin(ts * 11.0 + ph * 1.9) + 0.3 * sin(ts * 19.0 + ph * 0.7)),
    );
    let col = cf.color.xyz * max(intensity, 0.15 * cf.param.x) * tint;
    return CampfireSample(pos, col);
}

// Direct diffuse lighting: ambient + point lights + campfires + baked AO.
// Campfire core occlusion also uses SHADOW_SKIP_EPS (see loop below).
fn scene_ambient(n: vec3<f32>) -> vec3<f32> {
    let t = clamp(n.y * 0.5 + 0.5, 0.0, 1.0);
    return mix(params.ambient_ground.xyz, params.ambient_sky.xyz, t);
}

fn shade_diffuse(hp: vec3<f32>, alb: vec3<f32>, n: vec3<f32>, ep: vec3<f32>, tw: vec3<f32>) -> vec3<f32> {
    var lit = alb * scene_ambient(n);
    for (var i = 0u; i < params.light_count; i = i + 1u) {
        lit = add_point_light(lit, hp, alb, n, ep, lights[i], tw);
    }
    for (var ci = 0u; ci < params.campfire_count; ci = ci + 1u) {
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
                let att = min(1.0, 1.0 / (LIGHT_ATTEN_BASE + cd2 * LIGHT_ATTEN_QUADRATIC));
                let peak = att * ndl * cf.param.x * max(alb.x, max(alb.y, alb.z));
                if (tw_peak(tw) * peak >= SHADOW_SKIP_EPS) {
                    if (shadowed(ep, cdir, cdist)) {
                        continue;
                    }
                }
            }
        }
        let ts = params.time * cf.param.w;
        for (var j = 0u; j < 3u; j = j + 1u) {
            let sl = campfire_sublight(cf, j, ts);
            lit = add_point_light_raw(lit, hp, alb, n, ep, sl.pos, sl.col, cull.x, cull.y, tw, vec3<f32>(0.0, 1.0, 0.0), 0.0);
        }
    }
    if (params.ao_enabled != 0u) {
        lit = lit * ao_sample(ep, n);
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
    return c;
}
