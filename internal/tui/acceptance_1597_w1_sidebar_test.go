package tui

// Acceptance suite for forge card 1597 (LUCINATE-V1-01), work item W1:
// persistent sessions sidebar.
//
// Contract (spec 1597.json, sha256 dca1ab2b…): these tests are RED at base
// 0d0390a — each failing assertion names a mechanism that does not exist yet
// (composite join, focus routing, sessions.changed fanout, re-subscription).
// They compile at base because they reference only pre-existing symbols;
// post-W1 mechanisms are probed behaviourally, never by new symbol name.
// Existing test files are untouched (spec forbidden[] F1/F3).
//
// Commands returned by Update are executed by w1Pump, the way the bubbletea
// program would: without that, a debounced-refresh implementation that runs
// as a tea.Cmd would never fire in a hermetic test.

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// w1ProbeBackend records SessionsSubscribe calls so the reconnect
// re-subscription pin (W1-AC5) can count them. Defining the method here is
// legal at base, where nothing requires it; at base the count stays 0, which
// is exactly what the re-subscribe assertion rejects.
type w1ProbeBackend struct {
	fakeBackend
	subscribeCalls int32 // atomic
}

func (f *w1ProbeBackend) SessionsSubscribe(ctx context.Context) (json.RawMessage, error) {
	atomic.AddInt32(&f.subscribeCalls, 1)
	return json.RawMessage("{}"), nil
}

const w1SidebarTitle = "Sessions"

// w1CompositeApp builds an AppModel parked in viewChat with a populated
// sessions list and a probe backend — the state the sidebar lives in once
// W1 lands. The size message is NOT sent here; callers send the width they
// are pinning.
func w1CompositeApp(t *testing.T, fb *w1ProbeBackend) AppModel {
	t.Helper()
	m := NewApp(nil, AppOptions{
		Store:          &config.Connections{},
		BackendFactory: func(*config.Connection) (backend.Backend, error) { return fb, nil },
	})
	m.backend = fb
	m.chatModel = newChatModel(fb, "sess-1", "agent-1", "Scout", "model-1", config.DefaultPreferences(), false, "", "", false)
	m.chatModel.viewport = viewport.New()
	m.sessionsModel = newSessionsModel(fb, "agent-1", "Scout", "model-1", "sess-1", true, nil, false)
	// Pre-size the list so SetItems does not trip pagination on a zero
	// width. Post-W1 the app re-sizes the sidebar from the window width.
	m.sessionsModel.setSize(30, 40)
	m.sessionsModel, _ = m.sessionsModel.Update(sessionsLoadedMsg{
		sessions: []sessionItem{
			{key: "sess-1", title: "First"},
			{key: "sess-2", title: "Second"},
			{key: "sess-3", title: "Third"},
		},
	})
	// sessionsLoadedMsg parks the selection past the (absent) group header
	// at index 1; normalise to 0 so cursor-movement assertions have a
	// stable baseline.
	m.sessionsModel.list.Select(0)
	m.state = viewChat
	return m
}

// w1Deliver feeds one message into the app and returns the updated model.
func w1Deliver(m AppModel, msg tea.Msg) AppModel {
	next, _ := m.Update(msg)
	if am, ok := next.(AppModel); ok {
		return am
	}
	return m
}

// w1Pump executes returned commands and feeds produced messages back into
// the app, bounded by wall clock and iterations, so async mechanisms
// (debounce cmds, history fetch) actually run under test.
func w1Pump(m AppModel, cmd tea.Cmd, wall time.Duration) AppModel {
	deadline := time.Now().Add(wall)
	pending := []tea.Cmd{cmd}
	for i := 0; i < 500 && len(pending) > 0 && time.Now().Before(deadline); i++ {
		c := pending[0]
		pending = pending[1:]
		if c == nil {
			continue
		}
		msg := c()
		if msg == nil {
			continue
		}
		next, nc := m.Update(msg)
		if am, ok := next.(AppModel); ok {
			m = am
		}
		if nc != nil {
			pending = append(pending, nc)
		}
		// Batch results expand into their constituent commands — the
		// program loop would run each of them.
		if bm, ok := msg.(tea.BatchMsg); ok {
			pending = append(pending, bm...)
		}
	}
	return m
}

