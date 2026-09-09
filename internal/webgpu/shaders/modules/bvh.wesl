// bvh.wesl — Spatial acceleration and visibility queries.
// Walks the flat BVH (+ instanced TLAS/BLAS) to find the nearest surface along a
// ray (nearest_hit) or any blocker along a shadow ray (shadowed). Also walks
// infinite plane index lists that sit outside the BVH.

import package::types::{BVH_TAG_TLAS, HIT_NO_INSTANCE, Hit, RAY_EPSILON, SHADOW_NEAREST_BLOCKER, SHADOW_WIDEST_BLOCKER, SHADOW_WIDEST_EXACT, T_MISS, blockers, bvh_nodes, idx_tables, inst_records, inst_templates, params};
import package::profile::{prof_step, prof_tests};
import package::intersect::{intersect, intersect_blocker, slab_hit, slab_near, slab_range};
import package::terrain::{hit_terrain, hit_water};
import package::instance::{inst_local_dir, inst_local_origin};

// BVH_STACK_SIZE is the depth of every depth-first traversal stack below. These
// are per-thread scratch arrays and several are live at once along the trace
// call chain, so the size costs GPU occupancy directly. A DFS that pushes both
// children then pops one holds at most depth+1 entries; the deepest tree any
// scene packs is far shallower. Keep in sync with gpuscene.BVHStackSize, which
// TestBVHTraversalStackDepth checks against every packed scene.
const BVH_STACK_SIZE: u32 = 32u;

// Push near child on top (popped first), skipping either child whose AABB entry
// is already past `bound`.
//
// The test used to be the caller's job, done with slab_hit when the node came
// back off the stack. Doing it here instead costs nothing — slab_near has the
// entry distance in hand — and it means a node is tested once rather than
// twice, since the caller's test was re-deriving what this one already knew.
//
// Nearest-hit search still has to re-test on the way out: `bound` there is a
// best_t that a nearer primitive may have tightened while the node sat on the
// stack. Shadow any-hit does not, its bound being a fixed max_t, so those
// traversals drop the second test entirely.
fn bvh_child_push(left: u32, right: u32, ro: vec3<f32>, inv: vec3<f32>, bound: f32, stack: ptr<function, array<u32, BVH_STACK_SIZE>>, sp: ptr<function, u32>) {
    let ln = bvh_nodes[left];
    let rn = bvh_nodes[right];
    let t_l = slab_near(ln.min_b.xyz, ln.max_b.xyz, ro, inv);
    let t_r = slab_near(rn.min_b.xyz, rn.max_b.xyz, ro, inv);
    var near = left;
    var far = right;
    var t_near = t_l;
    var t_far = t_r;
    if (t_r < t_l) {
        near = right;
        far = left;
        t_near = t_r;
        t_far = t_l;
    }
    if (t_far < bound && (*sp) + 1u <= BVH_STACK_SIZE) {
        (*stack)[*sp] = far;
        *sp = (*sp) + 1u;
    }
    if (t_near < bound && (*sp) < BVH_STACK_SIZE) {
        (*stack)[*sp] = near;
        *sp = (*sp) + 1u;
    }
}

fn bvh_nearest_subtree(root: u32, ro: vec3<f32>, rd: vec3<f32>, best_t: f32, blockers: bool) -> f32 {
    let inv = vec3<f32>(1.0) / rd;
    var t = best_t;
    var stack: array<u32, BVH_STACK_SIZE>;
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
            bvh_child_push(n.info.x, n.info.y, ro, inv, t, &stack, &sp);
        }
    }
    return t;
}

fn bvh_nearest_subtree_hit(root: u32, ro: vec3<f32>, rd: vec3<f32>, h: Hit, inst_idx: u32) -> Hit {
    let inv = vec3<f32>(1.0) / rd;
    var best = h;
    var stack: array<u32, BVH_STACK_SIZE>;
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
            bvh_child_push(n.info.x, n.info.y, ro, inv, best.t, &stack, &sp);
        }
    }
    return best;
}

