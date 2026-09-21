// mslpatch generates the Metal backend's shader from the same WGSL the WebGPU
// backend uses, replacing only the BVH traversal with Apple's intersector.
//
// Shading, the penumbra filter, the reflection filter and adaptive AA are all
// naga's output from shaders/modules/*.wesl — there is no second copy of them.
// See docs/metal-backend.md.
//
// The mechanism worth understanding is how the acceleration structure reaches
// nearest_hit. MSL has no module-scope resources, so naga threads every binding
// through as a parameter to whatever transitively uses it. So this injects a
// dummy storage binding into the WGSL, referenced only inside nearest_hit, and
// naga does the threading for us — into nearest_hit, ray_color, supersample_edge
// and the kernel entry points, with every call site passing it by name. Then the
// patch only has to change that parameter's *type*. No call site is touched.
//
//	go run ./tools/metal-rt/mslpatch \
//	    internal/webgpu/shaders/trace_linked.wgsl /tmp/trace.metal
package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// rtBinding is a group-0 slot the WGSL does not use. It exists only so naga
// threads a parameter along the call chain; the patch then retypes it.
const rtBinding = 30

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: mslpatch <linked.wgsl> <out.metal>")
		os.Exit(2)
	}
	src, err := os.ReadFile(os.Args[1])
	if err != nil {
		die(err)
	}
	wgsl, err := injectHandle(string(src))
	if err != nil {
		die(err)
	}
	tmp, err := os.CreateTemp("", "mslpatch-*.wgsl")
	if err != nil {
		die(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(wgsl); err != nil {
		die(err)
	}
	tmp.Close()

	msl := strings.TrimSuffix(tmp.Name(), ".wgsl") + ".metal"
	defer os.Remove(msl)
	// metal1.0, naga's default target, predates the raytracing headers.
	out, err := exec.Command(nagaBin(), "--metal-version", "3.0", tmp.Name(), msl).CombinedOutput()
	if err != nil {
		die(fmt.Errorf("naga: %v\n%s", err, out))
	}
	gen, err := os.ReadFile(msl)
	if err != nil {
		die(err)
	}
	patched, err := patch(string(gen))
	if err != nil {
		die(err)
	}
	if err := os.WriteFile(os.Args[2], []byte(patched), 0o644); err != nil {
		die(err)
	}
	fmt.Printf("wrote %s (%d lines)\n", os.Args[2], strings.Count(patched, "\n"))
}

func nagaBin() string {
	if p, err := exec.LookPath("naga"); err == nil {
		return p
	}
	return os.ExpandEnv("$HOME/.cargo/bin/naga")
}

// injectHandle adds the dummy binding and one reference to it inside
// nearest_hit, which is what makes naga thread it down the call chain.
func injectHandle(s string) (string, error) {
	decl := fmt.Sprintf("@group(0) @binding(%d)\nvar<storage, read> rt_handle: array<u32>;\n\n", rtBinding)
	anchor := "@group(0) @binding(18)"
	if strings.Count(s, anchor) != 1 {
		return "", fmt.Errorf("anchor %q appears %d times", anchor, strings.Count(s, anchor))
	}
	s = strings.Replace(s, anchor, decl+anchor, 1)

	// One reference per traversal entry point. Never true, but naga cannot know
	// that, so the binding stays live and gets threaded to both call chains --
	// nearest_hit for view rays, blocker_bvh_any_hit for shadow rays.
	for _, e := range entryPoints {
		i := strings.Index(s, e.wgslDecl)
		if i < 0 {
			return "", fmt.Errorf("%s not found in the linked WGSL", e.name)
		}
		j := strings.Index(s[i:], "{")
		if j < 0 {
			return "", fmt.Errorf("%s body not found", e.name)
		}
		j += i + 1
		s = s[:j] + "\n  if (rt_handle[0] == 0xdeadbeefu) { return " + e.wgslZero + "; }\n" + s[j:]
	}
	return s, nil
}

// The two traversal entry points Metal's intersector replaces. Everything else
// in the shader is naga's output, unmodified.
var entryPoints = []struct {
	name     string
	wgslDecl string
	wgslZero string
}{
	{"nearest_hit", "fn nearest_hit(", "Hit(0.0, 0u, 0u, 0u)"},
	{"blocker_bvh_any_hit", "fn blocker_bvh_any_hit(", "vec2<f32>(-1.0, 1.0)"},
}

var (
	// naga names the parameter after the binding and threads it by reference.
	reHandleParam = regexp.MustCompile(`device type_\d+ const& rt_handle`)
	// On a kernel entry point it carries a placeholder attribute, because the
	// CLI has no binding map. A real backend supplies one; here it becomes an
	// argument buffer.
	reHandleEntry = regexp.MustCompile(`device type_\d+ const& rt_handle \[\[user\(fake\d+\)\]\]`)
	reNearestHit  = regexp.MustCompile(`(?s)\nHit nearest_hit\(\n    metal::float3 (\w+),\n    metal::float3 (\w+),`)
	reBlocker     = regexp.MustCompile(`(?s)\nmetal::float2 blocker_bvh_any_hit\(\n    metal::float3 (\w+),\n    metal::float3 (\w+),\n    float (\w+),`)
)

const rtStruct = `
// Supplied by the Metal backend as an argument buffer: the scene's acceleration
// structure and the table of per-primitive intersection functions. This replaces
// the dummy storage binding the WGSL declared purely to make naga thread a
// parameter down to nearest_hit.
struct RTHandle {
    metal::raytracing::primitive_acceleration_structure accel;
    metal::raytracing::intersection_function_table<> table;
};
`

func patch(s string) (string, error) {
	if !strings.Contains(s, "rt_handle") {
		return "", fmt.Errorf("naga did not thread rt_handle; the injected reference was optimized away")
	}
	m := reNearestHit.FindStringSubmatch(s)
	if m == nil {
		return "", fmt.Errorf("could not locate nearest_hit's generated signature")
	}
	roName, rdName := m[1], m[2]

	// The raytracing header, and the handle type.
	s = strings.Replace(s, "#include <metal_stdlib>",
		"#include <metal_stdlib>\n#include <metal_raytracing>", 1)
	anchor := "using metal::uint;"
	if !strings.Contains(s, anchor) {
		return "", fmt.Errorf("naga preamble changed: %q not found", anchor)
	}
	s = strings.Replace(s, anchor, anchor+"\n"+rtStruct, 1)

	// Retype the threaded parameter everywhere. Entry points first, so their
	// attribute is rewritten rather than left dangling.
	s = reHandleEntry.ReplaceAllString(s, fmt.Sprintf("constant RTHandle& rt_handle [[buffer(%d)]]", rtBinding))
	s = reHandleParam.ReplaceAllString(s, "constant RTHandle& rt_handle")

	s, err := replaceBody(s, "\nHit nearest_hit(", nearestBody(roName, rdName))
	if err != nil {
		return "", err
	}
	bm := reBlocker.FindStringSubmatch(s)
	if bm == nil {
		return "", fmt.Errorf("could not locate blocker_bvh_any_hit's generated signature")
	}
	s, err = replaceBody(s, "\nmetal::float2 blocker_bvh_any_hit(", blockerBody(bm[1], bm[2], bm[3]))
	if err != nil {
		return "", err
	}
	return s, nil
}

func nearestBody(ro, rd string) string {
	return fmt.Sprintf(`
    metal::raytracing::ray r;
    r.origin = %s;
    r.direction = %s;
    r.min_distance = 1e-4f;
    r.max_distance = 1e30f;
    metal::raytracing::intersector<> isect;
    auto res = isect.intersect(r, rt_handle.accel, rt_handle.table);
    Hit out;
    if (res.type == metal::raytracing::intersection_type::none) {
        out.t = 1e30f;
        out.idx = 0xffffffffu;
    } else {
        out.t = res.distance;
        out.idx = res.primitive_id;
    }
    out.kind = 0u;
    out.inst_idx = 0xffffffffu;
    return out;
`, ro, rd)
}

// replaceBody swaps the body of the function whose definition starts at head,
// keeping its generated signature intact.
func replaceBody(s, head, body string) (string, error) {
	i := strings.Index(s, head)
	if i < 0 {
		return "", fmt.Errorf("function %q not found", strings.TrimSpace(head))
	}
	open := strings.Index(s[i:], ") {")
	if open < 0 {
		return "", fmt.Errorf("body of %q not found", strings.TrimSpace(head))
	}
	open += i + len(") {")
	depth := 1
	j := open
	for ; j < len(s) && depth > 0; j++ {
		switch s[j] {
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	if depth != 0 {
		return "", fmt.Errorf("unbalanced braces in %q", strings.TrimSpace(head))
	}
	return s[:open] + body + s[j-1:], nil
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "mslpatch:", err)
	os.Exit(1)
}

// blockerBody is the shadow-ray half. The WGSL returns (distance, transmission);
// any-hit is all this needs, so the intersector is told to stop at the first
// acceptance rather than search for the nearest.
func blockerBody(ro, rd, maxT string) string {
	return fmt.Sprintf(`
    metal::raytracing::ray r;
    r.origin = %s;
    r.direction = %s;
    r.min_distance = 1e-4f;
    r.max_distance = %s - 0.05f;
    metal::raytracing::intersector<> isect;
    isect.accept_any_intersection(true);
    auto res = isect.intersect(r, rt_handle.accel, rt_handle.table);
    if (res.type == metal::raytracing::intersection_type::none) {
        return metal::float2(-1.0f, 1.0f);
    }
    return metal::float2(res.distance, 0.0f);
`, ro, rd, maxT)
}
