package world

import (
	"strconv"
	"testing"

	"regionio/internal/worldgen"
)

func TestWorldGenRegionRandomAndPaleMossCarpetPlaceAt(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}

	// 1. WorldGenRegionRandom must mirror
	//    NewXoroshiro(seed).ForkPositional().FromHashOf("minecraft:worldgen_region_random").ForkPositional().At(cx<<4, 0, cz<<4).
	const testSeed int64 = 27182818
	gotRand := worldgen.WorldGenRegionRandom(testSeed, -3, 5)
	wantRand := worldgen.NewXoroshiro(testSeed).
		ForkPositional().
		FromHashOf("minecraft:worldgen_region_random").
		ForkPositional().
		At(-48, 0, 80)
	for i := 0; i < 8; i++ {
		if g, w := gotRand.NextLong(), wantRand.NextLong(); g != w {
			t.Fatalf("WorldGenRegionRandom draw %d = %d, want %d", i, g, w)
		}
	}

	// 2. MossyCarpetBlock.placeAt consumes WorldGenRegion.getRandom() (regionRandom), NOT
	//    FeaturePlaceContext.random(), computes N/E/S/W wall sides, and spawns a bottom=false
	//    topper above when pos.above() is air or replaceable.
	paleMossBlockID, _ := nameToStateID("minecraft:pale_moss_block", nil)
	paleLogID, _ := nameToStateID("minecraft:pale_oak_log", map[string]string{"axis": "y"})
	stoneID := StateStone
	paleLeavesID, _ := nameToStateID("minecraft:pale_oak_leaves", map[string]string{
		"distance": "7", "persistent": "false", "waterlogged": "false",
	})
	shortGrassID, _ := nameToStateID("minecraft:short_grass", nil)

	reg := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
	reg.bindSeed(testSeed)
	reg.setBlock(8, 63, 8, paleMossBlockID)
	// Place wall supports around (8, 64, 8) and (8, 65, 8):
	// North (8,*,7): stone -> full sturdy cube (supports wall)
	reg.setBlock(8, 64, 7, stoneID)
	reg.setBlock(8, 65, 7, stoneID)
	// East (9,*,8): pale_oak_log -> full sturdy cube (supports wall)
	reg.setBlock(9, 64, 8, paleLogID)
	reg.setBlock(9, 65, 8, paleLogID)
	// West (7,*,8): pale_oak_leaves only at y=64 (canMultifaceAttachTo=true at 64, air at 65)
	reg.setBlock(7, 64, 8, paleLeavesID)
	// Replaceable short_grass at pos.above() = (8, 65, 8) -> allowed for topper placement!
	reg.setBlock(8, 65, 8, shortGrassID)

	featureRng := worldgen.NewXoroshiro(999111)
	featureRngCheck := worldgen.NewXoroshiro(999111)

	simpleCfg, err := set.SimpleBlock("minecraft:pale_moss_vegetation")
	if err != nil {
		t.Fatalf("SimpleBlock(pale_moss_vegetation): %v", err)
	}
	// Directly test pale_moss_carpet SimpleBlock placement without touching random for weighted selection.
	carpetOnlyCfg := worldgen.SimpleBlockFeatureConfig{
		Provider: worldgen.StateProviderSpec{Type: "simple"},
		States: []worldgen.WeightedBlockState{
			{Weight: 1, State: worldgen.BlockState{
				Name: "minecraft:pale_moss_carpet",
				Properties: map[string]string{
					"bottom": "true",
					"north":  "none",
					"east":   "none",
					"south":  "none",
					"west":   "none",
				},
			}},
		},
	}
	_ = simpleCfg

	expectedRegionRng := worldgen.WorldGenRegionRandom(testSeed, 0, 0)
	// Two sides supported at y=65: north (stone) and east (pale_oak_log).
	wantNorth := "none"
	if expectedRegionRng.NextBoolean() {
		wantNorth = "low"
	}
	wantEast := "none"
	if expectedRegionRng.NextBoolean() {
		wantEast = "low"
	}

	if !reg.placeSimpleBlockFeature(featureRng, worldgen.FeaturePosition{X: 8, Y: 64, Z: 8}, carpetOnlyCfg, set) {
		t.Fatal("placeSimpleBlockFeature(pale_moss_carpet) returned false, want true")
	}
	if g, w := featureRng.NextLong(), featureRngCheck.NextLong(); g != w {
		t.Fatalf("pale_moss_carpet consumed feature random instead of regionRandom: got %d, want %d", g, w)
	}
	baseSt, _ := stateByID(reg.getBlock(8, 64, 8))
	if baseSt.Name != "minecraft:pale_moss_carpet" || baseSt.Properties["bottom"] != "true" {
		t.Fatalf("base state = %+v, want pale_moss_carpet[bottom=true]", baseSt)
	}
	if baseSt.Properties["west"] != "low" || baseSt.Properties["south"] != "none" {
		t.Fatalf("base west/south = (%s,%s), want (low,none)", baseSt.Properties["west"], baseSt.Properties["south"])
	}
	wantBaseNorth := "low"
	if wantNorth == "low" {
		wantBaseNorth = "tall"
	}
	wantBaseEast := "low"
	if wantEast == "low" {
		wantBaseEast = "tall"
	}
	if wantNorth == "none" && wantEast == "none" {
		// No topper placed; short_grass at (8,65,8) remains untouched and regionRandom next draw matches.
		if stAbove, _ := stateByID(reg.getBlock(8, 65, 8)); stAbove.Name != "minecraft:short_grass" {
			t.Fatalf("when all topper rolls fail, (8,65,8) = %s, want short_grass", stAbove.Name)
		}
	} else {
		topSt, _ := stateByID(reg.getBlock(8, 65, 8))
		if topSt.Name != "minecraft:pale_moss_carpet" || topSt.Properties["bottom"] != "false" ||
			topSt.Properties["north"] != wantNorth || topSt.Properties["east"] != wantEast {
			t.Fatalf("topper state = %+v, want bottom=false north=%s east=%s", topSt, wantNorth, wantEast)
		}
	}
	if baseSt.Properties["north"] != wantBaseNorth || baseSt.Properties["east"] != wantBaseEast {
		t.Fatalf("base north/east = (%s,%s), want (%s,%s)",
			baseSt.Properties["north"], baseSt.Properties["east"], wantBaseNorth, wantBaseEast)
	}
	if g, w := reg.regionRandom().NextLong(), expectedRegionRng.NextLong(); g != w {
		t.Fatalf("regionRandom post-placement desync: got %d, want %d", g, w)
	}

	// Non-replaceable block above must draw ZERO booleans from regionRandom.
	reg.setBlock(4, 63, 4, paleMossBlockID)
	reg.setBlock(4, 64, 3, stoneID)
	reg.setBlock(4, 65, 3, stoneID)
	reg.setBlock(4, 65, 4, paleLogID) // non-replaceable log blocks topper
	if !reg.placeSimpleBlockFeature(featureRng, worldgen.FeaturePosition{X: 4, Y: 64, Z: 4}, carpetOnlyCfg, set) {
		t.Fatal("placeSimpleBlockFeature with non-replaceable above returned false")
	}
	if g, w := reg.regionRandom().NextLong(), expectedRegionRng.NextLong(); g != w {
		t.Fatalf("non-replaceable above unexpectedly drew from regionRandom: got %d, want %d", g, w)
	}

	// 3. TreeFeature updateShapeAtEdge cleanup:
	//    - overwriting pale_moss_carpet[bottom=true] with a tree removes its orphaned bottom=false topper
	//    - overwriting tall_grass[half=lower] removes orphaned tall_grass[half=upper]
	//    - placing a trunk log adjacent to pale_moss_carpet[bottom=true] updates its wall face to "low"
	//    - placing pale_hanging_moss above pale_hanging_moss turns upper tip from "true" to "false"
	regEdge := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
	topperCarpet, _ := nameToStateID("minecraft:pale_moss_carpet", map[string]string{
		"bottom": "false", "north": "low", "east": "none", "south": "none", "west": "none",
	})
	baseCarpet, _ := nameToStateID("minecraft:pale_moss_carpet", map[string]string{
		"bottom": "true", "north": "none", "east": "none", "south": "none", "west": "none",
	})
	lowerTall, _ := nameToStateID("minecraft:tall_grass", map[string]string{"half": "lower"})
	upperTall, _ := nameToStateID("minecraft:tall_grass", map[string]string{"half": "upper"})
	hangTip, _ := nameToStateID("minecraft:pale_hanging_moss", map[string]string{"tip": "true"})

	// Case A: trunk at (8,64,8) overwrites bottom=true carpet (which had bottom=false topper at y=65)
	regEdge.setBlock(8, 64, 7, stoneID)
	regEdge.setBlock(8, 65, 7, stoneID)
	regEdge.setBlock(8, 64, 8, baseCarpet)
	regEdge.setBlock(8, 65, 8, topperCarpet)
	// Case B: neighboring carpet at (9,64,8) sits east of the trunk placed at (8,64,8)
	regEdge.setBlock(9, 64, 8, baseCarpet)
	// Case C: trunk at (8,64,9) overwrites lower tall_grass at (8,64,9) whose upper half is at (8,65,9)
	regEdge.setBlock(8, 64, 9, lowerTall)
	regEdge.setBlock(8, 65, 9, upperTall)
	// Case D: decoration places pale_hanging_moss[tip=true] at (10,66,8) under a log, directly above existing hangTip at (10,65,8)
	regEdge.setBlock(10, 67, 8, paleLogID)
	regEdge.setBlock(10, 65, 8, hangTip)
	regEdge.setBlock(10, 66, 8, hangTip)
	// Replace simulated tree blocks at (8,64,8) and (8,64,9) with pale_oak_log
	regEdge.setBlock(8, 64, 8, paleLogID)
	regEdge.setBlock(8, 64, 9, paleLogID)

	tp := &treePlacer{
		r:   regEdge,
		set: set,
		placements: []treePlacement{
			{x: 8, y: 64, z: 8, isLog: true},
			{x: 8, y: 64, z: 9, isLog: true},
		},
		decorations: []pos3{
			{10, 66, 8},
		},
	}
	tp.updateShapeAtEdge(nil)

	if !isAirState(regEdge.getBlock(8, 65, 8)) {
		t.Fatalf("orphaned pale_moss_carpet[bottom=false] at (8,65,8) not cleared: %s", stateLabel(regEdge.getBlock(8, 65, 8)))
	}
	if !isAirState(regEdge.getBlock(8, 65, 9)) {
		t.Fatalf("orphaned tall_grass[half=upper] at (8,65,9) not cleared: %s", stateLabel(regEdge.getBlock(8, 65, 9)))
	}
	adjCarpet, _ := stateByID(regEdge.getBlock(9, 64, 8))
	if adjCarpet.Name != "minecraft:pale_moss_carpet" || adjCarpet.Properties["west"] != "low" {
		t.Fatalf("adjacent pale_moss_carpet west face = %+v, want west=low", adjCarpet)
	}
	upperHang, _ := stateByID(regEdge.getBlock(10, 66, 8))
	if upperHang.Name != "minecraft:pale_hanging_moss" || upperHang.Properties["tip"] != "false" {
		t.Fatalf("upper pale_hanging_moss = %+v, want tip=false", upperHang)
	}
}

