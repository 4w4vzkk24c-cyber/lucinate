package tui

// Who said what in the transcript: a name word and a bar down the left edge
// of every line of a message, both in the sender's colour. Cyan for the
// operator, yellow for the assistant (operator's choice, 2026-10-02). The
// bar carries the sender onto wrapped lines, where the name does not reach;
// the name keeps it from being colour alone.

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	"github.com/charmbracelet/x/ansi"
)

const (
	senderTestCyan   = "38;2;42;161;152" // #2aa198
	senderTestYellow = "38;2;181;137;0"  // #b58900

	// The bar is the sender colour at 20% over the Solarized background
	// (operator, 2026-10-02: full-strength bars were distracting).
	senderTestDarkCyanBar    = "38;2;8;67;74"     // 20% #2aa198 over base03 #002b36
	senderTestDarkYellowBar  = "38;2;36;62;43"    // 20% #b58900 over base03
	senderTestLightCyanBar   = "38;2;211;229;212" // 20% #2aa198 over base3 #fdf6e3
	senderTestLightYellowBar = "38;2;239;224;182" // 20% #b58900 over base3
)

func senderTestChat(width int, agentName string, messages ...chatMessage) *chatModel {
	vp := viewport.New()
	vp.SetWidth(width)
	vp.SetHeight(60)
	m := &chatModel{viewport: vp, width: width, agentName: agentName, messages: messages}
	m.updateViewport()
	return m
}

