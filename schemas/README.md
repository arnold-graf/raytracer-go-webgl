# Scene TOML JSON Schemas

Human-editable scene files (`scenes/**/*.toml`) and player configs are described here as [JSON Schema](https://json-schema.org/) documents. TOML maps cleanly to JSON for validation — see [Schema Validation for TOML](https://json-schema-everywhere.github.io/toml).

**Keep schemas in sync with the loader.** When you add or change TOML tables or fields in `internal/sceneio/toml.go` or `internal/sceneio/player.go`, update the matching schema in this directory.

| Schema | TOML files | Go source |
|--------|------------|-----------|
| [`scene.schema.json`](scene.schema.json) | Scenes and object libraries (`scenes/`) | `internal/sceneio/toml.go` |
| [`player.schema.json`](player.schema.json) | Player movement config | `internal/sceneio/player.go` |

## IDE support (VS Code / Cursor)

Install [**Tombi**](https://marketplace.visualstudio.com/items?itemName=tombi-toml.tombi) (`tombi-toml.tombi`). Cursor prompts to install recommended extensions from `.vscode/extensions.json` when you open the repo.

This repo wires schemas via:

- `tombi.toml` — `[[schemas]]` mappings for scene and player TOML files (primary)
- `taplo.toml` — same associations for Taplo CLI (`taplo check`)
- `.vscode/settings.json` — Tombi as the default TOML formatter

**Glob note:** `scenes/*.toml` (files directly in `scenes/`) needs its own pattern; `scenes/**/*.toml` alone does **not** match `scenes/npc-test.toml`.

After installing, open a scene file: you should get diagnostics (squiggles), hover docs from schema `description` fields, and completion on table keys.

**Per-file override** (optional): add a header directive at the top of a TOML file:

```toml
#:schema ../schemas/scene.schema.json
```

**Even Better TOML / Taplo** (alternative): configure `evenBetterToml.schema.associations` or use `taplo.toml`. Taplo only supports [JSON Schema Draft 4](https://taplo.tamasfe.dev/configuration/developing-schemas.html); disable the Schema Store catalog (`evenBetterToml.schema.repositoryEnabled: false`) so catalog schemas do not override repo associations.

**Tombi style lints:** `tombi.toml` disables `tables-out-of-order` and `dotted-keys-out-of-order` so scene files can group primitives however you like (e.g. interleaving `[[box]]` and `[[cylinder]]`).

**Tombi strict mode:** Primitives use `allOf` to compose geometry fields with `primitive_transform`, `primitive_surface`, and optional `interact_props`. Tombi validates each `allOf` branch separately, so partial subschemas set `"additionalProperties": true` to avoid false positives on valid keys like `material` or `pos_z`. The root schema still uses `"additionalProperties": false` to reject unknown top-level tables.

IDE validation is best-effort; the Go validator below is the authoritative check.

## Validating a file

TOML maps to JSON for validation — see [Schema Validation for TOML](https://json-schema-everywhere.github.io/toml). The schemas declare **Draft 4** for IDE (Even Better TOML) compatibility; the Go validator accepts them as well.

**Quick check** (Go, from repo root):

```bash
go run github.com/BurntSushi/toml/cmd/tomlv@latest -json scenes/outdoors-night-villa.toml \
  | go run ./schemas/cmd/validate schemas/scene.schema.json
```

Or with Python:

```bash
pip install jsonschema
tomlv -json scenes/outdoors-night-villa.toml | python -c "
import json, sys, jsonschema
schema = json.load(open('schemas/scene.schema.json'))
data = json.load(sys.stdin)
jsonschema.Draft202012Validator(schema).validate(data)
print('valid')
"
```

[Polyglottal JSON Schema Validator (pajv)](https://github.com/ota-meshi/polyglottal-json-schema-validator) can validate TOML directly when using a 2020-12-capable release:

```bash
npx pajv validate -s schemas/scene.schema.json -d scenes/outdoors-night-villa.toml
npx pajv validate -s schemas/player.schema.json -d player.toml
```

## Authoring notes

- **Float literals:** Prefer `0.0` over `0` for `vec3` arrays (`[x, y, z]`). The Go decoder uses fixed `[3]float64` arrays; integer literals in vectors can fail to decode.
- **Table names:** Primitives use array-of-tables (`[[box]]`, `[[sphere]]`, …). Singleton config uses `[camera]`, `[environment]`. Nested terrain and box tables use dotted names (`[[terrain.feature]]`, `[[terrain.pad]]`, `[[box.hole]]`). Interaction fields (`hint`, `on_use`, `use_range`) are optional flat keys on `[[box]]`, `[[door]]`, and `[[document]]`.
- **Templates:** Object files may contain Go `text/template` actions (`{{ }}`). Validation runs on the rendered result when templates are used; static files validate as-is.
- **Runtime textures:** `capture_forward`, `capture_left`, etc. are assigned by the portal system at runtime and are listed in the schema for completeness, not for ordinary scene authoring.
