package tui

// Rendered rows are re-wrapped off the UI goroutine. A resize, a theme
// change or a seed from the cache leaves rendered rows as they are and
// stale; AppModel.Update issues one command that renders them again with a
// renderer of its own, and the result is taken in when it lands.
//
// Markdown rendering costs milliseconds per thousand characters, so doing
// it inside setSize or a switch froze the UI for as long as the transcript
// was large. And a glamour renderer shared between two commands crashes.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// rerenderMarkdown is Markdown long enough to wrap differently at every
// width these tests use.
var rerenderMarkdown = "**Summary** " + strings.Repeat("the quick brown fox jumps over the lazy dog ", 12)

// rerenderApp is the wide composite on sess-2 with one long Markdown reply
// loaded and rendered at width 140.
func rerenderApp(t *testing.T) (AppModel, map[string]json.RawMessage) {
	t.Helper()
	m, replies := tcacheApp(t)
	replies["sess-2"] = tcacheHistoryJSON(rerenderMarkdown)
	return tcacheLoad(tcacheSelect(m, "sess-2")), replies
}

type rerenderRow struct {
	content string
	stamp   renderStamp
}

func rerenderSnapshot(c chatModel) []rerenderRow {
	var rows []rerenderRow
	for _, msg := range c.messages {
		if msg.rendered {
			rows = append(rows, rerenderRow{msg.content, msg.stamp})
		}
	}
	return rows
}

func rerenderSame(a, b []rerenderRow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// rerenderStep delivers msg and returns the app with the command it issued.
func rerenderStep(m AppModel, msg tea.Msg) (AppModel, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(AppModel), cmd
}

// rerenderResults runs a command tree and returns the re-render results in
// it, ignoring everything else the tree produces.
func rerenderResults(cmd tea.Cmd) []transcriptRerenderedMsg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case transcriptRerenderedMsg:
		return []transcriptRerenderedMsg{msg}
	case tea.BatchMsg:
		var out []transcriptRerenderedMsg
		for _, c := range msg {
			out = append(out, rerenderResults(c)...)
		}
		return out
	}
	return nil
}

// rerenderOne is the single re-render result a command tree must hold.
func rerenderOne(t *testing.T, cmd tea.Cmd) transcriptRerenderedMsg {
	t.Helper()
	results := rerenderResults(cmd)
	if len(results) != 1 {
		t.Fatalf("the update issued %d re-render commands, want exactly 1", len(results))
	}
	return results[0]
}

func rerenderInFlight(m AppModel) bool {
	return m.chatModel.rerenderFor != renderStamp{}
}

// A resize must not touch rendered rows on the UI goroutine: they stay
// byte-identical until the command's result is delivered. This is the test
// that goes red if the re-render loop is put back into setSize.
func TestRerender_ResizeLeavesRowsUntilTheCommandLands(t *testing.T) {
	m, _ := rerenderApp(t)
	before := rerenderSnapshot(m.chatModel)
	if len(before) != 1 || before[0].stamp != m.chatModel.stamp() {
		t.Fatalf("setup: want one rendered row at the pane's stamp, got %+v", before)
	}

	m, cmd := rerenderStep(m, tea.WindowSizeMsg{Width: 100, Height: 40})

	if after := rerenderSnapshot(m.chatModel); !rerenderSame(before, after) {
		t.Fatal("a resize re-rendered rows before any command ran: Markdown was rendered on the UI goroutine")
	}
	result := rerenderOne(t, cmd)
	if result.stamp != m.chatModel.stamp() {
		t.Errorf("the re-render was issued for %+v, want the pane's stamp %+v", result.stamp, m.chatModel.stamp())
	}

	m, cmd = rerenderStep(m, result)

	after := rerenderSnapshot(m.chatModel)
	if after[0].content == before[0].content {
		t.Error("the delivered re-render did not re-wrap the row")
	}
	if after[0].stamp != m.chatModel.stamp() {
		t.Errorf("the re-rendered row carries stamp %+v, want %+v", after[0].stamp, m.chatModel.stamp())
	}
	if rerenderInFlight(m) || len(rerenderResults(cmd)) != 0 {
		t.Error("a re-render was issued again after every row was brought up to date")
	}
}

