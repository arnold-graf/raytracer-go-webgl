// terrain_zone.wesl — Terrain polygons with procedural cobble/stone surfaces.
// Blends albedo and perturbs normals from a 2D height field; no geometric displacement.

import package::types::{TEX_DIRT, TerrainZone, params, terrain_zone_verts, terrain_zones, terrains};
import package::math::{mix3, smoothstepf};
import package::texture::{cobble_height_2d, tex_cobble_2d, texture_eval};

const TERRAIN_ZONE_BUMP_DEFAULT: f32 = 1.0;
const TERRAIN_ZONE_CELL_DEFAULT: f32 = 0.34;
const TERRAIN_ZONE_FADE_GRASS: u32 = 0u;
const TERRAIN_ZONE_FADE_DIRT: u32 = 1u;

// zone_stone_uv maps world XZ to cobble texture space (meters, optional Y rotation).
fn zone_stone_uv(xz: vec2<f32>, angle: f32) -> vec2<f32> {
    let c = cos(angle);
    let s = sin(angle);
    return vec2<f32>(xz.x * c + xz.y * s, -xz.x * s + xz.y * c);
}

fn zone_cell_size(scale: f32) -> f32 {
    if (scale <= 0.0) {
        return TERRAIN_ZONE_CELL_DEFAULT;
    }
    return scale;
}

fn dist_point_segment(p: vec2<f32>, a: vec2<f32>, b: vec2<f32>) -> f32 {
    let ab = b - a;
    let denom = dot(ab, ab);
    if (denom <= 1e-12) {
        return length(p - a);
    }
    let t = clamp(dot(p - a, ab) / denom, 0.0, 1.0);
    return length(p - a - ab * t);
}

// point_in_polygon reports whether xz lies inside a simple polygon (CCW winding).
fn point_in_polygon(xz: vec2<f32>, base: u32, n: u32) -> bool {
    var winding = 0;
    for (var i = 0u; i < n; i = i + 1u) {
        let v0 = terrain_zone_verts[base + i].pos.xy;
        let v1 = terrain_zone_verts[base + ((i + 1u) % n)].pos.xy;
        if (v0.y <= xz.y) {
            if (v1.y > xz.y) {
                let cross = (v1.x - v0.x) * (xz.y - v0.y) - (xz.x - v0.x) * (v1.y - v0.y);
                if (cross > 0.0) {
                    winding = winding + 1;
                }
            }
        } else if (v1.y <= xz.y) {
            let cross = (v1.x - v0.x) * (xz.y - v0.y) - (xz.x - v0.x) * (v1.y - v0.y);
            if (cross < 0.0) {
                winding = winding - 1;
            }
        }
    }
    return winding != 0;
}

// Polygon weight: 1 inside, smooth fade outside over fade_width.
fn zone_weight_poly(xz: vec2<f32>, z: TerrainZone) -> f32 {
    let b = z.bounds;
    if (xz.x < b.x || xz.x > b.z || xz.y < b.y || xz.y > b.w) {
        return 0.0;
    }
    let n = z.info.y;
    if (n < 3u) {
        return 0.0;
    }
    let base = z.info.x;
    if (point_in_polygon(xz, base, n)) {
        return 1.0;
    }
    var d_out = 1e9;
    for (var j = 0u; j < n; j = j + 1u) {
        let a = terrain_zone_verts[base + j].pos.xy;
        let b2 = terrain_zone_verts[base + ((j + 1u) % n)].pos.xy;
        let d = dist_point_segment(xz, a, b2);
        if (d < d_out) {
            d_out = d;
        }
    }
    let fade = max(z.params.x, 1e-4);
    return 1.0 - smoothstepf(0.0, fade, d_out);
}

