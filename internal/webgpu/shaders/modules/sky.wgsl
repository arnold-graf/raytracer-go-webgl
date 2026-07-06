// sky.wgsl — Background radiance when a ray misses geometry.
// Procedural sky variants (clear, cloudy, night stars, storm, sunset) plus the
// optional sun/moon disc. sky() is called from ray_color on a miss.

fn body_or(def: vec3<f32>) -> vec3<f32> {
    if (params.body_enabled != 0u) {
        return params.body_dir.xyz;
    }
    return normalize(def);
}

fn clear_sky(d: vec3<f32>) -> vec3<f32> {
    let t = clamp01(d.y * 0.5 + 0.5);
    let sun = max(0.0, dot(d, body_or(vec3<f32>(0.4, 0.8, -0.45))));
    let s2 = sun * sun;
    let s4 = s2 * s2;
    let s8 = s4 * s4;
    let s16 = s8 * s8;
    let s32 = s16 * s16;
    let sun64 = s32 * s32;
    return vec3<f32>(
        0.05 + 0.10 * t + sun64 * 3.0,
        0.07 + 0.12 * t + sun64 * 2.5,
        0.12 + 0.22 * t + sun64 * 1.5,
    );
}

// cloudy_sky: overcast blue daytime with soft white/grey clouds.
fn cloudy_sky(d: vec3<f32>, time: f32) -> vec3<f32> {
    let up = smoothstepf(-0.05, 0.7, d.y);
    let base = mix3(vec3<f32>(0.72, 0.80, 0.88), vec3<f32>(0.30, 0.50, 0.80), up);

    let drift = time * 0.04;
    let density = fbm(d.x * 2.4 + drift, d.y * 2.4, d.z * 2.4, 5u) * 0.5 + 0.5;
    let cover = smoothstepf(0.42, 0.72, density) * smoothstepf(-0.02, 0.12, d.y);
    let shade = fbm(d.x * 1.2 + drift, d.y * 1.2 + 9.0, d.z * 1.2, 3u) * 0.5 + 0.5;
    let cloud = mix3(vec3<f32>(0.50, 0.53, 0.58), vec3<f32>(1.05, 1.05, 1.04), shade);

    let col = mix3(base, cloud, cover);

    let sun = max(0.0, dot(d, body_or(vec3<f32>(0.4, 0.85, -0.4))));
    let s2 = sun * sun;
    let s4 = s2 * s2;
    let s8 = s4 * s4;
    let glow = s8 * s8; // sun^16
    return col + vec3<f32>(0.5, 0.48, 0.42) * (glow * (1.0 - 0.7 * cover));
}

// hash3 / star_field are a sin-based starfield (a frac(sin) value hash placing
// occasional bright points).
fn hash3(x: f32, y: f32, z: f32) -> f32 {
    let s = sin(x * 127.1 + y * 311.7 + z * 74.7) * 43758.5453;
    return s - floor(s);
}

fn star_field(d: vec3<f32>, scale: f32) -> f32 {
    let x = d.x * scale;
    let y = d.y * scale;
    let z = d.z * scale;
    let ix = floor(x);
    let iy = floor(y);
    let iz = floor(z);
    let h = hash3(ix, iy, iz);
    if (h < 0.94) {
        return 0.0;
    }
    let jx = hash3(ix + 1.3, iy, iz);
    let jy = hash3(ix, iy + 5.1, iz);
    let jz = hash3(ix, iy, iz + 2.7);
    let dx = x - ix - jx;
    let dy = y - iy - jy;
    let dz = z - iz - jz;
    let d2 = dx * dx + dy * dy + dz * dz;
    let bright = (h - 0.94) / 0.06;
    return bright * bright * max(0.0, 1.0 - d2 * 16.0);
}

// night_stars_sky: deep-blue gradient with a sparse starfield.
fn night_stars_sky(d: vec3<f32>) -> vec3<f32> {
    let up = smoothstepf(0.0, 0.7, d.y);
    var base = mix3(vec3<f32>(0.020, 0.030, 0.060), vec3<f32>(0.004, 0.008, 0.022), up);
    if (d.y > 0.0) {
        let s = star_field(d, 90.0);
        base = base + vec3<f32>(s, s, s * 1.08);
    }
    let moon = max(0.0, dot(d, body_or(vec3<f32>(-0.3, 0.85, -0.4))));
    let m2 = moon * moon;
    let m4 = m2 * m2;
    let m8 = m4 * m4;
    return base + vec3<f32>(0.10, 0.12, 0.16) * (m8 * m8);
}

