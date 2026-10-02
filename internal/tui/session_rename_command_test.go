package tui

// /rename <title> gives the open session a name of the operator's choosing,
// and the sidebar shows it in place of the title derived from the first
// message.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/backend"
)

type renameCall struct{ key, title string }

// renameApp is the wide composite parked on sess-2 with a backend that
// records renames and returns renameErr.
func renameApp(t *testing.T, renameErr error) (AppModel, *[]renameCall) {
	t.Helper()
	calls := &[]renameCall{}
	fb := &w1ProbeBackend{}
	fb.sessionRenameHook = func(_ context.Context, key, title string) error {
		*calls = append(*calls, renameCall{key, title})
		return renameErr
	}
	m := w1CompositeApp(t, fb)
	m = w1Deliver(m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = w1Deliver(m, sessionSelectedMsg{sessionKey: "sess-2", agentName: "Scout", modelID: "model-1"})
	m.chatModel.historyLoading = false
	return m, calls
}

// renameRun types the command and runs everything it sets in motion.
func renameRun(t *testing.T, m AppModel, text string) AppModel {
	t.Helper()
	handled, cmd := m.chatModel.handleSlashCommand(text)
	if !handled {
		t.Fatalf("%q is not a recognised command", text)
	}
	return w1Pump(m, cmd, time.Second)
}

func TestSessionRename_SetsTheTitleOfTheOpenSession(t *testing.T) {
	m, calls := renameApp(t, nil)
	m = renameRun(t, m, "/rename   Quarterly Plan: Draft 2  ")

	if len(*calls) != 1 {
		t.Fatalf("rename called %d times, want 1", len(*calls))
	}
	got := (*calls)[0]
	if got.key != "sess-2" {
		t.Errorf("renamed session %q, want the open session sess-2", got.key)
	}
	if got.title != "Quarterly Plan: Draft 2" {
		t.Errorf("title sent as %q, want it trimmed with its case and inner spacing kept", got.title)
	}
	if text := removeTranscript(m); !strings.Contains(text, "Quarterly Plan: Draft 2") {
		t.Errorf("the chat does not confirm the new name: %q", text)
	}
}

func TestSessionRename_BareCommandExplainsItselfAndRenamesNothing(t *testing.T) {
	for _, text := range []string{"/rename", "/rename    "} {
		m, calls := renameApp(t, nil)
		m = renameRun(t, m, text)

		if len(*calls) != 0 {
			t.Errorf("%q renamed the session to %q", text, (*calls)[0].title)
		}
		if got := removeTranscript(m); !strings.Contains(got, "/rename <title>") {
			t.Errorf("%q did not show its usage: %q", text, got)
		}
	}
}

func TestSessionRename_FailureSaysWhy(t *testing.T) {
	m, _ := renameApp(t, errors.New("label already in use: Quarterly Plan"))
	m = renameRun(t, m, "/rename Quarterly Plan")

	got := removeTranscript(m)
	if !strings.Contains(got, "label already in use: Quarterly Plan") {
		t.Errorf("the failure is not shown in the chat: %q", got)
	}
	if strings.Contains(got, "renamed to") {
		t.Errorf("a failed rename was reported as done: %q", got)
	}
}

// A new name must show up without waiting for a gateway event.
func TestSessionRename_ReloadsTheSidebar(t *testing.T) {
	m, _ := renameApp(t, nil)
	listed := 0
	m.backend.(*w1ProbeBackend).sessionsListHook = func(context.Context, string) (json.RawMessage, error) {
		listed++
		return json.RawMessage(`{"sessions":[{"key":"sess-2","label":"Quarterly Plan","derivedTitle":"can you check"}]}`), nil
	}
	m = renameRun(t, m, "/rename Quarterly Plan")

	if listed == 0 {
		t.Fatal("/rename did not reload the session list")
	}
	if row := ansi.Strip(stopMarkerRow(t, m.sessionsModel, "Quarterly Plan")); strings.Contains(row, "can you check") {
		t.Errorf("the sidebar row still shows the derived title: %q", row)
	}
}

// The outcome belongs to the session it was asked for: if the operator has
// moved on, the confirmation must not be written into the chat they are on.
func TestSessionRename_OutcomeForALeftSessionStaysOutOfTheOpenChat(t *testing.T) {
	m, calls := renameApp(t, nil)
	handled, cmd := m.chatModel.handleSlashCommand("/rename Quarterly Plan")
	if !handled || cmd == nil {
		t.Fatal("/rename returned no command")
	}
	m = w1Deliver(m, sessionSelectedMsg{sessionKey: "sess-1", agentName: "Scout", modelID: "model-1"})
	m.chatModel.historyLoading = false
	m = w1Pump(m, cmd, time.Second)

	if len(*calls) != 1 || (*calls)[0].key != "sess-2" {
		t.Fatalf("renamed %+v, want exactly sess-2", *calls)
	}
	if got := removeTranscript(m); strings.Contains(got, "Quarterly Plan") {
		t.Errorf("sess-1's chat was told about sess-2's rename: %q", got)
	}
}

// A long title is cut by display width, never mid-character: the old cap
// sliced bytes, which split a CJK or emoji title into invalid UTF-8.
func TestSidebarTitle_LongTitleStaysValidUTF8(t *testing.T) {
	long := strings.Repeat("修复侧边栏", 40)
	for _, field := range []string{"label", "derivedTitle"} {
		items, err := parseSessionsPayload([]byte(`{"sessions":[{"key":"sess-9","` + field + `":"` + long + `"}]}`))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got := items[0].title; !utf8.ValidString(got) {
			t.Errorf("%s: capped title is not valid UTF-8: %q", field, got)
		}
		if got := ansi.StringWidth(items[0].title); got > sessionTitleMaxCells {
			t.Errorf("%s: capped title is %d cells, over the %d-cell cap", field, got, sessionTitleMaxCells)
		}
	}
}

// backendWithoutRename exposes only the core Backend methods.
type backendWithoutRename struct{ backend.Backend }

func TestSessionRename_UnavailableOnABackendWithoutIt(t *testing.T) {
	m, calls := renameApp(t, nil)
	m.chatModel.backend = backendWithoutRename{m.chatModel.backend}
	m = renameRun(t, m, "/rename Quarterly Plan")

	if len(*calls) != 0 {
		t.Errorf("renamed %+v on a backend that cannot rename", *calls)
	}
	if got := removeTranscript(m); !strings.Contains(got, "/rename is not available") {
		t.Errorf("the chat does not say /rename is unavailable: %q", got)
	}
}

func TestSessionRename_IsListedAndCompletes(t *testing.T) {
	if !strings.Contains(helpBody, "/rename <title> — ") {
		t.Error("/rename is missing from /help")
	}
	found := false
	for _, c := range slashCommands {
		if c == "/rename" {
			found = true
		}
	}
	if !found {
		t.Error("/rename is missing from tab completion")
	}
}

// The sidebar title is the operator's label when there is one, else the
// title derived from the conversation, else the session key.
func TestSidebarTitle_PrefersTheLabel(t *testing.T) {
	cases := []struct{ name, fields, want, notWant string }{
		{"label and derived title", `"label":"Quarterly Plan","derivedTitle":"can you check the build"`, "Quarterly Plan", "can you check"},
		{"label only", `"label":"Quarterly Plan"`, "Quarterly Plan", "sess-9"},
		{"derived title only", `"derivedTitle":"can you check the build"`, "can you check the build", "sess-9"},
		{"blank label falls back", `"label":"   ","derivedTitle":"can you check the build"`, "can you check the build", "sess-9"},
		{"neither", `"hasActiveRun":false`, "sess-9", "Quarterly"},
		// Six spaces separate the glyph from a title (one, plus the empty
		// five-cell state column); a seventh means the label kept its own.
		{"label is trimmed", `"label":"  Quarterly Plan  ","derivedTitle":"x"`, "Quarterly Plan", "       Quarterly Plan"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, err := parseSessionsPayload([]byte(`{"sessions":[{"key":"sess-9",` + tc.fields + `}]}`))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			m := newTestSessionsModel()
			m.setSize(60, 40)
			m, _ = m.Update(sessionsLoadedMsg{sessions: items})
			row := ansi.Strip(stopMarkerRow(t, m, tc.want))
			if strings.Contains(row, tc.notWant) {
				t.Errorf("row shows %q where it should not: %q", tc.notWant, row)
			}
		})
	}
}
