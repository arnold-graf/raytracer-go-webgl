# Plan: Valid-TOML object parameters (v2)

## Status: IMPLEMENTED

## Naming

Props and consts use plain TOML keys (`width`, `half`, `albedo`) — no `$` prefix.
Loop variables (`i`, `n`) and `# let` bindings are also bare names.

## Decisions (locked)

| # | Decision |
| - | -------- |
| 1 | Index helpers (`leg_x`, `leg_z`, `ring_lerp`, `floor`) in v1 |
| 2 | `# let` in loops included in v1 |
| 3 | Table name `[props]` (and `[const]`) |
| 4 | Parameterized files forbid `{{`; legacy `text/template` removed |
| 5 | Plain prop/const names (no `$` prefix) |

## Problem

Parameterized object files (`scenes/objects/*.toml`) use Go `text/template` (`{{ … }}`).
That works, but every parameterized file is **invalid TOML**:

- editors can't parse or schema-check the file as written
- `{{range}}` / `{{if}}` break table structure
- logic is stringly typed (`orVec3`, `seq`, nested `{{if lt $i 2}}…`)
- surprises from Go template scoping and whitespace trimming

There are **14** templated object files today. They share a small set of patterns:
props with defaults, derived geometry constants, repeated primitives (stairs,
shelves, grid beams, chair legs), and occasional optional keys (`texture`).

## Goal

Replace `text/template` in **object files only** with a preprocessor that:

1. Leaves files **valid TOML** (parseable by any TOML tool before expansion)
2. Uses **`[props]`** for overridable parameters (merged with `[[include]] params`)
3. Uses **`[const]`** for derived values (simple expressions)
4. Uses **single-quoted strings** for variable references and calculations at use sites
5. Uses **comment directives** for loops and optional blocks
6. Names all props and consts with a **`$` prefix** everywhere they appear

Scene files (`server-room-1.toml`, etc.) stay plain TOML. Only leaf object files
opt in.

---

## Naming: `$` prefix

Props and consts are always written `$name` — in definitions, references,
expressions, comment directives, and `[[include]] params`.

| Context | Example |
| ------- | ------- |
| `[props]` key | `"$width" = 1.6` |
| `[const]` key | `"$half" = '$width / 2'` |
| Expression / use site | `pos_x = '-$half'` |
| `# if` / `# for` bound | `# if $texture` · `# for i in range($steps)` |
| `[[include]] params` | `params = { "$width" = 1.8 }` |

**TOML keys:** bare keys cannot contain `$` (TOML spec). Prop and const names are
quoted: `"$width"`, `"$albedo"`. The `$` is part of the name, not decoration.

**Loop variables** (`i`, `j`, …) and **`# let` bindings** (`n`, …) are bare
names — they are not props/consts and do not take `$`.

**Geometry fields** (`pos_x`, `width`, `material`, …) are never prefixed with `$`.
A single-quoted `'$width'` refers to the prop; a bare `width` on a `[[box]]` is
the primitive's extent field.

**v2 files must not contain `{{`.** A load error if `{{` appears anywhere in a
file that uses `[props]`, `[const]`, or comment directives. Unmigrated files keep
the legacy `text/template` path until converted; once migrated, `text/template`
is removed for that file.

---

## Example: `simple-table.toml` today → v2

### Today (invalid TOML)

```toml
{{$width := or .width 1.6}}
{{$half := div $width 2}}
…
pos_x = {{neg $half}}
{{range $i := seq 4}}
[[cylinder]]
pos_x = {{if lt $i 2}}…{{end}}
{{end}}
```

### Proposed (valid TOML)

```toml
[props]
"$width" = 1.6
"$depth" = '$width'
"$height" = 0.9
"$top" = 0.06
"$leg_radius" = 0.05
"$inset" = 0.1
"$texture" = "wood"
"$albedo" = [0.55, 0.42, 0.28]
"$reflect" = 0.01
"$rough" = 0.02

[const]
"$half" = '$width / 2'
"$half_d" = '$depth / 2'
"$off" = '$half - $inset'
"$off_z" = '$half_d - $inset'
"$leg_h" = '$height - $top'
"$leg_d" = '$leg_radius * 2'

[[box]] # tabletop
pos_x = '-$half'
pos_y = '$leg_h'
pos_z = '-$half_d'
width = '$width'
height = '$top'
depth = '$depth'
material = "diffuse"
albedo = '$albedo'
texture = '$texture'
reflect = '$reflect'
rough = '$rough'

# for i in range(4)
[[cylinder]] # leg
pos_x = 'leg_x(i, $off, $leg_radius)'
pos_y = 0.0
pos_z = 'leg_z(i, $off_z, $leg_radius)'
width = '$leg_d'
height = '$leg_h'
material = "diffuse"
albedo = '$albedo'
texture = '$texture'
reflect = '$reflect'
rough = '$rough'
# endfor
```

Include override:

```toml
[[include]]
file = "objects/simple-table.toml"
params = { "$width" = 2.0, "$texture" = "marble" }
```

---

## Syntax reference

### `[props]` — parameters

Top-level table. Keys are quoted `"$name"`; values are defaults.

