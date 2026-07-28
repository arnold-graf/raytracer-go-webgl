# Plan: Terrain Caves (Carving)

## Status: PROPOSED

## Goal

Allow **in-game carving of caves into existing terrain**. A carved cave inserts a
two-layer volume (floor + ceiling) below the existing ground surface. The player
walks on the ground, enters a cave through a hole or top opening, and then
experiences the cave's own floor and ceiling as separate traversable surfaces.

This is **not** a new terrain type or a replacement for the heightfield. It is an
**augmentation** — the ground heightfield stays exactly as it is; a cave volume is
punched into it and rendered as secondary surfaces.

---

## What the player experiences

```
                ┌────────────────── ground (current hgrid) ──────────────────┐
                │  walkable, fully unchanged from today                       │
                │                                                             │
  ray ↓ rays ↓ rays ↓   ← downward rays hit ground                          │
                │                                                             │
   ┌────────────┼────────────────────────────────────────────────────────────┤
   │  CAVE CEILING  ← below-ground ceiling surface                         │
   │                                            ray ↑ rays ↑                  │
   │                                    ↑ rays (inside cave) hit ceiling    │
   │                                                                    air │
   │                                     cave "room"                         │
   │                                                                    space│
   │                                            ray ↓ rays ↓                  │
   └────────────┼────────────────────────────────────────────────────────────┤
                │  CAVE FLOOR   ← bottom surface of cave volume             │
                │                                                             │
                │  ground rock behind cave floor                            │
                └────────────────────────────────────────────────────────────┘
```

The ground surface is **unchanged** — players walk on it, shadows test it,
physics use it. A cave carved into the ground exposes three additional surfaces:
the **ceiling** (just below ground) and the **floor** (deeper down). Entering a
cave means going through the ground/ceiling from above (walking through a tunnel
entrance or a cave opening from below).

---

## The core problem

Terrain is a **2.5D heightfield**: one height per (x, z). A ray marcher steps
along the ray and checks `ray.y - terrainHeight(ray.x, ray.z)` for sign
crossings. This works perfectly for a single surface per column, but a cave has
**two surfaces** (ceiling + floor) in the same vertical column.

The solution: keep the ground heightfield exactly as-is, and add two new
heightfields for the cave volume. The ray marcher selects the correct surface
based on the ray's direction and whether the ray origin is above or below ground.

---

## Design decisions (locked)

| Decision | Choice | Rationale |
| -------- | ------ | --------- |
| **Ground stays untouched** | `hgrid` is not modified by carving | Physics, shadows, placement — everything already works on ground |
| **Cave ceiling/floor** | Stored in separate arrays (`cmax`, `floor`) | Per-column ceiling and floor heights, only populated where caves exist |
| **Surface selection by direction** | Downward ray → ground/floor; upward ray → ceiling | Simple to reason about; no need for complex surface state tracking |
| **Carving is runtime API** | No TOML `[[terrain.cave]]` in v1 | User wants to carve "in-game", not author by hand |
| **Sparse cave data** | `cmax[i] == 0` means "no cave here" | Most of the terrain has no caves; sparse data = low overhead |
| **Cave normals** | Flip ceiling normal downward | Floor looks like an upside-down terrain surface — correct for caves |
| **GPU cave surfaces** | Storage buffer for cmax + floor | Dynamically edited, rebuilt per carve operation |

---

## Architecture

### Data structure changes

```go
type Terrain struct {
    // ── existing (unchanged) ──
    hgrid        []float64  // ground heights (heightfield, always present)
    ngrid        []vec.V    // ground normals
    cmin, cmax   []float64  // coarse min/max for empty-space skip
    cgnx, cgnz   int
    cInvDx, cInvDz float64
    // ... features, pads, island, textures, etc.

    // ── new: cave surfaces ──
    caveEnabled  bool       // true if any cave data exists in this terrain
    caveCeiling  []float64  // cave ceiling heights (nil or 0 = no ceiling at this cell)
    caveFloor    []float64  // cave floor heights  (nil or 0 = no floor at this cell)
    caveCeilingRebuild func() // deferred rebuild of affected cells
    caveFloorRebuild  func()
}
```

