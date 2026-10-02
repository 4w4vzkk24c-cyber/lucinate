package tui

import (
	"image/color"
	"os"
	"sync"

	"charm.land/lipgloss/v2"
)

var (
	// Colours — using dark theme values.
	// Solarized Dark palette
	subtle      = lipgloss.Color("#586e75") // Base01 — comments, secondary
	accent      = lipgloss.Color("#268bd2") // Blue — interactive, selection
	userClr     = lipgloss.Color("#859900") // Green — user messages
	errClr      = lipgloss.Color("#dc322f") // Red — errors
	execClr     = lipgloss.Color("#cb4b16") // Orange — remote exec
	localExcClr = lipgloss.Color("#dc322f") // Red — local exec (shares Red; prefix+border distinguish)

	// Session state lamps (house accents): one meaning each.
	runClr   = lipgloss.Color("#2aa198") // Cyan — a run in flight
	okClr    = lipgloss.Color("#859900") // Green — ended cleanly
	staleClr = lipgloss.Color("#b58900") // Yellow — stopped by the operator

	// Transcript sender colours (operator's choice, 2026-10-02): the name
	// and the bar beside each message. These reuse two lamp hues in a
	// different pane; in the transcript they mean who spoke, nothing else.
	userSenderClr      = lipgloss.Color("#2aa198") // Cyan — the operator
	assistantSenderClr = lipgloss.Color("#b58900") // Yellow — the assistant

	// Header bar.
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#eee8d5")).
			Background(accent).
			Padding(0, 1)

	// User message prefix.
	userPrefixStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(userSenderClr)

	// Assistant message prefix.
	assistantPrefixStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(assistantSenderClr)

	// Streaming cursor.
	cursorStyle = lipgloss.NewStyle().
			Foreground(accent).
			Bold(true)

	// In-app mouse selection highlight. Reverse video, like a terminal's
	// native selection; applied over ANSI-stripped text so the highlight
	// overrides inner styling (see applySelectionHighlight).
	selectionStyle = lipgloss.NewStyle().Reverse(true)

	// Input area border.
	inputBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(subtle).
				Padding(0, 1)

	// Thinking content body (reasoning blocks from the model).
	thinkingBodyStyle = lipgloss.NewStyle().
				Foreground(subtle)

	// Status / info text.
	statusStyle = lipgloss.NewStyle().
			Foreground(subtle)

	// Empty-history placeholder — brighter than statusStyle so it stands
	// out against the empty conversation pane.
	emptyHistoryStyle = lipgloss.NewStyle().
				Foreground(accent).
				Italic(true)

	// Error text.
	errorStyle = lipgloss.NewStyle().
			Foreground(errClr).
			Bold(true)

	// Navigation-confirm prompt — the y/n band pinned directly above the
	// input when a switch would discard queued messages or cancel a
	// routine. Accent-coloured and bold so it reads as "needs an answer"
	// without the alarm of error red.
	navConfirmStyle = lipgloss.NewStyle().
			Foreground(accent).
			Bold(true)

	// Connection-status badge styles, sized to read against the purple
	// header background where the badge is rendered.
	headerBadgeWarnStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#002b36")).
				Background(accent)
	headerBadgeErrStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#eee8d5")).
				Background(lipgloss.Color("#dc322f")).
				Padding(0, 1)

	// Input area border for exec mode.
	execBorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(execClr).
			Padding(0, 1)

	// Exec command prefix style (remote).
	execPrefixStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(execClr)

	// Input area border for local exec mode.
	localExecBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(localExcClr).
				Padding(0, 1)

	// Local exec command prefix style.
	localExecPrefixStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(localExcClr)

	// Pending (queued) message prefix — dimmed, italic shadow of the user style.
	pendingPrefixStyle = lipgloss.NewStyle().
				Italic(true).
				Faint(true).
				Foreground(userSenderClr)

	// Pending (queued) message body — dimmed italic to match prefix.
	pendingBodyStyle = lipgloss.NewStyle().
				Italic(true).
				Faint(true)

	// Help text.
	helpStyle = lipgloss.NewStyle().
			Foreground(subtle)

	// Completion-menu highlight — applied to the row at the current
	// cycle index when Tab is cycling through candidates.
	completionMenuHighlightStyle = lipgloss.NewStyle().
					Foreground(accent).
					Bold(true)

	// Connection status bar — a thin dim row above non-chat views
	// telling the user which connection is in scope. The chat view
	// folds the same info into its own header bar.
	connBannerStyle = lipgloss.NewStyle().
			Foreground(subtle)

	// Tool-activity strip styles. Running reuses the muted status colour;
	// success borrows the local-exec green so the visual language matches
	// "this finished cleanly"; error falls through to errorStyle.
	toolRunningStyle = lipgloss.NewStyle().
				Foreground(subtle)
	toolSuccessStyle = lipgloss.NewStyle().
				Foreground(localExcClr)

	// W3 staged-attachment chip (one per file, above the input).
	attachChipStyle = lipgloss.NewStyle().
			Foreground(userClr).
			Bold(true)

	// W3 ctrl+a path prompt line.
	attachPromptStyle = lipgloss.NewStyle().
				Foreground(accent)
)

