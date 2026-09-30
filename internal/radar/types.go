// Package radar implements the state-radar observability CLI: a read-only
// terminal snapshot of in-flight work (kanban-zed cards), the ranked roadmap
// (Δ.md), and git telemetry, rendered in canonical Solarized.
//
// Card 1602 (LUCINATE-RADAR-01). Radar never writes kanban cards or Δ.md.
package radar

import (
	"errors"
	"time"
)

// LadderStage is a rung of the delivery ladder a card currently occupies.
type LadderStage int

// Ladder rungs, in order. StageLand is never auto-derived from git evidence
// in v1 (spec r3 F7).
const (
	StageDesign LadderStage = iota + 1
	StageSpec
	StageTestsRed
	StageBuild
	StageVerify
	StageAudit
	StageLand
)

// String returns the lowercase ladder-rung name.
func (l LadderStage) String() string {
	switch l {
	case StageDesign:
		return "design"
	case StageSpec:
		return "spec"
	case StageTestsRed:
		return "tests-red"
	case StageBuild:
		return "build"
	case StageVerify:
		return "verify"
	case StageAudit:
		return "audit"
	case StageLand:
		return "land"
	default:
		return "unknown"
	}
}

// RadarSnapshot is the full orientation state; the frozen --json contract.
type RadarSnapshot struct {
	Timestamp          time.Time
	InFlight           *InFlightTask
	InFlightCardStatus string
	Evidence           []LandedEvidence
	DirtyFiles         []string
	Stalled            []StalledDecision
	Queue              []QueuedItem
}

// InFlightTask is the single card the operator should be deep in right now.
type InFlightTask struct {
	ID, Title, Description               string
	Stage                                LadderStage
	ProgressPct                          int
	ReEntryFile, ReEntryCmd, ContextNote string
}

// StalledDecision is a blocked/review card waiting on the operator.
type StalledDecision struct {
	ID, Title, Blocker string
}

// QueuedItem is one ranked roadmap row.
type QueuedItem struct {
	ID, Title, Priority string
}

// LandedEvidence is one recent commit by the operator.
type LandedEvidence struct {
	Hash, Message, Age string
}

// ActiveCard is a scanned kanban card that is neither done nor archived.
type ActiveCard struct {
	ID, Title, Status, Priority, Path string
}

// GitTelemetry is the working-tree and recent-history state of a repo.
type GitTelemetry struct {
	DirtyFiles  []string
	ReEntryFile string
	Landed      []LandedEvidence
}

// ErrRoadmapUnavailable is the sentinel wrapped for a missing or unreadable
// Δ.md (including macOS iCloud eviction). Callers check with errors.Is.
var ErrRoadmapUnavailable = errors.New("roadmap unavailable")
