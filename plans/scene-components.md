# Plan: Go Scene Components (React-like authoring)

## Status: PROPOSED

## Goal

Add a **typed, code-first authoring layer** for scene composition: function
components that return primitive/include trees, with **local and global reactive
state**, **higher-order placement wrappers**, and **keyed reconciliation** so
hot reload and runtime updates patch transforms and subtrees instead of
rebuilding the whole world.

TOML stays the format for **leaf assets** (machined geometry, parameterized
objects like `pine-tree.toml`). Go becomes the format for **layouts,
assemblies, and behaviour** (server room, desk with props, cupboard with doors).

Same non-negotiables as other plans: emit the same `*scene.Scene` the engine
already uses; `door.Manager`, physics, instancing, and WebGPU must not fork.
Static TOML scenes must behave exactly as today when no Go components are used.

---

## Why now

Recent authoring pain (rotated desk assemblies, `transform_origin`, sibling
`[[include]]` frames) is really **composition + identity + state**:

| Problem today | Component model |
| ------------- | --------------- |
| Six sibling includes on a rotated desk | One `Group` / `Placed` shared frame |
| `text/template` string math | Typed `Props` structs |
| Door spec + panels + interact scattered in TOML | Colocated `DoubleDoor` component |
| Hot reload reshuffles primitive indices | Stable keys → patch or rebuild subtree |
| Quest / power flags need a second system | `UseGlobal` signals |

`transform_origin` and `PlacementTransform` are the right low-level foundation;
this plan adds the **authoring tree** on top.

---

## What already exists

| Piece | Location | Role today |
| ----- | -------- | ---------- |
| Scene merge | `internal/sceneio` | TOML → flat `*scene.Scene` |
| Placement | `scene.PlacementTransform`, `transform_origin` | Include + primitive frames |
| Dependency graph | `sceneio.LoadDeps` | Hot-reload file watching |
| Hot reload | `internal/app/app.go` | Re-load scene; preserve player pose |
| Doors | `scene.DoorSpec`, `door.Manager` | Declarative spec; runtime panel animation |
| Interact | `scene.Interactable`, `app.UseHandlers` | E-key handlers (`door`, `document`, …) |
| Dynamic bodies | `scene.DynamicBody` | Door panels registered for partial updates |
| Instancing | `scene.InstancingCatalog` | BLAS templates + TLAS placements |
| Touch / generation | `scene.Touch()` | GPU cache invalidation |

Components **emit** these structures; they do not replace runtime systems.

---

## Design principles

1. **Not a virtual DOM** — fine-grained reactive patches (Solid/Svelte style), not
   full tree diff every frame.
2. **Build phase vs runtime phase** — components run at load/reload/state change;
   per-frame door animation stays in `door.Manager`.
3. **Stable keys everywhere** — reconciliation identity is `(component path, key)`,
   not slice index.
4. **Dual stack** — `.toml` and Go scenes coexist; `sceneio.Load` routes by extension
   or explicit registry.
5. **Inspectable** — component path → world bounds → transform chain for debugging.

---

## Core API (`internal/scenekit`)

### Node tree

```go
package scenekit

type Node interface {
    // Key returns a stable sibling identity within the parent.
    Key() string
    build(ctx *Ctx) *Built
}

type Built struct {
    Children []*Built
    // Emitted into scene during flatten:
    Boxes, Spheres, …     // or refs into catalog
    Doors                 []scene.DoorSpec
    Interacts             []scene.Interactable
    Placement             *scene.Transform  // for this subtree root
}
```

**Component** = named function with automatic key prefix:

```go
func Component(id string, fn func(*Ctx) Children) Node
func Scene(fn func(*Ctx) Children) Node
```

**Leaf builders** (mirror TOML primitives):

```go
func Box(key string, surf Surface, min, max vec.V) Node
func Include(key, tomlPath string, params map[string]any) Node
func Group(key string, children ...Node) Node
```

**Placement** (replaces planned `[[group]]` + `transform_origin` authoring):

```go
type Placement struct {
    At              vec.V
    RotateX, Y, Z   float64
    TransformOrigin Origin // Center | Corner | vec.V
}

func Placed(p Placement, child Node) Node
```

Uses `scene.PlacementTransform` internally.

### Context and state

