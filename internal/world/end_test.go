package world

import (
	"strings"
	"testing"

	"regionio/internal/worldgen"
)

func TestEndDensityAndBiomes(t *testing.T) {
	const seed = int64(12345)
	od, err := worldgen.LoadEndFinalDensity(seed)
	if err != nil {
		t.Fatalf("LoadEndFinalDensity: %v", err)
	}
	if od.MinY != 0 || od.Height != 128 {
		t.Fatalf("End bounds = (%d, %d), want (0, 128)", od.MinY, od.Height)
	}
	if od.Erosion == nil {
		t.Fatal("expected End erosion density function to be loaded")
	}

	// Central island at (0, 56, 0) should be solid (density > 0).
	if d := od.Final.Compute(worldgen.FunctionContext{X: 0, Y: 56, Z: 0}); d <= 0 {
		t.Fatalf("expected solid density at (0, 56, 0), got %v", d)
	}
	// Void ring between main island and outer islands at (500, 56, 0) should be air (density < 0).
	if d := od.Final.Compute(worldgen.FunctionContext{X: 500, Y: 56, Z: 0}); d >= 0 {
		t.Fatalf("expected negative density in void ring at (500, 56, 0), got %v", d)
	}

	// Within 64 chunks of origin (sectionX^2 + sectionZ^2 <= 4096), biome must be minecraft:the_end.
	if b := worldgen.EndBiomeAt(od.Erosion, 0, 0, 0); b != "minecraft:the_end" {
		t.Fatalf("EndBiomeAt(0,0,0) = %q, want minecraft:the_end", b)
	}
	if b := worldgen.EndBiomeAt(od.Erosion, 64*4, 0, 0); b != "minecraft:the_end" {
		t.Fatalf("EndBiomeAt(64*4,0,0) = %q, want minecraft:the_end", b)
	}

	// Scan outer End region and verify all 4 outer End biomes appear.
	seen := make(map[string]int)
	for cx := 65; cx < 140; cx++ {
		for cz := 0; cz < 80; cz++ {
			b := worldgen.EndBiomeAt(od.Erosion, cx*4, 0, cz*4)
			seen[b]++
		}
	}
	for _, want := range []string{
		"minecraft:end_highlands",
		"minecraft:end_midlands",
		"minecraft:small_end_islands",
		"minecraft:end_barrens",
	} {
		if seen[want] == 0 {
			t.Errorf("expected outer End biome %q to appear, seen=%v", want, seen)
		}
	}
}

