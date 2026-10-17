package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func TestNetherDensityAndBiomes26_1_2(t *testing.T) {
	const seed int64 = -4172144997902289642
	od, err := worldgen.LoadNetherFinalDensity(seed)
	if err != nil {
		t.Fatalf("LoadNetherFinalDensity: %v", err)
	}
	if od.MinY != 0 || od.Height != 128 || od.SeaLevel != 32 {
		t.Fatalf("unexpected Nether bounds: MinY=%d Height=%d SeaLevel=%d", od.MinY, od.Height, od.SeaLevel)
	}
	rules, err := od.SurfaceRule()
	if err != nil || rules == nil {
		t.Fatalf("od.SurfaceRule(): rules=%v err=%v", rules, err)
	}

	table := worldgen.NetherBiomeTable()
	seen := make(map[string]bool)
	for x := -2048; x <= 2048; x += 64 {
		for z := -2048; z <= 2048; z += 64 {
			pt := worldgen.SampleColumn(od, od.SeaLevel, x, z)
			seen[table.FindBiome(pt)] = true
		}
	}
	wantBiomes := []string{
		"minecraft:nether_wastes",
		"minecraft:soul_sand_valley",
		"minecraft:crimson_forest",
		"minecraft:warped_forest",
		"minecraft:basalt_deltas",
	}
	for _, b := range wantBiomes {
		if !seen[b] {
			t.Errorf("expected Nether biome %s to appear across [-2048..2048]^2, seen=%v", b, seen)
		}
	}
}

func TestNetherBaseTerrainSurfaceAndCarver(t *testing.T) {
	const seed int64 = 12345
	od, carver, _ := netherGeneratorInputs(seed)
	base := generateNetherBaseTerrain(od, carver, seed, 0, 0)
	c := base.c

	// Verify bedrock floor at y=0 and bedrock roof at y=127 across all 16x16 columns,
	// and empty air above y=127.
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			if got := c.GetBlock(lx, 0, lz); got != StateBedrock {
				t.Fatalf("floor at (%d, 0, %d) = %s (%d), want bedrock", lx, lz, BlockName(got), got)
			}
			if got := c.GetBlock(lx, NetherRoofY, lz); got != StateBedrock {
				t.Fatalf("roof at (%d, 127, %d) = %s (%d), want bedrock", lx, lz, BlockName(got), got)
			}
			if got := c.GetBlock(lx, 128, lz); got != StateAir {
				t.Fatalf("above roof at (%d, 128, %d) = %s (%d), want air", lx, lz, BlockName(got), got)
			}
		}
	}

	// Verify gradient bedrock appears in y=1..4 and y=123..126, and interior has netherrack/air/lava.
	var gradFloor, gradRoof, solidInterior, airOrLava int
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			for y := 1; y <= 4; y++ {
				if c.GetBlock(lx, y, lz) == StateBedrock {
					gradFloor++
				}
			}
			for y := 123; y <= 126; y++ {
				if c.GetBlock(lx, y, lz) == StateBedrock {
					gradRoof++
				}
			}
			for y := 5; y <= 122; y++ {
				st := c.GetBlock(lx, y, lz)
				if st == StateAir || st == StateLava {
					airOrLava++
				} else {
					solidInterior++
				}
			}
		}
	}
	if gradFloor == 0 || gradFloor == 16*16*4 {
		t.Fatalf("expected partial bedrock gradient on floor y=1..4, got %d/%d", gradFloor, 16*16*4)
	}
	if gradRoof == 0 || gradRoof == 16*16*4 {
		t.Fatalf("expected partial bedrock gradient on roof y=123..126, got %d/%d", gradRoof, 16*16*4)
	}
	if solidInterior == 0 || airOrLava == 0 {
		t.Fatalf("expected both solid terrain (%d) and open/lava space (%d) in Nether interior", solidInterior, airOrLava)
	}
}