```go
type Ctx struct {
    path string          // "server_room/cupboard/doors"
    store *Store
    emit  *Emitter       // accumulates DoorSpec, handlers, etc.
}

func (c *Ctx) ID(local string) string       // path + "/" + local
func (c *Ctx) UseState[T any](key string, initial T) *Signal[T]
func (c *Ctx) UseGlobal[T any](key string, default T) *Signal[T]
func (c *Ctx) OnUse(handler string, fn app.UseHandler)
func (c *Ctx) EmitDoor(spec scene.DoorSpec)
func (c *Ctx) EmitLight(key string, props LightProps) *LightBinding
```

```go
// LightProps mirrors [[light]] in TOML.
type LightProps struct {
    Pos        vec.V
    Color      vec.V   // RGB; intensity folded into Color or separate Brightness
    Brightness float64
    Range      float64
    Radius     float64
}

// LightBinding is a stable handle for runtime patches without structural rebuild.
type LightBinding struct {
    Key string
    set func(LightProps)
}
```

| API | Semantics |
| --- | --------- |
| `UseState` | Local to component instance; resets on hot reload unless persisted |
| `UseGlobal` | Shared store (`"server_room.power"`, `"quest.vault_locked"`) |
| `OnUse` | Register handler once per stable `Interactable` id; wires to `UseHandlers` |
| `EmitDoor` | Append `DoorSpec`; panels come from child `Group` geometry |
| `EmitLight` | Append `scene.Light`; returns `LightBinding` for `Set` without re-emit |

Signals record **dependencies** during build; when a signal changes, only
subtrees that read it re-build.

### Higher-order components

Functions that wrap placement or inject children — not inheritance:

```go
func OnFloor(at vec.V, yaw float64, child Node) Node {
    return Placed(Placement{
        At: at, RotateY: yaw,
        TransformOrigin: OriginCorner,
    }, child)
}

func WithInteriorLight(powerKey string, pos vec.V, child Node) Node {
    return Component("lit_wrapper", func(ctx *Ctx) Children {
        power := ctx.UseGlobal(powerKey, true)
        kids := Children{child}
        if power.Get() {
            kids = append(kids, PointLight("bulb", pos, 0.3, 4))
        }
        return kids
    })
}
```

---

## Example: cupboard with doors and use handler

Port of `objects/cupboard-double-door.toml`. Local origin = front-left-bottom;
doors swing toward **-Z** (`open_sign = -1`).

### Props

```go
type CupboardProps struct {
    Width, Height, Depth float64
    Shelves              int
    Material             string
    Albedo               vec.V
}
```

### Component

```go
func Cupboard(props CupboardProps) Node {
    return Component("cupboard", func(ctx *Ctx) Children {
        w := defaultF(props.Width, 1.6)
        h := defaultF(props.Height, 2.2)
        d := defaultF(props.Depth, 0.6)
        shelves := defaultI(props.Shelves, 3)
        side, back, top := 0.06, 0.06, 0.06
        surf := Surface{Material: props.Material, Albedo: props.Albedo,
            Reflect: 0.02, Rough: 0.04}

        carcass := Group("carcass",
            Box("left",  surf, vec.New(0, 0, 0),       vec.New(side, h, d)),
            Box("right", surf, vec.New(w-side, 0, 0),  vec.New(side, h, d)),
            Box("back",  surf, vec.New(0, 0, d-back), vec.New(w, h, back)),
            Box("top",   surf, vec.New(0, h-top, 0),  vec.New(w, top, d)),
            Shelves("shelves", surf, shelfArgs(w, h, d, side, back, top, shelves)...),
        )

        doors := DoubleDoor(DoubleDoorProps{
            ID:       ctx.ID("doors"),
            HingeL:   vec.New(side, 0, 0),
            HingeR:   vec.New(w-side, 0, 0),
            Axis:     "y",
            OpenSign: -1,
            Speed:    1.8,
            Interact: Interact{
                Center: vec.New(w/2, h/2, 0),
                Range:  2.0,
                Hint:   "press {{use_button}} to open",
            },
            LeftPanel:  DoorPanel{Width: (w - 2*side) / 2, Height: h},
            RightPanel: DoorPanel{Width: (w - 2*side) / 2, Height: h, ClosedYaw: -2},
        })

        return Children{carcass, doors}
    })
}
```

### `DoubleDoor` sub-component

