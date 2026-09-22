package shaders

import (
	_ "embed"
)

//go:generate sh link.sh

//go:embed trace_linked.wgsl
var linkedWGSL string

// LinkedWGSL returns the linked megakernel source. The Metal backend reads its
// binding declarations from here so the two backends cannot disagree about
// which resource sits at which binding.
func LinkedWGSL() string { return linkedWGSL }
