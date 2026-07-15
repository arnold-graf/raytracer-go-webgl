# Scene & Object TOML Reference

How scenes and reusable objects are described in this raytracer. Scenes are
plain [TOML](https://toml.io) files that the loader in
`internal/sceneio/toml.go` decodes into a `scene.Scene`. There is no scene
editor — you hand-write these files and (optionally) hot-reload them while the
app runs.

- [Quick start](#quick-start)
- [Coordinate system & conventions](#coordinate-system--conventions)
- [Top-level structure](#top-level-structure)
- [Shared surface fields](#shared-surface-fields)
- [Per-primitive transforms](#per-primitive-transforms)
- [Primitives](#primitives)
- [Lights, flickering lights & sounds](#lights-flickering-lights--sounds)
- [Terrain & water](#terrain--water)
- [Camera & environment](#camera--environment)
- [Composing scenes: `extends`](#composing-scenes-extends)
- [Reusable objects: `[[include]]`](#reusable-objects-include)
- [Parameterized objects (templating)](#parameterized-objects-templating)
- [Hot reload](#hot-reload)
- [Enumerations (materials, textures, skies, sounds)](#enumerations)
- [Gotchas](#gotchas)

---

## Quick start

Run a scene:

```bash
go run . -scene scenes/indoor-outdoor.toml                 # interactive (Ebiten window, WebGPU)
go run ./cmd/preview -scene scenes/indoor-outdoor.toml -o out.png -w 900 -h 600 # headless PNG (WebGPU)
```

A minimal scene:

```toml
[camera]
pos = [0.0, 1.6, 5.0]
yaw = 0.0
pitch = 0.0

[environment]
sky = "clear"

[[sphere]]
center = [0.0, 1.0, 0.0]
radius = 1.0
material = "diffuse"
albedo = [0.8, 0.3, 0.3]

[[plane]]
normal = [0.0, 1.0, 0.0]
d = 0.0
material = "checker"
albedo = [0.9, 0.9, 0.9]
albedo2 = [0.2, 0.2, 0.2]
```

---

## Coordinate system & conventions

- **Right-handed, Y-up.** `+Y` is up; the floor is usually `y = 0`.
- **Units are arbitrary** but treated as meters by convention (player eye height
  is ~1.3, walls a few units tall).
- **Vectors** are 3-element arrays `[x, y, z]`. Colors are also `[r, g, b]`,
  usually `0..1` but emitters/lights use values `> 1` for intensity.
- **Camera `yaw`/`pitch` are radians** (not degrees). Pitch is clamped to
  ±1.3 rad in-engine.
- **Rotation fields (`rotate_x/y/z`) are degrees.** Rotations apply in X→Y→Z
  order about a pivot.
- **Numbers:** floats are safest (`0.0`), but the decoder accepts integer
  literals in float fields too (`range = 16`).

---

## Top-level structure

A scene file is a single TOML document. These top-level keys are recognized
(all optional):

| Key | Kind | Purpose |
|-----|------|---------|
| `extends` | string | Inherit from a base scene (see [extends](#composing-scenes-extends)) |
| `[camera]` | table | Spawn pose |
| `[environment]` | table | Sky, ambient light, sun |
| `[[include]]` | array | Merge a reusable object/sub-scene |
| `[[sphere]]` | array | Sphere primitives |
| `[[plane]]` | array | Infinite planes |
| `[[box]]` | array | Axis-aligned boxes (with optional CSG holes) |
| `[[cylinder]]` | array | Finite cylinders |
| `[[cone]]` | array | Finite cones |
| `[[torus]]` | array | Tori (ring lies flat in XZ, axis = Y) |
| `[[terrain]]` | array | Heightfield terrain (+ features & pads) |
| `[[water]]` | array | Circular water pools |
| `[[light]]` | array | Point lights |
| `[[light_flickering]]` | array | Animated flickering light cluster |
| `[[sound]]` | array | Spatial ambient emitters |

`[[name]]` is TOML's "array of tables": repeat the block to add more of that
kind.

---

## Shared surface fields

Every primitive (sphere, plane, box, cylinder, cone, torus, water) accepts the
same shading fields:

| Field | Type | Default | Meaning |
|-------|------|---------|---------|
| `material` | string | — (required) | One of the [materials](#materials) |
| `albedo` | `[r,g,b]` | `[0,0,0]` | Base color (or tint for a texture; emitters use `>1` for brightness) |
| `texture` | string | none | Procedural [texture](#textures) layered over `albedo` |
| `rough` | float | `0.0` | Microfacet roughness (blurs reflections/refractions) |
| `ior` | float | `1.5` | Index of refraction (glass) |
| `reflect` | float | `0.0` | `0..1` mirror reflection blended on top of a diffuse/textured surface |
| `transmit` | float | `0.0` | `0..1` glass transparency (tint from `albedo`) |

Notes:
- `reflect` adds a mirror layer to an otherwise diffuse surface; it's ignored by
  materials that are already reflective/refractive (`mirror`, `metal`, `glass`,
  `emit`).
- `texture` multiplies/tints by `albedo`; if `albedo` is omitted the texture
  shows its natural colors.

---

## Per-primitive transforms

Any primitive may be rotated in place by adding rotation fields. Rotation is
about `transform_origin` (defaults to the geometric **center**), in **degrees**,
X→Y→Z order:

```toml
[[box]]
min = [-1.0, 0.0, -1.0]
max = [ 1.0, 2.0,  1.0]
material = "diffuse"
albedo = [0.8, 0.8, 0.8]
rotate_y = 30.0
transform_origin = [0.0, 0.0, 0.0]   # optional: rotate about a corner/hinge
```

Unified placement rule: `world(p) = R · (p_local − origin) + at`. For
primitives with only `rotate_*`, `at` is implicit zero and `transform_origin`
defaults to the shape's center (box midpoint, cylinder axis midpoint, etc.).

Internally the renderer intersects in the primitive's local space and maps the
normal back to world space, so rotated geometry is exact (no AABB
approximation). Omitting all three `rotate_*` leaves the primitive
axis-aligned (no transform overhead).

---

## Primitives

### Sphere
```toml
[[sphere]]
center = [0.0, 1.0, 0.0]
radius = 1.0
material = "metal"
albedo = [1.0, 0.78, 0.2]
rough = 0.06
```

### Plane (infinite)
```toml
[[plane]]
normal = [0.0, 1.0, 0.0]
d = 0.0                 # plane is  normal·x + d = 0
material = "checker"
albedo  = [0.9, 0.9, 0.9]
albedo2 = [0.1, 0.1, 0.1]  # second checker color (only for material = "checker")
```
Planes are infinite, so they're best for fully enclosed scenes or as a ground
in an outdoor scene without terrain. (In mixed indoor/outdoor scenes use boxes
for floors so they don't slice through the open world.)

### Box (with optional CSG holes)
```toml
[[box]]
min = [-4.9, 0.0, -5.9]
max = [-4.5, 6.3,  5.9]
material = "diffuse"
albedo = [1.0, 1.0, 1.0]
texture = "brick"

# A real see-through opening cut through the box (constructive solid geometry).
# Make it overshoot the wall thickness so it pierces both faces cleanly.
[[box.hole]]
min = [-5.0, 1.5, -1.0]
max = [-4.4, 3.5,  1.0]
```
`[[box.hole]]` sub-tables subtract rectangular volumes from the box — used for
windows and doorways. A box may have multiple holes.

### Cylinder (finite, axis = Y)
Defined like a box footprint: `pos_*` is the minimum corner of the square
bounding the circular cross-section; `width` is the diameter; `height` is the
vertical extent. For tapering, set `width_bottom` and `width_top` (diameters).

```toml
[[cylinder]]
pos_x = -0.28
pos_y = 0.4
pos_z = -0.28
width = 0.56
height = 4.0
material = "diffuse"
texture = "stone"
```

Tapered example (pine trunk):

```toml
[[cylinder]]
pos_x = -0.65
pos_y = 0.4
pos_z = -0.65
width_bottom = 1.3
width_top = 0.4
height = 7.2
material = "diffuse"
texture = "wood"
```

### Cone (finite, axis = Y)
```toml
[[cone]]
cx = 0.0
cz = 0.0
rbase = 0.45    # radius at ybase; tapers to a point at ytip
ybase = 4.4
ytip  = 5.3
material = "metal"
```

### Torus (ring in XZ plane, axis = Y)
```toml
[[torus]]
center = [0.0, 1.9, 0.0]
major = 0.8     # ring radius
minor = 0.22    # tube radius (so total height = 2*minor)
material = "metal"
albedo = [1.0, 0.6, 0.1]
```

Spheres, boxes, cylinders, cones and tori are accelerated by a BVH. Planes,
terrain and water are tested directly.

---

## Lights, flickering lights & sounds

### Point light
```toml
[[light]]
pos = [0.0, 4.0, 0.0]
color = [8.0, 6.0, 4.0]  # per-channel intensity (HDR, can exceed 1)
radius = 0.35            # informational (soft-shadow size)
range = 16.0            # cull distance: beyond it the light + its shadow ray are skipped (0 = infinite)
brightness = 1.0        # scales color (folded in at load; default 1)
```
`range` is the key performance/locality knob: a light with `range = 16` only
affects geometry within 16 units, so interior lights vanish (with their shadow
rays) once you walk outside.

### Light flickering (animated cluster)
```toml
[[light_flickering]]
center = [0.0, 0.42, 3.0]
color = [5.5, 2.7, 0.95]  # default warm [3.6,1.7,0.55] if omitted
brightness = 0.25         # default 1
range = 20.0
flicker = 0.75            # flicker depth (default 0.45)
jitter = 0.16             # positional jitter of sub-lights / "dancing shadows" (default 0.16)
speed = 1.0               # flicker speed (default 1)
seed = 0.0                # optional, for deterministic variation
lights = 3                # sub-light count (default 3)
```
A bare `[[light_flickering]]` with just a `center` already looks like a fire (all other
fields default). Pair with `objects/campfire.toml` for logs plus light.

### Sound (spatial ambience)
```toml
[[sound]]
sound = "crickets"   # only registered ambient sound currently
at = [-9.0, 2.7, 2.0]
gain = 0.32          # default 0.3
radius = 20.0        # default 20; audible falloff radius
```
Ambient emitters are ray-occluded: a wall between you and the emitter muffles
it (so crickets outside go quiet indoors). Footstep sounds are derived
automatically from the surface you walk on — they are not authored here.

---

## Terrain & water

### Terrain (heightfield)
```toml
[[terrain]]
origin = [-40.0, 0.0, -40.0]
size = [80.0, 80.0]
base = 0.0
detail = 0.35          # fine noise amplitude
detail_scale = 0.12    # fine noise frequency
step = 0.28
grass = "grass"        # textures for the three height/slope bands
rock  = "stone"
snow  = "snow"
slope_lo = 0.32        # below this slope = grass; above slope_hi = rock
slope_hi = 0.68
snow_lo = 7.5          # height where snow begins/ends
snow_hi = 10.5

[[terrain.feature]]    # sculpt hills/valleys/ridges
kind = "peak"          # "peak" or "valley"
pos = [-16.0, -24.0]   # X,Z
height = 12.0
width = 11.0
steepness = 2.0
extend = [3.0, 1.0]    # optional: stretch into a ridge
angle = 0.0            # optional: rotate the feature

[[terrain.pad]]        # flatten a building site into the terrain
center = [16.0, -2.0]  # X,Z
half = [4.9, 5.9]      # inner flat half-extent
level = 0.0            # height offset above natural ground (default)
absolute = true        # set for a fixed world elevation instead
margin = 4.0           # smooth blend ring around the pad
```
Use `[[terrain.pad]]` to give buildings a flat footprint so floors don't poke
through uneven ground. By default `level` is added to the natural terrain height
at the pad center (ideal for reusable object files). Set `absolute = true` when
authoring a scene pad at a specific world elevation.

### Water (circular pool or infinite ocean)
```toml
[[water]]
pos = [0.0, 8.0]       # X,Z center (ignored for infinite ocean)
radius = 5.5           # disk radius; 0 = infinite ocean to the horizon
level = -1.2           # water surface height
mask = true            # clip water over dry land (default true when radius <= 0)
material = "mirror"
albedo = [0.55, 0.70, 0.85]
ripple = 0.05
ripple_animation_speed = 0.6
ripple_direction = [1.0, 0.4]
```

Island landmasses use `[terrain.island]` to fade height to a seabed `floor`
outside `radius` over a smooth `margin`:

```toml
[terrain.island]
center = [0.0, 0.0]
radius = 70.0
margin = 45.0
floor = -12.0
```

---

## Camera & environment

```toml
[camera]
pos = [16.0, 1.6, 16.0]
yaw = 0.0      # radians
pitch = 0.02   # radians

[environment]
sky = "night_stars"             # see Skies; default "clear"
ambient_sky    = [0.035, 0.050, 0.090]  # hemispheric ambient (up)
ambient_ground = [0.018, 0.018, 0.025]  # hemispheric ambient (down)
sun_dir   = [-0.25, -0.82, 0.42]        # direction light travels; normalized at load
sun_color = [0.10, 0.13, 0.20]

# Optional visible sun/moon disc. It is drawn in the sky opposite sun_dir (the
# body sits where the light comes from), so sun_dir must be set. It is purely
# cosmetic — geometry occludes it and it appears in reflections.
[environment.sun]
color = [0.85, 0.90, 1.05]  # disc radiance (values > 1 read as bright/HDR)
size  = 4.0                 # angular diameter in degrees
glow  = 1.0                 # halo strength (omit or 0 -> 1.0; 0-ish for a bare disc)
```
The sky/ambient model is evaluated in the WebGPU shader. The disc is composited
on top of the selected sky variant; the variants also have their own built-in
sun/moon glow, so for one coherent body align `sun_dir` with the variant's light
or pick a `sky` whose glow you don't mind doubling.

---

## Composing scenes: `extends`

A scene can inherit from a base scene and override parts of it:

```toml
extends = "outdoors.toml"   # path relative to this file

[environment]
sky = "sunset"
```

`extends` is used for sky-preset variants (see `scenes/outdoors-*.toml`).

**What a child can override:** `camera`, `environment`, `[[light]]` (replaces
the base's entire light list), and `[[light_flickering]]` (replaces the base's
flickering lights). It may also add `[[include]]` blocks.

**Important limitation:** loose primitive tables (`[[sphere]]`, `[[box]]`, …)
written directly in an `extends` child are **ignored** — only overrides and
includes are applied on top of the base. To add geometry to an extended scene,
put it in a separate file and pull it in via `[[include]]`.

---

## Reusable objects: `[[include]]`

An object is just a scene file written in **local coordinates**. You drop it into
a parent scene with `[[include]]`, which merges all of its primitives after
applying an instance transform:

```toml
[[include]]
file = "objects/staircase.toml"  # path relative to the including file
at = [16.0, 0.0, -2.0]           # where transform_origin lands in the parent
rotate_y = 180.0
transform_origin = [0, 0, 0]     # stairs: pivot at bottom-front corner (file origin)
```

By default `transform_origin = "center"` (omitted in files). `at` is where the
sub-scene bounds center lands. For objects authored at file origin (stairs,
pine trees), set `transform_origin = [0, 0, 0]` on the `[[include]]`.

How it works:
- The object's geometry stays in its local space; the include attaches a
  transform: rotate about `transform_origin`, then translate so that anchor
  lands at `at`. The renderer maps rays into local space and back, so rotated
  composites are exact.
- **Includes nest.** An object can itself `[[include]]` other objects; the
  transforms compose. For example `objects/building.toml` includes
  `objects/otto-wagner-sphere-lamp.toml`, and both compose with wherever the
  building is placed.
- All primitive kinds (spheres, cylinders, cones, tori, boxes, lights,
  flickering lights, sounds) are merged and placed correctly.

Real objects live in `scenes/objects/` — `building.toml`, `staircase.toml`,
`otto-wagner-sphere-lamp.toml`, `tiled-stove-round.toml` are good examples.

---

## Parameterized objects

Objects can be parameterized so one file produces variants. Files remain **valid
TOML**; expansion is handled by `internal/sceneparam` before decode.

Pass parameters from the include with an inline `params` table (merged into
`[props]`):

```toml
[[include]]
file = "objects/otto-wagner-sphere-lamp.toml"
at = [14.5, 5.7, -2.0]
params = { stem_len = 2.0, orb_radius = 0.5 }
```

In the object file:

```toml
[props]
stem_len = 1.5
orb_radius = 0.4

[const]
orb_y = '-(stem_len + orb_radius * 0.875)'

[[cylinder]] # stem
pos_x = -0.045
pos_y = '-stem_len'
pos_z = -0.045
width = 0.09
height = 'stem_len'

[[sphere]]
center = [0.0, 'orb_y', 0.0]
radius = 'orb_radius'
```

Syntax:
- **`[props]`** — overridable defaults; `params` from `[[include]]` shallow-merge on top.
- **`[const]`** — derived values (`half = 'width / 2'`); evaluated after merge.
- **Single-quoted strings** at use sites — expressions (`pos_x = '-half'`,
  `albedo = 'albedo'`). Double-quoted strings and bare numbers are literals.
- **Comment directives** — `# for i in range(steps)` … `# endfor`, `# let n = i + 1`,
  `# if texture` … `# endif`.
- **Helpers** — `leg_x(i, off, r)`, `leg_z(i, off, r)`, `ring_lerp(i, n, top, bot)`,
  `floor(x)`.

Example — staircase steps:

```toml
[props]
steps = 8
run = 0.5
rise = 0.375
width = 1.6

# for i in range(steps)
[[box]]
pos_x = 'i * run'
height = '(i + 1) * rise'
width = 'run'
depth = 'width'
# endfor
```

Files without `[props]`, `[const]`, or `# for` pass through unchanged. Parameterized
files must not contain `{{` (legacy Go templates are removed).

See `plans/scene-templating-v2.md` and `scenes/objects/staircase.toml`.

- On reload the WebGPU scene buffers rebuild on the swap.

## Hot reload

When you pass `-scene <file>` (and/or `-player <file>`), the app watches those
files **and everything they reach** through `extends` and `[[include]]`, and
rebuilds the scene live when any of them changes:

- Editing an included object (e.g. `objects/building.toml`) or its params in the
  parent triggers a reload — the watcher tracks the full dependency set.
- Reloads are polled a few times per second; the camera pose and feature
  toggles are preserved across a reload.
- **Bad edits are safe.** A template error, a TOML syntax error, or an unknown
  material/texture makes the reload fail; the app keeps the last good scene,
  shows a `scene reload FAILED: …` toast, and retries on the next poll. It never
  crashes on a malformed save.
- Works on both CPU and WebGPU backends (the GPU scene buffers rebuild on the
  swap).

Press `0` in-app to hide/show the dev HUD (fps, backend, timings); the reload
toast still appears so you get confirmation while iterating.
`wood`, `brick`, `stone`, `stone_wall`, `cement`, `marble`, `grass`, `dirt`,
`snow`, `wallpaper_navy`, `wallpaper_green`, `wallpaper_rose` (and `none`).

`stone_wall` is a natural rubble-stone masonry (irregular rounded stones in
greys/beiges with sandy mortar gaps). It uses triplanar projection, so it keeps
the stone pattern on walls facing any axis and blends smoothly on slanted or
curved surfaces.

## Enumerations
tint by `albedo` and cost nothing to "store". The procedural definitions are
generated on the CPU (`internal/texture`) and ported to the WebGPU shader.
`diffuse`, `mirror`, `metal`, `glass`, `emit`, `checker`

- `diffuse` — matte (add `reflect`/`texture` to embellish).
- `metal` — glossy reflective; tinted by `albedo`, blurred by `rough`.
- `mirror` — near-perfect reflection.
- `glass` — refraction (tint from `albedo`, `transmit`, `ior`) blended with a
  Fresnel reflection.
- `emit` — light-emitting; `albedo` values `> 1` set brightness.
- `checker` — diffuse checkerboard using `albedo` + `albedo2` (planes).

### Textures
`wood`, `brick`, `stone`, `cement`, `marble`, `grass`, `dirt`, `snow`,
`wallpaper_navy`, `wallpaper_green`, `wallpaper_rose` (and `none`).

All are procedural (world-space Perlin/fBm), so there are no image files — they
tint by `albedo` and cost nothing to "store". They're CPU/GPU bit-parity
matched.

### Skies
`clear`, `cloudy`, `night_stars`, `night_storm`, `sunset` (default `clear`).

### Sounds (ambient)
`crickets` (attach to trees with `[[sound]]`).

---

## Gotchas

- **Camera angles are radians, rotations are degrees.** Easy to mix up.
- **`extends` children can't add loose primitives** — only override
  camera/environment/lights/light_flickering and add includes. Use an include for
  geometry.
- **Box holes should overshoot** the wall thickness so they pierce both faces;
  a hole flush with the face can leave a sliver.
- **Infinite planes** will slice through an open outdoor world — use boxes for
  floors/walls in mixed indoor-outdoor scenes.
- **Object files use local coordinates.** Author them around their own origin,
  then place with `at`/`rotate_*` in the include — don't bake world positions
  into the object.
- **Templated objects aren't standalone-previewable** (see the templating
  tradeoff above).
- **Light `range`** is what keeps interior lights local; without it (or with
  `0`) a light is global and you pay for its shadow rays everywhere.
