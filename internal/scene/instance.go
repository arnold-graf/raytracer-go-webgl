package scene

import (
	"math"
	"path/filepath"

	"raytracer/internal/vec"
)

// InstancingCatalog holds shared geometry templates (BLAS) and world placements
// (TLAS leaves). Templates stay in local space; placements carry the composed
// world transform applied at load time (including terrain follow).
type InstancingCatalog struct {
	Templates  []InstanceTemplate
	Placements []InstancePlacement
}

// InstanceTemplate is one unique include file's geometry, built once and shared
// by every placement that references it.
type InstanceTemplate struct {
	Key    string // absolute path + params fingerprint
	Source string // absolute path to the template TOML
	Params map[string]any
	Scene  *Scene // geometry in template-local space
}

// InstancePlacement is one world instance of a template.
type InstancePlacement struct {
	TemplateIndex int
	Xform         *Transform
	// YOffset is added above terrain when FollowTerrain is true.
	YOffset       float64
	FollowTerrain bool
}

// InstanceFollow is deprecated: follow metadata lives on InstancePlacement.
type InstanceFollow struct {
	PlacementIndex int
	YOffset        float64
}

// Instancing returns the scene's instancing catalog, lazily allocated.
func (s *Scene) Instancing() *InstancingCatalog {
	if s == nil {
		return nil
	}
	if s.instancing == nil {
		s.instancing = &InstancingCatalog{}
	}
	return s.instancing
}

// HasInstancing reports whether the scene uses GPU instancing (templates were
// registered at load time and static geometry was not duplicated into slices).
func (s *Scene) HasInstancing() bool {
	return s != nil && s.instancing != nil && len(s.instancing.Placements) > 0
}

// FinalizeInstancing records static primitive counts, then materializes
// instances into the flat slices so CPU paths (AO, probe, physics) see the
// full world. The instancing catalog is kept for the GPU TLAS/BLAS packer.
func (s *Scene) FinalizeInstancing() {
	if s == nil || !s.HasInstancing() {
		return
	}
	s.staticCounts = CountPrimitives(s)
	s.staticCountsValid = true
	s.materializeInstances()
}

// StaticPrimitiveCounts returns the primitive slice lengths before instance
// geometry was materialized into the flat arrays. ok is false when the scene
// has no instancing or FinalizeInstancing has not run yet.
func (s *Scene) StaticPrimitiveCounts() (PrimitiveCounts, bool) {
	if s == nil || !s.staticCountsValid {
		return PrimitiveCounts{}, false
	}
	return s.staticCounts, true
}

// ApplyInstanceTerrainFollow adjusts placements that opt into follow_terrain.
func (s *Scene) ApplyInstanceTerrainFollow() {
	cat := s.Instancing()
	if s == nil || cat == nil {
		return
	}
	for i := range cat.Placements {
		p := &cat.Placements[i]
		if !p.FollowTerrain {
			continue
		}
		snapPlacementByOrigin(s, p, p.YOffset)
	}
}

func snapPlacementByOrigin(s *Scene, p *InstancePlacement, yOffset float64) {
	if p == nil || p.Xform == nil {
		return
	}
	anchor := p.Xform.PlacementAnchor()
	h, ok := s.TerrainHeightAt(anchor.X, anchor.Z)
	if !ok {
		return
	}
	dy := h + yOffset - anchor.Y
	if math.Abs(dy) < 1e-9 {
		return
	}
	shift := NewInstanceTransform(0, 0, 0, vec.V{Y: dy})
	p.Xform = shift.Compose(p.Xform)
}

func (s *Scene) materializeInstances() {
	cat := s.Instancing()
	if cat == nil {
		return
	}
	for pi, pl := range cat.Placements {
		if pl.TemplateIndex < 0 || pl.TemplateIndex >= len(cat.Templates) {
			continue
		}
		tmpl := cat.Templates[pl.TemplateIndex]
		if tmpl.Scene == nil {
			continue
		}
		before := CountPrimitives(s)
		iaBefore := len(s.Interactables)
		mergeSceneInto(s, tmpl.Scene, pl.Xform)
		after := CountPrimitives(s)
		iaAfter := len(s.Interactables)
		s.mergeInstanceReactive(tmpl, pi, before, after, iaBefore, iaAfter, pl.Xform)
	}
}

