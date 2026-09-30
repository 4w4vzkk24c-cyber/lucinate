// Package closer renders the last-mile ladder: for each item the operator has
// started (kanban cards + ranked roadmap rows), the rung it has reached and the
// single next action that closes it.
//
// Read-only: closer never writes, stages, commits, merges, or pushes, and every
// git invocation is a read-only query. The verify: execution path is opt-in
// (--verify) and runs the card's own command, never a closer-authored one.
package closer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lucinate-ai/lucinate/internal/radar"
)

// Rung is one rung of the evidence ladder, plus the honest miss value.
type Rung string

// The four ladder rungs and the never-assumed miss value. consumed exists as a
// literal but is NEVER assigned: no consumer log exists in v1.
const (
	RungIntegrated Rung = "integrated"
	RungWritten    Rung = "written"
	RungRuns       Rung = "runs"
	RungConsumed   Rung = "consumed"
	RungUntested   Rung = "untested"
)

// Item is one ladder row: the item, its rung, and exactly one last-mile action.
type Item struct {
	ID, Title string
	Rung      Rung
	LastMile  string
}

// Store paths under HOME (resolved through os.UserHomeDir() by cmd/closer; no
// absolute path literal here).
const (
	tasksRelPath = "Repositories/kanban-zed/tasks"
	roadmapRel   = "Obsidian/Roadmap/Δ.md"
	scanBudget   = 250 * time.Millisecond
)

// Classify builds the ladder with the runs rung disabled (no --verify): every
// verify:-carrying card is honestly untested.
func Classify(repo string, now time.Time) ([]Item, error) {
	return ClassifyVerify(repo, now, false)
}

// ClassifyVerify builds the ladder. When verify is true, a card carrying a
// verify: field has that command executed and its exit code recorded (the runs
// rung); when false, such a card is reported untested — never assumed.
// Consumed is never assigned on any path.
func ClassifyVerify(repo string, now time.Time, verify bool) ([]Item, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	cards, _ := radar.ScanTasks(filepath.Join(home, filepath.FromSlash(tasksRelPath)), scanBudget)
	rows, _ := radar.ParseRoadmap(filepath.Join(home, filepath.FromSlash(roadmapRel)), 120)

	dirty := gitDirty(repo)
	defaultRef := gitDefaultRef(repo)

	var items []Item

	// Card-sourced items: the ONLY source carrying a path, hence the only
	// source that can carry a verify: command (ActiveCard.Path).
	for _, c := range cards {
		items = append(items, classifyCard(repo, c, readCardMeta(c.Path), dirty, defaultRef, verify))
	}

	// Roadmap-sourced items: QueuedItem carries no path, so no verify: field is
	// possible — they are always untested and never assumed (row 12).
	for _, r := range rows {
		items = append(items, Item{
			ID:       r.ID,
			Title:    r.Title,
			Rung:     RungUntested,
			LastMile: "wire ?",
		})
	}

	sortItems(items)
	return items, nil
}

// classifyCard assigns the rung and last mile for one card-sourced item.
func classifyCard(repo string, c radar.ActiveCard, meta cardMeta, dirty map[string]bool, defaultRef string, verify bool) Item {
	it := Item{ID: c.ID, Title: c.Title}

	// A card carrying a verify: field is a probe. It reaches the runs rung only
	// under --verify (the command executed, its exit code recorded); without the
	// flag it is honestly untested — never promoted to integrated.
	if meta.verify != "" {
		if !verify {
			it.Rung = RungUntested
			it.LastMile = "wire ?"
			return it
		}
		code := runVerify(repo, meta.verify)
		it.Rung = RungRuns
		switch {
		case code != 0:
			it.LastMile = "verify: " + meta.verify
		case meta.wire != "":
			it.LastMile = "wire " + meta.wire
		default:
			it.LastMile = "consumed: N/A"
		}
		return it
	}

	allExist := len(meta.files) > 0
	isDirty := false
	for _, f := range meta.files {
		if !fileExists(repo, f) {
			allExist = false
		}
		if dirty[f] {
			isDirty = true
		}
	}

	// integrated: every referenced file's newest commit is an ancestor of the
	// default branch and the tree is clean — proven by merge-base --is-ancestor.
	if allExist && !isDirty && cardIntegrated(repo, meta.files, defaultRef) {
		it.Rung = RungIntegrated
		if meta.wire != "" {
			it.LastMile = "wire " + meta.wire
		} else {
			it.LastMile = "consumed: N/A"
		}
		return it
	}

	// written: a referenced file is dirty, or an unmerged branch carries it.
	if isDirty {
		it.Rung = RungWritten
		it.LastMile = "commit"
		return it
	}
	if br := firstUnmergedBranch(repo, meta.files, defaultRef); br != "" {
		it.Rung = RungWritten
		it.LastMile = "merge " + br
		return it
	}

	// untested: no integrated proof, no dirty file, no unmerged branch.
	it.Rung = RungUntested
	it.LastMile = "wire ?"
	return it
}

// cardIntegrated proves each referenced file via merge-base --is-ancestor: the
// newest commit touching the file must be an ancestor of the default branch.
func cardIntegrated(repo string, files []string, defaultRef string) bool {
	if defaultRef == "" {
		return false
	}
	for _, f := range files {
		tip := gitLastTouch(repo, f)
		if tip == "" || !gitIsAncestor(repo, tip, defaultRef) {
			return false
		}
	}
	return true
}

