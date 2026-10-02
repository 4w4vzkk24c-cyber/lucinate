package tui

// /new starts a fresh session from the chat composer and opens it, without
// a detour through the sessions browser.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// newSessionApp is the wide composite, parked on sess-1, whose backend
// reports every session it is asked to create.
func newSessionApp(t *testing.T, create func(agentID, key string) (string, error)) AppModel {
	t.Helper()
	fb := &w1ProbeBackend{}
	fb.createSessionHook = func(_ context.Context, agentID, key string) (string, error) {
		return create(agentID, key)
	}
	m := w1CompositeApp(t, fb)
	return w1Deliver(m, tea.WindowSizeMsg{Width: 140, Height: 40})
}

// newSessionRun types /new into the chat and runs the command it returns.
func newSessionRun(t *testing.T, m AppModel) AppModel {
	t.Helper()
	handled, cmd := m.chatModel.handleSlashCommand("/new")
	if !handled {
		t.Fatal("/new is not a recognised command")
	}
	if cmd == nil {
		t.Fatal("/new returned no command")
	}
	return w1Deliver(m, cmd())
}

func TestNewSessionCommand_CreatesAndOpensASession(t *testing.T) {
	var askedAgent, askedKey string
	m := newSessionApp(t, func(agentID, key string) (string, error) {
		askedAgent, askedKey = agentID, key
		return "agent:agent-1:" + key, nil
	})

	m = newSessionRun(t, m)

	if askedAgent != "agent-1" {
		t.Errorf("session created for agent %q, want the open chat's agent agent-1", askedAgent)
	}
	if askedKey == "" {
		t.Error("session created with an empty key")
	}
	if want := "agent:agent-1:" + askedKey; m.chatModel.sessionKey != want {
		t.Errorf("open session is %q, want the created session %q", m.chatModel.sessionKey, want)
	}
	if m.state != viewChat {
		t.Errorf("view is %v after /new, want the chat view", m.state)
	}
	if len(m.chatModel.messages) != 0 {
		t.Errorf("the new session opened with %d messages carried over", len(m.chatModel.messages))
	}
}

func TestNewSessionCommand_FailureStaysOnTheOpenSessionAndSaysWhy(t *testing.T) {
	m := newSessionApp(t, func(string, string) (string, error) {
		return "", errors.New("gateway refused: quota")
	})
	m.chatModel.historyLoading = false

	m = newSessionRun(t, m)

	if m.chatModel.sessionKey != "sess-1" {
		t.Errorf("a failed /new moved the chat to %q, want it left on sess-1", m.chatModel.sessionKey)
	}
	if got := ansi.Strip(strings.Join(m.chatModel.selLines, "\n")); !strings.Contains(got, "gateway refused: quota") {
		t.Errorf("the failure is not shown in the chat: %q", got)
	}
	if m.sessionsModel.err != nil {
		t.Errorf("a failed /new put the sidebar into its error state: %v", m.sessionsModel.err)
	}
}

// newSessionAsk types /new and returns the command, so the test can move
// the operator before the reply lands.
func newSessionAsk(t *testing.T, m AppModel) tea.Cmd {
	t.Helper()
	handled, cmd := m.chatModel.handleSlashCommand("/new")
	if !handled || cmd == nil {
		t.Fatal("/new returned no command")
	}
	return cmd
}

func newSessionOK(_, key string) (string, error) { return "agent:agent-1:" + key, nil }

func newSessionRefused(string, string) (string, error) {
	return "", errors.New("gateway refused: quota")
}

// /new names the chat it was typed into; the browser's "new session" names none.
func TestNewSessionCommand_ReplyNamesTheChatItWasIssuedFrom(t *testing.T) {
	m := newSessionApp(t, newSessionOK)

	fromChat := newSessionAsk(t, m)().(newSessionCreatedMsg)
	if fromChat.inPlaceOf != "sess-1" {
		t.Errorf("/new typed into sess-1 replies inPlaceOf=%q", fromChat.inPlaceOf)
	}

	_, cmd := m.sessionsModel.TriggerAction("new-session")
	if cmd == nil {
		t.Fatal("the browser's new-session action returned no command")
	}
	if fromBrowser := cmd().(newSessionCreatedMsg); fromBrowser.inPlaceOf != "" {
		t.Errorf("the browser's new session replies inPlaceOf=%q, want none", fromBrowser.inPlaceOf)
	}
}

