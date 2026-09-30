package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lucinate-ai/lucinate/internal/config"
)

type configItemKind int

const (
	configItemBool configItemKind = iota
	configItemInt
	// configItemAction is a row that, when activated (enter), navigates
	// to a sub-screen rather than holding a value of its own.
	configItemAction
	// configItemEnum is a row that cycles through a fixed choice list
	// (←/→ or space) and persists the selected value.
	configItemEnum
	// configItemText is a row holding a free-form string (enter to edit,
	// type, enter to commit, esc to cancel).
	configItemText
)

type configItem struct {
	label   string
	key     string
	kind    configItemKind
	checked bool // for bool items
	value   int  // for int items
	min     int
	max     int
	step    int
	choices []string // for enum items
	index   int      // for enum items
	text    string   // for text items
}

type configModel struct {
	items     []configItem
	cursor    int
	prefs     config.Preferences
	width     int
	height    int
	hideHints bool
	// editingText is true while the focused configItemText row is
	// capturing keystrokes; editKey names the row being edited.
	editingText bool
	editingKey  string
}

func newConfigModel(prefs config.Preferences, hideHints bool) configModel {
	mode := prefs.ThemeSettings().Mode
	if mode == "" {
		mode = config.ThemeModeAuto
	}
	modeIndex := 0
	for i, c := range []string{config.ThemeModeAuto, config.ThemeModeDark, config.ThemeModeLight} {
		if c == mode {
			modeIndex = i
		}
	}
	return configModel{
		prefs:     prefs,
		hideHints: hideHints,
		items: []configItem{
			{label: "Completion notification (terminal bell)", key: "completionBell", kind: configItemBool, checked: prefs.CompletionBell},
			{label: "Check for updates on startup", key: "checkForUpdates", kind: configItemBool, checked: prefs.UpdateChecksEnabled()},
			{label: "History limit (messages loaded per session)", key: "historyLimit", kind: configItemInt, value: prefs.HistoryLimit, min: 10, max: 500, step: 10},
			{label: "Connect timeout (seconds, increase for slow backends)", key: "connectTimeoutSeconds", kind: configItemInt, value: prefs.ConnectTimeoutSeconds, min: 5, max: 300, step: 5},
			{label: "Theme mode (terminal palette selection)", key: "themeMode", kind: configItemEnum, choices: []string{config.ThemeModeAuto, config.ThemeModeDark, config.ThemeModeLight}, index: modeIndex},
			{label: "Theme style file (Glamour JSON, relative to ~/.lucinate)", key: "themeStylePath", kind: configItemText, text: prefs.ThemeSettings().StylePath},
			{label: "Ask command defaults…", key: "askDefaults", kind: configItemAction},
		},
	}
}

func (m configModel) Init() tea.Cmd { return nil }

