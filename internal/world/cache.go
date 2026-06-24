package world

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"regionio/internal/protocol"
)

// Generator produces the chunk at the given coordinate.
type Generator func(cx, cz int32) *Chunk

// Cache is the live world: it owns the mutable chunk data and memoizes the
// framed, compression-ready level_chunk packet for each chunk. A block edit
// mutates the chunk and invalidates its cached frame so the next request
// re-encodes it.
//
// When a Store is attached (NewCacheWithStore), the cache is read-through — a
// chunk miss first tries disk, then generation — and edits mark chunks dirty
// for the background autosave. Frames are built for a fixed compression
// threshold shared by all play connections, so one frame is valid for every
// client.
//
// Generation can be expensive; it runs outside the lock to avoid blocking other
// chunk requests. An eviction policy belongs here once worlds stream far.
type Cache struct {
	threshold int32
	gen       Generator
	store     *Store // nil = in-memory only (tests, flat worlds)

	mu     sync.Mutex
	chunks map[[2]int32]*Chunk
	frames map[[2]int32][]byte
	dirty  map[[2]int32]struct{}
}

// NewCache returns a world cache that frames packets at the given compression
// threshold using gen to produce missing chunks. It has no persistence.
func NewCache(threshold int32, gen Generator) *Cache {
	return &Cache{
		threshold: threshold,
		gen:       gen,
		chunks:    make(map[[2]int32]*Chunk),
		frames:    make(map[[2]int32][]byte),
		dirty:     make(map[[2]int32]struct{}),
	}
}

// NewCacheWithStore returns a cache backed by store: chunk misses load from disk
// first (then fall back to gen), and edits are persisted by the autosave loop.
func NewCacheWithStore(threshold int32, gen Generator, store *Store) *Cache {
	c := NewCache(threshold, gen)
	c.store = store
	return c
}

// chunkAt returns the chunk at (cx, cz). Resolution order: in-memory cache →
// disk (if a store is attached) → generation. Generation and disk reads run
// outside the lock.
func (c *Cache) chunkAt(cx, cz int32) *Chunk {
	key := [2]int32{cx, cz}

	c.mu.Lock()
	if ch, ok := c.chunks[key]; ok {
		c.mu.Unlock()
		return ch
	}
	c.mu.Unlock()

	// Try disk before generation so saved edits survive restarts.
	var ch *Chunk
	if c.store != nil {
		if loaded, err := c.store.LoadChunk(cx, cz); err == nil {
			ch = loaded
		}
	}
	if ch == nil {
		ch = c.gen(cx, cz) // generate outside the lock
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.chunks[key]; ok {
		return existing // another goroutine won the race
	}
	c.chunks[key] = ch
	return ch
}

// Frame returns the prebuilt level_chunk packet for (cx, cz), building it on
// first request and caching until the chunk is edited. The slice must not be
// mutated.
func (c *Cache) Frame(cx, cz int32) []byte {
	key := [2]int32{cx, cz}

	c.mu.Lock()
	if f, ok := c.frames[key]; ok {
		c.mu.Unlock()
		return f
	}
	c.mu.Unlock()

	ch := c.chunkAt(cx, cz)
	frame := protocol.AppendPacket(nil, c.threshold, protocol.PlayLevelChunk, ch.Encode())

	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.frames[key]; ok {
		return existing
	}
	c.frames[key] = frame
	return frame
}

// SetBlock changes the block at world coordinates (x, y, z), invalidating the
// affected chunk's cached frame and marking it dirty for autosave. It reports
// whether a chunk was actually touched (false if y is out of range).
func (c *Cache) SetBlock(x, y, z int, state uint16) bool {
	if y < MinY || y >= MinY+WorldHeight {
		return false
	}
	cx := int32(x >> 4)
	cz := int32(z >> 4)
	ch := c.chunkAt(cx, cz)

	ch.SetBlock(x, y, z, state)

	c.mu.Lock()
	key := [2]int32{cx, cz}
	delete(c.frames, key)
	if c.store != nil {
		c.dirty[key] = struct{}{}
	}
	c.mu.Unlock()
	return true
}

// markDirty flags the chunk at (cx, cz) for the next autosave. Public so tests
// can simulate edits that the generator made worth persisting.
func (c *Cache) markDirty(cx, cz int32) {
	if c.store == nil {
		return
	}
	c.mu.Lock()
	c.dirty[[2]int32{cx, cz}] = struct{}{}
	c.mu.Unlock()
}

// StartAutosave launches a goroutine that periodically persists dirty chunks
// until ctx is cancelled, then performs a final flush. It returns a done channel
// that is closed once the goroutine has fully exited (including the final
// SaveAll) — callers must wait on it before closing the underlying Store to
// avoid racing the saver against Close. Call this once per server lifetime.
func (c *Cache) StartAutosave(ctx context.Context, log *slog.Logger, interval time.Duration) <-chan struct{} {
	done := make(chan struct{})
	if c.store == nil {
		close(done)
		return done // in-memory cache: nothing to save
	}
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				if err := c.SaveAll(); err != nil && log != nil {
					log.Error("world: final autosave failed", "err", err)
				}
				return
			case <-t.C:
				if err := c.flushDirty(); err != nil && log != nil {
					log.Error("world: autosave failed", "err", err)
				}
			}
		}
	}()
	return done
}

// flushDirty saves every chunk currently marked dirty and clears the set.
func (c *Cache) flushDirty() error {
	c.mu.Lock()
	keys := make([][2]int32, 0, len(c.dirty))
	for k := range c.dirty {
		keys = append(keys, k)
	}
	chunks := make(map[[2]int32]*Chunk, len(keys))
	for _, k := range keys {
		chunks[k] = c.chunks[k]
	}
	c.dirty = make(map[[2]int32]struct{})
	c.mu.Unlock()

	for _, k := range keys {
		ch := chunks[k]
		if ch == nil {
			continue
		}
		if err := c.store.SaveChunk(ch); err != nil {
			c.mu.Lock()
			c.dirty[k] = struct{}{} // re-mark; retry next cycle
			c.mu.Unlock()
			return err
		}
	}
	return nil
}

// SaveAll synchronously persists every chunk currently in memory. Used at
// shutdown to guarantee no edit is lost.
func (c *Cache) SaveAll() error {
	if c.store == nil {
		return nil
	}
	c.mu.Lock()
	keys := make([][2]int32, 0, len(c.chunks))
	for k := range c.chunks {
		keys = append(keys, k)
	}
	chunks := make(map[[2]int32]*Chunk, len(keys))
	for _, k := range keys {
		chunks[k] = c.chunks[k]
	}
	c.mu.Unlock()

	var firstErr error
	for _, k := range keys {
		if ch := chunks[k]; ch != nil {
			if err := c.store.SaveChunk(ch); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
