package webgpu

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/rajveermalviya/go-webgpu/wgpu"

	"raytracer/internal/gibake"
	"raytracer/internal/gpuscene"
)

// uploadBakedGI writes the loaded volume into the tail of ao_volume.
//
// Once, with the rest of the static scene data: a bake does not change, which
// is the entire point of it.
func (r *Renderer) uploadBakedGI() error {
	if r.baked == nil || r.bakedUp {
		return nil
	}
	v := r.baked
	if err := r.queue.WriteBuffer(r.aoVolume, gpuscene.AOVolumeFloats*4, floatBytes(v.Probes)); err != nil {
		return fmt.Errorf("upload baked probes: %w", err)
	}
	if err := r.queue.WriteBuffer(r.aoVolume, r.bakedIndexBase*4, u32Bytes(v.Index)); err != nil {
		return fmt.Errorf("upload baked index grid: %w", err)
	}
	if err := r.queue.WriteBuffer(r.aoVolume, r.bakedCellBase*4, u32Bytes(v.Cells)); err != nil {
		return fmt.Errorf("upload baked cell list: %w", err)
	}
	r.bakedUp = true
	return nil
}

// BakedVolume is the loaded volume, or nil. The baker needs its layout to know
// how many probes to drive and where to read them back from.
func (r *Renderer) BakedVolume() *gibake.Volume { return r.baked }

// DriveBake makes the next frames run the probe pass over every probe in the
// loaded volume, blending at the given rate.
//
// The rate is the baker's ray-count knob. At 1/k on the k'th iteration the
// blend is an exact running mean, so N iterations average N * GI_PROBE_RAYS
// rays per probe rather than trailing them exponentially — which is how a bake
// buys quality the live field cannot, without touching the shader's ray count.
func (r *Renderer) DriveBake(blend float32) {
	r.bakeDrive = true
	r.bakeBlend = blend
}

// StopBake returns to normal rendering, where a loaded volume is read and
// never written.
func (r *Renderer) StopBake() { r.bakeDrive = false }

// readChunkBytes bounds one readback copy.
//
// A single 364 MB copy-and-map failed on Metal with nothing but an encoder
// assertion, so the transfer is chunked. It costs nothing: a bake reads back
// once, at the end.
const readChunkBytes = 64 << 20

// ReadBakedProbes copies the probe region back off the GPU into the loaded
// volume, which is what the baker writes to disk.
func (r *Renderer) ReadBakedProbes() error {
	if r.baked == nil {
		return fmt.Errorf("no baked volume loaded")
	}
	total := uint64(len(r.baked.Probes)) * 4
	if total == 0 {
		return nil
	}
	for off := uint64(0); off < total; off += readChunkBytes {
		n := total - off
		if n > readChunkBytes {
			n = readChunkBytes
		}
		if err := r.readProbeChunk(off, n); err != nil {
			return err
		}
	}
	return nil
}

func (r *Renderer) readProbeChunk(off, n uint64) error {
	staging, err := r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "baked probe readback",
		Usage: wgpu.BufferUsage_CopyDst | wgpu.BufferUsage_MapRead,
		Size:  n,
	})
	if err != nil {
		return fmt.Errorf("create probe readback buffer: %w", err)
	}
	defer staging.Release()

	enc, err := r.device.CreateCommandEncoder(nil)
	if err != nil {
		return err
	}
	// Finish the encoder on every path: releasing one that was never ended
	// aborts the process with a Metal assertion rather than returning.
	if err := enc.CopyBufferToBuffer(r.aoVolume, gpuscene.AOVolumeFloats*4+off, staging, 0, n); err != nil {
		if cmd, ferr := enc.Finish(nil); ferr == nil {
			cmd.Release()
		}
		enc.Release()
		return fmt.Errorf("copy probe region: %w", err)
	}
	cmd, err := enc.Finish(nil)
	if err != nil {
		enc.Release()
		return err
	}
	r.queue.Submit(cmd)
	cmd.Release()
	enc.Release()

	done := make(chan error, 1)
	if err := staging.MapAsync(wgpu.MapMode_Read, 0, n, func(status wgpu.BufferMapAsyncStatus) {
		if status != wgpu.BufferMapAsyncStatus_Success {
			done <- fmt.Errorf("map probe readback: %s", status)
			return
		}
		done <- nil
	}); err != nil {
		return err
	}
	r.device.Poll(true, nil)
	select {
	case err := <-done:
		if err != nil {
			return err
		}
	case <-time.After(60 * time.Second):
		return fmt.Errorf("probe readback timed out")
	}
	raw := staging.GetMappedRange(0, uint(n))
	base := int(off / 4)
	for i := 0; i < int(n)/4; i++ {
		r.baked.Probes[base+i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4 : i*4+4]))
	}
	return staging.Unmap()
}