| TOML type | Role |
| --------- | ---- |
| number, string, bool | scalar prop |
| array | vec3 (`$albedo`), string list (`$paragraphs`), etc. |
| `'expr'` string | default computed from other props (e.g. `"$depth" = '$width'`) |

**Merge rule:** on `[[include]]`, `params` shallow-merges over `[props]` (keys
must be `"$name"`). Missing keys keep file defaults.

`[props]` is stripped before the scene decoder runs.

### `[const]` — derived values

Top-level table. Each `"$name"` is either a literal or a **single-quoted expression**
evaluated after props are merged.

```toml
[const]
"$half" = '$width / 2'
"$bulb_y" = '-($stem + $spacing * ($rings + 1) / 2)'
"$shelf_step" = '($shelf_y1 - $shelf_y0) / ($shelves + 1)'
```

Evaluation order: dependency sort within `[const]`; cycles are a load error.

`[const]` is stripped before scene decode.

### Single-quoted use sites

Anywhere a normal TOML scalar would go, a **single-quoted string** means
“evaluate this expression and substitute the result”:

```toml
pos_x = '-$half'
width = '$width'
height = '(i + 1) * $rise'       # `i` loop var; `$rise` prop
headline = '$headline'           # string prop → TOML string
albedo = '$albedo'               # vec3 prop → TOML array
```

**Literals stay literal:**

```toml
material = "diffuse"               # double-quoted: never evaluated
pos_y = 0.0                        # bare number: never evaluated
rotate_y = 72                      # bare number
```

**Rule of thumb:** single quotes = expression; double quotes / bare numbers =
fixed.

### Expression language (v1)

Minimal arithmetic DSL shared by `[props]`, `[const]`, and use sites:

| Feature | Syntax |
| ------- | ------ |
| Props / consts | `$width`, `$half`, … |
| Loop / let vars | `i`, `n`, … (no `$`) |
| Numbers | `1.6`, `-0.25` |
| Ops | `+`, `-`, `*`, `/`, unary `-` |
| Parens | `($a + $b) * $c` |
| Comparisons | none in v1 (use comment `# if` instead) |

**Not in v1:** string concat, user-defined functions, ternary.

### Built-in index helpers

Fixed library for common indexed geometry (four corners, star legs, etc.):

| Helper | Signature | Semantics |
| ------ | --------- | --------- |
| `leg_x` | `leg_x(i, $off, $radius)` | X for leg index `i ∈ {0,1,2,3}` |
| `leg_z` | `leg_z(i, $off, $radius)` | Z for leg index `i ∈ {0,1,2,3}` |

Corner order matches today's `simple-table` / `office-chair` templates:
`i=0` → `(-off, -off)`, `i=1` → `(off, -off)`, `i=2` → `(-off, off)`,
`i=3` → `(off, off)` (with radius inset applied per helper).

Additional helpers can be added when a second object needs the same pattern;
prefer a small fixed set over general functions.

### Comment directives

Line comments only (`# …`). Recognized forms:

#### Loops

```toml
# for i in range($steps)           # i = 0 .. $steps-1
[[box]]
pos_x = 'i * $run'
height = '(i + 1) * $rise'
# endfor
```

```toml
# for i in range(4)                # fixed count
# for i in range($shelves)         # prop/const as bound
```

Nested loops allowed. Loop variable shadows outer names.

#### Loop-local binding (`# let`)

```toml
# for i in range($shelves)
# let n = i + 1
[[box]]
pos_y = '$shelf_y0 + $shelf_step * n'
# endfor
```

`# let` binds a bare name for the rest of that loop iteration (and nested
blocks). Expression on the right uses `$props`, consts, and outer loop vars.

#### Conditionals (optional keys)

```toml
# if $texture
texture = '$texture'
# endif
```

```toml
# if not $texture
# endif
```

Truthy: non-zero number, non-empty string, non-empty array. `"$texture" = ""`
is falsy.

---

## Processing pipeline

```
raw object .toml (valid TOML + comment directives; no {{)
        │
        ▼
 1. Comment preprocessor
    - expand # for / # let / # if regions (textual duplication)
    - produces expanded TOML text (still valid)
        │
        ▼
 2. TOML decode (standard library)
    - read [props], [const], geometry tables
        │
        ▼
 3. Merge [[include]] params into props (keys "$name")
        │
        ▼
 4. Evaluate [const] DAG (expressions in single quotes)
        │
        ▼
 5. Walk geometry tree; evaluate single-quoted scalars
    - '$width' → 1.6
    - '-$half' → -0.8
    - '$albedo' → [0.55, 0.42, 0.28]
        │
        ▼
 6. Strip [props] / [const]; encode to TOML bytes
        │
        ▼
 existing sceneio.load → scene.Scene
```

**Detection:**

| File contents | Path |
| ------------- | ---- |
| `[props]`, `[const]`, or `# for` / `# if` | v2 expander (reject `{{`) |
| `{{` only | legacy `text/template` (unmigrated) |
| neither | pass through verbatim |

---

## What stays the same