func (s *Scene) mergeInstanceReactive(tmpl InstanceTemplate, placementIndex int, before, after PrimitiveCounts, iaBefore, iaAfter int, xf *Transform) {
	if s == nil || tmpl.Scene == nil || tmpl.Scene.Reactive == nil {
		return
	}
	if s.Reactive == nil {
		s.Reactive = &ReactiveSpec{}
	}
	scope := InstanceScopeID(tmpl.Source, placementIndex)
	for _, frag := range tmpl.Scene.Reactive.Fragments {
		frag.SourcePath = tmpl.Source
		if len(tmpl.Params) > 0 {
			frag.IncludeProps = tmpl.Params
		}
		if frag.ScopeID == "" {
			frag.ScopeID = ScopeIDForPath(tmpl.Source)
		}
		frag.ScopeID = scope
		frag.Instanced = true
		frag.Span = SpanFromMerge(before, after, iaBefore, iaAfter)
		if xf != nil {
			t := *xf
			frag.Transform = &t
		}
		s.Reactive.MergeFragment(frag, 0)
	}
}

// TemplateWorldBounds returns the world-space AABB enclosing all finite
// geometry of template under placement xf.
func TemplateWorldBounds(tmpl *Scene, xf *Transform) (min, max vec.V, ok bool) {
	if tmpl == nil {
		return vec.V{}, vec.V{}, false
	}
	first := true
	accum := func(lmin, lmax vec.V) {
		wmin, wmax := lmin, lmax
		if xf != nil {
			wmin, wmax = transformAABB(xf, lmin, lmax)
		}
		if first {
			min, max = wmin, wmax
			first = false
		} else {
			min = minV(min, wmin)
			max = maxV(max, wmax)
		}
	}
	forEachTemplateBounds(tmpl, false, accum)
	return min, max, !first
}

// ForEachTemplateBlockerBounds calls fn for each shadow-casting template prim.
func ForEachTemplateBlockerBounds(tmpl *Scene, fn func(lmin, lmax vec.V)) {
	forEachTemplateBounds(tmpl, true, fn)
}

