// Megakernel path tracer: camera rays, analytic primitive intersection, flat
// ambient diffuse shading, point lights, hard shadows, emissive passthrough,
// and the procedural sky on a miss.
//
// This shader is the only renderer. Comments that mention "the CPU" or the
// original JS renderer are historical: the algorithms were first written in JS
// (realtime_raytracer_dos_geo.html) and later in a Go CPU tracer that has since
// been removed; this WGSL was kept byte-parity with that tracer during the port.

struct Params {
    width: u32,
    height: u32,
    prim_count: u32,
    light_count: u32,
    blocker_count: u32,
    shadows: u32,
    bvh_node_count: u32,
    blocker_bvh_node_count: u32,
    terrain_count: u32,
    water_count: u32,
	time: f32,
    max_bounce_depth: u32,
    aspect: f32,
    fov_scale: f32,
    ambient: f32,
    mirror: u32, // 1 when reflections/refractions are enabled (tr.Opts.Mirror)
    cam_pos: vec4<f32>,
    fwd: vec4<f32>,
    right: vec4<f32>,
    up: vec4<f32>,
    campfire_count: u32,
    ao_enabled: u32,
    ao_nx: u32,
    ao_ny: u32,
    ao_nz: u32,
    ao_inv: f32,
    ao_cell: f32,
    ao_bias: f32,
    ao_min: vec4<f32>,
    sky: u32, // selects the procedural sky variant (see SKY_* / scene.Sky*)
    // Visible celestial body (sun/moon disc). body_enabled gates it; body_dir
    // points from the camera toward the body; body_cos_radius is cos(angular
    // radius); body_glow scales the halo; body_color.xyz is the disc radiance.
    body_enabled: u32,
    body_cos_radius: f32,
    body_glow: f32,
    body_dir: vec4<f32>,
    body_color: vec4<f32>,
    // color_quant: 0 = 8-bit dither only, 1 = 15-bit (5-5-5), 2 = 256-color (3-3-2).
    color_quant: u32,
    capture_loaded: u32,
    capture_w: u32,
    capture_h: u32,
    inst_template_count: u32,
    inst_count: u32,
    inst_node_base: u32,
    inst_node_count: u32,
    blocker_section_start: u32,
    blocker_inst_base: u32,
	blocker_inst_count: u32,
    profile_enabled: u32,
};

// Prim mirrors GPUPrimitive in scene.go (std430, 144-byte stride).
struct Prim {
    geo_a: vec4<f32>,   // sphere: center.xyz, radius | plane: n.xyz, d | box: min.xyz, holeStart
                        // cylinder: cx, cz, radius, ymin | cone: cx, cz, rbase, ybase
                        // torus: center.xyz, majorR
                        // ring: cx, cz, radius, cy
    geo_b: vec4<f32>,   // box: max.xyz, holeCount | cylinder: ymax, radius_top | cone: ytip | torus: minorR | ring: height
    albedo: vec4<f32>,  // linear rgb in xyz
    albedo2: vec4<f32>, // checker second color
    surf: vec4<f32>,    // rough, ior, reflect, transmit
    info: vec4<u32>,    // kind, material, texture, flags (bit0 = transformed)
    xf0: vec4<f32>,     // world->local rotation row0 (xyz) + translation t.x (w)
    xf1: vec4<f32>,     // world->local rotation row1 (xyz) + translation t.y (w)
    xf2: vec4<f32>,     // world->local rotation row2 (xyz) + translation t.z (w)
};

// Hole mirrors GPUHole in scene.go (std430, 32-byte stride): one axis-aligned
// region subtracted from a box (CSG difference) in the box's local space.
struct Hole {
    mn: vec4<f32>,
    mx: vec4<f32>,
};

// CampfireParams holds a campfire's constant parameters. The campfire loop
// in shade_diffuse resolves sub-light positions and intensities from these
// each frame. Mirrors struct CampfireParams in scene.go (std430, 64-byte stride).
struct CampfireParams {
    core: vec4<f32>,  // cx, cy, cz, range
    color: vec4<f32>, // base color (r, g, b, 0) — tints applied per sub-light
    param: vec4<f32>, // brightness, jitter, flicker, speed
    phase: vec4<f32>,  // seed phase, 0, 0, 0
};

// Light mirrors GPULight in scene.go (std430, 48-byte stride).
struct Light {
    pos: vec4<f32>,
    color: vec4<f32>,
    falloff: vec4<f32>, // cullR2, invR2, _, _
};

// BVHNode mirrors GPUBVHNode in bvh.go (std430, 48-byte stride).
// info: interior -> (left, right, 0, 0), leaf -> (_, _, start, count)
struct BVHNode {
    min_b: vec4<f32>,
    max_b: vec4<f32>,
    info: vec4<u32>,
};

struct TemplateRecord {
    prim_base: u32,
    blocker_base: u32,
    blas_root: u32,
    blocker_blas_root: u32,
};

struct InstanceRecord {
    xf0: vec4<f32>,
    xf1: vec4<f32>,
    xf2: vec4<f32>,
    template_id: u32,
    _pad0: u32,
    _pad1: u32,
    _pad2: u32,
};

struct Terrain {
    bounds0: vec4<f32>,  // originX, originZ, sizeX, sizeZ
    bounds1: vec4<f32>,  // minY, maxY, step, _
    grid: vec4<u32>,     // gnx, gnz, heightOffset, normalOffset
    material: vec4<u32>, // grass, rock, snow, _
    color0: vec4<f32>,
    color1: vec4<f32>,
    color2: vec4<f32>,
    blend: vec4<f32>,    // slopeLo, slopeHi, snowLo, snowHi
};

struct Water {
    geom: vec4<f32>,   // cx, cz, radius, level
    params: vec4<f32>, // ripple, rippleSpeed, dirX, dirZ
    albedo: vec4<f32>,
    surf: vec4<f32>,
    info: vec4<u32>,   // material, texture, _, _
};

@group(0) @binding(0)
var<uniform> params: Params;

@group(0) @binding(1)
var<storage, read_write> pixels: array<u32>;

@group(0) @binding(2)
var<storage, read> prims: array<Prim>;

@group(0) @binding(3)
var<storage, read> lights: array<Light>;

@group(0) @binding(4)
var<storage, read> blockers: array<Prim>;

@group(0) @binding(5)
var<storage, read> bvh_nodes: array<BVHNode>;

@group(0) @binding(6)
var<storage, read> terrains: array<Terrain>;

@group(0) @binding(7)
var<storage, read> terrain_samples: array<vec4<f32>>;

@group(0) @binding(8)
var<storage, read> waters: array<Water>;

@group(0) @binding(9)
var<storage, read> perm: array<u32>;

@group(0) @binding(10)
var<storage, read> ao_volume: array<f32>;

@group(0) @binding(11)
var<storage, read> campfires: array<CampfireParams>;

@group(0) @binding(12)
var<storage, read> holes: array<Hole>;

@group(0) @binding(13)
var<storage, read> capture_pixels: array<u32>;

@group(0) @binding(14)
var<storage, read> inst_templates: array<TemplateRecord>;

@group(0) @binding(15)
var<storage, read> inst_records: array<InstanceRecord>;

@group(0) @binding(16)
var<storage, read_write> profile_counters: array<atomic<u32>>;

const PROF_PIXELS: u32 = 0u;
const PROF_PATH_SEGS: u32 = 1u;
const PROF_HIT_PRIM: u32 = 2u;
const PROF_HIT_INST: u32 = 3u;
const PROF_HIT_TERRAIN: u32 = 4u;
const PROF_HIT_WATER: u32 = 5u;
const PROF_SKY: u32 = 6u;
const PROF_SHADOW_RAYS: u32 = 7u;
const PROF_SHADOW_BLOCK: u32 = 8u;
const PROF_TERRAIN_STEPS: u32 = 9u;
const PROF_MIRROR_BOUNCES: u32 = 10u;
const PROF_GLASS_BOUNCES: u32 = 11u;
const PROF_DIFFUSE_REFL: u32 = 12u;
const PROF_PRI_HIT_PRIM: u32 = 13u;
const PROF_PRI_HIT_INST: u32 = 14u;
const PROF_PRI_HIT_TERRAIN: u32 = 15u;
const PROF_PRI_HIT_WATER: u32 = 16u;
const PROF_PRI_SKY: u32 = 17u;
const PROF_COUNTER_COUNT: u32 = 18u;

fn prof_inc(idx: u32, delta: u32) {
    if (params.profile_enabled != 0u && idx < PROF_COUNTER_COUNT) {
        atomicAdd(&profile_counters[idx], delta);
    }
}

const BVH_TAG_TLAS: u32 = 1u;

