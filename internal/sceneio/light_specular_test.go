package sceneio

import (
	"os"
	"path/filepath"
	"testing"
)

func loadLightScene(t *testing.T, body string) []struct{ NoSpecular bool } {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "s.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]struct{ NoSpecular bool }, len(s.Lights))
	for i, l := range s.Lights {
		out[i].NoSpecular = l.NoSpecular
	}
	return out
}

// specular = false drops a light from highlights. Omitted must stay specular,
// because every scene written before this existed omits it.
func TestLightSpecularProp(t *testing.T) {
	got := loadLightScene(t, `
[[light]]
pos = [0.0, 1.0, 0.0]
color = [1.0, 1.0, 1.0]

[[light]]
pos = [1.0, 1.0, 0.0]
color = [1.0, 1.0, 1.0]
specular = false

[[light]]
pos = [2.0, 1.0, 0.0]
color = [1.0, 1.0, 1.0]
specular = true
`)
	if len(got) != 3 {
		t.Fatalf("got %d lights, want 3", len(got))
	}
	if got[0].NoSpecular {
		t.Error("omitted specular excluded the light from highlights; the default must be specular")
	}
	if !got[1].NoSpecular {
		t.Error("specular = false did not exclude the light")
	}
	if got[2].NoSpecular {
		t.Error("specular = true excluded the light")
	}
}
