package world

// Env-gated parity check of the ocean-ruin replay against a vanilla capture.
// Skips unless both fixtures are provided:
//
//	go run ./cmd/vanillacapture -server server.jar -featureless -blocks-only \
//	  -chunks "5,3;...;9,7" -output <base.bin>
//	go run ./cmd/vanillacapture -server server.jar -no-features -blocks-only \
//	  -chunks "6,4;7,5;8,6;7,4" -output <ruin.bin>
//
//	REGIONIO_BASE_CAPTURE=<base.bin> REGIONIO_RUIN_CAPTURE=<ruin.bin> \
//	  go test ./internal/world/ -run TestOceanRuinCaptureParity -v
//
// The committed TestOceanRuinFixture12345 pins the same ground truth on the
// parity seed; this variant compares every cell of the whole capture region,
// which catches drift the four pinned cells cannot see.

import (
	"encoding/binary"
	"io"
	"os"
	"testing"
)

// readBaseBlocks loads a RIOBASE1 blocks-only capture into per-chunk slices.
func readBaseBlocks(path string, t *testing.T) map[[2]int32][]uint16 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var header [16]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		t.Fatalf("header %s: %v", path, err)
	}
	out := map[[2]int32][]uint16{}
	for {
		var coords [8]byte
		if _, err := io.ReadFull(f, coords[:]); err != nil {
			break
		}
		x := int32(binary.BigEndian.Uint32(coords[:4]))
		z := int32(binary.BigEndian.Uint32(coords[4:]))
		blocks := make([]uint16, 16*16*WorldHeight)
		for i := range blocks {
			var st [2]byte
			if _, err := io.ReadFull(f, st[:]); err != nil {
				t.Fatal(err)
			}
			blocks[i] = binary.BigEndian.Uint16(st[:])
		}
		out[[2]int32{x, z}] = blocks
	}
	return out
}

func TestOceanRuinCaptureParity(t *testing.T) {
	basePath := os.Getenv("REGIONIO_BASE_CAPTURE")
	ruinPath := os.Getenv("REGIONIO_RUIN_CAPTURE")
	if basePath == "" || ruinPath == "" {
		t.Skip("set REGIONIO_BASE_CAPTURE and REGIONIO_RUIN_CAPTURE to vanilla -blocks-only captures")
	}
	initRuinPieceStates()
	base := readBaseBlocks(basePath, t)
	capture := readBaseBlocks(ruinPath, t)

	od, fluidPicker, veins, carver := testWorldgenInputs(t, 12345)
	var chunks []*Chunk
	for cx := int32(5); cx <= 9; cx++ {
		for cz := int32(3); cz <= 7; cz++ {
			chunks = append(chunks, generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, 12345, cx, cz))
		}
	}
	region, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if err := region.placeScheduledStructures(od, 12345, 7, 5); err != nil {
		t.Fatal(err)
	}

	idx := func(x, y, z int) int { return ((y-MinY)*16+z&15)*16 + x&15 }
	diffs := 0
	checked := 0
	for key, cap := range capture {
		baseChunk, ok := base[key]
		if !ok {
			continue
		}
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				for y := 49; y <= 62; y++ {
					wx, wz := int(key[0])*16+x, int(key[1])*16+z
					// Only cells the structure pass can have touched: the
					// ruin footprint columns of the known start.
					if wx < 111 || wx > 119 || wz < 74 || wz > 81 {
						continue
					}
					checked++
					capState := cap[idx(x, y, z)]
					goState := region.getBlock(wx, y, wz)
					if goState == capState {
						continue
					}
					diffs++
					t.Errorf("cell (%d,%d,%d) go=%d (%s) cap=%d (%s) base=%d (%s)",
						wx, y, wz, goState, whoami(goState), capState, whoami(capState),
						baseChunk[idx(x, y, z)], whoami(baseChunk[idx(x, y, z)]))
				}
			}
		}
	}
	t.Logf("ocean ruin capture parity: %d/%d cells exact, %d diffs", checked-diffs, checked, diffs)
}