Both `caveCeiling` and `caveFloor` are sparse: a zero value means "no cave here,
ground is the only surface". Only cells within carved regions have non-zero
entries. This keeps memory overhead negligible when most of the terrain is
cave-free.

### Ray height query

The key function becomes direction-aware:

```go
// heightAt returns the nearest surface height along the ray's direction.
// If rayDirY < 0 (ray going down): returns ground height when ray origin is
//   above ground, or cave floor when ray origin is inside the cave (below ground).
// If rayDirY > 0 (ray going up): returns cave ceiling height when ray origin
//   is inside the cave, or ground height when above ground.
func (t *Terrain) heightAt(px, pz, py, rayDirY float64) float64 {
    if rayDirY < 0 {
        // Ray going down: ground first, cave floor only if below ground
        ground := t.Height(px, pz)
        if py < ground {
            // Inside or below ground — check for cave floor
            if floor := t.CaveFloor(px, pz); floor > 0 {
                return floor
            }
        }
        return ground
    }
    // Ray going up: cave ceiling first if inside cave, otherwise ground
    if py < t.Height(px, pz) {
        // Below ground — check for ceiling
        if ceil := t.CaveCeiling(px, pz); ceil > 0 {
            return ceil
        }
    }
    return t.Height(px, pz)
}
```

### Ray marcher update

The existing `marchFine` uses `py - t.Height(px, pz)`. It becomes:

```go
func (t *Terrain) marchFine(r vec.Ray, tEnter, tExit float64, refine bool) float64 {
    // … setup …
    for tc < tExit {
        step := t.computeStep(fc, r.Dir.Y)
        tn := tc + step
        if tn > tExit { tn = tExit }

        nx := r.Origin.X + r.Dir.X*tn
        ny := r.Origin.Y + r.Dir.Y*tn
        nz := r.Origin.Z + r.Dir.Z*tn
        fn := ny - t.heightAt(nx, nz, ny, r.Dir.Y) // ← direction-aware

        if fn <= 0 && fc > 0 {
            return t.refine(tc, tn, r)
        }
        tc, px, py, pz, fc = tn, nx, ny, nz, fn
    }
    return Inf
}
```

The key insight: when the ray is **above ground**, `heightAt` always returns
ground height regardless of ray direction, so the marcher behaves identically to
today. When the ray is **below ground** (inside a cave), `heightAt` returns the
appropriate cave surface, and the marcher finds the floor or ceiling.

### Normal computation

```go
func (t *Terrain) Normal(p vec.V, rayDirY float64) vec.V {
    ground := t.Height(p.X, p.Z)
    if p.Y < ground {
        // Below ground — inside cave
        if ceil := t.CaveCeiling(p.X, p.Z); ceil > 0 && abs(p.Y-ceil) < abs(p.Y-ground) {
            // Hit ceiling — normal points down (inverted)
            return t.CeilingNormal(p).Scale(-1)
        }
        if floor := t.CaveFloor(p.X, p.Z); floor > 0 && abs(p.Y-floor) < abs(p.Y-ground) {
            // Hit floor — normal points up (same as ground)
            return t.FloorNormal(p)
        }
    }
    // Above or on ground — normal as today
    return t.NormalAtGround(p)
}
```

The albedo (texture color) can reuse the same terrain texture — `terrain_albedo`
already blends grass/rock/snow by slope and height. For cave surfaces, swap
`rock` → `dirt`/`cave_wall` and `snow` → `ice` or just use `rock`. This is a
material distinction rather than a geometry one.

### In-game carving API

