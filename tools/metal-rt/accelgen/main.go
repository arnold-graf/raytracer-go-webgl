// accelgen emits the geometry the Metal harness builds its acceleration
// structures from, derived from the same packed BVH the WGSL backend traverses.
// See internal/webgpu/accelpack.go for the shape and why it is taken from the
// tree rather than recomputed from primitives.
//
//	go run ./tools/metal-rt/accelgen scenes/office-sunset/index.toml /tmp/accel.bin
//
// The format is little-endian and deliberately dull:
//
//	u32 magic 'ACL1', u32 nGeom, u32 nBlocker, u32 nInstance
//	per BLAS (geom then blocker): u32 leafCount, then leafCount * (u32 prim, 6*f32 bounds)
//	per instance: 12*f32 object->world, u32 blas, u32 pad
package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"

	"raytracer/internal/sceneio"
	"raytracer/internal/webgpu"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: accelgen <scene.toml> <out.bin>")
		os.Exit(2)
	}
	sc, err := sceneio.Load(os.Args[1])
	if err != nil {
		die(err)
	}
	pack, prims, blockers, ok := webgpu.AccelPackForScene(sc)
	if !ok {
		die(fmt.Errorf("scene has no instancing; the static-only path is not wired yet"))
	}

	f, err := os.Create(os.Args[2])
	if err != nil {
		die(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	put := func(v any) {
		if err := binary.Write(w, binary.LittleEndian, v); err != nil {
			die(err)
		}
	}
	put(uint32(0x314C4341)) // 'ACL1'
	put(uint32(len(pack.Geom)))
	put(uint32(len(pack.Blockers)))
	put(uint32(len(pack.Instances)))

	geomLeaves, blockerLeaves := 0, 0
	writeBLAS := func(bs []webgpu.AccelBLAS, total *int) {
		for _, b := range bs {
			put(uint32(len(b.Leaves)))
			*total += len(b.Leaves)
			for _, l := range b.Leaves {
				put(l.Prim)
				put(l.Min)
				put(l.Max)
			}
		}
	}
	writeBLAS(pack.Geom, &geomLeaves)
	writeBLAS(pack.Blockers, &blockerLeaves)
	for _, in := range pack.Instances {
		put(in.Xf)
		put(in.BLAS)
		put(uint32(0))
	}
	if err := w.Flush(); err != nil {
		die(err)
	}

	fmt.Printf("geom    %d structures, %d leaves\n", len(pack.Geom), geomLeaves)
	fmt.Printf("blocker %d structures, %d leaves\n", len(pack.Blockers), blockerLeaves)
	fmt.Printf("inst    %d placements (1 static + %d)\n", len(pack.Instances), len(pack.Instances)-1)
	fmt.Printf("prims   %d packed, blockers %d packed\n", len(prims), len(blockers))

	// The check worth making here rather than in Swift: every leaf index must
	// address a real primitive, because the intersection function will use it
	// verbatim and an out-of-range read is silent on the GPU.
	bad := 0
	for _, b := range pack.Geom {
		for _, l := range b.Leaves {
			if int(l.Prim) >= len(prims) {
				bad++
			}
		}
	}
	for _, b := range pack.Blockers {
		for _, l := range b.Leaves {
			if int(l.Prim) >= len(blockers) {
				bad++
			}
		}
	}
	if bad > 0 {
		die(fmt.Errorf("%d leaf indices out of range", bad))
	}
	fmt.Println("all leaf indices in range")
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "accelgen:", err)
	os.Exit(1)
}
