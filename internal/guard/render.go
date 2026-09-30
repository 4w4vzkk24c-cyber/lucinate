package guard

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lucinate-ai/lucinate/internal/radar"
)

// palette resolves the ten Solarized hex values once (the wrappers-only
// radar.Solarized set) so every style below draws from the frozen palette.
var palette = radar.Solarized()

// cardChrome is the horizontal frame the card adds around content: the
// NormalBorder (2 cols) plus Padding(1,2) (4 cols) — the landed cardStyle
// arithmetic radar uses, so a content budget of W-6 keeps the outer width <= W.
const cardChrome = 6

// parkPickPrompt names the two actions the operator may take (park a thread /
// pick one); the spec fixes the two action names, not their casing.
const parkPickPrompt = "park a thread / pick one"

// RenderCard renders the Solarized bordered thread card: the ROOT anchor, one
// line per thread carrying that thread's Next as its resume hook, and the
// park/pick prompt. Bounded to min(termWidth, 100) via the radar wrappers.
func RenderCard(s ThreadState) string {
	W := radar.ResolveWidth()
	contentW := W - cardChrome
	if contentW < 0 {
		contentW = 0
	}

	base01 := lipgloss.NewStyle().Foreground(lipgloss.Color(palette[5]))
	base2 := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette[8]))
	cyan := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette[0]))
	green := lipgloss.NewStyle().Foreground(lipgloss.Color(palette[3]))

	open := OpenCount(s)
	lines := []string{
		base2.Render("THREAD GUARD") + base01.Render(fmt.Sprintf(" · %d open / %d threshold", open, Threshold)),
		"",
		base01.Render("ROOT"),
		"  " + base2.Render(s.Root),
		"",
		base01.Render(fmt.Sprintf("THREADS (%d)", len(s.Threads))),
	}
	for _, t := range s.Threads {
		statusStyle := cyan
		if t.Status == "closed" {
			statusStyle = base01
		}
		line := "  " + statusStyle.Render("#"+t.ID+" "+t.Label) +
			base01.Render(" · "+t.Status)
		if t.Next != "" {
			line += base01.Render(" · next: ") + green.Render(t.Next)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", base01.Render("  "+parkPickPrompt))

	card := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(palette[6])).
		Background(lipgloss.Color(palette[7])).
		Foreground(lipgloss.Color(palette[4])).
		Padding(1, 2).
		Width(max(W-2, 0))

	body := clipBlock(strings.Join(lines, "\n"), contentW)
	return card.Render(body)
}

// clipBlock clips every line of a block to at most width visible columns,
// ANSI-aware, through the radar wrapper.
func clipBlock(block string, width int) string {
	lines := strings.Split(block, "\n")
	for i, ln := range lines {
		lines[i] = radar.ClipLine(ln, width)
	}
	return strings.Join(lines, "\n")
}

// RenderQuiet renders the single-line machine tripwire and its exit code:
// under threshold => "threads N/3 — under threshold" / 0; at or over =>
// "CHECKPOINT: N open threads" / 10. No color, no box runes, one line.
func RenderQuiet(open int) (string, int) {
	if open >= Threshold {
		return fmt.Sprintf("CHECKPOINT: %d open threads", open), 10
	}
	return fmt.Sprintf("threads %d/%d — under threshold", open, Threshold), 0
}

// snapshot is the --json contract: {Root, Threads, Open, Threshold, Over}.
type snapshot struct {
	Root      string   `json:"Root"`
	Threads   []Thread `json:"Threads"`
	Open      int      `json:"Open"`
	Threshold int      `json:"Threshold"`
	Over      bool     `json:"Over"`
}

// RenderJSON marshals the --json snapshot with Over == (Open >= Threshold).
func RenderJSON(s ThreadState) ([]byte, error) {
	open := OpenCount(s)
	threads := s.Threads
	if threads == nil {
		threads = []Thread{}
	}
	return json.Marshal(snapshot{
		Root:      s.Root,
		Threads:   threads,
		Open:      open,
		Threshold: Threshold,
		Over:      open >= Threshold,
	})
}
