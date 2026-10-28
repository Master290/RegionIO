package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func countBlocksInBox(region *decorationRegion, box msBox) map[string]int {
	counts := make(map[string]int)
	for x := box.minX; x <= box.maxX; x++ {
		for z := box.minZ; z <= box.maxZ; z++ {
			for y := max(box.minY, MinY+1); y <= min(box.maxY, MinY+WorldHeight-1); y++ {
				id := region.getBlock(x, y, z)
				if id == StateAir || id == StateStone {
					continue
				}
				if st, ok := stateByID(id); ok {
					counts[st.Name]++
				}
			}
		}
	}
	return counts
}

func TestBuildAndPlaceIglooWithAndWithoutBasement(t *testing.T) {
	var withBasement, withoutBasement *IglooStart
	for s := int64(1); s < 200 && (withBasement == nil || withoutBasement == nil); s++ {
		st := BuildIglooStart(s, 0, 0)
		if len(st.Pieces) > 1 && withBasement == nil {
			withBasement = st
		} else if len(st.Pieces) == 1 && withoutBasement == nil {
			withoutBasement = st
		}
	}
	if withBasement == nil || withoutBasement == nil {
		t.Fatalf("failed to find both basement (%v) and non-basement (%v) igloos", withBasement, withoutBasement)
	}

	for _, tc := range []struct {
		name  string
		start *IglooStart
		base  bool
	}{
		{"without_basement", withoutBasement, false},
		{"with_basement", withBasement, true},
	} {
		region := buildFlatTestDecorationRegion(t, 0, 0, 2, 64, false)
		starts := []*IglooStart{tc.start}
		for cx := int32(-1); cx <= 1; cx++ {
			for cz := int32(-1); cz <= 1; cz++ {
				PlaceIgloosForChunk(region, starts, 12345, cx, cz)
			}
		}
		scanBox := msBox{tc.start.Box.minX, 20, tc.start.Box.minZ, tc.start.Box.maxX, 80, tc.start.Box.maxZ}
		counts := countBlocksInBox(region, scanBox)
		if counts["minecraft:structure_block"] > 0 {
			t.Fatalf("igloo (%s) left %d structure_blocks", tc.name, counts["minecraft:structure_block"])
		}
		if counts["minecraft:snow_block"] == 0 {
			t.Fatalf("igloo (%s) placed 0 snow_blocks", tc.name)
		}
		if tc.base {
			if counts["minecraft:ladder"] == 0 {
				t.Fatalf("igloo with basement placed 0 ladders")
			}
			if counts["minecraft:chest"] == 0 {
				t.Fatalf("igloo with basement placed 0 chests")
			}
			if counts["minecraft:brewing_stand"] == 0 {
				t.Fatalf("igloo with basement placed 0 brewing_stands")
			}
		} else {
			if counts["minecraft:oak_trapdoor"] > 0 {
				t.Fatalf("igloo without basement left oak_trapdoor instead of replacing with snow_block")
			}
		}
		t.Logf("igloo (%s, pieces=%d): snow_block=%d ladder=%d chest=%d",
			tc.name, len(tc.start.Pieces), counts["minecraft:snow_block"], counts["minecraft:ladder"], counts["minecraft:chest"])
	}
}

func TestBuildAndPlaceSwampHut(t *testing.T) {
	const seed int64 = 12345
	start := BuildSwampHutStart(seed, 0, 0)
	region := buildFlatTestDecorationRegion(t, 0, 0, 2, 62, false)
	starts := []*SwampHutStart{start}
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			PlaceSwampHutsForChunk(region, starts, seed, cx, cz)
		}
	}
	scanBox := msBox{start.Piece.box.minX, 55, start.Piece.box.minZ, start.Piece.box.maxX, 75, start.Piece.box.maxZ}
	counts := countBlocksInBox(region, scanBox)
	for _, want := range []string{
		"minecraft:spruce_planks",
		"minecraft:spruce_stairs",
		"minecraft:oak_log",
		"minecraft:oak_fence",
		"minecraft:potted_red_mushroom",
		"minecraft:crafting_table",
		"minecraft:cauldron",
	} {
		if counts[want] == 0 {
			t.Errorf("swamp hut placed 0 %s (counts=%v)", want, counts)
		}
	}
}

