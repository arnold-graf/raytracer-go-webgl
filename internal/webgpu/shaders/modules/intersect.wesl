// intersect.wesl — Ray–primitive intersection tests.
// Analytic hit tests for spheres, boxes, cylinders, lenses, etc., including
// CSG box holes and local-space transforms. intersect() / intersect_blocker()
// are the entry points called from BVH leaf nodes.



import package::types::{PRIM_BOX, PRIM_CONE, PRIM_CYLINDER, PRIM_FLAG_TRANSFORMED, PRIM_LENS, PRIM_PLANE, PRIM_RING, PRIM_SPHERE, Prim, RAY_EPSILON, RING_SHELL, T_MISS, blockers, holes, prims};

fn hit_sphere(p: Prim, ro: vec3<f32>, rd: vec3<f32>) -> f32 {
    let center = p.geo_a.xyz;
    let radius = p.geo_a.w;
    let b = ro - center;
    let bd = dot(b, rd);
    let c = dot(b, b) - radius * radius;
    let disc = bd * bd - c;
    if (disc < 0.0) {
        return T_MISS;
    }
    let sq = sqrt(disc);
    var t = -bd - sq;
    if (t < RAY_EPSILON) {
        t = -bd + sq;
    }
    if (t < RAY_EPSILON) {
        return T_MISS;
    }
    return t;
}

fn hit_plane(p: Prim, ro: vec3<f32>, rd: vec3<f32>) -> f32 {
    let n = p.geo_a.xyz;
    let d = p.geo_a.w;
    let denom = dot(rd, n);
    if (abs(denom) < 1e-6) {
        return T_MISS;
    }
    let t = -(dot(ro, n) + d) / denom;
    if (t < RAY_EPSILON) {
        return T_MISS;
    }
    return t;
}

// slab_interval returns the parametric span [tmin, tmax] over which the ray is
// inside the AABB [bmin, bmax]; ok is false on a miss. tmax may be negative.
// Mirrors scene.slabInterval (used by the box CSG difference).
fn slab_interval(bmin: vec3<f32>, bmax: vec3<f32>, ro: vec3<f32>, rd: vec3<f32>) -> vec3<f32> {
    let inv = vec3<f32>(1.0) / rd;
    let ta = (bmin - ro) * inv;
    let tb = (bmax - ro) * inv;
    let lo = min(ta, tb);
    let hi = max(ta, tb);
    let tmin = max(max(lo.x, lo.y), lo.z);
    let tmax = min(min(hi.x, hi.y), hi.z);
    if (tmax < tmin) {
        return vec3<f32>(0.0, 0.0, 0.0); // ok = z == 0
    }
    return vec3<f32>(tmin, tmax, 1.0);
}

fn hit_box(p: Prim, ro: vec3<f32>, rd: vec3<f32>) -> f32 {
    let s = slab_interval(p.geo_a.xyz, p.geo_b.xyz, ro, rd);
    if (s.z == 0.0 || s.y < RAY_EPSILON) {
        return T_MISS;
    }
    let count = u32(p.geo_b.w);
    if (count == 0u) {
        if (s.x < RAY_EPSILON) {
            return s.y;
        }
        return s.x;
    }
    return box_holed_nearest(p, ro, rd, s.x, s.y, count);
}