func TestCreakingHeartAndPaleMossDecoratorsParity(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}
	creakingCfg, err := set.Tree("minecraft:pale_oak_creaking")
	if err != nil {
		t.Fatalf("Tree(pale_oak_creaking): %v", err)
	}

	// 1. Place pale_oak_creaking at chunk center (8,71,8) -> creaking_heart inside ±1 region
	//    must have matching StructureBlockEntity, survive Anvil chunkToNBT/nbtToChunk round-trip,
	//    and be removed if overwritten by stone.
	var (
		checkedHeart   bool
		checkedCarpet  bool
		checkedHanging bool
	)
	grassID, _ := nameToStateID("minecraft:grass_block", map[string]string{"snowy": "false"})
	dirtID, _ := nameToStateID("minecraft:dirt", nil)

	for seed := int64(1); seed <= 60; seed++ {
		reg := buildFlatTestDecorationRegion(t, 0, 0, 1, 70, false)
		for cx := int32(-1); cx <= 1; cx++ {
			for cz := int32(-1); cz <= 1; cz++ {
				ch := reg.chunks[[2]int32{cx, cz}]
				for x := 0; x < 16; x++ {
					for z := 0; z < 16; z++ {
						for y := 64; y < 70; y++ {
							ch.SetBlock(x, y, z, dirtID)
						}
						ch.SetBlock(x, 70, z, grassID)
					}
				}
			}
		}
		rng := worldgen.NewXoroshiro(seed * 7919)
		if err := reg.placeTree(set, rng, worldgen.FeaturePosition{X: 8, Y: 71, Z: 8}, creakingCfg); err != nil {
			t.Fatalf("placeTree(pale_oak_creaking): %v", err)
		}

		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				for y := 66; y <= 90; y++ {
					st, ok := stateByID(reg.getBlock(x, y, z))
					if !ok {
						continue
					}
					switch st.Name {
					case "minecraft:creaking_heart":
						checkedHeart = true
						ch := reg.chunks[[2]int32{0, 0}]
						be, hasBE := ch.StructureBlockEntityAt(x, y, z)
						if !hasBE || be.ID != "minecraft:creaking_heart" {
							t.Fatalf("missing creaking_heart block entity at (%d,%d,%d)", x, y, z)
						}
						// Verify NBT persistence round-trip.
						reloaded, err := nbtToChunk(chunkToNBT(ch), 0, 0, 0, 0)
						if err != nil {
							t.Fatalf("nbtToChunk: %v", err)
						}
						if rbe, ok := reloaded.StructureBlockEntityAt(x, y, z); !ok || rbe.ID != "minecraft:creaking_heart" {
							t.Fatalf("reloaded chunk missing creaking_heart BE at (%d,%d,%d): %+v, %v", x, y, z, rbe, ok)
						}
						// Verify overwriting creaking_heart clears its StructureBlockEntity.
						reg.setBlock(x, y, z, StateStone)
						if _, stillHas := ch.StructureBlockEntityAt(x, y, z); stillHas {
							t.Fatalf("overwriting creaking_heart at (%d,%d,%d) left stale block entity", x, y, z)
						}
					case "minecraft:pale_moss_carpet":
						checkedCarpet = true
						if st.Properties["bottom"] == "false" {
							below, _ := stateByID(reg.getBlock(x, y-1, z))
							if below.Name != "minecraft:pale_moss_carpet" || below.Properties["bottom"] != "true" {
								t.Fatalf("topper carpet at (%d,%d,%d) without bottom=true below (got %+v)", x, y, z, below)
							}
						}
					case "minecraft:pale_hanging_moss":
						checkedHanging = true
						below, _ := stateByID(reg.getBlock(x, y-1, z))
						wantTip := strconv.FormatBool(below.Name != "minecraft:pale_hanging_moss")
						if st.Properties["tip"] != wantTip {
							t.Fatalf("pale_hanging_moss at (%d,%d,%d) has tip=%s, want %s", x, y, z, st.Properties["tip"], wantTip)
						}
					}
				}
			}
		}
		if checkedHeart && checkedCarpet && checkedHanging {
			break
		}
	}
	if !checkedHeart || !checkedCarpet || !checkedHanging {
		t.Fatalf("incomplete pale_oak_creaking checks: heart=%v carpet=%v hanging=%v", checkedHeart, checkedCarpet, checkedHanging)
	}

	// 2. CreakingHeartDecorator outside ±1 write radius must NOT register a StructureBlockEntity.
	regGuard := buildFlatTestDecorationRegion(t, 0, 0, 2, 70, false)
	paleLogID, _ := nameToStateID("minecraft:pale_oak_log", map[string]string{"axis": "y"})
	// Place a 3D cross of logs at world x=35 (chunk 2, outside source (0,0)'s ±1 guard).
	var guardLogs []treePlacement
	for _, off := range [][3]int{{0, 0, 0}, {1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
		regGuard.setBlockGlobal(35+off[0], 72+off[1], 8+off[2], paleLogID)
		guardLogs = append(guardLogs, treePlacement{x: 35 + off[0], y: 72 + off[1], z: 8 + off[2], isLog: true})
	}
	dec := &treeDecoration{
		t:    &treePlacer{r: regGuard, set: set, random: worldgen.NewXoroshiro(42)},
		logs: guardLogs,
	}
	if err := dec.placeCreakingHeart(1.0); err != nil {
		t.Fatalf("placeCreakingHeart: %v", err)
	}
	outChunk := regGuard.chunks[[2]int32{2, 0}]
	if be, ok := outChunk.StructureBlockEntityAt(35, 72, 8); ok {
		t.Fatalf("placeCreakingHeart wrote StructureBlockEntity outside ±1 write radius: %+v", be)
	}
}

