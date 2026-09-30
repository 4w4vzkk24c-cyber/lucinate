package radar

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
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

// render caps: the queue and stalled sections show at most this many rows
// apiece, then a single "+N more (of M)" line (D3).
const sectionRowCap = 5

// cardOuterWidthCap bounds every rendered line — the card frame included — to
// at most this many visible columns (D1).
const cardOuterWidthCap = 100

// cardChrome is the horizontal frame the card adds around content: the
// NormalBorder (2 cols, one left + one right) plus Padding(1,2) (4 cols). The
// content budget is therefore W - 6 (the landed cardStyle arithmetic; spec r1
// correction of the design note's W-4 shorthand).
const cardChrome = 6

// resolveWidth returns the effective render width bound: min(termWidth, 100).
// It is resolved per render call (never cached at init) so the COLUMNS seam
// stays live for tests and exec.
func resolveWidth() int {
	w := termWidth()
	if w > cardOuterWidthCap {
		w = cardOuterWidthCap
	}
	if w < 1 {
		w = cardOuterWidthCap
	}
	return w
}

// termWidth resolves the terminal width at RENDER time, in precedence order:
//  1. the COLUMNS environment variable when it parses as a positive integer;
//  2. else a TTY width query on stdout;
//  3. else 100 (piped/unknown).
//
// The COLUMNS read is deliberately inside the function (per call), never at
// package init: an init-cached resolver would ignore the test/exec env seam.
func termWidth() int {
	if c := os.Getenv("COLUMNS"); c != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(c)); err == nil && n > 0 {
			return n
		}
	}
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		return w
	}
	return cardOuterWidthCap
}

// clipLine hard-clips one line to at most width visible columns, ANSI-aware
// (SGR sequences pass through, wide runes are measured as cells).
func clipLine(line string, width int) string {
	if width < 0 {
		width = 0
	}
	return ansi.Truncate(line, width, "")
}

