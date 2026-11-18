package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func TestOceanRuinWarmRotationWaterloggingAndLootSeedParity(t *testing.T) {
	stairsNorth := testMustStateID(t, "minecraft:sandstone_stairs", map[string]string{
		"facing": "north", "half": "bottom", "shape": "straight", "waterlogged": "false",
	})
	stairsEast := testMustStateID(t, "minecraft:sandstone_stairs", map[string]string{
		"facing": "east", "half": "bottom", "shape": "straight", "waterlogged": "false",
	})
	if got := rotateTemplateState(stairsNorth, 1); got != stairsEast {
		t.Fatalf("rotateTemplateState(sandstone_stairs[north], 1) = %s, want %s", stateLabel(got), stateLabel(stairsEast))
	}

	gen := NewVanillaRegionGenerator(-135792468)

	type wantBE struct {
		id        string
		x, y, z   int
		lootTable string
		lootSeed  int64
	}
	type chunkBECase struct {
		cx, cz    int32
		fullOnly  bool
		want      []wantBE
		checkSand bool
	}

	beCases := []chunkBECase{
		{
			cx: 100, cz: 24,
			want: []wantBE{
				{"minecraft:chest", 1610, 53, 393, "minecraft:chests/underwater_ruin_big", 327276770894224912},
			},
		},
		{
			cx: 101, cz: 23,
			checkSand: true,
			want: []wantBE{
				{"minecraft:chest", 1622, 49, 378, "minecraft:chests/underwater_ruin_small", -3945322421660737227},
				{"minecraft:brushable_block", 1621, 48, 376, "minecraft:archaeology/ocean_ruin_warm", 47612160474841448},
				{"minecraft:brushable_block", 1619, 48, 380, "minecraft:archaeology/ocean_ruin_warm", 7408765962876320799},
				{"minecraft:brushable_block", 1618, 48, 380, "minecraft:archaeology/ocean_ruin_warm", -8835826997305152973},
				{"minecraft:brushable_block", 1620, 49, 376, "minecraft:archaeology/ocean_ruin_warm", 7448328244429286069},
				{"minecraft:brushable_block", 1621, 49, 378, "minecraft:archaeology/ocean_ruin_warm", 7940117683873402692},
			},
		},
		{
			cx: 101, cz: 24,
			fullOnly: true,
			want: []wantBE{
				{"minecraft:chest", 1619, 48, 388, "minecraft:chests/underwater_ruin_small", 7021670693966721566},
				{"minecraft:brushable_block", 1618, 48, 385, "minecraft:archaeology/ocean_ruin_warm", 2659845528604868278},
				{"minecraft:brushable_block", 1619, 48, 386, "minecraft:archaeology/ocean_ruin_warm", 4060916289001824657},
				{"minecraft:brushable_block", 1621, 48, 387, "minecraft:archaeology/ocean_ruin_warm", 5939825419200402945},
				{"minecraft:brushable_block", 1619, 48, 390, "minecraft:archaeology/ocean_ruin_warm", -8501791538124035346},
				{"minecraft:brushable_block", 1618, 48, 390, "minecraft:archaeology/ocean_ruin_warm", 1176691986775959440},
			},
		},
		{
			cx: -100, cz: -197,
			fullOnly: true,
			want: []wantBE{
				{"minecraft:chest", -1597, 48, -3144, "minecraft:chests/underwater_ruin_small", -6025049115763629012},
				{"minecraft:brushable_block", -1595, 48, -3145, "minecraft:archaeology/ocean_ruin_warm", 2794589107931183443},
				{"minecraft:brushable_block", -1595, 48, -3142, "minecraft:archaeology/ocean_ruin_warm", 8632464904318268660},
				{"minecraft:brushable_block", -1595, 50, -3141, "minecraft:archaeology/ocean_ruin_warm", 3145328370941721681},
			},
		},
	}

	waterLevel1 := testMustStateID(t, "minecraft:water", map[string]string{"level": "1"})
	magmaID := testMustStateID(t, "minecraft:magma_block", nil)
	bubbleDragID := testMustStateID(t, "minecraft:bubble_column", map[string]string{"drag": "true"})

	for _, tc := range beCases {
		if testing.Short() && tc.fullOnly {
			continue
		}
		c := gen(tc.cx, tc.cz)
		gotBEs := c.StructureBlockEntities()
		if len(gotBEs) != len(tc.want) {
			t.Fatalf("chunk (%d,%d) StructureBlockEntities count = %d, want %d (%+v)",
				tc.cx, tc.cz, len(gotBEs), len(tc.want), gotBEs)
		}

		type beKey struct {
			id      string
			x, y, z int
		}
		byKey := make(map[beKey]StructureBlockEntity, len(gotBEs))
		for _, be := range gotBEs {
			byKey[beKey{be.ID, be.X, be.Y, be.Z}] = be
		}
		for _, w := range tc.want {
			got, ok := byKey[beKey{w.id, w.x, w.y, w.z}]
			if !ok {
				t.Fatalf("chunk (%d,%d) missing block entity %s at (%d,%d,%d)",
					tc.cx, tc.cz, w.id, w.x, w.y, w.z)
			}
			if got.LootTable != w.lootTable {
				t.Fatalf("chunk (%d,%d) BE %s at (%d,%d,%d) LootTable = %q, want %q",
					tc.cx, tc.cz, w.id, w.x, w.y, w.z, got.LootTable, w.lootTable)
			}
			if got.LootTableSeed != w.lootSeed {
				t.Fatalf("chunk (%d,%d) BE %s at (%d,%d,%d) LootTableSeed = %d, want %d",
					tc.cx, tc.cz, w.id, w.x, w.y, w.z, got.LootTableSeed, w.lootSeed)
			}
			if tc.checkSand && w.id == "minecraft:brushable_block" {
				lx := w.x - int(tc.cx)*16
				lz := w.z - int(tc.cz)*16
				st, _ := stateByID(c.GetBlock(lx, w.y, lz))
				if st.Name != "minecraft:suspicious_sand" {
					t.Fatalf("chunk (%d,%d) brushable_block at (%d,%d,%d) holds %s, want minecraft:suspicious_sand",
						tc.cx, tc.cz, w.x, w.y, w.z, st.Name)
				}
			}
		}

		if tc.cx == 100 && tc.cz == 24 {
			checkBlock := func(wx, wy, wz int, want uint16) {
				got := c.GetBlock(wx-1600, wy, wz-384)
				if got != want {
					t.Fatalf("chunk (100,24) at (%d,%d,%d) = %s, want %s",
						wx, wy, wz, stateLabel(got), stateLabel(want))
				}
			}
			checkBlock(1605, 53, 390, StateAir)
			checkBlock(1603, 53, 396, StateAir)
			checkBlock(1605, 53, 393, waterLevel1)
			for _, p := range [][3]int{
				{1603, 53, 385},
				{1604, 53, 386},
				{1607, 53, 386},
				{1608, 53, 386},
				{1611, 53, 397},
				{1607, 54, 394},
			} {
				checkBlock(p[0], p[1], p[2], StateWater)
			}

			chestID := c.GetBlock(1610-1600, 53, 393-384)
			chestSt, ok := stateByID(chestID)
			if !ok || chestSt.Name != "minecraft:chest" || chestSt.Properties["waterlogged"] != "true" {
				t.Fatalf("chunk (100,24) chest at (1610,53,393) = %s, want waterlogged chest", stateLabel(chestID))
			}

			checkBlock(1606, 53, 393, magmaID)
			checkBlock(1606, 54, 393, bubbleDragID)

			warmBlocks := make(map[string]int)
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					for y := 40; y <= 70; y++ {
						if st, ok := stateByID(c.GetBlock(x, y, z)); ok {
							warmBlocks[st.Name]++
						}
					}
				}
			}
			for _, name := range []string{
				"minecraft:sandstone",
				"minecraft:cut_sandstone",
				"minecraft:chiseled_sandstone",
				"minecraft:sea_lantern",
				"minecraft:sand",
			} {
				if warmBlocks[name] == 0 {
					t.Fatalf("chunk (100,24) missing expected warm ocean ruin block %s", name)
				}
			}
		}
	}
}