```go
// CarveCave carves a cave volume into this terrain starting from the ground
// surface downward. The cave occupies:
//
//   ground           → your walking surface (unchanged)
//   ground + ceilOff → cave ceiling (carved down from ground)
//   ceiling - ceilHgt → cave floor
//
// entranceRadius (default 0): if > 0, opens a vertical entrance at the top
// of the cave — the ceiling is zeroed in a circular zone of this radius so
// the player can walk in from above. Use CarveCaveFull for a fully enclosed
// cave (no entrance from above or sides).
func (t *Terrain) CarveCave(cx, cz, radius, ceilingOffset, ceilingHeight, entranceRadius float64) {
    // ceilingOffset > 0: how far below ground the ceiling sits (default 1–3 m)
    // ceilingHeight > 0: height of the cave ceiling above the floor (default 2–4 m)
    // entranceRadius > 0: radius of the top opening in the ceiling (default 0 = enclosed)

    t.ensurePrepared()
    if t.caveEnabled {
        t.rebuildCaveData(cx, cz, radius, -ceilingOffset, ceilingHeight)
        if entranceRadius > 0 {
            t.openEntrance(cx, cz, entranceRadius)
        }
    } else {
        t.buildCaveData(cx, cz, radius, -ceilingOffset, ceilingHeight, entranceRadius)
    }
    t.caveEnabled = true
}

// CarveCaveFull carves a fully enclosed cave: the ceiling sits at ground level
// (ceilingOffset = 0), so the player cannot walk in from above. The only way
// into the cave is through a separately opened entrance.
func (t *Terrain) CarveCaveFull(cx, cz, radius, ceilingDepth, ceilingHeight float64) {
    t.ensurePrepared()
    t.buildCaveData(cx, cz, radius, 0, ceilingHeight, 0)
    t.caveEnabled = true
}

// OpenEntrance adds a vertical entrance hole to an existing cave. It zeros the
// ceiling heights within a circular zone centered at (cx, cz) with the given
// radius. After opening, the player can walk down through the hole from the
// ground surface. If the cave floor is below ground (the normal case), this
// creates a shaft or well-like entrance.
func (t *Terrain) OpenEntrance(cx, cz, radius float64) {
    t.ensurePrepared()
    t.openEntrance(cx, cz, radius)
}
```

The implementation:

```go
func (t *Terrain) buildCaveData(cx, cz, radius, ceilingOffset, ceilingHeight, entranceRadius float64) {
    if t.caveCeiling == nil {
        t.caveCeiling = make([]float64, t.gnx*t.gnz)
        t.caveFloor = make([]float64, t.gnx*t.gnz)
    }

    for z := int(math.Max(0, cz-radius)); z < int(math.Min(float64(t.gnz-1), cz+radius)); z++ {
        for x := int(math.Max(0, cx-radius)); x < int(math.Min(float64(t.gnx-1), cx+radius)); x++ {
            dist := math.Hypot(float64(x)-float64(cx), float64(z)-float64(cz)) / radius
            if dist > 1.0 { continue }

            falloff := t.carveFalloff(dist, radius)
            ceilOff := ceilingOffset * falloff
            ceilHgt := ceilingHeight * falloff

            idx := z*t.gnx + x
            t.caveCeiling[idx] = t.hgrid[idx] + ceilOff
            t.caveFloor[idx] = t.caveCeiling[idx] - ceilHgt
        }
    }

    // Zero ceiling in the entrance zone (if specified)
    if entranceRadius > 0 {
        t.openEntrance(cx, cz, entranceRadius)
    }

    t.buildCoarse()       // update min/max for empty-space skip
    t.buildMipPyramid()   // rebuild mip chain
    t.stale = false
}
```

### Cave entrances

A cave entrance is simply a region where the cave ceiling is **zeroed** (or set
back to ground level), creating an opening in the terrain. The ray marcher
already handles this: when `caveCeiling[i] == 0`, it falls back to the ground
height, so rays pass through as if there is no cave at that cell.

Two natural entrance shapes:

#### Vertical entrance (shaft/well — walk in from above)

```
      ground surface (unchanged)
      ───────────────────────────────────────
                 │
           ┌─────┤ ← entrance opening (ceiling zeroed)
           │     │
           │     │
           │     │  ← player walks down
           │     │
      ┌────┤     │
      │ cave │    │ ← cave ceiling (present except at opening)
      │      │    │
      │ floor│    │ ← floor extends wider than opening
      └──────┘    │
                  │
```

Create with `CarveCave(cx, cz, radius, ceilingOffset, ceilingHeight, entranceRadius)`
where `entranceRadius > 0`. The ceiling is zeroed in a circular zone of that
radius at the top of the cave.

#### Horizontal entrance (tunnel — walk in from the side)

```
   ground
      ─────────────────────────────────────────
                  │ ╔══════════╗
                  │ ║  cave    ║
                  │ ║          ║
                  │ ║          ║
      ground      │ ╚══════════╝
      level ──────┤            ← at ground level, no ceiling = entrance
                  │ cave floor (under ground)
                  └───────────
```

