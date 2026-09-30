package tui

import (
	"strings"
	"testing"
)

// Verify the Solarized Dark glamour style renders code blocks without
// panicking — this is the exact code path that crashed with ANSI indices.
func TestSolarizedDarkRendersCodeBlocks(t *testing.T) {
	r := darkRenderer(80)
	out, err := r.Render("```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```")
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if !strings.Contains(out, "func") {
		t.Fatalf("expected code content in output, got: %q", out[:100])
	}
}
