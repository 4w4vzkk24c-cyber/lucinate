package radar

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Store layout under HOME (resolved via os.UserHomeDir() by callers; tests
// override HOME to a fixture — nothing here may hardcode an absolute path).
const (
	tasksRelPath  = "Repositories/kanban-zed/tasks"
	repoRelPath   = "Repositories/kanban-zed"
	roadmapRel    = "Obsidian/Roadmap/Δ.md"
	defaultBudget = 250 * time.Millisecond
	stalledCap    = 4
)

// scanBudget is the whole-scan deadline Build grants ScanTasks.
var scanBudget = defaultBudget

// ScanTasks concurrently scans a kanban tasks directory (*.md, non-recursive)
// and returns the active cards: any card whose frontmatter status is not
// "done" or "archived". Frontmatter parsing terminates at the second "---"
// line, so "status: done" mentions in a card body never filter the card, and
// a file whose frontmatter never closes still yields its parsed fields.
// budget is a whole-scan wall-clock deadline (<= 0: unbounded).
func ScanTasks(dir string, budget time.Duration) ([]ActiveCard, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no store: graceful empty, not an error
		}
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}

	var (
		deadline time.Time
		ctx      context.Context
		cancel   context.CancelFunc
	)
	if budget > 0 {
		deadline = time.Now().Add(budget)
		ctx, cancel = context.WithDeadline(context.Background(), deadline)
		defer cancel()
	}

	results := make([]ActiveCard, len(paths))
	indices := make(chan int)
	var wg sync.WaitGroup
	workers := 16
	if workers > len(paths) {
		workers = len(paths)
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range indices {
				if !deadline.IsZero() && time.Now().After(deadline) {
					continue
				}
				if c, ok := parseCard(paths[i]); ok {
					results[i] = c
				}
			}
		}()
	}
	for i := range paths {
		indices <- i
	}
	close(indices)
	wg.Wait()
	_ = ctx

	cards := make([]ActiveCard, 0, len(results))
	for _, c := range results {
		if c.ID != "" {
			cards = append(cards, c)
		}
	}
	return cards, nil
}

// parseCard reads one card file and extracts id/title/status/priority from
// its frontmatter — the lines between the first "---" and the second "---"
// (or EOF when the frontmatter is unterminated).
func parseCard(path string) (ActiveCard, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ActiveCard{}, false
	}
	var c ActiveCard
	c.Path = path
	inFM := false
	lines := strings.Split(string(data), "\n")
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if i == 0 {
			if t != "---" {
				return ActiveCard{}, false // not frontmatter
			}
			inFM = true
			continue
		}
		if t == "---" {
			break // second delimiter terminates the frontmatter
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
		case "id":
			c.ID = v
		case "title":
			c.Title = v
		case "status":
			c.Status = v
		case "priority":
			c.Priority = v
		}
	}
	if c.Status == "done" || c.Status == "archived" {
		return ActiveCard{}, false
	}
	return c, true
}

var kanbanRefRe = regexp.MustCompile(`\*kanban #([0-9]+)\*`)

// ParseRoadmap reads at most maxLines lines of the roadmap file and returns
// the queued rows in document order: rows[0] is the "## Do now" entry when
// the section matched and carries a task; the remaining rows are the ranked
// band entries. A band-only file (no "## Do now" section) yields an empty
// slice — band entries are never promoted into rows[0]. A missing or
// unreadable file (including iCloud-evicted) returns an error wrapping
// ErrRoadmapUnavailable.
func ParseRoadmap(path string, maxLines int) ([]QueuedItem, error) {
	rows, _, err := parseRoadmap(path, maxLines)
	return rows, err
}

