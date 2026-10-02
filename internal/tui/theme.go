package tui

// theme.go — W2 loadable Glamour theming. One themed constructor serves
// BOTH renderer construction sites (newChatModel and chatModel.setSize);
// neither site hardcodes a Glamour palette any more. The style bytes are
// strict-decoded before Glamour sees them because the stock
// WithStylesFromJSONBytes unmarshal silently drops unknown/mistyped keys
// to defaults — schema drift must warn as loudly as corrupt JSON. Any
// failure yields a visible warning plus an explicit dark fallback: never
// a crash, never silence (W2-AC2, bans B5/B6).

import (
	_ "embed"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// solarizedDarkStyle is the embedded Solarized Dark glamour JSON —
// ANSI 256-color indices mapped to the canonical Solarized hues:
//   Cyan #2aa198→37, Blue #268bd2→32, Yellow #b58900→136,
//   Orange #cb4b16→166, Green #859900→64, Red #dc322f→160,
//   Base00 #657b83→246, Base01 #586e75→240, Base02 #073642→236,
//   Base03 #002b36→234, Magenta #d33682→133
//
//go:embed solarized_dark.json
var solarizedDarkStyle string

// solarizedLightStyle is the embedded Solarized Light glamour JSON — the
// same element mapping as the dark style, inverted: Base3 document text
// #657b83 on Base2 code surfaces #eee8d5. Without this the light terminal
// inherited the dark style's Base02 #073642 code backgrounds, which read
// as near-black blocks on a cream page.
//go:embed solarized_light.json
var solarizedLightStyle string

// themedMarkdownRenderer builds a renderer for one stamp. It is the factory
// chat commands use: each command calls it inside its own goroutine and
// never lets the result leave, because a glamour TermRenderer is not safe
// to share between goroutines. A theme warning is not surfaced here;
// chatModel.setSize reports it when it builds the UI goroutine's renderer.
func themedMarkdownRenderer(s renderStamp) markdownRenderer {
	theme := s.theme
	renderer, _ := newThemedRenderer(config.Preferences{Theme: &theme}, s.width)
	if renderer == nil {
		// A nil *TermRenderer in the interface would pass a nil check.
		return nil
	}
	return renderer
}

// newThemedRenderer builds the markdown renderer from the configured
// theme at the given wrap width. A non-empty second return is a warning
// the caller must surface (chatModel appends it to notifications) — the
// renderer returned alongside it is always usable (dark fallback).
func newThemedRenderer(prefs config.Preferences, wrapWidth int) (*glamour.TermRenderer, string) {
	theme := prefs.ThemeSettings()
	stylePath := theme.StylePath
	if stylePath != "" && !filepath.IsAbs(stylePath) {
		// Relative style paths resolve against the lucinate data dir
		// (~/.lucinate); the stored value itself stays relative.
		if dir, err := config.DataDir(); err == nil {
			stylePath = filepath.Join(dir, stylePath)
		}
	}
	if stylePath == "" {
		// No user style file: the embedded palette follows theme.mode, the
		// same axis paletteForMode uses for the TUI chrome, so the transcript
		// and the chrome agree in light terminals (and in auto/light).
		if paletteIsLight(theme.Mode) {
			return lightRenderer(wrapWidth), ""
		}
		return darkRenderer(wrapWidth), ""
	}
	raw, err := os.ReadFile(stylePath)
	if err != nil {
		return fallbackRenderer(theme.Mode, wrapWidth), fmt.Sprintf("theme: cannot read style file %s (%v) — falling back to the mode's palette", stylePath, err)
	}
	if err := strictDecodeStyle(raw); err != nil {
		return fallbackRenderer(theme.Mode, wrapWidth), fmt.Sprintf("theme: style file %s rejected (%v) — falling back to the mode's palette", stylePath, err)
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStylesFromJSONBytes(raw),
		glamour.WithWordWrap(wrapWidth),
	)
	if err != nil {
		return fallbackRenderer(theme.Mode, wrapWidth), fmt.Sprintf("theme: style file %s could not be applied (%v) — falling back to the mode's palette", stylePath, err)
	}
	return renderer, ""
}

// paletteIsLight reports whether a theme.mode value resolves to the light
// palette. It mirrors paletteForMode's resolution (including the once-per-
// process terminal detection for auto/unset) without building a palette.
func paletteIsLight(mode string) bool {
	switch mode {
	case "dark":
		return false
	case "light":
		return true
	default: // "auto" and ""
		return !detectDarkBackground()
	}
}

// fallbackRenderer is the mode-appropriate embedded style, used whenever a
// configured style file is unreadable, rejected, or unappliable.
func fallbackRenderer(mode string, wrapWidth int) *glamour.TermRenderer {
	if paletteIsLight(mode) {
		return lightRenderer(wrapWidth)
	}
	return darkRenderer(wrapWidth)
}

// lightRenderer is the embedded Solarized Light style: cream code surfaces
// instead of the dark style's near-black ones.
func lightRenderer(wrapWidth int) *glamour.TermRenderer {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStylesFromJSONBytes([]byte(solarizedLightStyle)),
		glamour.WithWordWrap(wrapWidth),
	)
	if err == nil {
		return renderer
	}
	// Embedded JSON should never fail; degrade to the stock preset.
	renderer, _ = glamour.NewTermRenderer(
		glamour.WithStandardStyle("light"),
		glamour.WithWordWrap(wrapWidth),
	)
	return renderer
}

// darkRenderer is the explicit fallback (and the default when no style
// file is configured): the embedded Solarized Dark palette, replacing
// Glamour's stock "dark" preset so markdown rendering matches the TUI.
func darkRenderer(wrapWidth int) *glamour.TermRenderer {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStylesFromJSONBytes([]byte(solarizedDarkStyle)),
		glamour.WithWordWrap(wrapWidth),
	)
	if err == nil {
		return renderer
	}
	// Embedded JSON should never fail; if it somehow does, degrade to the
	// stock dark preset rather than crashing.
	renderer, _ = glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(wrapWidth),
	)
	return renderer
}

// strictDecodeStyle decodes Glamour style JSON with unknown-field
// rejection. Glamour's own unmarshal (glamour.go WithStylesFromJSONBytes)
// is a plain json.Unmarshal into ansi.StyleConfig, so a key outside the
// v2.0.1 schema — or a mistyped value — silently drops to defaults
// instead of erroring. Decoding the same target strictly first turns
// that silent drift into a loud, actionable failure.
func strictDecodeStyle(raw []byte) error {
	var cfg ansi.StyleConfig
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(&cfg)
}
