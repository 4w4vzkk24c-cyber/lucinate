// Package guard_test is the RED acceptance suite for card 1607 (EF suite v1,
// part 2): the guard thread-divergence tripwire over
// ~/.openclaw/state/threads.json.
//
// Contract: quorum-test-generator writes tests only. At base 16b2990 neither
// internal/guard nor cmd/guard exists, so this suite is red by design until
// quorum-builder lands state.go / render.go / cmd/guard/main.go. Making it
// green, from that first commit onward, is the builder's job.
//
// Brief ids -> test names:
//
//	TG1 TestGuardQuietThresholdExitCodes  (AC2, M14 threshold boundary + exit 0/10 + quiet shape)
//	TG2 TestGuardCardContent              (AC3: root, per-thread next, park/pick prompt, palette, width)
//	TG3 TestGuardJSONSnapshot             (AC2 --json: Root/Threads/Open/Threshold/Over)
//	TG4 TestGuardMissingStateDegrade      (AC1: missing/malformed/unreadable => NO THREAD STATE, exit 0)
//	TG5 TestGuardStateIsHomeDerived       (AC1/M15: two fixture HOMEs => two different states)
//
// Hermeticity: every fixture lives under t.TempDir(); HOME is overridden for
// every binary exec; no test reads the live ~/.openclaw/state store.
package guard_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// ---------------------------------------------------------------- scaffolding

// repoRootPath derives the repo root from this file's location without a
// *testing.T so the binary build helper can use it too.
func repoRootPath() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller failed")
	}
	// <root>/internal/guard/guard_test.go -> <root>
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..")), nil
}

// guardBin builds cmd/guard once per test binary, into a temp dir OUTSIDE the
// repo (the worktree stays untouched by this suite).
var guardBin = sync.OnceValues(func() (string, error) {
	root, err := repoRootPath()
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "guard-bin-*")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "guard")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/guard")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build ./cmd/guard: %v: %s", err, out)
	}
	return bin, nil
})

