package sceneio

import "fmt"

// placementDTO resolves pos_* (minimum corner / anchor) vs center_* (geometric
// center) per axis. Each axis accepts at most one of pos_* or center_*; axes
// may be mixed (e.g. pos_x with center_y and center_z).
type placementDTO struct {
	PosX    *float64 `toml:"pos_x"`
	PosY    *float64 `toml:"pos_y"`
	PosZ    *float64 `toml:"pos_z"`
	CenterX *float64 `toml:"center_x"`
	CenterY *float64 `toml:"center_y"`
	CenterZ *float64 `toml:"center_z"`
}

func (p placementDTO) validate() error {
	if p.PosX != nil && p.CenterX != nil {
		return fmt.Errorf("cannot set both pos_x and center_x")
	}
	if p.PosY != nil && p.CenterY != nil {
		return fmt.Errorf("cannot set both pos_y and center_y")
	}
	if p.PosZ != nil && p.CenterZ != nil {
		return fmt.Errorf("cannot set both pos_z and center_z")
	}
	return nil
}

// corner returns the minimum-corner / anchor position from placement and positive
// extents along +X, +Y, +Z.
func (p placementDTO) corner(extX, extY, extZ float64) (x, y, z float64, err error) {
	if err = p.validate(); err != nil {
		return 0, 0, 0, err
	}
	if p.PosX != nil {
		x = *p.PosX
	} else if p.CenterX != nil {
		x = *p.CenterX - extX/2
	}
	if p.PosY != nil {
		y = *p.PosY
	} else if p.CenterY != nil {
		y = *p.CenterY - extY/2
	}
	if p.PosZ != nil {
		z = *p.PosZ
	} else if p.CenterZ != nil {
		z = *p.CenterZ - extZ/2
	}
	return x, y, z, nil
}