// night_storm_sky: dramatic moonlit storm clouds.
fn night_storm_sky(d: vec3<f32>, time: f32) -> vec3<f32> {
    let moon_color = vec3<f32>(0.62, 0.70, 0.92);
    let moon_dir = body_or(vec3<f32>(0.08, 0.42, -0.90));
    let g = clamp01(dot(d, moon_dir));
    let g2 = g * g;
    let g4 = g2 * g2;
    let g8 = g4 * g4;
    let broad = g8;      // g^8 halo
    let core = g8 * g8;  // g^16 disc

    let base = mix3(vec3<f32>(0.018, 0.026, 0.045), vec3<f32>(0.005, 0.009, 0.020), smoothstepf(0.0, 0.8, d.y));
    let sky_glow = base + moon_color * (0.7 * broad + 1.2 * core);

    let drift = time * 0.025;
    let n = turbulence(d.x * 1.8 + drift, d.y * 1.8 + 3.0, d.z * 1.8, 6u);
    let cover = smoothstepf(0.06, 0.26, n);

    let cloud = vec3<f32>(0.012, 0.017, 0.030) + moon_color * (0.16 * broad);

    return mix3(sky_glow, cloud, cover);
}

// sunset_sky: warm horizon fading to deep blue, dark rim-lit clouds.
fn sunset_sky(d: vec3<f32>, time: f32) -> vec3<f32> {
    let warm = vec3<f32>(1.20, 0.45, 0.18);
    let mid = vec3<f32>(0.55, 0.28, 0.42);
    let zen = vec3<f32>(0.08, 0.10, 0.26);
    var base = mix3(warm, mid, smoothstepf(-0.05, 0.28, d.y));
    base = mix3(base, zen, smoothstepf(0.18, 0.75, d.y));

    let sun_dir = body_or(vec3<f32>(0.30, 0.05, -0.95));
    let sd = clamp01(dot(d, sun_dir));
    let sd2 = sd * sd;
    let sd4 = sd2 * sd2;
    let sd8 = sd4 * sd4;
    let disc = sd8 * sd8 * sd8; // tight sun disc/glow
    base = base + vec3<f32>(1.6, 0.9, 0.4) * (disc + 0.25 * sd8);

    let drift = time * 0.03;
    let density = fbm(d.x * 2.1 + drift, d.y * 2.1 + 5.0, d.z * 2.1, 5u) * 0.5 + 0.5;
    let cover = smoothstepf(0.4, 0.7, density) * smoothstepf(-0.03, 0.15, d.y);
    let rim = smoothstepf(0.38, 0.52, density) * (0.25 + 0.75 * sd4);
    var cloud = vec3<f32>(0.08, 0.05, 0.09);
    cloud = cloud + vec3<f32>(1.4, 0.65, 0.28) * rim;

    return mix3(base, cloud, cover);
}

// celestial_body returns the additive radiance of the configured sun/moon disc
// along view direction d: a soft-limbed disc plus a falloff halo. It is composed
// on top of every sky variant, so it also shows up in reflections. The body is
// purely cosmetic (it does not light the scene).
//
// A procedural moon texture (craters) would slot in here: build a tangent frame
// around body_dir and map d's offset within the disc to a uv in [-1,1], then
// modulate the disc color by a noise-based crater function (the fbm/value-noise
// helpers above are already available).
fn celestial_body(d: vec3<f32>) -> vec3<f32> {
    if (params.body_enabled == 0u) {
        return vec3<f32>(0.0, 0.0, 0.0);
    }
    let cos_a = dot(d, params.body_dir.xyz); // d and body_dir are unit vectors
    let cr = params.body_cos_radius;

    // Disc with a soft limb: full intensity inside ~0.9R, fading to 0 at the
    // edge R. Working in cosine space avoids a per-pixel acos.
    let cr_inner = cos(acos(clamp(cr, -1.0, 1.0)) * 0.9);
    let disc = smoothstepf(cr, cr_inner, cos_a);

    // Halo: a soft glow reaching ~3 disc-radii out, scaled by body_glow. The
    // steep power keeps it concentrated near the limb so the disc size still
    // reads clearly.
    let cr_halo = cos(acos(clamp(cr, -1.0, 1.0)) * 3.5);
    var halo = clamp01((cos_a - cr_halo) / max(1.0e-4, 1.0 - cr_halo));
    halo = halo * halo * halo * halo * params.body_glow * 0.6;

    return params.body_color.xyz * (disc + halo);
}

// sky dispatches on the selected variant (params.sky), then adds the celestial
// body on top.
fn sky(d: vec3<f32>) -> vec3<f32> {
    var base: vec3<f32>;
    switch (params.sky) {
        case 1u: { base = cloudy_sky(d, params.time); }
        case 2u: { base = night_stars_sky(d); }
        case 3u: { base = night_storm_sky(d, params.time); }
        case 4u: { base = sunset_sky(d, params.time); }
        default: { base = clear_sky(d); }
    }
    return base + celestial_body(d);
}
