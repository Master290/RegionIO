package world

import (
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