const PRIM_FLAG_TRANSFORMED: u32 = 1u;

const PRIM_SPHERE: u32 = 0u;
const PRIM_PLANE: u32 = 1u;
const PRIM_BOX: u32 = 2u;
const PRIM_CYLINDER: u32 = 3u;
const PRIM_CONE: u32 = 4u;
const PRIM_TORUS: u32 = 5u;
const PRIM_RING: u32 = 6u;
const PRIM_LENS: u32 = 7u;
const RING_SHELL: f32 = 0.01;
const MAT_DIFFUSE: u32 = 0u;
const MAT_MIRROR: u32 = 1u;
const MAT_METAL: u32 = 3u;
const MAT_GLASS: u32 = 4u;
const MAT_EMIT: u32 = 5u; // scene.MatEmit
const MAT_CHECKER: u32 = 6u;

// Sky variants, matching scene.Sky* in scene.go.
const SKY_CLEAR: u32 = 0u;
const SKY_CLOUDY: u32 = 1u;
const SKY_NIGHT_STARS: u32 = 2u;
const SKY_NIGHT_STORM: u32 = 3u;
const SKY_SUNSET: u32 = 4u;

const TEX_NONE: u32 = 0u;
const TEX_WOOD: u32 = 1u;
const TEX_BRICK: u32 = 2u;
const TEX_STONE: u32 = 3u;
const TEX_CEMENT: u32 = 4u;
const TEX_MARBLE: u32 = 5u;
const TEX_GRASS: u32 = 6u;
const TEX_DIRT: u32 = 7u;
const TEX_SNOW: u32 = 8u;
const TEX_WALLPAPER_NAVY: u32 = 9u;
const TEX_WALLPAPER_GREEN: u32 = 10u;
const TEX_WALLPAPER_ROSE: u32 = 11u;
const TEX_STONE_WALL: u32 = 12u;
const TEX_CAPTURE_BASE: u32 = 50u;
const TEX_CAPTURE_COUNT: u32 = 5u;

const RAY_EPSILON: f32 = 1e-4;
const SURFACE_EPSILON: f32 = 5e-4;
const T_MISS: f32 = 1e30;
const LIGHT_CULL_EPS: f32 = 0.0025;
const LIGHT_ATTEN_BASE: f32 = 0.5;
const LIGHT_ATTEN_QUADRATIC: f32 = 0.08;

struct Hit {
    t: f32,
    idx: u32,
    kind: u32,
    inst_idx: u32, // 0xffffffff when the hit is on static (non-instanced) geometry
};

const HIT_NO_INSTANCE: u32 = 0xffffffffu;

fn clamp01(x: f32) -> f32 {
    return clamp(x, 0.0, 1.0);
}

fn mix3(a: vec3<f32>, b: vec3<f32>, t: f32) -> vec3<f32> {
    return a + (b - a) * t;
}

fn smoothstepf(e0: f32, e1: f32, x: f32) -> f32 {
    let t = clamp01((x - e0) / (e1 - e0));
    return t * t * (3.0 - 2.0 * t);
}

fn fracf(x: f32) -> f32 {
    return x - floor(x);
}

// Exact port of internal/texture/noise.go: Ken Perlin's reference 3D noise over
// the uploaded permutation table, so GPU procedural textures match the CPU
// (modulo f32 vs f64 rounding) rather than approximating with value noise.
fn p_fade(t: f32) -> f32 {
    return t * t * t * (t * (t * 6.0 - 15.0) + 10.0);
}

fn p_lerp(t: f32, a: f32, b: f32) -> f32 {
    return a + t * (b - a);
}

fn p_grad(h: i32, x: f32, y: f32, z: f32) -> f32 {
    let hh = h & 15;
    var u = x;
    if (hh >= 8) { u = y; }
    var v = z;
    if (hh < 4) {
        v = y;
    } else if (hh == 12 || hh == 14) {
        v = x;
    }
    var ru = u;
    if ((hh & 1) != 0) { ru = -u; }
    var rv = v;
    if ((hh & 2) != 0) { rv = -v; }
    return ru + rv;
}

fn perlin(px: f32, py: f32, pz: f32) -> f32 {
    let xi = i32(floor(px)) & 255;
    let yi = i32(floor(py)) & 255;
    let zi = i32(floor(pz)) & 255;
    let x = px - floor(px);
    let y = py - floor(py);
    let z = pz - floor(pz);
    let u = p_fade(x);
    let v = p_fade(y);
    let w = p_fade(z);

    let a = i32(perm[u32(xi)]) + yi;
    let aa = i32(perm[u32(a)]) + zi;
    let ab = i32(perm[u32(a + 1)]) + zi;
    let b = i32(perm[u32(xi + 1)]) + yi;
    let ba = i32(perm[u32(b)]) + zi;
    let bb = i32(perm[u32(b + 1)]) + zi;

    return p_lerp(w,
        p_lerp(v,
            p_lerp(u, p_grad(i32(perm[u32(aa)]), x, y, z), p_grad(i32(perm[u32(ba)]), x - 1.0, y, z)),
            p_lerp(u, p_grad(i32(perm[u32(ab)]), x, y - 1.0, z), p_grad(i32(perm[u32(bb)]), x - 1.0, y - 1.0, z))),
        p_lerp(v,
            p_lerp(u, p_grad(i32(perm[u32(aa + 1)]), x, y, z - 1.0), p_grad(i32(perm[u32(ba + 1)]), x - 1.0, y, z - 1.0)),
            p_lerp(u, p_grad(i32(perm[u32(ab + 1)]), x, y - 1.0, z - 1.0), p_grad(i32(perm[u32(bb + 1)]), x - 1.0, y - 1.0, z - 1.0))));
}

fn fbm(x: f32, y: f32, z: f32, octaves: u32) -> f32 {
    var sum = 0.0;
    var amp = 1.0;
    var freq = 1.0;
    var norm = 0.0;
    for (var i = 0u; i < octaves; i = i + 1u) {
        sum = sum + amp * perlin(x * freq, y * freq, z * freq);
        norm = norm + amp;
        amp = amp * 0.5;
        freq = freq * 2.0;
    }
    if (norm == 0.0) { return 0.0; }
    return sum / norm;
}

fn turbulence(x: f32, y: f32, z: f32, octaves: u32) -> f32 {
    var sum = 0.0;
    var amp = 1.0;
    var freq = 1.0;
    var norm = 0.0;
    for (var i = 0u; i < octaves; i = i + 1u) {
        sum = sum + amp * abs(perlin(x * freq, y * freq, z * freq));
        norm = norm + amp;
        amp = amp * 0.5;
        freq = freq * 2.0;
    }
    if (norm == 0.0) { return 0.0; }
    return sum / norm;
}

// cell_rand is a bit-exact port of internal/texture/texture.go's integer hash,
// so brick walls match the CPU. u32 wrap-around arithmetic is identical on both
// sides (unlike the old frac(sin*43758) hash, which diverged under f32).
fn cell_rand(c: f32, r: f32, seed: f32) -> f32 {
    var h = u32(i32(c)) * 0x27d4eb2du + u32(i32(r)) * 0x9e3779b1u + u32(i32(seed)) * 0x85ebca6bu;
    h = h ^ (h >> 15u);
    h = h * 0x2c1b3c6du;
    h = h ^ (h >> 13u);
    h = h * 0x297a2d39u;
    h = h ^ (h >> 16u);
    return f32(h >> 8u) / 16777216.0;
}

// The texture functions below are faithful ports of internal/texture/*.go.
const TEX_PI: f32 = 3.141592653589793;

fn tex_wood(p: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let dist = length(vec2<f32>(p.y, p.z));
    let g = dist * 2.2 + 0.6 * turbulence(p.x * 0.6, p.y * 1.5, p.z * 1.5, 4u);
    let rings = 0.5 + 0.5 * sin(g * 2.0 * TEX_PI * TEX_PI);
    let light = vec3<f32>(0.58, 0.38, 0.19);
    let dark = vec3<f32>(0.33, 0.19, 0.09);
    let streak = 0.85 + 0.15 * perlin(p.x * 12.0, p.y * 2.0, p.z * 2.0);
    return mix3(dark, light, rings) * streak * tint;
}

fn brick_palette(i: i32) -> vec3<f32> {
    var pal = array<vec3<f32>, 6>(
        vec3<f32>(0.28, 0.10, 0.07),
        vec3<f32>(0.22, 0.08, 0.06),
        vec3<f32>(0.34, 0.15, 0.09),
        vec3<f32>(0.18, 0.09, 0.07),
        vec3<f32>(0.13, 0.07, 0.06),
        vec3<f32>(0.25, 0.13, 0.10),
    );
    var idx = i;
    if (idx < 0) { idx = 0; }
    if (idx > 5) { idx = 5; }
    return pal[idx];
}

