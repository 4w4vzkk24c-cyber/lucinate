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

// sessionParkBehindTranscript puts the open chat behind a cron transcript,
// the way opening one from the crons list does.
func sessionParkBehindTranscript(m AppModel) AppModel {
	m.cronsReturnChat, m.cronsReturnValid = m.chatModel, true
	m.chatModel = newChatModel(m.backend, "", "agent-1", "Scout", "", m.prefs, true, "", "", false)
	m.chatModel.transcript = true
	m.chatModel.historyLoading = false
	m.chatModel.messages = []chatMessage{{role: "assistant", content: "cron run summary"}}
	m.applyChatLayout()
	return m
}

// sessionTranscriptUntouched fails the test if the open cron transcript was
// changed or replaced.
func sessionTranscriptUntouched(t *testing.T, m AppModel) {
	t.Helper()
	open := m.chatModel
	if !open.transcript || open.sessionKey != "" || len(open.messages) != 1 || open.messages[0].content != "cron run summary" {
		t.Errorf("the open cron transcript was changed: transcript=%v key=%q rows=%+v", open.transcript, open.sessionKey, open.messages)
	}
}

func sessionParkedTranscript(m AppModel) string {
	m.cronsReturnChat.updateViewport()
	return ansi.Strip(strings.Join(m.cronsReturnChat.selLines, "\n"))
}

// The outcome of a removal goes to the chat it was issued from. When that
// chat is parked behind a cron transcript, it is the one moved to a
// neighbour, and the transcript the operator is reading stays.
func TestSessionRemove_OutcomeReachesTheParkedChat(t *testing.T) {
	m, _ := removeApp(t) // sess-2 open
	m = removeAsk(t, m, "/delete")
	late := removeConfirmed(t, &m)
	m = sessionParkBehindTranscript(m)

	m = w1Pump(m, late, time.Second)

	sessionTranscriptUntouched(t, m)
	if !m.cronsReturnValid {
		t.Fatal("the parked chat was discarded")
	}
	if got := m.cronsReturnChat.sessionKey; got != "sess-3" {
		t.Errorf("the parked chat is on %q after its session was deleted, want the neighbour sess-3", got)
	}
	if m.cronsReturnChat.sessionGone {
		t.Error("the chat that replaced the parked one is marked as on a deleted session")
	}
	if m.state != viewChat {
		t.Errorf("the view changed to %v", m.state)
	}
}

func TestSessionRemove_FailureIsWrittenToTheParkedChat(t *testing.T) {
	m, calls := removeApp(t)
	calls.err = errors.New("gateway refused: session is busy")
	m = removeAsk(t, m, "/archive")
	late := removeConfirmed(t, &m)
	m = sessionParkBehindTranscript(m)

	m = w1Pump(m, late, time.Second)

	sessionTranscriptUntouched(t, m)
	if m.cronsReturnChat.sessionKey != "sess-2" || m.cronsReturnChat.sessionGone {
		t.Errorf("a failed archive moved or retired the parked chat: key %q gone=%v", m.cronsReturnChat.sessionKey, m.cronsReturnChat.sessionGone)
	}
	if got := sessionParkedTranscript(m); !strings.Contains(got, "gateway refused: session is busy") {
		t.Errorf("the failure is not shown in the parked chat: %q", got)
	}
}

// The last session, removed while its chat is parked: the session created
// in its place opens in the parked chat.
func TestSessionRemove_LastSessionParkedGetsANewSessionInPlace(t *testing.T) {
	m, calls := removeApp(t)
	m.sessionsModel, _ = m.sessionsModel.Update(sessionsLoadedMsg{sessions: []sessionItem{{key: "sess-2", title: "Only"}}})
	m = removeAsk(t, m, "/delete")
	late := removeConfirmed(t, &m)
	m = sessionParkBehindTranscript(m)

	m = w1Pump(m, late, time.Second)

	sessionTranscriptUntouched(t, m)
	if calls.created != 1 || m.cronsReturnChat.sessionKey != "created-session" {
		t.Errorf("created %d sessions and the parked chat is on %q, want it on the one new session", calls.created, m.cronsReturnChat.sessionKey)
	}
}

// A removal that lands while the operator is in another view moves the chat
// and leaves the operator where they are.
func TestSessionRemove_OutcomeDoesNotChangeTheView(t *testing.T) {
	m, _ := removeApp(t)
	m = removeAsk(t, m, "/delete")
	late := removeConfirmed(t, &m)
	m.state = viewConfig
	m.sidebarFocus = true

	m = w1Pump(m, late, time.Second)

	if m.state != viewConfig {
		t.Errorf("a removal that landed in the config view moved the operator to view %v", m.state)
	}
	if !m.sidebarFocus {
		t.Error("a removal outcome took focus from the sidebar")
	}
	if m.chatModel.sessionKey != "sess-3" {
		t.Errorf("the chat is on %q, want the neighbour sess-3", m.chatModel.sessionKey)
	}
}

// sessionCursorKey is the session the sidebar cursor is on.
func sessionCursorKey(m AppModel) string {
	if item, ok := m.sessionsModel.list.SelectedItem().(sessionItem); ok {
		return item.key
	}
	return ""
}