```go
func DoubleDoor(props DoubleDoorProps) Node {
    return Component(props.ID, func(ctx *Ctx) Children {
        locked := ctx.UseGlobal("quest.vault_locked", false)

        ctx.OnUse("door", func(uc *app.UseContext) error {
            if locked.Get() {
                return nil
            }
            return uc.Game.doors.ToggleInteract(uc.Interact, uc.Camera.Pos)
        })

        ctx.EmitDoor(scene.DoorSpec{
            ID: props.ID, Kind: "double",
            Hinge: props.HingeL, HingeRight: props.HingeR,
            Axis: props.Axis, OpenAngle: math.Pi / 2,
            Swing: "one_way", OpenSign: props.OpenSign, Speed: props.Speed,
            PanelClosedAngles: []float64{0, props.RightPanel.ClosedYaw * math.Pi / 180},
            Interact: &scene.Interactable{
                Handler: "door", DoorID: props.ID,
                Center: props.Interact.Center, Range: props.Interact.Range,
                Hint: props.Interact.Hint,
            },
        })

        return Children{
            Group("left",  doorPanelMesh("panel", props.LeftPanel)),
            Group("right", doorPanelMesh("panel", props.RightPanel)),
        }
    })
}
```

**Door rotation** is unchanged: `door.Manager` animates panel `Xform` each frame
from `DoorSpec`. The component only declares hinges, panels, and interact.

### Placement in a level

```go
func ServerRoomCupboard() Node {
    return OnFloor(vec.New(16, 0, 18.8), 0,
        Cupboard(CupboardProps{
            Width: 2, Height: 3, Depth: 0.5, Shelves: 4,
            Albedo: vec.New(0.1, 0.1, 0.2),
        }),
    )
}
```

### Example: desk lamp — conditional glow and adjustable light

A **desk anglepoise lamp** on the server-room desk: the shade mesh is always
present; the **emit sphere** and **point light** appear only when
`server_room.power` is true. Pressing E on the lamp cycles brightness and nudges
the bulb upward via an **inline handler** that writes component-local state (no
separate TOML `on_use` id).

```go
type LampProps struct {
    StemLen    float64
    BaseAt     vec.V // bulb anchor in lamp-local space
}

type BulbState struct {
    Pos        vec.V
    Brightness float64
    Range      float64
}

func DeskLamp(props LampProps) Node {
    return Component("desk_lamp", func(ctx *Ctx) Children {
        stem := defaultF(props.StemLen, 0.84)
        base := props.BaseAt
        if base == (vec.V{}) {
            base = vec.New(0, stem, 0)
        }

        // --- global: whole room power (quest, breaker, etc.) ---
        power := ctx.UseGlobal("server_room.power", true)

        // --- local: bulb pose/intensity owned by this component ---
        bulb := ctx.UseState("bulb", BulbState{
            Pos:        base,
            Brightness: 0.06,
            Range:      5.0,
        })

        // Static mesh (always rendered)
        mesh := Group("mesh",
            Include("objects/desk-anglepoise-lamp.toml", map[string]any{
                "stem_len": stem, "brightness": 0, // geometry only; light is separate
            }),
        )

        // Conditional primitives: only when power is on
        var lit Children
        if power.Get() {
            b := bulb.Get()
            glow := vec.New(1.0, 0.92, 0.75).Scale(b.Brightness * 8)

            lit = Children{
                Sphere("bulb_glow", Surface{Mat: scene.MatEmit, Albedo: glow},
                    b.Pos, 0.04),
            }

            // Light emitted from current bulb state; binding survives signal updates
            binding := ctx.EmitLight(ctx.ID("bulb"), LightProps{
                Pos:        b.Pos,
                Color:      vec.New(1.0, 0.92, 0.75),
                Brightness: b.Brightness,
                Range:      b.Range,
                Radius:     0.05,
            })

            // Keep binding in sync when bulb state changes (handler or hot reload)
            bulb.OnChange(func(st BulbState) {
                binding.Set(LightProps{
                    Pos:        st.Pos,
                    Color:      vec.New(1.0, 0.92, 0.75),
                    Brightness: st.Brightness,
                    Range:      st.Range,
                    Radius:     0.05,
                })
            })
        }

        // Inline use handler: adjust local light, not a global UseHandlers entry
        ctx.OnUse("lamp_adjust", func(uc *app.UseContext) error {
            if !power.Get() {
                return nil // dead circuit — hint could say "no power"
            }
            st := bulb.Get()
            switch {
            case st.Brightness < 0.04:
                st.Brightness = 0.06
            case st.Brightness < 0.08:
                st.Brightness = 0.12
            default:
                st.Brightness = 0.02
            }
            st.Pos.Y += 0.03 // raise bulb each toggle (shade tilt fantasy)
            bulb.Set(st)    // → re-build lit subtree OR patch via binding.Set
            return nil
        })

        ctx.EmitInteract(scene.Interactable{
            Handler: "lamp_adjust",
            Center:  base.Add(vec.New(0, 0.1, 0)),
            Range:   1.5,
            Hint:    "press {{use_button}} to adjust lamp",
        })

        return append(Children{mesh}, lit...)
    })
}
```

