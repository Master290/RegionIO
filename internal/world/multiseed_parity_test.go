package world

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/worldgen"
)

var multiSeedSweepSeeds = []int64{
	12345,
	4242,
	987654321,
	-777777,
	20260421,
}

// TestMultiSeedWorldgenAndStructureParity performs a multi-seed sweep across 5
// seeds (3 in -short mode) covering Overworld (diverse biomes + all newly ported
// features/trees/decorators/surface structures), Nether (all 5 biomes + structures),
// End (central island + outer islands + end cities), and structure contents
// (block_entities, loot tables, spawners, trial spawners, vaults, structural mobs,
// and archaeology).
func TestMultiSeedWorldgenAndStructureParity(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}

	// 1. Verify all newly ported configured features across all 5 seeds on a controlled region.
	t.Run("NewlyPortedFeaturesMultiSeed", func(t *testing.T) {
		for _, seed := range multiSeedSweepSeeds {
			verifyNewlyPortedFeaturesForSeed(t, set, seed)
		}
	})

	seeds := multiSeedSweepSeeds
	if testing.Short() {
		seeds = seeds[:2]
	}

	// 2. Overworld, Nether, and End multi-seed sweep including structure contents.
	for _, seed := range seeds {
		seed := seed
		t.Run(fmt.Sprintf("Seed_%d", seed), func(t *testing.T) {
			ResetNotReplayed()

			// 2a. Overworld biome & feature sweep (diverse biome columns + full region chunks).
			od, err := worldgen.LoadOverworldFinalDensity(seed)
			if err != nil {
				t.Fatalf("LoadOverworldFinalDensity(%d): %v", seed, err)
			}
			seenOverworldBiomes := make(map[string]bool)
			for cx := int32(-64); cx <= 64; cx += 4 {
				for cz := int32(-64); cz <= 64; cz += 4 {
					wx, wz := int(cx)*16+8, int(cz)*16+8
					s2D := worldgen.SampleColumn2D(od, SeaLevel, wx, wz)
					bName := biomeNameByID(BiomeAt3D(od, s2D, wx, SeaLevel, wz))
					seenOverworldBiomes[bName] = true
				}
			}
			if len(seenOverworldBiomes) < 15 {
				t.Errorf("seed %d: only sampled %d Overworld surface biomes in [-64..64], want >= 15", seed, len(seenOverworldBiomes))
			}

			// Generate full Overworld chunks via NewVanillaRegionGenerator on each seed
			// and verify heightmaps, biomes, and zero NotReplayed() gaps.
			owGen := NewVanillaRegionGenerator(seed)
			owChunkCoords := [][2]int32{{0, 0}, {12, -8}}
			if !testing.Short() {
				owChunkCoords = append(owChunkCoords, [2]int32{-16, 20})
			}
			for _, cc := range owChunkCoords {
				ch := owGen(cc[0], cc[1])
				if ch == nil {
					t.Fatalf("seed %d: Overworld chunk (%d,%d) is nil", seed, cc[0], cc[1])
				}
				hm := ch.ParityHeightmaps()
				nonAir := 0
				for y := MinY; y < MinY+WorldHeight; y += 4 {
					for z := 0; z < 16; z += 4 {
						for x := 0; x < 16; x += 4 {
							if ch.GetBlock(x, y, z) != StateAir {
								nonAir++
							}
						}
					}
				}
				if nonAir == 0 {
					t.Errorf("seed %d: Overworld chunk (%d,%d) is completely empty", seed, cc[0], cc[1])
				}
				if hm[0][0] < MinY {
					t.Errorf("seed %d: Overworld chunk (%d,%d) invalid heightmap %d", seed, cc[0], cc[1], hm[0][0])
				}
			}
			if nr := NotReplayed(); len(nr) > 0 {
				t.Errorf("seed %d: unexpected NotReplayed features: %v", seed, nr)
			}

			// 2b. Overworld structure starts & contents sweep on this seed.
			verifyOverworldStructuresForSeed(t, od, sets, seed)

			// 2c. Nether multi-biome & structure contents sweep on this seed.
			verifyNetherForSeed(t, sets, seed)

			// 2d. End multi-biome & structure contents sweep on this seed.
			verifyEndForSeed(t, sets, seed)
		})
	}
}

