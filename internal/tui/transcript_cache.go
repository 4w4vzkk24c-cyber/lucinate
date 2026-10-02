package tui

// transcriptCacheCapacity is how many sessions' transcripts are remembered.
const transcriptCacheCapacity = 10

// transcriptCache remembers the transcripts of recently left sessions so a
// revisit can paint before the gateway answers. It is least-recently-used,
// keyed by session key, and holds only server-canonical rows (gen 0).
//
// It is written in exactly one way: AppModel.stashChat, when the open chat
// is replaced. History replies never write it, so it cannot disagree with
// what the chat was showing. Rows are copied in and out: chatModel.setSize
// re-renders rows in place, and a shared slice would let one chat's resize
// rewrite another's remembered transcript.
//
// Held by pointer on AppModel so the value-copied model keeps one cache.
// Every method is safe on a nil receiver, for AppModels built without one.
type transcriptCache struct {
	rows  map[string][]chatMessage
	order []string // least recently used first
}

func newTranscriptCache() *transcriptCache {
	return &transcriptCache{rows: make(map[string][]chatMessage)}
}

// put remembers a copy of rows under key and marks key most recently used,
// evicting the least recently used session past capacity.
func (c *transcriptCache) put(key string, rows []chatMessage) {
	if c == nil {
		return
	}
	c.rows[key] = append([]chatMessage(nil), rows...)
	c.touch(key)
	for len(c.order) > transcriptCacheCapacity {
		delete(c.rows, c.order[0])
		c.order = c.order[1:]
	}
}

// get returns a copy of the rows remembered under key and marks key most
// recently used.
func (c *transcriptCache) get(key string) ([]chatMessage, bool) {
	if c == nil {
		return nil, false
	}
	rows, ok := c.rows[key]
	if !ok {
		return nil, false
	}
	c.touch(key)
	return append([]chatMessage(nil), rows...), true
}

// drop forgets key.
func (c *transcriptCache) drop(key string) {
	if c == nil {
		return
	}
	delete(c.rows, key)
	c.order = withoutKey(c.order, key)
}

// clear forgets everything.
func (c *transcriptCache) clear() {
	if c == nil {
		return
	}
	c.rows = make(map[string][]chatMessage)
	c.order = nil
}

// touch moves key to the most-recently-used end.
func (c *transcriptCache) touch(key string) {
	c.order = append(withoutKey(c.order, key), key)
}

// withoutKey returns order with key removed, in a new slice.
func withoutKey(order []string, key string) []string {
	kept := make([]string, 0, len(order))
	for _, k := range order {
		if k != key {
			kept = append(kept, k)
		}
	}
	return kept
}