func TestEndBaseTerrainAndFeatures(t *testing.T) {
	eb := loadEndBlocks()

	// 1. Central island chunk (0, 0).
	c0 := GenerateEnd(0, 0)
	if c0.GetBiome(0, 60, 0) != BiomeTheEnd {
		t.Fatalf("chunk (0,0) biome = %d, want BiomeTheEnd (%d)", c0.GetBiome(0, 60, 0), BiomeTheEnd)
	}
	foundEndStone := false
	for y := 40; y < 75; y++ {
		if c0.GetBlock(8, y, 8) == eb.endStone {
			foundEndStone = true
			break
		}
	}
	if !foundEndStone {
		t.Fatal("expected end_stone in central End island chunk (0, 0)")
	}

	// 2. Obsidian spawn platform at (100, 48, 0) in chunk (6, 0).
	cPlatform := GenerateEnd(6, 0)
	for dx := -2; dx <= 2; dx++ {
		for dz := 0; dz <= 2; dz++ {
			lx := (100 + dx) - 6*16
			lz := dz
			if got := cPlatform.GetBlock(lx, EndPlatformFloorY, lz); got != eb.obsidian {
				t.Fatalf("platform block at (%d, %d, %d) = %d, want obsidian (%d)", 100+dx, EndPlatformFloorY, dz, got, eb.obsidian)
			}
			for y := EndSpawnY; y <= EndSpawnY+2; y++ {
				if got := cPlatform.GetBlock(lx, y, lz); got != StateAir {
					t.Fatalf("above platform at (%d, %d, %d) = %d, want air", 100+dx, y, dz, got)
				}
			}
		}
	}

	// 3. Obsidian spikes (10 pillars, 2 guarded with iron bars).
	spikes := EndSpikesForSeed(0)
	guardedCount := 0
	for i, s := range spikes {
		if s.Radius < 2 || s.Radius > 5 {
			t.Fatalf("spike[%d] radius = %d, want 2..5", i, s.Radius)
		}
		if s.Height < 76 || s.Height > 103 {
			t.Fatalf("spike[%d] height = %d, want 76..103", i, s.Height)
		}
		if s.Guarded {
			guardedCount++
		}
	}
	if guardedCount != 2 {
		t.Fatalf("guarded spike count = %d, want 2", guardedCount)
	}
	// Generate the chunk containing a guarded spike and verify obsidian, iron_bars, bedrock, and fire.
	var guarded EndSpike
	for _, s := range spikes {
		if s.Guarded {
			guarded = s
			break
		}
	}
	scx, scz := int32(guarded.CenterX>>4), int32(guarded.CenterZ>>4)
	cSpike := GenerateEnd(scx, scz)
	slx, slz := guarded.CenterX-int(scx)*16, guarded.CenterZ-int(scz)*16
	if got := cSpike.GetBlock(slx, guarded.Height-1, slz); got != eb.obsidian {
		t.Fatalf("spike column at (%d, %d, %d) = %d, want obsidian", guarded.CenterX, guarded.Height-1, guarded.CenterZ, got)
	}
	if got := cSpike.GetBlock(slx, guarded.Height, slz); got != StateBedrock {
		t.Fatalf("spike cap at (%d, %d, %d) = %d, want bedrock", guarded.CenterX, guarded.Height, guarded.CenterZ, got)
	}
	if got := cSpike.GetBlock(slx, guarded.Height+1, slz); got != eb.fire {
		t.Fatalf("spike fire at (%d, %d, %d) = %d, want fire", guarded.CenterX, guarded.Height+1, guarded.CenterZ, got)
	}
	if got := BlockName(cSpike.GetBlock(slx, guarded.Height+3, slz)); got != "minecraft:iron_bars" {
		t.Fatalf("guarded spike cage roof at (%d, %d, %d) = %q, want minecraft:iron_bars", guarded.CenterX, guarded.Height+3, guarded.CenterZ, got)
	}

	// 4. End island feature, End gateway feature, and Chorus plant feature.
	featChunk := NewDimensionChunk(0, 0, BiomeEndHighlands, EndDimensionType)
	r := worldgen.NewWorldgenRandom(42)
	PlaceEndIsland(featChunk, 8, 60, 8, r)
	if featChunk.GetBlock(8, 60, 8) != eb.endStone {
		t.Fatal("expected PlaceEndIsland to place end_stone at origin")
	}

	PlaceEndGateway(featChunk, 4, 80, 4)
	if featChunk.GetBlock(4, 80, 4) != eb.endGateway {
		t.Fatal("expected PlaceEndGateway to place end_gateway at center")
	}
	if featChunk.GetBlock(4, 82, 4) != StateBedrock || featChunk.GetBlock(4, 78, 4) != StateBedrock {
		t.Fatal("expected PlaceEndGateway to place bedrock caps at dy=+-2")
	}

	// Place Chorus plant on top of the end_stone island at (8, 61, 8).
	if !PlaceChorusPlant(featChunk, 8, 61, 8, r) {
		t.Fatal("expected PlaceChorusPlant to succeed on end_stone")
	}
	if BlockName(featChunk.GetBlock(8, 61, 8)) != "minecraft:chorus_plant" {
		t.Fatalf("expected chorus_plant at (8, 61, 8), got %q", BlockName(featChunk.GetBlock(8, 61, 8)))
	}
	foundFlower := false
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			for y := 61; y < 90; y++ {
				if featChunk.GetBlock(lx, y, lz) == eb.chorusFlower[5] {
					foundFlower = true
					break
				}
			}
		}
	}
	if !foundFlower {
		t.Fatal("expected PlaceChorusPlant to place dead chorus_flower[age=5] on branch tips")
	}
}