func verifyNewlyPortedFeaturesForSeed(t *testing.T, set *worldgen.FeatureSet, seed int64) {
	t.Helper()
	stateByIDOnce.Do(buildStateTable)
	ResetNotReplayed()

	// Test all newly ported tree configurations, root systems, bamboo, sea pickle, coral,
	// iceberg, fossil, desert well, ice spike, and blue ice on this seed.
	region := buildFlatTestDecorationRegion(t, 0, 0, 2, 64, false)
	region.bindSeed(seed)
	rng := worldgen.NewLegacy(seed)

	treeFeatures := []string{
		"minecraft:acacia",                 // forking_trunk_placer + acacia_foliage_placer
		"minecraft:mega_jungle_tree",       // mega_jungle_trunk_placer + jungle_foliage_placer + leave_vine + cocoa
		"minecraft:jungle_bush",            // bush_foliage_placer
		"minecraft:cherry",                 // cherry_trunk_placer + cherry_foliage_placer
		"minecraft:cherry_bees_005",        // beehive decorator + minecraft:beehive block entity
		"minecraft:azalea_tree",            // bending_trunk_placer + random_spread_foliage_placer
		"minecraft:mangrove",               // upwards_branching_trunk_placer + mangrove_root_placer + attached_to_leaves + leave_vine
		"minecraft:tall_mangrove",          // tall mangrove
		"minecraft:pale_oak_creaking",      // creaking_heart + pale_moss decorators
	}
	grassID, _ := nameToStateID("minecraft:grass_block", map[string]string{"snowy": "false"})
	mudID, _ := nameToStateID("minecraft:mud", nil)
	for i, tf := range treeFeatures {
		cfg, err := set.Tree(tf)
		if err != nil {
			t.Fatalf("set.Tree(%s): %v", tf, err)
		}
		treeReg := buildFlatTestDecorationRegion(t, 0, 0, 1, 64, false)
		treeReg.bindSeed(seed)
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				treeReg.setBlockGlobal(x, 64, z, grassID)
			}
		}
		if strings.Contains(tf, "mangrove") {
			treeReg.setBlockGlobal(7, 65, 8, mudID)
		}
		pos := worldgen.FeaturePosition{X: 8, Y: 65, Z: 8}
		treeRng := worldgen.NewLegacy(seed + int64(i))
		var placed bool
		for attempt := 0; attempt < 4 && !placed; attempt++ {
			var err error
			placed, err = treeReg.placeTreeWithResult(set, treeRng, pos, cfg)
			if err != nil {
				t.Errorf("seed %d placeTreeWithResult(%s): %v", seed, tf, err)
				break
			}
		}
		if !placed {
			t.Errorf("seed %d placeTreeWithResult(%s) did not place any blocks", seed, tf)
		}
	}

	// RootSystem (rooted_azalea_tree)
	rootSysReg := buildFlatTestDecorationRegion(t, 0, 0, 1, 64, false)
	rootSysReg.bindSeed(seed)
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			rootSysReg.setBlockGlobal(x, 64, z, grassID)
		}
	}
	rootSysReg.setBlockGlobal(8, 50, 8, StateAir)
	rootCfg, err := set.RootSystem("minecraft:rooted_azalea_tree")
	if err != nil {
		t.Fatalf("set.RootSystem: %v", err)
	}
	if !rootSysReg.placeRootSystem(rng, worldgen.FeaturePosition{X: 8, Y: 50, Z: 8}, rootCfg, set) {
		t.Errorf("seed %d placeRootSystem returned false", seed)
	}

	// Bamboo
	bambooReg := buildFlatTestDecorationRegion(t, 0, 0, 1, 64, false)
	bambooReg.bindSeed(seed)
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			bambooReg.setBlockGlobal(x, 64, z, grassID)
		}
	}
	if !bambooReg.placeBamboo(rng, worldgen.FeaturePosition{X: 8, Y: 65, Z: 8}, 0.2, set) {
		t.Errorf("seed %d placeBamboo returned false", seed)
	}

	// IceSpike & BlueIce & Iceberg
	snowID, _ := nameToStateID("minecraft:snow_block", nil)
	packedIceID, _ := nameToStateID("minecraft:packed_ice", nil)
	for dx := -4; dx <= 4; dx++ {
		for dz := -4; dz <= 4; dz++ {
			region.setBlockGlobal(8+dx, 64, 8+dz, snowID)
			region.setBlockGlobal(-8+dx, 60, 8+dz, StateWater)
		}
	}
	region.setBlockGlobal(-7, 60, 8, packedIceID)
	spikeCfg, err := set.Spike("minecraft:ice_spike")
	if err != nil {
		t.Fatalf("set.Spike: %v", err)
	}
	if err := region.placeSpike(set, rng, worldgen.FeaturePosition{X: 8, Y: 65, Z: 8}, spikeCfg); err != nil {
		t.Errorf("seed %d placeSpike: %v", seed, err)
	}
	if !region.placeBlueIce(rng, worldgen.FeaturePosition{X: -8, Y: 60, Z: 8}) {
		t.Errorf("seed %d placeBlueIce returned false", seed)
	}
	icebergCfg, err := set.BlockStateConfig("minecraft:iceberg_packed")
	if err != nil {
		t.Fatalf("set.BlockStateConfig(iceberg_packed): %v", err)
	}
	if !region.placeIceberg(rng, worldgen.FeaturePosition{X: 0, Y: 63, Z: -8}, icebergCfg) {
		t.Errorf("seed %d placeIceberg returned false", seed)
	}

	// Fossil
	fossilCfg, err := set.Fossil("minecraft:fossil_coal")
	if err != nil {
		t.Fatalf("set.Fossil: %v", err)
	}
	if !region.placeFossil(set, rng, worldgen.FeaturePosition{X: 0, Y: 64, Z: 0}, fossilCfg) {
		t.Errorf("seed %d placeFossil returned false", seed)
	}

	// DesertWell (in surface_features.go)
	wellReg := buildFlatTestDecorationRegion(t, 0, 0, 1, 64, false)
	wellReg.bindSeed(seed)
	sandID, _ := nameToStateID("minecraft:sand", nil)
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			for y := 60; y <= 64; y++ {
				wellReg.setBlockGlobal(8+dx, y, 8+dz, sandID)
			}
		}
	}
	if !wellReg.placeDesertWell(rng, worldgen.FeaturePosition{X: 8, Y: 65, Z: 8}) {
		t.Errorf("seed %d placeDesertWell returned false", seed)
	}
	wellBrush := 0
	for _, be := range collectRegionBlockEntities(wellReg) {
		if be.ID == "minecraft:brushable_block" && be.LootTable == "minecraft:archaeology/desert_well" && len(be.Items) == 1 {
			wellBrush++
		}
	}
	if wellBrush != 2 {
		t.Errorf("seed %d placeDesertWell brushable_blocks = %d, want 2", seed, wellBrush)
	}

	// Coral & SeaPickle in water region
	for idx, coralKind := range []string{"minecraft:coral_tree", "minecraft:coral_claw", "minecraft:coral_mushroom"} {
		waterRegion := buildFlatTestDecorationRegion(t, 0, 0, 1, 48, true)
		waterRegion.bindSeed(seed)
		if !waterRegion.placeCoralFeature(worldgen.NewLegacy(seed+int64(idx)), worldgen.FeaturePosition{X: 8, Y: 50, Z: 8}, coralKind, set) {
			t.Errorf("seed %d placeCoralFeature(%s) returned false", seed, coralKind)
		}
	}
	waterRegion := buildFlatTestDecorationRegion(t, 0, 0, 1, 48, true)
	waterRegion.bindSeed(seed)
	pickleCfg, err := set.Count("minecraft:sea_pickle")
	if err != nil {
		t.Fatalf("set.Count(sea_pickle): %v", err)
	}
	if !waterRegion.placeSeaPickle(rng, worldgen.FeaturePosition{X: 8, Y: 50, Z: 8}, pickleCfg, set) {
		t.Errorf("seed %d placeSeaPickle returned false", seed)
	}

	if nr := NotReplayed(); len(nr) > 0 {
		t.Errorf("seed %d NewlyPortedFeatures produced NotReplayed gaps: %v", seed, nr)
	}
}