fn terrain_zone_pick(i: u32, xz: vec2<f32>) -> vec4<f32> {
    // xyzw = weight, texture as f32 bits unused — return weight in x, zone index in y
    let tr = terrains[i];
    let zbase = tr.zones.x;
    let zcount = tr.zones.y;
    var best_w = 0.0;
    var best_idx = -1;
    for (var zi = 0u; zi < zcount; zi = zi + 1u) {
        let z = terrain_zones[zbase + zi];
        let w = zone_weight_poly(xz, z);
        if (w > best_w) {
            best_w = w;
            best_idx = i32(zi);
        }
    }
    return vec4<f32>(best_w, f32(best_idx), 0.0, 0.0);
}

fn terrain_zone_at(i: u32, pick: i32) -> TerrainZone {
    let tr = terrains[i];
    return terrain_zones[tr.zones.x + u32(pick)];
}

fn terrain_zone_bump_normal(p: vec3<f32>, base_n: vec3<f32>, xz: vec2<f32>, z: TerrainZone, w: f32) -> vec3<f32> {
    if (w <= 0.001) {
        return base_n;
    }
    var bump = z.params.w;
    if (bump <= 0.0) {
        bump = TERRAIN_ZONE_BUMP_DEFAULT;
    }
    let uv = zone_stone_uv(xz, z.params.z);
    let cell = zone_cell_size(z.params.y);
    let eps = cell * 0.035;
    let h_u = cobble_height_2d(p, uv.x + eps, uv.y, cell) - cobble_height_2d(p, uv.x - eps, uv.y, cell);
    let h_v = cobble_height_2d(p, uv.x, uv.y + eps, cell) - cobble_height_2d(p, uv.x, uv.y - eps, cell);
    var tu = normalize(vec3<f32>(1.0, 0.0, 0.0));
    var tv = normalize(cross(base_n, tu));
    tu = normalize(cross(tv, base_n));
    let dhd_u = h_u / (2.0 * eps);
    let dhd_v = h_v / (2.0 * eps);
    let tu_b = tu + base_n * (bump * dhd_u);
    let tv_b = tv + base_n * (bump * dhd_v);
    let bumped = normalize(cross(tv_b, tu_b));
    return normalize(mix(base_n, bumped, w));
}

fn terrain_zone_albedo(i: u32, p: vec3<f32>, n: vec3<f32>, base: vec3<f32>) -> vec3<f32> {
    let pick = terrain_zone_pick(i, p.xz);
    let w = pick.x;
    if (w <= 0.001 || pick.y < 0.0) {
        return base;
    }
    let z = terrain_zone_at(i, i32(pick.y));
    let uv = zone_stone_uv(p.xz, z.params.z);
    let cell = zone_cell_size(z.params.y);
    var under = base;
    if (z.info.w == TERRAIN_ZONE_FADE_DIRT) {
        under = texture_eval(TEX_DIRT, p, n, vec3<f32>(1.0, 1.0, 1.0));
    }
    let stone = tex_cobble_2d(p, uv.x, uv.y, cell) * z.albedo.xyz;
    return mix3(under, stone, w);
}

fn terrain_zone_shade_normal(i: u32, p: vec3<f32>, base_n: vec3<f32>) -> vec3<f32> {
    let pick = terrain_zone_pick(i, p.xz);
    if (pick.x <= 0.001 || pick.y < 0.0) {
        return base_n;
    }
    let z = terrain_zone_at(i, i32(pick.y));
    return terrain_zone_bump_normal(p, base_n, p.xz, z, pick.x);
}

// terrain_zone_surface returns rough, reflect, specular, shininess scaled by zone weight.
fn terrain_zone_surface(i: u32, xz: vec2<f32>) -> vec4<f32> {
    let pick = terrain_zone_pick(i, xz);
    if (pick.x <= 0.001 || pick.y < 0.0) {
        return vec4<f32>(0.0, 0.0, 0.0, 0.0);
    }
    let z = terrain_zone_at(i, i32(pick.y));
    let w = pick.x;
    return vec4<f32>(z.surf.x * w, z.surf.y * w, z.surf.z * w, z.surf.w);
}
