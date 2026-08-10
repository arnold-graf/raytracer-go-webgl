// flame.wesl — Campfire particle flames (emissive spheres, not in the BVH).

import package::types::{HIT_NO_INSTANCE, Hit, RAY_EPSILON, T_MISS, flame_particles, params};

fn ray_sphere_ro(ro: vec3<f32>, rd: vec3<f32>, center: vec3<f32>, radius: f32) -> f32 {
    let oc = ro - center;
    let b = dot(oc, rd);
    let c = dot(oc, oc) - radius * radius;
    let disc = b * b - c;
    if (disc < 0.0) {
        return T_MISS;
    }
    let sq = sqrt(disc);
    var t = -b - sq;
    if (t < RAY_EPSILON) {
        t = -b + sq;
    }
    if (t < RAY_EPSILON) {
        return T_MISS;
    }
    return t;
}

fn nearest_flame_hit(ro: vec3<f32>, rd: vec3<f32>, h: Hit) -> Hit {
    var best = h;
    let n = params.flame_particle_count;
    for (var i = 0u; i < n; i = i + 1u) {
        let fp = flame_particles[i];
        let radius = fp.pos.w;
        if (radius <= 0.0) {
            continue;
        }
        let t = ray_sphere_ro(ro, rd, fp.pos.xyz, radius);
        if (t < best.t) {
            best.t = t;
            best.idx = i;
            best.kind = 3u;
            best.inst_idx = HIT_NO_INSTANCE;
        }
    }
    return best;
}