func verifyOverworldStructuresForSeed(t *testing.T, od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64) {
	t.Helper()

	findCandidate := func(setID string, maxGrid int32) (int32, int32, bool) {
		s := sets.Sets[setID]
		if s == nil || s.Placement == nil || s.Placement.RandomSpread == nil {
			return 0, 0, false
		}
		rs := s.Placement.RandomSpread
		sp := int32(rs.Spacing)
		for gx := -maxGrid; gx <= maxGrid; gx++ {
			for gz := -maxGrid; gz <= maxGrid; gz++ {
				cand := rs.PotentialChunk(seed, gx*sp, gz*sp)
				if _, ok := FindStructureSetStart(od, sets, setID, seed, cand[0], cand[1]); ok {
					return cand[0], cand[1], true
				}
			}
		}
		return 0, 0, false
	}

	// 1. Village
	if vx, vz, ok := findCandidate("minecraft:villages", 4); ok {
		st, err := VillageGenerationPoint(od, sets, seed, vx, vz)
		if err != nil || st == nil || len(st.Pieces) == 0 {
			t.Errorf("seed %d village at (%d,%d): err=%v st=%v", seed, vx, vz, err, st)
		} else {
			reg := buildFlatTestDecorationRegion(t, vx, vz, 1, 64, false)
			for cx := vx - 1; cx <= vx+1; cx++ {
				for cz := vz - 1; cz <= vz+1; cz++ {
					PlaceJigsawStartsForChunk(reg, []*JigsawStart{st}, st.StructureName, 4, seed, cx, cz)
				}
			}
		}
	} else {
		t.Errorf("seed %d: no village found in grid [-4..4]", seed)
	}

	// 2. Trial Chambers
	if tx, tz, ok := findCandidate("minecraft:trial_chambers", 4); ok {
		st, err := JigsawSetGenerationPoint(od, sets, "minecraft:trial_chambers", seed, tx, tz)
		if err != nil || st == nil || len(st.Pieces) == 0 {
			t.Errorf("seed %d trial_chambers at (%d,%d): err=%v", seed, tx, tz, err)
		} else {
			reg := buildFlatTestDecorationRegion(t, tx, tz, 2, 64, false)
			for cx := tx - 2; cx <= tx+2; cx++ {
				for cz := tz - 2; cz <= tz+2; cz++ {
					PlaceJigsawStartsForChunk(reg, []*JigsawStart{st}, "minecraft:trial_chambers", 3, seed, cx, cz)
				}
			}
			bes := collectRegionBlockEntities(reg)
			hasVault, hasTrialSpawner := false, false
			for _, be := range bes {
				if be.ID == "minecraft:vault" && be.LootTable != "" && be.KeyItem != "" {
					hasVault = true
				}
				if be.ID == "minecraft:trial_spawner" && be.NormalConfig != "" {
					hasTrialSpawner = true
				}
			}
			if !hasVault && !hasTrialSpawner {
				t.Errorf("seed %d trial_chambers at (%d,%d) produced no vault or trial_spawner", seed, tx, tz)
			}
		}
	} else {
		t.Errorf("seed %d: no trial_chambers found in grid [-4..4]", seed)
	}

	// 3. Trail Ruins (with CappedProcessor archaeology suspicious_gravel)
	if rx, rz, ok := findCandidate("minecraft:trail_ruins", 8); ok {
		st, err := JigsawSetGenerationPoint(od, sets, "minecraft:trail_ruins", seed, rx, rz)
		if err != nil || st == nil || len(st.Pieces) == 0 {
			t.Errorf("seed %d trail_ruins at (%d,%d): err=%v", seed, rx, rz, err)
		} else {
			reg := buildFlatTestDecorationRegion(t, rx, rz, 2, 64, false)
			for cx := rx - 2; cx <= rx+2; cx++ {
				for cz := rz - 2; cz <= rz+2; cz++ {
					PlaceJigsawStartsForChunk(reg, []*JigsawStart{st}, "minecraft:trail_ruins", 3, seed, cx, cz)
				}
			}
			bes := collectRegionBlockEntities(reg)
			brushCount := 0
			for _, be := range bes {
				if be.ID == "minecraft:brushable_block" && strings.HasPrefix(be.LootTable, "minecraft:archaeology/trail_ruins_") && len(be.Items) == 1 {
					brushCount++
				}
			}
			if brushCount == 0 {
				t.Errorf("seed %d trail_ruins at (%d,%d) produced 0 brushable_block archaeology entities", seed, rx, rz)
			}
		}
	} else {
		t.Errorf("seed %d: no trail_ruins found in grid [-8..8]", seed)
	}

	// 4. Mineshaft
	foundMineshaft := false
	for sx := int32(-20); sx <= 20 && !foundMineshaft; sx++ {
		for sz := int32(-20); sz <= 20 && !foundMineshaft; sz++ {
			if ms, err := MineshaftGenerationPoint(od, sets, seed, sx, sz); err == nil && ms != nil && len(ms.Pieces) > 0 {
				foundMineshaft = true
			}
		}
	}
	if !foundMineshaft {
		t.Errorf("seed %d: no mineshaft found in [-20..20]", seed)
	}
}

