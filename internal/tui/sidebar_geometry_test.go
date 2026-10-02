package tui

// Geometry of the sessions sidebar beside the chat pane: rows are built to
// display width, and the mouse is hit-tested in the chat pane's own columns.
// Both assert on what the terminal shows — the rendered row, the rendered
// frame — because both bugs were invisible to tests that read model fields.

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/a3tai/openclaw-go/protocol"
	"github.com/charmbracelet/x/ansi"
)

func TestSidebarRow_ExactPaneWidthForEveryTitle(t *testing.T) {
	titles := map[string]string{
		"short ascii":  "Plan",
		"long ascii":   "plain ascii title that is definitely longer than the pane",
		"cjk":          "修复侧边栏的宽度问题并且验证所有的会话标题",
		"emoji":        "🍋🍋🍋🍋🍋🍋🍋🍋🍋🍋🍋🍋🍋🍋🍋🍋🍋🍋🍋🍋",
		"mixed":        "fix 修复 the 🍋 sidebar width for every session title",
		"empty → key":  "",
		"combining é":  "café society meeting notes for the long weekend ahead",
		"exactly fits": "twenty-cell title ok",
	}
	d := sessionDelegate{}
	for name, title := range titles {
		for _, pane := range []int{20, 24, 37, 70} {
			for _, selected := range []bool{false, true} {
				l := list.New(nil, d, pane, 20)
				index := 1
				if selected {
					index = l.Index()
				}
				var buf strings.Builder
				d.Render(&buf, l, index, sessionItem{key: "agent:main:dashboard:fallback-key", title: title})
				row := buf.String()
				if !utf8.ValidString(row) {
					t.Errorf("%s pane=%d selected=%v: row is not valid UTF-8: %q", name, pane, selected, row)
				}
				if got := lipgloss.Width(row); got != pane {
					t.Errorf("%s pane=%d selected=%v: row is %d cells, want %d: %q", name, pane, selected, got, pane, ansi.Strip(row))
				}
			}
		}
	}
}

func TestSidebarRow_TruncatedTitleEndsWithEllipsis(t *testing.T) {
	d := sessionDelegate{}
	l := list.New(nil, d, 24, 20)
	var buf strings.Builder
	d.Render(&buf, l, 1, sessionItem{key: "k", title: "修复侧边栏的宽度问题并且验证所有的会话标题"})
	row := strings.TrimRight(ansi.Strip(buf.String()), " ")
	if !strings.HasSuffix(row, "…") {
		t.Errorf("truncated title does not end with an ellipsis: %q", row)
	}
	if !strings.Contains(row, "修复侧边") {
		t.Errorf("truncated title lost its leading characters: %q", row)
	}
}

// sidebarGeometryApp is the wide composite (sidebar + chat) with one known
// line in the transcript.
func sidebarGeometryApp(t *testing.T, width int) AppModel {
	t.Helper()
	m := w1CompositeApp(t, &w1ProbeBackend{})
	m.chatModel.historyLoading = false
	m.chatModel.appendMessage(chatMessage{role: "system", content: "alpha bravo charlie"})
	return w1Deliver(m, tea.WindowSizeMsg{Width: width, Height: 40})
}

// sidebarGeometryLocate returns the screen cell of needle in the rendered frame.
func sidebarGeometryLocate(t *testing.T, m AppModel, needle string) (x, y int) {
	t.Helper()
	for row, line := range strings.Split(m.View().Content, "\n") {
		plain := ansi.Strip(line)
		if at := strings.Index(plain, needle); at >= 0 {
			return ansi.StringWidth(plain[:at]), row
		}
	}
	t.Fatalf("%q is not on screen:\n%s", needle, ansi.Strip(m.View().Content))
	return 0, 0
}

