// profile.wgsl — Optional performance counters.
// Atomic counters read back by gpuprof and the in-game HUD (~once per second).
// When params.profile_enabled is 0 the helpers compile to no-ops at runtime.

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
const PROF_BVH_STEPS: u32 = 18u;
const PROF_PRIM_TESTS: u32 = 19u;
const PROF_COUNTER_COUNT: u32 = 20u;

fn prof_inc(idx: u32, delta: u32) {
    if (params.profile_enabled != 0u && idx < PROF_COUNTER_COUNT) {
        atomicAdd(&profile_counters[idx], delta);
    }
}

// BVH traversal-quality accumulators (Representative-Ray-Set style): node visits
// and leaf primitive intersection tests are summed per-invocation in private
// memory to avoid per-node atomic contention, then flushed once per pixel in
// main(). Guarded by profile_enabled so normal frames pay nothing.
var<private> bvh_steps_acc: u32 = 0u;
var<private> prim_tests_acc: u32 = 0u;

fn prof_step() {
    if (params.profile_enabled != 0u) {
        bvh_steps_acc = bvh_steps_acc + 1u;
    }
}

fn prof_tests(n: u32) {
    if (params.profile_enabled != 0u) {
        prim_tests_acc = prim_tests_acc + n;
    }
}