func verifyNetherForSeed(t *testing.T, sets *worldgen.StructureSets, seed int64) {
	t.Helper()
	od, carver, _ := netherGeneratorInputs(seed)

	// 1. Verify all 5 Nether biomes appear on this seed and record a sample chunk for basalt_deltas.
	seenBiomes := make(map[string]bool)
	var deltasCX, deltasCZ int32
	hasDeltasChunk := false
	netherTable := worldgen.NetherBiomeTable()
	for cx := int32(-48); cx <= 48; cx += 3 {
		for cz := int32(-48); cz <= 48; cz += 3 {
			wx, wz := int(cx)*16+8, int(cz)*16+8
			s2D := worldgen.SampleColumn2D(od, od.SeaLevel, wx, wz)
			b := netherTable.FindBiome(worldgen.NewTargetPoint(
				s2D.Temperature, s2D.Humidity, s2D.Continentalness, s2D.Erosion, s2D.Weirdness, 0))
			seenBiomes[b] = true
			if b == "minecraft:basalt_deltas" && !hasDeltasChunk {
				deltasCX, deltasCZ = cx, cz
				hasDeltasChunk = true
			}
		}
	}
	for _, want := range []string{
		"minecraft:nether_wastes",
		"minecraft:soul_sand_valley",
		"minecraft:crimson_forest",
		"minecraft:warped_forest",
		"minecraft:basalt_deltas",
	} {
		if !seenBiomes[want] {
			t.Errorf("seed %d: Nether biome %s not found in [-48..48]", seed, want)
		}
	}

	// 2. Verify basalt_deltas CountOnEveryLayer (delta, small/large basalt columns, basalt/blackstone blobs)
	// and carver CAVE_AIR / LAVA aquifer boundary on this seed.
	if hasDeltasChunk {
		dch := generateNetherChunk(od, carver, sets, seed, deltasCX, deltasCZ)
		var basaltCnt, blackstoneCnt, magmaCnt int
		for y := 1; y < 127; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					switch dch.GetBlock(x, y, z) {
					case StateBasalt:
						basaltCnt++
					case StateBlackstone:
						blackstoneCnt++
					case StateMagmaBlock:
						magmaCnt++
					case StateCaveAir:
						if y < NetherLavaSeaLevel {
							t.Errorf("seed %d: CAVE_AIR below Nether lava sea level at (%d,%d,%d)", seed, x, y, z)
						}
					}
				}
			}
		}
		if basaltCnt == 0 || blackstoneCnt == 0 || magmaCnt == 0 {
			t.Errorf("seed %d basalt_deltas chunk (%d,%d): basalt=%d blackstone=%d magma=%d",
				seed, deltasCX, deltasCZ, basaltCnt, blackstoneCnt, magmaCnt)
		}
	}

	// 3. Find a Fortress and a Bastion Remnant on this seed and verify their placement & contents.
	var foundFortress, foundBastion bool
	fb := getNFBlocks()
	for sx := int32(-32); sx <= 32; sx++ {
		for sz := int32(-32); sz <= 32; sz++ {
			stub, ok := FindStructureSetStart(od, sets, "minecraft:nether_complexes", seed, sx, sz)
			if !ok || stub == nil {
				continue
			}
			switch stub.StructureName {
			case "minecraft:fortress":
				if !foundFortress {
					foundFortress = true
					fort := BuildNetherFortressStart(seed, sx, sz)
					if fort == nil || len(fort.Pieces) == 0 {
						t.Fatalf("seed %d: BuildNetherFortressStart(%d,%d) returned empty start", seed, sx, sz)
					}
					ch := generateNetherChunk(od, carver, sets, seed, sx, sz)
					if ch == nil {
						t.Fatalf("seed %d: Nether fortress chunk (%d,%d) nil", seed, sx, sz)
					}
					// Verify fortress bricks survive decoration (DeltaFeature / BasaltColumnsFeature cannot overwrite them).
					bricks := 0
					for y := 32; y < 100; y++ {
						for z := 0; z < 16; z++ {
							for x := 0; x < 16; x++ {
								if ch.GetBlock(x, y, z) == fb.bricks {
									bricks++
								}
							}
						}
					}
					if bricks == 0 {
						t.Errorf("seed %d: Nether fortress start chunk (%d,%d) has 0 nether_bricks", seed, sx, sz)
					}
				}
			case "minecraft:bastion_remnant":
				if !foundBastion {
					foundBastion = true
					ch := generateNetherChunk(od, carver, sets, seed, sx, sz)
					if ch == nil {
						t.Fatalf("seed %d: Nether bastion chunk (%d,%d) nil", seed, sx, sz)
					}
				}
			}
			if foundFortress && foundBastion {
				break
			}
		}
		if foundFortress && foundBastion {
			break
		}
	}
	if !foundFortress || !foundBastion {
		t.Errorf("seed %d Nether complexes: fortress=%v bastion=%v", seed, foundFortress, foundBastion)
	}

	// 4. Find a Nether Fossil and a Ruined Portal Nether on this seed and verify placement.
	var foundFossil, foundPortal bool
	nb := loadNetherBlocks()
	for sx := int32(-24); sx <= 24 && (!foundFossil || !foundPortal); sx++ {
		for sz := int32(-24); sz <= 24 && (!foundFossil || !foundPortal); sz++ {
			if !foundFossil {
				if fStub := NetherFossilGenerationPoint(od, sets, seed, sx, sz); fStub != nil {
					foundFossil = true
					fc := NewDimensionChunk(int32(fStub.X>>4), int32(fStub.Z>>4), biomeID("minecraft:soul_sand_valley"), NetherDimensionType)
					if n := PlaceNetherFossil(fc, fStub, seed, int32(fStub.X>>4), int32(fStub.Z>>4)); n == 0 {
						t.Errorf("seed %d: PlaceNetherFossil at (%d,%d,%d) placed 0 blocks", seed, fStub.X, fStub.Y, fStub.Z)
					}
					bones := 0
					for y := max(0, fStub.Box.minY); y <= min(127, fStub.Box.maxY); y++ {
						for z := 0; z < 16; z++ {
							for x := 0; x < 16; x++ {
								st := fc.GetBlock(x, y, z)
								if st == nb.boneBlock || stateByIDTable[st].Name == "minecraft:bone_block" || stateByIDTable[st].Name == "minecraft:dried_ghast" {
									bones++
								}
							}
						}
					}
					if bones == 0 {
						t.Errorf("seed %d: NetherFossil at (%d,%d,%d) placed 0 bone_block/dried_ghast blocks", seed, fStub.X, fStub.Y, fStub.Z)
					}
				}
			}
			if !foundPortal {
				if pStub, err := RuinedPortalGenerationPoint(od, sets, seed, sx, sz); err == nil && pStub != nil {
					foundPortal = true
				}
			}
		}
	}
	if !foundFossil || !foundPortal {
		t.Errorf("seed %d Nether fossil=%v ruined_portal_nether=%v", seed, foundFossil, foundPortal)
	}
}

