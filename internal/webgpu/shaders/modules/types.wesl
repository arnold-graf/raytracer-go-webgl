// types.wesl — Data layouts and GPU bindings.
// Defines the structs the CPU packs (Params, Prim, BVHNode, Light, …), every
// @group(0) storage/uniform binding, shared constants (primitive kinds, materials,
// ray epsilons, lighting tuning knobs), and the Hit result returned by tracing.

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
    ambient_sky: vec4<f32>,    // hemispheric ambient (xyz); from [environment].ambient_sky
    ambient_ground: vec4<f32>, // hemispheric ambient (xyz); from [environment].ambient_ground
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
    // color_quant: 0 = 8-bit dither only, 1 = 15-bit (5-5-5), 2 = crush (24 levels/ch).
    color_quant: u32,
    capture_loaded: u32,
    capture_w: u32,
    capture_h: u32,
    document_loaded: u32,
    inst_template_count: u32,
    inst_count: u32,
    inst_node_base: u32,
    inst_node_count: u32,
    blocker_section_start: u32,
    blocker_inst_base: u32,
	blocker_inst_count: u32,
    profile_enabled: u32,
    // Counts of the precomputed plane-index lists (plane_idx / blocker_plane_idx),
    // so the tracer iterates only planes instead of scanning every primitive.
    plane_count: u32,
    blocker_plane_count: u32,
    flame_particle_count: u32,
    // adaptive_aa: when set, primary rays at pixel edges are compared; mismatches
    // trigger a 2×2 supersample for that pixel.
    adaptive_aa: u32,
};