// box_holed_nearest performs the CSG difference of the box span [tmin,tmax]
// minus each hole's span, returning the nearest boundary >= RAY_EPSILON. It is
// a direct port of scene.Box.Intersect's segment walk (up to 8 segments).
fn box_holed_nearest(p: Prim, ro: vec3<f32>, rd: vec3<f32>, tmin: f32, tmax: f32, count: u32) -> f32 {
    var seg_lo: array<f32, 8>;
    var seg_hi: array<f32, 8>;
    seg_lo[0] = tmin;
    seg_hi[0] = tmax;
    var n = 1u;

    let start = u32(p.geo_a.w);
    for (var hi = 0u; hi < count; hi = hi + 1u) {
        let hole = holes[start + hi];
        let hs = slab_interval(hole.mn.xyz, hole.mx.xyz, ro, rd);
        if (hs.z == 0.0 || hs.y <= hs.x) {
            continue;
        }
        let h0 = hs.x;
        let h1 = hs.y;
        var nlo: array<f32, 8>;
        var nhi: array<f32, 8>;
        var m = 0u;
        for (var i = 0u; i < n; i = i + 1u) {
            let lo = seg_lo[i];
            let hgh = seg_hi[i];
            if (h1 <= lo || h0 >= hgh) { // no overlap
                if (m < 8u) { nlo[m] = lo; nhi[m] = hgh; m = m + 1u; }
                continue;
            }
            if (h0 > lo && m < 8u) { nlo[m] = lo; nhi[m] = h0; m = m + 1u; }
            if (h1 < hgh && m < 8u) { nlo[m] = h1; nhi[m] = hgh; m = m + 1u; }
        }
        for (var i = 0u; i < m; i = i + 1u) {
            seg_lo[i] = nlo[i];
            seg_hi[i] = nhi[i];
        }
        n = m;
    }

    var best = T_MISS;
    for (var i = 0u; i < n; i = i + 1u) {
        let lo = seg_lo[i];
        let hgh = seg_hi[i];
        if (lo >= RAY_EPSILON) {
            if (lo < best) { best = lo; }
        } else if (hgh >= RAY_EPSILON) {
            if (hgh < best) { best = hgh; }
        }
    }
    return best;
}

fn cyl_cap(ro: vec3<f32>, rd: vec3<f32>, cx: f32, cz: f32, radius: f32, y: f32) -> f32 {
    let t = (y - ro.y) / rd.y;
    if (t <= RAY_EPSILON) {
        return T_MISS;
    }
    let hx = ro.x + rd.x * t;
    let hz = ro.z + rd.z * t;
    let dd = (hx - cx) * (hx - cx) + (hz - cz) * (hz - cz);
    if (dd <= radius * radius) {
        return t;
    }
    return T_MISS;
}

fn hit_cylinder(p: Prim, ro: vec3<f32>, rd: vec3<f32>) -> f32 {
    let cx = p.geo_a.x;
    let cz = p.geo_a.y;
    let r0 = p.geo_a.z;
    let ymin = p.geo_a.w;
    let ymax = p.geo_b.x;
    var r1 = p.geo_b.y;
    if (r1 == 0.0) {
        r1 = r0;
    }
    let h = ymax - ymin;
    if (h <= 0.0) {
        return T_MISS;
    }
    let alpha = (r1 - r0) / h;
    let px = ro.x - cx;
    let pz = ro.z - cz;
    let A = r0 + alpha * (ro.y - ymin);
    let B = alpha * rd.y;
    let a = rd.x * rd.x + rd.z * rd.z - B * B;
    let b = 2.0 * (px * rd.x + pz * rd.z - A * B);
    let cc = px * px + pz * pz - A * A;
    var best = T_MISS;
    if (abs(a) > 1e-12) {
        let disc = b * b - 4.0 * a * cc;
        if (disc >= 0.0) {
            let sq = sqrt(disc);
            var t = (-b - sq) / (2.0 * a);
            if (t >= RAY_EPSILON) {
                let hy = ro.y + rd.y * t;
                if (hy >= ymin && hy <= ymax && t < best) {
                    best = t;
                }
            }
            t = (-b + sq) / (2.0 * a);
            if (t >= RAY_EPSILON) {
                let hy = ro.y + rd.y * t;
                if (hy >= ymin && hy <= ymax && t < best) {
                    best = t;
                }
            }
        }
    }
    if (abs(rd.y) > 1e-6) {
        if (p.geo_b.z < 0.5) {
            let tb = cyl_cap(ro, rd, cx, cz, r0, ymin);
            if (tb < best) { best = tb; }
        }
        if (p.geo_b.w < 0.5) {
            let tt = cyl_cap(ro, rd, cx, cz, r1, ymax);
            if (tt < best) { best = tt; }
        }
    }
    return best;
}

