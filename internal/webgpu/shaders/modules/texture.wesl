// texture.wesl — Surface color and image sampling.
// Procedural materials (wood, brick, wallpaper, grass, …), portal capture
// textures, and in-world document images. texture_eval() is the main entry.






























import package::types::{DOCUMENT_TEX_H, DOCUMENT_TEX_W, MAT_CHECKER, Prim, TEX_BRICK, TEX_CAPTURE_BASE, TEX_CAPTURE_COUNT, TEX_CEMENT, TEX_DIRT, TEX_DOCUMENT_BASE, TEX_DOCUMENT_COUNT, TEX_GRASS, TEX_MARBLE, TEX_NONE, TEX_PAPER, TEX_SNOW, TEX_STONE, TEX_STONE_WALL, TEX_WALLPAPER_GREEN, TEX_WALLPAPER_ROSE, TEX_WOOD, box_face_textures, capture_pixels, document_pixels, params};
import package::math::{TEX_PI, cell_rand, fbm, fracf, mix3, perlin, smoothstepf, turbulence};

fn tex_wood(p: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let dist = length(vec2<f32>(p.y, p.z));
    let g = dist * 2.2 + 0.6 * turbulence(p.x * 0.6, p.y * 1.5, p.z * 1.5, 4u);
    let rings = 0.5 + 0.5 * sin(g * 2.0 * TEX_PI * TEX_PI);
    let light = vec3<f32>(0.58, 0.38, 0.19);
    let dark = vec3<f32>(0.33, 0.19, 0.09);
    let streak = 0.85 + 0.15 * perlin(p.x * 12.0, p.y * 2.0, p.z * 2.0);
    return mix3(dark, light, rings) * streak * tint;
}

fn brick_palette(i: i32) -> vec3<f32> {
    var pal = array<vec3<f32>, 6>(
        vec3<f32>(0.28, 0.10, 0.07),
        vec3<f32>(0.22, 0.08, 0.06),
        vec3<f32>(0.34, 0.15, 0.09),
        vec3<f32>(0.18, 0.09, 0.07),
        vec3<f32>(0.13, 0.07, 0.06),
        vec3<f32>(0.25, 0.13, 0.10),
    );
    var idx = i;
    if (idx < 0) { idx = 0; }
    if (idx > 5) { idx = 5; }
    return pal[idx];
}

fn tex_brick(p: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let brick_w = 0.5;
    let brick_h = 0.22;
    let mortar = 0.05;
    let row = floor(p.y / brick_h);
    var x = p.x;
    if ((i32(row) & 1) == 1) { x = x + brick_w * 0.5; }
    let col = floor(x / brick_w);

    let pick = cell_rand(col, row, 1.0);
    let bright = cell_rand(col, row, 2.0);
    let desat = cell_rand(col, row, 3.0);
    var decay = cell_rand(col, row, 4.0);
    decay = decay * decay;

    var base = brick_palette(i32(pick * 6.0));
    base = base * (0.5 + 0.45 * bright);
    base.x = base.x * (0.88 + 0.24 * cell_rand(col, row, 5.0));
    base.y = base.y * (0.85 + 0.30 * cell_rand(col, row, 6.0));
    base.z = base.z * (0.85 + 0.30 * cell_rand(col, row, 7.0));
    let gg = (base.x + base.y + base.z) / 3.0;
    base = mix3(base, vec3<f32>(gg, gg, gg), 0.5 * desat * decay);

    let mottle = 0.78 + 0.22 * fbm(p.x * 9.0 + col * 4.0, p.y * 9.0 + row * 4.0, p.z * 9.0, 3u);
    let grain = 0.78 + 0.22 * fbm(p.x * 40.0, p.y * 40.0, p.z * 40.0, 4u);
    let stain = fbm(p.x * 5.0 + col * 7.3, p.y * 5.0 + row * 3.1, p.z * 5.0, 4u);
    let weather = 1.0 - decay * 0.55 * smoothstepf(-0.3, 0.6, stain);
    var face = base * (mottle * grain * weather * (1.0 - 0.3 * decay));

    let crack = smoothstepf(0.5, 0.72, turbulence(p.x * 9.0 + col, p.y * 9.0 + row, p.z * 9.0, 4u));
    face = face * (1.0 - 0.7 * crack * (0.25 + 0.75 * decay));

    let mortar_col = vec3<f32>(0.20, 0.19, 0.17) * (0.75 + 0.4 * fbm(p.x * 7.0, p.y * 7.0, p.z * 7.0, 3u));

    let mx = mortar / brick_w;
    let my = mortar / brick_h;
    let erode = 1.0 + 3.0 * decay;
    let ex = mx * erode * (0.8 + 0.4 * perlin(p.x * 15.0, p.y * 15.0, p.z * 15.0));
    let ey = my * erode * (0.8 + 0.4 * perlin(p.x * 15.0 + 5.0, p.y * 15.0 + 5.0, p.z * 15.0));
    let fx = fracf(x / brick_w);
    let fy = fracf(p.y / brick_h);
    let mask = smoothstepf(0.0, ex, fx) * smoothstepf(0.0, ex, 1.0 - fx) *
        smoothstepf(0.0, ey, fy) * smoothstepf(0.0, ey, 1.0 - fy);

    return mix3(mortar_col, face, mask) * tint;
}