func forEachTemplateBounds(tmpl *Scene, blockersOnly bool, fn func(lmin, lmax vec.V)) {
	for i := range tmpl.Spheres {
		sp := &tmpl.Spheres[i]
		if blockersOnly && (sp.Mat == MatEmit || sp.Mat == MatGlass) {
			continue
		}
		r := sp.Radius
		lmin := sp.Center.Sub(vec.V{X: r, Y: r, Z: r})
		lmax := sp.Center.Add(vec.V{X: r, Y: r, Z: r})
		if sp.Xform != nil {
			lmin, lmax = transformAABB(sp.Xform, lmin, lmax)
		}
		fn(lmin, lmax)
	}
	for i := range tmpl.Boxes {
		bx := &tmpl.Boxes[i]
		if blockersOnly && bx.Mat == MatGlass {
			continue
		}
		lmin, lmax := bx.Min, bx.Max
		if bx.Xform != nil {
			lmin, lmax = transformAABB(bx.Xform, lmin, lmax)
		}
		fn(lmin, lmax)
	}
	for i := range tmpl.Cylinders {
		cy := &tmpl.Cylinders[i]
		if blockersOnly && cy.Mat == MatGlass {
			continue
		}
		r := cy.MaxRadius()
		lmin := vec.V{X: cy.CX - r, Y: cy.YMin, Z: cy.CZ - r}
		lmax := vec.V{X: cy.CX + r, Y: cy.YMax, Z: cy.CZ + r}
		if cy.Xform != nil {
			lmin, lmax = transformAABB(cy.Xform, lmin, lmax)
		}
		fn(lmin, lmax)
	}
	for i := range tmpl.Cones {
		co := &tmpl.Cones[i]
		if blockersOnly && co.Mat == MatGlass {
			continue
		}
		lmin := vec.V{X: co.CX - co.RBase, Y: co.YBase, Z: co.CZ - co.RBase}
		lmax := vec.V{X: co.CX + co.RBase, Y: co.YTip, Z: co.CZ + co.RBase}
		if co.Xform != nil {
			lmin, lmax = transformAABB(co.Xform, lmin, lmax)
		}
		fn(lmin, lmax)
	}
	if !blockersOnly {
		for i := range tmpl.Tori {
			t := &tmpl.Tori[i]
			rxz := t.R + t.Rm
			lmin := vec.V{X: t.Center.X - rxz, Y: t.Center.Y - t.Rm, Z: t.Center.Z - rxz}
			lmax := vec.V{X: t.Center.X + rxz, Y: t.Center.Y + t.Rm, Z: t.Center.Z + rxz}
			if t.Xform != nil {
				lmin, lmax = transformAABB(t.Xform, lmin, lmax)
			}
			fn(lmin, lmax)
		}
	}
	for i := range tmpl.Rings {
		rg := &tmpl.Rings[i]
		if blockersOnly && rg.Mat == MatGlass {
			continue
		}
		sh := rg.Shell()
		half := rg.HalfHeight()
		lmin := vec.V{X: rg.CX - rg.Radius - sh, Y: rg.CY - half - sh, Z: rg.CZ - rg.Radius - sh}
		lmax := vec.V{X: rg.CX + rg.Radius + sh, Y: rg.CY + half + sh, Z: rg.CZ + rg.Radius + sh}
		if rg.Xform != nil {
			lmin, lmax = transformAABB(rg.Xform, lmin, lmax)
		}
		fn(lmin, lmax)
	}
	for i := range tmpl.Lenses {
		ln := &tmpl.Lenses[i]
		if blockersOnly && ln.Mat == MatGlass {
			continue
		}
		lmin, lmax := ln.WorldBounds()
		if ln.Xform != nil {
			lmin, lmax = transformAABB(ln.Xform, lmin, lmax)
		}
		fn(lmin, lmax)
	}
}

func transformAABB(xf *Transform, lmin, lmax vec.V) (vec.V, vec.V) {
	wmin := vec.V{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)}
	wmax := vec.V{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}
	for _, dx := range [2]float64{0, 1} {
		for _, dy := range [2]float64{0, 1} {
			for _, dz := range [2]float64{0, 1} {
				c := xf.ToWorld(vec.V{
					X: lmin.X + dx*(lmax.X-lmin.X),
					Y: lmin.Y + dy*(lmax.Y-lmin.Y),
					Z: lmin.Z + dz*(lmax.Z-lmin.Z),
				})
				wmin = minV(wmin, c)
				wmax = maxV(wmax, c)
			}
		}
	}
	return wmin, wmax
}

func minV(a, b vec.V) vec.V {
	return vec.V{X: math.Min(a.X, b.X), Y: math.Min(a.Y, b.Y), Z: math.Min(a.Z, b.Z)}
}

func maxV(a, b vec.V) vec.V {
	return vec.V{X: math.Max(a.X, b.X), Y: math.Max(a.Y, b.Y), Z: math.Max(a.Z, b.Z)}
}

