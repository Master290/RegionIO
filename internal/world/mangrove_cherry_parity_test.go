package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func TestMangroveUpwardsBranchingTrunkPlacer(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}
	cfg, err := set.Tree("minecraft:mangrove")
	if err != nil {
		t.Fatalf("set.Tree(mangrove): %v", err)
	}
	growThroughList, err := parseBlockListOrTag(cfg.TrunkPlacer.Fields["can_grow_through"])
	if err != nil {
		t.Fatalf("parseBlockListOrTag(can_grow_through): %v", err)
	}

	c := NewChunk(0, 0, BiomePlains)
	region := &decorationRegion{
		chunks:  map[[2]int32]*Chunk{{0, 0}: c},
		sourceX: 0,
		sourceZ: 0,
	}
	planter := &treePlacer{
		r:           region,
		set:         set,
		random:      worldgen.NewXoroshiro(8675309),
		config:      cfg,
		trunkHeight: 6,
	}

	// In mangrove.json and tall_mangrove.json, extra_branch_length is uniform(0, 1),
	// so branchPos = max(0, branchLen - extraBranchLength - 1) is always 0.
	// With branchPos = 0 and branchSteps = 3, vanilla UpwardsBranchingTrunkPlacer.placeBranch
	// skips j=0 (steps=3 -> 2), then places branch logs at j=1 (dx=1, dy=1) and j=2 (dx=2, dy=2),
	// adding per-step foliage attachments at (x+1, cy+1, z) and (x+2, cy+2, z), plus two final
	// attachments at (x+2, cy+3, z) and (x+2, cy+1, z) because yPos - cy = 3 > 1.
	var attachments []trunkAttachment
	if err := planter.placeUpwardsBranch(8, 64, 8, [2]int{1, 0}, 0, 3, growThroughList, &attachments); err != nil {
		t.Fatalf("placeUpwardsBranch: %v", err)
	}

	log1, ok1 := stateByID(region.getBlock(9, 65, 8))
	log2, ok2 := stateByID(region.getBlock(10, 66, 8))
	if !ok1 || log1.Name != "minecraft:mangrove_log" {
		t.Fatalf("expected branch log at (9,65,8), got %+v", log1)
	}
	if !ok2 || log2.Name != "minecraft:mangrove_log" {
		t.Fatalf("expected branch log at (10,66,8), got %+v", log2)
	}

	wantAttachments := []trunkAttachment{
		{x: 9, y: 65, z: 8},
		{x: 10, y: 66, z: 8},
		{x: 10, y: 67, z: 8},
		{x: 10, y: 65, z: 8},
	}
	if len(attachments) != len(wantAttachments) {
		t.Fatalf("attachments count = %d (%+v), want %d (%+v)", len(attachments), attachments, len(wantAttachments), wantAttachments)
	}
	for i, want := range wantAttachments {
		if attachments[i] != want {
			t.Fatalf("attachments[%d] = %+v, want %+v", i, attachments[i], want)
		}
	}
}

func TestMangroveTrunkIsFreeCanGrowThrough(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}
	cfg, err := set.Tree("minecraft:mangrove")
	if err != nil {
		t.Fatalf("set.Tree(mangrove): %v", err)
	}

	c := NewChunk(0, 0, BiomePlains)
	region := &decorationRegion{
		chunks:  map[[2]int32]*Chunk{{0, 0}: c},
		sourceX: 0,
		sourceZ: 0,
	}
	planter := &treePlacer{
		r:           region,
		set:         set,
		random:      worldgen.NewXoroshiro(-4242424242),
		config:      cfg,
		trunkHeight: 5,
	}

	// Place blocks from #minecraft:mangrove_logs_can_grow_through in the trunk column:
	// mud, muddy_mangrove_roots, mangrove_roots, moss_carpet, mangrove_propagule.
	growThroughBlocks := []string{
		"minecraft:mud",
		"minecraft:muddy_mangrove_roots",
		"minecraft:mangrove_roots",
		"minecraft:moss_carpet",
		"minecraft:mangrove_propagule",
	}
	for i, name := range growThroughBlocks {
		id, ok := nameToStateID(name, nil)
		if !ok {
			t.Fatalf("missing block state for %s", name)
		}
		region.setBlock(8, 64+i, 8, id)
		if !planter.isFree(8, 64+i, 8) {
			t.Fatalf("expected upwards_branching_trunk_placer.isFree to accept %s in #minecraft:mangrove_logs_can_grow_through", name)
		}
	}
	if got := planter.maxFreeTreeHeight(worldgen.FeaturePosition{X: 8, Y: 64, Z: 8}); got != 5 {
		t.Fatalf("maxFreeTreeHeight through #minecraft:mangrove_logs_can_grow_through = %d, want 5", got)
	}
}

