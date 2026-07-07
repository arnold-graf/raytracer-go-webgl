package webgpu

import (
	"bytes"
	"math"
	"path/filepath"
	"testing"

	"raytracer/internal/npc"
	"raytracer/internal/render"
	"raytracer/internal/sceneio"
)

func TestInstancedScenePacksNPCBody(t *testing.T) {
	root := filepath.Join("..", "..")
	sc, err := sceneio.Load(filepath.Join(root, "scenes", "office-sunset", "index.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := npc.NewManager()
	if err := m.Instantiate(sc, npc.FootWorld(sc)); err != nil {
		t.Fatal(err)
	}
	if len(sc.DynamicBodies) == 0 {
		t.Skip("server-room NPC spawn is commented out in server-room-1.toml")
	}
	if len(sc.DynamicBodies) != 1 {
		t.Fatalf("dynamic bodies = %d, want 1", len(sc.DynamicBodies))
	}

	prims, _, _, _, _, _, dynGPU, ok := packInstancedScene(sc)
	if !ok {
		t.Fatal("packInstancedScene failed")
	}
	if len(dynGPU.cylinder) < 8 {
		t.Fatalf("dynamic cylinder map = %d, want at least 8 limb cylinders", len(dynGPU.cylinder))
	}
	if len(dynGPU.sphere) != 1 {
		t.Fatalf("dynamic sphere map = %d, want 1 (head)", len(dynGPU.sphere))
	}

	foundHead := false
	for _, gi := range dynGPU.sphere {
		if gi < 0 || gi >= len(prims) {
			t.Fatalf("head gpu index %d out of range (prims=%d)", gi, len(prims))
		}
		if prims[gi].Meta[0] == primSphere && prims[gi].Albedo[0] > 0.6 {
			foundHead = true
		}
	}
	if !foundHead {
		t.Fatal("expected skin-toned head sphere in GPU prims")
	}

	var c sceneCache
	c.rebuild(&render.View{Scene: sc})
	if len(c.layout.gpu.cylinder) < 8 {
		t.Fatalf("cache dynamic cylinders = %d", len(c.layout.gpu.cylinder))
	}
}

func TestInstancedNPCPartialTransformUpdate(t *testing.T) {
	root := filepath.Join("..", "..")
	sc, err := sceneio.Load(filepath.Join(root, "scenes", "office-sunset", "index.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := npc.NewManager()
	if err := m.Instantiate(sc, npc.FootWorld(sc)); err != nil {
		t.Fatal(err)
	}
	if len(sc.DynamicBodies) == 0 {
		t.Skip("server-room NPC spawn is commented out in server-room-1.toml")
	}

	var c sceneCache
	c.rebuild(&render.View{Scene: sc})
	tlasBefore := append([]GPUBVHNode(nil), c.bvhNodes[c.bvhNodeCount:]...)
	sc.TouchTransforms()
	c.updateDynamicTransforms(sc)
	if len(c.partialPrimSpans) == 0 {
		t.Fatal("expected partial spans for NPC pose update")
	}
	if len(c.partialBlockerSpans) == 0 {
		t.Fatal("expected partial blocker spans for NPC shadows")
	}
	if !bytes.Equal(
		gpuBVHBytes(tlasBefore),
		gpuBVHBytes(c.bvhNodes[c.bvhNodeCount:]),
	) {
		t.Fatal("instanced TLAS/BLAS should not change on NPC transform refit")
	}
}

func gpuBVHBytes(nodes []GPUBVHNode) []byte {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]byte, len(nodes)*48)
	for i := range nodes {
		copyNodeBytes(out[i*48:(i+1)*48], nodes[i])
	}
	return out
}

func copyNodeBytes(dst []byte, n GPUBVHNode) {
	putF32 := func(off int, v float32) {
		u := math.Float32bits(v)
		dst[off+0] = byte(u)
		dst[off+1] = byte(u >> 8)
		dst[off+2] = byte(u >> 16)
		dst[off+3] = byte(u >> 24)
	}
	putU32 := func(off int, u uint32) {
		dst[off+0] = byte(u)
		dst[off+1] = byte(u >> 8)
		dst[off+2] = byte(u >> 16)
		dst[off+3] = byte(u >> 24)
	}
	for i := 0; i < 4; i++ {
		putF32(i*4, n.Min[i])
		putF32(16+i*4, n.Max[i])
		putU32(32+i*4, n.Info[i])
	}
}
