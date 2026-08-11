// flame.wesl — Procedural volumetric campfire flames and heat shimmer.

import package::types::{CampfireParams, RAY_EPSILON, campfires, params};
import package::math::{clamp01, fbm, smoothstepf, turbulence};

const FLAME_BASE_Y: f32 = -0.40;
const FLAME_HEIGHT: f32 = 1.4;
const FLAME_BRIGHTNESS: f32 = 6.0;
const FLAME_MARCH_STEPS: u32 = 20u;
const SHIMMER_STRENGTH: f32 = 0.05;
const HEAT_TOP: f32 = 3.0;
const FLAME_RAD_BASE: f32 = 0.10;
const FLAME_RAD_GROWTH: f32 = 0.38;
const FLAME_RAD_TIP: f32 = 0.04;
const FLAME_TOP_FADE_START: f32 = 0.55;
const FLAME_TOP_FADE_END: f32 = 1.0;
const FLAME_TIP_WISP_START: f32 = 0.65;

fn campfire_flame_enabled(cf: CampfireParams) -> bool {
    return cf.phase.y > 0.5;
}

fn campfire_flame_scale(cf: CampfireParams) -> f32 {
    let s = cf.phase.z;
    if (s <= 0.0) {
        return 1.0;
    }
    return s;
}

fn flame_density_at(p: vec3<f32>, cf: CampfireParams, ts: f32) -> f32 {
    let scale = campfire_flame_scale(cf);
    let base = cf.core.xyz;
    let local = p - base;
    let ly = local.y - FLAME_BASE_Y * scale;
    if (ly < 0.0 || ly > FLAME_HEIGHT * scale * 1.1) {
        return 0.0;
    }
    let h = clamp01(ly / (FLAME_HEIGHT * scale));
    let radial = length(vec2<f32>(local.x, local.z));
    // Taper toward a narrow tip instead of a flat circular cap.
    let radius_at = mix((FLAME_RAD_BASE + h * FLAME_RAD_GROWTH) * scale, FLAME_RAD_TIP * scale, h * h);
    let envelope = smoothstepf(radius_at, radius_at * 0.2, radial);
    if (envelope <= 0.0) {
        return 0.0;
    }
    let vert = smoothstepf(0.0, 0.08, h) * (1.0 - smoothstepf(FLAME_TOP_FADE_START, FLAME_TOP_FADE_END, h));
    let ph = cf.phase.x;
    let anim = ts * cf.param.w;
    let nx = local.x * 3.4 + ph + anim * 0.85;
    let ny = ly * 5.2 - anim * 2.3;
    let nz = local.z * 3.4 + ph * 1.37 + anim * 0.65;
    let turb = turbulence(nx, ny, nz, 4u);
    let noise = fbm(nx * 0.9, ny * 1.1, nz * 0.9, 3u);
    let shape = mix(noise, turb, 0.5);
    let carve = smoothstepf(0.12, 0.48, shape + 0.22 * turb);
    let tip_wisp = 1.0 - smoothstepf(FLAME_TIP_WISP_START, 1.0, h) * 0.45;
    return envelope * vert * carve * tip_wisp;
}

fn flame_color_at(p: vec3<f32>, cf: CampfireParams) -> vec3<f32> {
    let scale = campfire_flame_scale(cf);
    let base = cf.core.xyz;
    let ly = p.y - base.y - FLAME_BASE_Y * scale;
    let h = clamp01(ly / (FLAME_HEIGHT * scale));
    var col: vec3<f32>;
    if (h < 0.35) {
        col = mix(cf.flame_ember.xyz, cf.flame_mid.xyz, h / 0.35);
    } else if (h < 0.75) {
        col = mix(cf.flame_mid.xyz, cf.flame_tip.xyz, (h - 0.35) / 0.4);
    } else {
        col = mix(cf.flame_tip.xyz, cf.flame_ash.xyz, (h - 0.75) / 0.25);
    }
    return col;
}

fn flame_bounds_t(ro: vec3<f32>, rd: vec3<f32>, cf: CampfireParams, max_t: f32) -> vec2<f32> {
    let scale = campfire_flame_scale(cf);
    let center_y = (FLAME_BASE_Y + FLAME_HEIGHT * 0.5) * scale;
    let center = cf.core.xyz + vec3<f32>(0.0, center_y, 0.0);
    // Sphere must enclose the full tapered volume (height + widest radial extent).
    let half_h = FLAME_HEIGHT * 0.5 * scale;
    let max_rad = (FLAME_RAD_BASE + FLAME_RAD_GROWTH) * scale;
    let radius = sqrt(half_h * half_h + max_rad * max_rad) + 0.08;
    let oc = ro - center;
    let b = dot(oc, rd);
    let c = dot(oc, oc) - radius * radius;
    let disc = b * b - c;
    if (disc < 0.0) {
        return vec2<f32>(-1.0, -1.0);
    }
    let sq = sqrt(disc);
    var t0 = -b - sq;
    var t1 = -b + sq;
    if (t0 < RAY_EPSILON) {
        t0 = RAY_EPSILON;
    }
    if (t1 > max_t) {
        t1 = max_t;
    }
    if (t1 <= t0) {
        return vec2<f32>(-1.0, -1.0);
    }
    return vec2<f32>(t0, t1);
}

