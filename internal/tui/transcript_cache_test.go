package tui

// Session switching without the wait: a session's transcript is remembered
// when you leave it and painted at once when you come back, and a first
// visit renders at most historyLimit messages however many the gateway
// sends.
//
// The cache is written in one place only — when the open chat is replaced —
// and holds what that chat was showing. Tests therefore drive the app the
// way the operator does: select, load, leave, return.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// tcacheRenderer counts Markdown renders.
type tcacheRenderer struct{ calls int }

func (r *tcacheRenderer) Render(in string) (string, error) {
	r.calls++
	return in, nil
}

func tcacheHistoryJSON(texts ...string) json.RawMessage {
	entries := make([]string, len(texts))
	for i, text := range texts {
		body, _ := json.Marshal(text)
		entries[i] = fmt.Sprintf(`{"role":"assistant","content":[{"type":"text","text":%s}]}`, body)
	}
	return json.RawMessage(`{"messages":[` + strings.Join(entries, ",") + `]}`)
}

// tcacheApp is the wide composite whose backend answers a history fetch from
// replies[sessionKey], defaulting to one message naming the session.
func tcacheApp(t *testing.T) (AppModel, map[string]json.RawMessage) {
	t.Helper()
	replies := map[string]json.RawMessage{}
	fb := &w1ProbeBackend{}
	fb.chatHistoryHook = func(_ context.Context, sessionKey string, _ int) (json.RawMessage, error) {
		if reply, ok := replies[sessionKey]; ok {
			return reply, nil
		}
		return tcacheHistoryJSON("transcript of " + sessionKey), nil
	}
	m := w1CompositeApp(t, fb)
	return w1Deliver(m, tea.WindowSizeMsg{Width: 140, Height: 40}), replies
}

func tcacheSelect(m AppModel, key string) AppModel {
	return w1Deliver(m, sessionSelectedMsg{sessionKey: key, agentName: "Scout", modelID: "model-1"})
}

// tcacheLoad delivers the open session's history reply.
func tcacheLoad(m AppModel) AppModel {
	return w1Deliver(m, m.chatModel.loadHistory()())
}

func tcacheTranscript(m AppModel) string {
	return ansi.Strip(strings.Join(m.chatModel.selLines, "\n"))
}

// tcacheVisited returns an app that opened sess-2, let it load, moved to
// sess-3 and let that load: sess-2 is now a session the operator has left.
func tcacheVisited(t *testing.T) (AppModel, map[string]json.RawMessage) {
	t.Helper()
	m, replies := tcacheApp(t)
	m = tcacheLoad(tcacheSelect(m, "sess-2"))
	m = tcacheLoad(tcacheSelect(m, "sess-3"))
	return m, replies
}

// (a) The trim happens before rendering, so the cost is bounded by the limit.
func TestFetchHistory_TrimsToTheLimitBeforeRendering(t *testing.T) {
	texts := make([]string, 300)
	for i := range texts {
		texts[i] = fmt.Sprintf("**message %03d**", i)
	}
	fb := newFakeBackend()
	fb.chatHistoryHook = func(context.Context, string, int) (json.RawMessage, error) {
		return tcacheHistoryJSON(texts...), nil
	}

	renderer := &tcacheRenderer{}
	got, err := fetchHistory(fb, "k", renderer, 50)
	if err != nil {
		t.Fatalf("fetchHistory: %v", err)
	}
	if len(got) != 50 {
		t.Fatalf("kept %d messages, want the last 50", len(got))
	}
	if !strings.Contains(got[0].content, "message 250") || !strings.Contains(got[49].content, "message 299") {
		t.Errorf("kept %q .. %q, want message 250 .. message 299", got[0].content, got[49].content)
	}
	if renderer.calls > 50 {
		t.Errorf("rendered %d messages to keep 50; the trim must come before the render", renderer.calls)
	}
	if renderer.calls == 0 {
		t.Error("nothing was rendered; the kept messages are Markdown")
	}

	all, _ := fetchHistory(fb, "k", &tcacheRenderer{}, 0)
	if len(all) != 300 {
		t.Errorf("a limit of 0 kept %d messages, want all 300", len(all))
	}
}

