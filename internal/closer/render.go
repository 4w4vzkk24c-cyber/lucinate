package closer

import (
	"encoding/json"
	"fmt"
	"strings"

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
		lines = append(lines, fmt.Sprintf("#%s %s · %s · %s", it.ID, it.Title, it.Rung, it.LastMile))
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
