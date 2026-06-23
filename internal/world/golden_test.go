package world

import (
	"bytes"
	"os"
	"testing"

	"regionio/internal/protocol"
)

// extractSectionData returns the section-data byte slice of a level_chunk body
// (the bytes covered by the VarInt size that follows the heightmaps).
func extractSectionData(t *testing.T, body []byte) []byte {
	t.Helper()
	r := protocol.NewReader(body[8:]) // skip chunk X,Z
	count, _ := r.VarInt()
	for i := int32(0); i < count; i++ {
		r.VarInt()         // heightmap type
		n, _ := r.VarInt() // long count
		for j := int32(0); j < n; j++ {
			r.Int64()
		}
	}
	size, _ := r.VarInt()
	consumed := len(body[8:]) - r.Remaining()
	return body[8+consumed : 8+consumed+int(size)]
}

// TestGoldenAgainstVanilla asserts our flat-chunk section data is byte-for-byte
// identical to a chunk captured from the official 26.1.2 server (same world
// coordinate). This guards the paletted-container and heightmap encoding.
// Light is intentionally not compared (we send full-bright, which differs).
func TestGoldenAgainstVanilla(t *testing.T) {
	vanilla, err := os.ReadFile("testdata/vanilla_flat_chunk.bin")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	ours := GenerateFlat(0, -1).Encode() // fixture was captured at chunk (0, -1)
	want := extractSectionData(t, vanilla)
	got := extractSectionData(t, ours)

	if !bytes.Equal(want, got) {
		t.Fatalf("section data differs: vanilla=%d bytes, ours=%d bytes", len(want), len(got))
	}
}
