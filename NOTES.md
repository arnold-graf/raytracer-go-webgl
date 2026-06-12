# Project: Retro-Trace 3D

A retro 3D engine, evocative of the different techniques used in the mid 90ies.
Properties include: Optimization and designed for low resolutions, ray-tracing
of analytical shapes (think bryce 3D or even Toy Story) and (WIP) height-mapped
sprite based characters and objects. Its designed to have most of its assets
procedurally generated.

Configuration and "mapping" works via TOML files.

What makes it special in practice over say, GZDoom, is that the ray-tracing
gives these simple shapes and textures a lot of character simply by virtue of
the physics-inspired renderer.

## Future Plans

1. Port the ray-tracing itself to WebGPU for a 10-100x speed-up. Our geometry
   style with analytic shapes is ideally suited to that. There’s a plan in the
   plans folder.
2. Stress test the scale. I’d like to see a large expansive world, at least 2 km
   by 2km, with 500 m high peaks. It could contain many buildings with multiple
   stories and rooms (~100)? It would contain 100s of trees too. how would our
   engine handle that? what would we need to do to accomodate for that? I guess
   we could just start expanding and then profile.
3. An new primitive: Sprite/billboard NPCs with bump/height maps Verdict: the
most novel and the most involved rendering feature — very doable, design it as
its own primitive. A Doom-style impostor that still participates in ray-traced
lighting is a real technique. The pieces: Geometry: a camera-facing billboard (a
quad/plane that rotates to face the viewer) with an alpha-tested texture — rays
that hit transparent texels miss the sprite and continue. That alpha test in the
intersection is the core change. Lighting via a normal/bump map — yes, this is
exactly possible and it's the right instinct. You author (or derive) a normal
map for the sprite; at a hit you look up the per-texel normal instead of the
flat quad normal, then run your normal lighting/shadow code. That's what makes
"light catches the side of the arm." A pure height map works too (derive normals
from height gradients), but a normal map is more direct. Rim/backlight outline
("light behind the person lights the edge") — that's translucency/scatter at
grazing angles: where the surface normal is near-perpendicular to the light, add
a wrap-lighting or subsurface term. Combined with alpha-tested edges it reads as
a glowing silhouette. Very achievable as a shading term keyed on dot(normal,
light) near zero plus thickness. Shadows: the sprite should cast a shadow using
its alpha mask (an alpha-tested any-hit), or you accept a billboard-shaped
shadow for cheapness. WebGPU impact: significant — plan a phase for it. This
adds a new primitive kind (textured alpha-tested billboard), texture sampling of
actual image data (your current textures are all procedural — no image samplers
in the pipeline yet), and a normal-map fetch. On the GPU that means real
texture_2d bindings and samplers, which the procedural-only plan doesn't
currently provision. It's not hard, but it's a new capability in the GPU
renderer. I'd add it as a late phase (after textures/terrain) and note the
sampler/atlas requirement in the plan now.