func TestBuildAndPlaceDesertPyramid(t *testing.T) {
	const seed int64 = 12345
	start := BuildDesertPyramidStart(seed, 0, 0)
	region := buildFlatTestDecorationRegion(t, 0, 0, 2, 64, false)
	starts := []*DesertPyramidStart{start}
	for cx := int32(-1); cx <= 2; cx++ {
		for cz := int32(-1); cz <= 2; cz++ {
			PlaceDesertPyramidsForChunk(region, starts, seed, cx, cz)
		}
	}
	scanBox := msBox{start.Piece.box.minX, 45, start.Piece.box.minZ, start.Piece.box.maxX, 85, start.Piece.box.maxZ}
	counts := countBlocksInBox(region, scanBox)
	for _, want := range []string{
		"minecraft:sandstone",
		"minecraft:cut_sandstone",
		"minecraft:chiseled_sandstone",
		"minecraft:sandstone_stairs",
		"minecraft:orange_terracotta",
		"minecraft:blue_terracotta",
		"minecraft:tnt",
		"minecraft:stone_pressure_plate",
		"minecraft:chest",
		"minecraft:suspicious_sand",
	} {
		if counts[want] == 0 {
			t.Errorf("desert pyramid placed 0 %s", want)
		}
	}
	if counts["minecraft:tnt"] != 9 {
		t.Errorf("desert pyramid placed %d tnt, want 9", counts["minecraft:tnt"])
	}
	if counts["minecraft:chest"] != 4 {
		t.Errorf("desert pyramid placed %d chests, want 4", counts["minecraft:chest"])
	}
	if counts["minecraft:suspicious_sand"] < 5 || counts["minecraft:suspicious_sand"] > 8 {
		t.Errorf("desert pyramid placed %d suspicious_sand, want 5..8", counts["minecraft:suspicious_sand"])
	}
	t.Logf("desert pyramid: sandstone=%d chests=%d tnt=%d suspicious_sand=%d",
		counts["minecraft:sandstone"], counts["minecraft:chest"], counts["minecraft:tnt"], counts["minecraft:suspicious_sand"])
}

func TestBuildAndPlaceJungleTemple(t *testing.T) {
	const seed int64 = 12345
	start := BuildJungleTempleStart(seed, 0, 0)
	region := buildFlatTestDecorationRegion(t, 0, 0, 2, 64, false)
	starts := []*JungleTempleStart{start}
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			PlaceJungleTemplesForChunk(region, starts, seed, cx, cz)
		}
	}
	scanBox := msBox{start.Piece.box.minX, 55, start.Piece.box.minZ, start.Piece.box.maxX, 80, start.Piece.box.maxZ}
	counts := countBlocksInBox(region, scanBox)
	for _, want := range []string{
		"minecraft:cobblestone",
		"minecraft:mossy_cobblestone",
		"minecraft:cobblestone_stairs",
		"minecraft:chiseled_stone_bricks",
		"minecraft:dispenser",
		"minecraft:chest",
		"minecraft:lever",
		"minecraft:sticky_piston",
		"minecraft:repeater",
		"minecraft:redstone_wire",
		"minecraft:tripwire_hook",
		"minecraft:tripwire",
		"minecraft:vine",
	} {
		if counts[want] == 0 {
			t.Errorf("jungle temple placed 0 %s", want)
		}
	}
	if counts["minecraft:dispenser"] != 2 {
		t.Errorf("jungle temple placed %d dispensers, want 2", counts["minecraft:dispenser"])
	}
	if counts["minecraft:chest"] != 2 {
		t.Errorf("jungle temple placed %d chests, want 2", counts["minecraft:chest"])
	}
	if counts["minecraft:lever"] != 3 {
		t.Errorf("jungle temple placed %d levers, want 3", counts["minecraft:lever"])
	}
	if counts["minecraft:sticky_piston"] != 3 {
		t.Errorf("jungle temple placed %d sticky_pistons, want 3", counts["minecraft:sticky_piston"])
	}
}

