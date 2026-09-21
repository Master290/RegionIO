package world

import (
	"hash/crc32"
	"testing"
)

// generatedRegionChunks are the four chunks the committed captures cover. They
// are also the four whose decoration regions overlap, so asking for them in a
// different order is what exercises the shared base-terrain cache.
var generatedRegionChunks = [][2]int32{{0, 0}, {1, 0}, {0, 1}, {-1, -1}}

// TestGeneratedChunkOrderIndependence asserts that a chunk's content does not
// depend on the order the generator was asked for chunks in.
//
// This is a server correctness property, not a parity nicety: the generator is
// called in whatever order players load chunks, so an order-dependent generator
// produces different terrain for the same seed and no later comparison to a
// capture means anything. The hazard that makes it worth a test is
// vanillaTerrainCache - the generator hands out cached base terrain and every
// target decorates private clones of it, and the cross-chunk feature writes reach
// one chunk outside the source. If a clone ever became shallow, or a decorated
// chunk were handed back out of the cache, the first target asked for would stay
// clean and every later one would inherit its neighbours' decoration. That is
// exactly the shape of bug this investigation kept being tempted to blame on
// feature code, and the parity fixtures could not see it because they ask in one
// fixed order.
//
// Two generators are built on purpose: order-independence within one instance is
// the cache property, and equality across instances says the cache is the only
// route by which one request could affect another.
func TestGeneratedChunkOrderIndependence(t *testing.T) {
	reversed := make([][2]int32, len(generatedRegionChunks))
	for i, key := range generatedRegionChunks {
		reversed[len(generatedRegionChunks)-1-i] = key
	}
	checksums := func(order [][2]int32) map[[2]int32]uint32 {
		gen := NewVanillaRegionGenerator(12345)
		out := make(map[[2]int32]uint32, len(order))
		for _, key := range order {
			out[key] = chunkChecksum(gen(key[0], key[1]))
		}
		return out
	}
	forward := checksums(generatedRegionChunks)
	backward := checksums(reversed)
	for _, key := range generatedRegionChunks {
		if forward[key] != backward[key] {
			t.Errorf("chunk (%d,%d) depends on the order it was requested in: %08x requested first, %08x requested last",
				key[0], key[1], forward[key], backward[key])
		}
	}
}

// chunkChecksum hashes every block state in the chunk, Y-major.
func chunkChecksum(c *Chunk) uint32 {
	hash := crc32.NewIEEE()
	buf := make([]byte, 2)
	for y := MinY; y < MinY+WorldHeight; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				state := c.GetBlock(x, y, z)
				buf[0] = byte(state >> 8)
				buf[1] = byte(state)
				_, _ = hash.Write(buf)
			}
		}
	}
	return hash.Sum32()
}
