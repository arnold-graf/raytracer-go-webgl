#include <metal_stdlib>
#include <metal_raytracing>
using namespace metal;
using namespace raytracing;

struct BBResult { bool accept [[accept_intersection]]; float distance [[distance]]; };
struct AABB { packed_float3 mn; packed_float3 mx; };
struct Cam {
    packed_float3 pos, fwd, right, up;
    float aspect, fovScale;
    uint w, h;
};

// Slab test against the primitive's own box. Stands in for hit_prim: the point
// of the benchmark is traversal throughput, not primitive variety.
[[intersection(bounding_box)]]
BBResult prim_isect(float3 origin [[origin]],
                    float3 direction [[direction]],
                    float minDist [[min_distance]],
                    float maxDist [[max_distance]],
                    uint pid [[primitive_id]],
                    const device AABB *boxes [[buffer(0)]])
{
    BBResult r; r.accept = false; r.distance = maxDist;
    AABB b = boxes[pid];
    float3 inv = 1.0f / direction;
    float3 t0 = (float3(b.mn) - origin) * inv;
    float3 t1 = (float3(b.mx) - origin) * inv;
    float3 lo = min(t0, t1), hi = max(t0, t1);
    float tn = max(max(lo.x, lo.y), lo.z);
    float tf = min(min(hi.x, hi.y), hi.z);
    float t = max(tn, minDist);
    if (tf >= t && t < maxDist) { r.accept = true; r.distance = t; }
    return r;
}

kernel void bench(primitive_acceleration_structure accel [[buffer(0)]],
                  intersection_function_table<> table [[buffer(1)]],
                  constant Cam &cam [[buffer(2)]],
                  device float *out [[buffer(3)]],
                  uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= cam.w || gid.y >= cam.h) return;
    // Matches pixel_ray_dir in trace.wesl exactly.
    float u = (float(gid.x) + 0.5f) / float(cam.w) * 2.0f - 1.0f;
    float v = 1.0f - (float(gid.y) + 0.5f) / float(cam.h) * 2.0f;
    ray r;
    r.origin = float3(cam.pos);
    r.direction = normalize(float3(cam.fwd)
                            + float3(cam.right) * (u * cam.aspect * cam.fovScale)
                            + float3(cam.up) * (v * cam.fovScale));
    r.min_distance = 1e-4f;
    r.max_distance = INFINITY;

    intersector<> isect;
    auto hit = isect.intersect(r, accel, table);
    out[gid.y * cam.w + gid.x] = (hit.type == intersection_type::none) ? -1.0f : hit.distance;
}