func TestBuildAndPlaceOceanMonument(t *testing.T) {
	const seed int64 = 12345
	start := BuildOceanMonumentStart(seed, 0, 0)
	region := buildFlatTestDecorationRegion(t, 0, 0, 3, 35, true)
	starts := []*OceanMonumentStart{start}
	for cx := int32(-3); cx <= 3; cx++ {
		for cz := int32(-3); cz <= 3; cz++ {
			PlaceOceanMonumentsForChunk(region, starts, seed, cx, cz)
		}
	}
	counts := countBlocksInBox(region, start.Piece.box)
	for _, want := range []string{
		"minecraft:prismarine",
		"minecraft:prismarine_bricks",
		"minecraft:dark_prismarine",
		"minecraft:sea_lantern",
		"minecraft:wet_sponge",
		"minecraft:gold_block",
	} {
		if counts[want] == 0 {
			t.Errorf("ocean monument placed 0 %s", want)
		}
	}
	if counts["minecraft:gold_block"] != 8 {
		t.Errorf("ocean monument placed %d gold_blocks, want 8", counts["minecraft:gold_block"])
	}
	t.Logf("ocean monument: prismarine=%d bricks=%d dark=%d lanterns=%d gold=%d",
		counts["minecraft:prismarine"], counts["minecraft:prismarine_bricks"],
		counts["minecraft:dark_prismarine"], counts["minecraft:sea_lantern"], counts["minecraft:gold_block"])
}

func TestBuildAndPlaceWoodlandMansion(t *testing.T) {
	const seed int64 = 12345
	start := BuildWoodlandMansionStart(seed, 0, 0, [3]int{7, 65, 7})
	if len(start.Pieces) < 20 {
		t.Fatalf("woodland mansion generated %d pieces, want >= 20", len(start.Pieces))
	}
	region := buildFlatTestDecorationRegion(t, 0, 0, 3, 62, false)
	starts := []*WoodlandMansionStart{start}
	for cx := int32(-3); cx <= 3; cx++ {
		for cz := int32(-3); cz <= 3; cz++ {
			PlaceWoodlandMansionsForChunk(region, starts, seed, cx, cz)
		}
	}
	scanBox := msBox{start.Box.minX, 60, start.Box.minZ, start.Box.maxX, start.Box.maxY, start.Box.maxZ}
	counts := countBlocksInBox(region, scanBox)
	if counts["minecraft:structure_block"] > 0 {
		t.Fatalf("woodland mansion left %d structure_blocks", counts["minecraft:structure_block"])
	}
	if counts["minecraft:dark_oak_planks"] == 0 {
		t.Errorf("woodland mansion placed 0 dark_oak_planks")
	}
	if counts["minecraft:cobblestone"] == 0 {
		t.Errorf("woodland mansion placed 0 cobblestone")
	}
	t.Logf("woodland mansion (pieces=%d): dark_oak_planks=%d birch_planks=%d cobblestone=%d",
		len(start.Pieces), counts["minecraft:dark_oak_planks"], counts["minecraft:birch_planks"], counts["minecraft:cobblestone"])
}

