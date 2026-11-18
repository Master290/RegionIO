package world

import (
	"fmt"
	"testing"

	"regionio/internal/worldgen"
)

func TestForestMeadowTaigaMechanicsParity(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}

	t.Run("NoiseAndDualNoiseFlowerProviders", func(t *testing.T) {
		ff, err := set.SimpleBlock("minecraft:flower_flower_forest")
		if err != nil {
			t.Fatalf("SimpleBlock(flower_flower_forest): %v", err)
		}
		if ff.Provider.Type != "noise" || len(ff.Provider.States) != 11 {
			t.Fatalf("flower_flower_forest = type %q len=%d, want noise/11", ff.Provider.Type, len(ff.Provider.States))
		}
		fm, err := set.SimpleBlock("minecraft:flower_meadow")
		if err != nil {
			t.Fatalf("SimpleBlock(flower_meadow): %v", err)
		}
		if fm.Provider.Type != "dual_noise" || len(fm.Provider.States) != 8 ||
			fm.Provider.VarietyMin != 1 || fm.Provider.VarietyMax != 3 {
			t.Fatalf("flower_meadow = type %q len=%d variety=[%d,%d], want dual_noise/8/[1,3]",
				fm.Provider.Type, len(fm.Provider.States), fm.Provider.VarietyMin, fm.Provider.VarietyMax)
		}

		reg := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
		rng := worldgen.NewLegacy(99)
		seenFF := make(map[uint16]bool)
		seenFM := make(map[uint16]bool)
		// sampleStateProvider must forward position rather than evaluating at (0,0,0).
		for x := 0; x < 96; x += 6 {
			pos := worldgen.FeaturePosition{X: x, Y: 70, Z: x * 3}
			idFF, err := reg.sampleStateProvider(set, ff.Provider, rng, pos)
			if err != nil {
				t.Fatalf("sampleStateProvider(flower_flower_forest, %+v): %v", pos, err)
			}
			seenFF[idFF] = true

			idFM, err := reg.sampleStateProvider(set, fm.Provider, rng, pos)
			if err != nil {
				t.Fatalf("sampleStateProvider(flower_meadow, %+v): %v", pos, err)
			}
			seenFM[idFM] = true
		}
		if len(seenFF) < 3 {
			t.Fatalf("flower_flower_forest sampled only %d distinct states via sampleStateProvider; position may not be forwarded", len(seenFF))
		}
		if len(seenFM) < 3 {
			t.Fatalf("flower_meadow sampled only %d distinct states via sampleStateProvider; position may not be forwarded", len(seenFM))
		}
	})

	t.Run("BeehiveDecoratorSpawnDirectionsAndOccupants", func(t *testing.T) {
		stone := testMustStateID(t, "minecraft:stone", nil)
		birchLog := testMustStateID(t, "minecraft:birch_log", map[string]string{"axis": "y"})
		birchLeaf := testMustStateID(t, "minecraft:birch_leaves", map[string]string{
			"distance": "1", "persistent": "false", "waterlogged": "false",
		})
		beeNestSouth := testMustStateID(t, "minecraft:bee_nest", map[string]string{"facing": "south", "honey_level": "0"})

		// Blocking EAST, SOUTH, WEST must leave NORTH (8,65,7) unchosen because
		// SPAWN_DIRECTIONS excludes NORTH = WORLDGEN_FACING.getOpposite().
		blockedReg := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
		blockedReg.setBlock(8, 64, 8, birchLog)
		blockedReg.setBlock(8, 65, 8, birchLog)
		blockedReg.setBlock(8, 66, 8, birchLeaf)
		blockedReg.setBlock(9, 65, 8, stone) // EAST blocked
		blockedReg.setBlock(8, 65, 9, stone) // SOUTH blocked
		blockedReg.setBlock(7, 65, 8, stone) // WEST blocked
		decBlocked := &treeDecoration{
			t:      &treePlacer{r: blockedReg, set: set, random: worldgen.NewXoroshiro(1234)},
			logs:   []treePlacement{{x: 8, y: 64, z: 8, isLog: true}, {x: 8, y: 65, z: 8, isLog: true}},
			leaves: []treePlacement{{x: 8, y: 66, z: 8}},
		}
		if err := decBlocked.placeBeehive(1.0); err != nil {
			t.Fatalf("placeBeehive(blocked): %v", err)
		}
		if got := blockedReg.getBlock(8, 65, 7); got != StateAir {
			t.Fatalf("placeBeehive unexpectedly used NORTH slot (8,65,7): %s", stateLabel(got))
		}

		// With EAST/SOUTH/WEST open, candidate order before shufflePositions must match
		// Direction.Plane.HORIZONTAL without NORTH = {(9,65,8), (8,65,9), (7,65,8)}.
		seenOffsets := make(map[[2]int]int)
		for seed := int64(1); seed <= 25; seed++ {
			reg := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
			reg.setBlock(8, 64, 8, birchLog)
			reg.setBlock(8, 65, 8, birchLog)
			reg.setBlock(8, 66, 8, birchLeaf)

			rng := worldgen.NewXoroshiro(seed * 977)
			rngRef := worldgen.NewXoroshiro(seed * 977)
			_ = rngRef.NextFloat() // probability check
			expected := []treePlacement{
				{x: 9, y: 65, z: 8},
				{x: 8, y: 65, z: 9},
				{x: 7, y: 65, z: 8},
			}
			shufflePositions(expected, rngRef)

			dec := &treeDecoration{
				t:      &treePlacer{r: reg, set: set, random: rng},
				logs:   []treePlacement{{x: 8, y: 64, z: 8, isLog: true}, {x: 8, y: 65, z: 8, isLog: true}},
				leaves: []treePlacement{{x: 8, y: 66, z: 8}},
			}
			if err := dec.placeBeehive(1.0); err != nil {
				t.Fatalf("seed %d placeBeehive: %v", seed, err)
			}
			wantPos := expected[0]
			if got := reg.getBlock(wantPos.x, wantPos.y, wantPos.z); got != beeNestSouth {
				t.Fatalf("seed %d nest at (%d,%d,%d) = %s, want %s (SPAWN_DIRECTIONS order mismatch)",
					seed, wantPos.x, wantPos.y, wantPos.z, stateLabel(got), stateLabel(beeNestSouth))
			}
			seenOffsets[[2]int{wantPos.x - 8, wantPos.z - 8}]++

			be, ok := reg.chunks[[2]int32{0, 0}].StructureBlockEntityAt(wantPos.x, wantPos.y, wantPos.z)
			if !ok || be.ID != "minecraft:beehive" {
				t.Fatalf("seed %d missing minecraft:beehive block entity at (%d,%d,%d)", seed, wantPos.x, wantPos.y, wantPos.z)
			}
			if len(be.Bees) < 2 || len(be.Bees) > 3 {
				t.Fatalf("seed %d beehive bees count = %d, want 2..3", seed, len(be.Bees))
			}
			for i, occ := range be.Bees {
				if occ.EntityID != "minecraft:bee" || occ.TicksInHive < 0 || occ.TicksInHive >= 599 || occ.MinTicksInHive != 600 {
					t.Fatalf("seed %d bee[%d] = %+v, unexpected occupant fields", seed, i, occ)
				}
			}
		}
		if len(seenOffsets) != 3 {
			t.Fatalf("expected all 3 SPAWN_DIRECTIONS (EAST/SOUTH/WEST) to be selected across seeds, got %+v", seenOffsets)
		}
	})

	t.Run("PineAndMegaPineUpdateLeavesHashSetParity", func(t *testing.T) {
		spruceLog := testMustStateID(t, "minecraft:spruce_log", map[string]string{"axis": "y"})
		spruceLeaf7 := testMustStateID(t, "minecraft:spruce_leaves", map[string]string{
			"distance": "7", "persistent": "false", "waterlogged": "false",
		})
		spruceLeaf1 := testMustStateID(t, "minecraft:spruce_leaves", map[string]string{
			"distance": "1", "persistent": "false", "waterlogged": "false",
		})

		// Reproduce vanilla's (32,-76) PineFoliagePlacer crown at world (518, 94..103, -1209):
		// HashSet<BlockPos> order pops y=102 before y=103 when expanding distance 1 -> 2,
		// so (517,103,-1209) stays at distance=2 while (518,104,-1209) reaches distance=1.
		reg := buildFlatTestDecorationRegion(t, 32, -76, 1, 63, false)
		if err := reg.setSource(32, -76); err != nil {
			t.Fatalf("setSource(32,-76): %v", err)
		}
		tp := &treePlacer{r: reg, set: set}
		for y := 94; y <= 103; y++ {
			reg.setBlock(518, y, -1209, spruceLog)
			tp.placements = append(tp.placements, treePlacement{x: 518, y: y, z: -1209, isLog: true})
		}
		for _, row := range []struct {
			y int
			r int
		}{
			{105, 0}, {104, 1}, {103, 2}, {102, 3}, {101, 2},
		} {
			for dx := -row.r; dx <= row.r; dx++ {
				for dz := -row.r; dz <= row.r; dz++ {
					if abs(dx) == row.r && abs(dz) == row.r && row.r > 0 {
						continue
					}
					if dx == 0 && dz == 0 && row.y <= 103 {
						continue
					}
					x, z := 518+dx, -1209+dz
					reg.setBlock(x, row.y, z, spruceLeaf7)
					tp.placements = append(tp.placements, treePlacement{x: x, y: row.y, z: z})
				}
			}
		}
		tp.applyLeafDistance()

		stAdj, ok := stateByID(reg.getBlock(517, 103, -1209))
		if !ok || stAdj.Properties["distance"] != "2" {
			t.Fatalf("(517,103,-1209) distance = %q, want 2 from TreeFeature.updateLeaves HashSet order", stAdj.Properties["distance"])
		}
		stTop, ok := stateByID(reg.getBlock(518, 104, -1209))
		if !ok || stTop.Properties["distance"] != "1" {
			t.Fatalf("(518,104,-1209) distance = %q, want 1", stTop.Properties["distance"])
		}

		// Placing an exterior leaf overlapping box edge triggers LeavesBlock.updateShape -> tick wave.
		reg2 := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
		reg2.setBlock(5, 70, 5, spruceLeaf1)
		reg2.setBlock(4, 70, 5, spruceLeaf7)
		reg2.setBlock(3, 70, 5, spruceLeaf7)
		tp2 := &treePlacer{r: reg2, set: set}
		reg2.setBlock(6, 70, 5, spruceLog)
		tp2.placements = []treePlacement{
			{x: 6, y: 70, z: 5, isLog: true},
			{x: 5, y: 70, z: 5},
		}
		tp2.applyLeafDistance()
		if stOut, _ := stateByID(reg2.getBlock(3, 70, 5)); stOut.Properties["distance"] != "3" {
			t.Fatalf("exterior leaf (3,70,5) distance = %q, want 3 via edge tick propagation", stOut.Properties["distance"])
		}
	})

	t.Run("FallenTreePostProcessingAndDecorators", func(t *testing.T) {
		fallenBirch, err := set.FallenTree("minecraft:fallen_birch_tree")
		if err != nil {
			t.Fatalf("FallenTree(fallen_birch_tree): %v", err)
		}
		grass := testMustStateID(t, "minecraft:grass_block", map[string]string{"snowy": "false"})
		snow1 := testMustStateID(t, "minecraft:snow", map[string]string{"layers": "1"})

		placed := false
		for attempt := int64(1); attempt <= 60; attempt++ {
			reg := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
			for x := -4; x <= 20; x++ {
				for z := -4; z <= 20; z++ {
					reg.setBlock(x, 63, z, grass)
					// Pre-place snow above expected horizontal log elevation so
					// markAboveForPostProcessing records every fallen log's column.
					if !(x == 8 && z == 8) {
						reg.setBlock(x, 65, z, snow1)
					}
				}
			}
			reg.setBlock(8, 65, 8, snow1)

			rng := worldgen.NewXoroshiro(4242 + attempt*17)
			if err := reg.placeFallenTree(set, rng, worldgen.FeaturePosition{X: 8, Y: 64, Z: 8}, fallenBirch); err != nil {
				t.Fatalf("placeFallenTree: %v", err)
			}

			horizCount := 0
			mushCount := 0
			for x := -4; x <= 20; x++ {
				for z := -4; z <= 20; z++ {
					st, ok := stateByID(reg.getBlock(x, 64, z))
					if ok && st.Name == "minecraft:birch_log" && (st.Properties["axis"] == "x" || st.Properties["axis"] == "z") {
						horizCount++
					}
					if ab, ok := stateByID(reg.getBlock(x, 65, z)); ok && (ab.Name == "minecraft:red_mushroom" || ab.Name == "minecraft:brown_mushroom") {
						mushCount++
					}
				}
			}
			if horizCount == 0 {
				continue
			}

			// Verify stump markAboveForPostProcessing marked (8,65,8) for snow/post-processing.
			foundStumpPP := false
			for _, pp := range reg.postProcessPositions {
				if pp == [3]int{8, 65, 8} {
					foundStumpPP = true
					break
				}
			}
			if !foundStumpPP {
				t.Fatalf("stump above cell (8,65,8) missing from postProcessPositions")
			}

			// Add snow[layers=1] above any placed mushroom at y=65 to confirm y+2 marking cleans it up too.
			if mushCount > 0 {
				for x := -4; x <= 20; x++ {
					for z := -4; z <= 20; z++ {
						if ab, ok := stateByID(reg.getBlock(x, 65, z)); ok && (ab.Name == "minecraft:red_mushroom" || ab.Name == "minecraft:brown_mushroom") {
							reg.setBlock(x, 66, z, snow1)
							// Re-verify post-processing removes unsupported snow above mushroom at y+1 -> y+2.
							if ab.Name != "" {
								reg.markAboveForPostProcessing(x, 64, z)
							}
						}
					}
				}
				reg.runPostProcessing(set)
				for x := -4; x <= 20; x++ {
					for z := -4; z <= 20; z++ {
						if ab, ok := stateByID(reg.getBlock(x, 65, z)); ok && (ab.Name == "minecraft:red_mushroom" || ab.Name == "minecraft:brown_mushroom") {
							if reg.getBlock(x, 66, z) != StateAir {
								t.Fatalf("unsupported snow above mushroom at (%d,66,%d) survived post-processing", x, z)
							}
						}
					}
				}
			}
			placed = true
			if attempt >= 10 && mushCount > 0 {
				break
			}
		}
		if !placed {
			t.Fatal("failed to place horizontal fallen birch tree in 60 attempts")
		}
	})
}

