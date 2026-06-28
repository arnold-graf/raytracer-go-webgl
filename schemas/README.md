# Scene TOML JSON Schemas

Human-editable scene files (`scenes/**/*.toml`) and player configs are described here as [JSON Schema](https://json-schema.org/) documents. TOML maps cleanly to JSON for validation — see [Schema Validation for TOML](https://json-schema-everywhere.github.io/toml).

**Keep schemas in sync with the loader.** When you add or change TOML tables or fields in `internal/sceneio/toml.go` or `internal/sceneio/player.go`, update the matching schema in this directory.

| Schema | TOML files | Go source |
|--------|------------|-----------|
| [`scene.schema.json`](scene.schema.json) | Scenes and object libraries (`scenes/`) | `internal/sceneio/toml.go` |
| [`player.schema.json`](player.schema.json) | Player movement config | `internal/sceneio/player.go` |

## IDE support (VS Code / Cursor)

Install the [**Even Better TOML**](https://marketplace.visualstudio.com/items?itemName=tamasfe.even-better-toml) extension (`tamasfe.even-better-toml`). Cursor prompts to install recommended extensions from `.vscode/extensions.json` when you open the repo.

This repo wires schemas via:

- `.vscode/settings.json` — `evenBetterToml.schema.associations` for `scenes/*.toml`, `scenes/**/*.toml`, and `player.toml`
- `taplo.toml` — same associations for Taplo CLI and the extension’s language server

**Glob note:** `scenes/*.toml` (files directly in `scenes/`) needs its own pattern; `scenes/**/*.toml` alone does **not** match `scenes/npc-test.toml`.

After installing, open a scene file: you should get diagnostics (squiggles), hover docs from schema `description` fields, and completion on table keys.

**Per-file override** (optional): add a header directive at the top of a TOML file ([Taplo directives](https://taplo.tamasfe.dev/configuration/directives.html)):

```toml
#:schema ../schemas/scene.schema.json
```

**Alternative:** [Tombi](https://open-vsx.org/extension/tombi-toml/tombi) is a newer TOML language server for Cursor. Configure it with `tombi.toml` and `[[schemas]]` entries instead of `taplo.toml`.

**Taplo / Even Better TOML** only supports [JSON Schema Draft 4](https://taplo.tamasfe.dev/configuration/developing-schemas.html). The scene schema uses `definitions` (not `$defs`) and Draft-4-style `"exclusiveMinimum": true` (boolean). Disable the Schema Store catalog in `.vscode/settings.json` (`evenBetterToml.schema.repositoryEnabled: false`) so random catalog schemas do not override repo associations.

IDE validation is best-effort; the Go validator above is the authoritative check.

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
- **Table names:** Primitives use array-of-tables (`[[box]]`, `[[sphere]]`, …). Singleton config uses `[camera]`, `[environment]`, `[interact]`. Nested terrain and box tables use dotted names (`[[terrain.feature]]`, `[[terrain.pad]]`, `[[box.hole]]`).
- **Templates:** Object files may contain Go `text/template` actions (`{{ }}`). Validation runs on the rendered result when templates are used; static files validate as-is.
- **Runtime textures:** `capture_forward`, `capture_left`, etc. are assigned by the portal system at runtime and are listed in the schema for completeness, not for ordinary scene authoring.
