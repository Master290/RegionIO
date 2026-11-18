package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func mustTestStateID(t *testing.T, name string, props map[string]string) uint16 {
	t.Helper()
	id, ok := nameToStateID(name, props)
	if !ok {
		t.Fatalf("nameToStateID(%q, %v) failed", name, props)
	}
	return id
}

func TestCaveBiomeMechanicsParity(t *testing.T) {
	deepslateID := mustTestStateID(t, "minecraft:deepslate", map[string]string{"axis": "y"})

	t.Run("JigsawFeaturePoolSculkPatchAncientCity", func(t *testing.T) {
		ResetNotReplayed()
		reg := buildFlatTestDecorationRegion(t, 0, 0, 1, 20, false)
		reg.bindSeed(42024202)
		for x := 4; x <= 12; x++ {
			for z := 4; z <= 12; z++ {
				for y := 16; y <= 23; y++ {
					st := deepslateID
					if x >= 5 && x <= 11 && z >= 5 && z <= 11 && y >= 19 && y <= 22 {
						st = StateAir
					}
					reg.setBlockGlobal(x, y, z, st)
				}
			}
		}
		reg.sourceX, reg.sourceZ = 50, 50

		piece := &JigsawPiece{
			Element: worldgen.PoolElementDef{
				ElementType: "minecraft:feature_pool_element",
				Feature:     "minecraft:sculk_patch_ancient_city",
				Projection:  "rigid",
			},
			Pos: [3]int{8, 19, 8},
			Box: msBox{minX: 7, minY: 18, minZ: 7, maxX: 9, maxY: 20, maxZ: 9},
		}
		start := &JigsawStart{
			StructureName: "minecraft:ancient_city",
			Step:          "underground_decoration",
			ChunkX:        0,
			ChunkZ:        0,
			Box:           msBox{minX: 0, minY: 16, minZ: 0, maxX: 15, maxY: 24, maxZ: 15},
			Pieces:        []*JigsawPiece{piece},
		}

		PlaceJigsawStartsForChunk(reg, []*JigsawStart{start}, "minecraft:ancient_city", 7, 42024202, 0, 0)

		if reg.sourceX != 50 || reg.sourceZ != 50 {
			t.Fatalf("source chunk = (%d,%d), want restored (50,50)", reg.sourceX, reg.sourceZ)
		}
		if nr := NotReplayed(); len(nr) != 0 {
			t.Fatalf("NotReplayed non-empty: %v", nr)
		}
		chunk00 := reg.chunks[[2]int32{0, 0}]
		sculkCount := 0
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				for y := 15; y <= 25; y++ {
					id := chunk00.GetBlock(x, y, z)
					st, ok := stateByID(id)
					if !ok {
						continue
					}
					switch st.Name {
					case "minecraft:sculk":
						sculkCount++
					case "minecraft:sculk_catalyst", "minecraft:sculk_sensor", "minecraft:sculk_shrieker":
						be, ok := chunk00.StructureBlockEntityAt(x, y, z)
						if !ok || be.ID != st.Name {
							t.Errorf("missing/mismatched BE at (%d,%d,%d) for %s: got %+v ok=%v", x, y, z, st.Name, be, ok)
						}
					}
				}
			}
		}
		if sculkCount == 0 {
			t.Fatalf("expected sculk blocks placed by feature_pool_element sculk_patch_ancient_city, got 0")
		}
	})

	t.Run("AncientCityDeferredToStep7", func(t *testing.T) {
		reg := buildFlatTestDecorationRegion(t, 0, 0, 1, 20, false)
		reg.bindSeed(42024202)
		reg.deferAncientCities = true
		for x := 4; x <= 12; x++ {
			for z := 4; z <= 12; z++ {
				for y := 16; y <= 23; y++ {
					st := deepslateID
					if x >= 5 && x <= 11 && z >= 5 && z <= 11 && y >= 19 && y <= 22 {
						st = StateAir
					}
					reg.setBlockGlobal(x, y, z, st)
				}
			}
		}

		piece := &JigsawPiece{
			Element: worldgen.PoolElementDef{
				ElementType: "minecraft:feature_pool_element",
				Feature:     "minecraft:sculk_patch_ancient_city",
				Projection:  "rigid",
			},
			Pos: [3]int{8, 19, 8},
			Box: msBox{minX: 7, minY: 18, minZ: 7, maxX: 9, maxY: 20, maxZ: 9},
		}
		start := &JigsawStart{
			StructureName: "minecraft:ancient_city",
			Step:          "underground_decoration",
			ChunkX:        0,
			ChunkZ:        0,
			Box:           msBox{minX: 0, minY: 16, minZ: 0, maxX: 15, maxY: 24, maxZ: 15},
			Pieces:        []*JigsawPiece{piece},
		}
		reg.pendingAncientCities = append(reg.pendingAncientCities, start)

		reg.placePendingAncientCitiesForChunk(42024202, 0, 0)
		if !reg.placedAncientCityChunks[[2]int32{0, 0}] {
			t.Fatalf("expected placedAncientCityChunks[(0,0)] = true")
		}
		chunk00 := reg.chunks[[2]int32{0, 0}]
		sculkCount := 0
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				for y := 15; y <= 25; y++ {
					if st, ok := stateByID(chunk00.GetBlock(x, y, z)); ok && st.Name == "minecraft:sculk" {
						sculkCount++
					}
				}
			}
		}
		if sculkCount == 0 {
			t.Fatalf("expected sculk blocks placed during deferred step-7 placement")
		}

		// Idempotent re-invocation on the same chunk must not overwrite later blocks.
		reg.setBlockGlobal(8, 18, 8, StateStone)
		reg.placePendingAncientCitiesForChunk(42024202, 0, 0)
		if got := reg.getBlock(8, 18, 8); got != StateStone {
			t.Fatalf("(8,18,8) = %d, want preserved StateStone %d on duplicate chunk call", got, StateStone)
		}

		reg.flushRemainingPendingAncientCities(42024202)
		if len(reg.pendingAncientCities) != 0 {
			t.Fatalf("pendingAncientCities len = %d after flush, want 0", len(reg.pendingAncientCities))
		}
		if !reg.placedAncientCityChunks[[2]int32{1, 0}] {
			t.Fatalf("expected placedAncientCityChunks[(1,0)] = true after flush")
		}
	})

	t.Run("SculkCollisionFullBlockAndMultifaceFluids", func(t *testing.T) {
		set, err := worldgen.LoadFeatureSet()
		if err != nil {
			t.Fatalf("LoadFeatureSet: %v", err)
		}
		fullBlocks := []uint16{
			deepslateID,
			mustTestStateID(t, "minecraft:deepslate_bricks", nil),
			mustTestStateID(t, "minecraft:glass", nil),
			mustTestStateID(t, "minecraft:oak_leaves", map[string]string{
				"distance": "7", "persistent": "false", "waterlogged": "false",
			}),
		}
		for _, id := range fullBlocks {
			if !isSculkCollisionFullBlock(id) {
				st, _ := stateByID(id)
				t.Errorf("isSculkCollisionFullBlock(%s) = false, want true", st.Name)
			}
		}
		nonFullBlocks := []uint16{
			mustTestStateID(t, "minecraft:soul_sand", nil),
			mustTestStateID(t, "minecraft:mud", nil),
			mustTestStateID(t, "minecraft:deepslate_tile_slab", map[string]string{
				"type": "bottom", "waterlogged": "false",
			}),
			mustTestStateID(t, "minecraft:deepslate_brick_wall", nil),
			StateAir,
			StateWater,
		}
		for _, id := range nonFullBlocks {
			if isSculkCollisionFullBlock(id) {
				st, _ := stateByID(id)
				t.Errorf("isSculkCollisionFullBlock(%s) = true, want false", st.Name)
			}
		}

		reg := buildFlatTestDecorationRegion(t, 0, 0, 0, 20, false)
		flowingWaterID := mustTestStateID(t, "minecraft:water", map[string]string{"level": "3"})
		seagrassID := mustTestStateID(t, "minecraft:seagrass", nil)

		if hasWaterSourceFluid(flowingWaterID) {
			t.Errorf("hasWaterSourceFluid(water[level=3]) = true, want false")
		}
		if hasWaterSourceFluid(StateLava) {
			t.Errorf("hasWaterSourceFluid(lava) = true, want false")
		}
		if !hasWaterSourceFluid(StateWater) {
			t.Errorf("hasWaterSourceFluid(water[level=0]) = false, want true")
		}
		if !hasWaterSourceFluid(seagrassID) {
			t.Errorf("hasWaterSourceFluid(seagrass) = false, want true")
		}

		if reg.multifaceStateCanBeReplaced(set, "minecraft:sculk_vein", 8, 22, 8, 8, 22, 8, multifaceDirDown, flowingWaterID) {
			t.Errorf("sculk_vein replaced flowing water[level=3], want false")
		}
		if reg.multifaceStateCanBeReplaced(set, "minecraft:sculk_vein", 8, 22, 8, 8, 22, 8, multifaceDirDown, StateLava) {
			t.Errorf("sculk_vein replaced lava, want false")
		}
		if !reg.multifaceStateCanBeReplaced(set, "minecraft:sculk_vein", 8, 21, 8, 8, 21, 8, multifaceDirDown, StateWater) {
			t.Errorf("sculk_vein did not replace water[level=0], want true")
		}
		if !reg.multifaceStateCanBeReplaced(set, "minecraft:sculk_vein", 8, 21, 8, 8, 21, 8, multifaceDirDown, seagrassID) {
			t.Errorf("sculk_vein did not replace seagrass, want true")
		}

		placedVein, ok := reg.multifaceStateForPlacement("minecraft:sculk_vein", seagrassID, 8, 21, 8, multifaceDirDown)
		if !ok {
			t.Fatalf("multifaceStateForPlacement over seagrass returned ok=false")
		}
		st, ok := stateByID(placedVein)
		if !ok || st.Name != "minecraft:sculk_vein" || st.Properties["waterlogged"] != "true" || st.Properties[multifaceDirDown.name] != "true" {
			t.Fatalf("unexpected placed sculk_vein state over seagrass: %+v ok=%v", st, ok)
		}
	})

	t.Run("SmallDripleaf2612SurvivalRules", func(t *testing.T) {
		set, err := worldgen.LoadFeatureSet()
		if err != nil {
			t.Fatalf("LoadFeatureSet: %v", err)
		}
		reg := buildFlatTestDecorationRegion(t, 0, 0, 0, 20, false)
		clayID := mustTestStateID(t, "minecraft:clay", nil)
		mossID := mustTestStateID(t, "minecraft:moss_block", nil)
		flowingWaterID := mustTestStateID(t, "minecraft:water", map[string]string{"level": "4"})
		pos := worldgen.FeaturePosition{X: 8, Y: 21, Z: 8}

		cases := []struct {
			name  string
			below uint16
			cell  uint16
			want  bool
		}{
			{"clay+air", clayID, StateAir, true},
			{"moss+air", mossID, StateAir, true},
			{"dirt+air", StateDirt, StateAir, false},
			{"dirt+water_source", StateDirt, StateWater, true},
			{"dirt+flowing_water", StateDirt, flowingWaterID, false},
			{"stone+water_source", StateStone, StateWater, false},
		}
		for _, tc := range cases {
			reg.setBlock(8, 20, 8, tc.below)
			reg.setBlock(8, 21, 8, tc.cell)
			if got := reg.simpleBlockCanSurvive(pos, "minecraft:small_dripleaf", set); got != tc.want {
				t.Errorf("%s: simpleBlockCanSurvive = %v, want %v", tc.name, got, tc.want)
			}
		}
	})

	t.Run("UnderwaterMagmaBetweenClosedAndPostProcessBubbleColumns", func(t *testing.T) {
		set, err := worldgen.LoadFeatureSet()
		if err != nil {
			t.Fatalf("LoadFeatureSet: %v", err)
		}
		magmaID := mustTestStateID(t, "minecraft:magma_block", nil)
		soulSandID := mustTestStateID(t, "minecraft:soul_sand", nil)
		flowingWaterID := mustTestStateID(t, "minecraft:water", map[string]string{"level": "8"})

		reg := buildFlatTestDecorationRegion(t, 0, 0, 0, 20, false)
		for x := 6; x <= 10; x++ {
			for z := 6; z <= 10; z++ {
				for y := 21; y <= 24; y++ {
					reg.setBlock(x, y, z, StateWater)
				}
			}
		}
		cfg := worldgen.UnderwaterMagmaFeatureConfig{
			FloorSearchRange:           5,
			PlacementRadiusAroundFloor: 1,
			PlacementProbability:       0.5,
		}
		reg.placeUnderwaterMagma(worldgen.NewXoroshiro(777), worldgen.FeaturePosition{X: 8, Y: 22, Z: 8}, cfg, magmaID)

		refRng := worldgen.NewXoroshiro(777)
		xyzDiffers := false
		xyzRng := worldgen.NewXoroshiro(777)
		// Compute X-Y-Z order vs Z-Y-X order to verify BlockPos.betweenClosedStream Z-Y-X iteration.
		var zyxMask [3][3]bool
		for z := 7; z <= 9; z++ {
			for y := 19; y <= 21; y++ {
				for x := 7; x <= 9; x++ {
					roll := refRng.NextFloat()
					if y == 21 {
						if got := reg.getBlock(x, y, z); got != StateWater {
							t.Fatalf("(%d,%d,%d) = %d, want StateWater", x, y, z, got)
						}
						continue
					}
					wantMagma := roll < cfg.PlacementProbability
					if y == 20 {
						zyxMask[z-7][x-7] = wantMagma
					}
					got := reg.getBlock(x, y, z)
					if wantMagma && got != magmaID {
						t.Fatalf("(%d,%d,%d) = %d, want magma_block %d", x, y, z, got, magmaID)
					}
					if !wantMagma && got != StateStone {
						t.Fatalf("(%d,%d,%d) = %d, want StateStone", x, y, z, got)
					}
				}
			}
		}
		for x := 7; x <= 9; x++ {
			for y := 19; y <= 21; y++ {
				for z := 7; z <= 9; z++ {
					roll := xyzRng.NextFloat()
					if y == 20 && (roll < cfg.PlacementProbability) != zyxMask[z-7][x-7] {
						xyzDiffers = true
					}
				}
			}
		}
		if !xyzDiffers {
			t.Fatalf("test seed 777 did not distinguish Z-Y-X from X-Y-Z order")
		}

		ppReg := buildFlatTestDecorationRegion(t, 0, 0, 0, 20, false)
		for y := 21; y <= 23; y++ {
			ppReg.setBlock(4, y, 4, StateWater)
		}
		for y := 21; y <= 22; y++ {
			ppReg.setBlock(6, y, 6, StateWater)
		}
		ppReg.setBlock(8, 21, 8, flowingWaterID)

		ppReg.setBlock(4, 20, 4, magmaID)
		ppReg.setBlockGlobal(6, 20, 6, soulSandID)
		ppReg.setBlock(8, 20, 8, magmaID)
		ppReg.runPostProcessing(set)

		for y := 21; y <= 23; y++ {
			st, ok := stateByID(ppReg.getBlock(4, y, 4))
			if !ok || st.Name != "minecraft:bubble_column" || st.Properties["drag"] != "true" {
				t.Errorf("(4,%d,4) = %+v ok=%v, want bubble_column[drag=true]", y, st, ok)
			}
		}
		for y := 21; y <= 22; y++ {
			st, ok := stateByID(ppReg.getBlock(6, y, 6))
			if !ok || st.Name != "minecraft:bubble_column" || st.Properties["drag"] != "false" {
				t.Errorf("(6,%d,6) = %+v ok=%v, want bubble_column[drag=false]", y, st, ok)
			}
		}
		if got := ppReg.getBlock(8, 21, 8); got != flowingWaterID {
			t.Errorf("(8,21,8) = %d, want untouched flowing water %d", got, flowingWaterID)
		}
	})
}

