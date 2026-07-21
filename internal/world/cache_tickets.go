package world

import "sync"

func (c *Cache) beginUse(key [2]int32) func() {
	c.mu.Lock()
	c.inflight[key]++
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		c.inflight[key]--
		if c.inflight[key] <= 0 {
			delete(c.inflight, key)
		}
		c.evictIfNeeded()
		c.mu.Unlock()
	}
}

// TicketLevel describes why a streamer keeps a chunk resident. View tickets
// are client-visible; Prefetch tickets retain the predictive outer ring.
type TicketLevel uint8

const (
	TicketView TicketLevel = iota
	TicketPrefetch
)

// TicketSet is one owner's atomic chunk-residency claim. A streamer replaces
// the complete set on recenter and closes it on disconnect. Multiple sets may
// overlap; a chunk becomes evictable only after its final owner releases it.
type TicketSet struct {
	mu     sync.Mutex
	cache  *Cache
	held   map[[2]int32]TicketLevel
	closed bool
}

// NewTicketSet creates an empty ticket set associated with this cache.
func (c *Cache) NewTicketSet() *TicketSet {
	return &TicketSet{cache: c, held: make(map[[2]int32]TicketLevel)}
}

// Replace atomically changes the ticket set. View entries take precedence when
// a coordinate is present in both slices.
func (t *TicketSet) Replace(view, prefetch []ChunkPos) {
	if t == nil || t.cache == nil {
		return
	}
	desired := make(map[[2]int32]TicketLevel, len(view)+len(prefetch))
	for _, pos := range prefetch {
		desired[[2]int32{pos.X, pos.Z}] = TicketPrefetch
	}
	for _, pos := range view {
		desired[[2]int32{pos.X, pos.Z}] = TicketView
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.cache.mu.Lock()
	for key := range t.held {
		if _, keep := desired[key]; keep {
			continue
		}
		t.cache.tickets[key]--
		if t.cache.tickets[key] <= 0 {
			delete(t.cache.tickets, key)
		}
	}
	for key := range desired {
		if _, alreadyHeld := t.held[key]; !alreadyHeld {
			t.cache.tickets[key]++
		}
	}
	t.held = desired
	t.cache.evictIfNeeded()
	t.cache.mu.Unlock()
}

// Close releases every ticket. It is safe to call more than once.
func (t *TicketSet) Close() {
	if t == nil || t.cache == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.cache.mu.Lock()
	for key := range t.held {
		t.cache.tickets[key]--
		if t.cache.tickets[key] <= 0 {
			delete(t.cache.tickets, key)
		}
	}
	clear(t.held)
	t.closed = true
	t.cache.evictIfNeeded()
	t.cache.mu.Unlock()
}

// CacheStats is a concurrency-safe lifecycle snapshot used by diagnostics and
// load tests.
type CacheStats struct {
	Chunks         int
	Frames         int
	TicketedChunks int
	Tickets        int
}

// Stats returns current cache and ticket counts.
func (c *Cache) Stats() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	stats := CacheStats{Chunks: len(c.chunks), Frames: len(c.frames), TicketedChunks: len(c.tickets)}
	for _, count := range c.tickets {
		stats.Tickets += count
	}
	return stats
}
