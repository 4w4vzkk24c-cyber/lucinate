package radar

// RED acceptance suite for card 1602 (state-radar CLI) — AC1–AC7 + M1–M9 kill map.
// Card 1606 v1.1 (r5): AC8-AC11 pinned RED-first via M10-M13 - see the map tail.
//
// Contract: quorum-test-generator writes tests only. At base this package has no
// implementation, so the suite is red by design until quorum-builder lands
// types.go/scan.go/git.go/render.go + cmd/radar. Kill map:
//
//	M1 → TestScanTasksFiltersDoneArchived (filter inversion / body-mention leak / unterminated frontmatter)
//	M2 → TestCollectGitTelemetry (ReEntryFile must be exactly "path:N" from the first diff -U0 hunk)
//	M3 → TestRenderPaletteConstantsAreSolarized + TestRenderFullSolarizedSGRSubset (single-token drift)
//	M4 → TestParseRoadmapUnavailableSentinel + TestRenderDegradeLine + TestCLIDegradeExitZero (degrade path)
//	M5 → TestCLIModes (--summary line-count / no-box-runes assertions)
//	M6 → TestCLIModes (--json unmarshal into RadarSnapshot + Timestamp non-zero)
//	M7 → TestMakeBuildRadarTarget (target deleted or pointed at wrong package)
//	M8 → TestSuitePresenceAndNoSkips (partial silencing: skip/exclusion; whole-file deletion is caught by the parent collect-count funnel)
//	M9 → TestParseRoadmapExtractsDoNowAndRanked (happy path must yield rows[0] WIP + ranked rows)
//
// Hermeticity: every fixture lives under t.TempDir(); HOME is overridden for every
// binary exec; no test reads ~/Repositories/kanban-zed or ~/Obsidian/Roadmap.
//
// 1606 v1.1 kill-map additions (card 1606, spec r5):
//	M10 -> TestRenderWidthCap (width-cap removal: termWidth unbounded or init-cached)
//	M11 -> TestCollectGitAuthorCaseInsensitive (-i removal: --author=Zane case-sensitive again)
//	M12 -> TestRenderCapsAndMoreLine (display-cap removal: rows uncapped, '+N more (of M)' missing)
//	M13 -> TestBuildDoNowFallbackCardStatus (fallback removal: v1 iff-in-progress restored)
//
// D4 (spec r5 named-interaction): TestBuildInFlightContract (a)/(b)/(c) now
// assert the v1.1 fallback contract - (a) additionally reads
// InFlightCardStatus "in-progress", (b) is non-nil with card status "todo",
// (c) is non-nil and row-sourced with "" - RED-then-GREEN with the builder's
// changes. The other 20 test functions stay byte-untouched.
//
// 1609 v1.2 kill-map additions (card 1609, spec r3):
//	M20 -> TestRadarTelemetryFollowsCwd (telemetry re-hardcoded to the store: the cwd markers vanish)
//	M21 -> TestRadarTelemetryFollowsCwd (card scan moved to cwd: Stalled empty, HOME card title lost)
//	M22 -> TestCLIBinaryTelemetryFollowsCmdDir (execRadar's cmd.Dir removed: the binary inherits
//	       the clean go-test cwd and reports no bin_only.go marker)
//
// 1609 v1.2 amendment (spec row 6, authoritative): the cwd-broken fixtures
// below gain t.Chdir(<their store repo>) immediately before each Build(home)
// invocation - assertions unchanged. TestBuildFilemodeOnlyDirtyTree is the
// seventh: telemetry reads the process cwd, so unamended it falls back to the
// clean worktree, the tree reads clean, and the card-path default fires
// against its r4-F9 empty assertions. TestBuildStalledRules stays green
// without t.Chdir (its assertions are cwd-independent).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// ---------------------------------------------------------------- scaffolding

// repoRootPath computes the repo root from this file's location without a
// *testing.T, so non-test scaffolding (buildRadarBin) can use it too.
func repoRootPath() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("runtime.Caller failed")
	}
	// <root>/internal/radar/radar_test.go -> <root>
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..")), nil
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := repoRootPath()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

var buildRadarBin = sync.OnceValues(func() (string, error) {
	// D1: go test runs with CWD = package dir (internal/radar); build from the
	// repo root so ./cmd/radar resolves.
	root, err := repoRootPath()
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "radar-bin-*")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "radar")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/radar")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build ./cmd/radar: %v: %s", err, out)
	}
	return bin, nil
})

// gitEnv isolates git from the operator's config: fixture HOME, no leaked
// GIT_* vars (the exact leak class kanban #1461 records).
func gitEnv(home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "HOME="+home)
}