// W1-AC1 / M3: at >=120 cols both panes render concurrently and the sidebar
// width is clamp(20, 30% of cols, 70). At base the size goes to chat alone:
// the sidebar title is absent from the chat render and the list width is
// never re-derived from the window (still the 30-col fixture pre-size) —
// the title and upper-clamp assertions are red.
func TestW1AC1_CompositePanesAt120ColsWithClampedSidebar(t *testing.T) {
	fb := &w1ProbeBackend{}
	m := w1CompositeApp(t, fb)
	m = w1Deliver(m, tea.WindowSizeMsg{Width: 120, Height: 40})

	view := m.View().Content
	if !strings.Contains(view, w1SidebarTitle) {
		t.Errorf("W1-AC1: at 120 cols the sidebar pane is missing from the render (renamed-modal shape, ban B3); view does not contain %q", w1SidebarTitle)
	}

	w := m.sessionsModel.list.Width()
	if w < 20 || w > 70 {
		t.Errorf("W1-AC1: sidebar width %d outside clamp(20, 30%% of 120, 70) band [20,70]", w)
	}

	// Upper clamp: 30% of 250 = 75 exceeds the 70 ceiling.
	m250 := w1CompositeApp(t, fb)
	m250 = w1Deliver(m250, tea.WindowSizeMsg{Width: 250, Height: 40})
	if got := m250.sessionsModel.list.Width(); got != 70 {
		t.Errorf("W1-AC1: sidebar width at 250 cols = %d, want upper clamp 70", got)
	}

	// Single-line item height and zero spacing
	if h := (sessionDelegate{}).Height(); h != 1 {
		t.Errorf("W1-AC1: sessionDelegate.Height() = %d, want 1 for single-line density", h)
	}
	if s := (sessionDelegate{}).Spacing(); s != 0 {
		t.Errorf("W1-AC1: sessionDelegate.Spacing() = %d, want 0", s)
	}
}

// W1-AC2 / M4: keystrokes typed into the chat input never move the sidebar;
// ctrl+s toggles focus and only then do nav keys move the selection. At
// base ctrl+s is unbound in viewChat, so the post-toggle navigation
// assertion is red.
func TestW1AC2_FocusIsolationAndCtrlSToggle(t *testing.T) {
	fb := &w1ProbeBackend{}
	m := w1CompositeApp(t, fb)
	m = w1Deliver(m, tea.WindowSizeMsg{Width: 140, Height: 40})

	typed := []tea.KeyPressMsg{
		{Code: 'h', Text: "h"}, {Code: 'i', Text: "i"},
		{Code: tea.KeyDown}, {Code: tea.KeyDown},
	}
	for _, k := range typed {
		next, _ := m.Update(k)
		m = next.(AppModel)
	}
	if got := m.sessionsModel.list.Index(); got != 0 {
		t.Errorf("W1-AC2: sidebar cursor moved to %d from chat-typed keys with sidebar unfocused", got)
	}
	if got := m.chatModel.textarea.Value(); got != "hi" {
		t.Errorf("W1-AC2: chat input lost typed keys, got %q", got)
	}

	toggle := tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	next, _ := m.Update(toggle)
	m = next.(AppModel)
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(AppModel)
	if got := m.sessionsModel.list.Index(); got == 0 {
		t.Errorf("W1-AC2: ctrl+s did not move focus to the sidebar (down after toggle did not advance the selection)")
	}

	next, _ = m.Update(toggle)
	m = next.(AppModel)
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(AppModel)
	if got := m.sessionsModel.list.Index(); got != 0 {
		t.Errorf("W1-AC2: after toggling focus back to chat, down moved the sidebar cursor to %d", got)
	}
}

// W1-AC3 / M18: selecting a session in the sidebar switches the chat to it
// and restores history via the existing sessionSelectedMsg -> fetchHistory
// flow. At base there is no sidebar in viewChat to select from, so neither
// the session key nor the history fetch happens — red.
func TestW1AC3_SidebarSelectionSwitchesChatAndRestoresHistory(t *testing.T) {
	fb := &w1ProbeBackend{}
	var historyFor atomic.Value
	historyFor.Store("")
	fb.chatHistoryHook = func(ctx context.Context, sessionKey string, limit int) (json.RawMessage, error) {
		historyFor.Store(sessionKey)
		return json.RawMessage(`{"messages":[]}`), nil
	}
	m := w1CompositeApp(t, fb)
	m = w1Deliver(m, tea.WindowSizeMsg{Width: 140, Height: 40})

	next, _ := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(AppModel)
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(AppModel)
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(AppModel)
	if cmd == nil {
		t.Fatalf("W1-AC3: enter on the focused sidebar produced no command (selection wiring absent, M18)")
	}
	m = w1Pump(m, cmd, 2*time.Second)

	if got := m.chatModel.sessionKey; got != "sess-2" {
		t.Errorf("W1-AC3: chat session key = %q after selecting sess-2 in the sidebar, want sess-2", got)
	}
	if got, _ := historyFor.Load().(string); got != "sess-2" {
		t.Errorf("W1-AC3: fetchHistory issued for %q, want sess-2 (history restore skipped)", got)
	}
}