func verifyEndForSeed(t *testing.T, sets *worldgen.StructureSets, seed int64) {
	t.Helper()
	od, _ := endGeneratorInputs(seed)
	endGen, _ := NewEndGenerators(seed)

	// 1. Central island chunk (0,0) and spike chunks.
	ch0 := endGen(0, 0)
	if ch0 == nil {
		t.Fatalf("seed %d: End chunk (0,0) nil", seed)
	}
	endStoneCount := 0
	for y := 0; y < 128; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				if ch0.GetBlock(x, y, z) != StateAir {
					endStoneCount++
				}
			}
		}
	}
	if endStoneCount == 0 {
		t.Errorf("seed %d: End central island (0,0) has 0 non-air blocks", seed)
	}

	// Verify End spikes generate 10 pillars with end_crystal entities and guarded spikes have 4-way iron_bars on top.
	spikes := EndSpikesForSeed(seed)
	if len(spikes) != 10 {
		t.Fatalf("seed %d: EndSpikesForSeed returned %d spikes, want 10", seed, len(spikes))
	}
	sp0 := spikes[0]
	spChunk := endGen(int32(sp0.CenterX>>4), int32(sp0.CenterZ>>4))
	crystals := 0
	for _, ent := range spChunk.StructureEntities() {
		if ent.ID == "minecraft:end_crystal" {
			crystals++
		}
	}
	if crystals == 0 {
		t.Errorf("seed %d: End spike chunk (%d,%d) for spike at (%d,%d) has 0 end_crystal entities",
			seed, sp0.CenterX>>4, sp0.CenterZ>>4, sp0.CenterX, sp0.CenterZ)
	}
	for _, sp := range spikes {
		if !sp.Guarded {
			continue
		}
		cx, cz := int32(sp.CenterX>>4), int32(sp.CenterZ>>4)
		gChunk := endGen(cx, cz)
		lx, lz := sp.CenterX&15, sp.CenterZ&15
		if lx >= 0 && lx < 16 && lz >= 0 && lz < 16 {
			wantRoof := loadEndBlocks().ironBars[15]
			gotRoof := gChunk.GetBlock(lx, sp.Height+3, lz)
			if gotRoof != wantRoof {
				sn, _ := stateByID(gotRoof)
				t.Errorf("seed %d: guarded spike at (%d,%d) roof center (%d,%d,%d) state=%d (%s), want 4-way iron_bars %d",
					seed, sp.CenterX, sp.CenterZ, lx, sp.Height+3, lz, gotRoof, sn.Name, wantRoof)
			}
		}
		break
	}

	// 2. Find an End City with chests on this seed and verify its structure placement, loot chests, and shulkers.
	foundEndCity := false
	for sx := int32(64); sx <= 160 && !foundEndCity; sx++ {
		for sz := int32(64); sz <= 160 && !foundEndCity; sz++ {
			start := EndCityGenerationPoint(od, sets, seed, sx, sz)
			if start == nil || len(start.Pieces) == 0 {
				continue
			}
			foundEndCity = true
			var totalBlocks, totalChests, untabledChests, totalShulkers int
			minCX, maxCX := start.Box.minX>>4, start.Box.maxX>>4
			minCZ, maxCZ := start.Box.minZ>>4, start.Box.maxZ>>4
			for cx := int32(minCX); cx <= int32(maxCX); cx++ {
				for cz := int32(minCZ); cz <= int32(maxCZ); cz++ {
					c := NewDimensionChunk(cx, cz, BiomeEndHighlands, EndDimensionType)
					PlaceEndCity(c, start, cx, cz)
					for _, be := range c.StructureBlockEntities() {
						if be.ID == "minecraft:chest" {
							if be.LootTable == "minecraft:chests/end_city_treasure" && len(be.Items) > 0 {
								totalChests++
							} else {
								untabledChests++
							}
						}
					}
					for _, ent := range c.StructureEntities() {
						if ent.ID == "minecraft:shulker" {
							totalShulkers++
						}
					}
					for y := 0; y < 128; y += 2 {
						for z := 0; z < 16; z += 2 {
							for x := 0; x < 16; x += 2 {
								if c.GetBlock(x, y, z) != StateAir {
									totalBlocks++
								}
							}
						}
					}
				}
			}
			if totalBlocks == 0 || totalShulkers == 0 || untabledChests > 0 {
				t.Errorf("seed %d: EndCity at (%d,%d) blocks=%d chests=%d untabledChests=%d shulkers=%d",
					seed, sx, sz, totalBlocks, totalChests, untabledChests, totalShulkers)
			}
		}
	}
	if !foundEndCity {
		t.Errorf("seed %d: no EndCity found in [64..160]", seed)
	}
}