// cleanEnv drops the env seams whose host values would make an assertion
// nondeterministic (COLUMNS is set explicitly per test instead).
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k := strings.SplitN(kv, "=", 2)[0]
		switch k {
		case "COLUMNS", "COLORTERM", "CLICOLOR_FORCE", "TERM":
			continue
		}
		if strings.HasPrefix(k, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// truecolor forces lipgloss's writer down the truecolor SGR path so the
// palette-subset assertion has sequences to inspect (the radar suite's exact
// seam, radar_test.go:639-644).
var truecolor = []string{"COLORTERM=truecolor", "CLICOLOR_FORCE=1", "TERM=xterm-256color"}

// execGuard runs the guard binary against a fixture HOME.
func execGuard(t *testing.T, home string, extraEnv []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	bin, err := guardBin()
	if err != nil {
		t.Fatalf("cmd/guard not buildable (builder must add cmd/guard/main.go): %v", err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(cleanEnv(), "HOME="+home)
	cmd.Env = append(cmd.Env, extraEnv...)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	code = 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("exec guard %v: %v", args, err)
	}
	return out.String(), errb.String(), code
}

// threadSpec is one fixture thread; opened_at is RFC3339 and next is the
// resume hook the card must carry.
type threadSpec struct {
	id, label, openedAt, next, status string
}

// stateDoc renders the state document exactly as required_behaviour row 1
// fixes it: {"root": "...", "threads": [{id,label,opened_at,next,status}]}.
func stateDoc(root string, threads []threadSpec) string {
	var b strings.Builder
	b.WriteString(`{"root":` + strconv.Quote(root) + `,"threads":[`)
	for i, th := range threads {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"id":` + strconv.Quote(th.id) +
			`,"label":` + strconv.Quote(th.label) +
			`,"opened_at":` + strconv.Quote(th.openedAt) +
			`,"next":` + strconv.Quote(th.next) +
			`,"status":` + strconv.Quote(th.status) + `}`)
	}
	b.WriteString("]}")
	return b.String()
}

// openThreads builds n open threads plus c closed ones. The closed threads come
// first, so an implementation that counts by index or drops the status filter
// trips immediately.
func openThreads(open, closed int) []threadSpec {
	var ts []threadSpec
	for i := 1; i <= closed; i++ {
		ts = append(ts, threadSpec{
			id: fmt.Sprintf("c%d", i), label: fmt.Sprintf("closed thread %d", i),
			openedAt: "2026-09-30T09:00:00-05:00", next: fmt.Sprintf("closed-next-%d", i),
			status: "closed",
		})
	}
	for i := 1; i <= open; i++ {
		ts = append(ts, threadSpec{
			id: fmt.Sprintf("t%d", i), label: fmt.Sprintf("open thread %d", i),
			openedAt: "2026-09-30T10:00:00-05:00", next: fmt.Sprintf("next-op-%d", i),
			status: "open",
		})
	}
	return ts
}

// writeState is the whole fixture surface for guard: the HOME-resolved state
// document (nothing else in the tree is created).
func writeState(t *testing.T, home, doc string) {
	t.Helper()
	p := filepath.Join(home, ".openclaw", "state", "threads.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

func visibleWidth(s string) int { return ansi.StringWidth(ansi.Strip(s)) }

func maxLineWidth(s string) int {
	max := 0
	for _, ln := range strings.Split(s, "\n") {
		if w := visibleWidth(ln); w > max {
			max = w
		}
	}
	return max
}

// hasBoxDrawingRune reports whether s carries a U+2500–U+257F rune.
func hasBoxDrawingRune(s string) bool {
	for _, r := range s {
		if r >= 0x2500 && r <= 0x257F {
			return true
		}
	}
	return false
}

// solarizedTriples is the canonical ten (render.go:17-27) as "r;g;b".
var solarizedTriples = map[string]string{
	"42;161;152":  "#2aa198 cyan",
	"181;137;0":   "#b58900 yellow",
	"203;75;22":   "#cb4b16 orange",
	"133;153;0":   "#859900 green",
	"131;148;150": "#839496 base0",
	"88;110;117":  "#586e75 base01",
	"7;54;66":     "#073642 base02",
	"0;43;54":     "#002b36 base03",
	"238;232;213": "#eee8d5 base2",
	"253;246;227": "#fdf6e3 base3",
}

var sgrRGBRe = regexp.MustCompile(`([34]8);2;(\d+);(\d+);(\d+)`)

func lines(s string) []string { return strings.Split(strings.TrimRight(s, "\n"), "\n") }

func linesContain(ls []string, needle string) bool {
	for _, ln := range ls {
		if strings.Contains(ln, needle) {
			return true
		}
	}
	return false
}

// containsWordFold reports whether s carries word as a whole word, ignoring
// case — used for the park/pick prompt, whose exact casing the spec leaves open
// while fixing the two action names.
func containsWordFold(s, word string) bool {
	return regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(word) + `\b`).MatchString(s)
}

// ---------------------------------------------------------------- JSON helpers

// jsonKey resolves an object key case-insensitively: the snapshot contract pins
// the field NAMES (Root/Threads/Open/Threshold/Over) and the VALUES, not a
// casing convention, so assertions run on the value.
func jsonKey(m map[string]json.RawMessage, name string) (json.RawMessage, bool) {
	for k, v := range m {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return nil, false
}

// jsonKeyNorm also ignores underscores (the store's opened_at spelling).
func jsonKeyNorm(m map[string]json.RawMessage, name string) (json.RawMessage, bool) {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }
	want := norm(name)
	for k, v := range m {
		if norm(k) == want {
			return v, true
		}
	}
	return nil, false
}

func decodeObject(t *testing.T, what string, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s is not a JSON object: %v\nraw: %s", what, err, raw)
	}
	return m
}

func snapshotString(t *testing.T, m map[string]json.RawMessage, field string) string {
	t.Helper()
	raw, ok := jsonKey(m, field)
	if !ok {
		t.Fatalf("--json snapshot is missing the %q field (required_behaviour row 4); keys: %v", field, keysOf(m))
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("--json %s is not a string: %v", field, err)
	}
	return s
}

func snapshotInt(t *testing.T, m map[string]json.RawMessage, field string) int {
	t.Helper()
	raw, ok := jsonKey(m, field)
	if !ok {
		t.Fatalf("--json snapshot is missing the %q field (required_behaviour row 4); keys: %v", field, keysOf(m))
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		t.Fatalf("--json %s is not an integer: %v", field, err)
	}
	return n
}

func snapshotBool(t *testing.T, m map[string]json.RawMessage, field string) bool {
	t.Helper()
	raw, ok := jsonKey(m, field)
	if !ok {
		t.Fatalf("--json snapshot is missing the %q field (required_behaviour row 4); keys: %v", field, keysOf(m))
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("--json %s is not a bool: %v", field, err)
	}
	return b
}

func keysOf(m map[string]json.RawMessage) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// ---------------------------------------------------------------- TG1 / AC2 / M14

// TestGuardQuietThresholdExitCodes pins AC2 and M14's boundary flip: a thread is
// open iff its status is not "closed"; the threshold is 3; --quiet prints
// exactly one newline-terminated line and exits 0 below the threshold and 10 at
// or above it. N = 2 / 3 / 9, each fixture carrying one closed thread that must
// not count.
func TestGuardQuietThresholdExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		open     int
		closed   int
		wantLine string
		wantCode int
	}{
		{name: "N=2 under threshold", open: 2, closed: 1, wantLine: "threads 2/3 — under threshold", wantCode: 0},
		{name: "N=3 at threshold", open: 3, closed: 1, wantLine: "CHECKPOINT: 3 open threads", wantCode: 10},
		{name: "N=9 over threshold", open: 9, closed: 1, wantLine: "CHECKPOINT: 9 open threads", wantCode: 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeState(t, home, stateDoc("ship EF suite v1", openThreads(tc.open, tc.closed)))

			stdout, stderr, code := execGuard(t, home, nil, "--quiet")
			if code != tc.wantCode {
				t.Errorf("--quiet at Open=%d: exit %d, want %d (nonzero only for the fire condition)", tc.open, code, tc.wantCode)
			}
			if stderr != "" {
				t.Errorf("--quiet at Open=%d wrote to stderr: %q", tc.open, stderr)
			}
			if stdout != tc.wantLine+"\n" {
				t.Errorf("--quiet at Open=%d: stdout %q, want exactly %q plus one newline", tc.open, stdout, tc.wantLine)
			}
			if got := len(lines(stdout)); got != 1 {
				t.Errorf("--quiet at Open=%d must emit exactly one line, got %d: %q", tc.open, got, stdout)
			}
			if strings.Contains(stdout, "\x1b") {
				t.Errorf("--quiet must not carry an ANSI escape: %q", stdout)
			}
			if hasBoxDrawingRune(stdout) {
				t.Errorf("--quiet must not carry box runes: %q", stdout)
			}
		})
	}

	// The closed thread must be excluded at every N: counting all threads would
	// fire at N=2 (2 open + 1 closed = 3).
	home := t.TempDir()
	writeState(t, home, stateDoc("ship EF suite v1", openThreads(2, 1)))
	stdout, _, code := execGuard(t, home, nil, "--quiet")
	if code != 0 || !strings.HasPrefix(stdout, "threads 2/3") {
		t.Errorf("a status:closed thread must not count as open: exit %d stdout %q", code, stdout)
	}

	// Unknown flag: exit 2 (the usage class cmd/radar/main.go pins).
	if _, _, code := execGuard(t, home, nil, "--nope"); code != 2 {
		t.Errorf("unknown flag: exit %d, want 2", code)
	}
	// --quiet and --json are mutually exclusive: usage on stderr, exit 2.
	_, stderr, code := execGuard(t, home, nil, "--quiet", "--json")
	if code != 2 {
		t.Errorf("--quiet --json: exit %d, want 2", code)
	}
	if stderr == "" {
		t.Errorf("--quiet --json must print a usage error on stderr")
	}
}

// ---------------------------------------------------------------- TG2 / AC3

// TestGuardCardContent pins AC3: the default mode renders the Solarized card —
// the ROOT anchor, one line per thread carrying that thread's Next verbatim,
// and the park/pick prompt — bounded to min(termWidth,100) and emitting only
// the ten Solarized RGB triples.
func TestGuardCardContent(t *testing.T) {
	const root = "ship EF suite v1"
	home := t.TempDir()
	writeState(t, home, stateDoc(root, []threadSpec{
		{id: "t1", label: "guard", openedAt: "2026-09-30T10:00:00-05:00", next: "resume guard renderer", status: "open"},
		{id: "t2", label: "closer", openedAt: "2026-09-30T10:05:00-05:00", next: "resume closer ladder", status: "open"},
		{id: "t3", label: "radar", openedAt: "2026-09-30T10:10:00-05:00", next: "resume radar wrappers", status: "open"},
	}))

	stdout, _, code := execGuard(t, home, truecolor)
	if code != 0 {
		t.Fatalf("default mode exit %d, want 0", code)
	}
	ls := lines(stdout)
	if !linesContain(ls, root) {
		t.Errorf("card must carry the ROOT anchor %q; got:\n%s", root, stdout)
	}
	for _, next := range []string{"resume guard renderer", "resume closer ladder", "resume radar wrappers"} {
		if !linesContain(ls, next) {
			t.Errorf("card must carry thread Next %q on its own rendered line; got:\n%s", next, stdout)
		}
	}
	if len(ls) < 4 {
		t.Errorf("three distinct Next hooks must not collapse onto one line; got %d lines:\n%s", len(ls), stdout)
	}
	if !containsWordFold(stdout, "park") || !containsWordFold(stdout, "pick") {
		t.Errorf("card must name the two actions (park a thread / pick one); got:\n%s", stdout)
	}

	// Palette: every emitted truecolor SGR triple is one of the ten.
	seen := map[string]bool{}
	for _, m := range sgrRGBRe.FindAllStringSubmatch(stdout, -1) {
		triple := m[2] + ";" + m[3] + ";" + m[4]
		seen[triple] = true
		if _, ok := solarizedTriples[triple]; !ok {
			t.Errorf("emitted RGB SGR triple %s (fg/bg %s) is outside the Solarized ten", triple, m[1])
		}
	}
	if len(seen) == 0 {
		t.Fatal("no truecolor SGR sequences emitted under a forced-truecolor profile (AC3 requires colored spans)")
	}

	// Width bound: min(COLUMNS, 100), exercised with a Next long enough that an
	// unbounded renderer would exceed both bounds.
	longHome := t.TempDir()
	writeState(t, longHome, stateDoc(root, []threadSpec{
		{id: "t1", label: "wide", openedAt: "2026-09-30T10:00:00-05:00", next: strings.Repeat("w", 160), status: "open"},
	}))
	for _, tc := range []struct {
		columns string
		bound   int
	}{{"200", 100}, {"60", 60}} {
		env := append([]string{"COLUMNS=" + tc.columns}, truecolor...)
		out, _, code := execGuard(t, longHome, env)
		if code != 0 {
			t.Fatalf("COLUMNS=%s exit %d, want 0", tc.columns, code)
		}
		if got := maxLineWidth(out); got > tc.bound {
			t.Errorf("COLUMNS=%s: widest rendered line is %d visible cols > bound %d", tc.columns, got, tc.bound)
		}
	}
}

// ---------------------------------------------------------------- TG3 / AC2

// TestGuardJSONSnapshot pins the --json contract: a single document
// unmarshalling into {Root, Threads, Open, Threshold, Over} with
// Over == (Open >= Threshold).
func TestGuardJSONSnapshot(t *testing.T) {
	const root = "ship EF suite v1"
	for _, tc := range []struct {
		name     string
		open     int
		closed   int
		wantOver bool
	}{
		{name: "N=2 under", open: 2, closed: 1, wantOver: false},
		{name: "N=3 at threshold", open: 3, closed: 1, wantOver: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeState(t, home, stateDoc(root, openThreads(tc.open, tc.closed)))

			stdout, stderr, code := execGuard(t, home, nil, "--json")
			if code != 0 {
				t.Fatalf("--json exit %d (stderr %q), want 0", code, stderr)
			}
			top := decodeObject(t, "--json output", []byte(stdout))
			if got := snapshotString(t, top, "Root"); got != root {
				t.Errorf("--json Root = %q, want %q", got, root)
			}
			if got := snapshotInt(t, top, "Open"); got != tc.open {
				t.Errorf("--json Open = %d, want %d (closed threads excluded)", got, tc.open)
			}
			if got := snapshotInt(t, top, "Threshold"); got != 3 {
				t.Errorf("--json Threshold = %d, want 3", got)
			}
			if got := snapshotBool(t, top, "Over"); got != tc.wantOver {
				t.Errorf("--json Over = %v, want %v (Over == Open >= Threshold)", got, tc.wantOver)
			}
			rawThreads, ok := jsonKey(top, "Threads")
			if !ok {
				t.Fatalf("--json snapshot is missing the Threads field")
			}
			var threads []map[string]json.RawMessage
			if err := json.Unmarshal(rawThreads, &threads); err != nil {
				t.Fatalf("--json Threads is not an array of objects: %v", err)
			}
			if len(threads) != tc.open+tc.closed {
				t.Errorf("--json Threads carries %d entries, want %d (the store's full thread list)", len(threads), tc.open+tc.closed)
			}
			found := false
			for _, th := range threads {
				if raw, ok := jsonKeyNorm(th, "Next"); ok {
					var s string
					if err := json.Unmarshal(raw, &s); err == nil && s == "next-op-1" {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("--json thread entries must carry each thread's Next (e.g. %q); got %v", "next-op-1", threads)
			}
		})
	}
}

// ---------------------------------------------------------------- TG4 / AC1

// TestGuardMissingStateDegrade pins AC1's disclosed degrade: a missing,
// malformed, or unreadable state file is not an error path — exactly one
// "NO THREAD STATE" line and exit 0, never a panic, never nonzero.
func TestGuardMissingStateDegrade(t *testing.T) {
	cases := map[string]func(t *testing.T, home string){
		"missing file": func(t *testing.T, home string) {
			// HOME with no state file at all (guard's live first-run state).
		},
		"malformed json": func(t *testing.T, home string) {
			writeState(t, home, `{"root": "half a doc", "threads": [`)
		},
		"unreadable path": func(t *testing.T, home string) {
			// A directory where the state file belongs: os.ReadFile fails.
			p := filepath.Join(home, ".openclaw", "state", "threads.json")
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			setup(t, home)

			stdout, stderr, code := execGuard(t, home, nil)
			if code != 0 {
				t.Errorf("missing/unreadable state must exit 0 (a monitor must not page on the absence of its own input); got %d (stderr %q)", code, stderr)
			}
			if strings.Contains(stdout, "panic") || strings.Contains(stderr, "panic") {
				t.Fatalf("degrade path panicked: stdout %q stderr %q", stdout, stderr)
			}
			ls := lines(stdout)
			if len(ls) != 1 {
				t.Errorf("degrade must render exactly one line, got %d: %q", len(ls), stdout)
			}
			if len(ls) > 0 {
				if got := strings.TrimSpace(ansi.Strip(ls[0])); got != "NO THREAD STATE" {
					t.Errorf("degrade line = %q, want the disclosed literal %q", got, "NO THREAD STATE")
				}
			}
			// The same degrade holds under --quiet (still the non-fire exit).
			stdout, _, code = execGuard(t, home, nil, "--quiet")
			if code != 0 {
				t.Errorf("--quiet on missing state must exit 0, got %d", code)
			}
			if strings.TrimSpace(ansi.Strip(stdout)) != "NO THREAD STATE" {
				t.Errorf("--quiet on missing state = %q, want the NO THREAD STATE line", stdout)
			}
		})
	}
}

// ---------------------------------------------------------------- TG5 / AC1 / M15

// TestGuardStateIsHomeDerived pins AC1's HOME resolution (M15's kill): two
// different fixture HOMEs must yield two different states from the same binary.
// A hardcoded state path (or a package-init constant) reads one file for both
// HOMEs and goes red here.
func TestGuardStateIsHomeDerived(t *testing.T) {
	homeA := t.TempDir()
	writeState(t, homeA, stateDoc("anchor A", openThreads(3, 0)))
	homeB := t.TempDir()
	writeState(t, homeB, stateDoc("anchor B", openThreads(1, 0)))

	outA, _, codeA := execGuard(t, homeA, nil, "--quiet")
	outB, _, codeB := execGuard(t, homeB, nil, "--quiet")
	if codeA != 10 || codeB != 0 {
		t.Errorf("two HOMEs must give two states: A exit %d (want 10), B exit %d (want 0)", codeA, codeB)
	}
	if outA == outB {
		t.Errorf("identical output across two fixture HOMEs — the state path is not HOME-derived (M15 shape): %q", outA)
	}

	jsonA, _, codeA := execGuard(t, homeA, nil, "--json")
	jsonB, _, codeB := execGuard(t, homeB, nil, "--json")
	if codeA != 0 || codeB != 0 {
		t.Fatalf("--json exits: A %d, B %d (want 0)", codeA, codeB)
	}
	if got := snapshotString(t, decodeObject(t, "A --json", []byte(jsonA)), "Root"); got != "anchor A" {
		t.Errorf("HOME A --json Root = %q, want %q", got, "anchor A")
	}
	if got := snapshotString(t, decodeObject(t, "B --json", []byte(jsonB)), "Root"); got != "anchor B" {
		t.Errorf("HOME B --json Root = %q, want %q", got, "anchor B")
	}
}
