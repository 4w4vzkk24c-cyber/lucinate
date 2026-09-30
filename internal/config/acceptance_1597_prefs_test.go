package config

// Acceptance suite for forge card 1597 (LUCINATE-V1-01), W2-AC4:
// theme.stylePath and theme.mode round-trip through ~/.lucinate/config.json.
//
// RED at base: Preferences has no theme object, so LoadPreferences drops
// the theme key on read and SavePreferences never writes it back — the
// round-trip assertion fails. Post-W2 the fields persist and a relative
// stylePath persists relative (resolution against ~/.lucinate/ is pinned
// behaviourally in internal/tui/acceptance_1597_w2_theme_test.go).
//
// No compile-time references to the new fields: the round-trip is asserted
// on the JSON bytes, so this file compiles at base.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestW2AC4_ThemeFieldsRoundTripThroughConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgDir := filepath.Join(home, ".lucinate")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatalf("mkdir .lucinate: %v", err)
	}
	original := `{"completionBell":true,"historyLimit":50,"connectTimeoutSeconds":15,` +
		`"theme":{"stylePath":"custom.json","mode":"light"}}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(original), 0600); err != nil {
		t.Fatalf("seed config.json: %v", err)
	}

	loaded := LoadPreferences()
	if err := SavePreferences(loaded); err != nil {
		t.Fatalf("SavePreferences: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(cfgDir, "config.json"))
	if err != nil {
		t.Fatalf("read back config.json: %v", err)
	}
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("decode saved config.json: %v", err)
	}
	theme, ok := saved["theme"].(map[string]any)
	if !ok {
		t.Fatalf("W2-AC4: theme object dropped on round-trip (M11): saved=%s", string(data))
	}
	if got, _ := theme["stylePath"].(string); got != "custom.json" {
		t.Errorf("W2-AC4: theme.stylePath round-tripped as %q, want %q (a relative path must persist relative)", got, "custom.json")
	}
	if got, _ := theme["mode"].(string); got != "light" {
		t.Errorf("W2-AC4: theme.mode round-tripped as %q, want %q", got, "light")
	}
}

func TestW2AC4_ThemeFieldsSurviveLoadSaveLoadCycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgDir := filepath.Join(home, ".lucinate")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatalf("mkdir .lucinate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"),
		[]byte(`{"theme":{"stylePath":"themes/paper.json","mode":"dark"}}`), 0600); err != nil {
		t.Fatalf("seed config.json: %v", err)
	}

	// Load → save → load: both hops must preserve the theme object.
	first := LoadPreferences()
	if err := SavePreferences(first); err != nil {
		t.Fatalf("first SavePreferences: %v", err)
	}
	second := LoadPreferences()
	if err := SavePreferences(second); err != nil {
		t.Fatalf("second SavePreferences: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(cfgDir, "config.json"))
	if err != nil {
		t.Fatalf("read back config.json: %v", err)
	}
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("decode saved config.json: %v", err)
	}
	theme, ok := saved["theme"].(map[string]any)
	if !ok {
		t.Fatalf("W2-AC4: theme object lost across load/save/load (M11): saved=%s", string(data))
	}
	if got, _ := theme["stylePath"].(string); got != "themes/paper.json" {
		t.Errorf("W2-AC4: stylePath = %q after double round-trip, want themes/paper.json", got)
	}
	if got, _ := theme["mode"].(string); got != "dark" {
		t.Errorf("W2-AC4: mode = %q after double round-trip, want dark", got)
	}
}
