package world

import (
	"encoding/json"
	"strings"
	"testing"

	"regionio/internal/worldgen"
)

func testMustStateID(t *testing.T, name string, props map[string]string) uint16 {
	t.Helper()
	id, ok := nameToStateID(name, props)
	if !ok {
		t.Fatalf("missing block state %s %v", name, props)
	}
	return id
}

func chunkHasBiome(c *Chunk, target string) bool {
	for wy := MinY; wy < MinY+WorldHeight; wy += 4 {
		for qz := 0; qz < 4; qz++ {
			for qx := 0; qx < 4; qx++ {
				if biomeNameByID(c.GetBiome(qx*4, wy, qz*4)) == target {
					return true
				}
			}
		}
	}
	return false
}

func TestCactusAndSugarCaneSurvivalParity(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}
	reg := buildFlatTestDecorationRegion(t, 0, 0, 0, 63, false)

	airID := testMustStateID(t, "minecraft:air", nil)
	sandID := testMustStateID(t, "minecraft:sand", nil)
	redSandID := testMustStateID(t, "minecraft:red_sand", nil)
	grassID := testMustStateID(t, "minecraft:grass_block", map[string]string{"snowy": "false"})
	mudID := testMustStateID(t, "minecraft:mud", nil)
	mossID := testMustStateID(t, "minecraft:moss_block", nil)
	terracottaID := testMustStateID(t, "minecraft:terracotta", nil)
	cactusID := testMustStateID(t, "minecraft:cactus", map[string]string{"age": "0"})
	lavaID := testMustStateID(t, "minecraft:lava", map[string]string{"level": "0"})
	waterID := testMustStateID(t, "minecraft:water", map[string]string{"level": "0"})
	bubbleID := testMustStateID(t, "minecraft:bubble_column", map[string]string{"drag": "false"})
	frostedIceID := testMustStateID(t, "minecraft:frosted_ice", map[string]string{"age": "0"})
	sugarCaneID := testMustStateID(t, "minecraft:sugar_cane", map[string]string{"age": "0"})

	pos := worldgen.FeaturePosition{X: 8, Y: 64, Z: 8}
	clearCactusSpot := func(below, above uint16) {
		for dy := 63; dy <= 66; dy++ {
			for dz := 7; dz <= 9; dz++ {
				for dx := 7; dx <= 9; dx++ {
					reg.setBlock(dx, dy, dz, airID)
				}
			}
		}
		reg.setBlock(8, 63, 8, below)
		reg.setBlock(8, 65, 8, above)
	}

	// Cactus survival: sand, red_sand, cactus allowed; terracotta/grass rejected.
	for _, tc := range []struct {
		name  string
		below uint16
		above uint16
		hx    uint16
		want  bool
	}{
		{"sand_clear", sandID, airID, airID, true},
		{"red_sand_clear", redSandID, airID, airID, true},
		{"cactus_stack", cactusID, airID, airID, true},
		{"sand_water_adjacent_ok", sandID, airID, waterID, true},
		{"terracotta_base_rejected", terracottaID, airID, airID, false},
		{"grass_base_rejected", grassID, airID, airID, false},
		{"adjacent_solid_rejected", sandID, airID, terracottaID, false},
		{"adjacent_cactus_rejected", sandID, airID, cactusID, false},
		{"adjacent_lava_rejected", sandID, airID, lavaID, false},
		{"water_above_rejected", sandID, waterID, airID, false},
		{"lava_above_rejected", sandID, lavaID, airID, false},
		{"bubble_column_above_rejected", sandID, bubbleID, airID, false},
	} {
		clearCactusSpot(tc.below, tc.above)
		if tc.hx != airID {
			reg.setBlock(9, 64, 8, tc.hx)
		}
		if got := reg.canCactusSurvive(pos, set); got != tc.want {
			t.Errorf("canCactusSurvive(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}

	// Verify cactus + cactus_flower would_survive predicates & BlockColumnFeature placement.
	cactusCfg, err := set.BlockColumn("minecraft:cactus")
	if err != nil {
		t.Fatalf("BlockColumn(minecraft:cactus): %v", err)
	}
	clearCactusSpot(redSandID, airID)
	cactusSurviveJSON := json.RawMessage(`{"type":"minecraft:would_survive","state":{"Name":"minecraft:cactus","Properties":{"age":"0"}}}`)
	ok, err := reg.testBlockPredicate(set, cactusSurviveJSON, pos)
	if err != nil || !ok {
		t.Fatalf("would_survive(cactus) on red_sand = (%v, %v), want (true, nil)", ok, err)
	}
	reg.setBlock(9, 64, 8, lavaID)
	if ok, err := reg.testBlockPredicate(set, cactusSurviveJSON, pos); err != nil || ok {
		t.Fatalf("would_survive(cactus) beside lava = (%v, %v), want (false, nil)", ok, err)
	}
	clearCactusSpot(redSandID, airID)

	// Place prioritized cactus + cactus_flower column via BlockColumnFeature.
	tippedCfg := cactusCfg
	tippedCfg.PrioritizeTip = true
	var sawFlower bool
	for seed := int64(1); seed <= 64 && !sawFlower; seed++ {
		clearCactusSpot(redSandID, airID)
		rng := worldgen.NewXoroshiro(seed)
		if !reg.placeBlockColumn(rng, pos, tippedCfg, set) {
			t.Fatalf("placeBlockColumn(cactus, seed=%d) returned false", seed)
		}
		topY := 64
		for topY < 68 && reg.getBlock(8, topY, 8) != airID {
			st, _ := stateByID(reg.getBlock(8, topY, 8))
			if st.Name == "minecraft:cactus_flower" {
				sawFlower = true
			}
			topY++
		}
	}
	if !sawFlower {
		t.Fatal("BlockColumnFeature did not place cactus_flower across 64 seeds")
	}

	// Cactus flower survival: cactus & sturdy support allowed; air/water rejected.
	for _, tc := range []struct {
		name  string
		below uint16
		want  bool
	}{
		{"on_cactus", cactusID, true},
		{"on_terracotta", terracottaID, true},
		{"on_air", airID, false},
		{"on_water", waterID, false},
	} {
		clearCactusSpot(tc.below, airID)
		if got := reg.canCactusFlowerSurvive(pos, set); got != tc.want {
			t.Errorf("canCactusFlowerSurvive(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}

	// Sugar cane survival: 26.1.2 #supports_sugar_cane soils + horizontal water/frosted_ice or cane stack.
	for _, tc := range []struct {
		name  string
		below uint16
		adj   uint16
		want  bool
	}{
		{"grass_with_water", grassID, waterID, true},
		{"mud_with_water", mudID, waterID, true},
		{"moss_with_water", mossID, waterID, true},
		{"sand_with_frosted_ice", sandID, frostedIceID, true},
		{"red_sand_with_water", redSandID, waterID, true},
		{"grass_without_water", grassID, airID, false},
		{"stone_with_water", reg.getBlock(0, 63, 0), waterID, false},
		{"cane_on_cane_without_water", sugarCaneID, airID, true},
	} {
		clearCactusSpot(tc.below, airID)
		if tc.adj != airID {
			reg.setBlock(9, 63, 8, tc.adj)
		}
		if got := reg.canSugarCaneSurvive(pos, set); got != tc.want {
			t.Errorf("canSugarCaneSurvive(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestBadlandsSurfaceBandsSpireSteepAndWoodedCapParity(t *testing.T) {
	const seed int64 = 16180339
	owGen := NewVanillaRegionGenerator(seed)

	// Eroded badlands chunk with tall spires and exposed terracotta bands on steep faces.
	eroded := owGen(-239, 97)
	if !chunkHasBiome(eroded, "minecraft:eroded_badlands") {
		t.Fatal("chunk (-239,97) does not contain minecraft:eroded_badlands")
	}
	var maxSurfaceY int
	terracottaKinds := make(map[string]int)
	var lowRedSand int
	for lz := 0; lz < 16; lz++ {
		for lx := 0; lx < 16; lx++ {
			colTop := MinY - 1
			for y := MinY + WorldHeight - 1; y >= MinY; y-- {
				st, _ := stateByID(eroded.GetBlock(lx, y, lz))
				if st.Name == "minecraft:air" || st.Name == "minecraft:cave_air" || st.Name == "minecraft:void_air" {
					continue
				}
				if colTop == MinY-1 {
					colTop = y
					if y > maxSurfaceY {
						maxSurfaceY = y
					}
					if y <= 75 && st.Name == "minecraft:red_sand" {
						lowRedSand++
					}
				}
				if y >= 63 && strings.HasSuffix(st.Name, "terracotta") {
					terracottaKinds[st.Name]++
				}
			}
		}
	}
	if maxSurfaceY < 90 {
		t.Fatalf("eroded_badlands (-239,97) maxSurfaceY = %d, want >= 90 spire", maxSurfaceY)
	}
	if len(terracottaKinds) < 4 {
		t.Fatalf("eroded_badlands (-239,97) distinct terracotta bands = %d (%v), want >= 4", len(terracottaKinds), terracottaKinds)
	}
	if lowRedSand == 0 {
		t.Fatal("eroded_badlands (-239,97) expected red_sand on lower flat columns (y <= 75)")
	}

	// Direct check that updating worldSurface[lx][lz] after eroded spire placement makes neighbors see steepAt.
	var ws [16][16]int
	for z := range ws {
		for x := range ws[z] {
			ws[z][x] = 64
		}
	}
	if steepAt(&ws, 5, 5) {
		t.Fatal("steepAt on flat 64 grid should be false")
	}
	ws[4][5] = 100 // prior column raised by ErodedBadlandsExtension
	if !steepAt(&ws, 5, 5) {
		t.Fatal("steepAt should be true adjacent to raised eroded spire column")
	}

	// Low-elevation wooded_badlands chunk (-164,80) sits below y_above(97+2*depth): no grass_block below y=95.
	woodedLow := owGen(-164, 80)
	if !chunkHasBiome(woodedLow, "minecraft:wooded_badlands") {
		t.Fatal("chunk (-164,80) does not contain minecraft:wooded_badlands")
	}
	var lowGrass int
	for lz := 0; lz < 16; lz++ {
		for lx := 0; lx < 16; lx++ {
			for y := 60; y < 95; y++ {
				st, _ := stateByID(woodedLow.GetBlock(lx, y, lz))
				if st.Name == "minecraft:grass_block" {
					lowGrass++
				}
			}
		}
	}
	if lowGrass != 0 {
		t.Fatalf("wooded_badlands (-164,80) unexpectedly placed grass_block below plateau threshold: count=%d", lowGrass)
	}

	// Direct unit check of the wooded_badlands y_above(97 + 2*surfaceDepth) surface rule threshold.
	od0, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		t.Fatalf("LoadOverworldFinalDensity(%d): %v", seed, err)
	}
	rules, err := od0.SurfaceRule()
	if err != nil {
		t.Fatalf("SurfaceRule: %v", err)
	}
	sctx := rules.NewContext()
	unitWX, unitWZ := -164*16+8, 80*16+8
	fillColumnAndApply := func(topY int) [WorldHeight]uint16 {
		var col [WorldHeight]uint16
		for y := MinY; y <= topY; y++ {
			col[y-MinY] = StateStone
		}
		var ws [16][16]int
		for z := range ws {
			for x := range ws[z] {
				ws[z][x] = topY
			}
		}
		applySurfaceRuleColumn(od0, rules, sctx, &col, unitWX, unitWZ, 8, 8, &ws, "minecraft:wooded_badlands", false)
		return col
	}
	colLow := fillColumnAndApply(92)
	if topSt, _ := stateByID(colLow[92-MinY]); !strings.HasSuffix(topSt.Name, "terracotta") {
		t.Fatalf("wooded_badlands mid-slope column (y=92) top = %s, want terracotta band", topSt.Name)
	}
	for y := 60; y <= 92; y++ {
		if st, _ := stateByID(colLow[y-MinY]); st.Name == "minecraft:grass_block" || st.Name == "minecraft:coarse_dirt" {
			t.Fatalf("wooded_badlands low column unexpectedly had %s at y=%d", st.Name, y)
		}
	}
	colHigh := fillColumnAndApply(112)
	topHighSt, _ := stateByID(colHigh[112-MinY])
	if topHighSt.Name != "minecraft:grass_block" && topHighSt.Name != "minecraft:coarse_dirt" {
		t.Fatalf("wooded_badlands high column (y=112) top = %s, want grass_block or coarse_dirt", topHighSt.Name)
	}
	if subSt, _ := stateByID(colHigh[104-MinY]); !strings.HasSuffix(subSt.Name, "terracotta") {
		t.Fatalf("wooded_badlands high column subsurface (y=104) = %s, want terracotta band", subSt.Name)
	}

	// Fast world-wide 2D climate scan for real high-plateau wooded_badlands chunk(s) above y=97.
	var highChunks [][2]int32
	var highCapSoil, oakLogs int
	for _, candSeed := range []int64{seed, -777888999, 1668348391} {
		if highCapSoil > 0 && oakLogs > 0 {
			break
		}
		od := od0
		gen := owGen
		if candSeed != seed {
			od, err = worldgen.LoadOverworldFinalDensity(candSeed)
			if err != nil {
				t.Fatalf("LoadOverworldFinalDensity(%d): %v", candSeed, err)
			}
			gen = NewVanillaRegionGenerator(candSeed)
		}
		var seedChunks [][2]int32
		for cz := int32(-384); cz <= 384 && len(seedChunks) < 4; cz += 2 {
			for cx := int32(-384); cx <= 384 && len(seedChunks) < 4; cx += 2 {
				wx, wz := int(cx)*16+8, int(cz)*16+8
				if BiomeNameAt(od, wx, wz) != "minecraft:wooded_badlands" {
					continue
				}
				if od.PreliminarySurfaceLevelAt(wx, wz) < 84 {
					continue
				}
				surfY := preCarveFirstOccupiedHeight(od, candSeed, wx, wz, false)
				if surfY < 106 {
					continue
				}
				s2D := worldgen.SampleColumn2D(od, SeaLevel, wx, wz)
				if biomeNameByID(BiomeAt3D(od, s2D, wx, surfY, wz)) != "minecraft:wooded_badlands" {
					continue
				}
				seedChunks = append(seedChunks, [2]int32{cx, cz})
			}
		}
		if len(seedChunks) == 0 {
			continue
		}
		highChunks = append(highChunks, seedChunks...)
		for _, cc := range seedChunks {
			woodedHigh := gen(cc[0], cc[1])
			for lz := 0; lz < 16; lz++ {
				for lx := 0; lx < 16; lx++ {
					for y := 60; y < MinY+WorldHeight; y++ {
						st, _ := stateByID(woodedHigh.GetBlock(lx, y, lz))
						if y >= 98 && (st.Name == "minecraft:grass_block" || st.Name == "minecraft:coarse_dirt") {
							highCapSoil++
						}
						if y < 95 && st.Name == "minecraft:grass_block" {
							lowGrass++
						}
						if y >= 98 && st.Name == "minecraft:oak_log" {
							oakLogs++
						}
					}
				}
			}
			if highCapSoil > 0 && oakLogs > 0 {
				break
			}
		}
	}
	if len(highChunks) == 0 {
		t.Fatal("found no high-plateau minecraft:wooded_badlands chunk across candidate seeds")
	}
	if highCapSoil == 0 || oakLogs == 0 {
		t.Fatalf("wooded_badlands high plateau %v expected high plateau cap and oak trees: highCapSoil=%d oakLogs=%d",
			highChunks, highCapSoil, oakLogs)
	}
	if lowGrass != 0 {
		t.Fatalf("wooded_badlands high plateau %v unexpectedly placed grass_block below plateau threshold: count=%d",
			highChunks, lowGrass)
	}
}

func TestAcaciaFallenOakAndGoldExtraParity(t *testing.T) {
	const seed int64 = 16180339
	owGen := NewVanillaRegionGenerator(seed)

	// Savanna chunk: acacia logs + non-persistent leaves with valid distance (1..7).
	sav := owGen(31, 11)
	if !chunkHasBiome(sav, "minecraft:savanna") {
		t.Fatal("chunk (31,11) does not contain minecraft:savanna")
	}
	var acaciaLogs, acaciaLeaves int
	for y := 63; y < MinY+WorldHeight; y++ {
		for lz := 0; lz < 16; lz++ {
			for lx := 0; lx < 16; lx++ {
				st, _ := stateByID(sav.GetBlock(lx, y, lz))
				switch st.Name {
				case "minecraft:acacia_log":
					acaciaLogs++
				case "minecraft:acacia_leaves":
					acaciaLeaves++
					if st.Properties["persistent"] != "false" {
						t.Fatalf("acacia_leaves (%d,%d,%d) persistent=%q, want false", lx, y, lz, st.Properties["persistent"])
					}
					d := st.Properties["distance"]
					if len(d) != 1 || d[0] < '1' || d[0] > '7' {
						t.Fatalf("acacia_leaves (%d,%d,%d) invalid distance=%q", lx, y, lz, d)
					}
				}
			}
		}
	}
	if acaciaLogs == 0 || acaciaLeaves == 0 {
		t.Fatalf("savanna (31,11) missing acacia trees: logs=%d leaves=%d", acaciaLogs, acaciaLeaves)
	}

	// Badlands extra high-altitude gold ore (y >= 32, triangle plateau in badlands biomes).
	badlands := owGen(-185, 52)
	if !chunkHasBiome(badlands, "minecraft:badlands") {
		t.Fatal("chunk (-185,52) does not contain minecraft:badlands")
	}
	var highGold int
	for y := 32; y < MinY+WorldHeight; y++ {
		for lz := 0; lz < 16; lz++ {
			for lx := 0; lx < 16; lx++ {
				st, _ := stateByID(badlands.GetBlock(lx, y, lz))
				if st.Name == "minecraft:gold_ore" {
					highGold++
				}
			}
		}
	}
	if highGold == 0 {
		t.Fatal("badlands (-185,52) expected high-elevation minecraft:gold_ore (y >= 32) from ore_gold_extra")
	}

	// Fallen oak tree cleanup: replacing lower tall_grass clears orphaned upper tall_grass via updateShapeAtEdge.
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}
	fallenCfg, err := set.FallenTree("minecraft:fallen_oak_tree")
	if err != nil {
		t.Fatalf("FallenTree(minecraft:fallen_oak_tree): %v", err)
	}
	reg := buildFlatTestDecorationRegion(t, 0, 0, 0, 63, false)
	dirtID := testMustStateID(t, "minecraft:dirt", nil)
	tallLower := testMustStateID(t, "minecraft:tall_grass", map[string]string{"half": "lower"})
	tallUpper := testMustStateID(t, "minecraft:tall_grass", map[string]string{"half": "upper"})
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			reg.setBlock(x, 63, z, dirtID)
			reg.setBlock(x, 64, z, tallLower)
			reg.setBlock(x, 65, z, tallUpper)
		}
	}
	rng := worldgen.NewXoroshiro(424242)
	if err := reg.placeFallenTree(set, rng, worldgen.FeaturePosition{X: 8, Y: 64, Z: 8}, fallenCfg); err != nil {
		t.Fatalf("placeFallenTree: %v", err)
	}
	var placedLogs int
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			st64, _ := stateByID(reg.getBlock(x, 64, z))
			st65, _ := stateByID(reg.getBlock(x, 65, z))
			if st64.Name == "minecraft:oak_log" {
				placedLogs++
				if st65.Name == "minecraft:tall_grass" && st65.Properties["half"] == "upper" {
					t.Fatalf("orphaned tall_grass[half=upper] remained at (%d,65,%d) above fallen oak_log", x, z)
				}
			}
		}
	}
	if placedLogs < 2 {
		t.Fatalf("placeFallenTree placed only %d logs, expected stump + fallen trunk", placedLogs)
	}
}

func TestMineshaftMesaAndPostProcessShapesParity(t *testing.T) {
	const seed int64 = 16180339
	owGen := NewVanillaRegionGenerator(seed)

	c := owGen(-247, 86)
	var planks, fences, rails, chains int
	for y := 63; y < MinY+WorldHeight; y++ {
		for lz := 0; lz < 16; lz++ {
			for lx := 0; lx < 16; lx++ {
				st, _ := stateByID(c.GetBlock(lx, y, lz))
				switch st.Name {
				case "minecraft:dark_oak_planks":
					planks++
				case "minecraft:dark_oak_fence":
					fences++
				case "minecraft:rail":
					rails++
				case "minecraft:iron_chain":
					chains++
				}
			}
		}
	}
	if planks == 0 || fences == 0 || rails == 0 || chains == 0 {
		t.Fatalf("mineshaft_mesa (-247,86) missing surface mineshaft blocks: planks=%d fences=%d rails=%d chains=%d",
			planks, fences, rails, chains)
	}

	// Regression for fillPillarDownOrChainUp + msCanHangChainBelow at world (-3945, 94..106, 1379) (local 7, y, 3):
	// Lower bridge chain hangs up to terracotta ceiling at y=106, and upper overlapping bridge planks at y=98
	// must continue scanning upward despite iron_chain at y=97 below it, hanging fence at y=99 below iron_chain at y=100.
	checkBlock := func(y int, wantName string, wantProps map[string]string) {
		t.Helper()
		st, _ := stateByID(c.GetBlock(7, y, 3))
		if st.Name != wantName {
			t.Fatalf("(-247,86) local (7,%d,3) = %s %v, want %s %v", y, st.Name, st.Properties, wantName, wantProps)
		}
		for k, v := range wantProps {
			if st.Properties[k] != v {
				t.Fatalf("(-247,86) local (7,%d,3) %s[%s]=%q, want %q", y, wantName, k, st.Properties[k], v)
			}
		}
	}
	unconnectedFence := map[string]string{"north": "false", "south": "false", "east": "false", "west": "false", "waterlogged": "false"}
	chainProps := map[string]string{"axis": "y", "waterlogged": "false"}
	checkBlock(94, "minecraft:dark_oak_planks", nil)
	checkBlock(95, "minecraft:dark_oak_fence", unconnectedFence)
	checkBlock(96, "minecraft:iron_chain", chainProps)
	checkBlock(97, "minecraft:iron_chain", chainProps)
	checkBlock(98, "minecraft:dark_oak_planks", nil)
	checkBlock(99, "minecraft:dark_oak_fence", unconnectedFence)
	for y := 100; y <= 105; y++ {
		checkBlock(y, "minecraft:iron_chain", chainProps)
	}
	checkBlock(106, "minecraft:light_gray_terracotta", nil)

	// Direct unit check of SHAPE_CHECK_BLOCKS post-processing (marked fences connect, unmarked chain fences do not,
	// and unsupported marked wall_torches are removed).
	reg := buildFlatTestDecorationRegion(t, 0, 0, 0, 63, false)
	fenceDefault := testMustStateID(t, "minecraft:dark_oak_fence", unconnectedFence)
	wallTorchNorth := testMustStateID(t, "minecraft:wall_torch", map[string]string{"facing": "north"})

	reg.setBlock(6, 64, 6, fenceDefault)
	reg.setBlock(7, 64, 6, fenceDefault)
	reg.markForPostProcessing(6, 64, 6)
	reg.markForPostProcessing(7, 64, 6)

	reg.setBlock(10, 64, 10, fenceDefault)
	reg.setBlock(11, 64, 10, fenceDefault) // purposefully not marked

	reg.setBlock(4, 64, 4, wallTorchNorth)
	reg.markForPostProcessing(4, 64, 4) // support at (4,64,5) is air

	reg.runPostProcessing(nil)

	if st, _ := stateByID(reg.getBlock(6, 64, 6)); st.Properties["east"] != "true" {
		t.Fatalf("marked fence (6,64,6) east = %q, want true", st.Properties["east"])
	}
	if st, _ := stateByID(reg.getBlock(7, 64, 6)); st.Properties["west"] != "true" {
		t.Fatalf("marked fence (7,64,6) west = %q, want true", st.Properties["west"])
	}
	if st, _ := stateByID(reg.getBlock(10, 64, 10)); st.Properties["east"] != "false" {
		t.Fatalf("unmarked fence (10,64,10) east = %q, want false (unmarked blocks untouched)", st.Properties["east"])
	}
	if st, _ := stateByID(reg.getBlock(4, 64, 4)); st.Name != "minecraft:air" {
		t.Fatalf("unsupported marked wall_torch (4,64,4) = %s, want minecraft:air", st.Name)
	}
}

func TestSavannaAndBadlandsMultiSeedParity(t *testing.T) {
	parsed, ok := ParseWorldSeed("savanna-mesa-2612")
	if !ok || parsed != 1668348391 {
		t.Fatalf("ParseWorldSeed(savanna-mesa-2612) = (%d, %v), want (1668348391, true)", parsed, ok)
	}

	type targetChunk struct {
		cx, cz int32
		biome  string
	}
	seeds := []struct {
		name   string
		seed   int64
		chunks []targetChunk
	}{
		{
			name: "16180339",
			seed: 16180339,
			chunks: []targetChunk{
				{31, 11, "minecraft:savanna"},
				{16, 32, "minecraft:savanna_plateau"},
				{-52, 80, "minecraft:windswept_savanna"},
				{-185, 52, "minecraft:badlands"},
				{-239, 97, "minecraft:eroded_badlands"},
				{-164, 80, "minecraft:wooded_badlands"},
			},
		},
		{
			name: "-777888999",
			seed: -777888999,
			chunks: []targetChunk{
				{-4, 20, "minecraft:savanna"},
				{0, 20, "minecraft:savanna_plateau"},
				{92, 104, "minecraft:windswept_savanna"},
				{-40, 1, "minecraft:badlands"},
				{-44, 4, "minecraft:eroded_badlands"},
				{12, -21, "minecraft:wooded_badlands"},
			},
		},
		{
			name: "savanna-mesa-2612",
			seed: parsed,
			chunks: []targetChunk{
				{16, -36, "minecraft:savanna"},
				{27, -77, "minecraft:savanna_plateau"},
				{68, -16, "minecraft:windswept_savanna"},
				{-16, 4, "minecraft:badlands"},
				{-56, 12, "minecraft:eroded_badlands"},
				{-17, 13, "minecraft:wooded_badlands"},
			},
		},
	}

	for si, sc := range seeds {
		if si > 0 && testing.Short() {
			continue
		}
		t.Run(sc.name, func(t *testing.T) {
			owGen := NewVanillaRegionGenerator(sc.seed)
			for _, tc := range sc.chunks {
				c := owGen(tc.cx, tc.cz)
				if !chunkHasBiome(c, tc.biome) {
					t.Errorf("chunk (%d,%d) missing biome %s", tc.cx, tc.cz, tc.biome)
				}
				surface, motion, _ := c.heightmaps()
				for i := 0; i < 256; i++ {
					if surface[i] < motion[i] {
						t.Fatalf("chunk (%d,%d) col %d heightmap WORLD_SURFACE=%d < MOTION_BLOCKING=%d",
							tc.cx, tc.cz, i, surface[i], motion[i])
					}
				}
				for y := MinY + 1; y < MinY+WorldHeight-1; y++ {
					for lz := 0; lz < 16; lz++ {
						for lx := 0; lx < 16; lx++ {
							st, _ := stateByID(c.GetBlock(lx, y, lz))
							if isDoubleHeightBlock(st.Name) {
								half := st.Properties["half"]
								if half == "upper" {
									below, _ := stateByID(c.GetBlock(lx, y-1, lz))
									if below.Name != st.Name || below.Properties["half"] != "lower" {
										t.Fatalf("chunk (%d,%d) orphaned upper %s at (%d,%d,%d)", tc.cx, tc.cz, st.Name, lx, y, lz)
									}
								} else if half == "lower" {
									above, _ := stateByID(c.GetBlock(lx, y+1, lz))
									if above.Name != st.Name || above.Properties["half"] != "upper" {
										t.Fatalf("chunk (%d,%d) orphaned lower %s at (%d,%d,%d)", tc.cx, tc.cz, st.Name, lx, y, lz)
									}
								}
							}
							if st.Name == "minecraft:cactus" {
								above := c.GetBlock(lx, y+1, lz)
								if isLiquidBlockState(above) {
									t.Fatalf("chunk (%d,%d) cactus under liquid at (%d,%d,%d)", tc.cx, tc.cz, lx, y, lz)
								}
								below, _ := stateByID(c.GetBlock(lx, y-1, lz))
								if below.Name != "minecraft:cactus" && below.Name != "minecraft:sand" && below.Name != "minecraft:red_sand" {
									t.Fatalf("chunk (%d,%d) cactus on invalid base %s at (%d,%d,%d)", tc.cx, tc.cz, below.Name, lx, y, lz)
								}
								if lx > 0 && lx < 15 && lz > 0 && lz < 15 {
									for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
										nb := c.GetBlock(lx+d[0], y, lz+d[1])
										if isSolidState(nb) || isLavaState(nb) {
											t.Fatalf("chunk (%d,%d) cactus beside solid/lava at (%d,%d,%d)", tc.cx, tc.cz, lx, y, lz)
										}
									}
								}
							}
							if st.Name == "minecraft:sugar_cane" {
								below, _ := stateByID(c.GetBlock(lx, y-1, lz))
								if below.Name == "" || below.Name == "minecraft:air" || below.Name == "minecraft:stone" {
									t.Fatalf("chunk (%d,%d) unsupported sugar_cane on %s at (%d,%d,%d)", tc.cx, tc.cz, below.Name, lx, y, lz)
								}
							}
						}
					}
				}
				nbtData := chunkToNBT(c)
				roundTripped, err := nbtToChunk(nbtData, int(c.X>>5), int(c.Z>>5), int(c.X&31), int(c.Z&31))
				if err != nil {
					t.Fatalf("chunk (%d,%d) NBT round-trip error: %v", tc.cx, tc.cz, err)
				}
				if roundTripped.GetBlock(7, 64, 7) != c.GetBlock(7, 64, 7) {
					t.Fatalf("chunk (%d,%d) NBT round-trip mismatch at (7,64,7)", tc.cx, tc.cz)
				}
			}
		})
	}
}