func TestFossilCoralClawAndSwampHutParity(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}

	// Part A: Synthetic placeCoralClaw test where stone blocks all 4 cardinal
	// sideways steps at origin.Y=45; after failing sidewayLen, claw must step
	// back, increment by++ to 46, and place upward upLen coral blocks.
	stoneID := testMustStateID(t, "minecraft:stone", nil)
	tubeBlockID := testMustStateID(t, "minecraft:tube_coral_block", nil)
	cardinals := [][2]int{{0, -1}, {1, 0}, {0, 1}, {-1, 0}}

	var foundUpwardAfterBlocked bool
	for cand := int64(1); cand <= 50; cand++ {
		reg := buildFlatTestDecorationRegion(t, 0, 0, 1, 40, true)
		if err := reg.setSource(0, 0); err != nil {
			t.Fatalf("setSource: %v", err)
		}
		for _, d := range cardinals {
			reg.setBlock(8+d[0], 45, 8+d[1], stoneID)
		}
		rng := worldgen.NewXoroshiro(cand)
		reg.placeCoralClaw(rng, worldgen.FeaturePosition{X: 8, Y: 45, Z: 8}, tubeBlockID, set)
		for _, d := range cardinals {
			if reg.getBlock(8+d[0], 46, 8+d[1]) == tubeBlockID {
				foundUpwardAfterBlocked = true
				break
			}
		}
		if foundUpwardAfterBlocked {
			break
		}
	}
	if !foundUpwardAfterBlocked {
		t.Fatal("placeCoralClaw did not place upward segment at y=46 after blocked sideways step for seeds 1..50")
	}

	// Part B: Synthetic SwampHutPiece SHAPE_CHECK_BLOCKS fence post-processing.
	regHut := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
	if err := regHut.setSource(0, 0); err != nil {
		t.Fatalf("setSource: %v", err)
	}
	hut := BuildSwampHutStart(42, 0, 0)
	PlaceSwampHutsForChunk(regHut, []*SwampHutStart{hut}, 42, 0, 0)
	regHut.runPostProcessing(set)

	var synthFences int
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			for y := 60; y <= 75; y++ {
				st, ok := stateByID(regHut.getBlock(x, y, z))
				if !ok || st.Name != "minecraft:oak_fence" {
					continue
				}
				synthFences++
				if st.Properties["waterlogged"] != "false" {
					t.Fatalf("synthetic swamp hut fence at (%d,%d,%d) waterlogged=%q, want false", x, y, z, st.Properties["waterlogged"])
				}
				if st.Properties["north"] != "true" && st.Properties["east"] != "true" &&
					st.Properties["south"] != "true" && st.Properties["west"] != "true" {
					t.Fatalf("synthetic swamp hut fence at (%d,%d,%d) unconnected: %s", x, y, z, stateLabel(regHut.getBlock(x, y, z)))
				}
			}
		}
	}
	if synthFences != 4 {
		t.Fatalf("synthetic swamp hut oak_fence count = %d, want 4", synthFences)
	}

	// Part C: Full worldgen fossil_lower and swamp_hut verification.
	genFossil := NewVanillaRegionGenerator(8675309)
	fossilChunk := genFossil(-23, -361)
	var boneBlocks, deepslateDiamonds, coalOres int
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			for y := MinY; y < MinY+WorldHeight; y++ {
				id := fossilChunk.GetBlock(x, y, z)
				if isAirState(id) {
					continue
				}
				st, _ := stateByID(id)
				switch st.Name {
				case "minecraft:bone_block":
					boneBlocks++
				case "minecraft:deepslate_diamond_ore":
					deepslateDiamonds++
				case "minecraft:coal_ore":
					coalOres++
				}
			}
		}
	}
	if boneBlocks != 62 || deepslateDiamonds != 38 || coalOres != 85 {
		t.Fatalf("seed 8675309 (-23,-361) fossil counts = (bone=%d, diamond=%d, coal=%d), want (62, 38, 85)",
			boneBlocks, deepslateDiamonds, coalOres)
	}

	if !testing.Short() {
		genHut := NewVanillaRegionGenerator(-135792468)
		hutChunk := genHut(-77, -94)
		var hutFences int
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				for y := MinY; y < MinY+WorldHeight; y++ {
					id := hutChunk.GetBlock(x, y, z)
					if isAirState(id) {
						continue
					}
					st, _ := stateByID(id)
					if st.Name != "minecraft:oak_fence" {
						continue
					}
					hutFences++
					if st.Properties["waterlogged"] != "false" {
						t.Fatalf("swamp_hut (-77,-94) fence at (%d,%d,%d) waterlogged=%q, want false",
							x, y, z, st.Properties["waterlogged"])
					}
					if st.Properties["north"] != "true" && st.Properties["east"] != "true" &&
						st.Properties["south"] != "true" && st.Properties["west"] != "true" {
						t.Fatalf("swamp_hut (-77,-94) fence at (%d,%d,%d) unconnected: %s",
							x, y, z, stateLabel(id))
					}
				}
			}
		}
		if hutFences != 4 {
			t.Fatalf("seed -135792468 (-77,-94) oak_fence count = %d, want 4", hutFences)
		}
	}
}

