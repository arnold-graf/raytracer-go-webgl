// Package metal renders with Apple's ray-tracing acceleration structures in
// place of the WGSL BVH traversal.
//
// It is a sibling of internal/webgpu, not a replacement: the same megakernel,
// generated from the same WGSL (gen.sh), fed by the same packer
// (webgpu.Packer). Traversal is the only thing that differs, which is what
// keeps the two backends from drifting. See docs/metal-backend.md.
package metal

/*
#cgo CFLAGS: -x objective-c -fmodules -Wno-unused-command-line-argument
#cgo LDFLAGS: -framework Metal -framework Foundation
#include "metal.h"
#include <stdlib.h>
*/
import "C"

import (
	_ "embed"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"unsafe"

	"raytracer/internal/camera"
	"raytracer/internal/render"
	"raytracer/internal/webgpu"
)

//go:embed trace.metallib
var traceLib []byte

//go:embed trace.metal.json
var traceManifest []byte

const (
	// sizesBinding parks naga's runtime-array size table past the real
	// bindings, which run 0..29. rtHandleBinding is where mslpatch puts the
	// argument buffer holding the structures and function tables.
	sizesBinding      = 31
	rtHandleBinding   = 30
	aaDispatchBinding = 29
	aaListBinding     = 28
	outputBinding     = 1
	// bufferSizesFields is how many uints naga's size table holds. Most back
	// bounds checks that never fire for a valid index; the one that is
	// semantic is found and set explicitly. See sizeTable.
	bufferSizesFields = 64
)

// Supported reports whether this machine can run the Metal backend at all.
func Supported() bool { return C.mtl_supported() == 1 }

type argBinding struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
}

type tgBinding struct {
	Index int `json:"index"`
	Bytes int `json:"bytes"`
}

// Renderer implements render.Renderer.
type Renderer struct {
	b      *C.MTLBackend
	packer *webgpu.Packer
	w, h   int
	maxDim int

	built    bool
	builds   int
	refits   int
	lastAcc  *webgpu.AccelPack
	aaReset  []byte
	errBuf   []byte
	frameErr error
}

// New creates the backend and uploads nothing; the first Render packs the scene
// and builds the acceleration structures.
func New(w, h int) (*Renderer, error) {
	if !Supported() {
		return nil, fmt.Errorf("metal: no ray-tracing capable device")
	}
	maxDim := w
	if h > maxDim {
		maxDim = h
	}
	r := &Renderer{
		packer: webgpu.NewPacker(maxDim),
		w:      w, h: h, maxDim: maxDim,
		errBuf: make([]byte, 1024),
	}
	errp := (*C.char)(unsafe.Pointer(&r.errBuf[0]))
	r.b = C.mtl_new(unsafe.Pointer(&traceLib[0]), C.size_t(len(traceLib)),
		errp, C.int(len(r.errBuf)))
	if r.b == nil {
		return nil, fmt.Errorf("metal: %s", cstr(r.errBuf))
	}

	sizes := webgpu.BindingSizes(maxDim)
	for b, n := range sizes {
		if C.mtl_alloc(r.b, C.int(b), C.size_t(n)) == 0 {
			r.Release()
			return nil, fmt.Errorf("metal: allocate binding %d (%d bytes)", b, n)
		}
	}
	if err := r.allocSizeTable(); err != nil {
		r.Release()
		return nil, err
	}
	if err := r.addKernels(); err != nil {
		r.Release()
		return nil, err
	}
	// The AA task list starts empty each frame or the previous frame's work is
	// redispatched.
	r.aaReset = make([]byte, 16)
	binary.LittleEndian.PutUint32(r.aaReset[4:], 1)
	binary.LittleEndian.PutUint32(r.aaReset[8:], 1)
	return r, nil
}

func (r *Renderer) Release() {
	if r.b != nil {
		C.mtl_free(r.b)
		r.b = nil
	}
}