fn accumulate_flame(ro: vec3<f32>, rd: vec3<f32>, max_t: f32, ts: f32) -> vec3<f32> {
    var accum = vec3<f32>(0.0, 0.0, 0.0);
    let n = params.campfire_count;
    for (var ci = 0u; ci < n; ci = ci + 1u) {
        let cf = campfires[ci];
        if (!campfire_flame_enabled(cf)) {
            continue;
        }
        let bounds = flame_bounds_t(ro, rd, cf, max_t);
        if (bounds.x < 0.0) {
            continue;
        }
        let span = bounds.y - bounds.x;
        let step_len = span / f32(FLAME_MARCH_STEPS);
        var pos = ro + rd * (bounds.x + step_len * 0.5);
        let ph = cf.phase.x;
        let flick = 0.6 * sin(ts * 7.0 * cf.param.w + ph) + 0.3 * sin(ts * 13.0 * cf.param.w + ph * 2.1);
        let flick_gain = 0.85 + 0.25 * flick * cf.param.z;
        for (var s = 0u; s < FLAME_MARCH_STEPS; s = s + 1u) {
            let d = flame_density_at(pos, cf, ts);
            if (d > 0.002) {
                let col = flame_color_at(pos, cf);
                accum = accum + col * d * step_len * flick_gain * FLAME_BRIGHTNESS;
            }
            pos = pos + rd * step_len;
        }
    }
    return accum;
}

fn heat_column_strength(p: vec3<f32>, cf: CampfireParams) -> f32 {
    let scale = campfire_flame_scale(cf);
    let base = cf.core.xyz;
    let ly = p.y - base.y;
    if (ly < -0.25 * scale || ly > HEAT_TOP * scale) {
        return 0.0;
    }
    let radial = length(vec2<f32>(p.x - base.x, p.z - base.z));
    let hnorm = clamp01((ly + 0.3 * scale) / (HEAT_TOP * scale));
    let radius_at = (0.12 + hnorm * 0.55) * scale;
    let radial_w = smoothstepf(radius_at, radius_at * 0.2, radial);
    let vert_w = smoothstepf(0.0, 0.15, ly + 0.25) * (1.0 - smoothstepf(0.9, 1.0, hnorm));
    return radial_w * vert_w;
}

fn heat_shimmer_dir(ro: vec3<f32>, rd: vec3<f32>, ts: f32, occlude_t: f32, occluded: bool) -> vec3<f32> {
    var out = rd;
    let n = params.campfire_count;
    for (var ci = 0u; ci < n; ci = ci + 1u) {
        let cf = campfires[ci];
        if (!campfire_flame_enabled(cf)) {
            continue;
        }
        let base = cf.core.xyz;
        let to_base = base - ro;
        let t_ray = dot(to_base, rd);
        if (t_ray < 0.0) {
            continue;
        }
        // Skip shimmer when scene geometry blocks the path to this heat column.
        if (occluded && occlude_t < t_ray - RAY_EPSILON) {
            continue;
        }
        let closest = ro + rd * t_ray;
        let strength = heat_column_strength(closest, cf);
        if (strength <= 0.0) {
            continue;
        }
        let ph = cf.phase.x;
        let anim = ts * cf.param.w;
        let nx = (closest.x - base.x) * 2.8 + anim * 1.4 + ph;
        let ny = (closest.y - base.y) * 3.5 - anim * 2.0;
        let nz = (closest.z - base.z) * 2.8 + anim * 0.85 + ph * 1.2;
        let eps = 0.07;
        let gx = fbm(nx + eps, ny, nz, 3u) - fbm(nx - eps, ny, nz, 3u);
        let gy = fbm(nx, ny + eps, nz, 3u) - fbm(nx, ny - eps, nz, 3u);
        let gz = fbm(nx, ny, nz + eps, 3u) - fbm(nx, ny, nz - eps, 3u);
        var tangent = normalize(cross(vec3<f32>(0.0, 1.0, 0.0), rd));
        if (length(tangent) < 0.001) {
            tangent = vec3<f32>(1.0, 0.0, 0.0);
        }
        let bitangent = normalize(cross(rd, tangent));
        let distort = tangent * gx * 1.2 + bitangent * gy * 1.2 + vec3<f32>(0.0, gz * 0.35, 0.0);
        let amp = SHIMMER_STRENGTH * strength * (0.6 + 0.4 * cf.param.z);
        out = normalize(out + distort * amp);
    }
    return out;
}