type caveChunkStats struct {
	blocks         map[string]int
	sdWaterlogged  int
	pdWaterlogged  int
	pdThickness    map[string]int
	dragTrue       int
	dragFalse      int
	shriekerSummon int
}

func collectCaveChunkStats(t *testing.T, c *Chunk, cx, cz int32) caveChunkStats {
	t.Helper()
	s := caveChunkStats{
		blocks:      make(map[string]int),
		pdThickness: make(map[string]int),
	}
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			for y := MinY; y < MinY+WorldHeight; y++ {
				id := c.GetBlock(x, y, z)
				if isAirState(id) {
					continue
				}
				st, ok := stateByID(id)
				if !ok {
					continue
				}
				s.blocks[st.Name]++
				switch st.Name {
				case "minecraft:small_dripleaf":
					if st.Properties["waterlogged"] == "true" {
						s.sdWaterlogged++
					}
				case "minecraft:pointed_dripstone":
					if st.Properties["waterlogged"] == "true" {
						s.pdWaterlogged++
					}
					s.pdThickness[st.Properties["thickness"]]++
				case "minecraft:bubble_column":
					if st.Properties["drag"] == "true" {
						s.dragTrue++
					} else {
						s.dragFalse++
					}
				case "minecraft:sculk_shrieker":
					if st.Properties["can_summon"] == "true" {
						s.shriekerSummon++
					}
					fallthrough
				case "minecraft:sculk_catalyst", "minecraft:sculk_sensor":
					wx, wz := int(cx)*16+x, int(cz)*16+z
					be, ok := c.StructureBlockEntityAt(wx, y, wz)
					if !ok || be.ID != st.Name {
						t.Errorf("chunk (%d,%d) block %s at (%d,%d,%d) missing matching BE: got %+v ok=%v",
							cx, cz, st.Name, wx, y, wz, be, ok)
					}
				}
			}
		}
	}
	return s
}

