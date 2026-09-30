// Package closer_test is the RED acceptance suite for card 1607 (EF suite v1,
// part 2): the closer last-mile ladder over the radar substrate.
//
// Contract: quorum-test-generator writes tests only. At base 16b2990 neither
// internal/closer nor cmd/closer exists, so this suite is red by design until
// quorum-builder lands classify.go / render.go / cmd/closer/main.go.
//
// Brief ids -> test names (AC4-AC8, M16-M19):
//
//	TC1 TestCloserIntegratedRequiresAncestry   (AC4, M16: ancestry, never a branch name)
//	TC2 TestCloserWrittenRung                  (AC4 written: dirty tree + unmerged branch)
//	TC3 TestCloserRunsRungDoubleGate           (AC5, M17: runs needs verify: AND --verify)
//	TC4 TestCloserConsumedIsNeverClaimed       (AC6, M18: the N/A literal, never assigned)
//	TC5 TestCloserOneLastMilePerItem           (AC7: exactly one action, one of five forms)
//	TC6 TestCloserReadOnlyRepoInvariant        (AC8, M19: .git digest + repo-root cleanliness)
//	TC7 TestCloserCapAndMoreLine               (AC7: cap 5 + "+N more (of M)"; --json uncapped)
//	    TestCloserMakeBuildTargets             (AC8: build wiring)
//
// Fixture design: each card's referenced files are chosen so its rung is
// unambiguous — a card whose files live only on an unmerged branch is `written`
// (merge), a card with a dirty tracked file is `written` (commit), a card whose
// files are merged AND dirty is ambiguous, so the fixtures never mix those two
// triggers on one card. `integrated` requires the referenced file's content to
// be an ancestor of the default branch AND clean in the tree.
//
// Hermeticity: every fixture repo and HOME lives under t.TempDir(); HOME is
// overridden for every exec; verify: fixtures are temp-only commands (a
// pre-created temp path, or the builtin `false`) — nothing a fixture runs writes
// into the repo. No test reads the live kanban-zed / Δ.md stores.
package closer_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// ---------------------------------------------------------------- scaffolding

func repoRootPath() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller failed")
	}
	// <root>/internal/closer/closer_test.go -> <root>
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..")), nil
}

var closerBin = sync.OnceValues(func() (string, error) {
	root, err := repoRootPath()
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "closer-bin-*")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "closer")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/closer")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build ./cmd/closer: %v: %s", err, out)
	}
	return bin, nil
})

// gitEnv scrubs GIT_* leakage and pins HOME to the fixture.
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

