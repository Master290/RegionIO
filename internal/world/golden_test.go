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

// extractLightData returns the complete LightData tail of level_chunk_with_light.
func extractLightData(t *testing.T, body []byte) []byte {
	t.Helper()
	r := protocol.NewReader(body[8:])
	heightmaps, err := r.VarInt()
	if err != nil {
		t.Fatal(err)
	}
	for i := int32(0); i < heightmaps; i++ {
		if _, err := r.VarInt(); err != nil {
			t.Fatal(err)
		}
		longs, err := r.VarInt()
		if err != nil {
			t.Fatal(err)
		}
		for j := int32(0); j < longs; j++ {
			if _, err := r.Int64(); err != nil {
				t.Fatal(err)
			}
		}
	}
	sectionBytes, err := r.VarInt()
	if err != nil {
		t.Fatal(err)
	}
	for i := int32(0); i < sectionBytes; i++ {
		if _, err := r.ReadByte(); err != nil {
			t.Fatal(err)
		}
	}
	blockEntities, err := r.VarInt()
	if err != nil {
		t.Fatal(err)
	}
	if blockEntities != 0 {
		t.Fatalf("fixture has %d block entities; extractor only supports zero", blockEntities)
	}
	offset := len(body) - r.Remaining()
	return body[offset:]
}

// TestGoldenAgainstVanilla asserts our flat chunk's section and light data are
// byte-for-byte identical to a capture from the official 26.1.2 server.
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

	wantLight := extractLightData(t, vanilla)
	gotLight := extractLightData(t, ours)
	if !bytes.Equal(wantLight, gotLight) {
		first := 0
		for first < len(wantLight) && first < len(gotLight) && wantLight[first] == gotLight[first] {
			first++
		}
		t.Fatalf("light data differs at byte %d: vanilla=%d bytes %v %x, ours=%d bytes %v %x", first, len(wantLight), lightSummary(t, wantLight), wantLight[:24], len(gotLight), lightSummary(t, gotLight), gotLight[:24])
	}
}

func lightSummary(t *testing.T, data []byte) [6]uint64 {
	t.Helper()
	r := protocol.NewReader(data)
	var summary [6]uint64
	for i := 0; i < 4; i++ {
		longs, err := r.VarInt()
		if err != nil {
			t.Fatal(err)
		}
		for j := int32(0); j < longs; j++ {
			value, err := r.Int64()
			if err != nil {
				t.Fatal(err)
			}
			if j == 0 {
				summary[i] = uint64(value)
			}
		}
	}
	for i := 0; i < 2; i++ {
		count, err := r.VarInt()
		if err != nil {
			t.Fatal(err)
		}
		summary[4+i] = uint64(count)
		for j := int32(0); j < count; j++ {
			length, err := r.VarInt()
			if err != nil {
				t.Fatal(err)
			}
			for k := int32(0); k < length; k++ {
				if _, err := r.ReadByte(); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	return summary
}