fn hit_cone(p: Prim, ro: vec3<f32>, rd: vec3<f32>) -> f32 {
    let cx = p.geo_a.x;
    let cz = p.geo_a.y;
    let rbase = p.geo_a.z;
    let ybase = p.geo_a.w;
    let ytip = p.geo_b.x;
    let h = ytip - ybase;
    let k = rbase / h;
    let ey = ro.y - ytip;
    let dx = rd.x;
    let dy = rd.y;
    let dz = rd.z;
    let ox = ro.x - cx;
    let oz = ro.z - cz;
    let a = dx * dx + dz * dz - dy * dy * k * k;
    let b = ox * dx + oz * dz - ey * dy * k * k;
    let cc = ox * ox + oz * oz - ey * ey * k * k;
    let disc = b * b - a * cc;
    if (disc < 0.0) {
        return T_MISS;
    }
    let sq = sqrt(disc);
    var t = (-b - sq) / a;
    let hy = ro.y + dy * t;
    if (t < RAY_EPSILON || hy < ybase || hy > ytip) {
        t = (-b + sq) / a;
        let hy2 = ro.y + dy * t;
        if (t < RAY_EPSILON || hy2 < ybase || hy2 > ytip) {
            if (abs(dy) < 1e-6) {
                return T_MISS;
            }
            let tc = (ybase - ro.y) / dy;
            if (tc < RAY_EPSILON) {
                return T_MISS;
            }
            let hx = ro.x + dx * tc;
            let hz = ro.z + dz * tc;
            let dd = (hx - cx) * (hx - cx) + (hz - cz) * (hz - cz);
            if (dd <= rbase * rbase) {
                return tc;
            }
            return T_MISS;
        }
    }
    return t;
}

fn torus_poly(a4: f32, a3: f32, a2: f32, a1: f32, a0: f32, t: f32) -> f32 {
    return ((a4 * t + a3) * t + a2) * t * t + a1 * t + a0;
}

fn hit_torus(p: Prim, ro: vec3<f32>, rd: vec3<f32>) -> f32 {
    let center = p.geo_a.xyz;
    let bigR = p.geo_a.w;
    let smR = p.geo_b.x;
    let e = ro - center;
    let dd = dot(rd, rd);
    let ed = dot(e, rd);
    let ee = dot(e, e);
    let rad = bigR + smR;
    if (ed * ed - dd * (ee - rad * rad) < 0.0) {
        return T_MISS;
    }
    let R2 = bigR * bigR;
    let r2 = smR * smR;
    let a4 = dd * dd;
    let a3 = 4.0 * dd * ed;
    let a2 = 2.0 * dd * (ee - r2 - R2) + 4.0 * ed * ed + 4.0 * R2 * rd.y * rd.y;
    let a1 = 4.0 * ed * (ee - r2 - R2) + 8.0 * R2 * e.y * rd.y;
    let a0 = (ee - r2 - R2) * (ee - r2 - R2) - 4.0 * R2 * (r2 - e.y * e.y);
    let stepw = 12.0 / 64.0;
    var prev = torus_poly(a4, a3, a2, a1, a0, RAY_EPSILON);
    for (var i = 1u; i <= 64u; i = i + 1u) {
        let t = RAY_EPSILON + f32(i) * stepw;
        let v = torus_poly(a4, a3, a2, a1, a0, t);
        if (prev * v < 0.0) {
            var lo = t - stepw;
            var hi = t;
            for (var j = 0u; j < 16u; j = j + 1u) {
                let m = (lo + hi) * 0.5;
                if (torus_poly(a4, a3, a2, a1, a0, m) * prev < 0.0) {
                    hi = m;
                } else {
                    lo = m;
                }
            }
            let tr2 = (lo + hi) * 0.5;
            if (tr2 > RAY_EPSILON) {
                return tr2;
            }
        }
        prev = v;
    }
    return T_MISS;
}

