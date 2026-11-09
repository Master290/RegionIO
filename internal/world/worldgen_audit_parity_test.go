package world

import (
	"sync"
	"testing"

	"regionio/internal/worldgen"
)

func computeLightChecksum64(c *Chunk) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	c.mu.RLock()
	defer c.mu.RUnlock()
	for si := 0; si < SectionCount; si++ {
		if sky := c.skyLight[si]; sky != nil {
			for _, b := range sky {
				h ^= uint64(b)
				h *= prime64
			}
		} else {
			h ^= 0xff
			h *= prime64
		}
		if blk := c.blockLight[si]; blk != nil {
			for _, b := range blk {
				h ^= uint64(b)
				h *= prime64
			}
		} else {
			h ^= 0xfe
			h *= prime64
		}
	}
	return h
}

// TestThreeSeedsThreeDimensionsChunkHashes verifies deterministic 64-bit chunk
// hashes (blocks, 3D biomes, and heightmaps) for seed 12345 and two additional
// seeds (4242 and 987654321) across all 3 dimensions (Overworld, Nether, End).
func TestThreeSeedsThreeDimensionsChunkHashes(t *testing.T) {
	seeds := []int64{12345, 4242, 987654321}

	type dimHashRecord struct {
		owHash     uint64
		netherHash uint64
		endHash    uint64
	}
	seen := make(map[int64]dimHashRecord, len(seeds))

	for _, seed := range seeds {
		owGen := NewVanillaRegionGenerator(seed)
		netherGen, _ := NewNetherGenerators(seed)
		endGen, _ := NewEndGenerators(seed)

		owChunk := owGen(0, 0)
		netherChunk := netherGen(0, 0)
		endChunk := endGen(0, 0)

		owHash := computeChunkExactHash64(owChunk)
		netherHash := computeChunkExactHash64(netherChunk)
		endHash := computeChunkExactHash64(endChunk)

		// Re-run fresh generators for the same seed and verify bit-identical hashes.
		if got := computeChunkExactHash64(NewVanillaRegionGenerator(seed)(0, 0)); got != owHash {
			t.Fatalf("seed %d Overworld (0,0) hash non-deterministic: %#016x vs %#016x", seed, got, owHash)
		}
		ng2, _ := NewNetherGenerators(seed)
		if got := computeChunkExactHash64(ng2(0, 0)); got != netherHash {
			t.Fatalf("seed %d Nether (0,0) hash non-deterministic: %#016x vs %#016x", seed, got, netherHash)
		}
		eg2, _ := NewEndGenerators(seed)
		if got := computeChunkExactHash64(eg2(0, 0)); got != endHash {
			t.Fatalf("seed %d End (0,0) hash non-deterministic: %#016x vs %#016x", seed, got, endHash)
		}

		// Also verify an outer End island chunk and a non-origin Nether chunk.
		netherChunk2 := netherGen(3, -2)
		endOuterChunk := endGen(65, 0)
		if computeChunkExactHash64(netherChunk2) == 0 || computeChunkExactHash64(endOuterChunk) == 0 {
			t.Fatalf("seed %d produced zero hash on secondary chunks", seed)
		}

		t.Logf("seed=%d Overworld(0,0)=%#016x Nether(0,0)=%#016x End(0,0)=%#016x",
			seed, owHash, netherHash, endHash)
		seen[seed] = dimHashRecord{owHash: owHash, netherHash: netherHash, endHash: endHash}
	}

	// Verify distinct seeds produce distinct terrain hashes in each dimension.
	if seen[12345].owHash == seen[4242].owHash || seen[12345].owHash == seen[987654321].owHash {
		t.Fatalf("Overworld chunk hashes collided across different seeds: %+v", seen)
	}
	if seen[12345].netherHash == seen[4242].netherHash || seen[12345].netherHash == seen[987654321].netherHash {
		t.Fatalf("Nether chunk hashes collided across different seeds: %+v", seen)
	}
	if seen[12345].endHash == seen[4242].endHash || seen[12345].endHash == seen[987654321].endHash {
		t.Fatalf("End chunk hashes collided across different seeds: %+v", seen)
	}

	// Pin golden hashes for seed 12345, 4242, and 987654321 across all 3 dimensions.
	golden := map[int64]dimHashRecord{
		12345: {
			owHash:     0x3e5cbdddf8f7bb78,
			netherHash: 0x0f64cc8cffc6e943,
			endHash:    0x254e853d69aec5a7,
		},
		4242: {
			owHash:     0xb4863dca2d9e2297,
			netherHash: 0x3b28d2511e85d50d,
			endHash:    0xd89c29997f90299a,
		},
		987654321: {
			owHash:     0xa109ee630f63f90c,
			netherHash: 0x6b26a2c82dd0d673,
			endHash:    0x13a7090db6345330,
		},
	}
	for seed, want := range golden {
		got := seen[seed]
		if got.owHash != want.owHash {
			t.Errorf("seed %d Overworld (0,0) hash = %#016x, want %#016x", seed, got.owHash, want.owHash)
		}
		if got.netherHash != want.netherHash {
			t.Errorf("seed %d Nether (0,0) hash = %#016x, want %#016x", seed, got.netherHash, want.netherHash)
		}
		if got.endHash != want.endHash {
			t.Errorf("seed %d End (0,0) hash = %#016x, want %#016x", seed, got.endHash, want.endHash)
		}
	}
}

