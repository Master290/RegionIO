package world

import (
	"os"
	"strings"
	"testing"

	"regionio/internal/worldgen"
)

func TestStage7ScheduleCoversAllOverworldFeatures(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), possibleBiomeOrder(), undergroundDecorationStage)
	if err != nil {
		t.Fatal(err)
	}
	if len(schedule) != 5 {
		t.Fatalf("stage 7 overworld schedule has %d entries, want 5: %+v", len(schedule), schedule)
	}
	for _, scheduled := range schedule {
		placed, ok := set.Placed[scheduled.Name]
		if !ok {
			t.Fatalf("missing placed feature %s", scheduled.Name)
		}
		configured, ok := set.Configured[placed.Feature]
		if !ok {
			t.Fatalf("missing configured feature %s", placed.Feature)
		}
		switch configured.Type {
		case "minecraft:ore":
			if _, err := set.Ore(placed.Feature); err != nil {
				t.Fatalf("decode ore %s: %v", placed.Feature, err)
			}
		case "minecraft:multiface_growth":
			if _, err := set.MultifaceGrowth(placed.Feature); err != nil {
				t.Fatalf("decode multiface_growth %s: %v", placed.Feature, err)
			}
		case "minecraft:sculk_patch":
			if _, err := set.SculkPatch(placed.Feature); err != nil {
				t.Fatalf("decode sculk_patch %s: %v", placed.Feature, err)
			}
		case "minecraft:dripstone_cluster":
			if _, err := set.DripstoneCluster(placed.Feature); err != nil {
				t.Fatalf("decode dripstone_cluster %s: %v", placed.Feature, err)
			}
		case "minecraft:simple_random_selector":
			if _, err := set.SimpleRandomSelector(placed.Feature); err != nil {
				t.Fatalf("decode simple_random_selector %s: %v", placed.Feature, err)
			}
		default:
			t.Fatalf("unexpected stage 7 configured feature type %s (%s)", configured.Type, scheduled.Name)
		}
	}
}

func TestMultifaceGrowthPlacesGlowLichenAndSpreads(t *testing.T) {
	region, set, _ := springRegion(t, StateStone, nil)
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			region.setBlock(8+dx, 11, 8+dz, StateStone)
		}
	}
	config, err := set.MultifaceGrowth("minecraft:glow_lichen")
	if err != nil {
		t.Fatal(err)
	}
	random := worldgen.NewXoroshiro(42)
	if !region.placeMultifaceGrowth(set, random, worldgen.FeaturePosition{X: 8, Y: 10, Z: 8}, config) {
		t.Fatal("placeMultifaceGrowth returned false under a stone ceiling")
	}
	got := region.getBlock(8, 10, 8)
	if !isBlockNamed(got, "minecraft:glow_lichen") || !multifaceHasFace(got, "up") {
		t.Fatalf("origin (8,10,8) = %s, want glow_lichen with up=true", stateLabel(got))
	}
}

func TestPlaceVinesAttachesToFirstHorizontalOrUpNeighbour(t *testing.T) {
	region, _, _ := springRegion(t, StateStone, [][3]int{{8, 9, 8}, {8, 10, 7}})
	if !region.placeVines(worldgen.FeaturePosition{X: 8, Y: 10, Z: 8}) {
		t.Fatal("placeVines returned false next to a north stone neighbour")
	}
	got := region.getBlock(8, 10, 8)
	if !isBlockNamed(got, "minecraft:vine") || !multifaceHasFace(got, "north") {
		t.Fatalf("vine state = %s, want vine[north=true] (down stone neighbour must be skipped)", stateLabel(got))
	}
}