// A session painted from the cache at another width is painted as it was
// remembered, and re-wrapped by the command.
func TestRerender_SeedAtAnotherWidthIsPaintedThenRewrapped(t *testing.T) {
	m, _ := rerenderApp(t)
	remembered := rerenderSnapshot(m.chatModel)
	m = tcacheLoad(tcacheSelect(m, "sess-3")) // leave sess-2: it is remembered at 140
	m = w1Deliver(m, tea.WindowSizeMsg{Width: 100, Height: 40})

	m, cmd := rerenderStep(m, sessionSelectedMsg{sessionKey: "sess-2", agentName: "Scout", modelID: "model-1"})

	if seeded := rerenderSnapshot(m.chatModel); !rerenderSame(remembered, seeded) {
		t.Fatal("a seeded switch re-rendered the remembered rows before any command ran")
	}
	if !rerenderInFlight(m) {
		t.Fatal("a seed at another width issued no re-render")
	}
	results := rerenderResults(cmd)
	if len(results) != 1 {
		t.Fatalf("the switch issued %d re-render commands, want 1", len(results))
	}
	m = w1Deliver(m, results[0])
	if got := rerenderSnapshot(m.chatModel); got[0].stamp != m.chatModel.stamp() || got[0].content == remembered[0].content {
		t.Error("the seeded row was not re-wrapped to the pane")
	}
}

func TestRerender_SeedAtTheSameStampIssuesNothing(t *testing.T) {
	m, _ := rerenderApp(t)
	m = tcacheLoad(tcacheSelect(m, "sess-3"))

	m, _ = rerenderStep(m, sessionSelectedMsg{sessionKey: "sess-2", agentName: "Scout", modelID: "model-1"})

	if !m.chatModel.seeded {
		t.Fatal("setup: sess-2 was not painted from the cache")
	}
	if rerenderInFlight(m) {
		t.Error("a seed rendered at the pane's own stamp was re-rendered")
	}
}

// The refresh after a turn is rendered at the pane's stamp and says so:
// its rows are not rendered a second time.
func TestRerender_RefreshAtTheSameStampIssuesNothing(t *testing.T) {
	m, replies := rerenderApp(t)
	replies["sess-2"] = tcacheHistoryJSON(rerenderMarkdown + " after the turn")

	m, cmd := rerenderStep(m, m.chatModel.refreshHistoryAt(0)())

	rows := rerenderSnapshot(m.chatModel)
	if len(rows) != 1 || rows[0].stamp != m.chatModel.stamp() {
		t.Fatalf("the refreshed row is stamped %+v, want the pane's %+v", rows, m.chatModel.stamp())
	}
	if rerenderInFlight(m) || len(rerenderResults(cmd)) != 0 {
		t.Error("rows a refresh had just rendered at the pane's stamp were rendered again")
	}
}

