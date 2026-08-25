package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func TestUnderwaterFloorMatchesColumnScanRange(t *testing.T) {
	chunk := NewChunk(0, 0, BiomePlains)
	region, err := newDecorationRegion([]*Chunk{chunk})
	if err != nil {
		t.Fatal(err)
	}
	for y := 7; y <= 10; y++ {
		chunk.SetBlock(3, y, 4, StateWater)
	}
	chunk.SetBlock(3, 6, 4, StateStone)
	if floor, ok := region.underwaterFloor(3, 10, 4, 5); !ok || floor != 6 {
		t.Fatalf("floor = %d, %v; want 6, true", floor, ok)
	}
	if _, ok := region.underwaterFloor(3, 10, 4, 4); ok {
		t.Fatal("floor outside Column.scan range was accepted")
	}
}

func TestUnderwaterMagmaPlacementIsDeterministic(t *testing.T) {
	makeRegion := func() *decorationRegion {
		chunk := NewChunk(0, 0, BiomePlains)
		for x := 6; x <= 10; x++ {
			for z := 6; z <= 10; z++ {
				for y := 0; y <= 5; y++ {
					chunk.SetBlock(x, y, z, StateStone)
				}
				for y := 6; y <= 10; y++ {
					chunk.SetBlock(x, y, z, StateWater)
				}
			}
		}
		region, err := newDecorationRegion([]*Chunk{chunk})
		if err != nil {
			t.Fatal(err)
		}
		if err := region.setSource(0, 0); err != nil {
			t.Fatal(err)
		}
		return region
	}
	config := worldgen.UnderwaterMagmaFeatureConfig{
		FloorSearchRange: 6, PlacementProbability: 1, PlacementRadiusAroundFloor: 1,
	}
	magma, ok := nameToStateID("minecraft:magma_block", nil)
	if !ok {
		t.Fatal("magma block state missing")
	}
	a, b := makeRegion(), makeRegion()
	origin := worldgen.FeaturePosition{X: 8, Y: 10, Z: 8}
	if !a.placeUnderwaterMagma(worldgen.NewWorldgenRandom(42), origin, config, magma) {
		t.Fatal("magma placement changed no blocks")
	}
	if !b.placeUnderwaterMagma(worldgen.NewWorldgenRandom(42), origin, config, magma) {
		t.Fatal("second magma placement changed no blocks")
	}
	for x := 6; x <= 10; x++ {
		for z := 6; z <= 10; z++ {
			for y := 0; y <= 10; y++ {
				if a.getBlock(x, y, z) != b.getBlock(x, y, z) {
					t.Fatalf("placements differ at (%d,%d,%d)", x, y, z)
				}
			}
		}
	}
}