fn hit_ring(p: Prim, ro: vec3<f32>, rd: vec3<f32>) -> f32 {
    let cx = p.geo_a.x;
    let cz = p.geo_a.y;
    let radius = p.geo_a.z;
    let cy = p.geo_a.w;
    var height = p.geo_b.x;
    if (height <= 0.0) {
        height = 0.03;
    }
    let band_half = height * 0.5;
    let ymin = cy - band_half;
    let ymax = cy + band_half;
    var shell = RING_SHELL;
    let min_shell = radius * 0.02;
    if (shell < min_shell) {
        shell = min_shell;
    }

    var best = T_MISS;
    let ox = ro.x - cx;
    let oz = ro.z - cz;
    let a = rd.x * rd.x + rd.z * rd.z;
    if (a > 1e-12) {
        let b = 2.0 * (ox * rd.x + oz * rd.z);
        let cc = ox * ox + oz * oz - radius * radius;
        let disc = b * b - 4.0 * a * cc;
        if (disc >= 0.0) {
            let sq = sqrt(disc);
            for (var k = 0u; k < 2u; k = k + 1u) {
                let t = (select(-sq, sq, k == 1u) - b) / (2.0 * a);
                if (t < RAY_EPSILON || t >= best) {
                    continue;
                }
                let y = ro.y + rd.y * t;
                if (y < ymin - 1e-6 || y > ymax + 1e-6) {
                    continue;
                }
                let px = ro.x + rd.x * t;
                let pz = ro.z + rd.z * t;
                let d = sqrt((px - cx) * (px - cx) + (pz - cz) * (pz - cz));
                if (abs(d - radius) <= shell) {
                    best = t;
                }
            }
        }
    }

    if (abs(rd.y) > 1e-6) {
        for (var k = 0u; k < 2u; k = k + 1u) {
            let yp = select(ymin, ymax, k == 1u);
            let t = (yp - ro.y) / rd.y;
            if (t < RAY_EPSILON || t >= best) {
                continue;
            }
            let px = ro.x + rd.x * t;
            let pz = ro.z + rd.z * t;
            let d = sqrt((px - cx) * (px - cx) + (pz - cz) * (pz - cz));
            if (abs(d - radius) <= shell) {
                best = t;
            }
        }
    }
    return best;
}

fn lens_sphere_cap(ro: vec3<f32>, rd: vec3<f32>, cy: f32, radius: f32, neg_facing: bool) -> f32 {
    let oc = vec3<f32>(ro.x, ro.y - cy, ro.z);
    let b = dot(oc, rd);
    let c = dot(oc, oc) - radius * radius;
    let disc = b * b - c;
    if (disc < 0.0) {
        return T_MISS;
    }
    let sq = sqrt(disc);
    var best = T_MISS;
    for (var k = 0u; k < 2u; k = k + 1u) {
        let t = (select(-sq, sq, k == 1u) - b);
        if (t < RAY_EPSILON || t >= best) {
            continue;
        }
        let py = ro.y + rd.y * t;
        if (neg_facing) {
            if (py > cy + 1e-6) {
                continue;
            }
        } else if (py < cy - 1e-6) {
            continue;
        }
        best = t;
    }
    return best;
}

fn hit_lens(p: Prim, ro: vec3<f32>, rd: vec3<f32>) -> f32 {
    let cx = p.geo_a.x;
    let cy = p.geo_a.y;
    let cz = p.geo_a.z;
    let aperture = p.geo_a.w;
    let r_front = p.geo_b.x;
    let r_back = p.geo_b.y;
    var thickness = p.geo_b.z;
    if (thickness <= 0.0) {
        thickness = 0.004;
    }
    if (aperture <= 0.0 || r_front <= 0.0 || r_back <= 0.0) {
        return T_MISS;
    }
    let half_t = thickness * 0.5;
    let y_front = cy - half_t + r_front;
    let y_back = cy + half_t - r_back;
    var best = T_MISS;
    let t_front = lens_sphere_cap(ro, rd, y_front, r_front, true);
    let t_back = lens_sphere_cap(ro, rd, y_back, r_back, false);
    for (var k = 0u; k < 2u; k = k + 1u) {
        let t = select(t_front, t_back, k == 1u);
        if (t >= best) {
            continue;
        }
        let px = ro.x + rd.x * t;
        let py = ro.y + rd.y * t;
        let pz = ro.z + rd.z * t;
        let dx = px - cx;
        let dz = pz - cz;
        if (dx * dx + dz * dz > aperture * aperture + 1e-9) {
            continue;
        }
        _ = py;
        best = t;
    }
    return best;
}