func TestForestMeadowTaigaMultiSeedParity(t *testing.T) {
	strSeed, ok := ParseWorldSeed("forest-meadow-taiga-2612")
	if !ok || strSeed != 1947873494 {
		t.Fatalf("ParseWorldSeed(\"forest-meadow-taiga-2612\") = (%d,%v), want (1947873494,true)", strSeed, ok)
	}

	type chunkSpec struct {
		seed         int64
		cx, cz       int32
		biome        string
		wantCounts   map[string]int
		wantNoiseFlw int
		wantHorizBir int
		wantHorizSpr int
		wantNestAt   *[3]int
		wantStates   []struct {
			x, y, z int
			name    string
			propKey string
			propVal string
		}
		fast bool
	}

	specs := []chunkSpec{
		{
			seed: 51515151, cx: 144, cz: -76, biome: "minecraft:flower_forest", fast: true,
			wantCounts: map[string]int{
				"minecraft:grass_block": 255,
				"minecraft:birch_log":   11,
				"minecraft:oak_log":     13,
			},
			wantNoiseFlw: 16,
		},
		{
			seed: 51515151, cx: -3, cz: -63, biome: "minecraft:meadow", fast: true,
			wantCounts: map[string]int{
				"minecraft:grass_block": 255,
				"minecraft:birch_log":   11,
				"minecraft:wildflowers": 18,
				"minecraft:bee_nest":    1,
			},
			wantNoiseFlw: 11,
			wantNestAt:   &[3]int{-37, 125, -1002},
		},
		{
			seed: 51515151, cx: 178, cz: -18, biome: "minecraft:old_growth_pine_taiga",
			wantCounts: map[string]int{
				"minecraft:grass_block":       167,
				"minecraft:podzol":            85,
				"minecraft:mossy_cobblestone": 10,
				"minecraft:spruce_log":        99,
				"minecraft:sweet_berry_bush":  11,
				"minecraft:brown_mushroom":    2,
			},
		},
		{
			seed: 51515151, cx: 184, cz: -18, biome: "minecraft:old_growth_pine_taiga",
			wantCounts: map[string]int{
				"minecraft:coarse_dirt":       90,
				"minecraft:podzol":            153,
				"minecraft:mossy_cobblestone": 34,
				"minecraft:spruce_log":        44,
				"minecraft:large_fern":        42,
			},
		},
		{
			seed: -717171717, cx: -8, cz: -26, biome: "minecraft:meadow",
			wantCounts: map[string]int{
				"minecraft:grass_block": 355,
				"minecraft:wildflowers": 19,
				"minecraft:leaf_litter": 6,
				"minecraft:tall_grass":  10,
			},
			wantNoiseFlw: 23,
		},
		{
			seed: -717171717, cx: -20, cz: -36, biome: "minecraft:old_growth_birch_forest",
			wantCounts: map[string]int{
				"minecraft:grass_block":    247,
				"minecraft:birch_log":      21,
				"minecraft:wildflowers":    9,
				"minecraft:red_mushroom":   1,
				"minecraft:brown_mushroom": 1,
			},
			wantHorizBir: 3,
		},
		{
			seed: -717171717, cx: -250, cz: 290, biome: "minecraft:old_growth_spruce_taiga",
			wantCounts: map[string]int{
				"minecraft:grass_block":       79,
				"minecraft:coarse_dirt":       7,
				"minecraft:podzol":            144,
				"minecraft:mossy_cobblestone": 36,
				"minecraft:spruce_log":        127,
			},
			wantStates: []struct {
				x, y, z int
				name    string
				propKey string
				propVal string
			}{
				{-3990, 102, 4645, "minecraft:spruce_leaves", "distance", "2"},
				{-3991, 103, 4645, "minecraft:spruce_leaves", "distance", "1"},
			},
		},
		{
			seed: -717171717, cx: 32, cz: -76, biome: "minecraft:old_growth_spruce_taiga",
			wantCounts: map[string]int{
				"minecraft:podzol":            40,
				"minecraft:mossy_cobblestone": 35,
				"minecraft:spruce_log":        20,
				"minecraft:brown_mushroom":    4,
			},
			wantHorizSpr: 4,
			wantStates: []struct {
				x, y, z int
				name    string
				propKey string
				propVal string
			}{
				{517, 103, -1209, "minecraft:spruce_leaves", "distance", "2"},
				{518, 104, -1209, "minecraft:spruce_leaves", "distance", "1"},
			},
		},
		{
			seed: strSeed, cx: -154, cz: -126, biome: "minecraft:flower_forest",
			wantCounts: map[string]int{
				"minecraft:grass_block": 250,
				"minecraft:oak_log":     39,
				"minecraft:bee_nest":    1,
			},
			wantNoiseFlw: 47,
			wantNestAt:   &[3]int{-2452, 78, -2015},
		},
		{
			seed: strSeed, cx: 48, cz: 20, biome: "minecraft:old_growth_birch_forest",
			wantCounts: map[string]int{
				"minecraft:birch_log":    77,
				"minecraft:wildflowers":  4,
				"minecraft:red_mushroom": 1,
			},
			wantHorizBir: 4,
		},
		{
			seed: strSeed, cx: 24, cz: -26, biome: "minecraft:forest",
			wantCounts: map[string]int{
				"minecraft:grass_block": 250,
				"minecraft:oak_log":     31,
				"minecraft:leaf_litter": 151,
			},
		},
	}

	ResetNotReplayed()
	gens := make(map[int64]func(int32, int32) *Chunk)

	for _, sc := range specs {
		if testing.Short() && !sc.fast {
			continue
		}
		name := fmt.Sprintf("seed=%d/(%d,%d)_%s", sc.seed, sc.cx, sc.cz, sc.biome)
		t.Run(name, func(t *testing.T) {
			gen := gens[sc.seed]
			if gen == nil {
				gen = NewVanillaRegionGenerator(sc.seed)
				gens[sc.seed] = gen
			}
			c := gen(sc.cx, sc.cz)
			counts := make(map[string]int)
			noiseFlw, horizBir, horizSpr := 0, 0, 0
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
						counts[st.Name]++
						switch st.Name {
						case "minecraft:birch_log":
							if st.Properties["axis"] == "x" || st.Properties["axis"] == "z" {
								horizBir++
							}
						case "minecraft:spruce_log":
							if st.Properties["axis"] == "x" || st.Properties["axis"] == "z" {
								horizSpr++
							}
						case "minecraft:wildflowers":
							if fa := st.Properties["flower_amount"]; fa < "1" || fa > "4" {
								t.Errorf("invalid wildflowers flower_amount=%q", fa)
							}
						case "minecraft:leaf_litter":
							if sa := st.Properties["segment_amount"]; sa < "1" || sa > "4" {
								t.Errorf("invalid leaf_litter segment_amount=%q", sa)
							}
						case "minecraft:dandelion", "minecraft:poppy", "minecraft:allium", "minecraft:azure_bluet",
							"minecraft:red_tulip", "minecraft:orange_tulip", "minecraft:white_tulip", "minecraft:pink_tulip",
							"minecraft:oxeye_daisy", "minecraft:cornflower", "minecraft:lily_of_the_valley":
							noiseFlw++
						}
					}
				}
			}

			for k, want := range sc.wantCounts {
				if got := counts[k]; got != want {
					t.Errorf("%s count[%s] = %d, want %d", name, k, got, want)
				}
			}
			if sc.wantNoiseFlw > 0 && noiseFlw != sc.wantNoiseFlw {
				t.Errorf("%s noiseFlowers = %d, want %d", name, noiseFlw, sc.wantNoiseFlw)
			}
			if sc.wantHorizBir > 0 && horizBir != sc.wantHorizBir {
				t.Errorf("%s horizBirch = %d, want %d", name, horizBir, sc.wantHorizBir)
			}
			if sc.wantHorizSpr > 0 && horizSpr != sc.wantHorizSpr {
				t.Errorf("%s horizSpruce = %d, want %d", name, horizSpr, sc.wantHorizSpr)
			}
			if sc.wantNestAt != nil {
				p := *sc.wantNestAt
				st, ok := stateByID(c.GetBlock(p[0]&15, p[1], p[2]&15))
				if !ok || st.Name != "minecraft:bee_nest" || st.Properties["facing"] != "south" {
					t.Fatalf("%s nest at (%d,%d,%d) = %+v, want bee_nest[facing=south]", name, p[0], p[1], p[2], st)
				}
				be, ok := c.StructureBlockEntityAt(p[0], p[1], p[2])
				if !ok || be.ID != "minecraft:beehive" || len(be.Bees) < 2 || len(be.Bees) > 3 {
					t.Fatalf("%s StructureBlockEntityAt(%d,%d,%d) = (%+v, %v), want beehive with 2..3 bees",
						name, p[0], p[1], p[2], be, ok)
				}
			}
			for _, ws := range sc.wantStates {
				st, ok := stateByID(c.GetBlock(ws.x&15, ws.y, ws.z&15))
				if !ok || st.Name != ws.name || st.Properties[ws.propKey] != ws.propVal {
					t.Errorf("%s block(%d,%d,%d) = %+v, want %s[%s=%s]",
						name, ws.x, ws.y, ws.z, st, ws.name, ws.propKey, ws.propVal)
				}
			}
		})
	}

	if missing := NotReplayed(); len(missing) != 0 {
		t.Fatalf("unexpected NotReplayed entries: %+v", missing)
	}
}
