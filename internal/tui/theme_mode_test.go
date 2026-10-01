package tui

import (
	"strings"
	"testing"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// The user-facing path: a light theme mode must produce a renderer whose
// inline code uses the Base2 light background. This test references only
// symbols that exist at the pre-fix base (newThemedRenderer, config.Preferences),
// so it can be run against the base to prove it is red there — the older
// newThemedRenderer ignored Theme.Mode entirely and always returned the dark
// style, so this assertion failed by construction.
func TestNewThemedRendererFollowsLightMode(t *testing.T) {
	mode := "light"
	prefs := config.Preferences{Theme: &config.ThemePreferences{Mode: mode}}
	r, warn := newThemedRenderer(prefs, 80)
	if warn != "" {
		t.Fatalf("unexpected warning for mode %q: %s", mode, warn)
	}
	out, err := r.Render("use `foo` here")
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if strings.Contains(out, "48;2;7;54;66") {
		t.Errorf("mode=light still renders the Base02 dark code slab: %q", out)
	}
	if !strings.Contains(out, "48;2;238;232;213") {
		t.Errorf("mode=light did not select the Base2 light code background: %q", out)
	}
}

// mode=dark keeps the dark slab through the same path.
func TestNewThemedRendererFollowsDarkMode(t *testing.T) {
	prefs := config.Preferences{Theme: &config.ThemePreferences{Mode: "dark"}}
	r, warn := newThemedRenderer(prefs, 80)
	if warn != "" {
		t.Fatalf("unexpected warning: %s", warn)
	}
	out, err := r.Render("use `foo` here")
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if !strings.Contains(out, "48;2;7;54;66") {
		t.Errorf("mode=dark lost its Base02 code background: %q", out)
	}
}

// An unset mode is the pre-W2 default: dark, unchanged.
func TestNewThemedRendererUnsetModeStaysDark(t *testing.T) {
	r, warn := newThemedRenderer(config.Preferences{}, 80)
	if warn != "" {
		t.Fatalf("unexpected warning: %s", warn)
	}
	out, err := r.Render("use `foo` here")
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if !strings.Contains(out, "48;2;7;54;66") {
		t.Errorf("unset mode did not keep the dark default: %q", out)
	}
}
