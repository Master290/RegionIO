package world

import (
	"encoding/binary"
	"io"
	"os"
	"testing"
)

// fixtureChunk is one chunk of a RIOPAR02 capture.
type fixtureChunk struct {
	cx, cz int32
	states []uint16
	// biomes holds the 1536 4x4x4 biome cells (y outermost, then z, then x) and
	// heights the two compacted 16x16 heightmaps, 384 u16 each. Callers that only
	// need blocks ignore them; they are retained because a capture that disagrees
	// about *where the surface is* invalidates any conclusion drawn from its blocks,
	// and that is only checkable if the parse keeps them.
	biomes  []uint16
	heights []uint16
}

// at reads a chunk-local cell. The capture tool writes y outermost, then z,
// then x, so x is the fastest-varying index.
func (c fixtureChunk) at(x, y, z int) uint16 {
	return c.states[((y-MinY)*16+z)*16+x]
}

// fixtureCapture is a parsed vanilla capture: the world seed and the chunks in
// the order they were written.
type fixtureCapture struct {
	seed   int64
	chunks []fixtureChunk
}

// readFixtureCapture parses a RIOPAR02 capture, keeping blocks, biome cells and
// the three heightmaps for every chunk. Callers that only need blocks use
// fixtureChunk.at; the rest is retained because a capture's blocks can only be
// interpreted if the reader can also show that the biome and the surface height
// underneath them agree.
func readFixtureCapture(t *testing.T, path string) fixtureCapture {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open capture %s: %v", path, err)
	}
	defer f.Close()

	var header [24]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		t.Fatalf("capture header: %v", err)
	}
	if string(header[:8]) != "RIOPAR02" {
		t.Fatalf("capture %s has magic %q", path, header[:8])
	}
	out := fixtureCapture{seed: int64(binary.BigEndian.Uint64(header[8:16]))}
	count := int(binary.BigEndian.Uint32(header[16:20]))
	if count <= 0 {
		t.Fatalf("capture %s holds %d chunks", path, count)
	}
	var scratch [2]byte
	for i := 0; i < count; i++ {
		var coords [8]byte
		if _, err := io.ReadFull(f, coords[:]); err != nil {
			t.Fatalf("capture chunk %d coords: %v", i, err)
		}
		ch := fixtureChunk{
			cx:     int32(binary.BigEndian.Uint32(coords[:4])),
			cz:     int32(binary.BigEndian.Uint32(coords[4:])),
			states: make([]uint16, 98304),
		}
		for j := range ch.states {
			if _, err := io.ReadFull(f, scratch[:]); err != nil {
				t.Fatalf("capture chunk %d state %d: %v", i, j, err)
			}
			ch.states[j] = binary.BigEndian.Uint16(scratch[:])
		}
		// Biome cells (1536) then two heightmaps (2 x 384), all u16.
		tail := make([]uint16, 1536+768)
		for j := range tail {
			if _, err := io.ReadFull(f, scratch[:]); err != nil {
				t.Fatalf("capture chunk %d biomes/heightmaps %d: %v", i, j, err)
			}
			tail[j] = binary.BigEndian.Uint16(scratch[:])
		}
		ch.biomes = tail[:1536]
		ch.heights = tail[1536:]
		out.chunks = append(out.chunks, ch)
	}
	return out
}
