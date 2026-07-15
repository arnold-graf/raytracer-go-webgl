package sceneio

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

const maxInstances = 2048

// expandInstancedInclude registers BLAS templates and TLAS placements for an
// [[include]] with instance = true. Layout files (only nested includes) expand
// recursively; leaf files become shared templates.
func expandInstancedInclude(dst *scene.Scene, inc includeDTO, parentDir string, parentXf *scene.Transform, seen map[string]bool, deps *[]string, followPlacements *[]scene.TerrainFollowPlacement) error {
	incPath := inc.File
	if !filepath.IsAbs(incPath) {
		incPath = filepath.Join(parentDir, incPath)
	}
	dto, resolved, err := decodeSceneFile(incPath, inc.Params)
	if err != nil {
		return err
	}

	// Terrain pads/features from layout files still merge into the parent.
	if err := mergeTerrainFromDTO(dst, dto, parentXf); err != nil {
		return err
	}

	for i, child := range dto.Include {
		childPath := child.File
		if !filepath.IsAbs(childPath) {
			childPath = filepath.Join(filepath.Dir(incPath), childPath)
		}
		childFollow := child.FollowTerrain
		childParams := mergeIncludeParams(resolved, child.Params)
		childSub, err := load(childPath, childParams, seen, deps, nil)
		if err != nil {
			return fmt.Errorf("include instance child[%d] %q: %w", i, child.File, err)
		}
		childXf, err := instanceTransformFromDTO(dst, child, childFollow, childSub)
		if err != nil {
			return fmt.Errorf("include instance child[%d] %q: %w", i, child.File, err)
		}
		childXf = parentXf.Compose(childXf)
		childDTO, _, err := decodeSceneFile(childPath, childParams)
		if err != nil {
			return fmt.Errorf("include instance child[%d] %q: %w", i, child.File, err)
		}
		if isLayoutDTO(childDTO) {
			if err := expandInstancedInclude(dst, child, filepath.Dir(incPath), childXf, seen, deps, followPlacements); err != nil {
				return fmt.Errorf("include instance child[%d] %q: %w", i, child.File, err)
			}
			continue
		}
		if err := registerInstancePlacement(dst, childPath, childParams, childXf, childFollow, child.At.toV().Y, seen, deps); err != nil {
			return fmt.Errorf("include instance child[%d] %q: %w", i, child.File, err)
		}
	}
	return nil
}

// registerLeafInstance registers a single template file as one TLAS placement.
func registerLeafInstance(dst *scene.Scene, inc includeDTO, parentDir string, seen map[string]bool, deps *[]string) error {
	incPath := inc.File
	if !filepath.IsAbs(incPath) {
		incPath = filepath.Join(parentDir, incPath)
	}
	dto, _, err := decodeSceneFile(incPath, inc.Params)
	if err != nil {
		return err
	}
	follow := inc.FollowTerrain
	if isLayoutDTO(dto) {
		sub, err := load(incPath, inc.Params, seen, deps, nil)
		if err != nil {
			return err
		}
		parentXf, err := instanceTransformForInclude(dst, inc, follow, sub)
		if err != nil {
			return err
		}
		return expandInstancedInclude(dst, inc, parentDir, parentXf, seen, deps, nil)
	}
	sub, err := dto.build()
	if err != nil {
		return err
	}
	xf, err := instanceTransformForInclude(dst, inc, follow, sub)
	if err != nil {
		return err
	}
	if err := mergeTerrainFromDTO(dst, dto, xf); err != nil {
		return err
	}
	return registerInstancePlacement(dst, incPath, inc.Params, xf, follow, inc.At.toV().Y, seen, deps)
}

func registerInstancePlacement(dst *scene.Scene, absPath string, params map[string]any, xf *scene.Transform, follow bool, yOffset float64, seen map[string]bool, deps *[]string) error {
	cat := dst.Instancing()
	if cat == nil {
		return fmt.Errorf("instancing catalog unavailable")
	}
	if len(cat.Placements) >= maxInstances {
		return fmt.Errorf("instance limit %d exceeded", maxInstances)
	}
	key := templateKey(absPath, params)
	tid, err := ensureTemplate(cat, key, absPath, params, seen, deps)
	if err != nil {
		return err
	}
	cat.Placements = append(cat.Placements, scene.InstancePlacement{
		TemplateIndex: tid,
		Xform:         xf,
		YOffset:       yOffset,
		FollowTerrain: follow,
	})
	return nil
}

