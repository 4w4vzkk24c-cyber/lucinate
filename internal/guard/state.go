// Package guard renders the thread-divergence tripwire: a read-only view of
// the operator's open work threads, read from <home>/.openclaw/state/threads.json.
//
// guard never writes the state file, never commits, merges, or stages; it is
// invoked and exits (card 1607, EF suite v1 part 2).
package guard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Threshold is the open-thread checkpoint: OpenCount >= Threshold fires.
const Threshold = 3

// Thread is one open work thread from the state document.
type Thread struct {
	ID       string    `json:"id"`
	Label    string    `json:"label"`
	OpenedAt time.Time `json:"opened_at"`
	Next     string    `json:"next"`
	Status   string    `json:"status"`
}

// ThreadState is the whole state document: the root anchor question and the
// thread list.
type ThreadState struct {
	Root    string   `json:"root"`
	Threads []Thread `json:"threads"`
}

// stateRelPath is the store path relative to HOME (resolved through
// os.UserHomeDir() by cmd/guard — no absolute path literal here).
const stateRelPath = ".openclaw/state/threads.json"

// ReadState reads the state document under home. A missing, unreadable, or
// malformed file is NOT an error path: it returns the zero ThreadState (the
// caller renders the disclosed NO THREAD STATE degrade and exits 0).
func ReadState(home string) (ThreadState, error) {
	data, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(stateRelPath)))
	if err != nil {
		return ThreadState{}, nil
	}
	var s ThreadState
	if err := json.Unmarshal(data, &s); err != nil {
		return ThreadState{}, nil
	}
	return s, nil
}

// OpenCount counts open threads: a thread is open iff its Status is not
// exactly "closed" (case-sensitive).
func OpenCount(s ThreadState) int {
	n := 0
	for _, t := range s.Threads {
		if t.Status != "closed" {
			n++
		}
	}
	return n
}
