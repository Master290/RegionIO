package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func TestSnowLayerCanSurviveOnFreezeTopLayerAndDiskPostProcessParity(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}

	// 1. 26.1.2 snow layer survival rules: tags #cannot_support_snow_layer / #support_override_snow_layer,
	// leaves collision face, snow[layers=8], and empty-collision exclusions (powder_snow, scaffolding, cactus).
	cannotSupport := []uint16{
		testMustStateID(t, "minecraft:ice", nil),
		testMustStateID(t, "minecraft:packed_ice", nil),
		testMustStateID(t, "minecraft:barrier", nil),
		testMustStateID(t, "minecraft:powder_snow", nil),
		testMustStateID(t, "minecraft:scaffolding", map[string]string{"bottom": "false", "distance": "0", "waterlogged": "false"}),
		testMustStateID(t, "minecraft:cactus", map[string]string{"age": "0"}),
		testMustStateID(t, "minecraft:snow", map[string]string{"layers": "1"}),
		testMustStateID(t, "minecraft:snow", map[string]string{"layers": "7"}),
		testMustStateID(t, "minecraft:spruce_slab", map[string]string{"type": "bottom", "waterlogged": "false"}),
		testMustStateID(t, "minecraft:spruce_stairs", map[string]string{"facing": "north", "half": "bottom", "shape": "straight", "waterlogged": "false"}),
	}
	for _, st := range cannotSupport {
		if snowLayerCanSurviveOn(st, set) {
			t.Fatalf("expected snow not to survive on %s", stateLabel(st))
		}
	}

	canSupport := []uint16{
		testMustStateID(t, "minecraft:honey_block", nil),
		testMustStateID(t, "minecraft:soul_sand", nil),
		testMustStateID(t, "minecraft:mud", nil),
		testMustStateID(t, "minecraft:spruce_leaves", map[string]string{"distance": "1", "persistent": "false", "waterlogged": "false"}),
		testMustStateID(t, "minecraft:oak_leaves", map[string]string{"distance": "1", "persistent": "false", "waterlogged": "false"}),
		testMustStateID(t, "minecraft:snow", map[string]string{"layers": "8"}),
		testMustStateID(t, "minecraft:blue_ice", nil),
		testMustStateID(t, "minecraft:snow_block", nil),
		testMustStateID(t, "minecraft:spruce_slab", map[string]string{"type": "top", "waterlogged": "false"}),
		testMustStateID(t, "minecraft:spruce_slab", map[string]string{"type": "double", "waterlogged": "false"}),
		testMustStateID(t, "minecraft:spruce_stairs", map[string]string{"facing": "north", "half": "top", "shape": "straight", "waterlogged": "false"}),
	}
	for _, st := range canSupport {
		if !snowLayerCanSurviveOn(st, set) {
			t.Fatalf("expected snow to survive on %s", stateLabel(st))
		}
	}

	// 2. Stage-10 freeze_top_layer places snow[layers=1] on spruce_leaves, sets snowy=true on
	// snowy dirt blocks, freezes still water without capping that new ice in snow, and skips
	// packed_ice and powder_snow.
	reg := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
	taigaID := biomeID("minecraft:snowy_taiga")
	for _, ch := range reg.chunks {
		for y := MinY; y < MinY+WorldHeight; y += 4 {
			for gz := 0; gz < 16; gz += 4 {
				for gx := 0; gx < 16; gx += 4 {
					ch.SetBiome(gx, y, gz, taigaID)
				}
			}
		}
	}

	grassFalse := testMustStateID(t, "minecraft:grass_block", map[string]string{"snowy": "false"})
	podzolFalse := testMustStateID(t, "minecraft:podzol", map[string]string{"snowy": "false"})
	myceliumFalse := testMustStateID(t, "minecraft:mycelium", map[string]string{"snowy": "false"})
	spruceLeaves := testMustStateID(t, "minecraft:spruce_leaves", map[string]string{"distance": "1", "persistent": "false", "waterlogged": "false"})
	packedIceID := testMustStateID(t, "minecraft:packed_ice", nil)
	powderSnowID := testMustStateID(t, "minecraft:powder_snow", nil)
	waterID := testMustStateID(t, "minecraft:water", map[string]string{"level": "0"})

	reg.setBlock(2, 63, 2, grassFalse)
	reg.setBlock(3, 63, 2, podzolFalse)
	reg.setBlock(4, 63, 2, myceliumFalse)
	reg.setBlock(5, 68, 5, spruceLeaves)
	reg.setBlock(6, 63, 6, packedIceID)
	reg.setBlock(7, 63, 7, powderSnowID)
	reg.setBlock(8, 63, 8, waterID)

	if err := reg.setSource(0, 0); err != nil {
		t.Fatalf("setSource: %v", err)
	}
	if err := reg.placeScheduledFreezeTopLayer(12345); err != nil {
		t.Fatalf("placeScheduledFreezeTopLayer: %v", err)
	}

	snowLayer1 := testMustStateID(t, "minecraft:snow", map[string]string{"layers": "1"})
	iceID := testMustStateID(t, "minecraft:ice", nil)

	if reg.getBlock(5, 69, 5) != snowLayer1 {
		t.Fatalf("spruce_leaves canopy top = %s, want minecraft:snow[layers=1]", stateLabel(reg.getBlock(5, 69, 5)))
	}
	for _, col := range []struct {
		x, z int
		want string
	}{
		{2, 2, "minecraft:grass_block"},
		{3, 2, "minecraft:podzol"},
		{4, 2, "minecraft:mycelium"},
	} {
		if reg.getBlock(col.x, 64, col.z) != snowLayer1 {
			t.Fatalf("above %s (%d,64,%d) = %s, want snow[layers=1]", col.want, col.x, col.z, stateLabel(reg.getBlock(col.x, 64, col.z)))
		}
		belowSt, _ := stateByID(reg.getBlock(col.x, 63, col.z))
		if belowSt.Name != col.want || belowSt.Properties["snowy"] != "true" {
			t.Fatalf("below (%d,63,%d) = %s snowy=%q, want %s snowy=true", col.x, col.z, belowSt.Name, belowSt.Properties["snowy"], col.want)
		}
	}
	if reg.getBlock(6, 64, 6) != StateAir {
		t.Fatalf("packed_ice top (6,64,6) = %s, want air", stateLabel(reg.getBlock(6, 64, 6)))
	}
	if reg.getBlock(7, 64, 7) != StateAir {
		t.Fatalf("powder_snow top (7,64,7) = %s, want air", stateLabel(reg.getBlock(7, 64, 7)))
	}
	if reg.getBlock(8, 63, 8) != iceID || reg.getBlock(8, 64, 8) != StateAir {
		t.Fatalf("water column (8,63..64,8) = (%s, %s), want (ice, air)", stateLabel(reg.getBlock(8, 63, 8)), stateLabel(reg.getBlock(8, 64, 8)))
	}

	// 3. DiskFeature (ice_patch) markAboveForPostProcessing removes orphaned snow above newly placed packed_ice.
	diskCfg, err := set.Disk("minecraft:ice_patch")
	if err != nil {
		t.Fatalf("set.Disk(minecraft:ice_patch): %v", err)
	}
	snowBlockID := testMustStateID(t, "minecraft:snow_block", nil)
	for dx := -4; dx <= 4; dx++ {
		for dz := -4; dz <= 4; dz++ {
			x, z := 10+dx, 10+dz
			reg.setBlock(x, 63, z, snowBlockID)
			reg.setBlock(x, 64, z, snowLayer1)
		}
	}
	rng := worldgen.NewXoroshiro(99887766)
	if err := reg.placeDisk(set, rng, worldgen.FeaturePosition{X: 10, Y: 63, Z: 10}, diskCfg); err != nil {
		t.Fatalf("placeDisk(ice_patch): %v", err)
	}
	reg.runPostProcessing(set)
	var replacedPackedIce int
	for dx := -4; dx <= 4; dx++ {
		for dz := -4; dz <= 4; dz++ {
			x, z := 10+dx, 10+dz
			if reg.getBlock(x, 63, z) == packedIceID {
				replacedPackedIce++
				if reg.getBlock(x, 64, z) != StateAir {
					t.Fatalf("ice_patch placed packed_ice at (%d,63,%d) but snow remained at y=64 (%s)", x, z, stateLabel(reg.getBlock(x, 64, z)))
				}
			}
		}
	}
	if replacedPackedIce == 0 {
		t.Fatal("ice_patch placed zero packed_ice blocks in synthetic disk test")
	}
}

