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
    // Lengths of the two plane-index lists inside idx_tables, so the tracer
    // iterates only planes instead of scanning every primitive.
    plane_count: u32,
    blocker_plane_count: u32,
    flame_particle_count: u32,
    // adaptive_aa: when set, primary rays at pixel edges are compared; mismatches
    // trigger a 2×2 supersample for that pixel.
    adaptive_aa: u32,
    // thin_glass_ghost: when set, primary thin-glass hits spawn a second-pane
    // reflection lobe (double-pane look). Toggled in-app with key 8.
    thin_glass_ghost: u32,
    // Light cluster grid: world -> cell index transform for light_span().
    // light_grid_inv_cell of zero collapses every point onto cell 0, which is
    // how the degenerate one-cell grid reproduces a full light loop.
    // light_grid_dim.w is where the clustered light indices start, relative to
    // the grid's own base. table_base holds the idx_tables word offsets of the
    // blocker-plane list (x) and the light grid (y), plus the number of wide
    // lights leading the grid region (z).
    light_grid_min: vec4<f32>,
    light_grid_inv_cell: vec4<f32>,
    light_grid_dim: vec4<u32>,
    table_base: vec4<u32>,
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
// info: interior -> (left, right, 0, 0), leaf -> (prim0, prim1, 0, count)
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
    zones: vec4<u32>,      // zoneBase, zoneCount, _, _
    mip: vec4<u32>,        // mipBase (vec2 index), l0nx, l0nz, levelCount
    coarse: vec4<f32>,     // cwx, cwz, cInvDx, cInvDz
};

struct TerrainZone {
    info: vec4<u32>,    // vertBase, vertCount, texture, fadeTo
    params: vec4<f32>,  // fadeWidth, scale, angle, bump
    albedo: vec4<f32>,
    bounds: vec4<f32>,  // minX, minZ, maxX, maxZ
    surf: vec4<f32>,    // rough, reflect, specular, shininess
};