func TestEndCityGenerationAndPlacement(t *testing.T) {
	const seed = int64(12345)
	od, sets := endGeneratorInputs(seed)
	eb := loadEndBlocks()

	// Find a valid End City start in the outer End islands.
	var foundStart *EndCityStart
	rs := sets.Sets["minecraft:end_cities"].Placement.RandomSpread
	spacing := int32(rs.Spacing)
	for gx := int32(4); gx <= 15 && foundStart == nil; gx++ {
		for gz := int32(-10); gz <= 10 && foundStart == nil; gz++ {
			cand := rs.PotentialChunk(seed, gx*spacing, gz*spacing)
			if start := EndCityGenerationPoint(od, sets, seed, cand[0], cand[1]); start != nil {
				foundStart = start
			}
		}
	}
	if foundStart == nil {
		t.Fatal("expected to find a valid End City start in outer End islands")
	}
	if len(foundStart.Pieces) < 4 {
		t.Fatalf("expected End City to have at least 4 pieces, got %d", len(foundStart.Pieces))
	}
	if foundStart.Pieces[0].Name != "base_floor" {
		t.Fatalf("expected first piece to be base_floor, got %q", foundStart.Pieces[0].Name)
	}

	// Verify FindStructureSetStart agrees on the End City start chunk.
	stub, ok := FindStructureSetStart(od, sets, "minecraft:end_cities", seed, foundStart.ChunkX, foundStart.ChunkZ)
	if !ok || stub == nil || stub.StructureName != "minecraft:end_city" {
		t.Fatalf("FindStructureSetStart(end_cities, %d, %d) = (%v, %v), want valid end_city", foundStart.ChunkX, foundStart.ChunkZ, stub, ok)
	}

	// Render the End City across its entire bounding box and verify purpur / end_stone_bricks
	// appear and no structure_block / structure_void markers leak into the world.
	purpurOrBricks := 0
	for cx := int32(foundStart.Box.minX >> 4); cx <= int32(foundStart.Box.maxX>>4); cx++ {
		for cz := int32(foundStart.Box.minZ >> 4); cz <= int32(foundStart.Box.maxZ>>4); cz++ {
			c := NewDimensionChunk(cx, cz, BiomeEndHighlands, EndDimensionType)
			PlaceEndCity(c, foundStart, cx, cz)
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					for y := 0; y < EndDimensionType.Height; y++ {
						st := c.GetBlock(lx, y, lz)
						if st == eb.purpurBlock || st == eb.purpurPillar || st == eb.endStoneBricks {
							purpurOrBricks++
						}
						name := BlockName(st)
						if name == "minecraft:structure_block" || name == "minecraft:structure_void" || name == "minecraft:jigsaw" {
							t.Fatalf("leaked marker block %s in End City chunk (%d, %d) at (%d, %d, %d)", name, cx, cz, lx, y, lz)
						}
					}
				}
			}
		}
	}
	if purpurOrBricks == 0 {
		t.Fatalf("expected End City at chunk (%d, %d) to place purpur/end_stone_bricks blocks", foundStart.ChunkX, foundStart.ChunkZ)
	}
}

func TestEndHeightmapsBiomesAndDeterminism(t *testing.T) {
	const seed int64 = 12345
	gen1 := NewEndGenerator(seed)
	gen2 := NewEndGenerator(seed)

	// 1. Void ring chunk (30, 0) between central island and outer islands.
	cVoid := gen1(30, 0)
	hmVoid := cVoid.ParityHeightmaps()
	for idx := 0; idx < 256; idx++ {
		if hmVoid[0][idx] != -1 || hmVoid[1][idx] != -1 || hmVoid[2][idx] != -1 {
			t.Fatalf("End void chunk heightmaps at idx %d = (%d, %d, %d), want -1", idx, hmVoid[0][idx], hmVoid[1][idx], hmVoid[2][idx])
		}
	}
	regVoid, err := newDecorationRegion([]*Chunk{cVoid})
	if err != nil {
		t.Fatalf("newDecorationRegion(cVoid): %v", err)
	}
	regVoid.bindSeed(seed)
	if h := regVoid.heightAt("WORLD_SURFACE_WG", 30*16+8, 8); h != 0 {
		t.Fatalf("WORLD_SURFACE_WG on empty End column = %d, want 0 (End minY)", h)
	}
	if h := regVoid.heightAt("WORLD_SURFACE", 30*16+8, 8); h != 0 {
		t.Fatalf("WORLD_SURFACE on empty End column = %d, want 0 (End minY)", h)
	}
	if bID, ok := regVoid.getBiome(30*16+8, 0, 8); !ok || bID != BiomeTheEnd {
		t.Fatalf("getBiome on End chunk (30,0) = (%d, %v), want (%d, true)", bID, ok, BiomeTheEnd)
	}

	// 2. Central island chunk (0, 0) and outer island chunk (70, 10) determinism & heightmaps.
	for _, coord := range [][2]int32{{0, 0}, {70, 10}} {
		cx, cz := coord[0], coord[1]
		c1 := gen1(cx, cz)
		c2 := gen2(cx, cz)
		hm1 := c1.ParityHeightmaps()
		hm2 := c2.ParityHeightmaps()
		if hm1 != hm2 {
			t.Fatalf("non-deterministic End heightmaps at (%d, %d)", cx, cz)
		}
		for y := 0; y < EndDimensionType.Height; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					if got, want := c1.GetBlock(x, y, z), c2.GetBlock(x, y, z); got != want {
						t.Fatalf("non-deterministic End block at (%d,%d) (%d,%d,%d): %d != %d", cx, cz, x, y, z, got, want)
					}
				}
			}
		}
		for y := 0; y < EndDimensionType.Height; y += biomeCellSize {
			for z := 0; z < 16; z += biomeCellSize {
				for x := 0; x < 16; x += biomeCellSize {
					if b1, b2 := c1.GetBiome(x, y, z), c2.GetBiome(x, y, z); b1 != b2 {
						t.Fatalf("non-deterministic End biome at (%d,%d) (%d,%d,%d): %d != %d", cx, cz, x, y, z, b1, b2)
					}
				}
			}
		}
		if len(c1.Encode()) == 0 {
			t.Fatalf("expected non-empty wire encoding for End chunk (%d, %d)", cx, cz)
		}
	}
}

