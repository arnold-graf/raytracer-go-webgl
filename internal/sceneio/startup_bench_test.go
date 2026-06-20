package sceneio

import (
	"testing"
	"time"

	"raytracer/internal/probe"
	"raytracer/internal/render"
	"raytracer/internal/webgpu"
)

func BenchmarkVillaStartupLoad(b *testing.B) {
	path := repoFile("scenes/outdoors-night-villa.toml")
	for i := 0; i < b.N; i++ {
		if _, err := Load(path); err != nil {
			b.Fatal(err)
		}
	}
}

func TestVillaStartupProfile(t *testing.T) {
	path := repoFile("scenes/outdoors-night-villa.toml")

	t0 := time.Now()
	sc, err := Load(path)
	load := time.Since(t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Terrains) > 0 {
		gnx, gnz := sc.Terrains[0].GridDimensions()
		t.Logf("terrain grid: %d x %d = %d cells", gnx, gnz, gnx*gnz)
	}

	t0 = time.Now()
	pb := probe.New(sc)
	probeNew := time.Since(t0)

	t0 = time.Now()
	aoData, aoOK := pb.BakeAO()
	bakeAO := time.Since(t0)
	if aoOK {
		cells := aoData.NX * aoData.NY * aoData.NZ
		t.Logf("AO volume: %dx%dx%d = %d cells", aoData.NX, aoData.NY, aoData.NZ, cells)
	}

	t0 = time.Now()
	ren, err := webgpu.New(512, 320)
	if err != nil {
		t.Skip("webgpu unavailable:", err)
	}
	defer ren.Release()
	webgpuInit := time.Since(t0)

	v := &render.View{Scene: sc, AOData: aoData, AOok: aoOK}
	buf := make([]byte, 512*320*4)
	t0 = time.Now()
	ren.Render(buf, nil, v, 1)
	firstFrame := time.Since(t0)

	total := load + probeNew + bakeAO + webgpuInit + firstFrame
	t.Logf("sceneio.Load:      %s", load)
	t.Logf("probe.New:         %s", probeNew)
	t.Logf("BakeAO:            %s", bakeAO)
	t.Logf("webgpu.New:        %s", webgpuInit)
	t.Logf("first Render:      %s", firstFrame)
	t.Logf("total (no window): %s", total)
}