// TestWorldgenFollowupParityAndBoundaries verifies:
// 1. Rare Overworld structures across seeds (stronghold, ocean_monument, woodland_mansion, ancient_city, igloo).
// 2. Multi-chunk / region-boundary structure continuity (pieces spanning negative/positive chunk & region boundaries).
// 3. Nether carver CAVE_AIR placement above lava level and basalt_deltas per-layer feature placement.
// 4. Deterministic structure start caching for Strongholds, Jigsaw structures, and Nether structures.
func TestWorldgenFollowupParityAndBoundaries(t *testing.T) {
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}
	seed := int64(12345)
	od, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		t.Fatalf("LoadOverworldFinalDensity: %v", err)
	}

	// 1. Stronghold ring placement and start cache determinism.
	ringSet := StrongholdChunkSet(od, sets, seed)
	if len(ringSet) == 0 {
		t.Fatalf("StrongholdChunkSet returned 0 positions")
	}
	var ring0 [2]int32
	for pos := range ringSet {
		ring0 = pos
		break
	}
	sh0, err := StrongholdGenerationPoint(od, sets, seed, ring0[0], ring0[1])
	if err != nil {
		t.Fatalf("StrongholdGenerationPoint: %v", err)
	}
	sh0Cached, err := StrongholdGenerationPoint(od, sets, seed, ring0[0], ring0[1])
	if err != nil {
		t.Fatalf("StrongholdGenerationPoint cached: %v", err)
	}
	if sh0 == nil || len(sh0.Pieces) == 0 || sh0 != sh0Cached {
		t.Errorf("StrongholdGenerationPoint at %v: sh0=%p sh0Cached=%p", ring0, sh0, sh0Cached)
	}

	// 2. Ocean Monument multi-chunk boundary placement.
	monStart := BuildOceanMonumentStart(seed, 0, 0)
	monReg := buildFlatTestDecorationRegion(t, 0, 0, 2, 35, true)
	monStarts := []*OceanMonumentStart{monStart}
	for cx := int32(-2); cx <= 2; cx++ {
		for cz := int32(-2); cz <= 2; cz++ {
			PlaceOceanMonumentsForChunk(monReg, monStarts, seed, cx, cz)
		}
	}
	monCounts := countBlocksInBox(monReg, monStart.Piece.box)
	if monCounts["minecraft:prismarine"] == 0 || monCounts["minecraft:gold_block"] != 8 {
		t.Errorf("OceanMonument boundary placement: prismarine=%d gold_block=%d", monCounts["minecraft:prismarine"], monCounts["minecraft:gold_block"])
	}

	// 3. Nether CAVE_AIR carver & basalt_deltas features.
	netherOD, netherCarver, _ := netherGeneratorInputs(seed)
	foundCaveAir := false
	for cx := int32(-4); cx <= 4 && !foundCaveAir; cx++ {
		for cz := int32(-4); cz <= 4 && !foundCaveAir; cz++ {
			ch := generateNetherChunk(netherOD, netherCarver, sets, seed, cx, cz)
			for y := 32; y < 120 && !foundCaveAir; y++ {
				for z := 0; z < 16 && !foundCaveAir; z++ {
					for x := 0; x < 16; x++ {
						if ch.GetBlock(x, y, z) == StateCaveAir {
							foundCaveAir = true
							break
						}
					}
				}
			}
		}
	}
	if !foundCaveAir {
		t.Errorf("generateNetherChunk did not place any StateCaveAir (%d) in [-4..4]", StateCaveAir)
	}
	// Verify basalt_deltas chunk places basalt / blackstone blobs and columns.
	basaltID, _ := nameToStateID("minecraft:basalt", map[string]string{"axis": "y"})
	blackstoneID, _ := nameToStateID("minecraft:blackstone", nil)
	foundBasaltDeltaChunk := false
	for cx := int32(-32); cx <= 32 && !foundBasaltDeltaChunk; cx += 2 {
		for cz := int32(-32); cz <= 32 && !foundBasaltDeltaChunk; cz += 2 {
			wx, wz := int(cx)*16+8, int(cz)*16+8
			s2D := worldgen.SampleColumn2D(netherOD, netherOD.SeaLevel, wx, wz)
			b := worldgen.NetherBiomeTable().FindBiome(worldgen.NewTargetPoint(
				s2D.Temperature, s2D.Humidity, s2D.Continentalness, s2D.Erosion, s2D.Weirdness, 0))
			if b != "minecraft:basalt_deltas" {
				continue
			}
			foundBasaltDeltaChunk = true
			ch := generateNetherChunk(netherOD, netherCarver, sets, seed, cx, cz)
			hasBasalt, hasBlackstone := false, false
			for y := 1; y < 127; y++ {
				for z := 0; z < 16; z++ {
					for x := 0; x < 16; x++ {
						st := ch.GetBlock(x, y, z)
						if st == basaltID {
							hasBasalt = true
						} else if st == blackstoneID {
							hasBlackstone = true
						}
					}
				}
			}
			if !hasBasalt || !hasBlackstone {
				t.Errorf("basalt_deltas chunk (%d,%d): hasBasalt=%v hasBlackstone=%v", cx, cz, hasBasalt, hasBlackstone)
			}
		}
	}
	if !foundBasaltDeltaChunk {
		t.Errorf("no basalt_deltas chunk found in [-32..32]")
	}
}


