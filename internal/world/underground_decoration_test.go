package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func TestStage7ScheduleCoversAllOverworldFeatures(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), possibleBiomeOrder(), undergroundDecorationStage)
	if err != nil {
		t.Fatal(err)
	}
	if len(schedule) != 5 {
		t.Fatalf("stage 7 overworld schedule has %d entries, want 5: %+v", len(schedule), schedule)
	}
	for _, scheduled := range schedule {
		placed, ok := set.Placed[scheduled.Name]
		if !ok {
			t.Fatalf("missing placed feature %s", scheduled.Name)
		}
		configured, ok := set.Configured[placed.Feature]
		if !ok {
			t.Fatalf("missing configured feature %s", placed.Feature)
		}
		switch configured.Type {
		case "minecraft:ore":
			if _, err := set.Ore(placed.Feature); err != nil {
				t.Fatalf("decode ore %s: %v", placed.Feature, err)
			}
		case "minecraft:multiface_growth":
			if _, err := set.MultifaceGrowth(placed.Feature); err != nil {
				t.Fatalf("decode multiface_growth %s: %v", placed.Feature, err)
			}
		case "minecraft:sculk_patch":
			if _, err := set.SculkPatch(placed.Feature); err != nil {
				t.Fatalf("decode sculk_patch %s: %v", placed.Feature, err)
			}
		case "minecraft:dripstone_cluster":
			if _, err := set.DripstoneCluster(placed.Feature); err != nil {
				t.Fatalf("decode dripstone_cluster %s: %v", placed.Feature, err)
			}
		case "minecraft:simple_random_selector":
			if _, err := set.SimpleRandomSelector(placed.Feature); err != nil {
				t.Fatalf("decode simple_random_selector %s: %v", placed.Feature, err)
			}
		default:
			t.Fatalf("unexpected stage 7 configured feature type %s (%s)", configured.Type, scheduled.Name)
		}
	}
}

func TestMultifaceGrowthPlacesGlowLichenAndSpreads(t *testing.T) {
	region, set, _ := springRegion(t, StateStone, nil)
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			region.setBlock(8+dx, 11, 8+dz, StateStone)
		}
	}
	config, err := set.MultifaceGrowth("minecraft:glow_lichen")
	if err != nil {
		t.Fatal(err)
	}
	random := worldgen.NewXoroshiro(42)
	if !region.placeMultifaceGrowth(set, random, worldgen.FeaturePosition{X: 8, Y: 10, Z: 8}, config) {
		t.Fatal("placeMultifaceGrowth returned false under a stone ceiling")
	}
	got := region.getBlock(8, 10, 8)
	if !isBlockNamed(got, "minecraft:glow_lichen") || !multifaceHasFace(got, "up") {
		t.Fatalf("origin (8,10,8) = %s, want glow_lichen with up=true", stateLabel(got))
	}
}

func TestPlaceVinesAttachesToFirstHorizontalOrUpNeighbour(t *testing.T) {
	region, _, _ := springRegion(t, StateStone, [][3]int{{8, 9, 8}, {8, 10, 7}})
	if !region.placeVines(worldgen.FeaturePosition{X: 8, Y: 10, Z: 8}) {
		t.Fatal("placeVines returned false next to a north stone neighbour")
	}
	got := region.getBlock(8, 10, 8)
	if !isBlockNamed(got, "minecraft:vine") || !multifaceHasFace(got, "north") {
		t.Fatalf("vine state = %s, want vine[north=true] (down stone neighbour must be skipped)", stateLabel(got))
	}
}

func TestSculkPatchSpreadsSculkAndVeinsInDeepDark(t *testing.T) {
	deepslate, ok := nameToStateID("minecraft:deepslate", map[string]string{"axis": "y"})
	if !ok {
		t.Fatal("missing deepslate")
	}
	region, set, _ := springRegion(t, deepslate, nil)
	for y := 4; y <= 14; y++ {
		for z := 2; z <= 14; z++ {
			for x := 2; x <= 14; x++ {
				if y <= 8 {
					region.setBlock(x, y, z, deepslate)
				}
			}
		}
	}
	config, err := set.SculkPatch("minecraft:sculk_patch_deep_dark")
	if err != nil {
		t.Fatal(err)
	}
	random := worldgen.NewXoroshiro(12345)
	if !region.placeSculkPatch(set, random, worldgen.FeaturePosition{X: 8, Y: 9, Z: 8}, config) {
		t.Fatal("placeSculkPatch returned false above a deepslate floor")
	}
	sculkCells, veinCells := 0, 0
	for y := 4; y <= 12; y++ {
		for z := 2; z <= 14; z++ {
			for x := 2; x <= 14; x++ {
				s := region.getBlock(x, y, z)
				if isBlockNamed(s, "minecraft:sculk") {
					sculkCells++
				} else if isBlockNamed(s, "minecraft:sculk_vein") {
					veinCells++
				}
			}
		}
	}
	if sculkCells == 0 || veinCells == 0 {
		t.Fatalf("sculk patch placed %d sculk and %d sculk_vein cells, want both > 0", sculkCells, veinCells)
	}
}

func TestDripstoneClusterAndPointedDripstonePlaceColumns(t *testing.T) {
	region, set, _ := springRegion(t, StateStone, nil)
	for z := 2; z <= 14; z++ {
		for x := 2; x <= 14; x++ {
			for y := 4; y <= 7; y++ {
				region.setBlock(x, y, z, StateStone)
			}
			for y := 13; y <= 16; y++ {
				region.setBlock(x, y, z, StateStone)
			}
		}
	}
	clusterCfg, err := set.DripstoneCluster("minecraft:dripstone_cluster")
	if err != nil {
		t.Fatal(err)
	}
	random := worldgen.NewXoroshiro(77)
	if !region.placeDripstoneCluster(set, random, worldgen.FeaturePosition{X: 8, Y: 10, Z: 8}, clusterCfg) {
		t.Fatal("placeDripstoneCluster returned false inside a stone cavern")
	}
	dripstoneBlocks, pointedBlocks := 0, 0
	for y := 4; y <= 16; y++ {
		for z := 2; z <= 14; z++ {
			for x := 2; x <= 14; x++ {
				s := region.getBlock(x, y, z)
				if isBlockNamed(s, "minecraft:dripstone_block") {
					dripstoneBlocks++
				} else if isBlockNamed(s, "minecraft:pointed_dripstone") {
					pointedBlocks++
				}
			}
		}
	}
	if dripstoneBlocks == 0 || pointedBlocks == 0 {
		t.Fatalf("dripstone cluster placed %d dripstone_block and %d pointed_dripstone cells, want both > 0",
			dripstoneBlocks, pointedBlocks)
	}
}