func runGit(t *testing.T, home, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

type cardSpec struct {
	id, title, status, priority string
	body                        string // extra body lines after the closing ---
}

func writeCard(t *testing.T, tasksDir string, c cardSpec) string {
	t.Helper()
	name := filepath.Join(tasksDir, c.id+"-card.md")
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\ntitle: %s\nstatus: %s\npriority: %s\ncreated: 2026-09-30T10:00:00-05:00\n---\n", c.id, c.title, c.status, c.priority)
	b.WriteString(c.body)
	if err := os.WriteFile(name, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return name
}

const deltaFixture = `# Roadmap

Preamble prose that is not a task.

## Do now (deep-work WIP = 1)

### → Wire the radar scan loop

- [ ] ` + "`   476`" + ` Wire the radar scan loop — pre-push gate corrupts state · *kanban #1461* · ` + "`in-progress`" + ` · ` + "`large/days`" + ` · *v1.2: platform/infra* — was unscored: notes here.

## Ranked — deep work, ready (159 after the leader)

### 457 — 1 item

- [ ] ` + "`   457`" + ` Ship the Solarized render pass — *kanban #1388* · ` + "`medium/hours`" + ` · *v1.2: strategy research* — was medium/days: notes.

### 286 — 1 item

- [ ] ` + "`   286`" + ` Back up the data dir — *kanban #1405* · ` + "`todo`" + ` · ` + "`medium/half_day`" + ` — was unscored: notes.
`

// bandOnlyDeltaFixture omits the "## Do now" section entirely (r5 F8 input).
const bandOnlyDeltaFixture = `# Roadmap

## Ranked — deep work, ready (159 after the leader)

### 457 — 1 item

- [ ] ` + "`   457`" + ` Ship the Solarized render pass — *kanban #1388* · ` + "`medium/hours`" + ` — notes.
`

func writeDelta(t *testing.T, home, content string) string {
	t.Helper()
	p := filepath.Join(home, "Obsidian", "Roadmap", "Δ.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// homeFixture builds a hermetic HOME with a git-initialized kanban-zed repo
// (one clean seed commit by Zane whose message references no card id) plus an
// optional Δ.md. Cards land under Repositories/kanban-zed/tasks/.
func homeFixture(t *testing.T, cards []cardSpec, delta string) string {
	t.Helper()
	home := t.TempDir()
	repo := filepath.Join(home, "Repositories", "kanban-zed")
	tasks := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range cards {
		writeCard(t, tasks, c)
	}
	runGit(t, home, repo, "init", "-b", "main")
	runGit(t, home, repo, "config", "user.name", "Zane")
	runGit(t, home, repo, "config", "user.email", "zane@fixture.invalid")
	if delta != "" {
		writeDelta(t, home, delta)
	}
	runGit(t, home, repo, "add", "-A")
	runGit(t, home, repo, "commit", "-m", "seed: fixture baseline (no card refs)")
	return home
}

// cwdRepoFixture builds a SECOND, independent git repo (its own t.TempDir)
// whose identity is structurally distinct from any HOME store: a tracked
// file named cwd_only.go, a fresh default-branch commit whose message
// references 1461 (the Landed evidence that alone decorates StageVerify),
// and a one-line dirtying of cwd_only.go so the re-entry anchor points at
// line 3. Returns the repo root; the caller t.Chdir's into it.
func cwdRepoFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	repo := filepath.Join(home, "cwd-repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, home, repo, "init", "-b", "main")
	runGit(t, home, repo, "config", "user.name", "Zane")
	runGit(t, home, repo, "config", "user.email", "zane@fixture.invalid")
	if err := os.WriteFile(filepath.Join(repo, "cwd_only.go"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, home, repo, "add", "-A")
	runGit(t, home, repo, "commit", "-m", "seed: cwd fixture baseline (no card refs)")
	runGit(t, home, repo, "commit", "--allow-empty", "-m", "feat(1461): cwd-side work")
	// Dirty the tracked file: the added line lands on line 3, so the anchor is
	// cwd_only.go:3 (an untracked file would yield a zero-hunk, empty anchor).
	if err := os.WriteFile(filepath.Join(repo, "cwd_only.go"), []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

// binOnlyRepoFixture builds a deterministic, hermetic git repo whose sole
// tracked file is the distinctly named bin_only.go, dirtied after its seed
// commit, so `git status --porcelain` reports exactly that marker. execRadar
// runs the spawned binary here: the binary's telemetry must describe the cwd
// it was handed, and no other fixture in the suite can satisfy the bin_only.go
// needle vacuously.
func binOnlyRepoFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	repo := filepath.Join(home, "bin-repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, home, repo, "init", "-b", "main")
	runGit(t, home, repo, "config", "user.name", "Zane")
	runGit(t, home, repo, "config", "user.email", "zane@fixture.invalid")
	if err := os.WriteFile(filepath.Join(repo, "bin_only.go"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, home, repo, "add", "-A")
	runGit(t, home, repo, "commit", "-m", "seed: bin-only fixture baseline (no card refs)")
	// Dirty the tracked file: the added line lands on line 3, so the anchor is
	// bin_only.go:3 (an untracked file would yield a zero-hunk, empty anchor).
	if err := os.WriteFile(filepath.Join(repo, "bin_only.go"), []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func execRadar(t *testing.T, home string, args ...string) (string, string, int) {
	t.Helper()
	bin, err := buildRadarBin()
	if err != nil {
		t.Fatalf("cmd/radar not buildable (builder must add cmd/radar/main.go): %v", err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "HOME="+home)
	// 1609 v1.2 hermeticity, now load-bearing: run the spawned binary inside a
	// fixture repo carrying the distinctly named dirty file bin_only.go, so its
	// cwd telemetry describes that fixture deterministically and stays
	// operator-state-independent. TestCLIBinaryTelemetryFollowsCmdDir is the
	// pin: drop this line and the binary inherits the go-test cwd (internal/
	// radar, a clean package dir), reports no bin_only.go, and reddens.
	cmd.Dir = binOnlyRepoFixture(t)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("exec radar %v: %v", args, err)
	}
	return stdout.String(), stderr.String(), code
}

// normID normalizes the legal cosmetic id spellings ("1461", "#1461", "Δ#1461")
// down to the bare digits the kanban frontmatter carries, so assertions pin the
// content contract without pinning prefix cosmetics the spec leaves open.
func normID(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "Δ#")
	s = strings.TrimPrefix(s, "#")
	if i := strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		s = s[:i]
	}
	return s
}

func hasBoxDrawingRune(s string) bool {
	for _, r := range s {
		if r >= 0x2500 && r <= 0x257F {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- AC1 / M1

func TestScanTasksFiltersDoneArchived(t *testing.T) {
	dir := t.TempDir()
	writeCard(t, dir, cardSpec{id: "101", title: "Finished long ago", status: "done", priority: "low"})
	writeCard(t, dir, cardSpec{id: "102", title: "Filed away", status: "archived", priority: "low"})
	writeCard(t, dir, cardSpec{
		id: "301", title: "Active card", status: "in-progress", priority: "high",
		body: "Body prose mentioning status: done must never filter this file.\n",
	})
	// Malformed: no closing --- delimiter; must survive as an active card.
	malformed := filepath.Join(dir, "302-card.md")
	if err := os.WriteFile(malformed, []byte("---\nid: 302\ntitle: Missing close\nstatus: todo\npriority: medium\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cards, err := ScanTasks(dir, 250*time.Millisecond)
	if err != nil {
		t.Fatalf("ScanTasks: %v", err)
	}
	got := map[string]ActiveCard{}
	for _, c := range cards {
		got[c.ID] = c
	}
	if len(cards) != 2 {
		t.Fatalf("want exactly 2 active cards, got %d: %+v", len(cards), cards)
	}
	for _, id := range []string{"101", "102"} {
		if _, ok := got[id]; ok {
			t.Errorf("excluded status leaked into scan: id %s present", id)
		}
	}
	a, ok := got["301"]
	if !ok {
		t.Fatalf("active in-progress card 301 dropped: %+v", cards)
	}
	if a.Title != "Active card" || a.Priority != "high" || a.Status != "in-progress" {
		t.Errorf("card 301 fields wrong: %+v", a)
	}
	m, ok := got["302"]
	if !ok {
		t.Fatalf("unterminated-frontmatter card 302 dropped: %+v", cards)
	}
	if m.Title != "Missing close" || m.Priority != "medium" || m.Status != "todo" {
		t.Errorf("card 302 fields wrong: %+v", m)
	}
	if a.Path == "" {
		t.Errorf("ActiveCard.Path empty for card 301")
	}
}

func TestScanTasksPerf1600Under30ms(t *testing.T) {
	dir := t.TempDir()
	statuses := []string{"todo", "in-progress", "blocked", "review", "done", "archived"}
	for i := 0; i < 1600; i++ {
		writeCard(t, dir, cardSpec{
			id:       strconv.Itoa(5000 + i),
			title:    "Perf filler card " + strconv.Itoa(i),
			status:   statuses[i%len(statuses)],
			priority: "medium",
		})
	}
	start := time.Now()
	cards, err := ScanTasks(dir, 250*time.Millisecond)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ScanTasks: %v", err)
	}
	if len(cards) == 0 {
		t.Fatal("scan returned zero cards over 1,600-file fixture")
	}
	if elapsed >= 30*time.Millisecond {
		t.Fatalf("1,600-file scan took %v (budget 30ms, AC1)", elapsed)
	}
}

// ---------------------------------------------------------------- AC2 / M4 / M9

func TestParseRoadmapExtractsDoNowAndRanked(t *testing.T) {
	path := writeDelta(t, t.TempDir(), deltaFixture)
	rows, err := ParseRoadmap(path, 120)
	if err != nil {
		t.Fatalf("ParseRoadmap happy path: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want rows[0] WIP + 2 ranked rows, got %d: %+v", len(rows), rows)
	}
	// r3 F5: rows[0] is the ## Do now entry; rows[1:] are the ranked band.
	if normID(rows[0].ID) != "1461" || rows[0].Title != "Wire the radar scan loop" {
		t.Errorf("rows[0] (Do now) wrong: %+v", rows[0])
	}
	if strings.TrimSpace(rows[0].Priority) != "476" {
		t.Errorf("rows[0] must carry the raw score, got priority %q", rows[0].Priority)
	}
	want := []struct{ id, title, score string }{
		{"1388", "Ship the Solarized render pass", "457"},
		{"1405", "Back up the data dir", "286"},
	}
	for i, w := range want {
		r := rows[i+1]
		if normID(r.ID) != w.id || r.Title != w.title || strings.TrimSpace(r.Priority) != w.score {
			t.Errorf("rows[%d] wrong: want %v, got %+v", i+1, w, r)
		}
	}

	// Audit r1 (minor, band over-capture): an unrelated "## Notes" heading
	// between Do-now and the ranked band must NOT open a capture band — a
	// task-shaped line parked under it must never reach the queue pipeline
	// (Build feeds Queue from rows[1:]).
	notesFixture := `# Roadmap

## Do now (deep-work WIP = 1)

### → Wire the radar scan loop

- [ ] ` + "`   476`" + ` Wire the radar scan loop — notes · *kanban #1461* · ` + "`in-progress`" + `

## Notes

- [ ] ` + "`   999`" + ` Parked thought under an unrelated section — *kanban #7777* · ` + "`todo`" + `

## Ranked — deep work, ready

### 457 — 1 item

- [ ] ` + "`   457`" + ` Ship the Solarized render pass — *kanban #1388* · ` + "`medium/hours`" + `
`
	notesRows, err := ParseRoadmap(writeDelta(t, t.TempDir(), notesFixture), 120)
	if err != nil {
		t.Fatalf("ParseRoadmap notes case: %v", err)
	}
	for _, r := range notesRows {
		if normID(r.ID) == "7777" {
			t.Errorf("task-shaped line under unrelated ## Notes leaked into the queue pipeline: %+v", notesRows)
		}
	}
	if len(notesRows) != 2 || normID(notesRows[0].ID) != "1461" || normID(notesRows[1].ID) != "1388" {
		t.Errorf("## Notes must contribute no rows; want [1461, 1388], got %+v", notesRows)
	}
}

func TestParseRoadmapIgnoresLinesPast120(t *testing.T) {
	var b strings.Builder
	b.WriteString(deltaFixture)
	for i := 0; i < 130; i++ {
		b.WriteString("filler line that must never be parsed as a task\n")
	}
	b.WriteString("- [ ] `    99` Late entry past the window — *kanban #7777* · `todo`\n")
	path := writeDelta(t, t.TempDir(), b.String())
	rows, err := ParseRoadmap(path, 120)
	if err != nil {
		t.Fatalf("ParseRoadmap: %v", err)
	}
	for _, r := range rows {
		if normID(r.ID) == "7777" {
			t.Errorf("parser read past maxLines=120: %+v", r)
		}
	}
}

func TestParseRoadmapUnavailableSentinel(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(home, "Obsidian", "Roadmap", "Δ.md")
	// Missing file.
	if _, err := ParseRoadmap(missing, 120); !errors.Is(err, ErrRoadmapUnavailable) {
		t.Errorf("missing Δ.md: want ErrRoadmapUnavailable, got %v", err)
	}
	// Directory where the file should be (read fails, open succeeds).
	if err := os.MkdirAll(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRoadmap(missing, 120); !errors.Is(err, ErrRoadmapUnavailable) {
		t.Errorf("unreadable Δ.md (dir): want ErrRoadmapUnavailable, got %v", err)
	}
	// Unreadable file (the errno class iCloud eviction surfaces as; EDEADLK is
	// the same read-failure seam). Root can read anything, so only run this
	// case unprivileged — never t.Skip, AC7 forbids skipped radar tests.
	// D3: fresh HOME — case 2 turned home's Δ.md path into a directory, so
	// writeDelta into it would EISDIR.
	if os.Geteuid() != 0 {
		p := writeDelta(t, t.TempDir(), deltaFixture)
		if err := os.Chmod(p, 0o000); err != nil {
			t.Fatal(err)
		}
		if _, err := ParseRoadmap(p, 120); !errors.Is(err, ErrRoadmapUnavailable) {
			t.Errorf("unreadable Δ.md (chmod 000): want ErrRoadmapUnavailable, got %v", err)
		}
	}
}

func TestParseRoadmapBandOnlyYieldsNoRows(t *testing.T) {
	// r5 F8: a band-only Δ.md yields empty rows — ranked entries are NEVER
	// promoted into rows[0]. Reading P2 is illegal.
	path := writeDelta(t, t.TempDir(), bandOnlyDeltaFixture)
	rows, err := ParseRoadmap(path, 120)
	if err != nil {
		t.Fatalf("band-only Δ.md must not error: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("band-only Δ.md must yield empty rows, got %+v", rows)
	}
	home := homeFixture(t, []cardSpec{{id: "1388", title: "Ship the Solarized render pass", status: "todo", priority: "high"}}, bandOnlyDeltaFixture)
	snap, err := Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.InFlight != nil {
		t.Errorf("band-only Δ.md must leave InFlight nil, got %+v", snap.InFlight)
	}
	if len(snap.Queue) != 0 {
		t.Errorf("band-only Δ.md must leave Queue empty, got %+v", snap.Queue)
	}
}

func TestRenderDegradeLine(t *testing.T) {
	snap := RadarSnapshot{Timestamp: time.Now()}
	degrade := fmt.Errorf("roadmap unavailable: %w", ErrRoadmapUnavailable)
	full := RenderFull(snap, degrade)
	if !strings.Contains(full, "[Queue unavailable: Δ.md evicted]") {
		t.Errorf("RenderFull must emit the exact degrade line, got %q", full)
	}
	sum := RenderSummary(snap, degrade)
	if !strings.Contains(sum, "[Queue unavailable: Δ.md evicted]") {
		t.Errorf("RenderSummary must emit the exact degrade line, got %q", sum)
	}
	if _, err := RenderJSON(snap); err != nil {
		t.Errorf("RenderJSON: %v", err)
	}
}

// ---------------------------------------------------------------- AC3 / M2

func gitTelemetryFixture(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, home, repo, "init", "-b", "main")
	runGit(t, home, repo, "config", "user.name", "Zane")
	runGit(t, home, repo, "config", "user.email", "zane@fixture.invalid")

	// Commit 1: fresh, on main, by Zane.
	if err := os.WriteFile(filepath.Join(repo, "cmd", "foo.go"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "old.txt"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, home, repo, "add", "-A")
	runGit(t, home, repo, "commit", "-m", "seed fresh")

	// Commit 2: 3 days old — must never appear in Landed.
	old := time.Now().Add(-72 * time.Hour).Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(repo, "old.txt"), []byte("stale\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "commit", "-am", "seed old")
	cmd.Dir = repo
	cmd.Env = append(gitEnv(home), "GIT_AUTHOR_DATE="+old, "GIT_COMMITTER_DATE="+old)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("old commit: %v: %s", err, out)
	}

	// Commits 3+4: side-branch commit merged --no-ff — the merge commit must
	// never appear in Landed.
	runGit(t, home, repo, "checkout", "-b", "side")
	if err := os.WriteFile(filepath.Join(repo, "side.txt"), []byte("side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, home, repo, "add", "-A")
	runGit(t, home, repo, "commit", "-m", "side work")
	runGit(t, home, repo, "checkout", "main")
	runGit(t, home, repo, "merge", "--no-ff", "side", "-m", "Merge branch 'side'")

	// Dirty the tracked file: added line lands on line 3.
	f := filepath.Join(repo, "cmd", "foo.go")
	if err := os.WriteFile(f, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, repo
}

func TestCollectGitTelemetry(t *testing.T) {
	home, repo := gitTelemetryFixture(t)
	now := time.Now()

	got, err := CollectGit(repo, now)
	if err != nil {
		t.Fatalf("CollectGit dirty tree: %v", err)
	}
	if len(got.DirtyFiles) != 1 || !strings.HasSuffix(got.DirtyFiles[0], "cmd/foo.go") {
		t.Errorf("DirtyFiles: want [cmd/foo.go], got %v", got.DirtyFiles)
	}
	// M2 killer: the anchor must be path:line from the FIRST hunk — added
	// gamma is line 3; empty or off-by-one anchors fail here.
	if got.ReEntryFile != "cmd/foo.go:3" {
		t.Errorf("ReEntryFile: want exactly cmd/foo.go:3, got %q", got.ReEntryFile)
	}
	if len(got.Landed) == 0 || len(got.Landed) > 5 {
		t.Fatalf("Landed: want 1..5 entries, got %d", len(got.Landed))
	}
	var hasFresh, hasOld, hasMerge bool
	for _, e := range got.Landed {
		if strings.Contains(e.Message, "seed fresh") {
			hasFresh = true
			if e.Hash == "" {
				t.Errorf("LandedEvidence.Hash empty for fresh commit")
			}
		}
		if strings.Contains(e.Message, "seed old") {
			hasOld = true
		}
		if strings.Contains(e.Message, "Merge") {
			hasMerge = true
		}
	}
	if !hasFresh {
		t.Errorf("Landed missing the fresh commit: %+v", got.Landed)
	}
	if hasOld {
		t.Errorf("Landed includes the 3-day-old commit: %+v", got.Landed)
	}
	if hasMerge {
		t.Errorf("Landed includes a merge commit: %+v", got.Landed)
	}

	// Clean tree: zero values, nil error.
	runGit(t, home, repo, "add", "-A")
	runGit(t, home, repo, "commit", "-m", "commit the dirty line")
	clean, err := CollectGit(repo, time.Now())
	if err != nil {
		t.Fatalf("CollectGit clean tree must be nil error, got %v", err)
	}
	if len(clean.DirtyFiles) != 0 {
		t.Errorf("clean tree DirtyFiles: %v", clean.DirtyFiles)
	}
	if clean.ReEntryFile != "" {
		t.Errorf("clean tree ReEntryFile: want empty, got %q", clean.ReEntryFile)
	}
}

// ---------------------------------------------------------------- AC4 / M3

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

func TestRenderPaletteConstantsAreSolarized(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "radar", "render.go"))
	if err != nil {
		t.Fatalf("render.go must exist (builder owns the palette): %v", err)
	}
	re := regexp.MustCompile(`#[0-9a-fA-F]{6}`)
	for _, hex := range re.FindAllString(string(src), -1) {
		rgb := hexToTriple(hex)
		if _, ok := solarizedTriples[rgb]; !ok {
			t.Errorf("palette constant %s (rgb %s) is outside the ten Solarized values", hex, rgb)
		}
	}
}

func hexToTriple(hex string) string {
	v, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil {
		return "invalid"
	}
	return fmt.Sprintf("%d;%d;%d", (v>>16)&0xff, (v>>8)&0xff, v&0xff)
}

func TestRenderFullSolarizedSGRSubset(t *testing.T) {
	home := homeFixture(t, []cardSpec{
		{id: "1461", title: "Wire the radar scan loop", status: "in-progress", priority: "high"},
		{id: "1101", title: "Blocked decision card", status: "blocked", priority: "high"},
	}, deltaFixture)
	bin, err := buildRadarBin()
	if err != nil {
		t.Fatalf("cmd/radar not buildable: %v", err)
	}
	cmd := exec.Command(bin)
	// 1609 v1.2 hermeticity: this site builds its own command (no execRadar),
	// so it needs its own non-repo cmd.Dir or cwd telemetry would read the
	// live checkout.
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"CLICOLOR_FORCE=1",
		"COLORTERM=truecolor",
		"TERM=xterm-256color",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("radar default mode under truecolor: %v", err)
	}
	stdout := string(out)

	re := regexp.MustCompile(`([34]8);2;(\d+);(\d+);(\d+)`)
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(stdout, -1) {
		triple := m[2] + ";" + m[3] + ";" + m[4]
		seen[triple] = true
		if _, ok := solarizedTriples[triple]; !ok {
			t.Errorf("emitted RGB SGR triple %s (fg/bg %s) is outside the Solarized ten", triple, m[1])
		}
	}
	if len(seen) == 0 {
		t.Fatal("no truecolor SGR sequences emitted under a forced-truecolor profile (AC4 requires colored spans)")
	}
	for triple, name := range solarizedTriples {
		if name == "#eee8d5 base2" || name == "#fdf6e3 base3" {
			continue // light-mode tokens; dark card need not emit them
		}
		if !seen[triple] {
			t.Errorf("required Solarized triple missing from full card: %s (%s)", triple, name)
		}
	}
}

// ---------------------------------------------------------------- AC5 / M5 / M6

func TestCLIModes(t *testing.T) {
	home := homeFixture(t, []cardSpec{
		{id: "1461", title: "Wire the radar scan loop", status: "in-progress", priority: "high"},
	}, deltaFixture)

	// Default: full bordered card.
	stdout, stderr, code := execRadar(t, home)
	if code != 0 || stderr != "" {
		t.Fatalf("default mode: exit %d stderr %q", code, stderr)
	}
	if !hasBoxDrawingRune(stdout) {
		t.Errorf("default mode must render a bordered card (box-drawing rune U+2500–U+257F), got %q", stdout)
	}

	// --summary: exactly 3 newline-terminated lines, zero box-drawing runes,
	// names the in-flight card id. (M5 killer.)
	stdout, _, code = execRadar(t, home, "--summary")
	if code != 0 {
		t.Fatalf("--summary exit %d", code)
	}
	if !strings.HasSuffix(stdout, "\n") {
		t.Errorf("--summary output must be newline-terminated")
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 3 {
		t.Errorf("--summary must emit exactly 3 lines, got %d: %q", len(lines), stdout)
	}
	if hasBoxDrawingRune(stdout) {
		t.Errorf("--summary must contain zero box-drawing runes: %q", stdout)
	}
	if !strings.Contains(stdout, "1461") {
		t.Errorf("--summary must name the in-flight card id: %q", stdout)
	}

	// --json: single document unmarshalling into RadarSnapshot, non-zero
	// Timestamp. (M6 killer.)
	stdout, _, code = execRadar(t, home, "--json")
	if code != 0 {
		t.Fatalf("--json exit %d", code)
	}
	var snap RadarSnapshot
	if err := json.Unmarshal([]byte(stdout), &snap); err != nil {
		t.Errorf("--json output does not unmarshal into radar.RadarSnapshot: %v (%q)", err, stdout)
	}
	if snap.Timestamp.IsZero() {
		t.Errorf("--json Timestamp must be non-zero")
	}

	// Mode mutual exclusion + unknown flag: exit 2, usage on stderr.
	_, stderr, code = execRadar(t, home, "--summary", "--json")
	if code != 2 {
		t.Errorf("--summary --json must exit 2, got %d", code)
	}
	if stderr == "" {
		t.Errorf("combined modes must print usage on stderr")
	}
	_, _, code = execRadar(t, home, "--bogus")
	if code != 2 {
		t.Errorf("unknown flag must exit 2, got %d", code)
	}
}

func TestCLIDegradeExitZero(t *testing.T) {
	// Empty fixture HOME: no kanban store, no Δ.md — radar must degrade with
	// exit 0 everywhere (M4 at the binary level), and --summary falls back to
	// the NO ACTIVE WORK text.
	home := t.TempDir()
	_, _, code := execRadar(t, home)
	if code != 0 {
		t.Errorf("default mode on empty fixture HOME: exit %d, want 0 (graceful degrade)", code)
	}
	stdout, _, code := execRadar(t, home, "--summary")
	if code != 0 {
		t.Errorf("--summary on empty fixture HOME: exit %d, want 0", code)
	}
	if !strings.Contains(stdout, "NO ACTIVE WORK") {
		t.Errorf("--summary must name NO ACTIVE WORK when nothing is in flight: %q", stdout)
	}
	stdout, _, code = execRadar(t, home, "--json")
	if code != 0 {
		t.Errorf("--json on empty fixture HOME: exit %d, want 0", code)
	}
	var snap RadarSnapshot
	if err := json.Unmarshal([]byte(stdout), &snap); err != nil {
		t.Errorf("--json degrade output must still be a RadarSnapshot: %v", err)
	}
}

// TestCLIBinaryTelemetryFollowsCmdDir pins the 1609 v1.2 hermeticity fix at
// the binary boundary: execRadar's cmd.Dir points at a fixture repo carrying
// the distinctly named dirty file bin_only.go, and the spawned radar must
// report THAT marker — i.e. its telemetry describes the cwd it was given, not
// an ambient checkout.
//
// GREEN at head: cmd.Dir = binOnlyRepoFixture -> git status there yields
// bin_only.go -> the marker appears in the --json DirtyFiles and in the full
// card's WORKING TREE line.
//
// RED under the mutant that removes cmd.Dir: the binary inherits the go-test
// cwd (internal/radar, a clean package dir), reports zero dirty files, and
// both assertions below fail. That green-at-head -> red-under-mutant
// transition is the pin.
func TestCLIBinaryTelemetryFollowsCmdDir(t *testing.T) {
	home := homeFixture(t, []cardSpec{
		{id: "1461", title: "Wire the radar scan loop", status: "in-progress", priority: "high"},
	}, deltaFixture)

	stdout, _, code := execRadar(t, home, "--json")
	if code != 0 {
		t.Fatalf("--json exit %d", code)
	}
	var snap RadarSnapshot
	if err := json.Unmarshal([]byte(stdout), &snap); err != nil {
		t.Fatalf("--json output does not unmarshal: %v (%q)", err, stdout)
	}
	var hasMarker bool
	for _, f := range snap.DirtyFiles {
		if strings.Contains(f, "bin_only.go") {
			hasMarker = true
		}
	}
	if !hasMarker {
		t.Errorf("binary telemetry must follow cmd.Dir: --json DirtyFiles must report the fixture marker bin_only.go, got %v", snap.DirtyFiles)
	}

	// The full card surfaces the same cwd marker in its WORKING TREE line.
	full, _, code := execRadar(t, home)
	if code != 0 {
		t.Fatalf("default mode exit %d", code)
	}
	if !strings.Contains(sgrStrip(full), "bin_only.go") {
		t.Errorf("full card must render the cwd fixture's dirty marker bin_only.go, got %q", full)
	}
}

// ---------------------------------------------------------------- Build contract (r3 F5 / F7, r4 F9)

func TestBuildInFlightContract(t *testing.T) {
	// (a) Do-now entry + matching in-progress card => InFlight populated.
	home := homeFixture(t, []cardSpec{
		{id: "1461", title: "Wire the radar scan loop", status: "in-progress", priority: "high"},
	}, deltaFixture)
	t.Chdir(filepath.Join(home, "Repositories", "kanban-zed"))
	snap, err := Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.InFlight == nil {
		t.Fatal("InFlight must be non-nil when Do-now card matches an in-progress kanban card")
	}
	if normID(snap.InFlight.ID) != "1461" {
		t.Errorf("InFlight.ID: want 1461, got %q", snap.InFlight.ID)
	}
	if snap.InFlight.Title != "Wire the radar scan loop" {
		t.Errorf("InFlight.Title must come from the card: %q", snap.InFlight.Title)
	}
	if snap.InFlight.Description != "Wire the radar scan loop" {
		t.Errorf("InFlight.Description comes from the card title: %q", snap.InFlight.Description)
	}
	if snap.InFlight.ContextNote != "" {
		t.Errorf("ContextNote must be empty, got %q", snap.InFlight.ContextNote)
	}
	// No git evidence references 1461 => StageDesign/15 default (r3 F7 iii).
	if snap.InFlight.Stage != StageDesign || snap.InFlight.ProgressPct != 15 {
		t.Errorf("default decoration: want StageDesign/15, got %v/%d", snap.InFlight.Stage, snap.InFlight.ProgressPct)
	}
	// Clean tree: ReEntryFile defaults to the card's ActiveCard.Path; ReEntryCmd empty.
	if !strings.HasSuffix(snap.InFlight.ReEntryFile, "1461-card.md") || strings.Contains(snap.InFlight.ReEntryFile, ":") {
		t.Errorf("clean-tree ReEntryFile must default to the card path, got %q", snap.InFlight.ReEntryFile)
	}
	if snap.InFlight.ReEntryCmd != "" {
		t.Errorf("clean-tree ReEntryCmd must be empty, got %q", snap.InFlight.ReEntryCmd)
	}
	// Queue is rows[1:].
	if len(snap.Queue) != 2 || normID(snap.Queue[0].ID) != "1388" || normID(snap.Queue[1].ID) != "1405" {
		t.Errorf("Queue must be rows[1:]: %+v", snap.Queue)
	}

	// 1606 D4 (i): the in-progress match also records the card-status field,
	// read through reflection (cardStatus) so the amended assertion compiles
	// at the RED base, where types.go does not yet carry the field.
	if got := cardStatus(t, snap); got != "in-progress" {
		t.Errorf("InFlightCardStatus: want in-progress (D4 i), got %q", got)
	}

	// (b) 1606 D4 (ii) amendment: the Do-now card at a non-in-progress status
	// still builds InFlight from that card through the decorateInFlight path;
	// the mismatch surfaces via InFlightCardStatus ("todo"). RED at base: v1
	// leaves InFlight nil here.
	home = homeFixture(t, []cardSpec{{id: "1461", title: "Wire the radar scan loop", status: "todo", priority: "high"}}, deltaFixture)
	t.Chdir(filepath.Join(home, "Repositories", "kanban-zed"))
	snap, err = Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.InFlight == nil {
		t.Errorf("(b): InFlight must be non-nil for the Do-now card at todo (D4 ii fallback)")
	} else {
		if normID(snap.InFlight.ID) != "1461" {
			t.Errorf("(b): card-sourced InFlight.ID: want 1461, got %q", snap.InFlight.ID)
		}
		if snap.InFlight.Title != "Wire the radar scan loop" {
			t.Errorf("(b): card-sourced InFlight.Title: want the card title, got %q", snap.InFlight.Title)
		}
		if !strings.HasSuffix(snap.InFlight.ReEntryFile, "1461-card.md") {
			t.Errorf("(b): clean-tree ReEntryFile must default to the card path (same decorateInFlight path), got %q", snap.InFlight.ReEntryFile)
		}
	}
	if got := cardStatus(t, snap); got != "todo" {
		t.Errorf("(b): InFlightCardStatus: want todo, got %q", got)
	}

	// (c) 1606 D4 (iii) amendment: no card matches the Do-now id at all =>
	// InFlight is built from the ROW itself (row id/text, StageDesign/15,
	// clean tree => empty ReEntryFile/ReEntryCmd: no card path to default
	// to). RED at base: v1 leaves InFlight nil here.
	orphan := strings.Replace(deltaFixture, "*kanban #1461*", "*kanban #9999*", 1)
	home = homeFixture(t, []cardSpec{{id: "1461", title: "Wire the radar scan loop", status: "in-progress", priority: "high"}}, orphan)
	t.Chdir(filepath.Join(home, "Repositories", "kanban-zed"))
	snap, err = Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.InFlight == nil {
		t.Errorf("(c): InFlight must be non-nil (row-sourced fallback, D4 iii)")
	} else {
		if normID(snap.InFlight.ID) != "9999" {
			t.Errorf("(c): row-sourced InFlight.ID: want 9999, got %q", snap.InFlight.ID)
		}
		if snap.InFlight.Title != "Wire the radar scan loop" || snap.InFlight.Description != "Wire the radar scan loop" {
			t.Errorf("(c): row-sourced Title/Description must equal the row text, got %q/%q", snap.InFlight.Title, snap.InFlight.Description)
		}
		if snap.InFlight.Stage != StageDesign || snap.InFlight.ProgressPct != 15 {
			t.Errorf("(c): want StageDesign/15, got %v/%d", snap.InFlight.Stage, snap.InFlight.ProgressPct)
		}
		if snap.InFlight.ReEntryFile != "" || snap.InFlight.ReEntryCmd != "" || snap.InFlight.ContextNote != "" {
			t.Errorf("(c): clean tree + no card => empty ReEntryFile/ReEntryCmd/ContextNote, got %q/%q/%q", snap.InFlight.ReEntryFile, snap.InFlight.ReEntryCmd, snap.InFlight.ContextNote)
		}
	}
	if got := cardStatus(t, snap); got != "" {
		t.Errorf("(c): InFlightCardStatus must stay empty with no card, got %q", got)
	}
}

func TestBuildStalledRules(t *testing.T) {
	cards := []cardSpec{
		{id: "1461", title: "Wire the radar scan loop", status: "in-progress", priority: "high"},
		{id: "1101", title: "Gate the manual orders", status: "blocked", priority: "high"},
		{id: "1102", title: "Review the shadow switch", status: "review", priority: "medium"},
		{id: "1103", title: "Unblock the merge gate", status: "blocked", priority: "medium"},
		{id: "1104", title: "Decide the exit rule", status: "review", priority: "low"},
	}
	var b strings.Builder
	b.WriteString("## Do now (deep-work WIP = 1)\n\n### → Wire the radar scan loop\n\n- [ ] `   476` Wire the radar scan loop — notes · *kanban #1461* · `in-progress`\n\n## Ranked — decisions\n")
	scores := []string{"457", "286", "140", "57"}
	for i, c := range cards[1:] {
		fmt.Fprintf(&b, "\n### %s — 1 item\n\n- [ ] `  %s` %s — *kanban #%s* · `todo`\n", scores[i], scores[i], c.title, c.id)
	}
	home := homeFixture(t, cards, b.String())
	snap, err := Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	wantOrder := []string{"1101", "1102", "1103", "1104"}
	if len(snap.Stalled) != 4 {
		t.Fatalf("Stalled: want 4 in ranked order, got %d: %+v", len(snap.Stalled), snap.Stalled)
	}
	for i, w := range wantOrder {
		if normID(snap.Stalled[i].ID) != w {
			t.Errorf("Stalled[%d]: want %s (Δ.md-score order), got %+v", i, w, snap.Stalled[i])
		}
	}

	// Cap at 4: six ranked stalled cards must truncate.
	cards = append(cards,
		cardSpec{id: "1105", title: "Stalled five", status: "blocked", priority: "low"},
		cardSpec{id: "1106", title: "Stalled six", status: "review", priority: "low"},
	)
	b.Reset()
	b.WriteString("## Do now (deep-work WIP = 1)\n\n### → Wire the radar scan loop\n\n- [ ] `   476` Wire the radar scan loop — notes · *kanban #1461* · `in-progress`\n\n## Ranked — decisions\n")
	for i, c := range cards[1:] {
		fmt.Fprintf(&b, "\n### %d — 1 item\n\n- [ ] `  %d` %s — *kanban #%s* · `todo`\n", 500-i*10, 500-i*10, c.title, c.id)
	}
	home = homeFixture(t, cards, b.String())
	snap, err = Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(snap.Stalled) != 4 {
		t.Errorf("Stalled must cap at 4, got %d: %+v", len(snap.Stalled), snap.Stalled)
	}
	if normID(snap.Stalled[0].ID) != "1101" {
		t.Errorf("cap must keep the top-ranked first, got %+v", snap.Stalled)
	}

	// No blocked/review cards => empty Stalled.
	home = homeFixture(t, []cardSpec{{id: "1461", title: "Wire the radar scan loop", status: "in-progress", priority: "high"}}, deltaFixture)
	snap, err = Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(snap.Stalled) != 0 {
		t.Errorf("Stalled must be empty when no card is blocked/review: %+v", snap.Stalled)
	}
}

func TestBuildStageDecoration(t *testing.T) {
	// (i) A Landed commit on the default branch referencing the card id
	// => StageVerify/70.
	home := homeFixture(t, []cardSpec{
		{id: "1461", title: "Wire the radar scan loop", status: "in-progress", priority: "high"},
	}, deltaFixture)
	repo := filepath.Join(home, "Repositories", "kanban-zed")
	runGit(t, home, repo, "commit", "--allow-empty", "-m", "feat(1461): wire the scan loop")
	t.Chdir(repo)
	snap, err := Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.InFlight == nil || snap.InFlight.Stage != StageVerify || snap.InFlight.ProgressPct != 70 {
		t.Errorf("default-branch evidence: want StageVerify/70, got %+v", snap.InFlight)
	}

	// (ii) A referencing commit reachable only from a side branch => StageBuild/50.
	home = homeFixture(t, []cardSpec{
		{id: "1461", title: "Wire the radar scan loop", status: "in-progress", priority: "high"},
	}, deltaFixture)
	repo = filepath.Join(home, "Repositories", "kanban-zed")
	runGit(t, home, repo, "checkout", "-b", "feature/scan-loop")
	runGit(t, home, repo, "commit", "--allow-empty", "-m", "wip(1461): scan loop on branch")
	runGit(t, home, repo, "checkout", "main")
	t.Chdir(repo)
	snap, err = Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.InFlight == nil || snap.InFlight.Stage != StageBuild || snap.InFlight.ProgressPct != 50 {
		t.Errorf("branch-only evidence: want StageBuild/50, got %+v", snap.InFlight)
	}
	// StageLand is never auto-derived in v1.
	if snap.InFlight.Stage == StageLand {
		t.Errorf("StageLand must never be auto-derived from git evidence")
	}
}

// TestBuildStageDecorationStaleBranchCommit pins spec row 11(ii) with no
// recency bound — audit r1 finding 1 (F7(ii) window drift). The ONLY commit
// referencing the card id is dated 3 days ago, outside the 24h window the
// implementation borrows from the Landed query, and must still decorate
// StageBuild/50. RED at 6b074b5 by design: branch evidence runs through the
// 24h cutoff, so the stale commit is dropped and StageDesign/15 leaks out.
// The builder removes that bound to turn this green.
func TestBuildStageDecorationStaleBranchCommit(t *testing.T) {
	home := homeFixture(t, []cardSpec{
		{id: "1461", title: "Wire the radar scan loop", status: "in-progress", priority: "high"},
	}, deltaFixture)
	repo := filepath.Join(home, "Repositories", "kanban-zed")
	runGit(t, home, repo, "checkout", "-b", "feature/scan-loop")
	stale := time.Now().Add(-72 * time.Hour).Format(time.RFC3339)
	cmd := exec.Command("git", "commit", "--allow-empty", "-m", "wip(1461): scan loop on branch")
	cmd.Dir = repo
	cmd.Env = append(gitEnv(home), "GIT_AUTHOR_DATE="+stale, "GIT_COMMITTER_DATE="+stale)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stale branch commit: %v: %s", err, out)
	}
	runGit(t, home, repo, "checkout", "main")
	t.Chdir(repo)
	snap, err := Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.InFlight == nil || snap.InFlight.Stage != StageBuild || snap.InFlight.ProgressPct != 50 {
		t.Errorf("stale (3-day-old) branch evidence must yield StageBuild/50 per spec 11(ii), got %+v", snap.InFlight)
	}
}

// TestBuildStageDecorationDeepHistoryBranchCommit pins spec row 11(ii) with no
// listing bound — audit r2 residual. The ONLY commit referencing the card id
// is the OLDEST of seven non-merge Zane branch commits (six unrelated fillers
// on top), i.e. 6 commits behind HEAD. The decoration evidence path still
// truncates to the newest 5 (parseLanded survivor cap), so the deep-history
// card-ref commit is dropped, idRefsCard misses, and StageDesign/15 leaks
// out. RED at 85a2977 by design; the builder lifts the cap to turn this green.
func TestBuildStageDecorationDeepHistoryBranchCommit(t *testing.T) {
	home := homeFixture(t, []cardSpec{
		{id: "1461", title: "Wire the radar scan loop", status: "in-progress", priority: "high"},
	}, deltaFixture)
	repo := filepath.Join(home, "Repositories", "kanban-zed")
	runGit(t, home, repo, "checkout", "-b", "feature/scan-loop")
	base := time.Now().Add(-10 * 24 * time.Hour).UTC()
	for i := 0; i < 7; i++ {
		msg := fmt.Sprintf("chore: unrelated filler commit %d", i+1)
		if i == 0 {
			// Oldest of the seven: the ONLY commit referencing the card id.
			msg = "wip(1461): scan loop on branch"
		}
		when := base.Add(time.Duration(i) * time.Hour).Format(time.RFC3339)
		cmd := exec.Command("git", "commit", "--allow-empty", "-m", msg)
		cmd.Dir = repo
		cmd.Env = append(gitEnv(home), "GIT_AUTHOR_DATE="+when, "GIT_COMMITTER_DATE="+when)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("branch commit %d: %v: %s", i+1, err, out)
		}
	}
	runGit(t, home, repo, "checkout", "main")
	t.Chdir(repo)
	snap, err := Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.InFlight == nil || snap.InFlight.Stage != StageBuild || snap.InFlight.ProgressPct != 50 {
		t.Errorf("deep-history branch evidence (card-ref commit 6 back) must yield StageBuild/50 per spec 11(ii), got %+v", snap.InFlight)
	}
}

func TestBuildDirtyAnchorAndReEntryCmd(t *testing.T) {
	home := homeFixture(t, []cardSpec{
		{id: "1461", title: "Wire the radar scan loop", status: "in-progress", priority: "high"},
	}, deltaFixture)
	repo := filepath.Join(home, "Repositories", "kanban-zed")
	foo := filepath.Join(repo, "cmd", "foo.go")
	if err := os.MkdirAll(filepath.Dir(foo), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foo, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, home, repo, "add", "-A")
	runGit(t, home, repo, "commit", "-m", "seed foo.go")
	if err := os.WriteFile(foo, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(repo)
	snap, err := Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.InFlight == nil {
		t.Fatal("InFlight must be present")
	}
	anchor := snap.InFlight.ReEntryFile
	if !strings.Contains(anchor, "foo.go:3") {
		t.Fatalf("dirty anchor must be path:line with the added line 3, got %q", anchor)
	}
	// r3 F7: ReEntryCmd is the fixed form "go test ./<dir-of-anchor>/ -count=1".
	// Audit r1 (minor): the expected directory is the fixture's known constant,
	// not a value re-derived from the anchor under test — a wrong-but-self-
	// consistent anchor path can no longer pass this assertion.
	const wantDir = "cmd" // known fixture layout: the dirty file is <repo>/cmd/foo.go
	wantCmd := fmt.Sprintf("go test ./%s/ -count=1", wantDir)
	if snap.InFlight.ReEntryCmd != wantCmd {
		t.Errorf("ReEntryCmd fixed form: want %q, got %q", wantCmd, snap.InFlight.ReEntryCmd)
	}
}

func TestBuildFilemodeOnlyDirtyTree(t *testing.T) {
	// r4 F9: dirty tree with zero @@ hunks (filemode-only change) =>
	// ReEntryFile empty and ReEntryCmd empty — never interpolated.
	home := homeFixture(t, []cardSpec{
		{id: "1461", title: "Wire the radar scan loop", status: "in-progress", priority: "high"},
	}, deltaFixture)
	repo := filepath.Join(home, "Repositories", "kanban-zed")
	foo := filepath.Join(repo, "cmd", "foo.go")
	if err := os.MkdirAll(filepath.Dir(foo), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foo, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, home, repo, "add", "-A")
	runGit(t, home, repo, "commit", "-m", "seed foo.go")
	if err := os.Chmod(foo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	snap, err := Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.InFlight == nil {
		t.Fatal("InFlight must be present")
	}
	if snap.InFlight.ReEntryFile != "" {
		t.Errorf("filemode-only dirty tree: ReEntryFile must be empty, got %q", snap.InFlight.ReEntryFile)
	}
	if snap.InFlight.ReEntryCmd != "" {
		t.Errorf("filemode-only dirty tree: ReEntryCmd must be empty, got %q", snap.InFlight.ReEntryCmd)
	}
}

// ---------------------------------------------------------------- AC6 / M7

func TestMakeBuildRadarTarget(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("make", "build-radar")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make build-radar: %v: %s (M7: target must exist and point at ./cmd/radar/)", err, out)
	}
	bin := filepath.Join(root, "radar")
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("make build-radar produced no ./radar: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("./radar is not executable: %v", info.Mode())
	}
	t.Cleanup(func() { _ = os.Remove(bin) })

	mk, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	phony := regexp.MustCompile(`(?m)^\.PHONY:.*\bbuild-radar\b`)
	if !phony.Match(mk) {
		t.Errorf("Makefile must declare build-radar .PHONY beside the build target")
	}
}

// ---------------------------------------------------------------- AC7 / M8

func TestSuitePresenceAndNoSkips(t *testing.T) {
	// M8 (partial silencing): the package must still collect this suite, and
	// no radar test may skip itself. Whole-file deletion is caught by the
	// parent's collect-count funnel against the recorded baseline.
	root := repoRoot(t)
	cmd := exec.Command("go", "test", "./internal/radar/", "-list", "^Test", "-run", "ZZZNEVERRUN")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test -list failed (package must compile once built): %v: %s", err, out)
	}
	listed := map[string]bool{}
	for _, ln := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(ln, "Test") {
			listed[ln] = true
		}
	}
	if len(listed) < 16 {
		t.Errorf("internal/radar must collect >=16 tests, got %d: %s", len(listed), out)
	}
	for _, name := range []string{
		"TestScanTasksFiltersDoneArchived",
		"TestCollectGitTelemetry",
		"TestCLIModes",
		"TestMakeBuildRadarTarget",
	} {
		if !listed[name] {
			t.Errorf("collector lost %s (M8 shape)", name)
		}
	}

	// Audit r1 (Important, M8 hole): the self-scan is restored. The needles
	// are built by concatenation so this scanner's own source never contains
	// the contiguous byte sequence it searches for — and every .go in the
	// package is scanned, radar_test.go included.
	skipNeedle := "t." + "Skip("
	shortNeedle := "testing." + "Short"
	entries, err := os.ReadDir(filepath.Join(root, "internal", "radar"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, "internal", "radar", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), skipNeedle) {
			t.Errorf("%s contains a skip call — skipped radar tests are forbidden (AC7)", e.Name())
		}
		if strings.Contains(string(src), shortNeedle) {
			t.Errorf("%s gates tests on a short-mode guard — silent exclusion is forbidden (AC7)", e.Name())
		}
	}
}

func TestSuiteHermeticity(t *testing.T) {
	// AC7: no radar source or test may hardcode an absolute user path; every
	// store read must resolve through the overridden HOME.
	root := repoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "internal", "radar"))
	if err != nil {
		t.Fatal(err)
	}
	// Audit r1 (Important, M8 hole): the self-scan is restored here too; the
	// needle is split so this file's own source cannot match it, and every
	// .go in the package is scanned, radar_test.go included.
	usersNeedle := "/Use" + "rs/"
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, "internal", "radar", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), usersNeedle) {
			t.Errorf("%s contains an absolute home-path literal — store paths must resolve via os.UserHomeDir()", e.Name())
		}
	}
}

// ----------------------------------------------------------------
// 1606 v1.1: D1-D4 RED pins (AC8-AC11; mutations M10-M13)
// ----------------------------------------------------------------

// The pins below are RED at 6a2536e by design: no width cap (D1), no -i
// author flags (D2), no 5-row display caps (D3), no Do-now fallback (D4).
// RadarSnapshot.InFlightCardStatus (D4's one new field) does not exist at
// base, so every v1.1 field assertion reads it by reflection (cardStatus) or
// from the rendered --json map (renderJSONCardStatus): compile-safe at the
// RED base (the untouched 20 tests keep their per-test verdicts), then
// real-value assertions once the builder lands types.go.

var sgrRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// sgrStrip removes ANSI SGR sequences - the AC8 measurement preprocess.
func sgrStrip(s string) string {
	return sgrRe.ReplaceAllString(s, "")
}

// visibleWidth counts the runes a line occupies after SGR stripping; every
// AC8 fixture row is ASCII, so runes equal displayed columns.
func visibleWidth(s string) int {
	return utf8.RuneCountInString(sgrStrip(s))
}

// maxLineWidth is the widest rendered line after SGR stripping.
func maxLineWidth(s string) int {
	widest := 0
	for _, ln := range strings.Split(s, "\n") {
		if w := visibleWidth(ln); w > widest {
			widest = w
		}
	}
	return widest
}

// cardStatus reads RadarSnapshot.InFlightCardStatus by reflection - the
// compile-safe seam; a missing field is an explicit test failure (the RED
// shape at base), never a package build error.
func cardStatus(t *testing.T, snap RadarSnapshot) string {
	t.Helper()
	v := reflect.ValueOf(snap).FieldByName("InFlightCardStatus")
	if !v.IsValid() {
		t.Errorf("RadarSnapshot.InFlightCardStatus field missing (1606 D4 unimplemented at base)")
		return ""
	}
	return v.String()
}

// renderJSONCardStatus reads InFlightCardStatus out of the rendered --json
// document (map-key access; same compile-safe seam as cardStatus).
func renderJSONCardStatus(t *testing.T, snap RadarSnapshot) string {
	t.Helper()
	out, err := RenderJSON(snap)
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("RenderJSON output is not a JSON object: %v (%s)", err, out)
	}
	s, ok := doc["InFlightCardStatus"].(string)
	if !ok {
		t.Errorf("RenderJSON output lacks an InFlightCardStatus string (1606 D4): %s", out)
		return ""
	}
	return s
}

// degradeErr builds the sentinel wrap the renderers accept, for D1's
// degrade-phrase exception cases.
func degradeErr() error {
	return fmt.Errorf("roadmap unavailable: %w", ErrRoadmapUnavailable)
}

// linesContain reports whether any one line fully contains the needle.
func linesContain(lines []string, needle string) bool {
	for _, ln := range lines {
		if strings.Contains(ln, needle) {
			return true
		}
	}
	return false
}

// TestRenderWidthCap pins AC8 (D1): every rendered line of both modes stays
// within min(termWidth, 100) visible columns (SGR-stripped rune count), with
// termWidth resolved PER RENDER CALL (the COLUMNS=60 case fails exactly when
// a resolver caches at init): COLUMNS=200 -> hard cap 100; COLUMNS=60 -> 60;
// COLUMNS unset (piped semantics) -> the default-100 bound; --summary stays
// exactly 3 lines under width clipping; the degrade phrase renders whole
// even at COLUMNS=20 (the greppability exclusion band), with every
// non-exempt line still bounded.
func TestRenderWidthCap(t *testing.T) {
	long := strings.Repeat("w", 160)
	snap := RadarSnapshot{
		Timestamp: time.Now(),
		InFlight:  &InFlightTask{ID: "1461", Title: long, Stage: StageDesign, ProgressPct: 15},
		Stalled:   []StalledDecision{{ID: "8001", Title: long, Blocker: "blocked"}},
		Queue:     []QueuedItem{{ID: "7001", Title: "q-row " + long, Priority: "476"}},
	}
	degraded := RadarSnapshot{Timestamp: time.Now()}

	// assertBounded pins both modes at the current COLUMNS value.
	assertBounded := func(bound int) {
		t.Helper()
		if w := maxLineWidth(RenderFull(snap, nil)); w > bound {
			t.Errorf("RenderFull widest line is %d visible cols > bound %d", w, bound)
		}
		sum := RenderSummary(snap, nil)
		lines := strings.Split(strings.TrimRight(sum, "\n"), "\n")
		if len(lines) != 3 {
			t.Errorf("RenderSummary must stay exactly 3 lines, got %d: %q", len(lines), sum)
		}
		for i, ln := range lines {
			if w := visibleWidth(ln); w > bound {
				t.Errorf("RenderSummary line %d is %d visible cols > bound %d: %q", i+1, w, bound, ln)
			}
		}
	}

	t.Setenv("COLUMNS", "200")
	assertBounded(100) // min(200, 100): the hard cap, not the env value

	// Per-call resolver pin: an init-cached width (100) fails exactly here.
	t.Setenv("COLUMNS", "60")
	assertBounded(60)

	// COLUMNS unset (piped semantics): present-but-unparseable would take the
	// TTY path, so the variable is removed for real; the bound is the piped
	// default 100. t.Setenv has registered the original value's restore.
	t.Setenv("COLUMNS", "unparseable")
	if err := os.Unsetenv("COLUMNS"); err != nil {
		t.Fatal(err)
	}
	assertBounded(100)

	// D1 exception, both modes: the degrade phrase stays contiguous even
	// where a strict clip would lose it (COLUMNS=20: content budget 14 < the
	// 33-col phrase), while every non-exempt line stays bounded.
	for _, w := range []int{40, 20} {
		t.Setenv("COLUMNS", strconv.Itoa(w))
		for mode, out := range map[string]string{
			"full":    RenderFull(degraded, degradeErr()),
			"summary": RenderSummary(degraded, degradeErr()),
		} {
			lines := strings.Split(out, "\n")
			whole := false
			for _, ln := range lines {
				if strings.Contains(ln, degradeMessage) {
					whole = true
					break
				}
			}
			if !whole {
				t.Errorf("[mode=%s COLUMNS=%d] degrade phrase split or lost - the greppability exception must keep it on one line: %q", mode, w, out)
			}
			for _, ln := range lines {
				if strings.Contains(ln, degradeMessage) {
					continue // the only exempt line class
				}
				if vw := visibleWidth(ln); vw > w {
					t.Errorf("[mode=%s COLUMNS=%d] non-exempt line is %d visible cols > bound %d: %q", mode, w, vw, w, ln)
				}
			}
		}
		sum := RenderSummary(degraded, degradeErr())
		if got := strings.Split(strings.TrimRight(sum, "\n"), "\n"); len(got) != 3 {
			t.Errorf("degraded --summary must stay 3 lines at COLUMNS=%d, got %d: %q", w, len(got), sum)
		}
	}
}

// TestCollectGitAuthorCaseInsensitive pins AC9 (D2): BOTH --author-filtered
// git log sites (CollectGit's Landed log and gitAllMessages' decoration log)
// pass -i, so --author=Zane matches any letter-case of the author name, and
// no other flag changes. RED at base: the case-sensitive filter leaves the
// lowercase-authored day structurally empty while DirtyFiles/ReEntryFile on
// the same fixture stay green (the live 13:55 CDT defect shape).
func TestCollectGitAuthorCaseInsensitive(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, home, repo, "init", "-b", "main")
	runGit(t, home, repo, "config", "user.name", "Zane")
	runGit(t, home, repo, "config", "user.email", "zane@fixture.invalid")

	// Commit 1: authored entirely lowercase, straight through the
	// GIT_AUTHOR_NAME env seam (the fixture never touches config user.name).
	if err := os.WriteFile(filepath.Join(repo, "cmd", "lower.go"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, home, repo, "add", "-A")
	commitAs(t, home, repo, "zanerobinson", "feat(1607): lowercase authored fresh work")

	// Commit 2: uppercase author control (TestCollectGitTelemetry's shape)
	// - must stay green before and after the -i add.
	if err := os.WriteFile(filepath.Join(repo, "control.txt"), []byte("control\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, home, repo, "add", "-A")
	commitAs(t, home, repo, "Zane", "fix(1607): uppercase authored control")

	// Dirty the tracked file: the DirtyFiles/ReEntryFile pair is the
	// isolation control that stays green on exactly the fixture whose Landed
	// assertion goes red (isolating the author filter as the killed mechanism).
	if err := os.WriteFile(filepath.Join(repo, "cmd", "lower.go"), []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := CollectGit(repo, time.Now())
	if err != nil {
		t.Fatalf("CollectGit: %v", err)
	}
	if len(got.DirtyFiles) != 1 || !strings.HasSuffix(got.DirtyFiles[0], "cmd/lower.go") {
		t.Errorf("isolation control: DirtyFiles must stay green on the lowercase fixture, got %v", got.DirtyFiles)
	}
	if got.ReEntryFile != "cmd/lower.go:3" {
		t.Errorf("isolation control: ReEntryFile must stay green, got %q", got.ReEntryFile)
	}
	if !landedHas(got.Landed, "lowercase authored fresh work") {
		t.Errorf("Landed must match the lowercase author zanerobinson via -i (RED at base: case-sensitive --author=Zane), got %+v", got.Landed)
	}
	if !landedHas(got.Landed, "uppercase authored control") {
		t.Errorf("Landed must keep matching uppercase Zane (control stays green verbatim), got %+v", got.Landed)
	}

	// The decoration site (gitAllMessages) needs -i too: branch evidence.
	msgs := gitAllMessages(repo)
	if !anyContains(msgs, "lowercase authored fresh work") {
		t.Errorf("gitAllMessages must match the lowercase author (second -i site), got %v", msgs)
	}
	if !anyContains(msgs, "uppercase authored control") {
		t.Errorf("gitAllMessages must keep matching uppercase Zane (control), got %v", msgs)
	}
}

// commitAs authors one fresh commit with an explicit GIT_AUTHOR identity.
func commitAs(t *testing.T, home, repo, author, subject string) {
	t.Helper()
	cmd := exec.Command("git", "commit", "-m", subject)
	cmd.Dir = repo
	cmd.Env = append(gitEnv(home),
		"GIT_AUTHOR_NAME="+author,
		"GIT_AUTHOR_EMAIL="+strings.ToLower(author)+"@fixture.invalid",
		"GIT_COMMITTER_NAME=Zane",
		"GIT_COMMITTER_EMAIL=zane@fixture.invalid",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit as %q: %v: %s", author, err, out)
	}
}

// landedHas reports whether any LandedEvidence message contains the needle.
func landedHas(landed []LandedEvidence, needle string) bool {
	for _, e := range landed {
		if strings.Contains(e.Message, needle) {
			return true
		}
	}
	return false
}

// anyContains reports whether any commit message contains the needle.
func anyContains(msgs []string, needle string) bool {
	for _, m := range msgs {
		if strings.Contains(m, needle) {
			return true
		}
	}
	return false
}

// TestRenderCapsAndMoreLine pins AC10 (D3): the queue and stalled sections
// render at most the first 5 rows plus a final '+N more (of M)' line; <=5
// items render no more-line; section headers keep the totals; --summary
// line 2 keeps totals (and 3 lines); --json stays uncapped. The snapshot is
// a hand-built RadarSnapshot literal, never through Build: Build caps
// Stalled at 4, so the 7-stalled fixture is only reachable as a literal
// (spec critic r1 F3).
func TestRenderCapsAndMoreLine(t *testing.T) {
	snap := RadarSnapshot{
		Timestamp: time.Now(),
		InFlight:  &InFlightTask{ID: "1461", Title: "Wire the radar scan loop", Stage: StageDesign, ProgressPct: 15},
		Stalled:   stalledFixture(7, 8001),
		Queue:     queueFixture(8, 7001),
	}
	full := sgrStrip(RenderFull(snap, nil))
	assertSectionWindow(t, full, "QUEUE (8)", seqIDs(8, 7001), 5, "+3 more (of 8)")
	assertSectionWindow(t, full, "STALLED (7)", seqIDs(7, 8001), 5, "+2 more (of 7)")

	// <=5 items: exact rows, no more-line (queue at the 5 boundary and at 2,
	// stalled at the 5 boundary).
	five := RadarSnapshot{
		Timestamp: time.Now(),
		InFlight:  &InFlightTask{ID: "1461", Title: "Wire the radar scan loop", Stage: StageDesign, ProgressPct: 15},
		Stalled:   stalledFixture(5, 8001),
		Queue:     queueFixture(5, 7001),
	}
	full5 := sgrStrip(RenderFull(five, nil))
	assertSectionFull(t, full5, "QUEUE (5)", seqIDs(5, 7001))
	assertSectionFull(t, full5, "STALLED (5)", seqIDs(5, 8001))

	two := RadarSnapshot{
		Timestamp: time.Now(),
		Queue:     queueFixture(2, 7001),
	}
	full2 := sgrStrip(RenderFull(two, nil))
	assertSectionFull(t, full2, "QUEUE (2)", seqIDs(2, 7001))

	// --summary keeps the totals (never the capped 5) and stays 3 lines.
	sum := RenderSummary(snap, nil)
	lines := strings.Split(strings.TrimRight(sum, "\n"), "\n")
	if len(lines) != 3 {
		t.Errorf("--summary must stay 3 lines, got %d: %q", len(lines), sum)
	}
	if lines[1] != "QUEUE 8 \u00b7 STALLED 7" {
		t.Errorf("--summary line 2 must keep the totals, want %q, got %q", "QUEUE 8 \u00b7 STALLED 7", lines[1])
	}

	// --json stays uncapped: render caps never mutate the snapshot.
	var jsnap RadarSnapshot
	out, err := RenderJSON(snap)
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	if err := json.Unmarshal(out, &jsnap); err != nil {
		t.Fatalf("--json unmarshal: %v", err)
	}
	if len(jsnap.Queue) != 8 || len(jsnap.Stalled) != 7 {
		t.Errorf("--json must stay uncapped (queue 8, stalled 7), got %d/%d", len(jsnap.Queue), len(jsnap.Stalled))
	}
}

// queueFixture builds n ranked QueuedItems with decimal ids from firstID.
func queueFixture(n, firstID int) []QueuedItem {
	items := make([]QueuedItem, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, QueuedItem{
			ID:       strconv.Itoa(firstID + i),
			Title:    fmt.Sprintf("Queue fixture row %d", i+1),
			Priority: strconv.Itoa(900 - i),
		})
	}
	return items
}

// stalledFixture builds n StalledDecisions with decimal ids from firstID.
func stalledFixture(n, firstID int) []StalledDecision {
	items := make([]StalledDecision, 0, n)
	blockers := []string{"blocked", "review"}
	for i := 0; i < n; i++ {
		items = append(items, StalledDecision{
			ID:      strconv.Itoa(firstID + i),
			Title:   fmt.Sprintf("Stalled fixture row %d", i+1),
			Blocker: blockers[i%len(blockers)],
		})
	}
	return items
}

// seqIDs is the id list firstID..firstID+n-1, in decimal.
func seqIDs(n, firstID int) []string {
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, strconv.Itoa(firstID+i))
	}
	return ids
}

// assertSectionWindow pins the D3 cap for one section: the header carries
// the total, exactly the first `shown` ids render as rows, later row bodies
// are absent, and the '+N more (of M)' line appears exactly once.
func assertSectionWindow(t *testing.T, out, header string, ids []string, shown int, moreLine string) {
	t.Helper()
	lines := strings.Split(out, "\n")
	if !linesContain(lines, header) {
		t.Errorf("section header %q missing (totals are unchanged by the cap)", header)
	}
	for i, id := range ids {
		hits := 0
		for _, ln := range lines {
			if strings.Contains(ln, "#"+id) {
				hits++
			}
		}
		if i < shown {
			if hits != 1 {
				t.Errorf("row %q must render exactly once within the first %d, found %d lines", id, shown, hits)
			}
			continue
		}
		if hits != 0 {
			t.Errorf("row %q sits beyond the display cap and must not render, found %d lines", id, hits)
		}
	}
	if n := strings.Count(out, moreLine); n != 1 {
		t.Errorf("more-line %q must appear exactly once, got %d", moreLine, n)
	}
}

// assertSectionFull pins an under-cap section: the total header, every row
// rendered exactly once, and no more-line at all.
func assertSectionFull(t *testing.T, out, header string, ids []string) {
	t.Helper()
	if !linesContain(strings.Split(out, "\n"), header) {
		t.Errorf("section header %q missing", header)
	}
	assertFullRows(t, out, ids)
	assertNoMoreLine(t, out)
}

// assertFullRows pins that every fixture id renders exactly once.
func assertFullRows(t *testing.T, out string, ids []string) {
	t.Helper()
	lines := strings.Split(out, "\n")
	for _, id := range ids {
		if !linesContain(lines, "#"+id) {
			t.Errorf("uncapped section must render row %q; missing from: %s", id, out)
		}
	}
}

// assertNoMoreLine fails when any '+N more (of M)' line appears.
func assertNoMoreLine(t *testing.T, out string) {
	t.Helper()
	re := regexp.MustCompile(`\+\d+ more \(of \d+\)`)
	if re.MatchString(out) {
		t.Errorf("sections with <=5 items must not render a more-line, found one in: %s", out)
	}
}

// headingEmptyDelta is the Do-now seed-gate input (spec critic r1 F1 / R2-1):
// the '## Do now' heading matches but ZERO task lines parse under it, so
// rows[0] is a band row (doNowSeeded=false) whose id here matches an
// in-progress card - the promotion v1 wrongly fires and v1.1 must gate.
const headingEmptyDelta = "# Roadmap\n\n" +
	"## Do now (deep-work WIP = 1)\n\n" +
	"## Ranked \u2014 decisions\n\n" +
	"### 457 \u2014 1 item\n\n" +
	"- [ ] `   457` Ship the Solarized render pass \u2014 *kanban #1461* \u00b7 `medium/hours`\n"

// TestBuildDoNowFallbackCardStatus pins AC11 (D4) end to end: the Do-now row
// with a matching card at 'backlog' builds InFlight from the card with
// InFlightCardStatus "backlog" plus both render markers (full-card
// ' [card: backlog]', summary line 1 ' STATUS?' within the 3-line
// contract); the in-progress match sets "in-progress" with no markers; the
// heading-present-empty roadmap leaves InFlight nil with the band row still
// in Queue (never promoted, even though its id matches the in-progress
// card); the band-only roadmap keeps InFlight nil with a non-nil empty
// Queue whose --json renders the literal [] (never null).
func TestBuildDoNowFallbackCardStatus(t *testing.T) {
	// (1) Do-now row + card at 'backlog' => card-sourced InFlight + markers.
	home := homeFixture(t, []cardSpec{
		{id: "1461", title: "Wire the radar scan loop", status: "backlog", priority: "high"},
	}, deltaFixture)
	t.Chdir(filepath.Join(home, "Repositories", "kanban-zed"))
	snap, err := Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.InFlight == nil {
		t.Errorf("backlog card + Do-now row: InFlight must be non-nil (D4 ii fallback; RED at base: v1 leaves nil)")
	} else {
		if normID(snap.InFlight.ID) != "1461" {
			t.Errorf("card-sourced InFlight.ID: want 1461, got %q", snap.InFlight.ID)
		}
		if snap.InFlight.Title != "Wire the radar scan loop" {
			t.Errorf("card-sourced InFlight.Title: want the card title, got %q", snap.InFlight.Title)
		}
		if !strings.HasSuffix(snap.InFlight.ReEntryFile, "1461-card.md") {
			t.Errorf("clean-tree ReEntryFile must default to the card path (decorateInFlight), got %q", snap.InFlight.ReEntryFile)
		}
	}
	if got := cardStatus(t, snap); got != "backlog" {
		t.Errorf("backlog card + Do-now row: InFlightCardStatus want backlog, got %q", got)
	}
	if !strings.Contains(sgrStrip(RenderFull(snap, nil)), " [card: backlog]") {
		t.Errorf("full card active line must carry the [card: backlog] marker, got %q", RenderFull(snap, nil))
	}
	sum := RenderSummary(snap, nil)
	sumLines := strings.Split(strings.TrimRight(sum, "\n"), "\n")
	if len(sumLines) != 3 {
		t.Errorf("summary must stay exactly 3 lines with the mismatch marker, got %d: %q", len(sumLines), sum)
	}
	if !strings.Contains(sgrStrip(sumLines[0]), " STATUS?") {
		t.Errorf("summary line 1 must carry the STATUS? marker, got %q", sumLines[0])
	}
	if linesContain(sumLines[1:], "STATUS?") {
		t.Errorf("STATUS? must stay on summary line 1, leaked into: %v", sumLines[1:])
	}
	// Seeded => Queue is rows[1:]: band rows stay, the Do-now row leaves.
	if len(snap.Queue) != 2 || normID(snap.Queue[0].ID) != "1388" || normID(snap.Queue[1].ID) != "1405" {
		t.Errorf("seeded Queue split: want rows[1:] = [1388 1405], got %+v", snap.Queue)
	}
	if got := renderJSONCardStatus(t, snap); got != "backlog" {
		t.Errorf("--json InFlightCardStatus want backlog, got %q", got)
	}

	// (2) in-progress match => field "in-progress", no markers anywhere.
	home = homeFixture(t, []cardSpec{
		{id: "1461", title: "Wire the radar scan loop", status: "in-progress", priority: "high"},
	}, deltaFixture)
	t.Chdir(filepath.Join(home, "Repositories", "kanban-zed"))
	snap, err = Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := cardStatus(t, snap); got != "in-progress" {
		t.Errorf("in-progress match: InFlightCardStatus want in-progress, got %q", got)
	}
	if full := sgrStrip(RenderFull(snap, nil)); strings.Contains(full, "[card:") || strings.Contains(full, "STATUS?") {
		t.Errorf("in-progress match must render no markers in the full card, got %q", full)
	}
	if sum := RenderSummary(snap, nil); strings.Contains(sum, "STATUS?") {
		t.Errorf("in-progress match must not surface STATUS? in the summary, got %q", sum)
	}

	// (3) heading-present-empty roadmap: Do-now heading matched, zero task
	// lines => InFlight nil, band rows in Queue, NO ACTIVE WORK in both
	// modes (never promoted, even with the matching in-progress card).
	home = homeFixture(t, []cardSpec{
		{id: "1461", title: "Wire the radar scan loop", status: "in-progress", priority: "high"},
	}, headingEmptyDelta)
	snap, err = Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.InFlight != nil {
		t.Errorf("heading-present-empty: band row #1461 must never promote to InFlight (RED at base: v1 promotes it), got %+v", snap.InFlight)
	}
	if got := cardStatus(t, snap); got != "" {
		t.Errorf("heading-present-empty: InFlightCardStatus must be empty, got %q", got)
	}
	if len(snap.Queue) != 1 || normID(snap.Queue[0].ID) != "1461" {
		t.Errorf("heading-present-empty: the band rows must land in Queue (never dropped), want [1461], got %+v", snap.Queue)
	}
	for mode, out := range map[string]string{
		"full":    RenderFull(snap, nil),
		"summary": RenderSummary(snap, nil),
	} {
		if !strings.Contains(sgrStrip(out), "NO ACTIVE WORK") {
			t.Errorf("heading-present-empty: %s mode must render NO ACTIVE WORK, got %q", mode, out)
		}
	}

	// (4) band-only roadmap: InFlight nil; Queue the empty non-nil
	// []QueuedItem{} whose --json keeps the literal [] (never null).
	home = homeFixture(t, []cardSpec{
		{id: "1388", title: "Ship the Solarized render pass", status: "todo", priority: "high"},
	}, bandOnlyDeltaFixture)
	snap, err = Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.InFlight != nil {
		t.Errorf("band-only: InFlight must stay nil, got %+v", snap.InFlight)
	}
	if !reflect.DeepEqual(snap.Queue, []QueuedItem{}) {
		t.Errorf("band-only: Queue must be the empty non-nil []QueuedItem{} (a nil slice must fail), got %#v", snap.Queue)
	}
	out, err := RenderJSON(snap)
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	if !strings.Contains(string(out), `"Queue":[]`) {
		t.Errorf("band-only: --json must keep the literal [] for Queue (never null), got %s", out)
	}
}

// ----------------------------------------------------------------
// 1609 v1.2: AC12/AC13 RED pin (cwd telemetry; mutations M20/M21)
// ----------------------------------------------------------------

// TestRadarTelemetryFollowsCwd pins AC12 + AC13 in one function: after the
// process cwd is set to an INDEPENDENT repo whose dirty file is cwd_only.go
// and whose fresh default-branch commit references 1461, Build's git
// telemetry must describe the cwd repo (DirtyFiles, the ReEntryFile anchor,
// and the Landed evidence that decorates StageVerify) while the card scan
// still reads the HOME store (InFlight.Title + the blocked 1101 in Stalled).
//
// RED at fee1c44: v1.1 hardcodes telemetry to home/Repositories/kanban-zed,
// so this reports the store's store_only.go and a StageDesign/15 leak.
//
// Negative-lookahead-safe: a POSITIVE cwd marker (cwd_only.go present) is
// paired with a NEGATIVE store marker (store_only.go absent) over two
// structurally distinct filenames, so no single substring can satisfy the
// pin vacuously. M20 (telemetry re-hardcoded to the store) reddens exactly
// the cwd clauses; M21 (card scan moved to cwd) reddens the Stalled/title
// clauses.
func TestRadarTelemetryFollowsCwd(t *testing.T) {
	// HOME fixture: the card store. Card 1461 in-progress drives InFlight;
	// card 1101 blocked is the card-scan probe. The store tree is dirtied
	// with store_only.go so a store-telemetry regression is observable.
	home := homeFixture(t, []cardSpec{
		{id: "1461", title: "Wire the radar scan loop", status: "in-progress", priority: "high"},
		{id: "1101", title: "Gate the manual orders", status: "blocked", priority: "high"},
	}, deltaFixture)
	storeRepo := filepath.Join(home, "Repositories", "kanban-zed")
	if err := os.WriteFile(filepath.Join(storeRepo, "store_only.go"), []byte("store\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second, independent repo; its root becomes the process working dir.
	cwdRepo := cwdRepoFixture(t)
	t.Chdir(cwdRepo)

	snap, err := Build(home)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// AC12 (telemetry followed cwd): the dirty list, the re-entry anchor, and
	// the Landed evidence that decorates Stage all come from the cwd repo.
	var hasCwd, hasStore bool
	for _, f := range snap.DirtyFiles {
		if strings.Contains(f, "cwd_only.go") {
			hasCwd = true
		}
		if strings.Contains(f, "store_only.go") {
			hasStore = true
		}
	}
	if !hasCwd {
		t.Errorf("AC12: DirtyFiles must describe the cwd repo (cwd_only.go), got %v", snap.DirtyFiles)
	}
	if hasStore {
		t.Errorf("AC12: DirtyFiles must NOT describe the store (store_only.go) under cwd telemetry, got %v", snap.DirtyFiles)
	}
	if len(snap.DirtyFiles) == 0 {
		t.Errorf("AC12: DirtyFiles must be non-empty - the cwd tree, not the store, is described")
	}
	if snap.InFlight == nil {
		t.Fatalf("AC12/AC13: InFlight must be present for the HOME in-progress card 1461")
	}
	if snap.InFlight.Stage != StageVerify || snap.InFlight.ProgressPct != 70 {
		t.Errorf("AC12: Stage must be StageVerify/70 from the cwd repo's fresh commit referencing 1461, got %v/%d", snap.InFlight.Stage, snap.InFlight.ProgressPct)
	}
	if !strings.Contains(snap.InFlight.ReEntryFile, "cwd_only.go:") {
		t.Errorf("AC12: ReEntryFile must anchor to the cwd tree (contains cwd_only.go:), got %q", snap.InFlight.ReEntryFile)
	}

	// AC13 (the card scan did NOT move): the card-sourced fields still come
	// from the HOME store; a cwd card scan finds no tasks/ and yields zero.
	if snap.InFlight.Title != "Wire the radar scan loop" {
		t.Errorf("AC13: InFlight.Title must come from the HOME card 1461, got %q", snap.InFlight.Title)
	}
	if len(snap.Stalled) != 1 || normID(snap.Stalled[0].ID) != "1101" {
		t.Errorf("AC13: card scan must still read the HOME store (exactly blocked 1101 in Stalled), got %+v", snap.Stalled)
	}

	// AC12 store-fallback clause + seam restore: with os.Getwd failing,
	// telemetryDir must return the store path exactly. The seam is restored
	// via t.Cleanup so no later order-dependent test inherits a broken getwd.
	// (Compile note: getwd/telemetryDir are the builder's step-2 symbols; this
	// assertion is red-until-then by design, per 1602's RED-first convention.)
	t.Cleanup(func() { getwd = os.Getwd })
	getwd = func() (string, error) { return "", errors.New("getwd forced failure (1609 test seam)") }
	if got, want := telemetryDir(home), filepath.Join(home, filepath.FromSlash(repoRelPath)); got != want {
		t.Errorf("AC12 fallback: telemetryDir with Getwd error must return the store path exactly, want %q, got %q", want, got)
	}
}