func assertHasIntersectingStructure(t *testing.T, c *Chunk, cx, cz int32, wantID string) {
	t.Helper()
	for _, st := range c.IntersectingStructureStarts() {
		if st.StructureID == wantID {
			return
		}
	}
	t.Errorf("chunk (%d,%d) missing intersecting structure %q", cx, cz, wantID)
}

func TestCaveBiomesMultiSeedParity(t *testing.T) {
	strSeed, ok := ParseWorldSeed("caves-deep-dark-2612")
	if !ok || strSeed != 278772194 {
		t.Fatalf("ParseWorldSeed(\"caves-deep-dark-2612\") = (%d,%v), want (278772194,true)", strSeed, ok)
	}

	ResetNotReplayed()
	gens := map[int64]func(int32, int32) *Chunk{
		-909090909: NewVanillaRegionGenerator(-909090909),
		strSeed:    NewVanillaRegionGenerator(strSeed),
	}

	// Fast core chunks (run in both -short and full runs).
	{
		c := gens[-909090909](-60, -21)
		s := collectCaveChunkStats(t, c, -60, -21)
		for block, want := range map[string]int{
			"minecraft:rooted_dirt":             441,
			"minecraft:hanging_roots":           5,
			"minecraft:azalea_leaves":           58,
			"minecraft:flowering_azalea_leaves": 20,
			"minecraft:clay":                    644,
			"minecraft:big_dripleaf":            3,
			"minecraft:big_dripleaf_stem":       2,
			"minecraft:small_dripleaf":          2,
			"minecraft:moss_block":              221,
			"minecraft:cave_vines":              6,
			"minecraft:cave_vines_plant":        2,
		} {
			if got := s.blocks[block]; got != want {
				t.Errorf("(-60,-21) %s count = %d, want %d", block, got, want)
			}
		}
		if s.sdWaterlogged < 1 {
			t.Errorf("(-60,-21) waterlogged small_dripleaf = %d, want >= 1", s.sdWaterlogged)
		}
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				for y := MinY; y < MinY+WorldHeight-1; y++ {
					if st, ok := stateByID(c.GetBlock(x, y, z)); ok && st.Name == "minecraft:big_dripleaf_stem" {
						above, aok := stateByID(c.GetBlock(x, y+1, z))
						if !aok || (above.Name != "minecraft:big_dripleaf" && above.Name != "minecraft:big_dripleaf_stem") {
							t.Errorf("(-60,-21) big_dripleaf_stem at (%d,%d,%d) has %v above", x, y, z, above.Name)
						}
					}
				}
			}
		}
	}

	{
		c := gens[strSeed](-134, 158)
		s := collectCaveChunkStats(t, c, -134, 158)
		if s.blocks["minecraft:sculk"] < 120 {
			t.Errorf("(-134,158) sculk = %d, want >= 120", s.blocks["minecraft:sculk"])
		}
		if s.blocks["minecraft:sculk_vein"] < 80 {
			t.Errorf("(-134,158) sculk_vein = %d, want >= 80", s.blocks["minecraft:sculk_vein"])
		}
		if s.blocks["minecraft:sculk_sensor"] != 5 {
			t.Errorf("(-134,158) sculk_sensor = %d, want 5", s.blocks["minecraft:sculk_sensor"])
		}
		if s.blocks["minecraft:bubble_column"] != 59 || s.dragTrue != 59 {
			t.Errorf("(-134,158) bubble_column = %d (drag=true %d), want 59/59", s.blocks["minecraft:bubble_column"], s.dragTrue)
		}
		if s.blocks["minecraft:spawner"] != 0 {
			t.Errorf("(-134,158) spawner = %d, want 0 in deep_dark", s.blocks["minecraft:spawner"])
		}
		if got := len(c.StructureBlockEntities()); got != 5 {
			t.Errorf("(-134,158) StructureBlockEntities = %d, want 5", got)
		}
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				for y := MinY + 1; y < MinY+WorldHeight; y++ {
					if st, ok := stateByID(c.GetBlock(x, y, z)); ok && st.Name == "minecraft:bubble_column" {
						below, bok := stateByID(c.GetBlock(x, y-1, z))
						if !bok || (below.Name != "minecraft:bubble_column" && below.Name != "minecraft:magma_block") {
							t.Errorf("(-134,158) bubble_column at (%d,%d,%d) sits on %v", x, y, z, below.Name)
						}
					}
				}
			}
		}
		assertHasIntersectingStructure(t, c, -134, 158, "minecraft:ancient_city")
	}

	if !testing.Short() {
		gens[42024202] = NewVanillaRegionGenerator(42024202)

		// 3. dripstone_caves on seed 42024202.
		{
			c := gens[42024202](-160, -152)
			s := collectCaveChunkStats(t, c, -160, -152)
			if s.blocks["minecraft:dripstone_block"] < 5300 {
				t.Errorf("(-160,-152) dripstone_block = %d, want >= 5300", s.blocks["minecraft:dripstone_block"])
			}
			if s.blocks["minecraft:pointed_dripstone"] < 70 {
				t.Errorf("(-160,-152) pointed_dripstone = %d, want >= 70", s.blocks["minecraft:pointed_dripstone"])
			}
			if s.pdThickness["tip_merge"] < 2 || s.pdThickness["frustum"] == 0 ||
				s.pdThickness["middle"] == 0 || s.pdThickness["base"] == 0 || s.pdThickness["tip"] == 0 {
				t.Errorf("(-160,-152) incomplete pointed_dripstone thicknesses: %v", s.pdThickness)
			}
			if s.blocks["minecraft:copper_ore"] != 124 || s.blocks["minecraft:deepslate_copper_ore"] != 53 {
				t.Errorf("(-160,-152) copper_ore=(%d,%d), want (124,53)",
					s.blocks["minecraft:copper_ore"], s.blocks["minecraft:deepslate_copper_ore"])
			}
			if s.blocks["minecraft:bubble_column"] != 5 || s.dragTrue != 5 {
				t.Errorf("(-160,-152) bubble_column = %d (drag=true %d), want 5/5", s.blocks["minecraft:bubble_column"], s.dragTrue)
			}
		}

		// 4. deep_dark on seed 42024202.
		{
			c := gens[42024202](-130, -160)
			s := collectCaveChunkStats(t, c, -130, -160)
			if s.blocks["minecraft:sculk"] < 480 {
				t.Errorf("(-130,-160) sculk = %d, want >= 480", s.blocks["minecraft:sculk"])
			}
			if s.blocks["minecraft:sculk_vein"] < 100 {
				t.Errorf("(-130,-160) sculk_vein = %d, want >= 100", s.blocks["minecraft:sculk_vein"])
			}
			if s.blocks["minecraft:sculk_sensor"] != 7 {
				t.Errorf("(-130,-160) sculk_sensor = %d, want 7", s.blocks["minecraft:sculk_sensor"])
			}
			if s.blocks["minecraft:sculk_shrieker"] != 1 || s.shriekerSummon != 1 {
				t.Errorf("(-130,-160) sculk_shrieker = %d (can_summon %d), want 1/1", s.blocks["minecraft:sculk_shrieker"], s.shriekerSummon)
			}
			if got := len(c.StructureBlockEntities()); got != 8 {
				t.Errorf("(-130,-160) StructureBlockEntities = %d, want 8", got)
			}
			if s.blocks["minecraft:spawner"] != 0 {
				t.Errorf("(-130,-160) spawner = %d, want 0", s.blocks["minecraft:spawner"])
			}
		}

		// 5. ancient_city on seed 42024202.
		{
			c := gens[42024202](-107, -142)
			s := collectCaveChunkStats(t, c, -107, -142)
			if s.blocks["minecraft:sculk"] < 700 {
				t.Errorf("(-107,-142) sculk = %d, want >= 700", s.blocks["minecraft:sculk"])
			}
			if s.blocks["minecraft:sculk_vein"] < 200 {
				t.Errorf("(-107,-142) sculk_vein = %d, want >= 200", s.blocks["minecraft:sculk_vein"])
			}
			if s.blocks["minecraft:sculk_catalyst"] < 2 {
				t.Errorf("(-107,-142) sculk_catalyst = %d, want >= 2", s.blocks["minecraft:sculk_catalyst"])
			}
			if s.blocks["minecraft:sculk_shrieker"] < 1 {
				t.Errorf("(-107,-142) sculk_shrieker = %d, want >= 1", s.blocks["minecraft:sculk_shrieker"])
			}
			if s.blocks["minecraft:spawner"] != 0 {
				t.Errorf("(-107,-142) spawner = %d, want 0", s.blocks["minecraft:spawner"])
			}
			assertHasIntersectingStructure(t, c, -107, -142, "minecraft:ancient_city")
		}

		// 6. ancient_city on seed -909090909.
		{
			c := gens[-909090909](-122, -4)
			s := collectCaveChunkStats(t, c, -122, -4)
			if s.blocks["minecraft:dripstone_block"] < 1000 || s.blocks["minecraft:pointed_dripstone"] < 140 {
				t.Errorf("(-122,-4) dripstone = (%d,%d), want >= (1000,140)",
					s.blocks["minecraft:dripstone_block"], s.blocks["minecraft:pointed_dripstone"])
			}
			if s.blocks["minecraft:sculk"] < 450 || s.blocks["minecraft:sculk_vein"] < 100 {
				t.Errorf("(-122,-4) sculk = (%d,%d), want >= (450,100)",
					s.blocks["minecraft:sculk"], s.blocks["minecraft:sculk_vein"])
			}
			if s.blocks["minecraft:sculk_shrieker"] < 2 || s.shriekerSummon < 2 {
				t.Errorf("(-122,-4) sculk_shrieker = %d (can_summon %d), want >= 2",
					s.blocks["minecraft:sculk_shrieker"], s.shriekerSummon)
			}
			if s.blocks["minecraft:spawner"] != 0 {
				t.Errorf("(-122,-4) spawner = %d, want 0", s.blocks["minecraft:spawner"])
			}
			assertHasIntersectingStructure(t, c, -122, -4, "minecraft:ancient_city")
		}

		// 7. lush_caves with spore_blossom on string seed.
		{
			c := gens[strSeed](-160, -152)
			s := collectCaveChunkStats(t, c, -160, -152)
			for block, want := range map[string]int{
				"minecraft:clay":             228,
				"minecraft:moss_block":       297,
				"minecraft:cave_vines":       23,
				"minecraft:cave_vines_plant": 34,
				"minecraft:spore_blossom":    2,
			} {
				if got := s.blocks[block]; got != want {
					t.Errorf("(-160,-152) %s count = %d, want %d", block, got, want)
				}
			}
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					for y := MinY; y < MinY+WorldHeight-1; y++ {
						if st, ok := stateByID(c.GetBlock(x, y, z)); ok && st.Name == "minecraft:spore_blossom" {
							above := c.GetBlock(x, y+1, z)
							if !fullSolidState(above) {
								t.Errorf("(-160,-152) spore_blossom at (%d,%d,%d) has non-solid block %d above", x, y, z, above)
							}
						}
					}
				}
			}
		}

		// 8. dripstone_caves on string seed.
		{
			c := gens[strSeed](-154, 26)
			s := collectCaveChunkStats(t, c, -154, 26)
			if s.blocks["minecraft:dripstone_block"] < 650 || s.blocks["minecraft:pointed_dripstone"] < 190 {
				t.Errorf("(-154,26) dripstone = (%d,%d), want >= (650,190)",
					s.blocks["minecraft:dripstone_block"], s.blocks["minecraft:pointed_dripstone"])
			}
			if s.pdWaterlogged < 1 {
				t.Errorf("(-154,26) waterlogged pointed_dripstone = %d, want >= 1", s.pdWaterlogged)
			}
			if s.blocks["minecraft:copper_ore"] != 309 {
				t.Errorf("(-154,26) copper_ore = %d, want 309", s.blocks["minecraft:copper_ore"])
			}
		}
	}

	if nr := NotReplayed(); len(nr) != 0 {
		t.Fatalf("NotReplayed non-empty after cave biome parity chunks: %v", nr)
	}
}
