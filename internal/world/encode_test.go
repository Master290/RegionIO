package world

import (
	"testing"

	"regionio/internal/protocol"
)

// parsePalettedContainer consumes one paletted container of entryCount entries.
// The long-array length is derived from bits-per-entry, not length-prefixed.
func parsePalettedContainer(t *testing.T, r *protocol.Reader, maxBits, entryCount int) {
	t.Helper()
	bpe, err := r.ReadByte()
	if err != nil {
		t.Fatalf("bpe: %v", err)
	}
	if bpe == 0 {
		if _, err := r.VarInt(); err != nil { // single value
			t.Fatalf("single value: %v", err)
		}
		return
	}
	if int(bpe) <= maxBits { // indirect: palette precedes data
		n, err := r.VarInt()
		if err != nil || n < 0 {
			t.Fatalf("palette len: %v", err)
		}
		for i := int32(0); i < n; i++ {
			if _, err := r.VarInt(); err != nil {
				t.Fatalf("palette entry: %v", err)
			}
		}
	}
	perLong := 64 / int(bpe)
	longs := (entryCount + perLong - 1) / perLong
	for i := 0; i < longs; i++ {
		if _, err := r.Int64(); err != nil {
			t.Fatalf("data long: %v", err)
		}
	}
}

func skipBitSet(t *testing.T, r *protocol.Reader) {
	t.Helper()
	n, err := r.VarInt()
	if err != nil || n < 0 {
		t.Fatalf("bitset len: %v", err)
	}
	for i := int32(0); i < n; i++ {
		if _, err := r.Int64(); err != nil {
			t.Fatalf("bitset long: %v", err)
		}
	}
}

// TestFlatChunkEncodesCleanly fully parses an encoded flat chunk and asserts
// the byte stream is consumed exactly, with the expected high-level structure.
func TestFlatChunkEncodesCleanly(t *testing.T) {
	body := GenerateFlat(2, -3).Encode()

	// X and Z are plain big-endian ints.
	if got := readInt32(t, body[0:4]); got != 2 {
		t.Fatalf("chunkX = %d, want 2", got)
	}
	if got := readInt32(t, body[4:8]); got != -3 {
		t.Fatalf("chunkZ = %d, want -3", got)
	}
	r := protocol.NewReader(body[8:])

	// Heightmaps: 3 entries, each 37 longs of packed 9-bit heights.
	hmCount, err := r.VarInt()
	if err != nil || hmCount != 3 {
		t.Fatalf("heightmap count = %d (err %v), want 3", hmCount, err)
	}
	for i := int32(0); i < hmCount; i++ {
		if _, err := r.VarInt(); err != nil { // type
			t.Fatalf("hm type: %v", err)
		}
		longs, err := r.VarInt()
		if err != nil || longs != 37 {
			t.Fatalf("hm longs = %d (err %v), want 37", longs, err)
		}
		for j := int32(0); j < longs; j++ {
			if _, err := r.Int64(); err != nil {
				t.Fatalf("hm long: %v", err)
			}
		}
	}

	// Section data block.
	dataLen, err := r.VarInt()
	if err != nil || dataLen <= 0 {
		t.Fatalf("data len = %d (err %v)", dataLen, err)
	}
	nonAirSections := 0
	for s := 0; s < SectionCount; s++ {
		count, err := r.Uint16()
		if err != nil {
			t.Fatalf("section %d count: %v", s, err)
		}
		if _, err := r.Uint16(); err != nil { // reserved 2-byte field
			t.Fatalf("section %d reserved: %v", s, err)
		}
		if count > 0 {
			nonAirSections++
		}
		parsePalettedContainer(t, r, 8, 4096) // blocks
		parsePalettedContainer(t, r, 3, 64)   // biomes
	}
	if nonAirSections != 1 {
		t.Fatalf("non-air sections = %d, want 1 (flat layers live in section 0)", nonAirSections)
	}

	// Block entities.
	if be, err := r.VarInt(); err != nil || be != 0 {
		t.Fatalf("block entities = %d (err %v), want 0", be, err)
	}

	// Light: four bitsets, then sky arrays, then block arrays.
	skipBitSet(t, r) // sky mask
	skipBitSet(t, r) // block mask
	skipBitSet(t, r) // empty sky mask
	skipBitSet(t, r) // empty block mask
	skyArrays, err := r.VarInt()
	if err != nil || skyArrays != lightSections {
		t.Fatalf("sky arrays = %d (err %v), want %d", skyArrays, err, lightSections)
	}
	for i := int32(0); i < skyArrays; i++ {
		n, err := r.VarInt()
		if err != nil || n != 2048 {
			t.Fatalf("sky array len = %d (err %v), want 2048", n, err)
		}
		for j := int32(0); j < n; j++ {
			if _, err := r.ReadByte(); err != nil {
				t.Fatalf("sky byte: %v", err)
			}
		}
	}
	if blockArrays, err := r.VarInt(); err != nil || blockArrays != 0 {
		t.Fatalf("block arrays = %d (err %v), want 0", blockArrays, err)
	}

	if rem := r.Remaining(); rem != 0 {
		t.Fatalf("trailing bytes after parse: %d", rem)
	}
}

func readInt32(t *testing.T, b []byte) int32 {
	t.Helper()
	if len(b) < 4 {
		t.Fatal("short int32")
	}
	return int32(uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]))
}