func TestDarkOakAndHugeMushroomsParity(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}

	grassID, _ := nameToStateID("minecraft:grass_block", map[string]string{"snowy": "false"})
	dirtID, _ := nameToStateID("minecraft:dirt", nil)
	setupGrassFloor := func(reg *decorationRegion) {
		for cx := int32(-1); cx <= 1; cx++ {
			for cz := int32(-1); cz <= 1; cz++ {
				ch := reg.chunks[[2]int32{cx, cz}]
				for x := 0; x < 16; x++ {
					for z := 0; z < 16; z++ {
						for y := 60; y < 63; y++ {
							ch.SetBlock(x, y, z, dirtID)
						}
						ch.SetBlock(x, 63, z, grassID)
					}
				}
			}
		}
	}

	// 1. Dark oak tree + dark_oak_leaf_litter
	darkOakLeafCfg, err := set.Tree("minecraft:dark_oak_leaf_litter")
	if err != nil {
		t.Fatalf("Tree(dark_oak_leaf_litter): %v", err)
	}
	var gotDarkLogs, gotDarkLeaves, gotLitter int
	for attempt := int64(0); attempt < 25; attempt++ {
		reg := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
		setupGrassFloor(reg)
		rng := worldgen.NewXoroshiro(1000 + attempt*37)
		if err := reg.placeTree(set, rng, worldgen.FeaturePosition{X: 8, Y: 64, Z: 8}, darkOakLeafCfg); err != nil {
			t.Fatalf("placeTree(dark_oak_leaf_litter): %v", err)
		}
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				for y := 64; y < 85; y++ {
					st, ok := stateByID(reg.getBlock(x, y, z))
					if !ok {
						continue
					}
					switch st.Name {
					case "minecraft:dark_oak_log":
						gotDarkLogs++
					case "minecraft:dark_oak_leaves":
						gotDarkLeaves++
						if d, _ := strconv.Atoi(st.Properties["distance"]); d < 1 || d > 6 {
							t.Fatalf("dark_oak_leaves at (%d,%d,%d) has unexpected distance=%q", x, y, z, st.Properties["distance"])
						}
					case "minecraft:leaf_litter":
						gotLitter++
					}
				}
			}
		}
		if gotDarkLogs > 0 && gotDarkLeaves > 0 && gotLitter > 0 {
			break
		}
	}
	if gotDarkLogs == 0 || gotDarkLeaves == 0 || gotLitter == 0 {
		t.Fatalf("dark_oak_leaf_litter incomplete: logs=%d leaves=%d litter=%d", gotDarkLogs, gotDarkLeaves, gotLitter)
	}

	// 2. HugeBrownMushroomFeature: foliage_radius=3 flat 7x7 cap excluding corners, up=true,down=false
	brownCfg, err := set.HugeMushroom("minecraft:huge_brown_mushroom")
	if err != nil {
		t.Fatalf("HugeMushroom(huge_brown_mushroom): %v", err)
	}
	regBrown := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
	setupGrassFloor(regBrown)
	if err := regBrown.placeHugeMushroom(set, worldgen.NewXoroshiro(424242), worldgen.FeaturePosition{X: 8, Y: 64, Z: 8}, "minecraft:huge_brown_mushroom", brownCfg); err != nil {
		t.Fatalf("placeHugeMushroom(brown): %v", err)
	}
	brownCapCount := 0
	stemCount := 0
	for x := 4; x <= 12; x++ {
		for z := 4; z <= 12; z++ {
			for y := 64; y <= 80; y++ {
				st, ok := stateByID(regBrown.getBlock(x, y, z))
				if !ok {
					continue
				}
				if st.Name == "minecraft:brown_mushroom_block" {
					brownCapCount++
					if st.Properties["up"] != "true" || st.Properties["down"] != "false" {
						t.Fatalf("brown mushroom cap at (%d,%d,%d) up/down = (%s,%s), want (true,false)",
							x, y, z, st.Properties["up"], st.Properties["down"])
					}
					if x == 8 && z == 8 && (st.Properties["north"] != "false" || st.Properties["south"] != "false" || st.Properties["east"] != "false" || st.Properties["west"] != "false") {
						t.Fatalf("brown mushroom cap center at (%d,%d,%d) has outer faces true: %+v", x, y, z, st.Properties)
					}
				} else if st.Name == "minecraft:mushroom_stem" {
					stemCount++
					if st.Properties["up"] != "false" || st.Properties["down"] != "false" {
						t.Fatalf("mushroom_stem at (%d,%d,%d) up/down = (%s,%s), want (false,false)",
							x, y, z, st.Properties["up"], st.Properties["down"])
					}
				}
			}
		}
	}
	// 7x7 without 4 corners = 45 cap blocks; stem height 4..6
	if brownCapCount != 45 || stemCount < 4 {
		t.Fatalf("huge_brown_mushroom blocks = (cap=%d, stem=%d), want (cap=45, stem>=4)", brownCapCount, stemCount)
	}

	// 3. HugeRedMushroomFeature: 4-row dome, bottom cap skirt has up=false, upper rows have up=true
	redCfg, err := set.HugeMushroom("minecraft:huge_red_mushroom")
	if err != nil {
		t.Fatalf("HugeMushroom(huge_red_mushroom): %v", err)
	}
	regRed := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
	setupGrassFloor(regRed)
	if err := regRed.placeHugeMushroom(set, worldgen.NewXoroshiro(424242), worldgen.FeaturePosition{X: 8, Y: 64, Z: 8}, "minecraft:huge_red_mushroom", redCfg); err != nil {
		t.Fatalf("placeHugeMushroom(red): %v", err)
	}
	var sawRedUpTrue, sawRedUpFalse bool
	for x := 4; x <= 12; x++ {
		for z := 4; z <= 12; z++ {
			for y := 64; y <= 80; y++ {
				st, ok := stateByID(regRed.getBlock(x, y, z))
				if !ok || st.Name != "minecraft:red_mushroom_block" {
					continue
				}
				if st.Properties["up"] == "true" {
					sawRedUpTrue = true
				} else if st.Properties["up"] == "false" {
					sawRedUpFalse = true
				}
			}
		}
	}
	if !sawRedUpTrue || !sawRedUpFalse {
		t.Fatalf("huge_red_mushroom missing dome faces: sawUpTrue=%v sawUpFalse=%v", sawRedUpTrue, sawRedUpFalse)
	}

	// 4. fallen_oak_tree and fallen_birch_tree in dark_forest_vegetation: horizontal log
	//    axis and AttachedToLogsDecorator brown/red mushrooms.
	for _, spec := range []struct {
		feature string
		logName string
	}{
		{"minecraft:fallen_oak_tree", "minecraft:oak_log"},
		{"minecraft:fallen_birch_tree", "minecraft:birch_log"},
	} {
		fallenCfg, err := set.FallenTree(spec.feature)
		if err != nil {
			t.Fatalf("FallenTree(%s): %v", spec.feature, err)
		}
		var sawHorizAxis, sawAttachedMushroom bool
		for attempt := int64(0); attempt < 30; attempt++ {
			regFallen := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
			setupGrassFloor(regFallen)
			if err := regFallen.placeFallenTree(set, worldgen.NewXoroshiro(9001+attempt*97), worldgen.FeaturePosition{X: 8, Y: 64, Z: 8}, fallenCfg); err != nil {
				t.Fatalf("placeFallenTree(%s): %v", spec.feature, err)
			}
			for x := 0; x < 16; x++ {
				for z := 0; z < 16; z++ {
					for y := 64; y <= 67; y++ {
						st, ok := stateByID(regFallen.getBlock(x, y, z))
						if !ok {
							continue
						}
						if st.Name == spec.logName && (st.Properties["axis"] == "x" || st.Properties["axis"] == "z") {
							sawHorizAxis = true
						}
						if st.Name == "minecraft:brown_mushroom" || st.Name == "minecraft:red_mushroom" {
							sawAttachedMushroom = true
						}
					}
				}
			}
			if sawHorizAxis && sawAttachedMushroom {
				break
			}
		}
		if !sawHorizAxis || !sawAttachedMushroom {
			t.Fatalf("%s missing horizontal log (%v) or attached mushroom (%v)", spec.feature, sawHorizAxis, sawAttachedMushroom)
		}
	}
}

