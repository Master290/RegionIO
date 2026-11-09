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

func TestLargeDripstonePlacementScansCavernAndPlacesColumns(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	config, err := set.LargeDripstone("minecraft:large_dripstone")
	if err != nil {
		t.Fatal(err)
	}
	if config.FloorToCeilingSearchRange != 30 || config.ColumnRadiusMin != 3 || config.ColumnRadiusMax != 19 {
		t.Fatalf("unexpected large_dripstone config: %+v", config)
	}

	chunks := make([]*Chunk, 0, 9)
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			chunk := NewChunk(cx, cz, BiomePlains)
			for y := MinY; y < MinY+WorldHeight; y++ {
				state := StateStone
				if y >= 0 && y < 24 {
					state = StateAir
				}
				for x := 0; x < 16; x++ {
					for z := 0; z < 16; z++ {
						chunk.setBlockRaw(x, y, z, state)
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

	// Solid origin must return early without consuming RNG draws.
	rngA := worldgen.NewWorldgenRandom(99)
	rngB := worldgen.NewWorldgenRandom(99)
	if err := region.placeLargeDripstone(set, rngA, worldgen.FeaturePosition{X: 8, Y: -10, Z: 8}, config); err != nil {
		t.Fatal(err)
	}
	if got, want := rngA.NextInt(), rngB.NextInt(); got != want {
		t.Fatalf("solid origin consumed RNG draws: got %d, want %d", got, want)
	}

	// Open cavern origin (y=10 inside y=0..23 air pocket) must scan floor=-1, ceiling=24 and place dripstone_block.
	if err := region.placeLargeDripstone(set, worldgen.NewWorldgenRandom(12345), worldgen.FeaturePosition{X: 8, Y: 10, Z: 8}, config); err != nil {
		t.Fatal(err)
	}
	dripstoneState, ok := nameToStateID("minecraft:dripstone_block", nil)
	if !ok {
		t.Fatal("missing minecraft:dripstone_block state")
	}
	placed := 0
	center := region.chunks[[2]int32{0, 0}]
	for y := -5; y < 30; y++ {
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				if center.GetBlock(x, y, z) == dripstoneState {
					placed++
				}
			}
		}
	}
	if placed == 0 {
		t.Fatal("expected large_dripstone to place minecraft:dripstone_block in cavern")
	}
}

func TestGeodesOreVeinsAndBonusChestParitySeed12345(t *testing.T) {
	const seed int64 = 12345
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}

	// 1. Amethyst geode full layer & bud placement parity on seed 12345.
	geodeCfg, err := set.Geode("minecraft:amethyst_geode")
	if err != nil {
		t.Fatal(err)
	}
	geodeReg := buildFlatTestDecorationRegion(t, 0, 0, 1, 64, false)
	geodeReg.bindSeed(seed)
	if !geodeReg.placeGeode(worldgen.NewWorldgenRandom(seed), seed, worldgen.FeaturePosition{X: 8, Y: 20, Z: 8}, geodeCfg, set) {
		t.Fatal("placeGeode on seed 12345 returned false")
	}
	geodeCounts := make(map[string]int)
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			ch := geodeReg.chunks[[2]int32{cx, cz}]
			for y := 0; y <= 40; y++ {
				for x := 0; x < 16; x++ {
					for z := 0; z < 16; z++ {
						if st, ok := stateByID(ch.GetBlock(x, y, z)); ok && st.Name != "minecraft:stone" {
							geodeCounts[st.Name]++
						}
					}
				}
			}
		}
	}
	for _, wantBlock := range []string{
		"minecraft:smooth_basalt",
		"minecraft:calcite",
		"minecraft:amethyst_block",
		"minecraft:budding_amethyst",
	} {
		if geodeCounts[wantBlock] == 0 {
			t.Errorf("amethyst_geode on seed 12345 missing %s (counts=%v)", wantBlock, geodeCounts)
		}
	}
	totalBuds := geodeCounts["minecraft:small_amethyst_bud"] +
		geodeCounts["minecraft:medium_amethyst_bud"] +
		geodeCounts["minecraft:large_amethyst_bud"] +
		geodeCounts["minecraft:amethyst_cluster"]
	if totalBuds == 0 {
		t.Errorf("amethyst_geode on seed 12345 placed 0 amethyst buds/clusters on budding_amethyst (counts=%v)", geodeCounts)
	}

	// 2. OreVeinifier (copper/granite in y=0..50, iron/tuff in y=-60..-8) on seed 12345.
	od, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		t.Fatal(err)
	}
	veins := worldgen.NewOreVeinifier(od)
	copperCount, rawCopperCount, graniteCount := 0, 0, 0
	ironCount, rawIronCount, tuffCount := 0, 0, 0
	for x := -512; x <= 512; x += 8 {
		for z := -512; z <= 512; z += 8 {
			for y := -60; y <= 50; y += 2 {
				b, ok := veins.Calculate(worldgen.FunctionContext{X: float64(x), Y: float64(y), Z: float64(z)}, x, y, z)
				if !ok {
					continue
				}
				switch b {
				case StateCopperOre:
					if y < 0 || y > 50 {
						t.Fatalf("copper ore at y=%d outside [0..50]", y)
					}
					copperCount++
				case StateRawCopperBlock:
					if y < 0 || y > 50 {
						t.Fatalf("raw copper block at y=%d outside [0..50]", y)
					}
					rawCopperCount++
				case StateGranite:
					if y < 0 || y > 50 {
						t.Fatalf("copper vein granite at y=%d outside [0..50]", y)
					}
					graniteCount++
				case StateDeepslateIronOre:
					if y < -60 || y > -8 {
						t.Fatalf("deepslate iron ore at y=%d outside [-60..-8]", y)
					}
					ironCount++
				case StateRawIronBlock:
					if y < -60 || y > -8 {
						t.Fatalf("raw iron block at y=%d outside [-60..-8]", y)
					}
					rawIronCount++
				case StateTuff:
					if y < -60 || y > -8 {
						t.Fatalf("iron vein tuff at y=%d outside [-60..-8]", y)
					}
					tuffCount++
				}
			}
		}
	}
	if copperCount == 0 || graniteCount == 0 || ironCount == 0 || tuffCount == 0 || rawCopperCount+rawIronCount == 0 {
		t.Fatalf("unexpected OreVeinifier counts on seed 12345: copper=%d rawCopper=%d granite=%d iron=%d rawIron=%d tuff=%d",
			copperCount, rawCopperCount, graniteCount, ironCount, rawIronCount, tuffCount)
	}

	// 3. BonusChestFeature (minecraft:bonus_chest) with spawn_bonus_chest loot and torches.
	bonusReg := buildFlatTestDecorationRegion(t, 0, 0, 1, 64, false)
	bonusReg.bindSeed(seed)
	if !bonusReg.placeBonusChest(worldgen.NewWorldgenRandom(seed), worldgen.FeaturePosition{X: 8, Y: 65, Z: 8}) {
		t.Fatal("placeBonusChest returned false on flat region")
	}
	bes := collectRegionBlockEntities(bonusReg)
	foundBonusChestBE := false
	var chestX, chestY, chestZ int
	for _, be := range bes {
		if be.ID == "minecraft:chest" && be.LootTable == "minecraft:chests/spawn_bonus_chest" && be.LootTableSeed != 0 && len(be.Items) > 0 {
			foundBonusChestBE = true
			chestX, chestY, chestZ = be.X, be.Y, be.Z
			break
		}
	}
	if !foundBonusChestBE {
		t.Fatalf("expected bonus chest block entity with minecraft:chests/spawn_bonus_chest and non-empty items, got %+v", bes)
	}
	torchCount := 0
	for _, d := range [4][2]int{{0, -1}, {1, 0}, {0, 1}, {-1, 0}} {
		if st, ok := stateByID(bonusReg.getBlock(chestX+d[0], chestY, chestZ+d[1])); ok && st.Name == "minecraft:torch" {
			torchCount++
		}
	}
	if torchCount != 4 {
		t.Fatalf("expected 4 torches around bonus chest at (%d,%d,%d), got %d", chestX, chestY, chestZ, torchCount)
	}
}

