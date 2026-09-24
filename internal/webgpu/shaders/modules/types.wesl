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
    // soft_shadows: when set, main() records the dominant light's shadow into
    // shadow_aux and the shadow_soften pass widens it into a penumbra. Occupies
    // what was struct padding before light_grid_min's vec4 alignment, so Params
    // keeps its size.
    soft_shadows: u32,
    // refl_filter: when set, the primary glossy lobe is traced as an unperturbed
    // mirror ray, held out of the pixel colour in refl_aux, and blurred by the
    // refl_blur passes before being added back. Clear, and the lobe is jittered
    // and added inline as it always was.
    refl_filter: u32,
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
    // bounce_rays: cosine-weighted indirect rays cast from each primary diffuse
    // hit. 0 is off, which is the default; key 8 toggles it. Capped by
    // BOUNCE_MAX_RAYS in trace.wesl. See docs/diffuse-bounce.md.
    bounce_rays: u32,
    // bounce_ambient: the share of the ambient constant left in place while the
    // bounce is on. 0 replaces ambient with the traced bounce outright, which is
    // the comparison worth looking at; 1 adds the bounce on top and double-counts
    // indirect light on purpose.
    bounce_ambient: f32,
    // bounce_atrous: how many edge-avoiding wavelet passes run over the
    // indirect term, 0..BOUNCE_ATROUS_MAX. 0 leaves it unfiltered spatially.
    // Its parity also says which of bounce/bounce_h holds the final result.
    bounce_atrous: u32,
    // bounce_temporal: bit 0 enables reprojection and accumulation; bit 1 says
    // this frame must ignore whatever history the buffer holds (first frame
    // after a resize, a scene swap, or the toggle coming on). Bits 2 and up are
    // a free-running frame counter, which is what decorrelates the sampler
    // between frames -- see BOUNCE_TEMPORAL_FRAME_SHIFT.
    bounce_temporal: u32,
    // The previous frame's camera basis, for reprojection. Orthonormal, so
    // inverting pixel_ray_dir is three dot products.
    prev_cam_pos: vec4<f32>,
    prev_fwd: vec4<f32>,
    prev_right: vec4<f32>,
    prev_up: vec4<f32>,
    // (temporal_alpha, max_history, phi_normal, phi_depth).
    bounce_filt: vec4<f32>,
    // bounce_shadow_rr: the display-level contribution at which a light's
    // shadow ray is *always* traced from a bounce hit. Below it the ray is
    // rouletted; 0 disables the roulette and traces every one, as before.
    // See BOUNCE_SHADOW_RR_MIN_P.
    bounce_shadow_rr: f32,
    // bounce_flags: BOUNCE_FLAG_* below.
    bounce_flags: u32,
    // bounce_clamp: how many standard deviations of the current neighbourhood
    // the reprojected history may sit outside before it is pulled back in.
    // 0 disables the clamp, which is what the filter shipped with. See
    // BOUNCE_CLAMP_* below.
    bounce_clamp: f32,
    // bounce_motion: how far the camera moved last frame, in world units, plus
    // a rotation term expressed the same way. Drives BOUNCE_FLAG_VEL_HISTORY.
    bounce_motion: f32,
    // bounce_tile: edge in pixels of the coherent sampling tile. A uniform and
    // not a constant because a shader constant cannot be swept without
    // regenerating the Metal library, and this is a look knob.
    bounce_tile: f32,
    // bounce_spread: how much of the per-pixel rotation survives inside a
    // coherent tile, 0..1. 0 gives every pixel in the tile one identical
    // sample; 1 is the per-pixel sampler with no coherence at all.
    bounce_spread: f32,
};

// A clamped history is one the reprojection got wrong, so it should stop being
// treated as 32 frames of evidence. BOUNCE_CLAMP_TRUST is the share of the
// history length that survives when the clamp bites hard, which is what lets a
// pixel snap back instead of easing out of the error over the ~20 frames the
// alpha floor would otherwise take.
const BOUNCE_CLAMP_TRUST: f32 = 0.25;

