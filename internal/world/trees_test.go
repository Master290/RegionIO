package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func TestStraightBlobTreeFromDatapack(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	config, err := set.Tree("minecraft:oak")
	if err != nil {
		t.Fatal(err)
	}
	chunk := NewChunk(0, 0, BiomePlains)
	chunk.setBlockRaw(8, 63, 8, StateGrass)
	if !placeStraightBlobTree(chunk, worldgen.NewLegacy(42), 8, 64, 8, config) {
		t.Fatal("datapack oak placement failed")
	}
	logs, leaves := 0, 0
	for y := 64; y < 80; y++ {
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				switch chunk.GetBlock(x, y, z) {
				case StateOakLog:
					logs++
				case StateOakLeaf:
					leaves++
				}
			}
		}
	}
	if logs < 4 || leaves < 10 {
		t.Fatalf("tree has %d logs and %d leaves", logs, leaves)
	}
}

func TestBiomeTreeStageProducesTrees(t *testing.T) {
	gen := NewVanillaGenerator(12345)
	chunk := gen(3, -9)
	trees := 0
	for y := SeaLevel; y < 160; y++ {
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				if chunk.GetBlock(x, y, z) == StateOakLog {
					trees++
				}
			}
		}
	}
	if trees == 0 {
		t.Fatal("biome vegetation stages produced no oak logs")
	}
}