// --- W2 palettes / background detection -----------------------------------

// chatPalette carries the colour values the chat view renders with. The
// package vars above remain the dark reference (defaults unchanged); a
// light variant selects by theme.mode, with auto deferring to a once-per-
// process terminal-background query.
type chatPalette struct {
	subtle      color.Color
	accent      color.Color
	userClr     color.Color
	errClr      color.Color
	execClr     color.Color
	localExcClr color.Color
	// background is the pane background this palette is drawn on. Nothing
	// paints it; muted tints (the sender bar) blend toward it.
	background color.Color
}

// darkPalette mirrors the package-level dark colour values exactly — a
// zero-value chatModel (theme never resolved) must render bytes identical
// to the pre-W2 output.
var darkPalette = chatPalette{
	subtle:      subtle,
	accent:      accent,
	userClr:     userClr,
	errClr:      errClr,
	execClr:     execClr,
	localExcClr: localExcClr,
	background:  lipgloss.Color("#002b36"), // Solarized Base03
}

// lightPalette is a light-background variant: darker accents and text
// colours for contrast on light terminals.
var lightPalette = chatPalette{
	subtle:      lipgloss.Color("#93a1a1"), // Solarized Base1
	accent:      lipgloss.Color("#268bd2"), // Solarized Blue
	userClr:     lipgloss.Color("#859900"), // Solarized Green
	errClr:      lipgloss.Color("#dc322f"), // Solarized Red
	execClr:     lipgloss.Color("#cb4b16"), // Solarized Orange
	localExcClr: lipgloss.Color("#dc322f"), // Solarized Red (shares Red; prefix+border distinguish)
	background:  lipgloss.Color("#fdf6e3"), // Solarized Base3
}

var (
	detectBackgroundOnce sync.Once
	detectedDarkBg       = true
)

// detectDarkBackground queries the terminal background colour once per
// process (lipgloss.HasDarkBackground over stdin/stdout). Non-queryable
// terminals (pipes, tests, embedded hosts) return the dark default —
// which is exactly the "defaults unchanged when detection is unavailable"
// requirement.
func detectDarkBackground() bool {
	detectBackgroundOnce.Do(func() {
		detectedDarkBg = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	})
	return detectedDarkBg
}

// paletteForMode resolves the palette for a theme.mode value: explicit
// dark/light override detection; auto (or any unrecognised value, which
// includes the unset pre-W2 default) defers to it.
func paletteForMode(mode string) chatPalette {
	switch mode {
	case "dark":
		return darkPalette
	case "light":
		return lightPalette
	default: // "auto" and ""
		if detectDarkBackground() {
			return darkPalette
		}
		return lightPalette
	}
}

// chatTheme holds the per-model chat-view styles derived from a palette.
// Constructed from the model's palette at render time so a zero-value
// model still renders exactly the dark package-var bytes.
type chatTheme struct {
	header      lipgloss.Style
	badgeWarn   lipgloss.Style
	inputBorder lipgloss.Style
	execBorder  lipgloss.Style
	localBorder lipgloss.Style
	help        lipgloss.Style
}

// newChatTheme derives the chat-view styles from a palette, mirroring the
// package-var definitions field for field.
func newChatTheme(pal chatPalette) chatTheme {
	return chatTheme{
		header: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#eee8d5")).
			Background(pal.accent).
			Padding(0, 1),
		badgeWarn: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#002b36")).
			Background(pal.accent),
		inputBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(pal.subtle).
			Padding(0, 1),
		execBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(pal.execClr).
			Padding(0, 1),
		localBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(pal.localExcClr).
			Padding(0, 1),
		help: lipgloss.NewStyle().
			Foreground(pal.subtle),
	}
}