// firstUnmergedBranch returns the name of a local branch whose tip commit
// touched one of the referenced files and is NOT an ancestor of defaultRef.
// A branch name alone is never evidence of integration; this only supplies the
// <branch> token for the merge last-mile.
func firstUnmergedBranch(repo string, files []string, defaultRef string) string {
	if defaultRef == "" {
		return ""
	}
	for _, f := range files {
		tip := gitLastTouch(repo, f)
		if tip == "" || gitIsAncestor(repo, tip, defaultRef) {
			continue
		}
		if br := gitBranchContaining(repo, tip); br != "" {
			return br
		}
	}
	return ""
}

// sortItems orders items closest-to-the-last-mile first (written / runs /
// untested before integrated), stable by id within a rung, so the display cap
// surfaces the items that still need action.
func sortItems(items []Item) {
	rank := func(r Rung) int {
		switch r {
		case RungWritten:
			return 0
		case RungRuns:
			return 1
		case RungUntested:
			return 2
		default:
			return 3
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := rank(items[i].Rung), rank(items[j].Rung)
		if ri != rj {
			return ri < rj
		}
		return items[i].ID < items[j].ID
	})
}

// cardMeta is the subset of a card frontmatter the ladder reads.
type cardMeta struct {
	verify string
	wire   string
	files  []string
}

// readCardMeta reads verify:/wire:/files: from a card's frontmatter (the lines
// between the first two "---" delimiters). Unreadable or malformed cards yield
// the zero meta, never an error — the ladder degrades to untested.
func readCardMeta(path string) cardMeta {
	var m cardMeta
	if path == "" {
		return m
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return m
	}
	lines := strings.Split(string(data), "\n")
	inFM := false
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if i == 0 {
			if t != "---" {
				return cardMeta{}
			}
			inFM = true
			continue
		}
		if t == "---" {
			break
		}
		if !inFM {
			break
		}
		k, v, ok := strings.Cut(ln, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "verify":
			m.verify = v
		case "wire":
			m.wire = v
		case "files":
			m.files = parseFilesField(v)
		}
	}
	return m
}

// parseFilesField parses a frontmatter files field "[a, b, c]" into paths.
func parseFilesField(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "[")
	v = strings.TrimSuffix(v, "]")
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.Trim(strings.TrimSpace(p), `"'`); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func fileExists(repo, rel string) bool {
	_, err := os.Stat(filepath.Join(repo, filepath.FromSlash(rel)))
	return err == nil
}

// ---------------------------------------------------------------- read-only git

const gitTimeout = 3 * time.Second

// gitRun runs one read-only git query in repo, discarding stderr so a non-repo
// degrades quietly (the 2>/dev/null || true seam). ok is false on any failure.
func gitRun(repo string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repo
	out, err := cmd.Output()
	return string(out), err == nil
}

// gitDirty returns the set of repo-relative dirty paths (git status --
// porcelain). A non-repo yields an empty set.
func gitDirty(repo string) map[string]bool {
	out, ok := gitRun(repo, "status", "--porcelain")
	if !ok {
		return map[string]bool{}
	}
	set := map[string]bool{}
	for _, ln := range strings.Split(out, "\n") {
		if len(ln) < 4 {
			continue
		}
		p := strings.TrimSpace(ln[3:])
		if p == "" {
			continue
		}
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		set[p] = true
	}
	return set
}

// gitDefaultRef resolves the repo's default branch ref (never a hardcoded
// "main"): the origin/HEAD symbolic ref, else the current branch.
func gitDefaultRef(repo string) string {
	if out, ok := gitRun(repo, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); ok {
		if r := strings.TrimSpace(out); r != "" {
			return r
		}
	}
	if out, ok := gitRun(repo, "rev-parse", "--abbrev-ref", "HEAD"); ok {
		if r := strings.TrimSpace(out); r != "" && r != "HEAD" {
			return r
		}
	}
	return ""
}

// gitLastTouch returns the newest commit (across all refs) that touched path.
func gitLastTouch(repo, path string) string {
	out, ok := gitRun(repo, "log", "--all", "-1", "--format=%H", "--", path)
	if !ok {
		return ""
	}
	return strings.TrimSpace(out)
}

// gitIsAncestor is the SOLE integrated predicate: git merge-base --is-ancestor
// exit 0 => ancestor, exit 1 => not, exit 128 => error (all non-ancestor here).
func gitIsAncestor(repo, commit, ref string) bool {
	if commit == "" || ref == "" {
		return false
	}
	_, ok := gitRun(repo, "merge-base", "--is-ancestor", commit, ref)
	return ok
}

// gitBranchContaining returns the first local branch whose tip contains commit,
// or "" when none.
func gitBranchContaining(repo, commit string) string {
	out, ok := gitRun(repo, "branch", "--contains", commit, "--format=%(refname:short)")
	if !ok {
		return ""
	}
	for _, ln := range strings.Split(out, "\n") {
		if b := strings.TrimSpace(ln); b != "" {
			return b
		}
	}
	return ""
}

// runVerify executes a card's verify: command in repo and returns its exit
// code. Only reached when the --verify flag is set.
func runVerify(repo, cmd string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	c.Dir = repo
	if err := c.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		return 127
	}
	return 0
}
