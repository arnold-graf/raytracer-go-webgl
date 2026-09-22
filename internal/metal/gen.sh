#!/bin/sh
# Regenerates the embedded Metal library from the WGSL sources.
#
# The shader is generated, not written: link.sh produces trace_linked.wgsl,
# mslpatch splices Apple's intersector into rt_nearest and blocker_bvh_any_hit
# and emits the two intersection functions from naga's own intersect(), and
# mslbind assigns buffer and threadgroup indices. Only the .metallib is checked
# in, because building it needs naga and the Metal toolchain.
#
# Run this after changing anything under shaders/, then commit trace.metallib
# and trace.metal.json together.
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
printf 'wrote %s (%s) and %s\n' \
    "$OUT/trace.metallib" "$(du -h "$OUT/trace.metallib" | cut -f1)" "$OUT/trace.metal.json"