fn tex_stone(p: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let n = 0.5 + 0.5 * fbm(p.x * 1.5, p.y * 1.5, p.z * 1.5, 5u);
    let light = vec3<f32>(0.60, 0.58, 0.54);
    let dark = vec3<f32>(0.34, 0.33, 0.31);
    var c = mix3(dark, light, n);
    let seam = abs(perlin(p.x * 6.0, p.y * 6.0, p.z * 6.0));
    c = c * (0.7 + 0.3 * smoothstepf(0.02, 0.12, seam));
    return c * tint;
}

fn tex_cement(p: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let n = fbm(p.x * 4.0, p.y * 4.0, p.z * 4.0, 4u);
    let speck = 0.5 + 0.5 * perlin(p.x * 40.0, p.y * 40.0, p.z * 40.0);
    let g = 0.62 + 0.06 * n + 0.03 * (speck - 0.5);
    return vec3<f32>(g, g, g * 0.99) * tint;
}

fn tex_paper(p: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let fiber = fbm(p.x * 18.0, p.y * 18.0, p.z * 18.0, 3u);
    let grain = 0.5 + 0.5 * perlin(p.x * 80.0, p.y * 80.0, p.z * 80.0);
    let base = vec3<f32>(0.96, 0.94, 0.90);
    var c = base * (0.92 + 0.08 * fiber);
    c = c * (0.95 + 0.05 * grain);
    return c * tint;
}

fn tex_marble(p: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let t = turbulence(p.x * 1.2, p.y * 1.2, p.z * 1.2, 5u);
    let veins = 0.5 + 0.5 * sin((p.x + p.z) * 1.5 + 6.0 * t);
    let base = vec3<f32>(0.85, 0.85, 0.88);
    let vein = vec3<f32>(0.18, 0.18, 0.22);
    return mix3(vein, base, pow(veins, 0.6)) * tint;
}

fn tex_grass(p: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let patch_n = fbm(p.x * 0.6, p.y * 0.6, p.z * 0.6, 3u);
    let blade = 0.5 + 0.5 * perlin(p.x * 9.0, p.y * 9.0, p.z * 9.0);
    let lush = vec3<f32>(0.12, 0.30, 0.08);
    let dry = vec3<f32>(0.36, 0.34, 0.13);
    var c = mix3(lush, dry, smoothstepf(-0.25, 0.5, patch_n));
    c = c * (0.78 + 0.22 * blade);
    return c * tint;
}

fn tex_dirt(p: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let n = 0.5 + 0.5 * fbm(p.x * 3.0, p.y * 3.0, p.z * 3.0, 4u);
    let base = vec3<f32>(0.26, 0.17, 0.10);
    let dark = vec3<f32>(0.15, 0.10, 0.06);
    return mix3(dark, base, n) * tint;
}

