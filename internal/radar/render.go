package radar

import (
	"encoding/json"
	"io"

	"charm.land/lipgloss/v2"
)

// Solarized palette — the canonical ten, and nothing else (AC4 / M3).
const (
	solarCyan   = "#2aa198" // cyan
	solarYellow = "#b58900" // yellow
	solarOrange = "#cb4b16" // orange
	solarGreen  = "#859900" // green
	solarBase0  = "#839496" // base0
	solarBase01 = "#586e75" // base01
	solarBase02 = "#073642" // base02
	solarBase03 = "#002b36" // base03
	solarBase2  = "#eee8d5" // base2
	solarBase3  = "#fdf6e3" // base3
)

// Output is the writer-injection seam for the rendered card. It defaults to
// lipgloss.Writer (a colorprofile writer over stdout) so the color profile
// follows the terminal environment.
var Output io.Writer = lipgloss.Writer

// RenderFull renders the bordered Solarized card.
func RenderFull(s RadarSnapshot, degrade error) string {
	return ""
}

// RenderSummary renders exactly three plain lines.
func RenderSummary(s RadarSnapshot, degrade error) string {
	return ""
}

// RenderJSON marshals the snapshot.
func RenderJSON(s RadarSnapshot) ([]byte, error) {
	return json.Marshal(s)
}