func TestNetherFeaturesPlacement(t *testing.T) {
	c := NewDimensionChunk(0, 0, BiomeNetherWastes, NetherDimensionType)
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			c.setBlockRaw(lx, 0, lz, StateBedrock)
			for y := 1; y <= 40; y++ {
				c.setBlockRaw(lx, y, lz, StateNetherrack)
			}
			for y := 90; y < 127; y++ {
				c.setBlockRaw(lx, y, lz, StateNetherrack)
			}
			c.setBlockRaw(lx, 127, lz, StateBedrock)
		}
	}

	r := worldgen.NewXoroshiro(42)

	// 1. Basalt pillar from ceiling (y=89) down to floor (y=41).
	if !PlaceBasaltPillar(c, 8, 89, 8, r) {
		t.Fatal("expected PlaceBasaltPillar to succeed at (8, 89, 8)")
	}
	if c.GetBlock(8, 65, 8) != StateBasalt {
		t.Fatalf("expected basalt at pillar center (8, 65, 8), got %s", BlockName(c.GetBlock(8, 65, 8)))
	}

	// 2. Glowstone blob hanging under netherrack ceiling (y=89).
	if !PlaceGlowstoneBlob(c, 4, 89, 4, r) {
		t.Fatal("expected PlaceGlowstoneBlob to succeed at (4, 89, 4)")
	}
	if c.GetBlock(4, 89, 4) != StateGlowstone {
		t.Fatalf("expected glowstone at (4, 89, 4), got %s", BlockName(c.GetBlock(4, 89, 4)))
	}

	// 3. Weeping vines hanging under netherrack ceiling (y=89).
	if !PlaceWeepingVines(c, 12, 89, 4, r) {
		t.Fatal("expected PlaceWeepingVines to succeed at (12, 89, 4)")
	}
	if c.GetBlock(12, 89, 4) != StateNetherWartBlock {
		t.Fatalf("expected nether_wart_block at (12, 89, 4), got %s", BlockName(c.GetBlock(12, 89, 4)))
	}

	// 4. Huge crimson fungus & crimson vegetation on crimson_nylium floor.
	c.setBlockRaw(4, 40, 12, StateCrimsonNylium)
	if !PlaceHugeFungus(c, 4, 41, 12, r, true) {
		t.Fatal("expected PlaceHugeFungus(crimson) to succeed at (4, 41, 12)")
	}
	nb := loadNetherBlocks()
	if c.GetBlock(4, 41, 12) != nb.crimsonStem {
		t.Fatalf("expected crimson_stem at (4, 41, 12), got %s", BlockName(c.GetBlock(4, 41, 12)))
	}

	// 5. Twisting vines on warped_nylium floor.
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			c.setBlockRaw(12+dx, 40, 12+dz, StateWarpedNylium)
		}
	}
	if !PlaceTwistingVines(c, 12, 41, 12, r, 4, 2, 8) {
		t.Fatal("expected PlaceTwistingVines to succeed at (12, 41, 12)")
	}
	if !PlaceNetherForestVegetation(c, 12, 41, 12, r, false, 4, 2) {
		t.Fatal("expected PlaceNetherForestVegetation to succeed at (12, 41, 12)")
	}

	// 6. ReplaceBlobs (netherrack -> blackstone).
	if !PlaceReplaceBlobs(c, 2, 30, 2, r, StateNetherrack, StateBlackstone, 2, 4) {
		t.Fatal("expected PlaceReplaceBlobs to succeed at (2, 30, 2)")
	}
	if c.GetBlock(2, 30, 2) != StateBlackstone {
		t.Fatalf("expected blackstone at (2, 30, 2), got %s", BlockName(c.GetBlock(2, 30, 2)))
	}

	// 7. BasaltColumns & DeltaFeature on floor (y=41).
	if !PlaceBasaltColumns(c, 8, 41, 2, r, 0, 1, 1, 3) {
		t.Fatal("expected PlaceBasaltColumns to succeed at (8, 41, 2)")
	}
	if !PlaceDeltaFeature(c, 8, 40, 14, r, StateLava, StateMagmaBlock, 2, 4, 1, 2) {
		t.Fatal("expected PlaceDeltaFeature to succeed at (8, 40, 14)")
	}
}

