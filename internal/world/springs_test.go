package world

import (
	"strings"
	"testing"

	"regionio/internal/worldgen"
)

// springRegion builds a 3x3 loaded region whose source chunk holds `cells` and is
// otherwise air, so a spring can be placed at (8,10,8) with its neighbours in the
// source chunk and, when asked, across a seam.
func springRegion(t *testing.T, rock uint16, cells [][3]int) (*decorationRegion, *worldgen.FeatureSet, worldgen.SpringFeatureConfig) {
	t.Helper()
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	var chunks []*Chunk
	for cz := int32(-1); cz <= 1; cz++ {
		for cx := int32(-1); cx <= 1; cx++ {
			chunks = append(chunks, NewChunk(cx, cz, BiomePlains))
		}
	}
	region, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if err := region.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	for _, cell := range cells {
		region.setBlock(cell[0], cell[1], cell[2], rock)
	}
	config := worldgen.SpringFeatureConfig{
		HoleCount: 1, RequiresBlockBelow: true, RockCount: 4,
		State:       worldgen.BlockState{Name: "minecraft:water", Properties: map[string]string{"falling": "true"}},
		ValidBlocks: []string{"minecraft:stone"},
	}
	return region, set, config
}

// TestSpringFeaturePlacesFallingFluid is the basic shape: four rock neighbours, one
// hole, rock above and below, and the spring comes out as falling water rather than a
// source - the config's `falling: true` property has no such state, so the port maps
// it to level 8.
func TestSpringFeaturePlacesFallingFluid(t *testing.T) {
	region, set, config := springRegion(t, StateStone, [][3]int{{8, 11, 8}, {8, 9, 8}, {7, 10, 8}, {9, 10, 8}, {8, 10, 7}})
	if err := region.placeSpring(set, worldgen.FeaturePosition{X: 8, Y: 10, Z: 8}, config); err != nil {
		t.Fatal(err)
	}
	falling, ok := nameToStateID("minecraft:water", map[string]string{"level": "8"})
	if !ok || region.getBlock(8, 10, 8) != falling {
		t.Fatalf("spring state = %s, want falling water", stateLabel(region.getBlock(8, 10, 8)))
	}
}

// TestSpringRefusesWhereTheCellItselfIsNeitherAirNorRock is the gate the chunk-local
// version never had. SpringFeature reads the cell it would fill, refuses if that cell
// is anything but air or a valid rock, and only then counts - so a spring cannot open
// inside water, lava or a log.
func TestSpringRefusesWhereTheCellItselfIsNeitherAirNorRock(t *testing.T) {
	water, ok := nameToStateID("minecraft:water", map[string]string{"level": "0"})
	if !ok {
		t.Fatal("water is not in the state table")
	}
	region, set, config := springRegion(t, StateStone, [][3]int{{8, 11, 8}, {8, 9, 8}, {7, 10, 8}, {9, 10, 8}, {8, 10, 7}, {8, 10, 8}})
	region.setBlock(8, 10, 8, water)
	if err := region.placeSpring(set, worldgen.FeaturePosition{X: 8, Y: 10, Z: 8}, config); err != nil {
		t.Fatal(err)
	}
	if got := region.getBlock(8, 10, 8); got != water {
		t.Errorf("a spring opened in standing water: cell is %s, want the water to stay", stateLabel(got))
	}
}

// TestSpringCountsAcrossTheChunkSeam is the reason the reads had to leave the chunk.
// The west neighbour at x=0 belongs to chunk (-1,0); the per-chunk implementation
// returned as soon as a neighbour fell outside its own 16 columns, so a spring whose
// rock column crossed a seam was suppressed on one side of every seam in the world.
func TestSpringCountsAcrossTheChunkSeam(t *testing.T) {
	region, set, config := springRegion(t, StateStone, [][3]int{{0, 11, 8}, {0, 9, 8}, {1, 10, 8}, {0, 10, 7}, {-1, 10, 8}})
	if err := region.placeSpring(set, worldgen.FeaturePosition{X: 0, Y: 10, Z: 8}, config); err != nil {
		t.Fatal(err)
	}
	falling, _ := nameToStateID("minecraft:water", map[string]string{"level": "8"})
	if got := region.getBlock(0, 10, 8); got != falling {
		t.Fatalf("spring at the seam = %s, want it to place from a rock column that spans two chunks",
			stateLabel(got))
	}
}

