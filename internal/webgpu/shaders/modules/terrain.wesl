// terrain.wesl — Heightfield terrain and water surfaces.
// Hybrid LOD: coarse baked grid for the mid field, full analytic height (with
// fBm) in the near band keyed on camera distance. Ray marching samples
// terrain_height, which blends the two over the configured fade band.



import package::types::{RAY_EPSILON, T_MISS, params, terrain_features, terrain_pads, terrain_samples, terrains, waters};
import package::profile::{PROF_TERRAIN_STEPS, prof_inc};
import package::math::{fbm, mix3, perlin, smoothstepf};

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
    let h00 = terrain_samples[base].w;
    let h10 = terrain_samples[base + 1u].w;
    let h01 = terrain_samples[base + gnx].w;
    let h11 = terrain_samples[base + gnx + 1u].w;
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
        h = h + tr.analytic.y * detail_w * fbm(x * scale, 0.0, z * scale, 4u);
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
    let tr = terrains[i];
    if (tr.island1.z < 0.5) {
        return terrain_height_baked(i, x, z);
    }
    let baked = terrain_height_baked(i, x, z);
    let cam = params.cam_pos.xz;
    let dist = length(vec2<f32>(x, z) - cam);
    let nearStart = tr.analytic.w;
    let nearEnd = tr.island1.y;
    if (dist >= nearEnd) {
        return baked;
    }
    let detailW = 1.0 - smoothstepf(nearStart, nearEnd, dist);
    let analytic = terrain_height_analytic(i, x, z, detailW);
    if (dist <= nearStart) {
        return analytic;
    }
    let t = smoothstepf(nearStart, nearEnd, dist);
    return mix(analytic, baked, t);
}

fn terrain_normal_baked(i: u32, p: vec3<f32>) -> vec3<f32> {
    let tr = terrains[i];
    let gnx = tr.grid.x;
    let gnz = tr.grid.y;
    let off = tr.grid.z;
    let fx0 = clamp((p.x - tr.bounds0.x) / tr.bounds0.z * f32(gnx - 1u), 0.0, f32(gnx - 1u));
    let fz0 = clamp((p.z - tr.bounds0.y) / tr.bounds0.w * f32(gnz - 1u), 0.0, f32(gnz - 1u));
    var ix = u32(floor(fx0));
    var iz = u32(floor(fz0));
    if (ix >= gnx - 1u) { ix = gnx - 2u; }
    if (iz >= gnz - 1u) { iz = gnz - 2u; }
    let tx = fx0 - f32(ix);
    let tz = fz0 - f32(iz);
    let base = off + iz * gnx + ix;
    let n00 = terrain_samples[base].xyz;
    let n10 = terrain_samples[base + 1u].xyz;
    let n01 = terrain_samples[base + gnx].xyz;
    let n11 = terrain_samples[base + gnx + 1u].xyz;
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

fn hit_terrain(i: u32, ro: vec3<f32>, rd: vec3<f32>, max_t: f32, refine: bool) -> f32 {
    let te = terrain_slab(i, ro, rd);
    var tc = te.x;
    var t_exit = min(te.y, max_t);
    if (tc >= t_exit || tc >= T_MISS) {
        return T_MISS;
    }
    let tr = terrains[i];
    let base = tr.bounds1.z;
    var p = ro + rd * tc;
    var fc = p.y - terrain_height(i, p.x, p.z);
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
        let f_next = pn.y - terrain_height(i, pn.x, pn.z);
        if (f_next <= 0.0 && fc > 0.0) {
            if (!refine) {
                return tn;
            }
            var lo = tc;
            var hi = tn;
            for (var j = 0u; j < 10u; j = j + 1u) {
                let m = (lo + hi) * 0.5;
                let pm = ro + rd * m;
                if (pm.y - terrain_height(i, pm.x, pm.z) <= 0.0) {
                    hi = m;
                } else {
                    lo = m;
                }
            }
            return (lo + hi) * 0.5;
        }
        tc = tn;
        p = pn;
        fc = f_next;
    }
    return T_MISS;
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
        w.params.x * perlin(p.x * 2.5 + dx, 0.0, p.z * 2.5 + dz),
        1.0,
        w.params.x * perlin(p.x * 2.5 + dx + 7.0, 0.0, p.z * 2.5 + dz + 3.0),
    ));
}
