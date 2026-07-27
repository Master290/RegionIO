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

func parseBitSet(t *testing.T, r *protocol.Reader) int {
	t.Helper()
	n, err := r.VarInt()
	if err != nil || n < 0 {
		t.Fatalf("bitset len: %v", err)
	}
	bits := 0
	for i := int32(0); i < n; i++ {
		val, err := r.Int64()
		if err != nil {
			t.Fatalf("bitset long: %v", err)
		}
		// Count set bits
		for val > 0 {
			bits += int(val & 1)
			val >>= 1
		}
	}
	return bits
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
		if _, err := r.Uint16(); err != nil { // fluidCount
			t.Fatalf("section %d fluid count: %v", s, err)
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
	expectedSkyArrays := parseBitSet(t, r)   // sky mask
	expectedBlockArrays := parseBitSet(t, r) // block mask
	parseBitSet(t, r)                        // empty sky mask
	parseBitSet(t, r)                        // empty block mask
	skyArrays, err := r.VarInt()
	if err != nil || skyArrays != int32(expectedSkyArrays) {
		t.Fatalf("sky arrays = %d (err %v), want %d", skyArrays, err, expectedSkyArrays)
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
	blockArrays, err := r.VarInt()
	if err != nil || blockArrays != int32(expectedBlockArrays) {
		t.Fatalf("block arrays = %d (err %v), want %d", blockArrays, err, expectedBlockArrays)
	}
	for i := int32(0); i < blockArrays; i++ {
		n, err := r.VarInt()
		if err != nil || n != 2048 {
			t.Fatalf("block array len = %d (err %v), want 2048", n, err)
		}
		for j := int32(0); j < n; j++ {
			if _, err := r.ReadByte(); err != nil {
				t.Fatalf("block byte: %v", err)
			}
		}
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

// readBiomeContainerAsClient consumes one biome paletted container exactly the
// way the vanilla client does, and returns the number of bytes it used.
//
// The client picks the palette form from the bits-per-entry byte alone, using
// the SECTION_BIOMES strategy: `tableswitch {0..3}` where 0 is single-valued,
// 1-3 are linear (palette prefix present), and every other value falls through
// to the global palette — no palette prefix, and the data re-read at the
// registry's own bit width regardless of the byte we sent. This differs from
// block states, which additionally have a hashmap tier for 5-8 bits.
func readBiomeContainerAsClient(t *testing.T, buf []byte) int {
	t.Helper()
	r := protocol.NewReader(buf)
	bpe, err := r.ReadByte()
	if err != nil {
		t.Fatalf("bits per entry: %v", err)
	}
	if bpe == 0 {
		if _, err := r.VarInt(); err != nil {
			t.Fatalf("single value: %v", err)
		}
		return len(buf) - r.Remaining()
	}
	dataBits := int(bpe)
	if dataBits <= maxBiomeLinearBits {
		n, err := r.VarInt()
		if err != nil || n < 0 {
			t.Fatalf("palette length: %v", err)
		}
		for i := int32(0); i < n; i++ {
			if _, err := r.VarInt(); err != nil {
				t.Fatalf("palette entry %d: %v", i, err)
			}
		}
	} else {
		dataBits = bitsFor(totalBiomes)
	}
	perLong := 64 / dataBits
	longs := (biomeCellsPerSection + perLong - 1) / perLong
	for i := 0; i < longs; i++ {
		if _, err := r.Int64(); err != nil {
			t.Fatalf("data long %d: %v", i, err)
		}
	}
	return len(buf) - r.Remaining()
}

// TestBiomePaletteFormMatchesVanillaThresholds pins the SECTION_BIOMES palette
// contract at the linear/global boundary.
//
// A section holding 9 or more distinct biomes needs 4 bits per entry. Written as
// a linear palette, the client reads it as global instead: it consumes no
// palette prefix and re-reads the long array at 7 bits, so it walks off the end
// of the container and every following field in the chunk payload is
// misaligned. Sections straddling the surface and the cave biomes really do
// carry that many, so this is reachable in ordinary terrain.
//
// The assertion is the desync itself: decode each container the way the client
// would and require it to consume exactly the bytes we produced.
func TestBiomePaletteFormMatchesVanillaThresholds(t *testing.T) {
	globalBits := bitsFor(totalBiomes)

	for _, tc := range []struct {
		name     string
		distinct int
		wantBits int
	}{
		{"one biome stays single-valued", 1, 0},
		{"two biomes", 2, 1},
		{"eight biomes is the widest linear palette", 8, 3},
		{"nine biomes must switch to global", 9, globalBits},
		{"twenty biomes", 20, globalBits},
		{"every registry biome", totalBiomes, globalBits},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cells [biomeCellsPerSection]uint16
			for i := range cells {
				cells[i] = uint16(i % tc.distinct)
			}

			w := protocol.NewWriter(128)
			writeBiomePalette(w, &cells)
			got := w.Bytes()

			if int(got[0]) != tc.wantBits {
				t.Errorf("bits per entry = %d, want %d", got[0], tc.wantBits)
			}
			if used := readBiomeContainerAsClient(t, got); used != len(got) {
				t.Errorf("client consumed %d of %d bytes; container is misframed by %d",
					used, len(got), len(got)-used)
			}
		})
	}
}