func TestPaleGardenAndDarkForestMultiSeedParity(t *testing.T) {
	strSeed, ok := ParseWorldSeed("pale-dark-canopy")
	if !ok {
		t.Fatal(`ParseWorldSeed("pale-dark-canopy") returned false`)
	}
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}

	seeds := []struct {
		name string
		seed int64
	}{
		{"27182818", 27182818},
		{"-42424242", -42424242},
		{"pale-dark-canopy", strSeed},
	}

	var (
		totalPaleLogs, totalPaleLeaves, totalCreakingHearts int
		totalMossBlocks, totalCarpetBase, totalCarpetTopper int
		totalCarpetLow, totalCarpetTall                     int
		totalHangingBody, totalHangingTip, totalEyeblossoms int

		totalDarkLogs, totalDarkLeaves, totalLeafLitter int
		totalBrownCaps, totalRedCaps, totalStems        int
	)

	chunksPerBiome := 2
	if testing.Short() {
		chunksPerBiome = 1
	}

	for _, sc := range seeds {
		t.Run(sc.name, func(t *testing.T) {
			od, err := worldgen.LoadOverworldFinalDensity(sc.seed)
			if err != nil {
				t.Fatalf("LoadOverworldFinalDensity(%d): %v", sc.seed, err)
			}

			paleChunks := locateInteriorBiomeChunks(t, od, "minecraft:pale_garden", chunksPerBiome)
			t.Logf("seed %s pale_garden chunks: %v", sc.name, paleChunks)
			for _, cc := range paleChunks {
				reg := decorate3x3AroundCenter(t, od, sc.seed, cc[0], cc[1])
				for dz := int32(-1); dz <= 1; dz++ {
					for dx := int32(-1); dx <= 1; dx++ {
						ch := reg.chunks[[2]int32{cc[0] + dx, cc[1] + dz}]
						for y := MinY; y < MinY+WorldHeight; y++ {
							for lz := 0; lz < 16; lz++ {
								for lx := 0; lx < 16; lx++ {
									st, ok := stateByID(ch.GetBlock(lx, y, lz))
									if !ok {
										continue
									}
									wx, wz := int(cc[0]+dx)*16+lx, int(cc[1]+dz)*16+lz
									switch st.Name {
									case "minecraft:pale_oak_log":
										totalPaleLogs++
									case "minecraft:pale_oak_leaves":
										totalPaleLeaves++
									case "minecraft:creaking_heart":
										totalCreakingHearts++
										if be, ok := ch.StructureBlockEntityAt(wx, y, wz); !ok || be.ID != "minecraft:creaking_heart" {
											t.Fatalf("creaking_heart at (%d,%d,%d) missing StructureBlockEntity: %+v, %v", wx, y, wz, be, ok)
										}
									case "minecraft:pale_moss_block":
										totalMossBlocks++
									case "minecraft:pale_moss_carpet":
										if st.Properties["bottom"] == "true" {
											totalCarpetBase++
											above, _ := stateByID(reg.getBlock(wx, y+1, wz))
											for _, dir := range paleMossHorizDirs {
												switch st.Properties[dir.name] {
												case "low":
													totalCarpetLow++
												case "tall":
													totalCarpetTall++
													if above.Name != "minecraft:pale_moss_carpet" ||
														above.Properties["bottom"] != "false" ||
														above.Properties[dir.name] == "none" {
														t.Fatalf("pale_moss_carpet[bottom=true] at (%d,%d,%d) has tall %s without matching topper above (%+v)",
															wx, y, wz, dir.name, above)
													}
												}
											}
										} else {
											totalCarpetTopper++
											below, _ := stateByID(reg.getBlock(wx, y-1, wz))
											if below.Name != "minecraft:pale_moss_carpet" || below.Properties["bottom"] != "true" {
												t.Fatalf("orphaned pale_moss_carpet[bottom=false] at (%d,%d,%d) over %+v", wx, y, wz, below)
											}
										}
									case "minecraft:pale_hanging_moss":
										above := reg.getBlock(wx, y+1, wz)
										if !canMultifaceAttachTo(above) && !isBlockNamed(above, "minecraft:pale_hanging_moss") {
											t.Fatalf("unsupported pale_hanging_moss at (%d,%d,%d) under %s", wx, y, wz, stateLabel(above))
										}
										below := reg.getBlock(wx, y-1, wz)
										wantTip := strconv.FormatBool(!isBlockNamed(below, "minecraft:pale_hanging_moss"))
										if st.Properties["tip"] != wantTip {
											t.Fatalf("pale_hanging_moss at (%d,%d,%d) tip=%s, want %s (below=%s)",
												wx, y, wz, st.Properties["tip"], wantTip, stateLabel(below))
										}
										if st.Properties["tip"] == "false" {
											totalHangingBody++
										} else {
											totalHangingTip++
										}
									case "minecraft:closed_eyeblossom":
										totalEyeblossoms++
									}
								}
							}
						}
					}
				}
			}

			darkChunks := locateInteriorBiomeChunks(t, od, "minecraft:dark_forest", chunksPerBiome)
			t.Logf("seed %s dark_forest chunks: %v", sc.name, darkChunks)
			for _, cc := range darkChunks {
				reg := decorate3x3AroundCenter(t, od, sc.seed, cc[0], cc[1])
				for dz := int32(-1); dz <= 1; dz++ {
					for dx := int32(-1); dx <= 1; dx++ {
						ch := reg.chunks[[2]int32{cc[0] + dx, cc[1] + dz}]
						for y := MinY; y < MinY+WorldHeight; y++ {
							for lz := 0; lz < 16; lz++ {
								for lx := 0; lx < 16; lx++ {
									st, ok := stateByID(ch.GetBlock(lx, y, lz))
									if !ok {
										continue
									}
									wx, wz := int(cc[0]+dx)*16+lx, int(cc[1]+dz)*16+lz
									switch st.Name {
									case "minecraft:dark_oak_log":
										totalDarkLogs++
									case "minecraft:dark_oak_leaves":
										totalDarkLeaves++
										if st.Properties["persistent"] == "false" {
											if d, _ := strconv.Atoi(st.Properties["distance"]); d < 1 || d > 7 {
												t.Fatalf("dark_oak_leaves at (%d,%d,%d) invalid distance=%q", wx, y, wz, st.Properties["distance"])
											}
										}
									case "minecraft:leaf_litter":
										totalLeafLitter++
										if !isFaceSturdy(reg.getBlock(wx, y-1, wz)) {
											t.Fatalf("leaf_litter at (%d,%d,%d) on non-sturdy %s", wx, y, wz, stateLabel(reg.getBlock(wx, y-1, wz)))
										}
									case "minecraft:brown_mushroom_block":
										totalBrownCaps++
									case "minecraft:red_mushroom_block":
										totalRedCaps++
									case "minecraft:mushroom_stem":
										totalStems++
									}
								}
							}
						}
					}
				}
			}
		})
	}

	_ = set
	if totalPaleLogs == 0 || totalPaleLeaves == 0 || totalCreakingHearts == 0 {
		t.Fatalf("pale_garden trees/hearts missing: logs=%d leaves=%d hearts=%d",
			totalPaleLogs, totalPaleLeaves, totalCreakingHearts)
	}
	if totalMossBlocks == 0 || totalCarpetBase == 0 || totalCarpetTopper == 0 || totalCarpetLow == 0 || totalCarpetTall == 0 {
		t.Fatalf("pale_moss / carpet wall-sides missing: moss=%d base=%d topper=%d low=%d tall=%d",
			totalMossBlocks, totalCarpetBase, totalCarpetTopper, totalCarpetLow, totalCarpetTall)
	}
	if totalHangingBody == 0 || totalHangingTip == 0 || totalEyeblossoms == 0 {
		t.Fatalf("pale_garden hanging_moss/eyeblossoms missing: body=%d tip=%d eyeblossoms=%d",
			totalHangingBody, totalHangingTip, totalEyeblossoms)
	}
	if totalDarkLogs == 0 || totalDarkLeaves == 0 || totalLeafLitter == 0 || totalBrownCaps == 0 || totalRedCaps == 0 || totalStems == 0 {
		t.Fatalf("dark_forest blocks missing: darkLogs=%d darkLeaves=%d litter=%d brown=%d red=%d stems=%d",
			totalDarkLogs, totalDarkLeaves, totalLeafLitter, totalBrownCaps, totalRedCaps, totalStems)
	}
}
