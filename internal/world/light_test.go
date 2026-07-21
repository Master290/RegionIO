package world

import (
	_ "embed"
	"testing"

	"regionio/internal/protocol"
)

// Captured from vanilla 26.1.2 after placing minecraft:glowstone at
// (15,100,8). Layout is YZX over [0..30]x[85..115]x[-7..23].
//
//go:embed testdata/vanilla_glowstone_block_light.bin
var vanillaGlowstoneBlockLight []byte

func TestIncrementalBlockLightAgainstVanillaFixture(t *testing.T) {
	const minX, minY, minZ, size = 0, 85, -7, 31
	if len(vanillaGlowstoneBlockLight) != size*size*size {
		t.Fatalf("vanilla fixture length = %d, want %d", len(vanillaGlowstoneBlockLight), size*size*size)
	}
	cache := NewCache(-1, func(cx, cz int32) *Chunk {
		return NewChunk(cx, cz, BiomePlains)
	})
	for cz := int32(-1); cz <= 1; cz++ {
		for cx := int32(-1); cx <= 1; cx++ {
			cache.chunkAt(cx, cz)
		}
	}
	glowstone := nameToStateID("minecraft:glowstone", nil)
	if valid, _ := cache.SetBlockWithLight(15, 100, 8, glowstone); !valid {
		t.Fatal("glowstone edit rejected")
	}

	fixtureIndex := 0
	mismatches := 0
	for y := minY; y < minY+size; y++ {
		for z := minZ; z < minZ+size; z++ {
			for x := minX; x < minX+size; x++ {
				chunk := cache.chunkAt(int32(x>>4), int32(z>>4))
				_, got, ready := chunk.LightAt(x, y, z)
				want := vanillaGlowstoneBlockLight[fixtureIndex]
				fixtureIndex++
				if !ready || got != want {
					if mismatches < 10 {
						t.Errorf("block light (%d,%d,%d) = %d, ready=%v; vanilla=%d", x, y, z, got, ready, want)
					}
					mismatches++
				}
			}
		}
	}
	if mismatches > 10 {
		t.Errorf("... and %d additional light mismatches", mismatches-10)
	}
}

func TestIncrementalBlockLightCrossesChunkBoundaryAndClears(t *testing.T) {
	cache := NewCache(-1, func(cx, cz int32) *Chunk {
		return NewChunk(cx, cz, BiomePlains)
	})
	left := cache.chunkAt(0, 0)
	right := cache.chunkAt(1, 0)
	if _, err := cache.FrameErr(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.FrameErr(1, 0); err != nil {
		t.Fatal(err)
	}

	glowstone := nameToStateID("minecraft:glowstone", nil)
	if emission := lightEmission(glowstone); emission != 15 {
		t.Fatalf("glowstone emission = %d, want 15", emission)
	}
	valid, changed := cache.SetBlockWithLight(15, 0, 8, glowstone)
	if !valid || !containsChunkPos(changed, 0, 0) || !containsChunkPos(changed, 1, 0) {
		t.Fatalf("place changed = %v, valid=%v; want chunks (0,0) and (1,0)", changed, valid)
	}
	assertLight(t, left, 15, 0, 8, 15)
	assertLight(t, right, 0, 0, 8, 14)
	assertLight(t, right, 1, 0, 8, 13)

	valid, changed = cache.SetBlockWithLight(15, 0, 8, StateAir)
	if !valid || !containsChunkPos(changed, 0, 0) || !containsChunkPos(changed, 1, 0) {
		t.Fatalf("remove changed = %v, valid=%v; want chunks (0,0) and (1,0)", changed, valid)
	}
	assertLight(t, left, 15, 0, 8, 0)
	assertLight(t, right, 0, 0, 8, 0)
}

func TestIncrementalSkyLightSpreadsUnderRoofAcrossChunkBoundary(t *testing.T) {
	cache := NewCache(-1, func(cx, cz int32) *Chunk {
		chunk := NewChunk(cx, cz, BiomePlains)
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				chunk.setBlockRaw(x, 1, z, StateStone)
			}
		}
		return chunk
	})
	left := cache.chunkAt(0, 0)
	right := cache.chunkAt(1, 0)
	if _, err := cache.FrameErr(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.FrameErr(1, 0); err != nil {
		t.Fatal(err)
	}

	assertSky(t, right, 0, 0, 8, 0)
	valid, changed := cache.SetBlockWithLight(15, 1, 8, StateAir)
	if !valid || !containsChunkPos(changed, 0, 0) || !containsChunkPos(changed, 1, 0) {
		t.Fatalf("open changed = %v, valid=%v; want chunks (0,0) and (1,0)", changed, valid)
	}
	assertSky(t, left, 15, 0, 8, 15)
	assertSky(t, right, 0, 0, 8, 14)
	assertSky(t, right, 1, 0, 8, 13)

	valid, changed = cache.SetBlockWithLight(15, 1, 8, StateStone)
	if !valid || !containsChunkPos(changed, 0, 0) || !containsChunkPos(changed, 1, 0) {
		t.Fatalf("close changed = %v, valid=%v; want chunks (0,0) and (1,0)", changed, valid)
	}
	assertSky(t, left, 15, 0, 8, 0)
	assertSky(t, right, 0, 0, 8, 0)
}