func TestRemainingJigsawStructures(t *testing.T) {
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}
	flatSurface := func(x, z int, oceanFloor bool) int {
		return 64
	}

	cases := []struct {
		name      string
		step      int
		wantBlock string
	}{
		{"minecraft:pillager_outpost", 4, "minecraft:dark_oak_planks"},
		{"minecraft:ancient_city", 7, "minecraft:deepslate_bricks"},
		{"minecraft:trail_ruins", 3, "minecraft:mud_bricks"},
		{"minecraft:trial_chambers", 3, "minecraft:tuff_bricks"},
	}

	for _, tc := range cases {
		def := sets.Structures[tc.name]
		if def == nil {
			t.Fatalf("missing structure definition %s", tc.name)
		}
		start, err := BuildJigsawStart(nil, nil, def, 12345, 0, 0, flatSurface)
		if err != nil {
			t.Fatalf("BuildJigsawStart(%s): %v", tc.name, err)
		}
		if start == nil || len(start.Pieces) < 5 {
			t.Fatalf("%s generated %v pieces, want >= 5", tc.name, start)
		}

		region := buildFlatTestDecorationRegion(t, 0, 0, 3, 64, false)
		for cx := int32(-3); cx <= 3; cx++ {
			for cz := int32(-3); cz <= 3; cz++ {
				PlaceJigsawStartsForChunk(region, []*JigsawStart{start}, tc.name, tc.step, 12345, cx, cz)
			}
		}

		counts := countBlocksInBox(region, start.Box)
		if counts["minecraft:jigsaw"] > 0 {
			t.Errorf("%s left %d unreplaced jigsaw blocks", tc.name, counts["minecraft:jigsaw"])
		}
		if counts["minecraft:structure_block"] > 0 {
			t.Errorf("%s left %d unignored structure_blocks", tc.name, counts["minecraft:structure_block"])
		}
		if counts[tc.wantBlock] == 0 {
			t.Errorf("%s placed 0 %s blocks", tc.name, tc.wantBlock)
		}
		if tc.name == "minecraft:trail_ruins" && counts["minecraft:suspicious_gravel"] == 0 {
			t.Errorf("trail_ruins placed 0 suspicious_gravel via CappedProcessor")
		}
		t.Logf("%s: pieces=%d %s=%d", tc.name, len(start.Pieces), tc.wantBlock, counts[tc.wantBlock])
	}
}