| Item | Notes |
| ---- | ----- |
| `[[include]]` | unchanged structure |
| `transform_origin`, rotations | plain TOML |
| Scene-level files | never templated |
| Instancing (`instance = true`) | expanded before BLAS registration |
| Hot reload | expand on each load; cache key = file + params hash |

**What changes for callers:** include `params` keys gain `$` prefix and quoting:

```toml
# before
params = { width = 1.8, albedo = [0.1, 0.2, 0.3] }

# after
params = { "$width" = 1.8, "$albedo" = [0.1, 0.2, 0.3] }
```

All scene files that pass params to migrated objects must be updated in the
same migration pass (or the loader accepts both key forms temporarily with a
deprecation warning — prefer one-shot update).

---

## Migration map (current → v2)

| Go template | v2 |
| ----------- | -- |
| `{{$w := or .width 1.6}}` | `"$width" = 1.6` in `[props]` |
| `{{$d := or .depth $width}}` | `"$depth" = '$width'` in `[props]` |
| `{{$half := div $width 2}}` | `"$half" = '$width / 2'` in `[const]` |
| `{{neg $half}}` | `'-$half'` |
| `{{mul $i $run}}` | `'i * $run'` |
| `{{range $i := seq $steps}}` | `# for i in range($steps)` … `# endfor` |
| `{{if $texture}}texture = …{{end}}` | `# if $texture` … `# endif` |
| `orVec3 .albedo r g b` | `"$albedo" = [r,g,b]` in `[props]` |
| `{{printf "%q" .}}` in arrays | `"$paragraphs" = ["line1", "line2"]` |
| `{{$label := or .label "OBSOLETE"}}` | `"$label" = "OBSOLETE"` |
| `params = { width = 1.8 }` | `params = { "$width" = 1.8 }` |

**14 object files** to migrate: `simple-table`, `staircase`, `grid`, `office-chair`,
`desk-anglepoise-lamp`, `art-deco-ring-lamp`, `otto-wagner-sphere-lamp`,
`campfire-light`, `cupboard-*`, `workstation`, `floppy-disk-3.5`, `simple-chair`.

**Scene / preview files** that pass `params` must be updated for `$` keys
(`npc-test.toml`, `art-nouveau-villa.toml`, `preview/*.toml`, etc.).

Hardest cases:

- **grid.toml** — nested `# for` over `$v_count` × `$h_count`
- **cupboard shelves** — `# let n = i + 1` + computed `y`
- **simple-table / office-chair legs** — `leg_x(i, …)` / `leg_z(i, …)`
- **workstation paragraphs** — `"$paragraphs"` as prop array (valid TOML)

---

## Validation & tooling

| Stage | Validator |
| ----- | --------- |
| Source file | Standard TOML parser; reject `{{` in v2 files |
| After expand | Existing `schemas/scene.schema.json` on output |
| CI | `expand(object, {})` then `sceneio.Load` for each object; golden primitive counts |

Editors get syntax highlighting on the whole file; `[props]` can be folded.
Comment loops are invisible to the TOML parser but visible to authors.

---

## Implementation sketch

New package: `internal/sceneparam` (or `internal/sceneio/expand`).

```
expand.go       ExpandObject(raw, params) ([]byte, error)
expr.go         parse + eval expression AST ($-prefixed identifiers)
const.go        topological sort [const]
loop.go         comment directive expander (# for, # let, # if)
subst.go        walk decoded TOML, resolve 'expr' strings
helpers.go      leg_x, leg_z index helpers
legacy.go       renderObjectTemplate for unmigrated files only
```

Tests: port `TestStaircaseSeqTemplate`, `TestSimpleTableDepthParam`,
`TestWorkstationScreenParams`, etc. to v2 syntax; add round-trip tests
(parse → expand → parse); test `{{` rejection on v2 files.

**Effort estimate:** ~2–3 days for engine + migration of 14 objects + param
call-site updates + `SCENES.README.md`.

---

## Non-goals (v1)

- Scene-level templating (layouts stay plain TOML or future Go `scenekit`)
- User-defined functions beyond index helpers
- String interpolation / formatting
- Loops over arbitrary arrays (`# for x in $corners`) — defer
- `{{` in any v2 file

---

## Decisions (locked)

| # | Decision |
| - | -------- |
| 1 | Ship index helpers (`leg_x`, `leg_z`) in v1 |
| 2 | `# let` in loops included in v1 |
| 3 | Table name `[props]` (and `[const]`) |
| 4 | v2 files forbid `{{`; legacy path only for unmigrated files |
| 5 | All props/consts use `$` prefix when defined, referenced, and in include params |

---

## Relation to Go components plan

`plans/scene-components.md` proposes Go for **layouts and assemblies**; TOML
stays for **leaf machined geometry**. This plan aligns: v2 makes leaf objects
pleasant to author and validate; complex composition moves to `scenekit` later,
not more template syntax.

---

## Next steps

1. Implement `internal/sceneparam` per pipeline above
2. Migrate `staircase.toml` as reference object + tests
3. Batch-migrate remaining 13 objects and all `params` call sites
4. Remove `text/template` from `sceneio` once migration complete
5. Update `SCENES.README.md` and object file header comments
