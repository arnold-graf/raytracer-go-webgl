#include <metal_stdlib>
#include <metal_raytracing>
using namespace metal;
using namespace raytracing;

struct BBResult { bool accept [[accept_intersection]]; float distance [[distance]]; };
struct Prim { float3 mn; float3 mx; uint kind; };

// A procedural primitive test, compiled as its own function rather than inlined
// into the traversal. This is the shape our boxes/spheres/cylinders would take.
[[intersection(bounding_box)]]
BBResult prim_isect(float3 origin [[origin]],
                    float3 direction [[direction]],
                    float minDist [[min_distance]],
                    float maxDist [[max_distance]],
                    uint pid [[primitive_id]],
                    const device Prim *prims [[buffer(0)]])
{
    BBResult r;
    r.accept = false;
    r.distance = maxDist;
    Prim p = prims[pid];
    float3 inv = 1.0f / direction;
    float3 t0 = (p.mn - origin) * inv;
    float3 t1 = (p.mx - origin) * inv;
    float3 lo = min(t0, t1), hi = max(t0, t1);
    float tn = max(max(lo.x, lo.y), lo.z);
    float tf = min(min(hi.x, hi.y), hi.z);
    if (tf >= max(tn, minDist) && tn < maxDist) {
        r.accept = true;
        r.distance = max(tn, minDist);
    }
    return r;
}

// Traversal over procedural primitives, with the per-primitive test dispatched
// through a function table instead of inlined.
kernel void rt_bbox(primitive_acceleration_structure accel [[buffer(0)]],
                    intersection_function_table<> table [[buffer(1)]],
                    device float *out [[buffer(2)]],
                    uint2 gid [[thread_position_in_grid]])
{
    ray r;
    r.origin = float3(0.0f);
    r.direction = normalize(float3(float(gid.x), float(gid.y), 1.0f));
    r.min_distance = 0.001f;
    r.max_distance = INFINITY;
    intersector<> isect;
    auto hit = isect.intersect(r, accel, table);
    out[gid.x] = hit.distance;
}

// The same, with a shading tail and shadow rays.
kernel void rt_bbox_shade(primitive_acceleration_structure accel [[buffer(0)]],
                          intersection_function_table<> table [[buffer(1)]],
                          device float *out [[buffer(2)]],
                          constant float3 *lights [[buffer(3)]],
                          uint2 gid [[thread_position_in_grid]])
{
    float3 acc = float3(0.0f);
    ray r;
    r.origin = float3(0.0f);
    r.direction = normalize(float3(float(gid.x), float(gid.y), 1.0f));
    r.min_distance = 0.001f;
    r.max_distance = INFINITY;
    intersector<> isect;
    for (int seg = 0; seg < 4; ++seg) {
        auto hit = isect.intersect(r, accel, table);
        if (hit.type == intersection_type::none) break;
        float3 p = r.origin + r.direction * hit.distance;
        float3 n = normalize(p);
        for (int l = 0; l < 8; ++l) {
            float3 ld = lights[l] - p;
            float d2 = dot(ld, ld);
            float3 ln = ld * rsqrt(max(d2, 1e-6f));
            ray s;
            s.origin = p + n * 1e-3f;
            s.direction = ln;
            s.min_distance = 1e-3f;
            s.max_distance = sqrt(d2);
            intersector<> sh;
            sh.accept_any_intersection(true);
            auto blocked = sh.intersect(s, accel, table);
            if (blocked.type == intersection_type::none) {
                acc += max(dot(n, ln), 0.0f) / max(d2, 1.0f);
            }
        }
        r.origin = p + n * 1e-3f;
        r.direction = reflect(r.direction, n);
    }
    out[gid.x] = acc.x + acc.y + acc.z;
}
