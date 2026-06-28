// Command npc-pose-report analyzes a JSONL NPC pose dump for gait issues.
//
// Usage:
//
//	go run . -scene scenes/npc-test.toml -dump-npc-poses /tmp/walk.jsonl -dump-npc-frames 240
//	go run ./cmd/npc-pose-report /tmp/walk.jsonl
package main

import (
	"fmt"
	"os"

	"raytracer/internal/character"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: npc-pose-report <poses.jsonl>")
		os.Exit(1)
	}
	recs, err := character.ReadPoseRecords(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(character.FormatGaitReport(character.AnalyzePoseRecords(recs)))
	fmt.Println("\nPer-frame summary:")
	for _, rec := range recs {
		fmt.Println(character.FormatFrameSummary(rec))
	}
}
