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
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"

	"github.com/lucinate-ai/lucinate/internal/config"
)

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
		return darkRenderer(wrapWidth), ""
	}
	raw, err := os.ReadFile(stylePath)
	if err != nil {
		return darkRenderer(wrapWidth), fmt.Sprintf("theme: cannot read style file %s (%v) — falling back to dark", stylePath, err)
	}
	if err := strictDecodeStyle(raw); err != nil {
		return darkRenderer(wrapWidth), fmt.Sprintf("theme: style file %s rejected (%v) — falling back to dark", stylePath, err)
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStylesFromJSONBytes(raw),
		glamour.WithWordWrap(wrapWidth),
	)
	if err != nil {
		return darkRenderer(wrapWidth), fmt.Sprintf("theme: style file %s could not be applied (%v) — falling back to dark", stylePath, err)
	}
	return renderer, ""
}

// darkRenderer is the explicit fallback (and the default when no style
// file is configured): the pre-W2 dark standard style.
func darkRenderer(wrapWidth int) *glamour.TermRenderer {
	renderer, _ := glamour.NewTermRenderer(
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