// mergeSceneInto appends template geometry into dst with composed transforms.
// It mirrors sceneio.mergeScene but lives here so instancing materialization
// does not import sceneio.
func mergeSceneInto(dst, sub *Scene, xf *Transform) {
	boxOffset := len(dst.Boxes)
	for i := range sub.Spheres {
		o := sub.Spheres[i]
		o.Xform = xf.Compose(o.Xform)
		dst.Spheres = append(dst.Spheres, o)
	}
	for i := range sub.Planes {
		o := sub.Planes[i]
		o.Xform = xf.Compose(o.Xform)
		if xf != nil {
			pp := planePointFromNormal(o.N, o.D)
			o.N = xf.WorldNormal(o.N)
			o.D = -o.N.Dot(xf.ToWorld(pp))
		}
		dst.Planes = append(dst.Planes, o)
	}
	for i := range sub.Boxes {
		o := sub.Boxes[i]
		o.Xform = xf.Compose(o.Xform)
		dst.Boxes = append(dst.Boxes, o)
	}
	for i := range sub.Cylinders {
		o := sub.Cylinders[i]
		o.Xform = xf.Compose(o.Xform)
		dst.Cylinders = append(dst.Cylinders, o)
	}
	for i := range sub.Cones {
		o := sub.Cones[i]
		o.Xform = xf.Compose(o.Xform)
		dst.Cones = append(dst.Cones, o)
	}
	for i := range sub.Tori {
		o := sub.Tori[i]
		o.Xform = xf.Compose(o.Xform)
		dst.Tori = append(dst.Tori, o)
	}
	for i := range sub.Rings {
		o := sub.Rings[i]
		o.Xform = xf.Compose(o.Xform)
		dst.Rings = append(dst.Rings, o)
	}
	for i := range sub.Lenses {
		o := sub.Lenses[i]
		o.Xform = xf.Compose(o.Xform)
		dst.Lenses = append(dst.Lenses, o)
	}
	for i := range sub.Lights {
		l := sub.Lights[i]
		if xf != nil {
			l.Pos = xf.ToWorld(l.Pos)
		}
		dst.Lights = append(dst.Lights, l)
	}
	for i := range sub.Campfires {
		c := sub.Campfires[i]
		if xf != nil {
			c.Center = xf.ToWorld(c.Center)
		}
		dst.Campfires = append(dst.Campfires, c)
	}
	for i := range sub.Ambiences {
		a := sub.Ambiences[i]
		if xf != nil {
			a.Pos = xf.ToWorld(a.Pos)
		}
		dst.Ambiences = append(dst.Ambiences, a)
	}
	dst.MergeInteractables(sub, boxOffset)
}

// CloneLocalScene deep-copies finite geometry and interactables without applying
// a placement transform. Used to refresh instancing BLAS templates after reactive edits.
func CloneLocalScene(src *Scene) *Scene {
	if src == nil {
		return nil
	}
	dst := &Scene{}
	mergeSceneInto(dst, src, nil)
	if src.Reactive != nil {
		spec := *src.Reactive
		frags := append([]ReactiveFragment(nil), spec.Fragments...)
		spec.Fragments = frags
		dst.Reactive = &spec
	}
	return dst
}

// UpdateInstanceTemplate replaces a shared BLAS template when reactive state
// re-expands an instanced object file. The GPU renders from templates, not the
// materialized flat slices that SpliceFragment edits.
func (s *Scene) UpdateInstanceTemplate(source string, params map[string]any, local *Scene) bool {
	cat := s.Instancing()
	if cat == nil || local == nil || source == "" {
		return false
	}
	for i := range cat.Templates {
		t := &cat.Templates[i]
		if !templateSourceMatch(t.Source, source) {
			continue
		}
		if !instanceParamsMatch(t.Params, params) {
			continue
		}
		cat.Templates[i].Scene = CloneLocalScene(local)
		return true
	}
	return false
}

func templateSourceMatch(a, b string) bool {
	if a == b {
		return true
	}
	return filepath.Clean(a) == filepath.Clean(b) ||
		filepath.Base(a) == filepath.Base(b)
}

func instanceParamsMatch(a, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || !paramValuesEqual(av, bv) {
			return false
		}
	}
	return true
}

func paramValuesEqual(a, b any) bool {
	if a == b {
		return true
	}
	af, afok := paramAsFloat(a)
	bf, bfok := paramAsFloat(b)
	if afok && bfok {
		return math.Abs(af-bf) <= 1e-9
	}
	return false
}

func paramAsFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	default:
		return 0, false
	}
}

func planePointFromNormal(n vec.V, d float64) vec.V {
	if n.LenSq() < 1e-12 {
		return vec.V{}
	}
	return n.Scale(-d / n.LenSq())
}