// TestGenerationOrderAndMultithreadedDeterminism verifies that generating
// adjacent chunks in forward order, reverse order, spiral order, and concurrently
// across multiple goroutines produces identical block/biome/heightmap hashes and
// identical sky/block lighting across Overworld, Nether, and End.
func TestGenerationOrderAndMultithreadedDeterminism(t *testing.T) {
	const seed = int64(12345)
	coords := [][2]int32{{0, 0}, {1, 0}, {0, 1}, {1, 1}}
	reversed := [][2]int32{{1, 1}, {0, 1}, {1, 0}, {0, 0}}
	permuted := [][2]int32{{0, 1}, {1, 1}, {0, 0}, {1, 0}}

	checkDimension := func(t *testing.T, name string, makeGen func() Generator) {
		t.Helper()
		runOrder := func(order [][2]int32) map[[2]int32]uint64 {
			gen := makeGen()
			out := make(map[[2]int32]uint64, len(order))
			for _, key := range order {
				out[key] = computeChunkExactHash64(gen(key[0], key[1]))
			}
			return out
		}

		fwd := runOrder(coords)
		rev := runOrder(reversed)
		perm := runOrder(permuted)

		// Concurrent multi-goroutine generation + lighting on a shared Cache.
		cacheFwd := NewCache(64, makeGen())
		for _, key := range coords {
			if _, err := cacheFwd.FrameErr(key[0], key[1]); err != nil {
				t.Fatalf("%s cacheFwd FrameErr(%v): %v", name, key, err)
			}
		}

		cacheConc := NewCache(64, makeGen())
		var wg sync.WaitGroup
		for _, key := range reversed {
			key := key
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := cacheConc.FrameErr(key[0], key[1]); err != nil {
					t.Errorf("%s cacheConc FrameErr(%v): %v", name, key, err)
				}
			}()
		}
		wg.Wait()

		for _, key := range coords {
			if fwd[key] != rev[key] {
				t.Errorf("%s chunk %v forward hash %#016x != reverse hash %#016x", name, key, fwd[key], rev[key])
			}
			if fwd[key] != perm[key] {
				t.Errorf("%s chunk %v forward hash %#016x != permuted hash %#016x", name, key, fwd[key], perm[key])
			}
			chFwd := cacheFwd.chunkAt(key[0], key[1])
			chConc := cacheConc.chunkAt(key[0], key[1])
			if h1, h2 := computeChunkExactHash64(chFwd), computeChunkExactHash64(chConc); h1 != fwd[key] || h2 != fwd[key] {
				t.Errorf("%s chunk %v cache hash mismatch: fwd=%#016x conc=%#016x want=%#016x", name, key, h1, h2, fwd[key])
			}
			if l1, l2 := computeLightChecksum64(chFwd), computeLightChecksum64(chConc); l1 != l2 {
				t.Errorf("%s chunk %v light checksum mismatch between sequential and concurrent generation: %#016x vs %#016x", name, key, l1, l2)
			}
		}
	}

	t.Run("Overworld", func(t *testing.T) {
		checkDimension(t, "Overworld", func() Generator { return NewVanillaRegionGenerator(seed) })
	})
	t.Run("Nether", func(t *testing.T) {
		checkDimension(t, "Nether", func() Generator {
			g, _ := NewNetherGenerators(seed)
			return g
		})
	})
	t.Run("End", func(t *testing.T) {
		checkDimension(t, "End", func() Generator {
			g, _ := NewEndGenerators(seed)
			return g
		})
	})
}