fn tex_snow(p: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let drift = 0.5 + 0.5 * fbm(p.x * 1.5, p.y * 1.5, p.z * 1.5, 3u);
    let sparkle = perlin(p.x * 55.0, p.y * 55.0, p.z * 55.0);
    let v = 0.86 + 0.10 * drift;
    var c = vec3<f32>(v * 0.97, v * 0.99, min(1.0, v * 1.04));
    if (sparkle > 0.85) { c = c * 1.12; }
    return c * tint;
}

// --- wallpaper (port of internal/texture/wallpaper.go) ----------------------

fn wp_line(d: f32, w: f32) -> f32 { return 1.0 - smoothstepf(w, w + 0.010, d); }

fn wp_dot(x: f32, y: f32, r: f32) -> f32 {
    return 1.0 - smoothstepf(r, r + 0.012, length(vec2<f32>(x, y)));
}

fn wp_ring(x: f32, y: f32, cx: f32, cy: f32, rx: f32, ry: f32, t: f32) -> f32 {
    let q = length(vec2<f32>((x - cx) / rx, (y - cy) / ry));
    return 1.0 - smoothstepf(t, t + 0.20, abs(q - 1.0));
}

fn wp_fill(x: f32, y: f32, cx: f32, cy: f32, rx: f32, ry: f32) -> f32 {
    let q = length(vec2<f32>((x - cx) / rx, (y - cy) / ry));
    return 1.0 - smoothstepf(0.86, 1.02, q);
}

fn wp_band(x: f32, lo: f32, hi: f32) -> f32 {
    return smoothstepf(lo - 0.03, lo + 0.03, x) * (1.0 - smoothstepf(hi - 0.03, hi + 0.03, x));
}

fn wp_rot(x: f32, y: f32, a: f32) -> vec2<f32> {
    let c = cos(a);
    let s = sin(a);
    return vec2<f32>(x * c - y * s, x * s + y * c);
}

fn wp_iris(mx: f32, ly: f32) -> f32 {
    var g = 0.0;
    g = max(g, wp_ring(mx, ly, 0.0, 0.085, 0.052, 0.150, 0.18));
    let r0 = wp_rot(mx - 0.012, ly - 0.03, -0.62);
    g = max(g, wp_ring(r0.x, r0.y, 0.105, 0.0, 0.120, 0.046, 0.22));
    g = max(g, wp_fill(mx, ly, 0.0, -0.085, 0.030, 0.060));
    g = max(g, wp_line(mx, 0.010) * wp_band(ly, -0.40, -0.10));
    return g;
}

fn wp_leaves(mx: f32, ly: f32) -> f32 {
    let r0 = wp_rot(mx - 0.035, ly + 0.085, -0.52);
    return wp_ring(r0.x, r0.y, 0.105, 0.0, 0.230, 0.034, 0.20);
}

fn wp_motif(fu: f32, fv: f32) -> f32 {
    let mx = abs(fu - 0.5);
    let ly = fv - 0.5;
    var g = 0.0;
    let sx = 0.5 * sin(TEX_PI * fv);
    let d = abs(mx - sx);
    g = max(g, wp_line(d, 0.013));
    g = max(g, 0.55 * wp_line(abs(d - 0.040), 0.005));
    g = max(g, wp_dot(mx, abs(ly) - 0.5, 0.034));
    g = max(g, wp_iris(mx, ly));
    g = max(g, wp_leaves(mx, ly));
    if (g > 1.0) { g = 1.0; }
    return g;
}

fn tex_wallpaper(p: vec3<f32>, tint: vec3<f32>, tex: u32) -> vec3<f32> {
    var bg = vec3<f32>(0.052, 0.073, 0.145);
    var ink = vec3<f32>(0.45, 0.36, 0.18);
    if (tex == TEX_WALLPAPER_GREEN) {
        bg = vec3<f32>(0.058, 0.110, 0.085);
        ink = vec3<f32>(0.46, 0.39, 0.22);
    } else if (tex == TEX_WALLPAPER_ROSE) {
        bg = vec3<f32>(0.160, 0.060, 0.078);
        ink = vec3<f32>(0.48, 0.36, 0.19);
    }
    let u = (p.x + p.z) / 0.55;
    let v = p.y / 0.775;
    let g = wp_motif(fracf(u), fracf(v)) * 0.68;
    let bgm = bg * (0.96 + 0.06 * (0.5 + 0.5 * fbm(p.x * 3.1, p.y * 3.1, p.z * 3.1, 3u)));
    let inkm = ink * (0.88 + 0.12 * (0.5 + 0.5 * perlin(p.x * 5.0 + 11.0, p.y * 5.0, p.z * 5.0)));
    return mix3(bgm, inkm, g) * tint;
}

