// mslbind makes naga's MSL bindable.
//
// With no binding map the naga CLI marks every resource argument
// [[user(fake0)]], which Metal will compile but nothing can bind. This assigns
// each kernel's arguments sequential [[buffer(n)]] indices so a harness can
// create the pipeline and, more importantly, so Xcode can show its pipeline
// statistics -- register use, occupancy and scratch, which is the one thing no
// API exposes.
//
//	naga --metal-version 3.0 trace_linked.wgsl /tmp/trace.metal
//	go run ./tools/metal-rt/mslbind /tmp/trace.metal /tmp/bound.metal
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	reKernel = regexp.MustCompile(`(?m)^(?:\[\[max_total_threads_per_threadgroup\(\d+\)\]\] )?kernel void (\w+)\(`)
	reFake   = regexp.MustCompile(`\[\[user\(fake\d+\)\]\]`)
	// The identifier just before the placeholder is naga's name for the
	// resource, which is the WGSL global's name. That is the only link back
	// from a Metal buffer index to a @group(0) @binding(n), because naga
	// numbers arguments in signature order and not by binding.
	reFakeNamed = regexp.MustCompile(`(\w+)(\s*)\[\[user\(fake\d+\)\]\]`)
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: mslbind <in.metal> <out.metal>")
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		die(err)
	}
	s := string(b)

	locs := reKernel.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		die(fmt.Errorf("no kernels found"))
	}
	var out strings.Builder
	out.WriteString(s[:locs[0][0]])
	for i, loc := range locs {
		end := len(s)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		name := s[loc[2]:loc[3]]
		// Only the signature, up to the opening brace of the body.
		body := strings.Index(s[loc[0]:end], ") {")
		if body < 0 {
			die(fmt.Errorf("%s: signature end not found", name))
		}
		sig, rest := s[loc[0]:loc[0]+body], s[loc[0]+body:end]
		n := 0
		sig = reFakeNamed.ReplaceAllStringFunc(sig, func(m string) string {
			sub := reFakeNamed.FindStringSubmatch(m)
			manifest[name] = append(manifest[name], argBinding{Index: n, Name: sub[1]})
			r := fmt.Sprintf("%s%s[[buffer(%d)]]", sub[1], sub[2], n)
			n++
			return r
		})
		// Anything the named form missed would compile and never bind, so it
		// is an error rather than a fallback.
		if left := reFake.FindString(sig); left != "" {
			die(fmt.Errorf("%s: unnamed resource argument %s", name, left))
		}
		fmt.Printf("%-20s %d buffer arguments\n", name, n)
		out.WriteString(sig)
		out.WriteString(rest)
	}
	if err := os.WriteFile(os.Args[2], []byte(out.String()), 0o644); err != nil {
		die(err)
	}
	// The manifest is what lets a harness bind dumped buffers to the right
	// Metal indices; see tools/metal-rt/harness.
	mf := os.Args[2] + ".json"
	j, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		die(err)
	}
	if err := os.WriteFile(mf, j, 0o644); err != nil {
		die(err)
	}
	fmt.Printf("wrote %s\n", mf)
}

type argBinding struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
}

var manifest = map[string][]argBinding{}

func die(err error) {
	fmt.Fprintln(os.Stderr, "mslbind:", err)
	os.Exit(1)
}
