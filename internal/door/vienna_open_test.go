package door

import (
	"math"
	"path/filepath"
	"testing"

	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func TestViennaApartmentDoorOpens(t *testing.T) {
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
		t.Fatal("vienna_apartment_door agent not found")
	}
	p := &a.Panels[0]
	skip := mgr.skipBoxFunc()
	t.Logf("hinge=%v panelBox=%d axis=%s", p.Hinge, p.Geom.PrimaryBox(), a.Axis)

	for _, deg := range []float64{0, 1, 5, 15, 45, 90} {
		rad := deg * math.Pi / 180
		t.Logf("%.0f deg: blocked=%v", deg, PanelHitsStaticAt(sc, a, p, rad, skip))
	}

	player := vec.New(18.86, 4.65, 42.5)
	mgr.Toggle(sc, a.ID, player)
	for i := 0; i < 180; i++ {
		mgr.Update(sc, player, 0, 1.75, 1.0/60.0)
	}
	t.Logf("after toggle: state=%s angle=%.4f target=%.4f", a.State, p.Angle, p.Target)
	if math.Abs(p.Angle) < 0.1 {
		t.Fatalf("door did not open: angle=%.4f state=%s", p.Angle, a.State)
	}
}