// parseRoadmap is ParseRoadmap plus the do-now-presence flag Build needs to
// keep rows[0] unambiguous (spec r3 F5 / r5 F8).
func parseRoadmap(path string, maxLines int) (rows []QueuedItem, doNow bool, err error) {
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil, false, fmt.Errorf("%w: %s", ErrRoadmapUnavailable, readErr)
	}
	lines := strings.Split(string(data), "\n")
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[:maxLines]
	}

	var doNowRows, bandRows []QueuedItem
	section := "" // "", "donow", "band"
	for _, ln := range lines {
		if strings.HasPrefix(ln, "## ") {
			switch {
			case strings.HasPrefix(ln, "## Do now"):
				section = "donow"
				doNow = true
			case strings.HasPrefix(ln, "## Ranked"):
				section = "band"
			default:
				section = "" // unrelated ## section: closes Do-now, opens no band capture
			}
			continue
		}
		if strings.HasPrefix(ln, "#") { // "### …" subsection headers
			continue
		}
		item, ok := parseTaskLine(ln)
		if !ok {
			continue
		}
		switch section {
		case "donow":
			doNowRows = append(doNowRows, item)
		case "band":
			bandRows = append(bandRows, item)
		}
	}

	if !doNow {
		return nil, false, nil // band-only: empty slice, never promoted
	}
	if len(doNowRows) > 0 {
		rows = append(rows, doNowRows[0])
	}
	rows = append(rows, bandRows...)
	return rows, true, nil
}

// parseTaskLine parses one roadmap task line of the shape
//
//   - [ ] `  476` Wire the radar scan loop — notes · *kanban #1461* · `in-progress`
//
// into {ID: "1461", Title: "Wire the radar scan loop", Priority: "476"}.
func parseTaskLine(ln string) (QueuedItem, bool) {
	s := strings.TrimSpace(ln)
	rest, ok := strings.CutPrefix(s, "- [ ]")
	if !ok {
		return QueuedItem{}, false
	}
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, "`") {
		return QueuedItem{}, false
	}
	scoreEnd := strings.Index(rest[1:], "`")
	if scoreEnd < 0 {
		return QueuedItem{}, false
	}
	score := strings.TrimSpace(rest[1 : 1+scoreEnd])
	after := rest[1+scoreEnd+1:]

	title := strings.TrimSpace(after)
	if i := strings.Index(after, " — "); i >= 0 {
		title = strings.TrimSpace(after[:i])
	}

	m := kanbanRefRe.FindStringSubmatch(after)
	if m == nil || score == "" {
		return QueuedItem{}, false // not a card task line
	}
	return QueuedItem{ID: m[1], Title: title, Priority: score}, true
}

// bareID reduces the legal id spellings ("1461", "#1461", "Δ#1461") to the
// bare frontmatter digits.
func bareID(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "Δ#")
	s = strings.TrimPrefix(s, "#")
	if i := strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		s = s[:i]
	}
	return s
}

// Build aggregates the three sources into one RadarSnapshot. It owns
// composition only (spec r2 F2): the scan, the roadmap parse, and git
// telemetry. A missing or evicted Δ.md is reported through the error return
// as an ErrRoadmapUnavailable wrap — never a panic, never a fatal exit.
func Build(home string) (RadarSnapshot, error) {
	now := time.Now()
	snap := RadarSnapshot{
		Timestamp:  now,
		Evidence:   []LandedEvidence{},
		DirtyFiles: []string{},
		Stalled:    []StalledDecision{},
		Queue:      []QueuedItem{},
	}

	cards, err := ScanTasks(filepath.Join(home, filepath.FromSlash(tasksRelPath)), scanBudget)
	if err != nil {
		cards = nil // degraded scan still renders
	}

	rows, doNow, perr := parseRoadmap(filepath.Join(home, filepath.FromSlash(roadmapRel)), 120)
	degrade := perr
	if perr == nil {
		if doNow {
			if len(rows) > 0 {
				snap.Queue = append(snap.Queue, rows[1:]...)
			}
		} else {
			snap.Queue = []QueuedItem{}
		}
	}

	tel, _ := CollectGit(filepath.Join(home, filepath.FromSlash(repoRelPath)), now)
	snap.DirtyFiles = append(snap.DirtyFiles, tel.DirtyFiles...)
	snap.Evidence = append(snap.Evidence, tel.Landed...)

	// Branch evidence beyond the default-branch log (spec r3 F7 ii).
	branchMsgs := gitAllMessages(filepath.Join(home, filepath.FromSlash(repoRelPath)))

	// InFlight: rows[0] exists AND a matching active card is in-progress.
	if doNow && len(rows) > 0 {
		want := bareID(rows[0].ID)
		for i := range cards {
			if bareID(cards[i].ID) == want && cards[i].Status == "in-progress" {
				snap.InFlight = decorateInFlight(&cards[i], tel, branchMsgs)
				break
			}
		}
	}

	snap.Stalled = stalledDecisions(cards, rows)

	return snap, degrade
}

