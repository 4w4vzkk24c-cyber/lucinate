package tui

// The spinner shown while the assistant's reply is on its way is magenta
// (operator's choice, 2026-10-02). The spinner on a pending system row
// (compacting, clearing) is a different thing and keeps the accent colour.

import (
	"strings"
	"testing"
)

const (
	spinnerTestMagenta = "38;2;211;54;130" // #d33682
	spinnerTestBlue    = "38;2;38;139;210" // #268bd2
)

// spinnerTestStyling returns the escape codes drawn immediately before the
// current spinner frame on the line containing needle.
func spinnerTestStyling(t *testing.T, m *chatModel, needle string) string {
	t.Helper()
	frame := spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
	for _, line := range m.selLines {
		at := strings.Index(line, frame)
		if at < 0 || !strings.Contains(line, needle) {
			continue
		}
		return line[strings.LastIndex(line[:at], "\x1b["):at]
	}
	t.Fatalf("no spinner on a line containing %q in:\n%q", needle, m.selLines)
	return ""
}

func TestThinkingSpinner_IsMagentaWhileTheReplyStreams(t *testing.T) {
	for _, content := range []string{"", "partial reply so far"} {
		m := senderTestChat(120, "main",
			chatMessage{role: "user", content: "hello"},
			chatMessage{role: "assistant", content: content, streaming: true},
		)
		got := spinnerTestStyling(t, m, "vesper")
		if !strings.Contains(got, spinnerTestMagenta) {
			t.Errorf("content=%q: the reply spinner is styled %q, want magenta %s", content, got, spinnerTestMagenta)
		}
	}
}

func TestThinkingSpinner_PendingSystemRowKeepsTheAccent(t *testing.T) {
	m := senderTestChat(120, "main", chatMessage{role: "system", content: "Compacting session...", pending: true})
	got := spinnerTestStyling(t, m, "Compacting session")
	if !strings.Contains(got, spinnerTestBlue) || strings.Contains(got, spinnerTestMagenta) {
		t.Errorf("the pending-row spinner is styled %q, want the accent %s", got, spinnerTestBlue)
	}
}