// senderTestBlock returns the rendered lines of the message containing needle:
// the contiguous run of non-blank lines around it.
func senderTestBlock(t *testing.T, m *chatModel, needle string) []string {
	t.Helper()
	at := -1
	for i, line := range m.selLines {
		if strings.Contains(ansi.Strip(line), needle) {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatalf("%q not rendered in:\n%s", needle, ansi.Strip(strings.Join(m.selLines, "\n")))
	}
	lo, hi := at, at
	for lo > 0 && strings.TrimSpace(ansi.Strip(m.selLines[lo-1])) != "" {
		lo--
	}
	for hi < len(m.selLines)-1 && strings.TrimSpace(ansi.Strip(m.selLines[hi+1])) != "" {
		hi++
	}
	return m.selLines[lo : hi+1]
}

var senderTestLong = strings.Repeat("alpha ", 40) + "omega"

func TestMessageSender_BarOnEveryLineOfAMessage(t *testing.T) {
	for _, width := range []int{120, 40} { // inline and stacked layouts
		m := senderTestChat(width, "main",
			chatMessage{role: "user", content: senderTestLong},
			chatMessage{role: "assistant", content: "line one\nline two\nline three", rendered: true},
		)
		for _, needle := range []string{"omega", "line two"} {
			block := senderTestBlock(t, m, needle)
			if len(block) < 3 {
				t.Fatalf("width=%d: %q block has %d lines; the test needs a multi-line message", width, needle, len(block))
			}
			for i, line := range block {
				if !strings.HasPrefix(ansi.Strip(line), messageBar) {
					t.Errorf("width=%d %q line %d has no sender bar: %q", width, needle, i, ansi.Strip(line))
				}
			}
		}
	}
}

func TestMessageSender_NamesAreWordsNotEmoji(t *testing.T) {
	m := senderTestChat(120, "main",
		chatMessage{role: "user", content: "hello there"},
		chatMessage{role: "assistant", content: "general kenobi"},
	)
	user := ansi.Strip(senderTestBlock(t, m, "hello there")[0])
	assistant := ansi.Strip(senderTestBlock(t, m, "general kenobi")[0])
	if !strings.HasPrefix(user, messageBar+"zane ") {
		t.Errorf("user line does not open with the bar and the name: %q", user)
	}
	if !strings.HasPrefix(assistant, messageBar+"vesper ") {
		t.Errorf("main-agent line does not open with the bar and the name: %q", assistant)
	}
	all := strings.Join(m.selLines, "\n")
	for _, emoji := range []string{"🦚", "🍋"} {
		if strings.Contains(all, emoji) {
			t.Errorf("transcript still carries the %s prefix", emoji)
		}
	}

	other := senderTestChat(120, "quorum-builder", chatMessage{role: "assistant", content: "built it"})
	if got := ansi.Strip(senderTestBlock(t, other, "built it")[0]); !strings.HasPrefix(got, messageBar+"quorum-builder ") {
		t.Errorf("a non-main agent lost its own name: %q", got)
	}
}

func TestMessageSender_ColoursAreCyanForUserYellowForAssistant(t *testing.T) {
	m := senderTestChat(120, "main",
		chatMessage{role: "user", content: senderTestLong},
		chatMessage{role: "assistant", content: "line one\nline two\nline three", rendered: true},
	)
	senderTestAssertBars(t, m, "omega", senderTestDarkCyanBar)
	senderTestAssertBars(t, m, "line two", senderTestDarkYellowBar)

	// The names stay at full strength: they are the word that says who
	// spoke, and a muted word is an unreadable one.
	for _, tc := range []struct{ needle, name, want string }{
		{"alpha", "zane", senderTestCyan},
		{"line one", "vesper", senderTestYellow},
	} {
		first := senderTestBlock(t, m, tc.needle)[0]
		styling := first[strings.Index(first, messageBar):strings.Index(first, tc.name)]
		if !strings.Contains(styling, tc.want) {
			t.Errorf("name %q is not at full strength %s: %q", tc.name, tc.want, styling)
		}
	}
}

// senderTestAssertBars checks every bar of the message containing needle is
// drawn in exactly the wanted colour.
func senderTestAssertBars(t *testing.T, m *chatModel, needle, want string) {
	t.Helper()
	for i, line := range senderTestBlock(t, m, needle) {
		bar := line[:strings.Index(line, messageBar)]
		if !strings.Contains(bar, want+"m") {
			t.Errorf("%q line %d: bar styled %q, want %s", needle, i, bar, want)
		}
	}
}

// The mute blends toward the background, so it must follow the palette: a
// bar muted toward the dark base on a light terminal would be the loudest
// thing on screen.
func TestMessageSender_BarMuteFollowsThePalette(t *testing.T) {
	m := senderTestChat(120, "main")
	light := lightPalette
	m.palette = &light
	m.messages = []chatMessage{
		{role: "user", content: senderTestLong},
		{role: "assistant", content: "line one\nline two\nline three", rendered: true},
	}
	m.pendingMessages = []string{"queued one\nqueued two"}
	m.updateViewport()

	senderTestAssertBars(t, m, "omega", senderTestLightCyanBar)
	senderTestAssertBars(t, m, "line two", senderTestLightYellowBar)
	for i, line := range strings.Split(m.renderPendingMessages(), "\n") {
		if bar := line[:strings.Index(line, messageBar)]; !strings.Contains(bar, senderTestLightCyanBar+"m") {
			t.Errorf("pending line %d: bar styled %q, want %s", i, bar, senderTestLightCyanBar)
		}
	}
}

func TestMessageSender_BarIsMutedAtLeastEightyPercent(t *testing.T) {
	if senderBarStrength > 0.2 {
		t.Errorf("sender bar keeps %.0f%% of its colour; the operator asked for 20%% or less", senderBarStrength*100)
	}
	if senderBarStrength <= 0 {
		t.Error("a bar with no colour left no longer says who spoke")
	}
}

func TestMessageSender_BodiesAlignAndFitThePane(t *testing.T) {
	const width = 120
	m := senderTestChat(width, "main",
		chatMessage{role: "user", content: senderTestLong},
		chatMessage{role: "assistant", content: "kenobi " + senderTestLong},
	)
	userCol := ansi.StringWidth(strings.SplitN(ansi.Strip(senderTestBlock(t, m, "omega")[0]), "alpha", 2)[0])
	asstCol := ansi.StringWidth(strings.SplitN(ansi.Strip(senderTestBlock(t, m, "kenobi")[0]), "kenobi", 2)[0])
	if userCol != asstCol {
		t.Errorf("message bodies start at different columns: user %d, assistant %d", userCol, asstCol)
	}
	for _, needle := range []string{"omega", "kenobi"} {
		block := senderTestBlock(t, m, needle)
		for i, line := range block {
			if got := ansi.StringWidth(line); got > width-4 {
				t.Errorf("%q line %d is %d cells, over the %d-cell content width", needle, i, got, width-4)
			}
			if i == 0 {
				continue
			}
			rest := strings.TrimPrefix(ansi.Strip(line), messageBar)
			indent := len(rest) - len(strings.TrimLeft(rest, " "))
			if col := messageBarCells + indent; col != userCol {
				t.Errorf("%q continuation line %d starts its body at cell %d, want %d", needle, i, col, userCol)
			}
		}
	}
}

func TestMessageSender_SystemLinesHaveNoBar(t *testing.T) {
	m := senderTestChat(120, "main", chatMessage{role: "system", content: "session compacted"})
	if line := ansi.Strip(senderTestBlock(t, m, "session compacted")[0]); strings.Contains(line, messageBar) {
		t.Errorf("a system line carries a sender bar: %q", line)
	}
}

func TestMessageSender_PendingMessagesCarryTheUserBar(t *testing.T) {
	m := senderTestChat(120, "main")
	m.pendingMessages = []string{"queued one\nqueued two"}
	for i, line := range strings.Split(m.renderPendingMessages(), "\n") {
		if !strings.HasPrefix(ansi.Strip(line), messageBar) {
			t.Errorf("pending line %d has no sender bar: %q", i, ansi.Strip(line))
		}
	}
}

// Copying whole lines must not put the bar on the clipboard.
func TestMessageSender_BarIsNotCopied(t *testing.T) {
	m := senderTestChat(120, "main", chatMessage{role: "user", content: senderTestLong})
	last := len(m.selLines) - 1
	got := extractSelection(m.selLines, selPoint{0, 0}, selPoint{last, ansi.StringWidth(m.selLines[last])})
	if strings.Contains(got, messageBar) {
		t.Errorf("copied text contains the sender bar: %q", got)
	}
	if !strings.Contains(got, "omega") || !strings.Contains(got, "zane") {
		t.Errorf("copied text lost content: %q", got)
	}
}