// Prim mirrors GPUPrimitive in scene.go (std430, 144-byte stride).
struct Prim {
    geo_a: vec4<f32>,   // sphere: center.xyz, radius | plane: n.xyz, d | box: min.xyz, holeStart
                        // cylinder: cx, cz, radius, ymin | cone: cx, cz, rbase, ybase
                        // torus: center.xyz, majorR
                        // ring: cx, cz, radius, cy
    geo_b: vec4<f32>,   // box: max.xyz, holeCount | sphere: cut_off | cylinder: ymax, radius_top | cone: ytip | torus: minorR | ring: height
    albedo: vec4<f32>,  // linear rgb in xyz
    albedo2: vec4<f32>, // checker second color
    surf: vec4<f32>,    // rough, ior, reflect, transmit
    info: vec4<u32>,    // kind, material, texture, flags (bit0 = transformed, bit1 = thin glass)
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
// each frame. Mirrors struct CampfireParams in scene.go (std430, 128-byte stride).
struct CampfireParams {
    core: vec4<f32>,  // cx, cy, cz, range
    color: vec4<f32>, // base color (r, g, b, 0) — tints applied per sub-light
    param: vec4<f32>, // brightness, jitter, flicker, speed
    phase: vec4<f32>, // seed, flame_enabled (1/0), flame_scale, 0
    flame_ember: vec4<f32>,
    flame_mid: vec4<f32>,
    flame_tip: vec4<f32>,
    flame_ash: vec4<f32>,
};

// Light mirrors GPULight in scene.go (std430, 48-byte stride).
struct Light {
    pos: vec4<f32>,
    color: vec4<f32>,
    falloff: vec4<f32>, // cullR2, invR2, spotYaw, spotPitch
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
    analytic: vec4<f32>,   // base, detail, detailScale, nearStart
    island0: vec4<f32>,    // centerX, centerZ, radius, margin
    island1: vec4<f32>,    // floor, nearEnd, hybrid (1/0), _
    offsets: vec4<u32>,    // featureBase, padBase, featureCount, padCount
    mip: vec4<u32>,        // mipBase (vec2 index), l0nx, l0nz, levelCount
    coarse: vec4<f32>,     // cwx, cwz, cInvDx, cInvDz
};

struct TerrainFeature {
    pos: vec4<f32>,   // x, z, height, width
    shape: vec4<f32>, // steepness, extendX, extendZ, angle
    cull: vec4<f32>,  // cullR2 (world XZ dist² beyond which |contribution| < 1e-5 m), _, _, _
};

struct TerrainPad {
    center: vec4<f32>, // cx, cz, halfX, halfZ
    params: vec4<f32>, // level, margin, angle, _
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

// Terrain bake: per terrain a heights region (1 f32 per cell, Grid[2]) then a
// normals region (3 f32 per cell, Grid[3]) — flat so height taps read 4 bytes.
@group(0) @binding(7)
var<storage, read> terrain_samples: array<f32>;

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

@group(0) @binding(19)
var<storage, read> document_pixels: array<u32>;

@group(0) @binding(20)
var<storage, read> box_face_textures: array<u32>;

@group(0) @binding(21)
var<storage, read> terrain_features: array<TerrainFeature>;

@group(0) @binding(22)
var<storage, read> terrain_pads: array<TerrainPad>;

@group(0) @binding(23)
var<storage, read> terrain_mip: array<vec2<f32>>;

// Adaptive AA pass-1 scratch: linear HDR color and the primary-ray hit fingerprint.
@group(0) @binding(24)
var<storage, read_write> hdr_pixels: array<vec4<f32>>;

@group(0) @binding(25)
var<storage, read_write> aa_hits: array<u32>;

@group(0) @binding(14)
var<storage, read> inst_templates: array<TemplateRecord>;

@group(0) @binding(15)
var<storage, read> inst_records: array<InstanceRecord>;

@group(0) @binding(16)
var<storage, read_write> profile_counters: array<atomic<u32>>;

// plane_idx / blocker_plane_idx list the indices of the infinite planes inside
// the prims / blockers buffers. Planes are excluded from the BVH, so instead of
// scanning every primitive each ray we walk these short lists.
@group(0) @binding(17)
var<storage, read> plane_idx: array<u32>;

@group(0) @binding(18)
var<storage, read> blocker_plane_idx: array<u32>;
const BVH_TAG_TLAS: u32 = 1u;

const PRIM_FLAG_TRANSFORMED: u32 = 1u;
const PRIM_FLAG_GLASS_THIN: u32 = 2u;

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
const TEX_PAPER: u32 = 13u;
const TEX_CAPTURE_BASE: u32 = 50u;
const TEX_CAPTURE_COUNT: u32 = 6u;
const TEX_DOCUMENT_BASE: u32 = 56u;
const TEX_DOCUMENT_COUNT: u32 = 16u;
const DOCUMENT_TEX_W: u32 = 512u;
const DOCUMENT_TEX_H: u32 = 512u;

// ── Ray intersection guards ──────────────────────────────────────────────────
// Minimum hit distance (avoids self-intersection). Used by all primitive tests.
const RAY_EPSILON: f32 = 1e-4;
// Nudge ray origins off surfaces before shadow and bounce rays.
const SURFACE_EPSILON: f32 = 5e-4;
// Sentinel "no geometry hit" distance for BVH traversal.
const T_MISS: f32 = 1e30;

// ── Lighting & shadow tuning knobs ───────────────────────────────────────────
// Used in add_point_light_raw() and shade_diffuse(). Longer notes:
// docs/reflection-optimization.md
//
// tw_peak(tw) is the brightest channel of the ray's throughput weight. It is
// < 1 on mirror/glass bounce paths, so bounce shading is where these matter.

// Drop a point light when tw_peak(tw) × att × N·L × light_color is below this.
// The light is skipped entirely (no shading, no shadow ray).
//   Higher → faster, fewer lights evaluated.
//   Lower  → more accurate, especially on dim reflection paths.
const LIGHT_CULL_EPS: f32 = 0.0025;

// optimization: skip the shadow ray when tw_peak(tw) × unshadowed_brightness
// is below this. The light is added as if unshadowed (no BVH shadow traversal).
// Main perf win on mirror-heavy scenes; tune if shadows look wrong in reflections.
//   0.0          → never skip (slowest, most exact)
//   1/256 = 0.00390625 → aggressive; may merge multi-light shadows (e.g. campfire)
//   1/1024 = 0.0009765625 → current default (good speed/look balance)
//   1/2048 = 0.00048828125 or ↓  → conservative; closer to exact, less perf gain
const SHADOW_SKIP_EPS: f32 = 0.00390625;

// optimization: drop a reflected/refracted child ray when the throughput it
// would carry, tw_peak(tw) × lobe_weight, is below this. The existing lobe
// gates (refl_min, the 0.98 transmit cutoff) only look at the *local* Fresnel
// weight, so a segment already dimmed to a few percent by earlier bounces still
// branched as if it were a primary ray. Multiplying in the accumulated
// throughput prunes the faint tail of the ray tree, which is most of the work
// in scenes with facing glass panes (light bouncing between parallel panes
// spawns a deep, exponentially dimming tree).
//
// The bound is on *throughput*, not final radiance, so the safe value depends
// on how bright a surface the dropped ray could have hit. Tonemap + gamma
// amplify small values against a dark background, hence the conservative
// default.
//   0.0            → never drop (exact, slowest)
//   1/1024 ≈ 0.001 → current default; verified pixel-identical on the office
//                    scene's glass skyway
//   1/256 ≈ 0.004  → faster still, but starts to lift faint reflections
const SEG_MIN_CONTRIB: f32 = 0.0009765625;

// Scene feature flags. Every FEAT_* below is true in the checked-in shader, so
// the linked WGSL on disk is always the full-featured one and nothing changes if
// specialization is off. shaders.Specialize rewrites these lines to `false` for
// the features a scene does not use, before the module is compiled.
//
// Why this exists: the tracer is one megakernel, and register allocation is
// global to it. Terrain marching, water, and flame each carry a large live
// working set even in a scene that has none of them — the runtime `count == 0`
// guards skip the *work* but not the *registers*, which cost occupancy on every
// ray in every scene. Flipping a flag to false lets the compiler const-fold the
// early return and drop the body entirely.
//
// These are `const`, not `override`, deliberately: go-webgpu v0.17.1 declares
// ConstantEntry but leaves the Constants field commented out on every pipeline
// descriptor, so pipeline-overridable constants cannot be plumbed through. A
// source rewrite before CreateShaderModule needs no binding support.
//
// A flag may only be cleared when the feature is genuinely absent from the
// scene, in which case the guarded code was unreachable anyway and the frame is
// unchanged. See featuresFor in device.go for the mapping.
//
// Volumetric flame is deliberately NOT on this list. Stripping it is just as
// unreachable on paper — both entry points loop over campfire_count — but it
// measurably perturbed 8 pixels of scenes/default.toml (two of them by 9 and 18
// levels). No logic reaches that code with no campfires, so the cause is the
// compiler reallocating registers and flipping a hit decision at a silhouette.
// It was worth 0.4 ms of 26.5; the bit-identical guarantee is worth more.
//
// Per-primitive-kind flags were tried too — gating each hit_* body on whether
// the scene packs that kind — and made the office scene *slower*, 26.5 ms to
// 48.2 ms, for stripping only plane, torus and lens. Frames stayed identical and
// the pipeline was built once, so this is the Metal compiler restructuring the
// hot hit_prim dispatch chain, not extra work. Do not re-add without measuring.
const FEAT_TERRAIN: bool = true;
const FEAT_WATER: bool = true;
const FEAT_CAMPFIRE: bool = true;

// Per-primitive-kind flags. hit_prim is the hottest function in the tracer —
// roughly ten million calls a frame — and its dispatch chain keeps every kind's
// intersection body reachable, and so live for register allocation, even in a
// scene that packs none of them.
//
// These gate the *dispatch site*, not the function bodies. An earlier attempt
// put `if (!FEAT_PRIM_X) { return T_MISS; }` at the top of each hit_X instead,
// which left every call edge in place and merely made the callees trivial; that
// measured 26.5 ms -> 48.2 ms on the office scene. The likely cause is that the
// shrunken neighbours freed up inliner budget for hit_box, which carries two
// dynamically indexed 8-element arrays for hole CSG and is called from every BVH
// leaf loop. Gating the call edge leaves the kept arms byte-identical and simply
// orphans the stripped ones: same office scene, 26.2 ms -> 25.4 ms.
//
// The gap between those two shapes is far larger than the win itself, so if a
// kind ever needs gating somewhere new, gate where it is *called*, never inside
// what is called.
//
// Both dispatches on primitive kind are covered: hit_prim in intersect.wesl and
// normal_at in shade.wesl. A kind must stay enabled if it appears in prims or in
// blockers; see featuresFor in device.go.
const FEAT_PRIM_SPHERE: bool = true;
const FEAT_PRIM_PLANE: bool = true;
const FEAT_PRIM_BOX: bool = true;
const FEAT_PRIM_CYLINDER: bool = true;
const FEAT_PRIM_CONE: bool = true;
const FEAT_PRIM_TORUS: bool = true;
const FEAT_PRIM_RING: bool = true;
const FEAT_PRIM_LENS: bool = true;

// Point-light attenuation: att = 1 / (LIGHT_ATTEN_BASE + LIGHT_ATTEN_QUADRATIC × d²)
const LIGHT_ATTEN_BASE: f32 = 0.5;        // Floor — stops singularity at d = 0
const LIGHT_ATTEN_QUADRATIC: f32 = 0.08; // How quickly intensity falls off with distance

struct Hit {
    t: f32,
    idx: u32,
    kind: u32,
    inst_idx: u32, // 0xffffffff when the hit is on static (non-instanced) geometry
};

const HIT_NO_INSTANCE: u32 = 0xffffffffu;
