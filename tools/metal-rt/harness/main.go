// harness runs the megakernel on Metal's acceleration structures.
//
// It packs nothing and ports nothing. The scene bytes come from the WGSL
// backend's own upload path (RT_DUMP_BUFFERS, see internal/webgpu/bufdump.go),
// the shader is naga's translation of the same WGSL with only the traversal
// spliced (tools/metal-rt/mslpatch), and the per-primitive test is naga's
// generated intersect(). The one thing that differs is who walks the tree,
// which is the point.
//
//	go run ./tools/metal-rt/accelgen scenes/office-sunset/index.toml tmp/.../accel.bin
//	RT_DUMP_BUFFERS=tmp/.../bufs go run ./cmd/gpuprof -scene ... -dump ref.rgba
//	go run ./tools/metal-rt/mslpatch internal/webgpu/shaders/trace_linked.wgsl rt.metal
//	go run ./tools/metal-rt/mslbind rt.metal bound.metal
//	xcrun -sdk macosx metal -std=metal3.0 -c bound.metal -o bound.air
//	xcrun -sdk macosx metallib bound.air -o bound.metallib
//	go run ./tools/metal-rt/harness -dir tmp/... -out out.rgba
package main

/*
#cgo CFLAGS: -x objective-c -fmodules -Wno-unused-command-line-argument
#cgo LDFLAGS: -framework Metal -framework Foundation
#include "metalrt.h"
#include <stdlib.h>
*/
import "C"

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"unsafe"
)

// bufferSizesFields is how many uints naga's runtime-array size table holds.
// The harness cannot map each field back to a buffer -- the numbering is
// naga-internal, running past our binding count -- so every field is set
// permissively and every allocation is padded. If the rendered image matches
// the WGSL backend's, the bounds checks provably never fired.
const bufferSizesFields = 64

const allocPad = 1 << 16

// sizesBinding parks naga's runtime-array size table past the real bindings,
// which run 0..29. aaDispatchBinding is the indirect header aa_classify fills.
const (
	sizesBinding      = 31
	aaDispatchBinding = 29
)

type bindingEntry struct {
	Binding uint32 `json:"binding"`
	File    string `json:"file"`
	Size    int    `json:"size"`
}

type argBinding struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
}