// execCloser runs the closer binary with cmd.Dir=repo (the repo whose default
// branch the ladder resolves) and HOME=home (the kanban/Δ.md store).
func execCloser(t *testing.T, home, repo string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	bin, err := closerBin()
	if err != nil {
		t.Fatalf("cmd/closer not buildable (builder must add cmd/closer/main.go): %v", err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = repo
	cmd.Env = gitEnv(home)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	code = 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("exec closer %v: %v", args, err)
	}
	return out.String(), errb.String(), code
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
}

// closerCard is one fixture card: frontmatter fields plus the optional
// verify: / wire: / files: lines the ladder reads.
type closerCard struct {
	id, title, status string
	verify            string // omit for a card with no verify: field
	wire              string // omit for a card with no wire: field
	files             []string
}

// writeCard writes a kanban card whose frontmatter mirrors the live store's
// shape (id/title/status/priority/created) plus the closer-specific fields.
func writeCard(t *testing.T, tasksDir string, c closerCard) string {
	t.Helper()
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	status := c.status
	if status == "" {
		status = "in-progress"
	}
	name := filepath.Join(tasksDir, c.id+"-card.md")
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\ntitle: %s\nstatus: %s\npriority: high\n", c.id, c.title, status)
	b.WriteString("created: 2026-09-30T10:00:00-05:00\n")
	if c.verify != "" {
		fmt.Fprintf(&b, "verify: %s\n", c.verify)
	}
	if c.wire != "" {
		fmt.Fprintf(&b, "wire: %s\n", c.wire)
	}
	if len(c.files) > 0 {
		fmt.Fprintf(&b, "files: [%s]\n", strings.Join(c.files, ", "))
	}
	b.WriteString("---\n")
	b.WriteString("Body prose. status: done mentioned here must not filter this card.\n")
	if err := os.WriteFile(name, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return name
}

// fixture is a hermetic HOME + git repo whose kanban tasks dir is inside the
// repo, so the ladder's default-branch ancestry and the store share one repo.
type fixture struct {
	home, repo, tasks string
}

// fixtureIDs is the eight-card baseline set (used by the cap/uncapped pins).
var fixtureIDs = []string{"9001", "9002", "9003", "9004", "9005", "9006", "9007", "9008"}

// knownIDs are every card id the fixtures can render, so the output parser
// never mistakes a timestamp or count for an item id.
var knownIDs = map[string]bool{
	"9001": true, "9002": true, "9003": true, "9004": true,
	"9005": true, "9006": true, "9007": true, "9008": true,
	"9100": true, "9101": true, "9102": true, "9201": true,
}

// newFixture builds the repo on a default branch named trunk (never "main", so
// a hardcoded main fails), three landed files, eight mixed-rung cards, a
// roadmap row, and two branches (one merged, one not).
func newFixture(t *testing.T) fixture {
	t.Helper()
	home := t.TempDir()
	repo := filepath.Join(home, "Repositories", "kanban-zed")
	tasks := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, home, repo, "init", "-b", "trunk")
	runGit(t, home, repo, "config", "user.name", "Zane")
	runGit(t, home, repo, "config", "user.email", "zane@fixture.invalid")

	// The landed files the integrated cards reference (committed on trunk).
	for _, p := range []string{"internal/guard/guard.go", "internal/radar/export.go"} {
		writeFile(t, filepath.Join(repo, p), "package fixture // landed\n")
	}
	// A file whose only commits live on an unmerged branch (written: merge).
	writeFile(t, filepath.Join(repo, "internal/remote/remote.go"), "package fixture // seed\n")
	// The dirty target's committed baseline (dirtied again AFTER the cards are
	// committed, so the dirty change is never swallowed by a fixture commit).
	writeFile(t, filepath.Join(repo, "internal/dirty/dirty.go"), "package fixture // committed baseline\n")
	// The roadmap (a roadmap row carries no path, hence no verify: field).
	writeFile(t, filepath.Join(home, "Obsidian", "Roadmap", "Δ.md"),
		"# Roadmap\n\n## Do now\n\n### 9100 — 1 item\n\n- [ ] `   9100` Wire the closer render — *kanban #9100* · notes.\n")

	runGit(t, home, repo, "add", "-A")
	runGit(t, home, repo, "commit", "-m", "seed: fixture baseline")
	head := strings.TrimSpace(runGit(t, home, repo, "rev-parse", "HEAD"))
	runGit(t, home, repo, "update-ref", "refs/remotes/origin/trunk", head)
	runGit(t, home, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")

	// (b) an unmerged branch: feature/9002 lands a commit trunk never saw, so
	// the file it touches exists ONLY as unmerged history.
	runGit(t, home, repo, "checkout", "-qb", "feature/9002")
	writeFile(t, filepath.Join(repo, "internal/remote/remote.go"), "package fixture // feature/9002 landed here\n")
	runGit(t, home, repo, "add", "-A")
	runGit(t, home, repo, "commit", "-m", "feat(9002): remote wiring, never merged")
	runGit(t, home, repo, "checkout", "-q", "trunk")

	// (c) a name-only branch: feature/1461 is unmerged and references nothing
	// the cards point at — a branch name is never evidence.
	runGit(t, home, repo, "checkout", "-qb", "feature/1461")
	writeFile(t, filepath.Join(repo, "internal/nameonly/nameonly.go"), "package fixture // name-only\n")
	runGit(t, home, repo, "add", "-A")
	runGit(t, home, repo, "commit", "-m", "feat(1461): name only, never merged")
	runGit(t, home, repo, "checkout", "-q", "trunk")

	// Eight cards covering every rung the ladder can honestly produce. Every
	// referenced path is unique to one card except the landed files shared by
	// the integrated cards (which are clean, so they stay unambiguous).
	for _, c := range []closerCard{
		{id: "9001", title: "guard state reader", files: []string{"internal/guard/guard.go"}},
		{id: "9002", title: "remote wiring", files: []string{"internal/remote/remote.go"}},
		{id: "9003", title: "dirty ref", files: []string{"internal/dirty/dirty.go"}},
		{id: "9004", title: "radar wrappers", files: []string{"internal/radar/export.go"}},
		{id: "9005", title: "absent artifact", files: []string{"no-such/absent.go"}},
		{id: "9006", title: "bridge holder", files: []string{"internal/guard/guard.go"}, wire: "internal/closer/closer.go"},
		{id: "9007", title: "plain holder", files: []string{"internal/guard/guard.go"}},
		{id: "9008", title: "no probe field", files: []string{"no-such/absent-9008.go"}},
	} {
		writeCard(t, tasks, c)
	}
	runGit(t, home, repo, "add", "-A")
	runGit(t, home, repo, "commit", "-m", "cards: mixed ladder fixture")

	// (d) a dirty tracked file (the written-by-dirty rung) — applied LAST, never
	// committed, and kept separate from every integrated card's files so no card
	// mixes the two triggers.
	appendFile(t, filepath.Join(repo, "internal/dirty/dirty.go"), "// uncommitted work in progress\n")

	return fixture{home: home, repo: repo, tasks: tasks}
}

// cleanFixture is newFixture with the dirty file restored, for the read-only
// pin where the repo root must be byte-stable across a run.
func cleanFixture(t *testing.T) fixture {
	t.Helper()
	fx := newFixture(t)
	runGit(t, fx.home, fx.repo, "checkout", "-q", "--", ".")
	return fx
}

// ---------------------------------------------------------------- output parsing

type renderedItem struct {
	id, title, rung, action string
}

var (
	moreLineRe  = regexp.MustCompile(`^\+\s*(\d+)\s+more\s+\(of\s+(\d+)\)\s*$`)
	wordIDRe    = regexp.MustCompile(`\b\d{4,6}\b`)
	rungOrder   = []string{"untested", "integrated", "written", "runs", "consumed"}
	wireRe      = regexp.MustCompile(`\bwire\s+(\S+)`)
	mergeRe     = regexp.MustCompile(`\bmerge\s+(\S+)`)
	verifyActRe = regexp.MustCompile(`\bverify:\s*(.+?)\s*$`)
	commitRe    = regexp.MustCompile(`\bcommit\b`)
	consumedAct = regexp.MustCompile(`\bconsumed:\s*N/A\b`)
)

// parseRendered splits closer's default output into item lines and the overflow
// line. An item line is a line carrying one of the fixture ids.
func parseRendered(t *testing.T, out string) (items []renderedItem, more []string, moreN, moreM int) {
	t.Helper()
	for _, raw := range strings.Split(out, "\n") {
		ln := strings.TrimSpace(ansi.Strip(raw))
		if ln == "" {
			continue
		}
		if m := moreLineRe.FindStringSubmatch(ln); m != nil {
			more = append(more, ln)
			moreN, _ = strconv.Atoi(m[1])
			moreM, _ = strconv.Atoi(m[2])
			continue
		}
		id := ""
		for _, cand := range wordIDRe.FindAllString(ln, -1) {
			if knownIDs[cand] {
				id = cand
				break
			}
		}
		if id == "" {
			continue // header / count / provenance line, not an item row
		}
		it := renderedItem{id: id}
		// The legal last-mile literal "consumed: N/A" carries the word
		// "consumed"; mask it before rung detection so only a genuine consumed
		// rung (a bare cell, never the literal) trips TC4.
		rungScan := consumedAct.ReplaceAllString(ln, "@@NA@@")
		best, bestIdx := "", -1
		for _, r := range rungOrder {
			if idx := strings.Index(rungScan, r); idx >= 0 && (bestIdx < 0 || idx < bestIdx) {
				best, bestIdx = r, idx
			}
		}
		it.rung = best

		var kinds []string
		if m := consumedAct.FindString(ln); m != "" {
			kinds = append(kinds, "consumed")
			it.action = m
		}
		if m := verifyActRe.FindStringSubmatch(ln); m != nil {
			kinds = append(kinds, "verify")
			it.action = "verify: " + strings.TrimSpace(m[1])
		}
		if m := mergeRe.FindStringSubmatch(ln); m != nil {
			kinds = append(kinds, "merge")
			if it.action == "" {
				it.action = "merge " + m[1]
			}
		}
		if m := wireRe.FindStringSubmatch(ln); m != nil {
			kinds = append(kinds, "wire")
			if it.action == "" {
				it.action = "wire " + m[1]
			}
		}
		if commitRe.MatchString(ln) {
			kinds = append(kinds, "commit")
			if it.action == "" {
				it.action = "commit"
			}
		}
		if len(kinds) > 1 {
			t.Errorf("rendered item line for %s carries %d last-mile actions (%v) — exactly one is required: %q", id, len(kinds), kinds, ln)
		}
		items = append(items, it)
	}
	return items, more, moreN, moreM
}

func itemByID(items []renderedItem, id string) (renderedItem, bool) {
	for _, it := range items {
		if it.id == id {
			return it, true
		}
	}
	return renderedItem{}, false
}

// ---------------------------------------------------------------- json helpers

type jsonItem struct {
	ID, Title, Rung, LastMile string
}

// closerJSON decodes the --json document into the top-level map and its items.
func closerJSON(t *testing.T, raw []byte) (map[string]json.RawMessage, []jsonItem) {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("closer --json is not an object: %v\nraw: %s", err, raw)
	}
	var itemsRaw json.RawMessage
	for k, v := range top {
		if strings.EqualFold(k, "items") {
			itemsRaw = v
		}
	}
	if itemsRaw == nil {
		t.Fatalf("closer --json is missing the Items array; keys: %v", keysOf(top))
	}
	var rawItems []map[string]json.RawMessage
	if err := json.Unmarshal(itemsRaw, &rawItems); err != nil {
		t.Fatalf("closer --json Items is not an array of objects: %v", err)
	}
	var out []jsonItem
	for _, ri := range rawItems {
		var it jsonItem
		for k, v := range ri {
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				continue
			}
			switch strings.ToLower(strings.ReplaceAll(k, "_", "")) {
			case "id":
				it.ID = s
			case "title":
				it.Title = s
			case "rung":
				it.Rung = s
			case "lastmile", "next", "action":
				it.LastMile = s
			}
		}
		out = append(out, it)
	}
	return top, out
}