func TestWarmLukewarmOceanSwampAndMushroomFieldsThreeSeeds(t *testing.T) {
	strSeed, ok := ParseWorldSeed("coral-swamp-mycelium")
	if !ok || strSeed != 1319868492 {
		t.Fatalf("ParseWorldSeed(\"coral-swamp-mycelium\") = (%d, %v), want (1319868492, true)", strSeed, ok)
	}

	type chunkCase struct {
		seed   int64
		cx, cz int32
		biome  string
	}
	cases := []chunkCase{
		{seed: 8675309, cx: -120, cz: -400, biome: "minecraft:warm_ocean"},
		{seed: -135792468, cx: 382, cz: -126, biome: "minecraft:mushroom_fields"},
		{seed: 8675309, cx: -20, cz: -332, biome: "minecraft:swamp"},
		{seed: -135792468, cx: 384, cz: -124, biome: "minecraft:mushroom_fields"},
		{seed: -135792468, cx: -376, cz: -312, biome: "minecraft:warm_ocean"},
		{seed: strSeed, cx: 108, cz: -396, biome: "minecraft:warm_ocean"},
		{seed: strSeed, cx: 108, cz: -344, biome: "minecraft:deep_lukewarm_ocean"},
		{seed: strSeed, cx: 96, cz: -400, biome: "minecraft:lukewarm_ocean"},
		{seed: strSeed, cx: 293, cz: -402, biome: "minecraft:swamp"},
	}

	gens := make(map[int64]Generator)
	for i, tc := range cases {
		if testing.Short() && i >= 2 {
			continue
		}
		gen, exists := gens[tc.seed]
		if !exists {
			gen = NewVanillaRegionGenerator(tc.seed)
			gens[tc.seed] = gen
		}
		c := gen(tc.cx, tc.cz)
		if !chunkHasBiome(c, tc.biome) {
			t.Fatalf("seed=%d chunk=(%d,%d) missing expected biome %s", tc.seed, tc.cx, tc.cz, tc.biome)
		}

		counts := make(map[string]int)
		var wallCoralWL, seaPickleWL, y62Water, y62Land int
		var stitchedMushroomCap, badKelpTipAge int

		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				for y := MinY; y < MinY+WorldHeight; y++ {
					id := c.GetBlock(x, y, z)
					if isAirState(id) {
						continue
					}
					st, _ := stateByID(id)
					counts[st.Name]++
					if y == 62 {
						switch {
						case st.Name == "minecraft:water" && st.Properties["level"] == "0":
							y62Water++
						case st.Name == "minecraft:grass_block", st.Name == "minecraft:dirt", st.Name == "minecraft:mud",
							st.Name == "minecraft:sand", st.Name == "minecraft:clay":
							y62Land++
						}
					}
					if len(st.Name) > 9 && st.Name[len(st.Name)-9:] == "_wall_fan" && st.Properties["waterlogged"] == "true" {
						wallCoralWL++
					}
					if st.Name == "minecraft:sea_pickle" && st.Properties["waterlogged"] == "true" {
						seaPickleWL++
					}
					if (st.Name == "minecraft:brown_mushroom_block" || st.Name == "minecraft:red_mushroom_block") &&
						(st.Properties["north"] == "false" || st.Properties["east"] == "false" ||
							st.Properties["south"] == "false" || st.Properties["west"] == "false" ||
							st.Properties["down"] == "false") {
						stitchedMushroomCap++
					}
					if st.Name == "minecraft:kelp" {
						age := st.Properties["age"]
						if age != "20" && age != "21" && age != "22" && age != "23" {
							badKelpTipAge++
						}
					}
				}
			}
		}

		coralBlocks := counts["minecraft:brain_coral_block"] + counts["minecraft:tube_coral_block"] +
			counts["minecraft:bubble_coral_block"] + counts["minecraft:fire_coral_block"] +
			counts["minecraft:horn_coral_block"]
		topCoralsAndFans := counts["minecraft:tube_coral"] + counts["minecraft:brain_coral"] +
			counts["minecraft:bubble_coral"] + counts["minecraft:fire_coral"] + counts["minecraft:horn_coral"] +
			counts["minecraft:tube_coral_fan"] + counts["minecraft:brain_coral_fan"] +
			counts["minecraft:bubble_coral_fan"] + counts["minecraft:fire_coral_fan"] + counts["minecraft:horn_coral_fan"]

		switch {
		case tc.seed == 8675309 && tc.cx == -120 && tc.cz == -400:
			if wallCoralWL != 42 || seaPickleWL != 2 || counts["minecraft:seagrass"] != 30 ||
				counts["minecraft:tall_seagrass"] != 30 || coralBlocks != 74 || topCoralsAndFans != 11 {
				t.Fatalf("warm_ocean 8675309 (-120,-400) mismatch: wallWL=%d pickleWL=%d seagrass=%d tall=%d coralBlk=%d top=%d",
					wallCoralWL, seaPickleWL, counts["minecraft:seagrass"], counts["minecraft:tall_seagrass"], coralBlocks, topCoralsAndFans)
			}
		case tc.seed == -135792468 && tc.cx == 382 && tc.cz == -126:
			if counts["minecraft:mycelium"] != 256 || counts["minecraft:mushroom_stem"] != 4 ||
				counts["minecraft:brown_mushroom_block"] != 54 || counts["minecraft:brown_mushroom"] != 22 {
				t.Fatalf("mushroom_fields -135792468 (382,-126) mismatch: myc=%d stem=%d brownBlk=%d brownM=%d",
					counts["minecraft:mycelium"], counts["minecraft:mushroom_stem"], counts["minecraft:brown_mushroom_block"], counts["minecraft:brown_mushroom"])
			}
		case tc.seed == 8675309 && tc.cx == -20 && tc.cz == -332:
			if y62Water != 181 || y62Land != 53 || counts["minecraft:oak_log"] != 16 ||
				counts["minecraft:oak_leaves"] != 230 || counts["minecraft:vine"] != 64 ||
				counts["minecraft:lily_pad"] != 7 || counts["minecraft:blue_orchid"] != 5 {
				t.Fatalf("swamp 8675309 (-20,-332) mismatch: y62=(w=%d,l=%d) log=%d leaves=%d vine=%d lily=%d orchid=%d",
					y62Water, y62Land, counts["minecraft:oak_log"], counts["minecraft:oak_leaves"], counts["minecraft:vine"],
					counts["minecraft:lily_pad"], counts["minecraft:blue_orchid"])
			}
		case tc.seed == -135792468 && tc.cx == 384 && tc.cz == -124:
			if counts["minecraft:mycelium"] != 254 || counts["minecraft:mushroom_stem"] != 5 ||
				counts["minecraft:brown_mushroom_block"] != 45 || counts["minecraft:red_mushroom_block"] != 18 ||
				stitchedMushroomCap == 0 {
				t.Fatalf("mushroom_fields -135792468 (384,-124) mismatch: myc=%d stem=%d brownBlk=%d redBlk=%d stitched=%d",
					counts["minecraft:mycelium"], counts["minecraft:mushroom_stem"], counts["minecraft:brown_mushroom_block"],
					counts["minecraft:red_mushroom_block"], stitchedMushroomCap)
			}
		case tc.seed == -135792468 && tc.cx == -376 && tc.cz == -312:
			if wallCoralWL != 127 || seaPickleWL != 7 || counts["minecraft:seagrass"] != 26 ||
				counts["minecraft:tall_seagrass"] != 20 || coralBlocks != 220 || topCoralsAndFans != 27 {
				t.Fatalf("warm_ocean -135792468 (-376,-312) mismatch: wallWL=%d pickleWL=%d seagrass=%d tall=%d coralBlk=%d top=%d",
					wallCoralWL, seaPickleWL, counts["minecraft:seagrass"], counts["minecraft:tall_seagrass"], coralBlocks, topCoralsAndFans)
			}
		case tc.seed == strSeed && tc.cx == 108 && tc.cz == -396:
			if wallCoralWL != 30 || counts["minecraft:seagrass"] != 30 || counts["minecraft:tall_seagrass"] != 22 ||
				coralBlocks != 55 || topCoralsAndFans != 13 {
				t.Fatalf("warm_ocean strSeed (108,-396) mismatch: wallWL=%d seagrass=%d tall=%d coralBlk=%d top=%d",
					wallCoralWL, counts["minecraft:seagrass"], counts["minecraft:tall_seagrass"], coralBlocks, topCoralsAndFans)
			}
		case tc.seed == strSeed && tc.cx == 108 && tc.cz == -344:
			if counts["minecraft:seagrass"] != 11 || counts["minecraft:tall_seagrass"] != 112 ||
				counts["minecraft:kelp"] != 45 || counts["minecraft:kelp_plant"] != 293 || badKelpTipAge != 0 {
				t.Fatalf("deep_lukewarm_ocean strSeed (108,-344) mismatch: seagrass=%d tall=%d kelp=%d kelpPlant=%d badKelpAge=%d",
					counts["minecraft:seagrass"], counts["minecraft:tall_seagrass"], counts["minecraft:kelp"], counts["minecraft:kelp_plant"], badKelpTipAge)
			}
		case tc.seed == strSeed && tc.cx == 96 && tc.cz == -400:
			if counts["minecraft:seagrass"] != 55 || counts["minecraft:tall_seagrass"] != 48 ||
				counts["minecraft:kelp"] != 36 || counts["minecraft:kelp_plant"] != 187 {
				t.Fatalf("lukewarm_ocean strSeed (96,-400) mismatch: seagrass=%d tall=%d kelp=%d kelpPlant=%d",
					counts["minecraft:seagrass"], counts["minecraft:tall_seagrass"], counts["minecraft:kelp"], counts["minecraft:kelp_plant"])
			}
		case tc.seed == strSeed && tc.cx == 293 && tc.cz == -402:
			if counts["minecraft:bone_block"] != 61 || counts["minecraft:coal_ore"] != 78 ||
				y62Water != 220 || y62Land != 5 || counts["minecraft:oak_log"] != 10 ||
				counts["minecraft:oak_leaves"] != 222 || counts["minecraft:vine"] != 63 || counts["minecraft:lily_pad"] != 11 {
				t.Fatalf("swamp strSeed (293,-402) mismatch: bone=%d coal=%d y62=(w=%d,l=%d) log=%d leaves=%d vine=%d lily=%d",
					counts["minecraft:bone_block"], counts["minecraft:coal_ore"], y62Water, y62Land,
					counts["minecraft:oak_log"], counts["minecraft:oak_leaves"], counts["minecraft:vine"], counts["minecraft:lily_pad"])
			}
		}
	}
}
