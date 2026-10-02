package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// sessionItem is a list item for the session browser.
type sessionItem struct {
	key          string
	title        string
	lastMessage  string
	updatedAt    int64 // Unix millis
	group        string
	hasActiveRun *bool
	// stoppedAt is when the client saw this session's run end; zero when
	// no stop marker is showing. See markRecentStops.
	stoppedAt time.Time
}

// isRunning reports whether the gateway positively reports an active run.
func (i sessionItem) isRunning() bool {
	return i.hasActiveRun != nil && *i.hasActiveRun
}

// stopMarkerTTL is how long a session whose run just ended keeps its stop
// glyph in the sidebar before settling to idle.
const stopMarkerTTL = 8 * time.Second

// stopMarkerExpiredMsg fires stopMarkerTTL after a stop was first seen, so
// the marker clears without waiting for the next sessions.changed event.
type stopMarkerExpiredMsg struct{}

// markRecentStops returns next with stop markers applied against the
// previous list: a session that was running and no longer is gets stamped
// now, and a stamp younger than stopMarkerTTL is carried across refreshes.
// newStop reports whether any session was freshly stamped. The transition
// is all the client can see — sessions.list carries no last-outcome field,
// so a finished run and an aborted one mark identically.
func markRecentStops(prev, next []sessionItem, now time.Time) (marked []sessionItem, newStop bool) {
	before := make(map[string]sessionItem, len(prev))
	for _, p := range prev {
		before[p.key] = p
	}
	marked = make([]sessionItem, len(next))
	for idx, n := range next {
		p, known := before[n.key]
		switch {
		case !known || n.isRunning():
			// Nothing to compare against, or running again.
		case p.isRunning():
			n.stoppedAt = now
			newStop = true
		case stopMarkerLive(p, now):
			n.stoppedAt = p.stoppedAt
		}
		marked[idx] = n
	}
	return marked, newStop
}

// stopMarkerLive reports whether an item's stop marker is still inside its TTL.
func stopMarkerLive(i sessionItem, now time.Time) bool {
	return !i.stoppedAt.IsZero() && now.Sub(i.stoppedAt) < stopMarkerTTL
}

func (i sessionItem) FilterValue() string {
	if i.title != "" {
		return cleanSessionDisplayKey(i.title)
	}
	return cleanSessionDisplayKey(i.key)
}

// sessionGroupHeader is a non-selectable list item used as a group separator.
type sessionGroupHeader struct {
	label string
}

func (h sessionGroupHeader) FilterValue() string { return "" }

// sessionRowChromeCells is what a session row spends before its title: the
// two-cell cursor prefix and the two-cell activity indicator.
const sessionRowChromeCells = 4

// sessionDelegate renders each item in the session list.
type sessionDelegate struct{}