// TestLocateStructureAndBiomeCommandsAndFillBiomePlace verifies LocateNearestStructure
// and LocateNearestBiome across Overworld, Nether, and End (including tags and
// explorer map targets), plus FillBiomeVolume and PlaceConfiguredFeatureAt.
func TestLocateStructureAndBiomeCommandsAndFillBiomePlace(t *testing.T) {
	const seed = int64(12345)
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}
	owOD, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		t.Fatalf("LoadOverworldFinalDensity: %v", err)
	}
	netherOD, err := worldgen.LoadNetherFinalDensity(seed)
	if err != nil {
		t.Fatalf("LoadNetherFinalDensity: %v", err)
	}
	endOD, err := worldgen.LoadEndFinalDensity(seed)
	if err != nil {
		t.Fatalf("LoadEndFinalDensity: %v", err)
	}

	// 1. /locate structure in Overworld, Nether, and End (including explorer map tags and strongholds).
	structureCases := []struct {
		name      string
		od        *worldgen.OverworldDensity
		target    string
		originX   int
		originZ   int
		maxRadius int
	}{
		{"Overworld_VillageTag", owOD, "#minecraft:village", 0, 0, 16},
		{"Overworld_MonumentExplorerTag", owOD, "#minecraft:on_ocean_explorer_maps", 0, 0, 20},
		{"Overworld_MansionExplorerTag", owOD, "#minecraft:on_woodland_explorer_maps", 0, 0, 20},
		{"Overworld_TrialChambersExplorerTag", owOD, "#minecraft:on_trial_chambers_maps", 0, 0, 16},
		{"Overworld_BuriedTreasureTag", owOD, "#minecraft:on_treasure_maps", 0, 0, 48},
		{"Overworld_StrongholdConcentric", owOD, "#minecraft:eye_of_ender_located", 0, 0, 16},
		{"Overworld_PillagerOutpost", owOD, "minecraft:pillager_outpost", 0, 0, 20},
		{"Overworld_Mineshaft", owOD, "#minecraft:mineshaft", 0, 0, 10},
		{"Overworld_RuinedPortal", owOD, "#minecraft:ruined_portal", 0, 0, 10},
		{"Nether_Fortress", netherOD, "minecraft:fortress", 0, 0, 12},
		{"Nether_BastionRemnant", netherOD, "minecraft:bastion_remnant", 0, 0, 12},
		{"End_EndCity", endOD, "minecraft:end_city", 1200, 0, 20},
	}

	for _, tc := range structureCases {
		fx, fy, fz, structID, ok := LocateNearestStructure(tc.od, sets, seed, tc.target, tc.originX, tc.originZ, tc.maxRadius)
		if !ok {
			t.Errorf("%s: LocateNearestStructure(%q) failed to find a start within %d regions", tc.name, tc.target, tc.maxRadius)
			continue
		}
		// Verify determinism: repeating the query returns the exact same coordinates.
		fx2, fy2, fz2, structID2, ok2 := LocateNearestStructure(tc.od, sets, seed, tc.target, tc.originX, tc.originZ, tc.maxRadius)
		if !ok2 || fx != fx2 || fy != fy2 || fz != fz2 || structID != structID2 {
			t.Errorf("%s: LocateNearestStructure non-deterministic: (%d,%d,%d,%s) vs (%d,%d,%d,%s)",
				tc.name, fx, fy, fz, structID, fx2, fy2, fz2, structID2)
		}
		t.Logf("LocateNearestStructure %s (%s) -> %s at [%d, %d, %d]", tc.name, tc.target, structID, fx, fy, fz)
	}

	// 2. /locate biome in Overworld, Nether, and End (including biome tags).
	biomeCases := []struct {
		name    string
		od      *worldgen.OverworldDensity
		target  string
		originX int
		originY int
		originZ int
	}{
		{"Overworld_Plains", owOD, "minecraft:plains", 0, 64, 0},
		{"Overworld_CherryGrove", owOD, "minecraft:cherry_grove", 0, 100, 0},
		{"Overworld_DeepDark", owOD, "minecraft:deep_dark", 0, -32, 0},
		{"Overworld_TagBadlands", owOD, "#minecraft:is_badlands", 0, 64, 0},
		{"Overworld_TagOcean", owOD, "#minecraft:is_ocean", 0, 64, 0},
		{"Nether_CrimsonForest", netherOD, "minecraft:crimson_forest", 0, 64, 0},
		{"Nether_SoulSandValley", netherOD, "minecraft:soul_sand_valley", 0, 64, 0},
		{"Nether_WarpedForest", netherOD, "minecraft:warped_forest", 0, 64, 0},
		{"End_TheEnd", endOD, "minecraft:the_end", 0, 64, 0},
		{"End_EndHighlands", endOD, "minecraft:end_highlands", 1200, 64, 0},
	}

	for _, tc := range biomeCases {
		bx, by, bz, bName, ok := LocateNearestBiome(tc.od, nil, sets, tc.target, tc.originX, tc.originY, tc.originZ, 6400, 32, 64)
		if !ok {
			t.Errorf("%s: LocateNearestBiome(%q) not found within 6400 blocks", tc.name, tc.target)
			continue
		}
		t.Logf("LocateNearestBiome %s (%s) -> %s at [%d, %d, %d]", tc.name, tc.target, bName, bx, by, bz)
	}

	// 3. /fillbiome + /locate biome on live Cache + /place feature.
	cache := NewCache(32, GenerateFlat)
	cherryID := biomeIDByName("minecraft:cherry_grove")
	cells := cache.FillBiomeVolume(0, 64, 0, 15, 79, 15, cherryID, nil)
	if cells != 4*4*4 {
		t.Fatalf("FillBiomeVolume(16x16x16) updated %d cells, want 64", cells)
	}
	bx, by, bz, bName, ok := LocateNearestBiome(nil, cache, sets, "minecraft:cherry_grove", 0, 64, 0, 128, 4, 4)
	if !ok || bName != "minecraft:cherry_grove" || bx != 0 || by != 64 || bz != 0 {
		t.Fatalf("LocateNearestBiome after FillBiomeVolume = (%d,%d,%d,%q,%v), want (0,64,0,minecraft:cherry_grove,true)",
			bx, by, bz, bName, ok)
	}

	// Place bonus_chest via PlaceConfiguredFeatureAt (/place feature minecraft:bonus_chest).
	placed, err := cache.PlaceConfiguredFeatureAt(seed, "minecraft:bonus_chest", 8, FlatSurfaceY+1, 8)
	if err != nil || !placed {
		t.Fatalf("PlaceConfiguredFeatureAt(bonus_chest): placed=%v err=%v", placed, err)
	}
}