// Returns the distance to a blocker, or -1 when the segment is clear. The
// distance costs nothing: an any-hit already computes t to compare against the
// segment length, it just used to throw it away. Penumbra estimation needs it
// (see shadow_blocker_t in shade.wesl).
//
// It is *a* blocker, not necessarily the nearest — this is still an any-hit and
// returns on the first one found. That is the right trade for penumbra sizing,
// which wants a representative blocker depth and would otherwise have to pay for
// a full nearest-hit traversal on every shadow ray in the scene.
fn inst_bvh_any_hit(root: u32, ro: vec3<f32>, rd: vec3<f32>, max_t: f32) -> f32 {
    let inv = vec3<f32>(1.0) / rd;
    let limit = max_t - 0.05;
    var best = -1.0;
    var stack: array<u32, BVH_STACK_SIZE>;
    var sp = 0u;
    // The loop below does not test the node it pops: bvh_child_push tested it
    // against this same max_t on the way in, and an any-hit bound never moves.
    // The root has no parent to have done that, so it is tested here.
    let rn = bvh_nodes[root];
    if (slab_hit(rn.min_b.xyz, rn.max_b.xyz, ro, inv, max_t)) {
        stack[sp] = root;
        sp = sp + 1u;
    }

    loop {
        if (sp == 0u) {
            break;
        }
        sp = sp - 1u;
        let n = bvh_nodes[stack[sp]];
        prof_step();
        if (n.info.z == BVH_TAG_TLAS && n.info.w > 0u) {
            let inst_idx = n.info.x;
            let tmpl = inst_templates[inst_records[inst_idx].template_id];
            let lro = inst_local_origin(inst_idx, ro);
            let lrd = inst_local_dir(inst_idx, rd);
            // bvh_nearest_subtree returns the nearest hit in the template. For
            // the widest pick that is the wrong end, but an instanced blocker is
            // one connected object, so its near and far faces sit within its own
            // thickness of each other -- far less than the distances the width
            // formula cares about. Left as the nearest for now, and noted.
            let ti = bvh_nearest_subtree(tmpl.blocker_blas_root, lro, lrd, limit, true);
            if (ti < limit) {
                // Clamped because the caller reads "negative means clear", and a
                // subtree hit right at the ray origin can come back at or below
                // zero. Before this function returned a distance that case was a
                // plain `true` and stayed blocked.
                let tc = max(ti, 0.0);
                if (!SHADOW_NEAREST_BLOCKER && !SHADOW_WIDEST_BLOCKER) {
                    return tc;
                }
                if (SHADOW_WIDEST_BLOCKER && !SHADOW_WIDEST_EXACT) {
                    return tc;
                }
                best = blocker_keep(best, tc);
            }
            continue;
        }
        if (n.info.z == BVH_TAG_TLAS && n.info.w == 0u) {
            // Near-first here, unlike the static blocker tree. An instanced
            // blocker is one connected object, so the widest pick has nothing to
            // choose between within it -- and far-first ordering costs real time
            // on scenes whose occluders are mostly near, the villa's trees being
            // the case that showed it (+19% before this was split out).
            bvh_child_push(n.info.x, n.info.y, ro, inv, max_t, &stack, &sp);
            continue;
        }
        let count = n.info.w;
        if (count > 0u) {
            prof_tests(count);
            for (var k = 0u; k < count; k = k + 1u) {
                let prim_idx = select(n.info.x, n.info.y, k == 1u);
                let t = intersect_blocker(prim_idx, ro, rd);
                if (t > RAY_EPSILON && t < limit) {
                        if (!SHADOW_NEAREST_BLOCKER && !SHADOW_WIDEST_BLOCKER) {
                            return t;
                        }
                        // Far-first ordering means this is already the farthest
                        // blocker in all but a sliver of cases, so the
                        // approximate mode stops here and keeps any-hit's cost.
                        if (SHADOW_WIDEST_BLOCKER && !SHADOW_WIDEST_EXACT) {
                            return t;
                        }
                        best = blocker_keep(best, t);
                }
            }
        } else if (n.info.z != BVH_TAG_TLAS) {
            bvh_child_push(n.info.x, n.info.y, ro, inv, max_t, &stack, &sp);
        }
    }
    return best;
}