func TestSculkPatchSpreadsSculkAndVeinsInDeepDark(t *testing.T) {
	deepslate, ok := nameToStateID("minecraft:deepslate", map[string]string{"axis": "y"})
	if !ok {
		t.Fatal("missing deepslate")
	}
	region, set, _ := springRegion(t, deepslate, nil)
	for y := 4; y <= 14; y++ {
		for z := 2; z <= 14; z++ {
			for x := 2; x <= 14; x++ {
				if y <= 8 {
					region.setBlock(x, y, z, deepslate)
				}
			}
		}
	}
	config, err := set.SculkPatch("minecraft:sculk_patch_deep_dark")
	if err != nil {
		t.Fatal(err)
	}
	random := worldgen.NewXoroshiro(12345)
	if !region.placeSculkPatch(set, random, worldgen.FeaturePosition{X: 8, Y: 9, Z: 8}, config) {
		t.Fatal("placeSculkPatch returned false above a deepslate floor")
	}
	sculkCells, veinCells := 0, 0
	for y := 4; y <= 12; y++ {
		for z := 2; z <= 14; z++ {
			for x := 2; x <= 14; x++ {
				s := region.getBlock(x, y, z)
				if isBlockNamed(s, "minecraft:sculk") {
					sculkCells++
				} else if isBlockNamed(s, "minecraft:sculk_vein") {
					veinCells++
				}
			}
		}
	}
	if sculkCells == 0 || veinCells == 0 {
		t.Fatalf("sculk patch placed %d sculk and %d sculk_vein cells, want both > 0", sculkCells, veinCells)
	}
}

func TestDripstoneClusterAndPointedDripstonePlaceColumns(t *testing.T) {
	region, set, _ := springRegion(t, StateStone, nil)
	for z := 2; z <= 14; z++ {
		for x := 2; x <= 14; x++ {
			for y := 4; y <= 7; y++ {
				region.setBlock(x, y, z, StateStone)
			}
			for y := 13; y <= 16; y++ {
				region.setBlock(x, y, z, StateStone)
			}
		}
	}
	clusterCfg, err := set.DripstoneCluster("minecraft:dripstone_cluster")
	if err != nil {
		t.Fatal(err)
	}
	random := worldgen.NewXoroshiro(77)
	if !region.placeDripstoneCluster(set, random, worldgen.FeaturePosition{X: 8, Y: 10, Z: 8}, clusterCfg) {
		t.Fatal("placeDripstoneCluster returned false inside a stone cavern")
	}
	dripstoneBlocks, pointedBlocks := 0, 0
	for y := 4; y <= 16; y++ {
		for z := 2; z <= 14; z++ {
			for x := 2; x <= 14; x++ {
				s := region.getBlock(x, y, z)
				if isBlockNamed(s, "minecraft:dripstone_block") {
					dripstoneBlocks++
				} else if isBlockNamed(s, "minecraft:pointed_dripstone") {
					pointedBlocks++
				}
			}
		}
	}
	if dripstoneBlocks == 0 || pointedBlocks == 0 {
		t.Fatalf("dripstone cluster placed %d dripstone_block and %d pointed_dripstone cells, want both > 0",
			dripstoneBlocks, pointedBlocks)
	}
}

func TestSculkPatchBytecodeRules(t *testing.T) {
	deepslate, ok := nameToStateID("minecraft:deepslate", map[string]string{"axis": "y"})
	if !ok {
		t.Fatal("missing deepslate")
	}
	caveAir, ok := nameToStateID("minecraft:cave_air", nil)
	if !ok {
		t.Fatal("missing cave_air")
	}
	catalyst, ok := nameToStateID("minecraft:sculk_catalyst", map[string]string{"bloom": "false"})
	if !ok {
		t.Fatal("missing sculk_catalyst")
	}
	sensor, ok := nameToStateID("minecraft:sculk_sensor", map[string]string{
		"power":              "0",
		"sculk_sensor_phase": "inactive",
		"waterlogged":        "false",
	})
	if !ok {
		t.Fatal("missing sculk_sensor")
	}
	shrieker, ok := nameToStateID("minecraft:sculk_shrieker", map[string]string{
		"can_summon":  "true",
		"shrieking":   "false",
		"waterlogged": "false",
	})
	if !ok {
		t.Fatal("missing sculk_shrieker")
	}
	sculk, ok := nameToStateID("minecraft:sculk", nil)
	if !ok {
		t.Fatal("missing sculk")
	}

	// 1. Only SculkBlock and SculkVeinBlock implement SculkBehaviour in 26.1.2.
	if !isSculkBehaviourBlock(sculk) {
		t.Fatal("expected minecraft:sculk to implement SculkBehaviour")
	}
	for _, nonBehaviour := range []uint16{catalyst, sensor, shrieker, deepslate} {
		if isSculkBehaviourBlock(nonBehaviour) {
			t.Fatalf("did not expect %s to implement SculkBehaviour", stateLabel(nonBehaviour))
		}
	}

	// 2. extra_rare_growths checks BlockState.isAir() (including cave_air),
	// matching SculkPatchFeature.place offset 265.
	region, set, _ := springRegion(t, deepslate, nil)
	for z := 2; z <= 14; z++ {
		for x := 2; x <= 14; x++ {
			for y := 4; y <= 8; y++ {
				region.setBlock(x, y, z, deepslate)
			}
			for y := 9; y <= 14; y++ {
				region.setBlock(x, y, z, caveAir)
			}
		}
	}
	cfg, err := set.SculkPatch("minecraft:sculk_patch_ancient_city")
	if err != nil {
		t.Fatal(err)
	}
	cfg.ChargeCount = 1
	cfg.AmountPerCharge = 1
	cfg.SpreadAttempts = 1
	cfg.GrowthRounds = 0
	cfg.SpreadRounds = 0
	cfg.CatalystChance = 0.0
	if !region.placeSculkPatch(set, worldgen.NewXoroshiro(99), worldgen.FeaturePosition{X: 8, Y: 9, Z: 8}, cfg) {
		t.Fatal("expected placeSculkPatch to succeed in cave_air above deepslate")
	}
	shriekers := 0
	for z := 6; z <= 10; z++ {
		for x := 6; x <= 10; x++ {
			if region.getBlock(x, 9, z) == shrieker {
				shriekers++
			}
		}
	}
	if shriekers == 0 {
		t.Fatal("expected extra_rare_growths to place sculk_shrieker into cave_air cells above sturdy deepslate")
	}
}

