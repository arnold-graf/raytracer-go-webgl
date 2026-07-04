package webgpu

import (
	"path/filepath"
	"testing"

	"raytracer/internal/door"
	"raytracer/internal/sceneio"
)

func TestVillaDoorNotDuplicatedInGPU(t *testing.T) {
	path := filepath.Join("..", "..", "scenes", "outdoors-night-villa.toml")
	sc, err := sceneio.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	mgr := door.NewManager()
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	static, ok := sc.StaticPrimitiveCounts()
	if !ok {
		t.Fatal("expected instanced scene static counts")
	}
	staticScene := sliceStaticScene(sc, static)
	withDoorStatic := PackPrimitives(staticScene)
	withoutDoorStatic := packPrimitivesOmitDynamic(staticScene, sc)
	wantOmitted := 0
	for _, db := range sc.DynamicBodies {
		wantOmitted += db.Boxes[1] - db.Boxes[0]
	}
	gotOmitted := len(withDoorStatic) - len(withoutDoorStatic)
	if gotOmitted != wantOmitted {
		t.Fatalf("static pack omitted %d door boxes, want %d", gotOmitted, wantOmitted)
	}
}
