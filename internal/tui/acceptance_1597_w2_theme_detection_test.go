package tui

import (
	"testing"
)

// Parent-added pin (VERIFY, 2026-09-30, card 1597): mutant M10 — the
// auto-mode palette must follow the detected terminal background.
// TestW2AC3 cannot observe detection polarity in a tty-less test
// environment (HasDarkBackground returns the dark default), so this
// test injects the detected value directly.
//
// Injection mechanics (learned the hard way — v1 of this pin was red
// at base): paletteForMode("auto") calls detectDarkBackground() at
// BOTH construction (chat.go newChatModel) and render (View ->
// effectivePalette) time, and detectBackgroundOnce.Do re-runs the
// REAL query if the once is unconsumed, overwriting any preset
// detectedDarkBg. So: consume the once with a no-op (the real query
// never runs), and set the var immediately before each construction
// AND each render.
func TestW2AC3B_AutoModeFollowsDetectedBackground(t *testing.T) {
	setBg := func(dark bool) {
		detectBackgroundOnce.Do(func() {}) // consume: real detection never (re)runs
		detectedDarkBg = dark
	}
	newAutoChat := func() chatModel {
		return w2NewThemedChat(t, w2PrefsFromJSON(t, `{"theme":{"mode":"auto"}}`))
	}

	setBg(false)
	lightChat := newAutoChat()
	setBg(false)
	lightOut := renderChatFrame(lightChat)

	setBg(true)
	darkChat := newAutoChat()
	setBg(true)
	darkOut := renderChatFrame(darkChat)

	if lightOut == darkOut {
		t.Errorf("W2-AC3b: auto mode ignored detected background — light-detected and dark-detected render identically (M10 unpinned)")
	}
}
