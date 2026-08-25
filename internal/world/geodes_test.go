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