// TestBiomeSpawnTablesPlayerSpawnAndHeightmaps2612 verifies:
//   - Biome spawn tables (spawners, spawn_costs, creature_spawn_probability) for all 65 biomes
//   - Overworld spawn_target climate search (Climate$SpawnFinder) and PlayerSpawnFinder
//   - All 6 heightmap types (WORLD_SURFACE, WORLD_SURFACE_WG, OCEAN_FLOOR, OCEAN_FLOOR_WG,
//     MOTION_BLOCKING, MOTION_BLOCKING_NO_LEAVES)
func TestBiomeSpawnTablesPlayerSpawnAndHeightmaps2612(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}
	if len(set.Biomes) != 65 {
		t.Fatalf("biome count = %d, want 65 (vanilla 26.1.2)", len(set.Biomes))
	}

	// 1. Verify creature_spawn_probability and spawn_costs against vanilla 26.1.2 values.
	probChecks := map[string]float64{
		"minecraft:plains":          0.1,
		"minecraft:badlands":        0.03,
		"minecraft:eroded_badlands": 0.03,
		"minecraft:wooded_badlands": 0.04,
		"minecraft:ice_spikes":      0.07,
		"minecraft:snowy_plains":    0.07,
	}
	for bName, wantProb := range probChecks {
		bg, ok := BiomeSpawnSettingsByName(bName)
		if !ok {
			t.Fatalf("missing biome %s", bName)
		}
		if got := bg.CreatureProbability(); got != wantProb {
			t.Errorf("%s CreatureProbability() = %v, want %v", bName, got, wantProb)
		}
	}

	ssv, ok := BiomeSpawnSettingsByName("minecraft:soul_sand_valley")
	if !ok {
		t.Fatal("missing soul_sand_valley")
	}
	for _, mob := range []string{"minecraft:skeleton", "minecraft:ghast", "minecraft:enderman", "minecraft:strider"} {
		cost, hasCost := ssv.MobSpawnCost(mob)
		if !hasCost || cost.Charge != 0.7 || cost.EnergyBudget != 0.15 {
			t.Errorf("soul_sand_valley MobSpawnCost(%s) = %+v (%v), want charge=0.7 energy_budget=0.15", mob, cost, hasCost)
		}
	}
	wf, ok := BiomeSpawnSettingsByName("minecraft:warped_forest")
	if !ok {
		t.Fatal("missing warped_forest")
	}
	if cost, hasCost := wf.MobSpawnCost("minecraft:enderman"); !hasCost || cost.Charge != 1.0 || cost.EnergyBudget != 0.12 {
		t.Errorf("warped_forest MobSpawnCost(enderman) = %+v (%v), want charge=1.0 energy_budget=0.12", cost, hasCost)
	}
	rng := worldgen.NewLegacy(12345)
	if spawner, ok := ssv.SelectSpawner("monster", rng); !ok || spawner.Type == "" || spawner.MinCount <= 0 {
		t.Errorf("soul_sand_valley SelectSpawner(monster) = %+v, %v", spawner, ok)
	}

	// 2. Verify Overworld spawn_target and Climate$SpawnFinder + FindPlayerWorldSpawn.
	for _, seed := range []int64{12345, 4242, 987654321} {
		od, err := worldgen.LoadOverworldFinalDensity(seed)
		if err != nil {
			t.Fatalf("LoadOverworldFinalDensity(%d): %v", seed, err)
		}
		if len(od.SpawnTarget) != 2 {
			t.Fatalf("seed %d: len(od.SpawnTarget) = %d, want 2", seed, len(od.SpawnTarget))
		}
		tx, tz, fit := od.FindSpawnTarget()
		tx2, tz2, fit2 := od.FindSpawnTarget()
		if tx != tx2 || tz != tz2 || fit != fit2 {
			t.Fatalf("seed %d: FindSpawnTarget non-deterministic", seed)
		}
		t.Logf("seed %d: Climate.SpawnFinder target = (%d, %d) fitness=%d", seed, tx, tz, fit)
	}

	od12345, _ := worldgen.LoadOverworldFinalDensity(12345)
	flatCache := NewCache(16, GenerateFlat)
	sx, sy, sz, ok := FindPlayerWorldSpawn(od12345, flatCache, 10, 0)
	if !ok || sy != FlatSurfaceY+1 {
		t.Fatalf("FindPlayerWorldSpawn on flat cache = (%d, %d, %d, %v), want sy=%d, true", sx, sy, sz, ok, FlatSurfaceY+1)
	}

	// 3. Verify all 6 heightmap types on Chunk.HeightAt (WORLD_SURFACE, WORLD_SURFACE_WG,
	// OCEAN_FLOOR, OCEAN_FLOOR_WG, MOTION_BLOCKING, MOTION_BLOCKING_NO_LEAVES).
	ch := NewChunk(0, 0, BiomePlains)
	// Column (4, 4): stone at 50, oak_leaves at 52, dandelion at 53.
	ch.SetBlock(4, 50, 4, StateStone)
	ch.SetBlock(4, 52, 4, StateOakLeaf)
	dandelion, _ := nameToStateID("minecraft:dandelion", nil)
	ch.SetBlock(4, 53, 4, dandelion)
	// Column (5, 5): stone ocean floor at 50, water at 51..52.
	ch.SetBlock(5, 50, 5, StateStone)
	ch.SetBlock(5, 51, 5, StateWater)
	ch.SetBlock(5, 52, 5, StateWater)

	if got := ch.HeightAt("WORLD_SURFACE", 4, 4); got != 54 {
		t.Errorf("WORLD_SURFACE(4,4) = %d, want 54 (above dandelion)", got)
	}
	if got := ch.HeightAt("WORLD_SURFACE_WG", 4, 4); got != 54 {
		t.Errorf("WORLD_SURFACE_WG(4,4) = %d, want 54", got)
	}
	if got := ch.HeightAt("MOTION_BLOCKING", 4, 4); got != 53 {
		t.Errorf("MOTION_BLOCKING(4,4) = %d, want 53 (above oak_leaves)", got)
	}
	if got := ch.HeightAt("MOTION_BLOCKING_NO_LEAVES", 4, 4); got != 51 {
		t.Errorf("MOTION_BLOCKING_NO_LEAVES(4,4) = %d, want 51 (above stone)", got)
	}
	if got := ch.HeightAt("MOTION_BLOCKING", 5, 5); got != 53 {
		t.Errorf("MOTION_BLOCKING(5,5) = %d, want 53 (above water)", got)
	}
	if got := ch.HeightAt("OCEAN_FLOOR", 5, 5); got != 51 {
		t.Errorf("OCEAN_FLOOR(5,5) = %d, want 51 (above stone under water)", got)
	}
	if got := ch.HeightAt("OCEAN_FLOOR_WG", 5, 5); got != 51 {
		t.Errorf("OCEAN_FLOOR_WG(5,5) = %d, want 51 (above stone under water)", got)
	}
}