// The new session replaces the chat /new was typed into, wherever that chat
// is by the time the gateway answers.
func TestNewSessionCommand_OpensInTheParkedChat(t *testing.T) {
	m := newSessionApp(t, newSessionOK)
	m.chatModel.historyLoading = false
	late := newSessionAsk(t, m)
	m = sessionParkBehindTranscript(m)

	m = w1Deliver(m, late())

	sessionTranscriptUntouched(t, m)
	if got := m.cronsReturnChat.sessionKey; !strings.HasPrefix(got, "agent:agent-1:") {
		t.Errorf("the parked chat is on %q, want the created session", got)
	}
}

func TestNewSessionCommand_FailureIsWrittenToTheParkedChat(t *testing.T) {
	m := newSessionApp(t, newSessionRefused)
	m.chatModel.historyLoading = false
	late := newSessionAsk(t, m)
	m = sessionParkBehindTranscript(m)

	m = w1Deliver(m, late())

	sessionTranscriptUntouched(t, m)
	if got := sessionParkedTranscript(m); !strings.Contains(got, "gateway refused: quota") {
		t.Errorf("the failure is not shown in the parked chat: %q", got)
	}
	if m.sessionsModel.err != nil {
		t.Errorf("a failed /new put the sidebar into its error state: %v", m.sessionsModel.err)
	}
}

// Landing in another view, /new still opens in its chat and leaves the
// operator where they are; a failure goes to that chat, not the sidebar.
func TestNewSessionCommand_OutcomeDoesNotChangeTheView(t *testing.T) {
	m := newSessionApp(t, newSessionOK)
	late := newSessionAsk(t, m)
	m.state = viewConfig

	m = w1Deliver(m, late())

	if m.state != viewConfig {
		t.Errorf("a /new that landed in the config view moved the operator to view %v", m.state)
	}
	if !strings.HasPrefix(m.chatModel.sessionKey, "agent:agent-1:") {
		t.Errorf("the chat is on %q, want the created session", m.chatModel.sessionKey)
	}

	m = newSessionApp(t, newSessionRefused)
	m.chatModel.historyLoading = false
	late = newSessionAsk(t, m)
	m.state = viewConfig

	m = w1Deliver(m, late())

	if m.sessionsModel.err != nil {
		t.Errorf("a failed /new that landed in another view put the sidebar into its error state: %v", m.sessionsModel.err)
	}
	if got := removeTranscript(m); !strings.Contains(got, "gateway refused: quota") {
		t.Errorf("the failure is not shown in the chat it was asked from: %q", got)
	}
}

// If the operator has switched sessions before the gateway answers, the
// chat they are on is left alone: the new session is listed, not opened.
func TestNewSessionCommand_AfterASwitchOpensNothing(t *testing.T) {
	for name, create := range map[string]func(string, string) (string, error){"success": newSessionOK, "failure": newSessionRefused} {
		m := newSessionApp(t, create)
		late := newSessionAsk(t, m)
		m = w1Deliver(m, sessionSelectedMsg{sessionKey: "sess-2", agentName: "Scout", modelID: "model-1"})
		m.chatModel.historyLoading = false
		rows := len(m.chatModel.messages)

		m = w1Deliver(m, late())

		if m.chatModel.sessionKey != "sess-2" || len(m.chatModel.messages) != rows {
			t.Errorf("%s: a late /new outcome changed the chat the operator moved to: key %q, %d rows (was %d)", name, m.chatModel.sessionKey, len(m.chatModel.messages), rows)
		}
		if m.sessionsModel.err != nil {
			t.Errorf("%s: a late /new outcome put the sidebar into its error state", name)
		}
	}
}