**Placement in the desk assembly** (shared parent frame — no sibling rotation pain):

```go
func ServerRoomDesk() Node {
    return Group("desk",
        OnFloor(vec.New(0, 0, 0), 20, // desk-local; parent Placed applies world pose
            Include("objects/simple-table.toml", map[string]any{
                "width": 2.0, "height": 0.9, "texture": "wood",
            }),
        ),
        Placed(Placement{
            At: vec.New(-0.38, 0.9, 0), RotateY: 35,
            TransformOrigin: OriginCenter,
        }, DeskLamp(LampProps{StemLen: 0.84})),
    )
}
```

**Reconciliation behaviour for this example:**

| Event | What updates |
| ----- | ------------ |
| `server_room.power` false → true | **Structural**: add `bulb_glow` sphere + `[[light]]` row |
| `server_room.power` true → false | **Structural**: remove glow + light |
| `bulb.Set` (brightness / pos) | **Props**: `LightBinding.Set` patches `scene.Lights[i]` in place; optional glow sphere `Xform`/radius patch |
| Hot reload `.go` file | Full rebuild; `bulb` local state resets unless serialized |

Global power might be flipped from elsewhere (a breaker switch component):

```go
func BreakerSwitch(at vec.V) Node {
    return Component("breaker", func(ctx *Ctx) Children {
        power := ctx.UseGlobal("server_room.power", true)
        ctx.OnUse("toggle_power", func(uc *app.UseContext) error {
            power.Set(!power.Get()) // all DeskLamp instances re-build lit branch
            return nil
        })
        return Children{
            Include("objects/exit-button.toml", nil), // stand-in mesh
            // interact omitted for brevity
        }
    })
}
```

---

## Reconciliation

On hot reload or signal change, compare previous and next descriptor trees by
**full path key** (`server_room/cupboard/carcass/left`).

| Change | Action |
| ------ | ------ |
| `Unchanged` | Skip |
| `TransformOnly` | Patch `Xform` on existing primitives / TLAS row |
| `Props` (material) | Patch surface fields; `Touch()` |
| `Structural` | Rebuild subtree; remap `DoorPanelGeom` ranges if door panels move |
| `Removed` | Delete primitives; compact or tombstone indices |

**Instancing:** static subtrees (pine BLAS) unchanged; patch TLAS `InstancePlacement`
rows only.

**Doors:** panel box index ranges (`DoorPanelGeom`) stored per stable door id;
structural rebuild must call `door.Manager.Rebind(sc, id)` or full reinstantiate.

Pseudocode:

```go
func Reconcile(prev, next *Tree, sc *scene.Scene) {
    for _, d := range diff.ByKey(prev, next) {
        switch d.Kind {
        case Unchanged:
        case TransformOnly:
            patchXform(sc, d)
        case Structural:
            rebuildSubtree(sc, d)
        }
    }
    if anyChanged {
        sc.Touch()
    }
}
```

No per-frame reconciliation — only on reload, signal flip, or explicit
`store.Set`.

---

## Hot reload

### Phase 1 (simple)

```
scenes/
  server_room.go      // registers ServerRoom via init()
  server_room_test.go
```

- `//go:build scenegen` tag on `scenes/` package.
- Watcher polls `.go` mtimes under `scenes/` **and** `LoadDeps` paths for TOML
  assets referenced via `Include()`.
- On change: `go build -tags scenegen -o /tmp/scenegen ./scenes/...` → load
  registered root → `Reconcile` into live `*scene.Scene` → preserve player
  (same as today).
- Toast: `"scene reloaded (components)"`.

### Phase 2

