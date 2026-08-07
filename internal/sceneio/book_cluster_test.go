package sceneio_test

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"raytracer/internal/sceneio"
)

func TestBookClusterLoadsWithVariedHeights(t *testing.T) {
	path := filepath.Join("..", "..", "scenes", "preview", "book-cluster.toml")
	s, err := sceneio.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(s.Boxes) < 12 {
		t.Fatalf("boxes = %d, want at least 12 (3+ books × 4 boxes)", len(s.Boxes))
	}

	// Collect distinct top Y values (book heights differ).
	tops := map[float64]bool{}
	for _, b := range s.Boxes {
		tops[math.Round(b.Max.Y*1000)/1000] = true
	}
	if len(tops) < 2 {
		t.Fatalf("expected varied book heights, got tops %v", tops)
	}

	// Paper blocks are inset; their X extent reflects per-book thickness.
	widths := map[float64]bool{}
	for _, b := range s.Boxes {
		w := math.Round((b.Max.X-b.Min.X)*1000) / 1000
		if w > 0.01 && w < 0.05 { // paper block thickness range
			widths[w] = true
		}
	}
	if len(widths) < 2 {
		t.Fatalf("expected varied book thicknesses, got %v", widths)
	}
}

func TestBookClusterSeedChangesLayout(t *testing.T) {
	cluster, err := filepath.Abs(filepath.Join("..", "..", "scenes", "objects", "book-cluster.toml"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.toml", `[[include]]
file = `+quote(cluster)+`
at = [0,0,0]
params = { seed = 1, width = 0.42 }
`)
	write("b.toml", `[[include]]
file = `+quote(cluster)+`
at = [0,0,0]
params = { seed = 99, width = 0.42 }
`)
	sa, err := sceneio.Load(filepath.Join(dir, "a.toml"))
	if err != nil {
		t.Fatalf("load a: %v", err)
	}
	sb, err := sceneio.Load(filepath.Join(dir, "b.toml"))
	if err != nil {
		t.Fatalf("load b: %v", err)
	}
	if len(sa.Boxes) != len(sb.Boxes) {
		t.Fatalf("box count mismatch %d vs %d", len(sa.Boxes), len(sb.Boxes))
	}
	same := true
	for i := range sa.Boxes {
		if sa.Boxes[i].Min != sb.Boxes[i].Min || sa.Boxes[i].Max != sb.Boxes[i].Max {
			same = false
			break
		}
	}
	if same {
		t.Fatal("different seeds should change book layout")
	}
}

func quote(s string) string {
	return `"` + s + `"`
}
