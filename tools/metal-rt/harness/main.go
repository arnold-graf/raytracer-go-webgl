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
	kernel := flag.String("kernel", "main_", "kernel to dispatch")
	w := flag.Int("w", 512, "width (matches the in-game render resolution in main.go)")
	h := flag.Int("h", 320, "height (matches the in-game render resolution in main.go)")
	iters := flag.Int("iters", 20, "dispatches, best is reported")
	out := flag.String("out", "", "write the output buffer here as rgba")
	verbose := flag.Bool("v", false, "print acceleration structure sizes")
	maxInst := flag.Int("maxinst", 0, "cap instances (0 = all), to isolate a bad placement")
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
	args, ok := manifest[*kernel]
	if !ok {
		die(fmt.Errorf("kernel %q not in the manifest", *kernel))
	}
	nameToBinding := parseWGSLBindings(*wgsl)

	var bindings []bindingEntry
	readJSON(filepath.Join(*dir, "bufs", "bindings.json"), &bindings)
	byBinding := map[uint32]bindingEntry{}
	for _, b := range bindings {
		byBinding[b.Binding] = b
	}

	cerr := make([]byte, 1024)
	errp := (*C.char)(unsafe.Pointer(&cerr[0]))
	m := C.mrt_new(cstr(filepath.Join(*dir, "bound.metallib")), errp, C.int(len(cerr)))
	if m == nil {
		die(fmt.Errorf("mrt_new: %s", gostr(cerr)))
	}
	defer C.mrt_free(m)

	// Bind every argument the kernel declares.
	var sizesIdx, primsIdx, blockersIdx, holesIdx = -1, -1, -1, -1
	for _, a := range args {
		if a.Name == "_buffer_sizes" {
			sizesIdx = a.Index
			sz := make([]byte, bufferSizesFields*4)
			for i := 0; i < bufferSizesFields; i++ {
				binary.LittleEndian.PutUint32(sz[i*4:], ^uint32(0)/4)
			}
			setBuffer(m, a.Index, sz)
			continue
		}
		bind, ok := lookupBinding(nameToBinding, a.Name)
		if !ok {
			die(fmt.Errorf("no WGSL binding for resource %q (metal index %d)", a.Name, a.Index))
		}
		e, ok := byBinding[bind]
		if !ok {
			die(fmt.Errorf("binding %d (%s) was not dumped", bind, a.Name))
		}
		data, err := os.ReadFile(filepath.Join(*dir, "bufs", e.File))
		if err != nil {
			die(err)
		}
		setBuffer(m, a.Index, data)
		switch baseName(a.Name) {
		case "prims":
			primsIdx = a.Index
		case "blockers":
			blockersIdx = a.Index
		case "holes":
			holesIdx = a.Index
		}
	}
	for name, idx := range map[string]int{"prims": primsIdx, "blockers": blockersIdx, "holes": holesIdx, "_buffer_sizes": sizesIdx} {
		if idx < 0 {
			die(fmt.Errorf("%s is not an argument of %s; the intersection functions need it", name, *kernel))
		}
	}

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
	if C.mrt_build_pipeline(m, cstr(*kernel), C.int(primsIdx), C.int(blockersIdx),
		C.int(holesIdx), C.int(sizesIdx), 30, errp, C.int(len(cerr))) == 0 {
		die(fmt.Errorf("build_pipeline: %s", gostr(cerr)))
	}
	fmt.Println("pipeline linked with prim_isect and blocker_isect")

	gx, gy := (*w+7)/8, (*h+7)/8
	ms := C.mrt_dispatch(m, C.int(gx), C.int(gy), 8, 8, C.int(*iters), errp, C.int(len(cerr)))
	if ms < 0 {
		die(fmt.Errorf("dispatch: %s", gostr(cerr)))
	}
	fmt.Printf("%s: %.3f ms (best of %d) at %dx%d\n", *kernel, float64(ms), *iters, *w, *h)

	if *out != "" {
		// Binding 1 is the output image.
		idx := -1
		for _, a := range args {
			if b, ok := lookupBinding(nameToBinding, a.Name); ok && b == 1 {
				idx = a.Index
			}
		}
		if idx < 0 {
			die(fmt.Errorf("output binding 1 is not an argument of %s", *kernel))
		}
		buf := make([]byte, *w**h*4)
		if C.mrt_read_buffer(m, C.int(idx), unsafe.Pointer(&buf[0]), C.size_t(len(buf))) == 0 {
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