func TestChunk4020And4021SculkParityAndSourceOrdering(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-order deep-dark sculk fixture parity in -short mode")
	}
	if _, err := os.Stat(vanillaLandFixture); err != nil {
		t.Skipf("land fixture not installed: %v", err)
	}
	cap := readFixtureCapture(t, vanillaLandFixture)
	fixMap := make(map[[2]int32]fixtureChunk)
	for _, ch := range cap.chunks {
		fixMap[[2]int32{ch.cx, ch.cz}] = ch
	}
	if _, err := os.Stat(vanillaLandSourcesFixture); err == nil {
		srcCap := readFixtureCapture(t, vanillaLandSourcesFixture)
		for _, ch := range srcCap.chunks {
			fixMap[[2]int32{ch.cx, ch.cz}] = ch
		}
	}

	// 1. Chunk (-40, 21) has 0 sculk mismatches against vanillaLandFixture.
	ch4021, ok := fixMap[[2]int32{-40, 21}]
	if !ok {
		t.Fatal("chunk (-40, 21) not found in land fixture")
	}
	gen := NewVanillaRegionGenerator(cap.seed)
	mine4021 := gen(-40, 21)
	for y := MinY; y < MinY+WorldHeight; y++ {
		for lz := 0; lz < 16; lz++ {
			for lx := 0; lx < 16; lx++ {
				w := stateLabel(ch4021.at(lx, y, lz))
				g := stateLabel(mine4021.GetBlock(lx, y, lz))
				if w != g && (strings.Contains(w, "sculk") || strings.Contains(g, "sculk")) {
					t.Fatalf("chunk (-40, 21) sculk mismatch at (%d,%d,%d): got %s, want %s",
						-40*16+lx, y, 21*16+lz, g, w)
				}
			}
		}
	}

	// 2. In chunk (-40, 20), running source (-39, 20) before (-39, 19) resolves
	// all 44 eastern sculk cells (X = -628..-625, Y = -42..-30) and the
	// sculk_sensor at (-626, -49, 324), leaving 0 false-positive sculk blocks
	// (got == minecraft:sculk && want != minecraft:sculk).
	ch4020, ok := fixMap[[2]int32{-40, 20}]
	if !ok {
		t.Fatal("chunk (-40, 20) not found in land fixture")
	}
	od, fluidPicker, veins, carver := vanillaGeneratorInputs(cap.seed)
	baseMap := make(map[[2]int32]*Chunk)
	for cx := int32(-42); cx <= -38; cx++ {
		for cz := int32(18); cz <= 22; cz++ {
			baseMap[[2]int32{cx, cz}] = generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, cap.seed, cx, cz)
		}
	}
	cloneRegion := func() *decorationRegion {
		var chs []*Chunk
		for _, c := range baseMap {
			chs = append(chs, terrainClone(c))
		}
		reg, err := newDecorationRegion(chs)
		if err != nil {
			t.Fatal(err)
		}
		return reg
	}

	regSouthToNorth := cloneRegion()
	var sourcesSouthToNorth []decorationSource
	for cz := int32(21); cz >= 19; cz-- {
		for cx := int32(-41); cx <= -39; cx++ {
			sourcesSouthToNorth = append(sourcesSouthToNorth, decorationSource{cx, cz})
		}
	}
	if err := regSouthToNorth.replayScheduledOresWithSources(od, cap.seed, -40, 20, sourcesSouthToNorth); err != nil {
		t.Fatal(err)
	}
	sculkDiffs := 0
	falsePositiveSculk := 0
	for y := MinY; y < MinY+WorldHeight; y++ {
		for lz := 0; lz < 16; lz++ {
			for lx := 0; lx < 16; lx++ {
				gx, gz := -40*16+lx, 20*16+lz
				w := stateLabel(ch4020.at(lx, y, lz))
				g := stateLabel(regSouthToNorth.getBlock(gx, y, gz))
				if w != g && (strings.Contains(w, "sculk") || strings.Contains(g, "sculk")) {
					sculkDiffs++
					if g == "minecraft:sculk" {
						falsePositiveSculk++
					}
				}
			}
		}
	}
	if falsePositiveSculk != 0 {
		t.Fatalf("expected 0 false-positive minecraft:sculk blocks in (-40, 20), got %d", falsePositiveSculk)
	}
	if sculkDiffs != 30 {
		t.Fatalf("expected 30 sculk diffs in (-40, 20) under south-to-north source order, got %d", sculkDiffs)
	}
	if got := stateLabel(regSouthToNorth.getBlock(-626, -49, 324)); got != "minecraft:sculk_sensor" {
		t.Fatalf("(-626, -49, 324) = %s, want minecraft:sculk_sensor", got)
	}

	// 3. Verify the (-38, 19) -> (-40, 20) -> (-39, 19) -> (-40, 19) -> (-39, 20)
	// cross-chunk deep_dark sculk ordering for (-39, 20):
	for cx := int32(-42); cx <= -37; cx++ {
		for cz := int32(18); cz <= 22; cz++ {
			if _, ok := baseMap[[2]int32{cx, cz}]; !ok {
				baseMap[[2]int32{cx, cz}] = generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, cap.seed, cx, cz)
			}
		}
	}
	reg3920 := cloneRegion()
	if err := reg3920.replayScheduledOresWithSources(od, cap.seed, -39, 20, decorationSources(-39, 20)); err != nil {
		t.Fatal(err)
	}
	if got := stateLabel(reg3920.getBlock(-624, -52, 320)); got != "minecraft:sculk_catalyst" {
		t.Fatalf("(-624, -52, 320) under decorationSources(-39, 20) = %s, want minecraft:sculk_catalyst", got)
	}
	if got := stateLabel(reg3920.getBlock(-624, -51, 320)); got != "minecraft:air" {
		t.Fatalf("(-624, -51, 320) under decorationSources(-39, 20) = %s, want minecraft:air", got)
	}
	if ch3920, ok := fixMap[[2]int32{-39, 20}]; ok {
		stateDiffs3920, nameDiffs3920 := 0, 0
		for y := MinY; y < MinY+WorldHeight; y++ {
			for lz := 0; lz < 16; lz++ {
				for lx := 0; lx < 16; lx++ {
					wantID := ch3920.at(lx, y, lz)
					gotID := reg3920.getBlock(-39*16+lx, y, 20*16+lz)
					if wantID != gotID {
						stateDiffs3920++
					}
					if stateLabel(wantID) != stateLabel(gotID) {
						nameDiffs3920++
					}
				}
			}
		}
		if stateDiffs3920 > 12 || nameDiffs3920 > 11 {
			t.Fatalf("(-39, 20) mismatches: stateDiffs=%d (want <=12), nameDiffs=%d (want <=11)", stateDiffs3920, nameDiffs3920)
		}
	}
}