// A result for a width the pane has left changes nothing, and the rows are
// re-rendered for the width it has now.
func TestRerender_SupersededResultIsDiscarded(t *testing.T) {
	m, _ := rerenderApp(t)
	before := rerenderSnapshot(m.chatModel)

	m, first := rerenderStep(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	stale := rerenderOne(t, first)

	// A drag resize is a stream of sizes. While one re-render is in
	// flight none of them starts another: each would render the whole
	// transcript, all at once.
	for _, width := range []int{104, 110, 116, 120} {
		var cmd tea.Cmd
		m, cmd = rerenderStep(m, tea.WindowSizeMsg{Width: width, Height: 40})
		if n := len(rerenderResults(cmd)); n != 0 {
			t.Fatalf("a resize to %d issued %d re-renders while one was in flight", width, n)
		}
	}

	m, followUp := rerenderStep(m, stale)
	if after := rerenderSnapshot(m.chatModel); !rerenderSame(before, after) {
		t.Error("a re-render for a width the pane has left was applied")
	}
	current := rerenderOne(t, followUp)
	if current.stamp != m.chatModel.stamp() {
		t.Fatalf("the follow-up re-render is for %+v, want the pane's width now %+v", current.stamp, m.chatModel.stamp())
	}

	m, last := rerenderStep(m, current)
	if got := rerenderSnapshot(m.chatModel); got[0].stamp != m.chatModel.stamp() {
		t.Errorf("the row carries %+v after the current result, want %+v", got[0].stamp, m.chatModel.stamp())
	}
	if rerenderInFlight(m) || len(rerenderResults(last)) != 0 {
		t.Error("a re-render was issued after the rows converged")
	}
}

// A theme change at the same width makes rendered rows stale too.
func TestRerender_ThemeChangeAtTheSameWidthRerenders(t *testing.T) {
	m, _ := rerenderApp(t)
	width := m.chatModel.stamp().width
	prefs := m.prefs
	prefs.Theme = &config.ThemePreferences{Mode: config.ThemeModeLight}

	m, cmd := rerenderStep(m, prefsUpdatedMsg{prefs: prefs})

	result := rerenderOne(t, cmd)
	if result.stamp.theme.Mode != config.ThemeModeLight || result.stamp.width != width {
		t.Fatalf("the re-render was issued for %+v, want the light theme at width %d", result.stamp, width)
	}
	m = w1Deliver(m, result)
	if got := rerenderSnapshot(m.chatModel); got[0].stamp != m.chatModel.stamp() {
		t.Error("the row was not re-rendered in the new theme")
	}
}

// History fetched before a resize lands after it, rendered at the old
// width, while a re-render of the rows it replaces is in flight. The new
// rows must not be left at the old width.
func TestRerender_HistoryRenderedBeforeAResizeStillConverges(t *testing.T) {
	m, replies := rerenderApp(t)
	fetch := m.chatModel.refreshHistoryAt(0) // captures width 140
	replies["sess-2"] = tcacheHistoryJSON("**Fresh** "+strings.Repeat("alpha beta gamma delta ", 20), rerenderMarkdown+" and more")

	m, resize := rerenderStep(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	overOldRows := rerenderOne(t, resize)

	m = w1Deliver(m, fetch())
	for _, row := range rerenderSnapshot(m.chatModel) {
		if row.stamp == m.chatModel.stamp() {
			t.Fatal("setup: the late history reply was not rendered at the old width")
		}
	}

	m, again := rerenderStep(m, overOldRows)
	if !rerenderInFlight(m) {
		t.Fatal("rows rendered at the old width were left there: no second re-render was issued")
	}
	m, last := rerenderStep(m, rerenderOne(t, again))

	rows := rerenderSnapshot(m.chatModel)
	if len(rows) != 2 {
		t.Fatalf("want the two fresh rows, got %d", len(rows))
	}
	for i, row := range rows {
		if row.stamp != m.chatModel.stamp() {
			t.Errorf("row %d is still at %+v, want %+v", i, row.stamp, m.chatModel.stamp())
		}
	}
	if rerenderInFlight(m) || len(rerenderResults(last)) != 0 {
		t.Error("a re-render was issued after every row converged")
	}
}

// rerenderFactory builds counting renderers and records each one it built.
type rerenderFactory struct {
	mu    sync.Mutex
	built []*rerenderCounting
	err   error
}

type rerenderCounting struct {
	calls int
	err   error
}

func (r *rerenderCounting) Render(in string) (string, error) {
	r.calls++
	if r.err != nil {
		return "", r.err
	}
	return "rendered:" + in, nil
}

func (f *rerenderFactory) build(renderStamp) markdownRenderer {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := &rerenderCounting{err: f.err}
	f.built = append(f.built, r)
	return r
}

func (f *rerenderFactory) renders() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, r := range f.built {
		total += r.calls
	}
	return total
}

// rerenderChat is a sized chat on sess-1 with one rendered row that is
// stale, a counting factory, and a backend that answers history.
func rerenderChat(t *testing.T, factory *rerenderFactory) chatModel {
	t.Helper()
	fb := newFakeBackend()
	fb.chatHistoryHook = func(context.Context, string, int) (json.RawMessage, error) {
		return tcacheHistoryJSON("**from the gateway**"), nil
	}
	m := newChatModel(fb, "sess-1", "agent-1", "Scout", "model-1", config.DefaultPreferences(), false, "", "", false)
	m.setSize(100, 40)
	m.historyLoading = false
	m.newRenderer = factory.build
	m.messages = []chatMessage{{role: "assistant", content: "old", raw: "**old**", rendered: true, stamp: renderStamp{width: 37}}}
	return m
}

// Nothing is rendered when the command is asked for, only when it runs, and
// the history fetch and the re-render each build a renderer of their own.
func TestRerender_EveryCommandBuildsItsOwnRenderer(t *testing.T) {
	factory := &rerenderFactory{}
	m := rerenderChat(t, factory)

	fetch := m.loadHistory()
	rerender := m.rerenderCmd()
	m.setSize(90, 40)
	if rerender == nil {
		t.Fatal("no re-render was issued for a stale row")
	}
	if len(factory.built) != 0 || factory.renders() != 0 {
		t.Fatalf("asking for the commands built %d renderers and rendered %d times on the calling goroutine", len(factory.built), factory.renders())
	}

	fetch()
	rerender()

	if len(factory.built) != 2 {
		t.Fatalf("two commands built %d renderers, want one each", len(factory.built))
	}
	if factory.built[0] == factory.built[1] {
		t.Error("the history fetch and the re-render shared one renderer")
	}
	for i, r := range factory.built {
		if r.calls != 1 {
			t.Errorf("renderer %d rendered %d times, want 1", i, r.calls)
		}
	}
}

// With the real renderer, the fetch and the re-render run at once. Under
// -race this fails if they share a renderer; without it, sharing crashes.
func TestRerender_FetchAndRerenderRunConcurrently(t *testing.T) {
	m := rerenderChat(t, &rerenderFactory{})
	m.newRenderer = themedMarkdownRenderer
	m.messages[0].raw = rerenderMarkdown

	for i := 0; i < 25; i++ {
		m.rerenderFor = renderStamp{}
		fetch, rerender := m.loadHistory(), m.rerenderCmd()
		var wg sync.WaitGroup
		for _, cmd := range []tea.Cmd{fetch, rerender, m.refreshHistoryAt(0)} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cmd()
			}()
		}
		wg.Wait()
	}
}