// decorateInFlight applies the r3 F7 mapping over git evidence:
// (i) default-branch (Landed) commit referencing the id => StageVerify/70;
// (ii) else any branch commit referencing the id => StageBuild/50;
// (iii) else StageDesign/15. StageLand is never auto-derived.
func decorateInFlight(card *ActiveCard, tel GitTelemetry, branchMsgs []string) *InFlightTask {
	it := &InFlightTask{
		ID:          card.ID,
		Title:       card.Title,
		Description: card.Title,
		Stage:       StageDesign,
		ProgressPct: 15,
	}
	id := bareID(card.ID)
	if idRefsCard(id, landedMessages(tel.Landed)) {
		it.Stage, it.ProgressPct = StageVerify, 70
	} else if idRefsCard(id, branchMsgs) {
		it.Stage, it.ProgressPct = StageBuild, 50
	}

	switch {
	case len(tel.DirtyFiles) == 0:
		// Clean tree: the re-entry anchor defaults to the card's own path.
		it.ReEntryFile = card.Path
		it.ReEntryCmd = ""
	case tel.ReEntryFile != "":
		it.ReEntryFile = tel.ReEntryFile
		dir := filepath.Dir(strings.SplitN(tel.ReEntryFile, ":", 2)[0])
		it.ReEntryCmd = fmt.Sprintf("go test ./%s/ -count=1", dir)
	default:
		// Dirty with zero @@ hunks (filemode-only, r4 F9): never interpolate.
		it.ReEntryFile = ""
		it.ReEntryCmd = ""
	}
	return it
}

// landedMessages extracts the commit messages from Landed evidence.
func landedMessages(landed []LandedEvidence) []string {
	msgs := make([]string, 0, len(landed))
	for _, e := range landed {
		msgs = append(msgs, e.Message)
	}
	return msgs
}

// idRefsCard reports whether any message references the card id with
// non-digit boundaries (so "1461" does not match "14610").
func idRefsCard(id string, msgs []string) bool {
	if id == "" {
		return false
	}
	re, err := regexp.Compile(`(^|[^0-9])` + regexp.QuoteMeta(id) + `([^0-9]|$)`)
	if err != nil {
		return false
	}
	for _, m := range msgs {
		if re.MatchString(m) {
			return true
		}
	}
	return false
}

// stalledDecisions selects blocked/review cards, ordered by Δ.md score
// (ranked cards first, descending score; unranked after in scan order),
// capped at 4 (spec r2 F3).
func stalledDecisions(cards []ActiveCard, rows []QueuedItem) []StalledDecision {
	score := make(map[string]int, len(rows))
	for _, r := range rows {
		if n, err := strconv.Atoi(r.Priority); err == nil {
			score[bareID(r.ID)] = n
		}
	}
	var stalled []ActiveCard
	for _, c := range cards {
		if c.Status == "blocked" || c.Status == "review" {
			stalled = append(stalled, c)
		}
	}
	sort.SliceStable(stalled, func(i, j int) bool {
		si, oki := score[bareID(stalled[i].ID)]
		sj, okj := score[bareID(stalled[j].ID)]
		if oki != okj {
			return oki // ranked before unranked
		}
		return oki && si > sj
	})
	if len(stalled) > stalledCap {
		stalled = stalled[:stalledCap]
	}
	out := make([]StalledDecision, 0, len(stalled))
	for _, c := range stalled {
		out = append(out, StalledDecision{ID: c.ID, Title: c.Title, Blocker: c.Status})
	}
	return out
}
