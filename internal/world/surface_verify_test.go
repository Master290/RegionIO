package world

import (
	"testing"
)

// TestSurfaceVariesByBiome is the load-bearing correctness check for surface
// rules: it generates real chunks and confirms the top surface block differs
// across biomes. Before surface rules every chunk resolved to grass (9); after,
// deserts/oceans/badlands carry sand/gravel/terracotta-family blocks.
func TestSurfaceVariesByBiome(t *testing.T) {
	gen := NewVanillaGenerator(12345)
	seen := make(map[uint16]int) // surfaceBlockID → chunk count
	// Scan a moderate area to find dry land (surface above sea level), where
	// surface rules actually place biome-specific blocks. Ocean columns sit
	// below sea level and stay stone, which is correct.
	for cx := 0; cx < 16; cx++ {
		for cz := 0; cz < 16; cz++ {
			ch := gen(int32(cx)-8, int32(cz)-8)
			if ch == nil {
				continue
			}
			blk, dry := centreSurfaceBlock(ch)
			if !dry {
				continue // skip ocean/underwater columns
			}
			if blk != 0 {
				seen[blk]++
			}
		}
	}
	if len(seen) < 2 {
		t.Fatalf("expected >=2 distinct dry-land surface blocks, got %d (%v)", len(seen), seen)
	}
	t.Logf("dry-land surface block distribution: %v", seen)
}

// centreSurfaceBlock returns the topmost non-air/non-water block at (8,8) and
// whether that block sits at or above sea level (i.e. it is dry land, where
// surface rules apply rather than being submerged).
func centreSurfaceBlock(ch *Chunk) (uint16, bool) {
	for i := SectionCount - 1; i >= 0; i-- {
		s := ch.sections[i]
		if s == nil {
			continue
		}
		for ly := 15; ly >= 0; ly-- {
			st := s[blockIndex(8, MinY+i*16+ly, 8)]
			if st != StateAir && st != StateWater {
				// Dry only if this top block is at/above sea level.
				return st, (MinY + i*16 + ly) >= SeaLevel
			}
		}
	}
	return 0, false
}
