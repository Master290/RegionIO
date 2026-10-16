package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func TestGeodePlacementIsDeterministicAndLayered(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	config, err := set.Geode("minecraft:amethyst_geode")
	if err != nil {
		t.Fatal(err)
	}
	makeRegion := func() *decorationRegion {
		chunks := make([]*Chunk, 0, 25)
		for cx := int32(-2); cx <= 2; cx++ {
			for cz := int32(-2); cz <= 2; cz++ {
				chunk := NewChunk(cx, cz, BiomePlains)
				for y := MinY; y < MinY+WorldHeight; y++ {
					for x := 0; x < 16; x++ {
						for z := 0; z < 16; z++ {
							chunk.setBlockRaw(x, y, z, StateStone)
						}
					}
				}
				chunks = append(chunks, chunk)
			}
		}
		region, err := newDecorationRegion(chunks)
		if err != nil {
			t.Fatal(err)
		}
		if err := region.setSource(0, 0); err != nil {
			t.Fatal(err)
		}
		return region
	}
	origin := worldgen.FeaturePosition{X: 8, Y: 0, Z: 8}
	a, b := makeRegion(), makeRegion()
	if !a.placeGeode(worldgen.NewWorldgenRandom(42), 12345, origin, config, set) {
		t.Fatal("geode placement changed no blocks")
	}
	if !b.placeGeode(worldgen.NewWorldgenRandom(42), 12345, origin, config, set) {
		t.Fatal("second geode placement changed no blocks")
	}
	counts := map[uint16]int{}
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			left := a.chunks[[2]int32{cx, cz}]
			right := b.chunks[[2]int32{cx, cz}]
			for y := MinY; y < MinY+WorldHeight; y++ {
				for x := 0; x < 16; x++ {
					for z := 0; z < 16; z++ {
						if left.GetBlock(x, y, z) != right.GetBlock(x, y, z) {
							t.Fatalf("geodes differ at chunk (%d,%d) block (%d,%d,%d)", cx, cz, x, y, z)
						}
						state := left.GetBlock(x, y, z)
						if state != StateStone {
							counts[state]++
						}
					}
				}
			}
		}
	}
	for _, name := range []string{"minecraft:smooth_basalt", "minecraft:calcite", "minecraft:amethyst_block"} {
		state, ok := nameToStateID(name, nil)
		if !ok || counts[state] == 0 {
			t.Fatalf("geode did not place %s: counts=%v", name, counts)
		}
	}
}

func TestLargeDripstonePlacementScansCavernAndPlacesColumns(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	config, err := set.LargeDripstone("minecraft:large_dripstone")
	if err != nil {
		t.Fatal(err)
	}
	if config.FloorToCeilingSearchRange != 30 || config.ColumnRadiusMin != 3 || config.ColumnRadiusMax != 19 {
		t.Fatalf("unexpected large_dripstone config: %+v", config)
	}

	chunks := make([]*Chunk, 0, 9)
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			chunk := NewChunk(cx, cz, BiomePlains)
			for y := MinY; y < MinY+WorldHeight; y++ {
				state := StateStone
				if y >= 0 && y < 24 {
					state = StateAir
				}
				for x := 0; x < 16; x++ {
					for z := 0; z < 16; z++ {
						chunk.setBlockRaw(x, y, z, state)
					}
				}
			}
			chunks = append(chunks, chunk)
		}
	}
	region, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if err := region.setSource(0, 0); err != nil {
		t.Fatal(err)
	}

	// Solid origin must return early without consuming RNG draws.
	rngA := worldgen.NewWorldgenRandom(99)
	rngB := worldgen.NewWorldgenRandom(99)
	if err := region.placeLargeDripstone(set, rngA, worldgen.FeaturePosition{X: 8, Y: -10, Z: 8}, config); err != nil {
		t.Fatal(err)
	}
	if got, want := rngA.NextInt(), rngB.NextInt(); got != want {
		t.Fatalf("solid origin consumed RNG draws: got %d, want %d", got, want)
	}

	// Open cavern origin (y=10 inside y=0..23 air pocket) must scan floor=-1, ceiling=24 and place dripstone_block.
	if err := region.placeLargeDripstone(set, worldgen.NewWorldgenRandom(12345), worldgen.FeaturePosition{X: 8, Y: 10, Z: 8}, config); err != nil {
		t.Fatal(err)
	}
	dripstoneState, ok := nameToStateID("minecraft:dripstone_block", nil)
	if !ok {
		t.Fatal("missing minecraft:dripstone_block state")
	}
	placed := 0
	center := region.chunks[[2]int32{0, 0}]
	for y := -5; y < 30; y++ {
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				if center.GetBlock(x, y, z) == dripstoneState {
					placed++
				}
			}
		}
	}
	if placed == 0 {
		t.Fatal("expected large_dripstone to place minecraft:dripstone_block in cavern")
	}
}