fn tex_brick(p: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let brick_w = 0.5;
    let brick_h = 0.22;
    let mortar = 0.05;
    let row = floor(p.y / brick_h);
    var x = p.x;
    if ((i32(row) & 1) == 1) { x = x + brick_w * 0.5; }
    let col = floor(x / brick_w);

    let pick = cell_rand(col, row, 1.0);
    let bright = cell_rand(col, row, 2.0);
    let desat = cell_rand(col, row, 3.0);
    var decay = cell_rand(col, row, 4.0);
    decay = decay * decay;

    var base = brick_palette(i32(pick * 6.0));
    base = base * (0.5 + 0.45 * bright);
    base.x = base.x * (0.88 + 0.24 * cell_rand(col, row, 5.0));
    base.y = base.y * (0.85 + 0.30 * cell_rand(col, row, 6.0));
    base.z = base.z * (0.85 + 0.30 * cell_rand(col, row, 7.0));
    let gg = (base.x + base.y + base.z) / 3.0;
    base = mix3(base, vec3<f32>(gg, gg, gg), 0.5 * desat * decay);

    let mottle = 0.78 + 0.22 * fbm(p.x * 9.0 + col * 4.0, p.y * 9.0 + row * 4.0, p.z * 9.0, 3u);
    let grain = 0.78 + 0.22 * fbm(p.x * 40.0, p.y * 40.0, p.z * 40.0, 4u);
    let stain = fbm(p.x * 5.0 + col * 7.3, p.y * 5.0 + row * 3.1, p.z * 5.0, 4u);
    let weather = 1.0 - decay * 0.55 * smoothstepf(-0.3, 0.6, stain);
    var face = base * (mottle * grain * weather * (1.0 - 0.3 * decay));

    let crack = smoothstepf(0.5, 0.72, turbulence(p.x * 9.0 + col, p.y * 9.0 + row, p.z * 9.0, 4u));
    face = face * (1.0 - 0.7 * crack * (0.25 + 0.75 * decay));

    let mortar_col = vec3<f32>(0.20, 0.19, 0.17) * (0.75 + 0.4 * fbm(p.x * 7.0, p.y * 7.0, p.z * 7.0, 3u));

    let mx = mortar / brick_w;
    let my = mortar / brick_h;
    let erode = 1.0 + 3.0 * decay;
    let ex = mx * erode * (0.8 + 0.4 * perlin(p.x * 15.0, p.y * 15.0, p.z * 15.0));
    let ey = my * erode * (0.8 + 0.4 * perlin(p.x * 15.0 + 5.0, p.y * 15.0 + 5.0, p.z * 15.0));
    let fx = fracf(x / brick_w);
    let fy = fracf(p.y / brick_h);
    let mask = smoothstepf(0.0, ex, fx) * smoothstepf(0.0, ex, 1.0 - fx) *
        smoothstepf(0.0, ey, fy) * smoothstepf(0.0, ey, 1.0 - fy);

    return mix3(mortar_col, face, mask) * tint;
}

fn tex_stone(p: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let n = 0.5 + 0.5 * fbm(p.x * 1.5, p.y * 1.5, p.z * 1.5, 5u);
    let light = vec3<f32>(0.60, 0.58, 0.54);
    let dark = vec3<f32>(0.34, 0.33, 0.31);
    var c = mix3(dark, light, n);
    let seam = abs(perlin(p.x * 6.0, p.y * 6.0, p.z * 6.0));
    c = c * (0.7 + 0.3 * smoothstepf(0.02, 0.12, seam));
    return c * tint;
}

fn tex_cement(p: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let n = fbm(p.x * 4.0, p.y * 4.0, p.z * 4.0, 4u);
    let speck = 0.5 + 0.5 * perlin(p.x * 40.0, p.y * 40.0, p.z * 40.0);
    let g = 0.62 + 0.06 * n + 0.03 * (speck - 0.5);
    return vec3<f32>(g, g, g * 0.99) * tint;
}

fn tex_marble(p: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let t = turbulence(p.x * 1.2, p.y * 1.2, p.z * 1.2, 5u);
    let veins = 0.5 + 0.5 * sin((p.x + p.z) * 1.5 + 6.0 * t);
    let base = vec3<f32>(0.85, 0.85, 0.88);
    let vein = vec3<f32>(0.18, 0.18, 0.22);
    return mix3(vein, base, pow(veins, 0.6)) * tint;
}

fn tex_grass(p: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let patch_n = fbm(p.x * 0.6, p.y * 0.6, p.z * 0.6, 3u);
    let blade = 0.5 + 0.5 * perlin(p.x * 9.0, p.y * 9.0, p.z * 9.0);
    let lush = vec3<f32>(0.12, 0.30, 0.08);
    let dry = vec3<f32>(0.36, 0.34, 0.13);
    var c = mix3(lush, dry, smoothstepf(-0.25, 0.5, patch_n));
    c = c * (0.78 + 0.22 * blade);
    return c * tint;
}

fn tex_dirt(p: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let n = 0.5 + 0.5 * fbm(p.x * 3.0, p.y * 3.0, p.z * 3.0, 4u);
    let base = vec3<f32>(0.26, 0.17, 0.10);
    let dark = vec3<f32>(0.15, 0.10, 0.06);
    return mix3(dark, base, n) * tint;
}

fn tex_snow(p: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let drift = 0.5 + 0.5 * fbm(p.x * 1.5, p.y * 1.5, p.z * 1.5, 3u);
    let sparkle = perlin(p.x * 55.0, p.y * 55.0, p.z * 55.0);
    let v = 0.86 + 0.10 * drift;
    var c = vec3<f32>(v * 0.97, v * 0.99, min(1.0, v * 1.04));
    if (sparkle > 0.85) { c = c * 1.12; }
    return c * tint;
}

// --- wallpaper (port of internal/texture/wallpaper.go) ----------------------

fn wp_line(d: f32, w: f32) -> f32 { return 1.0 - smoothstepf(w, w + 0.010, d); }

fn wp_dot(x: f32, y: f32, r: f32) -> f32 {
    return 1.0 - smoothstepf(r, r + 0.012, length(vec2<f32>(x, y)));
}

fn wp_ring(x: f32, y: f32, cx: f32, cy: f32, rx: f32, ry: f32, t: f32) -> f32 {
    let q = length(vec2<f32>((x - cx) / rx, (y - cy) / ry));
    return 1.0 - smoothstepf(t, t + 0.20, abs(q - 1.0));
}

fn wp_fill(x: f32, y: f32, cx: f32, cy: f32, rx: f32, ry: f32) -> f32 {
    let q = length(vec2<f32>((x - cx) / rx, (y - cy) / ry));
    return 1.0 - smoothstepf(0.86, 1.02, q);
}

fn wp_band(x: f32, lo: f32, hi: f32) -> f32 {
    return smoothstepf(lo - 0.03, lo + 0.03, x) * (1.0 - smoothstepf(hi - 0.03, hi + 0.03, x));
}

fn wp_rot(x: f32, y: f32, a: f32) -> vec2<f32> {
    let c = cos(a);
    let s = sin(a);
    return vec2<f32>(x * c - y * s, x * s + y * c);
}

fn wp_iris(mx: f32, ly: f32) -> f32 {
    var g = 0.0;
    g = max(g, wp_ring(mx, ly, 0.0, 0.085, 0.052, 0.150, 0.18));
    let r0 = wp_rot(mx - 0.012, ly - 0.03, -0.62);
    g = max(g, wp_ring(r0.x, r0.y, 0.105, 0.0, 0.120, 0.046, 0.22));
    g = max(g, wp_fill(mx, ly, 0.0, -0.085, 0.030, 0.060));
    g = max(g, wp_line(mx, 0.010) * wp_band(ly, -0.40, -0.10));
    return g;
}

fn wp_leaves(mx: f32, ly: f32) -> f32 {
    let r0 = wp_rot(mx - 0.035, ly + 0.085, -0.52);
    return wp_ring(r0.x, r0.y, 0.105, 0.0, 0.230, 0.034, 0.20);
}

fn wp_motif(fu: f32, fv: f32) -> f32 {
    let mx = abs(fu - 0.5);
    let ly = fv - 0.5;
    var g = 0.0;
    let sx = 0.5 * sin(TEX_PI * fv);
    let d = abs(mx - sx);
    g = max(g, wp_line(d, 0.013));
    g = max(g, 0.55 * wp_line(abs(d - 0.040), 0.005));
    g = max(g, wp_dot(mx, abs(ly) - 0.5, 0.034));
    g = max(g, wp_iris(mx, ly));
    g = max(g, wp_leaves(mx, ly));
    if (g > 1.0) { g = 1.0; }
    return g;
}

