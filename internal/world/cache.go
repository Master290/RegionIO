package world

import (
	"container/list"
	"context"
	"errors"
	"fmt"
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
// When a Store is attached (NewCacheWithStore / NewCacheWithLimit), the cache is
// read-through — a chunk miss first tries disk, then generation — and edits mark
// chunks dirty for the background autosave. Frames are built for a fixed
// compression threshold shared by all play connections, so one frame is valid
// for every client.
//
// When maxChunks > 0, the cache evicts least-recently-used chunks to keep memory
// bounded (LRU via a doubly-linked list + index map, O(1) touch/evict). Dirty
// chunks are never evicted until the autosave flushes them, so no edit is lost.
//
// Generation can be expensive; it runs outside the lock to avoid blocking other
// chunk requests.
type Cache struct {
	threshold int32
	gen       Generator
	store     *Store // nil = in-memory only (tests, flat worlds)
	maxChunks int    // LRU capacity; 0 = unbounded

	mu      sync.Mutex
	lightMu sync.Mutex
	chunks  map[[2]int32]*Chunk
	frames  map[[2]int32][]byte
	dirty   map[[2]int32]uint64
	// LRU bookkeeping: order is MRU(front)→LRU(back); index gives O(1) lookup.
	order *list.List // elements are *[2]int32; nil when maxChunks==0
	index map[[2]int32]*list.Element
	loads map[[2]int32]*chunkLoad
}

// chunkLoad coordinates concurrent misses for the same coordinate. The first
// caller performs disk I/O or generation; all others wait for that exact result.
type chunkLoad struct {
	done chan struct{}
	ch   *Chunk
	err  error
}

// NewCache returns a world cache that frames packets at the given compression
// threshold using gen to produce missing chunks. It has no persistence and no
// eviction limit (unbounded; for tests/flat worlds).
func NewCache(threshold int32, gen Generator) *Cache {
	c := &Cache{
		threshold: threshold,
		gen:       gen,
		chunks:    make(map[[2]int32]*Chunk),
		frames:    make(map[[2]int32][]byte),
		dirty:     make(map[[2]int32]uint64),
		loads:     make(map[[2]int32]*chunkLoad),
	}
	return c
}

// NewCacheWithStore returns a cache backed by store: chunk misses load from disk
// first (then fall back to gen), and edits are persisted by the autosave loop.
// The cache is unbounded.
func NewCacheWithStore(threshold int32, gen Generator, store *Store) *Cache {
	c := NewCache(threshold, gen)
	c.store = store
	return c
}

// NewCacheWithLimit is the full constructor: persistence (store may be nil) and
// an LRU cap of maxChunks chunks (0 = unbounded). When bounded, the cache evicts
// least-recently-used chunks on miss, keeping memory near maxChunks×(chunk+frame)
// ≈ maxChunks×200KiB.
func NewCacheWithLimit(threshold int32, gen Generator, store *Store, maxChunks int) *Cache {
	c := NewCacheWithStore(threshold, gen, store)
	if maxChunks < 0 {
		maxChunks = 0
	}
	c.maxChunks = maxChunks
	if maxChunks > 0 {
		c.order = list.New()
		c.index = make(map[[2]int32]*list.Element)
	}
	return c
}

// touch marks key as most-recently-used. Must be called under c.mu.
func (c *Cache) touch(key [2]int32) {
	if c.maxChunks <= 0 {
		return
	}
	if e, ok := c.index[key]; ok {
		c.order.MoveToFront(e)
	} else {
		c.index[key] = c.order.PushFront(&key)
	}
}

// evictIfNeeded drops least-recently-used chunks until len(chunks) <= maxChunks.
// Dirty chunks are skipped (moved back to MRU and the eviction halts) so the
// autosave can persist them first. Must be called under c.mu.
func (c *Cache) evictIfNeeded() {
	if c.maxChunks <= 0 {
		return
	}
	checked := 0
	for len(c.chunks) > c.maxChunks && checked < len(c.chunks) {
		back := c.order.Back()
		if back == nil {
			return
		}
		key := *back.Value.(*[2]int32)
		// Never drop a dirty chunk: it has unsaved edits. Bump it to MRU and
		// stop evicting this cycle; the autosave flush will clear it and the
		// next eviction pass can reclaim it.
		if _, dirty := c.dirty[key]; dirty && c.store != nil {
			c.order.MoveToFront(back)
			checked++
			continue
		}
		delete(c.chunks, key)
		delete(c.frames, key)
		c.order.Remove(back)
		delete(c.index, key)
		checked = 0
	}
}

// chunkAt returns the chunk at (cx, cz). Resolution order: in-memory cache →
// disk (if a store is attached) → generation. Generation and disk reads run
// outside the lock.
func (c *Cache) chunkAt(cx, cz int32) *Chunk {
	ch, _ := c.chunkAtErr(cx, cz)
	return ch
}

// chunkAtErr is the error-preserving form used by network and mutation paths.
// A corrupt or unreadable stored chunk is never replaced by generated terrain.
func (c *Cache) chunkAtErr(cx, cz int32) (*Chunk, error) {
	key := [2]int32{cx, cz}

	c.mu.Lock()
	if ch, ok := c.chunks[key]; ok {
		c.touch(key)
		c.mu.Unlock()
		return ch, nil
	}
	if pending, ok := c.loads[key]; ok {
		c.mu.Unlock()
		<-pending.done
		return pending.ch, pending.err
	}
	pending := &chunkLoad{done: make(chan struct{})}
	c.loads[key] = pending
	c.mu.Unlock()

	// Try disk before generation so saved edits survive restarts.
	var ch *Chunk
	var loadErr error
	if c.store != nil {
		if loaded, err := c.store.LoadChunk(cx, cz); err == nil {
			ch = loaded
		} else if !errors.Is(err, ErrChunkNotFound) {
			loadErr = fmt.Errorf("world: load chunk (%d,%d): %w", cx, cz, err)
		}
	}
	if ch == nil && loadErr == nil {
		ch = c.gen(cx, cz) // generate outside the lock
		if ch == nil {
			loadErr = fmt.Errorf("world: generator returned nil chunk (%d,%d)", cx, cz)
		}
	}

	c.mu.Lock()
	if loadErr == nil {
		c.chunks[key] = ch
		c.touch(key)
		c.evictIfNeeded()
	}
	pending.ch, pending.err = ch, loadErr
	delete(c.loads, key)
	close(pending.done)
	c.mu.Unlock()
	return ch, loadErr
}

// Frame returns the prebuilt level_chunk packet for (cx, cz), building it on
// first request and caching until the chunk is edited. The slice must not be
// mutated.
func (c *Cache) Frame(cx, cz int32) []byte {
	frame, _ := c.FrameErr(cx, cz)
	return frame
}

// FrameErr returns a framed chunk packet while preserving storage failures.
// Callers serving clients should prefer it to Frame so corruption is observable.
func (c *Cache) FrameErr(cx, cz int32) ([]byte, error) {
	key := [2]int32{cx, cz}

	for {
		c.mu.Lock()
		if f, ok := c.frames[key]; ok {
			c.touch(key)
			c.mu.Unlock()
			return f, nil
		}
		c.mu.Unlock()

		ch, err := c.chunkAtErr(cx, cz)
		if err != nil {
			return nil, err
		}
		if err := c.ensureLight(ch); err != nil {
			return nil, err
		}
		snapshot, revision := ch.snapshot()
		frame := protocol.AppendPacket(nil, c.threshold, protocol.PlayLevelChunk, snapshot.encode())

		c.mu.Lock()
		if existing, ok := c.frames[key]; ok {
			c.touch(key)
			c.mu.Unlock()
			return existing, nil
		}
		// An edit or eviction while the frame was being built makes it stale.
		// Retry from a fresh snapshot instead of publishing old bytes forever.
		if c.chunks[key] != ch || ch.currentRevision() != revision {
			c.mu.Unlock()
			continue
		}
		c.frames[key] = frame
		c.touch(key)
		c.evictIfNeeded()
		c.mu.Unlock()
		return frame, nil
	}
}

// GetBlock returns the block state at world coordinates (x, y, z).
// It loads or generates the chunk if necessary.
func (c *Cache) GetBlock(x, y, z int) uint16 {
	if y < MinY || y >= MinY+WorldHeight {
		return 0 // StateAir
	}
	cx := int32(x >> 4)
	cz := int32(z >> 4)
	ch, err := c.chunkAtErr(cx, cz)
	if err != nil {
		return StateAir
	}
	return ch.GetBlock(x, y, z)
}

// LightUpdate returns the standalone light_update body for a loaded chunk.
func (c *Cache) LightUpdate(cx, cz int32) ([]byte, error) {
	ch, err := c.chunkAtErr(cx, cz)
	if err != nil {
		return nil, err
	}
	if err := c.ensureLight(ch); err != nil {
		return nil, err
	}
	return ch.EncodeLightUpdate(), nil
}

// SetBlock changes a block and incrementally updates lighting. Callers that need
// to broadcast every affected light chunk should use SetBlockWithLight.
func (c *Cache) SetBlock(x, y, z int, state uint16) bool {
	valid, _ := c.SetBlockWithLight(x, y, z, state)
	return valid
}

// ChunkPos identifies a chunk changed by a lighting update.
type ChunkPos struct {
	X, Z int32
}

// SetBlockWithLight changes a block and returns the loaded chunks whose stored
// light changed. Lighting operations are serialized so concurrent edits cannot
// publish mutually stale propagation results.
func (c *Cache) SetBlockWithLight(x, y, z int, state uint16) (bool, []ChunkPos) {
	if y < MinY || y >= MinY+WorldHeight {
		return false, nil
	}
	cx := int32(x >> 4)
	cz := int32(z >> 4)
	ch, err := c.chunkAtErr(cx, cz)
	if err != nil {
		return false, nil
	}

	c.lightMu.Lock()
	defer c.lightMu.Unlock()
	live := c.cachedLightNeighborhood(cx, cz)
	for _, neighbor := range live {
		if err := c.ensureLightLocked(neighbor); err != nil {
			return false, nil
		}
	}
	_, changed := ch.setBlock(x, y, z, state)
	if !changed {
		return true, nil
	}
	lightChanged, err := c.updateLightAfterBlockLocked(x, y, z, live)
	if err != nil {
		// The block edit is still valid and dirty; a later Frame call will rebuild
		// its light from the authoritative blocks.
		ch.mu.Lock()
		ch.lightReady = false
		ch.mu.Unlock()
		lightChanged = []ChunkPos{{X: cx, Z: cz}}
	}

	c.mu.Lock()
	key := [2]int32{cx, cz}
	delete(c.frames, key)
	if c.store != nil {
		c.dirty[key] = ch.currentRevision()
	}
	c.touch(key) // edited chunk is most-recently-used
	c.mu.Unlock()
	return true, lightChanged
}

// markDirty flags the chunk at (cx, cz) for the next autosave. Public so tests
// can simulate edits that the generator made worth persisting.
func (c *Cache) markDirty(cx, cz int32) {
	if c.store == nil {
		return
	}
	c.mu.Lock()
	key := [2]int32{cx, cz}
	if ch := c.chunks[key]; ch != nil {
		c.dirty[key] = ch.currentRevision()
	}
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
	c.mu.Unlock()

	var firstErr error
	for _, k := range keys {
		ch := chunks[k]
		if ch == nil {
			c.mu.Lock()
			delete(c.dirty, k)
			c.mu.Unlock()
			continue
		}
		snapshot, savedRevision := ch.snapshot()
		if err := c.store.saveSnapshot(snapshot); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		c.mu.Lock()
		if dirtyRevision, ok := c.dirty[k]; ok && dirtyRevision <= savedRevision {
			delete(c.dirty, k)
		}
		c.evictIfNeeded()
		c.mu.Unlock()
	}
	return firstErr
}

// SaveAll synchronously persists every chunk currently in memory. Used at
// shutdown to guarantee no edit is lost.
func (c *Cache) SaveAll() error {
	if c.store == nil {
		return nil
	}
	return c.flushDirty()
}