func TestEndCrossChunkIslandsAndChorusPlants(t *testing.T) {
	eb := loadEndBlocks()

	crossedIntoC1 := false
	for s := int64(1); s < 100; s++ {
		c0 := NewDimensionChunk(0, 0, BiomeEndHighlands, EndDimensionType)
		c1 := NewDimensionChunk(1, 0, BiomeEndHighlands, EndDimensionType)
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				c0.SetBlock(x, 60, z, eb.endStone)
				c1.SetBlock(x, 60, z, eb.endStone)
			}
		}
		// Place an end_stone block at (14, 61, 8) next to chorus root (15, 61, 8).
		c0.SetBlock(14, 61, 8, eb.endStone)

		env0 := newEndDecorationEnv(c0, nil)
		env1 := newEndDecorationEnv(c1, nil)
		for z := 0; z < 16; z++ {
			env0.setBlock(16, 60, z, eb.endStone)
			env1.setBlock(15, 60, z, eb.endStone)
		}
		env1.setBlock(14, 61, 8, eb.endStone)

		r0 := worldgen.NewWorldgenRandom(s)
		r1 := worldgen.NewWorldgenRandom(s)
		placeChorusPlantInEnv(env0, 15, 61, 8, r0)
		placeChorusPlantInEnv(env1, 15, 61, 8, r1)

		// Root at (15, 61, 8) has end_stone to the west (14, 61, 8), so west must be true.
		rootSt := c0.GetBlock(15, 61, 8)
		if info, ok := stateByID(rootSt); !ok || info.Name != "minecraft:chorus_plant" || info.Properties["west"] != "true" || info.Properties["down"] != "true" {
			t.Fatalf("root chorus_plant at (15,61,8) state = %+v, want west=true down=true", info)
		}

		for lx := 0; lx < 8; lx++ {
			for lz := 0; lz < 16; lz++ {
				for y := 61; y < 85; y++ {
					if BlockName(c1.GetBlock(lx, y, lz)) == "minecraft:chorus_plant" || BlockName(c1.GetBlock(lx, y, lz)) == "minecraft:chorus_flower" {
						crossedIntoC1 = true
						break
					}
				}
			}
		}
		if crossedIntoC1 {
			break
		}
	}
	if !crossedIntoC1 {
		t.Fatal("expected chorus_plant rooted at x=15 in chunk (0,0) to branch across border into chunk (1,0)")
	}
}