func (d sessionDelegate) Height() int                             { return 1 }
func (d sessionDelegate) Spacing() int                            { return 0 }
func (d sessionDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d sessionDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	switch i := item.(type) {
	case sessionGroupHeader:
		header := lipgloss.NewStyle().
			Bold(true).
			Foreground(accent).
			Render(fmt.Sprintf("── %s ──", i.label))
		fmt.Fprint(w, "  "+header+"\n")

	case sessionItem:
		displayTitle := i.title
		if displayTitle == "" {
			displayTitle = cleanSessionDisplayKey(i.key)
		} else {
			displayTitle = cleanSessionDisplayKey(displayTitle)
		}

		// The title gets what the cursor prefix and the indicator leave.
		// Truncation is by display cells, never bytes: a byte slice cuts
		// a CJK or emoji title mid-rune and emits invalid UTF-8.
		titleCells := max(m.Width()-sessionRowChromeCells, 0)
		displayTitle = ansi.Truncate(displayTitle, titleCells, "…")

		// Activity indicator:
		// ● (accent/amber) if active, ○ (subtle) if unknown/omitted (*bool == nil), or space if idle (false)
		// Activity indicator with session-type differentiation:
		// Real agent sessions (kings):   ♛ active, ♕ dormant
		// Subagent sessions (pawns):   ♟ active, ♙ standby
		isSub := strings.Contains(i.key, ":subagent:")
		indicator := "  "
		if !i.stoppedAt.IsZero() {
			// Just stopped: one glyph for agents and subagents alike,
			// until expireStopMarkers clears the stamp.
			indicator = lipgloss.NewStyle().Foreground(execClr).Bold(true).Render("■ ")
		} else if i.hasActiveRun != nil {
			if *i.hasActiveRun {
				if isSub {
					indicator = lipgloss.NewStyle().Foreground(subtle).Bold(true).Render("♟ ")
				} else {
					indicator = lipgloss.NewStyle().Foreground(accent).Bold(true).Render("♛ ")
				}
			}
		} else {
			if isSub {
					indicator = lipgloss.NewStyle().Foreground(subtle).Render("♙ ")
			} else {
					indicator = lipgloss.NewStyle().Foreground(subtle).Render("♕ ")
			}
		}

		// Pad to the pane: every row is exactly m.Width() cells, so the
		// sidebar's rendered width never depends on its longest title.
		fill := strings.Repeat(" ", max(titleCells-ansi.StringWidth(displayTitle), 0))
		if index == m.Index() {
			titleStr := lipgloss.NewStyle().
				Foreground(accent).
				Bold(true).
				Render(displayTitle)
			fmt.Fprint(w, "> "+indicator+titleStr+fill)
		} else {
			fmt.Fprint(w, "  "+indicator+displayTitle+fill)
		}
	}
}

// sessionsModel is the session browser view.
type sessionsModel struct {
	list      list.Model
	backend   backend.Backend
	agentID   string
	agentName string
	modelID   string
	mainKey   string
	loading   bool
	err       error
	hideHints bool
	// hideSubagents filters ":subagent:" sessions from the sidebar
	// list. Toggled via the 'h' key / toggle-subagents action.
	hideSubagents bool
	// allSessions holds the unfiltered session list so toggling
	// hideSubagents rebuilds the display without a gateway re-fetch.
	allSessions []sessionItem
	// selecting is true after the user has picked a session and we're
	// about to transition into the chat view. The window is brief —
	// sessionSelectedMsg dispatches synchronously and the chat view's
	// own loading state takes over on the next tick — but during it
	// the list is still on screen, so we freeze input + render a
	// loading line to match the agent picker's behaviour and stop the
	// user from racing the cursor against the transition.
	selecting bool
	// selectingTitle is the chosen session's display label, surfaced
	// in the loading view so the user can confirm what they picked.
	selectingTitle string
	activeConn     *config.Connection // rendered above the session list — see renderConnectionBanner.
	// now is the clock stop markers are stamped and expired against; nil
	// means time.Now. Tests inject a hand-advanced clock.
	now func() time.Time
}

func (m sessionsModel) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// expireStopMarkers clears stop markers past their TTL, updating the
// visible rows in place so the cursor stays where the user left it.
func (m *sessionsModel) expireStopMarkers() {
	now := m.clock()
	for idx := range m.allSessions {
		if !stopMarkerLive(m.allSessions[idx], now) {
			m.allSessions[idx].stoppedAt = time.Time{}
		}
	}
	for idx, it := range m.list.Items() {
		s, ok := it.(sessionItem)
		if !ok || s.stoppedAt.IsZero() || stopMarkerLive(s, now) {
			continue
		}
		s.stoppedAt = time.Time{}
		m.list.SetItem(idx, s)
	}
}