// How far the clamp has to move a value, relative to the neighbourhood's own
// spread, before that trust penalty applies. Below this the clamp is just
// trimming noise and the history is fine.
const BOUNCE_CLAMP_BITE: f32 = 0.5;

// BOUNCE_FLAG_BLUE swaps the hash-per-sample white-noise direction sampler for
// a low-discrepancy one: an R2 sequence over (frame, sample) Cranley-Patterson
// rotated by a screen-space interleaved-gradient offset. Same ray count, better
// distributed, so less variance for the denoiser to remove.
const BOUNCE_FLAG_BLUE: u32 = 1u;

// BOUNCE_FLAG_HALF traces the hemisphere on half the pixels each frame, in a
// checkerboard that inverts every frame. The other half keeps its reprojected
// history, or is filled from its neighbours where it has none.
const BOUNCE_FLAG_HALF: u32 = 2u;

// BOUNCE_FLAG_HALF_TILE makes that checkerboard one 8x8 workgroup per cell
// instead of one pixel. A per-pixel checkerboard halves the ray count and
// almost nothing else: traced and skipped pixels share a 32-lane SIMD group, so
// the group still walks the gather and the skipped lanes are merely masked off.
// Deciding per workgroup means whole SIMD groups skip it.
const BOUNCE_FLAG_HALF_TILE: u32 = 4u;

// BOUNCE_FLAG_HIST_NEAREST forces the history fetch back to a single nearest
// tap, so bilinear can be measured against what it replaced.
const BOUNCE_FLAG_HIST_NEAREST: u32 = 8u;

// BOUNCE_FLAG_MINIFY discounts a history sample by how badly the fetch is
// undersampling it, on the theory that backing away magnifies the previous
// frame so a 2x2 tap point-samples a wider footprint than it covers.
//
// **Off by default: measured to do nothing.** Front office, backing away at
// 0.4 units/frame, low-frequency error in linear radiance: 0.889 without,
// 0.894 with. The theory is sound and the mechanism is not what is wrong here
// -- backing away is worst because it continuously reveals new floor at the
// bottom of frame, so that band never accumulates any history to undersample.
const BOUNCE_FLAG_MINIFY: u32 = 16u;

// BOUNCE_FLAG_COHERENT rotates the low-discrepancy sampler per *tile* rather
// than per pixel, so neighbouring pixels trace near-parallel bounce rays.
//
// Incoherence is the single largest measured inefficiency in the renderer.
// Villa hearth, 2 rays, same ray count and same shading: 157.2 ms of bounce
// with per-pixel rotation against 69.2 ms with none at all. 88 ms of a 201 ms
// frame is threads in a SIMD group walking different parts of the tree and
// touching different geometry at the far end.
//
// Taking the rotation off entirely is not an option -- it is what decorrelates
// neighbouring pixels, and without it a tile's pixels all sample the same
// direction and share one estimate. Rotating per tile keeps that correlation
// *inside* a tile, where the a-trous is about to average over it anyway, while
// the frame counter keeps successive frames independent so the temporal mean
// still converges. Spatially correlated, temporally decorrelated: the opposite
// of the usual arrangement, and it costs nothing to try.
const BOUNCE_FLAG_COHERENT: u32 = 512u;

