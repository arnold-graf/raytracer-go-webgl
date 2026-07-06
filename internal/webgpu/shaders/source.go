// Package shaders provides the trace compute shader, linked from WESL modules.
package shaders

import _ "embed"

//go:generate sh link.sh

//go:embed trace_linked.wgsl
var linkedWGSL string

// Source returns the linked trace compute shader WGSL passed to createShaderModule.
func Source() string {
	return linkedWGSL
}