struct TerrainZoneVert {
    pos: vec4<f32>, // x, z, _, _
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

@group(0) @binding(26)
var<storage, read> terrain_zones: array<TerrainZone>;

@group(0) @binding(27)
var<storage, read> terrain_zone_verts: array<TerrainZoneVert>;

// Adaptive AA pass-1 scratch: linear HDR color and the primary-ray hit fingerprint.
@group(0) @binding(24)
var<storage, read_write> hdr_pixels: array<vec4<f32>>;

@group(0) @binding(25)
var<storage, read_write> aa_hits: array<u32>;

// Adaptive AA work list. aa_classify appends one packed entry per pixel that
// needs supersampling (see aa_pack_task in trace.wesl) and aa_resolve consumes
// the list, so the expensive pass launches threads only for real work instead
// of one per screen pixel.
@group(0) @binding(28)
var<storage, read_write> aa_list: array<u32>;

// aa_dispatch is both the append counter and the indirect dispatch header for
// aa_resolve: [workgroup_count_x, 1, 1, task_count]. The host resets it to
// [0, 1, 1, 0] each frame.
@group(0) @binding(29)
var<storage, read_write> aa_dispatch: array<atomic<u32>>;

// AA_RESOLVE_WG is the 1D workgroup size of the compacted aa_resolve pass and
// must match its @workgroup_size and the host's indirect header arithmetic.
const AA_RESOLVE_WG: u32 = 64u;

@group(0) @binding(14)
var<storage, read> inst_templates: array<TemplateRecord>;

@group(0) @binding(15)
var<storage, read> inst_records: array<InstanceRecord>;

@group(0) @binding(16)
var<storage, read_write> profile_counters: array<atomic<u32>>;

// idx_tables concatenates every u32 index list the tracer walks, because Metal
// allows a stage only 32 buffer bindings and this megakernel had used them all.
// The regions sit at fixed bases (see params.plane_base / light_grid_base):
//
//   - plane indices: the infinite planes inside prims. Planes are excluded from
//     the BVH, so rays walk this short list instead of scanning primitives.
//   - blocker plane indices: the same for the shadow-blocker set.
//   - light cluster grid: [wide | cell offsets | light indices]. The wide list
//     is the scene-spanning lights every point evaluates; the rest is a CSR row
//     table over grid cells plus the light indices it points into, so a lit
//     point walks only the lights that reach its own cell (lightgrid.go).
@group(0) @binding(17)
var<storage, read> idx_tables: array<u32>;

const BVH_TAG_TLAS: u32 = 1u;

const PRIM_FLAG_TRANSFORMED: u32 = 1u;
const PRIM_FLAG_GLASS_THIN: u32 = 2u;
const PRIM_FLAG_TEXTURE_NORMAL_MAP: u32 = 4u;

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
const TEX_TILES: u32 = 14u;
const TEX_MARBLE_2: u32 = 15u;
const TEX_PARQUET_FLOOR: u32 = 16u;
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

// optimization: skip the shadow ray when the light cannot move the output pixel
// by this many 8-bit levels. The light is then added as if unshadowed, with no
// BVH shadow traversal.
//
// The test (display_step_levels in math.wesl) compares the *display* step the
// light's contribution makes on top of the radiance already accumulated at the
// hit, rather than thresholding linear radiance directly. Linear radiance is the
// wrong space for this decision in both directions: the tonemap compresses
// highlights, so a contribution that clears a fixed linear bar can still be
// worth a hundredth of a level on a bright surface, while against a near-black
// interior the same bar sits well above one level and the old gate was quietly
// erasing visible shadows. Shadow rays are the largest ray population in an
// interior view — around 60% of all rays in office-sunset — so this is where
// there is the most work to skip without anything to see.
//
// Contributions below the bar are still *added*, so the error per light is
// bounded by the threshold and only accumulates across lights that are each
// individually invisible.
//   0.0  → never skip (exact, slowest)
//   0.5  → sub-level exactness; ~2x the shadow rays of the linear gate it
//          replaced, because gamma makes dim contributions visible against a
//          dark interior and almost nothing qualifies as skippable
//   4.0  → current default; half the 8.2-level step of the 15-bit output, so a
//          skipped shadow stays inside one quantized bucket
//   8.0  → a full quantization step; measurably faster but visibly thins the
//          contact shadows under desks and furniture
const SHADOW_SKIP_LEVELS: f32 = 4.0;

// Throughput handed to the ghost (second glass pane) lighting pass. Small enough
// that every light's shadow test folds to "not worth a ray", which is what makes
// the pass cheap; the pane is a faint overlay where shadowing is not missed.
const GHOST_SHADOW_TW: f32 = 0.001953125;

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

// optimization: the minimum throughput a *semi-glossy diffuse* reflection lobe
// (Surface.Reflect on a diffuse/checker material) must carry before it is worth
// a ray, on bounce paths only. This is deliberately far coarser than
// SEG_MIN_CONTRIB, which governs mirror and glass lobes.
//
// This applies from depth 1 down. Primary hits keep using SEG_MIN_CONTRIB, so a
// deliberately subtle authored sheen — the server-room floor ships at
// reflect 0.06, and props sit at 0.02 — always renders. An earlier version
// applied the threshold at every depth and silently erased every reflect < 0.05
// surface in the scene, which is a scene-authoring trap: the author sets a value
// and gets nothing back.
//
// Mirror and glass lobes carry most of their segment's radiance, so a faint one
// still resolves a recognizable image and the eye tracks it. A glossy lobe is by
// construction a *perturbation*: the surface already contributed
// `lit * (1 - refl)`, and dropping the lobe falls back to `lit`, i.e. the fully
// diffuse surface. The error is therefore bounded by refl × |reflected − local|,
// not by the reflected radiance itself.
//
// SEG_MIN_CONTRIB alone let glossy chains run absurdly deep: the office towers
// use refl 0.15–0.4, so a 0.15 surface reflecting a 0.15 surface still cleared
// 1/1024 at four bounces (0.15⁴ = 5e-4) — four BVH traversals plus four full
// direct-lighting evaluations to move a tonemapped pixel by well under one 8-bit
// level. Roughly a quarter of all glossy rays in this scene were in that tail.
//
//   0.0009765625 → old behaviour (SEG_MIN_CONTRIB); ~25% of glossy rays invisible
//   0.05         → current default; keeps the first bounce everywhere, prunes
//                  the tail from the second bounce down on weak reflectors
//   0.15         → also drops the first bounce on refl < 0.15 surfaces (visible)
const GLOSSY_MIN_CONTRIB: f32 = 0.05;

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