// W1-AC4 / M5 + M7: a sessions.changed event fans into a ~500 ms
// trailing-debounced refresh (SessionsList re-invocation within 2 s), and a
// trailing re-list fires after the event gap goes quiet. At base the event
// is ignored and SessionsList is never re-invoked — red.
func TestW1AC4_SessionsChangedDebouncedRefreshAndTrailingRelist(t *testing.T) {
	fb := &w1ProbeBackend{}
	var lists int32
	fb.sessionsListHook = func(ctx context.Context, agentID string) (json.RawMessage, error) {
		atomic.AddInt32(&lists, 1)
		return json.RawMessage(`{"sessions":[{"key":"sess-new","derivedTitle":"Brand New","updatedAt":99}]}`), nil
	}
	m := w1CompositeApp(t, fb)
	m = w1Deliver(m, tea.WindowSizeMsg{Width: 140, Height: 40})
	time.Sleep(150 * time.Millisecond) // let any startup list settle
	baseline := atomic.LoadInt32(&lists)

	changed := GatewayEventMsg(protocol.Event{EventName: "sessions.changed", Payload: json.RawMessage(`{"key":"sess-new"}`)})
	next, cmd := m.Update(changed)
	m = next.(AppModel)
	m = w1Pump(m, cmd, 500*time.Millisecond)
	next, cmd = m.Update(changed)
	m = next.(AppModel)
	m = w1Pump(m, cmd, 500*time.Millisecond)
	next, cmd = m.Update(changed)
	m = next.(AppModel)
	m = w1Pump(m, cmd, time.Second)

	if got := atomic.LoadInt32(&lists); got <= baseline {
		t.Fatalf("W1-AC4: no SessionsList refresh within 2s of sessions.changed (event fanout deleted, M5); lists=%d baseline=%d", got, baseline)
	}
	afterFirst := atomic.LoadInt32(&lists)

	// Gap pattern: two more events, then quiet — the trailing re-list must
	// still fire once the debounce window closes (M7).
	next, cmd = m.Update(changed)
	m = next.(AppModel)
	m = w1Pump(m, cmd, 500*time.Millisecond)
	time.Sleep(900 * time.Millisecond)
	if got := atomic.LoadInt32(&lists); got <= afterFirst {
		t.Errorf("W1-AC4: no trailing re-list after event gap (M7); lists=%d at gap close, afterFirst=%d", got, afterFirst)
	}

	found := false
	for _, it := range m.sessionsModel.list.Items() {
		if si, ok := it.(sessionItem); ok && si.key == "sess-new" {
			found = true
		}
	}
	if !found {
		t.Errorf("W1-AC4: refreshed sidebar does not show the new session sess-new")
	}
}

// W1-AC5 / M6: every Supervise connected-transition re-issues
// SessionsSubscribe before the sidebar renders again. At base nothing calls
// SessionsSubscribe — the count stays 0 and the pin is red.
func TestW1AC5_ResubscribeOnConnectedTransition(t *testing.T) {
	fb := &w1ProbeBackend{}
	m := w1CompositeApp(t, fb)
	m = w1Deliver(m, tea.WindowSizeMsg{Width: 140, Height: 40})

	next, _ := m.Update(ConnStateMsg{Status: client.StatusConnected})
	m = next.(AppModel)
	if got := atomic.LoadInt32(&fb.subscribeCalls); got < 1 {
		t.Fatalf("W1-AC5: SessionsSubscribe not issued on first connected transition; calls=%d", got)
	}

	next, _ = m.Update(ConnStateMsg{Status: client.StatusDisconnected})
	m = next.(AppModel)
	next, _ = m.Update(ConnStateMsg{Status: client.StatusConnected})
	m = next.(AppModel)
	_ = m.View() // the sidebar renders again after re-subscription
	if got := atomic.LoadInt32(&fb.subscribeCalls); got < 2 {
		t.Errorf("W1-AC5: subscribe count = %d after reconnect, want >=2 (subscription died with the connection and was never re-issued, M6)", got)
	}
}

// W1-AC6 / M19: below 100 cols the UI behaves exactly as today — the
// full-screen modal path is the narrow fallback. Green at base (pins
// retained behaviour); deleting the branch (M19) turns it red post-W1.
func TestW1AC6_NarrowWidthKeepsFullScreenModalPath(t *testing.T) {
	fb := &w1ProbeBackend{}
	m := w1CompositeApp(t, fb)
	m = w1Deliver(m, tea.WindowSizeMsg{Width: 99, Height: 40})

	view := m.View().Content
	if m.state == viewChat && strings.Contains(view, w1SidebarTitle) {
		t.Errorf("W1-AC6: sidebar rendered at 99 cols (narrow fallback branch deleted, M19)")
	}
	if view != m.chatModel.View() {
		t.Errorf("W1-AC6: narrow-width render diverges from the pre-W1 chat-only rendering")
	}

	// The existing /sessions full-screen swap must keep working as the
	// narrow path.
	_, cmd := m.chatModel.handleSlashCommand("/sessions")
	m = w1Pump(m, cmd, time.Second)
	if m.state != viewSessions {
		t.Fatalf("W1-AC6: /sessions did not open the sessions view at 99 cols; state=%v", m.state)
	}
	if !strings.Contains(m.View().Content, w1SidebarTitle) {
		t.Errorf("W1-AC6: sessions modal at 99 cols does not render the session list")
	}
}