A horizontal entrance exists naturally when `ceilingOffset < radius` — the ground
slopes into the cave at the side. For cleaner control, zero the ceiling along a
horizontal band using `OpenEntrance` or `OpenEntranceBand(cx, cz, halfX, halfZ)`.

#### Opening an entrance on an existing cave

```go
// After carving an enclosed cave, open an entrance:

// Vertical shaft entrance (circular hole at the top)
terrain.OpenEntrance(entranceCX, entranceCZ, entranceRadius)

// Horizontal tunnel entrance (rectangular opening)
terrain.OpenEntranceBand(entranceCX, entranceCZ, halfWidth, halfHeight)
```

Both methods set `caveCeiling[idx] = 0` for cells within the entrance zone,
which the ray marcher handles by falling back to ground height — the player
walks right through.

### Smooth falloff for carving

Caves should blend naturally into the terrain, not have sharp geometric edges.
The carving uses a smooth radial falloff:

```go
func (t *Terrain) carveFalloff(dist, radius float64) float64 {
    // 1.0 at center → 0.0 at radius edge (smooth)
    if dist >= radius { return 0 }
    t := dist / radius
    return t * t * (3 - 2 * t)  // smoothstep
}
```

### Edge erosion (optional but recommended)

When a cave has a side/tunnel entrance, the ground-level ceiling should be
"eroded" at the cave edge so the ground slopes naturally into the entrance.
This is a per-edge pass that blends the ceiling height to ground height at the
cave perimeter:

```go
func (t *Terrain) erodeCaveEdges() {
    // For cells near the cave boundary (not near an entrance), blend ceiling
    // → ground to avoid a hard "ceiling ledge" where the player walks in.
    // Entrance zones are left unmodified.
}
```

---

## GPU Changes

### `GPUTerrain` struct extension

```go
type GPUTerrain struct {
    // ... existing fields ...

    // New cave fields (sentinel values mean "no cave"):
    // caveBase  = terrain.offsets.y when caveEnabled, otherwise = heightOffset
    CaveEnabled uint32     // 0 or 1
    CaveDataOff uint32     // offset into terrain_cave_samples buffer
    CaveFlags   [2]uint32  // [flagsX, flagsZ] — which cells have cave data (bitmask)
}
```

### Cave samples buffer

A new storage buffer `terrain_cave_samples` stores per-cell packed data:

```wgsl
// terrain_cave_samples: packed vec2<f32> per cell
// x = ceiling height, y = floor height
// x == 0 && y == 0 → no cave at this cell
```

### WGSL changes

```wgsl
// In terrain.wesl:

fn terrain_height(cave_idx: u32, x: f32, z: f32, dir_y: f32) -> f32 {
    // ── ground surface (always present) ──
    let ground = terrain_height_sample(cave_idx, x, z, false);

    // ── cave surfaces ──
    if (dir_y < 0.0) {
        // Ray going down: if below ground, check for floor
        if (ground != terrain_height_baked(cave_idx, x, z)) {
            // We're in cave mode — return floor
            return terrain_cave_samples[cave_idx, x, z].y;
        }
    } else {
        // Ray going up: if below ground, check for ceiling
        if (ground != terrain_height_baked(cave_idx, x, z)) {
            return terrain_cave_samples[cave_idx, x, z].x;
        }
    }

    return ground;
}

fn terrain_normal(cave_idx: u32, p: vec3<f32>, dir_y: f32) -> vec3<f32> {
    let ground = terrain_height(cave_idx, p.x, p.z, dir_y);
    if (dir_y < 0.0) {
        // Downward ray
        if (p.y < ground) {
            // Inside cave — test floor (normal = up)
            return terrain_floor_normal(cave_idx, p);
        }
    } else {
        // Upward ray
        if (p.y < ground) {
            // Inside cave — test ceiling (normal = down, inverted)
            return terrain_ceiling_normal(cave_idx, p).xyz * -1.0;
        }
    }
    return terrain_ground_normal(cave_idx, p);
}
```

### Packing / uploading cave data

On every carve, the host rebuilds the affected cells and uploads only those cells
to the `terrain_cave_samples` buffer:

```go
func (t *Terrain) packCaveData() []float32 {
    // Pack non-zero ceiling/floor pairs into a compact slice
    var data []float32
    for i := 0; i < len(t.caveCeiling); i++ {
        ceil := t.caveCeiling[i]
        floor := t.caveFloor[i]
        if ceil != 0 && floor != 0 {
            data = append(data, float32(ceil), float32(floor))
        }
    }
    return data
}
```

---

## Shadow ray handling

Shadow rays are the trickiest case because they can fire in any direction:

| Shadow ray scenario | Surface to test |
|---------------------|-----------------|
| **Downward** from player on ground | Ground height (unchanged) |
| **Downward** from player inside cave | Cave floor |
| **Upward** from player inside cave | Cave ceiling |
| **Upward** from player on ground | No terrain occlusion (sky) |
| **Horizontal** from player inside cave | Cave ceiling or floor (whichever is closer) |

```go
func (t *Terrain) Occlude(r vec.Ray, maxT float64) float64 {
    if r.Dir.Y >= 0 {
        // Upward shadow ray: only relevant inside cave
        if py := r.Origin.Y; py < t.Height(r.Origin.X, r.Origin.Z) {
            if ceil := t.CaveCeiling(r.Origin.X, r.Origin.Z); ceil > 0 {
                return t.marchFineUpward(r, maxT)  // test ceiling
            }
        }
        return Inf  // no ceiling → no occlusion
    }
    // Downward shadow ray: ground always, floor only if inside cave
    ground := t.Height(r.Origin.X, r.Origin.Z)
    if r.Origin.Y < ground {
        if floor := t.CaveFloor(r.Origin.X, r.Origin.Z); floor > 0 {
            return t.marchFineDownward(r, maxT)  // test floor
        }
    }
    return t.march(r, maxT, false)  // test ground (unchanged)
}
```

---

## Implementation order

### Phase 1 — Go struct + marching

**Scope:** pure Go, no GPU, no UI. The existing preview renders caves correctly.

1. Add `caveCeiling`, `caveFloor`, `caveEnabled` to `Terrain` struct
2. Implement `CarveCave(cx, cz, radius, ceilingOffset, ceilingHeight)` method
3. Implement `CarveCaveFull(cx, cz, radius, ceilingDepth, ceilingHeight)` method
4. Update `heightAt(x, z, y, rayDirY)` to be direction-aware
5. Update `marchFine` to use `heightAt` instead of bare `Height`
6. Update `Normal` to return cave normals when appropriate
7. Update `AlbedoAt` for cave surface coloring
8. Update `NaturalTerrainHeightAt` to account for cave presence
9. Write unit tests: carve a cave, march into it, verify hits on floor/ceiling

**Gate:** preview renders a cave carved into `island.toml`. Player walks in, sees
ceiling above and floor below. Ray march finds correct surfaces from all angles.

### Phase 2 — Coarse grid + GPU

**Scope:** pack cave data into GPU buffers, update shader.

1. Add cave fields to `GPUTerrain` struct
2. Add `terrain_cave_samples` storage buffer in `scene.go`
3. Implement `packCaveData()` in Go — serialize non-zero cells
4. Update WGSL `terrain_height` to accept `dir_y` parameter
5. Update `terrain_normal` for cave surface normals
6. Update `hit_terrain_fine` and `hit_terrain_mip` for cave mode
7. Add cave shading (rock/dirt material for cave surfaces)

**Gate:** preview with cave carved, GPU renders ceiling and floor correctly.
Shadow rays inside cave occlude off ceiling/floor properly.

### Phase 3 — Input + volume carving

**Scope:** in-game UI to carve caves while playing.

1. Add input binding (e.g. `Ctrl + click` or `scroll-wheel-ctrl`) for carve mode
2. Implement carve hit-test: cast downward ray from camera, find terrain hit point
3. Show carve preview (wireframe radius circle + surface indicator)
4. On confirm, call `CarveCave(hitPoint.X, hitPoint.Z, radius, offset, height, entranceRadius)`
   — `entranceRadius > 0` opens a vertical shaft entrance at the top
