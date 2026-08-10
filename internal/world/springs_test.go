package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func TestSpringFeaturePlacesFallingFluid(t *testing.T) {
	chunk := NewChunk(0, 0, BiomePlains)
	valid := []string{"minecraft:stone"}
	for _, pos := range [][3]int{{8, 11, 8}, {8, 9, 8}, {7, 10, 8}, {9, 10, 8}, {8, 10, 7}} {
		chunk.setBlockRaw(pos[0], pos[1], pos[2], StateStone)
	}
	config := worldgen.SpringFeatureConfig{
		HoleCount: 1, RequiresBlockBelow: true, RockCount: 4,
		State:       worldgen.BlockState{Name: "minecraft:water", Properties: map[string]string{"falling": "true"}},
		ValidBlocks: valid,
	}
	placeSpring(chunk, 8, 10, 8, config)
	falling, ok := nameToStateID("minecraft:water", map[string]string{"level": "8"})
	if !ok || chunk.GetBlock(8, 10, 8) != falling {
		t.Fatalf("spring state = %d, want falling water %d", chunk.GetBlock(8, 10, 8), falling)
	}
}

func TestPlacedSpringsAreDeterministic(t *testing.T) {
	gen := NewVanillaGenerator(12345)
	a, b := gen(-3, 4), gen(-3, 4)
	for y := MinY; y < MinY+WorldHeight; y++ {
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				if got, want := a.GetBlock(x, y, z), b.GetBlock(x, y, z); got != want {
					t.Fatalf("block (%d,%d,%d): first %d second %d", x, y, z, got, want)
				}
			}
		}
	}
}
