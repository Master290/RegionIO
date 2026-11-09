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

func TestOceanMonumentWoodlandMansionPillagerOutpostSeed12345Parity(t *testing.T) {
	const seed = int64(12345)
	od, _, _, _ := vanillaGeneratorInputs(seed)
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}

	// 1. Start positions on seed=12345 across all three structure sets.
	findFirstValidStart := func(setName string, maxRegions int32) *StructureStartStub {
		set := sets.Sets[setName]
		if set == nil || set.Placement == nil || set.Placement.RandomSpread == nil {
			t.Fatalf("missing random_spread set %s", setName)
		}
		spread := set.Placement.RandomSpread
		cand00 := spread.PotentialChunk(seed, 0, 0)
		if cand00[0] < 0 || int(cand00[0]) >= spread.Spacing-spread.Separation ||
			cand00[1] < 0 || int(cand00[1]) >= spread.Spacing-spread.Separation {
			t.Fatalf("%s cell (0,0) candidate %v out of bounds [0, %d)",
				setName, cand00, spread.Spacing-spread.Separation)
		}
		for rx := -maxRegions; rx <= maxRegions; rx++ {
			for rz := -maxRegions; rz <= maxRegions; rz++ {
				cand := spread.PotentialChunk(seed, rx*int32(spread.Spacing), rz*int32(spread.Spacing))
				if !set.IsStartChunk(seed, cand[0], cand[1]) {
					continue
				}
				stub, ok := FindStructureSetStart(od, sets, setName, seed, cand[0], cand[1])
				if !ok || stub == nil {
					continue
				}
				def := sets.Structures[stub.StructureName]
				if def == nil || !isStructureBiomeAllowed(od, sets, def, stub.StubPos[0], stub.StubPos[1], stub.StubPos[2]) {
					t.Fatalf("%s start at %v stubPos %v failed biome check for %s", setName, cand, stub.StubPos, stub.StructureName)
				}
				return stub
			}
		}
		t.Fatalf("did not find any valid start for %s in [-%d..%d] regions on seed %d", setName, maxRegions, maxRegions, seed)
		return nil
	}

	monStub := findFirstValidStart("minecraft:ocean_monuments", 8)
	outStub := findFirstValidStart("minecraft:pillager_outposts", 8)
	manStub := findFirstValidStart("minecraft:woodland_mansions", 8)
	if monStub.StructureName != "minecraft:monument" ||
		outStub.StructureName != "minecraft:pillager_outpost" ||
		manStub.StructureName != "minecraft:mansion" {
		t.Fatalf("unexpected structure names: mon=%s out=%s man=%s",
			monStub.StructureName, outStub.StructureName, manStub.StructureName)
	}
	t.Logf("seed=12345 starts: monument=(%d,%d stub=%v) pillager_outpost=(%d,%d stub=%v) mansion=(%d,%d stub=%v)",
		monStub.ChunkX, monStub.ChunkZ, monStub.StubPos,
		outStub.ChunkX, outStub.ChunkZ, outStub.StubPos,
		manStub.ChunkX, manStub.ChunkZ, manStub.StubPos)

	// 2. Ocean Monument piece contents on seed=12345 (CoreRoom, Penthouse, WingRoom, SimpleTopRoom, EntryRoom).
	monStart := BuildOceanMonumentStart(seed, monStub.ChunkX, monStub.ChunkZ)
	if monStart == nil {
		t.Fatal("BuildOceanMonumentStart returned nil")
	}
	monRegion := buildFlatTestDecorationRegion(t, monStub.ChunkX, monStub.ChunkZ, 3, 35, true)
	for cx := monStub.ChunkX - 3; cx <= monStub.ChunkX+3; cx++ {
		for cz := monStub.ChunkZ - 3; cz <= monStub.ChunkZ+3; cz++ {
			PlaceOceanMonumentsForChunk(monRegion, []*OceanMonumentStart{monStart}, seed, cx, cz)
		}
	}
	monCounts := countBlocksInBox(monRegion, monStart.Piece.box)
	if monCounts["minecraft:gold_block"] != 8 {
		t.Errorf("OceanMonument CoreRoom gold_block = %d, want 8", monCounts["minecraft:gold_block"])
	}
	if monCounts["minecraft:wet_sponge"] == 0 {
		t.Errorf("OceanMonument SimpleTopRoom wet_sponge = 0, want > 0")
	}
	if monCounts["minecraft:prismarine"] == 0 || monCounts["minecraft:prismarine_bricks"] == 0 ||
		monCounts["minecraft:dark_prismarine"] == 0 || monCounts["minecraft:sea_lantern"] == 0 {
		t.Errorf("OceanMonument missing core blocks: %v", monCounts)
	}
	elderCount, guardianCount := 0, 0
	for _, ent := range collectRegionEntities(monRegion) {
		switch ent.ID {
		case "minecraft:elder_guardian":
			elderCount++
		case "minecraft:guardian":
			guardianCount++
		}
	}
	if elderCount != 3 || guardianCount == 0 {
		t.Errorf("OceanMonument entities: elder_guardian=%d (want 3), guardian=%d (want > 0)", elderCount, guardianCount)
	}

	// 3. Woodland Mansion piece contents on seed=12345 (entrance, rooms, chests, entities, foundation).
	manStart := BuildWoodlandMansionStart(seed, manStub.ChunkX, manStub.ChunkZ, manStub.StubPos)
	if manStart == nil || len(manStart.Pieces) < 100 {
		t.Fatalf("WoodlandMansion start=%v pieces=%d, want >= 100", manStart, len(manStart.Pieces))
	}
	manRegion := buildFlatTestDecorationRegion(t, manStub.ChunkX, manStub.ChunkZ, 6, manStub.StubPos[1]-3, false)
	for cx := manStub.ChunkX - 6; cx <= manStub.ChunkX+6; cx++ {
		for cz := manStub.ChunkZ - 6; cz <= manStub.ChunkZ+6; cz++ {
			PlaceWoodlandMansionsForChunk(manRegion, []*WoodlandMansionStart{manStart}, seed, cx, cz)
		}
	}
	manCounts := countBlocksInBox(manRegion, msBox{
		minX: manStart.Box.minX, minY: manStub.StubPos[1] - 3, minZ: manStart.Box.minZ,
		maxX: manStart.Box.maxX, maxY: manStart.Box.maxY, maxZ: manStart.Box.maxZ,
	})
	if manCounts["minecraft:dark_oak_planks"] == 0 || manCounts["minecraft:birch_planks"] == 0 ||
		manCounts["minecraft:cobblestone"] == 0 {
		t.Errorf("WoodlandMansion missing primary blocks: %v", manCounts)
	}
	// Verify cobblestone foundation was extended below minY down to terrain.
	foundationCobble := 0
	cobbyID, _ := nameToStateID("minecraft:cobblestone", nil)
	for x := manStart.Box.minX; x <= manStart.Box.maxX; x++ {
		for z := manStart.Box.minZ; z <= manStart.Box.maxZ; z++ {
			if manRegion.getBlock(x, manStart.Box.minY-1, z) == cobbyID {
				foundationCobble++
			}
		}
	}
	if foundationCobble == 0 {
		t.Errorf("WoodlandMansion afterPlace placed 0 cobblestone foundation blocks at y=%d", manStart.Box.minY-1)
	}
	manChests, evokers, vindicators := 0, 0, 0
	for _, be := range collectRegionBlockEntities(manRegion) {
		if be.ID == "minecraft:chest" && len(be.Items) > 0 {
			manChests++
		}
	}
	for _, ent := range collectRegionEntities(manRegion) {
		switch ent.ID {
		case "minecraft:evoker":
			evokers++
		case "minecraft:vindicator":
			vindicators++
		}
	}
	if manChests == 0 || evokers == 0 || vindicators == 0 {
		t.Errorf("WoodlandMansion lootChests=%d evokers=%d vindicators=%d, all want > 0",
			manChests, evokers, vindicators)
	}

	// 4. Pillager Outpost piece contents on seed=12345 (watchtower, chest loot, ominous banner, pillagers).
	outDef := sets.Structures["minecraft:pillager_outpost"]
	if outDef == nil {
		t.Fatal("missing minecraft:pillager_outpost definition")
	}
	outStart, err := BuildJigsawStart(nil, nil, outDef, seed, outStub.ChunkX, outStub.ChunkZ, func(x, z int, oceanFloor bool) int {
		return 64
	})
	if err != nil || outStart == nil || len(outStart.Pieces) == 0 {
		t.Fatalf("BuildJigsawStart(pillager_outpost): start=%v err=%v", outStart, err)
	}
	outRegion := buildFlatTestDecorationRegion(t, outStub.ChunkX, outStub.ChunkZ, 3, 64, false)
	for cx := outStub.ChunkX - 3; cx <= outStub.ChunkX+3; cx++ {
		for cz := outStub.ChunkZ - 3; cz <= outStub.ChunkZ+3; cz++ {
			PlaceJigsawStartsForChunk(outRegion, []*JigsawStart{outStart}, "minecraft:pillager_outpost", 4, seed, cx, cz)
		}
	}
	outCounts := countBlocksInBox(outRegion, outStart.Box)
	if outCounts["minecraft:dark_oak_planks"] == 0 || outCounts["minecraft:cobblestone"] == 0 ||
		outCounts["minecraft:chest"] == 0 {
		t.Errorf("PillagerOutpost watchtower missing expected blocks: %v", outCounts)
	}
	outChests, outBanners, outPillagers := 0, 0, 0
	for _, be := range collectRegionBlockEntities(outRegion) {
		switch be.ID {
		case "minecraft:chest":
			if len(be.Items) > 0 {
				outChests++
			}
		case "minecraft:banner":
			outBanners++
		}
	}
	for _, ent := range collectRegionEntities(outRegion) {
		if ent.ID == "minecraft:pillager" {
			outPillagers++
		}
	}
	if outChests == 0 || outBanners == 0 || outPillagers == 0 {
		t.Errorf("PillagerOutpost lootChests=%d banners=%d pillagers=%d, all want > 0",
			outChests, outBanners, outPillagers)
	}
}

