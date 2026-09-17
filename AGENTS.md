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

### WebGPU

Don’t use reserved words for variable or function names. reserved :
NULL, Self , abstract, active, alignas, alignof, as, asm, asm_fragment, async, attribute, auto, await, become, cast, catch, class, co_await, co_return, co_yield, coherent, column_major, common, compile, compile_fragment, concept, const_cast, consteval, constexpr, constinit, crate, debugger, decltype, delete, demote, demote_to_helper, do, dynamic_cast, enum, explicit, export, extends, extern, external, fallthrough, filter, final, finally, friend, from, fxgroup, get, goto, groupshared, highp, impl, implements, import, inline, instanceof, interface, layout, lowp, macro, macro_rules, match, mediump, meta, mod, module, move, mut, mutable, namespace, new, nil, noexcept, noinline, nointerpolation, non_coherent, noncoherent, noperspective, null, nullptr, of, operator, package, packoffset, partition, pass, patch, pixelfragment, precise, precision, premerge, priv, protected, pub, public, readonly, ref, regardless, register, reinterpret_cast, require, resource, restrict, self, set, shared, sizeof, smooth, snorm, static, static_assert, static_cast, std, subroutine, super, target, template, this, thread_local, throw, trait, try, type, typedef, typeid, typename, typeof, union, unless, unorm, unsafe, unsized, use, using, varying, virtual, volatile, wgsl, where, with, writeonly, yield

### Scratch files and scripts

Use the local tmp folder inside this repo, not a root folder like /tmp or
/private/tmp. We’re using zsh, and it doesn't word-split unquoted variables, so
keep that in mind when writing shell scripts or commands.

### Tests

Tests should never assert on properties of authored art (especially tomls). If
models are needed in a test, they should be created specifically for the
purpose. Tests should not fail because authored art changes.

### Performance Measurements

The machine’s performance can vary based on thermals. Always do interleaved
tests when comparing performance for a GPU code change.

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
