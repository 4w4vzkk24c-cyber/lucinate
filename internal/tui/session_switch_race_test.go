package tui

// Switching sessions while a history fetch is still in flight. Every switch
// builds a new chat model and issues its own fetch; the replies come back in
// whatever order the gateway answers. A reply that belongs to a session the
// operator has already left must not be painted into the one they are on.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// switchRaceApp is the wide composite whose backend answers a history fetch
// with one message naming the session it was asked about.
func switchRaceApp(t *testing.T) AppModel {
	t.Helper()
	fb := &w1ProbeBackend{}
	fb.chatHistoryHook = func(_ context.Context, sessionKey string, _ int) (json.RawMessage, error) {
		body := fmt.Sprintf(`{"messages":[{"role":"assistant","content":[{"type":"text","text":"transcript of %s"}]}]}`, sessionKey)
		return json.RawMessage(body), nil
	}
	m := w1CompositeApp(t, fb)
	return w1Deliver(m, tea.WindowSizeMsg{Width: 140, Height: 40})
}

func switchRaceSelect(m AppModel, key string) AppModel {
	return w1Deliver(m, sessionSelectedMsg{sessionKey: key, agentName: "Scout", modelID: "model-1"})
}

func switchRaceTranscript(m AppModel) string {
	return ansi.Strip(strings.Join(m.chatModel.selLines, "\n"))
}

func TestSessionSwitch_LateHistoryForALeftSessionIsDropped(t *testing.T) {
	m := switchRaceApp(t)

	// Open sess-2; its history fetch is issued but has not answered yet.
	m = switchRaceSelect(m, "sess-2")
	lateLoad := m.chatModel.loadHistory()

	// Move on to sess-3, whose history arrives promptly.
	m = switchRaceSelect(m, "sess-3")
	m = w1Deliver(m, m.chatModel.loadHistory()())
	if got := switchRaceTranscript(m); !strings.Contains(got, "transcript of sess-3") {
		t.Fatalf("sess-3 did not show its own history: %q", got)
	}

	// Now sess-2's reply lands.
	m = w1Deliver(m, lateLoad())

	got := switchRaceTranscript(m)
	if strings.Contains(got, "transcript of sess-2") {
		t.Errorf("sess-3 is showing sess-2's history after a late reply: %q", got)
	}
	if !strings.Contains(got, "transcript of sess-3") {
		t.Errorf("sess-3 lost its own history after a late reply: %q", got)
	}
	if m.chatModel.sessionKey != "sess-3" {
		t.Errorf("open session is %q, want sess-3", m.chatModel.sessionKey)
	}
}

// The reverse order: the left session answers first, while the open one is
// still loading. The open session must keep waiting, not show the wrong chat.
func TestSessionSwitch_EarlyHistoryForALeftSessionIsDropped(t *testing.T) {
	m := switchRaceApp(t)
	m = switchRaceSelect(m, "sess-2")
	staleLoad := m.chatModel.loadHistory()
	m = switchRaceSelect(m, "sess-3")

	m = w1Deliver(m, staleLoad())

	if got := switchRaceTranscript(m); strings.Contains(got, "transcript of sess-2") {
		t.Errorf("sess-3 is showing sess-2's history: %q", got)
	}
	if !m.chatModel.historyLoading {
		t.Error("a reply for another session ended sess-3's own loading state")
	}

	m = w1Deliver(m, m.chatModel.loadHistory()())
	if got := switchRaceTranscript(m); !strings.Contains(got, "transcript of sess-3") {
		t.Errorf("sess-3 never showed its own history: %q", got)
	}
}

// The post-turn refresh has the same shape and the same exposure.
func TestSessionSwitch_LateRefreshForALeftSessionIsDropped(t *testing.T) {
	m := switchRaceApp(t)
	m = switchRaceSelect(m, "sess-2")
	lateRefresh := m.chatModel.refreshHistoryAt(m.chatModel.gen)

	m = switchRaceSelect(m, "sess-3")
	m = w1Deliver(m, m.chatModel.loadHistory()())
	m = w1Deliver(m, lateRefresh())

	got := switchRaceTranscript(m)
	if strings.Contains(got, "transcript of sess-2") {
		t.Errorf("sess-3 is showing sess-2's history after a late refresh: %q", got)
	}
	if !strings.Contains(got, "transcript of sess-3") {
		t.Errorf("sess-3 lost its own history after a late refresh: %q", got)
	}
}

// A reply for the open session is still applied: the guard must not drop
// everything.
func TestSessionSwitch_OwnHistoryIsApplied(t *testing.T) {
	m := switchRaceApp(t)
	m = switchRaceSelect(m, "sess-2")
	m = w1Deliver(m, m.chatModel.loadHistory()())
	if got := switchRaceTranscript(m); !strings.Contains(got, "transcript of sess-2") {
		t.Errorf("sess-2 did not show its own history: %q", got)
	}

	// The refresh must land too, not merely leave the old rows standing:
	// the gateway now answers with a later state of the same session.
	fb := m.backend.(*w1ProbeBackend)
	fb.chatHistoryHook = func(_ context.Context, sessionKey string, _ int) (json.RawMessage, error) {
		body := fmt.Sprintf(`{"messages":[{"role":"assistant","content":[{"type":"text","text":"refreshed %s"}]}]}`, sessionKey)
		return json.RawMessage(body), nil
	}
	m = w1Deliver(m, m.chatModel.refreshHistoryAt(m.chatModel.gen)())
	if got := switchRaceTranscript(m); !strings.Contains(got, "refreshed sess-2") {
		t.Errorf("sess-2's own refresh was not applied: %q", got)
	}
}
