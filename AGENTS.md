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

Agents receive **text descriptions** of preview PNGs, not raw pixels. Dark or
one-sided lighting produces useless descriptions ("gray box in shadow"). Use a
**dedicated bright preview scene** — never verify facade detail in night-map
scenes like `outdoors-night-villa.toml`.

### Preview scene setup

Copy `scenes/preview/_template.toml` for new objects. The template uses
`sky = "clear"`, moderate ambient, and **key + fill lights** so both sides of
the subject read clearly.

```bash
cp scenes/preview/_template.toml scenes/preview/my-object.toml
# edit the [[include]] path, then:
go run ./cmd/preview -scene scenes/preview/my-object.toml -zoom 1.5 -w 1024 -h 640 -o tmp/my-object
```

Preview auto-centers the subject and writes twelve orbit screenshots
(`<name>-00.png` … `<name>-11.png`).

### Recommended commands

```bash
# Full orbit (12 angles) — best default
go run ./cmd/preview -scene scenes/preview/my-object.toml -zoom 1.5 -w 1024 -h 640 -o tmp/obj

# Named facade views (always pair -view with -views 1)
go run ./cmd/preview -scene scenes/preview/my-object.toml -view front -zoom 1.8 -w 1024 -h 640 -views 1 -o tmp/obj-front
go run ./cmd/preview -scene scenes/preview/my-object.toml -view side  -zoom 1.6 -w 1024 -h 640 -views 1 -o tmp/obj-side
go run ./cmd/preview -scene scenes/preview/my-object.toml -view low   -zoom 1.4 -w 1024 -h 640 -views 1 -o tmp/obj-low
```

`-view` accepts `front|back|left|right|side|top|low`. `-zoom` > 1 moves the
camera closer. `-elev` sets orbit ring elevation in degrees (default 25).
`-w 1024 -h 640` gives enough resolution for detail to survive image
description. Prefer auto-orbit over manual `-cam` unless you know the
coordinates.

Do not rely on geometry math or unit tests alone for object authoring — run
preview and inspect the renders (especially front, side, and top views).

## When Terminal Tool Calls fail

When using the terminal tool in zed.dev, ALWAYS include the cd parameter with the working directory, on every call, even for commands that don't depend on directory.
