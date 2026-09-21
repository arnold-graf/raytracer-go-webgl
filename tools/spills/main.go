// spills reports where the megakernel spills registers to thread scratch.
//
// Scratch is the one resource that has repeatedly moved this renderer -- taking
// an array out of box_holed_nearest was 14%, sizing the traversal stacks 7% --
// and no Metal API exposes it. tools/occupancy measures registers and is blind
// to it; frame-time A/B on a constant was the only instrument we had.
//
// It turns out the AGX backend emits LLVM register-allocator remarks, and a GPU
// frame capture carries them. They are YAML, they name the function, they count
// spills and reloads, and they carry a source line. That is a targeting
// instrument, not just a number.
//
//	naga --metal-version 3.0 internal/webgpu/shaders/trace_linked.wgsl /tmp/t.metal
//	go run ./tools/metal-rt/mslbind /tmp/t.metal /tmp/bound.metal
//	swiftc -O tools/metal-rt/capture.swift -o /tmp/capture
//	MTL_CAPTURE_ENABLED=1 /tmp/capture /tmp/bound.metal /tmp/trace.gputrace
//	go run ./tools/spills /tmp/trace.gputrace /tmp/bound.metal
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type site struct {
	line            int
	spills, reloads int
	cost            float64
}

var (
	reRemark  = regexp.MustCompile(`^--- !(Missed|Passed|Analysis)`)
	reLine    = regexp.MustCompile(`Line:\s+(\d+)`)
	reSpills  = regexp.MustCompile(`NumSpills:\s+'(\d+)'`)
	reReloads = regexp.MustCompile(`NumReloads:\s+'(\d+)'`)
	reCost    = regexp.MustCompile(`TotalSpillsCost:\s+'([\d.eE+-]+)'`)
	reFunc    = regexp.MustCompile(`^(?:\[\[max_total_threads_per_threadgroup\(\d+\)\]\] )?(?:kernel void|[A-Za-z_][\w:<>, ]*?)\s+(\w+)\(`)
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: spills <trace.gputrace> <bound.metal>")
		os.Exit(2)
	}
	msl := readLines(os.Args[2])
	sites := parse(os.Args[1])
	if len(sites) == 0 {
		fmt.Fprintln(os.Stderr, "no regalloc remarks found; was the capture made with MTL_CAPTURE_ENABLED=1?")
		os.Exit(1)
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].cost > sites[j].cost })

	var spills, reloads int
	var cost float64
	for _, s := range sites {
		spills += s.spills
		reloads += s.reloads
		cost += s.cost
	}
	fmt.Printf("total: %d spills, %d reloads, cost %.0f\n\n", spills, reloads, cost)
	fmt.Printf("%9s %8s %8s %10s %7s  %s\n", "MSL line", "spills", "reloads", "cost", "share", "enclosing function")
	for i, s := range sites {
		if i == 12 {
			break
		}
		fmt.Printf("%9d %8d %8d %10.0f %6.1f%%  %s\n",
			s.line, s.spills, s.reloads, s.cost, 100*s.cost/cost, owner(msl, s.line))
	}
}

// parse pulls the regalloc remarks out of the capture. The trace is an opaque
// package, but the remarks are plain text inside it, so this scans printable
// runs the way strings(1) does.
func parse(trace string) []site {
	agg := map[int]*site{}
	filepath.Walk(trace, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || fi.Size() < 1<<10 {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		var cur strings.Builder
		var block strings.Builder
		inBlock := false
		flush := func(s string) {
			if reRemark.MatchString(s) {
				record(agg, block.String())
				block.Reset()
				inBlock = strings.Contains(s, "Missed") || strings.Contains(s, "Passed") || strings.Contains(s, "Analysis")
			}
			if inBlock {
				block.WriteString(s)
				block.WriteByte('\n')
			}
		}
		r := bufio.NewReaderSize(f, 1<<20)
		for {
			b, err := r.ReadByte()
			if err != nil {
				break
			}
			if unicode.IsPrint(rune(b)) || b == '\t' {
				cur.WriteByte(b)
				continue
			}
			if cur.Len() >= 4 {
				flush(cur.String())
			}
			cur.Reset()
		}
		record(agg, block.String())
		return nil
	})
	out := make([]site, 0, len(agg))
	for _, s := range agg {
		out = append(out, *s)
	}
	return out
}

func record(agg map[int]*site, b string) {
	if !strings.Contains(b, "Pass:") || !strings.Contains(b, "regalloc") {
		return
	}
	m := reLine.FindStringSubmatch(b)
	if m == nil {
		return
	}
	ln, _ := strconv.Atoi(m[1])
	s := agg[ln]
	if s == nil {
		s = &site{line: ln}
		agg[ln] = s
	}
	if v := reSpills.FindStringSubmatch(b); v != nil {
		n, _ := strconv.Atoi(v[1])
		s.spills += n
	}
	if v := reReloads.FindStringSubmatch(b); v != nil {
		n, _ := strconv.Atoi(v[1])
		s.reloads += n
	}
	if v := reCost.FindStringSubmatch(b); v != nil {
		f, _ := strconv.ParseFloat(v[1], 64)
		s.cost += f
	}
}

// owner walks back to the top-level definition a line sits inside.
func owner(msl []string, line int) string {
	for i := min(line, len(msl)) - 1; i >= 0; i-- {
		l := msl[i]
		if l == "" || l[0] == ' ' || l[0] == '\t' || l[0] == '}' {
			continue
		}
		if m := reFunc.FindStringSubmatch(l); m != nil {
			return m[1] + "()"
		}
	}
	return "?"
}

func readLines(p string) []string {
	b, err := os.ReadFile(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spills:", err)
		os.Exit(1)
	}
	return strings.Split(string(b), "\n")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