func newSessionsModel(b backend.Backend, agentID, agentName, modelID, mainKey string, hideHints bool, activeConn *config.Connection, disableExitKeys bool) sessionsModel {
	l := list.New(nil, sessionDelegate{}, 0, 0)
	l.Title = "Sessions"
	l.SetShowStatusBar(false)
	// Same rationale as selectModel: hide the list widget's keyboard
	// footer when the embedder is rendering its own action surface.
	l.SetShowHelp(!hideHints)
	l.Styles.Title = headerStyle
	l.SetFilteringEnabled(false)
	if disableExitKeys {
		l.KeyMap.Quit.Unbind()
		l.KeyMap.ForceQuit.Unbind()
	}

	return sessionsModel{
		list:       l,
		backend:    b,
		agentID:    agentID,
		agentName:  agentName,
		modelID:    modelID,
		mainKey:    mainKey,
		loading:    true,
		hideHints:  hideHints,
		activeConn: activeConn,
	}
}

// sessionsListResponse is the structure of the sessions.list RPC response.
type sessionsListResponse struct {
	Sessions []json.RawMessage `json:"sessions"`
}

// sessionListEntry contains the fields we care about from a session entry.
// The gateway returns many additional fields which we ignore.
type sessionListEntry struct {
	Key                string `json:"key"`
	DerivedTitle       string `json:"derivedTitle"`
	LastMessagePreview string `json:"lastMessagePreview"`
	UpdatedAt          int64  `json:"updatedAt"`
	Model              string `json:"model"`
	HasActiveRun       *bool  `json:"hasActiveRun,omitempty"`
}

// cleanSessionDisplayKey strips internal routing prefixes like agent:<agentId>:
// generically so session display is clean and unburdened by metadata.
// The raw key itself is preserved untouched on sessionItem.key.
func cleanSessionDisplayKey(s string) string {
	s = strings.TrimSpace(s)
	// Strip generic agent:<agentId>: prefix
	if strings.HasPrefix(s, "agent:") {
		parts := strings.SplitN(s, ":", 3)
		if len(parts) == 3 {
			s = parts[2]
		} else if len(parts) == 2 {
			s = parts[1]
		}
	}
	// Also strip leading dashboard: if present
	if strings.HasPrefix(s, "dashboard:") {
		s = strings.TrimPrefix(s, "dashboard:")
	}
	// If it's a UUID, format cleanly or shorten
	return s
}

// cleanDerivedTitle strips gateway metadata prefixes from the derived title.
func cleanDerivedTitle(title string) string {
	// Strip "Sender (untrusted metadata): " and similar prefixes.
	if idx := strings.Index(title, "Sender (untrusted metadata):"); idx == 0 {
		title = strings.TrimSpace(title[len("Sender (untrusted metadata):"):])
	}
	// Strip leading markdown fences that sometimes appear.
	title = strings.TrimPrefix(title, "```json")
	title = strings.TrimPrefix(title, "```")
	title = strings.TrimSpace(title)
	// Strip JSON-like content at the kingt (e.g. '{ "label": "cli",...').
	if strings.HasPrefix(title, "{") {
		title = ""
	}
	title = cleanSessionDisplayKey(title)
	return title
}

// sessionGroup returns a human-readable group name based on the session key.
func sessionGroup(key string) string {
	if strings.Contains(key, ":cron:") {
		return "Scheduled"
	}
	return "Conversations"
}

// parseSessionsPayload decodes a sessions.list RPC response into sorted
// session items. Shared by the modal load path (loadSessions) and the
// sidebar's debounced refresh so both produce identical items for
// identical payloads.
func parseSessionsPayload(raw []byte) ([]sessionItem, error) {
	slog.Debug("sessions list", "raw", string(raw))
	var resp sessionsListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	var items []sessionItem
	for _, rawEntry := range resp.Sessions {
		var entry sessionListEntry
		if err := json.Unmarshal(rawEntry, &entry); err != nil {
			slog.Debug("sessions list entry parse error", "err", err)
			continue
		}
		title := cleanDerivedTitle(entry.DerivedTitle)
		if len(title) > 80 {
			title = title[:77] + "..."
		}
		items = append(items, sessionItem{
			key:          entry.Key,
			title:        title,
			lastMessage:  entry.LastMessagePreview,
			updatedAt:    entry.UpdatedAt,
			group:        sessionGroup(entry.Key),
			hasActiveRun: entry.HasActiveRun,
		})
	}
	// Sort by updatedAt descending within each group.
	sort.Slice(items, func(i, j int) bool {
		if items[i].group != items[j].group {
			// "Conversations" before "Scheduled"
			return items[i].group < items[j].group
		}
		return items[i].updatedAt > items[j].updatedAt
	})
	return items, nil
}

