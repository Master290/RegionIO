package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func TestNetherBiomesMechanicsParity(t *testing.T) {
	// 1. BlockPos.withinManhattan order: signed dx -> signed dy -> +dz then -dz.
	type manhattanPt struct{ x, y, z, d int }
	var visited []manhattanPt
	withinManhattan(10, 20, 30, 1, 1, 1, func(x, y, z, dist int) bool {
		visited = append(visited, manhattanPt{x, y, z, dist})
		return len(visited) < 7
	})
	wantVisited := []manhattanPt{
		{10, 20, 30, 0},
		{9, 20, 30, 1},
		{10, 19, 30, 1},
		{10, 20, 31, 1},
		{10, 20, 29, 1},
		{10, 21, 30, 1},
		{11, 20, 30, 1},
	}
	if len(visited) != len(wantVisited) {
		t.Fatalf("withinManhattan count = %d, want %d: %v", len(visited), len(wantVisited), visited)
	}
	for i := range wantVisited {
		if visited[i] != wantVisited[i] {
			t.Fatalf("withinManhattan[%d] = %v, want %v (full=%v)", i, visited[i], wantVisited[i], visited)
		}
	}

	// 2. Datapack metadata: FeatureSteps stages, patch_crimson_roots modifiers,
	// glowstone_extra biased_to_bottom count, and nether_fossil beard_thin adaptation.
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}
	steps, err := set.FeatureSteps(netherBiomeOrder)
	if err != nil {
		t.Fatalf("FeatureSteps(netherBiomeOrder): %v", err)
	}
	if len(steps) <= 9 {
		t.Fatalf("Nether FeatureSteps count = %d, want > 9", len(steps))
	}
	for st, minCount := range map[int]int{2: 1, 4: 3, 7: 23, 9: 10} {
		if len(steps[st]) != minCount {
			t.Fatalf("Nether FeatureSteps[%d] len = %d, want %d (%v)", st, len(steps[st]), minCount, steps[st])
		}
	}
	pcr := set.Placed["minecraft:patch_crimson_roots"]
	if len(pcr.Placement) != 5 {
		t.Fatalf("patch_crimson_roots placement modifiers = %d, want 5", len(pcr.Placement))
	}
	for _, mod := range pcr.Placement {
		if mod.Type == "minecraft:in_square" {
			t.Fatalf("patch_crimson_roots unexpectedly contains minecraft:in_square")
		}
	}
	ge := set.Placed["minecraft:glowstone_extra"]
	if len(ge.Placement) == 0 || ge.Placement[0].Type != "minecraft:count" {
		t.Fatalf("glowstone_extra placement[0] = %v, want minecraft:count", ge.Placement)
	}
	gePlan, err := set.Placement("minecraft:glowstone_extra")
	if err != nil || !gePlan.Count.BiasedToBottom || gePlan.Count.Min != 0 || gePlan.Count.Max != 9 {
		t.Fatalf("glowstone_extra plan = %+v, err=%v; want BiasedToBottom [0..9]", gePlan, err)
	}
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}
	fossilDef := sets.Structures["minecraft:nether_fossil"]
	if fossilDef == nil || fossilDef.TerrainAdaptation != "beard_thin" {
		t.Fatalf("nether_fossil def = %+v, want terrain_adaptation=beard_thin", fossilDef)
	}

	// 3. DeltaFeature writes rim then shifted contents and skips already-placed contents.
	cDelta := NewDimensionChunk(0, 0, biomeID("minecraft:basalt_deltas"), NetherDimensionType)
	for lx := 2; lx <= 13; lx++ {
		for lz := 2; lz <= 13; lz++ {
			for y := 35; y <= 40; y++ {
				cDelta.SetBlock(lx, y, lz, StateNetherrack)
			}
		}
	}
	placedDelta := false
	for s := int64(1); s <= 16; s++ {
		rng := worldgen.NewXoroshiro(s)
		if rng.NextDouble() < 0.9 {
			if PlaceDeltaFeature(cDelta, 8, 40, 8, worldgen.NewXoroshiro(s), StateLava, StateMagmaBlock, 2, 2, 1, 1) {
				placedDelta = true
				break
			}
		}
	}
	if !placedDelta {
		t.Fatal("expected PlaceDeltaFeature with size=2, rim=1 to succeed")
	}
	if got := cDelta.GetBlock(6, 40, 8); got != StateMagmaBlock {
		t.Fatalf("delta rim (6,40,8) = %s, want minecraft:magma_block", BlockName(got))
	}
	if got := cDelta.GetBlock(9, 40, 9); !isLavaState(got) {
		t.Fatalf("shifted delta contents (9,40,9) = %s, want minecraft:lava", BlockName(got))
	}
	if got := cDelta.GetBlock(10, 40, 10); got != StateNetherrack {
		t.Fatalf("delta should skip already-placed contents at (9,40,9) -> (10,40,10) = %s, want minecraft:netherrack", BlockName(got))
	}

	// 4. BasaltColumnsFeature treats lava at y == NetherDimensionType.SeaLevel (32) as air/lava-ocean.
	cCol := NewDimensionChunk(0, 0, biomeID("minecraft:basalt_deltas"), NetherDimensionType)
	for lx := 5; lx <= 11; lx++ {
		for lz := 5; lz <= 11; lz++ {
			cCol.SetBlock(lx, 31, lz, StateNetherrack)
			cCol.SetBlock(lx, 32, lz, StateLava)
		}
	}
	if NetherDimensionType.SeaLevel != 32 {
		t.Fatalf("NetherDimensionType.SeaLevel = %d, want 32", NetherDimensionType.SeaLevel)
	}
	if !PlaceBasaltColumns(cCol, 8, 32, 8, worldgen.NewXoroshiro(42), 1, 1, 2, 4) {
		t.Fatal("expected PlaceBasaltColumns at Nether sea level y=32 to succeed over netherrack")
	}
	if got := cCol.GetBlock(8, 32, 8); got != StateBasalt {
		t.Fatalf("column base at y=32 = %s, want minecraft:basalt", BlockName(got))
	}

	// 5. Nether fast_lava constants and live spread radius (7 blocks in Nether vs 3 in Overworld).
	if !NetherDimensionType.FastLava {
		t.Fatal("expected NetherDimensionType.FastLava to be true")
	}
	if fluidDropOff(fluidLava, true) != 1 || fluidSlopeFindDistance(fluidLava, true) != 4 || fluidTickDelay(fluidLava, true) != 10 {
		t.Fatalf("fastLava constants = (%d,%d,%d), want (1,4,10)",
			fluidDropOff(fluidLava, true), fluidSlopeFindDistance(fluidLava, true), fluidTickDelay(fluidLava, true))
	}
	if fluidDropOff(fluidLava, false) != 2 || fluidSlopeFindDistance(fluidLava, false) != 2 || fluidTickDelay(fluidLava, false) != 30 {
		t.Fatalf("normal lava constants = (%d,%d,%d), want (2,2,30)",
			fluidDropOff(fluidLava, false), fluidSlopeFindDistance(fluidLava, false), fluidTickDelay(fluidLava, false))
	}
	measureFlatLavaReach := func(nether bool) int {
		var c *Chunk
		if nether {
			c = NewDimensionChunk(0, 0, biomeID("minecraft:nether_wastes"), NetherDimensionType)
		} else {
			c = NewChunk(0, 0, BiomePlains)
		}
		for lx := 0; lx < 16; lx++ {
			for lz := 0; lz < 16; lz++ {
				c.SetBlock(lx, 63, lz, StateNetherrack)
				c.SetBlock(lx, 64, lz, StateAir)
			}
		}
		reg, err := newDecorationRegion([]*Chunk{c})
		if err != nil {
			t.Fatalf("newDecorationRegion(lava): %v", err)
		}
		c.SetBlock(4, 64, 8, StateLava)
		reg.tickScheduledSprings([][3]int{{4, 64, 8}})
		reach := 0
		for dx := 1; dx <= 10; dx++ {
			if isLavaState(c.GetBlock(4+dx, 64, 8)) {
				reach = dx
			}
		}
		return reach
	}
	if got := measureFlatLavaReach(true); got != 7 {
		t.Fatalf("Nether fast_lava flat spread reach = %d, want 7", got)
	}
	if got := measureFlatLavaReach(false); got != 3 {
		t.Fatalf("Overworld normal lava flat spread reach = %d, want 3", got)
	}

	// 6. PlaceNetherFossilInRegion places cross-chunk fossils cleanly and preserves beard_thin plans.
	cF0 := NewDimensionChunk(0, 0, biomeID("minecraft:soul_sand_valley"), NetherDimensionType)
	cF1 := NewDimensionChunk(1, 0, biomeID("minecraft:soul_sand_valley"), NetherDimensionType)
	regF, err := newDecorationRegion([]*Chunk{cF0, cF1})
	if err != nil {
		t.Fatalf("newDecorationRegion(fossil): %v", err)
	}
	fossilStub := &NetherFossilStub{
		X:        14,
		Y:        60,
		Z:        4,
		Rotation: 0,
		Template: "minecraft:nether_fossils/fossil_1",
		Box:      msBox{minX: 14, minY: 60, minZ: 4, maxX: 20, maxY: 68, maxZ: 12},
	}
	p0 := PlaceNetherFossilInRegion(regF, fossilStub, 63636363, 0, 0)
	p1 := PlaceNetherFossilInRegion(regF, fossilStub, 63636363, 1, 0)
	if p0 == 0 || p1 == 0 {
		t.Fatalf("expected PlaceNetherFossilInRegion across border to place blocks in both chunks, got (%d,%d)", p0, p1)
	}
	textSeed, ok := worldgen.ParseWorldSeed("nether-biomes-2612")
	if !ok || textSeed != 165634858 {
		t.Fatalf("ParseWorldSeed(nether-biomes-2612) = (%d, %v), want (165634858, true)", textSeed, ok)
	}
	for _, tc := range []struct {
		seed   int64
		cx, cz int32
	}{
		{63636363, 0, -2},
		{-848484848, 4, -2},
		{165634858, 0, 0},
	} {
		od, _, _ := netherGeneratorInputs(tc.seed)
		if beard := buildNetherChunkBeardifier(od, sets, tc.seed, tc.cx, tc.cz); beard == nil {
			t.Fatalf("expected non-nil nether_fossil beardifier for seed %d chunk (%d,%d)", tc.seed, tc.cx, tc.cz)
		}
	}
}

func TestNetherBiomesMultiSeedParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-seed Nether decoration check in short mode")
	}
	textSeed, ok := worldgen.ParseWorldSeed("nether-biomes-2612")
	if !ok || textSeed != 165634858 {
		t.Fatalf("ParseWorldSeed(nether-biomes-2612) = (%d, %v), want (165634858, true)", textSeed, ok)
	}

	validNetherBiome := make(map[string]bool, len(netherBiomeOrder))
	for _, b := range netherBiomeOrder {
		validNetherBiome[b] = true
	}

	type chunkSpec struct {
		cx, cz     int32
		biome      string
		wantBlocks []string
		wantBiomes []string
	}
	seedSpecs := []struct {
		seed   int64
		chunks []chunkSpec
	}{
		{
			seed: 63636363,
			chunks: []chunkSpec{
				{8, 9, "crimson_forest", []string{"minecraft:crimson_nylium", "minecraft:crimson_stem", "minecraft:nether_wart_block", "minecraft:weeping_vines", "minecraft:crimson_roots"}, []string{"minecraft:crimson_forest"}},
				{33, -26, "warped_forest", []string{"minecraft:warped_nylium", "minecraft:warped_stem", "minecraft:warped_wart_block", "minecraft:warped_roots", "minecraft:nether_sprouts"}, []string{"minecraft:warped_forest"}},
				{-3, -1, "basalt_deltas", []string{"minecraft:basalt", "minecraft:blackstone", "minecraft:magma_block"}, []string{"minecraft:basalt_deltas"}},
				{0, -2, "soul_sand_valley+fossil", []string{"minecraft:soul_sand", "minecraft:soul_soil", "minecraft:bone_block"}, []string{"minecraft:soul_sand_valley"}},
				{6, -4, "nether_wastes+soul_sand_valley", []string{"minecraft:netherrack"}, []string{"minecraft:nether_wastes", "minecraft:soul_sand_valley"}},
			},
		},
		{
			seed: -848484848,
			chunks: []chunkSpec{
				{0, 0, "crimson_forest", []string{"minecraft:crimson_nylium", "minecraft:crimson_stem", "minecraft:nether_wart_block", "minecraft:weeping_vines"}, []string{"minecraft:crimson_forest"}},
				{-12, -4, "warped_forest", []string{"minecraft:warped_nylium", "minecraft:warped_stem", "minecraft:warped_wart_block", "minecraft:warped_roots", "minecraft:nether_sprouts"}, []string{"minecraft:warped_forest"}},
				{13, -16, "basalt_deltas", []string{"minecraft:basalt", "minecraft:blackstone", "minecraft:magma_block"}, []string{"minecraft:basalt_deltas"}},
				{4, -2, "soul_sand_valley+fossil", []string{"minecraft:soul_sand", "minecraft:soul_soil", "minecraft:bone_block"}, []string{"minecraft:soul_sand_valley"}},
				{12, -12, "nether_wastes+soul_sand_valley", []string{"minecraft:netherrack"}, []string{"minecraft:nether_wastes", "minecraft:soul_sand_valley"}},
			},
		},
		{
			seed: textSeed,
			chunks: []chunkSpec{
				{-2, 10, "crimson_forest", []string{"minecraft:crimson_nylium", "minecraft:crimson_stem", "minecraft:nether_wart_block", "minecraft:weeping_vines"}, []string{"minecraft:crimson_forest"}},
				{-29, -29, "warped_forest", []string{"minecraft:warped_nylium", "minecraft:warped_stem", "minecraft:warped_wart_block", "minecraft:nether_sprouts"}, []string{"minecraft:warped_forest"}},
				{-8, -9, "basalt_deltas", []string{"minecraft:basalt", "minecraft:blackstone", "minecraft:magma_block"}, []string{"minecraft:basalt_deltas"}},
				{0, 0, "soul_sand_valley+fossil", []string{"minecraft:soul_sand", "minecraft:soul_soil", "minecraft:bone_block"}, []string{"minecraft:soul_sand_valley"}},
				{3, 2, "soul_sand_valley", []string{"minecraft:soul_sand", "minecraft:soul_soil"}, []string{"minecraft:soul_sand_valley"}},
			},
		},
	}

	for _, ss := range seedSpecs {
		ResetNotReplayed()
		gen1, batch1 := NewNetherGenerators(ss.seed)
		gen2, _ := NewNetherGenerators(ss.seed)

		for idx, cs := range ss.chunks {
			c := gen1(cs.cx, cs.cz)
			if nr := NotReplayed(); len(nr) != 0 {
				t.Fatalf("seed %d chunk (%d,%d) had un-replayed features: %v", ss.seed, cs.cx, cs.cz, nr)
			}

			hm := c.ParityHeightmaps()
			present := make(map[string]bool)
			for lz := 0; lz < 16; lz++ {
				for lx := 0; lx < 16; lx++ {
					if got := c.GetBlock(lx, 0, lz); got != StateBedrock {
						t.Fatalf("seed %d chunk (%d,%d) floor bedrock missing at (%d,0,%d): %s", ss.seed, cs.cx, cs.cz, lx, lz, BlockName(got))
					}
					if got := c.GetBlock(lx, NetherRoofY, lz); got != StateBedrock {
						t.Fatalf("seed %d chunk (%d,%d) roof bedrock missing at (%d,127,%d): %s", ss.seed, cs.cx, cs.cz, lx, lz, BlockName(got))
					}
					colIdx := (lz << 4) | lx
					for k := 0; k < 3; k++ {
						if hm[k][colIdx] < 0 || hm[k][colIdx] > 128 {
							t.Fatalf("seed %d chunk (%d,%d) hm[%d][%d] = %d out of [0..128]", ss.seed, cs.cx, cs.cz, k, colIdx, hm[k][colIdx])
						}
					}
					for y := 0; y < netherHeight; y++ {
						if st := c.GetBlock(lx, y, lz); st != StateAir && st != StateCaveAir {
							present[BlockName(st)] = true
						}
					}
				}
			}

			biomeCounts := make(map[string]int)
			for y := 0; y < netherHeight; y += biomeCellSize {
				for lz := 0; lz < 16; lz += biomeCellSize {
					for lx := 0; lx < 16; lx += biomeCellSize {
						bName := biomeNameByID(c.GetBiome(lx, y, lz))
						biomeCounts[bName]++
						if !validNetherBiome[bName] {
							t.Fatalf("seed %d chunk (%d,%d) invalid Nether biome %q at (%d,%d,%d)", ss.seed, cs.cx, cs.cz, bName, lx, y, lz)
						}
					}
				}
			}

			for _, wantBiome := range cs.wantBiomes {
				if biomeCounts[wantBiome] == 0 {
					t.Errorf("seed %d %s chunk (%d,%d) missing expected biome %s (got=%v)", ss.seed, cs.biome, cs.cx, cs.cz, wantBiome, biomeCounts)
				}
			}

			for _, want := range cs.wantBlocks {
				if !present[want] {
					t.Fatalf("seed %d %s chunk (%d,%d) (biomes=%v) missing expected block %s", ss.seed, cs.biome, cs.cx, cs.cz, biomeCounts, want)
				}
			}

			// Verify determinism and single-vs-batch generator match on representative chunks.
			if idx == 0 || cs.biome == "soul_sand_valley+fossil" {
				cCopy := gen2(cs.cx, cs.cz)
				for y := 0; y < NetherDimensionType.Height; y++ {
					for lz := 0; lz < 16; lz++ {
						for lx := 0; lx < 16; lx++ {
							if c.GetBlock(lx, y, lz) != cCopy.GetBlock(lx, y, lz) {
								t.Fatalf("seed %d chunk (%d,%d) non-deterministic block at (%d,%d,%d)", ss.seed, cs.cx, cs.cz, lx, y, lz)
							}
						}
					}
				}
			}
			if cs.biome == "soul_sand_valley+fossil" {
				bChunks, err := batch1(cs.cx, cs.cz)
				if err != nil {
					t.Fatalf("seed %d batch(%d,%d): %v", ss.seed, cs.cx, cs.cz, err)
				}
				bc := bChunks[[2]int32{cs.cx, cs.cz}]
				if bc == nil {
					t.Fatalf("seed %d batch(%d,%d) returned nil center chunk", ss.seed, cs.cx, cs.cz)
				}
				for y := 0; y < NetherDimensionType.Height; y++ {
					for lz := 0; lz < 16; lz++ {
						for lx := 0; lx < 16; lx++ {
							if c.GetBlock(lx, y, lz) != bc.GetBlock(lx, y, lz) {
								t.Fatalf("seed %d chunk (%d,%d) single vs batch mismatch at (%d,%d,%d): %s vs %s",
									ss.seed, cs.cx, cs.cz, lx, y, lz, BlockName(c.GetBlock(lx, y, lz)), BlockName(bc.GetBlock(lx, y, lz)))
							}
						}
					}
				}
			}
		}
	}
}
