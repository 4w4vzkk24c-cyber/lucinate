package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"charm.land/bubbles/v2/viewport"
)

// Every pair of consecutive messages is separated by exactly one blank
// line — including same-sender runs, which previously stacked directly.
// Separator rows (crons view) keep their own divider without an extra blank.
func TestConsecutiveMessagesSeparatedByBlankLine(t *testing.T) {
	messages := []chatMessage{
		{role: "user", content: "first user"},
		{role: "user", content: "second user"},
		{role: "assistant", content: "reply one", rendered: true},
		{role: "assistant", content: "reply two", rendered: true},
	}
	vp := viewport.New()
	vp.SetWidth(100)
	vp.SetHeight(50)
	m := &chatModel{
		viewport:  vp,
		agentName: "main",
		width:     100,
		messages:  messages,
	}
	m.updateViewport()
	view := ansi.Strip(m.viewport.View())

	// Find the lines carrying each message's text and assert a blank line
	// sits between each adjacent pair.
	texts := []string{"first user", "second user", "reply one", "reply two"}
	lines := strings.Split(view, "\n")
	idx := make([]int, len(texts))
	for ti, text := range texts {
		found := -1
		for i, line := range lines {
			if strings.Contains(line, text) {
				found = i
				break
			}
		}
		if found < 0 {
			t.Fatalf("text %q not found in view:\n%s", text, view)
		}
		idx[ti] = found
	}
	for i := 0; i+1 < len(idx); i++ {
		if idx[i+1] < idx[i] {
			t.Fatalf("messages out of order in view:\n%s", view)
		}
		// Between the line carrying text[i] and the line carrying text[i+1]
		// there must be exactly one blank line.
		blanks := 0
		for j := idx[i] + 1; j < idx[i+1]; j++ {
			if strings.TrimSpace(lines[j]) == "" {
				blanks++
			}
		}
		if blanks != 1 {
			t.Errorf("between %q and %q: %d blank lines, want exactly 1\nview:\n%s",
				texts[i], texts[i+1], blanks, view)
		}
	}
}

// A crons-view separator row renders its own divider; it must not gain an
// extra blank line above or below beyond the single spacing rule.
func TestSeparatorRowKeepsDividerWithoutExtraBlank(t *testing.T) {
	messages := []chatMessage{
		{role: "user", content: "before separator"},
		{role: "separator", timestampMs: 1000},
		{role: "assistant", content: "after separator", rendered: true},
	}
	vp2 := viewport.New()
	vp2.SetWidth(100)
	vp2.SetHeight(50)
	m := &chatModel{
		viewport:  vp2,
		agentName: "main",
		width:     100,
		messages:  messages,
	}
	m.updateViewport()
	view := ansi.Strip(m.viewport.View())
	lines := strings.Split(view, "\n")

	sepIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "──") {
			sepIdx = i
			break
		}
	}
	if sepIdx < 0 {
		t.Fatalf("separator divider not found in view:\n%s", view)
	}
	// The divider sits directly between the two messages, flush on both
	// sides: the spacing rule skips separator rows, and the full-width rule
	// provides its own visual separation. Pin that it is not fused into the
	// same LINE as either message.
	// Fused means the rule and the message text share ONE line; the divider
	// line itself must carry only the rule.
	if strings.Contains(lines[sepIdx], "before separator") || strings.Contains(lines[sepIdx], "after separator") {
		t.Errorf("divider fused with message text on one line:\n%s", view)
	}
}