fn tex_wallpaper(p: vec3<f32>, tint: vec3<f32>, tex: u32) -> vec3<f32> {
    var bg = vec3<f32>(0.052, 0.073, 0.145);
    var ink = vec3<f32>(0.45, 0.36, 0.18);
    if (tex == TEX_WALLPAPER_GREEN) {
        bg = vec3<f32>(0.058, 0.110, 0.085);
        ink = vec3<f32>(0.46, 0.39, 0.22);
    } else if (tex == TEX_WALLPAPER_ROSE) {
        bg = vec3<f32>(0.160, 0.060, 0.078);
        ink = vec3<f32>(0.48, 0.36, 0.19);
    }
    let u = (p.x + p.z) / 0.55;
    let v = p.y / 0.775;
    let g = wp_motif(fracf(u), fracf(v)) * 0.68;
    let bgm = bg * (0.96 + 0.06 * (0.5 + 0.5 * fbm(p.x * 3.1, p.y * 3.1, p.z * 3.1, 3u)));
    let inkm = ink * (0.88 + 0.12 * (0.5 + 0.5 * perlin(p.x * 5.0 + 11.0, p.y * 5.0, p.z * 5.0)));
    return mix3(bgm, inkm, g) * tint;
}

fn stone_wall_palette(i: i32) -> vec3<f32> {
    var pal = array<vec3<f32>, 6>(
        vec3<f32>(0.80, 0.79, 0.76),
        vec3<f32>(0.78, 0.73, 0.62),
        vec3<f32>(0.64, 0.63, 0.60),
        vec3<f32>(0.72, 0.66, 0.54),
        vec3<f32>(0.74, 0.71, 0.66),
        vec3<f32>(0.55, 0.54, 0.52),
    );
    var idx = i;
    if (idx < 0) { idx = 0; }
    if (idx > 5) { idx = 5; }
    return pal[idx];
}

fn tex_stone_wall_2d(p: vec3<f32>, u_in: f32, v_in: f32) -> vec3<f32> {
    let cell = 0.34;
    let u = u_in / cell;
    let v = v_in / cell;
    let cu = floor(u);
    let cv = floor(v);
    let fu = u - cu;
    let fv = v - cv;

    var f1 = 1.0e9;
    var f2 = 1.0e9;
    var ox = 0.0;
    var oy = 0.0;
    for (var j = -1; j <= 1; j = j + 1) {
        for (var i = -1; i <= 1; i = i + 1) {
            let cx = cu + f32(i);
            let cy = cv + f32(j);
            let px = f32(i) + cell_rand(cx, cy, 11.0);
            let py = f32(j) + cell_rand(cx, cy, 12.0);
            let dx = px - fu;
            let dy = py - fv;
            let d = sqrt(dx * dx + dy * dy);
            if (d < f1) {
                f2 = f1;
                f1 = d;
                ox = cx;
                oy = cy;
            } else if (d < f2) {
                f2 = d;
            }
        }
    }
    let edge = f2 - f1;

    let pick = cell_rand(ox, oy, 1.0);
    let bright = cell_rand(ox, oy, 2.0);
    var base = stone_wall_palette(i32(pick * 6.0));
    base = base * (0.62 + 0.55 * bright);
    base.x = base.x * (0.94 + 0.12 * cell_rand(ox, oy, 5.0));
    base.y = base.y * (0.94 + 0.12 * cell_rand(ox, oy, 6.0));
    base.z = base.z * (0.92 + 0.14 * cell_rand(ox, oy, 7.0));

    let mottle = 0.84 + 0.16 * fbm(p.x * 7.0 + ox * 3.0, p.y * 7.0 + oy * 3.0, p.z * 7.0, 4u);
    let grain = 0.90 + 0.10 * fbm(p.x * 32.0, p.y * 32.0, p.z * 32.0, 3u);
    let relief = 1.0 - 0.18 * smoothstepf(0.05, 0.5, f1);
    var face = base * (mottle * grain * relief);

    let mortar_col = vec3<f32>(0.40, 0.37, 0.31) * (0.78 + 0.30 * fbm(p.x * 6.0, p.y * 6.0, p.z * 6.0, 3u));

    let mask = smoothstepf(0.02, 0.12, edge);
    return mix3(mortar_col, face, mask);
}

fn tex_stone_wall(p: vec3<f32>, n: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    var w = abs(n);
    // Sharpen the weights so box-like surfaces stay crisp near edges while still
    // blending smoothly on curved geometry.
    w = w * w * w * w;
    let sum = w.x + w.y + w.z;
    if (sum <= 0.0) {
        return tex_stone_wall_2d(p, p.x, p.y) * tint;
    }

    // Project by dominant normal: Z-facing walls use XY, X-facing walls use ZY,
    // and horizontal surfaces use XZ.
    let x_proj = tex_stone_wall_2d(p, p.z, p.y) * w.x;
    let y_proj = tex_stone_wall_2d(p, p.x, p.z) * w.y;
    let z_proj = tex_stone_wall_2d(p, p.x, p.y) * w.z;
    return (x_proj + y_proj + z_proj) / sum * tint;
}

fn texture_eval(tex: u32, p: vec3<f32>, n: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    if (tex == TEX_NONE) { return tint; }
    if (tex == TEX_WOOD) { return tex_wood(p, tint); }
    if (tex == TEX_BRICK) { return tex_brick(p, tint); }
    if (tex == TEX_STONE) { return tex_stone(p, tint); }
    if (tex == TEX_CEMENT) { return tex_cement(p, tint); }
    if (tex == TEX_MARBLE) { return tex_marble(p, tint); }
    if (tex == TEX_GRASS) { return tex_grass(p, tint); }
    if (tex == TEX_DIRT) { return tex_dirt(p, tint); }
    if (tex == TEX_SNOW) { return tex_snow(p, tint); }
    if (tex == TEX_STONE_WALL) { return tex_stone_wall(p, n, tint); }
    return tex_wallpaper(p, tint, tex);
}

fn is_capture(tex: u32) -> bool {
    return tex >= TEX_CAPTURE_BASE && tex < TEX_CAPTURE_BASE + TEX_CAPTURE_COUNT;
}

// Cube interior bounds — keep in sync with texture.Cube* in cube_uv.go.
const CUBE_X0: f32 = -1.0;
const CUBE_X1: f32 = 4.0;
const CUBE_Y0: f32 = -1.0;
const CUBE_Y1: f32 = 4.0;
const CUBE_Z0: f32 = -1.0;
const CUBE_Z1: f32 = 4.0;

fn capture_room_uv(p: vec3<f32>, n: vec3<f32>) -> vec2<f32> {
    let an = abs(n);
    var u = 0.0;
    var v = 0.0;
    if (an.z >= an.x && an.z >= an.y) {
        // Front (+Z) / back (−Z) walls: u = X, v = Y
        u = (p.x - CUBE_X0) / (CUBE_X1 - CUBE_X0);
        v = (p.y - CUBE_Y0) / (CUBE_Y1 - CUBE_Y0);
        // +Z interior faces the viewer (−Z); image u follows +X to the right.
    } else if (an.x >= an.y) {
        // Left (+X) / right (−X) walls: u = Z, v = Y
        u = (p.z - CUBE_Z0) / (CUBE_Z1 - CUBE_Z0);
        v = (p.y - CUBE_Y0) / (CUBE_Y1 - CUBE_Y0);
        if (n.x > 0.0) {
            // Left wall: −Z (forward) is to the viewer's right → flip u.
            u = 1.0 - u;
        }
        // Right wall (−X): +Z is to the viewer's right, u increases with z.
    } else {
        // Floor (+Y) / ceiling (−Y): u = X, v = Z
        u = (p.x - CUBE_X0) / (CUBE_X1 - CUBE_X0);
        v = (p.z - CUBE_Z0) / (CUBE_Z1 - CUBE_Z0);
        if (n.y > 0.0) {
            // Floor: v increases with +Z (matches right wall and capture_down).
            return vec2(clamp(u, 0.0, 1.0), clamp(v, 0.0, 1.0));
        }
        // Ceiling: v=0 at +Z (same convention as wall v from Y).
    }
    return vec2(clamp(u, 0.0, 1.0), clamp(1.0 - v, 0.0, 1.0));
}

