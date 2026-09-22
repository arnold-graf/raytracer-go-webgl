package metal

/*
#include "metal.h"
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"regexp"
	"unsafe"

	"raytracer/internal/webgpu"
)

// The dispatch chain, in the order internal/webgpu submits it: main_, the
// reflection filter, the four separable penumbra passes and aa_classify share
// one encoder so each sees the last one's writes; aa_resolve runs in its own,
// dispatched indirectly from the task list aa_classify built.
//
// Only main_ and aa_resolve traverse, so only they need the intersection
// functions linked and an rt_handle argument buffer.
var chain = []struct {
	name     string
	tx, ty   int
	indirect int
	traces   bool
}{
	{"main_", 8, 8, -1, true},
	{"refl_fill", 8, 8, -1, false},
	{"refl_blur_h", 8, 8, -1, false},
	{"refl_blur_v", 8, 8, -1, false},
	{"shadow_radius_h", 8, 8, -1, false},
	{"shadow_radius_v", 8, 8, -1, false},
	{"shadow_soften_h", 8, 8, -1, false},
	{"shadow_soften_v", 8, 8, -1, false},
	{"aa_classify", 8, 8, -1, false},
	{"aa_resolve", 64, 1, aaDispatchBinding, true},
}

type manifest struct {
	Buffers          map[string][]argBinding `json:"buffers"`
	Threadgroups     map[string][]tgBinding  `json:"threadgroups"`
	SizeFields       []string                `json:"sizeFields"`
	ArrayLengthField string                  `json:"arrayLengthField"`
}

var loadedManifest manifest

func init() {
	if err := json.Unmarshal(traceManifest, &loadedManifest); err != nil {
		panic(fmt.Sprintf("metal: trace.metal.json: %v", err))
	}
}

// aaListSizeSlot is the position of the AA task list's length within naga's
// size struct. mslbind records which field the one arrayLength() lowering
// reads, because the field names are naga handle indices and cannot be derived
// from our binding numbers.
func aaListSizeSlot() (int, bool) {
	if loadedManifest.ArrayLengthField == "" {
		return 0, false
	}
	for i, n := range loadedManifest.SizeFields {
		if n == loadedManifest.ArrayLengthField {
			return i, true
		}
	}
	return 0, false
}

// naga appends _<n> to a resource name when it collides with something else in
// scope, so "blockers" arrives as "blockers_1".
var reNagaSuffix = regexp.MustCompile(`_\d+$`)

func (r *Renderer) addKernels() error {
	names := webgpu.BindingNames()
	errp := (*C.char)(unsafe.Pointer(&r.errBuf[0]))
	for _, k := range chain {
		args, ok := loadedManifest.Buffers[k.name]
		if !ok {
			return fmt.Errorf("metal: kernel %q missing from the manifest", k.name)
		}
		kmap := make([]C.int, len(args))
		for i, a := range args {
			if a.Name == "_buffer_sizes" {
				kmap[i] = C.int(sizesBinding)
				continue
			}
			b, ok := names[a.Name]
			if !ok {
				b, ok = names[reNagaSuffix.ReplaceAllString(a.Name, "")]
			}
			if !ok {
				return fmt.Errorf("metal: %s: no binding for resource %q", k.name, a.Name)
			}
			kmap[i] = C.int(b)
		}
		var tg []C.int
		for _, t := range loadedManifest.Threadgroups[k.name] {
			for len(tg) <= t.Index {
				tg = append(tg, 0)
			}
			tg[t.Index] = C.int(t.Bytes)
		}
		var tgp *C.int
		if len(tg) > 0 {
			tgp = &tg[0]
		}
		traces := 0
		if k.traces {
			traces = 1
		}
		if C.mtl_kernel(r.b, C.CString(k.name), &kmap[0], C.int(len(kmap)),
			C.int(k.tx), C.int(k.ty), C.int(k.indirect), tgp, C.int(len(tg)),
			C.int(traces), 2, 4, 12, C.int(sizesBinding), C.int(rtHandleBinding),
			errp, C.int(len(r.errBuf))) == 0 {
			return fmt.Errorf("metal: kernel %s: %s", k.name, cstr(r.errBuf))
		}
	}
	return nil
}
