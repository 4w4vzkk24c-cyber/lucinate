// Command closer renders the last-mile ladder over the radar substrate: which
// started things stopped short of the last mile, and the one action that closes
// each.
//
// Read-only: closer never writes, stages, commits, merges, or pushes; the only
// git invocations are read-only queries. Under --verify it executes a card's
// own verify: command and records its exit code.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/lucinate-ai/lucinate/internal/closer"
	"github.com/lucinate-ai/lucinate/internal/radar"
)

func main() {
	verify := flag.Bool("verify", false, "execute each card's verify: command (the runs rung gate)")
	jsonOut := flag.Bool("json", false, "emit the uncapped item list as a JSON document")
	flag.Parse()

	repo, err := os.Getwd()
	if err != nil {
		repo = "."
	}

	items, err := closer.ClassifyVerify(repo, time.Now(), *verify)
	if err != nil {
		// No nonzero exit class in v1: a classification error still renders the
		// (empty) ladder rather than paging.
		items = nil
	}

	if *jsonOut {
		b, err := closer.RenderJSON(items)
		if err != nil {
			fmt.Fprintln(os.Stderr, "closer:", err)
		} else {
			os.Stdout.Write(b)
			fmt.Println()
		}
		return
	}
	fmt.Print(closer.Render(items, radar.ResolveWidth()))
}