// A source the renderer rejects is stamped and left as it is: asking again
// would loop.
func TestRerender_FailedRenderIsNotRetried(t *testing.T) {
	factory := &rerenderFactory{err: errors.New("cannot render")}
	m := rerenderChat(t, factory)

	result := m.rerenderCmd()().(transcriptRerenderedMsg)
	m, _ = m.Update(result)

	if m.messages[0].content != "old" {
		t.Errorf("a failed render replaced the row's content with %q", m.messages[0].content)
	}
	if m.messages[0].stamp != m.stamp() {
		t.Error("a row whose render failed was left stale")
	}
	if m.rerenderCmd() != nil {
		t.Error("a re-render was issued again for a row that cannot be rendered")
	}
}

// One re-render at a time per chat: asking again issues nothing while one
// is in flight, for the same stamp or for another.
func TestRerender_OneAtATime(t *testing.T) {
	m := rerenderChat(t, &rerenderFactory{})
	if m.rerenderCmd() == nil {
		t.Fatal("no re-render was issued for a stale row")
	}
	if m.rerenderCmd() != nil {
		t.Error("a second re-render was issued while one is in flight for the same stamp")
	}
	m.setSize(80, 40)
	if m.rerenderCmd() != nil {
		t.Error("a second re-render was issued while one is in flight for another stamp")
	}
}

