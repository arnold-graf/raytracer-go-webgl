package shaders

import (
	_ "embed"
)

//go:generate sh link.sh

//go:embed trace_linked.wgsl
var linkedWGSL string