func TestNetherStructuresFortressBastionFossilAndPortal(t *testing.T) {
	const seed int64 = -4172144997902289642
	od, _, sets := netherGeneratorInputs(seed)

	// 1. Verify Nether Fortress start construction and piece placement.
	start := BuildNetherFortressStart(seed, 0, 0)
	if start == nil || len(start.Pieces) < 5 {
		t.Fatalf("expected NetherFortressStart to produce multiple bridge/castle pieces, got %v", start)
	}
	c := NewDimensionChunk(0, 0, BiomeNetherWastes, NetherDimensionType)
	PlaceNetherFortress(c, start, 0, 0)
	netherBricks, _ := BlockStateByName("minecraft:nether_bricks", nil)
	foundBricks := 0
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			for y := 32; y < 90; y++ {
				if c.GetBlock(lx, y, lz) == netherBricks {
					foundBricks++
				}
			}
		}
	}
	if foundBricks == 0 {
		t.Fatal("expected PlaceNetherFortress to place nether_bricks in start chunk (0, 0)")
	}

	// 2. Verify NetherComplexes evaluation (fortress + bastion_remnant).
	var fortressCount, bastionCount int
	var sampleBastion *StructureStartStub
	for cx := int32(-64); cx <= 64; cx++ {
		for cz := int32(-64); cz <= 64; cz++ {
			stub, ok := FindStructureSetStart(od, sets, "minecraft:nether_complexes", seed, cx, cz)
			if !ok || stub == nil {
				continue
			}
			switch stub.StructureName {
			case "minecraft:fortress":
				fortressCount++
			case "minecraft:bastion_remnant":
				bastionCount++
				if sampleBastion == nil {
					cp := *stub
					sampleBastion = &cp
				}
			}
		}
	}
	if fortressCount == 0 || bastionCount == 0 {
		t.Fatalf("expected both fortress (%d) and bastion_remnant (%d) in [-64..64]^2", fortressCount, bastionCount)
	}

	// Verify Bastion Jigsaw assembly clamps within Nether [0..127] and places blocks.
	bastionDef := sets.Structures["minecraft:bastion_remnant"]
	bastionStart, err := BuildJigsawStart(od, sets, bastionDef, seed, sampleBastion.ChunkX, sampleBastion.ChunkZ, nil)
	if err != nil || bastionStart == nil || len(bastionStart.Pieces) == 0 {
		t.Fatalf("expected BuildJigsawStart to assemble bastion_remnant at (%d, %d): err=%v", sampleBastion.ChunkX, sampleBastion.ChunkZ, err)
	}
	bc := NewDimensionChunk(sampleBastion.ChunkX, sampleBastion.ChunkZ, BiomeNetherWastes, NetherDimensionType)
	region, _ := newDecorationRegion([]*Chunk{bc})
	PlaceJigsawStartsForChunk(region, []*JigsawStart{bastionStart}, "minecraft:bastion_remnant", surfaceStructuresStep, seed, sampleBastion.ChunkX, sampleBastion.ChunkZ)
	bastionPlaced := 0
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			for y := 0; y < 128; y++ {
				if bc.GetBlock(lx, y, lz) != StateAir {
					bastionPlaced++
				}
			}
		}
	}
	if bastionPlaced == 0 {
		t.Fatalf("expected bastion_remnant to place blocks in start chunk (%d, %d)", sampleBastion.ChunkX, sampleBastion.ChunkZ)
	}

	// 3. Verify Nether Fossil generation point and placement in soul_sand_valley.
	var fossilStub *NetherFossilStub
	for cx := int32(-64); cx <= 64 && fossilStub == nil; cx++ {
		for cz := int32(-64); cz <= 64 && fossilStub == nil; cz++ {
			fossilStub = NetherFossilGenerationPoint(od, sets, seed, cx, cz)
		}
	}
	if fossilStub == nil {
		t.Fatal("expected at least one NetherFossilStub in [-64..64]^2")
	}
	fcx, fcz := int32(fossilStub.X>>4), int32(fossilStub.Z>>4)
	fc := NewDimensionChunk(fcx, fcz, BiomeNetherWastes, NetherDimensionType)
	if placed := PlaceNetherFossil(fc, fossilStub, seed, fcx, fcz); placed == 0 {
		t.Fatalf("expected PlaceNetherFossil to place bone blocks at %+v", fossilStub)
	}
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			for y := 0; y < NetherDimensionType.Height; y++ {
				name := BlockName(fc.GetBlock(lx, y, lz))
				if name == "minecraft:structure_block" || name == "minecraft:structure_void" || name == "minecraft:jigsaw" {
					t.Fatalf("leaked marker block %s in Nether Fossil chunk (%d, %d) at (%d, %d, %d)", name, fcx, fcz, lx, y, lz)
				}
			}
		}
	}

	// 4. Verify Ruined Portal Nether generation point and placement.
	var portalStub *RuinedPortalStub
	for cx := int32(-32); cx <= 32 && portalStub == nil; cx++ {
		for cz := int32(-32); cz <= 32 && portalStub == nil; cz++ {
			stub, err := RuinedPortalGenerationPoint(od, sets, seed, cx, cz)
			if err == nil && stub != nil && stub.Placement == "in_nether" {
				portalStub = stub
			}
		}
	}
	if portalStub == nil {
		t.Fatal("expected at least one ruined_portal_nether in [-32..32]^2")
	}
	if portalStub.Y < 15 || portalStub.Y > 110 {
		t.Fatalf("unexpected ruined_portal_nether Y=%d (want within [15..110])", portalStub.Y)
	}
}

