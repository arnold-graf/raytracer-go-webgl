// instance.wesl — Instanced geometry (trees, NPCs, …).
// Converts ray origins, directions, hit points, and normals between world
// space and an instance's local template space for TLAS/BLAS traversal.







import package::types::{inst_records};

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
