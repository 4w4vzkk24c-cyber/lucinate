package radar

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"charm.land/lipgloss/v2"
)

// Solarized palette — the canonical ten, and nothing else (AC4 / M3).
const (
	solarCyan   = "#2aa198" // cyan   — active card
	solarYellow = "#b58900" // yellow — stalled header
	solarOrange = "#cb4b16" // orange — stalled titles, degrade, progress
	solarGreen  = "#859900" // green  — shipped evidence
	solarBase0  = "#839496" // base0  — body
	solarBase01 = "#586e75" // base01 — muted labels
	solarBase02 = "#073642" // base02 — borders
	solarBase03 = "#002b36" // base03 — canvas background
	solarBase2  = "#eee8d5" // base2  — title
	solarBase3  = "#fdf6e3" // base3  — light-mode reserve
)

// Output is the writer-injection seam for the rendered card (the spec's
// profile seam): it defaults to lipgloss.Writer, a colorprofile writer over
// stdout whose profile follows the terminal environment — truecolor SGR pas
// through under COLORTERM/CLICOLOR_FORCE, and strip cleanly on a plain pipe.
var Output io.Writer = lipgloss.Writer

var (
	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(solarBase02)).
			Background(lipgloss.Color(solarBase03)).
			Foreground(lipgloss.Color(solarBase0)).
			Padding(1, 2)

	titleStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(solarBase2))
	labelStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color(solarBase01))
	activeStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(solarCyan))
	stalledHdrStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(solarYellow))
	stalledItemStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(solarOrange))
	evidenceStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(solarGreen))
	dangerStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color(solarOrange))
)

// degradeMessage is the exact queue line rendered when Δ.md is missing or
// evicted — one contiguous styled span so the phrase stays greppable.
const degradeMessage = "[Queue unavailable: Δ.md evicted]"

// RenderFull renders the bordered Solarized card.
func RenderFull(s RadarSnapshot, degrade error) string {
	lines := []string{
		titleStyle.Render("STATE RADAR") + labelStyle.Render(" · "+s.Timestamp.Format(timestampLayout)),
		"",
		labelStyle.Render("IN FLIGHT"),
	}

	if s.InFlight == nil {
		lines = append(lines, "  "+dangerStyle.Render("NO ACTIVE WORK"))
	} else {
		it := s.InFlight
		lines = append(lines, "  "+activeStyle.Render("#"+it.ID+" "+it.Title))
		meta := "  " + labelStyle.Render(it.Stage.String()+" · ") +
			dangerStyle.Render(fmt.Sprintf("%d%%", it.ProgressPct))
		if it.ReEntryFile != "" {
			meta += labelStyle.Render(" · re-enter: " + it.ReEntryFile)
		}
		lines = append(lines, meta)
	}

	lines = append(lines, "", stalledHdrStyle.Render(fmt.Sprintf("STALLED (%d)", len(s.Stalled))))
	for _, d := range s.Stalled {
		lines = append(lines, "  "+stalledItemStyle.Render("#"+d.ID+" "+d.Title)+labelStyle.Render(" — "+d.Blocker))
	}

	lines = append(lines, "", evidenceStyle.Render(fmt.Sprintf("SHIPPED 24H (%d)", len(s.Evidence))))
	for _, e := range s.Evidence {
		age := ""
		if e.Age != "" {
			age = labelStyle.Render(" (" + e.Age + ")")
		}
		lines = append(lines, "  "+evidenceStyle.Render(e.Hash+" "+e.Message)+age)
	}

	lines = append(lines, "", labelStyle.Render(fmt.Sprintf("QUEUE (%d)", len(s.Queue))))
	switch {
	case degrade != nil:
		lines = append(lines, "  "+dangerStyle.Render(degradeMessage))
	case len(s.Queue) == 0:
		lines = append(lines, "  "+labelStyle.Render("empty"))
	default:
		for _, q := range s.Queue {
			lines = append(lines, "  "+activeStyle.Render("#"+q.ID+" "+q.Title)+labelStyle.Render(" · "+q.Priority))
		}
	}

	dirty := "clean"
	if len(s.DirtyFiles) > 0 {
		dirty = strings.Join(s.DirtyFiles, ", ")
	}
	lines = append(lines, "",
		labelStyle.Render(fmt.Sprintf("WORKING TREE (%d): ", len(s.DirtyFiles)))+
			lipgloss.NewStyle().Foreground(lipgloss.Color(solarBase0)).Render(dirty))

	return cardStyle.Render(strings.Join(lines, "\n"))
}

// RenderSummary renders exactly three newline-terminated plain lines with no
// box-drawing runes: the active card (or NO ACTIVE WORK), the queue/stalled
// count (or the degrade message), and the evidence/dirty count.
func RenderSummary(s RadarSnapshot, degrade error) string {
	line1 := "NO ACTIVE WORK"
	if s.InFlight != nil {
		line1 = fmt.Sprintf("ACTIVE #%s %s — %s %d%%",
			s.InFlight.ID, s.InFlight.Title, s.InFlight.Stage, s.InFlight.ProgressPct)
	}
	line2 := fmt.Sprintf("QUEUE %d · STALLED %d", len(s.Queue), len(s.Stalled))
	if degrade != nil {
		line2 = degradeMessage
	}
	line3 := fmt.Sprintf("SHIPPED %d · DIRTY %d", len(s.Evidence), len(s.DirtyFiles))
	return line1 + "\n" + line2 + "\n" + line3 + "\n"
}

// RenderJSON marshals the snapshot as a single JSON document.
func RenderJSON(s RadarSnapshot) ([]byte, error) {
	return json.Marshal(s)
}

// timestampLayout is the card's header clock format.
const timestampLayout = "2006-01-02 15:04 MST"
