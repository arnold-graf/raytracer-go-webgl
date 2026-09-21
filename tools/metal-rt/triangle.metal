#include <metal_stdlib>
#include <metal_raytracing>
using namespace metal;
using namespace raytracing;

// rt_traverse: the same job as nearest_hit -- one primary ray, nearest hit --
// but with Apple's intersector doing the traversal instead of our own loop.
// The question is not whether it is faster; it is how many registers it leaves
// us, because our own traversal alone pins the kernel at 384 of 1024.
kernel void rt_traverse(instance_acceleration_structure accel [[buffer(0)]],
                        device float *out [[buffer(1)]],
                        uint2 gid [[thread_position_in_grid]])
{
    ray r;
    r.origin = float3(0.0f, 0.0f, 0.0f);
    r.direction = normalize(float3(float(gid.x), float(gid.y), 1.0f));
    r.min_distance = 0.001f;
    r.max_distance = INFINITY;

    intersector<instancing, triangle_data> isect;
    intersection_result<instancing, triangle_data> hit = isect.intersect(r, accel);
    out[gid.x] = hit.distance;
}

// rt_traverse_shade: traversal plus a shading-shaped tail, so the comparison is
// against a whole megakernel rather than a bare traversal.
kernel void rt_traverse_shade(instance_acceleration_structure accel [[buffer(0)]],
                              device float *out [[buffer(1)]],
                              constant float3 *lights [[buffer(2)]],
                              uint2 gid [[thread_position_in_grid]])
{
    float3 acc = float3(0.0f);
    ray r;
    r.origin = float3(0.0f);
    r.direction = normalize(float3(float(gid.x), float(gid.y), 1.0f));
    r.min_distance = 0.001f;
    r.max_distance = INFINITY;
    intersector<instancing, triangle_data> isect;

    for (int seg = 0; seg < 4; ++seg) {
        intersection_result<instancing, triangle_data> hit = isect.intersect(r, accel);
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
            intersector<instancing, triangle_data> sh;
            sh.accept_any_intersection(true);
            auto blocked = sh.intersect(s, accel);
            if (blocked.type == intersection_type::none) {
                acc += max(dot(n, ln), 0.0f) / max(d2, 1.0f);
            }
        }
        r.origin = p + n * 1e-3f;
        r.direction = reflect(r.direction, n);
    }
    out[gid.x] = acc.x + acc.y + acc.z;
}
