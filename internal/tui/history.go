package tui

import (
	"context"
	"encoding/json"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// historyResponse is the structure of the chat.history RPC response.
type historyResponse struct {
	Messages []historyMessage `json:"messages"`
}

type historyMessage struct {
	Role      string             `json:"role"`
	Content   []chatContentBlock `json:"content"`
	Timestamp int64              `json:"timestamp,omitempty"` // unix millis, when present
}

// UnmarshalJSON normalizes the message's content field, which the gateway
// sends in either of the two Anthropic shapes: a plain string (the short
// form) or an array of typed content blocks. A string payload is wrapped
// in a single text block so the rest of the history pipeline can treat
// both shapes uniformly. An unrecognised content shape leaves Content nil
// rather than failing the whole load — one odd message must not blank the
// entire conversation history.
func (hm *historyMessage) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		Timestamp int64           `json:"timestamp,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	hm.Role = raw.Role
	hm.Timestamp = raw.Timestamp
	hm.Content = nil
	if len(raw.Content) == 0 {
		return nil
	}
	// Array form: typed content blocks.
	var blocks []chatContentBlock
	if err := json.Unmarshal(raw.Content, &blocks); err == nil {
		hm.Content = blocks
		return nil
	}
	// String form: wrap the text in a single text block.
	var s string
	if err := json.Unmarshal(raw.Content, &s); err == nil {
		if s != "" {
			hm.Content = []chatContentBlock{{Type: "text", Text: s}}
		}
		return nil
	}
	// Unknown shape — leave content empty so the message is skipped
	// downstream instead of aborting the whole history load.
	return nil
}

func (m chatModel) loadHistory() tea.Cmd {
	sessionKey := m.sessionKey
	b := m.backend
	job := m.renderJob()
	limit := m.historyLimit
	return func() tea.Msg {
		msgs, err := fetchHistory(b, sessionKey, job.renderer(), limit)
		stampRendered(msgs, job.stamp)
		return historyLoadedMsg{sessionKey: sessionKey, messages: msgs, err: err}
	}
}

// refreshHistoryAt issues a server-history fetch and tags the result
// with the boundary captured at issue time. The merge in the
// historyRefreshMsg handler keeps every existing row whose gen exceeds
// boundary (the live tail) and replaces the rest with the fetched
// state — making it safe for callers to refresh while the next turn
// is already streaming, a tool card is mid-execution, or a pending
// system row is awaiting outcome.
//
// Callers in the chat-event final/error/aborted paths pass the gen
// they captured *before* bumping (i.e. the just-finalised turn's gen),
// so that turn and everything older gets replaced by canonical state.
// Callers on the queue-drained path pass m.gen-1 for the same reason.
func (m chatModel) refreshHistoryAt(boundary uint64) tea.Cmd {
	sessionKey := m.sessionKey
	b := m.backend
	job := m.renderJob()
	limit := m.historyLimit
	return func() tea.Msg {
		msgs, err := fetchHistory(b, sessionKey, job.renderer(), limit)
		stampRendered(msgs, job.stamp)
		return historyRefreshMsg{sessionKey: sessionKey, messages: msgs, boundary: boundary, err: err}
	}
}

// markdownRenderer is the one method of *glamour.TermRenderer the history
// path uses. Naming it lets a test count renders.
type markdownRenderer interface {
	Render(in string) (string, error)
}

// renderStamp is what a rendered row's content depends on besides its
// source: the wrap width and the theme. Comparable, so two stamps are equal
// exactly when a row rendered at one needs no re-render at the other.
type renderStamp struct {
	width int
	theme config.ThemePreferences
}

// renderJob is what a command needs to render Markdown off the UI
// goroutine: the stamp to render at and the factory to build a renderer
// with. The command calls renderer() inside its own closure, so every
// command renders with a renderer no other goroutine holds.
type renderJob struct {
	stamp   renderStamp
	factory func(renderStamp) markdownRenderer
}

// renderer builds this job's private renderer, or returns nil when the chat
// has no factory or the factory has none to give.
func (j renderJob) renderer() markdownRenderer {
	if j.factory == nil {
		return nil
	}
	return j.factory(j.stamp)
}

// renderJob captures the chat's current stamp and factory for a command.
func (m chatModel) renderJob() renderJob {
	return renderJob{stamp: m.stamp(), factory: m.newRenderer}
}

// stampRendered records s on every rendered row of rows.
func stampRendered(rows []chatMessage, s renderStamp) {
	for i := range rows {
		if rows[i].rendered && rows[i].raw != "" {
			rows[i].stamp = s
		}
	}
}

// staleAt reports whether the row was rendered at a stamp other than s.
func (c chatMessage) staleAt(s renderStamp) bool {
	return c.rendered && c.raw != "" && c.stamp != s
}

// rerenderCmd returns the command that re-renders the chat's stale rows at
// its current stamp, or nil when there are none or one is already in flight
// for that stamp. AppModel.Update is its only caller: every path that can
// make a row stale (a resize, a theme change, a seed from the cache, a
// history reply rendered before a resize) ends there, so none of them
// renders on the UI goroutine and none has to remember to ask.
func (m *chatModel) rerenderCmd() tea.Cmd {
	stamp := m.stamp()
	if m.newRenderer == nil || m.rerenderFor == stamp {
		return nil
	}
	seen := map[string]bool{}
	var raws []string
	for i := range m.messages {
		if raw := m.messages[i].raw; m.messages[i].staleAt(stamp) && !seen[raw] {
			seen[raw] = true
			raws = append(raws, raw)
		}
	}
	if len(raws) == 0 {
		return nil
	}
	m.rerenderFor = stamp
	sessionKey := m.sessionKey
	job := m.renderJob()
	return func() tea.Msg { return rerenderRows(sessionKey, job, raws) }
}

// rerenderRows renders each source with the job's private renderer. A
// source that cannot be rendered is reported as failed, so the chat can
// stamp its row and stop asking.
func rerenderRows(sessionKey string, job renderJob, raws []string) transcriptRerenderedMsg {
	out := transcriptRerenderedMsg{
		sessionKey: sessionKey,
		stamp:      job.stamp,
		rendered:   make(map[string]string, len(raws)),
		failed:     map[string]bool{},
	}
	renderer := job.renderer()
	for _, raw := range raws {
		if renderer == nil {
			out.failed[raw] = true
			continue
		}
		content, err := renderer.Render(raw)
		if err != nil {
			out.failed[raw] = true
			continue
		}
		out.rendered[raw] = strings.TrimSpace(content)
	}
	return out
}

// applyRerender takes a re-render result into the chat. A result for a
// stamp the chat has moved on from changes no row; the rows stay stale and
// the next Update issues a fresh command.
func (m *chatModel) applyRerender(msg transcriptRerenderedMsg) {
	m.rerenderFor = renderStamp{}
	if msg.stamp != m.stamp() {
		return
	}
	for i := range m.messages {
		row := &m.messages[i]
		if !row.staleAt(msg.stamp) {
			continue
		}
		if content, ok := msg.rendered[row.raw]; ok {
			row.content = content
			row.stamp = msg.stamp
		} else if msg.failed[row.raw] {
			row.stamp = msg.stamp
		}
	}
}

// fetchHistory loads a session's history and returns the rows the
// transcript shows: user and assistant messages with text, at most the last
// limit of them (all of them when limit is 0 or less). The gateway is asked
// for limit, but does not always honour it — a session with an expanded CLI
// import comes back whole — so the reply is trimmed here, and trimmed
// BEFORE rendering: Markdown rendering is the per-message cost, and it must
// be bounded by limit, not by what the gateway chose to send.
func fetchHistory(b backend.Backend, sessionKey string, renderer markdownRenderer, limit int) ([]chatMessage, error) {
	raw, err := b.ChatHistory(context.Background(), sessionKey, limit)
	if err != nil {
		return nil, err
	}
	var resp historyResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	msgs := lastShownMessages(resp.Messages, limit)
	for i := range msgs {
		if msgs[i].role != "assistant" || renderer == nil || !looksLikeMarkdown(msgs[i].content) {
			continue
		}
		if out, err := renderer.Render(msgs[i].content); err == nil {
			msgs[i].raw = msgs[i].content
			msgs[i].content = strings.TrimSpace(out)
			msgs[i].rendered = true
		}
	}
	return msgs, nil
}

// lastShownMessages returns, oldest first and unrendered, the last limit
// history entries the transcript shows (all of them when limit is 0 or
// less). It walks from the newest entry backwards and stops at limit, so
// entries beyond the window cost nothing.
func lastShownMessages(history []historyMessage, limit int) []chatMessage {
	var newestFirst []chatMessage
	for i := len(history) - 1; i >= 0; i-- {
		if limit > 0 && len(newestFirst) == limit {
			break
		}
		if msg, shown := shownMessage(history[i]); shown {
			newestFirst = append(newestFirst, msg)
		}
	}
	msgs := make([]chatMessage, len(newestFirst))
	for i, msg := range newestFirst {
		msgs[len(newestFirst)-1-i] = msg
	}
	return msgs
}

// shownMessage converts one history entry to a transcript row. shown is
// false for entries the transcript omits: other roles, and messages with no
// text left once the internal blocks are stripped.
func shownMessage(hm historyMessage) (msg chatMessage, shown bool) {
	if hm.Role != "user" && hm.Role != "assistant" {
		return chatMessage{}, false
	}
	var parts []string
	var thinkingParts []string
	for _, block := range hm.Content {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
		if block.Type == "thinking" && block.Text != "" {
			thinkingParts = append(thinkingParts, block.Text)
		}
	}
	text := strings.Join(parts, "\n")
	if hm.Role == "user" && text != "" {
		text = stripInternalContextBlocks(text)
		text = stripLocalAgentSkillBlocks(text)
		text = stripSystemLines(text)
	}
	if text == "" {
		return chatMessage{}, false
	}
	return chatMessage{
		role:        hm.Role,
		content:     text,
		thinking:    strings.Join(thinkingParts, "\n"),
		timestampMs: hm.Timestamp,
	}, true
}

// buildCronTranscriptMessages reconstructs a transcript-style message
// list from a cron job and its run log. Cron-isolated runs don't keep
// a queryable chat session, but the payload (the user's prompt) and
// each run's summary/error are persisted in the run log — which is
// what the cron-detail run-history previews already display. This
// surfaces the same content as a normal-looking conversation: each run
// becomes a separator, a user turn (the payload), an assistant turn
// (the summary, or the error if the run produced no output), and a
// trailing system error note if the run logged an error or delivery
// failure alongside the summary — the agent's work succeeded but the
// run was marked failed for an ancillary reason worth surfacing.
func buildCronTranscriptMessages(payload string, runs []protocol.CronRunLogEntry, renderer *glamour.TermRenderer) []chatMessage {
	if len(runs) == 0 {
		return nil
	}
	// Run logs arrive newest-first; render oldest-first so the
	// transcript reads top-down chronologically like a real chat.
	ordered := make([]protocol.CronRunLogEntry, len(runs))
	for i, r := range runs {
		ordered[len(runs)-1-i] = r
	}
	var msgs []chatMessage
	for _, r := range ordered {
		var ts int64
		if r.RunAtMs != nil {
			ts = *r.RunAtMs
		}
		msgs = append(msgs, chatMessage{role: "separator", timestampMs: ts})
		if payload != "" {
			msgs = append(msgs, chatMessage{role: "user", content: payload, timestampMs: ts})
		}
		errNote := cronRunErrorNote(r)
		switch {
		case r.Summary != "":
			content := r.Summary
			raw := ""
			rendered := false
			if renderer != nil && looksLikeMarkdown(content) {
				if out, err := renderer.Render(content); err == nil {
					raw = content
					content = strings.TrimSpace(out)
					rendered = true
				}
			}
			msgs = append(msgs, chatMessage{role: "assistant", content: content, raw: raw, rendered: rendered, timestampMs: ts})
			if errNote != "" {
				msgs = append(msgs, chatMessage{role: "system", errMsg: errNote, timestampMs: ts})
			}
		case errNote != "":
			msgs = append(msgs, chatMessage{role: "assistant", errMsg: errNote, timestampMs: ts})
		}
	}
	return msgs
}

// cronRunErrorNote joins the run-level error and delivery-level error
// into a single reader-friendly string, deduping when the gateway has
// echoed the same text into both fields.
func cronRunErrorNote(r protocol.CronRunLogEntry) string {
	var parts []string
	seen := map[string]bool{}
	add := func(label, val string) {
		v := strings.TrimSpace(val)
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		parts = append(parts, label+": "+v)
	}
	add("Run error", r.Error)
	add("Delivery error", r.DeliveryError)
	return strings.Join(parts, "\n")
}

// stripSystemLines removes "System:" prefixed lines and leading whitespace
// from user messages, returning only the human-authored portion.
// Also matches "System (untrusted):" which the gateway may substitute.
func stripSystemLines(s string) string {
	lines := strings.Split(s, "\n")
	var kept []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isSystemLine(trimmed) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// internalContextBegin / internalContextEnd delimit the gateway-injected
// context envelope. The gateway prepends it to the user turn it sends to
// the model; it is plumbing, not anything the human typed, so it must not
// surface in the rendered transcript or be offered up by the up-arrow
// history recall.
const (
	internalContextBegin = "<<<BEGIN_OPENCLAW_INTERNAL_CONTEXT>>>"
	internalContextEnd   = "<<<END_OPENCLAW_INTERNAL_CONTEXT>>>"
)

// stripInternalContextBlocks removes every BEGIN/END internal-context span
// (inclusive) from a user message, returning only the human-authored
// remainder. A BEGIN with no matching END strips to end of string — better
// to drop a malformed envelope entirely than leak gateway plumbing into the
// input. Handles markers that share a line with real text, not just the
// own-line form, so a stray prefix can't slip the recall.
func stripInternalContextBlocks(s string) string {
	if !strings.Contains(s, internalContextBegin) {
		return s
	}
	for {
		start := strings.Index(s, internalContextBegin)
		if start < 0 {
			break
		}
		rest := s[start+len(internalContextBegin):]
		endRel := strings.Index(rest, internalContextEnd)
		if endRel < 0 {
			s = s[:start]
			break
		}
		s = s[:start] + rest[endRel+len(internalContextEnd):]
	}
	return strings.TrimSpace(s)
}

// stripLocalAgentSkillBlocks removes the <local-agent-skill> envelope so it
// is hidden from the rendered transcript. Strips the "Please use the following
// skill(s):" preamble line and every <local-agent-skill ...>...</local-agent-skill>
// block (inclusive). Collapses runs of blank lines left behind.
func stripLocalAgentSkillBlocks(s string) string {
	if s == "" || !strings.Contains(s, "<local-agent-skill") {
		return s
	}
	lines := strings.Split(s, "\n")
	var kept []string
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inBlock {
			if strings.Contains(trimmed, "</local-agent-skill>") {
				inBlock = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "<local-agent-skill") {
			if !strings.Contains(trimmed, "</local-agent-skill>") {
				inBlock = true
			}
			continue
		}
		if trimmed == "Please use the following skill:" || trimmed == "Please use the following skills:" {
			continue
		}
		kept = append(kept, line)
	}
	// Collapse runs of blank lines.
	var out []string
	prevBlank := false
	for _, line := range kept {
		blank := strings.TrimSpace(line) == ""
		if blank && prevBlank {
			continue
		}
		out = append(out, line)
		prevBlank = blank
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// isSystemLine returns true if the line starts with a System prefix,
// matching both "System:" and gateway-rewritten forms like "System (untrusted):".
func isSystemLine(line string) bool {
	if strings.HasPrefix(line, "System:") {
		return true
	}
	if strings.HasPrefix(line, "System (") {
		// Match "System (<anything>):" pattern.
		if idx := strings.Index(line, "):"); idx >= 0 {
			return true
		}
	}
	return false
}

// looksLikeMarkdown returns true when assistant text likely benefits from
// Glamour rendering. Plain single-line replies should stay unrendered so they
// don't pick up paragraph indentation from the markdown renderer.
func looksLikeMarkdown(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	for _, marker := range []string{"```", "`", "**", "__", "* ", "- ", "> ", "|", "\n#"} {
		if strings.Contains(s, marker) {
			return true
		}
	}

	lines := strings.Split(s, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			return true
		}
		if len(line) >= 3 && line[0] >= '0' && line[0] <= '9' && line[1] == '.' && line[2] == ' ' {
			return true
		}
	}

	return false
}