func (m configModel) Update(msg tea.Msg) (configModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		// While a text row is being edited, every keystroke feeds the
		// editor: enter commits, esc cancels, backspace trims, runes
		// append. Nothing else in the view sees keys until editing ends.
		if m.editingText {
			item := m.editingItem()
			if item == nil {
				m.editingText = false
				return m, nil
			}
			switch msg.String() {
			case "enter":
				m.editingText = false
				m.applyToPrefs()
				prefs := m.prefs
				return m, func() tea.Msg {
					_ = config.SavePreferences(prefs)
					return prefsUpdatedMsg{prefs: prefs}
				}
			case "esc":
				m.editingText = false
				// Restore the persisted value — the edit is discarded.
				if p, ok := m.prefsEditBackup(); ok {
					item.text = p
				}
				return m, nil
			case "backspace":
				if len(item.text) > 0 {
					r := []rune(item.text)
					item.text = string(r[:len(r)-1])
				}
				return m, nil
			default:
				if msg.Text != "" {
					item.text += msg.Text
				}
				return m, nil
			}
		}
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "left", "h":
			item := &m.items[m.cursor]
			switch item.kind {
			case configItemInt:
				if item.value-item.step >= item.min {
					item.value -= item.step
					m.applyToPrefs()
					prefs := m.prefs
					return m, func() tea.Msg {
						_ = config.SavePreferences(prefs)
						return prefsUpdatedMsg{prefs: prefs}
					}
				}
			case configItemEnum:
				if item.index > 0 {
					item.index--
				}
				m.applyToPrefs()
				prefs := m.prefs
				return m, func() tea.Msg {
					_ = config.SavePreferences(prefs)
					return prefsUpdatedMsg{prefs: prefs}
				}
			}
		case "right", "l":
			item := &m.items[m.cursor]
			switch item.kind {
			case configItemInt:
				if item.value+item.step <= item.max {
					item.value += item.step
					m.applyToPrefs()
					prefs := m.prefs
					return m, func() tea.Msg {
						_ = config.SavePreferences(prefs)
						return prefsUpdatedMsg{prefs: prefs}
					}
				}
			case configItemEnum:
				if item.index < len(item.choices)-1 {
					item.index++
				}
				m.applyToPrefs()
				prefs := m.prefs
				return m, func() tea.Msg {
					_ = config.SavePreferences(prefs)
					return prefsUpdatedMsg{prefs: prefs}
				}
			}
		default:
			// Discoverable shortcuts route through TriggerAction so the
			// help line and the keystroke share a single source of truth.
			for _, a := range m.Actions() {
				if a.Key == msg.String() {
					return m.TriggerAction(a.ID)
				}
			}
		}
	}
	return m, nil
}

// Actions returns the discoverable, view-level commands the config
// view exposes. `toggle` is only present when the focused row is a bool
// item — flipping a checkbox is the only configurable operation that
// translates cleanly into a single named button on embedders with a
// native action surface. The ←/→ adjust controls remain inline (per-row
// form controls, not screen commands), so they don't appear here.
func (m configModel) Actions() []Action {
	actions := make([]Action, 0, 2)
	if m.editingText {
		// While a text row is capturing keystrokes the editor owns
		// enter/esc; no other action is offered.
		return actions
	}
	if m.cursor >= 0 && m.cursor < len(m.items) {
		switch m.items[m.cursor].kind {
		case configItemBool:
			actions = append(actions, Action{ID: "toggle", Label: "Toggle", Key: "space"})
		case configItemEnum:
			actions = append(actions, Action{ID: "cycle", Label: "Next", Key: "space"})
		case configItemText:
			actions = append(actions, Action{ID: "edit", Label: "Edit", Key: "enter"})
		case configItemAction:
			actions = append(actions, Action{ID: "open-ask", Label: "Configure", Key: "enter"})
		}
	}
	actions = append(actions, Action{ID: "back", Label: "Back", Key: "esc"})
	return actions
}

// TriggerAction invokes the named action.
func (m configModel) TriggerAction(id string) (configModel, tea.Cmd) {
	switch id {
	case "toggle":
		if m.cursor < 0 || m.cursor >= len(m.items) {
			return m, nil
		}
		item := &m.items[m.cursor]
		if item.kind != configItemBool {
			return m, nil
		}
		item.checked = !item.checked
		m.applyToPrefs()
		prefs := m.prefs
		return m, func() tea.Msg {
			_ = config.SavePreferences(prefs)
			return prefsUpdatedMsg{prefs: prefs}
		}
	case "cycle":
		if m.cursor < 0 || m.cursor >= len(m.items) {
			return m, nil
		}
		item := &m.items[m.cursor]
		if item.kind != configItemEnum || len(item.choices) == 0 {
			return m, nil
		}
		item.index = (item.index + 1) % len(item.choices)
		m.applyToPrefs()
		prefs := m.prefs
		return m, func() tea.Msg {
			_ = config.SavePreferences(prefs)
			return prefsUpdatedMsg{prefs: prefs}
		}
	case "edit":
		if m.cursor < 0 || m.cursor >= len(m.items) {
			return m, nil
		}
		item := &m.items[m.cursor]
		if item.kind != configItemText {
			return m, nil
		}
		m.editingText = true
		m.editingKey = item.key
		return m, nil
	case "open-ask":
		return m, func() tea.Msg { return showAskConfigMsg{} }
	case "back":
		return m, func() tea.Msg { return goBackFromConfigMsg{} }
	}
	return m, nil
}

