// Command radar renders a Solarized terminal orientation snapshot: the
// in-flight card, stalled decisions, shipped evidence, and the ranked queue.
//
// Read-only observability — radar never writes kanban cards or Δ.md.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lucinate-ai/lucinate/internal/radar"
)

func main() {
	summary := flag.Bool("summary", false, "emit a 3-line summary instead of the full card")
	jsonOut := flag.Bool("json", false, "emit the snapshot as a JSON document")
	flag.Parse()

	if *summary && *jsonOut {
		fmt.Fprintln(os.Stderr, "radar: --summary and --json are mutually exclusive")
		flag.Usage()
		os.Exit(2)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = "" // degrade: empty stores, snapshot still renders
	}

	snap, degrade := radar.Build(home)

	switch {
	case *jsonOut:
		b, err := radar.RenderJSON(snap)
		if err != nil {
			fmt.Fprintln(os.Stderr, "radar:", err)
			os.Exit(1)
		}
		os.Stdout.Write(b)
		fmt.Println()
	case *summary:
		fmt.Print(radar.RenderSummary(snap, degrade))
	default:
		fmt.Fprint(radar.Output, radar.RenderFull(snap, degrade))
	}
}