// An in-place follow-up moves the sidebar cursor to the session it opened,
// unless the operator is using the sidebar: then the cursor is theirs.
func TestSessionSelect_InPlaceMovesTheCursorOnlyWhenTheSidebarIsUnfocused(t *testing.T) {
	follow := sessionSelectedMsg{sessionKey: "sess-3", agentName: "Scout", modelID: "model-1", inPlaceOf: "sess-2"}

	m, _ := removeApp(t)
	m.syncSidebarCursor("sess-1")
	m = w1Deliver(m, follow)
	if got := sessionCursorKey(m); got != "sess-3" {
		t.Errorf("with the chat focused the cursor is on %q, want the session just opened, sess-3", got)
	}

	m, _ = removeApp(t)
	m.syncSidebarCursor("sess-1")
	m.sidebarFocus = true
	m = w1Deliver(m, follow)
	if got := sessionCursorKey(m); got != "sess-1" {
		t.Errorf("with the sidebar focused the cursor was moved to %q, want it left on sess-1", got)
	}

	m, _ = removeApp(t)
	m.syncSidebarCursor("sess-1")
	m = sessionParkBehindTranscript(m)
	m = w1Deliver(m, follow)
	if got := sessionCursorKey(m); got != "sess-1" {
		t.Errorf("a follow-up for the parked chat moved the cursor to %q, want it left on sess-1", got)
	}
}

// sessionStartupReplies runs a command tree and reports whether it holds
// the unkeyed part of a chat's startup (skill discovery stands for it).
func sessionStartupReplies(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case skillsDiscoveredMsg:
		return true
	case tea.BatchMsg:
		for _, c := range msg {
			if sessionStartupReplies(c) {
				return true
			}
		}
	}
	return false
}

// A chat opened in place while it cannot hear unkeyed replies (parked, or
// the operator in another view, or the sidebar focused) loads its history at
// once and holds the rest of its startup until it has focus. Issued early,
// those replies would be written into whatever the operator is looking at.
func TestSessionSelect_InPlaceStartupWaitsForFocus(t *testing.T) {
	follow := sessionSelectedMsg{sessionKey: "sess-3", agentName: "Scout", modelID: "model-1", inPlaceOf: "sess-2"}

	t.Run("parked behind a cron transcript", func(t *testing.T) {
		m, _ := removeApp(t)
		m = sessionParkBehindTranscript(m)

		next, cmd := m.Update(follow)
		m = next.(AppModel)
		if sessionStartupReplies(cmd) {
			t.Fatal("a chat opened while parked issued its unkeyed startup at once")
		}
		if !m.cronsReturnChat.initPending {
			t.Fatal("the parked chat does not know its startup is still owed")
		}

		m.cronsReturn = viewChat
		next, cmd = m.Update(goBackFromCronsMsg{})
		m = next.(AppModel)
		if !sessionStartupReplies(cmd) || m.chatModel.initPending {
			t.Error("the restored chat did not finish starting up once it had focus")
		}
		_, cmd = m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
		if sessionStartupReplies(cmd) {
			t.Error("the startup was issued a second time")
		}
	})

	t.Run("the sidebar has focus", func(t *testing.T) {
		m, _ := removeApp(t)
		m.sidebarFocus = true

		next, cmd := m.Update(follow)
		m = next.(AppModel)
		if sessionStartupReplies(cmd) || !m.chatModel.initPending {
			t.Fatal("a chat opened under a focused sidebar issued its unkeyed startup at once")
		}

		m.sidebarFocus = false
		next, cmd = m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
		if !sessionStartupReplies(cmd) || next.(AppModel).chatModel.initPending {
			t.Error("the chat did not finish starting up once it had focus")
		}
	})

	t.Run("the operator is in another view", func(t *testing.T) {
		m, _ := removeApp(t)
		m.state = viewConfig
		m.configReturn = viewChat

		next, cmd := m.Update(follow)
		m = next.(AppModel)
		if sessionStartupReplies(cmd) || !m.chatModel.initPending {
			t.Fatal("a chat opened while the config view is showing issued its unkeyed startup at once")
		}

		next, cmd = m.Update(goBackFromConfigMsg{})
		if !sessionStartupReplies(cmd) || next.(AppModel).chatModel.initPending {
			t.Error("the chat did not finish starting up once the chat view was showing")
		}
	})

	t.Run("the chat has focus", func(t *testing.T) {
		m, _ := removeApp(t)

		next, cmd := m.Update(follow)
		if !sessionStartupReplies(cmd) || next.(AppModel).chatModel.initPending {
			t.Error("a chat opened in place with focus did not start up at once")
		}
	})
}

// The follow-up switch names the chat it replaces. If that chat is gone by
// the time it lands, nothing is opened.
func TestSessionSelect_InPlaceOfAChatThatIsGoneOpensNothing(t *testing.T) {
	m, _ := removeApp(t) // sess-2 open

	m = w1Deliver(m, sessionSelectedMsg{sessionKey: "sess-3", agentName: "Scout", modelID: "model-1", inPlaceOf: "sess-9"})

	if m.chatModel.sessionKey != "sess-2" {
		t.Errorf("a follow-up for a chat that is gone replaced the open chat with %q", m.chatModel.sessionKey)
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