func (m *configModel) applyToPrefs() {
	for _, item := range m.items {
		switch item.key {
		case "completionBell":
			m.prefs.CompletionBell = item.checked
		case "checkForUpdates":
			v := item.checked
			m.prefs.CheckForUpdates = &v
		case "historyLimit":
			m.prefs.HistoryLimit = item.value
		case "connectTimeoutSeconds":
			m.prefs.ConnectTimeoutSeconds = item.value
		case "themeMode":
			if len(item.choices) > 0 {
				idx := item.index
				if idx < 0 || idx >= len(item.choices) {
					idx = 0
				}
				theme := m.prefs.ThemeSettings()
				theme.Mode = item.choices[idx]
				m.prefs.Theme = &theme
			}
		case "themeStylePath":
			theme := m.prefs.ThemeSettings()
			theme.StylePath = strings.TrimSpace(item.text)
			m.prefs.Theme = &theme
		}
	}
}

// editingItem returns the configItem currently capturing text, or nil.
func (m *configModel) editingItem() *configItem {
	for i := range m.items {
		if m.items[i].key == m.editingKey {
			return &m.items[i]
		}
	}
	return nil
}

// prefsEditBackup returns the persisted string for the row being edited
// so an esc-cancel can restore it.
func (m *configModel) prefsEditBackup() (string, bool) {
	switch m.editingKey {
	case "themeStylePath":
		return m.prefs.ThemeSettings().StylePath, true
	}
	return "", false
}

func (m configModel) View() string {
	var b strings.Builder

	header := headerStyle.Render(" Settings ")
	b.WriteString(header)
	b.WriteString("\n\n")

	for i, item := range m.items {
		var line string
		switch item.kind {
		case configItemBool:
			check := "[ ]"
			if item.checked {
				check = "[x]"
			}
			line = fmt.Sprintf("  %s %s", check, item.label)
		case configItemInt:
			line = fmt.Sprintf("  ◀ %d ▶  %s", item.value, item.label)
		case configItemEnum:
			value := ""
			if len(item.choices) > 0 {
				idx := item.index
				if idx < 0 || idx >= len(item.choices) {
					idx = 0
				}
				value = item.choices[idx]
			}
			line = fmt.Sprintf("  ◀ %s ▶  %s", value, item.label)
		case configItemText:
			if m.editingText && i == m.cursor {
				line = fmt.Sprintf("  ▸ %s▮  %s", item.text, item.label)
			} else if item.text != "" {
				line = fmt.Sprintf("  › %s  %s", item.text, item.label)
			} else {
				line = fmt.Sprintf("  › (unset)  %s", item.label)
			}
		case configItemAction:
			line = fmt.Sprintf("  › %s", item.label)
		}
		if i == m.cursor {
			line = lipgloss.NewStyle().Foreground(accent).Bold(true).Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	if !m.hideHints {
		b.WriteString("\n")
		// `toggle` and `back` come out of Actions(); ←/→ adjust stays
		// hand-rendered as a per-row form control (it operates on
		// whichever int item the cursor is on, not on the screen as a
		// whole).
		hint := renderActionHints(m.Actions())
		if hint == "" {
			hint = "  ←/→: adjust"
		} else {
			hint += " · ←/→: adjust"
		}
		b.WriteString(helpStyle.Render(hint))
	}

	return b.String()
}

func (m *configModel) setSize(w, h int) {
	m.width = w
	m.height = h
}