func (m sessionsModel) loadSessions() tea.Cmd {
	b := m.backend
	agentID := m.agentID
	return func() tea.Msg {
		raw, err := b.SessionsList(context.Background(), agentID)
		if err != nil {
			return sessionsLoadedMsg{err: err}
		}
		items, err := parseSessionsPayload(raw)
		if err != nil {
			return sessionsLoadedMsg{err: err}
		}
		return sessionsLoadedMsg{sessions: items}
	}
}

func (m sessionsModel) Init() tea.Cmd {
	return m.loadSessions()
}

func (m sessionsModel) Update(msg tea.Msg) (sessionsModel, tea.Cmd) {
	// After enter is pressed we hand control to the parent which
	// will swap us out for the chat view. Drop further input —
	// keystrokes, list-internal cmds — so the user can't keep
	// moving the cursor while the transition is in flight.
	// The expiry sweep runs ahead of that guard: it is not input, and a
	// dropped expiry would leave its marker up until the next refresh.
	if _, ok := msg.(stopMarkerExpiredMsg); ok {
		m.expireStopMarkers()
		return m, nil
	}
	if m.selecting {
		return m, nil
	}

	switch msg := msg.(type) {
	case sessionsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		var newStop bool
		m.allSessions, newStop = markRecentStops(m.allSessions, msg.sessions, m.clock())
		m.rebuildList()
		if newStop {
			return m, tea.Tick(stopMarkerTTL, func(time.Time) tea.Msg { return stopMarkerExpiredMsg{} })
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// skipHeaders adjusts the list selection to skip over group headers
// in the given direction (+1 for down, -1 for up).
func (m *sessionsModel) skipHeaders(dir int) {
	items := m.list.Items()
	idx := m.list.Index()
	for idx >= 0 && idx < len(items) {
		if _, isHeader := items[idx].(sessionGroupHeader); !isHeader {
			break
		}
		idx += dir
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(items) {
		idx = len(items) - 1
	}
	m.list.Select(idx)
}

func (m sessionsModel) handleKey(msg tea.KeyPressMsg) (sessionsModel, tea.Cmd) {
	// Cursor navigation stays inline — these are intrinsic list controls,
	// not discoverable view-level commands.
	switch msg.String() {
	case "up", "k":
		m.list, _ = m.list.Update(msg)
		m.skipHeaders(-1)
		return m, nil

	case "down", "j":
		m.list, _ = m.list.Update(msg)
		m.skipHeaders(1)
		return m, nil

	case "enter":
		if m.loading || m.err != nil {
			return m, nil
		}
		if item, ok := m.list.SelectedItem().(sessionItem); ok {
			m.selecting = true
			m.selectingTitle = item.title
			if m.selectingTitle == "" {
				m.selectingTitle = cleanSessionDisplayKey(item.key)
			}
			return m, func() tea.Msg {
				return sessionSelectedMsg{
					sessionKey: item.key,
					agentName:  m.agentName,
					modelID:    m.modelID,
				}
			}
		}
	}

	// Discoverable shortcuts route through TriggerAction so the help
	// line and the keystroke share a single source of truth (Actions()).
	for _, a := range m.Actions() {
		if a.Key == msg.String() {
			return m.TriggerAction(a.ID)
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// Actions returns the discoverable, view-level commands the session
// browser currently exposes. Loading/error transitions are reflected
// because the list is recomputed on every Update tick.
func (m sessionsModel) Actions() []Action {
	if m.selecting {
		// Mirror the loading view: no actionable surface while we
		// wait for the chat view to take over.
		return nil
	}
	var actions []Action
	if !m.loading && m.err == nil {
		actions = append(actions, Action{ID: "new-session", Label: "New session", Key: "n"})
	}
	actions = append(actions, Action{ID: "toggle-subagents", Label: "Hide/Show subagents", Key: "h"})
	actions = append(actions, Action{ID: "back", Label: "Back", Key: "esc"})
	if m.err != nil {
		actions = append(actions, Action{ID: "retry", Label: "Retry", Key: "r"})
	}
	return actions
}

// TriggerAction invokes the named action. Both keystrokes (via
// handleKey) and embedder calls (via Program.TriggerAction) reach the
// same dispatcher.
func (m sessionsModel) TriggerAction(id string) (sessionsModel, tea.Cmd) {
	switch id {
	case "toggle-subagents":
		m.hideSubagents = !m.hideSubagents
		m.rebuildList()
		return m, nil

	case "new-session":
		if m.loading || m.err != nil {
			return m, nil
		}
		b := m.backend
		agentID := m.agentID
		agentName := m.agentName
		modelID := m.modelID
		return m, func() tea.Msg {
			key := time.Now().Format("2006-01-02T15:04:05")
			sessionKey, err := b.CreateSession(context.Background(), agentID, key)
			return newSessionCreatedMsg{
				sessionKey: sessionKey,
				agentName:  agentName,
				modelID:    modelID,
				err:        err,
			}
		}
	case "back":
		return m, func() tea.Msg { return goBackFromSessionsMsg{} }
	case "retry":
		if m.err == nil {
			return m, nil
		}
		m.loading = true
		m.err = nil
		return m, m.loadSessions()
	}
	return m, nil
}

func (m sessionsModel) View() string {
	if m.loading {
		return "\n  Loading sessions...\n"
	}
	if m.selecting {
		// Pinned during the transition into chat so the list
		// disappears with the rest of the picker UI; the connection
		// banner stays so the user still sees scope.
		banner := renderConnectionBanner(m.activeConn)
		title := m.selectingTitle
		if title == "" {
			title = "session"
		}
		return banner + fmt.Sprintf("\n  Loading %s...\n", title)
	}
	hints := ""
	if !m.hideHints {
		hints = helpStyle.Render(renderActionHints(m.Actions()))
	}
	banner := renderConnectionBanner(m.activeConn)
	if m.err != nil {
		var b strings.Builder
		b.WriteString(banner)
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", m.err)))
		b.WriteString("\n\n")
		b.WriteString(hints)
		b.WriteString("\n")
		return b.String()
	}
	if len(m.list.Items()) == 0 {
		var b strings.Builder
		b.WriteString(banner)
		b.WriteString("\n")
		b.WriteString(headerStyle.Render(" Sessions "))
		b.WriteString("\n\n")
		b.WriteString("  No sessions found.\n\n")
		b.WriteString(hints)
		b.WriteString("\n")
		return b.String()
	}
	return banner + m.list.View() + "\n" + hints
}

func (m *sessionsModel) setSize(w, h int) {
	m.list.SetSize(w, h-2)
}

// rebuildList reconstructs the display list from allSessions,
// honoring the hideSubagents filter. Group headers are re-derived
// so no orphaned "Subagents" header survives filtering.
func (m *sessionsModel) rebuildList() {
	var listItems []list.Item
	lastGroup := ""
	for _, s := range m.allSessions {
		if m.hideSubagents && strings.Contains(s.key, ":subagent:") {
			continue
		}
		if s.group != lastGroup {
			listItems = append(listItems, sessionGroupHeader{label: s.group})
			lastGroup = s.group
		}
		listItems = append(listItems, s)
	}
	m.list.SetItems(listItems)
	// Skip past the first group header so a session is selected.
	if len(listItems) > 1 {
		m.list.Select(1)
	}
}
