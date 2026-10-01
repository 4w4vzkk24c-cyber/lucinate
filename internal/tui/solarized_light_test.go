package tui

import (
	"regexp"
	"strings"
	"testing"
)

// Inline code carries the background that reads as a "code slab". In a light
// terminal it must be Base2 #eee8d5 (48;2;238;232;213), never the dark
// style's Base02 #073642 (48;2;7;54;66) — the reported defect: near-black
// code backgrounds on a cream page.
func TestLightInlineCodeUsesLightBackground(t *testing.T) {
	out, err := lightRenderer(80).Render("use `foo` here")
	if err != nil {
		t.Fatalf("light render error: %v", err)
	}
	if strings.Contains(out, "48;2;7;54;66") {
		t.Errorf("light inline code still paints the Base02 dark slab: %q", out)
	}
	if !strings.Contains(out, "48;2;238;232;213") {
		t.Errorf("light inline code missing the Base2 background: %q", out)
	}
}

// The dark style must keep its Base02 slab — this is the mirror of the test
// above, so a future edit that lightens both is caught.
func TestDarkInlineCodeKeepsDarkBackground(t *testing.T) {
	out, err := darkRenderer(80).Render("use `foo` here")
	if err != nil {
		t.Fatalf("dark render error: %v", err)
	}
	if !strings.Contains(out, "48;2;7;54;66") {
		t.Errorf("dark inline code lost its Base02 background: %q", out)
	}
}

// Fenced blocks carry no background escape in either mode (glamour paints
// only inline code), so the two modes must still differ in their text color.
func TestFencedBlocksDifferBetweenLightAndDark(t *testing.T) {
	src := "```go\nfunc main() {}\n```"
	dark, err := darkRenderer(80).Render(src)
	if err != nil {
		t.Fatalf("dark render error: %v", err)
	}
	light, err := lightRenderer(80).Render(src)
	if err != nil {
		t.Fatalf("light render error: %v", err)
	}
	if dark == light {
		t.Error("fenced block rendering is identical in both modes — mode is not selecting a style")
	}
	// No background slab on fenced blocks; guard the invariant explicitly so a
	// future glamour change that starts emitting one is noticed here.
	bg := regexp.MustCompile(`48;2;\d+;\d+;\d+`)
	if got := bg.FindAllString(light, -1); len(got) != 0 {
		t.Errorf("light fenced block unexpectedly emits backgrounds: %v", got)
	}
}

// paletteIsLight must mirror paletteForMode: explicit modes are absolute,
// auto/unset defers to detection.
func TestPaletteIsLightMatchesPaletteForMode(t *testing.T) {
	for _, mode := range []string{"dark", "light", "auto", ""} {
		wantLight := paletteForMode(mode) == lightPalette
		if got := paletteIsLight(mode); got != wantLight {
			t.Errorf("paletteIsLight(%q) = %v, paletteForMode says light = %v", mode, got, wantLight)
		}
	}
}