func jsonItemByID(items []jsonItem, id string) (jsonItem, bool) {
	for _, it := range items {
		if it.ID == id {
			return it, true
		}
	}
	return jsonItem{}, false
}

func keysOf(m map[string]json.RawMessage) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// mustJSON collects the uncapped --json item list for a fixture.
func mustJSON(t *testing.T, fx fixture) []jsonItem {
	t.Helper()
	out, stderr, code := execCloser(t, fx.home, fx.repo, "--json")
	if code != 0 {
		t.Fatalf("closer --json exit %d (stderr %q), want 0", code, stderr)
	}
	_, items := closerJSON(t, []byte(out))
	return items
}

// ---------------------------------------------------------------- digest / scanning

// gitTreeDigest hashes every path and byte under <repo>/.git, so a mutating git
// call or a stray write shows up as a different digest.
func gitTreeDigest(t *testing.T, repo string) string {
	t.Helper()
	root := filepath.Join(repo, ".git")
	h := sha256.New()
	var paths []string
	if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		paths = append(paths, rel)
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		p := filepath.Join(root, rel)
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		fmt.Fprintf(h, "%s\t%d\t", rel, info.Mode())
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("read %s: %v", p, err)
			}
			h.Write(b)
		}
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// repoPaths lists every path under the repo root (excluding .git), so a run
// that leaves a new untracked path is caught even when git status is stable.
func repoPaths(t *testing.T, repo string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	if err := filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == filepath.Join(repo, ".git") {
			return fs.SkipDir
		}
		rel, rerr := filepath.Rel(repo, p)
		if rerr != nil {
			return rerr
		}
		out[rel] = true
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", repo, err)
	}
	return out
}