fn sample_capture(tex: u32, u: f32, v: f32, tint: vec3<f32>) -> vec3<f32> {
    if (params.capture_loaded == 0u) {
        return tint;
    }
    let w = params.capture_w;
    let h = params.capture_h;
    if (w == 0u || h == 0u) {
        return tint;
    }
    let slot = tex - TEX_CAPTURE_BASE;
    let xi = min(u32(u * f32(w)), w - 1u);
    let yi = min(u32(v * f32(h)), h - 1u);
    let idx = slot * w * h + yi * w + xi;
    let px = capture_pixels[idx];
    let r = f32(px & 255u) / 255.0;
    let g = f32((px >> 8u) & 255u) / 255.0;
    let b = f32((px >> 16u) & 255u) / 255.0;
    return vec3(r * tint.x, g * tint.y, b * tint.z);
}

fn texture_eval_capture(tex: u32, lp: vec3<f32>, ln: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let uv = capture_room_uv(lp, ln);
    return sample_capture(tex, uv.x, uv.y, tint);
}

// plane_albedo applies the analytic checker pattern (matching Plane.AlbedoAt)
// before any procedural texture is layered on top.
fn plane_albedo(p: Prim, hp: vec3<f32>) -> vec3<f32> {
    if (p.info.y == MAT_CHECKER) {
        let cx = i32(floor(hp.x + 0.5));
        let cz = i32(floor(hp.z + 0.5));
        if (((cx + cz) & 1) != 0) {
            return p.albedo2.xyz;
        }
    }
    return p.albedo.xyz;
}

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

// body_or returns the configured celestial-body direction when one is set,
// otherwise the variant's own default direction (def, normalized). Each sky
// variant routes its built-in sun/moon glow through this so the glow tracks the
// configured sun/moon disc instead of a hardcoded spot. Without a configured
// body, scenes render exactly as before.
fn body_or(def: vec3<f32>) -> vec3<f32> {
    if (params.body_enabled != 0u) {
        return params.body_dir.xyz;
    }
    return normalize(def);
}

fn clear_sky(d: vec3<f32>) -> vec3<f32> {
    let t = clamp01(d.y * 0.5 + 0.5);
    let sun = max(0.0, dot(d, body_or(vec3<f32>(0.4, 0.8, -0.45))));
    let s2 = sun * sun;
    let s4 = s2 * s2;
    let s8 = s4 * s4;
    let s16 = s8 * s8;
    let s32 = s16 * s16;
    let sun64 = s32 * s32;
    return vec3<f32>(
        0.05 + 0.10 * t + sun64 * 3.0,
        0.07 + 0.12 * t + sun64 * 2.5,
        0.12 + 0.22 * t + sun64 * 1.5,
    );
}

// cloudy_sky: overcast blue daytime with soft white/grey clouds.
fn cloudy_sky(d: vec3<f32>, time: f32) -> vec3<f32> {
    let up = smoothstepf(-0.05, 0.7, d.y);
    let base = mix3(vec3<f32>(0.72, 0.80, 0.88), vec3<f32>(0.30, 0.50, 0.80), up);

    let drift = time * 0.04;
    let density = fbm(d.x * 2.4 + drift, d.y * 2.4, d.z * 2.4, 5u) * 0.5 + 0.5;
    let cover = smoothstepf(0.42, 0.72, density) * smoothstepf(-0.02, 0.12, d.y);
    let shade = fbm(d.x * 1.2 + drift, d.y * 1.2 + 9.0, d.z * 1.2, 3u) * 0.5 + 0.5;
    let cloud = mix3(vec3<f32>(0.50, 0.53, 0.58), vec3<f32>(1.05, 1.05, 1.04), shade);

    let col = mix3(base, cloud, cover);

    let sun = max(0.0, dot(d, body_or(vec3<f32>(0.4, 0.85, -0.4))));
    let s2 = sun * sun;
    let s4 = s2 * s2;
    let s8 = s4 * s4;
    let glow = s8 * s8; // sun^16
    return col + vec3<f32>(0.5, 0.48, 0.42) * (glow * (1.0 - 0.7 * cover));
}

// hash3 / star_field are a sin-based starfield (a frac(sin) value hash placing
// occasional bright points).
fn hash3(x: f32, y: f32, z: f32) -> f32 {
    let s = sin(x * 127.1 + y * 311.7 + z * 74.7) * 43758.5453;
    return s - floor(s);
}

fn star_field(d: vec3<f32>, scale: f32) -> f32 {
    let x = d.x * scale;
    let y = d.y * scale;
    let z = d.z * scale;
    let ix = floor(x);
    let iy = floor(y);
    let iz = floor(z);
    let h = hash3(ix, iy, iz);
    if (h < 0.94) {
        return 0.0;
    }
    let jx = hash3(ix + 1.3, iy, iz);
    let jy = hash3(ix, iy + 5.1, iz);
    let jz = hash3(ix, iy, iz + 2.7);
    let dx = x - ix - jx;
    let dy = y - iy - jy;
    let dz = z - iz - jz;
    let d2 = dx * dx + dy * dy + dz * dz;
    let bright = (h - 0.94) / 0.06;
    return bright * bright * max(0.0, 1.0 - d2 * 16.0);
}

// night_stars_sky: deep-blue gradient with a sparse starfield.
fn night_stars_sky(d: vec3<f32>) -> vec3<f32> {
    let up = smoothstepf(0.0, 0.7, d.y);
    var base = mix3(vec3<f32>(0.020, 0.030, 0.060), vec3<f32>(0.004, 0.008, 0.022), up);
    if (d.y > 0.0) {
        let s = star_field(d, 90.0);
        base = base + vec3<f32>(s, s, s * 1.08);
    }
    let moon = max(0.0, dot(d, body_or(vec3<f32>(-0.3, 0.85, -0.4))));
    let m2 = moon * moon;
    let m4 = m2 * m2;
    let m8 = m4 * m4;
    return base + vec3<f32>(0.10, 0.12, 0.16) * (m8 * m8);
}

// night_storm_sky: dramatic moonlit storm clouds.
fn night_storm_sky(d: vec3<f32>, time: f32) -> vec3<f32> {
    let moon_color = vec3<f32>(0.62, 0.70, 0.92);
    let moon_dir = body_or(vec3<f32>(0.08, 0.42, -0.90));
    let g = clamp01(dot(d, moon_dir));
    let g2 = g * g;
    let g4 = g2 * g2;
    let g8 = g4 * g4;
    let broad = g8;      // g^8 halo
    let core = g8 * g8;  // g^16 disc

    let base = mix3(vec3<f32>(0.018, 0.026, 0.045), vec3<f32>(0.005, 0.009, 0.020), smoothstepf(0.0, 0.8, d.y));
    let sky_glow = base + moon_color * (0.7 * broad + 1.2 * core);

    let drift = time * 0.025;
    let n = turbulence(d.x * 1.8 + drift, d.y * 1.8 + 3.0, d.z * 1.8, 6u);
    let cover = smoothstepf(0.06, 0.26, n);

    let cloud = vec3<f32>(0.012, 0.017, 0.030) + moon_color * (0.16 * broad);

    return mix3(sky_glow, cloud, cover);
}

// sunset_sky: warm horizon fading to deep blue, dark rim-lit clouds.
fn sunset_sky(d: vec3<f32>, time: f32) -> vec3<f32> {
    let warm = vec3<f32>(1.20, 0.45, 0.18);
    let mid = vec3<f32>(0.55, 0.28, 0.42);
    let zen = vec3<f32>(0.08, 0.10, 0.26);
    var base = mix3(warm, mid, smoothstepf(-0.05, 0.28, d.y));
    base = mix3(base, zen, smoothstepf(0.18, 0.75, d.y));

    let sun_dir = body_or(vec3<f32>(0.30, 0.05, -0.95));
    let sd = clamp01(dot(d, sun_dir));
    let sd2 = sd * sd;
    let sd4 = sd2 * sd2;
    let sd8 = sd4 * sd4;
    let disc = sd8 * sd8 * sd8; // tight sun disc/glow
    base = base + vec3<f32>(1.6, 0.9, 0.4) * (disc + 0.25 * sd8);

    let drift = time * 0.03;
    let density = fbm(d.x * 2.1 + drift, d.y * 2.1 + 5.0, d.z * 2.1, 5u) * 0.5 + 0.5;
    let cover = smoothstepf(0.4, 0.7, density) * smoothstepf(-0.03, 0.15, d.y);
    let rim = smoothstepf(0.38, 0.52, density) * (0.25 + 0.75 * sd4);
    var cloud = vec3<f32>(0.08, 0.05, 0.09);
    cloud = cloud + vec3<f32>(1.4, 0.65, 0.28) * rim;

    return mix3(base, cloud, cover);
}

