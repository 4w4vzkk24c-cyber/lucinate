package tui

import (
	"sync"
	"testing"
)

// Parent-added pin (VERIFY, 2026-09-30, card 1597): mutant M10 — the
// auto-mode palette must follow the detected terminal background.
// TestW2AC3 cannot observe detection polarity in a tty-less test
// environment (HasDarkBackground returns the dark default), so this
// test injects the detected value directly.
func TestW2AC3B_AutoModeFollowsDetectedBackground(t *testing.T) {
	mkChat := func(dark bool) chatModel {
		detectBackgroundOnce = sync.Once{}
		detectedDarkBg = dark
		return w2NewThemedChat(t, w2PrefsFromJSON(t, `{"theme":{"mode":"auto"}}`))
	}
	lightBgChat := mkChat(false)
	darkBgChat := mkChat(true)

	lightOut := renderChatFrame(lightBgChat)
	darkOut := renderChatFrame(darkBgChat)

	if lightOut == darkOut {
		t.Errorf("W2-AC3b: auto mode ignored detected background — light-detected and dark-detected render identically (M10 unpinned)")
	}
}