// Default tile edge for that rotation, overridden by params.bounce_tile. The
// knob trades coherence against how much of a tile's noise is shared. Villa hearth, 2 rays, bounce cost and
// RMS against a 16-ray reference, the fresh column being 4 frames of history --
// the case with no temporal averaging to hide the correlation:
//
//	tile   bounce ms   settled    fresh   tile spread
//	1         154.8    0.00941  0.00919     --         per-pixel, what this replaces
//	2         131.3    0.00941  0.00930    0.42
//	4          97.0    0.00947  0.00984    0.86        <- default
//	8          69.0    0.00959  0.01154    1.72        visibly blotchy when fresh
//
// The cloud a tile produces is not really a sampling artifact, it is a *count*
// one. With one rotation per tile, all 16 pixels of a 4x4 draw the same
// direction and return the same estimate, so the a-trous averaging over that
// tile has 16 copies of one sample to work with rather than 16 samples. The
// tile's effective sample count is 1.
//
// BOUNCE_SPREAD fixes that without giving the coherence back. Each pixel keeps
// a *fraction* of its own rotation on top of the tile's, so the tile's rays
// span a small cone instead of a single direction: near enough to walk the same
// part of the tree, far enough apart that averaging them means something.
const BOUNCE_COHERENT_TILE: f32 = 2.0;

// BOUNCE_FLAG_ADAPT spends extra rays on a point that was outside the previous
// frame's frustum, and so has no history to average however long it waits.
//
// Backing away widens the view, so a border band around the whole frame is new
// every frame: measured at 12.1% of pixels at 0.4 units/frame against 0.2%
// walking forwards, which is the whole of that direction's asymmetry. Those
// pixels cannot be helped by reuse -- the band is deeper than any sane search
// -- and cannot be helped by waiting, because the next frame reveals a fresh
// one. Samples are the only thing that reaches them.
const BOUNCE_FLAG_ADAPT: u32 = 128u;

// Ray multiplier for such a pixel. At 12% of the frame, 4x costs about a third
// more bounce rays and buys the band the convergence the rest of the frame gets
// from 30 frames of accumulation.
const BOUNCE_ADAPT_BOOST: u32 = 4u;

// BOUNCE_FLAG_REUSE seeds a freshly disoccluded pixel from a converged
// neighbour instead of leaving it on its own one-or-two-sample estimate.
//
// **Off by default: measured worse.** It finds a usable neighbour for 0.8% of
// the pixels that need one backing away (610 of 79,242), because the region is
// a border band far deeper than the search, and every one of its neighbours is
// in the same band with no history either. Walking forwards it fires on 96% of
// a much smaller set and makes that direction *worse* -- 0.365 to 1.698 -- by
// planting a neighbour's value across a lighting gradient the plane test cannot
// see. Same surface is not the same lighting, and the a-trous at least has a
// luminance edge-stop where this has none.
const BOUNCE_FLAG_REUSE: u32 = 64u;

// How much history a reused estimate is credited with. It is a neighbour's
// answer, not this pixel's, so it must not arrive already converged: seeding
// the value kills the noise burst, and the low length keeps alpha high so the
// pixel's own samples overwrite the borrowed one within a few frames. Seeding
// the *length* as well would trade a noise burst for a smear.
const BOUNCE_REUSE_LEN: f32 = 4.0;

// Minimum history a neighbour must have before it is worth borrowing from.
// Below this it is barely better converged than the pixel doing the borrowing.
const BOUNCE_REUSE_MIN_LEN: f32 = 6.0;

// How far off the borrower's surface plane a neighbour may sit, relative to
// depth. A plane test, not a depth test: the floor is the surface this exists
// for and it is seen at a grazing angle, where depth changes fast along the
// surface but offset from the plane does not.
const BOUNCE_REUSE_PLANE_REL: f32 = 0.02;

// BOUNCE_FLAG_VEL_HISTORY shortens the running mean while the camera moves. A
// 32-frame window at 0.4 units/frame reaches back over 12 world units of camera
// travel, and every one of those frames was resampled into this one.
const BOUNCE_FLAG_VEL_HISTORY: u32 = 32u;

// How hard camera speed shortens the history, in inverse world-units per frame.
// eff = max_history / (1 + K * speed), so 0.4 units/frame at K = 7.5 takes a
// 32-frame window down to 8.
const BOUNCE_MOTION_K: f32 = 7.5;

