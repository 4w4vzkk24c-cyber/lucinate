// Command guard is the thread-divergence tripwire: it reads the operator's
// open threads (HOME-resolved ~/.openclaw/state/threads.json) and reports
// whether they have diverged past the checkpoint threshold.
//
// Read-only: guard never writes the state file, and it never runs a mutating
// git command. --quiet exits 0 below the threshold and 10 at or above; a
// missing/malformed state file degrades to "NO THREAD STATE" with exit 0.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lucinate-ai/lucinate/internal/guard"
)

func main() {
	quiet := flag.Bool("quiet", false, "emit the single-line tripwire (exit 10 at or over threshold)")
	jsonOut := flag.Bool("json", false, "emit the snapshot as a JSON document")
	flag.Parse()

	if *quiet && *jsonOut {
		fmt.Fprintln(os.Stderr, "guard: --quiet and --json are mutually exclusive")
		flag.Usage()
		os.Exit(2)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = "" // degrade: no state resolves, the disclose path still renders
	}
	state, _ := guard.ReadState(home)

	// A zero state (no root, no threads) is the disclosed missing-state
	// degrade. Under --json it still emits the JSON document — its empty
	// Root/Threads fields are the degrade indication — so a machine consumer
	// piping `guard --json` never receives the bare literal. On the default
	// and --quiet paths the bare literal remains the degrade text.
	degraded := state.Root == "" && len(state.Threads) == 0

	switch {
	case *jsonOut:
		b, err := guard.RenderJSON(state)
		if err != nil {
			fmt.Fprintln(os.Stderr, "guard:", err)
			os.Exit(1)
		}
		os.Stdout.Write(b)
		fmt.Println()
	case degraded:
		fmt.Println("NO THREAD STATE")
	case *quiet:
		line, code := guard.RenderQuiet(guard.OpenCount(state))
		fmt.Println(line)
		os.Exit(code)
	default:
		fmt.Println(guard.RenderCard(state))
	}
}
