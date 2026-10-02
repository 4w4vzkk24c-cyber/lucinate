package tui

// /archive and /delete act on the open session. Both confirm first, naming
// the session; both then move the chat to another session, since the one it
// was showing has left the list.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/backend"
)

// removeCalls records what the backend was asked to do.
type removeCalls struct {
	archived []string
	deleted  []string
	created  int
	err      error
}

// removeApp is the wide composite parked on sess-2 (of sess-1..3), with a
// backend that records removals.
func removeApp(t *testing.T) (AppModel, *removeCalls) {
	t.Helper()
	calls := &removeCalls{}
	fb := &w1ProbeBackend{}
	fb.sessionArchiveHook = func(_ context.Context, key string) error {
		calls.archived = append(calls.archived, key)
		return calls.err
	}
	fb.sessionDeleteHook = func(_ context.Context, key string) error {
		calls.deleted = append(calls.deleted, key)
		return calls.err
	}
	fb.createSessionHook = func(context.Context, string, string) (string, error) {
		calls.created++
		return "created-session", nil
	}
	m := w1CompositeApp(t, fb)
	m = w1Deliver(m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = w1Deliver(m, sessionSelectedMsg{sessionKey: "sess-2", agentName: "Scout", modelID: "model-1"})
	m.chatModel.historyLoading = false
	return m, calls
}

func removeTranscript(m AppModel) string {
	m.chatModel.updateViewport()
	return ansi.Strip(strings.Join(m.chatModel.selLines, "\n"))
}

// removeAsk types the command and returns the app with whatever it opened.
func removeAsk(t *testing.T, m AppModel, command string) AppModel {
	t.Helper()
	handled, cmd := m.chatModel.handleSlashCommand(command)
	if !handled {
		t.Fatalf("%s is not a recognised command", command)
	}
	if cmd != nil {
		m = w1Deliver(m, cmd())
	}
	return m
}

// removeConfirmed takes the pending confirmation as answered "y" and returns
// the command that performs the removal.
func removeConfirmed(t *testing.T, m *AppModel) tea.Cmd {
	t.Helper()
	if m.chatModel.pendingConfirm == nil {
		t.Fatal("no confirmation is pending")
	}
	action := m.chatModel.pendingConfirm.action
	m.chatModel.pendingConfirm = nil
	return action()
}

// removeRun asks, confirms, and runs everything that follows to completion.
func removeRun(t *testing.T, m AppModel, command string) AppModel {
	t.Helper()
	m = removeAsk(t, m, command)
	return w1Pump(m, removeConfirmed(t, &m), time.Second)
}

func TestSessionRemove_ConfirmsFirstNamingTheSession(t *testing.T) {
	for _, tc := range []struct{ command, word string }{
		{"/archive", "Archive"},
		{"/delete", "permanently"},
	} {
		m, calls := removeApp(t)
		m = removeAsk(t, m, tc.command)

		if len(calls.archived)+len(calls.deleted) != 0 {
			t.Errorf("%s acted before it was confirmed: %+v", tc.command, calls)
		}
		if m.chatModel.pendingConfirm == nil {
			t.Fatalf("%s did not ask for confirmation", tc.command)
		}
		prompt := m.chatModel.pendingConfirm.prompt
		if !strings.Contains(prompt, "sess-2") {
			t.Errorf("%s prompt does not name the session: %q", tc.command, prompt)
		}
		if !strings.Contains(prompt, tc.word) {
			t.Errorf("%s prompt does not say what it does (%q): %q", tc.command, tc.word, prompt)
		}
		if got := removeTranscript(m); !strings.Contains(got, prompt[:20]) {
			t.Errorf("%s prompt is not shown in the chat: %q", tc.command, got)
		}
	}
}

func TestSessionRemove_ArchivesTheOpenSessionAndMovesOn(t *testing.T) {
	m, calls := removeApp(t)
	m = removeRun(t, m, "/archive")

	if len(calls.archived) != 1 || calls.archived[0] != "sess-2" {
		t.Errorf("archived %v, want exactly sess-2", calls.archived)
	}
	if len(calls.deleted) != 0 {
		t.Errorf("/archive deleted %v", calls.deleted)
	}
	if got := m.chatModel.sessionKey; got != "sess-3" && got != "sess-1" {
		t.Errorf("after the archive the chat is on %q, want a neighbouring session", got)
	}
}

func TestSessionRemove_DeletesTheOpenSessionAndMovesOn(t *testing.T) {
	m, calls := removeApp(t)
	m = removeRun(t, m, "/delete")

	if len(calls.deleted) != 1 || calls.deleted[0] != "sess-2" {
		t.Errorf("deleted %v, want exactly sess-2", calls.deleted)
	}
	if len(calls.archived) != 0 {
		t.Errorf("/delete archived %v", calls.archived)
	}
	if got := m.chatModel.sessionKey; got != "sess-3" && got != "sess-1" {
		t.Errorf("after the delete the chat is on %q, want a neighbouring session", got)
	}
}

func TestSessionRemove_WithNoOtherSessionStartsANewOne(t *testing.T) {
	m, calls := removeApp(t)
	m.sessionsModel, _ = m.sessionsModel.Update(sessionsLoadedMsg{sessions: []sessionItem{{key: "sess-2", title: "Only"}}})

	m = removeRun(t, m, "/delete")

	if calls.created != 1 {
		t.Fatalf("created %d sessions after removing the only one, want 1", calls.created)
	}
	if m.chatModel.sessionKey != "created-session" {
		t.Errorf("the chat is on %q, want the newly created session", m.chatModel.sessionKey)
	}
}

func TestSessionRemove_FailureStaysPutAndSaysWhy(t *testing.T) {
	for _, command := range []string{"/archive", "/delete"} {
		m, calls := removeApp(t)
		calls.err = errors.New("gateway refused: session is busy")
		m = removeRun(t, m, command)

		if m.chatModel.sessionKey != "sess-2" {
			t.Errorf("%s: a failure moved the chat to %q", command, m.chatModel.sessionKey)
		}
		if calls.created != 0 {
			t.Errorf("%s: a failure still created a session", command)
		}
		if got := removeTranscript(m); !strings.Contains(got, "gateway refused: session is busy") {
			t.Errorf("%s: the failure is not shown in the chat: %q", command, got)
		}
	}
}

// The outcome of a removal belongs to the session it was asked for. If the
// operator has moved on by the time it lands, the chat they are on stays.
func TestSessionRemove_OutcomeForALeftSessionDoesNotMoveTheChat(t *testing.T) {
	m, calls := removeApp(t)
	m = removeAsk(t, m, "/archive")
	late := removeConfirmed(t, &m)

	m = w1Deliver(m, sessionSelectedMsg{sessionKey: "sess-1", agentName: "Scout", modelID: "model-1"})
	m = w1Pump(m, late, time.Second)

	// The target was pinned when the prompt opened: confirming archives the
	// session that was named, not whichever one is open by then.
	if len(calls.archived) != 1 || calls.archived[0] != "sess-2" {
		t.Fatalf("archived %v, want exactly the session the prompt named, sess-2", calls.archived)
	}
	if m.chatModel.sessionKey != "sess-1" {
		t.Errorf("a late archive outcome moved the chat off sess-1 to %q", m.chatModel.sessionKey)
	}
}

// backendWithoutArchive exposes only the core Backend methods, hiding every
// optional capability, archive included.
type backendWithoutArchive struct{ backend.Backend }

func TestSessionRemove_ArchiveUnavailableOnABackendWithoutIt(t *testing.T) {
	m, calls := removeApp(t)
	m.chatModel.backend = backendWithoutArchive{m.chatModel.backend}

	m = removeAsk(t, m, "/archive")

	if m.chatModel.pendingConfirm != nil {
		t.Error("/archive asked to confirm on a backend that cannot archive")
	}
	if len(calls.archived) != 0 {
		t.Errorf("archived %v on a backend that cannot archive", calls.archived)
	}
	if got := removeTranscript(m); !strings.Contains(got, "/archive is not available") {
		t.Errorf("the chat does not say /archive is unavailable: %q", got)
	}
}

func TestSessionRemove_CommandsAreListedAndComplete(t *testing.T) {
	for _, command := range []string{"/archive", "/delete"} {
		if !strings.Contains(helpBody, command+" — ") {
			t.Errorf("%s is missing from /help", command)
		}
		found := false
		for _, c := range slashCommands {
			if c == command {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is missing from tab completion", command)
		}
	}
}