func TestMultiBiomeChunkGenerationAndDecorationParitySeed12345(t *testing.T) {
	const seed int64 = 12345
	od, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		t.Fatal(err)
	}
	picker := worldgen.OverworldFluidPicker(od.SeaLevel)
	veins := worldgen.NewOreVeinifier(od)
	carver, err := worldgen.NewCarver(od, seed)
	if err != nil {
		t.Fatal(err)
	}
	initCarverReplaceable(carver.ReplaceableBlocks())

	type biomeCase struct {
		biome     string
		cx, cz    int32
		sampleY   int
		wantNames []string
	}
	cases := []biomeCase{
		{
			biome:     "minecraft:forest",
			cx:        -16,
			cz:        16,
			sampleY:   SeaLevel + 4,
			wantNames: []string{"minecraft:grass_block", "minecraft:oak_log", "minecraft:oak_leaves", "minecraft:birch_log", "minecraft:birch_leaves"},
		},
		{
			biome:     "minecraft:desert",
			cx:        -140,
			cz:        28,
			sampleY:   SeaLevel + 4,
			wantNames: []string{"minecraft:sand", "minecraft:sandstone"},
		},
		{
			biome:     "minecraft:lush_caves",
			cx:        0,
			cz:        0,
			sampleY:   0,
			wantNames: []string{"minecraft:moss_block", "minecraft:clay", "minecraft:cave_vines", "minecraft:azalea"},
		},
		{
			biome:     "minecraft:dripstone_caves",
			cx:        34,
			cz:        -34,
			sampleY:   0,
			wantNames: []string{"minecraft:dripstone_block", "minecraft:pointed_dripstone"},
		},
		{
			biome:     "minecraft:deep_dark",
			cx:        4,
			cz:        -4,
			sampleY:   -40,
			wantNames: []string{"minecraft:sculk", "minecraft:sculk_vein", "minecraft:sculk_catalyst", "minecraft:sculk_shrieker", "minecraft:sculk_sensor"},
		},
		{
			biome:     "minecraft:mangrove_swamp",
			cx:        -102,
			cz:        -92,
			sampleY:   SeaLevel + 2,
			wantNames: []string{"minecraft:mud", "minecraft:mangrove_log", "minecraft:mangrove_roots", "minecraft:mangrove_leaves"},
		},
		{
			biome:     "minecraft:cherry_grove",
			cx:        -26,
			cz:        48,
			sampleY:   100,
			wantNames: []string{"minecraft:grass_block", "minecraft:cherry_log", "minecraft:cherry_leaves", "minecraft:pink_petals"},
		},
	}

	buildAndDecorate := func(centerX, centerZ int32) *decorationRegion {
		chunks := make([]*Chunk, 0, 9)
		for cx := centerX - 1; cx <= centerX+1; cx++ {
			for cz := centerZ - 1; cz <= centerZ+1; cz++ {
				chunks = append(chunks, generateVanillaWithoutDecoration(od, picker, veins, carver, seed, cx, cz))
			}
		}
		reg, err := newDecorationRegion(chunks)
		if err != nil {
			t.Fatal(err)
		}
		reg.bindSeed(seed)
		if err := reg.setSource(centerX, centerZ); err != nil {
			t.Fatal(err)
		}
		if err := reg.placeScheduledLakes(seed); err != nil {
			t.Fatal(err)
		}
		if err := reg.placeScheduledGeodes(seed); err != nil {
			t.Fatal(err)
		}
		if err := reg.placeScheduledMonsterRooms(seed); err != nil {
			t.Fatal(err)
		}
		if err := reg.placeScheduledSurfaceStructures(seed); err != nil {
			t.Fatal(err)
		}
		if err := reg.placeScheduledUndergroundOresStage(seed); err != nil {
			t.Fatal(err)
		}
		if err := reg.placeScheduledUndergroundDecoration(seed); err != nil {
			t.Fatal(err)
		}
		if err := reg.placeScheduledSprings(seed); err != nil {
			t.Fatal(err)
		}
		if err := reg.placeScheduledVegetationPatches(seed); err != nil {
			t.Fatal(err)
		}
		if err := reg.placeScheduledFreezeTopLayer(seed); err != nil {
			t.Fatal(err)
		}
		return reg
	}

	ResetNotReplayed()
	for _, tc := range cases {
		t.Run(tc.biome, func(t *testing.T) {
			cx, cz := tc.cx, tc.cz
			wx, wz := int(cx)*16+8, int(cz)*16+8
			s2D := worldgen.SampleColumn2D(od, tc.sampleY, wx, wz)
			if got := biomeNameByID(BiomeAt3D(od, s2D, wx, tc.sampleY, wz)); got != tc.biome {
				t.Fatalf("expected biome %s at (%d,%d,%d), got %s", tc.biome, wx, tc.sampleY, wz, got)
			}
			regA := buildAndDecorate(cx, cz)
			regB := buildAndDecorate(cx, cz)

			seenNames := make(map[string]int)
			for ncx := cx - 1; ncx <= cx+1; ncx++ {
				for ncz := cz - 1; ncz <= cz+1; ncz++ {
					chA := regA.chunks[[2]int32{ncx, ncz}]
					chB := regB.chunks[[2]int32{ncx, ncz}]
					for y := MinY; y < MinY+WorldHeight; y++ {
						for x := 0; x < 16; x++ {
							for z := 0; z < 16; z++ {
								idA := chA.GetBlock(x, y, z)
								idB := chB.GetBlock(x, y, z)
								if idA != idB {
									t.Fatalf("biome %s chunk (%d,%d) non-deterministic at (%d,%d,%d): %d != %d",
										tc.biome, ncx, ncz, x, y, z, idA, idB)
								}
								if st, ok := stateByID(idA); ok {
									seenNames[st.Name]++
								}
							}
						}
					}
				}
			}

			matched := 0
			for _, want := range tc.wantNames {
				if seenNames[want] > 0 {
					matched++
				}
			}
			if matched == 0 {
				t.Fatalf("biome %s at chunk (%d,%d) placed none of expected characteristic blocks %v",
					tc.biome, cx, cz, tc.wantNames)
			}
			t.Logf("biome %s at chunk (%d,%d): verified 100%% determinism and %d/%d characteristic block families",
				tc.biome, cx, cz, matched, len(tc.wantNames))
		})
	}
	if nr := NotReplayed(); len(nr) > 0 {
		t.Fatalf("unexpected NotReplayed features across multi-biome decoration: %v", nr)
	}
}

