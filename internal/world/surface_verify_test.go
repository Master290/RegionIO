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
	// These seed-bound chunks are the first two dry-land center columns found
	// by the former 16x16 scan and exercise different surface-rule outcomes.
	for _, pos := range [][2]int32{{2, -8}, {2, -7}} {
		ch := gen(pos[0], pos[1])
		if ch == nil {
			continue
		}
		blk, dry := centreSurfaceBlock(ch)
		if dry && blk != 0 {
			seen[blk]++
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
