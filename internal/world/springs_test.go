package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func TestSpringFeaturePlacesFallingFluid(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
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
	placeSpring(set, chunk, 8, 10, 8, config)
	falling, ok := nameToStateID("minecraft:water", map[string]string{"level": "8"})
	if !ok || chunk.GetBlock(8, 10, 8) != falling {
		t.Fatalf("spring state = %d, want falling water %d", chunk.GetBlock(8, 10, 8), falling)
	}
}

// TestSpringCountsNonDefaultValidStateAsRock is the regression guard for the
// fourth membership set in the package that resolved a block list to each member's
// default state. SpringFeature requires an exact rock_count, so a valid block in a
// non-default state was counted as a hole and the spring was silently suppressed
// where vanilla places one.
//
// deepslate is a valid_blocks entry of spring_water and spring_lava_overworld and
// carries three states in this build, which is what makes the two readings differ.
// The stone case above cannot show it: stone has one state - as do powder_snow and
// gravel, which is the first thing guessed for this slot and turned out to be
// wrong, hence this sentence.
func TestSpringCountsNonDefaultValidStateAsRock(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	defaultDeepslate, ok := nameToStateID("minecraft:deepslate", nil)
	if !ok {
		t.Fatal("deepslate is not in the state table")
	}
	other := uint16(0)
	for _, id := range idsByName["minecraft:deepslate"] {
		if id != defaultDeepslate {
			other = id
			break
		}
	}
	if other == 0 {
		t.Skip("deepslate has only one state in this build, so the two readings cannot differ")
	}

	chunk := NewChunk(0, 0, BiomePlains)
	for _, pos := range [][3]int{{8, 11, 8}, {8, 9, 8}, {7, 10, 8}, {9, 10, 8}, {8, 10, 7}} {
		chunk.setBlockRaw(pos[0], pos[1], pos[2], other)
	}
	config := worldgen.SpringFeatureConfig{
		HoleCount: 1, RequiresBlockBelow: true, RockCount: 4,
		State:       worldgen.BlockState{Name: "minecraft:water", Properties: map[string]string{"falling": "true"}},
		ValidBlocks: []string{"minecraft:deepslate"},
	}
	placeSpring(set, chunk, 8, 10, 8, config)

	falling, ok := nameToStateID("minecraft:water", map[string]string{"level": "8"})
	if !ok || chunk.GetBlock(8, 10, 8) != falling {
		t.Fatalf("spring under a non-default deepslate state = %s, want falling water: a valid "+
			"block must count as rock in every state, as vanilla's BlockStateIngredient does",
			stateLabel(chunk.GetBlock(8, 10, 8)))
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
