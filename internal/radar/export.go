package radar

// export.go re-exports, as thin delegating wrappers, the three unexported
// radar helpers guard and closer need. Radar behaviour is frozen: every
// function here is a single call to the helper it wraps — no logic, no state,
// no other export (card 1607, spec row "radar reuse, wrappers only").

// ResolveWidth wraps resolveWidth (render.go): the effective render width
// bound, min(termWidth, 100), resolved per call so the COLUMNS seam stays live.
func ResolveWidth() int { return resolveWidth() }

// ClipLine wraps clipLine (render.go): ANSI-aware hard clip to width visible
// columns.
func ClipLine(line string, width int) string { return clipLine(line, width) }

// Solarized returns the canonical ten Solarized palette hex values in
// declaration order (render.go consts).
func Solarized() []string {
	return []string{
		solarCyan,
		solarYellow,
		solarOrange,
		solarGreen,
		solarBase0,
		solarBase01,
		solarBase02,
		solarBase03,
		solarBase2,
		solarBase3,
	}
}
