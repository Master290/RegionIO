package world

import (
	"testing"

	"regionio/internal/worldgen"
)

// TestRegionVegetationStageProducesTrees is the smoke test that the stage-9 replay
// reaches a tree at all. It used to run against the per-chunk legacy generator, whose
// hand-written tree path this replaced and deleted; the legacy path now has no trees,
// so the assertion belongs to the generator the product actually uses.
func TestRegionVegetationStageProducesTrees(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 3x3 region tree smoke scan in -short mode")
	}
	// A fixed 3x3 window over the old-growth taiga the land fixture measured, not a
	// search: the first version of this scanned up to 81 region chunks looking for an
	// oak log and stopped as soon as it found one, which made its runtime a property of
	// luck and cost ~97s per run once the region path was the thing under test. Nine
	// deterministic chunks say the same thing.
	gen := NewVanillaRegionGenerator(12345)
	cells := 0
	for cx := int32(15); cx <= 17; cx++ {
		for cz := int32(-41); cz <= -39; cz++ {
			chunk := gen(cx, cz)
			for y := SeaLevel; y < 160; y++ {
				for x := 0; x < 16; x++ {
					for z := 0; z < 16; z++ {
						if isTreeState(chunk.GetBlock(x, y, z)) {
							cells++
						}
					}
				}
			}
		}
	}
	if cells == 0 {
		t.Fatal("the region vegetation stage produced no tree blocks over a 3x3 taiga window")
	}
	t.Logf("%d tree cells over the 3x3 window", cells)
}

// decorateChunk4020TargetFirst runs the 3x3 decoration sources for (-40, 20) with
// (-40, 20) ordered before (-40, 19), matching the source order in which (-40, 20)'s
// first oak tree places its PlaceOnGroundDecorator leaf litter before (-40, 19)'s
// cross-chunk leaf litter occupies (-636..-634, 96, 318) and (-633, 96, 320).
func decorateChunk4020TargetFirst(t *testing.T, seed int64) *Chunk {
	t.Helper()
	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	var chunks []*Chunk
	var target *Chunk
	for cx := int32(-42); cx <= -38; cx++ {
		for cz := int32(18); cz <= 22; cz++ {
			ch := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
			chunks = append(chunks, ch)
			if cx == -40 && cz == 20 {
				target = ch
			}
		}
	}
	region, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	region.bindSeed(seed)

	sources := decorationSources(-40, 20)
	if err := region.replayScheduledOresWithSources(od, seed, -40, 20, sources); err != nil {
		t.Fatal(err)
	}
	return target
}

// TestChunk4020OakLeafDistanceParity verifies that leaf distance propagation across
// existing logs and adjacent settled leaves preserves exact vanilla parity for the
// oak tree canopy in chunk (-40, 20).
func TestChunk4020OakLeafDistanceParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full-region oak leaf distance fixture parity in -short mode")
	}
	cap := readFixtureCapture(t, vanillaLandFixture)
	chunk := decorateChunk4020TargetFirst(t, cap.seed)
	var vChunk fixtureChunk
	for _, ch := range cap.chunks {
		if ch.cx == -40 && ch.cz == 20 {
			vChunk = ch
			break
		}
	}
	coords := [][3]int{
		{-636, 96, 320},
		{-635, 96, 320},
		{-635, 96, 321},
		{-636, 97, 320},
		{-635, 97, 320},
		{-635, 97, 321},
		{-636, 100, 322},
	}
	for _, pos := range coords {
		lx, ly, lz := pos[0]&15, pos[1], pos[2]&15
		vanilla := vChunk.at(lx, ly, lz)
		ours := chunk.GetBlock(lx, ly, lz)
		if ours != vanilla {
			t.Errorf("pos (%d,%d,%d): ours=%s%s (%d) want=%s%s (%d)",
				pos[0], pos[1], pos[2],
				stateLabel(ours), stateProperties(ours), ours,
				stateLabel(vanilla), stateProperties(vanilla), vanilla)
		}
	}
}

