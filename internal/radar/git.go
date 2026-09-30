package radar

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// gitTimeout bounds every git invocation so a hung git process cannot block
// the render budget (spec constraint).
const gitTimeout = 3 * time.Second

// CollectGit gathers working-tree and recent-commit telemetry from the git
// repo at dir:
//
//   - DirtyFiles from git status --porcelain (repo-relative paths);
//   - ReEntryFile as "path:line" — the new-file line of the first @@ hunk of
//     git diff -U0 for the first dirty file (empty when the tree is clean,
//     and empty when the diff has zero @@ hunks, i.e. a filemode-only change);
//   - Landed: up to 5 non-merge commits by author Zane in the last 24 hours.
//
// A clean tree (or an unreadable repo) yields zero values and a nil error —
// telemetry is best-effort observability, never fatal.
func CollectGit(dir string, now time.Time) (GitTelemetry, error) {
	tel := GitTelemetry{DirtyFiles: []string{}, Landed: []LandedEvidence{}}

	out, err := gitRun(dir, "status", "--porcelain")
	if err != nil {
		return tel, nil // not a repo, or git failed: zero-value telemetry
	}
	tel.DirtyFiles = parsePorcelain(out)

	if len(tel.DirtyFiles) > 0 {
		if diff, derr := gitRun(dir, "diff", "-U0", "--", tel.DirtyFiles[0]); derr == nil {
			if n := firstHunkNewLine(diff); n > 0 {
				tel.ReEntryFile = tel.DirtyFiles[0] + ":" + strconv.Itoa(n)
			}
		}
	}

	cutoff := now.Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	logOut, lerr := gitRun(dir,
		"log", "--no-merges", "--author=Zane", "-n", "25",
		"--pretty=format:%h\t%cI\t%s\t(%cr)")
	if lerr == nil {
		tel.Landed = parseLanded(logOut, cutoff)
	}
	return tel, nil
}

// gitAllMessages returns recent non-merge commit subjects reachable from any
// ref (branch evidence for the r3 F7 decoration rule). Errors degrade to an
// empty result.
//
// Landed = 24h evidence contract; decoration = whole listing (no cutoff; -n 25 cap stays).
func gitAllMessages(dir string) []string {
	out, err := gitRun(dir,
		"log", "--all", "--no-merges", "--author=Zane", "-n", "25",
		"--pretty=format:%h\t%cI\t%s\t(%cr)")
	if err != nil {
		return nil
	}
	var msgs []string
	for _, e := range parseLanded(out, "") {
		msgs = append(msgs, e.Message)
	}
	return msgs
}

// gitRun runs one git command in dir with a hard timeout.
func gitRun(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

var hunkRe = regexp.MustCompile(`(?m)^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// firstHunkNewLine extracts the new-file start line of the first @@ hunk;
// 0 when the diff carries no hunks (e.g. a filemode-only change).
func firstHunkNewLine(diff string) int {
	m := hunkRe.FindStringSubmatch(diff)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// parsePorcelain converts git status --porcelain output into repo-relative
// paths ("XY path"; renames keep the new side).
func parsePorcelain(out string) []string {
	var files []string
	for _, ln := range strings.Split(out, "\n") {
		if len(ln) < 4 {
			continue
		}
		path := strings.TrimSpace(ln[3:])
		if path == "" {
			continue
		}
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:]
		}
		files = append(files, path)
	}
	return files
}

// parseLanded converts "%h\t%cI\t%s\t(%cr)" lines into evidence. A non-empty
// cutoff applies the 24h window as a post-filter on the committer date: git's
// --since is a traversal cutoff (it abandons a parent chain at the first
// too-old commit), which would wrongly drop fresh commits sitting behind an
// old one. A non-empty cutoff also caps the result at the newest five
// survivors (the Landed 24h-evidence contract). An empty cutoff keeps the
// whole listing with no survivor cap — the caller's git -n bound (25) is the
// only limit (decoration evidence; spec row 11(ii)).
func parseLanded(out string, cutoff string) []LandedEvidence {
	var cut time.Time
	if cutoff != "" {
		var err error
		if cut, err = time.Parse(time.RFC3339, cutoff); err != nil {
			return nil
		}
	}
	var landed []LandedEvidence
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		parts := strings.SplitN(ln, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		hash, iso, rest := parts[0], parts[1], parts[2]
		when, perr := time.Parse(time.RFC3339, iso)
		if perr != nil || (!cut.IsZero() && when.Before(cut)) {
			continue
		}
		subject, age := rest, ""
		if i := strings.LastIndex(rest, "\t("); i >= 0 {
			subject = rest[:i]
			age = strings.TrimSuffix(rest[i+2:], ")")
		}
		landed = append(landed, LandedEvidence{Hash: hash, Message: subject, Age: age})
		// Survivor cap belongs to the Landed 24h-evidence path only: an empty
		// cutoff (decoration evidence) must keep the whole -n 25 listing so a
		// card-ref commit 6+ back is still seen.
		if !cut.IsZero() && len(landed) >= 5 {
			break
		}
	}
	return landed
}
