#!/bin/sh
# Link, generate MSL, capture, and report spills. See tools/spills/README.md.
set -e
cd "$(dirname "$0")/../.."
OUT=${1:-/tmp/spills}
mkdir -p "$OUT"
NAGA=${NAGA:-$HOME/.cargo/bin/naga}
sh internal/webgpu/shaders/link.sh >/dev/null 2>&1
"$NAGA" --metal-version 3.0 internal/webgpu/shaders/trace_linked.wgsl "$OUT/t.metal" >/dev/null 2>&1
go run ./tools/metal-rt/mslbind "$OUT/t.metal" "$OUT/bound.metal" >/dev/null
[ -x "$OUT/capture" ] || swiftc -O tools/metal-rt/capture.swift -o "$OUT/capture" 2>/dev/null
rm -rf "$OUT/trace.gputrace"
MTL_CAPTURE_ENABLED=1 "$OUT/capture" "$OUT/bound.metal" "$OUT/trace.gputrace" >/dev/null
go run ./tools/spills "$OUT/trace.gputrace" "$OUT/bound.metal"
