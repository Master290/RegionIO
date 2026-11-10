package world

import (
	"math"
	"testing"
)

func unpackHeightmapForTest(packed []uint64) [256]uint16 {
	const bpe = 9
	const perLong = 64 / bpe // 7
	var out [256]uint16
	for i := 0; i < 256; i++ {
		out[i] = uint16((packed[i/perLong] >> uint((i%perLong)*bpe)) & 0x1FF)
	}
	return out
}

func TestFarCoordinatesGenerationParityAndDeterminism(t *testing.T) {
	farChunks := []struct {
		name   string
		cx, cz int32
	}{
		{"plus_1M", 62_500, 62_500},                  // block (1,000,000, 1,000,000)
		{"minus_1M", -62_500, -62_500},               // block (-1,000,000, -1,000,000)
		{"mixed_10M", 625_000, -625_000},             // block (10,000,000, -10,000,000)
		{"world_border_pos", 1_874_999, 0},           // block (29,999,984 .. 29,999,999, 0)
		{"world_border_neg", -1_874_999, -1_874_999}, // block (-29,999,984, -29,999,984)
	}

	owGen := NewVanillaGenerator(12345)
	netherGen := NewNetherGenerator(12345)
	endGen := NewEndGenerator(12345)

	for _, fc := range farChunks {
		t.Run("Overworld_"+fc.name, func(t *testing.T) {
			c1 := owGen(fc.cx, fc.cz)
			c2 := owGen(fc.cx, fc.cz)
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					// Bottom layer y = -64 must always be bedrock in Overworld.
					if got := c1.GetBlock(lx, MinY, lz); got != StateBedrock {
						t.Fatalf("Overworld(%d,%d) col (%d,%d) y=%d = %d, want StateBedrock (%d)",
							fc.cx, fc.cz, lx, lz, MinY, got, StateBedrock)
					}
					for y := MinY; y < MinY+WorldHeight; y += 16 {
						if b1, b2 := c1.GetBlock(lx, y, lz), c2.GetBlock(lx, y, lz); b1 != b2 {
							t.Fatalf("non-deterministic Overworld block at (%d,%d,%d): %d vs %d", lx, y, lz, b1, b2)
						}
					}
				}
			}
			ws1, mb1, nl1 := c1.heightmaps()
			ws2, mb2, nl2 := c2.heightmaps()
			if ws1 != ws2 || mb1 != mb2 || nl1 != nl2 {
				t.Fatalf("non-deterministic Overworld heightmaps at (%d,%d)", fc.cx, fc.cz)
			}
		})

		t.Run("Nether_"+fc.name, func(t *testing.T) {
			c1 := netherGen(fc.cx, fc.cz)
			c2 := netherGen(fc.cx, fc.cz)
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					// Nether floor (y=0) and ceiling (y=127) must be bedrock.
					if got := c1.GetBlock(lx, 0, lz); got != StateBedrock {
						t.Fatalf("Nether(%d,%d) floor y=0 at (%d,%d) = %d, want StateBedrock", fc.cx, fc.cz, lx, lz, got)
					}
					if got := c1.GetBlock(lx, 127, lz); got != StateBedrock {
						t.Fatalf("Nether(%d,%d) roof y=127 at (%d,%d) = %d, want StateBedrock", fc.cx, fc.cz, lx, lz, got)
					}
					if c1.GetBlock(lx, 64, lz) != c2.GetBlock(lx, 64, lz) {
						t.Fatalf("non-deterministic Nether block at (%d,64,%d)", lx, lz)
					}
				}
			}
		})

		t.Run("End_"+fc.name, func(t *testing.T) {
			c1 := endGen(fc.cx, fc.cz)
			c2 := endGen(fc.cx, fc.cz)
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					if c1.GetBlock(lx, 64, lz) != c2.GetBlock(lx, 64, lz) {
						t.Fatalf("non-deterministic End block at (%d,64,%d)", lx, lz)
					}
				}
			}
		})
	}
}

