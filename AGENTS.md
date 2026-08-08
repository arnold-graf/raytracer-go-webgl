# Agent notes

## Scene schema

When you add or change TOML tables or fields in `internal/sceneio/toml.go` (or
other scene loaders), **always update** the matching JSON Schema in
`schemas/scene.schema.json` (and `schemas/player.schema.json` for player
config). IDE diagnostics and `schemas/cmd/validate` depend on it — if the
schema lags the parser, authors see false squiggles or miss real errors.

## Visual verification

When creating or editing scene objects (`scenes/objects/*.toml`, included props,
furniture, etc.), **always verify the result visually** with the preview command
before considering the work done.

```bash
go run ./cmd/preview -scene scenes/preview/island.toml -o tmp/island
```

Preview auto-centers the subject and writes twelve orbit screenshots
(`<name>-00.png` … `<name>-11.png`). Add or reuse a small preview scene under
`scenes/preview/` that includes the object on a simple floor with basic lighting
if one does not exist yet.

Useful flags for inspecting characters and small props:

```bash
# Single front view, zoomed in
go run ./cmd/preview -scene scenes/npc-spider-test.toml -view front -zoom 2 -views 1 -o tmp/spider

# Low side angle
go run ./cmd/preview -scene scenes/npc-spider-test.toml -view low -zoom 1.5 -views 1 -o tmp/spider

# Manual camera (overrides auto orbit)
go run ./cmd/preview -scene scenes/npc-spider-test.toml -cam 0,0.5,6 -yaw 180 -pitch -5 -views 1 -o tmp/spider
```

`-view` accepts `front|back|left|right|side|top|low`. `-zoom` > 1 moves the
camera closer. `-elev` sets orbit ring elevation in degrees (default 25).

Do not rely on geometry math or unit tests alone for object authoring — run
preview and inspect the renders (especially front, side, and top views).

## When Terminal Tool Calls fail

When using the terminal tool in zed.dev, ALWAYS include the cd parameter with the working directory, on every call, even for commands that don't depend on directory.