func TestNewSessionCommand_IsListedAndCompletes(t *testing.T) {
	if !strings.Contains(helpBody, "/new — ") {
		t.Error("/new is missing from /help")
	}
	found := false
	for _, c := range slashCommands {
		if c == "/new" {
			found = true
		}
	}
	if !found {
		t.Error("/new is missing from tab completion")
	}
}

// With queued messages, /new replaces the chat and would drop them, so it
// asks first like every other navigation that replaces the chat.
func TestNewSessionCommand_ConfirmsBeforeDroppingQueuedMessages(t *testing.T) {
	created := false
	m := newSessionApp(t, func(_, key string) (string, error) {
		created = true
		return key, nil
	})
	m.chatModel.pendingMessages = []string{"not sent yet"}

	handled, cmd := m.chatModel.handleSlashCommand("/new")
	if !handled {
		t.Fatal("/new is not a recognised command")
	}
	if cmd != nil {
		w1Deliver(m, cmd())
	}
	if created {
		t.Error("/new created a session without confirming the queued message would be dropped")
	}
	if m.chatModel.pendingNavConfirm == nil {
		t.Error("/new did not ask before dropping a queued message")
	}
}

// The sidebar highlight follows the open session across a list refresh, so
// a session /new just created is the one marked once the list reloads.
func TestSidebarCursor_FollowsTheOpenSessionAcrossARefresh(t *testing.T) {
	m := newSessionApp(t, func(_, key string) (string, error) { return key, nil })
	m = w1Deliver(m, sessionSelectedMsg{sessionKey: "sess-3", agentName: "Scout", modelID: "model-1"})

	m = w1Deliver(m, sessionsLoadedMsg{sessions: []sessionItem{
		{key: "sess-9", title: "Newest"},
		{key: "sess-1", title: "First"},
		{key: "sess-3", title: "Third"},
	}})

	selected, ok := m.sessionsModel.list.SelectedItem().(sessionItem)
	if !ok || selected.key != "sess-3" {
		t.Errorf("sidebar highlights %+v after a refresh, want the open session sess-3", m.sessionsModel.list.SelectedItem())
	}
}

// While the operator is steering the sidebar, a refresh must not yank the
// cursor back to the open session.
func TestSidebarCursor_NotYankedWhileTheSidebarHasFocus(t *testing.T) {
	m := newSessionApp(t, func(_, key string) (string, error) { return key, nil })
	m = w1Deliver(m, sessionSelectedMsg{sessionKey: "sess-3", agentName: "Scout", modelID: "model-1"})
	m.sidebarFocus = true

	m = w1Deliver(m, sessionsLoadedMsg{sessions: []sessionItem{
		{key: "sess-9", title: "Newest"},
		{key: "sess-1", title: "First"},
		{key: "sess-3", title: "Third"},
	}})

	if selected, ok := m.sessionsModel.list.SelectedItem().(sessionItem); ok && selected.key == "sess-3" {
		t.Error("a refresh moved the focused sidebar's cursor to the open session")
	}
}

// After /new the sidebar is reloaded, so the new session is listed without
// waiting for a gateway event.
func TestNewSessionCommand_ReloadsTheSidebar(t *testing.T) {
	m := newSessionApp(t, func(_, key string) (string, error) { return "fresh-session", nil })
	listed := false
	m.backend.(*w1ProbeBackend).sessionsListHook = func(context.Context, string) (json.RawMessage, error) {
		listed = true
		return json.RawMessage(`{"sessions":[{"key":"fresh-session","derivedTitle":"Fresh"},{"key":"sess-1","derivedTitle":"First"}]}`), nil
	}
	handled, cmd := m.chatModel.handleSlashCommand("/new")
	if !handled || cmd == nil {
		t.Fatal("/new returned no command")
	}
	next, after := m.Update(cmd())
	m = w1Pump(next.(AppModel), after, 2*time.Second)

	if !listed {
		t.Fatal("/new did not reload the session list")
	}
	selected, ok := m.sessionsModel.list.SelectedItem().(sessionItem)
	if !ok || selected.key != "fresh-session" {
		t.Errorf("sidebar highlights %+v after /new, want the new session", m.sessionsModel.list.SelectedItem())
	}
}