func TestExtremeHeightsBoundaryAndHeightmaps(t *testing.T) {
	c := NewChunk(0, 0, BiomePlains)

	// Out-of-bounds Y (< -64 or > 319) must not panic or corrupt adjacent cells.
	c.SetBlock(0, MinY-1, 0, StateStone)
	c.SetBlock(0, MinY+WorldHeight, 0, StateStone)
	if got := c.GetBlock(0, MinY-1, 0); got != StateAir {
		t.Fatalf("GetBlock at y=%d = %d, want StateAir", MinY-1, got)
	}
	if got := c.GetBlock(0, MinY+WorldHeight, 0); got != StateAir {
		t.Fatalf("GetBlock at y=%d = %d, want StateAir", MinY+WorldHeight, got)
	}

	// Column (1, 1): empty column -> heightmap stores 0 (firstAvailable = MinY = -64).
	// Column (2, 2): single bedrock at y = -64 -> heightmap stores 1 (firstAvailable = -63, -63 - (-64) = 1).
	c.SetBlock(2, MinY, 2, StateBedrock)

	// Column (3, 3): solid stone at y = 319 (topmost buildable block) -> heightmap stores 384 (320 - (-64) = 384).
	const maxBuildY = MinY + WorldHeight - 1 // 319
	c.SetBlock(3, maxBuildY, 3, StateStone)

	// Column (4, 4): oak leaves at y = 319 and stone at y = -64:
	// WORLD_SURFACE and MOTION_BLOCKING = 384 (y=319+1),
	// MOTION_BLOCKING_NO_LEAVES = 1 (y=-64+1).
	c.SetBlock(4, MinY, 4, StateStone)
	c.SetBlock(4, maxBuildY, 4, StateOakLeaf)

	// Column (5, 5): water at y = 319 and stone at y = -64:
	// WORLD_SURFACE, MOTION_BLOCKING, and MOTION_BLOCKING_NO_LEAVES = 384.
	c.SetBlock(5, MinY, 5, StateStone)
	c.SetBlock(5, maxBuildY, 5, StateWater)

	ws, mb, nl := c.heightmaps()
	idx := func(x, z int) int { return z*16 + x }

	if got := ws[idx(1, 1)]; got != 0 {
		t.Fatalf("empty column WorldSurface = %d, want 0", got)
	}
	if got := ws[idx(2, 2)]; got != 1 {
		t.Fatalf("y=-64 bedrock column WorldSurface = %d, want 1", got)
	}
	if got := ws[idx(3, 3)]; got != WorldHeight {
		t.Fatalf("y=319 stone column WorldSurface = %d, want %d", got, WorldHeight)
	}
	if got := nl[idx(3, 3)]; got != WorldHeight {
		t.Fatalf("y=319 stone column MotionBlockingNoLeaves = %d, want %d", got, WorldHeight)
	}

	// Column (4, 4): leaves at y=319, stone at y=-64
	if got := ws[idx(4, 4)]; got != WorldHeight {
		t.Fatalf("y=319 leaves WorldSurface = %d, want %d", got, WorldHeight)
	}
	if got := mb[idx(4, 4)]; got != WorldHeight {
		t.Fatalf("y=319 leaves MotionBlocking = %d, want %d", got, WorldHeight)
	}
	if got := nl[idx(4, 4)]; got != 1 {
		t.Fatalf("y=319 leaves MotionBlockingNoLeaves = %d, want 1", got)
	}

	// Column (5, 5): water at y=319, stone at y=-64
	if got := ws[idx(5, 5)]; got != WorldHeight {
		t.Fatalf("y=319 water WorldSurface = %d, want %d", got, WorldHeight)
	}
	if got := mb[idx(5, 5)]; got != WorldHeight {
		t.Fatalf("y=319 water MotionBlocking = %d, want %d", got, WorldHeight)
	}
	if got := nl[idx(5, 5)]; got != WorldHeight {
		t.Fatalf("y=319 water MotionBlockingNoLeaves = %d, want %d", got, WorldHeight)
	}

	// Verify 9-bit packed heightmaps round-trip y=319 (height value 384) and y=-64 (height value 0/1) losslessly.
	unpackedWS := unpackHeightmapForTest(packHeightmap(ws))
	unpackedMB := unpackHeightmapForTest(packHeightmap(mb))
	unpackedNL := unpackHeightmapForTest(packHeightmap(nl))
	if unpackedWS != ws || unpackedMB != mb || unpackedNL != nl {
		t.Fatalf("9-bit heightmap packing round-trip mismatch at extreme heights")
	}
}

func TestEdgeAndStringSeedsGenerationParity(t *testing.T) {
	seeds := []struct {
		name string
		raw  string
		want int64
	}{
		{"zero", "0", 0},
		{"minus_one", "-1", -1},
		{"min_int64", "-9223372036854775808", math.MinInt64},
		{"max_int64", "9223372036854775807", math.MaxInt64},
		{"string_north_carolina", "North Carolina", -343522682},
		{"string_zero_hash", "pollinating sandboxes", 0},
		{"string_unicode", "Привет", 1177014952},
	}

	chunksBySeed := make(map[string]*Chunk, len(seeds))
	for _, sc := range seeds {
		parsed, ok := ParseWorldSeed(sc.raw)
		if !ok || parsed != sc.want {
			t.Fatalf("ParseWorldSeed(%q) = (%d, %v), want (%d, true)", sc.raw, parsed, ok, sc.want)
		}
		gen := NewVanillaGenerator(parsed)
		ch := gen(0, 0)
		if got := ch.GetBlock(0, MinY, 0); got != StateBedrock {
			t.Fatalf("seed %s (%d) missing bedrock at y=%d: got %d", sc.name, parsed, MinY, got)
		}
		chunksBySeed[sc.name] = ch
	}

	// String seed "pollinating sandboxes" hashes to 0 in Java String.hashCode() and must
	// generate bit-for-bit identical terrain to numeric seed "0".
	cZero := chunksBySeed["zero"]
	cZeroHash := chunksBySeed["string_zero_hash"]
	if cZero.columnHeights() != cZeroHash.columnHeights() {
		t.Fatalf("seed 0 and string seed 'pollinating sandboxes' (hashCode 0) produced different heightmaps")
	}
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			for y := MinY; y < MinY+WorldHeight; y += 8 {
				if cZero.GetBlock(lx, y, lz) != cZeroHash.GetBlock(lx, y, lz) {
					t.Fatalf("seed 0 vs 'pollinating sandboxes' block mismatch at (%d,%d,%d)", lx, y, lz)
				}
			}
		}
	}

	// Distinct seeds (0, -1, MinInt64) must produce distinct heightmaps at spawn chunk (0, 0).
	hmZero := chunksBySeed["zero"].columnHeights()
	hmMinusOne := chunksBySeed["minus_one"].columnHeights()
	hmMinInt64 := chunksBySeed["min_int64"].columnHeights()
	if hmZero == hmMinusOne {
		t.Fatalf("seed 0 and seed -1 unexpectedly produced identical heightmaps at (0,0)")
	}
	if hmZero == hmMinInt64 {
		t.Fatalf("seed 0 and seed MinInt64 unexpectedly produced identical heightmaps at (0,0)")
	}
}