// celestial_body returns the additive radiance of the configured sun/moon disc
// along view direction d: a soft-limbed disc plus a falloff halo. It is composed
// on top of every sky variant, so it also shows up in reflections. The body is
// purely cosmetic (it does not light the scene).
//
// A procedural moon texture (craters) would slot in here: build a tangent frame
// around body_dir and map d's offset within the disc to a uv in [-1,1], then
// modulate the disc color by a noise-based crater function (the fbm/value-noise
// helpers above are already available).
fn celestial_body(d: vec3<f32>) -> vec3<f32> {
    if (params.body_enabled == 0u) {
        return vec3<f32>(0.0, 0.0, 0.0);
    }
    let cos_a = dot(d, params.body_dir.xyz); // d and body_dir are unit vectors
    let cr = params.body_cos_radius;

    // Disc with a soft limb: full intensity inside ~0.9R, fading to 0 at the
    // edge R. Working in cosine space avoids a per-pixel acos.
    let cr_inner = cos(acos(clamp(cr, -1.0, 1.0)) * 0.9);
    let disc = smoothstepf(cr, cr_inner, cos_a);

    // Halo: a soft glow reaching ~3 disc-radii out, scaled by body_glow. The
    // steep power keeps it concentrated near the limb so the disc size still
    // reads clearly.
    let cr_halo = cos(acos(clamp(cr, -1.0, 1.0)) * 3.5);
    var halo = clamp01((cos_a - cr_halo) / max(1.0e-4, 1.0 - cr_halo));
    halo = halo * halo * halo * halo * params.body_glow * 0.6;

    return params.body_color.xyz * (disc + halo);
}

// sky dispatches on the selected variant (params.sky), then adds the celestial
// body on top.
fn sky(d: vec3<f32>) -> vec3<f32> {
    var base: vec3<f32>;
    switch (params.sky) {
        case 1u: { base = cloudy_sky(d, params.time); }
        case 2u: { base = night_stars_sky(d); }
        case 3u: { base = night_storm_sky(d, params.time); }
        case 4u: { base = sunset_sky(d, params.time); }
        default: { base = clear_sky(d); }
    }
    return base + celestial_body(d);
}

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