- Dependency graph from static analysis or runtime `Include()` registration list.
- Sub-second reload via incremental `go build` cache.

### Registry

```go
// scenes/register.go
func init() {
    scenekit.Register("office-sunset/server-room", ServerRoom)
}
```

CLI / `index.toml` equivalent:

```toml
# scenes/office-sunset/index.toml (unchanged)
# OR new:
# [scene]
# component = "office-sunset/server-room"
```

---

## Flattening to `*scene.Scene`

```text
Build(root, store) → descriptor tree
Flatten(tree, parentXform) → Emitter
Emitter.Apply(sc) → append primitives, DoorSpecs, Interactables
door.Manager.Instantiate(sc)
npc.Manager.Instantiate(sc)
sc.FinalizeInstancing()
```

`Include("objects/foo.toml")` calls existing `sceneio.load` for the asset;
returns a keyed subtree with bounds center resolved the same as today.

---

## TOML vs Go split

| Author in | Examples |
| --------- | -------- |
| **TOML** | `pine-tree.toml`, `staircase.toml`, door panel leaves, materials |
| **Go** | `server-room`, `server-room-desk`, `cupboard-double-door`, layouts |
| **Either** | Top-level entry (migrate gradually) |

Optional: `go generate` emits TOML from a component for diffing / sharing — not
required for v1.

---

## Implementation phases

### Phase 1 — `scenekit` skeleton

- `Node`, `Component`, `Group`, `Placed`, `Box`, `Include`
- `Ctx`, `Emitter`, flatten to `*scene.Scene`
- Unit tests: built scene matches `Load("cupboard-double-door.toml")` bounds

### Phase 2 — Cupboard vertical slice

- Port `cupboard-double-door` to Go component
- `DoubleDoor`, `EmitDoor`, `OnUse`
- `door_test.go` parity (collision, swing direction, ghost)

### Phase 3 — Reconciliation

- Keyed `Built` tree cache
- `Reconcile` for static + transform-only patches
- Hot reload via `scenegen` build tag

### Phase 4 — Signals

- `Store`, `UseGlobal`, `UseState`
- Subtree invalidation on `Set`
- Example: `WithInteriorLight` / `vault_locked`

### Phase 5 — Server room desk

- `OnFloor`, nested `Group`, shared rotation
- Replace `server-room-desk.toml` as proof of ergonomics win

### Phase 6 — Inspector (optional)

- Log component path → AABB → transform chain on click / debug overlay

---

## Tests

| Test | Gate |
| ---- | ---- |
| `Cupboard` Go build ≡ TOML load (bounds, box count, door spec) | Unit |
| Door swing / block / ghost unchanged | `internal/door` |
| Reconcile: change only `RotateY` → same box count, patched `Xform` | Unit |
| Reconcile: add shelf → structural rebuild; door panels rebind | Unit |
| Hot reload `.go` edit → scene updates; player pose preserved | Integration |
| `UseGlobal` flip → light appears; BLAS untouched | Unit |
| `DeskLamp` power off → no emit sphere/light; on → both present | Unit |
| Inline `bulb.Set` → `LightBinding` patches pos/brightness without box rebuild | Unit |
| Static TOML scene load path unchanged | Regression |

---

## Open questions

1. **Panel geometry** — inline `Box` in Go vs keep `cupboard-door-panel-left.toml`?
   (Recommend: TOML panels for v1, inline optional.)
2. **Index stability** — tombstone vs full scene swap on structural rebuild?
3. **NPC / document components** — same `Node` API or separate?
4. **Editor audience** — Go-only forever, or eventually a visual tool emitting keys?
5. **Wasm / plugin** — is `scenegen` binary enough for modding?

---

## Related plans

- `plans/scene-transforms.md` — `PlacementTransform`, `transform_origin` (done);
  `Placed()` is the component-facing wrapper.
- `plans/dynamic-objects.md` — movers patch `Xform`; components declare initial
  pose, movers animate afterward.
- `plans/dynamic-objects.md` / `door.Manager` — runtime animation stays out of
  component build.
- `plans/large-maps.md` — instancing + reconciliation patch TLAS rows.
- `plans/npc_system_phase_1.md` — NPC spawns as `EmitNPC` later.

---

## Non-goals (v1)

- JSX / string templates in Go
- Per-frame component re-execution
- Replacing all TOML scenes
- Visual scene editor
- Networked state sync
