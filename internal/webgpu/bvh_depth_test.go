package webgpu

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
)

// bvhDepth returns the maximum root-to-leaf depth of a packed BVH subtree,
// counting the root as depth 1. TLAS leaves stop the walk: the shader switches
// to a fresh stack when it descends into a BLAS.
func bvhDepth(nodes []GPUBVHNode, root uint32) int {
	if int(root) >= len(nodes) {
		return 0
	}
	n := nodes[root]
	if n.Info[3] > 0 {
		return 1
	}
	l := bvhDepth(nodes, n.Info[0])
	r := bvhDepth(nodes, n.Info[1])
	if r > l {
		l = r
	}
	return l + 1
}

type depthSample struct {
	what  string
	depth int
}

func sceneBVHDepths(sc *scene.Scene) []depthSample {
	var out []depthSample
	if prims, blockers, nodes, _, _, isp, _, ok := packInstancedScene(sc); ok {
		_, _ = prims, blockers
		out = append(out,
			depthSample{"static", bvhDepth(nodes, 0)},
			depthSample{"tlas", bvhDepth(nodes, isp.instNodeBase)},
			depthSample{"blockers", bvhDepth(nodes, isp.blockerSectionStart)},
			depthSample{"blocker-tlas", bvhDepth(nodes, isp.blockerInstBase)},
		)
		for i, t := range isp.templates {
			out = append(out,
				depthSample{fmt.Sprintf("blas[%d]", i), bvhDepth(nodes, t.BlasRoot)},
				depthSample{fmt.Sprintf("blocker-blas[%d]", i), bvhDepth(nodes, t.BlockerBlasRoot)},
			)
		}
		return out
	}
	if nodes := PackBVH(PackPrimitives(sc)); len(nodes) > 0 {
		out = append(out, depthSample{"static", bvhDepth(nodes, 0)})
	}
	if nodes := PackBVH(PackBlockers(sc)); len(nodes) > 0 {
		out = append(out, depthSample{"blockers", bvhDepth(nodes, 0)})
	}
	return out
}

// TestBVHTraversalStackDepth guards the WGSL traversal stack size. The shader
// walks each BVH depth-first with a fixed-size stack and silently drops nodes on
// overflow, which would make geometry vanish. A DFS that pushes both children
// then pops one never holds more than depth+1 pending entries, so every packed
// tree must stay inside bvhStackSize with room to spare.
func TestBVHTraversalStackDepth(t *testing.T) {
	paths, _ := filepath.Glob("../../scenes/*.toml")
	nested, _ := filepath.Glob("../../scenes/*/index.toml")
	paths = append(paths, nested...)
	sort.Strings(paths)

	worst, worstName := 0, ""
	measured := 0
	for _, path := range paths {
		sc, err := sceneio.Load(path)
		if err != nil {
			t.Logf("skip %s: %v", path, err)
			continue
		}
		for _, d := range sceneBVHDepths(sc) {
			measured++
			if d.depth > worst {
				worst, worstName = d.depth, path+" "+d.what
			}
			if d.depth+1 > bvhStackSize {
				t.Errorf("%s %s: BVH depth %d needs stack %d, have %d",
					path, d.what, d.depth, d.depth+1, bvhStackSize)
			}
		}
	}
	if measured == 0 {
		t.Fatal("no BVHs measured")
	}
	t.Logf("deepest of %d packed BVHs across %d scenes: %d levels (%s); stack size %d",
		measured, len(paths), worst, worstName, bvhStackSize)
}
