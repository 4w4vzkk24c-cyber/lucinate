package tui

// Sidebar session state: a glyph for what the session is (agent or
// subagent, filled while a run is in flight) and a word for what happened
// to it (RUN, DONE, FAIL, STOP).
//
// These tests start from the sessions.list JSON, not from a hand-built
// sessionItem. The original indicator was correct for the items its tests
// built and wrong for the gateway: the gateway always sends hasActiveRun as
// true or false, never omits it, so the "unknown" glyph could not appear and
// every idle session rendered as a blank.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

var sidebarStateWords = []string{"RUN", "DONE", "FAIL", "STOP"}

// sidebarStateRow renders the one session described by fields (a JSON
// object body) and returns its plain-text sidebar row.
func sidebarStateRow(t *testing.T, key, fields string) string {
	t.Helper()
	payload := fmt.Sprintf(`{"sessions":[{"key":%q,"derivedTitle":"Quarterly plan",%s}]}`, key, fields)
	items, err := parseSessionsPayload([]byte(payload))
	if err != nil {
		t.Fatalf("parse %s: %v", payload, err)
	}
	m := newTestSessionsModel()
	m.setSize(40, 40)
	m, _ = m.Update(sessionsLoadedMsg{sessions: items})
	return ansi.Strip(stopMarkerRow(t, m, "Quarterly plan"))
}

func sidebarStateWordIn(row string) string {
	for _, w := range sidebarStateWords {
		if strings.Contains(row, w) {
			return w
		}
	}
	return ""
}

func TestSidebarState_FromGatewayPayload(t *testing.T) {
	const agent = "agent:main:dashboard:11111111-2222-3333-4444-555555555555"
	const sub = "agent:main:subagent:11111111-2222-3333-4444-555555555555"
	cases := []struct {
		name      string
		key       string
		fields    string
		wantGlyph string
		wantWord  string
	}{
		{"idle agent", agent, `"hasActiveRun":false,"status":"done"`, "♕", ""},
		{"idle subagent", sub, `"hasActiveRun":false,"status":"done"`, "♙", ""},
		{"idle, no status", agent, `"hasActiveRun":false`, "♕", ""},
		{"flag omitted", agent, `"status":"done"`, "♕", ""},
		{"running agent", agent, `"hasActiveRun":true,"status":"running"`, "♛", "RUN"},
		{"running subagent", sub, `"hasActiveRun":true,"status":"running"`, "♟", "RUN"},
		{"failed", agent, `"hasActiveRun":false,"status":"failed"`, "♕", "FAIL"},
		{"timed out", agent, `"hasActiveRun":false,"status":"timeout"`, "♕", "FAIL"},
		{"killed", agent, `"hasActiveRun":false,"status":"killed"`, "♕", "STOP"},
		{"aborted last run", agent, `"hasActiveRun":false,"status":"done","abortedLastRun":true`, "♕", "STOP"},
		{"running again after a failure", agent, `"hasActiveRun":true,"status":"failed"`, "♛", "RUN"},
		{"running again after an abort", agent, `"hasActiveRun":true,"abortedLastRun":true`, "♛", "RUN"},
		{"status running, no active run", agent, `"hasActiveRun":false,"status":"running"`, "♕", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := sidebarStateRow(t, tc.key, tc.fields)
			if !strings.Contains(row, tc.wantGlyph) {
				t.Errorf("glyph: want %q in %q", tc.wantGlyph, row)
			}
			if got := sidebarStateWordIn(row); got != tc.wantWord {
				t.Errorf("state word: got %q, want %q in %q", got, tc.wantWord, row)
			}
		})
	}
}

// The state word sits in a fixed column, so titles line up whatever the state.
func TestSidebarState_TitlesAlignAcrossStates(t *testing.T) {
	const agent = "agent:main:dashboard:11111111-2222-3333-4444-555555555555"
	want := -1
	for _, fields := range []string{
		`"hasActiveRun":false`,
		`"hasActiveRun":true`,
		`"hasActiveRun":false,"status":"failed"`,
		`"hasActiveRun":false,"abortedLastRun":true`,
	} {
		row := sidebarStateRow(t, agent, fields)
		col := ansi.StringWidth(row[:strings.Index(row, "Quarterly plan")])
		if want == -1 {
			want = col
		}
		if col != want {
			t.Errorf("title starts at cell %d for {%s}, want %d: %q", col, fields, want, row)
		}
	}
}

// A run that ends cleanly shows DONE for the marker TTL; one that ends in a
// failure shows FAIL, and keeps it after the TTL.
func TestSidebarState_EndOfRunWord(t *testing.T) {
	clock := &stopMarkerClock{t: stopMarkerEpoch}
	load := func(m sessionsModel, fields string) sessionsModel {
		items, err := parseSessionsPayload([]byte(`{"sessions":[{"key":"sess-a","derivedTitle":"Alpha",` + fields + `}]}`))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		m, _ = m.Update(sessionsLoadedMsg{sessions: items})
		return m
	}

	clean := load(newStopMarkerModel(clock), `"hasActiveRun":true`)
	clean = load(clean, `"hasActiveRun":false,"status":"done"`)
	if got := sidebarStateWordIn(ansi.Strip(stopMarkerRow(t, clean, "Alpha"))); got != "DONE" {
		t.Errorf("clean end: state word %q, want DONE", got)
	}

	failed := load(newStopMarkerModel(clock), `"hasActiveRun":true`)
	failed = load(failed, `"hasActiveRun":false,"status":"failed"`)
	if got := sidebarStateWordIn(ansi.Strip(stopMarkerRow(t, failed, "Alpha"))); got != "FAIL" {
		t.Errorf("failed end: state word %q, want FAIL", got)
	}
	clock.t = clock.t.Add(stopMarkerTTL)
	failed, _ = failed.Update(stopMarkerExpiredMsg{})
	clean, _ = clean.Update(stopMarkerExpiredMsg{})
	if got := sidebarStateWordIn(ansi.Strip(stopMarkerRow(t, failed, "Alpha"))); got != "FAIL" {
		t.Errorf("after the TTL a failed session shows %q, want FAIL", got)
	}
	if got := sidebarStateWordIn(ansi.Strip(stopMarkerRow(t, clean, "Alpha"))); got != "" {
		t.Errorf("after the TTL a cleanly finished session still shows %q", got)
	}
}