func TestSidebarSelection_DragSelectsTheTextUnderThePointer(t *testing.T) {
	for _, width := range []int{100, 140, 250} {
		m := sidebarGeometryApp(t, width)
		x, y := sidebarGeometryLocate(t, m, "bravo")

		m = w1Deliver(m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		if !m.chatModel.sel.dragging {
			t.Fatalf("width=%d: a press on transcript text at (%d,%d) started no drag", width, x, y)
		}
		m = w1Deliver(m, tea.MouseMotionMsg{X: x + 4, Y: y, Button: tea.MouseLeft})

		got := extractSelection(m.chatModel.selLines, m.chatModel.sel.anchor, m.chatModel.sel.head)
		if got != "bravo" {
			t.Errorf("width=%d: dragging across \"bravo\" at screen x=%d selected %q", width, x, got)
		}
	}
}

// sidebarGeometryDrag drags across word on screen and returns what the chat
// selected.
func sidebarGeometryDrag(t *testing.T, m AppModel, word string) string {
	t.Helper()
	x, y := sidebarGeometryLocate(t, m, word)
	m = w1Deliver(m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m = w1Deliver(m, tea.MouseMotionMsg{X: x + len(word) - 1, Y: y, Button: tea.MouseLeft})
	return extractSelection(m.chatModel.selLines, m.chatModel.sel.anchor, m.chatModel.sel.head)
}

// A cron transcript sits beside the sidebar like any chat, and so does the
// chat restored when the operator leaves it. Both were sized to the whole
// terminal with no column offset, so a drag selected text one sidebar-width
// to the right of the pointer.
func TestSidebarSelection_CronTranscriptAndItsRestoreUsePaneGeometry(t *testing.T) {
	m := sidebarGeometryApp(t, 140)
	pane := m.chatModel.width

	m = w1Deliver(m, cronTranscriptMsg{
		job:       sampleJobs()[0],
		agentName: "Scout",
		runs:      []protocol.CronRunLogEntry{{Summary: "delta echo foxtrot"}},
	})
	if !m.chatModel.transcript {
		t.Fatal("setup: the cron transcript did not open")
	}
	if m.chatModel.width != pane || m.chatModel.originX != m.sidebarWidth() {
		t.Errorf("the cron transcript is %d wide at column %d, want the chat pane: %d wide at column %d",
			m.chatModel.width, m.chatModel.originX, pane, m.sidebarWidth())
	}
	if got := sidebarGeometryDrag(t, m, "echo"); got != "echo" {
		t.Errorf("dragging across \"echo\" in a cron transcript selected %q", got)
	}

	m.cronsReturn = viewChat // showCronsMsg records this when crons opens from the chat
	m = w1Deliver(m, goBackFromCronsMsg{})
	if m.chatModel.transcript {
		t.Fatal("setup: leaving the crons list did not restore the chat")
	}
	if m.chatModel.width != pane || m.chatModel.originX != m.sidebarWidth() {
		t.Errorf("the restored chat is %d wide at column %d, want %d wide at column %d",
			m.chatModel.width, m.chatModel.originX, pane, m.sidebarWidth())
	}
	if got := sidebarGeometryDrag(t, m, "bravo"); got != "bravo" {
		t.Errorf("dragging across \"bravo\" in the restored chat selected %q", got)
	}
}

// The chat pane's left edge is the sidebar width, whatever the titles are.
// Before fitPane the join padded only to the sidebar's widest line, so the
// chat pane slid left and right as sessions were renamed.
func TestSidebarPane_ChatColumnDoesNotMoveWithTitles(t *testing.T) {
	short := sidebarGeometryApp(t, 140)
	long := sidebarGeometryApp(t, 140)
	long.sessionsModel, _ = long.sessionsModel.Update(sessionsLoadedMsg{sessions: []sessionItem{
		{key: "sess-1", title: strings.Repeat("a very long session title ", 6)},
		{key: "sess-2", title: "修复侧边栏的宽度问题并且验证所有的会话标题修复侧边栏的宽度问题"},
	}})

	want := clampSidebarWidth(140)
	xShort, _ := sidebarGeometryLocate(t, short, "alpha bravo")
	xLong, _ := sidebarGeometryLocate(t, long, "alpha bravo")
	if xShort != xLong {
		t.Errorf("chat text moved with the sidebar titles: x=%d with short titles, x=%d with long", xShort, xLong)
	}
	if xShort < want {
		t.Errorf("chat text at x=%d is inside the %d-cell sidebar pane", xShort, want)
	}
	for row, line := range strings.Split(long.View().Content, "\n") {
		if got := lipgloss.Width(line); got > 140 {
			t.Errorf("frame row %d is %d cells, wider than the 140-cell terminal", row, got)
		}
	}
}

// The sidebar has states with no session rows at all — loading, empty, a
// failed list — whose lines are shorter or longer than the pane. The chat
// pane must start at the same column in every one of them.
func TestSidebarPane_ChatColumnHoldsInEverySidebarState(t *testing.T) {
	baseline := sidebarGeometryApp(t, 140)
	wantX, _ := sidebarGeometryLocate(t, baseline, "alpha bravo")

	states := map[string]func(*sessionsModel){
		"loading": func(s *sessionsModel) { s.loading = true },
		"empty": func(s *sessionsModel) {
			*s, _ = s.Update(sessionsLoadedMsg{})
		},
		"long error": func(s *sessionsModel) {
			*s, _ = s.Update(sessionsLoadedMsg{err: errors.New(strings.Repeat("gateway unreachable ", 12))})
		},
	}
	for name, apply := range states {
		m := sidebarGeometryApp(t, 140)
		apply(&m.sessionsModel)
		gotX, y := sidebarGeometryLocate(t, m, "alpha bravo")
		if gotX != wantX {
			t.Errorf("%s: chat text at x=%d, want x=%d", name, gotX, wantX)
		}
		frameRow := strings.Split(m.View().Content, "\n")[y]
		if got := lipgloss.Width(frameRow); got > 140 {
			t.Errorf("%s: frame row is %d cells, wider than the 140-cell terminal", name, got)
		}
	}
}

func TestSidebarSelection_PressOnTheSidebarStartsNoDrag(t *testing.T) {
	m := sidebarGeometryApp(t, 140)
	_, y := sidebarGeometryLocate(t, m, "bravo")

	m = w1Deliver(m, tea.MouseClickMsg{X: 3, Y: y, Button: tea.MouseLeft})
	if m.chatModel.sel.dragging {
		t.Error("a press inside the sidebar started a transcript selection")
	}
}

func TestSidebarSelection_NarrowTerminalHasNoOffset(t *testing.T) {
	m := sidebarGeometryApp(t, 99)
	x, y := sidebarGeometryLocate(t, m, "bravo")

	m = w1Deliver(m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m = w1Deliver(m, tea.MouseMotionMsg{X: x + 4, Y: y, Button: tea.MouseLeft})
	got := extractSelection(m.chatModel.selLines, m.chatModel.sel.anchor, m.chatModel.sel.head)
	if got != "bravo" {
		t.Errorf("without a sidebar, dragging across \"bravo\" selected %q", got)
	}
}