func TestRemainingStructuresSeed12345StartsAndPieceContentsParity(t *testing.T) {
	const seed = int64(12345)
	od, _, _, _ := vanillaGeneratorInputs(seed)
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}
	nd, err := worldgen.LoadNetherFinalDensity(seed)
	if err != nil {
		t.Fatalf("LoadNetherFinalDensity: %v", err)
	}
	ed, err := worldgen.LoadEndFinalDensity(seed)
	if err != nil {
		t.Fatalf("LoadEndFinalDensity: %v", err)
	}

	// -------------------------------------------------------------------------
	// 1. Start positions on seed=12345 across all Issue #23 structure sets
	// -------------------------------------------------------------------------
	findOverworldStart := func(setName, wantStructure string, maxRadius int32) *StructureStartStub {
		set := sets.Sets[setName]
		if set == nil {
			t.Fatalf("missing structure set %s", setName)
		}
		wantDef := sets.Structures[wantStructure]
		if set.Placement != nil && set.Placement.RandomSpread != nil && set.Placement.RandomSpread.Spacing > 1 {
			spread := set.Placement.RandomSpread
			for rx := -maxRadius; rx <= maxRadius; rx++ {
				for rz := -maxRadius; rz <= maxRadius; rz++ {
					cand := spread.PotentialChunk(seed, rx*int32(spread.Spacing), rz*int32(spread.Spacing))
					if !set.IsStartChunk(seed, cand[0], cand[1]) {
						continue
					}
					if wantDef != nil && !isStructureBiomeAllowed(od, sets, wantDef, int(cand[0])*16+8, 64, int(cand[1])*16+8) {
						continue
					}
					stub, ok := FindStructureSetStart(od, sets, setName, seed, cand[0], cand[1])
					if !ok || stub == nil {
						continue
					}
					if wantStructure != "" && stub.StructureName != wantStructure {
						continue
					}
					return stub
				}
			}
		} else {
			for cx := -maxRadius; cx <= maxRadius; cx++ {
				for cz := -maxRadius; cz <= maxRadius; cz++ {
					if !set.IsStartChunk(seed, cx, cz) {
						continue
					}
					if wantDef != nil && !isStructureBiomeAllowed(od, sets, wantDef, int(cx)*16+8, 64, int(cz)*16+8) {
						continue
					}
					stub, ok := FindStructureSetStart(od, sets, setName, seed, cx, cz)
					if !ok || stub == nil {
						continue
					}
					if wantStructure != "" && stub.StructureName != wantStructure {
						continue
					}
					return stub
				}
			}
		}
		t.Fatalf("did not find start for set %s (want=%s) within radius %d on seed %d", setName, wantStructure, maxRadius, seed)
		return nil
	}

	// Nether starts: fortress, bastion_remnant, nether_fossil, ruined_portal_nether
	var fortStub, bastStub *StructureStartStub
	netherSpread := sets.Sets["minecraft:nether_complexes"].Placement.RandomSpread
	for rx := int32(-6); rx <= 6 && (fortStub == nil || bastStub == nil); rx++ {
		for rz := int32(-6); rz <= 6 && (fortStub == nil || bastStub == nil); rz++ {
			cand := netherSpread.PotentialChunk(seed, rx*int32(netherSpread.Spacing), rz*int32(netherSpread.Spacing))
			stub, ok := FindStructureSetStart(nd, sets, "minecraft:nether_complexes", seed, cand[0], cand[1])
			if !ok || stub == nil {
				continue
			}
			if stub.StructureName == "minecraft:fortress" && fortStub == nil {
				fortStub = stub
			} else if stub.StructureName == "minecraft:bastion_remnant" && bastStub == nil {
				bastStub = stub
			}
		}
	}
	if fortStub == nil || bastStub == nil {
		t.Fatalf("missing nether_complexes starts on seed 12345: fortress=%v bastion=%v", fortStub, bastStub)
	}

	var nFossilStub *NetherFossilStub
	for cx := int32(-30); cx <= 30 && nFossilStub == nil; cx++ {
		for cz := int32(-30); cz <= 30 && nFossilStub == nil; cz++ {
			if stub := NetherFossilGenerationPoint(nd, sets, seed, cx, cz); stub != nil {
				nFossilStub = stub
			}
		}
	}
	if nFossilStub == nil {
		t.Fatalf("did not find valid nether_fossil start on seed 12345")
	}

	var nPortalStub *RuinedPortalStub
	for cx := int32(-25); cx <= 25 && nPortalStub == nil; cx++ {
		for cz := int32(-25); cz <= 25 && nPortalStub == nil; cz++ {
			stub, err := RuinedPortalGenerationPoint(nd, sets, seed, cx, cz)
			if err == nil && stub != nil {
				nPortalStub = stub
			}
		}
	}
	if nPortalStub == nil {
		t.Fatalf("did not find valid ruined_portal_nether start on seed 12345")
	}

	// End City start (and find one with a Ship for piece contents verification)
	var endCityStart, endCityWithShip *EndCityStart
	endSpread := sets.Sets["minecraft:end_cities"].Placement.RandomSpread
	for rx := int32(-12); rx <= 12 && (endCityStart == nil || endCityWithShip == nil); rx++ {
		for rz := int32(-12); rz <= 12 && (endCityStart == nil || endCityWithShip == nil); rz++ {
			cand := endSpread.PotentialChunk(seed, rx*int32(endSpread.Spacing), rz*int32(endSpread.Spacing))
			st := EndCityGenerationPoint(ed, sets, seed, cand[0], cand[1])
			if st == nil {
				continue
			}
			if endCityStart == nil {
				endCityStart = st
			}
			for _, p := range st.Pieces {
				if p.Name == "ship" {
					endCityWithShip = st
					break
				}
			}
		}
	}
	if endCityStart == nil || endCityWithShip == nil {
		t.Fatalf("missing end_city start (%v) or end_city with ship (%v) on seed 12345", endCityStart, endCityWithShip)
	}

	// Stronghold start (concentric rings)
	rings := sets.StrongholdRingPositions(seed, nil)
	if len(rings) != 128 {
		t.Fatalf("StrongholdRingPositions count = %d, want 128", len(rings))
	}

	// Overworld structure starts on seed=12345
	rpStub := findOverworldStart("minecraft:ruined_portals", "", 4)
	swOceanStub := findOverworldStart("minecraft:shipwrecks", "minecraft:shipwreck", 6)
	swBeachStub := findOverworldStart("minecraft:shipwrecks", "minecraft:shipwreck_beached", 12)
	orColdStub := findOverworldStart("minecraft:ocean_ruins", "minecraft:ocean_ruin_cold", 6)
	orWarmStub := findOverworldStart("minecraft:ocean_ruins", "minecraft:ocean_ruin_warm", 12)
	btStub := findOverworldStart("minecraft:buried_treasures", "minecraft:buried_treasure", 120)
	dpStub := findOverworldStart("minecraft:desert_pyramids", "minecraft:desert_pyramid", 20)
	jtStub := findOverworldStart("minecraft:jungle_temples", "minecraft:jungle_pyramid", 20)
	igStub := findOverworldStart("minecraft:igloos", "minecraft:igloo", 20)
	shStub := findOverworldStart("minecraft:swamp_huts", "minecraft:swamp_hut", 20)
	msNormStub := findOverworldStart("minecraft:mineshafts", "minecraft:mineshaft", 20)
	msMesaStub := findOverworldStart("minecraft:mineshafts", "minecraft:mineshaft_mesa", 350)
	trStub := findOverworldStart("minecraft:trail_ruins", "minecraft:trail_ruins", 20)

	t.Logf("seed=12345 starts: fortress=(%d,%d) bastion=(%d,%d) nether_fossil=(%d,%d,%d) end_city=(%d,%d ship=%d,%d) ruined_portal=(%d,%d) shipwreck=(%d,%d / beached=%d,%d) ocean_ruin=(cold=%d,%d / warm=%d,%d) buried_treasure=(%d,%d) desert_pyramid=(%d,%d) jungle_pyramid=(%d,%d) igloo=(%d,%d) swamp_hut=(%d,%d) mineshaft=(%d,%d / mesa=%d,%d) trail_ruins=(%d,%d)",
		fortStub.ChunkX, fortStub.ChunkZ, bastStub.ChunkX, bastStub.ChunkZ,
		nFossilStub.X, nFossilStub.Y, nFossilStub.Z,
		endCityStart.ChunkX, endCityStart.ChunkZ, endCityWithShip.ChunkX, endCityWithShip.ChunkZ,
		rpStub.ChunkX, rpStub.ChunkZ,
		swOceanStub.ChunkX, swOceanStub.ChunkZ, swBeachStub.ChunkX, swBeachStub.ChunkZ,
		orColdStub.ChunkX, orColdStub.ChunkZ, orWarmStub.ChunkX, orWarmStub.ChunkZ,
		btStub.ChunkX, btStub.ChunkZ, dpStub.ChunkX, dpStub.ChunkZ,
		jtStub.ChunkX, jtStub.ChunkZ, igStub.ChunkX, igStub.ChunkZ,
		shStub.ChunkX, shStub.ChunkZ, msNormStub.ChunkX, msNormStub.ChunkZ,
		msMesaStub.ChunkX, msMesaStub.ChunkZ, trStub.ChunkX, trStub.ChunkZ)

	// -------------------------------------------------------------------------
	// 2. Piece contents (blocks, block entities/loot, entities)
	// -------------------------------------------------------------------------

	// 2a. Nether Fortress (find a start on seed=12345 that includes nfMonsterThrone and cover its full Box)
	fortStart := BuildNetherFortressStart(seed, fortStub.ChunkX, fortStub.ChunkZ)
	hasThrone := func(st *NetherFortressStart) bool {
		if st == nil {
			return false
		}
		for _, p := range st.Pieces {
			if p.kind == nfMonsterThrone {
				return true
			}
		}
		return false
	}
	for off := int32(1); !hasThrone(fortStart) && off < 30; off++ {
		cand := BuildNetherFortressStart(seed, fortStub.ChunkX+off, fortStub.ChunkZ)
		if hasThrone(cand) {
			fortStart = cand
			break
		}
	}
	if fortStart == nil || !hasThrone(fortStart) {
		t.Fatalf("BuildNetherFortressStart failed to produce nfMonsterThrone: %v", fortStart)
	}
	fortMinCX, fortMaxCX := int32(fortStart.Box.minX>>4), int32(fortStart.Box.maxX>>4)
	fortMinCZ, fortMaxCZ := int32(fortStart.Box.minZ>>4), int32(fortStart.Box.maxZ>>4)
	fortCenterCX, fortCenterCZ := (fortMinCX+fortMaxCX)/2, (fortMinCZ+fortMaxCZ)/2
	fortRadius := max(fortMaxCX-fortCenterCX, fortMaxCZ-fortCenterCZ) + 1
	fortRegion := buildFlatTestDecorationRegion(t, fortCenterCX, fortCenterCZ, fortRadius, 45, false)
	for cx := fortMinCX; cx <= fortMaxCX; cx++ {
		for cz := fortMinCZ; cz <= fortMaxCZ; cz++ {
			if ch := fortRegion.chunks[[2]int32{cx, cz}]; ch != nil {
				PlaceNetherFortress(ch, fortStart, cx, cz)
			}
		}
	}
	fortCounts := countBlocksInBox(fortRegion, fortStart.Box)
	if fortCounts["minecraft:nether_bricks"] == 0 || fortCounts["minecraft:nether_brick_fence"] == 0 ||
		fortCounts["minecraft:spawner"] == 0 {
		t.Errorf("NetherFortress missing blocks: %v", fortCounts)
	}
	fortSpawners, fortChests, fortMobs := 0, 0, 0
	for _, be := range collectRegionBlockEntities(fortRegion) {
		switch be.ID {
		case "minecraft:mob_spawner":
			if be.SpawnerEntityID == "minecraft:blaze" {
				fortSpawners++
			}
		case "minecraft:chest":
			if len(be.Items) > 0 {
				fortChests++
			}
		}
	}
	for _, ent := range collectRegionEntities(fortRegion) {
		if ent.ID == "minecraft:wither_skeleton" || ent.ID == "minecraft:blaze" {
			fortMobs++
		}
	}
	if fortSpawners == 0 || fortMobs == 0 {
		t.Errorf("NetherFortress blazeSpawners=%d fortChests=%d fortMobs=%d", fortSpawners, fortChests, fortMobs)
	}

	// 2b. Bastion Remnant
	bastDef := sets.Structures["minecraft:bastion_remnant"]
	bastStart, err := BuildJigsawStart(nil, nil, bastDef, seed, bastStub.ChunkX, bastStub.ChunkZ, func(x, z int, oceanFloor bool) int {
		return 33
	})
	if err != nil || bastStart == nil || len(bastStart.Pieces) == 0 {
		t.Fatalf("BuildJigsawStart(bastion_remnant): %v, err=%v", bastStart, err)
	}
	bastRegion := buildFlatTestDecorationRegion(t, bastStub.ChunkX, bastStub.ChunkZ, 4, 32, false)
	for cx := bastStub.ChunkX - 4; cx <= bastStub.ChunkX+4; cx++ {
		for cz := bastStub.ChunkZ - 4; cz <= bastStub.ChunkZ+4; cz++ {
			PlaceJigsawStartsForChunk(bastRegion, []*JigsawStart{bastStart}, "minecraft:bastion_remnant", 4, seed, cx, cz)
		}
	}
	bastCounts := countBlocksInBox(bastRegion, bastStart.Box)
	if bastCounts["minecraft:blackstone"] == 0 || (bastCounts["minecraft:gilded_blackstone"] == 0 && bastCounts["minecraft:gold_block"] == 0) {
		t.Errorf("BastionRemnant missing blackstone/gold: %v", bastCounts)
	}
	bastChests, bastPiglins := 0, 0
	for _, be := range collectRegionBlockEntities(bastRegion) {
		if be.ID == "minecraft:chest" && len(be.Items) > 0 {
			bastChests++
		}
	}
	for _, ent := range collectRegionEntities(bastRegion) {
		if ent.ID == "minecraft:piglin" || ent.ID == "minecraft:piglin_brute" {
			bastPiglins++
		}
	}
	if bastChests == 0 || bastPiglins == 0 {
		t.Errorf("BastionRemnant chests=%d piglins=%d, want > 0", bastChests, bastPiglins)
	}

	// 2c. End City + Ship (cover the entire EndCityWithShip Box so the ship chunk is included)
	ecMinCX, ecMaxCX := int32(endCityWithShip.Box.minX>>4), int32(endCityWithShip.Box.maxX>>4)
	ecMinCZ, ecMaxCZ := int32(endCityWithShip.Box.minZ>>4), int32(endCityWithShip.Box.maxZ>>4)
	ecCenterCX, ecCenterCZ := (ecMinCX+ecMaxCX)/2, (ecMinCZ+ecMaxCZ)/2
	ecRadius := max(ecMaxCX-ecCenterCX, ecMaxCZ-ecCenterCZ) + 1
	ecRegion := buildFlatTestDecorationRegion(t, ecCenterCX, ecCenterCZ, ecRadius, 60, false)
	for cx := ecMinCX; cx <= ecMaxCX; cx++ {
		for cz := ecMinCZ; cz <= ecMaxCZ; cz++ {
			if ch := ecRegion.chunks[[2]int32{cx, cz}]; ch != nil {
				PlaceEndCity(ch, endCityWithShip, cx, cz)
			}
		}
	}
	ecCounts := countBlocksInBox(ecRegion, endCityWithShip.Box)
	if ecCounts["minecraft:purpur_block"] == 0 || ecCounts["minecraft:end_stone_bricks"] == 0 ||
		ecCounts["minecraft:brewing_stand"] == 0 {
		t.Errorf("EndCity+Ship missing blocks: %v", ecCounts)
	}
	ecChests, ecShulkers, ecElytraFrames := 0, 0, 0
	for _, be := range collectRegionBlockEntities(ecRegion) {
		if be.ID == "minecraft:chest" && (len(be.Items) > 0 || be.LootTable == "minecraft:chests/end_city_treasure") {
			ecChests++
		}
	}
	for _, ent := range collectRegionEntities(ecRegion) {
		switch ent.ID {
		case "minecraft:shulker":
			ecShulkers++
		case "minecraft:item_frame":
			if ent.Item == "minecraft:elytra" {
				ecElytraFrames++
			}
		}
	}
	if ecChests == 0 || ecShulkers == 0 || ecElytraFrames != 1 {
		t.Errorf("EndCity+Ship chests=%d shulkers=%d elytraFrames=%d (want 1)", ecChests, ecShulkers, ecElytraFrames)
	}

	// 2d. Stronghold + PortalRoom
	shStart := BuildStrongholdStart(seed, rings[0][0], rings[0][1])
	if shStart == nil || shStart.PortalRoom == nil {
		t.Fatalf("BuildStrongholdStart failed at ring[0]=%v", rings[0])
	}
	pr := shStart.PortalRoom
	prCX, prCZ := int32(pr.box.minX>>4), int32(pr.box.minZ>>4)
	shRegion := buildFlatTestDecorationRegion(t, prCX, prCZ, 4, 55, false)
	for cx := prCX - 4; cx <= prCX+4; cx++ {
		for cz := prCZ - 4; cz <= prCZ+4; cz++ {
			PlaceStrongholdStart(shRegion, shStart, seed, cx, cz)
		}
	}
	prCounts := countBlocksInBox(shRegion, pr.box)
	if prCounts["minecraft:end_portal_frame"] != 12 || prCounts["minecraft:spawner"] != 1 || prCounts["minecraft:lava"] < 15 {
		t.Errorf("Stronghold PortalRoom blocks: frames=%d spawner=%d lava=%d",
			prCounts["minecraft:end_portal_frame"], prCounts["minecraft:spawner"], prCounts["minecraft:lava"])
	}

	// 2e. Ruined Portal (on seed 12345 at chunk (1,0))
	rp10, err := RuinedPortalGenerationPoint(od, sets, seed, 1, 0)
	if err != nil || rp10 == nil {
		t.Fatalf("RuinedPortalGenerationPoint(1,0): %v, %v", rp10, err)
	}
	rpRegion := buildFlatTestDecorationRegion(t, 1, 0, 2, 12, false)
	if err := PlaceRuinedPortalPiece(rpRegion, rp10, seed, 1, 0); err != nil {
		t.Fatalf("PlaceRuinedPortalPiece: %v", err)
	}
	rpCounts := countBlocksInBox(rpRegion, msBox{minX: 0, minY: 0, minZ: -16, maxX: 48, maxY: 40, maxZ: 32})
	if rpCounts["minecraft:obsidian"]+rpCounts["minecraft:crying_obsidian"] == 0 ||
		rpCounts["minecraft:netherrack"] == 0 || rpCounts["minecraft:chest"] == 0 {
		t.Errorf("RuinedPortal missing blocks: %v", rpCounts)
	}

	// 2f. Ocean Ruin (warm with suspicious_sand + chest) & Buried Treasure & Shipwreck
	warmStub, warmRng, err := OceanRuinGenerationPoint(od, sets, seed, orWarmStub.ChunkX, orWarmStub.ChunkZ)
	if err != nil || warmStub == nil {
		t.Fatalf("OceanRuinGenerationPoint(warm): %v, %v", warmStub, err)
	}
	orRegion := buildFlatTestDecorationRegion(t, orWarmStub.ChunkX, orWarmStub.ChunkZ, 2, 48, true)
	if err := orRegion.setSource(orWarmStub.ChunkX, orWarmStub.ChunkZ); err != nil {
		t.Fatalf("setSource: %v", err)
	}
	if err := PlaceOceanRuinPieces(orRegion, warmRng, warmStub, seed); err != nil {
		t.Fatalf("PlaceOceanRuinPieces: %v", err)
	}
	orBrushable, orChests := 0, 0
	for _, be := range collectRegionBlockEntities(orRegion) {
		switch be.ID {
		case "minecraft:brushable_block":
			orBrushable++
		case "minecraft:chest":
			if len(be.Items) > 0 {
				orChests++
			}
		}
	}
	if orBrushable == 0 || orChests == 0 {
		t.Errorf("OceanRuin (warm) brushable=%d chests=%d, want > 0", orBrushable, orChests)
	}

	// Buried Treasure loot verification
	btRegion := buildFlatTestDecorationRegion(t, btStub.ChunkX, btStub.ChunkZ, 1, 56, true)
	PlaceBuriedTreasureInChunk(btRegion, seed, btStub.ChunkX, btStub.ChunkZ)
	btChests := 0
	for _, be := range collectRegionBlockEntities(btRegion) {
		if be.ID == "minecraft:chest" && len(be.Items) > 0 {
			btChests++
		}
	}
	if btChests != 1 {
		t.Errorf("BuriedTreasure loot chests = %d, want 1", btChests)
	}

	// 2g. Igloo (entities + loot), Swamp Hut (witch + cat), Desert Pyramid & Jungle Temple (loot)
	var igWithBase *IglooStart
	for s := int64(0); s < 50 && igWithBase == nil; s++ {
		st := BuildIglooStart(seed+s, igStub.ChunkX, igStub.ChunkZ)
		if len(st.Pieces) > 1 {
			igWithBase = st
		}
	}
	igRegion := buildFlatTestDecorationRegion(t, igStub.ChunkX, igStub.ChunkZ, 2, 64, false)
	for cx := igStub.ChunkX - 1; cx <= igStub.ChunkX+1; cx++ {
		for cz := igStub.ChunkZ - 1; cz <= igStub.ChunkZ+1; cz++ {
			PlaceIgloosForChunk(igRegion, []*IglooStart{igWithBase}, seed, cx, cz)
		}
	}
	igVillager, igZombieVillager, igChest := 0, 0, 0
	for _, ent := range collectRegionEntities(igRegion) {
		switch ent.ID {
		case "minecraft:villager":
			igVillager++
		case "minecraft:zombie_villager":
			igZombieVillager++
		}
	}
	for _, be := range collectRegionBlockEntities(igRegion) {
		if be.ID == "minecraft:chest" && len(be.Items) > 0 {
			igChest++
		}
	}
	if igVillager != 1 || igZombieVillager != 1 || igChest != 1 {
		t.Errorf("Igloo basement villager=%d zombie_villager=%d chest=%d, want 1,1,1",
			igVillager, igZombieVillager, igChest)
	}

	shutStart := BuildSwampHutStart(seed, shStub.ChunkX, shStub.ChunkZ)
	shutRegion := buildFlatTestDecorationRegion(t, shStub.ChunkX, shStub.ChunkZ, 2, 62, false)
	for cx := shStub.ChunkX - 1; cx <= shStub.ChunkX+1; cx++ {
		for cz := shStub.ChunkZ - 1; cz <= shStub.ChunkZ+1; cz++ {
			PlaceSwampHutsForChunk(shutRegion, []*SwampHutStart{shutStart}, seed, cx, cz)
		}
	}
	witches, cats := 0, 0
	for _, ent := range collectRegionEntities(shutRegion) {
		switch ent.ID {
		case "minecraft:witch":
			witches++
		case "minecraft:cat":
			cats++
		}
	}
	if witches != 1 || cats != 1 {
		t.Errorf("SwampHut witch=%d cat=%d, want 1,1", witches, cats)
	}

	// 2h. Mineshaft (spawner + chest_minecart)
	msStart := BuildMineshaftStart(seed, msNormStub.ChunkX, msNormStub.ChunkZ, false)
	msRegion := buildFlatTestDecorationRegion(t, msNormStub.ChunkX, msNormStub.ChunkZ, 4, 80, false)
	msRegion.bindSeed(seed)
	for cx := msNormStub.ChunkX - 4; cx <= msNormStub.ChunkX+4; cx++ {
		for cz := msNormStub.ChunkZ - 4; cz <= msNormStub.ChunkZ+4; cz++ {
			PlaceMineshaftStart(msRegion, msStart, seed, cx, cz)
		}
	}
	msCounts := countBlocksInBox(msRegion, msStart.Box)
	if msCounts["minecraft:oak_planks"] == 0 || msCounts["minecraft:rail"] == 0 || msCounts["minecraft:cobweb"] == 0 {
		t.Errorf("Mineshaft missing blocks: %v", msCounts)
	}
	msMinecarts := 0
	for _, ent := range collectRegionEntities(msRegion) {
		if ent.ID == "minecraft:chest_minecart" && ent.LootTable == "minecraft:chests/abandoned_mineshaft" {
			msMinecarts++
		}
	}
	if msMinecarts == 0 {
		t.Errorf("Mineshaft placed 0 chest_minecart entities")
	}

	// 2i. Overworld Fossil (placeFossil) & Nether Fossil (PlaceNetherFossil)
	fossilRegion := buildFlatTestDecorationRegion(t, 0, 0, 1, 50, false)
	if err := fossilRegion.setSource(0, 0); err != nil {
		t.Fatalf("setSource: %v", err)
	}
	fs := mustFeatureSet(t)
	fossilCfg, err := fs.Fossil("minecraft:fossil_coal")
	if err != nil {
		t.Fatalf("fs.Fossil: %v", err)
	}
	if !fossilRegion.placeFossil(fs, worldgen.NewLegacy(seed), worldgen.FeaturePosition{X: 4, Y: 40, Z: 4}, fossilCfg) {
		t.Errorf("placeFossil returned false in solid stone region")
	}
	fCounts := countBlocksInBox(fossilRegion, msBox{minX: -16, minY: MinY, minZ: -16, maxX: 31, maxY: 50, maxZ: 31})
	if fCounts["minecraft:bone_block"] == 0 {
		t.Errorf("Overworld placeFossil placed 0 bone_block: %v", fCounts)
	}

	nfCX, nfCZ := int32(nFossilStub.X>>4), int32(nFossilStub.Z>>4)
	nfRegion := buildFlatTestDecorationRegion(t, nfCX, nfCZ, 1, nFossilStub.Y-1, false)
	for cx := nfCX - 1; cx <= nfCX+1; cx++ {
		for cz := nfCZ - 1; cz <= nfCZ+1; cz++ {
			if ch := nfRegion.chunks[[2]int32{cx, cz}]; ch != nil {
				PlaceNetherFossil(ch, nFossilStub, seed, cx, cz)
			}
		}
	}
	nfCounts := countBlocksInBox(nfRegion, msBox{
		minX: nFossilStub.X - 8, minY: nFossilStub.Y - 2, minZ: nFossilStub.Z - 8,
		maxX: nFossilStub.X + 16, maxY: nFossilStub.Y + 16, maxZ: nFossilStub.Z + 16,
	})
	if nfCounts["minecraft:bone_block"] == 0 {
		t.Errorf("PlaceNetherFossil placed 0 bone_block: %v", nfCounts)
	}
}