// Render fills buf by packing the scene, updating the acceleration structures
// and running the kernel chain.
func (r *Renderer) Render(buf []byte, cam *camera.Camera, v *render.View, _ int) {
	if r.b == nil || cam == nil || len(buf) < r.w*r.h*4 {
		return
	}
	f := r.packer.Pack(cam, v, r.w, r.h)
	for b, data := range f.Bindings {
		if C.mtl_write(r.b, C.int(b), unsafe.Pointer(&data[0]), C.size_t(len(data))) == 0 {
			r.fail(buf, fmt.Errorf("write binding %d", b))
			return
		}
	}
	if err := r.syncAccel(f); err != nil {
		r.fail(buf, err)
		return
	}
	C.mtl_write(r.b, C.int(aaDispatchBinding), unsafe.Pointer(&r.aaReset[0]), C.size_t(len(r.aaReset)))

	gx, gy := C.int((r.w+7)/8), C.int((r.h+7)/8)
	enabled := enabledChain(f)
	errp := (*C.char)(unsafe.Pointer(&r.errBuf[0]))
	if C.mtl_frame(r.b, gx, gy, &enabled[0], errp, C.int(len(r.errBuf))) == 0 {
		r.fail(buf, fmt.Errorf("%s", cstr(r.errBuf)))
		return
	}
	n := r.w * r.h * 4
	C.mtl_read(r.b, C.int(outputBinding), unsafe.Pointer(&buf[0]), C.size_t(n))
	r.frameErr = nil
}

// syncAccel builds the structures the first time and afterwards refits them
// when only transforms moved. A refit keeps the topology, which is cheap and
// degrades as geometry travels from where the tree was built -- the same trade
// bvh_refit.go makes on the CPU side, and it is the CPU-side BVH refit that
// decides when a rebuild is due, since these structures are derived from it.
func (r *Renderer) syncAccel(f *webgpu.Frame) error {
	rebuild := !r.built || f.StaticChanged
	if !refitEnabled() {
		// Rebuilding on every transform change is slower but cannot wedge the
		// device, so it is the default. See refitEnabled.
		rebuild = rebuild || f.TransformsChanged
	}
	if !rebuild && !f.TransformsChanged {
		return nil
	}
	pack := webgpu.AccelPackFrom(f.Nodes, f.Templates, f.Instances, f.BlockerSectionStart)
	if r.built && !rebuild && !r.sameShape(pack) {
		// A refit cannot change how many boxes a structure holds.
		rebuild = true
	}
	if err := r.uploadAccel(pack, rebuild); err != nil {
		return err
	}
	errp := (*C.char)(unsafe.Pointer(&r.errBuf[0]))
	if rebuild {
		if C.mtl_accel_build(r.b, errp, C.int(len(r.errBuf))) == 0 {
			return fmt.Errorf("accel build: %s", cstr(r.errBuf))
		}
		r.built = true
		r.builds++
	} else {
		if C.mtl_accel_refit(r.b, errp, C.int(len(r.errBuf))) == 0 {
			return fmt.Errorf("accel refit: %s", cstr(r.errBuf))
		}
		r.refits++
	}
	r.lastAcc = pack
	return nil
}

func (r *Renderer) sameShape(p *webgpu.AccelPack) bool {
	o := r.lastAcc
	if o == nil || len(o.Geom) != len(p.Geom) || len(o.Blockers) != len(p.Blockers) ||
		len(o.Instances) != len(p.Instances) {
		return false
	}
	for i := range p.Geom {
		if len(o.Geom[i].Leaves) != len(p.Geom[i].Leaves) {
			return false
		}
	}
	for i := range p.Blockers {
		if len(o.Blockers[i].Leaves) != len(p.Blockers[i].Leaves) {
			return false
		}
	}
	return true
}