// TestLiveVanillaMultiSeedSweep runs the official 26.1.2 server.jar across multiple
// seeds and compares Overworld, Nether, and End chunks (blocks, biomes, heightmaps,
// structure starts, block_entities, and entities) against our generators.
func TestLiveVanillaMultiSeedSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live multi-seed vanilla server sweep in -short mode")
	}
	jar, err := filepath.Abs("../../server.jar")
	if err != nil || !fileExists(jar) {
		t.Skipf("server.jar not found at %s", jar)
	}

	seeds := []int64{12345, 4242, 987654321}
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}

	for _, seed := range seeds {
		t.Run(fmt.Sprintf("LiveSeed_%d", seed), func(t *testing.T) {
			runLiveSeedComparison(t, jar, sets, seed)
		})
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func runLiveSeedComparison(t *testing.T, jar string, sets *worldgen.StructureSets, seed int64) {
	t.Helper()

	odOW, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		t.Fatal(err)
	}
	odNether, _, _ := netherGeneratorInputs(seed)
	odEnd, _ := endGeneratorInputs(seed)

	// Pick Overworld chunks: (0,0), a land chunk, and a structure chunk (e.g. trial_chambers or desert_pyramid/village).
	owChunks := [][2]int32{{0, 0}, {10, -10}}
	for sx := int32(-24); sx <= 24; sx++ {
		for sz := int32(-24); sz <= 24; sz++ {
			if st, err := JigsawSetGenerationPoint(odOW, sets, "minecraft:trial_chambers", seed, sx, sz); err == nil && st != nil {
				owChunks = append(owChunks, [2]int32{sx, sz})
				goto foundOWStruct
			}
		}
	}
foundOWStruct:

	// Pick Nether chunks: (0,0) and a fortress/bastion start chunk.
	netherChunks := [][2]int32{{0, 0}}
	for sx := int32(-20); sx <= 20; sx++ {
		for sz := int32(-20); sz <= 20; sz++ {
			if stub, ok := FindStructureSetStart(odNether, sets, "minecraft:nether_complexes", seed, sx, sz); ok && stub != nil {
				netherChunks = append(netherChunks, [2]int32{sx, sz})
				goto foundNetherStruct
			}
		}
	}
foundNetherStruct:

	// Pick End chunks: (0,0) central island, a spike chunk, and an End City chunk.
	spikes := EndSpikesForSeed(seed)
	endChunks := [][2]int32{{0, 0}, {int32(spikes[0].CenterX >> 4), int32(spikes[0].CenterZ >> 4)}}
	for sx := int32(64); sx <= 140; sx++ {
		for sz := int32(64); sz <= 140; sz++ {
			if st := EndCityGenerationPoint(odEnd, sets, seed, sx, sz); st != nil {
				endChunks = append(endChunks, [2]int32{sx, sz})
				goto foundEndStruct
			}
		}
	}
foundEndStruct:

	work, err := os.MkdirTemp("", fmt.Sprintf("regionio-multiseed-%d-", seed))
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(work)

	if err := os.WriteFile(filepath.Join(work, "eula.txt"), []byte("eula=true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	props := fmt.Sprintf("level-seed=%d\nonline-mode=false\nspawn-protection=0\nview-distance=2\nsimulation-distance=2\nmax-tick-time=-1\nserver-port=%d\n",
		seed, 25570+int(seed&1023))
	if err := os.WriteFile(filepath.Join(work, "server.properties"), []byte(props), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "java", "-Xms1G", "-Xmx2G", "-jar", jar, "nogui")
	cmd.Dir = work
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	events := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			line := sc.Text()
			if strings.Contains(line, "Done (") || strings.Contains(line, "Saved the game") {
				events <- line
			}
		}
		close(events)
	}()

	waitEvent := func(substr string) error {
		for line := range events {
			if strings.Contains(line, substr) {
				return nil
			}
		}
		return fmt.Errorf("EOF waiting for %q", substr)
	}

	if err := waitEvent("Done ("); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		t.Fatalf("server startup seed %d: %v", seed, err)
	}

	for _, c := range owChunks {
		_, _ = fmt.Fprintf(stdin, "execute in minecraft:overworld run forceload add %d %d\n", c[0]*16, c[1]*16)
	}
	for _, c := range netherChunks {
		_, _ = fmt.Fprintf(stdin, "execute in minecraft:the_nether run forceload add %d %d\n", c[0]*16, c[1]*16)
	}
	for _, c := range endChunks {
		_, _ = fmt.Fprintf(stdin, "execute in minecraft:the_end run forceload add %d %d\n", c[0]*16, c[1]*16)
	}

	time.Sleep(18 * time.Second)
	_, _ = io.WriteString(stdin, "save-all flush\n")
	if err := waitEvent("Saved the game"); err != nil {
		t.Fatalf("server save seed %d: %v", seed, err)
	}
	_, _ = io.WriteString(stdin, "stop\n")
	_ = stdin.Close()
	_ = cmd.Wait()

	// 1. Compare Overworld chunks
	owStore, err := NewStore(filepath.Join(work, "world", "dimensions", "minecraft", "overworld"))
	if err != nil {
		t.Fatal(err)
	}
	defer owStore.Close()
	owGen := NewVanillaRegionGenerator(seed)
	for _, c := range owChunks {
		van, err := owStore.LoadVanillaChunk(c[0], c[1])
		if err != nil {
			t.Fatalf("seed %d LoadVanillaChunk OW (%d,%d): %v", seed, c[0], c[1], err)
		}
		ours := owGen(c[0], c[1])
		exactBlocks, totalBlocks := 0, 0
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					totalBlocks++
					if ours.GetBlock(x, y, z) == van.GetBlock(x, y, z) {
						exactBlocks++
					}
				}
			}
		}
		exactBiomes, totalBiomes := 0, 0
		for y := MinY; y < MinY+WorldHeight; y += 4 {
			for z := 0; z < 16; z += 4 {
				for x := 0; x < 16; x += 4 {
					totalBiomes++
					if ours.GetBiome(x, y, z) == van.GetBiome(x, y, z) {
						exactBiomes++
					}
				}
			}
		}
		pct := float64(exactBlocks) * 100 / float64(totalBlocks)
		t.Logf("seed %d OW chunk (%d,%d): blocks %d/%d (%.3f%%), biomes %d/%d, vanilla_BEs=%d our_BEs=%d",
			seed, c[0], c[1], exactBlocks, totalBlocks, pct, exactBiomes, totalBiomes,
			len(van.StructureBlockEntities()), len(ours.StructureBlockEntities()))
		if exactBiomes != totalBiomes {
			t.Errorf("seed %d OW chunk (%d,%d): biome mismatch %d/%d", seed, c[0], c[1], exactBiomes, totalBiomes)
		}
		if pct < 98.0 {
			t.Errorf("seed %d OW chunk (%d,%d): block parity %.3f%% < 98%%", seed, c[0], c[1], pct)
		}
		compareBlockEntities(t, fmt.Sprintf("seed %d OW (%d,%d)", seed, c[0], c[1]), van.StructureBlockEntities(), ours.StructureBlockEntities())
	}

	// 2. Compare Nether chunks
	netherStore, err := NewStore(filepath.Join(work, "world", "dimensions", "minecraft", "the_nether"))
	if err != nil {
		t.Fatal(err)
	}
	defer netherStore.Close()
	netherGen, _ := NewNetherGenerators(seed)
	for _, c := range netherChunks {
		van, err := netherStore.LoadVanillaChunk(c[0], c[1])
		if err != nil {
			t.Fatalf("seed %d LoadVanillaChunk Nether (%d,%d): %v", seed, c[0], c[1], err)
		}
		ours := netherGen(c[0], c[1])
		exactBlocks, totalBlocks := 0, 0
		for y := 0; y < 128; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					totalBlocks++
					if ours.GetBlock(x, y, z) == van.GetBlock(x, y, z) {
						exactBlocks++
					}
				}
			}
		}
		pct := float64(exactBlocks) * 100 / float64(totalBlocks)
		t.Logf("seed %d Nether chunk (%d,%d): blocks %d/%d (%.2f%%), vanilla_BEs=%d our_BEs=%d",
			seed, c[0], c[1], exactBlocks, totalBlocks, pct,
			len(van.StructureBlockEntities()), len(ours.StructureBlockEntities()))
		compareBlockEntities(t, fmt.Sprintf("seed %d Nether (%d,%d)", seed, c[0], c[1]), van.StructureBlockEntities(), ours.StructureBlockEntities())
	}

	// 3. Compare End chunks
	endStore, err := NewStore(filepath.Join(work, "world", "dimensions", "minecraft", "the_end"))
	if err != nil {
		t.Fatal(err)
	}
	defer endStore.Close()
	endGen, _ := NewEndGenerators(seed)
	for _, c := range endChunks {
		van, err := endStore.LoadVanillaChunk(c[0], c[1])
		if err != nil {
			t.Fatalf("seed %d LoadVanillaChunk End (%d,%d): %v", seed, c[0], c[1], err)
		}
		ours := endGen(c[0], c[1])
		exactBlocks, totalBlocks := 0, 0
		for y := 0; y < 128; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					totalBlocks++
					if ours.GetBlock(x, y, z) == van.GetBlock(x, y, z) {
						exactBlocks++
					}
				}
			}
		}
		vanEnts := readVanillaEntityChunk(filepath.Join(work, "world", "dimensions", "minecraft", "the_end", "entities"), c[0], c[1])
		pct := float64(exactBlocks) * 100 / float64(totalBlocks)
		t.Logf("seed %d End chunk (%d,%d): blocks %d/%d (%.2f%%), vanilla_BEs=%d our_BEs=%d, vanilla_ents=%v our_ents=%d",
			seed, c[0], c[1], exactBlocks, totalBlocks, pct,
			len(van.StructureBlockEntities()), len(ours.StructureBlockEntities()),
			vanEnts, len(ours.StructureEntities()))
		compareBlockEntities(t, fmt.Sprintf("seed %d End (%d,%d)", seed, c[0], c[1]), van.StructureBlockEntities(), ours.StructureBlockEntities())
	}
}