func main() {
	dir := flag.String("dir", "", "directory holding accel.bin, bufs/, bound.metallib, bound.metal.json")
	wgsl := flag.String("wgsl", "internal/webgpu/shaders/trace_linked.wgsl", "linked WGSL, for the binding names")
	w := flag.Int("w", 512, "width (matches the in-game render resolution in main.go)")
	h := flag.Int("h", 320, "height (matches the in-game render resolution in main.go)")
	iters := flag.Int("iters", 20, "dispatches, best is reported")
	out := flag.String("out", "", "write the output buffer here as rgba")
	verbose := flag.Bool("v", false, "print acceleration structure sizes")
	maxInst := flag.Int("maxinst", 0, "cap instances (0 = all), to isolate a bad placement")
	only := flag.String("only", "", "dispatch just this kernel instead of the whole frame")
	probe := flag.Bool("probe", false, "trace a few rays with the structures bound directly, then exit")
	flag.Parse()
	if *dir == "" {
		die(fmt.Errorf("-dir is required"))
	}

	// Metal index -> resource name, and name -> WGSL binding. naga orders
	// arguments by signature position, so this is the only link between the
	// dumped bindings and what the kernel expects.
	var manifest map[string][]argBinding
	readJSON(filepath.Join(*dir, "bound.metal.json"), &manifest)
	nameToBinding := parseWGSLBindings(*wgsl)

	var bindings []bindingEntry
	readJSON(filepath.Join(*dir, "bufs", "bindings.json"), &bindings)

	cerr := make([]byte, 1024)
	errp := (*C.char)(unsafe.Pointer(&cerr[0]))
	m := C.mrt_new(cstr(filepath.Join(*dir, "bound.metallib")), errp, C.int(len(cerr)))
	if m == nil {
		die(fmt.Errorf("mrt_new: %s", gostr(cerr)))
	}
	defer C.mrt_free(m)

	// One buffer per WGSL binding, shared by every kernel. naga's runtime-array
	// size table has no binding of its own, so it takes a slot past the end.
	for _, e := range bindings {
		data, err := os.ReadFile(filepath.Join(*dir, "bufs", e.File))
		if err != nil {
			die(err)
		}
		setBuffer(m, int(e.Binding), data)
	}
	sz := make([]byte, bufferSizesFields*4)
	for i := 0; i < bufferSizesFields; i++ {
		binary.LittleEndian.PutUint32(sz[i*4:], ^uint32(0)/4)
	}
	setBuffer(m, sizesBinding, sz)

	if *verbose {
		C.mrt_set_verbose(1)
	}
	loadAccel(m, filepath.Join(*dir, "accel.bin"), *maxInst)
	if C.mrt_build_accel(m, errp, C.int(len(cerr))) == 0 {
		die(fmt.Errorf("build_accel: %s", gostr(cerr)))
	}
	fmt.Println("acceleration structures built")

	if *probe {
		runProbe(m, errp, cerr)
		return
	}

	// The same order internal/webgpu/device.go submits: main, then the
	// reflection filter (which puts the glossy lobe back into hdr_pixels before
	// anything gathers over it), then the separable penumbra filter, then AA
	// classification -- all in one encoder so each sees the last one's writes.
	// aa_resolve runs indirectly from the task list aa_classify built.
	chain := []struct {
		name     string
		tx, ty   int
		indirect int
	}{
		{"main_", 8, 8, -1},
		{"refl_fill", 8, 8, -1},
		{"refl_blur_h", 8, 8, -1},
		{"refl_blur_v", 8, 8, -1},
		{"shadow_radius_h", 8, 8, -1},
		{"shadow_radius_v", 8, 8, -1},
		{"shadow_soften_h", 8, 8, -1},
		{"shadow_soften_v", 8, 8, -1},
		{"aa_classify", 8, 8, -1},
		{"aa_resolve", 64, 1, aaDispatchBinding},
	}
	if *only != "" {
		for _, k := range chain {
			if k.name == *only {
				chain = chain[:0]
				chain = append(chain, k)
				break
			}
		}
	}
	for _, k := range chain {
		kargs, ok := manifest[k.name]
		if !ok {
			die(fmt.Errorf("kernel %q not in the manifest", k.name))
		}
		kmap := make([]C.int, len(kargs))
		for i, a := range kargs {
			if a.Name == "_buffer_sizes" {
				kmap[i] = C.int(sizesBinding)
				continue
			}
			b, ok := lookupBinding(nameToBinding, a.Name)
			if !ok {
				die(fmt.Errorf("%s: no WGSL binding for %q", k.name, a.Name))
			}
			kmap[i] = C.int(b)
		}
		if C.mrt_add_kernel(m, cstr(k.name), &kmap[0], C.int(len(kmap)),
			C.int(k.tx), C.int(k.ty), C.int(k.indirect),
			2, 4, 12, C.int(sizesBinding), 30, errp, C.int(len(cerr))) == 0 {
			die(fmt.Errorf("add_kernel %s: %s", k.name, gostr(cerr)))
		}
	}
	fmt.Printf("%d kernels linked\n", len(chain))
	gx, gy := (*w+7)/8, (*h+7)/8
	// aa_classify appends to a task list with an indirect header; it has to
	// start empty each frame or the previous frame's work is redispatched.
	reset := make([]byte, 16)
	binary.LittleEndian.PutUint32(reset[4:], 1)
	binary.LittleEndian.PutUint32(reset[8:], 1)
	ms := C.mrt_run(m, C.int(gx), C.int(gy), C.int(*iters),
		C.int(aaDispatchBinding), unsafe.Pointer(&reset[0]), C.size_t(len(reset)),
		errp, C.int(len(cerr)))
	if ms < 0 {
		die(fmt.Errorf("run: %s", gostr(cerr)))
	}
	fmt.Printf("frame: %.3f ms (best of %d) at %dx%d\n", float64(ms), *iters, *w, *h)

	// The indirect header aa_classify fills: [groups_x, 1, 1, task_count].
	// If the count is zero, nothing was classified and aa_resolve dispatched
	// nothing, which is the difference between a filter running and a filter
	// silently not running.
	var hdr [4]uint32
	if C.mrt_read_buffer(m, C.int(aaDispatchBinding), unsafe.Pointer(&hdr[0]), 16) != 0 {
		fmt.Printf("aa dispatch header: groups=%d tasks=%d\n", hdr[0], hdr[3])
	}

	if *out != "" {
		buf := make([]byte, *w**h*4)
		if C.mrt_read_buffer(m, 1, unsafe.Pointer(&buf[0]), C.size_t(len(buf))) == 0 {
			die(fmt.Errorf("read output"))
		}
		if err := os.WriteFile(*out, buf, 0o644); err != nil {
			die(err)
		}
		fmt.Printf("wrote %s (%d bytes)\n", *out, len(buf))
	}
}