func TestIcebergFeatureHeightAndRngStreamParity(t *testing.T) {
	// Find a non-ellipse spire seed where (!isEllipse && nextDouble() > 0.9) triggers,
	// giving height > 18 (specifically >= 25), and verify placeIceberg writes above SeaLevel+18.
	var spireSeed int64
	var nonBoostSeed int64
	for cand := int64(1); cand <= 10000 && (spireSeed == 0 || nonBoostSeed == 0); cand++ {
		rng := worldgen.NewXoroshiro(cand)
		_ = rng.NextDouble() // freehand
		_ = rng.NextDouble() // shapeAngle
		_ = rng.NextIntN(5)  // ellipseA
		_ = rng.NextIntN(3)  // ellipseC
		isEllipse := rng.NextDouble() > 0.7
		h := int(rng.NextIntN(15)) + 3
		if !isEllipse && spireSeed == 0 && rng.NextDouble() > 0.9 {
			boost := int(rng.NextIntN(19)) + 7
			if h+boost >= 25 {
				spireSeed = cand
			}
			continue
		}
		if !isEllipse && nonBoostSeed == 0 && h >= 14 && rng.NextIntN(11) >= 2 {
			nonBoostSeed = cand
		}
	}
	if spireSeed == 0 || nonBoostSeed == 0 {
		t.Fatalf("failed to find iceberg test seeds: spire=%d nonBoost=%d", spireSeed, nonBoostSeed)
	}

	cfg := worldgen.BlockStateFeatureConfig{
		State: worldgen.BlockState{Name: "minecraft:packed_ice"},
	}

	regSpire := buildFlatTestDecorationRegion(t, 0, 0, 2, 30, true)
	if err := regSpire.setSource(0, 0); err != nil {
		t.Fatalf("setSource: %v", err)
	}
	if !regSpire.placeIceberg(worldgen.NewXoroshiro(spireSeed), worldgen.FeaturePosition{X: 8, Y: SeaLevel, Z: 8}, cfg) {
		t.Fatalf("placeIceberg(spireSeed=%d) returned false", spireSeed)
	}
	maxIcebergY := MinY
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			for y := SeaLevel; y < SeaLevel+48; y++ {
				if isIcebergBlockState(regSpire.getBlock(x, y, z)) {
					if y > maxIcebergY {
						maxIcebergY = y
					}
				}
			}
		}
	}
	if maxIcebergY <= SeaLevel+18 {
		t.Fatalf("spire iceberg maxY = %d, want > %d (above-water height must not be capped at 18)", maxIcebergY, SeaLevel+18)
	}

	// Verify ±1 chunk write guard: placing near chunk (1,0)'s far east edge from source (0,0) must not write chunk (2,0).
	regGuard := buildFlatTestDecorationRegion(t, 0, 0, 2, 30, true)
	if err := regGuard.setSource(0, 0); err != nil {
		t.Fatalf("setSource: %v", err)
	}
	regGuard.placeIceberg(worldgen.NewXoroshiro(nonBoostSeed), worldgen.FeaturePosition{X: 30, Y: SeaLevel, Z: 8}, cfg)
	for z := 0; z < 16; z++ {
		for y := SeaLevel - 20; y <= SeaLevel+30; y++ {
			if st := regGuard.getBlock(32, y, z); isIcebergBlockState(st) {
				t.Fatalf("placeIceberg wrote outside ±1 chunk write radius at (32,%d,%d): %s", y, z, stateLabel(st))
			}
		}
	}
}