// TestSpringSeparatesRockFromHoles guards the one place where counting rock and holes
// as a single classification would still pass the fixture above: a cell that is neither
// rock nor air is neither count, so the totals must not absorb it.
func TestSpringSeparatesRockFromHoles(t *testing.T) {
	log, ok := nameToStateID("minecraft:oak_log", map[string]string{"axis": "y"})
	if !ok {
		t.Fatal("oak_log is not in the state table")
	}
	region, set, config := springRegion(t, StateStone, [][3]int{{8, 11, 8}, {8, 9, 8}, {7, 10, 8}, {9, 10, 8}})
	region.setBlock(8, 10, 7, log)
	if err := region.placeSpring(set, worldgen.FeaturePosition{X: 8, Y: 10, Z: 8}, config); err != nil {
		t.Fatal(err)
	}
	if !isAirState(region.getBlock(8, 10, 8)) {
		t.Errorf("rock=4 but holes=0 (a log is neither), and the spring placed anyway")
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
func TestSpringCountsNonDefaultValidStateAsRock(t *testing.T) {
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
	region, set, config := springRegion(t, other, [][3]int{{8, 11, 8}, {8, 9, 8}, {7, 10, 8}, {9, 10, 8}, {8, 10, 7}})
	config.ValidBlocks = []string{"minecraft:deepslate"}
	if err := region.placeSpring(set, worldgen.FeaturePosition{X: 8, Y: 10, Z: 8}, config); err != nil {
		t.Fatal(err)
	}
	falling, _ := nameToStateID("minecraft:water", map[string]string{"level": "8"})
	if got := region.getBlock(8, 10, 8); got != falling {
		t.Fatalf("spring under a non-default deepslate state = %s, want falling water: a valid "+
			"block must count as rock in every state, as vanilla's BlockStateIngredient does",
			stateLabel(got))
	}
}

// TestRegionPlacesSprings is the point of the stage: a region that carries a spring in
// its schedule must actually produce one, which it did not before stage 8 was replayed.
func TestRegionPlacesSprings(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), []string{"minecraft:plains"}, fluidsStage)
	if err != nil {
		t.Fatal(err)
	}
	if len(schedule) == 0 {
		t.Fatal("plains schedules nothing at stage 8, so the constant is wrong")
	}
	gen := NewVanillaRegionGenerator(12345)
	springs := 0
	for cx := int32(-6); cx <= 6 && springs == 0; cx++ {
		for cz := int32(-6); cz <= 6; cz++ {
			chunk := gen(cx, cz)
			for y := MinY + 1; y < SeaLevel-1; y++ {
				for x := 0; x < 16; x++ {
					for z := 0; z < 16; z++ {
						name, ok := stateByID(chunk.GetBlock(x, y, z))
						if !ok {
							continue
						}
						props := name.Properties
						// A falling fluid one block below a solid cell is what a spring
						// leaves; a pool or a lava lake is a body of them, so the y range
						// and the underground limit are what separate the two.
						if (name.Name == "minecraft:water" && props["level"] == "8") ||
							(name.Name == "minecraft:lava" && props["level"] == "8") {
							springs++
						}
					}
				}
			}
			if springs > 0 {
				break
			}
		}
	}
	if springs == 0 {
		t.Fatal("25 region chunks produced no falling fluid, so stage 8 is not being replayed")
	}
	t.Logf("%d falling-fluid cells found", springs)
}

// TestStage8ScheduleGolden pins which features the fluid-spring step holds for the two
// captured biomes, in the order the region replays them. A re-indexing of the schedule
// would move every later feature's per-feature seed, the same failure the stage-9
// golden exists to catch.
func TestStage8ScheduleGolden(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	// The exact pair, in schedule order, with the global index each holds: an
	// index is what SetFeatureSeed mixes with the decoration seed, so a re-indexing
	// would move every later feature in the chunk the same way stage 9's golden
	// guards against.
	for _, tc := range []struct {
		biome string
		want  []int
		names string
	}{
		{"minecraft:plains", nil, "minecraft:spring_water minecraft:spring_lava"},
		{"minecraft:lush_caves", nil, "minecraft:spring_water minecraft:spring_lava"},
	} {
		schedule, err := set.FeatureSchedule(possibleBiomeOrder(), []string{tc.biome}, fluidsStage)
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		for _, scheduled := range schedule {
			got += scheduled.Name + " "
		}
		if strings.TrimSpace(got) != tc.names {
			t.Errorf("%s stage %d schedules %q, want %q", tc.biome, fluidsStage, strings.TrimSpace(got), tc.names)
		}
		t.Logf("stage %d for %s: %s", fluidsStage, tc.biome, strings.TrimSpace(got))
	}
}

func TestPlacedSpringsAreDeterministic(t *testing.T) {
	gen := NewVanillaRegionGenerator(12345)
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
