package tui

// Acceptance suite for forge card 1597 (LUCINATE-V1-01), work item W0
// (statically pinnable half).
//
// Per the spec's runtime-vs-static split: W0's live-gateway acceptance
// criteria (pairing through the staged operator gate, protocol-4
// negotiation, /sessions restore + round-trip, the ~/.openclaw write
// audit) are RUNTIME evidence the builder stages against gateway 2026.9.4
// — they are deliberately NOT simulated here (bans B1/B2). What is
// statically pinnable at base is the gate surface itself: the Makefile
// targets exist and the smoke target pins the startup smoke test that
// constructs AppModel in every entry-view variant. Mutation M0 (dropping
// make smoke from the W0 gate) removes exactly this surface.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestW0AC1_MakefileGateTargetsPinBuildTestSmoke statically pins the three
// W0 gate targets and the fact that make smoke drives TestStartupSmoke (the
// hermetic AppModel-construction smoke). Green at base by construction; it
// goes red only if the gate surface is gutted (M0).
func TestW0AC1_MakefileGateTargetsPinBuildTestSmoke(t *testing.T) {
	mk, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	m := string(mk)
	for _, target := range []string{"build:", "test:", "smoke:"} {
		if !strings.Contains(m, target) {
			t.Errorf("W0-AC1: Makefile gate target %s missing (gate surface gutted, M0)", strings.TrimSuffix(target, ":"))
		}
	}
	if !strings.Contains(m, "TestStartupSmoke") {
		t.Errorf("W0-AC1: make smoke no longer drives TestStartupSmoke — startup smoke dropped from the gate (M0)")
	}
}
