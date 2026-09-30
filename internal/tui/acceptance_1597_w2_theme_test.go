package tui

// Acceptance suite for forge card 1597 (LUCINATE-V1-01), work item W2:
// loadable Glamour theming + background detection.
//
// Contract (spec 1597.json): RED at base 0d0390a. At base Preferences has no
// theme object, so every pin that reads theme.stylePath / theme.mode sees
// defaults only — the custom-style, warning-banner and palette-override
// assertions fail. The theme fields and the single themed constructor are
// probed behaviourally (config JSON + rendered output + visible
// notifications), never by new symbol name.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// w2RenderMarker is a distinctive string planted in a Glamour style file via
// document.block_prefix — it lands in every render when (and only when) the
// user style file is actually applied (W2-AC1, kills M12).
const w2RenderMarker = "§W2CUSTOM§"

// w2StyleJSON returns a valid Glamour style document carrying the marker.
func w2StyleJSON() string {
	return `{"document":{"block_prefix":"` + w2RenderMarker + `"}}`
}

// w2PrefsFromJSON decodes a config.json payload into Preferences so the
// theme object can be supplied without compile-time references to fields
// that do not exist at base.
func w2PrefsFromJSON(t *testing.T, raw string) config.Preferences {
	t.Helper()
	var p config.Preferences
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("decode preferences fixture: %v", err)
	}
	return p
}

// w2NewThemedChat builds a chatModel with the given preferences and drives
// BOTH renderer construction sites (newChatModel and setSize).
func w2NewThemedChat(t *testing.T, prefs config.Preferences) chatModel {
	t.Helper()
	fb := &fakeBackend{}
	m := newChatModel(fb, "sess-1", "agent-1", "Scout", "model-1", prefs, false, "", "", false)
	m.viewport = viewport.New()
	m.setSize(120, 40)
	return m
}

// W2-AC1 / M11 + M12: a custom Glamour JSON style changes markdown
// rendering behind the single themed constructor. At base the style file is
// ignored, so the marker never appears — red.
func TestW2AC1_CustomStyleFileChangesRendering(t *testing.T) {
	dir := t.TempDir()
	stylePath := filepath.Join(dir, "custom.json")
	if err := os.WriteFile(stylePath, []byte(w2StyleJSON()), 0600); err != nil {
		t.Fatalf("write style fixture: %v", err)
	}
	prefs := w2PrefsFromJSON(t, `{"theme":{"stylePath":"`+stylePath+`","mode":"auto"}}`)

	m := w2NewThemedChat(t, prefs)
	out, err := m.renderer.Render("# Heading")
	if err != nil {
		t.Fatalf("render with custom style: %v", err)
	}
	if !strings.Contains(out, w2RenderMarker) {
		t.Errorf("W2-AC1: rendered markdown ignores the custom style file (marker %q absent) — second hardcoded palette or unloadable style, bans B5/M12", w2RenderMarker)
	}

	def := w2NewThemedChat(t, config.DefaultPreferences())
	defOut, _ := def.renderer.Render("# Heading")
	if out == defOut {
		t.Errorf("W2-AC1: custom-style render is byte-identical to default dark render — style file not applied (M11/M12)")
	}
}

// W2-AC1 (relative path resolution) + W2-AC4 (resolve on load): a relative
// theme.stylePath resolves against ~/.lucinate/. Red at base — the config
// loader drops the theme object, so nothing resolves or renders.
func TestW2AC1_RelativeStylePathResolvesAgainstLucinateDir(t *testing.T) {
	home := t.TempDir()
	lucDir := filepath.Join(home, ".lucinate")
	if err := os.MkdirAll(lucDir, 0700); err != nil {
		t.Fatalf("mkdir .lucinate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lucDir, "style.json"), []byte(w2StyleJSON()), 0600); err != nil {
		t.Fatalf("write style fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lucDir, "config.json"), []byte(`{"theme":{"stylePath":"style.json","mode":"auto"}}`), 0600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	t.Setenv("HOME", home)

	prefs := config.LoadPreferences()
	m := w2NewThemedChat(t, prefs)
	out, err := m.renderer.Render("# Heading")
	if err != nil {
		t.Fatalf("render with relative stylePath: %v", err)
	}
	if !strings.Contains(out, w2RenderMarker) {
		t.Errorf("W2-AC1/AC4: relative stylePath style.json was not resolved against ~/.lucinate (marker absent from render)")
	}
}

// W2-AC2 / M8 + M9: a missing, corrupt, or key-drifted style file yields a
// visible warning banner AND explicit fallback to dark — never a crash,
// never silence. The unknown-key case must fail the strict pre-decode
// (stock unmarshal silently drops unknown keys). At base there is no theme
// handling at all: no warnings surface — red.
func TestW2AC2_StyleFileFailureModesWarnAndFallBackToDark(t *testing.T) {
	dir := t.TempDir()
	def := w2NewThemedChat(t, config.DefaultPreferences())
	defOut, _ := def.renderer.Render("# Heading")

	cases := []struct {
		name      string
		stylePath string
	}{
		{"missing file", filepath.Join(dir, "nope.json")},
		{"corrupt json", writeStyleFixture(t, dir, "corrupt.json", "{not json")},
		// An unknown key inside otherwise-valid JSON — the strict
		// pre-decode must catch what the stock unmarshal silently drops.
		{"unknown key", writeStyleFixture(t, dir, "drift.json", `{"document":{"block_prefix":"x"},"totallyUnknownKey":1}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prefs := w2PrefsFromJSON(t, `{"theme":{"stylePath":"`+tc.stylePath+`","mode":"auto"}}`)
			m := w2NewThemedChat(t, prefs)

			out, err := m.renderer.Render("# Heading")
			if err != nil {
				t.Fatalf("W2-AC2: render after style failure crashed instead of falling back: %v", err)
			}
			if out != defOut {
				t.Errorf("W2-AC2: %s did not fall back to dark rendering", tc.name)
			}
			if len(m.notifications) == 0 {
				t.Errorf("W2-AC2: %s fell back to dark with no visible warning banner (silent fallback, ban B6/M9)", tc.name)
			}
		})
	}
}

// W2-AC3 / M10: theme.mode is honoured — explicit light and explicit dark
// produce measurably different palettes, and the no-theme default stays on
// the dark values. At base mode is unread, so light == dark == default —
// red. Detection itself (mode=auto + a light terminal) is builder-staged
// runtime evidence; this pin kills the hardwired-dark mutant M10.
func TestW2AC3_ThemeModeSelectsPalette(t *testing.T) {
	light := w2NewThemedChat(t, w2PrefsFromJSON(t, `{"theme":{"mode":"light"}}`))
	dark := w2NewThemedChat(t, w2PrefsFromJSON(t, `{"theme":{"mode":"dark"}}`))
	def := w2NewThemedChat(t, config.DefaultPreferences())

	lightOut := renderChatFrame(light)
	darkOut := renderChatFrame(dark)
	defOut := renderChatFrame(def)

	if lightOut == darkOut {
		t.Errorf("W2-AC3: mode=light and mode=dark render identically (palette hardwired dark, M10)")
	}
	if defOut != darkOut {
		t.Errorf("W2-AC3: defaults changed — no-theme render differs from explicit dark (defaults must stay on dark values)")
	}
}

// renderChatFrame renders one assistant message through the chat view so
// palette differences reach observable output.
func renderChatFrame(m chatModel) string {
	m.appendMessage(chatMessage{role: "assistant", content: "palette probe"})
	m.updateViewport()
	return m.View()
}

// writeStyleFixture writes a style fixture and returns its path.
func writeStyleFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}