fn stone_wall_palette(i: i32) -> vec3<f32> {
    var pal = array<vec3<f32>, 6>(
        vec3<f32>(0.80, 0.79, 0.76),
        vec3<f32>(0.78, 0.73, 0.62),
        vec3<f32>(0.64, 0.63, 0.60),
        vec3<f32>(0.72, 0.66, 0.54),
        vec3<f32>(0.74, 0.71, 0.66),
        vec3<f32>(0.55, 0.54, 0.52),
    );
    var idx = i;
    if (idx < 0) { idx = 0; }
    if (idx > 5) { idx = 5; }
    return pal[idx];
}

fn tex_stone_wall_2d(p: vec3<f32>, u_in: f32, v_in: f32) -> vec3<f32> {
    let cell = 0.34;
    let u = u_in / cell;
    let v = v_in / cell;
    let cu = floor(u);
    let cv = floor(v);
    let fu = u - cu;
    let fv = v - cv;

    var f1 = 1.0e9;
    var f2 = 1.0e9;
    var ox = 0.0;
    var oy = 0.0;
    for (var j = -1; j <= 1; j = j + 1) {
        for (var i = -1; i <= 1; i = i + 1) {
            let cx = cu + f32(i);
            let cy = cv + f32(j);
            let px = f32(i) + cell_rand(cx, cy, 11.0);
            let py = f32(j) + cell_rand(cx, cy, 12.0);
            let dx = px - fu;
            let dy = py - fv;
            let d = sqrt(dx * dx + dy * dy);
            if (d < f1) {
                f2 = f1;
                f1 = d;
                ox = cx;
                oy = cy;
            } else if (d < f2) {
                f2 = d;
            }
        }
    }
    let edge = f2 - f1;

    let pick = cell_rand(ox, oy, 1.0);
    let bright = cell_rand(ox, oy, 2.0);
    var base = stone_wall_palette(i32(pick * 6.0));
    base = base * (0.62 + 0.55 * bright);
    base.x = base.x * (0.94 + 0.12 * cell_rand(ox, oy, 5.0));
    base.y = base.y * (0.94 + 0.12 * cell_rand(ox, oy, 6.0));
    base.z = base.z * (0.92 + 0.14 * cell_rand(ox, oy, 7.0));

    let mottle = 0.84 + 0.16 * fbm(p.x * 7.0 + ox * 3.0, p.y * 7.0 + oy * 3.0, p.z * 7.0, 4u);
    let grain = 0.90 + 0.10 * fbm(p.x * 32.0, p.y * 32.0, p.z * 32.0, 3u);
    let relief = 1.0 - 0.18 * smoothstepf(0.05, 0.5, f1);
    var face = base * (mottle * grain * relief);

    let mortar_col = vec3<f32>(0.40, 0.37, 0.31) * (0.78 + 0.30 * fbm(p.x * 6.0, p.y * 6.0, p.z * 6.0, 3u));

    let mask = smoothstepf(0.02, 0.12, edge);
    return mix3(mortar_col, face, mask);
}

fn tex_stone_wall(p: vec3<f32>, n: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    var w = abs(n);
    // Sharpen the weights so box-like surfaces stay crisp near edges while still
    // blending smoothly on curved geometry.
    w = w * w * w * w;
    let sum = w.x + w.y + w.z;
    if (sum <= 0.0) {
        return tex_stone_wall_2d(p, p.x, p.y) * tint;
    }

    // Project by dominant normal: Z-facing walls use XY, X-facing walls use ZY,
    // and horizontal surfaces use XZ.
    let x_proj = tex_stone_wall_2d(p, p.z, p.y) * w.x;
    let y_proj = tex_stone_wall_2d(p, p.x, p.z) * w.y;
    let z_proj = tex_stone_wall_2d(p, p.x, p.y) * w.z;
    return (x_proj + y_proj + z_proj) / sum * tint;
}