// Entries the transcript never shows must not use up the limit.
func TestFetchHistory_LimitCountsOnlyShownMessages(t *testing.T) {
	var entries []string
	for i := 0; i < 20; i++ {
		entries = append(entries,
			fmt.Sprintf(`{"role":"assistant","content":[{"type":"text","text":"shown %02d"}]}`, i),
			`{"role":"toolResult","content":[{"type":"text","text":"tool output"}]}`,
			`{"role":"assistant","content":[{"type":"toolCall","text":""}]}`,
		)
	}
	fb := newFakeBackend()
	fb.chatHistoryHook = func(context.Context, string, int) (json.RawMessage, error) {
		return json.RawMessage(`{"messages":[` + strings.Join(entries, ",") + `]}`), nil
	}
	got, err := fetchHistory(fb, "k", nil, 10)
	if err != nil {
		t.Fatalf("fetchHistory: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("kept %d shown messages, want 10", len(got))
	}
	if got[0].content != "shown 10" || got[9].content != "shown 19" {
		t.Errorf("kept %q .. %q, want shown 10 .. shown 19", got[0].content, got[9].content)
	}
}

// (b) A revisited session paints before any reply arrives.
func TestTranscriptCache_RevisitPaintsAtOnce(t *testing.T) {
	m, _ := tcacheVisited(t)

	m = tcacheSelect(m, "sess-2")

	if got := tcacheTranscript(m); !strings.Contains(got, "transcript of sess-2") {
		t.Errorf("returning to sess-2 did not paint its transcript before the reply: %q", got)
	}
	if m.chatModel.historyLoading {
		t.Error("a revisited session still shows the loading state")
	}
}

// (c) The reply that follows replaces the remembered rows and keeps live ones.
func TestTranscriptCache_ReplyReplacesTheRememberedRows(t *testing.T) {
	m, replies := tcacheVisited(t)
	m = tcacheSelect(m, "sess-2")
	m.chatModel.appendMessage(chatMessage{role: "system", content: "a live note"})
	replies["sess-2"] = tcacheHistoryJSON("sess-2 as the gateway has it now")

	m = tcacheLoad(m)

	got := tcacheTranscript(m)
	if strings.Contains(got, "transcript of sess-2") {
		t.Errorf("the remembered rows were not replaced: %q", got)
	}
	if strings.Count(got, "sess-2 as the gateway has it now") != 1 {
		t.Errorf("the fresh rows appear %d times, want once: %q", strings.Count(got, "sess-2 as the gateway has it now"), got)
	}
	if !strings.Contains(got, "a live note") {
		t.Errorf("a row added after the revisit was lost: %q", got)
	}
}

// (c) /clear between the revisit and the reply must not crash.
func TestTranscriptCache_ClearBeforeTheReplyDoesNotPanic(t *testing.T) {
	m, _ := tcacheVisited(t)
	m = tcacheSelect(m, "sess-2")
	if handled, _ := m.chatModel.handleSlashCommand("/clear"); !handled {
		t.Fatal("/clear is not a recognised command")
	}

	m = tcacheLoad(m)

	if got := tcacheTranscript(m); !strings.Contains(got, "transcript of sess-2") {
		t.Errorf("after /clear the reply did not restore the history: %q", got)
	}
}

// (d) A load that lands after a newer refresh must not overwrite it, on
// screen or in what is remembered.
func TestTranscriptCache_LateLoadDoesNotOverwriteANewerRefresh(t *testing.T) {
	m, replies := tcacheVisited(t)
	m = tcacheSelect(m, "sess-2")
	staleLoad := m.chatModel.loadHistory()() // answered with the old state
	replies["sess-2"] = tcacheHistoryJSON("sess-2 after the turn")
	m = w1Deliver(m, m.chatModel.refreshHistoryAt(m.chatModel.gen)())

	m = w1Deliver(m, staleLoad)

	if got := tcacheTranscript(m); !strings.Contains(got, "sess-2 after the turn") || strings.Contains(got, "transcript of sess-2") {
		t.Errorf("a late load overwrote the newer refresh: %q", got)
	}
	if m.chatModel.historyLoading {
		t.Error("a discarded load left the loading state on")
	}

	delete(replies, "sess-2")
	m = tcacheSelect(tcacheSelect(m, "sess-3"), "sess-2")
	if got := tcacheTranscript(m); !strings.Contains(got, "sess-2 after the turn") {
		t.Errorf("the session was remembered in its older state: %q", got)
	}
}

// (e) An empty reply clears the remembered rows, now and for the next visit.
func TestTranscriptCache_EmptyReplyClearsTheRememberedRows(t *testing.T) {
	m, replies := tcacheVisited(t)
	m = tcacheSelect(m, "sess-2")
	replies["sess-2"] = json.RawMessage(`{"messages":[]}`)

	m = tcacheLoad(m)

	if got := tcacheTranscript(m); strings.Contains(got, "transcript of sess-2") {
		t.Errorf("an empty reply left the remembered rows on screen: %q", got)
	}
	m = tcacheSelect(tcacheSelect(m, "sess-3"), "sess-2")
	if !m.chatModel.historyLoading {
		t.Error("an emptied session was still painted from memory on the next visit")
	}
}

// (f) The cache holds the ten most recently used sessions and never shares
// its rows with a caller.
func TestTranscriptCache_EvictsLeastRecentlyUsedAndCopies(t *testing.T) {
	c := newTranscriptCache()
	for i := 0; i < transcriptCacheCapacity; i++ {
		c.put(fmt.Sprintf("s%d", i), []chatMessage{{content: fmt.Sprintf("rows of s%d", i)}})
	}
	if _, ok := c.get("s0"); !ok { // s0 is now the most recently used
		t.Fatal("s0 missing before the cache was full")
	}
	c.put("one-more", []chatMessage{{content: "x"}})

	if _, ok := c.get("s1"); ok {
		t.Error("s1 was the least recently used and should have been evicted")
	}
	if _, ok := c.get("s0"); !ok {
		t.Error("s0 was just read and should have survived the eviction")
	}

	rows := []chatMessage{{content: "original"}}
	c.put("copy", rows)
	rows[0].content = "changed after put"
	got, _ := c.get("copy")
	if got[0].content != "original" {
		t.Errorf("the cache shares its rows with the slice given to put: %q", got[0].content)
	}
	got[0].content = "changed after get"
	again, _ := c.get("copy")
	if again[0].content != "original" {
		t.Errorf("the cache shares its rows with the slice returned by get: %q", again[0].content)
	}
}

// (g) Leaving before the history has loaded remembers nothing.
func TestTranscriptCache_LeavingBeforeTheLoadRemembersNothing(t *testing.T) {
	m, _ := tcacheApp(t)
	m = tcacheSelect(m, "sess-2") // left before its reply
	m = tcacheSelect(m, "sess-3")

	m = tcacheSelect(m, "sess-2")

	if !m.chatModel.historyLoading {
		t.Error("a session left before it loaded was painted from memory")
	}
}

// (g) Leaving a chat that never loaded must not disturb what is already
// remembered for that session. Opening a session from the agent picker does
// not paint from memory, so this is the path where the two can coexist.
func TestTranscriptCache_LeavingAnUnloadedChatKeepsTheExistingEntry(t *testing.T) {
	m, _ := tcacheVisited(t) // sess-2 remembered, sess-3 open
	m = w1Deliver(m, sessionCreatedMsg{sessionKey: "sess-2", agentID: "agent-1", agentName: "Scout", modelID: "model-1"})
	if !m.chatModel.historyLoading {
		t.Fatal("precondition: a session opened from the agent picker starts unloaded")
	}

	m = tcacheSelect(m, "sess-3") // leave before the reply

	if _, ok := m.transcripts.get("sess-2"); !ok {
		t.Error("leaving a chat that never loaded dropped what was remembered for it")
	}
}

// Only what the gateway reported is remembered: rows of a turn still in
// flight when the operator left would be painted as if they were history.
func TestTranscriptCache_LiveRowsAreNotRemembered(t *testing.T) {
	m, _ := tcacheApp(t)
	m = tcacheLoad(tcacheSelect(m, "sess-2"))
	m.chatModel.appendMessage(chatMessage{role: "assistant", content: "half a reply", streaming: true})

	m = tcacheSelect(tcacheSelect(m, "sess-3"), "sess-2")

	got := tcacheTranscript(m)
	if strings.Contains(got, "half a reply") {
		t.Errorf("a live row was remembered and painted as history: %q", got)
	}
	if !strings.Contains(got, "transcript of sess-2") {
		t.Errorf("the session's history was not remembered: %q", got)
	}
}

// (g) A removed session is forgotten, whether or not it is the open one.
func TestTranscriptCache_RemovedSessionsAreForgotten(t *testing.T) {
	t.Run("the open session", func(t *testing.T) {
		m, _ := tcacheVisited(t)
		m = tcacheLoad(tcacheSelect(m, "sess-2"))
		m = w1Deliver(m, sessionRemovedMsg{sessionKey: "sess-2", verb: sessionRemovalDelete})
		m = tcacheSelect(m, "sess-3") // the move that follows a removal

		if _, ok := m.transcripts.get("sess-2"); ok {
			t.Error("the removed session was remembered when the chat moved off it")
		}
	})
	t.Run("a session that is not open", func(t *testing.T) {
		m, _ := tcacheVisited(t) // sess-2 remembered, sess-3 open
		m = w1Deliver(m, sessionRemovedMsg{sessionKey: "sess-2", verb: sessionRemovalArchive})

		if _, ok := m.transcripts.get("sess-2"); ok {
			t.Error("a removed session that was not open is still remembered")
		}
	})
	t.Run("a failed removal forgets nothing", func(t *testing.T) {
		m, _ := tcacheVisited(t)
		m = w1Deliver(m, sessionRemovedMsg{sessionKey: "sess-2", verb: sessionRemovalDelete, err: fmt.Errorf("refused")})

		if _, ok := m.transcripts.get("sess-2"); !ok {
			t.Error("a removal that failed still forgot the session")
		}
	})
}

// (g) /reset forgets the session that was reset, and its reply cannot touch
// a chat the operator has since switched to.
func TestTranscriptCache_ResetForgetsTheOldKey(t *testing.T) {
	m, _ := tcacheVisited(t) // sess-2 remembered, sess-3 open

	m = w1Deliver(m, sessionClearedMsg{sessionKey: "sess-2", newSessionKey: "sess-2-fresh"})

	if _, ok := m.transcripts.get("sess-2"); ok {
		t.Error("a reset session is still remembered under its old key")
	}
	if m.chatModel.sessionKey != "sess-3" {
		t.Errorf("a reset reply for sess-2 renamed the open chat to %q", m.chatModel.sessionKey)
	}
	if got := tcacheTranscript(m); !strings.Contains(got, "transcript of sess-3") {
		t.Errorf("a reset reply for sess-2 cleared the open chat: %q", got)
	}
}

func TestTranscriptCache_ResetOfTheOpenSessionStillApplies(t *testing.T) {
	m, _ := tcacheVisited(t) // sess-3 open

	m = w1Deliver(m, sessionClearedMsg{sessionKey: "sess-3", newSessionKey: "sess-3-fresh"})

	if m.chatModel.sessionKey != "sess-3-fresh" {
		t.Errorf("the open chat is on %q after its reset, want sess-3-fresh", m.chatModel.sessionKey)
	}
}

// (g) A connection change empties the cache, and the chat left over from
// the old connection is not remembered when the first new chat opens.
func TestTranscriptCache_ConnectionChangeForgetsEverything(t *testing.T) {
	m, _ := tcacheVisited(t) // sess-2 remembered, sess-3 open on the old backend

	m = w1Deliver(m, showConnectionsMsg{})
	if _, ok := m.transcripts.get("sess-2"); ok {
		t.Fatal("leaving the connection did not empty the cache")
	}

	m.backend = &w1ProbeBackend{} // the new connection
	m = w1Deliver(m, sessionCreatedMsg{sessionKey: "main", agentID: "agent-1", agentName: "Scout", modelID: "model-1"})

	if _, ok := m.transcripts.get("sess-3"); ok {
		t.Error("the old connection's chat was remembered when the first new chat opened")
	}
}

// (h) A history reply is applied wherever the operator happens to be.
func TestTranscriptCache_HistoryReplyIsDeliveredInEveryState(t *testing.T) {
	t.Run("another view", func(t *testing.T) {
		m, _ := tcacheApp(t)
		m = tcacheSelect(m, "sess-2")
		reply := m.chatModel.loadHistory()()
		m.state = viewConfig

		m = w1Deliver(m, reply)

		if m.chatModel.historyLoading {
			t.Error("a reply delivered outside the chat view was not applied")
		}
	})
	t.Run("the chat view with the sidebar focused", func(t *testing.T) {
		m, _ := tcacheApp(t)
		m = tcacheSelect(m, "sess-2")
		reply := m.chatModel.loadHistory()()
		m.sidebarFocus = true

		m = w1Deliver(m, reply)

		if m.chatModel.historyLoading {
			t.Error("a reply delivered while the sidebar had focus was not applied")
		}
	})
	t.Run("a chat parked behind a cron transcript", func(t *testing.T) {
		m, _ := tcacheApp(t)
		m = tcacheSelect(m, "sess-2")
		reply := m.chatModel.loadHistory()()
		m.cronsReturnChat, m.cronsReturnValid = m.chatModel, true
		m.chatModel = newChatModel(m.backend, "", "agent-1", "Scout", "", config.DefaultPreferences(), true, "", "", false)
		m.chatModel.transcript = true

		m = w1Deliver(m, reply)

		if m.cronsReturnChat.historyLoading {
			t.Error("a reply for the parked chat was not applied to it")
		}
		if len(m.chatModel.messages) != 0 {
			t.Errorf("a reply for the parked chat was written into the cron transcript: %d rows", len(m.chatModel.messages))
		}
	})
	t.Run("a reply for neither is dropped", func(t *testing.T) {
		m, _ := tcacheVisited(t) // sess-3 open
		m = w1Deliver(m, historyLoadedMsg{sessionKey: "sess-9", messages: []chatMessage{{role: "assistant", content: "stray"}}})

		if got := tcacheTranscript(m); strings.Contains(got, "stray") {
			t.Errorf("a reply for a session that is not open was applied: %q", got)
		}
	})
}
