package world

import (
	"sync"

	"regionio/internal/protocol"
)

// Generator produces the chunk at the given coordinate.
type Generator func(cx, cz int32) *Chunk

// Cache is the live world: it owns the mutable chunk data and memoizes the
// framed, compression-ready level_chunk packet for each chunk. A block edit
// mutates the chunk and invalidates its cached frame so the next request
// re-encodes it.
//
// Frames are built for a fixed compression threshold shared by all play
// connections, so one frame is valid for every client.
//
// Generation can be expensive; it runs outside the lock to avoid blocking other
// chunk requests. An eviction policy belongs here once worlds stream far.
type Cache struct {
	threshold int32
	gen       Generator

	mu     sync.Mutex
	chunks map[[2]int32]*Chunk
	frames map[[2]int32][]byte
}

// NewCache returns a world cache that frames packets at the given compression
// threshold using gen to produce missing chunks.
func NewCache(threshold int32, gen Generator) *Cache {
	return &Cache{
		threshold: threshold,
		gen:       gen,
		chunks:    make(map[[2]int32]*Chunk),
		frames:    make(map[[2]int32][]byte),
	}
}

// chunkAt returns the chunk at (cx, cz), generating it on first access.
func (c *Cache) chunkAt(cx, cz int32) *Chunk {
	key := [2]int32{cx, cz}

	c.mu.Lock()
	if ch, ok := c.chunks[key]; ok {
		c.mu.Unlock()
		return ch
	}
	c.mu.Unlock()

	ch := c.gen(cx, cz) // generate outside the lock

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
// affected chunk's cached frame. It reports whether a chunk was actually
// touched (false if y is out of range).
func (c *Cache) SetBlock(x, y, z int, state uint16) bool {
	if y < MinY || y >= MinY+WorldHeight {
		return false
	}
	cx := int32(x >> 4)
	cz := int32(z >> 4)
	ch := c.chunkAt(cx, cz)

	ch.SetBlock(x, y, z, state)

	c.mu.Lock()
	delete(c.frames, [2]int32{cx, cz})
	c.mu.Unlock()
	return true
}
