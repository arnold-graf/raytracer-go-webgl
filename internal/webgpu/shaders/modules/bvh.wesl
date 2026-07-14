// bvh.wesl — Spatial acceleration and visibility queries.
// Walks the flat BVH (+ instanced TLAS/BLAS) to find the nearest surface along a
// ray (nearest_hit) or any blocker along a shadow ray (shadowed). Also walks
// infinite plane index lists that sit outside the BVH.






import package::types::{BVH_TAG_TLAS, HIT_NO_INSTANCE, Hit, RAY_EPSILON, T_MISS, blockers, bvh_nodes, inst_records, inst_templates, params, plane_idx};
import package::profile::{prof_step, prof_tests};
import package::intersect::{intersect, intersect_blocker, slab_hit};
import package::terrain::{hit_terrain, hit_water};
import package::instance::{inst_local_dir, inst_local_origin};

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
        prof_step();
        if (!slab_hit(n.min_b.xyz, n.max_b.xyz, ro, inv, t)) {
            continue;
        }
        let count = n.info.w;
        if (count > 0u) {
            prof_tests(count);
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
        prof_step();
        if (!slab_hit(n.min_b.xyz, n.max_b.xyz, ro, inv, best.t)) {
            continue;
        }
        let count = n.info.w;
        if (count > 0u) {
            prof_tests(count);
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
        prof_step();
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
            prof_tests(count);
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
        prof_step();
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
            prof_step();
            if (!slab_hit(n.min_b.xyz, n.max_b.xyz, ro, inv, h.t)) {
                continue;
            }
            let count = n.info.w;
            if (count > 0u) {
                prof_tests(count);
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

    // Infinite planes are not part of the finite BVH; walk the precomputed plane
    // index list instead of scanning every primitive.
    for (var i = 0u; i < params.plane_count; i = i + 1u) {
        let pi = plane_idx[i];
        let t = intersect(pi, ro, rd);
        if (t < h.t) {
            h.t = t;
            h.idx = pi;
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
            prof_step();
            if (!slab_hit(n.min_b.xyz, n.max_b.xyz, ro, inv, max_t)) {
                continue;
            }
            let count = n.info.w;
            if (count > 0u) {
                prof_tests(count);
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
