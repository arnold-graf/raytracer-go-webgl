#!/bin/sh
# Runs every part of the Metal integration that exists today and reports what
# passes. Nothing here needs Xcode, a GPU capture, or the unfinished harness.
#
#   ./tools/metal-rt/verify.sh [workdir]     (default tmp/metal-rt-verify)
#
# Each check prints PASS or FAIL and the number it checked, so a regression
# shows up as a changed number rather than a silent skip.
set -u
cd "$(dirname "$0")/../.."
OUT=${1:-tmp/metal-rt-verify}
NAGA=${NAGA:-$(command -v naga || echo "$HOME/.cargo/bin/naga")}
SCENE=${SCENE:-scenes/office-sunset/index.toml}
mkdir -p "$OUT"
fails=0
ok()   { printf '  PASS  %s\n' "$1"; }
bad()  { printf '  FAIL  %s\n' "$1"; fails=$((fails+1)); }

echo
echo "1. Acceleration-structure extraction (unit tests)"
if go test ./internal/webgpu/ -run 'TestAccelPack|TestInvertXf' >"$OUT/tests.log" 2>&1; then
    ok "leaf coverage, instance numbering, transform round-trip"
else
    bad "see $OUT/tests.log"
fi

echo
echo "2. Acceleration structures over the real scene"
if go run ./tools/metal-rt/accelgen "$SCENE" "$OUT/accel.bin" >"$OUT/accel.log" 2>&1; then
    sed 's/^/      /' "$OUT/accel.log"
    # Full coverage is the property that matters: a missing leaf is geometry
    # that stops existing on the Metal path.
    g=$(awk '/^geom/{print $4}' "$OUT/accel.log")
    p=$(awk '/^prims/{print $2}' "$OUT/accel.log")
    if [ "$g" = "$p" ]; then ok "every packed primitive is in a structure ($g of $p)"
    else bad "geometry leaves $g != packed prims $p"; fi
else
    bad "accelgen failed; see $OUT/accel.log"
fi

echo
echo "3. The upload dump does not change the render"
A="$OUT/plain.rgba"; B="$OUT/dumped.rgba"
CAM="-w 1024 -h 640 -cam-x 44.9 -cam-y 201.3 -cam-z 33.1 -yaw-deg 91.3 -pitch-deg -0.52"
go run ./cmd/gpuprof -scene "$SCENE" $CAM -warmup 1 -frames 1 -ablate=false -quant 3 -dump "$A" >/dev/null 2>&1
RT_DUMP_BUFFERS="$OUT/bufs" go run ./cmd/gpuprof -scene "$SCENE" $CAM -warmup 1 -frames 1 -ablate=false -quant 3 -dump "$B" >/dev/null 2>&1
if [ -f "$A" ] && [ -f "$B" ] && cmp -s "$A" "$B"; then
    ok "bit-identical frames ($(wc -c <"$A" | tr -d ' ') bytes), $(ls "$OUT/bufs" | wc -l | tr -d ' ') bindings dumped"
else
    bad "frames differ, or a render failed"
fi

echo
echo "4. Codegen: WGSL -> MSL with the traversal spliced"
if [ ! -x "$NAGA" ] && ! command -v naga >/dev/null 2>&1; then
    bad "naga not found (set NAGA=/path/to/naga)"
else
    sh internal/webgpu/shaders/link.sh >/dev/null 2>&1
    if go run ./tools/metal-rt/mslpatch internal/webgpu/shaders/trace_linked.wgsl "$OUT/rt.metal" >"$OUT/patch.log" 2>&1; then
        ok "$(awk '{print $3, $4}' "$OUT/patch.log") generated"
    else
        bad "mslpatch failed; see $OUT/patch.log"
    fi
fi

echo
echo "5. The patched kernel compiles and links"
if [ -f "$OUT/rt.metal" ]; then
    if xcrun -sdk macosx metal -std=metal3.0 -c "$OUT/rt.metal" -o "$OUT/rt.air" 2>"$OUT/metal.log"; then
        ok "compiles at -std=metal3.0 ($(grep -c 'warning:' "$OUT/metal.log" | tr -d ' ') warnings, 0 errors)"
        if xcrun -sdk macosx metallib "$OUT/rt.air" -o "$OUT/rt.metallib" 2>>"$OUT/metal.log"; then
            have=$(strings -a "$OUT/rt.metallib" | grep -cE '^(main_|prim_isect|blocker_isect)$')
            if [ "$have" -ge 3 ]; then ok "metallib has main_, prim_isect, blocker_isect"
            else bad "metallib is missing an entry point ($have of 3)"; fi
        else
            bad "metallib failed; see $OUT/metal.log"
        fi
    else
        bad "compile failed; see $OUT/metal.log"
    fi
else
    bad "no MSL to compile (step 4 did not run)"
fi

echo
echo "6. The splice is surgical: planes, terrain and water survive it"
if [ -f "$OUT/rt.metal" ]; then
    # rt_nearest is replaced; nearest_hit must still call the three walks the
    # BVH does not cover. Losing them is the failure mode this guards.
    miss=""
    for fn in hit_terrain hit_water; do
        grep -q "$fn" "$OUT/rt.metal" || miss="$miss $fn"
    done
    grep -q 'intersector' "$OUT/rt.metal" || miss="$miss intersector"
    if [ -z "$miss" ]; then ok "intersector spliced in; terrain and water still called"
    else bad "missing:$miss"; fi
else
    bad "no MSL to inspect"
fi

echo
if [ "$fails" -eq 0 ]; then echo "all checks passed  (artifacts in $OUT)"
else echo "$fails check(s) failed  (artifacts in $OUT)"; fi
exit $fails
