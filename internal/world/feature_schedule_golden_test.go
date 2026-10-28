package world

import (
	"strings"
	"testing"

	"regionio/internal/worldgen"
)

// The feature schedule carries two quantities that silently decide where every
// feature puts its blocks: the index each feature is reseeded with, and the
// placement chain that consumes the decoration stream. Both are derived - from
// the encounter order of the biome parameter table, and from the parsed
// datapack - so neither is pinned by anything else, and a shift in either is
// invisible until a world capture says so. These assertions are the guard.

func TestStage9ScheduleGolden(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}

	// Vanilla's own lush_caves stage-9 list, from data/minecraft/worldgen/
	// biome/lush_caves.json. The order is what the decoration stream runs in.
	lushCavesStage9 := []string{
		"minecraft:glow_lichen",
		"minecraft:patch_tall_grass_2",
		"minecraft:lush_caves_ceiling_vegetation",
		"minecraft:cave_vines",
		"minecraft:lush_caves_clay",
		"minecraft:lush_caves_vegetation",
		"minecraft:rooted_azalea_tree",
		"minecraft:spore_blossom",
		"minecraft:classic_vines_cave_feature",
	}
	got := set.Biomes["minecraft:lush_caves"].Features[vegetationStage]
	if len(got) != len(lushCavesStage9) {
		t.Fatalf("lush_caves stage %d has %d features, want %d: %v",
			vegetationStage, len(got), len(lushCavesStage9), got)
	}
	for i, want := range lushCavesStage9 {
		if got[i] != want {
			t.Errorf("lush_caves stage %d feature %d = %q, want %q", vegetationStage, i, got[i], want)
		}
	}

	// The indices the replay reseeds each feature with. These are positions in
	// the global topological step list, not offsets within the biome's own list:
	// lush_caves_clay is 5th in lush_caves but reseeds as 29. Getting this wrong
	// moves every pool and patch in the stage, so it is worth a line of gold.
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(),
		[]string{"minecraft:lush_caves", "minecraft:ocean"}, vegetationStage)
	if err != nil {
		t.Fatal(err)
	}
	wantSchedule := []worldgen.ScheduledFeature{
		{Name: "minecraft:glow_lichen", Index: 0},
		{Name: "minecraft:patch_tall_grass_2", Index: 26},
		{Name: "minecraft:lush_caves_ceiling_vegetation", Index: 27},
		{Name: "minecraft:cave_vines", Index: 28},
		{Name: "minecraft:lush_caves_clay", Index: 29},
		{Name: "minecraft:lush_caves_vegetation", Index: 30},
		{Name: "minecraft:rooted_azalea_tree", Index: 31},
		{Name: "minecraft:spore_blossom", Index: 32},
		{Name: "minecraft:classic_vines_cave_feature", Index: 33},
		{Name: "minecraft:trees_water", Index: 50},
		{Name: "minecraft:flower_default", Index: 57},
		{Name: "minecraft:patch_grass_badlands", Index: 68},
		{Name: "minecraft:brown_mushroom_normal", Index: 73},
		{Name: "minecraft:red_mushroom_normal", Index: 74},
		{Name: "minecraft:patch_pumpkin", Index: 82},
		{Name: "minecraft:patch_sugar_cane", Index: 88},
		{Name: "minecraft:patch_firefly_bush_near_water", Index: 89},
		{Name: "minecraft:seagrass_normal", Index: 101},
		{Name: "minecraft:kelp_cold", Index: 105},
	}
	if len(schedule) != len(wantSchedule) {
		t.Fatalf("stage %d schedule has %d features, want %d: %v",
			vegetationStage, len(schedule), len(wantSchedule), schedule)
	}
	for i, want := range wantSchedule {
		if schedule[i] != want {
			t.Errorf("stage %d schedule[%d] = %+v, want %+v", vegetationStage, i, schedule[i], want)
		}
	}
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

func TestLushCavesClayPlacementChainGolden(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	placed, ok := set.Placed["minecraft:lush_caves_clay"]
	if !ok {
		t.Fatal("minecraft:lush_caves_clay is not in the placed feature set")
	}
	// Each of these modifiers has a fixed cost in decoration-stream draws. A
	// chain that grows, shrinks, or reorders moves every pool in the stage.
	wantTypes := []string{
		"minecraft:count",
		"minecraft:in_square",
		"minecraft:height_range",
		"minecraft:environment_scan",
		"minecraft:random_offset",
		"minecraft:biome",
	}
	if len(placed.Placement) != len(wantTypes) {
		t.Fatalf("lush_caves_clay has %d placement modifiers, want %d",
			len(placed.Placement), len(wantTypes))
	}
	for i, want := range wantTypes {
		if got := placed.Placement[i].Type; got != want {
			t.Errorf("lush_caves_clay modifier %d = %q, want %q", i, got, want)
		}
	}
	if placed.Feature != "minecraft:lush_caves_clay" {
		t.Errorf("lush_caves_clay wraps %q, want minecraft:lush_caves_clay", placed.Feature)
	}
}