// Ceiling on the minification discount, so a grazing surface whose depth ratio
// explodes cannot throw the history away entirely.
const BOUNCE_MINIFY_MAX: f32 = 8.0;

// BOUNCE_TEMPORAL_ON / _RESET are the bits of params.bounce_temporal.
const BOUNCE_TEMPORAL_ON: u32 = 1u;
const BOUNCE_TEMPORAL_RESET: u32 = 2u;
// Where the frame counter starts inside params.bounce_temporal. It is packed
// into the flag word rather than given a field because it is only ever read
// through the same uniform the flags are, and Params has no cheap 16-byte hole
// left.
const BOUNCE_TEMPORAL_FRAME_SHIFT: u32 = 2u;

// BOUNCE_ATROUS_MAX is how many a-trous entry points exist. Each doubles its
// hole size, so n passes reach a radius of 2*(2^n - 1) pixels for a fixed 5x5
// tap count each -- the same separable-cost argument the penumbra filter makes
// in soft-shadows.md.
const BOUNCE_ATROUS_MAX: u32 = 4u;

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
    surf2: vec4<f32>,   // reflect_blur, transmit_blur, spare, spare
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
    color: vec4<f32>, // base color (r, g, b, sub-light count; 0 = default 3)
    param: vec4<f32>, // brightness, jitter, flicker, speed
    phase: vec4<f32>, // seed, flame_enabled (1/0), flame_scale, emitter radius (0 = default)
    flame_ember: vec4<f32>, // rgb, max flicker travel distance (0 = uncapped)
    flame_mid: vec4<f32>,
    flame_tip: vec4<f32>,
    flame_ash: vec4<f32>,
};

// Light mirrors GPULight in scene.go (std430, 48-byte stride).
// Mirrors GPULight in scene.go (std430, 64-byte stride).
struct Light {
    pos: vec4<f32>,
    color: vec4<f32>,
    falloff: vec4<f32>, // cullR2, invR2, spotYaw, spotPitch
    shape: vec4<f32>,   // source radius (0 = use PENUMBRA_LIGHT_RADIUS), skip_penumbra_vote (1/0), _, _
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
    surf2: vec4<f32>,   // reflect_blur, and three spare
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
var<storage, read_write> ao_volume: array<f32>;

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

// Two filter channels, partitioned by which lights write them — not a split of
// one occlusion field. `penumbra_blur_vote = false` (moon, distant sun) goes in
// the muted channel and is blurred at *its* width; everything else stays in the
// local channel. That is a partition of the lighting, so a uniform moon umbra
// cannot dictate a campfire's radius, and a sun shaft is not gathered at lamp
// width.
//
// Splitting *one* frac by a per-pixel mix factor (width, angle, distance) is
// still wrong: it is not a partition of unity and invents signal out of a
// uniform umbra. Do not go back to that.
struct ShadowAux {
    // Local lights (vote = true). Per channel, the fraction of this point's
    // direct light that something is blocking. Albedo cancels out of the ratio,
    // which is the whole reason it is stored as a ratio: filtering the blocked
    // *radiance* directly turns the filter into an unsharp mask on the texture.
    //
    // The vertical pass overwrites this slot with the *target* fraction it
    // filtered to, so supersample_edge can reproduce the same correction.
    frac: vec3<f32>,
    pen_px: f32,
    // Shading normal and camera distance, so the filter can tell whether a
    // neighbour lies on the same surface. The packed primary-hit fingerprint the
    // AA pass uses is per *primitive*, and scene geometry is authored as many
    // small boxes — a rack, a wall of panels — so a continuous surface is
    // covered in fingerprint boundaries. Gating on it chopped the blur along
    // every seam and flipped it on and off as those seams moved.
    normal: vec3<f32>,
    // Zero marks a pixel with no diffuse surface (sky), which the filter skips.
    depth: f32,
    // Written by the horizontal half of the separable filter and read by the
    // vertical half: the row-blurred fraction and the row maximum of pen_px.
    frac_h: vec3<f32>,
    r_h: f32,
    // Unoccluded local-channel light, already scaled by throughput, AO and any
    // glossy (1 - refl) split.
    full: vec3<f32>,
    // 1 once the vertical pass has written a target into frac.
    soft_applied: f32,
    // Muted channel: lights with penumbra_blur_vote = false. Same layout as
    // the local channel, gathered only against the same channel's neighbours.
    frac_m: vec3<f32>,
    pen_m: f32,
    frac_h_m: vec3<f32>,
    r_h_m: f32,
    full_m: vec3<f32>,
    soft_applied_m: f32,

