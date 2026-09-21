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
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	reKernel = regexp.MustCompile(`(?m)^(?:\[\[max_total_threads_per_threadgroup\(\d+\)\]\] )?kernel void (\w+)\(`)
	reFake   = regexp.MustCompile(`\[\[user\(fake\d+\)\]\]`)
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
		sig = reFake.ReplaceAllStringFunc(sig, func(string) string {
			r := fmt.Sprintf("[[buffer(%d)]]", n)
			n++
			return r
		})
		fmt.Printf("%-20s %d buffer arguments\n", name, n)
		out.WriteString(sig)
		out.WriteString(rest)
	}
	if err := os.WriteFile(os.Args[2], []byte(out.String()), 0o644); err != nil {
		die(err)
	}
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "mslbind:", err)
	os.Exit(1)
}
