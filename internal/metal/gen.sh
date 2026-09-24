#!/bin/sh
# Regenerates the embedded Metal library from the WGSL sources.
#
# The shader is generated, not written: link.sh produces trace_linked.wgsl,
# mslpatch splices Apple's intersector into rt_nearest and blocker_bvh_any_hit
# and emits the two intersection functions from naga's own intersect(), and
# mslbind assigns buffer and threadgroup indices. Only the .metallib is checked
# in, because building it needs naga and the Metal toolchain.
#
# Run this after changing anything under shaders/, then commit trace.metallib,
# trace.metal.json and trace.sha256 together.
#
# Nothing runs this for you. The WebGPU path relinks itself (shaders/resolve.go
# reruns link.sh when a module is newer), but the Metal library is embedded in
# the binary, so editing a .wesl and running `go run .` -- which defaults to
# -backend auto, which picks Metal -- silently renders the *old* shader. The
# stamp this writes turns that into a startup warning.
set -eu
cd "$(dirname "$0")/../.."
OUT=internal/metal
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
NAGA=${NAGA:-$(command -v naga || echo "$HOME/.cargo/bin/naga")}

sh internal/webgpu/shaders/link.sh >/dev/null 2>&1
go run ./tools/metal-rt/mslpatch internal/webgpu/shaders/trace_linked.wgsl "$TMP/rt.metal" >/dev/null
go run ./tools/metal-rt/mslbind "$TMP/rt.metal" "$TMP/bound.metal" >/dev/null
xcrun -sdk macosx metal -std=metal3.0 -w -c "$TMP/bound.metal" -o "$TMP/bound.air"
xcrun -sdk macosx metallib "$TMP/bound.air" -o "$OUT/trace.metallib"
cp "$TMP/bound.metal.json" "$OUT/trace.metal.json"
# Stamp which modules/*.wesl this library was built from, so a stale metallib is
# a startup warning instead of edits that silently do nothing. Mirrors the
# digest link.sh puts in trace_linked.wgsl (shaders.ModulesSHA256).
( cd internal/webgpu/shaders && for f in modules/*.wesl; do printf '%s\n' "$f"; cat "$f"; done ) \
    | shasum -a 256 | cut -d' ' -f1 > "$OUT/trace.sha256"
printf 'wrote %s (%s), %s and %s\n' \
    "$OUT/trace.metallib" "$(du -h "$OUT/trace.metallib" | cut -f1)" \
    "$OUT/trace.metal.json" "$OUT/trace.sha256"