5. Right-click on existing cave to call `OpenEntrance(cx, cz, radius)` — add entrance hole
6. Support multiple carve regions (append to `caveCeiling`/`caveFloor`) and multiple entrances
7. Add carve undo (restore affected cells to zero)

**Gate:** player carves a cave with one click, walks in, sees everything correct.
Multiple caves on one terrain work independently.

### Phase 4 — Polish

1. Edge erosion at cave entrances (slope ground into ceiling)
2. Carve depth slider in UI (adjust `ceilingOffset` + `ceilingHeight` live)
3. Cave lighting — emit from cave interior for visibility
4. Cave-specific materials (dirt/stone vs ground grass/snow)
5. Carve with shape presets: tunnel (elongated), chamber (wide), shaft (vertical)
6. Export / import cave data as TOML for scene baking (optional convenience)
7. Performance profiling on large carved terrains

---

## Memory budget

| Data | Size per cell | Cave terrain (example) |
|------|--------------|----------------------|
| Ground hgrid | 8 bytes | ~4 MB (2000 × 2000) |
| Ground ngrid | 12 bytes | ~4.8 MB |
| Cave ceiling | 8 bytes (sparse) | ~0.5 MB (10% coverage) |
| Cave floor | 8 bytes (sparse) | ~0.5 MB |
| Coarse min/max | ~8 bytes each | unchanged |

Total overhead per cell: **16 bytes** (ceiling + floor), but sparse — only cells
with caves consume memory. For a 1% cave coverage on a 2 km terrain: ~10 MB extra.

---

## Test scenes (proposed)

| Scene | Exercises |
|-------|-----------|
| `scenes/tests/cave-simple.toml` | Single spherical cave, carve + walk-in from top |
| `scenes/tests/cave-entrance-shaft.toml` | Vertical entrance: well-like hole in ground, walk down into cave |
| `scenes/tests/cave-tunnel.toml` | Horizontal entrance: side-opening tunnel at ground level |
| `scenes/tests/cave-multi.toml` | Two separate caves on one terrain, each with entrance |
| `scenes/tests/cave-shadows.toml` | Shadows inside cave (ceiling occlusion) |
| `scenes/tests/cave-npc.toml` | Spider walking in/out of cave through entrance |

---

## Risks and mitigations

| Risk | Mitigation |
|------|-----------|
| **Ray misses cave ceiling/floor** | Ensure `marchFine` step size is small enough at cave entrances; test with narrow tunnels |
| **Shadow leaks from cave sides** | Horizontal shadow rays need to hit both ceiling and floor; handle with bidirectional search |
| **Cave ground-plane crossover** | Carved cave ceiling < ground height; validate `ceil < ground` before carving |
| **Visual seams at cave boundaries** | Smooth falloff on carve; edge erosion; avoid sharp transitions between cave and non-cave cells |
| **Performance on large carved areas** | Sparse storage (only non-zero cells); reuse coarse grid mip for skip; per-pixel cost increases only inside caves |
| **Bilinear interpolation at cave edges** | Interpolate cave ceiling/floor the same way as ground; ensure cells near edge have correct values |

---

## Relationship to other plans

| Plan | Relationship |
|------|-------------|
| `hybrid-terrain-perf.md` | Cave data lives alongside the coarse/fine height grids; carving invalidates both |
| `large-maps.md` | Caves are local edits within existing terrain; no streaming concerns |
| `webgpu-port.md` | Cave surfaces must be in the final parity-gated set |
| `portals.md` | Cave interiors could use portals to connect to other caves/rooms |
| `bvh-acceleration.md` | Cave surfaces are heightfields, not BVH — no BVH interaction |

---

## Definition of done (v1)

- [ ] `CarveCave` API carves a spherical cave into existing terrain with an entrance,
      preview renders ceiling and floor correctly from all angles
- [ ] Player walks through a cave entrance, interior rendering shows both ceiling
      and floor with correct normals and albedo
- [ ] Shadow rays from inside the cave occlude off ceiling (upward) and floor
      (downward) correctly
- [ ] Multiple caves on one terrain coexist without visual artifacts
- [ ] Carved terrain looks correct in `island.toml` preview orbit (front, side, top,
      and inside-cave views)
- [ ] Input-based carving: click to place cave, parameters adjustable in real-time