func porcelain(t *testing.T, home, repo string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, home, repo, "status", "--porcelain"))
}

// ---------------------------------------------------------------- TC1 / AC4 / M16

// TestCloserIntegratedRequiresAncestry pins AC4 (and M16): integrated comes
// only from "git merge-base --is-ancestor" — a merged branch is integrated; an
// unmerged branch and a name-only branch are written, never integrated; a
// non-repo degrades to untested with exit 0.
func TestCloserIntegratedRequiresAncestry(t *testing.T) {
	fx := newFixture(t)

	stdout, stderr, code := execCloser(t, fx.home, fx.repo, "--json")
	if code != 0 {
		t.Fatalf("closer --json exit %d (stderr %q), want 0", code, stderr)
	}
	_, items := closerJSON(t, []byte(stdout))

	// (a) a card whose referenced file is committed on the default branch.
	if it, ok := jsonItemByID(items, "9001"); !ok {
		t.Errorf("card 9001 (landed file) missing from --json items")
	} else if it.Rung != "integrated" {
		t.Errorf("card 9001: rung %q, want %q (internal/guard/guard.go is an ancestor of the default branch)", it.Rung, "integrated")
	}
	// (b) the unmerged branch is written (merge), never integrated.
	if it, ok := jsonItemByID(items, "9002"); !ok {
		t.Errorf("card 9002 (unmerged branch) missing from --json items")
	} else if it.Rung == "integrated" {
		t.Errorf("card 9002: rung %q, but feature/9002 is NOT an ancestor of the default branch — a branch name is not evidence", it.Rung)
	} else if it.Rung != "written" {
		t.Errorf("card 9002: rung %q, want %q (commits on a branch not merged to default)", it.Rung, "written")
	}

	// (d) a directory that is not a git repo: untested, exit 0, no stderr.
	nonRepo := t.TempDir()
	nrOut, nrErr, nrCode := execCloser(t, fx.home, nonRepo, "--json")
	if nrCode != 0 {
		t.Errorf("non-repo run: exit %d (stderr %q), want 0 (the 2>/dev/null || true seam)", nrCode, nrErr)
	}
	if nrErr != "" {
		t.Errorf("non-repo run wrote to stderr: %q", nrErr)
	}
	if strings.Contains(nrOut, "integrated") {
		t.Errorf("non-repo run reported integrated: %s", nrOut)
	}
	_, nrItems := closerJSON(t, []byte(nrOut))
	if len(nrItems) == 0 {
		t.Errorf("a non-repo still owns the store's items — the ladder must report them untested, not drop them")
	}
	for _, it := range nrItems {
		if it.Rung != "untested" {
			t.Errorf("non-repo item %s: rung %q, want untested (no ancestry proof is possible)", it.ID, it.Rung)
		}
	}
}