func TestNetherHeightmapsBiomesAndDeterminism(t *testing.T) {
	const seed int64 = 12345
	gen1 := NewNetherGenerator(seed)
	gen2 := NewNetherGenerator(seed)

	c1 := gen1(2, -3)
	c2 := gen2(2, -3)

	// 1. Verify ParityHeightmaps() and decorationRegion heightAt / getBiome bounds.
	hm := c1.ParityHeightmaps()
	for idx := 0; idx < 256; idx++ {
		if hm[0][idx] != NetherRoofY || hm[1][idx] != NetherRoofY || hm[2][idx] != NetherRoofY {
			t.Fatalf("Nether heightmaps at idx %d = (%d, %d, %d), want 127", idx, hm[0][idx], hm[1][idx], hm[2][idx])
		}
	}
	reg, err := newDecorationRegion([]*Chunk{c1})
	if err != nil {
		t.Fatalf("newDecorationRegion: %v", err)
	}
	reg.bindSeed(seed)
	if h := reg.heightAt("WORLD_SURFACE_WG", 2*16+8, -3*16+8); h != NetherRoofY+1 {
		t.Fatalf("WORLD_SURFACE_WG on Nether chunk = %d, want %d", h, NetherRoofY+1)
	}
	if h := reg.heightAt("WORLD_SURFACE", 2*16+8, -3*16+8); h != NetherRoofY+1 {
		t.Fatalf("WORLD_SURFACE on Nether chunk = %d, want %d", h, NetherRoofY+1)
	}
	if bID, ok := reg.getBiome(2*16+8, 0, -3*16+8); !ok || bID == 0 {
		t.Fatalf("getBiome at Nether y=0 returned (%d, %v), want valid Nether biome", bID, ok)
	}

	// 2. Verify bit-for-bit block and biome determinism across two independent generators.
	for y := 0; y < NetherDimensionType.Height; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				if got, want := c1.GetBlock(x, y, z), c2.GetBlock(x, y, z); got != want {
					t.Fatalf("non-deterministic Nether block at (%d,%d,%d): %d != %d", x, y, z, got, want)
				}
			}
		}
	}
	for y := 0; y < NetherDimensionType.Height; y += biomeCellSize {
		for z := 0; z < 16; z += biomeCellSize {
			for x := 0; x < 16; x += biomeCellSize {
				b1 := c1.GetBiome(x, y, z)
				b2 := c2.GetBiome(x, y, z)
				if b1 != b2 {
					t.Fatalf("non-deterministic Nether biome at (%d,%d,%d): %d != %d", x, y, z, b1, b2)
				}
			}
		}
	}
	if len(c1.Encode()) == 0 {
		t.Fatal("expected non-empty wire encoding for Nether chunk")
	}
}

