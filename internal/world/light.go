package world

import "regionio/internal/protocol"

// lightSections is the number of light subchunks: one below the world and one
// above, plus one per block section.
const lightSections = SectionCount + 2

// writeLight emits a fully-lit sky: every light section carries sky light 15,
// and block light is reported as uniformly empty. This avoids a black world
// without implementing real light propagation (deferred).
func (c *Chunk) writeLight(w *protocol.Writer) {
	full := allSectionsMask()

	writeBitSet(w, full)              // sky light mask: all sections present
	writeBitSet(w, nil)               // block light mask: none present
	writeBitSet(w, nil)               // empty sky light mask: none empty
	writeBitSet(w, full)              // empty block light mask: all empty

	// Sky light arrays: one 2048-byte (4096 nibbles) array of 0x0F per section.
	bright := make([]byte, 2048)
	for i := range bright {
		bright[i] = 0xFF // two nibbles of 15
	}
	w.VarInt(lightSections)
	for i := 0; i < lightSections; i++ {
		w.VarInt(2048)
		w.Raw(bright)
	}

	w.VarInt(0) // no block light arrays
}

// allSectionsMask returns a bitset (as longs) with the low lightSections bits set.
func allSectionsMask() []uint64 {
	return []uint64{(uint64(1) << lightSections) - 1}
}

// writeBitSet emits a length-prefixed array of longs.
func writeBitSet(w *protocol.Writer, longs []uint64) {
	w.VarInt(int32(len(longs)))
	for _, v := range longs {
		w.Int64(int64(v))
	}
}
