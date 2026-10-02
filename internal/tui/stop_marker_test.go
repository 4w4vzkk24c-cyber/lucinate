package tui

// Transient end-of-run marker for the sessions sidebar: a session whose run
// just ended cleanly shows DONE for stopMarkerTTL, then settles to idle.
// Derived from the hasActiveRun transition between two successive
// sessions.list payloads. How a run ended (FAIL, STOP) comes from the
// payload's status fields and is covered in sidebar_state_test.go.
//
// Every assertion reads the rendered list, never the model's fields: the
// marker is a display promise, and a stamp nobody draws is not a marker.

import (
	"strings"
	"testing"
	"time"
)

const stopMarkerGlyph = "DONE"

var stopMarkerEpoch = time.Unix(1_000, 0)

// stopMarkerClock is a hand-advanced clock for the sessions model.
type stopMarkerClock struct{ t time.Time }

func (c *stopMarkerClock) now() time.Time { return c.t }

func newStopMarkerModel(clock *stopMarkerClock) sessionsModel {
	m := newTestSessionsModel()
	m.now = clock.now
	m.setSize(40, 40)
	return m
}

func stopMarkerSessions(active map[string]*bool) []sessionItem {
	return []sessionItem{
		{key: "sess-a", title: "Alpha", group: "Conversations", hasActiveRun: active["sess-a"]},
		{key: "sess-b", title: "Bravo", group: "Conversations", hasActiveRun: active["sess-b"]},
	}
}

// stopMarkerRow returns the rendered sidebar line carrying title.
func stopMarkerRow(t *testing.T, m sessionsModel, title string) string {
	t.Helper()
	for _, line := range strings.Split(m.list.View(), "\n") {
		if strings.Contains(line, title) {
			return line
		}
	}
	t.Fatalf("no rendered row for %q in:\n%s", title, m.list.View())
	return ""
}

func stopMarkerBool(v bool) *bool { return &v }

func TestStopMarker_ShownWhenRunEnds(t *testing.T) {
	cases := []struct {
		name  string
		after *bool
	}{
		{"gateway reports idle", stopMarkerBool(false)},
		{"gateway omits the field", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := &stopMarkerClock{t: time.Unix(1_000, 0)}
			m := newStopMarkerModel(clock)
			m, _ = m.Update(sessionsLoadedMsg{sessions: stopMarkerSessions(map[string]*bool{"sess-a": stopMarkerBool(true)})})
			m, cmd := m.Update(sessionsLoadedMsg{sessions: stopMarkerSessions(map[string]*bool{"sess-a": tc.after})})

			if row := stopMarkerRow(t, m, "Alpha"); !strings.Contains(row, stopMarkerGlyph) {
				t.Errorf("stopped session row lacks %q: %q", stopMarkerGlyph, row)
			}
			if row := stopMarkerRow(t, m, "Bravo"); strings.Contains(row, stopMarkerGlyph) {
				t.Errorf("session that never ran carries a stop marker: %q", row)
			}
			if cmd == nil {
				t.Error("a new stop must schedule the expiry redraw; got no command")
			}
		})
	}
}

func TestStopMarker_SettlesToIdleAfterTTL(t *testing.T) {
	clock := &stopMarkerClock{t: time.Unix(1_000, 0)}
	m := newStopMarkerModel(clock)
	m, _ = m.Update(sessionsLoadedMsg{sessions: stopMarkerSessions(map[string]*bool{"sess-a": stopMarkerBool(true)})})
	m, _ = m.Update(sessionsLoadedMsg{sessions: stopMarkerSessions(map[string]*bool{"sess-a": stopMarkerBool(false)})})

	clock.t = clock.t.Add(stopMarkerTTL - time.Second)
	m, _ = m.Update(stopMarkerExpiredMsg{})
	if row := stopMarkerRow(t, m, "Alpha"); !strings.Contains(row, stopMarkerGlyph) {
		t.Errorf("marker cleared before its TTL elapsed: %q", row)
	}

	clock.t = clock.t.Add(time.Second)
	m, _ = m.Update(stopMarkerExpiredMsg{})
	if row := stopMarkerRow(t, m, "Alpha"); strings.Contains(row, stopMarkerGlyph) {
		t.Errorf("marker still shown after its TTL: %q", row)
	}
}