func setBuffer(m *C.MRT, index int, data []byte) {
	var p unsafe.Pointer
	if len(data) > 0 {
		p = unsafe.Pointer(&data[0])
	}
	if C.mrt_set_buffer(m, C.int(index), p, C.size_t(len(data)), C.size_t(allocPad)) == 0 {
		die(fmt.Errorf("set_buffer %d (%d bytes)", index, len(data)))
	}
}

// loadAccel reads accelgen's output and hands each structure to Metal.
func loadAccel(m *C.MRT, path string, maxInst int) {
	b, err := os.ReadFile(path)
	if err != nil {
		die(err)
	}
	r := &reader{b: b}
	if got := r.u32(); got != 0x314C4341 {
		die(fmt.Errorf("accel.bin: bad magic %#x", got))
	}
	nGeom, nBlock, nInst := int(r.u32()), int(r.u32()), int(r.u32())
	wantInst := nInst
	if maxInst > 0 && maxInst < nInst {
		wantInst = maxInst
	}
	if C.mrt_begin_accel(m, C.int(nGeom), C.int(nBlock), C.int(wantInst)) == 0 {
		die(fmt.Errorf("begin_accel"))
	}
	readSet := func(n int, blocker int) {
		for slot := 0; slot < n; slot++ {
			count := int(r.u32())
			bounds := make([]float32, count*6)
			gidx := make([]uint32, count)
			for i := 0; i < count; i++ {
				gidx[i] = r.u32()
				for k := 0; k < 6; k++ {
					bounds[i*6+k] = r.f32()
				}
			}
			var bp *C.float
			var gp *C.uint32_t
			if count > 0 {
				bp = (*C.float)(unsafe.Pointer(&bounds[0]))
				gp = (*C.uint32_t)(unsafe.Pointer(&gidx[0]))
			}
			if C.mrt_add_blas(m, C.int(blocker), C.int(slot), bp, gp, C.int(count)) == 0 {
				die(fmt.Errorf("add_blas %d", slot))
			}
		}
	}
	readSet(nGeom, 0)
	readSet(nBlock, 1)
	for i := 0; i < nInst; i++ {
		var xf [12]float32
		for k := 0; k < 12; k++ {
			xf[k] = r.f32()
		}
		blas := r.u32()
		r.u32() // pad
		if i >= wantInst {
			continue
		}
		if C.mrt_add_instance(m, (*C.float)(unsafe.Pointer(&xf[0])), C.uint32_t(blas)) == 0 {
			die(fmt.Errorf("add_instance %d", i))
		}
	}
	fmt.Printf("accel: %d geometry, %d blocker structures, %d instances\n", nGeom, nBlock, wantInst)
}

type reader struct {
	b []byte
	i int
}

func (r *reader) u32() uint32 {
	v := binary.LittleEndian.Uint32(r.b[r.i:])
	r.i += 4
	return v
}
func (r *reader) f32() float32 {
	v := binary.LittleEndian.Uint32(r.b[r.i:])
	r.i += 4
	return float32frombits(v)
}

func float32frombits(b uint32) float32 { return *(*float32)(unsafe.Pointer(&b)) }