// A successful /reset changes the chat's key, so the result in flight will
// be dropped by routing; it must not block the next re-render.
func TestRerender_ResetForgetsTheOneInFlight(t *testing.T) {
	m := rerenderChat(t, &rerenderFactory{})
	m.rerenderCmd()

	m, _ = m.Update(sessionClearedMsg{sessionKey: "sess-1", newSessionKey: "sess-1-fresh", deleted: true})

	if m.rerenderFor != (renderStamp{}) {
		t.Error("a reset left a re-render marked in flight for a key it will never reach")
	}
}

// A re-render result is for one session's chat.
func TestRerender_ResultForAnotherSessionIsIgnored(t *testing.T) {
	m := rerenderChat(t, &rerenderFactory{})
	result := m.rerenderCmd()().(transcriptRerenderedMsg)
	result.sessionKey = "sess-9"

	m, _ = m.Update(result)

	if m.messages[0].content != "old" {
		t.Error("a re-render for another session was applied")
	}
}

// A chat that was never sized still has a stamp: the one setSize would give
// its renderer at that width.
func TestRerender_UnsizedChatWrapsAtTheMinimum(t *testing.T) {
	m := newChatModel(newFakeBackend(), "sess-1", "agent-1", "Scout", "model-1", config.DefaultPreferences(), false, "", "", false)
	if got := m.stamp().width; got != 20 || got != m.wrapWidth() {
		t.Errorf("an unsized chat's stamp width is %d (wrapWidth %d), want 20", got, m.wrapWidth())
	}
	m.setSize(100, 40)
	if got := m.stamp().width; got != m.wrapWidth() || got <= 20 {
		t.Errorf("a sized chat's stamp width is %d (wrapWidth %d)", got, m.wrapWidth())
	}
}

// Both messages that change preferences reach the chat parked behind a cron
// transcript, so it is re-rendered in the new theme when it comes back.
func TestRerender_ThemeChangeReachesTheParkedChat(t *testing.T) {
	light := config.DefaultPreferences()
	light.Theme = &config.ThemePreferences{Mode: config.ThemeModeLight}
	for name, msg := range map[string]tea.Msg{
		"prefsUpdatedMsg":    prefsUpdatedMsg{prefs: light},
		"askConfigClosedMsg": askConfigClosedMsg{prefs: light},
	} {
		m, _ := rerenderApp(t)
		m = sessionParkBehindTranscript(m)

		m = w1Deliver(m, msg)
		if got := m.cronsReturnChat.prefs.ThemeSettings().Mode; got != config.ThemeModeLight {
			t.Fatalf("%s: the parked chat still has theme mode %q", name, got)
		}

		m.cronsReturn = viewChat
		m, cmd := rerenderStep(m, goBackFromCronsMsg{})
		result := rerenderOne(t, cmd)
		if m.chatModel.sessionKey != "sess-2" || result.stamp.theme.Mode != config.ThemeModeLight {
			t.Errorf("%s: the restored chat was not re-rendered in the new theme (%+v)", name, result.stamp)
		}
	}
}

// A cron transcript is rendered at the pane's width when it opens, so
// nothing in it is stale.
func TestRerender_CronTranscriptOpensWithNothingStale(t *testing.T) {
	m, _ := rerenderApp(t)

	m, _ = rerenderStep(m, cronTranscriptMsg{
		job:       sampleJobs()[0],
		agentName: "Scout",
		runs:      []protocol.CronRunLogEntry{{Summary: rerenderMarkdown}},
	})

	rows := rerenderSnapshot(m.chatModel)
	if len(rows) != 1 {
		t.Fatalf("want one rendered summary row, got %d", len(rows))
	}
	if rows[0].stamp != m.chatModel.stamp() {
		t.Errorf("the summary is stamped %+v, want the pane's %+v", rows[0].stamp, m.chatModel.stamp())
	}
	if rerenderInFlight(m) {
		t.Error("opening a cron transcript issued a re-render")
	}
}