func TestEndCityLootSeedStabilityAndEnderChestPreservation(t *testing.T) {
	const seed int64 = 12345
	var city *EndCityStart
	for s := int64(1); s < 200; s++ {
		rng := worldgen.NewLegacy(s)
		cand := BuildEndCityStart(0, 0, [3]int{7, 65, 7}, int(s&3), rng)
		if cand == nil {
			continue
		}
		cand.Seed = seed
		hasShip := false
		hasThirdFloor := false
		for _, p := range cand.Pieces {
			if p.Name == "ship" {
				hasShip = true
			}
			if p.Name == "third_floor_2" || p.Name == "fat_tower_top" {
				hasThirdFloor = true
			}
		}
		if hasShip && hasThirdFloor {
			city = cand
			break
		}
	}
	if city == nil {
		t.Fatal("failed to find EndCityStart with ship and third_floor_2/fat_tower_top")
	}

	minCX := int32(city.Box.minX >> 4)
	maxCX := int32(city.Box.maxX >> 4)
	minCZ := int32(city.Box.minZ >> 4)
	maxCZ := int32(city.Box.maxZ >> 4)

	var coords [][2]int32
	for cx := minCX; cx <= maxCX; cx++ {
		for cz := minCZ; cz <= maxCZ; cz++ {
			coords = append(coords, [2]int32{cx, cz})
		}
	}

	placeOrder := func(reverse bool) (map[[3]int]StructureBlockEntity, int) {
		order := append([][2]int32(nil), coords...)
		if reverse {
			for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
				order[i], order[j] = order[j], order[i]
			}
		}
		bes := make(map[[3]int]StructureBlockEntity)
		enderChests := 0
		for _, c := range order {
			ch := NewDimensionChunk(c[0], c[1], BiomeEndHighlands, EndDimensionType)
			PlaceEndCity(ch, city, c[0], c[1])
			for _, be := range ch.StructureBlockEntities() {
				bes[[3]int{be.X, be.Y, be.Z}] = be
				if be.ID == "minecraft:ender_chest" {
					enderChests++
					if be.LootTable != "" {
						t.Errorf("ender_chest at (%d,%d,%d) was assigned LootTable %q", be.X, be.Y, be.Z, be.LootTable)
					}
				}
			}
		}
		return bes, enderChests
	}

	fwd, enderFwd := placeOrder(false)
	rev, enderRev := placeOrder(true)
	if enderFwd == 0 || enderFwd != enderRev {
		t.Fatalf("expected preserved ender_chest block entities in End City, got fwd=%d rev=%d", enderFwd, enderRev)
	}
	if len(fwd) != len(rev) {
		t.Fatalf("End City block entity count mismatch: fwd=%d rev=%d", len(fwd), len(rev))
	}
	for pos, a := range fwd {
		b, ok := rev[pos]
		if !ok || a.ID != b.ID || a.LootTable != b.LootTable || a.LootTableSeed != b.LootTableSeed {
			t.Errorf("End City BE at %v mismatch across chunk order: %+v vs %+v", pos, a, b)
		}
	}
}

func TestEndGatewayExitReturnHooksAndEndPodium(t *testing.T) {
	const seed int64 = 12345
	od, _ := endGeneratorInputs(seed)
	eb := loadEndBlocks()

	// 1. Gateway block entity round-trip through Anvil NBT.
	ch := NewDimensionChunk(0, 0, BiomeEndHighlands, EndDimensionType)
	PlaceEndGateway(ch, 8, 75, 8)
	if ch.GetBlock(8, 75, 8) != eb.endGateway {
		t.Fatal("expected minecraft:end_gateway block at (8, 75, 8)")
	}
	be, ok := ch.StructureBlockEntityAt(8, 75, 8)
	if !ok {
		t.Fatal("expected StructureBlockEntity at (8, 75, 8) for end_gateway")
	}
	exitPos, exact, hasExit := ParseEndGatewayBlockEntity(be)
	if !hasExit || !exact || exitPos != EndSpawnPointPos {
		t.Fatalf("ParseEndGatewayBlockEntity = (%v, exact=%v, hasExit=%v), want (%v, true, true)", exitPos, exact, hasExit, EndSpawnPointPos)
	}
	root := chunkToNBT(ch)
	loaded, err := nbtToChunk(root, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("nbtToChunk: %v", err)
	}
	loadedBE, ok := loaded.StructureBlockEntityAt(8, 75, 8)
	if !ok {
		t.Fatal("missing round-tripped end_gateway block entity")
	}
	if gotExit, gotExact, gotHas := ParseEndGatewayBlockEntity(loadedBE); !gotHas || !gotExact || gotExit != EndSpawnPointPos {
		t.Fatalf("round-trip ParseEndGatewayBlockEntity = (%v, %v, %v)", gotExit, gotExact, gotHas)
	}

	// 2. Ring gateway order and outer-island target resolution.
	order := EndRingGatewayOrder(seed)
	seenSlots := make(map[int]bool, 20)
	for _, slot := range order {
		if slot < 0 || slot >= 20 || seenSlots[slot] {
			t.Fatalf("invalid EndRingGatewayOrder: %v", order)
		}
		seenSlots[slot] = true
	}
	ringPos := EndRingGatewayPos(order[0])
	if ringPos[1] != 75 {
		t.Fatalf("EndRingGatewayPos Y = %d, want 75", ringPos[1])
	}
	landing, returnGW, found := EndGatewayExitTarget(od, ringPos[0], ringPos[2])
	if !found {
		t.Fatalf("EndGatewayExitTarget failed for ring gateway at %v", ringPos)
	}
	if returnGW[1] != landing[1]+9 || landing[1] < 31 {
		t.Fatalf("unexpected EndGatewayExitTarget heights: landing=%v returnGW=%v", landing, returnGW)
	}

	// 3. Central End Podium (inactive and active) across the 4 chunks around (0, y, 0).
	podiumOrigin := EndPodiumOrigin(od)
	if podiumOrigin[0] != 0 || podiumOrigin[2] != 0 || podiumOrigin[1] < 48 {
		t.Fatalf("EndPodiumOrigin = %v", podiumOrigin)
	}
	portalBlocks := 0
	wallTorches := 0
	for _, c := range [][2]int32{{-1, -1}, {0, -1}, {-1, 0}, {0, 0}} {
		pch := NewDimensionChunk(c[0], c[1], BiomeTheEnd, EndDimensionType)
		if !PlaceEndPodium(pch, podiumOrigin[0], podiumOrigin[1], podiumOrigin[2], true) {
			t.Fatalf("expected PlaceEndPodium to intersect chunk (%d, %d)", c[0], c[1])
		}
		for lx := 0; lx < 16; lx++ {
			for lz := 0; lz < 16; lz++ {
				for y := podiumOrigin[1] - 1; y <= podiumOrigin[1]+4; y++ {
					st := pch.GetBlock(lx, y, lz)
					if st == eb.endPortal {
						portalBlocks++
					}
					if BlockName(st) == "minecraft:wall_torch" {
						wallTorches++
					}
				}
			}
		}
	}
	if portalBlocks == 0 {
		t.Errorf("active PlaceEndPodium placed 0 end_portal blocks")
	}
	if wallTorches != 4 {
		t.Errorf("PlaceEndPodium placed %d wall_torch blocks, want 4", wallTorches)
	}
}