// xf_to_local_point maps a world point into a transformed primitive's local
// space: inv * (w - t). The world->local rotation rows live in xf0..xf2.xyz and
// the translation in their .w lanes. Mirrors scene.Transform.LocalRay.
fn xf_to_local_point(p: Prim, w: vec3<f32>) -> vec3<f32> {
    let d = w - vec3<f32>(p.xf0.w, p.xf1.w, p.xf2.w);
    return vec3<f32>(dot(p.xf0.xyz, d), dot(p.xf1.xyz, d), dot(p.xf2.xyz, d));
}

// xf_to_local_dir rotates a world direction into local space (no translation).
// Rotation is orthonormal so length is preserved and the hit parameter t is
// identical in both spaces.
fn xf_to_local_dir(p: Prim, w: vec3<f32>) -> vec3<f32> {
    return vec3<f32>(dot(p.xf0.xyz, w), dot(p.xf1.xyz, w), dot(p.xf2.xyz, w));
}

// xf_to_world_normal rotates a local normal back to world space using the
// local->world rotation (= inv transpose, whose columns are the inv rows).
fn xf_to_world_normal(p: Prim, ln: vec3<f32>) -> vec3<f32> {
    return vec3<f32>(
        p.xf0.x * ln.x + p.xf1.x * ln.y + p.xf2.x * ln.z,
        p.xf0.y * ln.x + p.xf1.y * ln.y + p.xf2.y * ln.z,
        p.xf0.z * ln.x + p.xf1.z * ln.y + p.xf2.z * ln.z,
    );
}

fn hit_prim(p: Prim, ro: vec3<f32>, rd: vec3<f32>) -> f32 {
    var lro = ro;
    var lrd = rd;
    if ((p.info.w & PRIM_FLAG_TRANSFORMED) != 0u) {
        lro = xf_to_local_point(p, ro);
        lrd = xf_to_local_dir(p, rd);
    }
    let kind = p.info.x;
    if (kind == PRIM_SPHERE) {
        return hit_sphere(p, lro, lrd);
    }
    if (kind == PRIM_PLANE) {
        return hit_plane(p, lro, lrd);
    }
    if (kind == PRIM_BOX) {
        return hit_box(p, lro, lrd);
    }
    if (kind == PRIM_CYLINDER) {
        return hit_cylinder(p, lro, lrd);
    }
    if (kind == PRIM_CONE) {
        return hit_cone(p, lro, lrd);
    }
    if (kind == PRIM_RING) {
        return hit_ring(p, lro, lrd);
    }
    if (kind == PRIM_LENS) {
        return hit_lens(p, lro, lrd);
    }
    return hit_torus(p, lro, lrd);
}

fn intersect(idx: u32, ro: vec3<f32>, rd: vec3<f32>) -> f32 {
    return hit_prim(prims[idx], ro, rd);
}

fn intersect_blocker(idx: u32, ro: vec3<f32>, rd: vec3<f32>) -> f32 {
    return hit_prim(blockers[idx], ro, rd);
}

fn slab_hit(bmin: vec3<f32>, bmax: vec3<f32>, ro: vec3<f32>, inv: vec3<f32>, t_max: f32) -> bool {
    var t1 = (bmin.x - ro.x) * inv.x;
    var t2 = (bmax.x - ro.x) * inv.x;
    if (t1 > t2) {
        let tmp = t1;
        t1 = t2;
        t2 = tmp;
    }
    var t3 = (bmin.y - ro.y) * inv.y;
    var t4 = (bmax.y - ro.y) * inv.y;
    if (t3 > t4) {
        let tmp = t3;
        t3 = t4;
        t4 = tmp;
    }
    var t5 = (bmin.z - ro.z) * inv.z;
    var t6 = (bmax.z - ro.z) * inv.z;
    if (t5 > t6) {
        let tmp = t5;
        t5 = t6;
        t6 = tmp;
    }
    let tn = max(max(t1, t3), t5);
    let tf = min(min(t2, t4), t6);
    return !(tf < tn || tf < RAY_EPSILON || tn > t_max);
}