func TestMangrovePropaguleWouldSurvive(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}
	c := NewChunk(0, 0, BiomePlains)
	region := &decorationRegion{
		chunks:  map[[2]int32]*Chunk{{0, 0}: c},
		sourceX: 0,
		sourceZ: 0,
	}

	clayID, _ := nameToStateID("minecraft:clay", nil)
	mudID, _ := nameToStateID("minecraft:mud", nil)
	stoneID, _ := nameToStateID("minecraft:stone", nil)
	mLeavesID, _ := nameToStateID("minecraft:mangrove_leaves", nil)

	// 1. Floor survival: MangrovePropaguleBlock.mayPlaceOn checks #minecraft:supports_mangrove_propagule
	// (#minecraft:supports_vegetation + minecraft:clay).
	region.setBlock(4, 63, 4, clayID)
	region.setBlock(5, 63, 4, mudID)
	region.setBlock(6, 63, 4, stoneID)

	floorPred := []byte(`{"type":"minecraft:would_survive","state":{"Name":"minecraft:mangrove_propagule","Properties":{"age":"0","hanging":"false","stage":"0","waterlogged":"false"}}}`)
	if ok, err := region.testBlockPredicate(set, floorPred, worldgen.FeaturePosition{X: 4, Y: 64, Z: 4}); err != nil || !ok {
		t.Fatalf("expected mangrove_propagule to survive on minecraft:clay, got ok=%v err=%v", ok, err)
	}
	if ok, err := region.testBlockPredicate(set, floorPred, worldgen.FeaturePosition{X: 5, Y: 64, Z: 4}); err != nil || !ok {
		t.Fatalf("expected mangrove_propagule to survive on minecraft:mud, got ok=%v err=%v", ok, err)
	}
	if ok, err := region.testBlockPredicate(set, floorPred, worldgen.FeaturePosition{X: 6, Y: 64, Z: 4}); err != nil || ok {
		t.Fatalf("expected mangrove_propagule NOT to survive on minecraft:stone, got ok=%v err=%v", ok, err)
	}

	// 2. Hanging survival: MangrovePropaguleBlock.canSurvive with hanging=true checks
	// #minecraft:supports_hanging_mangrove_propagule (minecraft:mangrove_leaves) at pos.above().
	region.setBlock(4, 70, 4, mLeavesID)
	hangingPred := []byte(`{"type":"minecraft:would_survive","state":{"Name":"minecraft:mangrove_propagule","Properties":{"age":"0","hanging":"true","stage":"0","waterlogged":"false"}}}`)
	if ok, err := region.testBlockPredicate(set, hangingPred, worldgen.FeaturePosition{X: 4, Y: 69, Z: 4}); err != nil || !ok {
		t.Fatalf("expected hanging mangrove_propagule to survive below minecraft:mangrove_leaves, got ok=%v err=%v", ok, err)
	}
	if ok, err := region.testBlockPredicate(set, hangingPred, worldgen.FeaturePosition{X: 5, Y: 69, Z: 4}); err != nil || ok {
		t.Fatalf("expected hanging mangrove_propagule NOT to survive below air, got ok=%v err=%v", ok, err)
	}
}