func TestVillageAndOutpostJigsawResidualAndFreezeTopLayer(t *testing.T) {
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}
	reg, err := worldgen.LoadJigsawDataRegistry()
	if err != nil {
		t.Fatalf("LoadJigsawDataRegistry: %v", err)
	}
	fs := mustFeatureSet(t)
	flatSurface := func(x, z int, oceanFloor bool) int {
		return 64
	}

	villages := []struct {
		name      string
		wantBlock string
	}{
		{"minecraft:village_plains", "minecraft:oak_planks"},
		{"minecraft:village_desert", "minecraft:sandstone"},
		{"minecraft:village_savanna", "minecraft:acacia_planks"},
		{"minecraft:village_snowy", "minecraft:snow_block"},
		{"minecraft:village_taiga", "minecraft:spruce_planks"},
	}
	for _, v := range villages {
		def := sets.Structures[v.name]
		if def == nil {
			t.Fatalf("missing village definition %s", v.name)
		}
		start, err := BuildJigsawStart(nil, nil, def, 424242, 0, 0, flatSurface)
		if err != nil {
			t.Fatalf("BuildJigsawStart(%s): %v", v.name, err)
		}
		if start == nil || len(start.Pieces) < 5 {
			t.Fatalf("%s generated %v pieces, want >= 5", v.name, start)
		}
		region := buildFlatTestDecorationRegion(t, 0, 0, 4, 64, false)
		for cx := int32(-4); cx <= 4; cx++ {
			for cz := int32(-4); cz <= 4; cz++ {
				PlaceJigsawStartsForChunk(region, []*JigsawStart{start}, v.name, 4, 424242, cx, cz)
			}
		}
		counts := countBlocksInBox(region, start.Box)
		if counts["minecraft:jigsaw"] > 0 || counts["minecraft:structure_block"] > 0 {
			t.Errorf("%s left jigsaw=%d structure_block=%d", v.name, counts["minecraft:jigsaw"], counts["minecraft:structure_block"])
		}
		if counts[v.wantBlock] == 0 {
			t.Errorf("%s placed 0 %s", v.name, v.wantBlock)
		}
	}

	// FeaturePoolElement must execute the PlacedFeature's placement modifiers
	// (count + random_offset + block_predicate_filter) rather than placing once at origin.
	berryRegion := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
	grassID, ok := nameToStateID("minecraft:grass_block", map[string]string{"snowy": "false"})
	if !ok {
		t.Fatal("missing grass_block state")
	}
	for x := -16; x < 32; x++ {
		for z := -16; z < 32; z++ {
			berryRegion.setBlockGlobal(x, 63, z, grassID)
		}
	}
	chunkBox := msBox{minX: 0, minY: MinY, minZ: 0, maxX: 15, maxY: MinY + WorldHeight - 1, maxZ: 15}
	placeJigsawElementInChunk(
		berryRegion,
		reg,
		fs,
		worldgen.PoolElementDef{
			ElementType: "minecraft:feature_pool_element",
			Feature:     "minecraft:patch_berry_bush",
			Projection:  "rigid",
		},
		[3]int{8, 64, 8},
		0,
		chunkBox,
		worldgen.NewLegacy(13579),
		"apply_waterlogging",
	)
	berryCounts := countBlocksInBox(berryRegion, msBox{minX: 0, minY: 60, minZ: 0, maxX: 15, maxY: 70, maxZ: 15})
	if berryCounts["minecraft:sweet_berry_bush"] < 2 {
		t.Errorf("feature_pool_element patch_berry_bush placed %d sweet_berry_bush, want >= 2 via PlacedFeature modifiers",
			berryCounts["minecraft:sweet_berry_bush"])
	}

	// freeze_top_layer must use height-adjusted temperature above seaLevel+17:
	// windswept_hills (baseTemp=0.2) is warm at y=64 (no ice/snow), but cools
	// below 0.15 at y=160 (water freezes to ice, snow[layers=1] is placed, and
	// grass_block below becomes snowy=true).
	hillsID := biomeIDByName("minecraft:windswept_hills")
	lowRegion := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
	highRegion := buildFlatTestDecorationRegion(t, 0, 0, 1, 159, false)
	for _, r := range []*decorationRegion{lowRegion, highRegion} {
		for _, ch := range r.chunks {
			for qx := 0; qx < 4; qx++ {
				for qy := 0; qy < SectionCount*4; qy++ {
					for qz := 0; qz < 4; qz++ {
						ch.SetBiome(qx*4, MinY+qy*4, qz*4, hillsID)
					}
				}
			}
		}
	}
	lowRegion.setBlock(2, 63, 2, grassID)
	lowRegion.setBlock(3, 63, 3, StateWater)
	if err := lowRegion.placeScheduledFreezeTopLayer(12345); err != nil {
		t.Fatalf("placeScheduledFreezeTopLayer(low): %v", err)
	}
	if got := lowRegion.getBlock(3, 63, 3); got != StateWater {
		t.Errorf("windswept_hills at y=64 water block = %d, want StateWater (%d)", got, StateWater)
	}
	if got := lowRegion.getBlock(2, 64, 2); got != StateAir {
		t.Errorf("windswept_hills at y=64 air above grass = %d, want StateAir", got)
	}

	highRegion.setBlock(2, 159, 2, grassID)
	highRegion.setBlock(3, 159, 3, StateWater)
	if err := highRegion.placeScheduledFreezeTopLayer(12345); err != nil {
		t.Fatalf("placeScheduledFreezeTopLayer(high): %v", err)
	}
	iceID, _ := nameToStateID("minecraft:ice", nil)
	snowID, _ := nameToStateID("minecraft:snow", map[string]string{"layers": "1"})
	snowyGrassID, _ := nameToStateID("minecraft:grass_block", map[string]string{"snowy": "true"})
	if got := highRegion.getBlock(3, 159, 3); got != iceID {
		t.Errorf("windswept_hills at y=160 water block = %d, want ice (%d)", got, iceID)
	}
	if got := highRegion.getBlock(2, 160, 2); got != snowID {
		t.Errorf("windswept_hills at y=160 block above grass = %d, want snow[layers=1] (%d)", got, snowID)
	}
	if got := highRegion.getBlock(2, 159, 2); got != snowyGrassID {
		t.Errorf("windswept_hills at y=159 grass block = %d, want grass_block[snowy=true] (%d)", got, snowyGrassID)
	}
}