func computeChunkExactHash64(c *Chunk) uint64 {
	const (
		fnvOffset64 uint64 = 14695981039346656037
		fnvPrime64  uint64 = 1099511628211
	)
	h := fnvOffset64
	mix := func(v uint64) {
		h ^= v
		h *= fnvPrime64
	}
	mix(uint64(uint32(c.X)))
	mix(uint64(uint32(c.Z)))
	minY := c.minY()
	height := c.worldHeight()
	for y := minY; y < minY+height; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				mix(uint64(c.GetBlock(x, y, z)))
			}
		}
	}
	for y := minY; y < minY+height; y += biomeCellSize {
		for z := 0; z < 16; z += biomeCellSize {
			for x := 0; x < 16; x += biomeCellSize {
				mix(uint64(c.GetBiome(x, y, z)))
			}
		}
	}
	hm := c.ParityHeightmaps()
	for kind := 0; kind < len(hm); kind++ {
		for i := 0; i < 256; i++ {
			mix(uint64(uint32(hm[kind][i])))
		}
	}
	return h
}

func TestBiomeIDsAndChunkBlockHashesParitySeed12345(t *testing.T) {
	const seed int64 = 12345

	owOD, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		t.Fatal(err)
	}
	netherOD, netherCarver, netherSets := netherGeneratorInputs(seed)
	endOD, endSets := endGeneratorInputs(seed)

	// 1. Control point Biome ID checks across Overworld, Nether, and End on seed 12345.
	t.Run("BiomeIDControlPoints", func(t *testing.T) {
		owControlPoints := []struct {
			x, y, z   int
			wantBiome string
		}{
			{x: -16*16 + 8, y: SeaLevel + 4, z: 16*16 + 8, wantBiome: "minecraft:forest"},
			{x: -140*16 + 8, y: SeaLevel + 4, z: 28*16 + 8, wantBiome: "minecraft:desert"},
			{x: 8, y: 0, z: 8, wantBiome: "minecraft:lush_caves"},
			{x: 34*16 + 8, y: 0, z: -34*16 + 8, wantBiome: "minecraft:dripstone_caves"},
			{x: 4*16 + 8, y: -40, z: -4*16 + 8, wantBiome: "minecraft:deep_dark"},
			{x: -102*16 + 8, y: SeaLevel + 2, z: -92*16 + 8, wantBiome: "minecraft:mangrove_swamp"},
			{x: -26*16 + 8, y: 100, z: 48*16 + 8, wantBiome: "minecraft:cherry_grove"},
			{x: 16*16 + 8, y: SeaLevel + 4, z: -40*16 + 8, wantBiome: "minecraft:taiga"},
			{x: -40*16 + 8, y: SeaLevel + 4, z: 20*16 + 8, wantBiome: "minecraft:plains"},
		}
		for _, pt := range owControlPoints {
			s2D := worldgen.SampleColumn2D(owOD, pt.y, pt.x, pt.z)
			id := BiomeAt3D(owOD, s2D, pt.x, pt.y, pt.z)
			got := biomeNameByID(id)
			if got != pt.wantBiome || id != biomeID(pt.wantBiome) {
				t.Fatalf("Overworld biome at (%d,%d,%d) = %s (id=%d), want %s (id=%d)",
					pt.x, pt.y, pt.z, got, id, pt.wantBiome, biomeID(pt.wantBiome))
			}
		}

		// Locate and pin control points for all 5 Nether biomes on seed 12345.
		netherTable := worldgen.NetherBiomeTable()
		wantNetherBiomes := []string{
			"minecraft:nether_wastes",
			"minecraft:soul_sand_valley",
			"minecraft:crimson_forest",
			"minecraft:warped_forest",
			"minecraft:basalt_deltas",
		}
		for _, wantB := range wantNetherBiomes {
			found := false
			for r := 0; r <= 2048 && !found; r += 32 {
				for x := -r; x <= r && !found; x += 32 {
					for z := -r; z <= r && !found; z += 32 {
						if x != -r && x != r && z != -r && z != r {
							continue
						}
						pt := worldgen.SampleColumn(netherOD, netherOD.SeaLevel, x, z)
						if netherTable.FindBiome(pt) == wantB {
							id := biomeID(wantB)
							if biomeNameByID(id) != wantB {
								t.Fatalf("Nether biome roundtrip failed for %s (id=%d)", wantB, id)
							}
							found = true
						}
					}
				}
			}
			if !found {
				t.Fatalf("did not find Nether biome %s on seed 12345", wantB)
			}
		}

		// Locate and pin control points for all 5 End biomes on seed 12345.
		if got := worldgen.EndBiomeAt(endOD.Erosion, 0, 0, 0); got != "minecraft:the_end" {
			t.Fatalf("EndBiomeAt(0,0,0) = %s, want minecraft:the_end", got)
		}
		seenEnd := make(map[string][2]int)
		for cx := 65; cx <= 135; cx++ {
			for cz := 0; cz <= 75; cz++ {
				b := worldgen.EndBiomeAt(endOD.Erosion, cx*4, 0, cz*4)
				if _, exists := seenEnd[b]; !exists {
					seenEnd[b] = [2]int{cx, cz}
				}
			}
		}
		for _, wantB := range []string{
			"minecraft:end_highlands",
			"minecraft:end_midlands",
			"minecraft:small_end_islands",
			"minecraft:end_barrens",
		} {
			if _, ok := seenEnd[wantB]; !ok {
				t.Fatalf("End biome %s not found in outer End control sweep", wantB)
			}
		}
	})

	// 2. Overworld surface rules (badlands terracotta bands, mangrove mud, beach sand,
	// snow/ice temperature, deepslate & bedrock vertical gradients, aquifers & carvers)
	// and block-exact chunk hashes on seed 12345.
	t.Run("OverworldSurfaceRulesCarversAndChunkHashes", func(t *testing.T) {
		picker := worldgen.OverworldFluidPicker(owOD.SeaLevel)
		veins := worldgen.NewOreVeinifier(owOD)
		carver, err := worldgen.NewCarver(owOD, seed)
		if err != nil {
			t.Fatal(err)
		}
		initCarverReplaceable(carver.ReplaceableBlocks())

		// Find a badlands chunk on seed 12345 to verify terracotta bands & red_sand surface rules.
		var badlandsCX, badlandsCZ int32
		foundBadlands := false
		for r := int32(0); r <= 240 && !foundBadlands; r += 4 {
			for dx := -r; dx <= r && !foundBadlands; dx += 4 {
				for dz := -r; dz <= r && !foundBadlands; dz += 4 {
					if dx != -r && dx != r && dz != -r && dz != r {
						continue
					}
					wx, wz := int(dx)*16+8, int(dz)*16+8
					s2D := worldgen.SampleColumn2D(owOD, SeaLevel+8, wx, wz)
					b := biomeNameByID(BiomeAt3D(owOD, s2D, wx, SeaLevel+8, wz))
					if b == "minecraft:badlands" || b == "minecraft:wooded_badlands" || b == "minecraft:eroded_badlands" {
						badlandsCX, badlandsCZ = dx, dz
						foundBadlands = true
					}
				}
			}
		}
		if !foundBadlands {
			t.Fatal("did not find badlands biome on seed 12345 within radius 240")
		}

		owCoords := []struct {
			label  string
			cx, cz int32
		}{
			{"ocean_spawn", 0, 0},
			{"forest", -16, 16},
			{"desert", -140, 28},
			{"mangrove_swamp", -102, -92},
			{"cherry_grove", -26, 48},
			{"badlands", badlandsCX, badlandsCZ},
			{"taiga_land", 16, -40},
		}

		for _, tc := range owCoords {
			ch1 := generateVanillaWithoutDecoration(owOD, picker, veins, carver, seed, tc.cx, tc.cz)
			ch2 := generateVanillaWithoutDecoration(owOD, picker, veins, carver, seed, tc.cx, tc.cz)
			h1 := computeChunkExactHash64(ch1)
			h2 := computeChunkExactHash64(ch2)
			if h1 != h2 || h1 == 0 {
				t.Fatalf("Overworld chunk %s (%d,%d) hash mismatch: %x vs %x", tc.label, tc.cx, tc.cz, h1, h2)
			}

			// Verify bedrock floor at y=-64, bedrock gradient in y=-63..-60, and deepslate transition in y=-8..8.
			deepslateID, _ := nameToStateID("minecraft:deepslate", map[string]string{"axis": "y"})
			hasDeepslate := false
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					if ch1.GetBlock(lx, MinY, lz) != StateBedrock {
						t.Fatalf("Overworld chunk %s (%d,%d) missing bedrock at (%d,%d,%d)", tc.label, tc.cx, tc.cz, lx, MinY, lz)
					}
					if ch1.GetBlock(lx, -16, lz) == deepslateID {
						hasDeepslate = true
					}
				}
			}
			if !hasDeepslate {
				t.Fatalf("Overworld chunk %s (%d,%d) missing deepslate below y=0", tc.label, tc.cx, tc.cz)
			}

			if tc.label == "badlands" {
				terracottaCount := 0
				for y := SeaLevel; y < 140; y++ {
					for lx := 0; lx < 16; lx++ {
						for lz := 0; lz < 16; lz++ {
							name := BlockName(ch1.GetBlock(lx, y, lz))
							if strings.Contains(name, "terracotta") || name == "minecraft:red_sand" {
								terracottaCount++
							}
						}
					}
				}
				if terracottaCount == 0 {
					t.Fatalf("badlands chunk (%d,%d) placed 0 terracotta/red_sand blocks", tc.cx, tc.cz)
				}
			}
		}
	})

	// 3. Nether chunk block-exact hashes across all 5 Nether biomes on seed 12345.
	t.Run("NetherBiomesAndChunkHashes", func(t *testing.T) {
		netherTable := worldgen.NetherBiomeTable()
		wantNetherBiomes := []string{
			"minecraft:nether_wastes",
			"minecraft:soul_sand_valley",
			"minecraft:crimson_forest",
			"minecraft:warped_forest",
			"minecraft:basalt_deltas",
		}
		for _, wantB := range wantNetherBiomes {
			var targetCX, targetCZ int32
			found := false
			for r := int32(0); r <= 128 && !found; r += 2 {
				for dx := -r; dx <= r && !found; dx += 2 {
					for dz := -r; dz <= r && !found; dz += 2 {
						if dx != -r && dx != r && dz != -r && dz != r {
							continue
						}
						pt := worldgen.SampleColumn(netherOD, netherOD.SeaLevel, int(dx)*16+8, int(dz)*16+8)
						if netherTable.FindBiome(pt) == wantB {
							targetCX, targetCZ = dx, dz
							found = true
						}
					}
				}
			}
			if !found {
				t.Fatalf("could not find Nether chunk for biome %s", wantB)
			}
			c1 := generateNetherChunk(netherOD, netherCarver, netherSets, seed, targetCX, targetCZ)
			c2 := generateNetherChunk(netherOD, netherCarver, netherSets, seed, targetCX, targetCZ)
			h1 := computeChunkExactHash64(c1)
			h2 := computeChunkExactHash64(c2)
			if h1 != h2 || h1 == 0 {
				t.Fatalf("Nether biome %s chunk (%d,%d) hash mismatch: %x vs %x", wantB, targetCX, targetCZ, h1, h2)
			}
		}
	})

	// 4. End chunk block-exact hashes across central island, void ring, and outer End biomes on seed 12345.
	t.Run("EndBiomesAndChunkHashes", func(t *testing.T) {
		endCoords := [][2]int32{
			{0, 0},   // central island
			{6, 0},   // obsidian spawn platform
			{2, 0},   // obsidian spike
			{30, 0},  // void ring
			{70, 10}, // outer End islands
		}
		for _, cc := range endCoords {
			c1 := generateEndChunk(endOD, endSets, seed, cc[0], cc[1])
			c2 := generateEndChunk(endOD, endSets, seed, cc[0], cc[1])
			h1 := computeChunkExactHash64(c1)
			h2 := computeChunkExactHash64(c2)
			if h1 != h2 || h1 == 0 {
				t.Fatalf("End chunk (%d,%d) hash mismatch: %x vs %x", cc[0], cc[1], h1, h2)
			}
		}
	})
}



