package sceneio

import (
	"fmt"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// transformOriginDTO decodes transform_origin = "center" or [x, y, z].
type transformOriginDTO struct {
	center   bool
	point    vec.V
	explicit bool
}

func (o *transformOriginDTO) UnmarshalTOML(data interface{}) error {
	switch v := data.(type) {
	case string:
		if v == "center" {
			o.center = true
			return nil
		}
		return fmt.Errorf("transform_origin: unknown string %q (use \"center\" or [x, y, z])", v)
	case []interface{}:
		if len(v) != 3 {
			return fmt.Errorf("transform_origin: want [x, y, z]")
		}
		o.explicit = true
		for i, item := range v {
			f, err := tomlFloat(item)
			if err != nil {
				return fmt.Errorf("transform_origin: index %d: %w", i, err)
			}
			switch i {
			case 0:
				o.point.X = f
			case 1:
				o.point.Y = f
			case 2:
				o.point.Z = f
			}
		}
		return nil
	case []int64:
		if len(v) != 3 {
			return fmt.Errorf("transform_origin: want [x, y, z]")
		}
		o.explicit = true
		o.point = vec.New(float64(v[0]), float64(v[1]), float64(v[2]))
		return nil
	case []float64:
		if len(v) != 3 {
			return fmt.Errorf("transform_origin: want [x, y, z]")
		}
		o.explicit = true
		o.point = vec.New(v[0], v[1], v[2])
		return nil
	default:
		return fmt.Errorf("transform_origin: want \"center\" or [x, y, z]")
	}
}

func (o *transformOriginDTO) resolve(defaultCenter vec.V) vec.V {
	if o == nil || o.center {
		return defaultCenter
	}
	if o.explicit {
		return o.point
	}
	return defaultCenter
}

func (o *transformOriginDTO) isExplicitOrigin() bool {
	return o != nil && o.explicit
}

// transformDTO holds optional per-primitive rotation (degrees) about
// transform_origin (default: geometric center).
type transformDTO struct {
	RotateX         float64             `toml:"rotate_x"`
	RotateY         float64             `toml:"rotate_y"`
	RotateZ         float64             `toml:"rotate_z"`
	TransformOrigin *transformOriginDTO `toml:"transform_origin"`
}

func (t transformDTO) buildPlacement(defaultCenter vec.V) *scene.Transform {
	origin := t.TransformOrigin.resolve(defaultCenter)
	if t.RotateX == 0 && t.RotateY == 0 && t.RotateZ == 0 {
		return nil
	}
	return scene.PlacementTransform(t.RotateX, t.RotateY, t.RotateZ, origin, origin)
}

// OriginPivotInclude reports whether an included file should keep local (0,0,0)
// as transform_origin (stairs, trees authored at file origin).
func OriginPivotInclude(file string) bool {
	base := filepathBase(file)
	switch base {
	case "staircase.toml", "pine-tree.toml":
		return true
	default:
		return false
	}
}

func filepathBase(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[i+1:]
		}
	}
	return path
}

func tomlFloat(v interface{}) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case int64:
		return float64(n), nil
	case int:
		return float64(n), nil
	default:
		return 0, fmt.Errorf("not a number")
	}
}

func (inc includeDTO) HasExplicitTransformOrigin() bool {
	return inc.TransformOrigin != nil && inc.TransformOrigin.isExplicitOrigin()
}

func (inc includeDTO) resolvedOrigin(sub *scene.Scene) (vec.V, error) {
	if inc.TransformOrigin != nil && inc.TransformOrigin.isExplicitOrigin() {
		return inc.TransformOrigin.point, nil
	}
	if OriginPivotInclude(inc.File) {
		return vec.V{}, nil
	}
	if sub == nil {
		return vec.V{}, fmt.Errorf("sub-scene required to resolve center transform_origin")
	}
	c, ok := scene.LocalBoundsCenter(sub)
	if !ok {
		return vec.V{}, nil
	}
	return c, nil
}

func buildIncludeTransform(inc includeDTO, at vec.V, sub *scene.Scene) (*scene.Transform, error) {
	origin, err := inc.resolvedOrigin(sub)
	if err != nil {
		return nil, err
	}
	return scene.PlacementTransform(inc.RotateX, inc.RotateY, inc.RotateZ, at, origin), nil
}

// MigratedIncludeAt computes the updated at for center-default includes.
func MigratedIncludeAt(oldAt vec.V, inc includeDTO, sub *scene.Scene) (vec.V, bool, error) {
	if inc.TransformOrigin != nil && inc.TransformOrigin.isExplicitOrigin() {
		return oldAt, false, nil
	}
	if OriginPivotInclude(inc.File) {
		return oldAt, true, nil
	}
	c, ok := scene.LocalBoundsCenter(sub)
	if !ok {
		return oldAt, false, fmt.Errorf("no finite geometry")
	}
	return scene.MigratedIncludeAt(oldAt, inc.RotateX, inc.RotateY, inc.RotateZ, c), false, nil
}