// TestChunk4020SourceOrderOakParity verifies that when source (-40, 20) runs before
// source (-40, 19)'s cross-chunk leaf litter occupies (-636..-634, 96, 318) and
// (-633, 96, 320), the first oak tree's PlaceOnGroundDecorator consumes the expected
// 4 RNG draws on those cells and advances the stream to step 801, placing the second
// oak tree at (-637, 95, 322) and matching all 114 vanilla tree cells in (-40, 20).
func TestChunk4020SourceOrderOakParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full-region oak source-order fixture parity in -short mode")
	}
	cap := readFixtureCapture(t, vanillaLandFixture)
	var vChunk fixtureChunk
	for _, ch := range cap.chunks {
		if ch.cx == -40 && ch.cz == 20 {
			vChunk = ch
			break
		}
	}
	target := decorateChunk4020TargetFirst(t, cap.seed)
	matched, total := 0, 0
	for y := SeaLevel; y < 160; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				v := vChunk.at(x, y, z)
				if !isTreeState(v) {
					continue
				}
				total++
				if target.GetBlock(x, y, z) == v {
					matched++
				}
			}
		}
	}
	if matched != total || total != 114 {
		t.Fatalf("(-40, 20) tree cell parity = %d/%d, want 114/114", matched, total)
	}
}

// TestChunk1640BirchTallStep62Parity verifies that when source (16, -39) runs
// minecraft:birch_tall (fidx=23) and includes the border position at s=3
// (264, 104, -619), the RNG stream skips s=23 (268, 102, -624) and reaches s=62
// (261, 104, -624) to place super_birch_bees_0002, eliminating all 45 birch leaf
// surface mismatches (22 missing + 23 extra) at z=-626..-625 in chunk (16, -40).
func TestChunk1640BirchTallStep62Parity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full-region birch_tall fixture parity in -short mode")
	}
	cap := readFixtureCapture(t, vanillaLandFixture)
	var vChunk fixtureChunk
	for _, ch := range cap.chunks {
		if ch.cx == 16 && ch.cz == -40 {
			vChunk = ch
			break
		}
	}

	batch, err := NewVanillaBaseBatchGenerator(cap.seed)(16, -39)
	if err != nil {
		t.Fatal(err)
	}
	chunks := make([]*Chunk, 0, len(batch))
	for _, ch := range batch {
		chunks = append(chunks, ch)
	}
	region, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	region.bindSeed(cap.seed)
	if err := region.setSource(16, -39); err != nil {
		t.Fatal(err)
	}
	if err := region.ensureSourceNeighborhood(); err != nil {
		t.Fatal(err)
	}

	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	sched, err := set.FeatureSchedule(possibleBiomeOrder(), possibleBiomeOrder(), 9)
	if err != nil {
		t.Fatal(err)
	}
	fidxBT := -1
	for _, sf := range sched {
		if sf.Name == "minecraft:birch_tall" {
			fidxBT = sf.Index
			break
		}
	}
	if fidxBT < 0 {
		t.Fatal("minecraft:birch_tall not found in stage-9 schedule")
	}

	_, decSeed := worldgen.DecorationRandom(cap.seed, 16, -39)
	rng := worldgen.NewWorldgenRandom(0)
	rng.SetFeatureSeed(decSeed, fidxBT, 9)

	// With Climate.quantizeCoord(float) ((long)(f * 10000.0f)), s=3 at (264, 104, -619)
	// quantizes Temperature = -0.149977 to -1499 (old_growth_birch_forest), advancing
	// s=3 -> s=27 -> s=51 -> 53 -> 55 -> s=57 (failSurvive on s=27's canopy) -> s=62.
	ctx := region.placementContext(func(pos worldgen.FeaturePosition) bool {
		return region.biomeAllowsFeature(set, "minecraft:birch_tall", 9, pos)
	})
	origin := worldgen.FeaturePosition{X: 16 * 16, Y: 0, Z: -39 * 16}
	err = set.ForEachPlacementPosition("minecraft:birch_tall", rng, origin, ctx, func(pos worldgen.FeaturePosition) error {
		return region.placeConfiguredVegetation(set, rng, pos, "minecraft:birch_tall", ctx)
	})
	if err != nil {
		t.Fatal(err)
	}

	target := batch[[2]int32{16, -40}]
	matched, Mismatches := 0, 0
	for z := 14; z <= 15; z++ {
		for x := 0; x < 16; x++ {
			for y := 100; y <= 120; y++ {
				vIsBirch := stateLabel(vChunk.at(x, y, z)) == "minecraft:birch_leaves"
				oIsBirch := stateLabel(target.GetBlock(x, y, z)) == "minecraft:birch_leaves"
				if vIsBirch != oIsBirch {
					Mismatches++
				} else if vIsBirch {
					matched++
				}
			}
		}
	}
	if matched != 22 || Mismatches != 0 {
		t.Fatalf("(16, -40) birch_tall overhang: matched=%d (want 22), mismatches=%d (want 0)", matched, Mismatches)
	}
}
