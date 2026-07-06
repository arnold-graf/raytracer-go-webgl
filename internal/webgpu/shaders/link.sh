#!/bin/sh
# Link WESL shader modules into a single WGSL file for WebGPU.
# Requires Node (npx wesl-link) or Rust wesl-cli (cargo install wesl-cli).
set -e
cd "$(dirname "$0")"

python3 gen_imports.py

out="trace_linked.wgsl"
if command -v wesl >/dev/null 2>&1; then
	# https://github.com/webgpu-tools/wesl-rs — wesl compile links imports to flat WGSL
	wesl compile modules/trace.wesl >"$out"
else
	npx --yes wesl-link trace --baseDir modules --src '**/*.w[eg]sl' >"$out"
fi

echo "wrote $out ($(wc -l <"$out" | tr -d ' ') lines)"
