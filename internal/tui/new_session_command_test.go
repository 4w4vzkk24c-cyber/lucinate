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