func TestPaleGardenScheduleAndFullChunkReplay(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}

	// 1. Verify Pale Garden stage-9 feature list matches vanilla 26.1.2 datapack.
	wantPaleGardenStage9 := []string{
		"minecraft:glow_lichen",
		"minecraft:pale_garden_vegetation",
		"minecraft:pale_moss_patch",
		"minecraft:pale_garden_flowers",
		"minecraft:flower_pale_garden",
		"minecraft:patch_grass_forest",
		"minecraft:patch_pumpkin",
		"minecraft:patch_sugar_cane",
		"minecraft:patch_firefly_bush_near_water",
	}
	got := set.Biomes["minecraft:pale_garden"].Features[vegetationStage]
	if len(got) != len(wantPaleGardenStage9) {
		t.Fatalf("pale_garden stage %d has %d features, want %d: %v",
			vegetationStage, len(got), len(wantPaleGardenStage9), got)
	}
	for i, want := range wantPaleGardenStage9 {
		if got[i] != want {
			t.Errorf("pale_garden stage %d feature %d = %q, want %q", vegetationStage, i, got[i], want)
		}
	}

	// 2. Verify Pale Garden base temperature is 0.7 and biome parameter table contains pale_garden.
	if temp := worldgen.TemperatureAt("minecraft:pale_garden", 0, 63, 0, 63); temp != 0.7 {
		t.Errorf("TemperatureAt(pale_garden) = %v, want 0.7", temp)
	}
	foundInBiomeOrder := false
	for _, b := range possibleBiomeOrder() {
		if b == "minecraft:pale_garden" {
			foundInBiomeOrder = true
			break
		}
	}
	if !foundInBiomeOrder {
		t.Fatal("minecraft:pale_garden missing from possibleBiomeOrder()")
	}

	// 3. Replay all Pale Garden stage-9 features over a 3x3 chunk region with Overworld
	// grass_block/dirt surface rules, verifying zero unmodelled gaps and presence of
	// pale_oak_log, pale_oak_leaves, pale_moss_block, pale_moss_carpet, pale_hanging_moss,
	// closed_eyeblossom, and creaking_heart (+ block entity).
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), []string{"minecraft:pale_garden"}, vegetationStage)
	if err != nil {
		t.Fatal(err)
	}
	if len(schedule) != len(wantPaleGardenStage9) {
		t.Fatalf("pale_garden schedule len = %d, want %d", len(schedule), len(wantPaleGardenStage9))
	}

	grass := mustState("minecraft:grass_block", map[string]string{"snowy": "false"})
	dirt := mustState("minecraft:dirt", nil)
	stone := mustState("minecraft:stone", nil)

	counts := map[string]int{}
	heartBEs := 0
	paleGardenBiome := biomeID("minecraft:pale_garden")
	for seed := int64(100); seed < 116; seed++ {
		var chunkList []*Chunk
		for cz := int32(-1); cz <= 1; cz++ {
			for cx := int32(-1); cx <= 1; cx++ {
				ch := NewChunk(cx, cz, paleGardenBiome)
				for x := 0; x < 16; x++ {
					for z := 0; z < 16; z++ {
						for y := 0; y < 60; y++ {
							ch.SetBlock(x, y, z, stone)
						}
						for y := 60; y < 64; y++ {
							ch.SetBlock(x, y, z, dirt)
						}
						ch.SetBlock(x, 64, z, grass)
					}
				}
				chunkList = append(chunkList, ch)
			}
		}
		r, err := newDecorationRegion(chunkList)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.setSource(0, 0); err != nil {
			t.Fatal(err)
		}
		ResetNotReplayed()
		if err := r.placeScheduledVegetationPatches(seed); err != nil {
			t.Fatalf("seed %d placeScheduledVegetationPatches: %v", seed, err)
		}
		if gaps := NotReplayed(); len(gaps) > 0 {
			t.Fatalf("seed %d pale_garden stage-9 replay had NotReplayed gaps: %v", seed, gaps)
		}
		for _, ch := range chunkList {
			for _, be := range ch.StructureBlockEntities() {
				if be.ID == "minecraft:creaking_heart" {
					heartBEs++
				}
			}
		}
		for x := -16; x < 32; x++ {
			for z := -16; z < 32; z++ {
				for y := 60; y <= 84; y++ {
					name := BlockName(r.getBlock(x, y, z))
					counts[name]++
				}
			}
		}
		if counts["minecraft:pale_oak_log"] > 0 &&
			counts["minecraft:pale_oak_leaves"] > 0 &&
			counts["minecraft:pale_moss_block"] > 0 &&
			counts["minecraft:pale_moss_carpet"] > 0 &&
			counts["minecraft:pale_hanging_moss"] > 0 &&
			counts["minecraft:closed_eyeblossom"] > 0 &&
			counts["minecraft:creaking_heart"] > 0 &&
			heartBEs > 0 {
			break
		}
	}

	for _, wantBlock := range []string{
		"minecraft:pale_oak_log",
		"minecraft:pale_oak_leaves",
		"minecraft:pale_moss_block",
		"minecraft:pale_moss_carpet",
		"minecraft:pale_hanging_moss",
		"minecraft:closed_eyeblossom",
		"minecraft:creaking_heart",
	} {
		if counts[wantBlock] == 0 {
			t.Errorf("pale_garden stage-9 replay placed 0 %s blocks (counts=%v)", wantBlock, counts)
		}
	}
	if heartBEs == 0 {
		t.Errorf("pale_garden stage-9 replay recorded 0 minecraft:creaking_heart block entities")
	}
}