func TestStopMarker_ExpiryKeepsCursor(t *testing.T) {
	clock := &stopMarkerClock{t: time.Unix(1_000, 0)}
	m := newStopMarkerModel(clock)
	m, _ = m.Update(sessionsLoadedMsg{sessions: stopMarkerSessions(map[string]*bool{"sess-a": stopMarkerBool(true)})})
	m, _ = m.Update(sessionsLoadedMsg{sessions: stopMarkerSessions(map[string]*bool{"sess-a": stopMarkerBool(false)})})
	m.list.Select(2) // Bravo, past the group header and Alpha

	clock.t = clock.t.Add(stopMarkerTTL)
	m, _ = m.Update(stopMarkerExpiredMsg{})
	if got := m.list.Index(); got != 2 {
		t.Errorf("expiry moved the cursor to %d, want 2", got)
	}
}

func TestStopMarker_SurvivesUnrelatedRefreshWithinTTL(t *testing.T) {
	clock := &stopMarkerClock{t: time.Unix(1_000, 0)}
	m := newStopMarkerModel(clock)
	m, _ = m.Update(sessionsLoadedMsg{sessions: stopMarkerSessions(map[string]*bool{"sess-a": stopMarkerBool(true)})})
	idle := map[string]*bool{"sess-a": stopMarkerBool(false)}
	m, _ = m.Update(sessionsLoadedMsg{sessions: stopMarkerSessions(idle)})

	clock.t = clock.t.Add(time.Second)
	m, cmd := m.Update(sessionsLoadedMsg{sessions: stopMarkerSessions(idle)})
	if row := stopMarkerRow(t, m, "Alpha"); !strings.Contains(row, stopMarkerGlyph) {
		t.Errorf("a refresh inside the TTL dropped the marker: %q", row)
	}
	if cmd != nil {
		t.Error("a carried-over marker must not schedule a second expiry")
	}

	clock.t = clock.t.Add(stopMarkerTTL)
	m, _ = m.Update(sessionsLoadedMsg{sessions: stopMarkerSessions(idle)})
	if row := stopMarkerRow(t, m, "Alpha"); strings.Contains(row, stopMarkerGlyph) {
		t.Errorf("a refresh after the TTL carried the marker over: %q", row)
	}
}

func TestStopMarker_ClearedWhenSessionRunsAgain(t *testing.T) {
	clock := &stopMarkerClock{t: time.Unix(1_000, 0)}
	m := newStopMarkerModel(clock)
	running := map[string]*bool{"sess-a": stopMarkerBool(true)}
	m, _ = m.Update(sessionsLoadedMsg{sessions: stopMarkerSessions(running)})
	m, _ = m.Update(sessionsLoadedMsg{sessions: stopMarkerSessions(map[string]*bool{"sess-a": stopMarkerBool(false)})})
	m, _ = m.Update(sessionsLoadedMsg{sessions: stopMarkerSessions(running)})

	row := stopMarkerRow(t, m, "Alpha")
	if strings.Contains(row, stopMarkerGlyph) {
		t.Errorf("running session still shows the stop marker: %q", row)
	}
	if !strings.Contains(row, "♛") {
		t.Errorf("running session lost its active glyph: %q", row)
	}
}

// The expiry is delivered to the app, not the sessions model: without the
// app-level route the sidebar would hold its marker until the next refresh.
func TestStopMarker_AppRoutesExpiryToSidebar(t *testing.T) {
	clock := &stopMarkerClock{t: time.Unix(1_000, 0)}
	app := w1CompositeApp(t, &w1ProbeBackend{})
	app.sessionsModel.now = clock.now
	app = w1Deliver(app, sessionsLoadedMsg{sessions: stopMarkerSessions(map[string]*bool{"sess-a": stopMarkerBool(true)})})
	app = w1Deliver(app, sessionsLoadedMsg{sessions: stopMarkerSessions(map[string]*bool{"sess-a": stopMarkerBool(false)})})
	if row := stopMarkerRow(t, app.sessionsModel, "Alpha"); !strings.Contains(row, stopMarkerGlyph) {
		t.Fatalf("stop not marked through the app route: %q", row)
	}

	clock.t = clock.t.Add(stopMarkerTTL)
	app = w1Deliver(app, stopMarkerExpiredMsg{})
	if row := stopMarkerRow(t, app.sessionsModel, "Alpha"); strings.Contains(row, stopMarkerGlyph) {
		t.Errorf("app dropped the expiry; marker still shown: %q", row)
	}
}

func TestStopMarker_NotShownOnFirstLoad(t *testing.T) {
	clock := &stopMarkerClock{t: time.Unix(1_000, 0)}
	m := newStopMarkerModel(clock)
	m, cmd := m.Update(sessionsLoadedMsg{sessions: stopMarkerSessions(map[string]*bool{"sess-a": stopMarkerBool(false)})})

	if view := m.list.View(); strings.Contains(view, stopMarkerGlyph) {
		t.Errorf("first load invented a stop marker:\n%s", view)
	}
	if cmd != nil {
		t.Error("first load scheduled an expiry with nothing to expire")
	}
}
