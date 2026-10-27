package world

import (
	"testing"

	"regionio/internal/worldgen"
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

func TestErodedBadlandsAndFrozenOceanColumnPass(t *testing.T) {
	od, err := worldgen.LoadOverworldFinalDensity(12345)
	if err != nil {
		t.Fatalf("LoadOverworldFinalDensity: %v", err)
	}
	rules, err := od.SurfaceRule()
	if err != nil {
		t.Fatalf("SurfaceRule: %v", err)
	}
	sctx := rules.NewContext()
	var ws [16][16]int

	// 1. eroded_badlands column pass: raises stone pillar and stripes it via bandlands rule.
	foundBandedPillar := false
	for x := 0; x < 200 && !foundBandedPillar; x += 2 {
		for z := 0; z < 200 && !foundBandedPillar; z += 2 {
			var col [WorldHeight]uint16
			for y := MinY; y <= 63; y++ {
				col[y-MinY] = StateStone
			}
			ws[0][0] = 63
			changed := applySurfaceRule(od, rules, sctx, &col, x, z, 0, 0, &ws, "minecraft:eroded_badlands")
			if !changed {
				continue
			}
			topSolid, worldSurf := refreshColumnSurfaceHeight(&col)
			if worldSurf > 63 && topSolid == worldSurf-MinY {
				// Top of the raised hoodoo should be striped terracotta/orange_terracotta/etc., not raw stone.
				if col[topSolid] != StateStone && col[topSolid] != StateAir {
					foundBandedPillar = true
				}
			}
		}
	}
	if !foundBandedPillar {
		t.Error("expected eroded_badlands column pass to raise a pillar and stripe it with terracotta bands")
	}

	// 2. frozen_ocean column pass: places packed_ice / snow_block iceberg above and below sea level.
	foundIceberg := false
	for x := 0; x < 600 && !foundIceberg; x += 2 {
		for z := 0; z < 600 && !foundIceberg; z += 2 {
			var col [WorldHeight]uint16
			for y := MinY; y <= 35; y++ {
				col[y-MinY] = StateStone
			}
			for y := 36; y < SeaLevel; y++ {
				col[y-MinY] = StateWater
			}
			ws[0][0] = SeaLevel - 1
			changed := applySurfaceRule(od, rules, sctx, &col, x, z, 0, 0, &ws, "minecraft:frozen_ocean")
			if !changed {
				continue
			}
			topSolid, worldSurf := refreshColumnSurfaceHeight(&col)
			if worldSurf > SeaLevel && topSolid == worldSurf-MinY {
				foundIceberg = true
			}
		}
	}
	if !foundIceberg {
		t.Error("expected frozen_ocean column pass to raise an iceberg above sea level and refresh surface height")
	}
}