fn inst_nearest_hit(ro: vec3<f32>, rd: vec3<f32>, h: Hit) -> Hit {
    if (params.inst_count == 0u) {
        return h;
    }
    let inv = vec3<f32>(1.0) / rd;
    var best = h;
    var stack: array<u32, BVH_STACK_SIZE>;
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
            bvh_child_push(n.info.x, n.info.y, ro, inv, best.t, &stack, &sp);
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
        var stack: array<u32, BVH_STACK_SIZE>;
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
                bvh_child_push(n.info.x, n.info.y, ro, inv, h.t, &stack, &sp);
            }
        }
    }

    h = inst_nearest_hit(ro, rd, h);

    // Infinite planes are not part of the finite BVH; walk the precomputed plane
    // index list instead of scanning every primitive.
    for (var i = 0u; i < params.plane_count; i = i + 1u) {
        let pi = idx_tables[i];
        let t = intersect(pi, ro, rd);
        if (t < h.t) {
            h.t = t;
            h.idx = pi;
            h.kind = 0u;
        }
    }

    for (var i = 0u; i < params.terrain_count; i = i + 1u) {
        let t = hit_terrain(i, ro, rd, h.t, true, false);
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

// Distance to a blocker in the static blocker set, or -1 when clear. See
// inst_bvh_any_hit for why this is an any-hit distance and not the nearest.
//
// The stack holds *absolute* node indices, like every other traversal here.
// PackBVH numbers a section's children from zero and instance.go appends the
// static blocker tree verbatim, so blocker_off has to be folded in at the push
// rather than at the load: bvh_child_push dereferences the child indices to
// order them, and section-relative ones would have it read the main scene's
// nodes instead. Leaf slots stay untouched, being primitive indices already.
// blocker_keep folds one candidate blocker distance into the running pick.
// Widest takes the farthest from the receiver, nearest the closest; with neither
// the caller has already returned on the first hit it found.
fn blocker_keep(best: f32, t: f32) -> f32 {
    if (SHADOW_WIDEST_BLOCKER) {
        return max(best, t);
    }
    if (best < 0.0 || t < best) {
        return t;
    }
    return best;
}

// blocker_child_push_widest pushes children for the widest search. It prunes on
// the AABB *exit*: a box that ends nearer than the farthest blocker already
// found cannot hold a farther one. Near-side pruning still applies, since a box
// starting past max_t holds nothing that blocks at all.
fn blocker_child_push_widest(left: u32, right: u32, ro: vec3<f32>, inv: vec3<f32>, max_t: f32, best: f32, stack: ptr<function, array<u32, BVH_STACK_SIZE>>, sp: ptr<function, u32>) {
    let ln = bvh_nodes[left];
    let rn = bvh_nodes[right];
    let rl = slab_range(ln.min_b.xyz, ln.max_b.xyz, ro, inv);
    let rr = slab_range(rn.min_b.xyz, rn.max_b.xyz, ro, inv);
    // Far child first, so the farthest blocker is usually found early. That is
    // what lets the approximate mode stop at the first hit and still land on the
    // widest penumbra -- the mirror of near-first ordering for a nearest search.
    var first = left;
    var second = right;
    var a = rl;
    var b = rr;
    if (rr.y > rl.y) {
        first = right;
        second = left;
        a = rr;
        b = rl;
    }
    // Cull on both ends: a box starting past the ray's end holds nothing that
    // blocks, and one ending nearer than the best blocker so far holds nothing
    // farther. `best` is -1 in the approximate mode, where the second test is
    // vacuous and the compiler drops it.
    if (b.x < max_t && b.y > best && (*sp) + 1u <= BVH_STACK_SIZE) {
        (*stack)[*sp] = second;
        *sp = (*sp) + 1u;
    }
    if (a.x < max_t && a.y > best && (*sp) < BVH_STACK_SIZE) {
        (*stack)[*sp] = first;
        *sp = (*sp) + 1u;
    }
}

fn blocker_bvh_any_hit(ro: vec3<f32>, rd: vec3<f32>, max_t: f32) -> f32 {
    if (params.blocker_bvh_node_count == 0u && params.blocker_inst_count == 0u) {
        return -1.0;
    }
    var best = -1.0;
    let inv = vec3<f32>(1.0) / rd;
    let limit = max_t - 0.05;
    let blocker_off = select(params.bvh_node_count, params.blocker_section_start, params.inst_count > 0u);

    if (params.blocker_bvh_node_count > 0u) {
        var stack: array<u32, BVH_STACK_SIZE>;
        var sp = 0u;
        // The loop below does not test the node it pops: bvh_child_push tested it
        // against this same max_t on the way in, and an any-hit bound never moves.
        // The root has no parent to have done that, so it is tested here.
        let rn = bvh_nodes[blocker_off];
        if (slab_hit(rn.min_b.xyz, rn.max_b.xyz, ro, inv, max_t)) {
            stack[sp] = blocker_off;
            sp = sp + 1u;
        }

        loop {
            if (sp == 0u) {
                break;
            }
            sp = sp - 1u;
            let n = bvh_nodes[stack[sp]];
            prof_step();
            let count = n.info.w;
            if (count > 0u) {
                prof_tests(count);
                for (var k = 0u; k < count; k = k + 1u) {
                    let prim_idx = select(n.info.x, n.info.y, k == 1u);
                    let t = intersect_blocker(prim_idx, ro, rd);
                    if (t > RAY_EPSILON && t < limit) {
                        if (!SHADOW_NEAREST_BLOCKER && !SHADOW_WIDEST_BLOCKER) {
                            return t;
                        }
                        // Far-first ordering means this is already the farthest
                        // blocker in all but a sliver of cases, so the
                        // approximate mode stops here and keeps any-hit's cost.
                        if (SHADOW_WIDEST_BLOCKER && !SHADOW_WIDEST_EXACT) {
                            return t;
                        }
                        best = blocker_keep(best, t);
                    }
                }
            } else if (n.info.z != BVH_TAG_TLAS) {
                if (SHADOW_WIDEST_BLOCKER) {
                    blocker_child_push_widest(blocker_off + n.info.x, blocker_off + n.info.y, ro, inv, max_t, best, &stack, &sp);
                } else {
                    bvh_child_push(blocker_off + n.info.x, blocker_off + n.info.y, ro, inv, max_t, &stack, &sp);
                }
            }
        }
    }

    if (params.inst_count > 0u && params.blocker_inst_count > 0u) {
        let ti = inst_bvh_any_hit(params.blocker_inst_base, ro, rd, max_t);
        if (ti >= 0.0 && (best < 0.0 || ti < best)) {
            best = ti;
        }
    }
    return best;
}