// RenderFull renders the bordered Solarized card, bounded to W visible columns
// (D1): W = min(termWidth, 100); the card frame adds 6 horizontal columns, so
// content lines clip at W-6. The single exception is the degrade phrase, which
// is never split and renders whole even when W is narrower than the phrase.
func RenderFull(s RadarSnapshot, degrade error) string {
	W := resolveWidth()
	contentW := W - cardChrome
	if contentW < 0 {
		contentW = 0
	}

	lines := []string{
		titleStyle.Render("STATE RADAR") + labelStyle.Render(" · "+s.Timestamp.Format(timestampLayout)),
		"",
		labelStyle.Render("IN FLIGHT"),
	}

	if s.InFlight == nil {
		lines = append(lines, "  "+dangerStyle.Render("NO ACTIVE WORK"))
	} else {
		it := s.InFlight
		active := "  " + activeStyle.Render("#"+it.ID+" "+it.Title)
		if s.InFlightCardStatus != "" && s.InFlightCardStatus != "in-progress" {
			active += labelStyle.Render(" [card: " + s.InFlightCardStatus + "]")
		}
		lines = append(lines, active)
		meta := "  " + labelStyle.Render(it.Stage.String()+" · ") +
			dangerStyle.Render(fmt.Sprintf("%d%%", it.ProgressPct))
		if it.ReEntryFile != "" {
			meta += labelStyle.Render(" · re-enter: " + it.ReEntryFile)
		}
		lines = append(lines, meta)
	}

	lines = append(lines, "", stalledHdrStyle.Render(fmt.Sprintf("STALLED (%d)", len(s.Stalled))))
	for _, ln := range stalledSection(s.Stalled, contentW) {
		lines = append(lines, ln)
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
		// Greppability outranks the cap: the phrase must render whole even at
		// a W narrower than the phrase. It is inserted as a no-space sentinel
		// so lipgloss's internal word-wrap cannot split it, then spliced back
		// to the real phrase after the card is rendered.
		lines = append(lines, "  "+degradeSentinel)
	case len(s.Queue) == 0:
		lines = append(lines, "  "+labelStyle.Render("empty"))
	default:
		for i, q := range s.Queue {
			if i == sectionRowCap {
				lines = append(lines, "  "+labelStyle.Render(moreLine(len(s.Queue))))
				break
			}
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

	// The card frame plus content budget W-6 => outer width <= W.
	styled := cardStyle.Width(max(W-2, 0))
	body := clipBlock(strings.Join(lines, "\n"), contentW)
	out := styled.Render(body)
	// Splice the real (unbreakable) degrade phrase back in, replacing the
	// sentinel; the resulting line is the sole width-cap exemption.
	out = strings.Replace(out, degradeSentinel, dangerStyle.Render(degradeMessage), 1)
	return out
}

// stalledSection renders the stalled rows under the D3 cap: at most
// sectionRowCap rows, then a "+N more (of M)" line when more exist. The
// sections are pre-clipped to contentW.
func stalledSection(stalled []StalledDecision, contentW int) []string {
	var out []string
	for i, d := range stalled {
		if i == sectionRowCap {
			out = append(out, "  "+labelStyle.Render(moreLine(len(stalled))))
			break
		}
		out = append(out, "  "+stalledItemStyle.Render("#"+d.ID+" "+d.Title)+labelStyle.Render(" — "+d.Blocker))
	}
	return out
}

// moreLine is the literal D3 overflow line: "+N more (of M)", N = M-5.
func moreLine(total int) string {
	return fmt.Sprintf("+%d more (of %d)", total-sectionRowCap, total)
}

// degradeSentinel is the no-space placeholder standing in for the degrade
// phrase inside the lipgloss block; it cannot be word-wrapped.
const degradeSentinel = "@@DEGRADE@@"

// clipBlock clips every line of a block to at most width visible columns,
// ANSI-aware. Lines carrying the sentinel are left intact (the splice restores
// the phrase whole).
func clipBlock(block string, width int) string {
	lines := strings.Split(block, "\n")
	for i, ln := range lines {
		if strings.Contains(ln, degradeSentinel) {
			continue
		}
		lines[i] = clipLine(ln, width)
	}
	return strings.Join(lines, "\n")
}

// RenderSummary renders exactly three newline-terminated plain lines with no
// box-drawing runes: the active card (or NO ACTIVE WORK), the queue/stalled
// count (or the degrade message), and the evidence/dirty count. Each line
// clips to W (never wraps), and the D4 mismatch marker appends to line 1
// within the 3-line contract.
func RenderSummary(s RadarSnapshot, degrade error) string {
	W := resolveWidth()

	line1 := "NO ACTIVE WORK"
	if s.InFlight != nil {
		line1 = fmt.Sprintf("ACTIVE #%s %s — %s %d%%",
			s.InFlight.ID, s.InFlight.Title, s.InFlight.Stage, s.InFlight.ProgressPct)
		if s.InFlightCardStatus != "" && s.InFlightCardStatus != "in-progress" {
			line1 += " STATUS?"
		}
	}
	line2 := fmt.Sprintf("QUEUE %d · STALLED %d", len(s.Queue), len(s.Stalled))
	if degrade != nil {
		line2 = degradeMessage
	}
	line3 := fmt.Sprintf("SHIPPED %d · DIRTY %d", len(s.Evidence), len(s.DirtyFiles))

	lines := []string{}
	for _, ln := range []string{line1, line2, line3} {
		if strings.Contains(ln, degradeMessage) {
			lines = append(lines, ln) // never split the greppability phrase
			continue
		}
		lines = append(lines, clipLine(ln, W))
	}
	return strings.Join(lines, "\n") + "\n"
}

// RenderJSON marshals the snapshot as a single JSON document.
func RenderJSON(s RadarSnapshot) ([]byte, error) {
	return json.Marshal(s)
}

// timestampLayout is the card's header clock format.
const timestampLayout = "2006-01-02 15:04 MST"
