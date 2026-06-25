package world

import (
	"testing"
)

// flatGen returns a generator that produces distinct chunks keyed by coordinate,
// so eviction is observable: each (cx,cz) gets a chunk whose only block encodes
// its position (block at local 0,SeaLevel,0 = a sentinel derived from coords).
func flatGen() Generator {
	return func(cx, cz int32) *Chunk {
		c := NewChunk(cx, cz, BiomePlains)
		si := (SeaLevel - MinY) >> 4
		c.section(si)
		// Sentinel: the block at column (0,0) of the surface is the chunk's
		// low byte cx, and (1,0) is cz, so a reloaded chunk proves it's the
		// right coordinate.
		c.SetBlock(0, SeaLevel, 0, uint16(cx&0xFF))
		c.SetBlock(1, SeaLevel, 0, uint16(cz&0xFF))
		return c
	}
}

// cachedCount returns the number of chunks currently in the cache.
func cachedCount(c *Cache) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.chunks)
}

// hasChunk reports whether the cache holds the given chunk key.
func hasChunk(c *Cache, cx, cz int32) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.chunks[[2]int32{cx, cz}]
	return ok
}

// TestEvictionRespectsLimit confirms the cache stays at or below maxChunks after
// a burst of Frame calls that would otherwise grow it unbounded.
func TestEvictionRespectsLimit(t *testing.T) {
	c := NewCacheWithLimit(int32(256), flatGen(), nil, 4)
	for cx := int32(0); cx < 6; cx++ {
		c.Frame(cx, 0)
	}
	if got := cachedCount(c); got > 4 {
		t.Errorf("cached count = %d, want <= 4 after inserting 6", got)
	}
	// The oldest two (0,0) and (1,0) should have been evicted.
	if hasChunk(c, 0, 0) {
		t.Error("(0,0) should have been evicted as LRU")
	}
	if hasChunk(c, 1, 0) {
		t.Error("(1,0) should have been evicted as LRU")
	}
}

// TestEvictionLRUOrder confirms a touched (re-accessed) chunk survives while an
// untouched one between is evicted. Insert A B C D (cap 4); touch A; insert E →
// B (not A) is the victim.
func TestEvictionLRUOrder(t *testing.T) {
	c := NewCacheWithLimit(int32(256), flatGen(), nil, 4)
	c.Frame(0, 0) // A
	c.Frame(1, 0) // B
	c.Frame(2, 0) // C
	c.Frame(3, 0) // D
	// Re-access A so it is most-recently-used; B becomes least.
	c.Frame(0, 0)
	c.Frame(4, 0) // E → evicts B
	if !hasChunk(c, 0, 0) {
		t.Error("(0,0)/A should survive after being touched")
	}
	if hasChunk(c, 1, 0) {
		t.Error("(1,0)/B should have been evicted (least recently used)")
	}
}

// TestEvictionDropsBothMaps confirms eviction removes the entry from both the
// chunks and frames maps (otherwise memory would still leak).
func TestEvictionDropsBothMaps(t *testing.T) {
	c := NewCacheWithLimit(int32(256), flatGen(), nil, 2)
	c.Frame(0, 0)
	c.Frame(1, 0)
	c.Frame(2, 0) // evicts (0,0)
	c.mu.Lock()
	_, hasChunkMap := c.chunks[[2]int32{0, 0}]
	_, hasFrameMap := c.frames[[2]int32{0, 0}]
	c.mu.Unlock()
	if hasChunkMap {
		t.Error("evicted chunk still present in chunks map")
	}
	if hasFrameMap {
		t.Error("evicted chunk still present in frames map")
	}
}

// TestEvictionKeepsDirty confirms a dirty chunk (pending autosave) is NOT
// evicted, so its edits survive until flushed.
func TestEvictionKeepsDirty(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c := NewCacheWithLimit(int32(256), flatGen(), store, 2)

	c.Frame(0, 0) // load/generate into cache
	// Mark it dirty via a block edit (sets dirty + touches).
	if !c.SetBlock(0, SeaLevel, 0, StateBedrock) {
		t.Fatal("SetBlock failed")
	}
	// Now insert two more chunks to exceed the cap of 2; (0,0) is dirty and
	// must be retained.
	c.Frame(1, 0)
	c.Frame(2, 0)
	if !hasChunk(c, 0, 0) {
		t.Error("dirty chunk (0,0) was evicted; edits would be lost")
	}
}

// TestEvictionReloadsOnAccess confirms a chunk evicted then re-requested is
// regenerated (or loaded from disk) transparently and serves a valid frame.
func TestEvictionReloadsOnAccess(t *testing.T) {
	c := NewCacheWithLimit(int32(256), flatGen(), nil, 2)
	c.Frame(5, 7)
	c.Frame(6, 7)
	c.Frame(7, 7) // evicts (5,7)
	if hasChunk(c, 5, 7) {
		t.Fatal("(5,7) should have been evicted")
	}
	// Re-request: must regenerate and return a non-empty frame.
	frame := c.Frame(5, 7)
	if len(frame) == 0 {
		t.Fatal("reloaded chunk frame is empty")
	}
	if !hasChunk(c, 5, 7) {
		t.Error("re-requested chunk not present in cache after reload")
	}
}

// TestEvictionReloadPreservesEdits confirms that a dirty chunk, once flushed by
// the autosave and then evicted, reloads its saved edits from disk (not a stale
// re-generation). This is the end-to-end "edits survive eviction" guarantee.
func TestEvictionReloadPreservesEdits(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c := NewCacheWithLimit(int32(256), flatGen(), store, 2)

	// Edit (9,9) and flush it to disk.
	c.SetBlock(9*16+0, SeaLevel, 9*16+0, StateBedrock)
	if err := c.SaveAll(); err != nil {
		t.Fatal(err)
	}
	// Force eviction of (9,9) by pulling in other chunks (cap is 2; (9,9) is
	// dirty-but-now-flushed so it can be evicted).
	c.Frame(10, 10)
	c.Frame(11, 11)
	// Keep touching others until (9,9) is gone or we've filled beyond it. Since
	// it's no longer dirty after SaveAll, the next eviction pass can drop it.
	c.Frame(12, 12)
	// Reload (9,9) — should come from disk with the bedrock edit intact.
	frameBefore := c.Frame(9, 9)
	if len(frameBefore) == 0 {
		t.Fatal("reloaded frame empty")
	}
	ch := c.chunkAt(9, 9)
	if got := ch.GetBlock(9*16+0, SeaLevel, 9*16+0); got != StateBedrock {
		t.Errorf("after eviction+reload, edited block = %d, want bedrock %d", got, StateBedrock)
	}
}
