package door

import (
	"path/filepath"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func boxMid(b scene.Box) vec.V {
	return vec.New((b.Min.X+b.Max.X)/2, (b.Min.Y+b.Max.Y)/2, (b.Min.Z+b.Max.Z)/2)
}

func TestViennaDoorStaticHitBoxes(t *testing.T) {
	path := filepath.Join("..", "..", "scenes", "outdoors-night-villa.toml")
	sc, err := sceneio.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager()
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	var a *Agent
	for i := range mgr.agents {
		if mgr.agents[i].ID == "vienna_apartment_door" {
			a = &mgr.agents[i]
			break
		}
	}
	if a == nil {
		t.Fatal("no vienna door")
	}
	t.Logf("agent FrameBoxes=%v", a.FrameBoxes)
	p := &a.Panels[0]
	skip := mgr.skipBoxFunc()
	hits := panelStaticHits(sc, *p, skip)
	t.Logf("vienna closed panel hits %d static primitives: %v", len(hits), hits)
	for _, h := range hits {
		if h >= 0 && h < len(sc.Boxes) {
			b := sc.Boxes[h]
			c := b.Xform.ToWorld(boxMid(b))
			t.Logf("  hit box[%d] center=%v min=%v max=%v", h, c, b.Min, b.Max)
		}
	}

	// compare villa door (first instance)
	for i := range mgr.agents {
		if mgr.agents[i].ID != "villa_front_door" {
			continue
		}
		vp := &mgr.agents[i].Panels[0]
		vhits := panelStaticHits(sc, *vp, skip)
		t.Logf("villa hinge=%v panelBox=%d closed hits %d: %v", mgr.agents[i].Panels[0].Hinge, vp.Geom.PrimaryBox(), len(vhits), vhits)
		break
	}
}