// ---------------------------------------------------------------- TC2 / AC4

// TestCloserWrittenRung pins AC4's written arm: a dirty referenced file and an
// unmerged branch both render written; an item whose referenced file does not
// exist cannot be integrated or runs.
func TestCloserWrittenRung(t *testing.T) {
	fx := newFixture(t)
	items := mustJSON(t, fx)

	if it, ok := jsonItemByID(items, "9003"); !ok {
		t.Errorf("card 9003 (dirty referenced file) missing from items")
	} else if it.Rung != "written" {
		t.Errorf("card 9003: rung %q, want %q (the referenced file exists and is dirty)", it.Rung, "written")
	}
	if it, ok := jsonItemByID(items, "9002"); !ok {
		t.Errorf("card 9002 (unmerged branch) missing from items")
	} else if it.Rung != "written" {
		t.Errorf("card 9002: rung %q, want %q (unmerged commits)", it.Rung, "written")
	}
	if it, ok := jsonItemByID(items, "9005"); !ok {
		t.Errorf("card 9005 (absent referenced file) missing from items")
	} else if it.Rung == "integrated" || it.Rung == "runs" {
		t.Errorf("card 9005: rung %q, but its referenced file does not exist — must be untested", it.Rung)
	}
}

// ---------------------------------------------------------------- TC3 / AC5 / M17