func TestColdAndMountainBiomesThreeSeedGenerationParity(t *testing.T) {
	strSeed, ok := ParseWorldSeed("frozen-peaks-spikes")
	if !ok {
		t.Fatal("ParseWorldSeed(\"frozen-peaks-spikes\") failed")
	}

	type checkChunk struct {
		seed   int64
		cx, cz int32
		biome  string
	}
	cases := []checkChunk{
		{seed: 20261118, cx: -128, cz: 160, biome: "minecraft:ice_spikes"},
		{seed: 20261118, cx: 28, cz: 114, biome: "minecraft:grove"},
		{seed: -555666777, cx: 193, cz: -36, biome: "minecraft:snowy_taiga"},
		{seed: -555666777, cx: 18, cz: 14, biome: "minecraft:jagged_peaks"},
		{seed: strSeed, cx: 76, cz: 38, biome: "minecraft:frozen_peaks"},
		{seed: strSeed, cx: -28, cz: 14, biome: "minecraft:deep_frozen_ocean"},
		{seed: strSeed, cx: -28, cz: 15, biome: "minecraft:deep_frozen_ocean"},
	}

	gens := make(map[int64]Generator)
	for i, tc := range cases {
		if testing.Short() && i >= 2 {
			continue
		}
		gen, exists := gens[tc.seed]
		if !exists {
			gen = NewVanillaRegionGenerator(tc.seed)
			gens[tc.seed] = gen
		}
		c := gen(tc.cx, tc.cz)
		if !chunkHasBiome(c, tc.biome) {
			t.Fatalf("seed=%d chunk=(%d,%d) missing expected biome %s", tc.seed, tc.cx, tc.cz, tc.biome)
		}

		counts := make(map[string]int)
		var maxY, maxPackedIceY int
		maxY = MinY
		maxPackedIceY = MinY
		var badSnowOnUnsupported, unSnowyDirtUnderSnow, leavesWithSnow int

		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				for y := MinY; y < MinY+WorldHeight; y++ {
					id := c.GetBlock(x, y, z)
					if isAirState(id) {
						continue
					}
					st, _ := stateByID(id)
					counts[st.Name]++
					if y > maxY {
						maxY = y
					}
					if st.Name == "minecraft:packed_ice" && y > maxPackedIceY {
						maxPackedIceY = y
					}
					if st.Name == "minecraft:snow" && y > MinY {
						belowID := c.GetBlock(x, y-1, z)
						belowSt, _ := stateByID(belowID)
						switch belowSt.Name {
						case "minecraft:packed_ice", "minecraft:ice", "minecraft:powder_snow", "minecraft:water":
							badSnowOnUnsupported++
						case "minecraft:grass_block", "minecraft:podzol", "minecraft:mycelium":
							if belowSt.Properties["snowy"] != "true" {
								unSnowyDirtUnderSnow++
							}
						case "minecraft:spruce_leaves":
							leavesWithSnow++
						}
					}
				}
			}
		}

		if badSnowOnUnsupported != 0 {
			t.Fatalf("seed=%d chunk=(%d,%d) [%s] placed snow on %d unsupported blocks (packed_ice/ice/powder_snow/water)",
				tc.seed, tc.cx, tc.cz, tc.biome, badSnowOnUnsupported)
		}
		if unSnowyDirtUnderSnow != 0 {
			t.Fatalf("seed=%d chunk=(%d,%d) [%s] left %d grass/podzol/mycelium blocks with snowy=false under snow",
				tc.seed, tc.cx, tc.cz, tc.biome, unSnowyDirtUnderSnow)
		}

		switch tc.biome {
		case "minecraft:ice_spikes":
			if maxPackedIceY < 100 || counts["minecraft:packed_ice"] < 500 || counts["minecraft:snow_block"] < 150 {
				t.Fatalf("ice_spikes (%d,%d) unexpected spike/surface counts: maxPackedIceY=%d packed_ice=%d snow_block=%d",
					tc.cx, tc.cz, maxPackedIceY, counts["minecraft:packed_ice"], counts["minecraft:snow_block"])
			}
		case "minecraft:grove":
			if counts["minecraft:powder_snow"] == 0 || counts["minecraft:snow_block"] == 0 || leavesWithSnow == 0 ||
				counts["minecraft:emerald_ore"]+counts["minecraft:deepslate_emerald_ore"] == 0 {
				t.Fatalf("grove (%d,%d) missing expected features: powder_snow=%d snow_block=%d leavesWithSnow=%d emerald=%d",
					tc.cx, tc.cz, counts["minecraft:powder_snow"], counts["minecraft:snow_block"], leavesWithSnow,
					counts["minecraft:emerald_ore"]+counts["minecraft:deepslate_emerald_ore"])
			}
		case "minecraft:snowy_taiga":
			if counts["minecraft:spruce_leaves"] < 200 || leavesWithSnow < 20 || counts["minecraft:snow"] < 200 {
				t.Fatalf("snowy_taiga (%d,%d) missing expected canopy snow: spruce_leaves=%d leavesWithSnow=%d snow=%d",
					tc.cx, tc.cz, counts["minecraft:spruce_leaves"], leavesWithSnow, counts["minecraft:snow"])
			}
		case "minecraft:jagged_peaks":
			if maxY < 150 || counts["minecraft:snow_block"] < 200 ||
				counts["minecraft:emerald_ore"]+counts["minecraft:deepslate_emerald_ore"] == 0 {
				t.Fatalf("jagged_peaks (%d,%d) unexpected peak metrics: maxY=%d snow_block=%d emerald=%d",
					tc.cx, tc.cz, maxY, counts["minecraft:snow_block"],
					counts["minecraft:emerald_ore"]+counts["minecraft:deepslate_emerald_ore"])
			}
		case "minecraft:frozen_peaks":
			if maxY < 160 || counts["minecraft:packed_ice"] < 500 || counts["minecraft:snow_block"] < 200 ||
				counts["minecraft:emerald_ore"]+counts["minecraft:deepslate_emerald_ore"] == 0 {
				t.Fatalf("frozen_peaks (%d,%d) unexpected peak metrics: maxY=%d packed_ice=%d snow_block=%d emerald=%d",
					tc.cx, tc.cz, maxY, counts["minecraft:packed_ice"], counts["minecraft:snow_block"],
					counts["minecraft:emerald_ore"]+counts["minecraft:deepslate_emerald_ore"])
			}
		case "minecraft:deep_frozen_ocean":
			if tc.cz == 14 && maxPackedIceY <= SeaLevel+18 {
				t.Fatalf("deep_frozen_ocean (-28,14) spire iceberg maxPackedIceY = %d, want > %d", maxPackedIceY, SeaLevel+18)
			}
			if tc.cz == 15 && counts["minecraft:blue_ice"] == 0 {
				t.Fatal("deep_frozen_ocean (-28,15) expected underwater minecraft:blue_ice")
			}
		}
	}
}