fn texture_eval(tex: u32, p: vec3<f32>, n: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    if (tex == TEX_NONE) { return tint; }
    if (tex == TEX_WOOD) { return tex_wood(p, tint); }
    if (tex == TEX_BRICK) { return tex_brick(p, tint); }
    if (tex == TEX_STONE) { return tex_stone(p, tint); }
    if (tex == TEX_CEMENT) { return tex_cement(p, tint); }
    if (tex == TEX_MARBLE) { return tex_marble(p, tint); }
    if (tex == TEX_GRASS) { return tex_grass(p, tint); }
    if (tex == TEX_DIRT) { return tex_dirt(p, tint); }
    if (tex == TEX_SNOW) { return tex_snow(p, tint); }
    if (tex == TEX_STONE_WALL) { return tex_stone_wall(p, n, tint); }
    if (tex == TEX_PAPER) { return tex_paper(p, tint); }
    return tex_wallpaper(p, tint, tex);
}

fn is_document(tex: u32) -> bool {
    return tex >= TEX_DOCUMENT_BASE && tex < TEX_DOCUMENT_BASE + TEX_DOCUMENT_COUNT;
}

fn is_capture(tex: u32) -> bool {
    return tex >= TEX_CAPTURE_BASE && tex < TEX_CAPTURE_BASE + TEX_CAPTURE_COUNT;
}

// Box face indices match texture.BoxFace* (+X, -X, +Y, -Y, +Z, -Z).
fn box_face_index(n: vec3<f32>) -> i32 {
    let an = abs(n);
    if (an.z >= an.x && an.z >= an.y) {
        if (n.z >= 0.0) { return 4; }
        return 5;
    }
    if (an.x >= an.y) {
        if (n.x >= 0.0) { return 0; }
        return 1;
    }
    if (n.y >= 0.0) { return 2; }
    return 3;
}

fn box_face_uv(lp: vec3<f32>, ln: vec3<f32>, bmin: vec3<f32>, bmax: vec3<f32>) -> vec2<f32> {
    let face = box_face_index(ln);
    let dx = bmax.x - bmin.x;
    let dy = bmax.y - bmin.y;
    let dz = bmax.z - bmin.z;
    if (dx <= 0.0 || dy <= 0.0 || dz <= 0.0) {
        return vec2<f32>(-1.0, -1.0);
    }
    var u = 0.0;
    var v = 0.0;
    if (face == 4) {
        u = (lp.x - bmin.x) / dx;
        v = 1.0 - (lp.y - bmin.y) / dy;
    } else if (face == 5) {
        u = 1.0 - (lp.x - bmin.x) / dx;
        v = 1.0 - (lp.y - bmin.y) / dy;
    } else if (face == 0) {
        u = 1.0 - (lp.z - bmin.z) / dz;
        v = 1.0 - (lp.y - bmin.y) / dy;
    } else if (face == 1) {
        u = (lp.z - bmin.z) / dz;
        v = 1.0 - (lp.y - bmin.y) / dy;
    } else if (face == 2) {
        u = 1.0 - (lp.x - bmin.x) / dx;
        v = 1.0 - (lp.z - bmin.z) / dz;
    } else if (face == 3) {
        u = (lp.x - bmin.x) / dx;
        v = 1.0 - (lp.z - bmin.z) / dz;
    } else {
        return vec2<f32>(-1.0, -1.0);
    }
    return vec2(clamp(u, 0.0, 1.0), clamp(v, 0.0, 1.0));
}

fn box_face_texture(prim_idx: u32, ln: vec3<f32>) -> u32 {
    let face = box_face_index(ln);
    if (face < 0) {
        return TEX_NONE;
    }
    return box_face_textures[prim_idx * 6u + u32(face)];
}