func ensureTemplate(cat *scene.InstancingCatalog, key, absPath string, params map[string]any, seen map[string]bool, deps *[]string) (int, error) {
	for i, t := range cat.Templates {
		if t.Key == key {
			return i, nil
		}
	}
	tmpl, err := loadTemplateScene(absPath, params, seen, deps)
	if err != nil {
		return 0, err
	}
	cat.Templates = append(cat.Templates, scene.InstanceTemplate{
		Key:    key,
		Source: absPath,
		Scene:  tmpl,
	})
	return len(cat.Templates) - 1, nil
}

func loadTemplateScene(path string, params map[string]any, seen map[string]bool, deps *[]string) (*scene.Scene, error) {
	// Templates are leaf assets: load without propagating follow_terrain and
	// without merging nested includes into the parent (pine-tree has none).
	s, err := load(path, params, seen, deps, nil)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func templateKey(absPath string, params map[string]any) string {
	if len(params) == 0 {
		return absPath
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	h.Write([]byte(absPath))
	for _, k := range keys {
		h.Write([]byte(k))
		fmt.Fprintf(h, "=%v;", params[k])
	}
	return absPath + "|" + hex.EncodeToString(h.Sum(nil))[:16]
}

func isLayoutDTO(dto sceneDTO) bool {
	if len(dto.Include) == 0 {
		return false
	}
	return !dto.hasDirectGeometry()
}

func (dto sceneDTO) hasDirectGeometry() bool {
	return len(dto.Sphere) > 0 || len(dto.Plane) > 0 || len(dto.Box) > 0 ||
		len(dto.Cylinder) > 0 || len(dto.Cone) > 0 || len(dto.Torus) > 0 || len(dto.Ring) > 0 ||
		len(dto.Lens) > 0
}

func instanceTransformFromDTO(dst *scene.Scene, inc includeDTO, follow bool, sub *scene.Scene) (*scene.Transform, error) {
	at := inc.At.toV()
	if !follow {
		if h, ok := dst.TerrainHeightAt(at.X, at.Z); ok {
			at.Y = h + at.Y
		}
	}
	return buildIncludeTransform(inc, at, sub)
}

func mergeTerrainFromDTO(dst *scene.Scene, dto sceneDTO, xf *scene.Transform) error {
	s, err := dto.build()
	if err != nil {
		return err
	}
	mergeTerrainFromScene(dst, s, xf)
	return nil
}

func mergeTerrainFromScene(dst, sub *scene.Scene, xf *scene.Transform) {
	var pads []scene.TerrainPad
	var features []scene.TerrainFeature
	for i := range sub.Terrains {
		yaw := xf.YawRad()
		for _, p := range sub.Terrains[i].Pads {
			if xf != nil {
				c := xf.ToWorld(vec.New(p.CenterX, 0, p.CenterZ))
				p.CenterX, p.CenterZ = c.X, c.Z
				p.Angle += yaw
			}
			pads = append(pads, p)
		}
		for _, f := range sub.Terrains[i].Features {
			if xf != nil {
				w := xf.ToWorld(vec.New(f.PosX, 0, f.PosZ))
				f.PosX, f.PosZ = w.X, w.Z
				f.Angle += yaw
			}
			features = append(features, f)
		}
	}
	addTerrainPads(dst, pads)
	addTerrainFeatures(dst, features)
}

// mergeInstancingCatalog appends sub's instancing templates/placements into dst,
// composing each placement with the include transform xf.
func mergeInstancingCatalog(dst, sub *scene.Scene, xf *scene.Transform) {
	subCat := sub.Instancing()
	if subCat == nil || len(subCat.Placements) == 0 {
		return
	}
	dstCat := dst.Instancing()
	templateMap := make(map[int]int, len(subCat.Templates))
	for i, tmpl := range subCat.Templates {
		tid := -1
		for j, existing := range dstCat.Templates {
			if existing.Key == tmpl.Key {
				tid = j
				break
			}
		}
		if tid < 0 {
			dstCat.Templates = append(dstCat.Templates, tmpl)
			tid = len(dstCat.Templates) - 1
		}
		templateMap[i] = tid
	}
	for _, pl := range subCat.Placements {
		xfPl := pl.Xform
		if xf != nil {
			xfPl = xf.Compose(pl.Xform)
		}
		dstCat.Placements = append(dstCat.Placements, scene.InstancePlacement{
			TemplateIndex: templateMap[pl.TemplateIndex],
			Xform:         xfPl,
			YOffset:       pl.YOffset,
			FollowTerrain: pl.FollowTerrain,
		})
	}
}