// TestCloserRunsRungDoubleGate pins AC5 (and M17): runs needs BOTH the card's
// verify: field AND the --verify flag; only then is the named command executed
// and its exit code recorded. Any single-gate implementation goes red here.
func TestCloserRunsRungDoubleGate(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "verify-sentinel")

	// build adds a verify-carrying card to the mixed fixture and commits it.
	build := func(t *testing.T) fixture {
		t.Helper()
		fx := newFixture(t)
		writeCard(t, fx.tasks, closerCard{
			id: "9101", title: "runs-gate card",
			verify: "touch " + sentinel,
			files:  []string{"internal/guard/guard.go"},
		})
		runGit(t, fx.home, fx.repo, "add", "-A")
		runGit(t, fx.home, fx.repo, "commit", "-m", "cards: runs-gate fixture")
		return fx
	}

	t.Run("verify field AND --verify => runs, command executed", func(t *testing.T) {
		fx := build(t)
		_ = os.Remove(sentinel)
		out, stderr, code := execCloser(t, fx.home, fx.repo, "--verify", "--json")
		if code != 0 {
			t.Fatalf("--verify exit %d (stderr %q), want 0", code, stderr)
		}
		_, items := closerJSON(t, []byte(out))
		it, ok := jsonItemByID(items, "9101")
		if !ok {
			t.Fatalf("card 9101 missing from --json items")
		}
		if it.Rung != "runs" {
			t.Errorf("card 9101 with verify: and --verify: rung %q, want %q", it.Rung, "runs")
		}
		if _, err := os.Stat(sentinel); err != nil {
			t.Errorf("the verify: command was not executed under --verify (sentinel %s missing): %v", sentinel, err)
		}
	})

	t.Run("verify field WITHOUT --verify => untested, command NOT executed", func(t *testing.T) {
		fx := build(t)
		_ = os.Remove(sentinel)
		out, _, code := execCloser(t, fx.home, fx.repo, "--json")
		if code != 0 {
			t.Fatalf("no --verify exit %d, want 0", code)
		}
		_, items := closerJSON(t, []byte(out))
		it, ok := jsonItemByID(items, "9101")
		if !ok {
			t.Fatalf("card 9101 missing from --json items")
		}
		if it.Rung != "untested" {
			t.Errorf("card 9101 with verify: but no --verify: rung %q, want %q (never assumed, never promoted)", it.Rung, "untested")
		}
		if _, err := os.Stat(sentinel); err == nil {
			t.Errorf("the verify: command ran without --verify (sentinel %s exists) — M17 shape", sentinel)
		}
	})

	t.Run("no verify field WITH --verify => untested", func(t *testing.T) {
		fx := newFixture(t)
		items := mustJSON(t, fx)
		if it, ok := jsonItemByID(items, "9008"); !ok {
			t.Errorf("card 9008 (no verify: field) missing from items")
		} else if it.Rung == "runs" {
			t.Errorf("card 9008: rung runs, but the card carries no verify: field — must be untested")
		}
	})

	t.Run("roadmap-sourced item => untested even WITH --verify", func(t *testing.T) {
		fx := newFixture(t)
		out, _, code := execCloser(t, fx.home, fx.repo, "--verify", "--json")
		if code != 0 {
			t.Fatalf("--verify exit %d, want 0", code)
		}
		_, items := closerJSON(t, []byte(out))
		// Roadmap rows carry no path (QueuedItem has no verify source), so the
		// double gate can never be satisfied: untested, never assumed (row 12).
		if it, ok := jsonItemByID(items, "9100"); !ok {
			t.Errorf("roadmap item 9100 missing from items (roadmap rows are part of the item list)")
		} else if it.Rung != "untested" {
			t.Errorf("roadmap item 9100: rung %q, want untested (QueuedItem carries no verify: field)", it.Rung)
		}
	})

	t.Run("non-zero verify exit is recorded, never swallowed", func(t *testing.T) {
		fx := newFixture(t)
		writeCard(t, fx.tasks, closerCard{id: "9102", title: "failing check", verify: "false", files: []string{"internal/guard/guard.go"}})
		runGit(t, fx.home, fx.repo, "add", "-A")
		runGit(t, fx.home, fx.repo, "commit", "-m", "cards: failing verify")
		out, _, code := execCloser(t, fx.home, fx.repo, "--verify")
		if code != 0 {
			t.Fatalf("closer exit %d, want 0 (there is no nonzero exit class in v1)", code)
		}
		items, _, _, _ := parseRendered(t, out)
		it, ok := itemByID(items, "9102")
		if !ok {
			t.Fatalf("card 9102 missing from rendered output:\n%s", out)
		}
		if it.rung != "runs" {
			t.Errorf("card 9102: rung %q, want %q (the named command WAS executed)", it.rung, "runs")
		}
		if !strings.HasPrefix(it.action, "verify:") {
			t.Errorf("card 9102: last mile %q, want %q (re-running the failed check)", it.action, "verify: <cmd>")
		}
	})
}

// ---------------------------------------------------------------- TC4 / AC6 / M18

// TestCloserConsumedIsNeverClaimed pins AC6 (and M18): no item may carry the
// consumed rung, and the word appears only inside the literal "consumed: N/A".
func TestCloserConsumedIsNeverClaimed(t *testing.T) {
	fx := newFixture(t)

	out, _, code := execCloser(t, fx.home, fx.repo)
	if code != 0 {
		t.Fatalf("closer exit %d, want 0", code)
	}
	items, _, _, _ := parseRendered(t, out)
	if len(items) == 0 {
		t.Fatalf("closer rendered no items:\n%s", out)
	}
	for _, it := range items {
		if it.rung == "consumed" {
			t.Errorf("item %s reports rung consumed — it is N/A in v1 and must never be assigned", it.id)
		}
	}

	// Rendered text: mask the legal literal, then no "consumed" may remain.
	wordRe := regexp.MustCompile(`(?i)consumed`)
	masked := consumedAct.ReplaceAllString(ansi.Strip(out), "@@NA@@")
	if loc := wordRe.FindString(masked); loc != "" {
		t.Errorf("rendered output claims %q outside the N/A literal: %s", loc, masked)
	}

	// --json: same rule.
	for _, it := range mustJSON(t, fx) {
		if it.Rung == "consumed" {
			t.Errorf("--json item %s reports rung consumed — never assignable in v1", it.ID)
		}
		lm := strings.ToLower(strings.TrimSpace(it.LastMile))
		if strings.Contains(lm, "consumed") && lm != "consumed: n/a" {
			t.Errorf("--json item %s last mile claims consumed outside the N/A literal: %q", it.ID, it.LastMile)
		}
	}
}

// ---------------------------------------------------------------- TC5 / AC7