    // --- reflection filter ---
    //
    // The glossy lobe rides in this record rather than a buffer of its own
    // because there is no buffer of its own to be had: Metal allows a compute
    // stage 31 buffers and the megakernel uses every one (the same wall that put
    // every index list into idx_tables). It costs nothing to share — both are
    // per-pixel screen-space scratch written by main and consumed by a separable
    // filter, and the reflection filter wants `normal` and `depth` above anyway.
    //
    // A rough surface scatters the reflected ray through a fixed *angle*, so the
    // image it reflects is smeared by that angle times how far away that image
    // is. That is the penumbra filter's structure with the blocker distance
    // swapped for the reflected hit distance — a quantity a real ray already
    // measured, so this is a filter over exact data and not a reconstruction.

    // The lobe's radiance, weighted by throughput and the surface's reflect
    // fraction, held out of hdr_pixels until refl_blur_v adds it back. The
    // vertical pass overwrites it with the filtered result, so aa_resolve can
    // hand a tap the neighbourhood's conclusion instead of its own sharp lobe.
    refl_color: vec3<f32>,
    // Distance from the reflecting surface to what the reflected ray hit, or
    // negative where there is no lobe. Sizes the blur, and gates it: neighbours
    // whose reflected rays landed at very different distances are looking at
    // different objects and must not mix.
    refl_t: f32,
    // Row-blurred colour, written by the horizontal pass, read by the vertical.
    refl_color_h: vec3<f32>,
    refl_radius_px: f32,

    // The transmitted lobe, carried separately from the reflected one.
    //
    // Glass forks two lobes at one hit and blurs them by different amounts —
    // jitter_dir is called with `rough` on the reflection and `rough * 0.35` on
    // the refraction — so one shared channel could only ever draw both at one
    // width. They are also the two things an author most wants to dial
    // independently: a frosted pane that still reflects sharply, or a mirror-ish
    // pane you can barely see through. Keeping them apart from the start costs
    // 32 bytes and removes the need to unpick it later.
    refr_color: vec3<f32>,
    refr_t: f32,
    refr_color_h: vec3<f32>,
    refr_radius_px: f32,

    // The reflecting/refracting surface itself, for the same-surface gate and
    // for the apparent-distance term in the radius.
    //
    // Deliberately not the `normal`/`depth` above. Those are written only by a
    // *diffuse* primary hit, and glass never takes that path — but filling them
    // in for glass would also enrol glass pixels in the penumbra filter's
    // gathers carrying frac = 0, which reads as "fully lit" and would lighten
    // every shadow beside a window.
    lobe_normal: vec3<f32>,
    lobe_depth: f32,

    // --- diffuse bounce filter ---
    //
    // Same argument as the reflection filter above: there is no spare buffer
    // binding for a record of its own, so the indirect term's whole
    // reconstruction pipeline rides here. Five vec4s, 80 bytes per pixel.
    //
    // bounce/bounce_h are the a-trous ping and pong. xyz is indirect radiance
    // and w carries the temporal history length forward through the passes,
    // which is what the filter uses in place of SVGF's stored luminance
    // moments: a short history means the estimate is untrustworthy and should
    // be filtered hard, which is the same signal variance was providing.
    bounce: vec3<f32>,
    bounce_len: f32,
    bounce_h: vec3<f32>,
    bounce_len_h: f32,

