// math.wesl — Small math helpers and procedural noise.
// clamp/mix/smoothstep utilities plus Perlin noise, fbm, and turbulence shared
// by procedural textures, terrain blending, and the cloudy/storm sky.





























import package::types::{perm};

fn clamp01(x: f32) -> f32 {
    return clamp(x, 0.0, 1.0);
}

fn mix3(a: vec3<f32>, b: vec3<f32>, t: f32) -> vec3<f32> {
    return a + (b - a) * t;
}

fn smoothstepf(e0: f32, e1: f32, x: f32) -> f32 {
    let t = clamp01((x - e0) / (e1 - e0));
    return t * t * (3.0 - 2.0 * t);
}

fn fracf(x: f32) -> f32 {
    return x - floor(x);
}

// Exact port of internal/texture/noise.go: Ken Perlin's reference 3D noise over
// the uploaded permutation table, so GPU procedural textures match the CPU
// (modulo f32 vs f64 rounding) rather than approximating with value noise.
fn p_fade(t: f32) -> f32 {
    return t * t * t * (t * (t * 6.0 - 15.0) + 10.0);
}

fn p_lerp(t: f32, a: f32, b: f32) -> f32 {
    return a + t * (b - a);
}

fn p_grad(h: i32, x: f32, y: f32, z: f32) -> f32 {
    let hh = h & 15;
    var u = x;
    if (hh >= 8) { u = y; }
    var v = z;
    if (hh < 4) {
        v = y;
    } else if (hh == 12 || hh == 14) {
        v = x;
    }
    var ru = u;
    if ((hh & 1) != 0) { ru = -u; }
    var rv = v;
    if ((hh & 2) != 0) { rv = -v; }
    return ru + rv;
}

fn perlin(px: f32, py: f32, pz: f32) -> f32 {
    let xi = i32(floor(px)) & 255;
    let yi = i32(floor(py)) & 255;
    let zi = i32(floor(pz)) & 255;
    let x = px - floor(px);
    let y = py - floor(py);
    let z = pz - floor(pz);
    let u = p_fade(x);
    let v = p_fade(y);
    let w = p_fade(z);

    let a = i32(perm[u32(xi)]) + yi;
    let aa = i32(perm[u32(a)]) + zi;
    let ab = i32(perm[u32(a + 1)]) + zi;
    let b = i32(perm[u32(xi + 1)]) + yi;
    let ba = i32(perm[u32(b)]) + zi;
    let bb = i32(perm[u32(b + 1)]) + zi;

    return p_lerp(w,
        p_lerp(v,
            p_lerp(u, p_grad(i32(perm[u32(aa)]), x, y, z), p_grad(i32(perm[u32(ba)]), x - 1.0, y, z)),
            p_lerp(u, p_grad(i32(perm[u32(ab)]), x, y - 1.0, z), p_grad(i32(perm[u32(bb)]), x - 1.0, y - 1.0, z))),
        p_lerp(v,
            p_lerp(u, p_grad(i32(perm[u32(aa + 1)]), x, y, z - 1.0), p_grad(i32(perm[u32(ba + 1)]), x - 1.0, y, z - 1.0)),
            p_lerp(u, p_grad(i32(perm[u32(ab + 1)]), x, y - 1.0, z - 1.0), p_grad(i32(perm[u32(bb + 1)]), x - 1.0, y - 1.0, z - 1.0))));
}

// perlin restricted to the y=0 plane (terrain fBm, water ripples). With py=0
// the fade weight v is exactly 0, so the four y=1 corner gradients drop out
// algebraically: same result as perlin(px, 0, pz) with half the table fetches.
fn perlin_xz(px: f32, pz: f32) -> f32 {
    let xi = i32(floor(px)) & 255;
    let zi = i32(floor(pz)) & 255;
    let x = px - floor(px);
    let z = pz - floor(pz);
    let u = p_fade(x);
    let w = p_fade(z);

    let a = i32(perm[u32(xi)]);
    let aa = i32(perm[u32(a)]) + zi;
    let b = i32(perm[u32(xi + 1)]);
    let ba = i32(perm[u32(b)]) + zi;

    return p_lerp(w,
        p_lerp(u, p_grad(i32(perm[u32(aa)]), x, 0.0, z), p_grad(i32(perm[u32(ba)]), x - 1.0, 0.0, z)),
        p_lerp(u, p_grad(i32(perm[u32(aa + 1)]), x, 0.0, z - 1.0), p_grad(i32(perm[u32(ba + 1)]), x - 1.0, 0.0, z - 1.0)));
}

fn fbm_xz(x: f32, z: f32, octaves: u32) -> f32 {
    var sum = 0.0;
    var amp = 1.0;
    var freq = 1.0;
    var norm = 0.0;
    for (var i = 0u; i < octaves; i = i + 1u) {
        sum = sum + amp * perlin_xz(x * freq, z * freq);
        norm = norm + amp;
        amp = amp * 0.5;
        freq = freq * 2.0;
    }
    if (norm == 0.0) { return 0.0; }
    return sum / norm;
}

fn fbm(x: f32, y: f32, z: f32, octaves: u32) -> f32 {
    var sum = 0.0;
    var amp = 1.0;
    var freq = 1.0;
    var norm = 0.0;
    for (var i = 0u; i < octaves; i = i + 1u) {
        sum = sum + amp * perlin(x * freq, y * freq, z * freq);
        norm = norm + amp;
        amp = amp * 0.5;
        freq = freq * 2.0;
    }
    if (norm == 0.0) { return 0.0; }
    return sum / norm;
}

fn turbulence(x: f32, y: f32, z: f32, octaves: u32) -> f32 {
    var sum = 0.0;
    var amp = 1.0;
    var freq = 1.0;
    var norm = 0.0;
    for (var i = 0u; i < octaves; i = i + 1u) {
        sum = sum + amp * abs(perlin(x * freq, y * freq, z * freq));
        norm = norm + amp;
        amp = amp * 0.5;
        freq = freq * 2.0;
    }
    if (norm == 0.0) { return 0.0; }
    return sum / norm;
}

// cell_rand is a bit-exact port of internal/texture/texture.go's integer hash,
// so brick walls match the CPU. u32 wrap-around arithmetic is identical on both
// sides (unlike the old frac(sin*43758) hash, which diverged under f32).
fn cell_rand(c: f32, r: f32, seed: f32) -> f32 {
    var h = u32(i32(c)) * 0x27d4eb2du + u32(i32(r)) * 0x9e3779b1u + u32(i32(seed)) * 0x85ebca6bu;
    h = h ^ (h >> 15u);
    h = h * 0x2c1b3c6du;
    h = h ^ (h >> 13u);
    h = h * 0x297a2d39u;
    h = h ^ (h >> 16u);
    return f32(h >> 8u) / 16777216.0;
}

// The texture functions below are faithful ports of internal/texture/*.go.
const TEX_PI: f32 = 3.141592653589793;