// TestCloserOneLastMilePerItem pins AC7's first half: every item carries exactly
// one action from {commit, merge <branch>, wire <target>, verify: <cmd>,
// consumed: N/A}. A dirty-written item's action is commit; an unmerged item's is
// merge <branch>; an integrated item with a wire: field is wire <target>; and an
// integrated item with no wire field is consumed: N/A.
func TestCloserOneLastMilePerItem(t *testing.T) {
	fx := newFixture(t)

	rendered, _, code := execCloser(t, fx.home, fx.repo)
	if code != 0 {
		t.Fatalf("closer exit %d, want 0", code)
	}
	rows, _, _, _ := parseRendered(t, rendered)
	for _, it := range rows {
		if it.action == "" {
			t.Errorf("rendered item %s carries no last-mile action (exactly one is required)", it.id)
			continue
		}
		if !legalAction(it.action) {
			t.Errorf("rendered item %s: action %q is outside the five legal last-mile forms", it.id, it.action)
		}
	}

	// The uncapped list carries the same contract, so the cap cannot hide it.
	items := mustJSON(t, fx)
	for _, it := range items {
		if !legalAction(it.LastMile) {
			t.Errorf("item %s: last mile %q is outside the five legal forms", it.ID, it.LastMile)
		}
	}
	if it, ok := jsonItemByID(items, "9003"); !ok {
		t.Errorf("card 9003 (dirty) missing from items")
	} else if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(it.LastMile)), "commit") {
		t.Errorf("dirty item 9003: last mile %q, want %q", it.LastMile, "commit")
	}
	if it, ok := jsonItemByID(items, "9002"); !ok {
		t.Errorf("card 9002 (unmerged) missing from items")
	} else if !strings.HasPrefix(strings.ToLower(it.LastMile), "merge ") {
		t.Errorf("unmerged item 9002: last mile %q, want %q", it.LastMile, "merge <branch>")
	}
	if it, ok := jsonItemByID(items, "9006"); !ok {
		t.Errorf("card 9006 (integrated with a wire: field) missing from items")
	} else {
		if it.Rung != "integrated" {
			t.Errorf("card 9006: rung %q, want integrated (its file is committed on the default branch)", it.Rung)
		}
		if !strings.HasPrefix(strings.ToLower(it.LastMile), "wire ") {
			t.Errorf("integrated item 9006 with a wire: field: last mile %q, want %q", it.LastMile, "wire <target>")
		}
	}
	if it, ok := jsonItemByID(items, "9007"); !ok {
		t.Errorf("card 9007 (integrated, no wire: field) missing from items")
	} else {
		if it.Rung != "integrated" {
			t.Errorf("card 9007: rung %q, want integrated (its file is committed on the default branch)", it.Rung)
		}
		if got := strings.TrimSpace(it.LastMile); got != "consumed: N/A" {
			t.Errorf("integrated item 9007 with no wire: field: last mile %q, want the literal %q", got, "consumed: N/A")
		}
	}
}

// legalAction reports whether s is one of the five legal last-mile forms.
func legalAction(s string) bool {
	t := strings.ToLower(strings.TrimSpace(s))
	switch {
	case t == "commit", t == "consumed: n/a":
		return true
	case strings.HasPrefix(t, "merge "), strings.HasPrefix(t, "wire "), strings.HasPrefix(t, "verify:"):
		return true
	}
	return false
}

// ---------------------------------------------------------------- TC6 / AC8 / M19

// TestCloserReadOnlyRepoInvariant pins AC8 (and M19): a full closer run —
// including the --verify execution path — leaves the repo byte-stable:
// identical recursive .git digest, byte-stable `git status --porcelain`, and no
// new path under the repo root. The fixture's verify: command writes only to a
// temp path (never go build: the spec pins fixtures to temp-only commands).
func TestCloserReadOnlyRepoInvariant(t *testing.T) {
	fx := cleanFixture(t)
	if got := porcelain(t, fx.home, fx.repo); got != "" {
		t.Fatalf("fixture must start clean, got porcelain:\n%s", got)
	}

	sentinel := filepath.Join(t.TempDir(), "verify-sentinel")
	writeCard(t, fx.tasks, closerCard{id: "9201", title: "read-only probe", verify: "touch " + sentinel, files: []string{"internal/guard/guard.go"}})
	runGit(t, fx.home, fx.repo, "add", "-A")
	runGit(t, fx.home, fx.repo, "commit", "-m", "cards: read-only fixture")

	beforeDigest := gitTreeDigest(t, fx.repo)
	beforePaths := repoPaths(t, fx.repo)

	for _, args := range [][]string{{}, {"--json"}, {"--verify"}, {"--verify", "--json"}} {
		if _, stderr, code := execCloser(t, fx.home, fx.repo, args...); code != 0 {
			t.Errorf("closer %v: exit %d (stderr %q), want 0", args, code, stderr)
		}
	}

	if got := gitTreeDigest(t, fx.repo); got != beforeDigest {
		t.Errorf("the .git tree changed across a closer run (M19 shape): digest %s != baseline %s", got, beforeDigest)
	}
	if got := porcelain(t, fx.home, fx.repo); got != "" {
		t.Errorf("git status --porcelain is not byte-stable across a closer run: %q", got)
	}
	afterPaths := repoPaths(t, fx.repo)
	for p := range afterPaths {
		if !beforePaths[p] {
			t.Errorf("a closer run left a new path under the repo root: %s", p)
		}
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("the --verify run never executed the card's temp-only command: %v", err)
	}
}

