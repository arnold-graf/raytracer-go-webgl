package webgpu

// Capturing what the GPU actually received.
//
// The Metal backend (docs/metal-backend.md) runs the same megakernel that this
// package runs, translated by naga, but it cannot reuse this package's upload
// path: it has its own device, its own buffers, and no wgpu. Re-implementing the
// scene packing against MTLBuffer would mean a second copy of every packer in
// this file's neighbourhood, and any drift between the two would show up as a
// pixel difference that looks like a shading bug.
//
// So the Metal harness does not pack anything. It loads the bytes this backend
// uploaded, verbatim, and binds them at the same indices. That makes the scene
// data provably identical between the two backends and leaves exactly one thing
// under test: the traversal.
//
// Set RT_DUMP_BUFFERS=<dir> to write them. Every upload in this package goes
// through (*Renderer).wb, which records into a shadow copy of each binding --
// including partial and offset writes, so dirty-span uploads land where they
// would on the GPU. The dump is flushed at the end of each frame's upload, so
// the last frame rendered wins.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rajveermalviya/go-webgpu/wgpu"
)

type dumpSlot struct {
	binding uint32
	data    []byte
}

// pendingWrite is an upload that happened before the bind group existed, and so
// before the dump knew which binding the buffer belongs to.
type pendingWrite struct {
	buf    *wgpu.Buffer
	offset uint64
	data   []byte
}

type bufDump struct {
	dir     string
	slots   map[*wgpu.Buffer]*dumpSlot
	order   []*wgpu.Buffer
	pending []pendingWrite
}

// newBufDump returns nil unless RT_DUMP_BUFFERS names a directory, so the
// non-dumping path costs one nil check per upload.
func newBufDump() *bufDump {
	dir := os.Getenv("RT_DUMP_BUFFERS")
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "bufdump: %v\n", err)
		return nil
	}
	return &bufDump{dir: dir, slots: map[*wgpu.Buffer]*dumpSlot{}}
}

// register declares a binding and the size wgpu allocated for it. The shadow
// copy is that full size, not the size of any one write, so a binding written
// in dirty spans still dumps as the whole buffer the shader indexes into.
func (d *bufDump) register(binding uint32, buf *wgpu.Buffer, size uint64) {
	if d == nil || buf == nil {
		return
	}
	if _, ok := d.slots[buf]; ok {
		return
	}
	d.slots[buf] = &dumpSlot{binding: binding, data: make([]byte, size)}
	d.order = append(d.order, buf)
	// Replay anything uploaded before this buffer had a binding. Several
	// buffers are filled once during device setup, long before the bind group
	// exists -- perm (the Perlin permutation table) is the one that matters
	// most, because a zeroed permutation makes every fbm() and perlin() return
	// a constant and every procedural texture in the scene renders as flat
	// colour. Dropping those writes silently was a bug that looked exactly like
	// a shading bug in whatever consumed the dump.
	for _, w := range d.pending {
		if w.buf == buf {
			d.record(w.buf, w.offset, w.data)
		}
	}
}

func (d *bufDump) record(buf *wgpu.Buffer, offset uint64, data []byte) {
	if d == nil {
		return
	}
	s, ok := d.slots[buf]
	if !ok {
		// Either a buffer that is not in the bind group at all -- staging,
		// readback, indirect scratch -- or one written before registration.
		// Keeping a copy costs a little memory during setup and is the only
		// way to tell the two apart later.
		cp := make([]byte, len(data))
		copy(cp, data)
		d.pending = append(d.pending, pendingWrite{buf: buf, offset: offset, data: cp})
		return
	}
	if offset > uint64(len(s.data)) {
		return
	}
	copy(s.data[offset:], data)
}

// flush writes one file per binding plus a manifest. Binding 18 (shadowAux) and
// the other scratch buffers dump as zeros, which is what they hold at dispatch.
func (d *bufDump) flush() error {
	if d == nil {
		return nil
	}
	type entry struct {
		Binding uint32 `json:"binding"`
		File    string `json:"file"`
		Size    int    `json:"size"`
	}
	var man []entry
	for _, buf := range d.order {
		s := d.slots[buf]
		name := fmt.Sprintf("b%02d.bin", s.binding)
		if err := os.WriteFile(filepath.Join(d.dir, name), s.data, 0o644); err != nil {
			return err
		}
		man = append(man, entry{Binding: s.binding, File: name, Size: len(s.data)})
	}
	// Once every binding has been registered the journal has served its purpose,
	// and keeping it would pin megabytes of setup uploads for the process's life.
	d.pending = nil

	b, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d.dir, "bindings.json"), b, 0o644)
}

// wb is the single upload choke point. Every queue write in this package goes
// through it; see the note at the top of this file for why.
func (r *Renderer) wb(buf *wgpu.Buffer, offset uint64, data []byte) error {
	r.dump.record(buf, offset, data)
	return r.queue.WriteBuffer(buf, offset, data)
}