func TestCherryBeesBlockEntityAndNBTRoundTrip(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}
	cfg, err := set.Tree("minecraft:cherry_bees_005")
	if err != nil {
		t.Fatalf("set.Tree(cherry_bees_005): %v", err)
	}
	grassID, _ := nameToStateID("minecraft:grass_block", map[string]string{"snowy": "false"})

	var hiveChunk *Chunk
	var hiveBE StructureBlockEntity
	foundHive := false
	for attempt := int64(0); attempt < 200 && !foundHive; attempt++ {
		reg := buildFlatTestDecorationRegion(t, 0, 0, 1, 64, false)
		reg.bindSeed(8675309 + attempt)
		for x := -8; x < 24; x++ {
			for z := -8; z < 24; z++ {
				reg.setBlockGlobal(x, 64, z, grassID)
			}
		}
		rng := worldgen.NewXoroshiro(8675309 + attempt*31)
		placed, err := reg.placeTreeWithResult(set, rng, worldgen.FeaturePosition{X: 8, Y: 65, Z: 8}, cfg)
		if err != nil {
			t.Fatalf("placeTreeWithResult(cherry_bees_005): %v", err)
		}
		if !placed {
			continue
		}
		for _, ch := range reg.chunks {
			for _, be := range ch.StructureBlockEntities() {
				if be.ID == "minecraft:beehive" {
					hiveChunk = ch
					hiveBE = be
					foundHive = true
					break
				}
			}
		}
	}
	if !foundHive {
		t.Fatal("expected cherry_bees_005 to place a bee_nest with minecraft:beehive block entity within 200 attempts")
	}
	if len(hiveBE.Bees) < 2 || len(hiveBE.Bees) > 3 {
		t.Fatalf("beehive occupant count = %d, want 2..3 (vanilla 2 + nextInt(2))", len(hiveBE.Bees))
	}
	for i, occ := range hiveBE.Bees {
		if occ.EntityID != "minecraft:bee" {
			t.Fatalf("bee[%d].EntityID = %q, want minecraft:bee", i, occ.EntityID)
		}
		if occ.TicksInHive < 0 || occ.TicksInHive >= 599 {
			t.Fatalf("bee[%d].TicksInHive = %d, want 0..598", i, occ.TicksInHive)
		}
		if occ.MinTicksInHive != 600 {
			t.Fatalf("bee[%d].MinTicksInHive = %d, want 600", i, occ.MinTicksInHive)
		}
	}

	// Verify Anvil NBT round-trip preserves the "bees" list on the beehive block entity.
	root := chunkToNBT(hiveChunk)
	loaded, err := nbtToChunk(root, int(hiveChunk.X), int(hiveChunk.Z), 0, 0)
	if err != nil {
		t.Fatalf("nbtToChunk: %v", err)
	}
	loadedBE, ok := loaded.StructureBlockEntityAt(hiveBE.X, hiveBE.Y, hiveBE.Z)
	if !ok {
		t.Fatalf("missing round-tripped beehive block entity at (%d,%d,%d)", hiveBE.X, hiveBE.Y, hiveBE.Z)
	}
	if len(loadedBE.Bees) != len(hiveBE.Bees) {
		t.Fatalf("round-tripped bees count = %d, want %d", len(loadedBE.Bees), len(hiveBE.Bees))
	}
	for i := range hiveBE.Bees {
		if loadedBE.Bees[i] != hiveBE.Bees[i] {
			t.Fatalf("round-tripped bee[%d] = %+v, want %+v", i, loadedBE.Bees[i], hiveBE.Bees[i])
		}
	}
}

func decorate3x3AroundCenter(t *testing.T, od *worldgen.OverworldDensity, seed int64, centerX, centerZ int32) *decorationRegion {
	t.Helper()
	picker := worldgen.OverworldFluidPicker(od.SeaLevel)
	veins := worldgen.NewOreVeinifier(od)
	carver, err := worldgen.NewCarver(od, seed)
	if err != nil {
		t.Fatalf("NewCarver: %v", err)
	}
	initCarverReplaceable(carver.ReplaceableBlocks())

	chunks := make([]*Chunk, 0, 25)
	for cx := centerX - 2; cx <= centerX+2; cx++ {
		for cz := centerZ - 2; cz <= centerZ+2; cz++ {
			chunks = append(chunks, generateVanillaWithoutDecoration(od, picker, veins, carver, seed, cx, cz))
		}
	}
	reg, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatalf("newDecorationRegion: %v", err)
	}
	reg.bindSeed(seed)
	stages := []func(int64) error{
		reg.placeScheduledLakes,
		reg.placeScheduledGeodes,
		reg.placeScheduledMonsterRooms,
		reg.placeScheduledSurfaceStructures,
		reg.placeScheduledUndergroundOresStage,
		reg.placeScheduledUndergroundDecoration,
		reg.placeScheduledSprings,
		reg.placeScheduledVegetationPatches,
		reg.placeScheduledFreezeTopLayer,
	}
	for _, stage := range stages {
		for sx := centerX - 1; sx <= centerX+1; sx++ {
			for sz := centerZ - 1; sz <= centerZ+1; sz++ {
				if err := reg.setSource(sx, sz); err != nil {
					t.Fatalf("setSource(%d,%d): %v", sx, sz, err)
				}
				if err := stage(seed); err != nil {
					t.Fatalf("stage at (%d,%d): %v", sx, sz, err)
				}
			}
		}
	}
	return reg
}