// ---------------------------------------------------------------- TC7 / AC7

// TestCloserCapAndMoreLine pins AC7's display cap: at most 5 item lines then the
// literal "+N more (of M)" with N = M-5; when M <= 5 no more-line; --json stays
// uncapped.
func TestCloserCapAndMoreLine(t *testing.T) {
	fx := newFixture(t)

	out, _, code := execCloser(t, fx.home, fx.repo)
	if code != 0 {
		t.Fatalf("closer exit %d, want 0", code)
	}
	all := mustJSON(t, fx)
	total := len(all)
	if total <= 5 {
		t.Fatalf("fixture must produce more than 5 items to exercise the cap, got %d", total)
	}

	items, more, moreN, moreM := parseRendered(t, out)
	if len(items) != 5 {
		t.Errorf("display cap: want exactly 5 item lines at M=%d, got %d:\n%s", total, len(items), out)
	}
	if len(more) != 1 {
		t.Errorf("want exactly one '+N more (of M)' line, got %d: %v\n%s", len(more), more, out)
	} else {
		if moreM != total {
			t.Errorf("more-line says M=%d, want the total item count %d", moreM, total)
		}
		if moreN != total-5 {
			t.Errorf("more-line says N=%d, want N=M-5=%d", moreN, total-5)
		}
	}
	if len(all) < len(items) {
		t.Errorf("--json carries %d items, fewer than the rendered %d — render caps must not mutate the data", len(all), len(items))
	}
	for _, id := range fixtureIDs {
		if _, ok := jsonItemByID(all, id); !ok {
			t.Errorf("--json is missing card %s — the uncapped list is required", id)
		}
	}
}

// ---------------------------------------------------------------- AC8 build wiring

// TestCloserMakeBuildTargets pins AC8's build wiring: make build-guard and make
// build-closer each produce a binary beside build-radar, their .PHONY entries
// exist, and neither new package carries a t.Skip.
func TestCloserMakeBuildTargets(t *testing.T) {
	root, err := repoRootPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ target, bin string }{
		{"build-guard", "guard"},
		{"build-closer", "closer"},
	} {
		cmd := exec.Command("make", tc.target)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("make %s: %v: %s (the .PHONY target must exist beside build-radar)", tc.target, err, out)
			continue
		}
		bin := filepath.Join(root, tc.bin)
		info, err := os.Stat(bin)
		if err != nil {
			t.Errorf("make %s produced no ./%s: %v", tc.target, tc.bin, err)
			continue
		}
		if info.Mode()&0o111 == 0 {
			t.Errorf("./%s is not executable: %v", tc.bin, info.Mode())
		}
		t.Cleanup(func() { _ = os.Remove(bin) })
	}

	mk, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^\.PHONY:.*\bbuild-guard\b`).Match(mk) ||
		!regexp.MustCompile(`(?m)^\.PHONY:.*\bbuild-closer\b`).Match(mk) {
		t.Errorf("Makefile must declare build-guard and build-closer .PHONY beside build-radar")
	}

	// No skip anywhere in the two new packages (AC8: every test runs).
	skipNeedle := "t." + "Skip("
	shortNeedle := "testing." + "Short"
	for _, dir := range []string{"internal/guard", "internal/closer"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			src, err := os.ReadFile(filepath.Join(root, dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(src), skipNeedle) {
				t.Errorf("%s/%s contains a skip call — skipped acceptance tests are forbidden", dir, e.Name())
			}
			if strings.Contains(string(src), shortNeedle) {
				t.Errorf("%s/%s gates tests on a short-mode guard — silent exclusion is forbidden", dir, e.Name())
			}
		}
	}
}
