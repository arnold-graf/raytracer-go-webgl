package webgpu

import (
	"testing"

	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestRefitBVHAfterTransformMove(t *testing.T) {
	sc := &scene.Scene{
		Boxes: []scene.Box{{
			Min:     vec.V{},
			Max:     vec.V{X: 1, Y: 1, Z: 1},
			Surface: scene.Surface{Mat: scene.MatDiffuse, Xform: scene.NewRigidTransform(0, 0, 0, vec.V{})},
		}},
	}
	prims := PackPrimitives(sc)
	nodes := PackBVH(prims)
	rootMaxY := nodes[0].Max[1]

	sc.Boxes[0].Xform = scene.NewRigidTransform(0, 0, 0, vec.V{Y: 2})
	repackBox(sc, 0, &prims[0])
	RefitBVH(nodes, prims)

	if nodes[0].Max[1] <= rootMaxY+0.5 {
		t.Fatalf("refit BVH max Y = %v, expected rise after move (was %v)", nodes[0].Max[1], rootMaxY)
	}
}

func TestSceneCachePartialTransformUpdate(t *testing.T) {
	sc := &scene.Scene{
		Boxes: []scene.Box{{
			Min:     vec.V{},
			Max:     vec.V{X: 1, Y: 1, Z: 1},
			Surface: scene.Surface{Mat: scene.MatDiffuse, Xform: scene.NewRigidTransform(0, 0, 0, vec.V{})},
		}},
		DynamicBodies: []scene.DynamicBody{{Boxes: [2]int{0, 1}}},
	}
	v := &render.View{Scene: sc}
	var c sceneCache
	c.rebuild(v)
	gen0 := sc.Generation()
	x0 := sc.TransformGeneration()

	sc.Boxes[0].Xform = scene.NewRigidTransform(0, 0, 0, vec.V{X: 3})
	sc.TouchTransforms()

	if !c.fresh(v) {
		t.Fatal("geometry cache should remain fresh after TouchTransforms")
	}
	if c.transformsFresh(v) {
		t.Fatal("transform cache should be stale after TouchTransforms")
	}
	if sc.Generation() != gen0 {
		t.Fatal("TouchTransforms should not bump Generation")
	}

	c.updateDynamicTransforms(sc)
	if !c.transformsFresh(v) {
		t.Fatal("transform cache should be fresh after partial update")
	}
	if len(c.partialPrimSpans) == 0 {
		t.Fatal("expected partial prim spans")
	}
	if len(c.partialBlockerSpans) == 0 {
		t.Fatal("expected partial blocker spans")
	}
	if sc.TransformGeneration() <= x0 {
		t.Fatal("TransformGeneration should have advanced")
	}
}

func TestRebuildFlatSingleDynamicBox(t *testing.T) {
	sc := &scene.Scene{
		Boxes: []scene.Box{
			{Min: vec.V{}, Max: vec.V{X: 1}, Surface: scene.Surface{Mat: scene.MatDiffuse}},
			{Min: vec.V{X: 2}, Max: vec.V{X: 3}, Surface: scene.Surface{Mat: scene.MatDiffuse, Tex: 1}},
		},
		DynamicBodies: []scene.DynamicBody{{Boxes: [2]int{1, 2}}},
	}
	var c sceneCache
	c.rebuildFlat(sc)
	if len(c.prims) != 2 {
		t.Fatalf("prims = %d, want 2 (static box + one dynamic copy)", len(c.prims))
	}
	if gi, ok := c.layout.gpu.box[1]; !ok || gi != 1 {
		t.Fatalf("dynamic box GPU index = %v ok=%v, want 1", gi, ok)
	}
}
