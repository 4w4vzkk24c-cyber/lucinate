package closer

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/lucinate-ai/lucinate/internal/radar"
)

// rowCap is the display cap: at most this many item rows, then one overflow
// line (the literal radar landed in 1606; re-pinned here rather than wrapped).
const rowCap = 5

// Render renders the ladder: at most rowCap item rows, each carrying its id,
// title, rung, and exactly one last-mile action, then a single overflow line
// "+N more (of M)" with N = M-rowCap when more items exist. Plain text (no
// SGR), clipped to width visible columns.
func Render(items []Item, width int) string {
	lines := []string{"CLOSER — last mile"}

	shown := len(items)
	if shown > rowCap {
		shown = rowCap
	}
	for i := 0; i < shown; i++ {
		it := items[i]
		lines = append(lines, itemLine(it, width))
	}
	if len(items) > rowCap {
		lines = append(lines, fmt.Sprintf("+%d more (of %d)", len(items)-rowCap, len(items)))
	}

	if width > 0 {
		for i, ln := range lines {
			lines[i] = radar.ClipLine(ln, width)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// itemLine composes one ladder row. The last-mile action is the line's
// payload, so it is never the tail a whole-line clip can cut: the action's
// visible width is reserved and only the title (the middle field) is clipped
// to the remaining budget. At a wide enough width the composed line is exactly
// the canonical "#id title · rung · action", so wide output is byte-identical.
func itemLine(it Item, width int) string {
	prefix := fmt.Sprintf("#%s %s · %s", it.ID, it.Title, it.Rung)
	// The separator travels with the action so a clipped prefix can never fuse
	// into the action text ("…title imerge") and swallow its word boundary.
	suffix := " · " + it.LastMile
	if width <= 0 {
		return prefix + suffix
	}
	budget := width - visibleWidth(suffix)
	if budget < 0 {
		budget = 0
	}
	return radar.ClipLine(prefix, budget) + suffix
}

// visibleWidth counts the terminal cells a plain (SGR-free) string occupies:
// wide East Asian runes are two cells, everything else one.
func visibleWidth(s string) int {
	w := 0
	for _, r := range s {
		if r >= utf8.RuneSelf && isWideRune(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

// isWideRune reports whether r occupies two terminal cells.
func isWideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK radicals, Kangxi
		r >= 0x3041 && r <= 0x33FF, // kana, CJK symbols
		r >= 0x3400 && r <= 0x4DBF, // CJK Ext A
		r >= 0x4E00 && r <= 0x9FFF, // CJK Unified
		r >= 0xA000 && r <= 0xA4CF, // Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility
		r >= 0xFE30 && r <= 0xFE6F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1FAFF, // emoji
		r >= 0x20000 && r <= 0x3FFFD: // CJK Ext B+
		return true
	}
	return false
}

// jsonItem is one --json ladder row.
type jsonItem struct {
	ID       string `json:"ID"`
	Title    string `json:"Title"`
	Rung     string `json:"Rung"`
	LastMile string `json:"LastMile"`
}

// RenderJSON marshals the UNCAPPED item list as a single JSON document (the
// render cap never mutates the classified data).
func RenderJSON(items []Item) ([]byte, error) {
	out := make([]jsonItem, 0, len(items))
	for _, it := range items {
		out = append(out, jsonItem{ID: it.ID, Title: it.Title, Rung: string(it.Rung), LastMile: it.LastMile})
	}
	return json.Marshal(map[string]any{"Items": out})
}