// naga appends _<n> to a resource name when it collides with something else in
// scope, so "blockers" arrives as "blockers_1". The suffix carries no meaning
// here; the base name is what maps to a WGSL binding.
var reNagaSuffix = regexp.MustCompile(`_\d+$`)

func baseName(n string) string { return reNagaSuffix.ReplaceAllString(n, "") }

func lookupBinding(m map[string]uint32, name string) (uint32, bool) {
	if b, ok := m[name]; ok {
		return b, true
	}
	b, ok := m[baseName(name)]
	return b, ok
}

var reBinding = regexp.MustCompile(`@group\(0\)\s+@binding\((\d+)\)\s+var(?:<[^>]*>)?\s+(\w+)\s*:`)

func parseWGSLBindings(path string) map[string]uint32 {
	b, err := os.ReadFile(path)
	if err != nil {
		die(err)
	}
	out := map[string]uint32{}
	for _, m := range reBinding.FindAllStringSubmatch(string(b), -1) {
		var n uint32
		fmt.Sscanf(m[1], "%d", &n)
		out[m[2]] = n
	}
	if len(out) == 0 {
		die(fmt.Errorf("no @group(0) @binding declarations in %s", path))
	}
	return out
}

func readJSON(path string, v any) {
	b, err := os.ReadFile(path)
	if err != nil {
		die(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		die(fmt.Errorf("%s: %w", path, err))
	}
}

func cstr(s string) *C.char { return C.CString(s) }

func gostr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "harness:", err)
	os.Exit(1)
}

// runProbe fires rays at the scene with the acceleration structure bound
// directly to the kernel. The scene spans roughly x -0.1..120, y 0..233,
// z 0..151, and the camera sits inside it, so a ray straight down and a ray
// along each axis should all hit something.
func runProbe(m *C.MRT, errp *C.char, cerr []byte) {
	// The first static box is (119,200,50)..(120,225,150); its centre is
	// (119.5, 212.5, 100). A ray fired at that centre from inside the scene
	// must hit if the structure holds anything at all. The scene-wide rays
	// after it can legitimately miss -- the floor is an infinite plane and
	// lives outside the BVH -- so they are context, not the test.
	rays := []float32{
		110, 212.5, 100, 1, 0, 0, // straight at box[0]
		119.5, 212.5, 90, 0, 0, 1, // at box[0] along +z
		119.5, 300, 100, 0, -1, 0, // down onto box[0] from above
		44.9, 201.3, 33.1, 1, 0, 0, // from the camera, +x
		44.9, 201.3, 33.1, 0, -1, 0, // from the camera, down
	}
	n := len(rays) / 6
	var bh, th uint32
	if C.mrt_selftest(m, (*C.uint32_t)(unsafe.Pointer(&bh)), (*C.uint32_t)(unsafe.Pointer(&th)),
		errp, C.int(len(cerr))) == 0 {
		die(fmt.Errorf("selftest: %s", gostr(cerr)))
	}
	fmt.Printf("  %-32s BLAS=%s TLAS=%s\n", "selftest (1 box, 1 identity inst)",
		hitStr(bh), hitStr(th))

	names := []string{"at box[0] +x", "at box[0] +z", "onto box[0] down", "camera +x", "camera down"}
	for _, mode := range []struct {
		label string
		blas  C.int
	}{{"BLAS 0 directly (no instancing)", 1}, {"TLAS (through instances)", 0}} {
		hits := make([]uint32, n)
		if C.mrt_probe(m, (*C.float)(unsafe.Pointer(&rays[0])), C.int(n),
			(*C.uint32_t)(unsafe.Pointer(&hits[0])), mode.blas, errp, C.int(len(cerr))) == 0 {
			die(fmt.Errorf("probe: %s", gostr(cerr)))
		}
		total := 0
		for _, h := range hits {
			total += int(h)
		}
		fmt.Printf("  %-32s %d of %d hit", mode.label, total, n)
		for i, h := range hits {
			if h == 1 {
				fmt.Printf("  [%s]", names[i])
			}
		}
		fmt.Println()
	}
}

func hitStr(v uint32) string {
	if v == 1 {
		return "HIT"
	}
	return "miss"
}