fn terrain_height(i: u32, x: f32, z: f32) -> f32 {
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

fn terrain_normal(i: u32, p: vec3<f32>) -> vec3<f32> {
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
    let d = p.xz - w.geom.xy;
    if (dot(d, d) > w.geom.z * w.geom.z) {
        return T_MISS;
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

fn inst_local_origin(i: u32, ro: vec3<f32>) -> vec3<f32> {
    let r = inst_records[i];
    let v = ro - vec3<f32>(r.xf0.w, r.xf1.w, r.xf2.w);
    return vec3<f32>(dot(r.xf0.xyz, v), dot(r.xf1.xyz, v), dot(r.xf2.xyz, v));
}

fn inst_local_dir(i: u32, rd: vec3<f32>) -> vec3<f32> {
    let r = inst_records[i];
    return vec3<f32>(dot(r.xf0.xyz, rd), dot(r.xf1.xyz, rd), dot(r.xf2.xyz, rd));
}

// inst_local_point maps a world hit point into the template's local space.
fn inst_local_point(i: u32, wp: vec3<f32>) -> vec3<f32> {
    return inst_local_origin(i, wp);
}

// inst_world_normal rotates a template-space normal to world space.
fn inst_world_normal(i: u32, ln: vec3<f32>) -> vec3<f32> {
    let r = inst_records[i];
    return normalize(vec3<f32>(
        r.xf0.x * ln.x + r.xf1.x * ln.y + r.xf2.x * ln.z,
        r.xf0.y * ln.x + r.xf1.y * ln.y + r.xf2.y * ln.z,
        r.xf0.z * ln.x + r.xf1.z * ln.y + r.xf2.z * ln.z,
    ));
}

fn bvh_nearest_subtree(root: u32, ro: vec3<f32>, rd: vec3<f32>, best_t: f32, blockers: bool) -> f32 {
    let inv = vec3<f32>(1.0) / rd;
    var t = best_t;
    var stack: array<u32, 64>;
    var sp = 0u;
    stack[sp] = root;
    sp = sp + 1u;

    loop {
        if (sp == 0u) {
            break;
        }
        sp = sp - 1u;
        let n = bvh_nodes[stack[sp]];
        if (!slab_hit(n.min_b.xyz, n.max_b.xyz, ro, inv, t)) {
            continue;
        }
        let count = n.info.w;
        if (count > 0u) {
            for (var k = 0u; k < count; k = k + 1u) {
                let prim_idx = select(n.info.x, n.info.y, k == 1u);
                let hit_t = select(intersect(prim_idx, ro, rd), intersect_blocker(prim_idx, ro, rd), blockers);
                if (hit_t < t) {
                    t = hit_t;
                }
            }
        } else if (n.info.z != BVH_TAG_TLAS) {
            if (sp + 2u <= 64u) {
                stack[sp] = n.info.x;
                sp = sp + 1u;
                stack[sp] = n.info.y;
                sp = sp + 1u;
            }
        }
    }
    return t;
}

fn bvh_nearest_subtree_hit(root: u32, ro: vec3<f32>, rd: vec3<f32>, h: Hit, inst_idx: u32) -> Hit {
    let inv = vec3<f32>(1.0) / rd;
    var best = h;
    var stack: array<u32, 64>;
    var sp = 0u;
    stack[sp] = root;
    sp = sp + 1u;

    loop {
        if (sp == 0u) {
            break;
        }
        sp = sp - 1u;
        let n = bvh_nodes[stack[sp]];
        if (!slab_hit(n.min_b.xyz, n.max_b.xyz, ro, inv, best.t)) {
            continue;
        }
        let count = n.info.w;
        if (count > 0u) {
            for (var k = 0u; k < count; k = k + 1u) {
                let prim_idx = select(n.info.x, n.info.y, k == 1u);
                let t = intersect(prim_idx, ro, rd);
                if (t < best.t) {
                    best.t = t;
                    best.idx = prim_idx;
                    best.kind = 0u;
                    best.inst_idx = inst_idx;
                }
            }
        } else if (n.info.z != BVH_TAG_TLAS) {
            if (sp + 2u <= 64u) {
                stack[sp] = n.info.x;
                sp = sp + 1u;
                stack[sp] = n.info.y;
                sp = sp + 1u;
            }
        }
    }
    return best;
}

fn inst_bvh_any_hit(root: u32, ro: vec3<f32>, rd: vec3<f32>, max_t: f32) -> bool {
    let inv = vec3<f32>(1.0) / rd;
    let limit = max_t - 0.05;
    var stack: array<u32, 64>;
    var sp = 0u;
    stack[sp] = root;
    sp = sp + 1u;

    loop {
        if (sp == 0u) {
            break;
        }
        sp = sp - 1u;
        let n = bvh_nodes[stack[sp]];
        if (!slab_hit(n.min_b.xyz, n.max_b.xyz, ro, inv, max_t)) {
            continue;
        }
        if (n.info.z == BVH_TAG_TLAS && n.info.w > 0u) {
            let inst_idx = n.info.x;
            let tmpl = inst_templates[inst_records[inst_idx].template_id];
            let lro = inst_local_origin(inst_idx, ro);
            let lrd = inst_local_dir(inst_idx, rd);
            if (bvh_nearest_subtree(tmpl.blocker_blas_root, lro, lrd, limit, true) < limit) {
                return true;
            }
            continue;
        }
        if (n.info.z == BVH_TAG_TLAS && n.info.w == 0u) {
            if (sp + 2u <= 64u) {
                stack[sp] = n.info.x;
                sp = sp + 1u;
                stack[sp] = n.info.y;
                sp = sp + 1u;
            }
            continue;
        }
        let count = n.info.w;
        if (count > 0u) {
            for (var k = 0u; k < count; k = k + 1u) {
                let prim_idx = select(n.info.x, n.info.y, k == 1u);
                let t = intersect_blocker(prim_idx, ro, rd);
                if (t > RAY_EPSILON && t < limit) {
                    return true;
                }
            }
        } else if (n.info.z != BVH_TAG_TLAS) {
            if (sp + 2u <= 64u) {
                stack[sp] = n.info.x;
                sp = sp + 1u;
                stack[sp] = n.info.y;
                sp = sp + 1u;
            }
        }
    }
    return false;
}

fn inst_nearest_hit(ro: vec3<f32>, rd: vec3<f32>, h: Hit) -> Hit {
    if (params.inst_count == 0u) {
        return h;
    }
    let inv = vec3<f32>(1.0) / rd;
    var best = h;
    var stack: array<u32, 64>;
    var sp = 0u;
    stack[sp] = params.inst_node_base;
    sp = sp + 1u;

    loop {
        if (sp == 0u) {
            break;
        }
        sp = sp - 1u;
        let n = bvh_nodes[stack[sp]];
        if (!slab_hit(n.min_b.xyz, n.max_b.xyz, ro, inv, best.t)) {
            continue;
        }
        if (n.info.z == BVH_TAG_TLAS && n.info.w > 0u) {
            let inst_idx = n.info.x;
            let tmpl = inst_templates[inst_records[inst_idx].template_id];
            let lro = inst_local_origin(inst_idx, ro);
            let lrd = inst_local_dir(inst_idx, rd);
            best = bvh_nearest_subtree_hit(tmpl.blas_root, lro, lrd, best, inst_idx);
            continue;
        }
        if (n.info.z == BVH_TAG_TLAS && n.info.w == 0u) {
            if (sp + 2u <= 64u) {
                stack[sp] = n.info.x;
                sp = sp + 1u;
                stack[sp] = n.info.y;
                sp = sp + 1u;
            }
        }
    }
    return best;
}

fn nearest_hit(ro: vec3<f32>, rd: vec3<f32>) -> Hit {
    var h: Hit;
    h.t = T_MISS;
    h.idx = 0xffffffffu;
    h.kind = 0u;
    h.inst_idx = HIT_NO_INSTANCE;

    if (params.bvh_node_count > 0u) {
        let inv = vec3<f32>(1.0) / rd;
        var stack: array<u32, 64>;
        var sp = 0u;
        stack[sp] = 0u;
        sp = sp + 1u;

        loop {
            if (sp == 0u) {
                break;
            }
            sp = sp - 1u;
            let n = bvh_nodes[stack[sp]];
            if (!slab_hit(n.min_b.xyz, n.max_b.xyz, ro, inv, h.t)) {
                continue;
            }
            let count = n.info.w;
            if (count > 0u) {
                for (var k = 0u; k < count; k = k + 1u) {
                    let prim_idx = select(n.info.x, n.info.y, k == 1u);
                    let t = intersect(prim_idx, ro, rd);
                    if (t < h.t) {
                        h.t = t;
                        h.idx = prim_idx;
                        h.kind = 0u;
                        h.inst_idx = HIT_NO_INSTANCE;
                    }
                }
            } else {
                if (sp + 2u <= 64u) {
                    stack[sp] = n.info.x;
                    sp = sp + 1u;
                    stack[sp] = n.info.y;
                    sp = sp + 1u;
                }
            }
        }
    }

    h = inst_nearest_hit(ro, rd, h);

    // Infinite planes are not part of the finite BVH.
    for (var i = 0u; i < params.prim_count; i = i + 1u) {
        if (prims[i].info.x != PRIM_PLANE) {
            continue;
        }
        let t = intersect(i, ro, rd);
        if (t < h.t) {
            h.t = t;
            h.idx = i;
            h.kind = 0u;
        }
    }

    for (var i = 0u; i < params.terrain_count; i = i + 1u) {
        let t = hit_terrain(i, ro, rd, h.t, true);
        if (t < h.t) {
            h.t = t;
            h.idx = i;
            h.kind = 1u;
        }
    }

    for (var i = 0u; i < params.water_count; i = i + 1u) {
        let t = hit_water(i, ro, rd);
        if (t < h.t) {
            h.t = t;
            h.idx = i;
            h.kind = 2u;
        }
    }
    return h;
}

fn blocker_bvh_any_hit(ro: vec3<f32>, rd: vec3<f32>, max_t: f32) -> bool {
    if (params.blocker_bvh_node_count == 0u && params.blocker_inst_count == 0u) {
        return false;
    }
    let inv = vec3<f32>(1.0) / rd;
    let limit = max_t - 0.05;
    let blocker_off = select(params.bvh_node_count, params.blocker_section_start, params.inst_count > 0u);

    if (params.blocker_bvh_node_count > 0u) {
        var stack: array<u32, 64>;
        var sp = 0u;
        stack[sp] = 0u;
        sp = sp + 1u;

        loop {
            if (sp == 0u) {
                break;
            }
            sp = sp - 1u;
            let n = bvh_nodes[blocker_off + stack[sp]];
            if (!slab_hit(n.min_b.xyz, n.max_b.xyz, ro, inv, max_t)) {
                continue;
            }
            let count = n.info.w;
            if (count > 0u) {
                for (var k = 0u; k < count; k = k + 1u) {
                    let prim_idx = select(n.info.x, n.info.y, k == 1u);
                    let t = intersect_blocker(prim_idx, ro, rd);
                    if (t > RAY_EPSILON && t < limit) {
                        return true;
                    }
                }
            } else if (n.info.z != BVH_TAG_TLAS) {
                if (sp + 2u <= 64u) {
                    stack[sp] = n.info.x;
                    sp = sp + 1u;
                    stack[sp] = n.info.y;
                    sp = sp + 1u;
                }
            }
        }
    }

    if (params.inst_count > 0u && params.blocker_inst_count > 0u) {
        if (inst_bvh_any_hit(params.blocker_inst_base, ro, rd, max_t)) {
            return true;
        }
    }
    return false;
}

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

fn cone_normal(p: Prim, hp: vec3<f32>) -> vec3<f32> {
    let cx = p.geo_a.x;
    let cz = p.geo_a.y;
    let rbase = p.geo_a.z;
    let ybase = p.geo_a.w;
    let ytip = p.geo_b.x;
    if (abs(hp.y - ybase) < 0.01) {
        return vec3<f32>(0.0, -1.0, 0.0);
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

fn normal_at(p: Prim, hp: vec3<f32>) -> vec3<f32> {
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
        return cone_normal(p, hp);
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
    for (var i = 0u; i < params.blocker_count; i = i + 1u) {
        if (blockers[i].info.x != PRIM_PLANE) {
            continue;
        }
        let t = intersect_blocker(i, origin, dir);
        if (t > RAY_EPSILON && t < limit) {
            prof_inc(PROF_SHADOW_BLOCK, 1u);
            return true;
        }
    }
    for (var i = 0u; i < params.terrain_count; i = i + 1u) {
        let t = hit_terrain(i, origin, dir, max_t, false);
        if (t > RAY_EPSILON && t < limit) {
            prof_inc(PROF_SHADOW_BLOCK, 1u);
            return true;
        }
    }
    return false;
}

// add_point_light_raw is the shared core for one point light: distance cull,
// inverse-square + optional windowed falloff, a brightness cull, and a shadow
// ray. Static lights and flickering campfire sub-lights both feed through it.
fn add_point_light_raw(lit: vec3<f32>, hp: vec3<f32>, albedo: vec3<f32>, n: vec3<f32>, ep: vec3<f32>, pos: vec3<f32>, color: vec3<f32>, cull_r2: f32, inv_r2: f32) -> vec3<f32> {
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
    if (att * ndl * max(color.x, max(color.y, color.z)) < LIGHT_CULL_EPS) {
        return lit;
    }
    if (params.shadows != 0u && shadowed(ep, ln, ldist)) {
        return lit;
    }
    return lit + color * (att * ndl) * albedo;
}

fn add_point_light(lit: vec3<f32>, hp: vec3<f32>, albedo: vec3<f32>, n: vec3<f32>, ep: vec3<f32>, light: Light) -> vec3<f32> {
    return add_point_light_raw(lit, hp, albedo, n, ep, light.pos.xyz, light.color.xyz, light.falloff.x, light.falloff.y);
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

// shade_diffuse computes the diffuse direct lighting at a hit: flat ambient,
// static point lights and flickering campfires (with the shared core shadow
// early-out), then scaled by the baked ambient-occlusion volume. Used for
// scenes without an env sun/hemispheric ambient.
fn shade_diffuse(hp: vec3<f32>, alb: vec3<f32>, n: vec3<f32>, ep: vec3<f32>) -> vec3<f32> {
    var lit = alb * params.ambient;
    for (var i = 0u; i < params.light_count; i = i + 1u) {
        lit = add_point_light(lit, hp, alb, n, ep, lights[i]);
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
            if (shadowed(ep, cl / cdist, cdist)) {
                continue;
            }
        }
        let ts = params.time * cf.param.w;
        for (var j = 0u; j < 3u; j = j + 1u) {
            let sl = campfire_sublight(cf, j, ts);
            lit = add_point_light_raw(lit, hp, alb, n, ep, sl.pos, sl.col, cull.x, cull.y);
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

// RaySeg is one pending ray in the bounded work stack: a ray plus the
// multiplicative weight (throughput) its radiance contributes to the pixel, and
// its bounce depth. The stack lets glass blend its reflected and refracted
// lobes (a real ray tree) instead of picking one.
struct RaySeg {
    ro: vec3<f32>,
    rd: vec3<f32>,
    w: vec3<f32>,
    depth: u32,
};

// MAX_SEGS bounds the ray tree. Recursion is capped at depth 3 and only glass
// branches (into two children), at depths 0 and 1 (depth 2 is no longer
// "reflective" so it terminates), giving at most 1 + 2 + 4 = 7 live segments.
const MAX_SEGS: u32 = 16u;

// ray_color evaluates the radiance along the camera ray by walking a small work
// stack: sky on a miss, emissive passthrough, single-bounce mirror/metal, the
// two-lobe Fresnel glass blend, diffuse direct lighting, and semi-reflective
// diffuse/checker. Reflections are gated by params.mirror.
fn ray_color(origin: vec3<f32>, dir0: vec3<f32>) -> vec3<f32> {
    var accum = vec3<f32>(0.0, 0.0, 0.0);
    var stack: array<RaySeg, 16>;
    stack[0] = RaySeg(origin, dir0, vec3<f32>(1.0, 1.0, 1.0), 0u);
    var sp = 1u;

    loop {
        if (sp == 0u) {
            break;
        }
        sp = sp - 1u;
        let seg = stack[sp];
        let ro = seg.ro;
        let rd = seg.rd;
        let tw = seg.w;
        let depth = seg.depth;
        prof_inc(PROF_PATH_SEGS, 1u);

        let hit = nearest_hit(ro, rd);
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
        } else {
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

        if (hit.kind == 0u) {
            let p = prims[hit.idx];
            mat = p.info.y;
            surf = p.surf;
            // For a transformed primitive evaluate the normal and procedural
            // texture in template/local space, then rotate back to world.
            var tpl_hp = hp;
            if (hit.inst_idx != HIT_NO_INSTANCE) {
                tpl_hp = inst_local_point(hit.inst_idx, hp);
            }
            var tex_p = tpl_hp;
            var tex_n = n;
            if ((p.info.w & PRIM_FLAG_TRANSFORMED) != 0u) {
                let lhp = xf_to_local_point(p, tpl_hp);
                tex_n = normal_at(p, lhp);
                var ln = xf_to_world_normal(p, tex_n);
                if (hit.inst_idx != HIT_NO_INSTANCE) {
                    ln = inst_world_normal(hit.inst_idx, ln);
                }
                n = normalize(ln);
                tex_p = lhp;
            } else {
                tex_n = normal_at(p, tpl_hp);
                n = tex_n;
                if (hit.inst_idx != HIT_NO_INSTANCE) {
                    n = inst_world_normal(hit.inst_idx, n);
                }
            }
            if (is_capture(p.info.z) && p.info.x == PRIM_BOX) {
                alb = texture_eval_capture(p.info.z, tex_p, tex_n, p.albedo.xyz);
            } else {
                alb = texture_eval(p.info.z, tex_p, tex_n, plane_albedo(p, hp));
            }
        } else if (hit.kind == 1u) {
            mat = MAT_DIFFUSE;
            n = terrain_normal(hit.idx, hp);
            alb = terrain_albedo(hit.idx, hp, n);
        } else {
            let wp = waters[hit.idx];
            mat = wp.info.x;
            surf = wp.surf;
            n = water_normal(hit.idx, hp);
            alb = texture_eval(wp.info.y, hp, n, wp.albedo.xyz);
        }

        if (dot(n, rd) > 0.0) {
            n = -n;
        }

        if (mat == MAT_EMIT) {
            accum = accum + tw * alb;
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
        let ep = hp + n * SURFACE_EPSILON;
        // reflective = depth < max_bounce_depth && mirror enabled.
        var max_depth = params.max_bounce_depth;
        if (max_depth == 0u) {
            max_depth = 2u;
        }
        let reflective = depth < max_depth && params.mirror != 0u;

        if ((mat == MAT_MIRROR || mat == MAT_METAL) && reflective) {
            prof_inc(PROF_MIRROR_BOUNCES, 1u);
            if (sp < MAX_SEGS) {
                stack[sp] = RaySeg(ep, jitter_dir(reflect(rd, n), hp, rough), tw * alb * 0.96, depth + 1u);
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
            let eta = 1.0 / ior;
            let k = 1.0 - eta * eta * (1.0 - cosi * cosi);
            let tir = k < 0.0;
            var w_refl = reflectance;
            if (tir) {
                w_refl = 1.0;
            }

            // Transmitted (see-through) lobe, tinted by albedo and weighted by
            // (1 - reflectance). Skipped under TIR and near grazing angles where
            // it is almost fully reflected anyway.
            if (!tir && w_refl < 0.98 && sp < MAX_SEGS) {
                let cost = sqrt(k);
                let rr = jitter_dir(normalize(rd * eta + n * (eta * cosi - cost)), hp, rough * 0.35);
                stack[sp] = RaySeg(hp - n * SURFACE_EPSILON, rr, tw * alb * (1.0 - reflectance), depth + 1u);
                sp = sp + 1u;
            }

            // Reflected lobe (the world in front of the pane). Its weight is the
            // Fresnel reflectance, so even a head-on clear window keeps a ~4%
            // floor. reflMin keeps deep, faint bounces bounded.
            var refl_min = 0.02;
            if (depth > 0u) {
                refl_min = 0.2;
            }
            if (w_refl > refl_min && sp < MAX_SEGS) {
                stack[sp] = RaySeg(ep, jitter_dir(reflect(rd, n), hp, rough), tw * w_refl, depth + 1u);
                sp = sp + 1u;
            }
            continue;
        }

        // Diffuse / checker, plus mirror/metal/glass falling through here at the
        // depth cap (or when reflections are disabled): shaded as diffuse.
        let lit = shade_diffuse(hp, alb, n, ep);
        let refl = surf.z;
        if (refl > 0.0 && reflective && (mat == MAT_DIFFUSE || mat == MAT_CHECKER) && sp < MAX_SEGS) {
            prof_inc(PROF_DIFFUSE_REFL, 1u);
            accum = accum + tw * lit * (1.0 - refl);
            stack[sp] = RaySeg(ep, jitter_dir(reflect(rd, n), hp, rough), tw * refl, depth + 1u);
            sp = sp + 1u;
            continue;
        }
        accum = accum + tw * lit;
    }
    return accum;
}

// quantize_rgb reduces dithered 0..255 RGB to a retro color depth. mode 1 is
// classic 15-bit (5-5-5, 32768 colors); mode 2 is the PC 256-color cube (3-3-2).
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
    let rq = floor(rgb.x * 7.0 / 255.0 + 0.5);
    let gq = floor(rgb.y * 7.0 / 255.0 + 0.5);
    let bq = floor(rgb.z * 3.0 / 255.0 + 0.5);
    return vec3(rq * (255.0 / 7.0), gq * (255.0 / 7.0), bq * (255.0 / 3.0));
}

@compute @workgroup_size(8, 8, 1)
fn main(@builtin(global_invocation_id) gid: vec3<u32>) {
    if (gid.x >= params.width || gid.y >= params.height) {
        return;
    }

    let u = (f32(gid.x) + 0.5) / f32(params.width) * 2.0 - 1.0;
    let v = 1.0 - (f32(gid.y) + 0.5) / f32(params.height) * 2.0;
    let dir = normalize(
        params.fwd.xyz +
        params.right.xyz * (u * params.aspect * params.fov_scale) +
        params.up.xyz * (v * params.fov_scale)
    );
    let ro = params.cam_pos.xyz;

    prof_inc(PROF_PIXELS, 1u);
    var col = ray_color(ro, dir);
    col = tonemap(col);

    var bayer = array<u32, 16>(
        0u, 8u, 2u, 10u,
        12u, 4u, 14u, 6u,
        3u, 11u, 1u, 9u,
        15u, 7u, 13u, 5u,
    );
    let bayer_idx = (gid.y & 3u) * 4u + (gid.x & 3u);
    var rgb: vec3<f32>;
    if (params.color_quant == 3u) {
        rgb = clamp(col * 255.0, vec3(0.0), vec3(255.0));
    } else {
        let bdt = (f32(bayer[bayer_idx]) / 16.0 - 0.5) * 6.0;
        rgb = clamp(col * 255.0 + vec3(bdt), vec3(0.0), vec3(255.0));
    }
    rgb = quantize_rgb(rgb, params.color_quant);
    let r = u32(floor(rgb.x));
    let g = u32(floor(rgb.y));
    let b = u32(floor(rgb.z));
    let a = 255u;

    // Host reads little-endian bytes as RGBA.
    pixels[gid.y * params.width + gid.x] = r | (g << 8u) | (b << 16u) | (a << 24u);
}