func TestMangroveSwampAndCherryGroveNewSeedsParity(t *testing.T) {
	strSeed, ok := ParseWorldSeed("mangrove-cherry")
	if !ok || strSeed == 0 {
		t.Fatalf("ParseWorldSeed(\"mangrove-cherry\") = (%d, %v), want non-zero", strSeed, ok)
	}

	seeds := []struct {
		name string
		seed int64
	}{
		{name: "positive_8675309", seed: 8675309},
		{name: "negative_-4242424242", seed: -4242424242},
		{name: "string_mangrove-cherry", seed: strSeed},
	}

	totalMangroveLogs := 0
	totalMangroveLeaves := 0
	totalMangroveRoots := 0
	totalMuddyRoots := 0
	totalWaterloggedRoots := 0
	totalDryRoots := 0
	totalMossCarpet := 0
	totalHangingPropagules := 0
	propaguleAges := make(map[string]int)
	totalMud := 0
	totalSeagrass := 0
	totalLilyPad := 0

	totalCherryLogs := 0
	totalCherryBranchAxes := 0
	totalCherryLeaves := 0
	totalPinkPetals := 0
	petalFacings := make(map[string]int)
	petalAmounts := make(map[string]int)

	for _, sc := range seeds {
		t.Run(sc.name, func(t *testing.T) {
			owOD, err := worldgen.LoadOverworldFinalDensity(sc.seed)
			if err != nil {
				t.Fatalf("LoadOverworldFinalDensity(%d): %v", sc.seed, err)
			}

			// 1. Locate and decorate 3x3 chunks around minecraft:mangrove_swamp.
			mx, _, mz, mName, mOK := LocateNearestBiome(owOD, nil, nil, "minecraft:mangrove_swamp", 0, 64, 0, 12800, 64, 64)
			if !mOK || mName != "minecraft:mangrove_swamp" {
				t.Fatalf("seed %d: failed to locate minecraft:mangrove_swamp", sc.seed)
			}
			mcx, mcz := int32(mx>>4), int32(mz>>4)
			mReg := decorate3x3AroundCenter(t, owOD, sc.seed, mcx, mcz)
			seedMangroveLogs := 0
			seedMud := 0
			for dz := int32(-1); dz <= 1; dz++ {
				for dx := int32(-1); dx <= 1; dx++ {
					ch := mReg.chunks[[2]int32{mcx + dx, mcz + dz}]
					for y := MinY; y < MinY+WorldHeight; y++ {
						for lz := 0; lz < 16; lz++ {
							for lx := 0; lx < 16; lx++ {
								st, ok := stateByID(ch.GetBlock(lx, y, lz))
								if !ok {
									continue
								}
								switch st.Name {
								case "minecraft:mangrove_log":
									seedMangroveLogs++
									totalMangroveLogs++
								case "minecraft:mangrove_leaves":
									totalMangroveLeaves++
								case "minecraft:mangrove_roots":
									totalMangroveRoots++
									if st.Properties["waterlogged"] == "true" {
										totalWaterloggedRoots++
									} else {
										totalDryRoots++
									}
								case "minecraft:muddy_mangrove_roots":
									totalMuddyRoots++
								case "minecraft:moss_carpet":
									totalMossCarpet++
								case "minecraft:mangrove_propagule":
									if st.Properties["hanging"] == "true" {
										totalHangingPropagules++
										propaguleAges[st.Properties["age"]]++
									}
								case "minecraft:mud":
									seedMud++
									totalMud++
								case "minecraft:seagrass", "minecraft:tall_seagrass":
									totalSeagrass++
								case "minecraft:lily_pad":
									totalLilyPad++
								}
							}
						}
					}
				}
			}
			if seedMud == 0 {
				t.Fatalf("seed %d: expected mud in mangrove_swamp 3x3 around chunk (%d,%d)", sc.seed, mcx, mcz)
			}
			if seedMangroveLogs == 0 {
				t.Fatalf("seed %d: expected mangrove trees in mangrove_swamp 3x3 around chunk (%d,%d)", sc.seed, mcx, mcz)
			}
			t.Logf("seed %d mangrove chunk (%d,%d): logs=%d mud=%d roots=%d(wet=%d,dry=%d) muddy=%d seagrass=%d",
				sc.seed, mcx, mcz, seedMangroveLogs, seedMud, totalMangroveRoots, totalWaterloggedRoots, totalDryRoots, totalMuddyRoots, totalSeagrass)

			// 2. Locate and decorate 3x3 chunks around minecraft:cherry_grove.
			cx, _, cz, cName, cOK := LocateNearestBiome(owOD, nil, nil, "minecraft:cherry_grove", 0, 100, 0, 12800, 64, 64)
			if !cOK || cName != "minecraft:cherry_grove" {
				t.Fatalf("seed %d: failed to locate minecraft:cherry_grove", sc.seed)
			}
			ccx, ccz := int32(cx>>4), int32(cz>>4)
			cReg := decorate3x3AroundCenter(t, owOD, sc.seed, ccx, ccz)
			seedCherryLeavesOrPetals := 0
			for dz := int32(-1); dz <= 1; dz++ {
				for dx := int32(-1); dx <= 1; dx++ {
					ch := cReg.chunks[[2]int32{ccx + dx, ccz + dz}]
					for y := MinY; y < MinY+WorldHeight; y++ {
						for lz := 0; lz < 16; lz++ {
							for lx := 0; lx < 16; lx++ {
								st, ok := stateByID(ch.GetBlock(lx, y, lz))
								if !ok {
									continue
								}
								switch st.Name {
								case "minecraft:cherry_log":
									totalCherryLogs++
									if st.Properties["axis"] == "x" || st.Properties["axis"] == "z" {
										totalCherryBranchAxes++
									}
								case "minecraft:cherry_leaves":
									totalCherryLeaves++
									seedCherryLeavesOrPetals++
								case "minecraft:pink_petals":
									totalPinkPetals++
									seedCherryLeavesOrPetals++
									petalFacings[st.Properties["facing"]]++
									petalAmounts[st.Properties["flower_amount"]]++
								}
							}
						}
					}
				}
			}
			if seedCherryLeavesOrPetals == 0 {
				t.Fatalf("seed %d: expected cherry_leaves or pink_petals in cherry_grove 3x3 around chunk (%d,%d)", sc.seed, ccx, ccz)
			}
		})
	}

	if totalMangroveLogs == 0 || totalMangroveLeaves == 0 {
		t.Fatalf("expected mangrove logs and leaves across 3 seeds, got logs=%d leaves=%d", totalMangroveLogs, totalMangroveLeaves)
	}
	if totalMangroveRoots == 0 || totalMuddyRoots == 0 {
		t.Fatalf("expected both mangrove_roots (%d) and muddy_mangrove_roots (%d)", totalMangroveRoots, totalMuddyRoots)
	}
	if totalWaterloggedRoots == 0 || totalDryRoots == 0 {
		t.Fatalf("expected both waterlogged (%d) and dry (%d) mangrove_roots", totalWaterloggedRoots, totalDryRoots)
	}
	if totalMossCarpet == 0 {
		t.Fatalf("expected moss_carpet above mangrove roots, got 0")
	}
	if totalHangingPropagules == 0 || len(propaguleAges) < 3 {
		t.Fatalf("expected hanging mangrove_propagules with varied ages 0..4, got total=%d ages=%v", totalHangingPropagules, propaguleAges)
	}
	if totalSeagrass == 0 {
		t.Fatalf("expected aquatic seagrass in mangrove_swamp, got 0")
	}
	if totalCherryLogs == 0 || totalCherryBranchAxes == 0 || totalCherryLeaves == 0 {
		t.Fatalf("expected cherry logs (%d), horizontal branch logs (%d), and cherry leaves (%d)",
			totalCherryLogs, totalCherryBranchAxes, totalCherryLeaves)
	}
	if totalPinkPetals == 0 || len(petalFacings) < 4 || len(petalAmounts) < 4 {
		t.Fatalf("expected pink_petals with all 4 facings and flower_amount 1..4, got total=%d facings=%v amounts=%v",
			totalPinkPetals, petalFacings, petalAmounts)
	}
}