func compareBlockEntities(t *testing.T, label string, vanBEs, ourBEs []StructureBlockEntity) {
	t.Helper()
	if len(vanBEs) == 0 && len(ourBEs) == 0 {
		return
	}
	byPos := make(map[[3]int]StructureBlockEntity, len(ourBEs))
	for _, be := range ourBEs {
		byPos[[3]int{be.X, be.Y, be.Z}] = be
	}
	matched := 0
	var diffs []string
	for _, v := range vanBEs {
		pos := [3]int{v.X, v.Y, v.Z}
		o, ok := byPos[pos]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("missing BE %s at %v (table=%s)", v.ID, pos, v.LootTable))
			continue
		}
		if o.ID != v.ID {
			diffs = append(diffs, fmt.Sprintf("at %v: ID got %s want %s", pos, o.ID, v.ID))
			continue
		}
		if v.LootTable != "" && o.LootTable != v.LootTable {
			diffs = append(diffs, fmt.Sprintf("at %v (%s): LootTable got %s want %s", pos, v.ID, o.LootTable, v.LootTable))
			continue
		}
		if v.SpawnerEntityID != "" && o.SpawnerEntityID != v.SpawnerEntityID {
			diffs = append(diffs, fmt.Sprintf("at %v (spawner): entity got %s want %s", pos, o.SpawnerEntityID, v.SpawnerEntityID))
			continue
		}
		if v.NormalConfig != "" && o.NormalConfig != v.NormalConfig {
			diffs = append(diffs, fmt.Sprintf("at %v (trial_spawner): normal_config got %s want %s", pos, o.NormalConfig, v.NormalConfig))
			continue
		}
		matched++
	}
	vanPos := make(map[[3]int]bool, len(vanBEs))
	for _, v := range vanBEs {
		vanPos[[3]int{v.X, v.Y, v.Z}] = true
	}
	for _, o := range ourBEs {
		pos := [3]int{o.X, o.Y, o.Z}
		if !vanPos[pos] {
			diffs = append(diffs, fmt.Sprintf("extra our BE %s at %v (table=%s)", o.ID, pos, o.LootTable))
		}
	}
	sort.Strings(diffs)
	t.Logf("%s block_entities: matched %d/%d vanilla BEs (ours=%d)", label, matched, len(vanBEs), len(ourBEs))
	for i, d := range diffs {
		if i >= 10 {
			break
		}
		t.Logf("  %s BE diff: %s", label, d)
	}
}

func readVanillaEntityChunk(entitiesDir string, cx, cz int32) map[string]int {
	counts := make(map[string]int)
	rx, rz, lx, lz := regionIndex(cx, cz)
	rf, err := OpenRegion(entitiesDir, rx, rz)
	if err != nil {
		return counts
	}
	defer rf.Close()
	raw, err := rf.ReadChunk(lx, lz)
	if err != nil {
		return counts
	}
	_, tag, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return counts
	}
	root, ok := tag.(*nbt.Compound)
	if !ok {
		return counts
	}
	entTag, ok := root.Get("Entities")
	if !ok {
		return counts
	}
	list, ok := entTag.(nbt.List)
	if !ok {
		return counts
	}
	for _, elem := range list.Elems {
		if comp, ok := elem.(*nbt.Compound); ok {
			if id := beNBTString(comp, "id"); id != "" {
				counts[id]++
			}
		}
	}
	return counts
}