fn sample_capture(tex: u32, u: f32, v: f32, tint: vec3<f32>) -> vec3<f32> {
    if (params.capture_loaded == 0u) {
        return tint;
    }
    let w = params.capture_w;
    let h = params.capture_h;
    if (w == 0u || h == 0u) {
        return tint;
    }
    let slot = tex - TEX_CAPTURE_BASE;
    let xi = min(u32(u * f32(w)), w - 1u);
    let yi = min(u32(v * f32(h)), h - 1u);
    let idx = slot * w * h + yi * w + xi;
    let px = capture_pixels[idx];
    let r = f32(px & 255u) / 255.0;
    let g = f32((px >> 8u) & 255u) / 255.0;
    let b = f32((px >> 16u) & 255u) / 255.0;
    return vec3(r * tint.x, g * tint.y, b * tint.z);
}

fn document_face_uv(lp: vec3<f32>, ln: vec3<f32>, bmin: vec3<f32>, bmax: vec3<f32>) -> vec2<f32> {
    let an = abs(ln);
    if (an.z < an.x || an.z < an.y) {
        return vec2<f32>(-1.0, -1.0);
    }
    let u = (lp.x - bmin.x) / (bmax.x - bmin.x);
    let v = 1.0 - (lp.y - bmin.y) / (bmax.y - bmin.y);
    return vec2(clamp(u, 0.0, 1.0), clamp(v, 0.0, 1.0));
}

fn sample_document(tex: u32, u: f32, v: f32, tint: vec3<f32>) -> vec3<f32> {
    if (params.document_loaded == 0u) {
        return tex_paper(vec3<f32>(u * 10.0, v * 10.0, 0.0), tint);
    }
    let slot = tex - TEX_DOCUMENT_BASE;
    let w = DOCUMENT_TEX_W;
    let h = DOCUMENT_TEX_H;
    let xi = min(u32(u * f32(w)), w - 1u);
    let yi = min(u32(v * f32(h)), h - 1u);
    let idx = slot * w * h + yi * w + xi;
    let px = document_pixels[idx];
    let r = f32(px & 255u) / 255.0;
    let g = f32((px >> 8u) & 255u) / 255.0;
    let b = f32((px >> 16u) & 255u) / 255.0;
    return vec3(r * tint.x, g * tint.y, b * tint.z);
}

fn texture_eval_document(tex: u32, lp: vec3<f32>, ln: vec3<f32>, bmin: vec3<f32>, bmax: vec3<f32>, tint: vec3<f32>) -> vec3<f32> {
    let uv = document_face_uv(lp, ln, bmin, bmax);
    if (uv.x < 0.0) {
        return tex_paper(lp, tint);
    }
    let ink = sample_document(tex, uv.x, uv.y, vec3<f32>(1.0, 1.0, 1.0));
    let grain = tex_paper(lp, vec3<f32>(1.0, 1.0, 1.0));
    return ink * (0.98 + 0.02 * grain);
}

fn texture_eval_box(prim_idx: u32, lp: vec3<f32>, ln: vec3<f32>, bmin: vec3<f32>, bmax: vec3<f32>, fallback_tex: u32, tint: vec3<f32>) -> vec3<f32> {
    var tex = box_face_texture(prim_idx, ln);
    if (tex == TEX_NONE) {
        tex = fallback_tex;
    }
    if (tex == TEX_NONE) {
        return tint;
    }
    let uv = box_face_uv(lp, ln, bmin, bmax);
    if (uv.x < 0.0) {
        return tint;
    }
    if (is_capture(tex)) {
        if (params.capture_loaded == 0u) {
            return tint;
        }
        return sample_capture(tex, uv.x, uv.y, vec3<f32>(1.0, 1.0, 1.0));
    }
    if (is_document(tex)) {
        return texture_eval_document(tex, lp, ln, bmin, bmax, tint);
    }
    return texture_eval(tex, lp, ln, tint);
}

// plane_albedo applies the analytic checker pattern (matching Plane.AlbedoAt)
// before any procedural texture is layered on top.
fn plane_albedo(p: Prim, hp: vec3<f32>) -> vec3<f32> {
    if (p.info.y == MAT_CHECKER) {
        let cx = i32(floor(hp.x + 0.5));
        let cz = i32(floor(hp.z + 0.5));
        if (((cx + cz) & 1) != 0) {
            return p.albedo2.xyz;
        }
    }
    return p.albedo.xyz;
}