func (r *Renderer) uploadAccel(p *webgpu.AccelPack, rebuild bool) error {
	if rebuild {
		if C.mtl_accel_begin(r.b, C.int(len(p.Geom)), C.int(len(p.Blockers)),
			C.int(len(p.Instances))) == 0 {
			return fmt.Errorf("accel begin")
		}
	}
	put := func(blocker int, sets []webgpu.AccelBLAS) error {
		for slot, bl := range sets {
			bounds := make([]float32, len(bl.Leaves)*6)
			gidx := make([]uint32, len(bl.Leaves))
			for i, l := range bl.Leaves {
				gidx[i] = l.Prim
				copy(bounds[i*6:], l.Min[:])
				copy(bounds[i*6+3:], l.Max[:])
			}
			var bp *C.float
			var gp *C.uint32_t
			if len(bl.Leaves) > 0 {
				bp = (*C.float)(unsafe.Pointer(&bounds[0]))
				gp = (*C.uint32_t)(unsafe.Pointer(&gidx[0]))
			}
			if C.mtl_accel_blas(r.b, C.int(blocker), C.int(slot), bp, gp,
				C.int(len(bl.Leaves))) == 0 {
				return fmt.Errorf("accel blas %d", slot)
			}
		}
		return nil
	}
	if err := put(0, p.Geom); err != nil {
		return err
	}
	if err := put(1, p.Blockers); err != nil {
		return err
	}
	if rebuild {
		for _, in := range p.Instances {
			xf := in.Xf
			if C.mtl_accel_instance(r.b, (*C.float)(unsafe.Pointer(&xf[0])),
				C.uint32_t(in.BLAS)) == 0 {
				return fmt.Errorf("accel instance")
			}
		}
	}
	return nil
}

// fail blacks the frame and reports once. Reporting every frame would bury the
// first and only cause of the failure under thousands of copies of it, and a
// renderer that silently draws black is the hardest kind of bug to notice.
func (r *Renderer) fail(buf []byte, err error) {
	if r.frameErr == nil {
		log.Printf("metal: render failed, frames will be black until it recovers: %v", err)
	}
	r.frameErr = err
	for i := 0; i+3 < len(buf); i += 4 {
		buf[i], buf[i+1], buf[i+2], buf[i+3] = 0, 0, 0, 255
	}
}

// Err reports why the last frame failed, if it did.
func (r *Renderer) Err() error { return r.frameErr }

// AccelStats reports how many times the acceleration structures were rebuilt
// from scratch and how many times they were refit in place. A moving scene
// should settle into refits; a climbing rebuild count means something is
// invalidating static geometry every frame.
func (r *Renderer) AccelStats() (builds, refits int) { return r.builds, r.refits }

// refitEnabled gates in-place refitting, and defaults to off.
//
// Refitting a structure that was not built with
// MTLAccelerationStructureUsageRefit is undefined, and what it did here was
// hang the GPU until the watchdog killed the command buffer, taking the machine
// with it. The usage flag is now set, so this should be safe -- but "should be"
// is what the previous attempt had too, so it stays opt-in until it has been
// run on hardware deliberately. RT_METAL_REFIT=1 enables it.
func refitEnabled() bool { return os.Getenv("RT_METAL_REFIT") == "1" }

func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

var _ render.Renderer = (*Renderer)(nil)

// allocSizeTable fills naga's runtime-array size table. Most fields back bounds
// checks that never fire for a valid index, so they are set permissively. The
// shader calls arrayLength() exactly once, in aa_classify's guard against the
// task list, and that use is semantic: left permissive, threads past the list
// read uninitialised entries and write them to the frame.
func (r *Renderer) allocSizeTable() error {
	if C.mtl_alloc(r.b, C.int(sizesBinding), C.size_t(bufferSizesFields*4)) == 0 {
		return fmt.Errorf("metal: allocate size table")
	}
	sz := make([]byte, bufferSizesFields*4)
	for i := 0; i < bufferSizesFields; i++ {
		binary.LittleEndian.PutUint32(sz[i*4:], ^uint32(0)/4)
	}
	if slot, ok := aaListSizeSlot(); ok {
		binary.LittleEndian.PutUint32(sz[slot*4:], uint32(webgpu.BindingSizes(r.maxDim)[aaListBinding]))
	}
	C.mtl_write(r.b, C.int(sizesBinding), unsafe.Pointer(&sz[0]), C.size_t(len(sz)))
	return nil
}