    // The temporal history proper. bounce_temporal *reads* hist at the
    // reprojected pixel and *writes* hist_w at its own, so the two never alias
    // inside one dispatch; bounce_resolve copies w -> hist at end of frame.
    // That split is what makes the reprojection safe without ping-ponging the
    // whole record on frame parity.
    bounce_hist: vec3<f32>,
    bounce_hist_len: f32,
    bounce_hist_w: vec3<f32>,
    bounce_hist_len_w: f32,

    // Last frame's shading normal and camera distance, written by
    // bounce_resolve after everything else has read this frame's. Reprojection
    // compares against these to decide whether the pixel it landed on was
    // looking at the same surface.
    bounce_prev_n: vec3<f32>,
    bounce_prev_z: f32,
};

@group(0) @binding(18)
var<storage, read_write> shadow_aux: array<ShadowAux>;

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

// SHADOW_NEAREST_BLOCKER makes a shadow ray report the *nearest* blocker rather
// than the first one traversal happens to find.
//
// Visibility does not care — any blocker means shadowed — but the penumbra width
// does: w_pen = r*t/(ldist-t) is monotonic in t, so an any-hit distance biases
// the width wide. Worse, the bias is not stable: bvh_child_push orders children
// by AABB *entry*, which is not order by hit distance, so which blocker is found
// first can flip between adjacent pixels as the ray direction shifts, and the
// penumbra width flips with it.
//
// True costs a full traversal with no early exit on every shadow ray.
const SHADOW_NEAREST_BLOCKER: bool = false;

// Which blocker sets the penumbra width when several occlude the *same* light
// along one shadow ray. True takes the one farthest from the receiver, which is
// the one with the widest penumbra since pen = r*t/(ldist-t) rises with t.
//
// The distinction from the multi-*light* case is the whole point, and it runs
// the opposite way. Across lights, each contributes independently to how bright
// the pixel is, so a sharp contact shadow really is visible laid over a soft one
// and the sharpest has to win -- that is SHADOW_WIDTH_MEAN_POWER's job. Across
// blockers of one light, occlusion is binary: where the first already blocks it,
// a second adds nothing, so its sharp edge is *invisible* in the overlap. The
// width that matters there belongs to the outer boundary of the union, which is
// the widest blocker's. A near blocker's sharp edge still governs wherever its
// shadow reaches past the soft one, because there it is the only blocker.
//
// Picking the nearest instead puts a step in the width field at every inner
// silhouette -- a boundary across which the light level barely changes, so the
// image has no edge to hide it. That is exactly what showed up on the uplight
// wall once the blocker BVH started ordering its children correctly.
const SHADOW_WIDEST_BLOCKER: bool = true;

// Whether the widest search is exact. False keeps the early exit and relies on
// far-first child ordering to reach the farthest blocker first, which is what
// makes it cost the same as the plain any-hit it replaces.
const SHADOW_WIDEST_EXACT: bool = false;

// How several blocked lights' penumbra widths combine at one point, as the
// *negated* exponent of a contribution-weighted power mean (see shadow_aux_note).
// The accumulator sums w/pen^P and inverts with a 1/P power, so this constant is
// P and the mean it produces is the power mean of exponent -P:
//
//   -1   -> arithmetic mean; the widest shadow smears the sharpest away.
//           This is what shipped before, and it is reproduced exactly: at -1 the
//           two expressions cancel to sum(w*pen)/sum(w).
//    0   -> geometric mean. Not a usable setting -- 1/P divides by zero -- but
//           it is the limit the small values approach, not the arithmetic mean.
//    1   -> harmonic mean; a contact shadow keeps its edge through a broad one.
//   inf  -> the minimum; the sharpest blocked light wins outright.
//
// Measured on the front-office uplight frame, as the peak slope of the sunlight
// shaft's near (contact) edge, which is 1.00x at -1: 1.96x at 0.25, 2.01x at
// 0.5, 2.12x at 1, 2.19x at 2, 2.13x at 4, 2.11x at 8. Nearly all of the gain is
// in leaving the arithmetic mean at all, and the curve is flat from 0.25 up, so
// the setting wants to sit in the middle of that plateau rather than at its
// maximum -- the far end trades toward "sharpest wins", which is the setting
// most likely to under-filter a broad shadow where a tight one crosses it.
//
// **1 was too far up that plateau, and the predicted failure is what it did.**
// The courtyard uplight in office-sunset throws a broad arc onto the wall behind
// it. Along the far half of that arc one light is blocked and the width is its
// own 5-8 px, which the filter draws properly. Approaching the lamp, three more
// lights fall into the same silhouette; the tightest asks for 1.4 px, and at
// P = 1 the mean collapses onto it -- 7.8 px at x=374 down to 2.1 px at x=518,
// on one continuous shadow cast by one occluder. So the arc is softened at the
// far end and left nearly hard at the near one, and the bright wall inside the
// bend keeps a crisp border against an otherwise smooth gradient. Reported as a
// bright patch in a softened penumbra, which is what a hard edge looks like when
// its surroundings are soft.
//
// Edge steepness across that arc (max vertical gradient in linear radiance;
// the hard render is the ceiling, lower is softer):
//
//   x         370     410     430     450     470     485
//   hard   0.0542  0.0622  0.0673  0.0765  0.0737  0.0977
//   P = 1  0.0219  0.0218  0.0249  0.0376  0.0360  0.0736   <- 25% softened
//   P=0.25 0.0219  0.0218  0.0249  0.0376  0.0303  0.0535   <- 45%
//   P = -1 0.0219  0.0218  0.0211  0.0376  0.0276  0.0442   <- 55%
//
// 0.25 is the other end of the same flat plateau: it keeps 1.96x of the 2.12x
// contact-hardening gain the power mean exists for, and gives back most of the
// arc. The arithmetic mean recovers a little more and is not worth it -- that is
// the setting whose failure the whole power mean was built to fix.
//
// Scope, against P = 1 at 1024x640: the reported frame moves 1.66% of pixels,
// office 0.06%, night villa 0.07%, and campfire-shadow, penumbra-test and the
// atrium are bit-identical -- those have one blocked light per pixel, so the
// mean never arbitrates and no exponent can change them. Mean linear radiance
// moves by at most 0.03% on any of them.
//
// This is a move along a trade-off, not a repair. One radius per group of lights
// cannot draw a sharp core and a broad shoulder at the same edge, and no
// exponent makes it. Per-light radii can, and were built and reverted: see
// "tried and reverted: a per-band radius" in docs/soft-shadows.md.
const SHADOW_WIDTH_MEAN_POWER: f32 = 0.25;

// Widths below this count as zero, so a true contact shadow lands on a hard edge
// instead of dividing by something near zero.
const SHADOW_WIDTH_FLOOR: f32 = 0.25;

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

// How much of a light a glass blocker lets past, for glass that opts into
// casting a shadow with `shadow = true`. 1 is no shadow at all (the default for
// glass, which simply is not packed as a blocker); 0 is as dark as a wall.
//
// 0.5 is "half as dark as a regular shadow", which is what this is for: a pane
// packed as a blocker is otherwise binary, and a solid shadow under clear glass
// reads as a hole in the floor. It is a flat fraction, not a transmission model
// -- it ignores the pane's own `transmit`, its tint and its thickness, so two
// panes in a row land on 0.25 by multiplication rather than by absorption.
const GLASS_SHADOW_TRANSMIT: f32 = 0.5;
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
// ao_enabled is a bitfield, not a bool: bit 0 turns the baked volume on, bit 1
// restricts it to the ambient term instead of scaling direct light too.
const AO_ENABLED: u32 = 1u;
const AO_INDIRECT_ONLY: u32 = 2u;

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
//   1.0  → current default; see below
//   4.0  → half the 8.2-level step of the 15-bit output, so a skipped shadow
//          stays inside one quantized bucket. That budget is per *pixel*, and
//          the gate does not fire per pixel: it fires on an iso-distance
//          surface around each light, drawing a hard arc across every wall in
//          range where shadows abruptly stop being traced. A 4-level step along
//          a smooth curve is an obvious ring; the same step scattered over
//          isolated pixels would not be. Reported as "round hard rings of light"
//          and isolated by ablation — setting this to 0 removes the arc, while
//          zeroing LIGHT_CULL_EPS leaves it untouched. It is gone at 1.0 and
//          still clearly visible at 2.0 under a 110x high-pass.
//   8.0  → a full quantization step; measurably faster but visibly thins the
//          contact shadows under desks and furniture
//
// Lowered 4.0 → 1.0 because it measured free. Interleaved best-of-N on a quiet
// machine: server room 7.6 → 7.7 ms, villa 21.5 → 21.4, manhattan 8.9 → 8.9,
// default 5.2 → 5.3. An earlier non-interleaved sweep suggested this cost 67%,
// which was entirely machine noise — worth remembering before trading a visible
// artifact away for a number measured that way.
// Consulted only when the penumbra filter is off (`RAYTRACER_SOFT_SHADOWS=0`).
// With the filter on, a skipped light takes its whole ShadowAux record with it —
// see the note at the gate in shade.wesl for why that cannot be decided per
// pixel against `lit`.
const SHADOW_SKIP_LEVELS: f32 = 1.0;

// --- bounce shadow roulette ---
//
// Shadow rays cast *from bounce hits* are the single largest cost the traced
// indirect term carries. Measured in the villa at the hearth, 1024x640: the
// bounce adds 192 ms, and 136 ms of that (71%) is shadow rays -- each bounce ray
// provokes 3.09 of them, because in a dark room SHADOW_SKIP_LEVELS' display-space
// gate never fires and every one of the fire's sub-lights counts as significant.
//
// The obvious saving is to raise that threshold for bounce hits, and it is a
// trap. The note on SHADOW_SKIP_LEVELS above records why: a threshold on a
// smoothly varying quantity does not fire per pixel, it fires on an iso-surface
// around each light, "drawing a hard arc across every wall in range where
// shadows abruptly stop being traced". Reported once already as round hard rings
// of light. Raising the same threshold at bounce hits would draw the same rings
// in the indirect term.
//
// So the decision is randomised instead. Each light's shadow ray is kept with
// probability p proportional to how much that light's shadow could shift the
// pixel, and a kept light is scaled by 1/p: an unbiased estimator of exactly
// what the full loop computes. The iso-surface becomes white noise, and white
// noise is what the a-trous pass is already there to remove. Nothing in the
// frame is quietly darkened or brightened; the mean is preserved by
// construction.
//
// BOUNCE_SHADOW_RR_MIN_P floors the probability, which bounds the variance: a
// kept light is amplified by at most 1/0.25 = 4x, and only lights already worth
// a few display levels are ever amplified at all.
//
//	0.15 → cheaper, visible sparkle on the first frames after a disocclusion
//	0.25 → current default
//	0.5  → barely cheaper than tracing every ray
const BOUNCE_SHADOW_RR_MIN_P: f32 = 0.25;

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
// Width of the smooth join between the flat top and the falloff, in units of the
// attenuation denominator. 0 reproduces a hard `min()` and its visible ring;
// 0.0025 still showed the ring under 140x high-pass amplification; 0.01 does not.
// See light_atten() in shade.wesl.
const LIGHT_ATTEN_KNEE: f32 = 0.01;

struct Hit {
    t: f32,
    idx: u32,
    kind: u32,
    inst_idx: u32, // 0xffffffff when the hit is on static (non-instanced) geometry
};

const HIT_NO_INSTANCE: u32 = 0xffffffffu;
