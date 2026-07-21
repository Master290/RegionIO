package world

import "testing"

func TestSafeSpawnYUsesGeneratedSurface(t *testing.T) {
	cache := NewCache(-1, GenerateFlat)
	y, ok := cache.SafeSpawnY(8, 8)
	if !ok || y != FlatSurfaceY+1 {
		t.Fatalf("SafeSpawnY = %d, %v; want %d, true", y, ok, FlatSurfaceY+1)
	}
}

func TestSafeSpawnYRejectsUnderwaterColumn(t *testing.T) {
	cache := NewCache(-1, func(cx, cz int32) *Chunk {
		chunk := NewChunk(cx, cz, BiomePlains)
		chunk.setBlockRaw(8, 60, 8, StateStone)
		for y := 61; y <= SeaLevel; y++ {
			chunk.setBlockRaw(8, y, 8, StateWater)
		}
		return chunk
	})
	if y, ok := cache.SafeSpawnY(8, 8); ok {
		t.Fatalf("SafeSpawnY = %d, true; want underwater column rejected", y)
	}
}

func TestSafeSpawnYAcceptsNonOpaqueSolidFloor(t *testing.T) {
	stairs := nameToStateID("minecraft:oak_stairs", nil)
	if stairs == StateAir {
		t.Fatal("oak stairs state is unavailable")
	}
	cache := NewCache(-1, func(cx, cz int32) *Chunk {
		chunk := NewChunk(cx, cz, BiomePlains)
		chunk.setBlockRaw(8, 70, 8, stairs)
		return chunk
	})
	y, ok := cache.SafeSpawnY(8, 8)
	if !ok || y != 71 {
		t.Fatalf("SafeSpawnY = %d, %v; want 71, true for stairs", y, ok)
	}
}