func assertLight(t *testing.T, chunk *Chunk, x, y, z int, want byte) {
	t.Helper()
	_, got, ready := chunk.LightAt(x, y, z)
	if !ready || got != want {
		t.Fatalf("block light (%d,%d,%d) = %d, ready=%v; want %d", x, y, z, got, ready, want)
	}
}

func assertSky(t *testing.T, chunk *Chunk, x, y, z int, want byte) {
	t.Helper()
	got, _, ready := chunk.LightAt(x, y, z)
	if !ready || got != want {
		t.Fatalf("sky light (%d,%d,%d) = %d, ready=%v; want %d", x, y, z, got, ready, want)
	}
}

func containsChunkPos(chunks []ChunkPos, x, z int32) bool {
	for _, chunk := range chunks {
		if chunk.X == x && chunk.Z == z {
			return true
		}
	}
	return false
}

func TestEncodeLightUpdateLayout(t *testing.T) {
	chunk := NewChunk(-2, 3, BiomePlains)
	r := protocol.NewReader(chunk.EncodeLightUpdate())

	if x, err := r.VarInt(); err != nil || x != -2 {
		t.Fatalf("chunk x = %d, %v; want -2", x, err)
	}
	if z, err := r.VarInt(); err != nil || z != 3 {
		t.Fatalf("chunk z = %d, %v; want 3", z, err)
	}

	masks := make([]uint64, 4)
	for i := range masks {
		length, err := r.VarInt()
		if err != nil || length < 0 || length > 1 {
			t.Fatalf("mask[%d] length = %d, %v; want 0 or 1", i, length, err)
		}
		if length == 1 {
			value, err := r.Int64()
			if err != nil {
				t.Fatalf("mask[%d]: %v", i, err)
			}
			masks[i] = uint64(value)
		}
	}
	if masks[0] == 0 || masks[2] == 0 {
		t.Fatalf("sky masks were not populated: data=%#x empty=%#x", masks[0], masks[2])
	}
	if masks[1] != 0 || masks[3] != (uint64(1)<<lightSections)-1 {
		t.Fatalf("block masks = data %#x, empty %#x", masks[1], masks[3])
	}

	consumeLightArrays(t, r)
	consumeLightArrays(t, r)
	if r.Remaining() != 0 {
		t.Fatalf("light update trailing bytes = %d", r.Remaining())
	}
}

func consumeLightArrays(t *testing.T, r *protocol.Reader) {
	t.Helper()
	count, err := r.VarInt()
	if err != nil {
		t.Fatal(err)
	}
	for i := int32(0); i < count; i++ {
		length, err := r.VarInt()
		if err != nil || length != 2048 {
			t.Fatalf("light array[%d] length = %d, %v; want 2048", i, length, err)
		}
		for j := int32(0); j < length; j++ {
			if _, err := r.ReadByte(); err != nil {
				t.Fatalf("light array[%d] byte %d: %v", i, j, err)
			}
		}
	}
}
